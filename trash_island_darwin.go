//go:build darwin

package main

import (
	"math"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type islandShape int

const (
	islandRest    islandShape = iota // the notch itself: nothing shows
	islandCompact                    // a strip to both sides of the notch
	islandOpen                       // the strip and a body under it
)

const (
	islandWing       = 62  // what the compact shape adds to each side of the notch
	islandOpenWidth  = 390 // the open shape, a point wider over a notch of odd width
	islandPanelWidth = 480 // the window: the widest shape and a spring going past it
	islandPanelBelow = 160 // what the window has under the notch: the tallest body, and the spring

	islandOpenDelay  = 80 * time.Millisecond  // at the notch's own size before it grows
	islandLinger     = 10 * time.Second       // open and untouched, until it folds
	islandLeaveDelay = 900 * time.Millisecond // the pointer gone, until it folds
	islandArmDelay   = time.Second            // out by itself, until it takes the click that empties
	islandDoneStay   = 4 * time.Second        // a result with nothing to add, until it goes
	islandRetract    = 500 * time.Millisecond // going back into the notch, before its window goes
)

// trashIsland shows the Trash reminder at the notch: it comes out of the
// notch open, folds to a strip beside the notch when left alone, and opens
// again under the pointer. It holds no timer of its own: step says when it
// next has something to do.
type trashIsland struct {
	p     *trashReminderPopup
	notch islandNotch
	now   func() time.Time
	// gone is called once, when the island is back in the notch for good.
	gone func()
	// wake asks whoever drives the island to step it now.
	wake func()

	shape   islandShape
	hover   bool   // the pointer is on it
	leaving bool   // going back into the notch, whatever the pointer does
	ended   bool   // gone was called
	seen    string // the phase it last showed
	dirty   bool   // changed between steps
	// armedShown is whether the last step showed the button that empties as
	// one that takes a click.
	armedShown bool

	// When each change is due; zero when it is not.
	openAt, foldAt, leaveAt, goneAt time.Time
	armedAt                         time.Time
}

func newTrashIsland(p *trashReminderPopup, n islandNotch, now func() time.Time) *trashIsland {
	return &trashIsland{p: p, notch: n, now: now}
}

// appear starts the island at the notch's own size, from which it grows.
func (s *trashIsland) appear() {
	now := s.now()
	s.shape, s.hover, s.leaving, s.ended, s.dirty = islandRest, false, false, false, true
	s.seen, s.armedShown = s.p.phase, false
	s.openAt = now.Add(islandOpenDelay)
	s.foldAt = s.openAt.Add(islandLinger)
	s.armedAt = s.openAt.Add(islandArmDelay)
	s.leaveAt, s.goneAt = time.Time{}, time.Time{}
}

// leave sends the island back into the notch, for good.
func (s *trashIsland) leave() {
	if s.leaving || s.ended {
		return
	}
	s.shape, s.leaving, s.dirty = islandRest, true, true
	s.openAt, s.foldAt, s.leaveAt = time.Time{}, time.Time{}, time.Time{}
	s.goneAt = s.now().Add(islandRetract)
}

// armed reports whether the button that empties the Trash takes a click: an
// island that came out by itself may have landed under a click meant for
// what it covers.
func (s *trashIsland) armed() bool { return !s.now().Before(s.armedAt) }

// unwatched reports whether nobody reads the question now: folded, the
// pointer elsewhere. Only then may a new measurement replace its numbers.
func (s *trashIsland) unwatched() bool {
	return s.p.phase == "" && s.shape == islandCompact && !s.hover && !s.leaving && !s.ended
}

// step moves the island on at now, with the pointer on it or not. It returns
// when it next has something to do, zero for never, and whether what it
// shows changed.
func (s *trashIsland) step(now time.Time, inside bool) (next time.Time, changed bool) {
	if s.ended {
		return time.Time{}, false
	}
	changed, s.dirty = s.dirty, false
	before := s.shape
	due := func(at time.Time) bool { return !at.IsZero() && !now.Before(at) }
	if s.p.phase != s.seen {
		s.seen, changed = s.p.phase, true
		switch {
		case s.leaving:
		case s.p.phase == "done":
			s.shape, s.foldAt, s.leaveAt = islandOpen, time.Time{}, now.Add(islandDoneStay)
		case s.p.phase == "partial" || s.p.phase == "cancelled":
			s.shape, s.foldAt = islandOpen, now.Add(islandLinger)
		default:
			s.foldAt = time.Time{} // from here the pointer leaving folds it
		}
	}
	if due(s.openAt) {
		s.openAt = time.Time{}
		if s.shape == islandRest && !s.leaving {
			s.shape = islandOpen
		}
	}
	s.hover = inside
	switch {
	case s.leaving:
	case inside:
		// The pointer opens the strip and keeps the open shape.
		if s.shape == islandCompact {
			s.shape = islandOpen
		}
		s.foldAt = time.Time{}
	case s.shape == islandOpen && s.p.phase != "done":
		if s.foldAt.IsZero() {
			s.foldAt = now.Add(islandLeaveDelay)
		} else if due(s.foldAt) {
			s.shape, s.foldAt = islandCompact, time.Time{}
		}
	}
	if due(s.leaveAt) {
		if inside && !s.leaving {
			s.leaveAt = now.Add(time.Second) // a result stays while the pointer is on it
		} else {
			s.leave()
		}
	}
	if due(s.goneAt) {
		s.goneAt, s.ended = time.Time{}, true
		if s.gone != nil {
			s.gone()
		}
		return time.Time{}, true
	}
	armed := !now.Before(s.armedAt)
	changed = changed || s.dirty || s.shape != before || armed != s.armedShown
	s.dirty, s.armedShown = false, armed
	for _, at := range []time.Time{s.openAt, s.foldAt, s.leaveAt, s.goneAt} {
		if !at.IsZero() && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	// The button that empties is drawn again as it starts taking clicks.
	if !armed && s.p.phase == "" && !s.leaving && (next.IsZero() || s.armedAt.Before(next)) {
		next = s.armedAt
	}
	return next, changed
}

// size is the size the island goes to in its shape and phase.
func (s *trashIsland) size() (w, h int) {
	odd := s.notch.Width % 2
	switch s.shape {
	case islandCompact:
		return s.notch.Width + 2*islandWing, s.notch.Height
	case islandOpen:
		return islandOpenWidth + odd, s.notch.Height + trashIslandBody(s.p.phase)
	}
	return s.notch.Width, s.notch.Height
}

// trashIslandBody is the height of what the open shape shows under the
// notch in a phase.
func trashIslandBody(phase string) int {
	switch phase {
	case "cleaning", "stopping":
		return 88
	case "done":
		return 60
	case "partial", "cancelled":
		return 124
	}
	return 104
}

// frame is the island's window: as wide as the widest shape and a spring
// going past it, centred on the notch to the point, which takes a width of
// the notch's parity.
func (s *trashIsland) frame() mygo.Rectangle {
	w := islandPanelWidth + s.notch.Width%2
	return mygo.Rectangle{X: s.notch.X + (s.notch.Width-w)/2, Y: s.notch.Y, Width: w, Height: s.notch.Height + islandPanelBelow}
}

// region is where the island's window takes the mouse, from its top-left
// corner: the shape the island goes to. At the notch's own size it takes
// none.
func (s *trashIsland) region() (mygo.Rectangle, bool) {
	if s.shape == islandRest || s.ended {
		return mygo.Rectangle{}, false
	}
	w, h := s.size()
	return mygo.Rectangle{X: (s.frame().Width - w) / 2, Width: w, Height: h}, true
}

// contains reports whether a point of the screen is on the island.
func (s *trashIsland) contains(x, y int) bool {
	r, ok := s.region()
	if !ok {
		return false
	}
	f := s.frame()
	x, y = x-f.X-r.X, y-f.Y-r.Y
	return x >= 0 && x < r.Width && y >= 0 && y < r.Height
}

var (
	islandBlack  = ui.RGB(0, 0, 0)
	islandText   = ui.RGB(255, 255, 255)
	islandMuted  = ui.RGBA(255, 255, 255, 0.62)
	islandChip   = ui.RGBA(255, 255, 255, 0.16)
	islandChipOn = ui.RGBA(255, 255, 255, 0.28)
	islandSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 420 * time.Millisecond, Bounce: 0.18}}

	islandTrashIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/><path d="M10 11v6"/><path d="M14 11v6"/></svg>`))
	islandDoneIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>`))
)

