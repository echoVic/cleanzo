package main

// native_windows.go keeps the package compiling off macOS, where the status
// page cannot read anything: every accessor reports failure and the page
// shows its error state. The real bindings are native.go (darwin).

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

// nativeProcs reports no processes until the Windows probe is implemented.
func nativeProcs() []procReading { return nil }

// nativeResponsiblePid reports an unavailable responsibility relationship.
func nativeResponsiblePid(pid int32) int32 { return -1 }

// nativeMachineInfo reports an unavailable machine probe.
func nativeMachineInfo() (string, uint64, uint64, string, int64) {
	return "", 0, 0, "", 0
}

// workspaceList reports no apps until the Windows probe is implemented.
func workspaceList() []workspaceApp { return nil }

// workspaceTerminate refuses unsupported process termination.
func workspaceTerminate(pid int32) bool { return false }

// nativeSysctlString reports an unavailable Darwin sysctl.
func nativeSysctlString(name string) string { return "" }

// nativeSysctlUint64 reports an unavailable Darwin sysctl.
func nativeSysctlUint64(name string) uint64 { return 0 }

// nativeSysctlRaw reports an unavailable Darwin sysctl.
func nativeSysctlRaw(name string) []byte { return nil }
