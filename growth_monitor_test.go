package main

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
		{"launch after 20 hours waits five minutes", t0.Add(-21 * time.Hour), t0, t0, false, t0.Add(5 * time.Minute)},
		{"later than five minutes after launch goes now", t0.Add(-30 * time.Hour), t0, t0.Add(time.Hour), false, t0.Add(time.Hour)},
		{"otherwise 24 hours after the last record", t0.Add(-3 * time.Hour), t0, t0, false, t0.Add(21 * time.Hour)},
		{"a record from the future (a wrong clock) is as good as none", t0.Add(2 * time.Hour), t0.Add(-time.Hour), t0, false, t0},
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
	n, m, notify, changed, owner, power := growthNow, growthMeasure, growthNotify, growthChanged, growthOwnerOf, growthPower
	t.Cleanup(func() {
		growthNow, growthMeasure, growthNotify, growthChanged, growthOwnerOf, growthPower = n, m, notify, changed, owner, power
	})
	growthNow = func() time.Time { return *now }
	growthMeasure = func(ctx context.Context, prev *growthSnapshot) growthSnapshot {
		return growthSnapshot{At: now.Unix(), FDA: true, Entries: []growthEntry{done(todesk, *size)}}
	}
	growthNotify = func(a GrowthAlert) error { *sent = append(*sent, a); return nil }
	growthChanged = func(GrowthStatus) {}
	growthOwnerOf = func() func(growthEntry) growthOwner { return byBase }
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
	if got, want := m.status().NextAt, now.Add(30*time.Minute).Unix(); got != want {
		t.Errorf("on 19%% battery: next %d, want %d", got, want)
	}

	growthPower = func(context.Context) (bool, int) { return false, 100 }
	release := userScan()
	m.tick(true)
	release()
	if got, want := m.status().NextAt, now.Add(10*time.Minute).Unix(); got != want {
		t.Errorf("during a user scan: next %d, want %d", got, want)
	}
	if m.status().Running || len(loadGrowthSnapshots(growthDir())) != 0 {
		t.Errorf("a deferred tick must measure nothing: %+v", m.status())
	}
}

// TestSaveGrowthConfigWaitsForTheConfigLock pins that saving the settings
// holds growthConfigMu, which the menu bar's "turn off" takes too, so that
// neither loses the other's change.
func TestSaveGrowthConfigWaitsForTheConfigLock(t *testing.T) {
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	c := defaultGrowthConfig() // monitoring stays off, so nothing measures
	c.DailyGB = 5
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
	for key := range m["zh"] {
		if strings.HasPrefix(key, "growth.") && m["en"][key] == "" {
			t.Errorf("%s has no English text", key)
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
	for n, want := range map[int64]string{1023: "1023 B", 512 << 20: "512 MB", 6012954214: "5.6 GB", 152 * gb: "152 GB", 2 << 40: "2.0 TB"} {
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
