package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// vmStats64 keeps the shared memory conversion input available on Windows.
type vmStats64 struct {
	Free, Active, Inactive, Wire     uint32
	ZeroFill, Reactivations          uint64
	Pageins, Pageouts                uint64
	Faults, CowFaults, Lookups, Hits uint64
	Purges                           uint64
	Purgeable, Speculative           uint32
	Decompressions, Compressions     uint64
	Swapins, Swapouts                uint64
	CompressorPages, Throttled       uint32
	ExternalPages, InternalPages     uint32
	TotalUncompressed                uint64
	Swapped                          uint64
}

// hostCPULoad keeps the shared CPU conversion input available on Windows.
type hostCPULoad struct{ Ticks [4]uint32 }

// procReading is the platform-neutral input to process attribution.
type procReading struct {
	Pid       int32
	Path      string
	Footprint uint64
	UserNs    uint64
	SysNs     uint64
	Start     uint64
}

// nativeVMStats reports an unavailable memory probe.
func nativeVMStats() (vmStats64, bool) { return vmStats64{}, false }

// nativeCPUTicks reports an unavailable CPU probe.
func nativeCPUTicks() (hostCPULoad, bool) { return hostCPULoad{}, false }

// windowsProcessMemory is PROCESS_MEMORY_COUNTERS_EX2 from psapi.h (96 bytes on amd64/arm64).
type windowsProcessMemory struct {
	Size, PageFaultCount                                                                 uint32
	PeakWorkingSetSize, WorkingSetSize, QuotaPeakPagedPoolUsage, QuotaPagedPoolUsage     uintptr
	QuotaPeakNonPagedPoolUsage, QuotaNonPagedPoolUsage, PagefileUsage, PeakPagefileUsage uintptr
	PrivateUsage, PrivateWorkingSetSize, SharedCommitUsage                               uintptr
}

// Missing user32 wrappers use pointer-sized HWND and LONG_PTR arguments from winuser.h.
var (
	statusUser           = windows.NewLazySystemDLL("user32.dll")
	getProcessMemoryInfo = statusKernel.NewProc("K32GetProcessMemoryInfo")
	getWindow            = statusUser.NewProc("GetWindow")
	getWindowLongPtr     = statusUser.NewProc("GetWindowLongPtrW")
	postMessage          = statusUser.NewProc("PostMessageW")
	enumWindowsProc      = statusUser.NewProc("EnumWindows")
	dwmWindowAttribute   = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmGetWindowAttribute")
)

func init() {
	statusWindowsRoot, _ = windows.GetWindowsDirectory()
	statusPackageFamilyName = processPackageFamilyName
}

// statusSnapshotProcesses permits status failures to be tested independently of cleanup's probe.
var statusSnapshotProcesses = processSnapshot

// nativeProcs reads one current-session snapshot and the readable process counters.
func nativeProcs() []procReading {
	procs, err := statusSnapshotProcesses()
	if err != nil {
		return nil
	}
	return windowsProcReadings(procs)
}

// windowsProcReadings gathers private working set and process CPU time in nanoseconds.
func windowsProcReadings(procs []procInfo) []procReading {
	out := make([]procReading, 0, len(procs))
	for _, p := range procs {
		if p.Exe == "" {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, p.Pid)
		if err != nil {
			h, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, p.Pid)
		}
		if err != nil {
			continue
		}
		r := procReading{Pid: int32(p.Pid), Path: p.Exe}
		var created, exited, kernel, user windows.Filetime
		if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
			r.Start = filetimeTicks(created)
			r.UserNs = filetimeTicks(user) * 100
			r.SysNs = filetimeTicks(kernel) * 100
		}
		m := windowsProcessMemory{Size: uint32(unsafe.Sizeof(windowsProcessMemory{}))}
		ok, _, _ := getProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&m)), uintptr(m.Size))
		if ok == 0 {
			// Older systems only accept PROCESS_MEMORY_COUNTERS, the 72-byte prefix.
			m = windowsProcessMemory{Size: 72}
			ok, _, _ = getProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&m)), uintptr(m.Size))
		}
		if ok != 0 {
			r.Footprint = uint64(m.PrivateWorkingSetSize)
			if r.Footprint == 0 {
				r.Footprint = uint64(m.WorkingSetSize)
			}
		}
		windows.CloseHandle(h)
		out = append(out, r)
	}
	return out
}

