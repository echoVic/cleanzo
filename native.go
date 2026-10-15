//go:build darwin

package main

// native.go binds the macOS interfaces the status page reads, with purego:
// sysctl (via syscall), mach host statistics, libproc, the private
// responsibility API and NSWorkspace / NSRunningApplication through the
// Objective-C runtime. It only reads and converts; the rules live in
// monitor.go. Everything AppKit runs on the main thread inside an
// autorelease pool, via mygo.RunOnMain.

import (
	"encoding/binary"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
)

// ---- libSystem: mach and libproc -----------------------------------------

var (
	machHostSelf     func() uint32
	hostStat64       func(host uint32, flavor int32, info unsafe.Pointer, count *uint32) int32
	hostStat         func(host uint32, flavor int32, info unsafe.Pointer, count *uint32) int32
	procPidRusage    func(pid int32, flavor int32, buf unsafe.Pointer) int32
	procListAllPids  func(buf unsafe.Pointer, size int32) int32
	procPidPathFn    func(pid int32, buf unsafe.Pointer, size uint32) int32
	responsibleForFn func(pid int32) int32
	sysctlByNameFn   func(name *byte, oldp unsafe.Pointer, oldlenp *uintptr, newp unsafe.Pointer, newlen uintptr) int32
	hostPort         uint32

	nativeOnce sync.Once
)

const (
	hostVMInfo64Flavor    = 4 // HOST_VM_INFO64
	hostCPULoadInfoFlavor = 3 // HOST_CPU_LOAD_INFO
)

// vmStats64 mirrors struct vm_statistics64 from XNU's mach/vm_statistics.h
// (rev2 layout, 8-byte aligned). The smoke test checks used memory against
// hw.memsize, which is what catches a wrong layout.
type vmStats64 struct {
	Free, Active, Inactive, Wire     uint32
	ZeroFill, Reactivations          uint64
	Pageins, Pageouts                uint64
	Faults, CowFaults, Lookups, Hits uint64
	Purges                           uint64
	Purgeable, Speculative           uint32
	Decompressions, Compressions     uint64
	Swapins, Swapouts                uint64
	CompressorPages, Throttled       uint32 // pages the compressor holds in physical memory
	ExternalPages, InternalPages     uint32
	TotalUncompressed                uint64 // uncompressed size of what sits in the compressor
	Swapped                          uint64
}

// hostCPULoad mirrors struct host_cpu_load_info: ticks per CPU state.
type hostCPULoad struct {
	Ticks [4]uint32 // user, system, idle, nice
}

// Offsets into rusage_info_v2, which starts with a 16-byte UUID and only
// ever appends fields, so these are stable across macOS versions.
const (
	riUserNs    = 16 // ri_user_time, nanoseconds
	riSystemNs  = 24 // ri_system_time
	riFootprint = 72 // ri_phys_footprint, the Activity Monitor "Memory" column
	riStartAbs  = 80 // ri_proc_start_abstime, identifies reused pids
)

func nativeInit() {
	nativeOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return // the accessors below stay nil and report errors
		}
		purego.RegisterLibFunc(&machHostSelf, lib, "mach_host_self")
		purego.RegisterLibFunc(&hostStat64, lib, "host_statistics64")
		purego.RegisterLibFunc(&hostStat, lib, "host_statistics")
		purego.RegisterLibFunc(&procPidRusage, lib, "proc_pid_rusage")
		purego.RegisterLibFunc(&procListAllPids, lib, "proc_listallpids")
		purego.RegisterLibFunc(&procPidPathFn, lib, "proc_pidpath")
		purego.RegisterLibFunc(&sysctlByNameFn, lib, "sysctlbyname")
		// Private API: probe instead of hard-wiring it. When absent the
		// attribution falls back to executable names (see monitor.go).
		if _, err := purego.Dlsym(lib, "responsibility_get_pid_responsible_for_pid"); err == nil {
			purego.RegisterLibFunc(&responsibleForFn, lib, "responsibility_get_pid_responsible_for_pid")
		}
		if machHostSelf != nil {
			hostPort = machHostSelf()
		}
	})
}

// nativeVMStats reads the page statistics Activity Monitor derives from.
func nativeVMStats() (vmStats64, bool) {
	nativeInit()
	var vm vmStats64
	if hostStat64 == nil {
		return vm, false
	}
	count := uint32(unsafe.Sizeof(vm) / 4) // HOST_VM_INFO64_COUNT, in integers
	if hostStat64(hostPort, hostVMInfo64Flavor, unsafe.Pointer(&vm), &count) != 0 {
		return vmStats64{}, false
	}
	return vm, true
}

