//go:build darwin

package main

// iokit_darwin.go binds what the home page's machine reads need, with
// purego: CoreFoundation, IOKit (including the private IOHID event-system
// client the temperature sensors sit behind), CoreGraphics,
// SystemConfiguration and three libSystem calls. Everything loads on first
// use. A library or symbol that is missing turns its group off, and the
// readers in machine_darwin.go leave those fields unknown.

import (
	"bytes"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	iokitOnce sync.Once
	// Each group is true when every symbol in it resolved.
	ioOK  bool // CoreFoundation, IOKit and libSystem; everything else needs these
	hidOK bool // the private IOHID event-system client
	cgOK  bool // CoreGraphics displays
	scOK  bool // SystemConfiguration

	// machTaskSelf is mach_task_self(), for IOServiceOpen and vm_deallocate.
	machTaskSelf uint32
)

// ---- CoreFoundation ---------------------------------------------------------

var (
	cfStringCreateWithCString func(alloc uintptr, s string, encoding uint32) uintptr
	cfStringGetCString        func(s uintptr, buf *byte, size int64, encoding uint32) bool
	cfGetTypeID               func(ref uintptr) uint64
	cfStringGetTypeID         func() uint64
	cfNumberGetTypeID         func() uint64
	cfDataGetTypeID           func() uint64
	cfNumberCreate            func(alloc uintptr, typ int64, value unsafe.Pointer) uintptr
	cfNumberGetValue          func(n uintptr, typ int64, out unsafe.Pointer) bool
	cfDataGetLength           func(data uintptr) int64
	cfDataGetBytePtr          func(data uintptr) uintptr
	cfDictionaryCreateMutable func(alloc uintptr, capacity int64, keyCallBacks, valueCallBacks uintptr) uintptr
	cfDictionaryGetValue      func(dict, key uintptr) uintptr
	cfDictionarySetValue      func(dict, key, value uintptr)
	cfArrayGetCount           func(array uintptr) int64
	cfArrayGetValueAtIndex    func(array uintptr, i int64) uintptr
	cfUUIDCreateFromString    func(alloc, s uintptr) uintptr
	cfReleaseFn               func(ref uintptr)

	// &kCFTypeDictionaryKeyCallBacks and &kCFTypeDictionaryValueCallBacks.
	cfKeyCallBacks, cfValueCallBacks uintptr
)

// ---- IOKit --------------------------------------------------------------------

var (
	ioServiceMatching                 func(class string) uintptr
	ioServiceGetMatchingService       func(port uint32, matching uintptr) uint32
	ioServiceGetMatchingServices      func(port uint32, matching uintptr, iterator *uint32) int32
	ioIteratorNext                    func(iterator uint32) uint32
	ioObjectRelease                   func(object uint32) int32
	ioRegistryEntryFromPath           func(port uint32, path string) uint32
	ioRegistryEntryCreateCFProperty   func(entry uint32, key, alloc uintptr, options uint32) uintptr
	ioServiceOpen                     func(service, task, typ uint32, connect *uint32) int32
	ioServiceClose                    func(connect uint32) int32
	ioConnectCallStructMethod         func(connect, selector uint32, in unsafe.Pointer, inSize uintptr, out unsafe.Pointer, outSize *uintptr) int32
	ioCreatePlugInInterfaceForService func(service uint32, pluginType, interfaceType uintptr, iface *uintptr, score *int32) int32
	ioDestroyPlugInInterface          func(iface uintptr) int32
)

// ---- the IOHID event system (private; the temperature sensors) --------------

var (
	hidClientCreate        func(alloc uintptr) uintptr
	hidClientSetMatching   func(client, matching uintptr)
	hidClientCopyServices  func(client uintptr) uintptr
	hidServiceCopyProperty func(service, key uintptr) uintptr
	hidServiceCopyEvent    func(service uintptr, typ int64, options int32, timestamp int64) uintptr
	hidEventGetFloatValue  func(event uintptr, field int32) float64
)

