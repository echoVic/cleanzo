//go:build darwin

package main

import (
	"errors"
	"math"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// islandNotch is the notch of a display, in DIPs of the screen coordinates
// mygo's windows use: the origin is the top-left corner of the primary
// display.
type islandNotch struct {
	X, Y          int // its top-left corner
	Width, Height int
}

type nsEdgeInsets struct{ Top, Left, Bottom, Right float64 }

// islandNotchOf is the notch of a screen AppKit describes: its frame, how
// far its safe area is from its top, and the widths of the strips free to
// the left and right of the notch. primaryHeight is the height of the
// primary screen, from whose bottom AppKit counts.
func islandNotchOf(primaryHeight float64, frame nsRect, top, left, right float64) (islandNotch, bool) {
	width := frame.W - left - right
	if top <= 0 || left <= 0 || right <= 0 || width <= 0 {
		return islandNotch{}, false
	}
	return islandNotch{
		X:      int(math.Round(frame.X + left)),
		Y:      int(math.Round(primaryHeight - frame.Y - frame.H)),
		Width:  int(math.Round(width)),
		Height: int(math.Round(top)),
	}, true
}

// readIslandNotch finds the display with a notch, on the main thread: the
// screen whose safe area leaves room at its top, with a strip free to each
// side of what takes that room.
func readIslandNotch() (n islandNotch, ok bool) {
	runOnMain(func() {
		if !appKitInit() {
			return
		}
		withPool(func() {
			screens := objc.ID(objc.GetClass("NSScreen")).Send(nsSel("screens"))
			count := int(objc.Send[uint64](screens, nsSel("count")))
			if count == 0 {
				return
			}
			// The first screen is the primary one, whatever shows the menu bar.
			primary := objc.Send[nsRect](screens.Send(nsSel("objectAtIndex:"), 0), nsSel("frame"))
			for i := 0; i < count && !ok; i++ {
				s := screens.Send(nsSel("objectAtIndex:"), i)
				n, ok = islandNotchOf(primary.H, objc.Send[nsRect](s, nsSel("frame")),
					objc.Send[nsEdgeInsets](s, nsSel("safeAreaInsets")).Top,
					objc.Send[nsRect](s, nsSel("auxiliaryTopLeftArea")).W,
					objc.Send[nsRect](s, nsSel("auxiliaryTopRightArea")).W)
			}
		})
	})
	return n, ok
}

// readVoiceOver tells whether VoiceOver is on. An island takes the mouse
// alone, so a reminder shows in a window while it is.
func readVoiceOver() (on bool) {
	runOnMain(func() {
		if !appKitInit() {
			return
		}
		withPool(func() {
			ws := objc.ID(objc.GetClass("NSWorkspace")).Send(nsSel("sharedWorkspace"))
			on = objc.Send[bool](ws, nsSel("isVoiceOverEnabled"))
		})
	})
	return on
}

const (
	// nsNonactivatingPanel alone is the style of a panel without a title
	// bar that never activates its app.
	nsNonactivatingPanel = 1 << 7
	// islandLevel is above the menu bar, whose level is 24: an island
	// covers the menu bar to both sides of the notch.
	islandLevel = 27
)

var (
	islandClassOnce sync.Once
	islandClass     objc.Class
	objectSetClass  func(obj objc.ID, class objc.Class) objc.Class
)

// islandPanelClass is the class of an island's window: mygo's panel, which
// takes the keyboard as it is clicked, made one that cannot. It is zero
// where mygo's panel is not what it was.
func islandPanelClass() objc.Class {
	islandClassOnce.Do(func() {
		base := objc.GetClass("MyGoPanel")
		if base == 0 {
			return
		}
		lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		if _, err := purego.Dlsym(lib, "object_setClass"); err != nil {
			return
		}
		purego.RegisterLibFunc(&objectSetClass, lib, "object_setClass")
		class, err := objc.RegisterClass("CleanzoIslandPanel", base, nil, nil, []objc.MethodDef{
			{Cmd: nsSel("canBecomeKeyWindow"), Fn: func(objc.ID, objc.SEL) bool { return false }},
		})
		if err == nil {
			islandClass = class
		}
	})
	return islandClass
}

// islandPanel is the window of an island: a panel that neither activates
// the app nor takes the keyboard, above the menu bar, where the notch is.
type islandPanel struct {
	win *mygo.Window
}

// openIslandPanel shows a panel at frame, whose top is the top of the
// display with the notch. It fails where mygo's panel cannot be made one
// that stays there; nothing shows then. Where the window is settles on the
// turn after this one: AppKit has it under the menu bar until then.
func openIslandPanel(frame mygo.Rectangle, title string, view func(*ui.Context)) (*islandPanel, error) {
	if !appKitInit() {
		return nil, errors.New("AppKit is not there")
	}
	class := islandPanelClass()
	if class == 0 {
		return nil, errors.New("mygo's panel is not the class it was")
	}
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: title, Panel: true, Hidden: true, Frameless: true, Transparent: true,
		DisableShadow: true, DisableResize: true, DisableMove: true,
		Width: frame.Width, Height: frame.Height, X: frame.X, Y: frame.Y,
		Content: ui.View(view),
	})
	p := &islandPanel{win: w}
	err := errors.New("the window has no native handle")
	withPool(func() {
		h := objc.ID(w.NativeHandle())
		if h == 0 {
			return
		}
		// AppKit keeps a window with a title bar under the menu bar, and
		// mygo's frameless window has one, hidden. Without it the window is
		// left where it is put.
		h.Send(nsSel("setStyleMask:"), nsNonactivatingPanel)
		objectSetClass(h, class)
		w.SetBounds(frame)
		w.ShowInactive()
		h.Send(nsSel("setLevel:"), islandLevel)
		err = nil
	})
	if err != nil {
		w.Destroy()
		return nil, err
	}
	return p, nil
}

