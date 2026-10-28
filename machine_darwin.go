//go:build darwin

package main

// machine_darwin.go reads what the home page shows about this Mac (design
// docs/superpowers/specs/2026-10-28-home-machine-design.md §四): the
// identity once per visit (readMachineInfo) and the live readings every
// two seconds (machineReader). None of it needs administrator rights or
// starts a process.

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"golang.org/x/sys/unix"
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
// matter. A level with another name (a future core type) makes the split
// unknown rather than wrong: both counts come back 0 and the page leaves
// the split out.
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
	if perf+eff != int(nativeSysctlUint64("hw.physicalcpu")) {
		return 0, 0
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
// when none of it can be read; a virtual machine's boot disk still reports
// its size, without a model or a SMART log.
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

// nvmeModel is the internal NVMe controller's model number. Drives in
// Thunderbolt or USB4 enclosures are NVMe too; they say they're external.
func nvmeModel() (model string) {
	ioEach("IONVMeController", func(s uint32) bool {
		if ioPropString(s, "Physical Interconnect Location") == "External" {
			return true
		}
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

var (
	smartMu   sync.Mutex
	smartLast *[512]byte // the last log read, for when the drive is busy
)

// nvmeSMARTLog reads the internal drive's SMART log through macOS's NVMe
// SMART plug-in. It needs no administrator rights (checked 2026-10-28 as
// uid 501). The plug-in admits one reader at a time across the system, so
// reads are serialized here, retried while another program holds it, and
// fall back to the last log read.
func nvmeSMARTLog() ([512]byte, bool) {
	smartMu.Lock()
	defer smartMu.Unlock()
	for try := 0; try < 3; try++ {
		page, ok, busy := nvmeSMARTRead()
		if ok {
			smartLast = &page
			return page, true
		}
		if !busy {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if smartLast != nil {
		return *smartLast, true
	}
	return [512]byte{}, false
}

// nvmeSMARTRead reads the SMART log once. busy reports that a drive was
// there but another reader held the plug-in.
func nvmeSMARTRead() (page [512]byte, ok, busy bool) {
	userClient, plugIn := cfUUID(nvmeSMARTUserClientTypeID), cfUUID(ioCFPlugInInterfaceID)
	defer cfRelease(userClient)
	defer cfRelease(plugIn)
	if userClient == 0 || plugIn == 0 {
		return page, false, false
	}
	ioEach("IONVMeBlockStorageDevice", func(s uint32) bool {
		capable := ioProp(s, "NVMe SMART Capable")
		if capable == 0 {
			return true
		}
		cfRelease(capable)
		if nvmeExternal(s) {
			return true
		}
		var iface uintptr
		var score int32
		if rc := ioCreatePlugInInterfaceForService(s, userClient, plugIn, &iface, &score); rc != 0 || iface == 0 {
			// kIOReturnNoResources or kIOReturnExclusiveAccess: someone else has it open.
			busy = busy || uint32(rc) == 0xe00002be || uint32(rc) == 0xe00002c5
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
	return page, ok, busy
}

// nvmeExternal reports whether a block device says it sits outside the
// Mac, like a drive in a Thunderbolt enclosure.
func nvmeExternal(s uint32) bool {
	characteristics := ioProp(s, "Protocol Characteristics")
	defer cfRelease(characteristics)
	return cfGoString(cfDictValue(characteristics, "Physical Interconnect Location")) == "External"
}

// cfUUID makes a CFUUID from its text form (+1; release it).
func cfUUID(s string) uintptr {
	str := cfStr(s)
	defer cfRelease(str)
	return cfUUIDCreateFromString(0, str)
}

// ---- live readings (machineReader) --------------------------------------------

const (
	dieSensorsPerRead = 2 // plus the hottest; the first read takes them all
	displayCheckEvery = 5 // reads between display-list checks (~10 s)
)

// machineReader takes the live readings for one status watch. Only the
// watch loop's goroutine uses it.
type machineReader struct {
	reads int

	clusters  []byte // 'P' / 'E' by logical CPU
	prevTicks [][4]uint32

	prevAt                      time.Time
	diskOK                      bool
	prevDiskRead, prevDiskWrite uint64

	ifKinds    map[string]string // BSD name → "wifi", "ethernet", or "" for another kind
	prevIf     map[string]ifBytes
	store      uintptr // SCDynamicStore
	primaryKey uintptr // its State:/Network/Global/IPv4 key
	netKind    string

	gpu uint32 // the IOAccelerator service

	smc     uint32 // AppleSMC connection; 0 when it couldn't be opened
	fans    int    // FNum; -1 unknown
	smcKeys map[uint32]smcKeyInfo

	hid, hidServices uintptr   // the IOHID client and its sensor array, which keeps the services below alive
	dies             []uintptr // SoC die sensors
	nand             uintptr   // the SSD's NAND sensor
	rot              sensorRotation

	displaySig string
}

// newMachineReader opens what the live readings need; nil when IOKit itself
// is unavailable.
func newMachineReader() machineSampler {
	if !iokitInit() {
		return nil
	}
	r := &machineReader{fans: -1, smcKeys: map[uint32]smcKeyInfo{}}
	r.clusters = cpuClusters()
	r.gpu = ioFirst("IOAccelerator")
	r.openSMC()
	r.openSensors()
	r.openNetwork()
	return r
}

// read takes one live reading. The first call only starts the counters, so
// its rates and per-cluster CPU are unknown.
func (r *machineReader) read(now time.Time) *MachineSample {
	s := newMachineSample()
	s.Thermal = thermalState()
	var elapsed time.Duration
	if !r.prevAt.IsZero() {
		elapsed = now.Sub(r.prevAt)
	}
	r.prevAt = now

	ticks := cpuCoreTicks()
	if r.prevTicks != nil && ticks != nil {
		s.PerfCPU, s.EffCPU = clusterUsage(r.prevTicks, ticks, r.clusters)
	}
	r.prevTicks = ticks

	if bytesRead, bytesWritten, ok := diskBytes(); ok {
		if r.diskOK && elapsed > 0 {
			s.DiskRead = byteRate(r.prevDiskRead, bytesRead, elapsed)
			s.DiskWrite = byteRate(r.prevDiskWrite, bytesWritten, elapsed)
		}
		r.prevDiskRead, r.prevDiskWrite, r.diskOK = bytesRead, bytesWritten, true
	} else {
		r.diskOK = false
	}

	r.readNetwork(s, elapsed)
	s.GPU, s.GPUMemory = r.gpuStats()
	s.FanCount, s.Fans, s.Power = r.fans, r.fanSpeeds(), r.power()

	k := dieSensorsPerRead
	if r.reads == 0 {
		k = len(r.dies) // every sensor once, so the maximum starts right
	}
	for _, i := range r.rot.pick(k) {
		r.rot.record(i, sensorCelsius(r.dies[i]))
	}
	s.ChipTemp = r.rot.highest()
	s.DiskTemp = sensorCelsius(r.nand)

	if r.reads%displayCheckEvery == 0 {
		r.displaySig = displaySignature(cgDisplayIDs())
	}
	s.DisplaySig = r.displaySig
	r.reads++
	return s
}

// close gives back what newMachineReader opened.
func (r *machineReader) close() {
	if r.smc != 0 {
		ioServiceClose(r.smc)
		r.smc = 0
	}
	if r.gpu != 0 {
		ioObjectRelease(r.gpu)
		r.gpu = 0
	}
	cfRelease(r.hidServices)
	cfRelease(r.hid)
	cfRelease(r.primaryKey)
	cfRelease(r.store)
	r.hidServices, r.hid, r.primaryKey, r.store = 0, 0, 0, 0
	r.dies, r.nand = nil, 0
	r.rot = sensorRotation{}
}

// ---- CPU clusters -------------------------------------------------------------

// cpuClusters maps each logical CPU to its cluster, 'P' or 'E', from the
// device tree's cpu nodes. Don't assume the numbering: on an M2 Pro,
// cpu0–3 are the efficiency cores.
func cpuClusters() []byte {
	var out []byte
	ioEach("IOPlatformDevice", func(s uint32) bool {
		kind := ioPropString(s, "cluster-type")
		id, ok := ioPropInt(s, "logical-cpu-id")
		if kind == "" || !ok || id < 0 || id >= 1024 {
			return true
		}
		for int64(len(out)) <= id {
			out = append(out, 0)
		}
		out[id] = kind[0]
		return true
	})
	return out
}

// cpuCoreTicks reads each logical CPU's cumulative ticks: user, system,
// idle, nice.
func cpuCoreTicks() [][4]uint32 {
	if hostPort == 0 {
		return nil
	}
	var count, n uint32
	var info uintptr
	if hostProcessorInfo(hostPort, 2 /* PROCESSOR_CPU_LOAD_INFO */, &count, &info, &n) != 0 || info == 0 {
		return nil
	}
	defer vmDeallocate(machTaskSelf, info, uintptr(n)*4)
	if int(n) < int(count)*4 {
		return nil
	}
	raw := unsafe.Slice((*int32)(*(*unsafe.Pointer)(unsafe.Pointer(&info))), n)
	out := make([][4]uint32, count)
	for i := range out {
		for j := range out[i] {
			out[i][j] = uint32(raw[i*4+j])
		}
	}
	return out
}

// ---- disk ---------------------------------------------------------------------

// diskBytes sums every block storage driver's cumulative bytes read and
// written.
func diskBytes() (read, written uint64, ok bool) {
	ioEach("IOBlockStorageDriver", func(s uint32) bool {
		stats := ioProp(s, "Statistics")
		if stats == 0 {
			return true
		}
		defer cfRelease(stats)
		r, okRead := cfInt(cfDictValue(stats, "Bytes (Read)"))
		w, okWrite := cfInt(cfDictValue(stats, "Bytes (Write)"))
		if okRead && okWrite {
			read += uint64(r)
			written += uint64(w)
			ok = true
		}
		return true
	})
	return read, written, ok
}

// ---- network ------------------------------------------------------------------

// ifBytes is one interface's cumulative traffic.
type ifBytes struct{ in, out uint64 }

// Byte offsets in if_msghdr2, from x/sys/unix's copy of the C struct. The
// messages are packed at 4-byte boundaries, so the fields are read with
// encoding/binary rather than through a struct pointer, which could be
// misaligned.
const (
	ifm2Addrs = int(unsafe.Offsetof(unix.IfMsghdr2{}.Addrs))
	ifm2In    = int(unsafe.Offsetof(unix.IfMsghdr2{}.Data) + unsafe.Offsetof(unix.IfData64{}.Ibytes))
	ifm2Out   = int(unsafe.Offsetof(unix.IfMsghdr2{}.Data) + unsafe.Offsetof(unix.IfData64{}.Obytes))
)

// parseIfList2 reads NET_RT_IFLIST2 messages. Each RTM_IFINFO2 message is
// an if_msghdr2, with 64-bit counters, followed by the interface's
// link-level address, which carries its name. Other messages (the
// interface addresses) are skipped.
func parseIfList2(buf []byte) map[string]ifBytes {
	out := map[string]ifBytes{}
	for off := 0; off+4 <= len(buf); {
		n := int(binary.LittleEndian.Uint16(buf[off:]))
		if n < 4 || off+n > len(buf) {
			break
		}
		msg := buf[off : off+n]
		off += n
		if msg[3] != unix.RTM_IFINFO2 || n < unix.SizeofIfMsghdr2+8 {
			continue
		}
		if binary.LittleEndian.Uint32(msg[ifm2Addrs:])&unix.RTA_IFP == 0 {
			continue
		}
		// sockaddr_dl: sdl_len, sdl_family, sdl_index (2 bytes), sdl_type,
		// sdl_nlen, sdl_alen, sdl_slen, then the name.
		sdl := msg[unix.SizeofIfMsghdr2:]
		nameLen := int(sdl[5])
		if sdl[1] != unix.AF_LINK || 8+nameLen > len(sdl) {
			continue
		}
		out[string(sdl[8:8+nameLen])] = ifBytes{
			in:  binary.LittleEndian.Uint64(msg[ifm2In:]),
			out: binary.LittleEndian.Uint64(msg[ifm2Out:]),
		}
	}
	return out
}

// interfaceBytes reads every interface's 64-bit byte counters from the
// routing socket's interface list. getifaddrs has only 32-bit counters,
// which wrap at 4 GB.
func interfaceBytes() map[string]ifBytes {
	mib := [6]int32{unix.CTL_NET, unix.AF_ROUTE, 0, 0, unix.NET_RT_IFLIST2, 0}
	var size uintptr
	if sysctlMIB(&mib[0], uint32(len(mib)), nil, &size, nil, 0) != 0 || size == 0 {
		return nil
	}
	size += 4096 // room for an interface that appears between the two calls
	buf := make([]byte, size)
	if sysctlMIB(&mib[0], uint32(len(mib)), unsafe.Pointer(&buf[0]), &size, nil, 0) != 0 {
		return nil
	}
	return parseIfList2(buf[:size])
}

// interfaceKinds names each network interface's kind by BSD name: "wifi",
// "ethernet", or "" for anything else (Thunderbolt Bridge, Bluetooth…).
func interfaceKinds() map[string]string {
	kinds := map[string]string{}
	if !scOK {
		return kinds
	}
	all := scNetworkInterfaceCopyAll()
	if all == 0 {
		return kinds
	}
	defer cfRelease(all)
	for i := int64(0); i < cfArrayGetCount(all); i++ {
		iface := cfArrayGetValueAtIndex(all, i)
		name := cfGoString(scNetworkInterfaceGetBSDName(iface))
		if name == "" {
			continue
		}
		switch cfGoString(scNetworkInterfaceGetInterfaceType(iface)) {
		case "IEEE80211":
			kinds[name] = "wifi"
		case "Ethernet":
			kinds[name] = "ethernet"
		default:
			kinds[name] = ""
		}
	}
	return kinds
}

// openNetwork lists the interfaces and opens the dynamic store that names
// the primary one.
func (r *machineReader) openNetwork() {
	r.ifKinds = interfaceKinds()
	if !scOK {
		return
	}
	name := cfStr("Cleanzo")
	r.store = scDynamicStoreCreate(0, name, 0, 0)
	cfRelease(name)
	r.primaryKey = cfStr("State:/Network/Global/IPv4")
}

// primaryInterface is the interface macOS routes through, such as "en0";
// "" when not connected.
func (r *machineReader) primaryInterface() string {
	if r.store == 0 || r.primaryKey == 0 {
		return ""
	}
	state := scDynamicStoreCopyValue(r.store, r.primaryKey)
	if state == 0 {
		return ""
	}
	defer cfRelease(state)
	return cfGoString(cfDictValue(state, "PrimaryInterface"))
}

// readNetwork sums Wi-Fi and Ethernet traffic since the last read (not
// loopback or VPN tunnels, which would count the same bytes twice) and
// names the connection: the primary interface's kind, else the busiest
// interface's.
func (r *machineReader) readNetwork(s *MachineSample, elapsed time.Duration) {
	counts := interfaceBytes()
	primary := r.primaryInterface()
	if _, seen := r.ifKinds[primary]; !seen && strings.HasPrefix(primary, "en") {
		for name, kind := range interfaceKinds() { // an adapter plugged in since the page opened
			r.ifKinds[name] = kind
		}
		if _, seen := r.ifKinds[primary]; !seen {
			r.ifKinds[primary] = "" // don't ask again for this one
		}
	}
	var down, up, most uint64
	busiest, measured := "", false
	for name, cur := range counts {
		if r.ifKinds[name] == "" {
			continue
		}
		prev, ok := r.prevIf[name]
		if !ok || cur.in < prev.in || cur.out < prev.out {
			continue
		}
		measured = true
		down += cur.in - prev.in
		up += cur.out - prev.out
		if d := cur.in - prev.in + cur.out - prev.out; d > most {
			busiest, most = name, d
		}
	}
	r.prevIf = counts
	if measured && elapsed > 0 {
		s.NetDown = float64(down) / elapsed.Seconds()
		s.NetUp = float64(up) / elapsed.Seconds()
	}
	switch {
	case r.ifKinds[primary] != "":
		r.netKind = r.ifKinds[primary]
	case busiest != "":
		r.netKind = r.ifKinds[busiest]
	case primary == "":
		r.netKind = "" // not connected
	}
	s.NetKind = r.netKind
}

// ---- GPU ----------------------------------------------------------------------

// gpuStats reads the accelerator's utilization and the memory it has in use.
func (r *machineReader) gpuStats() (pct float64, mem uint64) {
	pct = -1
	if r.gpu == 0 {
		return pct, 0
	}
	stats := ioProp(r.gpu, "PerformanceStatistics")
	if stats == 0 {
		return pct, 0
	}
	defer cfRelease(stats)
	if v, ok := cfInt(cfDictValue(stats, "Device Utilization %")); ok {
		pct = clampPct(float64(v))
	}
	if v, ok := cfInt(cfDictValue(stats, "In use system memory")); ok && v > 0 {
		mem = uint64(v)
	}
	return pct, mem
}

// ---- SMC: fans and power ------------------------------------------------------

// smcParam is SMCKeyData_t, the AppleSMC user client's 80-byte argument,
// with C's padding spelled out.
type smcParam struct {
	Key      uint32
	Vers     [6]byte
	_        [2]byte
	PLimit   [16]byte
	DataSize uint32 // keyInfo.dataSize
	DataType uint32 // keyInfo.dataType
	DataAttr uint8  // keyInfo.dataAttributes
	_        [3]byte
	Result   uint8
	Status   uint8
	Data8    uint8 // the command
	_        uint8
	Data32   uint32
	Bytes    [32]byte
}

// smcKeyInfo is a key's size and type, which don't change.
type smcKeyInfo struct{ size, typ uint32 }

const (
	smcHandleEvent = 2 // the user client's struct method
	smcReadBytes   = 5
	smcReadKeyInfo = 9
)

// smcKey packs a four-character code, such as a key or a type.
func smcKey(s string) uint32 { return binary.BigEndian.Uint32([]byte(s)) }

// openSMC connects to AppleSMC and reads how many fans there are.
func (r *machineReader) openSMC() {
	svc := ioFirst("AppleSMC")
	if svc == 0 {
		return
	}
	defer ioObjectRelease(svc)
	var conn uint32
	if ioServiceOpen(svc, machTaskSelf, 0, &conn) != 0 || conn == 0 {
		return
	}
	r.smc = conn
	// Fan keys run F0Ac … F9Ac; a larger count isn't a fan count.
	if _, b, ok := r.smcRead("FNum"); ok && len(b) == 1 && b[0] <= 10 {
		r.fans = int(b[0])
	}
}

// smcCall runs one SMC request.
func (r *machineReader) smcCall(in *smcParam) (smcParam, bool) {
	var out smcParam
	size := unsafe.Sizeof(out)
	if ioConnectCallStructMethod(r.smc, smcHandleEvent, unsafe.Pointer(in), size, unsafe.Pointer(&out), &size) != 0 || out.Result != 0 {
		return smcParam{}, false
	}
	return out, true
}

// smcRead reads a key's type and bytes, asking for its size and type once.
func (r *machineReader) smcRead(key string) (typ uint32, data []byte, ok bool) {
	if r.smc == 0 {
		return 0, nil, false
	}
	k := smcKey(key)
	info, known := r.smcKeys[k]
	if !known {
		if out, ok := r.smcCall(&smcParam{Key: k, Data8: smcReadKeyInfo}); ok {
			info = smcKeyInfo{size: out.DataSize, typ: out.DataType}
		}
		r.smcKeys[k] = info // a key this Mac lacks stays at size 0 and isn't asked for again
	}
	if info.size == 0 {
		return 0, nil, false
	}
	out, ok := r.smcCall(&smcParam{Key: k, DataSize: info.size, Data8: smcReadBytes})
	if !ok || info.size > uint32(len(out.Bytes)) {
		return 0, nil, false
	}
	return info.typ, out.Bytes[:info.size], true
}

// smcFloat reads a 32-bit float key, like the fan speeds and the power.
func (r *machineReader) smcFloat(key string) (float64, bool) {
	typ, b, ok := r.smcRead(key)
	if !ok || typ != smcKey("flt ") || len(b) != 4 {
		return 0, false
	}
	v := float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false // JSON can't carry them: the sample would fail to send
	}
	return v, true
}

// fanSpeeds reads each fan's current speed (F<i>Ac). If any fan can't be
// read the speeds are unknown (empty), so a gap never moves a reading onto
// the wrong fan. Never nil.
func (r *machineReader) fanSpeeds() []int {
	speeds := []int{}
	for i := 0; i < r.fans; i++ {
		v, ok := r.smcFloat(fmt.Sprintf("F%dAc", i))
		if !ok || v < 0 {
			return []int{}
		}
		speeds = append(speeds, int(math.Round(v)))
	}
	return speeds
}

// power is the whole system's power draw (PSTR); 0 unknown.
func (r *machineReader) power() float64 {
	if v, ok := r.smcFloat("PSTR"); ok && v > 0 {
		return v
	}
	return 0
}

// ---- temperatures (IOHID) and thermal state -----------------------------------

const (
	hidTemperatureEvent = 15       // kIOHIDEventTypeTemperature
	hidTemperatureField = 15 << 16 // kIOHIDEventFieldTemperatureLevel
)

// openSensors finds the temperature sensors through the IOHID event
// system: the SoC die sensors ("PMU tdie…") and the SSD's NAND sensor.
func (r *machineReader) openSensors() {
	if !hidOK {
		return
	}
	client := hidClientCreate(0)
	if client == 0 {
		return
	}
	matching := cfDictionaryCreateMutable(0, 0, cfKeyCallBacks, cfValueCallBacks)
	for key, v := range map[string]int64{"PrimaryUsagePage": 0xff00, "PrimaryUsage": 5} {
		k, n := cfStr(key), cfNumber(v)
		cfDictionarySetValue(matching, k, n)
		cfRelease(k)
		cfRelease(n)
	}
	hidClientSetMatching(client, matching)
	cfRelease(matching)
	services := hidClientCopyServices(client)
	if services == 0 {
		cfRelease(client)
		return
	}
	r.hid, r.hidServices = client, services
	product := cfStr("Product")
	defer cfRelease(product)
	var mtr []uintptr
	for i := int64(0); i < cfArrayGetCount(services); i++ {
		s := cfArrayGetValueAtIndex(services, i)
		ref := hidServiceCopyProperty(s, product)
		name := cfGoString(ref)
		cfRelease(ref)
		switch {
		case strings.Contains(name, "tdie"):
			r.dies = append(r.dies, s)
		case strings.Contains(name, "MTR Temp Sensor"):
			mtr = append(mtr, s)
		case strings.HasPrefix(name, "NAND") && r.nand == 0:
			r.nand = s
		}
	}
	if len(r.dies) == 0 {
		r.dies = mtr // chips that name their die sensors differently ("pACC MTR Temp Sensor…")
	}
	r.rot = newSensorRotation(len(r.dies))
}

// sensorCelsius reads one temperature sensor; 0 when it can't, or the value
// is implausible.
func sensorCelsius(service uintptr) float64 {
	if service == 0 {
		return 0
	}
	event := hidServiceCopyEvent(service, hidTemperatureEvent, 0, 0)
	if event == 0 {
		return 0
	}
	defer cfRelease(event)
	t := hidEventGetFloatValue(event, hidTemperatureField)
	if !(t > 0 && t <= 150) { // also rejects NaN, which JSON can't carry
		return 0
	}
	return t
}

// thermalState is NSProcessInfo's thermal state: 0 nominal, 1 fair, 2
// serious, 3 critical; -1 when it can't be read. NSProcessInfo is
// thread-safe, so this runs on the loop's goroutine, inside a pool on a
// locked thread.
func thermalState() int {
	if !appKitInit() {
		return -1
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	state := -1
	withPool(func() {
		info := objc.ID(objc.GetClass("NSProcessInfo")).Send(nsSel("processInfo"))
		if info != 0 {
			state = int(objc.Send[int64](info, nsSel("thermalState")))
		}
	})
	return state
}

// ---- battery extras -------------------------------------------------------------

// smartBatteryExtras reads what system_profiler leaves out: the battery's
// temperature (AppleSmartBattery "Temperature", in hundredths of a degree)
// and, when plugged in, the adapter's rated watts.
func smartBatteryExtras(plugged bool) (tempC float64, watts int) {
	if !iokitInit() {
		return 0, 0
	}
	battery := ioFirst("AppleSmartBattery")
	if battery == 0 {
		return 0, 0
	}
	defer ioObjectRelease(battery)
	if t, ok := ioPropInt(battery, "Temperature"); ok && t > 0 {
		tempC = float64(t) / 100
	}
	if plugged {
		if details := ioProp(battery, "AdapterDetails"); details != 0 {
			if w, ok := cfInt(cfDictValue(details, "Watts")); ok && w > 0 {
				watts = int(w)
			}
			cfRelease(details)
		}
	}
	return tempC, watts
}
