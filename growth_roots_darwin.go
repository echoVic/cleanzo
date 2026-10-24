package main

import (
	"context"
	"path/filepath"

	"github.com/egoist/mygo"
)

// growthRoots are the folders whose entries are measured on macOS (spec §三).
// system is "/" in the app and a temporary folder in tests.
func growthRoots(home, system string) []growthRoot {
	lib := filepath.Join(home, "Library")
	sys := filepath.Join(system, "Library")
	return []growthRoot{
		{path: filepath.Join(lib, "Application Support"), kind: growthHome},
		{path: filepath.Join(lib, "Caches"), kind: growthHome},
		{path: filepath.Join(lib, "Containers"), kind: growthHome, needsFDA: true},
		{path: filepath.Join(lib, "Group Containers"), kind: growthHome, needsFDA: true},
		{path: filepath.Join(lib, "Logs"), kind: growthHome},
		{path: filepath.Join(lib, "HTTPStorages"), kind: growthHome},
		{path: filepath.Join(lib, "WebKit"), kind: growthHome},
		{path: filepath.Join(lib, "Developer"), kind: growthHome},
		{path: home, kind: growthDot},
		{path: filepath.Join(sys, "Application Support"), kind: growthSystem},
		{path: filepath.Join(sys, "Caches"), kind: growthSystem},
		{path: filepath.Join(sys, "Logs"), kind: growthSystem},
	}
}

// growthSupported tells whether this platform has the growth monitor.
func growthSupported() bool { return true }

// growthBattery reports whether the Mac runs on battery and its charge. A
// desktop Mac, or a battery that cannot be read, counts as plugged in.
func growthBattery(ctx context.Context) (onBattery bool, percent int) {
	if !mygo.Power.IsOnBattery() {
		return false, 100
	}
	if b := statusBattery(ctx); b != nil {
		return true, b.Percent
	}
	return true, 100
}
