//go:build darwin

package main

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeTrashSurface stands for a window or an island on screen.
type fakeTrashSurface struct {
	kind      string
	draws     int
	discarded bool
	moved     []islandNotch
	left      int
	alone     bool // nobody reads it
}

func (f *fakeTrashSurface) redraw()              { f.draws++ }
func (f *fakeTrashSurface) discard()             { f.discarded = true }
func (f *fakeTrashSurface) moveTo(n islandNotch) { f.moved = append(f.moved, n) }
func (f *fakeTrashSurface) unwatched() bool      { return f.alone }
func (f *fakeTrashSurface) leave()               { f.left++ }

// trashScreen is what a reminder finds as it shows: the notch, VoiceOver,
// and whether an island can be placed. opened lists what it showed on.
type trashScreen struct {
	notch     bool
	voiceOver bool
	placeable bool
	opened    []*fakeTrashSurface
	// main stands for the main thread: what a cleanup runs there from its
	// goroutine, and what the test reads of the reminder meanwhile, take
	// turns.
	main sync.Mutex
}

// waitFor waits until cond holds, read as the main thread would read it.
func (s *trashScreen) waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
		s.main.Lock()
		ok := cond()
		s.main.Unlock()
		if ok {
			return
		}
	}
	t.Fatal("waited five seconds")
}

func (s *trashScreen) kinds() []string {
	var out []string
	for _, f := range s.opened {
		out = append(out, f.kind)
	}
	return out
}

// fakeTrashScreen replaces the screen and the surfaces of reminders for a
// test: a notch, no VoiceOver, an island that can be placed.
func fakeTrashScreen(t *testing.T) *trashScreen {
	setHome(t, t.TempDir())
	s := &trashScreen{notch: true, placeable: true}
	onMain, notch, voice, island, window := runOnMain, islandNotchNow, voiceOverOn, openTrashIsland, openTrashWindow
	runOnMain = func(f func()) {
		s.main.Lock()
		defer s.main.Unlock()
		f()
	}
	t.Cleanup(func() {
		runOnMain, islandNotchNow, voiceOverOn, openTrashIsland, openTrashWindow = onMain, notch, voice, island, window
		islandUnplaceable.Store(false)
		trashReminderCurrent.Store(nil)
	})
	islandUnplaceable.Store(false)
	islandNotchNow = func() (islandNotch, bool) {
		return islandNotch{X: 771, Width: 185, Height: 32}, s.notch
	}
	voiceOverOn = func() bool { return s.voiceOver }
	openTrashIsland = func(*trashReminderRun, islandNotch) (trashIslandSurface, bool) {
		if !s.placeable {
			return nil, false
		}
		f := &fakeTrashSurface{kind: "island"}
		s.opened = append(s.opened, f)
		return f, true
	}
	openTrashWindow = func(*trashReminderRun) trashSurface {
		f := &fakeTrashSurface{kind: "window"}
		s.opened = append(s.opened, f)
		return f
	}
	return s
}

// TestTrashReminderShowsAtTheNotchWhenItCan: the island takes a notch, the
// setting, no VoiceOver, and a window that could be placed; anything else
// is the window. An island that could not be placed is not tried again.
func TestTrashReminderShowsAtTheNotchWhenItCan(t *testing.T) {
	cases := []struct {
		name string
		set  func(*trashScreen)
		want []string
	}{
		{"a notch", func(*trashScreen) {}, []string{"island"}},
		{"no notch", func(s *trashScreen) { s.notch = false }, []string{"window"}},
		{"VoiceOver", func(s *trashScreen) { s.voiceOver = true }, []string{"window"}},
		{"turned off in the settings", func(*trashScreen) {
			if err := saveTrashReminder(trashReminderDir(), TrashReminderConfig{Enabled: true, ThresholdGB: 1}); err != nil {
				t.Fatal(err)
			}
		}, []string{"window"}},
		{"an island that cannot be placed", func(s *trashScreen) { s.placeable = false }, []string{"window"}},
	}
	for _, c := range cases {
		s := fakeTrashScreen(t)
		c.set(s)
		newTrashReminderRun(trashReminderSnapshot{Size: 2 * gb}, func() {}).show()
		if got := s.kinds(); !slices.Equal(got, c.want) {
			t.Errorf("%s: shown on %v, want %v", c.name, got, c.want)
		}
	}

	s := fakeTrashScreen(t)
	s.placeable = false
	newTrashReminderRun(trashReminderSnapshot{Size: 2 * gb}, func() {}).show()
	s.placeable = true
	newTrashReminderRun(trashReminderSnapshot{Size: 2 * gb}, func() {}).show()
	if got := s.kinds(); !slices.Equal(got, []string{"window", "window"}) {
		t.Errorf("after an island that could not be placed, the next reminder shows on %v", got)
	}
}