// ---- libSystem ----------------------------------------------------------------

var (
	hostProcessorInfo func(host uint32, flavor int32, count *uint32, info *uintptr, infoCount *uint32) int32
	vmDeallocate      func(task uint32, address, size uintptr) int32
	sysctlMIB         func(mib *int32, n uint32, old unsafe.Pointer, oldLen *uintptr, newValue unsafe.Pointer, newLen uintptr) int32
)

// ---- CoreGraphics -------------------------------------------------------------

var (
	cgGetActiveDisplayList      func(max uint32, ids *uint32, count *uint32) int32
	cgDisplayIsBuiltin          func(id uint32) uint32 // boolean_t
	cgDisplayCopyDisplayMode    func(id uint32) uintptr
	cgDisplayModeGetWidth       func(mode uintptr) uint64
	cgDisplayModeGetHeight      func(mode uintptr) uint64
	cgDisplayModeGetRefreshRate func(mode uintptr) float64
	cgDisplayModeRelease        func(mode uintptr)
)

// ---- SystemConfiguration ------------------------------------------------------

var (
	scNetworkInterfaceCopyAll          func() uintptr
	scNetworkInterfaceGetBSDName       func(iface uintptr) uintptr
	scNetworkInterfaceGetInterfaceType func(iface uintptr) uintptr
	scDynamicStoreCreate               func(alloc, name, callout, context uintptr) uintptr
	scDynamicStoreCopyValue            func(store, key uintptr) uintptr
)

