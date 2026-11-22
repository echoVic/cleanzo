//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// islandCheckReport is what the island check found. It holds numbers of the
// screen and of the island's own window, nothing of what else is on screen.
type islandCheckReport struct {
	// Notch is nil where no display has one: nothing else was checked.
	Notch *islandNotch `json:"notch"`
	// Placed, AtNotch, Level and Visible tell where the window ended up.
	Placed  bool           `json:"placed"`
	Frame   mygo.Rectangle `json:"frame"`
	AtNotch bool           `json:"atNotch"`
	Level   int            `json:"level"`
	Visible bool           `json:"visible"`
	// As it showed: did it take the keyboard, activate the app, or change
	// which app is in front.
	KeyWindow    bool `json:"keyWindow"`
	AppActive    bool `json:"appActive"`
	FrontChanged bool `json:"frontChanged"`
	// A click on the button that empties, before the island takes it and
	// after.
	EarlyClickIgnored   bool `json:"earlyClickIgnored"`
	ClickStarted        bool `json:"clickStarted"`
	KeyAfterClick       bool `json:"keyAfterClick"`
	ActiveAfterClick    bool `json:"activeAfterClick"`
	FrontChangedByClick bool `json:"frontChangedByClick"`
	// Moves of the mouse the watch heard: all of them, and of those the
	// check sent to this app itself.
	MovesHeard     int `json:"movesHeard"`
	SentMovesHeard int `json:"sentMovesHeard"`
	// How the reminder ended.
	Phase string `json:"phase"`
	Ended bool   `json:"ended"`
	// Idle is the processor time the app took over a stretch with a folded
	// island waiting and over one without an island, where asked for.
	Idle     *islandCheckIdle `json:"idle,omitempty"`
	Failures []string         `json:"failures"`
}

type islandCheckIdle struct {
	Seconds       int     `json:"seconds"`
	WithoutIsland float64 `json:"withoutIslandCPUSeconds"`
	Folded        float64 `json:"foldedCPUSeconds"`
}

type nsPoint struct{ X, Y float64 }