// nativeCPUTicks returns cumulative CPU ticks: user, system, idle, nice.
func nativeCPUTicks() (hostCPULoad, bool) {
	nativeInit()
	var c hostCPULoad
	if hostStat == nil {
		return c, false
	}
	count := uint32(unsafe.Sizeof(c) / 4)
	if hostStat(hostPort, hostCPULoadInfoFlavor, unsafe.Pointer(&c), &count) != 0 {
		return hostCPULoad{}, false
	}
	return c, true
}

// procReading is one process's numbers, gathered in a single pass.
type procReading struct {
	Pid       int32
	Path      string
	Footprint uint64
	UserNs    uint64
	SysNs     uint64
	Start     uint64
}

// nativeProcs lists the current user's processes with their paths and usage.
// Processes whose usage cannot be read (other users', root's) are skipped.
func nativeProcs() []procReading {
	nativeInit()
	if procListAllPids == nil || procPidRusage == nil {
		return nil
	}
	size := procListAllPids(nil, 0)
	if size <= 0 {
		return nil
	}
	pids := make([]int32, size)
	n := procListAllPids(unsafe.Pointer(&pids[0]), 4*int32(len(pids)))
	if n <= 0 {
		return nil
	}
	var out []procReading
	var path [4096]byte
	var rusage [512]byte
	for _, pid := range pids[:n] {
		if procPidRusage(pid, 2 /* RUSAGE_INFO_V2 */, unsafe.Pointer(&rusage[0])) != 0 {
			continue
		}
		r := procReading{
			Pid:       pid,
			Footprint: binary.LittleEndian.Uint64(rusage[riFootprint:]),
			UserNs:    binary.LittleEndian.Uint64(rusage[riUserNs:]),
			SysNs:     binary.LittleEndian.Uint64(rusage[riSystemNs:]),
			Start:     binary.LittleEndian.Uint64(rusage[riStartAbs:]),
		}
		if l := procPidPathFn(pid, unsafe.Pointer(&path[0]), uint32(len(path))); l > 0 {
			r.Path = string(path[:l])
		}
		out = append(out, r)
	}
	return out
}

// nativeResponsiblePid returns the pid responsible for starting pid, or -1
// when the private API is unavailable or fails.
func nativeResponsiblePid(pid int32) int32 {
	nativeInit()
	if responsibleForFn == nil {
		return -1
	}
	return responsibleForFn(pid)
}

// ---- sysctl wrappers ------------------------------------------------------

func nativeSysctlString(name string) string {
	v, err := syscall.Sysctl(name)
	if err != nil {
		return ""
	}
	return v
}

// nativeSysctlUint64 reads an integer sysctl through the real
// sysctlbyname, because darwin's syscall.Sysctl returns the raw value
// bytes as a string, not decimal text.
func nativeSysctlUint64(name string) uint64 {
	nativeInit()
	if sysctlByNameFn == nil {
		return 0
	}
	nameBuf := append([]byte(name), 0)
	var v uint64
	l := unsafe.Sizeof(v)
	if sysctlByNameFn(&nameBuf[0], unsafe.Pointer(&v), &l, nil, 0) != 0 {
		return 0
	}
	return v
}

// nativeSysctlRaw reads a struct sysctl's raw bytes through the real
// sysctlbyname. kern.boottime (struct timeval) and vm.loadavg (struct
// loadavg) come back as their C structs, not the text the sysctl CLI
// renders, so the raw bytes are what monitor.go's parsers want.
// (真机 verification P1-1 / P1-4.)
func nativeSysctlRaw(name string) []byte {
	nativeInit()
	if sysctlByNameFn == nil {
		return nil
	}
	nameBuf := append([]byte(name), 0)
	var l uintptr
	if sysctlByNameFn(&nameBuf[0], nil, &l, nil, 0) != 0 || l == 0 || l > 4096 {
		return nil
	}
	buf := make([]byte, l)
	if sysctlByNameFn(&nameBuf[0], unsafe.Pointer(&buf[0]), &l, nil, 0) != 0 {
		return nil
	}
	return buf
}

// nativeMachineInfo reads the About This Mac line: chip, memory, cores,
// macOS version and boot time. Unreadable fields come back zero or empty.
func nativeMachineInfo() (chip string, memory, cores uint64, osVersion string, bootTime int64) {
	boot := parseBootTimeBytes(nativeSysctlRaw("kern.boottime"))
	if boot == 0 {
		// Struct read failed; the CLI's rendered text is the only other
		// shape kern.boottime could arrive in.
		boot = parseBootTime(nativeSysctlString("kern.boottime"))
	}
	return nativeSysctlString("machdep.cpu.brand_string"),
		nativeSysctlUint64("hw.memsize"),
		nativeSysctlUint64("hw.logicalcpu"),
		nativeSysctlString("kern.osproductversion"),
		boot
}