// TestTrashReminderMovesToAWindowWhenTheNotchIsGone: closing the lid takes
// the notch away under a reminder; it goes on in a window, at the phase and
// progress it had, and ends once, when that window closes.
func TestTrashReminderMovesToAWindowWhenTheNotchIsGone(t *testing.T) {
	s := fakeTrashScreen(t)
	ended := 0
	r := newTrashReminderRun(trashReminderSnapshot{Size: 2 * gb}, func() { ended++ })
	r.show()
	r.p.phase, r.p.freed = "cleaning", gb

	r.displaysChanged()
	if got := s.kinds(); !slices.Equal(got, []string{"island"}) || len(s.opened[0].moved) != 1 {
		t.Fatalf("the notch still there: shown on %v, the island moved %d times", got, len(s.opened[0].moved))
	}

	s.notch = false
	r.displaysChanged()
	if got := s.kinds(); !slices.Equal(got, []string{"island", "window"}) {
		t.Fatalf("the notch gone: shown on %v", got)
	}
	if !s.opened[0].discarded || s.opened[1].discarded {
		t.Fatalf("the island discarded: %v, the window: %v", s.opened[0].discarded, s.opened[1].discarded)
	}
	if r.p.phase != "cleaning" || r.p.freed != gb || ended != 0 || r.ctx.Err() != nil {
		t.Fatalf("after the move: phase %q, freed %d, ended %d times, cancelled: %v", r.p.phase, r.p.freed, ended, r.ctx.Err() != nil)
	}
	// A window does not follow the displays.
	s.notch = true
	r.displaysChanged()
	if got := s.kinds(); len(got) != 2 {
		t.Fatalf("a notch that came back moved the reminder again: %v", got)
	}

	r.end()
	r.end()
	if ended != 1 || r.ctx.Err() == nil {
		t.Fatalf("ended %d times, cleanup cancelled: %v", ended, r.ctx.Err() != nil)
	}
}

// TestTrashReminderAtTheNotchFollowsTheTrash: a reminder that waits at the
// notch leaves once the Trash is under the threshold, and takes a new
// measurement only while nobody reads it; a click then deletes what that
// measurement found.
func TestTrashReminderAtTheNotchFollowsTheTrash(t *testing.T) {
	s := fakeTrashScreen(t)
	r := newTrashReminderRun(trashReminderSnapshot{Size: 2 * gb, At: time.Unix(100, 0)}, func() {})
	cleaned := make(chan trashReminderSnapshot, 1)
	r.clean = func(_ context.Context, snap trashReminderSnapshot, _ func(int64)) CleanResult {
		cleaned <- snap
		return CleanResult{Freed: snap.Size}
	}
	if trashReminderWaiting() {
		t.Fatal("waiting before any reminder shows")
	}
	r.show()
	island := s.opened[0]
	if !trashReminderWaiting() {
		t.Fatal("a reminder asking at the notch does not wait")
	}

	grown := trashReminderSnapshot{Size: 3 * gb, At: time.Unix(200, 0)}
	if reviseTrashReminder(grown, true) || r.p.size != 2*gb || !r.snap.At.Equal(time.Unix(100, 0)) {
		t.Fatalf("read by the user: took the new measurement (size %d)", r.p.size)
	}
	island.alone = true
	draws := island.draws
	if !reviseTrashReminder(grown, true) || r.p.size != 3*gb || !r.snap.At.Equal(time.Unix(200, 0)) || island.draws == draws {
		t.Fatalf("out of sight: size %d, snapshot of %v, drawn again: %v", r.p.size, r.snap.At, island.draws != draws)
	}
	if island.left != 0 {
		t.Fatal("the island left for a Trash still over the threshold")
	}

	r.p.begin()
	if got := <-cleaned; got.Size != 3*gb || !got.At.Equal(time.Unix(200, 0)) {
		t.Fatalf("the click deleted the measurement of %v, %d bytes", got.At, got.Size)
	}
	s.waitFor(t, func() bool { return r.p.phase == "done" })
	if trashReminderWaiting() || reviseTrashReminder(trashReminderSnapshot{Size: gb / 2}, false) || island.left != 0 {
		t.Fatal("a reminder past its question took a new measurement")
	}

	again := newTrashReminderRun(trashReminderSnapshot{Size: 2 * gb}, func() {})
	again.show()
	if !reviseTrashReminder(trashReminderSnapshot{Size: gb / 2}, false) || s.opened[1].left != 1 {
		t.Fatalf("a Trash under the threshold: the island left %d times", s.opened[1].left)
	}
}

