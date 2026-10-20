package main

import (
	"context"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsMemoryStatus is MEMORYSTATUSEX from the SDK's sysinfoapi.h (64 bytes on amd64/arm64).
type windowsMemoryStatus struct {
	Length, Load                                                                                         uint32
	TotalPhys, AvailPhys, TotalPageFile, AvailPageFile, TotalVirtual, AvailVirtual, AvailExtendedVirtual uint64
}

// windowsPerformanceInfo is PERFORMANCE_INFORMATION from psapi.h (104 bytes on amd64/arm64).
type windowsPerformanceInfo struct {
	Size                                                                                uint32
	CommitTotal, CommitLimit, CommitPeak, PhysicalTotal, PhysicalAvailable, SystemCache uintptr
	KernelTotal, KernelPaged, KernelNonpaged, PageSize                                  uintptr
	HandleCount, ProcessCount, ThreadCount                                              uint32
}

// windowsPowerStatus is SYSTEM_POWER_STATUS from winbase.h (12 bytes on amd64/arm64).
type windowsPowerStatus struct {
	ACLineStatus, BatteryFlag, BatteryLifePercent, SystemStatusFlag byte
	BatteryLifeTime, BatteryFullLifeTime                            uint32
}

// Win32 readers absent from x/sys use the SDK layouts declared beside their consumers.
var (
	statusKernel         = windows.NewLazySystemDLL("kernel32.dll")
	globalMemoryStatusEx = statusKernel.NewProc("GlobalMemoryStatusEx")
	getPerformanceInfo   = statusKernel.NewProc("K32GetPerformanceInfo")
	getSystemTimes       = statusKernel.NewProc("GetSystemTimes") // sysinfoapi.h, three FILETIME pointers
	getSystemPowerStatus = statusKernel.NewProc("GetSystemPowerStatus")
)

// realStatusSource keeps GUI ancestry and app metadata from the same process sample.
func realStatusSource() statusSource {
	var apps []workspaceApp
	var ancestors map[uint32]uint32
	return statusSource{
		procs: func() []procReading {
			procs, err := statusSnapshotProcesses()
			if err != nil {
				apps = nil
				ancestors = nil
				return nil
			}
			apps, ancestors = statusApps(procs)
			return windowsProcReadings(procs)
		},
		apps: func() []workspaceApp { return apps },
		group: func(r procReading, byPid map[int32]procReading, _ func(int32) int32) (string, string) {
			return windowsAppGroup(r, byPid, ancestors)
		},
		memory: readMemoryStatus, cpu: windowsCPUTicks, battery: statusBattery, disk: diskStat, terminate: workspaceTerminate,
	}
}

// readMemoryStatus leaves unsupported performance categories unknown if their probe fails.
func readMemoryStatus() MemoryStatus {
	m := windowsMemoryStatus{Length: uint32(unsafe.Sizeof(windowsMemoryStatus{}))}
	if ok, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); ok == 0 {
		return MemoryStatus{}
	}
	p := windowsPerformanceInfo{Size: uint32(unsafe.Sizeof(windowsPerformanceInfo{}))}
	if ok, _, _ := getPerformanceInfo.Call(uintptr(unsafe.Pointer(&p)), uintptr(p.Size)); ok == 0 {
		p = windowsPerformanceInfo{}
	}
	page := uint64(p.PageSize)
	return memoryFromWindows(m.TotalPhys, m.AvailPhys, uint64(p.KernelNonpaged)*page, uint64(p.SystemCache)*page, uint64(p.CommitTotal)*page, uint64(p.CommitLimit)*page)
}

// windowsCPUTicks reads cumulative 100 ns ticks; kernel includes idle time.
func windowsCPUTicks() (uint64, uint64) {
	var idle, kernel, user windows.Filetime
	if ok, _, _ := getSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); ok == 0 {
		return 0, 0
	}
	i, k, u := filetimeTicks(idle), filetimeTicks(kernel), filetimeTicks(user)
	return k - min(k, i) + u, k + u
}

// statusBattery reports only the fields GetSystemPowerStatus can supply.
func statusBattery(ctx context.Context) *BatteryStatus {
	if ctx.Err() != nil {
		return nil
	}
	var p windowsPowerStatus
	if ok, _, _ := getSystemPowerStatus.Call(uintptr(unsafe.Pointer(&p))); ok == 0 || p.BatteryFlag == 128 {
		return nil
	}
	b := &BatteryStatus{Plugged: p.ACLineStatus == 1}
	if p.BatteryLifePercent <= 100 {
		b.Percent = int(p.BatteryLifePercent)
		b.Full = b.Percent == 100
	}
	if p.BatteryFlag != 255 {
		b.Charging = p.BatteryFlag&8 != 0
	}
	return b
}

// filetimeTicks preserves unsigned FILETIME counters before converting CPU durations to ns.
func filetimeTicks(f windows.Filetime) uint64 {
	return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime)
}
