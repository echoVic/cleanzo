package main

import (
	"context"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// TestWindowsStatusLayouts checks Windows SDK layouts on both amd64 and arm64 CI builds.
func TestWindowsStatusLayouts(t *testing.T) {
	for _, tt := range []struct {
		name      string
		got, want uintptr
	}{
		{"MEMORYSTATUSEX", unsafe.Sizeof(windowsMemoryStatus{}), 64},
		{"PERFORMANCE_INFORMATION", unsafe.Sizeof(windowsPerformanceInfo{}), 104},
		{"PROCESS_MEMORY_COUNTERS_EX2", unsafe.Sizeof(windowsProcessMemory{}), 96},
		{"SYSTEM_POWER_STATUS", unsafe.Sizeof(windowsPowerStatus{}), 12},
	} {
		if tt.got != tt.want {
			t.Errorf("%s size=%d want %d", tt.name, tt.got, tt.want)
		}
	}
	if unsafe.Offsetof(windowsPerformanceInfo{}.CommitTotal) != 8 || unsafe.Offsetof(windowsPerformanceInfo{}.HandleCount) != 88 || unsafe.Offsetof(windowsProcessMemory{}.PrivateWorkingSetSize) != 80 {
		t.Fatal("SDK offsets differ")
	}
}

// TestWindowsStatusReadings checks live probes without depending on a battery or desktop size.
func TestWindowsStatusReadings(t *testing.T) {
	source := realStatusSource()
	var mem windowsMemoryStatus
	mem.Length = uint32(unsafe.Sizeof(mem))
	if ok, _, err := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&mem))); ok == 0 {
		t.Fatal(err)
	}
	if got := source.memory(); got.Total != mem.TotalPhys || got.Total == 0 || got.Used > got.Total {
		t.Fatalf("memory=%+v, physical=%d", got, mem.TotalPhys)
	}
	found := false
	for _, p := range source.procs() {
		if p.Pid == int32(os.Getpid()) {
			found = true
			if p.Footprint == 0 || p.Start == 0 {
				t.Fatalf("self=%+v", p)
			}
		}
	}
	if !found {
		t.Fatal("test process missing")
	}
	busy, total := source.cpu()
	deadline := time.Now().Add(2 * time.Second)
	for {
		b, n := source.cpu()
		if n > total {
			if b < busy || b > n {
				t.Fatal("invalid CPU counters")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("CPU ticks did not increase")
		}
		time.Sleep(10 * time.Millisecond)
	}
	info := (Cleaner{}).Info()
	if info.Chip == "" || !strings.HasPrefix(info.OSVersion, "Windows") || info.Memory != mem.TotalPhys || info.Cores == 0 || info.BootTime <= 0 || info.BootTime > time.Now().Unix() {
		t.Fatalf("Info=%+v", info)
	}
	if d := source.disk(); d.Total <= 0 || d.Free < 0 || d.Used < 0 || d.Total != d.Free+d.Used {
		t.Fatalf("disk=%+v", d)
	}
	if b := source.battery(context.Background()); b != nil && (b.Percent < 0 || b.Percent > 100 || b.Cycles != 0 || b.MaxCapacity != 0 || b.Condition != "") {
		t.Fatalf("battery=%+v", b)
	}
}

