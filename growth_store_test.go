package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGrowthSnapshotsSaveLoadAndPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(200*86400, 0)
	old := growthSnapshot{At: now.Add(-91 * 24 * time.Hour).Unix(), Entries: []growthEntry{}}
	mid := growthSnapshot{At: now.Add(-24 * time.Hour).Unix(), FDA: true, Entries: []growthEntry{{Path: "/a", Kind: growthHome, Size: 7, Complete: true}}}
	cur := growthSnapshot{At: now.Unix(), FDA: true, Cut: true, Entries: []growthEntry{}}
	for _, s := range []growthSnapshot{old, mid, cur} {
		if err := saveGrowthSnapshot(dir, s, now); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(dir, "records", "garbage.json"), 3)
	got := loadGrowthSnapshots(dir)
	if len(got) != 2 || got[0].At != mid.At || got[1].At != cur.At || !got[1].Cut || got[0].Entries[0].Size != 7 {
		t.Fatalf("records = %+v", got)
	}
	if last := lastGrowthAt(dir); !last.Equal(now) {
		t.Fatalf("last = %v", last)
	}
	if last := lastGrowthAt(t.TempDir()); !last.IsZero() {
		t.Fatalf("no records: last = %v", last)
	}
}

func TestGrowthConfigDefaultsAndValidation(t *testing.T) {
	dir := t.TempDir()
	if c := loadGrowthConfig(dir); c.Enabled || c.DailyGB != 2 || c.WeeklyGB != 10 || c.Ignored == nil {
		t.Fatalf("defaults = %+v", c)
	}
	c, err := validateGrowthConfig(GrowthConfig{Enabled: false, OpenAtLogin: true, DailyGB: 5, WeeklyGB: 20, Ignored: []string{" APP:X ", "app:x", ""}})
	if err != nil || c.OpenAtLogin || len(c.Ignored) != 1 || c.Ignored[0] != "app:x" {
		t.Fatalf("validated = %+v, %v", c, err)
	}
	var ce codeError
	if _, err := validateGrowthConfig(GrowthConfig{DailyGB: 3, WeeklyGB: 10}); !errors.As(err, &ce) || ce.Code != "growth-threshold" {
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
}

func TestGrowthNotifiedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	n := loadGrowthNotified(dir)
	if n.Day == nil || n.Week == nil || n.Count == nil {
		t.Fatalf("maps must be ready: %+v", n)
	}
	n.Day["app:x"] = "2026-10-24"
	n.Recent = []GrowthAlert{{Key: "app:x", Bytes: 1}}
	if err := saveGrowthNotified(dir, n); err != nil {
		t.Fatal(err)
	}
	if got := loadGrowthNotified(dir); got.Day["app:x"] != "2026-10-24" || len(got.Recent) != 1 {
		t.Fatalf("reloaded = %+v", got)
	}
}
