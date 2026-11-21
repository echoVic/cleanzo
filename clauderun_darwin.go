package main

import (
	"context"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// claudeRunSupported reports whether the Running tab can read processes:
// macOS only.
func claudeRunSupported() bool { return true }

// realClaudeSource wires the Running tab to the native reads and signals.
func realClaudeSource() claudeSource {
	return claudeSource{
		snapshot: func() (claudeSnapshot, error) { return claudeSnapshot{}, nil },
		started: func(pid int32) (int64, bool) {
			kp, err := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
			if err != nil || kp.Proc.P_stat == 5 { // SZOMB: a zombie is not running
				return 0, false
			}
			return kp.Proc.P_starttime.Sec*1_000_000 + int64(kp.Proc.P_starttime.Usec), true
		},
		footprint:   func(int32) uint64 { return 0 },
		cwd:         func(int32) string { return "" },
		identity:    func(int32) (string, string) { return "", "" },
		responsible: nativeResponsiblePid,
		system:      systemExecutable,
		ports:       func(context.Context, []int32) map[int32][]int { return nil },
		closed:      func(string) (string, string) { return "", "" },
		// One process at a time: a pid of 0 or below would reach a process
		// group or every process, and pid 1 is launchd.
		signal: func(pid int32, sig syscall.Signal) error {
			if pid <= 1 {
				return syscall.EINVAL
			}
			return syscall.Kill(int(pid), sig)
		},
		now:   time.Now,
		sleep: sleepCtx,
	}
}

// claudeWorkingFolders is the working folders of this user's processes and
// of the open Claude Code sessions; OK is false when they cannot be read.
func claudeWorkingFolders() worktreeUsage { return worktreeUsage{} }
