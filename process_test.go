package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParsePsOutputKeepsOtherAccountsWithShortNames(t *testing.T) {
	out := []byte(strings.Join([]string{
		"    1     0   0.3  12288 launchd",
		"  414    88  40.2 138240 WindowServer",
		" 7045     0  10.8  53872 wdavdaemon",
		" 7046     0   2.5   1024 Google Chrome He",
		" 9001   501  99.0   4096 yes",
		"    0     0   5.0      0 kernel_task",
		"garbage line",
		"   12     0     x      1 badcpu",
		"",
	}, "\n"))
	got := parsePsOutput(out, 501)
	want := []otherProc{
		{Pid: 1, UID: 0, CPU: 0.3, Memory: 12288 * 1024, Path: "launchd", Name: "launchd"},
		{Pid: 414, UID: 88, CPU: 40.2, Memory: 138240 * 1024, Path: "WindowServer", Name: "WindowServer"},
		{Pid: 7045, UID: 0, CPU: 10.8, Memory: 53872 * 1024, Path: "wdavdaemon", Name: "wdavdaemon"},
		{Pid: 7046, UID: 0, CPU: 2.5, Memory: 1024 * 1024, Path: "Google Chrome He", Name: "Google Chrome He"},
		{Pid: 0, UID: 0, CPU: 5.0, Memory: 0, Path: "kernel_task", Name: "kernel_task"},
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
		{Pid: 2, Path: "/opt/homebrew/bin/node", UserNs: 9e9, Start: 99}, // pid reused despite a larger counter: none this round
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

func TestGroupProcessesSkipsOwnProcessesWithoutPaths(t *testing.T) {
	cur := snapshotProcs([]procReading{{Pid: 1, Footprint: 1024}, {Pid: 2, Path: "/usr/bin/yes", Footprint: 2048}})
	others := []otherProc{{Pid: 3, Path: "kernel_task", Memory: 4096}}
	rows := groupProcesses(cur, cur, others, nil, func(p int32) int32 { return p }, testSystemPath, noBundleIDs, 0)
	if len(rows) != 2 || rows[0].Key != "cli:yes" || rows[1].Key != "sys:kernel_task" {
		t.Fatalf("rows = %+v, want yes and kernel_task without the own pathless process", rows)
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
	defer cancel()
	src := fakeProcessSource(&ps)
	src.others = func(context.Context) []otherProc {
		ps.Add(1)
		cancel()
		return nil
	}
	sent := false
	err := watchProcessesWith(ctx, func(ProcessSample) error { sent = true; return nil }, src)
	if !errors.Is(err, context.Canceled) || sent {
		t.Fatalf("err = %v, sent = %v; want canceled before any sample", err, sent)
	}
	if ps.Load() != 1 {
		t.Fatalf("others called %d times, want the first read before sleepCtx", ps.Load())
	}
}

func TestParsePsOutputPreservesChineseName(t *testing.T) {
	got := parsePsOutput([]byte("7046 0 2.5 1024 语雀\n"), 501)
	want := otherProc{Pid: 7046, UID: 0, CPU: 2.5, Memory: 1024 * 1024, Path: "语雀", Name: "语雀"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestGroupProcessesPrefersWorkspaceBundleIDs(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "Workspace.app")
	fallback := filepath.Join(t.TempDir(), "Fallback.app")
	cur := snapshotProcs([]procReading{
		{Pid: 1, Path: filepath.Join(bundle, "Contents", "MacOS", "Workspace")},
		{Pid: 2, Path: filepath.Join(fallback, "Contents", "MacOS", "Fallback")},
	})
	apps := []workspaceApp{
		{Path: strings.ToUpper(bundle), BundleID: "COM.Example.Workspace"},
		{Path: fallback}, // A workspace entry without an ID still needs the fallback.
	}
	var reads []string
	rows := groupProcesses(cur, cur, nil, apps, func(p int32) int32 { return p }, testSystemPath, func(path string) string {
		reads = append(reads, path)
		return "COM.Example.Fallback"
	}, 0)
	got := rowsByKey(rows)
	if id := got["app:"+strings.ToLower(bundle)].BundleID; id != "com.example.workspace" {
		t.Errorf("workspace ID = %q", id)
	}
	if id := got["app:"+strings.ToLower(fallback)].BundleID; id != "com.example.fallback" {
		t.Errorf("fallback ID = %q", id)
	}
	if len(reads) != 1 || reads[0] != fallback {
		t.Errorf("Info.plist reads = %v, want only %s", reads, fallback)
	}
}

func TestWatchProcessesCachesBundleIDsAcrossWatches(t *testing.T) {
	resetProcessBundleIDs(t)
	defer setProcessTiming(time.Millisecond, time.Millisecond)()
	for _, id := range []string{"com.example.cached", ""} {
		t.Run("id="+id, func(t *testing.T) {
			bundle := filepath.Join(t.TempDir(), "Cached.app")
			var ps, reads atomic.Int32
			src := fakeProcessSource(&ps)
			src.procs = func() []procReading {
				return []procReading{{Pid: 1, Path: filepath.Join(bundle, "Contents", "MacOS", "Cached")}}
			}
			src.bundleID = func(string) string { reads.Add(1); return id }
			closed := errors.New("finished sample")
			var wg sync.WaitGroup
			for range 8 {
				wg.Go(func() {
					err := watchProcessesWith(context.Background(), func(s ProcessSample) error {
						if got := rowsByKey(s.Rows)["app:"+strings.ToLower(bundle)].BundleID; got != id {
							t.Errorf("bundle ID = %q, want %q", got, id)
						}
						return closed
					}, src)
					if !errors.Is(err, closed) {
						t.Errorf("watch error = %v", err)
					}
				})
			}
			wg.Wait()
			if n := reads.Load(); n != 1 {
				t.Errorf("bundle ID read %d times across watches, want 1", n)
			}
		})
	}
}

func resetProcessBundleIDs(t *testing.T) {
	t.Helper()
	processBundleIDs.Lock()
	old := processBundleIDs.values
	processBundleIDs.values = make(map[string]*processBundleIDEntry)
	processBundleIDs.Unlock()
	t.Cleanup(func() {
		processBundleIDs.Lock()
		processBundleIDs.values = old
		processBundleIDs.Unlock()
	})
}

func TestWatchProcessesCanceledBeforeReading(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ps atomic.Int32
	src := fakeProcessSource(&ps)
	reads := 0
	src.procs = func() []procReading { reads++; return nil }
	err := watchProcessesWith(ctx, func(ProcessSample) error { t.Error("sent after cancellation"); return nil }, src)
	if !errors.Is(err, context.Canceled) || reads != 0 || ps.Load() != 0 {
		t.Fatalf("err=%v procs=%d others=%d, want canceled and no reads", err, reads, ps.Load())
	}
}

func TestWatchProcessesStopsAfterPsCancels(t *testing.T) {
	defer setProcessTiming(time.Millisecond, time.Millisecond)()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ps atomic.Int32
	src := fakeProcessSource(&ps)
	reads, sent := 0, 0
	src.procs = func() []procReading { reads++; return nil }
	src.others = func(context.Context) []otherProc {
		if ps.Add(1) == 2 {
			cancel()
		}
		return nil
	}
	err := watchProcessesWith(ctx, func(ProcessSample) error { sent++; return nil }, src)
	if !errors.Is(err, context.Canceled) || reads != 4 || sent != 3 || ps.Load() != 2 {
		t.Fatalf("err=%v procs=%d sent=%d ps=%d, want canceled, 4, 3, 2", err, reads, sent, ps.Load())
	}
}

func TestWatchProcessesKeepsOthersWhenPsFails(t *testing.T) {
	defer setProcessTiming(time.Millisecond, time.Millisecond)()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var ps atomic.Int32
	src := fakeProcessSource(&ps)
	src.others = func(context.Context) []otherProc {
		switch ps.Add(1) {
		case 1:
			return []otherProc{{Pid: 9, CPU: 7, Memory: 42, Path: "/sbin/launchd"}}
		case 2:
			return nil // failure: keep the previous successful reading
		default:
			return []otherProc{} // success: no other accounts' processes remain
		}
	}
	samples := 0
	err := watchProcessesWith(ctx, func(s ProcessSample) error {
		samples++
		want := 1
		if samples == 7 {
			want = 0
			cancel()
		}
		if s.Others != want || len(s.Rows) != 1+want {
			t.Errorf("sample %d: others=%d rows=%d, want %d, %d", samples, s.Others, len(s.Rows), want, 1+want)
		}
		if want == 1 {
			r := rowsByKey(s.Rows)["sys:launchd"]
			if r.CPU != 7 || r.Memory != 42 || !r.Estimated {
				t.Errorf("sample %d lost the previous estimate: %+v", samples, r)
			}
		}
		return nil
	}, src)
	if !errors.Is(err, context.Canceled) || samples != 7 {
		t.Fatalf("err=%v samples=%d", err, samples)
	}
}

func TestWatchProcessesUsesReadStartForBothCPUEndpoints(t *testing.T) {
	defer setProcessTiming(time.Millisecond, time.Millisecond)()
	var ps atomic.Int32
	src := fakeProcessSource(&ps)
	at := time.Unix(1, 0)
	src.now = func() time.Time { return at }
	reads := uint64(0)
	src.procs = func() []procReading {
		reads++
		at = at.Add(500 * time.Millisecond)
		return []procReading{{Pid: 1, Path: "/usr/bin/yes", Start: 1, UserNs: reads * 1e9}}
	}
	src.others = func(context.Context) []otherProc { at = at.Add(750 * time.Millisecond); return nil }
	src.pressure = func() uint64 { at = at.Add(time.Second); return 0 }
	closed := errors.New("finished two samples")
	var cpus []float64
	err := watchProcessesWith(context.Background(), func(s ProcessSample) error {
		cpus = append(cpus, s.Rows[0].CPU)
		if len(cpus) == 2 {
			return closed
		}
		return nil
	}, src)
	if !errors.Is(err, closed) || len(cpus) != 2 {
		t.Fatalf("err=%v CPU=%v", err, cpus)
	}
	if cpus[0] < 79.99 || cpus[0] > 80.01 || cpus[1] < 66.66 || cpus[1] > 66.67 {
		t.Fatalf("CPU=%v, want [80, 66.6667] from read-start intervals", cpus)
	}
}

func TestQuitForceUnsupportedOffMacOS(t *testing.T) {
	if processSupported() {
		t.Skip("non-macOS binding contract")
	}
	ok, err := (Cleaner{}).ForceQuitApp("")
	ce, isCode := err.(codeError)
	if ok || !isCode || ce.Code != "unsupported" {
		t.Fatalf("got (%v, %v), want false and unsupported", ok, err)
	}
}

func TestWatchProcessesUnsupportedOffMacOS(t *testing.T) {
	if processSupported() {
		t.Skip("non-macOS binding contract")
	}
	err := (Cleaner{}).WatchProcesses(context.Background(), nil)
	ce, isCode := err.(codeError)
	if !isCode || ce.Code != "unsupported" {
		t.Fatalf("got %v, want unsupported", err)
	}
}

func TestBundleIDSlowReadDoesNotBlockOtherPaths(t *testing.T) {
	resetProcessBundleIDs(t)
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan string, 1)
	go func() {
		done <- processBundleID("slow.app", func(string) string {
			close(started)
			<-release
			return "com.example.slow"
		})
	}()
	defer func() {
		close(release)
		if got := <-done; got != "com.example.slow" {
			t.Errorf("slow ID = %q", got)
		}
	}()
	<-started
	// Detect the old deadlock without making the test depend on a timeout.
	if !processBundleIDs.TryLock() {
		t.Fatal("a blocked bundle read holds the global lock and blocks unrelated paths")
	}
	processBundleIDs.Unlock()
	if got := processBundleID("fast.app", func(string) string { return "com.example.fast" }); got != "com.example.fast" {
		t.Fatalf("unrelated bundle lookup = %q", got)
	}
}

func TestWatchProcessesExpiresOthersAfterTwoConsecutiveFailures(t *testing.T) {
	defer setProcessTiming(time.Millisecond, time.Millisecond)()
	old := []otherProc{{Pid: 9, CPU: 7, Memory: 42, Path: "/sbin/launchd"}}
	fresh := []otherProc{{Pid: 9, CPU: 11, Memory: 99, Path: "/sbin/launchd"}}
	for _, tc := range []struct {
		name     string
		readings [][]otherProc
		wantCPU  []float64 // one expected estimate per ps refresh period; zero means cleared
	}{
		{"two failures expire", [][]otherProc{old, nil, nil}, []float64{7, 7, 0}},
		{"success resets streak", [][]otherProc{old, nil, fresh, nil, nil}, []float64{7, 7, 11, 11, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var ps atomic.Int32
			src := fakeProcessSource(&ps)
			src.others = func(context.Context) []otherProc {
				return tc.readings[int(ps.Add(1))-1]
			}
			samples := 0
			err := watchProcessesWith(ctx, func(s ProcessSample) error {
				wantCPU := tc.wantCPU[samples/3]
				samples++
				if wantCPU == 0 {
					if s.Others != 0 || len(s.Rows) != 1 {
						t.Errorf("sample %d retained stale processes: %+v", samples, s)
					}
				} else {
					r := rowsByKey(s.Rows)["sys:launchd"]
					if s.Others != 1 || r.CPU != wantCPU || !r.Estimated {
						t.Errorf("sample %d: others=%d row=%+v, want CPU %v", samples, s.Others, r, wantCPU)
					}
				}
				if samples == 3*(len(tc.readings)-1)+1 {
					cancel()
				}
				return nil
			}, src)
			if !errors.Is(err, context.Canceled) || int(ps.Load()) != len(tc.readings) {
				t.Fatalf("err=%v ps=%d", err, ps.Load())
			}
		})
	}
}
