package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const gb = int64(1 << 30)

// t0 is a fixed morning; records are placed relative to it.
var t0 = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

// byBase owns an entry by its folder name: Caches/ToDesk and Application
// Support/ToDesk both count for "app:todesk".
func byBase(e growthEntry) growthOwner {
	b := filepath.Base(e.Path)
	return growthOwner{key: "app:" + strings.ToLower(b), name: b, kind: "app"}
}

func at(d time.Duration, fda bool, entries ...growthEntry) growthSnapshot {
	return growthSnapshot{At: t0.Add(d).Unix(), FDA: fda, Entries: entries}
}

func done(path string, size int64) growthEntry {
	return growthEntry{Path: path, Kind: growthHome, Size: size, Complete: true}
}

var (
	todesk      = filepath.Join("/h", "Library", "Application Support", "ToDesk")
	todeskCache = filepath.Join("/h", "Library", "Caches", "ToDesk")
	codex       = filepath.Join("/h", "Library", "Application Support", "Codex")
)

func cfg2() GrowthConfig { return defaultGrowthConfig() }

func TestFirstRecordIsOnlyABaseline(t *testing.T) {
	if a := evaluateGrowth(nil, at(0, true, done(todesk, 100*gb)), cfg2(), byBase); len(a) != 0 {
		t.Fatalf("alerts = %+v", a)
	}
}

func TestDayAlertAgainstARecord20To28HoursOlder(t *testing.T) {
	h := []growthSnapshot{at(-24*time.Hour, true, done(todesk, 10*gb), done(todeskCache, gb))}
	cur := at(0, true, done(todesk, 12*gb), done(todeskCache, 2*gb))
	a := evaluateGrowth(h, cur, cfg2(), byBase)
	if len(a) != 1 || a[0].Window != "day" || a[0].Bytes != 3*gb || a[0].AtLeast || a[0].Days != 1 || a[0].Total != 14*gb || a[0].Key != "app:todesk" || a[0].At != cur.At {
		t.Fatalf("alerts = %+v", a)
	}
}

func TestDayNeedsARecordInItsWindow(t *testing.T) {
	h := []growthSnapshot{at(-50*time.Hour, true, done(todesk, 10*gb))}
	if a := evaluateGrowth(h, at(0, true, done(todesk, 20*gb)), cfg2(), byBase); len(a) != 0 {
		t.Fatalf("a two-day gap is neither a day nor a week: %+v", a)
	}
}

func TestWeekAlertUsesTheEarliestRecordAtLeastThreeDaysOld(t *testing.T) {
	h := []growthSnapshot{
		at(-8*24*time.Hour, true, done(todesk, 0)),
		at(-5*24*time.Hour, true, done(todesk, 10*gb)),
		at(-4*24*time.Hour, true, done(todesk, 15*gb)),
	}
	a := evaluateGrowth(h, at(0, true, done(todesk, 21*gb)), cfg2(), byBase)
	if len(a) != 1 || a[0].Window != "week" || a[0].Bytes != 11*gb || a[0].Days != 5 {
		t.Fatalf("alerts = %+v", a)
	}
}

func TestLowerBoundsAndUncomparableEntries(t *testing.T) {
	h := []growthSnapshot{at(-24*time.Hour, true, done(todesk, 10*gb), growthEntry{Path: codex, Kind: growthHome, Size: 9 * gb})}
	partial := growthEntry{Path: todesk, Kind: growthHome, Size: 12*gb + gb/2}
	a := evaluateGrowth(h, at(0, true, partial, done(codex, 30*gb)), cfg2(), byBase)
	if len(a) != 1 || a[0].Key != "app:todesk" || !a[0].AtLeast || a[0].Bytes != 2*gb+gb/2 {
		t.Fatalf("only ToDesk, as a lower bound; Codex's base was unfinished: %+v", a)
	}
}

