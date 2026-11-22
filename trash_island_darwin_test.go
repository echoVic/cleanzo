//go:build darwin

package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// islandRun drives an island with a clock of its own. Time passes only as
// far as the island asked to be woken, as it does in the app, where nothing
// polls it.
type islandRun struct {
	t    *testing.T
	s    *trashIsland
	p    *trashReminderPopup
	now  time.Time
	gone int
}

func newIslandRun(t *testing.T) *islandRun {
	r := &islandRun{t: t, now: time.Unix(1000, 0)}
	r.p = &trashReminderPopup{size: 3 * gb, text: panelViewText}
	r.s = newTrashIsland(r.p, islandNotch{X: 771, Y: 0, Width: 185, Height: 32}, func() time.Time { return r.now })
	r.s.gone = func() { r.gone++ }
	r.s.appear()
	return r
}

// pass lets d go by, the pointer on the island or not.
func (r *islandRun) pass(d time.Duration, inside bool) {
	end := r.now.Add(d)
	for {
		next, _ := r.s.step(r.now, inside)
		if next.IsZero() || next.After(end) {
			break
		}
		r.now = next
	}
	r.now = end
	r.s.step(r.now, inside)
}

func (r *islandRun) want(what string, shape islandShape, gone int) {
	r.t.Helper()
	if r.s.shape != shape || r.gone != gone {
		r.t.Fatalf("%s: shape %d, gone %d times; want %d, %d", what, r.s.shape, r.gone, shape, gone)
	}
}

func TestTrashIslandComesOutThenFoldsAndStays(t *testing.T) {
	r := newIslandRun(t)
	r.want("as it shows", islandRest, 0)
	r.pass(100*time.Millisecond, false)
	r.want("a moment later", islandOpen, 0)
	r.pass(9800*time.Millisecond, false)
	r.want("before ten seconds untouched", islandOpen, 0)
	r.pass(300*time.Millisecond, false)
	r.want("after ten seconds untouched", islandCompact, 0)
	r.pass(time.Hour, false)
	r.want("an hour later", islandCompact, 0)
}

func TestPointerOpensTheIslandAndLeavingFoldsIt(t *testing.T) {
	r := newIslandRun(t)
	r.pass(10200*time.Millisecond, false)
	r.want("folded", islandCompact, 0)
	r.pass(10*time.Millisecond, true)
	r.want("the pointer on it", islandOpen, 0)
	r.pass(time.Minute, true)
	r.want("while the pointer stays", islandOpen, 0)
	r.pass(800*time.Millisecond, false)
	r.want("just after the pointer left", islandOpen, 0)
	r.pass(200*time.Millisecond, false)
	r.want("a second after the pointer left", islandCompact, 0)
}

// TestTrashIslandThatCameOutByItselfIsNotArmedAtOnce: a click meant for what
// the island covered as it came out must not empty the Trash, so the island
// takes that click only a second after it opened by itself. One the pointer
// opened takes it at once.
func TestTrashIslandThatCameOutByItselfIsNotArmedAtOnce(t *testing.T) {
	r := newIslandRun(t)
	r.pass(900*time.Millisecond, true)
	if r.s.armed() {
		t.Fatal("armed 0.9 s after it showed")
	}
	r.pass(300*time.Millisecond, true)
	if !r.s.armed() {
		t.Fatal("not armed 1.2 s after it showed")
	}
	r.pass(time.Second, false)
	r.pass(10*time.Second, false)
	r.want("folded", islandCompact, 0)
	r.pass(10*time.Millisecond, true)
	r.want("opened by the pointer", islandOpen, 0)
	if !r.s.armed() {
		t.Fatal("not armed as the pointer opened it")
	}
}

func TestTrashIslandShowsTheCleaningThenTheResultThenGoes(t *testing.T) {
	r := newIslandRun(t)
	r.pass(1200*time.Millisecond, true)
	r.p.begin()
	r.pass(5*time.Second, true)
	r.want("cleaning, the pointer on it", islandOpen, 0)
	r.pass(800*time.Millisecond, false)
	r.want("just after the pointer left", islandOpen, 0)
	r.pass(200*time.Millisecond, false)
	r.want("cleaning, the pointer gone", islandCompact, 0)
	r.pass(time.Minute, false)
	r.want("still cleaning", islandCompact, 0)

	r.p.finish(CleanResult{Freed: 3 * gb}, false)
	r.pass(3900*time.Millisecond, false)
	r.want("the result shows", islandOpen, 0)
	r.pass(200*time.Millisecond, false)
	r.want("going back after four seconds", islandRest, 0)
	r.pass(600*time.Millisecond, false)
	r.want("gone", islandRest, 1)
	r.pass(time.Hour, true)
	r.want("gone for good", islandRest, 1)
}

func TestTrashIslandKeepsAResultWhileThePointerIsOnIt(t *testing.T) {
	r := newIslandRun(t)
	r.pass(1200*time.Millisecond, true)
	r.p.begin()
	r.p.finish(CleanResult{Freed: 3 * gb}, false)
	r.pass(time.Minute, true)
	r.want("the pointer on the result", islandOpen, 0)
	r.pass(1700*time.Millisecond, false)
	r.want("gone once the pointer left", islandRest, 1)
}