// islandCheck runs the island check where CLEANZO_ISLAND_CHECK names a
// folder, and reports that it did. The app then shows a reminder with
// made-up numbers at the notch, whose cleanup deletes nothing, looks at what
// the system did with its window, writes what it found to that folder, and
// exits: nothing else of the app starts. CLEANZO_ISLAND_CHECK_IDLE, a number
// of seconds, adds the measure of what a folded island costs while it waits.
func islandCheck() bool {
	dir := os.Getenv("CLEANZO_ISLAND_CHECK")
	if dir == "" {
		return false
	}
	mygo.App.SetLanguages("en", "zh-Hans")
	// No Dock icon, and the app in front stays there.
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.WhenReady(func() {
		go func() {
			rep := runIslandCheck(dir)
			if rep.Failures == nil {
				rep.Failures = []string{}
			}
			data, err := json.MarshalIndent(rep, "", "  ")
			if err == nil {
				err = os.WriteFile(filepath.Join(dir, "island.json"), append(data, '\n'), 0o644)
			}
			if err != nil {
				log.Print(err)
				mygo.App.Exit(1)
			}
			fmt.Println(string(data))
			if len(rep.Failures) > 0 {
				mygo.App.Exit(1)
			}
			mygo.App.Exit(0)
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	return true
}

// islandCheckReminder is a reminder at the notch with made-up numbers. Its
// cleanup takes a little over a second and touches no file.
func islandCheckReminder(n islandNotch) (r *trashReminderRun, island *trashIslandOnScreen, ended chan struct{}) {
	ended = make(chan struct{})
	r = newTrashReminderRun(trashReminderSnapshot{Size: 3 * trashReminderGB}, func() { close(ended) })
	r.clean = func(ctx context.Context, s trashReminderSnapshot, progress func(int64)) CleanResult {
		for i := int64(1); i <= 10; i++ {
			select {
			case <-ctx.Done():
				return CleanResult{Freed: s.Size * (i - 1) / 10}
			case <-time.After(120 * time.Millisecond):
			}
			progress(s.Size * i / 10)
		}
		return CleanResult{Freed: s.Size}
	}
	mygo.RunOnMain(func() {
		if surface, ok := r.openIsland(n); ok {
			r.surface, r.island = surface, surface
			island = surface.(*trashIslandOnScreen)
		}
	})
	return r, island, ended
}

func runIslandCheck(dir string) (rep islandCheckReport) {
	fail := func(format string, args ...any) {
		rep.Failures = append(rep.Failures, fmt.Sprintf(format, args...))
	}
	main := mygo.RunOnMain
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail("%v", err)
		return rep
	}
	var n islandNotch
	var front int32
	found := false
	main(func() { n, found = readIslandNotch(); front = islandCheckFront() })
	if !found {
		return rep
	}
	rep.Notch = &n

	r, island, ended := islandCheckReminder(n)
	if island == nil {
		main(func() { fail("the island's window could not be kept at the notch: %v", islandPanelError) })
		return rep
	}
	rep.Placed = true
	win := island.win.(*islandPanel).win
	moves := 0
	var unwatch func()
	main(func() { unwatch = watchIslandPointer(func() { moves++ }) })
	defer func() { main(unwatch) }()
	facts := func() (key, active bool) {
		h := objc.ID(win.NativeHandle())
		app := objc.ID(objc.GetClass("NSApplication")).Send(nsSel("sharedApplication"))
		return objc.Send[bool](h, nsSel("isKeyWindow")), objc.Send[bool](app, nsSel("isActive"))
	}

	// Out of the notch, not yet taking the click that empties.
	time.Sleep(600 * time.Millisecond)
	var button ui.Rect
	main(func() {
		h := objc.ID(win.NativeHandle())
		rep.Frame = win.Bounds()
		rep.AtNotch = rep.Frame == island.s.frame()
		rep.Level = int(objc.Send[int64](h, nsSel("level")))
		rep.Visible = objc.Send[bool](h, nsSel("isVisible"))
		rep.KeyWindow, rep.AppActive = facts()
		rep.FrontChanged = islandCheckFront() != front
		islandCheckShot(win, filepath.Join(dir, "island-question.png"), fail)
		// The button that empties is the last of its row, at the island's
		// bottom right: a point well inside it, whatever its label.
		at, _ := island.s.region()
		button = ui.Rect{X: float32(at.X + at.Width - 68), Y: float32(at.Height - 40), W: 20, H: 20}
		islandCheckClick(win, button)
	})
	// The view takes a click on a later turn of the main thread.
	time.Sleep(200 * time.Millisecond)
	main(func() { rep.EarlyClickIgnored = r.p.phase == "" })

	// Taking it now.
	time.Sleep(700 * time.Millisecond)
	before := int32(0)
	main(func() {
		before = islandCheckFront()
		islandCheckClick(win, button)
	})
	time.Sleep(200 * time.Millisecond)
	heard := 0
	main(func() {
		rep.ClickStarted = r.p.phase == "cleaning"
		rep.KeyAfterClick, rep.ActiveAfterClick = facts()
		rep.FrontChangedByClick = islandCheckFront() != before
		islandCheckShot(win, filepath.Join(dir, "island-cleaning.png"), fail)
		heard = moves
		islandCheckMove()
	})
	time.Sleep(300 * time.Millisecond)
	main(func() { rep.MovesHeard, rep.SentMovesHeard = moves, moves-heard })

	// The made-up cleanup ends, the result shows, and the island goes back.
	select {
	case <-ended:
		rep.Ended = true
	case <-time.After(15 * time.Second):
	}
	main(func() { rep.Phase = r.p.phase })

	if seconds, _ := strconv.Atoi(os.Getenv("CLEANZO_ISLAND_CHECK_IDLE")); seconds > 0 {
		rep.Idle = islandCheckCost(n, seconds, fail)
	}

	for _, c := range []struct {
		ok   bool
		what string
	}{
		{rep.AtNotch, "the window is not where it was put"},
		{rep.Level == islandLevel, "the window is not above the menu bar"},
		{rep.Visible, "the window does not show"},
		{!rep.KeyWindow && !rep.KeyAfterClick, "the island took the keyboard"},
		{!rep.AppActive && !rep.ActiveAfterClick, "the island activated the app"},
		{!rep.FrontChangedByClick, "a click on the island changed the app in front"},
		{rep.EarlyClickIgnored, "a click emptied before the island took it"},
		{rep.ClickStarted, "a click on the button that empties started nothing"},
		{rep.SentMovesHeard > 0, "the moves of the mouse sent to the app were not heard"},
		{rep.Ended && rep.Phase == "done", "the reminder did not end with its cleanup done"},
	} {
		if !c.ok {
			fail("%s", c.what)
		}
	}
	return rep
}

// islandCheckCost measures the processor time of this app over seconds
// without an island, and over as long with a folded one waiting.
func islandCheckCost(n islandNotch, seconds int, fail func(string, ...any)) *islandCheckIdle {
	over := func() float64 {
		var a, b syscall.Rusage
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &a)
		time.Sleep(time.Duration(seconds) * time.Second)
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &b)
		cpu := func(r syscall.Rusage) float64 {
			return float64(r.Utime.Sec+r.Stime.Sec) + float64(r.Utime.Usec+r.Stime.Usec)/1e6
		}
		return cpu(b) - cpu(a)
	}
	idle := &islandCheckIdle{Seconds: seconds, WithoutIsland: over()}
	r, island, ended := islandCheckReminder(n)
	if island == nil {
		fail("the island's window could not be kept at the notch a second time")
		return idle
	}
	time.Sleep(islandOpenDelay + islandLinger + 2*time.Second)
	folded := false
	mygo.RunOnMain(func() { folded = island.s.shape == islandCompact })
	if !folded {
		fail("the island did not fold: the pointer may have been on it")
	}
	idle.Folded = over()
	mygo.RunOnMain(func() { r.p.close() })
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		fail("the folded island did not leave")
	}
	return idle
}