// view draws the island in its window: black, as the notch it grows from,
// whatever appearance the app shows. The window does not resize: the island
// moves in it, and what it leaves free shows what is behind the window.
func (s *trashIsland) view(c *ui.Context) {
	t := menuBarPanelTheme(c.Theme(), true)
	c.SetTheme(t)
	c.Root().Background(ui.Transparent)
	w, h := s.size()
	radius := float32(10)
	if s.shape == islandOpen {
		radius = 24
	}
	ui.Column(c).Fill().AlignItems(ui.Center).Children(func() {
		box := ui.Column(c.Key("island"))
		bw, bh, br := box.AnimateTo("w", float32(w), islandSpring), box.AnimateTo("h", float32(h), islandSpring), box.AnimateTo("r", radius, islandSpring)
		box.Size(bw, bh).Shrink(0).Radius(0, 0, br, br).Clip().AlignItems(ui.Center)
		// Its window shows a moment before the island comes out, so that it
		// grows from the notch's own size: nothing is drawn until then.
		if s.shape == islandRest && !s.leaving {
			return
		}
		box.Background(islandBlack).Children(func() {
			s.strip(c, t)
			if s.shape == islandOpen {
				s.body(c, t)
			}
		})
	})
}

// strip is what shows to both sides of the notch, folded or open: the
// Trash, and its size, the progress or the outcome.
func (s *trashIsland) strip(c *ui.Context, t *ui.Theme) {
	p := s.p
	ui.Row(c).WidthPercent(100).Height(float32(s.notch.Height)).Shrink(0).PaddingX(16).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, islandTrashIcon).FontSize(15).TextColor(islandText)
		ui.Spacer(c).Grow(1)
		switch p.phase {
		case "cleaning", "stopping":
			ui.Text(c, strconv.Itoa(int(100*s.fraction()))+"%").FontSize(12).Bold().TextColor(t.Accent)
		case "done":
			ui.Icon(c, islandDoneIcon).FontSize(15).TextColor(t.Accent)
		case "partial", "cancelled":
			ui.Text(c, "!").FontSize(13).Bold().TextColor(t.Warning)
		default:
			ui.Text(c, growthSize(p.size)).FontSize(12).Bold().TextColor(islandText)
		}
	})
}