func TestFullDiskAccessChangeAndDeniedFlipNeverCompare(t *testing.T) {
	h := []growthSnapshot{at(-24*time.Hour, false, done(todesk, gb))}
	if a := evaluateGrowth(h, at(0, true, done(todesk, 20*gb)), cfg2(), byBase); len(a) != 0 {
		t.Fatalf("FDA changed: %+v", a)
	}
	h = []growthSnapshot{at(-24*time.Hour, true, done(todesk, gb))}
	denied := done(todesk, 20*gb)
	denied.Denied = true
	if a := evaluateGrowth(h, at(0, true, denied), cfg2(), byBase); len(a) != 0 {
		t.Fatalf("readability changed: %+v", a)
	}
}

func TestNewEntriesCountFromZeroOnlyAgainstAWholeRun(t *testing.T) {
	whole := []growthSnapshot{at(-24*time.Hour, true)}
	if a := evaluateGrowth(whole, at(0, true, done(todesk, 3*gb)), cfg2(), byBase); len(a) != 1 || a[0].Bytes != 3*gb {
		t.Fatalf("new since a whole run: %+v", a)
	}
	cut := at(-24*time.Hour, true)
	cut.Cut = true
	if a := evaluateGrowth([]growthSnapshot{cut}, at(0, true, done(todesk, 3*gb)), cfg2(), byBase); len(a) != 0 {
		t.Fatalf("new since a cut run: %+v", a)
	}
}

func TestIgnoredAndShrinkingNeverAlert(t *testing.T) {
	h := []growthSnapshot{at(-24*time.Hour, true, done(todesk, 10*gb), done(codex, 50*gb))}
	c := cfg2()
	c.Ignored = []string{"app:todesk"}
	if a := evaluateGrowth(h, at(0, true, done(todesk, 20*gb), done(codex, 10*gb)), c, byBase); len(a) != 0 {
		t.Fatalf("alerts = %+v", a)
	}
}

func TestAlertNamesTheChildThatGrewMost(t *testing.T) {
	base := done(todesk, 10*gb)
	base.Children = map[string]int64{"dumps": 9 * gb, "logs": gb}
	cur := done(todesk, 16*gb)
	cur.Children = map[string]int64{"dumps": 15 * gb, "logs": gb}
	a := evaluateGrowth([]growthSnapshot{at(-24*time.Hour, true, base)}, at(0, true, cur), cfg2(), byBase)
	if len(a) != 1 || a[0].Folder != filepath.Join(todesk, "dumps") {
		t.Fatalf("alerts = %+v", a)
	}
}

func TestPickGrowthNotifications(t *testing.T) {
	now := t0
	n := loadGrowthNotified(t.TempDir())
	alerts := []GrowthAlert{
		{Key: "app:a", Window: "week", Bytes: 50 * gb},
		{Key: "app:a", Window: "day", Bytes: 5 * gb},
		{Key: "app:b", Window: "day", Bytes: 9 * gb},
		{Key: "app:c", Window: "day", Bytes: 3 * gb},
		{Key: "app:d", Window: "day", Bytes: 2 * gb},
	}
	got := pickGrowthNotifications(alerts, &n, now)
	if len(got) != 3 || got[0].Key != "app:b" || got[1].Key != "app:a" || got[1].Window != "day" || got[2].Key != "app:c" {
		t.Fatalf("first picks = %+v", got)
	}
	if again := pickGrowthNotifications(alerts, &n, now.Add(time.Hour)); len(again) != 0 {
		t.Fatalf("three a day in all, one a day per app: %+v", again)
	}
	tomorrow := now.Add(24 * time.Hour)
	week := []GrowthAlert{{Key: "app:e", Window: "week", Bytes: 20 * gb}}
	if got := pickGrowthNotifications(week, &n, tomorrow); len(got) != 1 {
		t.Fatalf("a new day has room: %+v", got)
	}
	if got := pickGrowthNotifications(week, &n, tomorrow.Add(48*time.Hour)); len(got) != 0 {
		t.Fatalf("a seven-day alert waits a week: %+v", got)
	}
}

