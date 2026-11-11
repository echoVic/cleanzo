package main

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

// realStatusSource wires the loop to the native reads in native.go and
// machine_darwin.go. It reads no processes and no NSWorkspace list: the
// home page only talks about the machine, and the 进程 page shows who uses
// what (design docs/superpowers/specs/2026-10-28-home-machine-design.md).
func realStatusSource() statusSource {
	return statusSource{
		memory: func() MemoryStatus {
			return readMemoryStatus()
		},
		cpu:     cpuBusyTotal,
		battery: statusBattery,
		disk:    diskStat,
		machine: newMachineReader,
	}
}

// cpuBusyTotal is the whole CPU's cumulative busy (user+system+nice) and
// total ticks, or zeros when they cannot be read.
func cpuBusyTotal() (uint64, uint64) {
	t, ok := nativeCPUTicks()
	if !ok {
		return 0, 0
	}
	busy := uint64(t.Ticks[0]) + uint64(t.Ticks[1]) + uint64(t.Ticks[3]) // user+system+nice
	return busy, busy + uint64(t.Ticks[2])
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

// readSwapUsage reads vm.swapusage, a struct sysctl; a failed read is
// zero swap.
func readSwapUsage() (used, total uint64) {
	su, _ := parseSwapUsageBytes(nativeSysctlRaw("vm.swapusage"))
	return su.Used, su.Total
}

// batteryProfiler is the system_profiler statusBattery runs; tests replace it.
var batteryProfiler = "/usr/sbin/system_profiler"

// statusBattery runs system_profiler, whose JSON is the only source that
// matches System Settings (it rounds Maximum Capacity itself). Slow, so it
// lives behind a context deadline and refreshes only once a minute. The
// battery's temperature and the adapter's watts, which it leaves out, come
// from AppleSmartBattery.
func statusBattery(ctx context.Context) *BatteryStatus {
	cmd := exec.CommandContext(ctx, batteryProfiler, "-json", "SPPowerDataType")
	// A child that keeps the output open is not waited for: the read ends a
	// second after the deadline or system_profiler's exit. ErrWaitDelay means
	// system_profiler exited zero, so what it printed is whole.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return nil
	}
	b := parseBatteryJSON(out)
	if b != nil {
		b.Temperature, b.AdapterWatts = smartBatteryExtras(b.Plugged)
	}
	return b
}