// fraction is how much of the Trash the cleanup freed so far.
func (s *trashIsland) fraction() float64 {
	return math.Min(1, float64(s.p.freed)/float64(max(1, s.p.size)))
}

// body is what the open island shows under the notch.
func (s *trashIsland) body(c *ui.Context, t *ui.Theme) {
	p, text := s.p, s.p.text
	title := func(s string) { ui.Text(c, s).FontSize(15).Bold().TextColor(islandText) }
	note := func(s string) { ui.Text(c, s).FontSize(12).TextColor(islandMuted) }
	freed := text("trash.popup.freed", map[string]string{"size": growthSize(p.freed)})
	fade := ui.ElementTransition{Enter: &ui.Motion{}, Exit: &ui.Motion{}}
	ui.Column(c.Key("body-"+p.phase)).Width(float32(islandOpenWidth+s.notch.Width%2)).Grow(1).Shrink(0).
		Padding(4, 18, 16, 18).Gap(6).Transition(fade).Children(func() {
		switch p.phase {
		case "":
			title(text("trash.island.title", map[string]string{"size": growthSize(p.size)}))
			note(text("trash.island.body", nil))
			ui.Spacer(c).Grow(1)
			ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
				s.pill(c, "later", text("trash.popup.later", nil), islandChip, islandChipOn, true, s.dismiss)
				s.pill(c, "empty", text("trash.popup.empty", nil), t.Danger, t.Danger.Mix(islandText, 0.14), s.armed(), s.empty)
			})
		case "cleaning", "stopping":
			title(text("trash.popup."+p.phase, nil))
			ui.Row(c).WidthPercent(100).Height(4).Radius(2).Background(islandChip).Children(func() {
				ui.Box(c).WidthPercent(float32(100 * s.fraction())).Height(4).Radius(2).Background(t.Accent)
			})
			ui.Spacer(c).Grow(1)
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				note(freed)
				ui.Spacer(c).Grow(1)
				s.pill(c, "cancel", text("trash.popup.cancel", nil), islandChip, islandChipOn, p.phase == "cleaning", s.stop)
			})
		case "done":
			title(text("trash.popup.done", nil))
			note(freed)
		default: // something was kept: the cleanup stopped, or could not delete it
			title(text("trash.popup."+p.phase, nil))
			note(freed)
			note(text("trash.popup.remaining", nil))
			ui.Spacer(c).Grow(1)
			ui.Row(c).Justify(ui.End).Children(func() {
				s.pill(c, "close", text("trash.popup.close", nil), islandChip, islandChipOn, true, s.dismiss)
			})
		}
	})
}