// TestTrashIslandKeepsWhatWasNotCleanedUntilItIsClosed: a result that says
// something was kept stays, folded like the question, until the user closes
// it.
func TestTrashIslandKeepsWhatWasNotCleanedUntilItIsClosed(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		r := newIslandRun(t)
		r.pass(1200*time.Millisecond, true)
		r.p.begin()
		r.pass(3*time.Second, false)
		r.want("cleaning, folded", islandCompact, 0)
		r.p.finish(CleanResult{Freed: gb, Failed: []string{"kept"}}, cancelled)
		r.pass(9900*time.Millisecond, false)
		r.want("the result shows", islandOpen, 0)
		r.pass(200*time.Millisecond, false)
		r.want("untouched for ten seconds", islandCompact, 0)
		r.pass(time.Hour, false)
		r.want("an hour later", islandCompact, 0)
		r.pass(10*time.Millisecond, true)
		r.want("the pointer on it", islandOpen, 0)
		r.s.leave()
		r.pass(400*time.Millisecond, true)
		r.want("closed, going back", islandRest, 0)
		r.pass(200*time.Millisecond, true)
		r.want("gone", islandRest, 1)
	}
}

func TestTrashIslandGoesWhenToldEvenUnderThePointer(t *testing.T) {
	r := newIslandRun(t)
	r.pass(1200*time.Millisecond, true)
	r.s.leave()
	r.want("told to go", islandRest, 0)
	r.pass(400*time.Millisecond, true)
	r.want("going back", islandRest, 0)
	r.pass(200*time.Millisecond, true)
	r.want("gone", islandRest, 1)
}

// TestTrashIslandAsksToBeWokenOnlyWhenSomethingIsDue: nothing polls the
// island, so a folded one that waits must ask for no wake-up at all.
func TestTrashIslandAsksToBeWokenOnlyWhenSomethingIsDue(t *testing.T) {
	r := newIslandRun(t)
	start := r.now
	if next, _ := r.s.step(r.now, false); !next.Equal(start.Add(80 * time.Millisecond)) {
		t.Fatalf("as it shows, wakes at %v, want 80 ms later", next.Sub(start))
	}
	r.pass(100*time.Millisecond, false)
	// The button to empty is drawn again as it is armed.
	if next, _ := r.s.step(r.now, false); !next.Equal(start.Add(1080 * time.Millisecond)) {
		t.Fatalf("open, wakes at %v, want 1.08 s after it showed", next.Sub(start))
	}
	r.pass(2*time.Second, false)
	if next, _ := r.s.step(r.now, false); !next.Equal(start.Add(10080 * time.Millisecond)) {
		t.Fatalf("armed, wakes at %v, want 10.08 s after it showed", next.Sub(start))
	}
	r.pass(10*time.Second, false)
	r.want("folded", islandCompact, 0)
	if next, changed := r.s.step(r.now, false); !next.IsZero() || changed {
		t.Fatalf("folded and waiting: wakes at %v, changed %v; want no wake-up and no change", next, changed)
	}
	if _, changed := r.s.step(r.now, true); !changed {
		t.Fatal("the pointer opened it without a change to draw")
	}
}

// TestTrashIslandTakesANewMeasurementOnlyOutOfSight: the size the user reads
// and what a click would delete are of one measurement, so a new one comes
// in only while the island is folded and the pointer is elsewhere.
func TestTrashIslandTakesANewMeasurementOnlyOutOfSight(t *testing.T) {
	r := newIslandRun(t)
	r.pass(time.Second, false)
	if r.s.unwatched() {
		t.Fatal("open: a new measurement would change what the user reads")
	}
	r.pass(10*time.Second, false)
	if !r.s.unwatched() {
		t.Fatal("folded, the pointer elsewhere: not taken")
	}
	r.pass(10*time.Millisecond, true)
	if r.s.unwatched() {
		t.Fatal("the pointer on it")
	}
	r.pass(2*time.Second, false)
	r.s.leave()
	if r.s.unwatched() {
		t.Fatal("going back into the notch")
	}

	cleaning := newIslandRun(t)
	cleaning.pass(1200*time.Millisecond, true)
	cleaning.p.begin()
	cleaning.pass(3*time.Second, false)
	cleaning.want("cleaning, folded", islandCompact, 0)
	if cleaning.s.unwatched() {
		t.Fatal("cleaning: what is being deleted is settled")
	}
}

// TestTrashIslandShapesAndWhereTheMouseGoes: the island grows from the
// notch, centred on it, and takes the mouse in its own shape alone.
func TestTrashIslandShapesAndWhereTheMouseGoes(t *testing.T) {
	r := newIslandRun(t)
	if got := r.s.frame(); got != (mygo.Rectangle{X: 623, Y: 0, Width: 481, Height: 192}) {
		t.Fatalf("the window: %+v", got)
	}
	if _, ok := r.s.region(); ok {
		t.Fatal("at the notch's own size it takes the mouse")
	}
	if r.s.contains(863, 10) {
		t.Fatal("at the notch's own size the pointer is on it")
	}
	r.pass(time.Second, false)
	if got, ok := r.s.region(); !ok || got != (mygo.Rectangle{X: 45, Y: 0, Width: 391, Height: 136}) {
		t.Fatalf("open: the mouse goes to %+v, %v", got, ok)
	}
	for _, c := range []struct {
		x, y int
		want bool
	}{{668, 0, true}, {667, 0, false}, {1058, 135, true}, {1059, 135, false}, {863, 136, false}} {
		if got := r.s.contains(c.x, c.y); got != c.want {
			t.Errorf("open: the pointer at %d,%d is on it: %v", c.x, c.y, got)
		}
	}
	r.pass(10*time.Second, false)
	if got, ok := r.s.region(); !ok || got != (mygo.Rectangle{X: 86, Y: 0, Width: 309, Height: 32}) {
		t.Fatalf("folded: the mouse goes to %+v, %v", got, ok)
	}
	for _, c := range []struct {
		x, y int
		want bool
	}{{709, 31, true}, {708, 31, false}, {1017, 0, true}, {1018, 0, false}, {863, 32, false}} {
		if got := r.s.contains(c.x, c.y); got != c.want {
			t.Errorf("folded: the pointer at %d,%d is on it: %v", c.x, c.y, got)
		}
	}
}
