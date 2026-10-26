//go:build !darwin

package main

import "context"

// processSupported is false off macOS: the page shows a note and
// WatchProcesses refuses.
func processSupported() bool { return false }

// realProcessSource reads nothing off macOS; WatchProcesses never uses it.
func realProcessSource() processSource {
	return processSource{
		procs:       func() []procReading { return nil },
		others:      func(context.Context) []otherProc { return nil },
		apps:        func() []workspaceApp { return nil },
		responsible: func(int32) int32 { return -1 },
		system:      func(string) bool { return false },
		bundleID:    func(string) string { return "" },
		pressure:    func() uint64 { return 0 },
		cpu:         func() (uint64, uint64) { return 0, 0 },
	}
}
