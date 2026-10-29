package main

// menubar_darwin.go is the menu bar readings on macOS (design:
// docs/superpowers/specs/2026-10-29-menubar-design.md): the native reads they
// show.

import (
	"math"
	"sync"
)

// realMenuBarSource wires the readings to the home page's reads
// (status_darwin.go, fs_darwin.go, machine_darwin.go) and the battery's charge.
func realMenuBarSource() menuBarSource {
	return menuBarSource{
		cpu:    cpuBusyTotal,
		memory: readMemoryStatus,
		disk:   diskStat,
		machine: func() menuBarMachine {
			r, _ := newMachineReader().(*machineReader)
			if r == nil { // a nil *machineReader would make a non-nil interface
				return nil
			}
			return r
		},
		battery: menuBarBatteryLevel,
	}
}

// menuBarBatteryLevel is the battery's charge in percent, read from
// AppleSmartBattery as the system's own battery item reads it, without
// system_profiler (spec §六); -1 without a battery or when it cannot be read.
// On Apple silicon CurrentCapacity is a percentage of MaxCapacity, 100; on
// Intel both are mAh: the ratio is the charge either way.
func menuBarBatteryLevel() int {
	if !iokitInit() {
		return -1
	}
	battery := ioFirst("AppleSmartBattery")
	if battery == 0 {
		return -1
	}
	defer ioObjectRelease(battery)
	cur, ok := ioPropInt(battery, "CurrentCapacity")
	full, fullOK := ioPropInt(battery, "MaxCapacity")
	if !ok || !fullOK || cur < 0 || full <= 0 {
		return -1
	}
	return int(math.Min(100, math.Round(float64(cur)*100/float64(full))))
}

// menuBarHasBattery tells whether this Mac has a battery, asked once.
var menuBarHasBattery = sync.OnceValue(func() bool {
	if !iokitInit() {
		return false
	}
	battery := ioFirst("AppleSmartBattery")
	if battery == 0 {
		return false
	}
	ioObjectRelease(battery)
	return true
})