// pill is a button of the island. One that takes no click yet shows fainter.
func (s *trashIsland) pill(c *ui.Context, key, label string, bg, over ui.Color, takes bool, click func()) {
	b := ui.Row(c.Key(key)).Height(28).PaddingX(14).AlignItems(ui.Center).Radius(14)
	switch {
	case !takes:
		b.Opacity(0.45)
	case b.Hovered():
		bg = over
	}
	b.Background(bg).OnClick(click).Children(func() {
		ui.Text(c, label).FontSize(12.5).TextColor(islandText)
	})
}

// empty starts the cleanup, once the island takes that click.
func (s *trashIsland) empty() {
	if !s.armed() {
		return
	}
	s.p.begin()
	s.poke()
}

func (s *trashIsland) stop() {
	s.p.stop()
	s.poke()
}

// dismiss answers "not now", or closes a result.
func (s *trashIsland) dismiss() {
	if s.p.close != nil {
		s.p.close()
	}
}

// poke has whoever drives the island step it now, after a click changed what
// it shows.
func (s *trashIsland) poke() {
	if s.wake != nil {
		s.wake()
	}
}

// islandWindow is what an island asks of its window.
type islandWindow interface {
	// place puts the window somewhere on screen.
	place(mygo.Rectangle)
	// takeMouse gives the mouse to r, from the window's top-left corner, and
	// lets it through everywhere else; to nothing at all when ok is false.
	takeMouse(r mygo.Rectangle, ok bool)
	redraw()
	close()
}

// trashIslandShow is an island on screen: it steps the island as the pointer
// moves and as the island asks to be woken, and has its window follow. It
// runs on the main thread.
type trashIslandShow struct {
	s   *trashIsland
	win islandWindow
	// pointer tells where the pointer is on screen.
	pointer func() (x, y int)
	// after runs fn on the main thread once d went by, unless stopped.
	after func(d time.Duration, fn func()) (stop func())
	// ended is called once, when the island is back in the notch for good
	// and its window is closed.
	ended func()

	stopWake func()    // cancels the wake-up pending for wakeAt
	wakeAt   time.Time // zero when none is pending
	mouse    mygo.Rectangle
	hasMouse bool
	told     bool // the window knows where the mouse goes
	over     bool // off the screen for good
}

// start brings the island out of the notch.
func (d *trashIslandShow) start() {
	d.s.gone, d.s.wake = d.finish, d.sync
	d.win.place(d.s.frame())
	d.s.appear()
	d.advance(true)
}

// sync steps the island with the pointer where it is now.
func (d *trashIslandShow) sync() { d.advance(false) }

// redraw steps the island after the reminder's state changed, and draws it.
func (d *trashIslandShow) redraw() { d.advance(true) }

// pointerMoved follows a move of the mouse. Most change nothing, and cost
// one comparison.
func (d *trashIslandShow) pointerMoved() {
	if d.over {
		return
	}
	if x, y := d.pointer(); d.s.contains(x, y) != d.s.hover {
		d.sync()
	}
}

// moveTo follows the notch to where a new arrangement of the displays put
// it.
func (d *trashIslandShow) moveTo(n islandNotch) {
	if d.over || n == d.s.notch {
		return
	}
	d.s.notch = n
	d.win.place(d.s.frame())
	d.told = false
	d.advance(true)
}

// discard takes the island off the screen at once, as something else takes
// its place: ended is not called.
func (d *trashIslandShow) discard() {
	if d.over {
		return
	}
	d.over = true
	d.wakeFor(time.Time{}, time.Time{})
	d.win.close()
}

