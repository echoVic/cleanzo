package main

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

func TestGrowthNextRun(t *testing.T) {
	cases := []struct {
		name                string
		last, launched, now time.Time
		justEnabled         bool
		want                time.Time
	}{
		{"enabling without records measures now", time.Time{}, t0, t0, true, t0},
		{"enabling after a recent record waits for its day", t0.Add(-2 * time.Hour), t0, t0, true, t0.Add(22 * time.Hour)},
		{"enabling after a record over 20 hours old measures now", t0.Add(-21 * time.Hour), t0, t0, true, t0},
		{"launch after 20 hours waits five minutes", t0.Add(-21 * time.Hour), t0, t0, false, t0.Add(5 * time.Minute)},
		{"later than five minutes after launch goes now", t0.Add(-30 * time.Hour), t0, t0.Add(time.Hour), false, t0.Add(time.Hour)},
		{"otherwise 24 hours after the last record", t0.Add(-3 * time.Hour), t0, t0, false, t0.Add(21 * time.Hour)},
		{"a record from the future (a wrong clock) is as good as none", t0.Add(2 * time.Hour), t0.Add(-time.Hour), t0, false, t0},
		// The record is 21 hours old, but it was taken during this run, three
		// days after launch: it was not stale at launch, so the day goes on.
		{"a record taken during this run keeps the 24 hour cadence", t0.Add(-21 * time.Hour), t0.Add(-72 * time.Hour), t0, false, t0.Add(3 * time.Hour)},
	}
	for _, c := range cases {
		if got := growthNextRun(c.last, c.launched, c.now, c.justEnabled); !got.Equal(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGrowthDeferral(t *testing.T) {
	if d := growthDeferral(true, 19, 0); d != 30*time.Minute {
		t.Errorf("low battery: %v", d)
	}
	if d := growthDeferral(true, 20, 0); d != 0 {
		t.Errorf("battery at 20%%, not below it: %v", d)
	}
	if d := growthDeferral(false, 5, 1); d != 10*time.Minute {
		t.Errorf("user scan: %v", d)
	}
	if d := growthDeferral(false, 100, 0); d != 0 {
		t.Errorf("plugged in, idle: %v", d)
	}
}

// stubGrowth replaces the monitor's seams for one test: a clock under the
// test's control, ToDesk measuring as size, posts recorded in sent, and a
// Mac that is plugged in (the real power source waits for the app's main
// thread, which a test does not have).
func stubGrowth(t *testing.T, now *time.Time, size *int64, sent *[]GrowthAlert) {
	t.Helper()
	setHome(t, t.TempDir())
	// These tests isolate growth/reading residency from the trash reminder.
	if err := saveTrashReminder(trashReminderDir(), TrashReminderConfig{ThresholdGB: 1}); err != nil {
		t.Fatal(err)
	}
	n, m, notify, changed, owner, power := growthNow, growthMeasure, growthNotify, growthChanged, growthOwnerOf, growthPower
	t.Cleanup(func() {
		growthNow, growthMeasure, growthNotify, growthChanged, growthOwnerOf, growthPower = n, m, notify, changed, owner, power
	})
	growthNow = func() time.Time { return *now }
	growthMeasure = func(ctx context.Context, prev *growthSnapshot, _ bool) growthSnapshot {
		return growthSnapshot{At: now.Unix(), FDA: true, Entries: []growthEntry{done(todesk, *size)}}
	}
	growthNotify = func(a GrowthAlert) error { *sent = append(*sent, a); return nil }
	growthChanged = func(GrowthStatus) {}
	growthOwnerOf = func() (func(growthEntry) growthOwner, time.Time) { return byBase, time.Time{} }
	growthPower = func(context.Context) (bool, int) { return false, 100 }
}

func TestMeasureOnceAlertsOnlyWhileMonitoring(t *testing.T) {
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	m := &growthMonitor{launched: t0, notifs: "unknown"}
	m.measureOnce(context.Background())
	now, size = t0.Add(24*time.Hour), 14*gb
	m.measureOnce(context.Background())
	if len(sent) != 0 || len(loadGrowthSnapshots(growthDir())) != 2 {
		t.Fatalf("monitoring off: sent %+v", sent)
	}
	c := defaultGrowthConfig()
	c.Enabled = true
	if err := saveGrowthConfig(growthDir(), c); err != nil {
		t.Fatal(err)
	}
	now, size = t0.Add(48*time.Hour), 18*gb
	m.measureOnce(context.Background())
	m.posting.Wait() // posts go out on a goroutine of their own
	if len(sent) != 1 || sent[0].Key != "app:todesk" || sent[0].Bytes != 4*gb || sent[0].Window != "day" {
		t.Fatalf("sent = %+v", sent)
	}
	if recent := loadGrowthNotified(growthDir()).recent(now); len(recent) != 1 {
		t.Fatalf("recent = %+v", recent)
	}
}

func TestStoppedRunSavesNothing(t *testing.T) {
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	(&growthMonitor{}).measureOnce(ctx)
	if got := loadGrowthSnapshots(growthDir()); len(got) != 0 {
		t.Fatalf("records = %+v", got)
	}
}

func TestUserScanCancelsOnlyBackgroundRuns(t *testing.T) {
	old := growth
	t.Cleanup(func() { growth = old })
	ctx, cancel := context.WithCancel(context.Background())
	growth = &growthMonitor{running: true, cancel: cancel}
	userScan()()
	if ctx.Err() == nil {
		t.Fatal("a background run must stop for a user scan")
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	growth = &growthMonitor{running: true, manual: true, cancel: cancel2}
	userScan()()
	if ctx2.Err() != nil {
		t.Fatal("Measure Now keeps going")
	}
}

// deferUserScan reports whether stmt is `defer userScan()()`.
func deferUserScan(stmt ast.Stmt) bool {
	def, ok := stmt.(*ast.DeferStmt)
	if !ok {
		return false
	}
	inner, ok := def.Call.Fun.(*ast.CallExpr)
	if !ok || len(def.Call.Args) != 0 || len(inner.Args) != 0 {
		return false
	}
	id, ok := inner.Fun.(*ast.Ident)
	return ok && id.Name == "userScan"
}

// TestUserScanHooksComeFirst pins that the four scans and cleans a user
// starts mark themselves with `defer userScan()()` before anything else, so
// background measuring stops for them and waits (spec §四).
func TestUserScanHooksComeFirst(t *testing.T) {
	for file, names := range map[string][]string{
		"junk.go": {"ScanJunk", "CleanJunk"},
		"main.go": {"FullScan", "LargeScan"},
	} {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !slices.Contains(names, fn.Name.Name) {
				continue
			}
			if recv, ok := fn.Recv.List[0].Type.(*ast.Ident); !ok || recv.Name != "Cleaner" {
				continue
			}
			found[fn.Name.Name] = true
			if len(fn.Body.List) == 0 || !deferUserScan(fn.Body.List[0]) {
				t.Errorf("%s: %s must start with defer userScan()()", file, fn.Name.Name)
			}
		}
		for _, name := range names {
			if !found[name] {
				t.Errorf("%s: no method Cleaner.%s", file, name)
			}
		}
	}
}

// TestGrowthDeferralHoldsATick covers tick's use of the power source and the
// scans: a due measurement waits, and the battery is asked with a deadline,
// since asking blocks without the app's main thread.
func TestGrowthDeferralHoldsATick(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	m := &growthMonitor{launched: t0, notifs: "unknown"}

	asked := false
	growthPower = func(context.Context) (bool, int) { asked = true; return true, 19 }
	m.tick(true)
	if asked || m.status().NextAt != 0 {
		t.Fatalf("monitoring is off: asked %v, next %d", asked, m.status().NextAt)
	}

	c := defaultGrowthConfig()
	c.Enabled = true
	if err := saveGrowthConfig(growthDir(), c); err != nil {
		t.Fatal(err)
	}
	var left time.Duration
	var hasDeadline bool
	growthPower = func(ctx context.Context) (bool, int) {
		deadline, ok := ctx.Deadline()
		hasDeadline, left = ok, time.Until(deadline)
		return true, 19
	}
	m.tick(true)
	if !hasDeadline || left > 30*time.Second || left < 10*time.Second {
		t.Errorf("the battery is asked with a 30 second deadline: set %v, left %v", hasDeadline, left)
	}
	// A measurement held back stays due: the plan keeps the time it fell due.
	if got, want := m.status().NextAt, now.Unix(); got != want {
		t.Errorf("on 19%% battery: next %d, want the time it fell due, %d", got, want)
	}

	// The ticks that follow ask again. While they keep waiting, for the battery
	// or for a user scan, the plan does not move and nobody is told.
	var told int
	growthChanged = func(GrowthStatus) { told++ }
	now = now.Add(15 * time.Minute)
	m.tick(false)
	growthPower = func(context.Context) (bool, int) { return false, 100 }
	release := userScan()
	now = now.Add(15 * time.Minute)
	m.tick(false)
	release()
	if got, want := m.status().NextAt, t0.Unix(); got != want || told != 0 {
		t.Errorf("ticks that keep waiting: next %d, want %d; the page was told %d times", got, want, told)
	}
	if m.status().Running || len(loadGrowthSnapshots(growthDir())) != 0 {
		t.Errorf("a deferred tick must measure nothing: %+v", m.status())
	}
}

// TestSaveGrowthConfigWaitsForTheConfigLock pins that saving the settings
// holds growthConfigMu, which the menu bar's "turn off" takes too, so that
// neither loses the other's change.
func TestSaveGrowthConfigWaitsForTheConfigLock(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform: saving is refused before the lock")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	c := GrowthConfigPatch{DailyGB: new(5.0)} // monitoring stays off, so nothing measures
	growthConfigMu.Lock()
	finished := make(chan error, 1)
	go func() { finished <- Cleaner{}.SaveGrowthConfig(c) }()
	select {
	case err := <-finished:
		growthConfigMu.Unlock()
		t.Fatalf("SaveGrowthConfig did not wait for the config lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if got := loadGrowthConfig(growthDir()).DailyGB; got != 2 {
		t.Errorf("saved while the lock was held: dailyGB %v", got)
	}
	growthConfigMu.Unlock()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SaveGrowthConfig never finished")
	}
	if got := loadGrowthConfig(growthDir()).DailyGB; got != 5 {
		t.Errorf("dailyGB = %v, want 5", got)
	}
}

func TestNativeTexts(t *testing.T) {
	var m map[string]map[string]string
	if err := json.Unmarshal(nativeMessages, &m); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"zh", "en"}, {"en", "zh"}} {
		for key := range m[pair[0]] {
			if (strings.HasPrefix(key, "growth.") || strings.HasPrefix(key, "menubar.")) && m[pair[1]][key] == "" {
				t.Errorf("%s has a %s text but no %s text", key, pair[0], pair[1])
			}
		}
	}
	if got := nativeTextFor("zh-Hans", "growth.notify.day", map[string]string{"name": "ToDesk", "size": "5.6 GB"}); got != "ToDesk 一天涨了 5.6 GB" {
		t.Errorf("zh = %q", got)
	}
	if got := nativeTextFor("en-US", "growth.notify.week", map[string]string{"name": "ToDesk", "size": "12 GB", "days": "5"}); got != "ToDesk grew 12 GB in 5 days" {
		t.Errorf("en = %q", got)
	}
}

func TestGrowthSizeMatchesThePage(t *testing.T) {
	// 1280 B is 1.25 KB and 102912 B is 100.5 KB: halves round up, as toFixed does.
	for n, want := range map[int64]string{1023: "1023 B", 1280: "1.3 KB", 102912: "101 KB", 512 << 20: "512 MB", 6012954214: "5.6 GB", 152 * gb: "152 GB", 2 << 40: "2.0 TB"} {
		if got := growthSize(n); got != want {
			t.Errorf("growthSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestGrowthAlertTexts(t *testing.T) {
	setHome(t, t.TempDir())
	folder := filepath.Join(homeDir(), "Library", "Caches", "ToDesk")
	a := GrowthAlert{Name: "ToDesk", Window: "day", Bytes: 6012954214, Folder: folder, Total: 152 * gb}
	if got := growthTitle("en", a); got != "ToDesk grew 5.6 GB in a day" {
		t.Errorf("title = %q", got)
	}
	if got := growthBody("en", a); got != "Mostly in "+filepath.Join("~", "Library", "Caches", "ToDesk")+"; 152 GB in all now" {
		t.Errorf("body = %q", got)
	}
	a.Folder = filepath.Join(t.TempDir(), "dumps") // outside the home folder: shown in full
	if got := growthBody("en", a); got != "Mostly in "+a.Folder+"; 152 GB in all now" {
		t.Errorf("outside the home folder: body = %q", got)
	}
	week := GrowthAlert{Name: "ToDesk", Window: "week", Days: 5, Bytes: 12 * gb, AtLeast: true}
	if got := growthTitle("zh-Hans", week); got != "ToDesk 5 天至少涨了 12.0 GB" {
		t.Errorf("week title = %q", got)
	}
}

// A value that looks like a placeholder is text, whatever the order in which
// the placeholders are filled.
func TestNativeTextsFillEachPlaceholderOnce(t *testing.T) {
	vars := map[string]string{"name": "{size}", "size": "5.6 GB"}
	for i := 0; i < 64; i++ {
		if got := nativeTextFor("en", "growth.notify.day", vars); got != "{size} grew 5.6 GB in a day" {
			t.Fatalf("round %d: %q", i, got)
		}
	}
}

// Posting can wait for the user to answer the permission prompt, and the app
// can be quit meanwhile: the log of what was picked is saved before the first
// post, or the same alerts would go out again.
func TestMeasureOnceSavesTheLogBeforePosting(t *testing.T) {
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	c := defaultGrowthConfig()
	c.Enabled = true
	if err := saveGrowthConfig(growthDir(), c); err != nil {
		t.Fatal(err)
	}
	m := &growthMonitor{launched: t0, notifs: "unknown"}
	m.measureOnce(context.Background()) // the baseline
	var logged []string
	growthNotify = func(a GrowthAlert) error {
		logged = append(logged, loadGrowthNotified(growthDir()).Day[a.Key])
		return nil
	}
	now, size = t0.Add(24*time.Hour), 14*gb
	m.measureOnce(context.Background())
	m.posting.Wait()
	if want := []string{now.Format(time.DateOnly)}; !slices.Equal(logged, want) {
		t.Fatalf("what the log said when each alert was posted: %q, want %q", logged, want)
	}
}

// growthWatch collects the monitor's updates, so that a test waits for a run
// on the run's own word and not on a sleep: run tells at once that it started,
// and again when it ended. With monitoring on, a run also tells that the plan
// moved just before it ends, so a test that waits for an end keeps monitoring
// off by then.
type growthWatch struct {
	t       *testing.T
	updates chan GrowthStatus
}

// watchGrowth replaces growthChanged. Call it after stubGrowth, which puts the
// original back.
func watchGrowth(t *testing.T) growthWatch {
	t.Helper()
	w := growthWatch{t: t, updates: make(chan GrowthStatus, 32)}
	growthChanged = func(s GrowthStatus) { w.updates <- s }
	return w
}

// started reports, without waiting, whether a run began since the last call.
func (w growthWatch) started() bool {
	for {
		select {
		case s := <-w.updates:
			if s.Running {
				return true
			}
		default:
			return false
		}
	}
}

// ended waits for the update that says the run is over.
func (w growthWatch) ended() {
	w.t.Helper()
	for {
		select {
		case s := <-w.updates:
			if !s.Running {
				return
			}
		case <-time.After(10 * time.Second):
			w.t.Fatal("the run never ended")
		}
	}
}

// A run is one at a time, whoever asked, and it leaves the monitor ready for
// the next.
func TestGrowthRunIsOneAtATime(t *testing.T) {
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	w := watchGrowth(t)
	atWork, proceed := make(chan struct{}), make(chan struct{})
	growthMeasure = func(ctx context.Context, prev *growthSnapshot, _ bool) growthSnapshot {
		atWork <- struct{}{}
		<-proceed
		return growthSnapshot{At: now.Unix(), FDA: true, Entries: []growthEntry{done(todesk, size)}}
	}
	m := &growthMonitor{launched: t0, notifs: "unknown"}
	state := func() (running, manual, hasCancel bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.running, m.manual, m.cancel != nil
	}
	for _, manual := range []bool{true, false} {
		startedAt := now
		if !m.run(manual) {
			t.Fatalf("run(%v) did not start", manual)
		}
		<-atWork
		if running, isManual, hasCancel := state(); !running || isManual != manual || !hasCancel {
			t.Errorf("run(%v) under way: running %v, manual %v, cancel set %v", manual, running, isManual, hasCancel)
		}
		if m.run(false) || m.run(true) {
			t.Errorf("run(%v) under way, and another one started", manual)
		}
		proceed <- struct{}{}
		if !w.started() {
			t.Errorf("run(%v) did not tell that it started", manual)
		}
		w.ended()
		if running, isManual, hasCancel := state(); running || isManual || hasCancel {
			t.Errorf("run(%v) over: running %v, manual %v, cancel set %v", manual, running, isManual, hasCancel)
		}
		// Only a background run counts as an attempt that automatic ones keep clear of.
		if manual && !m.lastAttempt.IsZero() || !manual && !m.lastAttempt.Equal(startedAt) {
			t.Errorf("run(%v): lastAttempt = %v, started at %v", manual, m.lastAttempt, startedAt)
		}
		now = now.Add(time.Hour) // the next record gets a name of its own
	}
	if got := len(loadGrowthSnapshots(growthDir())); got != 2 {
		t.Errorf("records = %d, want one per run", got)
	}
}

// Measure Now runs at normal priority; an automatic run in the background.
func TestManualRunsMeasureAtNormalPriority(t *testing.T) {
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	w := watchGrowth(t)
	var got []bool
	growthMeasure = func(ctx context.Context, prev *growthSnapshot, background bool) growthSnapshot {
		got = append(got, background)
		return growthSnapshot{At: now.Unix(), FDA: true, Entries: []growthEntry{done(todesk, size)}}
	}
	m := &growthMonitor{launched: t0, notifs: "unknown"}
	for _, manual := range []bool{true, false} {
		if !m.run(manual) {
			t.Fatalf("run(%v) did not start", manual)
		}
		w.started()
		w.ended()
		now = now.Add(time.Hour)
	}
	if want := []bool{false, true}; !slices.Equal(got, want) {
		t.Fatalf("background per run (manual, then automatic) = %v, want %v", got, want)
	}
}

// A user scan can start between tick looking and run starting, when there is
// no run yet for the scan to stop: run looks again, for background runs only.
func TestGrowthBackgroundRunWaitsForUserScans(t *testing.T) {
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	w := watchGrowth(t)
	m := &growthMonitor{launched: t0, notifs: "unknown"}
	release := userScan()
	if m.run(false) {
		w.started()
		w.ended() // let the stray run finish before the test ends
		release()
		t.Fatal("a background run started while the user scans")
	}
	if w.started() || m.status().Running || !m.lastAttempt.IsZero() {
		t.Error("the refused run left a trace")
	}
	if !m.run(true) {
		t.Fatal("Measure Now is not held back by a scan")
	}
	w.started()
	w.ended()
	release()
	now = now.Add(time.Hour)
	if !m.run(false) {
		t.Fatal("once the scan is over, a background run starts")
	}
	w.started()
	w.ended()
	if got := len(loadGrowthSnapshots(growthDir())); got != 2 {
		t.Errorf("records = %d, want 2", got)
	}
}

// A run that leaves no record, here because the save fails, must not be tried
// again at every tick: automatic attempts keep an hour apart. Turning
// monitoring on is not automatic and goes at once.
func TestGrowthAutomaticAttemptsKeepAnHourApart(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0.Add(time.Hour), 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	w := watchGrowth(t)
	// records is a file, so every save fails and no attempt leaves a record.
	if err := os.MkdirAll(growthDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(growthDir(), "records"), []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	measured := 0
	growthMeasure = func(ctx context.Context, prev *growthSnapshot, _ bool) growthSnapshot {
		measured++
		// Monitoring goes off during the run, so that the run tells its end once.
		_ = saveGrowthConfig(growthDir(), defaultGrowthConfig())
		return growthSnapshot{At: now.Unix(), FDA: true, Entries: []growthEntry{done(todesk, size)}}
	}
	m := &growthMonitor{launched: t0, notifs: "unknown"}
	attempt := func(justEnabled bool) bool {
		t.Helper()
		on := defaultGrowthConfig()
		on.Enabled = true
		if err := saveGrowthConfig(growthDir(), on); err != nil {
			t.Fatal(err)
		}
		m.tick(justEnabled)
		if !w.started() {
			return false
		}
		w.ended()
		return true
	}
	if !attempt(false) {
		t.Fatal("the first automatic attempt must measure")
	}
	now = now.Add(15 * time.Minute) // the next tick
	if attempt(false) {
		t.Error("an automatic attempt 15 minutes after the last measured again")
	}
	now = now.Add(46 * time.Minute) // an hour and a minute after the first
	if !attempt(false) {
		t.Error("an hour after the last attempt, an automatic attempt must measure")
	}
	now = now.Add(5 * time.Minute)
	if !attempt(true) {
		t.Error("turning monitoring on must measure at once, whatever came before")
	}
	if measured != 3 {
		t.Errorf("measured %d times, want 3", measured)
	}
}

// Turning monitoring off clears the plan for the next measurement at once,
// and the page hears about the cleared plan.
func TestSaveGrowthConfigClearsThePlanWhenOff(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	old := growth
	t.Cleanup(func() { growth = old })
	planned := t0.Add(5 * time.Hour)
	growth = &growthMonitor{launched: t0, notifs: "unknown", nextAt: planned}
	var told []GrowthStatus
	growthChanged = func(s GrowthStatus) { told = append(told, s) }

	on := defaultGrowthConfig()
	on.Enabled = true
	// Monitoring is on already, so saving it again starts no measurement.
	if err := saveGrowthConfig(growthDir(), on); err != nil {
		t.Fatal(err)
	}
	if err := (Cleaner{}).SaveGrowthConfig(GrowthConfigPatch{Enabled: new(true)}); err != nil {
		t.Fatal(err)
	}
	if got := growth.status().NextAt; got != planned.Unix() {
		t.Errorf("a save that keeps monitoring on moved the plan to %d", got)
	}
	if err := (Cleaner{}).SaveGrowthConfig(GrowthConfigPatch{Enabled: new(false)}); err != nil {
		t.Fatal(err)
	}
	if got := growth.status().NextAt; got != 0 {
		t.Errorf("monitoring is off but the plan says %d", got)
	}
	if len(told) == 0 || told[len(told)-1].NextAt != 0 {
		t.Errorf("the page was told %+v, want the cleared plan last", told)
	}
}

// Where the monitor does not run (Windows), its settings are not kept either.
func TestGrowthSettingsAreRefusedWhereUnsupported(t *testing.T) {
	if growthSupported() {
		t.Skip("this platform has the growth monitor")
	}
	setHome(t, t.TempDir())
	if err := (Cleaner{}).SaveGrowthConfig(GrowthConfigPatch{Enabled: new(true)}); err == nil {
		t.Error("SaveGrowthConfig must refuse")
	}
	if _, err := os.Stat(growthDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the growth folder was made: %v", err)
	}
	if err := (Cleaner{}).GrowthMeasureNow(); err == nil {
		t.Error("GrowthMeasureNow must refuse")
	}
}

// The default of the seam is set in init: it cannot be part of the
// declaration, which would make a cycle with the menu bar's menu.
func TestGrowthChangedHasADefault(t *testing.T) {
	if growthChanged == nil {
		t.Fatal("growthChanged has no default")
	}
	growthChanged(GrowthStatus{}) // no page is open: it reaches nobody
}

// nextRun is growthNextRun held back by the hour between automatic attempts.
func TestGrowthNextRunKeepsAttemptsAnHourApart(t *testing.T) {
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent) // a home of its own, without records
	m := &growthMonitor{launched: t0.Add(-48 * time.Hour)}
	check := func(what string, justEnabled bool, want time.Time) {
		t.Helper()
		if got := m.nextRun(now, justEnabled); !got.Equal(want) {
			t.Errorf("%s: got %v, want %v", what, got, want)
		}
	}
	check("no attempt yet: due now", false, now)
	m.lastAttempt = now.Add(-20 * time.Minute)
	check("an attempt 20 minutes ago: wait out the hour", false, now.Add(40*time.Minute))
	check("turning monitoring on does not wait", true, now)
	m.lastAttempt = now.Add(-2 * time.Hour)
	check("an attempt 2 hours ago: due now", false, now)
	m.lastAttempt = now.Add(time.Hour)
	check("an attempt dated in the future (a clock set back) holds nothing", false, now)
}

// A record dated after now, which a clock set back leaves behind, is not the
// last record a run starts from.
func TestMeasureOnceLeavesOutRecordsFromTheFuture(t *testing.T) {
	now, size := t0, 14*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	for _, s := range []growthSnapshot{at(-24*time.Hour, true, done(todesk, 10*gb)), at(5*time.Hour, true, done(todesk, 99*gb))} {
		if err := saveGrowthSnapshot(growthDir(), s, now); err != nil {
			t.Fatal(err)
		}
	}
	var prevAt int64
	growthMeasure = func(ctx context.Context, prev *growthSnapshot, _ bool) growthSnapshot {
		if prev != nil {
			prevAt = prev.At
		}
		return growthSnapshot{At: now.Unix(), FDA: true, Entries: []growthEntry{done(todesk, size)}}
	}
	(&growthMonitor{launched: t0, notifs: "unknown"}).measureOnce(context.Background())
	if want := t0.Add(-24 * time.Hour).Unix(); prevAt != want {
		t.Fatalf("the run started from the record at %d, want yesterday's, %d", prevAt, want)
	}
}

// The first notification can wait until the user answers macOS's permission
// prompt. The run does not wait for it: it ends, and says so, while the post
// still waits. What the post learns about permission reaches the page.
func TestGrowthRunEndsWhileANotificationWaits(t *testing.T) {
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	c := defaultGrowthConfig()
	c.Enabled = true
	if err := saveGrowthConfig(growthDir(), c); err != nil {
		t.Fatal(err)
	}
	m := &growthMonitor{launched: t0, notifs: "unknown"}
	m.measureOnce(context.Background()) // the baseline
	posting, answer := make(chan GrowthAlert, 1), make(chan error)
	growthNotify = func(a GrowthAlert) error { posting <- a; return <-answer }
	w := watchGrowth(t)
	now, size = t0.Add(24*time.Hour), 14*gb
	if !m.run(true) {
		t.Fatal("the run did not start")
	}
	select {
	case a := <-posting:
		if a.Key != "app:todesk" {
			t.Fatalf("posted %+v", a)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nothing was posted")
	}
	w.started()
	w.ended()
	if m.status().Running {
		t.Fatal("the run still counts as running while its notification waits")
	}
	answer <- mygo.ErrNotificationsDenied
	m.posting.Wait()
	for told := false; !told; {
		select {
		case s := <-w.updates:
			told = s.Notifications == "denied"
		case <-time.After(10 * time.Second):
			t.Fatal("the page was not told that notifications are denied")
		}
	}
	if got := m.status().Notifications; got != "denied" {
		t.Errorf("notifications %q, want denied", got)
	}
}

// Turning monitoring on posts one notification that says so, through the
// notify seam: macOS then asks for permission while the user is at the page,
// not during a run nobody watches. Saving it on again, or another change,
// posts nothing. What the post learns about permission is kept.
func TestTurningMonitoringOnPostsOneConfirmation(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	old := growth
	t.Cleanup(func() { growth = old })
	growth = &growthMonitor{launched: t0, notifs: "unknown"}
	updates := make(chan GrowthStatus, 16)
	growthChanged = func(s GrowthStatus) { updates <- s }
	growthPower = func(context.Context) (bool, int) { return true, 19 } // the baseline waits for the battery
	posts := make(chan GrowthAlert, 4)
	growthNotify = func(a GrowthAlert) error { posts <- a; return mygo.ErrNotificationsDenied }

	for _, p := range []GrowthConfigPatch{{Enabled: new(true), Asked: new(true)}, {Enabled: new(true)}, {DailyGB: new(5.0)}} {
		if err := (Cleaner{}).SaveGrowthConfig(p); err != nil {
			t.Fatal(err)
		}
	}
	// Three saves, the baseline's tick planning its wait, and the post telling
	// what it learned: five updates, after which nothing is left running.
	for range 5 {
		select {
		case <-updates:
		case <-time.After(10 * time.Second):
			t.Fatal("an update never came")
		}
	}
	growth.posting.Wait()
	close(posts)
	var got []GrowthAlert
	for a := range posts {
		got = append(got, a)
	}
	if len(got) != 1 || got[0] != growthOnNotice {
		t.Fatalf("posted %+v, want the confirmation once", got)
	}
	if s := growth.status().Notifications; s != "denied" {
		t.Errorf("notifications %q, want what the confirmation learned", s)
	}
}

// The confirmation's texts, in both languages.
func TestGrowthOnNoticeTexts(t *testing.T) {
	for locale, want := range map[string][2]string{
		"zh-Hans": {"占用增长监控已打开", "某个 App 涨得异常时，会在这里提醒你。"},
		"en-US":   {"Storage growth monitoring is on", "You'll be notified here when an app grows fast."},
	} {
		if title, body := growthNoticeText(locale, growthOnNotice); title != want[0] || body != want[1] {
			t.Errorf("%s: %q / %q, want %q / %q", locale, title, body, want[0], want[1])
		}
	}
	a := GrowthAlert{Name: "ToDesk", Window: "day", Bytes: 6012954214, Folder: "/x", Total: 152 * gb}
	if title, body := growthNoticeText("en", a); title != growthTitle("en", a) || body != growthBody("en", a) {
		t.Errorf("an alert's texts: %q / %q", title, body)
	}
}

// Turning monitoring off, from Settings or from the menu bar, stops a
// background measurement under way, which then saves nothing.
func TestTurningMonitoringOffStopsABackgroundRun(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	for name, off := range map[string]func(){
		"Settings": func() {
			if err := (Cleaner{}).SaveGrowthConfig(GrowthConfigPatch{Enabled: new(false)}); err != nil {
				t.Error(err)
			}
		},
		"the menu bar": turnOffGrowth,
	} {
		t.Run(name, func(t *testing.T) {
			now, size := t0, 10*gb
			var sent []GrowthAlert
			stubGrowth(t, &now, &size, &sent)
			old := growth
			t.Cleanup(func() { growth = old })
			growth = &growthMonitor{launched: t0, notifs: "unknown"}
			updates := make(chan GrowthStatus, 16)
			growthChanged = func(s GrowthStatus) { updates <- s }
			atWork := make(chan struct{}, 1)
			growthMeasure = func(ctx context.Context, prev *growthSnapshot, _ bool) growthSnapshot {
				atWork <- struct{}{}
				<-ctx.Done()
				return growthSnapshot{At: now.Unix(), FDA: true, Entries: []growthEntry{done(todesk, size)}}
			}
			on := defaultGrowthConfig()
			on.Enabled = true
			if err := saveGrowthConfig(growthDir(), on); err != nil {
				t.Fatal(err)
			}
			if !growth.run(false) {
				t.Fatal("the background run did not start")
			}
			<-atWork
			off()
			// The run's start, the change of settings and the run's end.
			for range 3 {
				select {
				case <-updates:
				case <-time.After(10 * time.Second):
					t.Fatal("the background run was not stopped")
				}
			}
			if s := growth.status(); s.Running || s.LastAt != 0 {
				t.Errorf("status %+v, want the run over and nothing saved", s)
			}
		})
	}
}
