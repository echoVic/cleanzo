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
	// How often the app was told that the user has no window open: as a
	// window closed beside the island, which must tell it once, and as one
	// closed with no island, which mygo itself reports.
	SettledBesideIsland  int `json:"settledBesideIsland"`
	SettledWithoutIsland int `json:"settledWithoutIsland"`
	// How the reminder ended.
	Phase string `json:"phase"`
	Ended bool   `json:"ended"`
	// Idle is the processor time the app took over a stretch with a folded
	// island waiting and over one without an island, where asked for.
	Idle *islandCheckIdle `json:"idle,omitempty"`
	// Window, Moved and Closed tell how a reminder fared in its window, how
	// one that went from the island into a window did, and how one whose
	// island was closed under it, as the app does when it quits. They are
	// checked where asked for: the window comes to the front.
	Window   *islandCheckWindowed `json:"window,omitempty"`
	Moved    *islandCheckWindowed `json:"moved,omitempty"`
	Closed   *islandCheckWindowed `json:"closed,omitempty"`
	Failures []string             `json:"failures"`
}

type islandCheckWindowed struct {
	// Shown is whether a window showed the reminder.
	Shown bool `json:"shown"`
	// Started is whether the cleanup was under way: after a click in the
	// window, or as the reminder left the island.
	Started bool `json:"started"`
	// Phase is where the reminder was at its end, and Ended how many times
	// it ended, which is once.
	Phase string `json:"phase"`
	Ended int    `json:"ended"`
	// Cancelled is whether the cleanup was told to stop.
	Cancelled bool `json:"cancelled"`
}

type islandCheckIdle struct {
	Seconds       int     `json:"seconds"`
	WithoutIsland float64 `json:"withoutIslandCPUSeconds"`
	Folded        float64 `json:"foldedCPUSeconds"`
	// MovesHeard counts the moves of the mouse the watch heard while the
	// island was folded: those of the user, over whatever app is in front.
	MovesHeard int `json:"movesHeard"`
}

type nsPoint struct{ X, Y float64 }

// islandCheckSettled counts what watchWindowsBesideIslands told the check.
// The main thread's.
var islandCheckSettled int

// islandCheck runs the island check where CLEANZO_ISLAND_CHECK names a
// folder, and reports that it did. The app then shows a reminder with
// made-up numbers at the notch, whose cleanup deletes nothing, looks at what
// the system did with its window, writes what it found to that folder, and
// exits: nothing else of the app starts. CLEANZO_ISLAND_CHECK_IDLE, a number
// of seconds, adds the measure of what a folded island costs while it waits.
// CLEANZO_ISLAND_CHECK_WINDOW adds the reminder in its window, which comes
// to the front, a reminder that moves from the island into a window, and one
// whose island is closed under it.
func islandCheck() bool {
	dir := os.Getenv("CLEANZO_ISLAND_CHECK")
	if dir == "" {
		return false
	}
	mygo.App.SetLanguages("en", "zh-Hans")
	// No Dock icon, and the app in front stays there.
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {})
	watchWindowsBesideIslands(func() { islandCheckSettled++ })
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

// islandCheckRun is a reminder with made-up numbers, not shown yet. Its
// cleanup takes a little over a second and touches no file. closed runs on
// the main thread as the reminder ends.
func islandCheckRun(closed func()) *trashReminderRun {
	r := newTrashReminderRun(trashReminderSnapshot{Size: 3 * trashReminderGB}, closed)
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
	return r
}

// islandCheckReminder is such a reminder at the notch.
func islandCheckReminder(n islandNotch) (r *trashReminderRun, island *trashIslandOnScreen, ended chan struct{}) {
	ended = make(chan struct{})
	r = islandCheckRun(func() { close(ended) })
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
		// bottom right: a point well inside it, whatever its label. A tester
		// would tell where exactly, but one built here shares what draws the
		// text with the window, whose text then comes out in pieces.
		at, _ := island.s.region()
		button = ui.Rect{X: float32(at.X + at.Width - 68), Y: float32(at.Height - 40), W: 20, H: 20}
		islandCheckClick(win, button)
	})
	// The view takes a click on a later turn of the main thread.
	time.Sleep(200 * time.Millisecond)
	// A window the user could have opened closes while the island shows.
	plain := func() {
		mygo.NewWindow(mygo.WindowOptions{Hidden: true, Width: 200, Height: 120, Content: ui.View(func(*ui.Context) {})}).Destroy()
	}
	main(func() {
		rep.EarlyClickIgnored = r.p.phase == ""
		plain()
		rep.SettledBesideIsland = islandCheckSettled
	})

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
	main(func() {
		rep.Phase = r.p.phase
		if rep.Ended {
			plain()
			rep.SettledWithoutIsland = islandCheckSettled - rep.SettledBesideIsland
		}
	})

	if seconds, _ := strconv.Atoi(os.Getenv("CLEANZO_ISLAND_CHECK_IDLE")); seconds > 0 {
		rep.Idle = islandCheckCost(n, seconds, fail)
	}
	if os.Getenv("CLEANZO_ISLAND_CHECK_WINDOW") != "" {
		rep.Window, rep.Moved, rep.Closed = islandCheckWindows(n, dir, fail)
		for _, c := range []struct {
			got  *islandCheckWindowed
			want islandCheckWindowed
			what string
		}{
			{rep.Window, islandCheckWindowed{Shown: true, Started: true, Phase: "done", Ended: 1}, "in its window"},
			{rep.Moved, islandCheckWindowed{Shown: true, Started: true, Phase: "done", Ended: 1}, "moved from the island into a window"},
			{rep.Closed, islandCheckWindowed{Started: true, Phase: "cleaning", Ended: 1, Cancelled: true}, "whose island was closed under it"},
		} {
			if *c.got != c.want {
				fail("the reminder %s: %+v, want %+v", c.what, *c.got, c.want)
			}
		}
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
		{rep.SettledBesideIsland == 1, "a window closing beside the island did not tell the app it has none left, once"},
		{rep.SettledWithoutIsland == 0, "a window closing with no island told the app what mygo already reports"},
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
	moves := 0
	var unwatch func()
	mygo.RunOnMain(func() { unwatch = watchIslandPointer(func() { moves++ }) })
	idle.Folded = over()
	mygo.RunOnMain(func() {
		unwatch()
		idle.MovesHeard = moves
		r.p.close()
	})
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		fail("the folded island did not leave")
	}
	return idle
}

