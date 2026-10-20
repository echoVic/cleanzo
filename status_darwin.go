package main

import (
	"context"
	"os/exec"
)

// realStatusSource wires the loop to the native reads in native.go.
func realStatusSource() statusSource {
	return statusSource{
		procs: nativeProcs,
		apps:  workspaceList,
		memory: func() MemoryStatus {
			return readMemoryStatus()
		},
		cpu: func() (uint64, uint64) {
			t, ok := nativeCPUTicks()
			if !ok {
				return 0, 0
			}
			busy := uint64(t.Ticks[0]) + uint64(t.Ticks[1]) + uint64(t.Ticks[3]) // user+system+nice
			total := busy + uint64(t.Ticks[2])
			return busy, total
		},
		battery:   statusBattery,
		disk:      diskStat,
		terminate: workspaceTerminate,
	}
}

// readMemoryStatus assembles the memory card from one vm read plus the
// sysctls for size, pressure and swap.
func readMemoryStatus() MemoryStatus {
	vm, ok := nativeVMStats()
	if !ok {
		return MemoryStatus{}
	}
	page := nativeSysctlUint64("hw.pagesize")
	total := nativeSysctlUint64("hw.memsize")
	if page == 0 || total == 0 {
		return MemoryStatus{}
	}
	pressure := nativeSysctlUint64("kern.memorystatus_vm_pressure_level")
	swapUsed, swapTotal := readSwapUsage()
	return memoryFromVM(total, page, vm, pressure, swapUsed, swapTotal)
}

// readSwapUsage is the wiring: vm.swapusage is a struct sysctl, so the
// binary parse runs first; the text parse only remains as a fallback for
// the CLI's rendered shape, used when the raw read itself failed.
func readSwapUsage() (used, total uint64) {
	if su, ok := parseSwapUsageBytes(nativeSysctlRaw("vm.swapusage")); ok {
		return su.Used, su.Total
	}
	return parseSwapUsage(nativeSysctlString("vm.swapusage"))
}

// statusBattery runs system_profiler, whose JSON is the only source that
// matches System Settings (it rounds Maximum Capacity itself). Slow, so it
// lives behind a context deadline and refreshes only once a minute.
func statusBattery(ctx context.Context) *BatteryStatus {
	cmd := exec.CommandContext(ctx, "/usr/sbin/system_profiler", "-json", "SPPowerDataType")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseBatteryJSON(out)
}