// ---- NSWorkspace / NSRunningApplication ----------------------------------

// runOnMain runs a closure on the main thread and waits for it to
// return. Production goes through mygo.RunOnMain, whose queue the app's
// event loop serves. Tests replace it with a synchronous runner: the
// test binary never runs the event loop, so a queued read would wait
// forever and time the whole suite out (真机 verification P1-2). The
// closure under test — AppKit init, autorelease pool, NSWorkspace read —
// stays exactly the same.
var runOnMain = mygo.RunOnMain

var (
	poolPush, poolPop uintptr
	appKitOnce        sync.Once
	selCache          = map[string]objc.SEL{}
	selMu             sync.Mutex
)

func nsSel(name string) objc.SEL {
	selMu.Lock()
	s, ok := selCache[name]
	selMu.Unlock()
	if ok {
		return s
	}
	s = objc.RegisterName(name)
	selMu.Lock()
	selCache[name] = s
	selMu.Unlock()
	return s
}

func appKitInit() bool {
	appKitOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		if push, err := purego.Dlsym(lib, "objc_autoreleasePoolPush"); err == nil {
			poolPush = push
		}
		if pop, err := purego.Dlsym(lib, "objc_autoreleasePoolPop"); err == nil {
			poolPop = pop
		}
		// Loads AppKit and registers its classes; NSWorkspace lives there.
		_, _ = purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	})
	return poolPush != 0 && objc.GetClass("NSWorkspace") != 0
}

// withPool runs fn inside an autorelease pool on the current thread.
func withPool(fn func()) {
	pool, _, _ := purego.SyscallN(poolPush)
	defer purego.SyscallN(poolPop, pool)
	fn()
}

// nsGoString copies an NSString into a Go string.
func nsGoString(str objc.ID) string {
	if str == 0 {
		return ""
	}
	n := int(objc.Send[uint64](str, nsSel("lengthOfBytesUsingEncoding:"), 4 /* UTF8 */))
	if n <= 0 {
		return ""
	}
	c := uintptr(str.Send(nsSel("UTF8String")))
	if c == 0 {
		return ""
	}
	// Taking the address and dereferencing keeps go vet from flagging the
	// uintptr→pointer conversion, the same trick purego's own GoString uses.
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&c))
	return string(unsafe.Slice((*byte)(ptr), n))
}

// workspaceList asks NSWorkspace for the running applications. NSWorkspace
// only refreshes its list on the main thread's run loop, so the call runs
// there, which the event loop serves.
func workspaceList() []workspaceApp {
	var out []workspaceApp
	var ok bool
	runOnMain(func() {
		if !appKitInit() {
			return
		}
		withPool(func() {
			ws := objc.ID(objc.GetClass("NSWorkspace")).Send(nsSel("sharedWorkspace"))
			apps := ws.Send(nsSel("runningApplications"))
			n := int(objc.Send[uint64](apps, nsSel("count")))
			for i := 0; i < n; i++ {
				app := apps.Send(nsSel("objectAtIndex:"), i)
				w := workspaceApp{
					Pid:      int32(objc.Send[uint64](app, nsSel("processIdentifier"))),
					Regular:  objc.Send[uint64](app, nsSel("activationPolicy")) == 0, // NSApplicationActivationPolicyRegular
					BundleID: nsGoString(app.Send(nsSel("bundleIdentifier"))),
				}
				if url := app.Send(nsSel("bundleURL")); url != 0 {
					w.Path = nsGoString(url.Send(nsSel("path")))
				}
				w.Name = nsGoString(app.Send(nsSel("localizedName")))
				out = append(out, w)
			}
			ok = true
		})
	})
	if !ok {
		return nil
	}
	return out
}

// workspaceTerminate asks one application to terminate, exactly like
// choosing Quit in the Dock. It never force-kills; an app that refuses (or
// has unsaved work) simply stays, and monitor.go decides the verdict.
func workspaceTerminate(pid int32) bool {
	ok := false
	runOnMain(func() {
		if !appKitInit() {
			return
		}
		withPool(func() {
			ws := objc.ID(objc.GetClass("NSWorkspace")).Send(nsSel("sharedWorkspace"))
			apps := ws.Send(nsSel("runningApplications"))
			n := int(objc.Send[uint64](apps, nsSel("count")))
			for i := 0; i < n; i++ {
				app := apps.Send(nsSel("objectAtIndex:"), i)
				if int32(objc.Send[uint64](app, nsSel("processIdentifier"))) != pid {
					continue
				}
				ok = byte(app.Send(nsSel("terminate"))) != 0
				return
			}
		})
	})
	return ok
}