// islandCheckWindows runs a reminder in its window, one that moves from the
// island into a window while it cleans, and one whose island is closed under
// it while it cleans.
func islandCheckWindows(n islandNotch, dir string, fail func(string, ...any)) (window, moved, closed *islandCheckWindowed) {
	main := mygo.RunOnMain
	window, moved, closed = &islandCheckWindowed{}, &islandCheckWindowed{}, &islandCheckWindowed{}
	shown := func(r *trashReminderRun) *mygo.Window {
		if s, ok := r.surface.(*trashWindowSurface); ok {
			return s.w
		}
		return nil
	}
	// The last button of the window's row, at its bottom right: the one
	// that empties, then the one that closes.
	last := func(w *mygo.Window) ui.Rect {
		_, h := w.ContentSize()
		return ui.Rect{X: trashPopupWidth - 70, Y: float32(h - 45), W: 20, H: 20}
	}
	// finish waits for the made-up cleanup, closes the window with its
	// button, and tells how the reminder ended.
	finish := func(r *trashReminderRun, got *islandCheckWindowed, ended *int, picture string) {
		for range 40 {
			done := false
			main(func() { done = r.p.phase == "done" })
			if done {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		main(func() {
			if w := shown(r); w != nil {
				islandCheckShot(w, filepath.Join(dir, picture), fail)
				islandCheckClick(w, last(w))
			}
		})
		time.Sleep(500 * time.Millisecond)
		main(func() { got.Phase, got.Ended = r.p.phase, *ended })
	}

	// In its window, from the question to the result.
	ends := 0
	r := islandCheckRun(func() { ends++ })
	main(func() { r.surface = r.openWindow() })
	time.Sleep(600 * time.Millisecond)
	main(func() {
		if w := shown(r); w != nil {
			window.Shown = true
			islandCheckShot(w, filepath.Join(dir, "window-question.png"), fail)
			islandCheckClick(w, last(w))
		}
	})
	time.Sleep(300 * time.Millisecond)
	main(func() { window.Started = r.p.phase == "cleaning" })
	finish(r, window, &ends, "window-result.png")

	// From the island into a window, the cleanup under way.
	moves := 0
	r, island, _ := islandCheckReminder(n)
	if island == nil {
		fail("the island's window could not be kept at the notch a third time")
		return window, moved, closed
	}
	main(func() { r.closed = func() { moves++ } })
	click := func(island *trashIslandOnScreen) {
		at, _ := island.s.region()
		islandCheckClick(island.win.(*islandPanel).win, ui.Rect{X: float32(at.X + at.Width - 68), Y: float32(at.Height - 40), W: 20, H: 20})
	}
	time.Sleep(islandOpenDelay + islandArmDelay + 300*time.Millisecond)
	main(func() { click(island) })
	time.Sleep(300 * time.Millisecond)
	main(func() {
		moved.Started = r.p.phase == "cleaning"
		r.intoWindow()
	})
	time.Sleep(300 * time.Millisecond)
	main(func() { moved.Shown = shown(r) != nil && island.over })
	finish(r, moved, &moves, "moved-result.png")

	// The island closed under a cleanup, as the app does when it quits.
	stops := 0
	r, island, _ = islandCheckReminder(n)
	if island == nil {
		fail("the island's window could not be kept at the notch a fourth time")
		return window, moved, closed
	}
	main(func() { r.closed = func() { stops++ } })
	time.Sleep(islandOpenDelay + islandArmDelay + 300*time.Millisecond)
	main(func() { click(island) })
	time.Sleep(300 * time.Millisecond)
	main(func() {
		closed.Started = r.p.phase == "cleaning"
		island.win.(*islandPanel).win.Destroy()
	})
	time.Sleep(500 * time.Millisecond)
	main(func() { closed.Phase, closed.Ended, closed.Cancelled = r.p.phase, stops, r.ctx.Err() != nil })
	return window, moved, closed
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
	// AppKit counts from the bottom of the window, where its content is.
	height := objc.Send[nsRect](h.Send(nsSel("contentView")), nsSel("frame")).H
	at := nsPoint{float64(r.X + r.W/2), height - float64(r.Y+r.H/2)}
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
