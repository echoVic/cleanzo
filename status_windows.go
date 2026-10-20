package main

import "context"

// realStatusSource reports empty readings until Windows status probes exist.
func realStatusSource() statusSource {
	return statusSource{
		procs:     nativeProcs,
		apps:      workspaceList,
		memory:    func() MemoryStatus { return MemoryStatus{} },
		cpu:       func() (uint64, uint64) { return 0, 0 },
		battery:   func(context.Context) *BatteryStatus { return nil },
		disk:      func() DiskStat { return DiskStat{} },
		terminate: workspaceTerminate,
	}
}