// finish closes the window of an island that is back in the notch.
func (d *trashIslandShow) finish() {
	if d.over {
		return
	}
	d.discard()
	if d.ended != nil {
		d.ended()
	}
}

func (d *trashIslandShow) advance(draw bool) {
	if d.over {
		return
	}
	now := d.s.now()
	x, y := d.pointer()
	inside := d.s.contains(x, y)
	next, changed := d.s.step(now, inside)
	// A shape that changed may have come under the pointer, or left it,
	// without the pointer moving.
	if again := d.s.contains(x, y); !d.over && changed && again != inside {
		next, _ = d.s.step(now, again)
	}
	if d.over {
		return // the island was gone at this step
	}
	if r, ok := d.s.region(); !d.told || ok != d.hasMouse || r != d.mouse {
		d.told, d.mouse, d.hasMouse = true, r, ok
		d.win.takeMouse(r, ok)
	}
	if changed || draw {
		d.win.redraw()
	}
	d.wakeFor(now, next)
}

// wakeFor has the island woken at next, and at no other time: one wake-up
// is pending at most, none while the island has nothing due.
func (d *trashIslandShow) wakeFor(now, next time.Time) {
	if next.Equal(d.wakeAt) {
		return
	}
	if d.stopWake != nil {
		d.stopWake()
		d.stopWake = nil
	}
	d.wakeAt = next
	if next.IsZero() {
		return
	}
	d.stopWake = d.after(max(0, next.Sub(now)), func() {
		d.stopWake, d.wakeAt = nil, time.Time{}
		d.sync()
	})
}

// trashIslandOnScreen is a reminder's island in its window, with what feeds
// it: the moves of the mouse, and the changes of the displays.
type trashIslandOnScreen struct {
	*trashIslandShow
	stops []func()
}

func (o *trashIslandOnScreen) unwatched() bool { return o.s.unwatched() }

func (o *trashIslandOnScreen) leave() {
	o.s.leave()
	o.sync()
}

func (o *trashIslandOnScreen) discard() {
	o.unwatch()
	o.trashIslandShow.discard()
}

func (o *trashIslandOnScreen) unwatch() {
	for _, stop := range o.stops {
		stop()
	}
	o.stops = nil
}

// islandPanelError is why the last island could not be placed. The main
// thread's.
var islandPanelError error

// openIsland shows the reminder at the notch n. It reports false where the
// island's window cannot be kept there.
func (r *trashReminderRun) openIsland(n islandNotch) (trashIslandSurface, bool) {
	island := newTrashIsland(r.p, n, time.Now)
	panel, err := openIslandPanel(island.frame(), nativeText("trash.popup.title", nil), island.view)
	if err != nil {
		islandPanelError = err
		return nil, false
	}
	o := &trashIslandOnScreen{trashIslandShow: &trashIslandShow{
		s: island, win: panel,
		pointer: func() (int, int) {
			p := mygo.Screen.CursorScreenPoint()
			return p.X, p.Y
		},
		after: func(d time.Duration, fn func()) func() {
			var stopped atomic.Bool
			t := time.AfterFunc(d, func() {
				runOnMain(func() {
					if !stopped.Load() {
						fn()
					}
				})
			})
			return func() {
				stopped.Store(true)
				t.Stop()
			}
		},
	}}
	// Back in the notch for good, the reminder is over.
	o.ended = func() {
		o.unwatch()
		r.end()
	}
	// The app closes the window under the island as it quits.
	panel.win.OnClosed(func() {
		if o.over {
			return
		}
		o.over = true
		o.unwatch()
		o.wakeFor(time.Time{}, time.Time{})
		r.end()
	})
	r.p.close, r.p.resize = o.leave, nil
	o.stops = []func(){watchIslandPointer(o.pointerMoved), mygo.Screen.OnDisplaysChanged(r.displaysChanged)}
	o.start()
	// The system settles where the window is on the turn after it shows,
	// and may one day keep it under the menu bar: the reminder then goes on
	// in a window, before the island would come out.
	o.after(islandOpenDelay/2, func() {
		if !o.over && panel.win.Bounds() != o.s.frame() {
			r.islandMisplaced()
		}
	})
	return o, true
}