func (p *islandPanel) place(r mygo.Rectangle) { p.win.SetBounds(r) }

func (p *islandPanel) takeMouse(r mygo.Rectangle, ok bool) {
	if !ok {
		p.win.SetInputRegion([]mygo.Rectangle{})
		return
	}
	p.win.SetInputRegion([]mygo.Rectangle{r})
}

func (p *islandPanel) redraw() { p.win.Invalidate() }

func (p *islandPanel) close() { p.win.Destroy() }

// watchIslandPointer calls moved, on the main thread, as the mouse moves
// anywhere on screen: over the windows of other apps, and over this app's.
// It returns what ends the watch. Nothing runs while the mouse rests.
func watchIslandPointer(moved func()) (stop func()) {
	if !appKitInit() {
		return func() {}
	}
	// Moves, and drags with the left, the right or another button.
	const mask = 1<<5 | 1<<6 | 1<<7 | 1<<27
	var monitors []objc.ID
	global := objc.NewBlock(func(objc.Block, objc.ID) { moved() })
	local := objc.NewBlock(func(_ objc.Block, event objc.ID) objc.ID {
		moved()
		return event
	})
	withPool(func() {
		events := objc.ID(objc.GetClass("NSEvent"))
		for _, m := range []objc.ID{
			events.Send(nsSel("addGlobalMonitorForEventsMatchingMask:handler:"), uint64(mask), global),
			events.Send(nsSel("addLocalMonitorForEventsMatchingMask:handler:"), uint64(mask), local),
		} {
			if m != 0 {
				monitors = append(monitors, m)
			}
		}
	})
	// Without both, a pointer over another app's window, or over this
	// app's, would go unseen: ask where it is now and then instead.
	var done chan struct{}
	if len(monitors) < 2 {
		done = make(chan struct{})
		go func() {
			tick := time.NewTicker(150 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					runOnMain(moved)
				}
			}
		}()
	}
	return func() {
		if done != nil {
			close(done)
		}
		withPool(func() {
			events := objc.ID(objc.GetClass("NSEvent"))
			for _, m := range monitors {
				events.Send(nsSel("removeMonitor:"), m)
			}
		})
		global.Release()
		local.Release()
	}
}
