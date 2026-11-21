//go:build !darwin

package main

import (
	"context"
	"syscall"
	"time"
)

// claudeRunSupported is false off macOS: the Running tab's methods refuse
// and its summary is empty.
func claudeRunSupported() bool { return false }

// realClaudeSource reads nothing off macOS; the Running tab's methods never
// use it.
func realClaudeSource() claudeSource {
	return claudeSource{
		snapshot:    func() (claudeSnapshot, error) { return claudeSnapshot{}, nil },
		started:     func(int32) (int64, bool) { return 0, false },
		footprint:   func(int32) uint64 { return 0 },
		cwd:         func(int32) string { return "" },
		identity:    func(int32) (string, string) { return "", "" },
		responsible: func(int32) int32 { return -1 },
		system:      func(string) bool { return false },
		ports:       func(context.Context, []int32) map[int32][]int { return nil },
		closed:      func(string) (string, string) { return "", "" },
		signal:      func(int32, syscall.Signal) error { return nil },
		now:         func() time.Time { return time.Time{} },
		sleep:       func(context.Context, time.Duration) error { return nil },
	}
}

// claudeWorkingFolders reads nothing off macOS, where worktrees are not
// looked for.
func claudeWorkingFolders() worktreeUsage { return worktreeUsage{} }