// iokitInit loads everything once and reports whether the core group
// (CoreFoundation, IOKit, libSystem) is usable; hidOK, cgOK and scOK say
// the same for the other groups.
func iokitInit() bool {
	iokitOnce.Do(func() {
		nativeInit() // hostPort, for host_processor_info
		cf := dlopenLib("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
		io := dlopenLib("/System/Library/Frameworks/IOKit.framework/IOKit")
		sys := dlopenLib("/usr/lib/libSystem.B.dylib")
		if cf == 0 || io == 0 || sys == 0 {
			return
		}
		ioOK = bindFuncs(cf, map[string]any{
			"CFStringCreateWithCString": &cfStringCreateWithCString,
			"CFStringGetCString":        &cfStringGetCString,
			"CFGetTypeID":               &cfGetTypeID,
			"CFStringGetTypeID":         &cfStringGetTypeID,
			"CFNumberGetTypeID":         &cfNumberGetTypeID,
			"CFDataGetTypeID":           &cfDataGetTypeID,
			"CFNumberCreate":            &cfNumberCreate,
			"CFNumberGetValue":          &cfNumberGetValue,
			"CFDataGetLength":           &cfDataGetLength,
			"CFDataGetBytePtr":          &cfDataGetBytePtr,
			"CFDictionaryCreateMutable": &cfDictionaryCreateMutable,
			"CFDictionaryGetValue":      &cfDictionaryGetValue,
			"CFDictionarySetValue":      &cfDictionarySetValue,
			"CFArrayGetCount":           &cfArrayGetCount,
			"CFArrayGetValueAtIndex":    &cfArrayGetValueAtIndex,
			"CFUUIDCreateFromString":    &cfUUIDCreateFromString,
			"CFRelease":                 &cfReleaseFn,
		}) && bindFuncs(io, map[string]any{
			"IOServiceMatching":                 &ioServiceMatching,
			"IOServiceGetMatchingService":       &ioServiceGetMatchingService,
			"IOServiceGetMatchingServices":      &ioServiceGetMatchingServices,
			"IOIteratorNext":                    &ioIteratorNext,
			"IOObjectRelease":                   &ioObjectRelease,
			"IORegistryEntryFromPath":           &ioRegistryEntryFromPath,
			"IORegistryEntryCreateCFProperty":   &ioRegistryEntryCreateCFProperty,
			"IOServiceOpen":                     &ioServiceOpen,
			"IOServiceClose":                    &ioServiceClose,
			"IOConnectCallStructMethod":         &ioConnectCallStructMethod,
			"IOCreatePlugInInterfaceForService": &ioCreatePlugInInterfaceForService,
			"IODestroyPlugInInterface":          &ioDestroyPlugInInterface,
		}) && bindFuncs(sys, map[string]any{
			"host_processor_info": &hostProcessorInfo,
			"vm_deallocate":       &vmDeallocate,
			"sysctl":              &sysctlMIB,
		})
		if !ioOK {
			return
		}
		keys, err1 := purego.Dlsym(cf, "kCFTypeDictionaryKeyCallBacks")
		values, err2 := purego.Dlsym(cf, "kCFTypeDictionaryValueCallBacks")
		task, err3 := purego.Dlsym(sys, "mach_task_self_")
		if err1 != nil || err2 != nil || err3 != nil {
			ioOK = false
			return
		}
		cfKeyCallBacks, cfValueCallBacks = keys, values
		// mach_task_self() is a macro that reads this variable.
		machTaskSelf = *(*uint32)(*(*unsafe.Pointer)(unsafe.Pointer(&task)))

		hidOK = bindFuncs(io, map[string]any{
			"IOHIDEventSystemClientCreate":       &hidClientCreate,
			"IOHIDEventSystemClientSetMatching":  &hidClientSetMatching,
			"IOHIDEventSystemClientCopyServices": &hidClientCopyServices,
			"IOHIDServiceClientCopyProperty":     &hidServiceCopyProperty,
			"IOHIDServiceClientCopyEvent":        &hidServiceCopyEvent,
			"IOHIDEventGetFloatValue":            &hidEventGetFloatValue,
		})
		if cg := dlopenLib("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"); cg != 0 {
			cgOK = bindFuncs(cg, map[string]any{
				"CGGetActiveDisplayList":      &cgGetActiveDisplayList,
				"CGDisplayIsBuiltin":          &cgDisplayIsBuiltin,
				"CGDisplayCopyDisplayMode":    &cgDisplayCopyDisplayMode,
				"CGDisplayModeGetWidth":       &cgDisplayModeGetWidth,
				"CGDisplayModeGetHeight":      &cgDisplayModeGetHeight,
				"CGDisplayModeGetRefreshRate": &cgDisplayModeGetRefreshRate,
				"CGDisplayModeRelease":        &cgDisplayModeRelease,
			})
		}
		if sc := dlopenLib("/System/Library/Frameworks/SystemConfiguration.framework/SystemConfiguration"); sc != 0 {
			scOK = bindFuncs(sc, map[string]any{
				"SCNetworkInterfaceCopyAll":          &scNetworkInterfaceCopyAll,
				"SCNetworkInterfaceGetBSDName":       &scNetworkInterfaceGetBSDName,
				"SCNetworkInterfaceGetInterfaceType": &scNetworkInterfaceGetInterfaceType,
				"SCDynamicStoreCreate":               &scDynamicStoreCreate,
				"SCDynamicStoreCopyValue":            &scDynamicStoreCopyValue,
			})
		}
	})
	return ioOK
}

// dlopenLib opens a system library, 0 when it can't.
func dlopenLib(path string) uintptr {
	lib, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return 0
	}
	return lib
}

// bindFuncs registers one group's functions and reports whether all of
// them resolved. purego panics on a missing symbol; the group then stays
// off instead of taking the app down.
func bindFuncs(lib uintptr, fns map[string]any) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	for name, fn := range fns {
		purego.RegisterLibFunc(fn, lib, name)
	}
	return true
}

// ---- helpers ------------------------------------------------------------------

const (
	cfStringEncodingUTF8 = 0x08000100
	cfNumberSInt64Type   = 4
)

// cfRelease is CFRelease for a reference that may be NULL: CoreFoundation
// traps on CFRelease(NULL).
func cfRelease(ref uintptr) {
	if ref != 0 {
		cfReleaseFn(ref)
	}
}