// TestWindowsTerminateRefusesExplorerAndSelf checks refusal even without Explorer running.
func TestWindowsTerminateRefusesExplorerAndSelf(t *testing.T) {
	if workspaceTerminate(int32(os.Getpid())) {
		t.Fatal("quit self accepted")
	}
	saved := statusSnapshotProcesses
	statusSnapshotProcesses = func() ([]procInfo, error) {
		return []procInfo{{Pid: 12345, Exe: `C:\Windows\explorer.exe`, Name: "explorer.exe"}}, nil
	}
	t.Cleanup(func() { statusSnapshotProcesses = saved })
	if workspaceTerminate(12345) {
		t.Fatal("quit Explorer accepted")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	statusSnapshotProcesses = func() ([]procInfo, error) { return []procInfo{{Pid: 12346, Exe: exe, Name: filepath.Base(exe)}}, nil }
	if workspaceTerminate(12346) {
		t.Fatal("quit another Cleanzo instance accepted")
	}

}

// TestWindowsWindowedAncestors covers nearest parents, PID reuse, unknown dates and cycles.
func TestWindowsWindowedAncestors(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	procs := []procInfo{{Pid: 1, Created: base}, {Pid: 2, PPid: 1, Created: base.Add(time.Second)}, {Pid: 3, PPid: 2, Created: base.Add(2 * time.Second)}, {Pid: 4, PPid: 3, Created: base}, {Pid: 5, PPid: 1}, {Pid: 6, PPid: 7, Created: base}, {Pid: 7, PPid: 6, Created: base}}
	got := windowedAncestors(procs, map[uint32]bool{1: true, 2: true})
	if got[3] != 2 || got[2] != 2 || got[4] != 0 || got[5] != 0 || got[6] != 0 {
		t.Fatalf("ancestors=%v", got)
	}
}

// TestWindowsVersionBuildWins ignores misleading registry ProductName values.
func TestWindowsVersionBuildWins(t *testing.T) {
	if windowsVersion("Windows 10 Pro", "24H2", "26100") != "Windows 11 24H2 (26100)" || windowsVersion("Windows 10 Pro", "22H2", "19045") != "Windows 10 22H2 (19045)" {
		t.Fatal("incorrect Windows build branding")
	}
}

// Compile-time bounds make each SDK size assertion run during both Windows architecture vet jobs.
var (
	_ [64 - unsafe.Sizeof(windowsMemoryStatus{})]byte
	_ [unsafe.Sizeof(windowsMemoryStatus{}) - 64]byte
	_ [104 - unsafe.Sizeof(windowsPerformanceInfo{})]byte
	_ [unsafe.Sizeof(windowsPerformanceInfo{}) - 104]byte
	_ [96 - unsafe.Sizeof(windowsProcessMemory{})]byte
	_ [unsafe.Sizeof(windowsProcessMemory{}) - 96]byte
	_ [12 - unsafe.Sizeof(windowsPowerStatus{})]byte
	_ [unsafe.Sizeof(windowsPowerStatus{}) - 12]byte
)

// TestWindowsWindowClassification uses tiny native windows, independent of the runner's resolution.
func TestWindowsWindowClassification(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	create := statusUser.NewProc("CreateWindowExW")
	destroy := statusUser.NewProc("DestroyWindow")
	class := windows.StringToUTF16Ptr("STATIC")
	var created []windows.HWND
	defer func() {
		for i := len(created) - 1; i >= 0; i-- {
			destroy.Call(uintptr(created[i]))
		}
	}()
	makeWindow := func(style, extended, owner uintptr) windows.HWND {
		t.Helper()
		hwnd, _, err := create.Call(extended, uintptr(unsafe.Pointer(class)), 0, style, 0, 0, 16, 16, owner, 0, 0, 0)
		if hwnd == 0 {
			t.Fatalf("CreateWindowExW: %v", err)
		}
		created = append(created, windows.HWND(hwnd))
		return windows.HWND(hwnd)
	}
	regular := makeWindow(0x90000000, 0, 0) // WS_POPUP | WS_VISIBLE, winuser.h
	hidden := makeWindow(0x80000000, 0, 0)
	tool := makeWindow(0x90000000, 0x80, 0)
	owned := makeWindow(0x90000000, 0, uintptr(regular))
	child := makeWindow(0x50000000, 0, uintptr(regular)) // WS_CHILD | WS_VISIBLE
	if !regularWindow(regular) || regularWindow(hidden) || regularWindow(tool) || regularWindow(owned) {
		t.Fatal("incorrect GUI classification")
	}
	seen := map[windows.HWND]uint32{}
	if !enumTopWindows(func(hwnd windows.HWND, pid uint32) { seen[hwnd] = pid }) || seen[regular] != uint32(os.Getpid()) {
		t.Fatal("native top-level window not enumerated")
	}
	savedPost := postWindowClose
	defer func() { postWindowClose = savedPost }()
	posted := map[windows.HWND]bool{}
	postWindowClose = func(hwnd windows.HWND) bool { posted[hwnd] = true; return true }
	if !closeTopWindows(uint32(os.Getpid())) || !posted[regular] || !posted[hidden] || !posted[tool] || !posted[owned] || posted[child] {
		t.Fatalf("WM_CLOSE targets=%v", posted)
	}
	for hwnd := range posted {
		if seen[hwnd] != uint32(os.Getpid()) {
			t.Fatal("WM_CLOSE sent to another process")
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	apps, ancestors := statusApps([]procInfo{{Pid: uint32(os.Getpid()), Exe: exe}})
	if len(apps) != 1 || !apps[0].Regular || apps[0].Name == "" || apps[0].Path != exe || apps[0].BundleID != "" || ancestors[uint32(os.Getpid())] != uint32(os.Getpid()) {
		t.Fatalf("apps=%+v ancestors=%v", apps, ancestors)
	}

}

// TestWindowsAppUsageGroupsChildren checks memory/CPU attribution and cached GUI names end to end.
func TestWindowsAppUsageGroupsChildren(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Example.exe")
	apps := []workspaceApp{{Pid: 900001, Path: path, Name: "Example Product", Regular: true}}
	procs := []procReading{{Pid: 900001, Path: path, Footprint: 20, UserNs: 200, Start: 1}, {Pid: 900002, Path: path + ".worker", Footprint: 30, UserNs: 300, Start: 2}, {Pid: 900003, Path: path + ".daemon", Footprint: 50}}
	prev := map[int32]procSample{900001: {Start: 1, UserNs: 100}, 900002: {Start: 2, UserNs: 100}}
	ancestors := map[uint32]uint32{900001: 900001, 900002: 900001}
	rows := mergeApps(prev, procs, apps, nativeResponsiblePid, time.Nanosecond*100, func(r procReading, byPid map[int32]procReading, _ func(int32) int32) (string, string) {
		return windowsAppGroup(r, byPid, ancestors)
	})
	if len(rows) != 1 || rows[0].Name != "Example Product" || rows[0].Memory != 50 || rows[0].CPU != 300 || rows[0].Procs != 2 || rows[0].Path != path || !rows[0].Quit {
		t.Fatalf("usage=%+v", rows)
	}
}

// enumerationTestWindow keeps a stable top-level window alive until every visitor has returned.
func enumerationTestWindow(t *testing.T) windows.HWND {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	class := windows.StringToUTF16Ptr("STATIC")
	hwnd, _, err := statusUser.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0, 0x80000000, 0, 0, 16, 16, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW: %v", err)
	}
	t.Cleanup(func() { statusUser.NewProc("DestroyWindow").Call(hwnd) })
	return windows.HWND(hwnd)
}

// growEnumerationStack moves Go callback stacks while Windows retains only the enumeration handle.
//
//go:noinline
func growEnumerationStack(depth int) uint64 {
	var words [128]uint64
	words[depth] = uint64(depth + 1)
	if depth > 0 {
		return words[depth] + growEnumerationStack(depth-1)
	}
	return words[0]
}

// TestWindowsConcurrentWindowEnumeration catches visitor mixups across callbacks and moving Go stacks.
func TestWindowsConcurrentWindowEnumeration(t *testing.T) {
	fixture := enumerationTestWindow(t)
	const workers, iterations = 8, 50
	start := make(chan struct{})
	results := make(chan error, workers)
	var wg sync.WaitGroup
	for worker := range workers {
		wg.Go(func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			owner := windows.GetCurrentThreadId()
			seen := map[windows.HWND]int{}
			callbacks := 0
			var wrongVisitor atomic.Bool
			<-start
			for iteration := range iterations {
				if !enumTopWindows(func(hwnd windows.HWND, _ uint32) {
					if hwnd != fixture {
						return
					} // other applications may change windows during CI
					if windows.GetCurrentThreadId() != owner {
						wrongVisitor.Store(true)
						return
					}
					_ = growEnumerationStack(32)
					runtime.Gosched()
					seen[hwnd]++
					callbacks++
				}) {
					results <- fmt.Errorf("worker %d: enumeration %d failed", worker, iteration)
					return
				}
			}
			if wrongVisitor.Load() || callbacks != iterations || seen[fixture] != iterations || len(seen) != 1 {
				results <- fmt.Errorf("worker %d: wrong visitor=%t callbacks=%d seen=%v", worker, wrongVisitor.Load(), callbacks, seen)
			}
		})
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
	enumWindowVisitors.RLock()
	remaining := len(enumWindowVisitors.visitors)
	enumWindowVisitors.RUnlock()
	if remaining != 0 {
		t.Fatalf("enumeration retained %d visitors", remaining)
	}
}

// TestWindowsWindowEnumerationReentrant checks that a visitor can enumerate again without blocking other handles.
func TestWindowsWindowEnumerationReentrant(t *testing.T) {
	fixture := enumerationTestWindow(t)
	outer, inner := 0, 0
	if !enumTopWindows(func(hwnd windows.HWND, _ uint32) {
		if hwnd != fixture {
			return
		}
		outer++
		if !enumTopWindows(func(nested windows.HWND, _ uint32) {
			if nested == fixture {
				inner++
			}
		}) {
			t.Error("nested enumeration failed")
		}
	}) || outer != 1 || inner != 1 {
		t.Fatalf("callbacks: outer=%d inner=%d", outer, inner)
	}
}