// nativeResponsiblePid is unused when the source supplies snapshot-local GUI ancestry.
func nativeResponsiblePid(pid int32) int32 { return -1 }

// nativeMachineInfo reads registry identity and computes the boot time from monotonic uptime.
func nativeMachineInfo() (string, uint64, uint64, string, int64) {
	cpu := windowsRegistryString(`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "ProcessorNameString")
	root := `SOFTWARE\Microsoft\Windows NT\CurrentVersion`
	version := windowsVersion(windowsRegistryString(root, "ProductName"), windowsRegistryString(root, "DisplayVersion"), windowsRegistryString(root, "CurrentBuild"))
	return strings.TrimSpace(cpu), readMemoryStatus().Total, uint64(windows.GetActiveProcessorCount(0xffff)), version, time.Now().Add(-windows.DurationSinceBoot()).Unix()
}

// windowsRegistryString returns an unknown string if the read-only machine key is unavailable.
func windowsRegistryString(path, name string) string {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, _ := key.GetStringValue(name)
	return value
}

// enumWindowVisitors roots each visitor in Go memory while Windows holds only its integer id.
var enumWindowVisitors = struct {
	sync.RWMutex
	next     atomic.Uintptr
	visitors map[uintptr]func(windows.HWND, uint32)
}{visitors: make(map[uintptr]func(windows.HWND, uint32))}

// enumWindowsCallback is registered once and resolves the integer lParam without converting it to a pointer.
var enumWindowsCallback = windows.NewCallback(func(hwnd windows.HWND, id uintptr) uintptr {
	enumWindowVisitors.RLock()
	visit := enumWindowVisitors.visitors[id]
	enumWindowVisitors.RUnlock()
	if visit == nil {
		return 0
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err == nil {
		visit(hwnd, pid)
	}
	return 1
})

// enumTopWindows isolates concurrent visitors and removes their handles when the synchronous call returns.
func enumTopWindows(visit func(windows.HWND, uint32)) bool {
	id := enumWindowVisitors.next.Add(1)
	enumWindowVisitors.Lock()
	enumWindowVisitors.visitors[id] = visit
	enumWindowVisitors.Unlock()
	defer func() {
		enumWindowVisitors.Lock()
		delete(enumWindowVisitors.visitors, id)
		enumWindowVisitors.Unlock()
	}()
	// The x/sys wrapper takes unsafe.Pointer for lParam; call directly to keep the id an integer.
	result, _, _ := syscall.SyscallN(enumWindowsProc.Addr(), enumWindowsCallback, id)
	return result != 0
}

// regularWindow requires a visible, unowned, non-tool, non-cloaked top-level window.
func regularWindow(hwnd windows.HWND) bool {
	if !windows.IsWindowVisible(hwnd) {
		return false
	}
	owner, _, _ := getWindow.Call(uintptr(hwnd), 4) // GW_OWNER
	// GWL_EXSTYLE = -20, WS_EX_TOOLWINDOW = 0x80 (winuser.h).
	style, _, _ := getWindowLongPtr.Call(uintptr(hwnd), ^uintptr(19))
	if owner != 0 || style&0x80 != 0 {
		return false
	}
	// DWMWA_CLOAKED = 14 (dwmapi.h). Failed queries mean "not cloaked".
	var cloaked uint32
	if dwmWindowAttribute.Find() == nil {
		result, _, _ := dwmWindowAttribute.Call(uintptr(hwnd), 14, uintptr(unsafe.Pointer(&cloaked)), unsafe.Sizeof(cloaked))
		if result == 0 && cloaked != 0 {
			return false
		}
	}
	return true
}

// statusApps names only windowed applications and shares their identities with ancestry resolution.
func statusApps(procs []procInfo) ([]workspaceApp, map[uint32]uint32) {
	windowed := map[uint32]bool{}
	if !enumTopWindows(func(hwnd windows.HWND, pid uint32) {
		if regularWindow(hwnd) {
			windowed[pid] = true
		}
	}) {
		return nil, nil
	}
	var apps []workspaceApp
	for _, p := range procs {
		if !windowed[p.Pid] || p.Exe == "" {
			continue
		}
		name := processProduct(p.Exe)
		if name == "" {
			name = filepath.Base(p.Exe)
		}
		apps = append(apps, workspaceApp{Pid: int32(p.Pid), Path: p.Exe, Name: name, Regular: true})
	}
	return apps, windowedAncestors(procs, windowed)
}

// windowedAncestors resolves the nearest GUI parent, rejecting missing dates, PID reuse and cycles.
func windowedAncestors(procs []procInfo, windowed map[uint32]bool) map[uint32]uint32 {
	byPid := make(map[uint32]procInfo, len(procs))
	for _, p := range procs {
		byPid[p.Pid] = p
	}
	out := make(map[uint32]uint32, len(procs))
	for _, p := range procs {
		current := p
		seen := map[uint32]bool{}
		for current.Pid != 0 && !seen[current.Pid] {
			seen[current.Pid] = true
			if windowed[current.Pid] {
				out[p.Pid] = current.Pid
				break
			}
			parent, ok := byPid[current.PPid]
			if !ok || current.Created.IsZero() || parent.Created.IsZero() || !parent.Created.Before(current.Created) {
				break
			}
			current = parent
		}
	}
	return out
}

// workspaceList rechecks GUI apps at quit time.
func workspaceList() []workspaceApp {
	procs, err := statusSnapshotProcesses()
	if err != nil {
		return nil
	}
	apps, _ := statusApps(procs)
	return apps
}

// workspaceTerminate asks this live process's user-closeable windows to close, without force termination.
func workspaceTerminate(pid int32) bool {
	if pid <= 0 || pid == int32(os.Getpid()) {
		return false
	}
	procs, err := statusSnapshotProcesses()
	if err != nil {
		return false
	}
	var target *procInfo
	for i := range procs {
		if int32(procs[i].Pid) == pid {
			target = &procs[i]
			break
		}
	}
	if target == nil || target.Exe == "" || strings.EqualFold(target.Name, "explorer.exe") || statusAppExcluded(workspaceApp{Pid: pid, Path: target.Exe}) {
		return false
	}
	// Keep the process object alive and recheck its birth time to guard PID reuse during enumeration.
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil || target.Created.IsZero() || !time.Unix(0, created.Nanoseconds()).Equal(target.Created) {
		return false
	}
	return closeTopWindows(uint32(pid))
}

// nativeSysctlString reports an unavailable Darwin sysctl.
func nativeSysctlString(name string) string { return "" }

// nativeSysctlUint64 reports an unavailable Darwin sysctl.
func nativeSysctlUint64(name string) uint64 { return 0 }

// nativeSysctlRaw reports an unavailable Darwin sysctl.
func nativeSysctlRaw(name string) []byte { return nil }

// windowsAppGroup attributes only processes with a live windowed identity in this sample.
func windowsAppGroup(r procReading, byPid map[int32]procReading, ancestors map[uint32]uint32) (string, string) {
	if parent, ok := byPid[int32(ancestors[uint32(r.Pid)])]; ok && parent.Path != "" && !statusProcessExcluded(parent.Pid, parent.Path) {
		return "app:" + strings.ToLower(parent.Path), parent.Path
	}
	return "", ""
}

// postWindowClose posts the caption X / Alt+F4 request without waiting for a response.
var postWindowClose = func(hwnd windows.HWND) bool {
	ok, _, _ := postMessage.Call(uintptr(hwnd), 0x0112, 0xf060, 0) // WM_SYSCOMMAND / SC_CLOSE (winuser.h)
	return ok != 0
}

// closeTopWindows never falls back to internal windows if no user-closeable window exists.
func closeTopWindows(pid uint32) bool {
	sent := false
	enumTopWindows(func(hwnd windows.HWND, owner uint32) {
		if owner == pid && regularWindow(hwnd) && postWindowClose(hwnd) {
			sent = true
		}
	})
	return sent
}