// cfStr makes a CFString (+1; release it).
func cfStr(s string) uintptr {
	return cfStringCreateWithCString(0, s, cfStringEncodingUTF8)
}

// cfGoString copies a CFString, or a CFData holding NUL-terminated text the
// way the device tree stores names, into a Go string. Anything else is "".
func cfGoString(ref uintptr) string {
	if ref == 0 {
		return ""
	}
	switch cfGetTypeID(ref) {
	case cfStringGetTypeID():
		var buf [512]byte
		if !cfStringGetCString(ref, &buf[0], int64(len(buf)), cfStringEncodingUTF8) {
			return ""
		}
		return cString(buf[:])
	case cfDataGetTypeID():
		n, p := cfDataGetLength(ref), cfDataGetBytePtr(ref)
		if n <= 0 || p == 0 {
			return ""
		}
		// Taking the address and dereferencing keeps go vet from flagging
		// the uintptr→pointer conversion, as in nsGoString.
		return cString(unsafe.Slice((*byte)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), n))
	}
	return ""
}

// cString is b up to its first NUL.
func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// cfInt reads a CFNumber as an int64.
func cfInt(ref uintptr) (int64, bool) {
	if ref == 0 || cfGetTypeID(ref) != cfNumberGetTypeID() {
		return 0, false
	}
	var v int64
	ok := cfNumberGetValue(ref, cfNumberSInt64Type, unsafe.Pointer(&v))
	return v, ok
}

// cfNumber makes a CFNumber (+1; release it).
func cfNumber(v int64) uintptr {
	return cfNumberCreate(0, cfNumberSInt64Type, unsafe.Pointer(&v))
}

// cfDictValue looks key up in a CFDictionary. The value belongs to the
// dictionary: don't release it.
func cfDictValue(dict uintptr, key string) uintptr {
	if dict == 0 {
		return 0
	}
	k := cfStr(key)
	defer cfRelease(k)
	return cfDictionaryGetValue(dict, k)
}

// ioProp copies one registry property (+1; release it), 0 when absent.
func ioProp(entry uint32, key string) uintptr {
	k := cfStr(key)
	defer cfRelease(k)
	return ioRegistryEntryCreateCFProperty(entry, k, 0, 0)
}

// ioPropInt reads a numeric registry property.
func ioPropInt(entry uint32, key string) (int64, bool) {
	ref := ioProp(entry, key)
	defer cfRelease(ref)
	return cfInt(ref)
}

// ioPropString reads a string, or NUL-terminated data, registry property.
func ioPropString(entry uint32, key string) string {
	ref := ioProp(entry, key)
	defer cfRelease(ref)
	return cfGoString(ref)
}

// ioEach calls fn with every service of class until fn returns false. Each
// service is released after fn returns, so fn must not keep it.
func ioEach(class string, fn func(service uint32) bool) {
	var it uint32
	// IOServiceGetMatchingServices consumes the matching dictionary.
	if ioServiceGetMatchingServices(0, ioServiceMatching(class), &it) != 0 {
		return
	}
	defer ioObjectRelease(it)
	for s := ioIteratorNext(it); s != 0; s = ioIteratorNext(it) {
		more := fn(s)
		ioObjectRelease(s)
		if !more {
			return
		}
	}
}

// ioFirst returns the first service of class (release it), 0 if none.
func ioFirst(class string) uint32 {
	return ioServiceGetMatchingService(0, ioServiceMatching(class))
}

// comFunc looks up the function at byte offset off in a COM-style
// interface's function table. IOKit's plug-in interfaces are IUnknown
// based: iface points at a pointer to the table, and every function takes
// iface first. Call the result with purego.SyscallN directly, writing any
// uintptr(unsafe.Pointer(&x)) in that call, so the pointers stay valid.
func comFunc(iface, off uintptr) uintptr {
	table := *(*uintptr)(*(*unsafe.Pointer)(unsafe.Pointer(&iface)))
	slot := table + off
	return *(*uintptr)(*(*unsafe.Pointer)(unsafe.Pointer(&slot)))
}
