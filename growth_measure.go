package main

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Limits of spec §四. They are variables only so tests can shorten them.
var (
	growthEntryLimit = 5 * time.Minute  // one entry, as long as Space Analysis allows a big folder
	growthRunLimit   = 15 * time.Minute // the whole run
)

// growthWorkers measure at the same time (spec §四).
const growthWorkers = 4

// Entries of growthChildEntry bytes or more also record their first-level
// children of growthChildFloor bytes or more, so a notification can name
// ToDesk/dumps rather than ToDesk. These are variables for tests.
var (
	growthChildEntry int64 = 1 << 30
	growthChildFloor int64 = 64 << 20
)

// errGrowthStop ends a walk whose time ran out.
var errGrowthStop = errors.New("growth: time limit")

// measureGrowthEntry walks t without following links or leaving its volume,
// summing what files take on disk the way the rest of Cleanzo counts
// (onDisk).
//   - When ctx ends first, the sum so far comes back with Complete false:
//     a lower bound.
//   - Folders that cannot be read set Denied, and the walk goes on.
//   - An item removed since it was listed is skipped without setting Denied.
//   - An entry that vanished since it was listed measures as an empty,
//     complete one.
func measureGrowthEntry(ctx context.Context, t growthTarget) growthEntry {
	e := growthEntry{Path: t.path, Kind: t.kind}
	root, err := os.Lstat(t.path)
	if err != nil {
		e.Complete, e.Denied = true, !errors.Is(err, fs.ErrNotExist)
		return e
	}
	if !root.IsDir() {
		e.Size, e.Complete = onDisk(root), true
		return e
	}
	dev := deviceOf(root)
	children := map[string]int64{}
	walkErr := filepath.WalkDir(t.path, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return errGrowthStop
		}
		if err != nil {
			// Removed since it was listed: gone, not unreadable.
			if !errors.Is(err, fs.ErrNotExist) {
				e.Denied = true
			}
			return nil
		}
		if p == t.path {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			// Removed since it was listed: gone, not unreadable.
			if !errors.Is(ierr, fs.ErrNotExist) {
				e.Denied = true
			}
			return nil
		}
		if d.IsDir() {
			if deviceOf(info) != dev {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		size := onDisk(info)
		e.Size += size
		rel, _ := filepath.Rel(t.path, p)
		first, _, _ := strings.Cut(rel, string(filepath.Separator))
		children[first] += size
		return nil
	})
	e.Complete = !errors.Is(walkErr, errGrowthStop) && ctx.Err() == nil
	if e.Size >= growthChildEntry {
		for name, size := range children {
			if size >= growthChildFloor {
				if e.Children == nil {
					e.Children = map[string]int64{}
				}
				e.Children[name] = size
			}
		}
	}
	return e
}

// growthOrder sorts targets largest first by the last record. Entries it
// never saw go last, in their listed order. A run cut short then loses only
// small entries.
func growthOrder(targets []growthTarget, prev *growthSnapshot) []growthTarget {
	last := map[string]int64{}
	if prev != nil {
		for _, e := range prev.Entries {
			last[e.Path] = e.Size
		}
	}
	out := slices.Clone(targets)
	slices.SortStableFunc(out, func(a, b growthTarget) int { return cmp.Compare(last[b.path], last[a.path]) })
	return out
}

// takeGrowthSnapshot measures targets with growthWorkers background threads
// (spec §四):
//   - each entry gets at most growthEntryLimit;
//   - the run stops at growthRunLimit;
//   - entries measured until then are kept, and Cut tells that some never started.
//
// Entries come sorted by path.
func takeGrowthSnapshot(ctx context.Context, targets []growthTarget, prev *growthSnapshot, fda bool, now time.Time) growthSnapshot {
	order := growthOrder(targets, prev)
	run, cancel := context.WithTimeout(ctx, growthRunLimit)
	defer cancel()
	jobs := make(chan growthTarget)
	results := make(chan growthEntry)
	var wg sync.WaitGroup
	for range growthWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			backgroundThread() // lowest CPU priority and throttled disk I/O; never unlocked, so Go retires the thread with them
			for t := range jobs {
				ectx, ecancel := context.WithTimeout(run, growthEntryLimit)
				results <- measureGrowthEntry(ectx, t)
				ecancel()
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, t := range order {
			if run.Err() != nil {
				return
			}
			select {
			case jobs <- t:
			case <-run.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	s := growthSnapshot{At: now.Unix(), FDA: fda, Entries: []growthEntry{}}
	for e := range results {
		s.Entries = append(s.Entries, e)
	}
	s.Cut = len(s.Entries) < len(order)
	slices.SortFunc(s.Entries, func(a, b growthEntry) int { return strings.Compare(a.Path, b.Path) })
	return s
}
