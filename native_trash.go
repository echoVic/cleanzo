package main

import (
	"context"
	"sync"
)

// binStats is the volume-scoped size and count returned by the shell.
type binStats struct{ size, items int64 }

// Native bin probes are installed only by Windows; folder-based macOS Trash is unchanged.
var (
	queryNativeBin = func(string) (binStats, error) { return binStats{}, errUnsupported }
	emptyNativeBin = func(string) error { return errUnsupported }
)

// binScan serializes native operations and remembers only the latest successful scan.
var binScan struct {
	sync.Mutex
	home  string
	stats binStats
	valid bool
}

// scanNativeBin records an authorization snapshot; failed scans invalidate earlier snapshots.
func scanNativeBin(env junkEnv, item JunkItem) JunkItem {
	binScan.Lock()
	defer binScan.Unlock()
	binScan.valid = false
	item.Size = 0
	if env.toolContext().Err() != nil {
		return item
	}
	stats, err := queryNativeBin(env.home)
	if err != nil {
		item.Denied = true
		return item
	}
	item.Size, item.Files = stats.size, stats.items
	binScan.home, binScan.stats, binScan.valid = canonical(env.home), stats, true
	return item
}

// cleanNativeBin compares the scan before emptying; a selection never authorizes a changed bin.
func cleanNativeBin(ctx context.Context, env junkEnv, paths []string) CleanResult {
	res := CleanResult{Name: "sys.trash"}
	if ctx.Err() != nil || len(paths) != 0 || env.guard.failed {
		res.Skipped = "blocked"
		return res
	}
	binScan.Lock()
	defer binScan.Unlock()
	stats, err := queryNativeBin(env.home)
	if err != nil {
		res.Skipped = "blocked"
		return res
	}
	if !binScan.valid || binScan.home != canonical(env.home) || stats != binScan.stats {
		res.Skipped = "changed"
		return res
	}
	binScan.valid = false
	if ctx.Err() != nil {
		return res
	}
	if err := emptyNativeBin(env.home); err != nil {
		res.Skipped = "blocked"
		return res
	}
	res.Freed, res.Files = stats.size, stats.items
	return res
}