func TestBuildGrowthApps(t *testing.T) {
	sys := filepath.Join("/", "Library", "Application Support", "ToDesk")
	h := []growthSnapshot{
		at(-10*24*time.Hour, true, done(todesk, gb), done(sys, gb)),
		at(-5*24*time.Hour, true, done(todesk, 2*gb), done(sys, 3*gb)),
		at(-24*time.Hour, true, done(todesk, 4*gb), done(sys, 5*gb)),
		at(0, true, done(todesk, 5*gb), done(sys, 9*gb), done(codex, gb)),
	}
	c := cfg2()
	c.Ignored = []string{"app:codex"}
	apps := buildGrowthApps(h, c, byBase, "/h", time.UTC)
	if len(apps) != 2 || apps[0].Key != "app:todesk" || apps[0].Size != 14*gb || apps[1].Key != "app:codex" || !apps[1].Ignored {
		t.Fatalf("apps = %+v", apps)
	}
	td := apps[0]
	if td.Day == nil || td.Day.Bytes != 5*gb || td.Week == nil || td.Week.Bytes != 9*gb || td.Week.Days != 5 || td.Month == nil || td.Month.Bytes != 12*gb || td.Month.Days != 10 {
		t.Fatalf("deltas day=%+v week=%+v month=%+v", td.Day, td.Week, td.Month)
	}
	if len(td.Spark) != 30 || td.Spark[29] != 14*gb || td.Spark[28] != 9*gb || td.Spark[27] != -1 || td.Spark[24] != 5*gb {
		t.Fatalf("spark = %v", td.Spark)
	}
	var outside bool
	for _, f := range td.Folders {
		if f.Path == sys {
			outside = f.Outside
		}
	}
	if !outside {
		t.Fatalf("a /Library folder must be marked outside home: %+v", td.Folders)
	}
	// Codex appears only now; the week base (5 days ago) measured everything, so Codex counts from 0.
	if apps[1].Week == nil || apps[1].Week.Bytes != gb {
		t.Fatalf("Codex is new since the week base: %+v", apps[1].Week)
	}
}

func TestGrowthNotifiedRemembersTheNewestAlertPerOwner(t *testing.T) {
	ago := func(days int) int64 { return t0.Add(-time.Duration(days) * 24 * time.Hour).Unix() }
	n := loadGrowthNotified(t.TempDir())
	n.Recent = []GrowthAlert{
		{Key: "app:again", Window: "day", Bytes: 2 * gb, At: ago(3)},
		{Key: "app:kept", Window: "week", Bytes: 12 * gb, At: ago(6)},
		{Key: "app:stale", Window: "day", Bytes: 3 * gb, At: ago(8)},
	}
	n.remember([]GrowthAlert{
		{Key: "app:again", Window: "week", Bytes: 20 * gb, At: t0.Unix()},
		{Key: "app:again", Window: "day", Bytes: 5 * gb, At: t0.Unix()},
		{Key: "app:new", Window: "day", Bytes: 4 * gb, At: t0.Unix()},
	}, t0)
	r := n.Recent
	if len(r) != 3 || r[0].Key != "app:again" || r[0].Bytes != 20*gb || r[1].Key != "app:new" || r[2].Key != "app:kept" {
		t.Fatalf("one per owner, this run first, the week-old stale one gone: %+v", r)
	}
	if got := n.recent(t0.Add(48 * time.Hour)); len(got) != 2 || got[0].Key != "app:again" || got[1].Key != "app:new" {
		t.Fatalf("two days on, kept is past seven days: %+v", got)
	}
	if got := n.recent(t0.Add(7 * 24 * time.Hour)); len(got) != 2 {
		t.Fatalf("exactly seven days still counts: %+v", got)
	}
	if got := n.recent(t0.Add(7*24*time.Hour + time.Second)); got == nil || len(got) != 0 {
		t.Fatalf("past seven days gives an empty list, not nil: %#v", got)
	}
	n.remember(nil, t0.Add(48*time.Hour))
	if len(n.Recent) != 2 {
		t.Fatalf("a run without alerts still forgets what is past seven days: %+v", n.Recent)
	}
}
