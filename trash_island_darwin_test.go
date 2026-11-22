//go:build darwin

package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
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

// islandView is an island that is out and armed, with a tester on its
// window's content.
func islandView(t *testing.T, lang string) (*islandRun, *ui.Tester) {
	r := newIslandRun(t)
	r.p.text = func(key string, vars map[string]string) string { return nativeTextFor(lang, key, vars) }
	r.pass(1200*time.Millisecond, true)
	f := r.s.frame()
	return r, ui.NewTester(r.s.view, f.Width, f.Height)
}

// TestTrashIslandViewAsksEmptiesAndReports walks the reminder through its
// phases on the island, by the clicks a user makes.
func TestTrashIslandViewAsksEmptiesAndReports(t *testing.T) {
	r, tt := islandView(t, "zh")
	started, cancelled, closed := 0, 0, 0
	r.p.start, r.p.cancel, r.p.close = func() { started++ }, func() { cancelled++ }, func() { closed++ }
	for _, want := range []string{"废纸篓已占用 3.0 GB", "清空将永久删除其中的内容，无法恢复。", "暂不清理", "清空（永久删除）"} {
		if !tt.HasText(want) {
			t.Fatalf("the question lacks %q: %q", want, tt.Texts())
		}
	}
	if started != 0 {
		t.Fatal("drawing it started the cleanup")
	}
	if err := tt.Click("清空（永久删除）"); err != nil {
		t.Fatal(err)
	}
	if started != 1 || r.p.phase != "cleaning" {
		t.Fatalf("after the click: started %d times, phase %q", started, r.p.phase)
	}
	r.p.freed = gb
	tt.Frame()
	for _, want := range []string{"正在清理，请稍候…", "已释放 1.0 GB", "33%", "取消"} {
		if !tt.HasText(want) {
			t.Fatalf("cleaning lacks %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 || r.p.phase != "stopping" {
		t.Fatalf("after cancel: cancelled %d times, phase %q", cancelled, r.p.phase)
	}
	tt.Frame()
	if !tt.HasText("正在停止…") {
		t.Fatalf("stopping: %q", tt.Texts())
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 {
		t.Fatal("cancel taken twice")
	}
	r.p.finish(CleanResult{Freed: gb}, true)
	r.pass(0, true)
	tt.Frame()
	for _, want := range []string{"已停止清理", "已释放 1.0 GB", "已删除的内容无法恢复；新增、变化或无法删除的内容已保留在废纸篓中。", "关闭"} {
		if !tt.HasText(want) {
			t.Fatalf("the stopped result lacks %q: %q", want, tt.Texts())
		}
	}
	if err := tt.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("closed %d times", closed)
	}
}

// TestTrashIslandViewLaterDismisses: the other answer to the question.
func TestTrashIslandViewLaterDismisses(t *testing.T) {
	r, tt := islandView(t, "zh")
	closed, started := 0, 0
	r.p.close, r.p.start = func() { closed++ }, func() { started++ }
	if err := tt.Click("暂不清理"); err != nil {
		t.Fatal(err)
	}
	if closed != 1 || started != 0 || r.p.phase != "" {
		t.Fatalf("closed %d times, started %d times, phase %q", closed, started, r.p.phase)
	}
}

// TestTrashIslandViewDoesNotEmptyUntilArmed: a click on the button that
// empties, in the second after the island came out by itself, deletes
// nothing; the same click a moment later does.
func TestTrashIslandViewDoesNotEmptyUntilArmed(t *testing.T) {
	r := newIslandRun(t)
	started := 0
	r.p.start = func() { started++ }
	r.pass(900*time.Millisecond, true)
	f := r.s.frame()
	tt := ui.NewTester(r.s.view, f.Width, f.Height)
	if err := tt.Click("清空（永久删除）"); err != nil {
		t.Fatal(err)
	}
	if started != 0 || r.p.phase != "" {
		t.Fatalf("0.9 s after it showed: started %d times, phase %q", started, r.p.phase)
	}
	r.pass(300*time.Millisecond, true)
	tt.Frame()
	if err := tt.Click("清空（永久删除）"); err != nil {
		t.Fatal(err)
	}
	if started != 1 || r.p.phase != "cleaning" {
		t.Fatalf("1.2 s after it showed: started %d times, phase %q", started, r.p.phase)
	}
}

// TestTrashIslandViewFoldedShowsTheStripAlone: folded, the island shows the
// size, the progress or the outcome beside the notch, and nothing to click.
func TestTrashIslandViewFoldedShowsTheStripAlone(t *testing.T) {
	for _, c := range []struct {
		phase string
		freed int64
		want  string
	}{{"", 0, "3.0 GB"}, {"cleaning", gb + gb/2, "50%"}, {"stopping", gb + gb/2, "50%"}, {"partial", gb, "!"}, {"cancelled", gb, "!"}} {
		r := newIslandRun(t)
		r.pass(11*time.Second, false)
		r.p.phase, r.p.freed, r.s.seen = c.phase, c.freed, c.phase
		r.want(c.phase+": folded", islandCompact, 0)
		f := r.s.frame()
		tt := ui.NewTester(r.s.view, f.Width, f.Height)
		if got := tt.Texts(); len(got) != 1 || got[0] != c.want {
			t.Errorf("%q folded shows %q, want %q alone", c.phase, got, c.want)
		}
	}
}

// TestTrashIslandViewFits: in both languages, what each phase shows stays
// inside the open island, clear of its rounded bottom and its sides.
func TestTrashIslandViewFits(t *testing.T) {
	last := map[string]string{
		"":          "trash.popup.empty",
		"cleaning":  "trash.popup.cancel",
		"stopping":  "trash.popup.cancel",
		"done":      "trash.popup.freed",
		"partial":   "trash.popup.close",
		"cancelled": "trash.popup.close",
	}
	for _, lang := range []string{"zh", "en"} {
		for _, phase := range []string{"", "cleaning", "stopping", "done", "partial", "cancelled"} {
			r, _ := islandView(t, lang)
			r.p.phase, r.p.freed, r.s.seen = phase, gb, phase
			r.s.shape = islandOpen
			f := r.s.frame()
			tt := ui.NewTester(r.s.view, f.Width, f.Height)
			w, h := r.s.size()
			left := float32(f.Width-w) / 2
			text := r.p.text(last[phase], map[string]string{"size": growthSize(gb)})
			at, ok := tt.Find(text)
			if !ok {
				t.Fatalf("%s %q: %q is not shown: %q", lang, phase, text, tt.Texts())
			}
			if bottom := at.Y + at.H; bottom > float32(h-12) {
				t.Errorf("%s %q: %q reaches %.1f, into the last 12 points of a height of %d", lang, phase, text, bottom, h)
			}
			for _, s := range tt.Texts() {
				r, ok := tt.Find(s)
				if !ok {
					continue
				}
				if r.X < left+12 || r.X+r.W > left+float32(w)-12 || r.Y+r.H > float32(h)-8 {
					t.Errorf("%s %q: %q at %+v is outside the island, %d by %d from x %.1f", lang, phase, s, r, w, h, left)
				}
			}
		}
	}
}

// TestTrashIslandPreviews saves a picture of each phase and shape, in both
// languages, where CLEANZO_TRASH_PREVIEWS names a folder.
func TestTrashIslandPreviews(t *testing.T) {
	dir := os.Getenv("CLEANZO_TRASH_PREVIEWS")
	if dir == "" {
		t.Skip("set CLEANZO_TRASH_PREVIEWS to save renders of the island")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"zh", "en"} {
		for _, phase := range []string{"", "cleaning", "done", "partial", "cancelled"} {
			for _, shape := range []islandShape{islandOpen, islandCompact} {
				r, _ := islandView(t, lang)
				r.p.phase, r.p.freed, r.s.seen, r.s.shape = phase, gb, phase, shape
				name := phase
				if name == "" {
					name = "confirm"
				}
				form := "open"
				if shape == islandCompact {
					form = "folded"
				}
				f := r.s.frame()
				out, err := os.Create(filepath.Join(dir, "island-"+lang+"-"+name+"-"+form+".png"))
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(out, ui.Render(r.s.view, f.Width, f.Height, 2))
				if closeErr := out.Close(); err == nil {
					err = closeErr
				}
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
