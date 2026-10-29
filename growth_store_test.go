package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"
)

func TestGrowthSnapshotsSaveLoadAndPrune(t *testing.T) {
	dir := t.TempDir()
	records := filepath.Join(dir, "records")
	now := time.Unix(200*86400, 0)
	ago := func(days int) int64 { return now.Add(-time.Duration(days) * 24 * time.Hour).Unix() }
	name := func(at int64) string { return strconv.FormatInt(at, 10) + ".json" }
	// Two records, each saved at its own time so that nothing is pruned yet:
	// the one 89 days old must stay, the one 91 days old must go.
	for _, at := range []int64{ago(89), ago(91)} {
		if err := saveGrowthSnapshot(dir, growthSnapshot{At: at, Entries: []growthEntry{}}, time.Unix(at, 0)); err != nil {
			t.Fatal(err)
		}
	}
	// Files that are not records. Each holds a valid snapshot, so only the
	// name tells them apart: they must stay, and loads must skip them.
	notRecord, err := json.Marshal(growthSnapshot{At: ago(2), Entries: []growthEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"notes.json", "5.json.tmp", "7", "backup.json"} {
		if err := os.WriteFile(filepath.Join(records, f), notRecord, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// An expired name on a folder is not a record either.
	if err := os.Mkdir(filepath.Join(records, "3.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	cur := growthSnapshot{At: now.Unix(), FDA: true, Cut: true, Entries: []growthEntry{}}
	if err := saveGrowthSnapshot(dir, cur, now); err != nil {
		t.Fatal(err)
	}
	// Only the record 91 days old may be removed.
	want := []string{"3.json", "5.json.tmp", "7", "backup.json", name(ago(89)), "notes.json", name(cur.At)}
	slices.Sort(want)
	entries, err := os.ReadDir(records)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, want) {
		t.Fatalf("records/ holds %v, want %v", names, want)
	}
	got := loadGrowthSnapshots(dir)
	if len(got) != 2 || got[0].At != ago(89) || got[1].At != cur.At || !got[1].Cut {
		t.Fatalf("loaded = %+v", got)
	}
	if last := lastGrowthAt(dir, now); !last.Equal(now) {
		t.Fatalf("last = %v", last)
	}
	if last := lastGrowthAt(t.TempDir(), now); !last.IsZero() {
		t.Fatalf("no records: last = %v", last)
	}
	junk := t.TempDir()
	writeFile(t, filepath.Join(junk, "records", "7"), 3)
	writeFile(t, filepath.Join(junk, "records", "notes.json"), 3)
	if last := lastGrowthAt(junk, now); !last.IsZero() {
		t.Fatalf("only files that are not records: last = %v", last)
	}
}

func TestGrowthSnapshotsSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(200*86400, 0)
	s := growthSnapshot{At: now.Add(-time.Hour).Unix(), FDA: true, Cut: true, Entries: []growthEntry{
		{Path: "/a", Kind: growthHome, Size: 7, Complete: true},
		{Path: "/b", Kind: growthSystem, Size: 9 << 30, Denied: true, Children: map[string]int64{"dumps": 8 << 30, "logs": 1 << 20}},
	}}
	if err := saveGrowthSnapshot(dir, s, now); err != nil {
		t.Fatal(err)
	}
	got := loadGrowthSnapshots(dir)
	if len(got) != 1 || !reflect.DeepEqual(got[0], s) {
		t.Fatalf("loaded %+v, saved %+v", got, s)
	}
}

func TestGrowthConfigDefaultsAndValidation(t *testing.T) {
	dir := t.TempDir()
	if c := loadGrowthConfig(dir); c.Enabled || c.DailyGB != 2 || c.WeeklyGB != 10 || c.Ignored == nil {
		t.Fatalf("defaults = %+v", c)
	}
	c, err := validateGrowthConfig(GrowthConfig{Enabled: false, OpenAtLogin: true, DailyGB: 5, WeeklyGB: 20, Ignored: []string{" APP:X ", "app:x", ""}}, false)
	if err != nil || c.OpenAtLogin || len(c.Ignored) != 1 || c.Ignored[0] != "app:x" {
		t.Fatalf("validated = %+v, %v", c, err)
	}
	var ce codeError
	if _, err := validateGrowthConfig(GrowthConfig{DailyGB: 3, WeeklyGB: 10}, false); !errors.As(err, &ce) || ce.Code != "growth-threshold" {
		t.Fatalf("bad threshold err = %v", err)
	}
	if err := saveGrowthConfig(dir, c); err != nil {
		t.Fatal(err)
	}
	if got := loadGrowthConfig(dir); got.DailyGB != 5 || got.WeeklyGB != 20 {
		t.Fatalf("reloaded = %+v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"dailyGB": 7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadGrowthConfig(dir); got.DailyGB != 2 {
		t.Fatalf("an unknown threshold must fall back to the default: %+v", got)
	}
	if c, err := validateGrowthConfig(GrowthConfig{OpenAtLogin: true, DailyGB: 5, WeeklyGB: 20}, true); err != nil || !c.OpenAtLogin {
		t.Errorf("a reading in the menu bar keeps the login item with monitoring off: %+v, %v", c, err)
	}
	kept := GrowthConfig{OpenAtLogin: true, DailyGB: 5, WeeklyGB: 20, Ignored: []string{}}
	if err := saveGrowthConfig(dir, kept); err != nil {
		t.Fatal(err)
	}
	if !loadGrowthConfig(dir).OpenAtLogin {
		t.Error("loading cleared a login item saved for the readings")
	}
}

func TestGrowthConfigRoundTripKeepsOpenAtLogin(t *testing.T) {
	dir := t.TempDir()
	c, err := validateGrowthConfig(GrowthConfig{Enabled: true, OpenAtLogin: true, DailyGB: 5, WeeklyGB: 20, Ignored: []string{"app:x"}, Asked: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveGrowthConfig(dir, c); err != nil {
		t.Fatal(err)
	}
	if got := loadGrowthConfig(dir); !got.Enabled || !got.OpenAtLogin || !reflect.DeepEqual(got, c) {
		t.Fatalf("reloaded = %+v, saved = %+v", got, c)
	}
}

func TestGrowthConfigSaveLeavesNoTempFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	// A folder where config.json should be makes the final rename fail.
	writeFile(t, filepath.Join(dir, "config.json", "inside"), 1)
	if err := saveGrowthConfig(dir, defaultGrowthConfig()); err == nil {
		t.Fatal("saving over a folder must fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Fatalf("a failed save left %s behind", e.Name())
		}
	}
}

func TestGrowthConfigSaveUsesItsOwnTempFile(t *testing.T) {
	dir := t.TempDir()
	// Another writer's file under the old fixed temporary name must survive.
	if err := os.WriteFile(filepath.Join(dir, "config.json.tmp"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveGrowthConfig(dir, defaultGrowthConfig()); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "config.json.tmp")); err != nil || string(data) != "keep" {
		t.Fatalf("the save touched config.json.tmp: %q, %v", data, err)
	}
	info, err := os.Stat(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestGrowthNotifiedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	n := loadGrowthNotified(dir)
	if n.Day == nil || n.Week == nil || n.Count == nil {
		t.Fatalf("maps must be ready: %+v", n)
	}
	n.Day["app:x"] = "2026-10-24"
	n.Week["app:x"] = 1792800000
	n.Count["2026-10-24"] = 2
	n.Recent = []GrowthAlert{{Key: "app:x", Name: "X", Window: "week", Days: 5, Bytes: 11 << 30, AtLeast: true, Folder: "/h/x/dumps", Total: 20 << 30, At: 1792800000}}
	if err := saveGrowthNotified(dir, n); err != nil {
		t.Fatal(err)
	}
	got := loadGrowthNotified(dir)
	if got.Day["app:x"] != "2026-10-24" || len(got.Recent) != 1 {
		t.Fatalf("reloaded = %+v", got)
	}
	if got.Week["app:x"] != 1792800000 || got.Count["2026-10-24"] != 2 || !reflect.DeepEqual(got.Recent, n.Recent) {
		t.Fatalf("week, count or recent differ: %+v", got)
	}
}

// growthTestEntry is a directory entry of a chosen type, so the record rule is
// tested without the file system.
type growthTestEntry struct {
	name string
	typ  fs.FileMode
}

func (e growthTestEntry) Name() string               { return e.name }
func (e growthTestEntry) IsDir() bool                { return e.typ.IsDir() }
func (e growthTestEntry) Type() fs.FileMode          { return e.typ }
func (e growthTestEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrInvalid }

func TestGrowthRecordAtAcceptsOnlyRegularDigitJSON(t *testing.T) {
	cases := []struct {
		name string
		typ  fs.FileMode
		ok   bool
	}{
		{"1793000000.json", 0, true},
		{"1793000000.json", fs.ModeDir, false},
		{"1793000000.json", fs.ModeSymlink, false},
		{"0.json", 0, false},
		{"-5.json", 0, false},
		{"+5.json", 0, false},
		{"12a.json", 0, false},
		{".json", 0, false},
		{"99999999999999999999.json", 0, false},
		{"5.json.tmp", 0, false},
		{"7", 0, false},
		{"notes.json", 0, false},
		{"backup.json", 0, false},
	}
	for _, c := range cases {
		at, ok := growthRecordAt(growthTestEntry{name: c.name, typ: c.typ})
		if ok != c.ok || (ok && at != 1793000000) {
			t.Errorf("growthRecordAt(%q, %v) = %d, %v; want %v", c.name, c.typ, at, ok, c.ok)
		}
	}
}

// A record dated after now, which a clock set back leaves behind, is not the
// last one: it would make every tick find the next measurement due.
func TestGrowthLastRecordIgnoresRecordsFromTheFuture(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(200*86400, 0)
	for _, at := range []int64{now.Add(-3 * time.Hour).Unix(), now.Unix(), now.Add(time.Second).Unix(), now.Add(48 * time.Hour).Unix()} {
		if err := saveGrowthSnapshot(dir, growthSnapshot{At: at, Entries: []growthEntry{}}, now); err != nil {
			t.Fatal(err)
		}
	}
	if last := lastGrowthAt(dir, now); !last.Equal(now) {
		t.Fatalf("last = %v, want %v: a record at now counts, those after it do not", last, now)
	}
	if last := lastGrowthAt(dir, now.Add(-time.Hour)); !last.Equal(now.Add(-3 * time.Hour)) {
		t.Fatalf("an hour earlier: last = %v", last)
	}
	only := t.TempDir()
	if err := saveGrowthSnapshot(only, growthSnapshot{At: now.Add(time.Hour).Unix(), Entries: []growthEntry{}}, now); err != nil {
		t.Fatal(err)
	}
	if last := lastGrowthAt(only, now); !last.IsZero() {
		t.Fatalf("only a record from the future: last = %v", last)
	}
}

// growthHistory is the records up to now, oldest first: one dated after now,
// which a clock set back leaves behind, is left out.
func TestGrowthHistoryLeavesOutRecordsFromTheFuture(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(200*86400, 0)
	ats := []int64{now.Add(48 * time.Hour).Unix(), now.Add(-24 * time.Hour).Unix(), now.Add(time.Second).Unix(), now.Unix()}
	for _, at := range ats {
		if err := saveGrowthSnapshot(dir, growthSnapshot{At: at, Entries: []growthEntry{}}, now); err != nil {
			t.Fatal(err)
		}
	}
	var got []int64
	for _, s := range growthHistory(dir, now) {
		got = append(got, s.At)
	}
	if want := []int64{now.Add(-24 * time.Hour).Unix(), now.Unix()}; !slices.Equal(got, want) {
		t.Fatalf("history %v, want %v", got, want)
	}
}
