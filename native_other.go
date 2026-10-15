//go:build !darwin

package main

// native_other.go keeps the package compiling off macOS, where the status
// page cannot read anything: every accessor reports failure and the page
// shows its error state. The real bindings are native.go (darwin).

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

type hostCPULoad struct{ Ticks [4]uint32 }

type procReading struct {
	Pid       int32
	Path      string
	Footprint uint64
	UserNs    uint64
	SysNs     uint64
	Start     uint64
}

func nativeVMStats() (vmStats64, bool)     { return vmStats64{}, false }
func nativeCPUTicks() (hostCPULoad, bool)  { return hostCPULoad{}, false }
func nativeProcs() []procReading           { return nil }
func nativeResponsiblePid(pid int32) int32 { return -1 }
func nativeMachineInfo() (string, uint64, uint64, string, int64) {
	return "", 0, 0, "", 0
}

// Workspace and sysctl reads only exist on macOS.
func workspaceList() []workspaceApp         { return nil }
func workspaceTerminate(pid int32) bool     { return false }
func nativeSysctlString(name string) string { return "" }
func nativeSysctlUint64(name string) uint64 { return 0 }
func nativeSysctlRaw(name string) []byte    { return nil }