func islandCheckFront() int32 {
	ws := objc.ID(objc.GetClass("NSWorkspace")).Send(nsSel("sharedWorkspace"))
	app := ws.Send(nsSel("frontmostApplication"))
	if app == 0 {
		return 0
	}
	return objc.Send[int32](app, nsSel("processIdentifier"))
}

func islandCheckShot(w *mygo.Window, path string, fail func(string, ...any)) {
	png, err := w.CapturePage()
	if err == nil {
		err = os.WriteFile(path, png, 0o644)
	}
	if err != nil {
		fail("%v", err)
	}
}

const islandCheckMouseEvent = "mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:"

// islandCheckClick sends the window a press and a release of the left
// button in the middle of r, a rectangle of its content. The pointer does
// not move.
func islandCheckClick(w *mygo.Window, r ui.Rect) {
	h := objc.ID(w.NativeHandle())
	height := objc.Send[nsRect](h, nsSel("frame")).H
	at := nsPoint{float64(r.X + r.W/2), height - float64(r.Y+r.H/2)} // AppKit counts from the bottom
	number := objc.Send[int64](h, nsSel("windowNumber"))
	for _, kind := range []uint64{5, 1, 2} { // moved, left button down, left button up
		ev := objc.ID(objc.GetClass("NSEvent")).Send(nsSel(islandCheckMouseEvent),
			kind, at, uint64(0), islandCheckUptime(), number, objc.ID(0), int64(0), int64(1), float32(1))
		h.Send(nsSel("sendEvent:"), ev)
	}
}

// islandCheckMove puts three moves of the mouse in this app's own queue,
// which the watch of the pointer hears as it does those the system sends.
func islandCheckMove() {
	app := objc.ID(objc.GetClass("NSApplication")).Send(nsSel("sharedApplication"))
	for i := range 3 {
		ev := objc.ID(objc.GetClass("NSEvent")).Send(nsSel(islandCheckMouseEvent),
			uint64(5), nsPoint{float64(10 + i), 10}, uint64(0), islandCheckUptime(), int64(0), objc.ID(0), int64(0), int64(0), float32(0))
		app.Send(nsSel("postEvent:atStart:"), ev, false)
	}
}

func islandCheckUptime() float64 {
	return objc.Send[float64](objc.ID(objc.GetClass("NSProcessInfo")).Send(nsSel("processInfo")), nsSel("systemUptime"))
}
