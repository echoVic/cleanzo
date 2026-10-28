//go:build darwin

package main

// machine_darwin.go reads what the home page shows about this Mac (design
// docs/superpowers/specs/2026-10-28-home-machine-design.md §四). None of it
// needs administrator rights or starts a process.

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// ---- what this Mac is (MachineInfo) -------------------------------------------

// readMachineInfo reads the identity block. Unreadable parts stay empty.
func readMachineInfo() MachineInfo {
	info := MachineInfo{ModelID: nativeSysctlString("hw.model"), Displays: []DisplayInfo{}}
	info.PerfCores, info.EffCores = coreCounts()
	if !iokitInit() {
		return info
	}
	info.Model = deviceTreeModel()
	info.GPUCores = gpuCoreCount()
	info.Displays = readDisplays()
	ids := make([]uint32, len(info.Displays))
	for i, d := range info.Displays {
		ids[i] = d.ID
	}
	info.DisplaySig = displaySignature(ids)
	info.SSD = readSSD()
	return info
}

// coreCounts splits the physical cores by performance level. Each level
// carries its name, so the order of perflevel0 and perflevel1 doesn't
// matter.
func coreCounts() (perf, eff int) {
	levels := int(nativeSysctlUint64("hw.nperflevels"))
	for i := 0; i < levels; i++ {
		n := int(nativeSysctlUint64(fmt.Sprintf("hw.perflevel%d.physicalcpu", i)))
		switch nativeSysctlString(fmt.Sprintf("hw.perflevel%d.name", i)) {
		case "Performance":
			perf = n
		case "Efficiency":
			eff = n
		}
	}
	return perf, eff
}

// deviceTreeModel is the marketing name the device tree carries, such as
// "MacBook Pro (16-inch, 2023)". macOS's own SIMachineAttributes.plist is
// too old to know newer Macs, so there is no lookup table.
func deviceTreeModel() string {
	product := ioRegistryEntryFromPath(0, "IODeviceTree:/product")
	if product == 0 {
		return ""
	}
	defer ioObjectRelease(product)
	return strings.TrimSpace(ioPropString(product, "product-name"))
}

// gpuCoreCount reads the GPU's core count from its accelerator service.
func gpuCoreCount() (cores int) {
	ioEach("IOAccelerator", func(s uint32) bool {
		if n, ok := ioPropInt(s, "gpu-core-count"); ok && n > 0 {
			cores = int(n)
			return false
		}
		return true
	})
	return cores
}

// cgDisplayIDs lists the active displays.
func cgDisplayIDs() []uint32 {
	if !cgOK {
		return nil
	}
	var ids [16]uint32
	var n uint32
	if cgGetActiveDisplayList(uint32(len(ids)), &ids[0], &n) != 0 {
		return nil
	}
	return append([]uint32(nil), ids[:n]...)
}

// readDisplays reads each active display's current mode, and its name from
// NSScreen.
func readDisplays() []DisplayInfo {
	out := []DisplayInfo{}
	for _, id := range cgDisplayIDs() {
		d := DisplayInfo{ID: id, Builtin: cgDisplayIsBuiltin(id) != 0}
		if mode := cgDisplayCopyDisplayMode(id); mode != 0 {
			d.Width = int(cgDisplayModeGetWidth(mode))
			d.Height = int(cgDisplayModeGetHeight(mode))
			d.Hz = int(math.Round(cgDisplayModeGetRefreshRate(mode)))
			cgDisplayModeRelease(mode)
		}
		out = append(out, d)
	}
	if len(out) == 0 {
		return out
	}
	names := screenNames()
	for i := range out {
		out[i].Name = names[out[i].ID]
	}
	return out
}

// screenNames maps display IDs to NSScreen's localized names ("Built-in
// Retina Display", "LS27D80xU"). AppKit wants the main thread.
func screenNames() map[uint32]string {
	names := map[uint32]string{}
	if !appKitInit() {
		return names
	}
	runOnMain(func() {
		withPool(func() {
			screens := objc.ID(objc.GetClass("NSScreen")).Send(nsSel("screens"))
			if screens == 0 {
				return
			}
			key := nsString("NSScreenNumber")
			count := objc.Send[uint64](screens, nsSel("count"))
			for i := uint64(0); i < count; i++ {
				screen := screens.Send(nsSel("objectAtIndex:"), i)
				number := screen.Send(nsSel("deviceDescription")).Send(nsSel("objectForKey:"), key)
				if number == 0 {
					continue
				}
				// unsignedIntValue returns 32 bits; the rest of the register isn't ours.
				id := uint32(objc.Send[uint64](number, nsSel("unsignedIntValue")))
				names[id] = nsGoString(screen.Send(nsSel("localizedName")))
			}
		})
	})
	return names
}

