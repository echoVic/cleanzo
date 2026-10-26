package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParsePsOutputKeepsOtherAccountsWithFullPaths(t *testing.T) {
	out := []byte(strings.Join([]string{
		"    1     0   0.3  12288 /sbin/launchd",
		"  414    88  40.2 138240 /System/Library/PrivateFrameworks/SkyLight.framework/Resources/WindowServer",
		" 7045     0  10.8  53872 /Applications/Microsoft Defender.app/Contents/MacOS/wdavdaemon",
		" 9001   501  99.0   4096 /usr/bin/yes",
		"    0     0   5.0      0 kernel_task",
		"garbage line",
		"   12     0     x      1 /bad/cpu",
		"",
	}, "\n"))
	got := parsePsOutput(out, 501)
	want := []otherProc{
		{Pid: 1, UID: 0, CPU: 0.3, Memory: 12288 * 1024, Path: "/sbin/launchd"},
		{Pid: 414, UID: 88, CPU: 40.2, Memory: 138240 * 1024, Path: "/System/Library/PrivateFrameworks/SkyLight.framework/Resources/WindowServer"},
		{Pid: 7045, UID: 0, CPU: 10.8, Memory: 53872 * 1024, Path: "/Applications/Microsoft Defender.app/Contents/MacOS/wdavdaemon"},
		{Pid: 0, UID: 0, CPU: 5.0, Memory: 0, Path: "kernel_task"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d processes, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("process %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// testSystemPath stands in for systemExecutable, so the grouping tests run
// on every platform.
func testSystemPath(p string) bool {
	return strings.HasPrefix(p, "/System/") || strings.HasPrefix(p, "/usr/libexec/") || strings.HasPrefix(p, "/sbin/")
}

func noBundleIDs(string) string { return "" }

func rowsByKey(rows []ProcessRow) map[string]ProcessRow {
	out := make(map[string]ProcessRow, len(rows))
	for _, r := range rows {
		out[r.Key] = r
	}
	return out
}

func TestGroupProcessesCPUFromThePreviousReading(t *testing.T) {
	prev := snapshotProcs([]procReading{
		{Pid: 1, Path: "/usr/bin/yes", UserNs: 1e9, Start: 7},
		{Pid: 2, Path: "/opt/homebrew/bin/node", UserNs: 5e9, SysNs: 1e9, Start: 8},
		{Pid: 3, Path: "/bin/zsh", UserNs: 3e9, Start: 9},
	})
	cur := snapshotProcs([]procReading{
		{Pid: 1, Path: "/usr/bin/yes", UserNs: 3e9, Start: 7},            // 2 s of CPU in 2 s: 100%
		{Pid: 2, Path: "/opt/homebrew/bin/node", UserNs: 1e9, Start: 99}, // pid reused: none this round
		{Pid: 3, Path: "/bin/zsh", UserNs: 2e9, Start: 9},                // counters went back: none
		{Pid: 4, Path: "/usr/bin/top", UserNs: 9e9, Start: 10},           // new: none
	})
	rows := groupProcesses(prev, cur, nil, nil, func(p int32) int32 { return p }, testSystemPath, noBundleIDs, 2*time.Second)
	got := rowsByKey(rows)
	if c := got["cli:yes"].CPU; c < 99.9 || c > 100.1 {
		t.Errorf("yes CPU = %v, want 100", c)
	}
	for _, k := range []string{"cli:node", "cli:zsh", "cli:top"} {
		if got[k].CPU != 0 {
			t.Errorf("%s CPU = %v, want 0", k, got[k].CPU)
		}
	}
}

// setProcessTiming shrinks the loop's waits and returns the restore.
func setProcessTiming(first, interval time.Duration) func() {
	f, i := processFirstDelay, processInterval
	processFirstDelay, processInterval = first, interval
	return func() { processFirstDelay, processInterval = f, i }
}

// fakeProcessSource counts ps runs; its CPU is always half busy.
func fakeProcessSource(ps *atomic.Int32) processSource {
	var ticks atomic.Uint64
	return processSource{
		procs: func() []procReading { return []procReading{{Pid: 1, Path: "/usr/bin/yes", Start: 1}} },
		others: func(context.Context) []otherProc {
			ps.Add(1)
			return []otherProc{{Pid: 9, CPU: 1, Memory: 1, Path: "/sbin/launchd"}}
		},
		apps:        func() []workspaceApp { return nil },
		responsible: func(p int32) int32 { return p },
		system:      testSystemPath,
		bundleID:    noBundleIDs,
		pressure:    func() uint64 { return 2 },
		cpu: func() (uint64, uint64) {
			n := ticks.Add(10)
			return n / 2, n
		},
	}
}

func TestWatchProcessesRunsPsAtTheStartAndEveryThirdSample(t *testing.T) {
	defer setProcessTiming(time.Millisecond, time.Millisecond)()
	var ps atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var samples []ProcessSample
	err := watchProcessesWith(ctx, func(s ProcessSample) error {
		samples = append(samples, s)
		if len(samples) == 7 {
			cancel()
		}
		return nil
	}, fakeProcessSource(&ps))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(samples) != 7 {
		t.Fatalf("got %d samples, want 7", len(samples))
	}
	if n := ps.Load(); n != 3 {
		t.Errorf("ps ran %d times for 7 samples, want 3 (start, 4th, 7th)", n)
	}
	s := samples[0]
	if s.Own != 1 || s.Others != 1 || s.Pressure != 2 || len(s.Rows) != 2 {
		t.Errorf("first sample = %+v", s)
	}
	if s.CPU < 49.9 || s.CPU > 50.1 {
		t.Errorf("CPU = %v, want 50", s.CPU)
	}
	n := ps.Load()
	time.Sleep(20 * time.Millisecond)
	if ps.Load() != n {
		t.Error("ps ran after the watch returned")
	}
}

func TestWatchProcessesStopsWhenSendFails(t *testing.T) {
	defer setProcessTiming(time.Millisecond, time.Millisecond)()
	var ps atomic.Int32
	closed := errors.New("channel closed")
	err := watchProcessesWith(context.Background(), func(ProcessSample) error { return closed }, fakeProcessSource(&ps))
	if !errors.Is(err, closed) {
		t.Fatalf("err = %v, want the send error", err)
	}
	if n := ps.Load(); n != 1 {
		t.Errorf("ps ran %d times, want only the first", n)
	}
}

func TestWatchProcessesStopsDuringTheFirstWait(t *testing.T) {
	defer setProcessTiming(time.Hour, time.Millisecond)()
	var ps atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sent := false
	err := watchProcessesWith(ctx, func(ProcessSample) error { sent = true; return nil }, fakeProcessSource(&ps))
	if !errors.Is(err, context.Canceled) || sent {
		t.Fatalf("err = %v, sent = %v; want canceled before any sample", err, sent)
	}
}
