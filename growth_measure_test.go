package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMeasureGrowthEntrySumsAndRecordsChildren(t *testing.T) {
	entry, floor := growthChildEntry, growthChildFloor
	growthChildEntry, growthChildFloor = 1, 1
	t.Cleanup(func() { growthChildEntry, growthChildFloor = entry, floor })
	root := filepath.Join(t.TempDir(), "ToDesk")
	writeFile(t, filepath.Join(root, "dumps", "a.dmp"), 5000)
	writeFile(t, filepath.Join(root, "dumps", "b.dmp"), 5000)
	writeFile(t, filepath.Join(root, "logs", "x.log"), 100)
	e := measureGrowthEntry(context.Background(), growthTarget{path: root, kind: growthSystem})
	if !e.Complete || e.Denied || e.Kind != growthSystem || e.Path != root {
		t.Fatalf("entry = %+v", e)
	}
	if e.Size != 10100 || e.Children["dumps"] != 10000 || e.Children["logs"] != 100 {
		t.Fatalf("size %d, children %v", e.Size, e.Children)
	}
}

func TestMeasureGrowthEntryKeepsOnlyBigChildren(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "dumps", "a.dmp"), 5000)
	if e := measureGrowthEntry(context.Background(), growthTarget{path: root, kind: growthHome}); e.Children != nil {
		t.Fatalf("children recorded for a small entry: %v", e.Children)
	}
}

func TestMeasureGrowthEntryStopsAtItsDeadline(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a"), 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := measureGrowthEntry(ctx, growthTarget{path: root, kind: growthHome}); e.Complete {
		t.Fatalf("a stopped walk must be incomplete: %+v", e)
	}
}

func TestMeasureGrowthEntryFileAndVanished(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "loose.db")
	writeFile(t, f, 300)
	if e := measureGrowthEntry(context.Background(), growthTarget{path: f, kind: growthHome}); e.Size != 300 || !e.Complete {
		t.Fatalf("file = %+v", e)
	}
	if e := measureGrowthEntry(context.Background(), growthTarget{path: filepath.Join(dir, "gone"), kind: growthHome}); e.Size != 0 || !e.Complete || e.Denied {
		t.Fatalf("vanished = %+v", e)
	}
}

func TestGrowthOrderPutsLastRecordsLargestFirst(t *testing.T) {
	ts := []growthTarget{{path: "a"}, {path: "b"}, {path: "c"}, {path: "new"}}
	prev := &growthSnapshot{Entries: []growthEntry{{Path: "a", Size: 5}, {Path: "b", Size: 1}, {Path: "c", Size: 9}}}
	var got []string
	for _, t := range growthOrder(ts, prev) {
		got = append(got, t.path)
	}
	if want := []string{"c", "a", "b", "new"}; !slices.Equal(got, want) {
		t.Fatalf("order = %q, want %q", got, want)
	}
}

func TestTakeGrowthSnapshot(t *testing.T) {
	dir := t.TempDir()
	var ts []growthTarget
	for _, n := range []string{"a", "b", "c"} {
		p := filepath.Join(dir, n)
		writeFile(t, filepath.Join(p, "f"), 10)
		ts = append(ts, growthTarget{path: p, kind: growthHome})
	}
	s := takeGrowthSnapshot(context.Background(), ts, nil, true, time.Unix(1000, 0))
	if s.Cut || len(s.Entries) != 3 || s.At != 1000 || !s.FDA || !slices.IsSortedFunc(s.Entries, func(a, b growthEntry) int { return strings.Compare(a.Path, b.Path) }) {
		t.Fatalf("snapshot = %+v", s)
	}
	limit := growthRunLimit
	growthRunLimit = 0
	t.Cleanup(func() { growthRunLimit = limit })
	if cut := takeGrowthSnapshot(context.Background(), ts, nil, true, time.Unix(1000, 0)); !cut.Cut || len(cut.Entries) != 0 {
		t.Fatalf("a run out of time must be cut and keep nothing unstarted: %+v", cut)
	}
}