// readSSD reads the internal NVMe drive: model, size and SMART health. nil
// when there is nothing to read, as in a virtual machine.
func readSSD() *SSDInfo {
	ssd := SSDInfo{Model: nvmeModel(), Capacity: internalDiskSize()}
	if page, ok := nvmeSMARTLog(); ok {
		smartLogInfo(page[:], &ssd)
	}
	if ssd.Model == "" && ssd.Capacity == 0 && !ssd.SMART {
		return nil
	}
	return &ssd
}

// nvmeModel is the NVMe controller's model number.
func nvmeModel() (model string) {
	ioEach("IONVMeController", func(s uint32) bool {
		model = strings.TrimSpace(ioPropString(s, "Model Number"))
		return model == ""
	})
	return model
}

// internalDiskSize is the size of disk0, the internal drive on Apple
// silicon, as its whole-disk media reports it.
func internalDiskSize() (size uint64) {
	ioEach("IOMedia", func(s uint32) bool {
		if ioPropString(s, "BSD Name") != "disk0" {
			return true
		}
		if n, ok := ioPropInt(s, "Size"); ok && n > 0 {
			size = uint64(n)
		}
		return false
	})
	return size
}

// The NVMe SMART plug-in's identifiers, from IOKit's NVMeSMARTLibExternal.h
// and IOCFPlugIn.h.
const (
	nvmeSMARTUserClientTypeID = "AA0FA6F9-C2D6-457F-B10B-59A13253292F" // kIONVMeSMARTUserClientTypeID
	ioCFPlugInInterfaceID     = "C244E858-109C-11D4-91D4-0050E4C6426F" // kIOCFPlugInInterfaceID
)

// nvmeSMARTInterfaceID is kIONVMeSMARTInterfaceID as the CFUUIDBytes that
// QueryInterface takes by value.
var nvmeSMARTInterfaceID = [16]byte{0xCC, 0xD1, 0xDB, 0x19, 0xFD, 0x9A, 0x4D, 0xAF, 0xBF, 0x95, 0x12, 0x45, 0x4B, 0x23, 0x0A, 0xB6}

// Byte offsets in the plug-in interfaces' function tables: IUnknown's
// QueryInterface and Release, then IONVMeSMARTInterface's SMARTReadData
// (after the 16-bit version and revision, padded to 8).
const (
	comQueryInterface = 8
	comRelease        = 24
	comSMARTReadData  = 40
)

// nvmeSMARTLog reads the internal drive's SMART log through macOS's NVMe
// SMART plug-in. It needs no administrator rights (checked 2026-10-28 as
// uid 501).
func nvmeSMARTLog() (page [512]byte, ok bool) {
	userClient, plugIn := cfUUID(nvmeSMARTUserClientTypeID), cfUUID(ioCFPlugInInterfaceID)
	defer cfRelease(userClient)
	defer cfRelease(plugIn)
	if userClient == 0 || plugIn == 0 {
		return page, false
	}
	ioEach("IONVMeBlockStorageDevice", func(s uint32) bool {
		capable := ioProp(s, "NVMe SMART Capable")
		if capable == 0 {
			return true
		}
		cfRelease(capable)
		var iface uintptr
		var score int32
		if ioCreatePlugInInterfaceForService(s, userClient, plugIn, &iface, &score) != 0 || iface == 0 {
			return true
		}
		defer ioDestroyPlugInInterface(iface)
		// QueryInterface takes the 16-byte CFUUIDBytes by value, in two registers.
		lo := uintptr(binary.LittleEndian.Uint64(nvmeSMARTInterfaceID[:8]))
		hi := uintptr(binary.LittleEndian.Uint64(nvmeSMARTInterfaceID[8:]))
		var smart uintptr
		hr, _, _ := purego.SyscallN(comFunc(iface, comQueryInterface), iface, lo, hi, uintptr(unsafe.Pointer(&smart)))
		if int32(hr) != 0 || smart == 0 {
			return true
		}
		buf := new([512]byte)
		rc, _, _ := purego.SyscallN(comFunc(smart, comSMARTReadData), smart, uintptr(unsafe.Pointer(&buf[0])))
		purego.SyscallN(comFunc(smart, comRelease), smart)
		if int32(rc) != 0 {
			return true
		}
		page, ok = *buf, true
		return false
	})
	return page, ok
}

// cfUUID makes a CFUUID from its text form (+1; release it).
func cfUUID(s string) uintptr {
	str := cfStr(s)
	defer cfRelease(str)
	return cfUUIDCreateFromString(0, str)
}
