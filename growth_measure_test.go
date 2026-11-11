package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
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
	s := takeGrowthSnapshot(context.Background(), ts, false, nil, true, true, time.Unix(1000, 0))
	if s.Cut || len(s.Entries) != 3 || s.At != 1000 || !s.FDA || !slices.IsSortedFunc(s.Entries, func(a, b growthEntry) int { return strings.Compare(a.Path, b.Path) }) {
		t.Fatalf("snapshot = %+v", s)
	}
	// A folder that could not be listed left entries out: the run is cut,
	// though it measured every target it had.
	if unlisted := takeGrowthSnapshot(context.Background(), ts, true, nil, true, true, time.Unix(1000, 0)); !unlisted.Cut || len(unlisted.Entries) != 3 {
		t.Fatalf("a run missing a folder's entries must be cut: %+v", unlisted)
	}
	limit := growthRunLimit
	growthRunLimit = 0
	t.Cleanup(func() { growthRunLimit = limit })
	if cut := takeGrowthSnapshot(context.Background(), ts, false, nil, true, true, time.Unix(1000, 0)); !cut.Cut || len(cut.Entries) != 0 {
		t.Fatalf("a run out of time must be cut and keep nothing unstarted: %+v", cut)
	}
}

// hookContext stands in for the context of a walk and counts its Err calls,
// which measureGrowthEntry makes once at the start of each step. On call at
// it runs hook once. From call stop on, Err reports context.Canceled; before
// that it reports nil.
type hookContext struct {
	context.Context
	calls, at, stop int
	hook            func()
}

func (c *hookContext) Err() error {
	c.calls++
	if c.calls == c.at && c.hook != nil {
		c.hook()
	}
	if c.stop > 0 && c.calls >= c.stop {
		return context.Canceled
	}
	return nil
}

// A file removed after the listing is gone, not unreadable: the walk goes on
// and the entry stays complete.
func TestMeasureGrowthEntrySkipsFileRemovedMidWalk(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a"), 10)
	writeFile(t, filepath.Join(root, "b"), 20)
	writeFile(t, filepath.Join(root, "c"), 40)
	ctx := &hookContext{Context: context.Background(), at: 3, hook: func() {
		if err := os.Remove(filepath.Join(root, "b")); err != nil {
			t.Fatal(err)
		}
	}}
	want := int64(50) // a and c: b was removed before its Lstat
	if runtime.GOOS == "windows" {
		want = 70 // Windows answers Info from the listing, so b still counts there
	}
	if e := measureGrowthEntry(ctx, growthTarget{path: root, kind: growthHome}); e.Denied || !e.Complete || e.Size != want {
		t.Fatalf("entry = %+v, want size %d, complete and not denied", e, want)
	}
}

// A folder removed after the listing is skipped the same way: the walk goes
// on, and the entry stays complete.
func TestMeasureGrowthEntrySkipsFolderRemovedMidWalk(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	writeFile(t, filepath.Join(sub, "x"), 100)
	writeFile(t, filepath.Join(root, "y"), 20)
	ctx := &hookContext{Context: context.Background(), at: 2, hook: func() {
		if err := os.RemoveAll(sub); err != nil {
			t.Fatal(err)
		}
	}}
	if e := measureGrowthEntry(ctx, growthTarget{path: root, kind: growthHome}); e.Denied || !e.Complete || e.Size != 20 {
		t.Fatalf("entry = %+v, want size 20, complete and not denied", e)
	}
}

// A walk stopped midway keeps what it counted before the stop, incomplete.
func TestMeasureGrowthEntryStopsMidWalkWithLowerBound(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a"), 10)
	writeFile(t, filepath.Join(root, "b"), 20)
	ctx := &hookContext{Context: context.Background(), stop: 3}
	if e := measureGrowthEntry(ctx, growthTarget{path: root, kind: growthHome}); e.Complete || e.Denied || e.Size != 10 {
		t.Fatalf("entry = %+v, want size 10 and incomplete", e)
	}
}

// growthEntryLimit bounds each entry of a run: an entry that runs out of time
// is kept incomplete, and the run is not cut, since nothing stayed unstarted.
func TestTakeGrowthSnapshotHonorsEntryLimit(t *testing.T) {
	limit := growthEntryLimit
	// 0, not a tiny limit: with a coarse clock, as on Windows, a tiny walk can
	// finish before a nanosecond is seen to pass.
	growthEntryLimit = 0
	t.Cleanup(func() { growthEntryLimit = limit })
	dir := t.TempDir()
	var ts []growthTarget
	for _, n := range []string{"a", "b", "c"} {
		p := filepath.Join(dir, n)
		writeFile(t, filepath.Join(p, "f"), 10)
		ts = append(ts, growthTarget{path: p, kind: growthHome})
	}
	s := takeGrowthSnapshot(context.Background(), ts, false, nil, true, true, time.Unix(1000, 0))
	if s.Cut || len(s.Entries) != 3 {
		t.Fatalf("snapshot = %+v", s)
	}
	for _, e := range s.Entries {
		if e.Complete {
			t.Fatalf("entry measured past its limit: %+v", e)
		}
	}
}

// Only a background run puts its measuring threads in the background; a run
// the user asked for keeps normal priority.
func TestTakeGrowthSnapshotLowersPriorityOnlyInTheBackground(t *testing.T) {
	var lowered atomic.Int32
	saved := growthLowerPriority
	growthLowerPriority = func() { lowered.Add(1) }
	t.Cleanup(func() { growthLowerPriority = saved })
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a", "f"), 10)
	ts := []growthTarget{{path: filepath.Join(dir, "a"), kind: growthHome}}
	for _, background := range []bool{false, true} {
		lowered.Store(0)
		s := takeGrowthSnapshot(context.Background(), ts, false, nil, true, background, time.Unix(1000, 0))
		want := int32(0)
		if background {
			want = growthWorkers
		}
		if len(s.Entries) != 1 || lowered.Load() != want {
			t.Errorf("background %v: %d threads lowered, want %d; entries %+v", background, lowered.Load(), want, s.Entries)
		}
	}
}