// TestTrashReminderInAWindowKeepsWhatItShowed: the window does not follow
// the Trash.
func TestTrashReminderInAWindowKeepsWhatItShowed(t *testing.T) {
	s := fakeTrashScreen(t)
	s.notch = false
	r := newTrashReminderRun(trashReminderSnapshot{Size: 2 * gb}, func() {})
	r.show()
	if trashReminderWaiting() || reviseTrashReminder(trashReminderSnapshot{Size: gb / 2}, false) || r.p.size != 2*gb {
		t.Fatal("a reminder in a window followed the Trash")
	}
}

// TestTrashReminderCleansReportsAndStops: the cleanup a click starts tells
// its progress and its result to the reminder, and stops when cancelled;
// nothing reaches a reminder that ended.
func TestTrashReminderCleansReportsAndStops(t *testing.T) {
	s := fakeTrashScreen(t)
	// A cleanup that frees half, then the rest once told to, unless it is
	// cancelled first.
	halfThenRest := func(proceed chan struct{}) func(context.Context, trashReminderSnapshot, func(int64)) CleanResult {
		return func(ctx context.Context, snap trashReminderSnapshot, progress func(int64)) CleanResult {
			progress(gb)
			select {
			case <-proceed:
				return CleanResult{Freed: snap.Size}
			case <-ctx.Done():
				return CleanResult{Freed: gb}
			}
		}
	}
	r := newTrashReminderRun(trashReminderSnapshot{Size: 2 * gb}, func() {})
	proceed := make(chan struct{})
	r.clean = halfThenRest(proceed)
	r.show()
	r.p.begin()
	s.waitFor(t, func() bool { return r.p.freed == gb && r.p.phase == "cleaning" && s.opened[0].draws > 0 })
	close(proceed)
	s.waitFor(t, func() bool { return r.p.phase == "done" && r.p.freed == 2*gb })

	stopped := newTrashReminderRun(trashReminderSnapshot{Size: 2 * gb}, func() {})
	stopped.clean = halfThenRest(make(chan struct{}))
	stopped.show()
	stopped.p.begin()
	s.waitFor(t, func() bool { return stopped.p.freed == gb })
	runOnMain(stopped.p.stop)
	s.waitFor(t, func() bool { return stopped.p.phase == "cancelled" && stopped.p.freed == gb })

	ended := newTrashReminderRun(trashReminderSnapshot{Size: 2 * gb}, func() {})
	ended.show()
	ended.end()
	ran := false
	ended.update(func() { ran = true })
	if ran {
		t.Fatal("an update reached a reminder that ended")
	}
}

// TestTrashReminderThatCannotCleanNowSaysSo: while the main window's cleanup
// holds the Trash, the reminder's own does not start.
func TestTrashReminderThatCannotCleanNowSaysSo(t *testing.T) {
	s := fakeTrashScreen(t)
	r := newTrashReminderRun(trashReminderSnapshot{Size: 2 * gb}, func() {})
	r.clean = func(context.Context, trashReminderSnapshot, func(int64)) CleanResult {
		t.Error("cleaned while another cleanup held the Trash")
		return CleanResult{}
	}
	r.show()
	trashCleanupGate.Lock()
	r.p.begin()
	s.waitFor(t, func() bool { return r.p.phase == "partial" })
	trashCleanupGate.Unlock()
}

// TestTrashReminderTellsThePageWhetherADisplayHasANotch: the settings page
// offers the choice of the notch only where there is one.
func TestTrashReminderTellsThePageWhetherADisplayHasANotch(t *testing.T) {
	s := fakeTrashScreen(t)
	if got := (Cleaner{}).TrashReminder(); !got.Notch || !got.Supported || !got.Config.Island {
		t.Fatalf("with a notch: %+v", got)
	}
	s.notch = false
	if got := (Cleaner{}).TrashReminder(); got.Notch {
		t.Fatalf("without a notch: %+v", got)
	}
}
