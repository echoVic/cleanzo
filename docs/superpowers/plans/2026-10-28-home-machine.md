# Home Page About the Machine — Implementation Plan

> **For agentic workers:** In this project the tasks are executed by traecli and the Codex CLI (see "Execution batches"). **Executors do not commit**: the reviewer checks and commits each task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** The macOS home page (系统状态) stops listing apps and talks only about the machine: what this Mac is, how healthy it is (thermal state, chip temperature, fans, power), and live CPU (performance and efficiency cores), GPU, memory, disk (with the SSD's model, health, life used, data written, temperature and read/write speed), network and battery (with its temperature and the adapter's watts). The Windows home page stays as it is.

**Architecture:**
- **Task 1 (Go):** `iokit_darwin.go` binds CoreFoundation, IOKit (with the private IOHID event-system client the temperature sensors sit behind), CoreGraphics, SystemConfiguration and three libSystem calls through purego, plus small helpers. A spike in a scratch module (`/tmp/czt/machprobe`, 2026-10-28) ran every binding on the maintainer's MacBook Pro (16-inch, 2023, M2 Pro, macOS 26.6.2) as uid 501, without sudo.
- **Task 2 (Go):** a new bound method, `Cleaner.MachineInfo()`, returns the identity: the device tree's model name, `hw.model`, performance and efficiency core counts, GPU cores, the active displays with names and modes, and the internal SSD (model, capacity and the SMART log through macOS's NVMe SMART plug-in). The page calls it on entry, and again when a sample's display signature changes.
- **Task 3 (Go):** the status loop gets an optional `machineSampler`. On macOS, `realStatusSource` drops the process and NSWorkspace reads (there is no app list any more) and adds a `machineReader`, which takes the live readings with each 2 s sample:
  - thermal state;
  - chip temperature: 2 die sensors in turn plus the hottest one per read (all of them on the first read), maximum of each sensor's latest reading;
  - fans and system power from the SMC, its key info cached;
  - per-cluster CPU from per-core ticks;
  - GPU utilization and memory from the cached accelerator service;
  - disk and network rates, the network's kind;
  - the SSD's NAND temperature;
  - the display signature, every fifth read.

  `StatusSample` gains `machine`; `BatteryStatus` gains the battery temperature and the adapter's watts.
- **Task 4 (web):**
  - the macOS header: the model as the title, a spec line, and an OS / uptime / displays line;
  - a health strip;
  - six cards: CPU with P/E rows, a new GPU card, memory, disk with SSD rows, a new network card, battery with temperature and adapter;
  - the top-apps card and its quit dialog render on Windows only;
  - the memory alert opens the 进程 page's 内存 tab on macOS;
  - the dev mock, README and CHANGELOG.

**Spike measurements** (per call, on the maintainer's Mac):

| Read | Cost |
|------|------|
| per-core CPU ticks | 21 µs |
| disk statistics (enumerate the drivers) | 345 µs |
| network counters (`NET_RT_IFLIST2`) | 90 µs |
| primary interface (SCDynamicStore) | 89 µs |
| GPU statistics, cached service | 62–131 µs |
| SMC `FNum` + 2 fans + `PSTR`, key info not cached | 1.6 ms |
| thermal state | 15 µs |
| one IOHID temperature sensor | ~1.1–1.3 ms (all 64: 68 ms) |
| active display list / with modes | 456 µs / 834 µs |
| SMART log (once per visit) | 39 ms |

The reads per sample come to about 6 ms every 2 s, about 0.3% of a core: 3 die sensors and the NAND sensor (~4.5 ms), the SMC with cached key info (~0.6 ms), disk, network, GPU and a fifth of a display check. Dropping the app list saves about 35 ms per sample: the process reads plus NSWorkspace.

**Tech Stack:** Go 1.27.3, mygo 0.7.1, purego v0.11.1 and golang.org/x/sys v0.49.0 (already imported by `process_darwin.go`). TypeScript and React 19 with bun test. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-28-home-machine-design.md` (Chinese), approved by the maintainer. The mockup is `.superpowers/brainstorm/36592-1793158059/content/home-machine-v2.html` (not in the repository).

## Global Constraints

- **The macOS home page lists no apps.** Every alert stays, including the crash-dump and growth alerts, which name apps (spec §三.3).
- **The Windows home page is unchanged**, including the app list and its quit flow (spec §二 非目标). `status_windows.go` is not touched.
- **No administrator rights and no child processes** for anything new (spec §四, §五). The battery's existing `system_profiler`, once a minute, stays.
- **Read only while the home page is visible.** No background sampling, no history (spec §二).
- **Not read in this version:** CPU and GPU power or frequency, the Wi-Fi name, the serial number (spec §二).
- **Budget** (spec §五): with the home page visible, Cleanzo's main process plus children ≤ 1% of one core; the web process ≤ 1.5%.
- **Unknown values** never show as blank, 0, NaN, `-1` or an error: the page leaves them out or writes 「—」 (spec §二, §六.2). Go's sentinels:
  - temperatures, power and byte counts: 0;
  - percentages and rates: -1;
  - `FanCount`: -1 (0 means the Mac has no fans);
  - `Thermal`: -1.
- **Platform rules** (enforced by `testenv_test.go`):
  - Exported Go names and `Cleaner` methods live in untagged files. `_darwin.go` and build-tagged files hold unexported names only (`TestBindingsArePlatformNeutral`).
  - Untagged non-test `.go` files build paths with `filepath` (`TestNoSlashPathLiterals`).
- **Bindings are generated.** After changing bound types or methods, run `bun run generate`. Never edit `src/mygo.ts` by hand.
- **i18n:**
  - every new key exists in both `zh` and `en`;
  - no value may contain macOS terms such as "Mac" or "macOS": `tests/i18n-windows.test.ts` resolves every key under Windows.
- **No new dependencies**, Go or npm. Don't run commands that need the network.
- **Executors do not commit.** Leave the changes in the working tree.

## Review Focus

1. **CoreFoundation and IOKit ownership** (Tasks 1–3):
   - Every Create or Copy result is released exactly once.
   - Get results are never released: dictionary values, array items, SCNetworkInterface names and types.
   - Registry entries and iterators get `IOObjectRelease`.
   - The SMART interface is released and the plug-in destroyed.
   - Nothing calls `CFRelease(NULL)`.
2. **purego memory safety:**
   - Go memory reaches C only through typed arguments of registered functions, or as `uintptr(unsafe.Pointer(&x))` written directly in a `purego.SyscallN` call. `SyscallN` carries `//go:uintptrescapes`, so those pointers stay alive and in place for the call.
   - Routing-socket messages are parsed with `encoding/binary`, never through a struct pointer into the buffer: the messages sit at 4-byte boundaries, and `-race`'s checkptr rejects a misaligned `*unix.IfMsghdr2`.
3. **Threads:**
   - NSScreen only inside `runOnMain`.
   - NSProcessInfo on a locked OS thread inside an autorelease pool.
   - The machine reader is used only by the watch loop's goroutine.
4. **Cost per sample:**
   - no enumeration of every IOService or IOPlatformDevice per sample;
   - 2 die sensors plus the hottest per read (all of them only on the first read);
   - the display list every fifth read;
   - SMC key info cached, the GPU service cached.
5. **Sentinels end to end:**
   - Go reports unknowns with the sentinels above, and the page hides or dashes them.
   - A fanless Mac says 「无风扇」; stopped fans say 「未转动」.
6. **Windows:**
   - `status_windows.go` is untouched, and its samples carry `machine: null`.
   - The Windows page renders as before. The 「查看全部」 button that goes away was macOS-only.

## Decisions this plan makes

The spec leaves these to the implementation (§七), or they follow from the spike. Reviewers, please check them.

- **Unknown readings:**
  - **health tiles:** a tile that can't be read is left out, and the strip disappears when all four are unknown;
  - **card rows:** a row that can't be read is left out;
  - **card hero numbers and core counts:** 「—」, like today's cards.
- **Charts:** GPU and network keep 30 points (1 minute), like CPU. GPU uses `--seg-wired` (purple), network `--seg-app` (blue). The network chart is download speed, scaled to the minute's maximum with a 100 KB/s floor.
- **Health strip copy:** thermal states 0–3 read 「正常 / 偏热 / 很热 / 过热」, as in the spec's draft.
- **Card order on macOS:** CPU, GPU, memory, then disk, network, battery, as in mockup v2. The Windows order is unchanged.
- **Where the data goes:** the live readings ride on the existing `StatusSample` as `machine`; there is no second stream. The identity is a separate call.
- **Fallbacks for private or missing interfaces:**
  - **chip temperature:** sensors named `tdie`; if there are none, sensors named `MTR Temp Sensor` (older chips); if neither, unknown;
  - **SMC:** if it can't be opened, fans and power are unknown;
  - **fan count:** `FNum` missing means unknown, not 0.
- **Chip temperature:** spec §八 asks for the hottest die sensor. Reading all 26 every 2 s costs about 30 ms. So each read takes 2 sensors in turn plus the one that last read hottest; the first read of a visit takes all of them. When the chip cools, the shown maximum follows within a few samples.
- **Displays:** the samples carry a signature of the active display IDs, checked every fifth read (about 10 s). When it differs from the identity's, the page reads `MachineInfo` again. A resolution change on the same display shows on the next visit.
- **Network:** rates count Wi-Fi and Ethernet interfaces only. Loopback and VPN tunnels would count the same bytes twice. The kind is the primary interface's; failing that, the busiest interface's.
- **Units:**
  - rates and SSD sizes are decimal (KB/s, MB/s, GB, TB), like Activity Monitor's disk and network columns and System Information;
  - memory stays binary, as today;
  - the spec line's disk is the whole internal disk (500 GB here), not the volume.
- **NAND temperature** is read every sample, as spec §四 says; it costs one sensor read.
- **CHANGELOG:** the unreleased bullets that describe the top-apps card on macOS change to match. The fix bullet about its CPU being 40× too low goes away, since that list no longer exists on macOS.

## File Structure

| File | Task | Change |
|------|------|--------|
| `iokit_darwin.go` | 1 | **New.** purego bindings and CF/IOKit helpers |
| `iokit_darwin_test.go` | 1 | **New.** |
| `machine.go` | 2, 3 | **New.** `MachineInfo`, `DisplayInfo`, `SSDInfo`, `Cleaner.MachineInfo` (2); `MachineSample`, `machineSampler` and the pure rules (3) |
| `machine_test.go` | 2, 3 | **New.** pure-rule tests |
| `machine_darwin.go` | 2, 3 | **New.** identity reads (2); `machineReader`, `smartBatteryExtras` (3) |
| `machine_darwin_test.go` | 2, 3 | **New.** live tests (strict with `CLEANZO_HW=1`) and the budget test (`CLEANZO_PERF=1`) |
| `machine_other.go` | 2 | **New.** `readMachineInfo` for Windows |
| `src/mygo.ts` | 2, 3 | regenerated |
| `monitor.go` | 3 | `BatteryStatus` fields, `StatusSample.Machine`, `statusSource.machine`, the loop |
| `monitor_test.go` | 3 | the loop without an app list |
| `status_darwin.go` | 3 | no process or app reads; the machine reader; battery extras |
| `process_darwin_test.go` | 3 | uses the shared `rusageCPU` |
| `src/dev/mock-mygo.ts` | 3, 4 | the new sample fields (3); `MachineInfo` and live machine readings (4) |
| `src/react/status/api.ts` | 4 | `machine()` |
| `src/react/status/format.ts` | 4 | model label, rate / temperature / size formats, SSD warnings, display text |
| `src/react/status/StatusPage.tsx` | 4 | header, health strip, GPU and network cards, card extensions, Windows-only list |
| `src/i18n.ts` | 4 | strings; `status.apps.viewAll` removed |
| `src/style.css` | 4 | header, health strip, card bits; `.st-viewall` removed |
| `tests/status-react.test.ts` | 4 | new tests; the View All test rewritten |
| `README.md`, `CHANGELOG.md` | 4 | docs |

The tasks run in order. Each needs the previous one's code, and Task 4 needs the TypeScript types that Tasks 2 and 3 generate.

---

### Task 1: Go — CoreFoundation, IOKit and friends through purego

**Files:**
- Create: `iokit_darwin.go`
- Create: `iokit_darwin_test.go`

**Interfaces:**
- Consumes: `nativeInit()` and `hostPort uint32` from `native.go`.
- Produces (darwin-only, unexported):
  - `iokitInit() bool`: true when CoreFoundation, IOKit and libSystem loaded. The flags `hidOK`, `cgOK`, `scOK bool` say the same for the IOHID client, CoreGraphics and SystemConfiguration. `machTaskSelf uint32`.
  - Function variables named after the C functions:
    - CoreFoundation: `cfDictionaryCreateMutable`, `cfDictionarySetValue`, `cfArrayGetCount`, `cfArrayGetValueAtIndex`, `cfUUIDCreateFromString`, plus `cfKeyCallBacks` and `cfValueCallBacks uintptr`;
    - IOKit: `ioRegistryEntryFromPath`, `ioObjectRelease`, `ioServiceOpen`, `ioServiceClose`, `ioConnectCallStructMethod`, `ioCreatePlugInInterfaceForService`, `ioDestroyPlugInInterface`;
    - IOHID: `hidClientCreate`, `hidClientSetMatching`, `hidClientCopyServices`, `hidServiceCopyProperty`, `hidServiceCopyEvent`, `hidEventGetFloatValue`;
    - libSystem: `hostProcessorInfo`, `vmDeallocate`, `sysctlMIB`;
    - CoreGraphics: `cgGetActiveDisplayList`, `cgDisplayIsBuiltin`, `cgDisplayCopyDisplayMode`, `cgDisplayModeGetWidth`, `cgDisplayModeGetHeight`, `cgDisplayModeGetRefreshRate`, `cgDisplayModeRelease`;
    - SystemConfiguration: `scNetworkInterfaceCopyAll`, `scNetworkInterfaceGetBSDName`, `scNetworkInterfaceGetInterfaceType`, `scDynamicStoreCreate`, `scDynamicStoreCopyValue`.
  - Helpers:
    - `cfRelease(ref uintptr)`, `cfStr(s string) uintptr`, `cfGoString(ref uintptr) string`, `cString(b []byte) string`;
    - `cfInt(ref uintptr) (int64, bool)`, `cfNumber(v int64) uintptr`, `cfDictValue(dict uintptr, key string) uintptr`;
    - `ioProp(entry uint32, key string) uintptr`, `ioPropInt(entry uint32, key string) (int64, bool)`, `ioPropString(entry uint32, key string) string`;
    - `ioEach(class string, fn func(service uint32) bool)`, `ioFirst(class string) uint32`;
    - `comFunc(iface, off uintptr) uintptr`.
  - Test helper `onThisMac() bool` in `iokit_darwin_test.go`: true when `CLEANZO_HW` is set.

- [ ] **Step 1: Write the failing tests**

Create `iokit_darwin_test.go`:

```go
//go:build darwin

package main

import (
	"os"
	"testing"
)

// onThisMac turns on the strict hardware checks: set CLEANZO_HW=1 on a
// real Mac. CI's virtual machines and the executors' sandboxes lack the
// sensors, the SMC and SMART, so without it the live tests check only what
// they did read.
func onThisMac() bool { return os.Getenv("CLEANZO_HW") != "" }

func TestIOKitLoads(t *testing.T) {
	if !iokitInit() {
		t.Fatal("CoreFoundation, IOKit and libSystem load on every Mac")
	}
	if machTaskSelf == 0 {
		t.Error("mach_task_self_ was not read")
	}
	t.Logf("IOHID %v, CoreGraphics %v, SystemConfiguration %v", hidOK, cgOK, scOK)
	if onThisMac() && !(hidOK && cgOK && scOK) {
		t.Error("on a real Mac every group loads")
	}
}

func TestCFHelpers(t *testing.T) {
	if !iokitInit() {
		t.Skip("IOKit is not available")
	}
	const text = "MacBook Pro (16-inch, 2023) · 中文"
	s := cfStr(text)
	defer cfRelease(s)
	if got := cfGoString(s); got != text {
		t.Errorf("cfGoString = %q, want %q", got, text)
	}
	n := cfNumber(1 << 40)
	defer cfRelease(n)
	if v, ok := cfInt(n); !ok || v != 1<<40 {
		t.Errorf("cfInt = %d, %v", v, ok)
	}
	if _, ok := cfInt(s); ok {
		t.Error("a CFString is not a number")
	}
	if cfGoString(n) != "" {
		t.Error("a CFNumber is not a string")
	}
	cfRelease(0) // CFRelease(NULL) would trap
	if _, ok := cfInt(0); ok || cfGoString(0) != "" {
		t.Error("NULL reads as nothing")
	}

	dict := cfDictionaryCreateMutable(0, 0, cfKeyCallBacks, cfValueCallBacks)
	defer cfRelease(dict)
	key := cfStr("Watts")
	cfDictionarySetValue(dict, key, n)
	cfRelease(key)
	if v, ok := cfInt(cfDictValue(dict, "Watts")); !ok || v != 1<<40 {
		t.Errorf("cfDictValue(Watts) = %d, %v", v, ok)
	}
	if cfDictValue(dict, "Missing") != 0 || cfDictValue(0, "Watts") != 0 {
		t.Error("a missing key or dictionary reads as 0")
	}
}

func TestCString(t *testing.T) {
	for in, want := range map[string]string{
		"Mac14,10\x00\x00junk": "Mac14,10",
		"no terminator":        "no terminator",
		"":                     "",
	} {
		if got := cString([]byte(in)); got != want {
			t.Errorf("cString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIOEach(t *testing.T) {
	if !iokitInit() {
		t.Skip("IOKit is not available")
	}
	seen := 0
	ioEach("IOMedia", func(uint32) bool { seen++; return true })
	if seen == 0 {
		t.Skip("no IOMedia here")
	}
	visited := 0
	ioEach("IOMedia", func(uint32) bool { visited++; return false })
	if visited != 1 {
		t.Errorf("ioEach went on after fn returned false: %d visits", visited)
	}
	ioEach("CleanzoNoSuchClass", func(uint32) bool {
		t.Error("visited a class that doesn't exist")
		return true
	})
	if s := ioFirst("CleanzoNoSuchClass"); s != 0 {
		ioObjectRelease(s)
		t.Error("ioFirst found a class that doesn't exist")
	}
}

func TestIOPropReadsTheDeviceTree(t *testing.T) {
	if !iokitInit() {
		t.Skip("IOKit is not available")
	}
	product := ioRegistryEntryFromPath(0, "IODeviceTree:/product")
	if product == 0 {
		t.Skip("no IODeviceTree:/product here")
	}
	defer ioObjectRelease(product)
	name := ioPropString(product, "product-name")
	t.Logf("product-name %q", name)
	if ref := ioProp(product, "cleanzo-no-such-property"); ref != 0 {
		cfRelease(ref)
		t.Error("a missing property reads as 0")
	}
	if onThisMac() && name == "" {
		t.Error("a real Mac's device tree names the product")
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test -count=1 -run 'TestIOKitLoads|TestCFHelpers|TestCString|TestIOEach|TestIOPropReadsTheDeviceTree' .`
Expected: the build fails with `undefined: iokitInit` and the other new names.

- [ ] **Step 3: Write `iokit_darwin.go`**

```go
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
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test -count=1 -run 'TestIOKitLoads|TestCFHelpers|TestCString|TestIOEach|TestIOPropReadsTheDeviceTree' -v .`
Expected: PASS. In a sandbox, `TestIOEach` or `TestIOPropReadsTheDeviceTree` may SKIP.

- [ ] **Step 5: Check the whole package**

Run: `gofmt -l . && go vet ./... && GOOS=windows GOARCH=amd64 go vet ./... && go test -count=1 -race -run 'TestIOKit|TestCFHelpers|TestCString|TestIOEach|TestIOProp|TestBindingsArePlatformNeutral|TestNoSlashPathLiterals' .`
Expected: `gofmt` prints nothing; vet and the tests pass.

- [ ] **Step 6: Leave the changes uncommitted** for the reviewer, who also runs the strict checks outside any sandbox: `CLEANZO_HW=1 go test -count=1 -run 'TestIOKitLoads|TestIOPropReadsTheDeviceTree' -v .`

---

### Task 2: Go — what this Mac is (`MachineInfo`)

**Files:**
- Create: `machine.go`, `machine_test.go`, `machine_darwin.go`, `machine_darwin_test.go`, `machine_other.go`
- Modify: `src/mygo.ts` (generated)

**Interfaces:**
- Consumes:
  - from Task 1: `iokitInit`, `cgOK`, `ioEach`, `ioProp`, `ioPropInt`, `ioPropString`, `ioRegistryEntryFromPath`, `ioObjectRelease`, `cfRelease`, `cfStr`, `cfUUIDCreateFromString`, `ioCreatePlugInInterfaceForService`, `ioDestroyPlugInInterface`, `comFunc`, the `cg*` functions, `onThisMac`;
  - from `native.go`: `nativeSysctlString`, `nativeSysctlUint64`, `appKitInit`, `withPool`, `nsSel`, `nsGoString`, `runOnMain`;
  - from `appicon_darwin.go`: `nsString(s string) objc.ID`.
- Produces:
  - Untagged `machine.go`:
    - `type MachineInfo struct { Model string; ModelID string; PerfCores, EffCores, GPUCores int; Displays []DisplayInfo; DisplaySig string; SSD *SSDInfo }`, JSON names `model, modelId, perfCores, effCores, gpuCores, displays, displaySig, ssd`.
    - `type DisplayInfo struct { ID uint32; Name string; Builtin bool; Width, Height, Hz int }`, JSON `id, name, builtin, width, height, hz`.
    - `type SSDInfo struct { Model string; Capacity uint64; SMART bool; Warning, TempC, Spare, Used int; Written uint64 }`, JSON `model, capacity, smart, warning, tempC, spare, used, written`.
    - `func (Cleaner) MachineInfo() MachineInfo`. TypeScript: `Cleaner.machineInfo(): Promise<MachineInfo>`.
    - `smartLogInfo(page []byte, ssd *SSDInfo) bool` and `displaySignature(ids []uint32) string`.
  - `readMachineInfo() MachineInfo` on both platforms.
  - darwin `cgDisplayIDs() []uint32`, used again by Task 3.

- [ ] **Step 1: Write the failing tests**

Create `machine_test.go`:

```go
package main

import (
	"encoding/binary"
	"slices"
	"testing"
)

func TestSmartLogInfo(t *testing.T) {
	page := make([]byte, 512)
	page[0] = 0x05                                // critical warning: spare and reliability bits
	binary.LittleEndian.PutUint16(page[1:3], 306) // 33 °C in kelvin
	page[3] = 100                                 // available spare
	page[5] = 26                                  // percentage used
	binary.LittleEndian.PutUint64(page[48:56], 1219727)
	var ssd SSDInfo
	if !smartLogInfo(page, &ssd) {
		t.Fatal("a full log page reads")
	}
	want := SSDInfo{SMART: true, Warning: 5, TempC: 33, Spare: 100, Used: 26, Written: 1219727 * 512000}
	if ssd != want {
		t.Errorf("got %+v, want %+v", ssd, want)
	}

	var short SSDInfo
	if smartLogInfo(page[:63], &short) || short.SMART {
		t.Error("a short page must not count as read")
	}

	binary.LittleEndian.PutUint16(page[1:3], 0)
	var cold SSDInfo
	smartLogInfo(page, &cold)
	if cold.TempC != 0 {
		t.Errorf("an unreported temperature stays 0, got %d", cold.TempC)
	}
}

func TestDisplaySignature(t *testing.T) {
	ids := []uint32{3, 1}
	if got := displaySignature(ids); got != "1,3" {
		t.Errorf("displaySignature = %q, want 1,3", got)
	}
	if !slices.Equal(ids, []uint32{3, 1}) {
		t.Error("displaySignature must not reorder its argument")
	}
	if displaySignature([]uint32{1, 3}) != displaySignature([]uint32{3, 1}) {
		t.Error("the order of the displays doesn't matter")
	}
	if displaySignature(nil) != "" {
		t.Error("no displays, empty signature")
	}
}
```

Create `machine_darwin_test.go`:

```go
//go:build darwin

package main

import "testing"

func TestReadMachineInfoLive(t *testing.T) {
	// NSScreen wants the main thread, which a test binary never serves, so
	// the names stay out here; the reviewer checks them in the app.
	prev := runOnMain
	runOnMain = func(func()) {}
	t.Cleanup(func() { runOnMain = prev })

	info := readMachineInfo()
	t.Logf("%+v", info)
	if info.SSD != nil {
		t.Logf("SSD %+v", *info.SSD)
	}
	if info.Displays == nil {
		t.Error("Displays must be [] rather than null")
	}
	if p, e := info.PerfCores, info.EffCores; p > 0 && e > 0 {
		if total := int(nativeSysctlUint64("hw.physicalcpu")); p+e != total {
			t.Errorf("%d performance + %d efficiency cores, hw.physicalcpu %d", p, e, total)
		}
	}
	for _, d := range info.Displays {
		if d.ID == 0 || d.Width <= 0 || d.Height <= 0 || d.Hz < 0 {
			t.Errorf("display %+v", d)
		}
	}
	if s := info.SSD; s != nil && s.SMART && (s.TempC < 0 || s.TempC > 120 || s.Spare > 100) {
		t.Errorf("implausible SMART log %+v", *s)
	}
	if !onThisMac() {
		return
	}
	if info.Model == "" || info.ModelID == "" || info.PerfCores == 0 || info.EffCores == 0 ||
		info.GPUCores == 0 || len(info.Displays) == 0 || info.DisplaySig == "" {
		t.Errorf("on a real Mac every identity field reads: %+v", info)
	}
	if s := info.SSD; s == nil || s.Model == "" || s.Capacity == 0 || !s.SMART || s.Written == 0 {
		t.Errorf("on a real Mac the SSD and its SMART log read: %+v", s)
	}
}
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test -count=1 -run 'TestSmartLogInfo|TestDisplaySignature|TestReadMachineInfoLive' .`
Expected: the build fails with `undefined: SSDInfo`, `smartLogInfo`, `displaySignature`, `readMachineInfo`.

- [ ] **Step 3: Write `machine.go`**

```go
package main

// machine.go is the home page's machine side (design:
// docs/superpowers/specs/2026-10-28-home-machine-design.md). MachineInfo
// says what this Mac is; the page reads it when it opens and again when
// the displays change. The reads are in machine_darwin.go; the rules here
// are pure, so they test on any platform.

import (
	"encoding/binary"
	"slices"
	"strconv"
	"strings"
)

// MachineInfo says what this Mac is. Fields that cannot be read stay zero
// or empty, and the page leaves them out.
type MachineInfo struct {
	Model      string        `json:"model"`      // the device tree's product-name, e.g. "MacBook Pro (16-inch, 2023)"
	ModelID    string        `json:"modelId"`    // hw.model, e.g. "Mac14,10"
	PerfCores  int           `json:"perfCores"`  // performance cores
	EffCores   int           `json:"effCores"`   // efficiency cores
	GPUCores   int           `json:"gpuCores"`   // GPU cores
	Displays   []DisplayInfo `json:"displays"`   // the active displays; never null
	DisplaySig string        `json:"displaySig"` // the active displays' IDs; samples carry it too, so the page re-reads on a change
	SSD        *SSDInfo      `json:"ssd"`        // nil: no internal NVMe drive could be read
}

// DisplayInfo is one active display in its current mode.
type DisplayInfo struct {
	ID      uint32 `json:"id"`
	Name    string `json:"name"` // NSScreen's localized name; "" unknown
	Builtin bool   `json:"builtin"`
	Width   int    `json:"width"`  // the mode's width in points
	Height  int    `json:"height"` // the mode's height in points
	Hz      int    `json:"hz"`     // refresh rate; 0 unknown
}

// SSDInfo is the internal drive. Warning through Written are valid only
// when SMART is true.
type SSDInfo struct {
	Model    string `json:"model"`    // e.g. "APPLE SSD AP0512Z"; "" unknown
	Capacity uint64 `json:"capacity"` // bytes, the whole disk; 0 unknown
	SMART    bool   `json:"smart"`    // the SMART log was read
	Warning  int    `json:"warning"`  // NVMe critical warning bits; 0 is healthy
	TempC    int    `json:"tempC"`    // composite temperature when read; 0 unknown
	Spare    int    `json:"spare"`    // available spare, percent
	Used     int    `json:"used"`     // percentage of rated life used; can pass 100
	Written  uint64 `json:"written"`  // bytes written over the drive's life
}

// MachineInfo reads what this Mac is: model, cores, GPU, displays and the
// internal SSD. Off macOS it is empty.
func (Cleaner) MachineInfo() MachineInfo {
	return readMachineInfo()
}

// smartLogInfo fills ssd from an NVMe SMART / Health Information log page
// (log page 02h): byte 0 critical warning, 1–2 composite temperature in
// kelvin, 3 available spare, 5 percentage used, 48–63 data units written,
// each unit 1000 × 512 bytes. It reports whether the page was long enough.
func smartLogInfo(page []byte, ssd *SSDInfo) bool {
	if len(page) < 64 {
		return false
	}
	ssd.SMART = true
	ssd.Warning = int(page[0])
	if kelvin := int(binary.LittleEndian.Uint16(page[1:3])); kelvin > 273 {
		ssd.TempC = kelvin - 273
	}
	ssd.Spare = int(page[3])
	ssd.Used = int(page[5])
	// The unit count is 128 bits; the low 64 are plenty.
	ssd.Written = binary.LittleEndian.Uint64(page[48:56]) * 512000
	return true
}

// displaySignature names a set of displays by their IDs, in any order.
func displaySignature(ids []uint32) string {
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	parts := make([]string, len(sorted))
	for i, id := range sorted {
		parts[i] = strconv.FormatUint(uint64(id), 10)
	}
	return strings.Join(parts, ",")
}
```

- [ ] **Step 4: Write `machine_other.go`**

```go
//go:build !darwin

package main

// readMachineInfo: the machine reads are macOS's; the Windows home page
// keeps its own layout.
func readMachineInfo() MachineInfo {
	return MachineInfo{Displays: []DisplayInfo{}}
}
```

- [ ] **Step 5: Write `machine_darwin.go`**

```go
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
```

- [ ] **Step 6: Run the tests to see them pass**

Run: `go test -count=1 -run 'TestSmartLogInfo|TestDisplaySignature|TestReadMachineInfoLive' -v .`
Expected: PASS. The live test logs the identity. In a sandbox it may log an empty SSD or no displays.

- [ ] **Step 7: Generate the bindings and type-check**

Run: `bun run generate && bun run typecheck`
Expected: `src/mygo.ts` gains `MachineInfo`, `DisplayInfo`, `SSDInfo` and `Cleaner.machineInfo()`; typecheck passes. The dev mock's handler table is `Partial`, so it needs no change yet.

- [ ] **Step 8: Check the whole package**

Run: `gofmt -l . && go vet ./... && GOOS=windows GOARCH=amd64 go vet ./... && go test -count=1 ./...`
Expected: `gofmt` prints nothing and everything passes. In a sandbox, add the skip list from "Execution batches".

- [ ] **Step 9: Leave the changes uncommitted.** The reviewer runs `CLEANZO_HW=1 go test -count=1 -run TestReadMachineInfoLive -v .` outside any sandbox: every identity field and the SMART log must read.

---

### Task 3: Go — live readings on the status samples, no app list on macOS

**Files:**
- Modify: `machine.go`, `machine_test.go`, `machine_darwin.go`, `machine_darwin_test.go`
- Modify: `monitor.go`, `monitor_test.go`, `status_darwin.go`, `process_darwin_test.go`
- Modify: `src/dev/mock-mygo.ts`, `src/mygo.ts` (generated)

**Interfaces:**
- Consumes:
  - from Tasks 1 and 2: the bindings and helpers, `displaySignature`, `cgDisplayIDs`, `onThisMac`;
  - from `native.go`: `hostPort`, `appKitInit`, `withPool`, `nsSel`;
  - from `monitor.go`: `clampPct`.
- Produces:
  - Untagged `machine.go`:
    - `type MachineSample struct { Thermal int; ChipTemp float64; FanCount int; Fans []int; Power, PerfCPU, EffCPU, GPU float64; GPUMemory uint64; DiskRead, DiskWrite, DiskTemp, NetDown, NetUp float64; NetKind, DisplaySig string }`, JSON `thermal, chipTemp, fanCount, fans, power, perfCpu, effCpu, gpu, gpuMemory, diskRead, diskWrite, diskTemp, netDown, netUp, netKind, displaySig`.
    - `newMachineSample() *MachineSample`: everything unknown, `Fans` non-nil.
    - `type machineSampler interface { read(now time.Time) *MachineSample; close() }`.
    - `clusterUsage(prev, cur [][4]uint32, kinds []byte) (perf, eff float64)`.
    - `byteRate(prev, cur uint64, elapsed time.Duration) float64`.
    - `type sensorRotation` with `newSensorRotation(n int)`, `pick(k int) []int`, `record(i int, celsius float64)`, `hottest() int`, `highest() float64`.
  - darwin:
    - `newMachineReader() machineSampler`;
    - `smartBatteryExtras(plugged bool) (tempC float64, watts int)`;
    - `type ifBytes struct{ in, out uint64 }`, `parseIfList2(buf []byte) map[string]ifBytes`, `interfaceBytes() map[string]ifBytes`;
    - `smcParam`;
    - test helper `rusageCPU() time.Duration`.
  - `monitor.go`:
    - `StatusSample.Machine *MachineSample` (`machine`);
    - `BatteryStatus.Temperature float64` (`temperature`) and `BatteryStatus.AdapterWatts int` (`adapterWatts`);
    - `statusSource.machine machineSampler`;
    - `statusSource.procs == nil` means no app list.
  - TypeScript after `bun run generate`: `StatusSample.machine: MachineSample | null`, `BatteryStatus.temperature: number`, `BatteryStatus.adapterWatts: number`, and `MachineSample` with the JSON names above.

- [ ] **Step 1: Write the failing pure-rule tests**

Replace the import block of `machine_test.go` with:

```go
import (
	"encoding/binary"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
)
```

Append to `machine_test.go`:

```go
func TestClusterUsage(t *testing.T) {
	// Ticks per core: user, system, idle, nice. Cores 0–1 are efficiency, 2–3 performance.
	kinds := []byte("EEPP")
	prev := [][4]uint32{{0, 0, 100, 0}, {0, 0, 100, 0}, {10, 10, 0, 0}, {0, 0, 0, 0}}
	cur := [][4]uint32{{10, 10, 120, 0}, {0, 0, 200, 0}, {60, 20, 20, 0}, {30, 0, 0, 10}}
	perf, eff := clusterUsage(prev, cur, kinds)
	// Efficiency: busy 20 of 140 ticks. Performance: busy 100 of 120.
	if math.Abs(eff-20.0/140*100) > 1e-9 || math.Abs(perf-100.0/120*100) > 1e-9 {
		t.Errorf("perf %.3f, eff %.3f; want %.3f, %.3f", perf, eff, 100.0/120*100, 20.0/140*100)
	}

	// The 32-bit counters wrap.
	perf, _ = clusterUsage([][4]uint32{{math.MaxUint32 - 9, 0, 0, 0}}, [][4]uint32{{10, 0, 10, 0}}, []byte("P"))
	if math.Abs(perf-20.0/30*100) > 1e-9 {
		t.Errorf("after a wrap perf = %.3f, want %.3f", perf, 20.0/30*100)
	}

	for name, tc := range map[string]struct {
		prev, cur [][4]uint32
		kinds     []byte
	}{
		"no cluster map":    {prev, cur, nil},
		"core count change": {prev[:3], cur, kinds},
		"no time passed":    {prev, prev, kinds},
	} {
		if p, e := clusterUsage(tc.prev, tc.cur, tc.kinds); p != -1 || e != -1 {
			t.Errorf("%s: got %v, %v; want -1, -1", name, p, e)
		}
	}
	if _, e := clusterUsage(prev[2:], cur[2:], []byte("PP")); e != -1 {
		t.Errorf("a Mac without efficiency cores reports -1 for them, got %v", e)
	}
}

func TestByteRate(t *testing.T) {
	if got := byteRate(100, 300, 2*time.Second); got != 100 {
		t.Errorf("byteRate = %v, want 100", got)
	}
	if byteRate(300, 100, time.Second) != -1 {
		t.Error("a counter that went backwards (a disk went away) is unknown")
	}
	if byteRate(1, 2, 0) != -1 {
		t.Error("no elapsed time is unknown")
	}
}

func TestSensorRotation(t *testing.T) {
	r := newSensorRotation(5)
	if got := r.pick(2); !slices.Equal(got, []int{0, 1}) {
		t.Errorf("before any reading: %v, want [0 1]", got)
	}
	r.record(0, 40)
	r.record(1, 55)
	if got := r.pick(2); !slices.Equal(got, []int{2, 3, 1}) {
		t.Errorf("the next two plus the hottest: %v, want [2 3 1]", got)
	}
	r.record(2, 50)
	r.record(3, 0) // unreadable: out of the maximum
	r.record(1, 45)
	if r.highest() != 50 {
		t.Errorf("highest = %v, want 50 once sensor 1 cooled", r.highest())
	}
	if got := r.pick(2); !slices.Equal(got, []int{4, 0, 2}) {
		t.Errorf("wraps around, plus the new hottest: %v, want [4 0 2]", got)
	}
	if got := r.pick(9); len(got) != 5 {
		t.Errorf("k above n reads each sensor once: %v", got)
	}
	empty := newSensorRotation(0)
	if empty.pick(2) != nil || empty.highest() != 0 || empty.hottest() != -1 {
		t.Error("no sensors, nothing to read")
	}
}

func TestNewMachineSampleIsAllUnknown(t *testing.T) {
	s := newMachineSample()
	if s.Thermal != -1 || s.FanCount != -1 || s.PerfCPU != -1 || s.EffCPU != -1 || s.GPU != -1 ||
		s.DiskRead != -1 || s.DiskWrite != -1 || s.NetDown != -1 || s.NetUp != -1 ||
		s.ChipTemp != 0 || s.Power != 0 || s.DiskTemp != 0 || s.GPUMemory != 0 {
		t.Errorf("not all unknown: %+v", *s)
	}
	b, err := json.Marshal(s)
	if err != nil || !strings.Contains(string(b), `"fans":[]`) {
		t.Errorf("fans must be [] in JSON, not null: %s", b)
	}
}
```

- [ ] **Step 2: Write the failing loop test**

Replace the import block of `monitor_test.go` with:

```go
import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)
```

Append after `TestWatchStatusCancelStops` in `monitor_test.go`:

```go
// fakeMachine counts reads and remembers being closed.
type fakeMachine struct {
	reads  int
	closed bool
}

func (f *fakeMachine) read(time.Time) *MachineSample {
	f.reads++
	s := newMachineSample()
	s.ChipTemp = float64(f.reads)
	return s
}

func (f *fakeMachine) close() { f.closed = true }

func TestWatchStatusWithoutAppList(t *testing.T) {
	oldDelay, oldBudget, oldInterval := statusFirstDelay, statusFirstBudget, statusInterval
	statusFirstDelay, statusFirstBudget, statusInterval = 10*time.Millisecond, time.Second, time.Hour
	defer func() { statusFirstDelay, statusFirstBudget, statusInterval = oldDelay, oldBudget, oldInterval }()

	src := fakeSource()
	src.procs, src.apps = nil, nil // macOS: the home page lists no apps
	machine := &fakeMachine{}
	src.machine = machine
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan StatusSample, 1)
	done := make(chan error, 1)
	go func() {
		done <- watchStatusWith(ctx, func(s StatusSample) error {
			select {
			case got <- s:
			default:
			}
			return nil
		}, src)
	}()
	var s StatusSample
	select {
	case s = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("no sample arrived")
	}
	cancel()
	<-done
	if s.Apps == nil || len(s.Apps) != 0 || s.Procs != 0 {
		t.Errorf("no app list: apps %v, procs %d", s.Apps, s.Procs)
	}
	if b, err := json.Marshal(s); err != nil || !strings.Contains(string(b), `"apps":[]`) {
		t.Errorf("apps must be [] in JSON, not null: %s", b)
	}
	// The first read starts the counters before the half-second wait; the
	// first sample carries the second read.
	if s.Machine == nil || s.Machine.ChipTemp != 2 {
		t.Errorf("machine reading = %+v, want the second read", s.Machine)
	}
	if !machine.closed {
		t.Error("the machine reader is closed when the watch stops")
	}
	if s.Memory.Total != 32<<30 || s.CPU.Usage != 25 {
		t.Errorf("the other readings still arrive: %+v %+v", s.Memory, s.CPU)
	}
}
```

- [ ] **Step 3: Write the failing darwin tests**

Replace the import block of `machine_darwin_test.go` with:

```go
import (
	"context"
	"encoding/binary"
	"encoding/json"
	"maps"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)
```

Append to `machine_darwin_test.go`:

```go
func TestSMCParamIs80Bytes(t *testing.T) {
	if n := unsafe.Sizeof(smcParam{}); n != 80 {
		t.Fatalf("smcParam is %d bytes; AppleSMC takes 80", n)
	}
}

// ifInfo2 builds one RTM_IFINFO2 message the way the kernel lays it out:
// if_msghdr2, then the link-level address with the interface's name.
func ifInfo2(name string, in, out uint64) []byte {
	size := unix.SizeofIfMsghdr2 + 8 + len(name)
	size += (4 - size%4) % 4
	msg := make([]byte, size)
	binary.LittleEndian.PutUint16(msg[0:], uint16(size))
	msg[3] = unix.RTM_IFINFO2
	binary.LittleEndian.PutUint32(msg[ifm2Addrs:], unix.RTA_IFP)
	binary.LittleEndian.PutUint64(msg[ifm2In:], in)
	binary.LittleEndian.PutUint64(msg[ifm2Out:], out)
	sdl := msg[unix.SizeofIfMsghdr2:]
	sdl[0] = byte(8 + len(name))
	sdl[1] = unix.AF_LINK
	sdl[5] = byte(len(name))
	copy(sdl[8:], name)
	return msg
}

func TestParseIfList2(t *testing.T) {
	addr := make([]byte, 20) // an address message: skipped
	binary.LittleEndian.PutUint16(addr, 20)
	addr[3] = unix.RTM_NEWADDR
	noName := ifInfo2("en9", 1, 1)
	binary.LittleEndian.PutUint32(noName[ifm2Addrs:], 0) // no link-level address: skipped
	var buf []byte
	buf = append(buf, ifInfo2("en0", 5<<32, 7)...) // a counter past 4 GB
	buf = append(buf, addr...)
	buf = append(buf, noName...)
	buf = append(buf, ifInfo2("lo0", 1, 2)...)
	buf = append(buf, ifInfo2("en1", 9, 9)[:40]...) // cut short: parsing stops
	got := parseIfList2(buf)
	want := map[string]ifBytes{"en0": {in: 5 << 32, out: 7}, "lo0": {in: 1, out: 2}}
	if !maps.Equal(got, want) {
		t.Errorf("parseIfList2 = %v, want %v", got, want)
	}
}

func TestInterfaceBytesLive(t *testing.T) {
	if !iokitInit() {
		t.Skip("IOKit is not available")
	}
	counts := interfaceBytes()
	t.Logf("%d interfaces; en0 %+v", len(counts), counts["en0"])
	if len(counts) == 0 {
		t.Skip("the interface list is not readable here")
	}
	if _, ok := counts["lo0"]; !ok {
		t.Error("every Mac has lo0")
	}
	if onThisMac() && counts["en0"].in == 0 {
		t.Error("en0 has received bytes on a real Mac")
	}
}

func TestMachineReaderLive(t *testing.T) {
	sampler := newMachineReader()
	if sampler == nil {
		t.Skip("IOKit is not available")
	}
	defer sampler.close()
	first := sampler.read(time.Now())
	if first.PerfCPU != -1 || first.DiskRead != -1 || first.NetDown != -1 {
		t.Errorf("the first read only starts the counters: %+v", *first)
	}
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	s := sampler.read(time.Now())
	t.Logf("second read took %v: %+v", time.Since(start), *s)

	for name, v := range map[string]float64{"PerfCPU": s.PerfCPU, "EffCPU": s.EffCPU, "GPU": s.GPU} {
		if v != -1 && (v < 0 || v > 100) {
			t.Errorf("%s = %v, want -1 or 0–100", name, v)
		}
	}
	for name, v := range map[string]float64{"DiskRead": s.DiskRead, "DiskWrite": s.DiskWrite, "NetDown": s.NetDown, "NetUp": s.NetUp} {
		if v != -1 && v < 0 {
			t.Errorf("%s = %v, want -1 or ≥ 0", name, v)
		}
	}
	if s.Thermal < -1 || s.Thermal > 3 {
		t.Errorf("Thermal = %d", s.Thermal)
	}
	if s.Fans == nil || s.FanCount < -1 || (s.FanCount >= 0 && len(s.Fans) > s.FanCount) {
		t.Errorf("FanCount %d, Fans %v", s.FanCount, s.Fans)
	}
	if s.ChipTemp < 0 || s.DiskTemp < 0 || s.Power < 0 {
		t.Errorf("temperatures and power are ≥ 0: %+v", *s)
	}
	if s.NetKind != "" && s.NetKind != "wifi" && s.NetKind != "ethernet" {
		t.Errorf("NetKind = %q", s.NetKind)
	}
	if !onThisMac() {
		return
	}
	if s.Thermal < 0 || s.ChipTemp < 15 || s.ChipTemp > 110 || s.FanCount < 0 || s.Power <= 0 ||
		s.PerfCPU < 0 || s.EffCPU < 0 || s.GPU < 0 || s.GPUMemory == 0 || s.DiskRead < 0 || s.NetDown < 0 ||
		s.NetKind == "" || s.DiskTemp <= 0 || s.DisplaySig == "" {
		t.Errorf("on a real Mac every live reading is known: %+v", *s)
	}
}

func TestSmartBatteryExtrasLive(t *testing.T) {
	temp, watts := smartBatteryExtras(true)
	t.Logf("battery %.1f °C, adapter %d W", temp, watts)
	if temp < 0 || temp > 80 || watts < 0 {
		t.Errorf("implausible battery readings: %.1f °C, %d W", temp, watts)
	}
	if _, w := smartBatteryExtras(false); w != 0 {
		t.Error("no adapter watts when not plugged in")
	}
	// The maintainer's Mac is a MacBook Pro.
	if onThisMac() && temp == 0 {
		t.Error("a MacBook's battery reports its temperature")
	}
}

// rusageCPU is the CPU this process and its reaped children have used.
func rusageCPU() time.Duration {
	var self, kids syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &self)
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &kids)
	tv := func(v syscall.Timeval) time.Duration { return time.Duration(v.Nano()) }
	return tv(self.Utime) + tv(self.Stime) + tv(kids.Utime) + tv(kids.Stime)
}

// TestStatusSamplingBudget measures spec §五 on this Mac: the status loop,
// with its machine readings and the battery's system_profiler, may use 1%
// of one core. Opt in with CLEANZO_PERF=1; it runs for 60 seconds. Don't
// use -race, which inflates the CPU.
func TestStatusSamplingBudget(t *testing.T) {
	if os.Getenv("CLEANZO_PERF") == "" {
		t.Skip("set CLEANZO_PERF=1 to measure the sampling cost on this Mac")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	samples := 0
	var last *MachineSample
	before, start := rusageCPU(), time.Now()
	_ = watchStatusWith(ctx, func(s StatusSample) error {
		samples++
		last = s.Machine
		_, err := json.Marshal(s)
		return err
	}, realStatusSource())
	used, wall := rusageCPU()-before, time.Since(start)
	pct := used.Seconds() / wall.Seconds() * 100
	t.Logf("%d samples, CPU %v in %v: %.2f%% of one core; last machine reading %+v",
		samples, used.Round(time.Millisecond), wall.Round(time.Second), pct, last)
	if pct > 1 {
		t.Errorf("sampling used %.2f%% of one core, budget 1%%", pct)
	}
}
```

In `process_darwin_test.go` (`TestProcessSamplingBudget`), replace

```go
	usage := func() time.Duration {
		var self, kids syscall.Rusage
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &self)
		_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &kids)
		tv := func(v syscall.Timeval) time.Duration { return time.Duration(v.Nano()) }
		return tv(self.Utime) + tv(self.Stime) + tv(kids.Utime) + tv(kids.Stime)
	}
```

with

```go
	usage := rusageCPU
```

The file keeps its `syscall` import; another test in it uses `syscall`.

- [ ] **Step 4: Run the tests to see them fail**

Run: `go test -count=1 -run 'TestClusterUsage|TestByteRate|TestSensorRotation|TestNewMachineSample|TestWatchStatusWithoutAppList|TestSMCParam|TestParseIfList2|TestInterfaceBytesLive|TestMachineReaderLive|TestSmartBatteryExtrasLive' .`
Expected: the build fails with `undefined: clusterUsage`, `machineSampler`, `smcParam` and so on.

- [ ] **Step 5: Add the live types and rules to `machine.go`**

Replace the import block of `machine.go` with:

```go
import (
	"encoding/binary"
	"slices"
	"strconv"
	"strings"
	"time"
)
```

Change its header comment's second sentence to: `MachineInfo says what this Mac is; the page reads it when it opens and again when the displays change. MachineSample is the live side, taken with each status sample.`

Append to `machine.go`:

```go
// MachineSample is the home page's live machine reading, taken with each
// status sample on macOS. Unknown values: temperatures, power and
// GPUMemory 0; percentages and rates -1; Thermal and FanCount -1
// (FanCount 0 means the Mac has no fans).
type MachineSample struct {
	Thermal    int     `json:"thermal"`    // NSProcessInfo thermal state: 0 nominal, 1 fair, 2 serious, 3 critical
	ChipTemp   float64 `json:"chipTemp"`   // °C, the hottest SoC die sensor
	FanCount   int     `json:"fanCount"`   // fans the SMC reports
	Fans       []int   `json:"fans"`       // rpm per fan; never null
	Power      float64 `json:"power"`      // W, the whole system (SMC PSTR)
	PerfCPU    float64 `json:"perfCpu"`    // % busy, performance cores
	EffCPU     float64 `json:"effCpu"`     // % busy, efficiency cores
	GPU        float64 `json:"gpu"`        // % device utilization
	GPUMemory  uint64  `json:"gpuMemory"`  // bytes the GPU has in use
	DiskRead   float64 `json:"diskRead"`   // bytes/s, all disks
	DiskWrite  float64 `json:"diskWrite"`  // bytes/s, all disks
	DiskTemp   float64 `json:"diskTemp"`   // °C, the SSD's NAND sensor
	NetDown    float64 `json:"netDown"`    // bytes/s over Wi-Fi and Ethernet
	NetUp      float64 `json:"netUp"`      // bytes/s over Wi-Fi and Ethernet
	NetKind    string  `json:"netKind"`    // "wifi", "ethernet", or "" when not connected or unknown
	DisplaySig string  `json:"displaySig"` // as MachineInfo.DisplaySig
}

// newMachineSample is a reading with everything unknown.
func newMachineSample() *MachineSample {
	return &MachineSample{
		Thermal: -1, FanCount: -1, Fans: []int{},
		PerfCPU: -1, EffCPU: -1, GPU: -1,
		DiskRead: -1, DiskWrite: -1, NetDown: -1, NetUp: -1,
	}
}

// machineSampler takes the live readings for one status watch. The loop
// calls read once before its first sample to start the counters, then once
// per sample, and close when it stops.
type machineSampler interface {
	read(now time.Time) *MachineSample
	close()
}

// clusterUsage is each core cluster's busy share between two per-core tick
// readings (user, system, idle, nice), in percent; -1 for a cluster with
// no cores or no elapsed ticks. kinds[i] is 'P' or 'E' for logical CPU i.
// The counters are 32-bit and wrap, so deltas are taken in uint32.
func clusterUsage(prev, cur [][4]uint32, kinds []byte) (perf, eff float64) {
	var busy, total [2]uint64 // [0] performance, [1] efficiency
	if len(prev) == len(cur) {
		for i := range cur {
			if i >= len(kinds) {
				break
			}
			k := 0
			switch kinds[i] {
			case 'P':
			case 'E':
				k = 1
			default:
				continue
			}
			delta := func(j int) uint64 { return uint64(cur[i][j] - prev[i][j]) }
			b := delta(0) + delta(1) + delta(3)
			busy[k] += b
			total[k] += b + delta(2)
		}
	}
	share := func(k int) float64 {
		if total[k] == 0 {
			return -1
		}
		return clampPct(float64(busy[k]) / float64(total[k]) * 100)
	}
	return share(0), share(1)
}

// byteRate is bytes per second between two cumulative counters; -1 when no
// time passed or the counter went backwards (a disk went away).
func byteRate(prev, cur uint64, elapsed time.Duration) float64 {
	if elapsed <= 0 || cur < prev {
		return -1
	}
	return float64(cur-prev) / elapsed.Seconds()
}

// sensorRotation spreads die-sensor reads over samples: one read costs
// about a millisecond and an M2 Pro has 26 die sensors. Each sample reads
// a few sensors in turn plus the one that last read hottest, and the chip
// temperature is the highest of every sensor's latest reading.
type sensorRotation struct {
	latest []float64 // °C per sensor; 0 not read yet, or unreadable
	next   int
}

func newSensorRotation(n int) sensorRotation {
	return sensorRotation{latest: make([]float64, n)}
}

// pick returns the sensors to read now: the next k in turn, plus the
// hottest if it isn't among them.
func (r *sensorRotation) pick(k int) []int {
	n := len(r.latest)
	if n == 0 {
		return nil
	}
	if k > n {
		k = n
	}
	out := make([]int, 0, k+1)
	for j := 0; j < k; j++ {
		out = append(out, (r.next+j)%n)
	}
	r.next = (r.next + k) % n
	if hot := r.hottest(); hot >= 0 && !slices.Contains(out, hot) {
		out = append(out, hot)
	}
	return out
}

// record keeps sensor i's latest reading.
func (r *sensorRotation) record(i int, celsius float64) { r.latest[i] = celsius }

// hottest is the sensor with the highest latest reading, -1 before any.
func (r *sensorRotation) hottest() int {
	best := -1
	for i, v := range r.latest {
		if v > 0 && (best < 0 || v > r.latest[best]) {
			best = i
		}
	}
	return best
}

// highest is the chip temperature: the highest latest reading, 0 before any.
func (r *sensorRotation) highest() float64 {
	if i := r.hottest(); i >= 0 {
		return r.latest[i]
	}
	return 0
}
```

- [ ] **Step 6: Carry the machine readings through the loop in `monitor.go`**

1. In `BatteryStatus`, replace

```go
	MaxCapacity int    `json:"maxCapacity"`
	Condition   string `json:"condition"` // e.g. "Normal"; "" unknown
}
```

with

```go
	MaxCapacity int    `json:"maxCapacity"`
	Condition   string `json:"condition"` // e.g. "Normal"; "" unknown
	// macOS only, from AppleSmartBattery; 0 unknown.
	Temperature  float64 `json:"temperature"`  // °C
	AdapterWatts int     `json:"adapterWatts"` // the connected adapter's rating; also 0 when not plugged in
}
```

2. In `StatusSample`, replace

```go
	Apps    []AppUsage     `json:"apps"`
	Procs   int            `json:"procs"` // processes counted, this user only
}
```

with

```go
	Apps    []AppUsage     `json:"apps"`    // always empty on macOS, where the 进程 page lists apps
	Procs   int            `json:"procs"`   // processes counted, this user only
	Machine *MachineSample `json:"machine"` // the home page's machine readings; macOS only, nil elsewhere
}
```

3. In `statusSource`, replace `	procs     func() []procReading` with `	procs     func() []procReading // nil: no app list (macOS)`. After the `group` field, add:

```go
	machine   machineSampler // optional: the live machine readings (macOS)
```

4. In `WatchStatus`'s doc comment, replace

```go
// background and rides along with each sample. Cancel the context (the
// frontend closes the channel) to stop.
```

with

```go
// background and rides along with each sample. On macOS each sample also
// carries the machine readings and no app list. Cancel the context (the
// frontend closes the channel) to stop.
```

5. In `watchStatusWith`, replace

```go
	prevProcs := readProcState(src)
	prevCPU := readCPUTicks(src)
	prevAt := time.Now()
```

with

```go
	prevProcs := readProcState(src)
	prevCPU := readCPUTicks(src)
	if src.machine != nil {
		defer src.machine.close()
		src.machine.read(time.Now()) // starts its counters over the same wait
	}
	prevAt := time.Now()
```

   and replace

```go
		sample := buildSample(prevProcs, curProcs, prevCPU, curCPU, src, elapsed)
		sample.Battery = latest
```

   with

```go
		sample := buildSample(prevProcs, curProcs, prevCPU, curCPU, src, elapsed)
		sample.Battery = latest
		if src.machine != nil {
			sample.Machine = src.machine.read(time.Now())
		}
```

6. In `readProcState`, replace

```go
	out := map[int32]procSample{}
	for _, r := range src.procs() {
```

with

```go
	out := map[int32]procSample{}
	if src.procs == nil {
		return out // macOS: the home page lists no apps
	}
	for _, r := range src.procs() {
```

7. In `buildSample`, replace

```go
	sample := StatusSample{
		Memory: src.memory(),
		Disk:   src.disk(),
		Apps:   mergeApps(prevProcs, curReadings, src.apps(), nativeResponsiblePid, elapsed, src.group),
		Procs:  len(curProcs),
	}
```

with

```go
	sample := StatusSample{
		Memory: src.memory(),
		Disk:   src.disk(),
		Apps:   []AppUsage{},
		Procs:  len(curProcs),
	}
	if src.procs != nil {
		sample.Apps = mergeApps(prevProcs, curReadings, src.apps(), nativeResponsiblePid, elapsed, src.group)
	}
```

- [ ] **Step 7: Write the live reader in `machine_darwin.go`**

Replace its import block with:

```go
import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"golang.org/x/sys/unix"
)
```

Replace its header comment with:

```go
// machine_darwin.go reads what the home page shows about this Mac (design
// docs/superpowers/specs/2026-10-28-home-machine-design.md §四): the
// identity once per visit (readMachineInfo) and the live readings every
// two seconds (machineReader). None of it needs administrator rights or
// starts a process.
```

Append to `machine_darwin.go`:

```go
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
		r.ifKinds = interfaceKinds() // an adapter plugged in since the page opened
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
		out, ok := r.smcCall(&smcParam{Key: k, Data8: smcReadKeyInfo})
		if !ok {
			return 0, nil, false
		}
		info = smcKeyInfo{size: out.DataSize, typ: out.DataType}
		r.smcKeys[k] = info
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
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))), true
}

// fanSpeeds reads each fan's current speed (F<i>Ac); never nil.
func (r *machineReader) fanSpeeds() []int {
	speeds := []int{}
	for i := 0; i < r.fans; i++ {
		if v, ok := r.smcFloat(fmt.Sprintf("F%dAc", i)); ok && v >= 0 {
			speeds = append(speeds, int(math.Round(v)))
		}
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
	if t <= 0 || t > 150 {
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
```

- [ ] **Step 8: Wire it up in `status_darwin.go`**

Replace

```go
// realStatusSource wires the loop to the native reads in native.go.
func realStatusSource() statusSource {
	return statusSource{
		procs: nativeProcs,
		apps:  workspaceList,
		memory: func() MemoryStatus {
			return readMemoryStatus()
		},
		cpu:       cpuBusyTotal,
		battery:   statusBattery,
		disk:      diskStat,
		terminate: workspaceTerminate,
	}
}
```

with

```go
// realStatusSource wires the loop to the native reads in native.go and
// machine_darwin.go. It reads no processes and no NSWorkspace list: the
// home page only talks about the machine, and the 进程 page shows who uses
// what (design docs/superpowers/specs/2026-10-28-home-machine-design.md).
func realStatusSource() statusSource {
	return statusSource{
		memory: func() MemoryStatus {
			return readMemoryStatus()
		},
		cpu:       cpuBusyTotal,
		battery:   statusBattery,
		disk:      diskStat,
		terminate: workspaceTerminate,
		machine:   newMachineReader(),
	}
}
```

Replace

```go
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
```

with

```go
// statusBattery runs system_profiler, whose JSON is the only source that
// matches System Settings (it rounds Maximum Capacity itself). Slow, so it
// lives behind a context deadline and refreshes only once a minute. The
// battery's temperature and the adapter's watts, which it leaves out, come
// from AppleSmartBattery.
func statusBattery(ctx context.Context) *BatteryStatus {
	cmd := exec.CommandContext(ctx, "/usr/sbin/system_profiler", "-json", "SPPowerDataType")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	b := parseBatteryJSON(out)
	if b != nil {
		b.Temperature, b.AdapterWatts = smartBatteryExtras(b.Plugged)
	}
	return b
}
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `go test -count=1 -race -run 'TestClusterUsage|TestByteRate|TestSensorRotation|TestNewMachineSample|TestWatchStatus|TestSMCParam|TestParseIfList2|TestInterfaceBytesLive|TestMachineReaderLive|TestSmartBatteryExtrasLive|TestReadMachineInfoLive' -v .`
Expected: PASS, with `TestStatusSamplingBudget` absent from the run. The live tests log their readings; in a sandbox most of them are -1 or 0.

- [ ] **Step 10: Generate the bindings and keep the dev mock compiling**

Run: `bun run generate`. `src/mygo.ts` now has `MachineSample`, `StatusSample.machine` and the two `BatteryStatus` fields.

In `src/dev/mock-mygo.ts`, inside `sample()`, replace

```ts
      disk: { ...disk }, battery: { percent: 100, charging: false, full: true, plugged: true, cycles: 170, maxCapacity: 83, condition: "Normal" },
```

with

```ts
      disk: { ...disk }, battery: { percent: 100, charging: false, full: true, plugged: true, cycles: 170, maxCapacity: 83, condition: "Normal", temperature: 0, adapterWatts: 0 },
```

and after the line `      procs: 740 + Math.round(drift(3) * 8),` add:

```ts
      machine: null,
```

Run: `bun run typecheck`
Expected: PASS.

- [ ] **Step 11: Check the whole package**

Run: `gofmt -l . && go vet ./... && GOOS=windows GOARCH=amd64 go vet ./... && go test -count=1 -race ./... && bun run test:web`
Expected: `gofmt` prints nothing and everything passes. In a sandbox, add the skip list from "Execution batches" to `go test`.

- [ ] **Step 12: Leave the changes uncommitted.** Outside any sandbox, the reviewer runs:
  - `CLEANZO_HW=1 go test -count=1 -run 'TestInterfaceBytesLive|TestMachineReaderLive|TestSmartBatteryExtrasLive|TestReadMachineInfoLive' -v .`: every reading is known.
  - `CLEANZO_PERF=1 go test -count=1 -run TestStatusSamplingBudget -v .`: ≤ 1% of one core.

---

### Task 4: Web — the home page about the machine, and the docs

**Files:**
- Modify: `src/react/status/api.ts`, `src/react/status/format.ts`, `src/react/status/StatusPage.tsx`
- Modify: `src/i18n.ts`, `src/style.css`, `src/dev/mock-mygo.ts`
- Modify: `tests/status-react.test.ts`
- Modify: `README.md`, `CHANGELOG.md`

**Interfaces:**
- Consumes (generated by Tasks 2 and 3):
  - `Cleaner.machineInfo(): Promise<MachineInfo>`;
  - `MachineInfo { model, modelId, perfCores, effCores, gpuCores, displays: DisplayInfo[], displaySig, ssd: SSDInfo | null }`;
  - `DisplayInfo { id, name, builtin, width, height, hz }`;
  - `SSDInfo { model, capacity, smart, warning, tempC, spare, used, written }`;
  - `StatusSample.machine: MachineSample | null`, with `MachineSample { thermal, chipTemp, fanCount, fans, power, perfCpu, effCpu, gpu, gpuMemory, diskRead, diskWrite, diskTemp, netDown, netUp, netKind, displaySig }`;
  - `BatteryStatus.temperature`, `BatteryStatus.adapterWatts`.
- Produces:
  - `statusApi.machine()`;
  - in `format.ts`: `modelLabel`, `fmtRate`, `fmtRateText`, `fmtTemp`, `fmtDec`, `ssdWarnings`, `SsdWarningKey`, `displayText`, `netSparkMax`, `NET_SPARK_FLOOR`;
  - exported components `HealthStrip`, `GpuCard`, `NetCard`, `BatteryCard` (now exported), and `CpuCard` and `DiskCard` with new optional props.

- [ ] **Step 1: Write the failing tests**

In `tests/status-react.test.ts`, replace the import block at the top with:

```ts
import { expect, test } from "bun:test";
import { t } from "../src/i18n";
import type { AppUsage, MachineSample, SSDInfo, StatusSample } from "../src/mygo";
import {
  NET_SPARK_FLOOR, SPARK_POINTS, STATUS_ALERT, displayText, emptyQuit, fmtDec, fmtMem, fmtPct, fmtRate, fmtRateText,
  fmtTemp, iconColor, modelLabel, netSparkMax, pickAlerts, quitErrCode, refreshFrozenRows, sortApps, splitMem,
  ssdWarnings, syncQuitRows, trimSpark, uptimeParts,
} from "../src/react/status/format";
```

In the `sample` fixture, replace

```ts
  battery: { percent: 100, charging: false, full: true, plugged: true, cycles: 170, maxCapacity: 83, condition: "Normal" },
  apps: [chrome(), code(), finder()],
  procs: 740,
  ...over,
});
```

with

```ts
  battery: { percent: 100, charging: false, full: true, plugged: true, cycles: 170, maxCapacity: 83, condition: "Normal", temperature: 0, adapterWatts: 0 },
  apps: [chrome(), code(), finder()],
  procs: 740,
  machine: null,
  ...over,
});

// 维护者这台 Mac 2026-10-28 的真实读数（spike）。
const machineSample = (over: Partial<MachineSample> = {}): MachineSample => ({
  thermal: 0, chipTemp: 51.9, fanCount: 2, fans: [1333, 1464], power: 37.8,
  perfCpu: 31, effCpu: 58, gpu: 8, gpuMemory: 1.1 * GB,
  diskRead: 10_300_000, diskWrite: 9_600_000, diskTemp: 36,
  netDown: 120_000, netUp: 110_000, netKind: "wifi", displaySig: "1,2",
  ...over,
});
const ssd = (over: Partial<SSDInfo> = {}): SSDInfo => ({
  model: "APPLE SSD AP0512Z", capacity: 500_277_792_768, smart: true, warning: 0,
  tempC: 33, spare: 100, used: 26, written: 624.4e12,
  ...over,
});
```

Replace the whole test `"the top-apps card links to the process page on macOS only"` with:

```ts
test("the top-apps list is Windows-only, without View All; macOS lists no apps", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { StatusPage } = await import("../src/react/status/StatusPage");
  const { setPlatformForTest } = await import("../src/platform");
  const props = { active: false, goClean() {}, goProcess() {} };
  try {
    setPlatformForTest("darwin");
    const mac = renderToStaticMarkup(createElement(StatusPage, props));
    expect(mac).not.toContain("st-listcard");
    expect(mac).not.toContain(t("status.apps.title"));
    setPlatformForTest("win32");
    const win = renderToStaticMarkup(createElement(StatusPage, props));
    expect(win).toContain("st-listcard");
    expect(win).toContain(t("status.apps.title"));
    expect(win).not.toContain("st-viewall");
  } finally { setPlatformForTest(undefined); }
});
```

Append to the end of the file:

```ts
test("modelLabel splits the device-tree name and localizes the details", () => {
  expect(modelLabel("MacBook Pro (16-inch, 2023)", "Mac14,10", "zh")).toEqual({ name: "MacBook Pro", note: "16 英寸 · 2023" });
  expect(modelLabel("MacBook Pro (16-inch, 2023)", "Mac14,10", "en")).toEqual({ name: "MacBook Pro", note: "16-inch, 2023" });
  expect(modelLabel("MacBook Air (13-inch, M2, 2022)", "Mac14,2", "zh")).toEqual({ name: "MacBook Air", note: "13 英寸 · M2 · 2022" });
  expect(modelLabel("MacBook Pro (14-inch, M3, Nov 2023)", "Mac15,3", "zh").note).toBe("14 英寸 · M3 · 2023 年 11 月");
  expect(modelLabel("Mac Studio (2023)", "Mac14,13", "en")).toEqual({ name: "Mac Studio", note: "2023" });
  expect(modelLabel("Mac Pro", "Mac14,8", "zh")).toEqual({ name: "Mac Pro", note: "" });
  // 设备树没有型号全名：由型号标识推出系列名，标识写在旁边（设计 §三.1）
  expect(modelLabel("", "Mac14,10", "zh")).toEqual({ name: "Mac", note: "Mac14,10" });
  expect(modelLabel("", "MacBookAir10,1", "en")).toEqual({ name: "MacBook Air", note: "MacBookAir10,1" });
  expect(modelLabel(" ", "", "zh")).toEqual({ name: "", note: "" });
});

test("machine numbers format without NaN: rates, temperatures, drive sizes", () => {
  expect(fmtRate(-1)).toEqual(["—", ""]);
  expect(fmtRate(Number.NaN)).toEqual(["—", ""]);
  expect(fmtRate(0)).toEqual(["0", "KB/s"]);
  expect(fmtRate(1_500)).toEqual(["2", "KB/s"]);
  expect(fmtRate(1_230_000)).toEqual(["1.2", "MB/s"]);
  expect(fmtRate(2_500_000_000)).toEqual(["2.5", "GB/s"]);
  expect(fmtRateText(-1)).toBe("—");
  expect(fmtRateText(10_300_000)).toBe("10.3 MB/s");
  expect(fmtTemp(0)).toBe("—");
  expect(fmtTemp(51.9)).toBe("52 °C");
  expect(fmtDec(0)).toBe("—");
  expect(fmtDec(500_277_792_768)).toBe("500 GB");
  expect(fmtDec(64.5e9)).toBe("64.5 GB");
  expect(fmtDec(624.4e12)).toBe("624 TB");
  expect(fmtDec(1.5e12)).toBe("1.5 TB");
  expect(fmtDec(2e12)).toBe("2 TB");
});

test("ssdWarnings names each NVMe critical-warning bit", () => {
  expect(ssdWarnings(0)).toEqual([]);
  expect(ssdWarnings(0x01)).toEqual(["status.ssd.warn.spare"]);
  expect(ssdWarnings(0x0a)).toEqual(["status.ssd.warn.temp", "status.ssd.warn.readonly"]);
  expect(ssdWarnings(0x20)).toEqual(["status.ssd.warn.other"]);
});

test("displayText writes the name, size and refresh rate", () => {
  const d = { id: 2, name: "LS27D80xU", builtin: false, width: 1920, height: 1080, hz: 60 };
  expect(displayText(d, "外接显示器")).toBe("LS27D80xU 1920×1080 @60Hz");
  expect(displayText({ ...d, name: " ", builtin: true, width: 1728, height: 1117, hz: 120 }, "内建显示器")).toBe("内建显示器 1728×1117 @120Hz");
  expect(displayText({ ...d, hz: 0 }, "外接显示器")).toBe("LS27D80xU 1920×1080");
  expect(displayText({ ...d, width: 0, height: 0 }, "外接显示器")).toBe("LS27D80xU");
});

test("the network chart keeps a floor so idle traffic doesn't fill it", () => {
  expect(netSparkMax([])).toBe(NET_SPARK_FLOOR);
  expect(netSparkMax([5_000, 20_000])).toBe(NET_SPARK_FLOOR);
  expect(netSparkMax([500_000, 2_000_000])).toBe(2_000_000);
});

test("the health strip shows what it read and leaves out the rest", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { HealthStrip } = await import("../src/react/status/StatusPage");
  const { setPref } = await import("../src/i18n");
  const html = (m: MachineSample) => renderToStaticMarkup(createElement(HealthStrip, { m, t }));
  try {
    setPref("zh");
    const full = html(machineSample());
    for (const s of ["散热状态", "正常", "芯片温度", "52 °C", "风扇", "1333 · 1464", "rpm", "整机功耗", "38 W"]) expect(full).toContain(s);
    expect(full).toContain('class="v num ok"');
    const hot = html(machineSample({ thermal: 2 }));
    expect(hot).toContain("很热");
    expect(hot).toContain('class="v num crit"');
    // MacBook Air 没有风扇：写「无风扇」，不写 0
    expect(html(machineSample({ fanCount: 0, fans: [] }))).toContain("无风扇");
    expect(html(machineSample({ fans: [0, 0] }))).toContain("未转动");
    // 读不到的整格省掉；什么都读不到时整条不出现
    const partial = html(machineSample({ chipTemp: 0, power: 0, fanCount: -1, fans: [] }));
    for (const s of ["芯片温度", "整机功耗", "风扇"]) expect(partial).not.toContain(s);
    expect(partial).not.toMatch(/NaN|undefined/);
    expect(html(machineSample({ thermal: -1, chipTemp: 0, power: 0, fanCount: -1, fans: [] }))).toBe("");
  } finally { setPref("system"); }
});

test("the macOS CPU card splits performance and efficiency cores", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { CpuCard } = await import("../src/react/status/StatusPage");
  const { setPref } = await import("../src/i18n");
  try {
    setPref("zh");
    const base = { cpu: sample().cpu, cores: 12, hist: [10], t };
    const split = renderToStaticMarkup(createElement(CpuCard, { ...base, m: machineSample(), split: { perf: 8, eff: 4 } }));
    for (const s of ["性能核（8）", "31%", "能效核（4）", "58%"]) expect(split).toContain(s);
    // 核数还没读到：只写「性能核」「能效核」
    const counts = renderToStaticMarkup(createElement(CpuCard, { ...base, m: machineSample() }));
    expect(counts).toContain("性能核");
    expect(counts).not.toContain("性能核（");
    const unknown = renderToStaticMarkup(createElement(CpuCard, { ...base, m: machineSample({ perfCpu: -1, effCpu: -1 }) }));
    expect(unknown).not.toContain("性能核");
  } finally { setPref("system"); }
});

test("the GPU and network cards show live numbers, and dashes until they are known", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { GpuCard, NetCard } = await import("../src/react/status/StatusPage");
  const { setPref } = await import("../src/i18n");
  try {
    setPref("zh");
    const gpu = renderToStaticMarkup(createElement(GpuCard, { m: machineSample(), cores: 19, displays: 2, hist: [8, 9], t }));
    for (const s of ["GPU", "19 核", "占用的内存", "1.1 GB", "显示器", "2 台"]) expect(gpu).toContain(s);
    const gpuUnknown = renderToStaticMarkup(createElement(GpuCard, { m: machineSample({ gpu: -1, gpuMemory: 0 }), cores: 0, displays: 0, hist: [], t }));
    expect(gpuUnknown).toContain('<b class="ph">—</b>');
    expect(gpuUnknown).not.toMatch(/NaN|undefined|>-1/);

    const net = renderToStaticMarkup(createElement(NetCard, { m: machineSample(), hist: [120_000, 90_000], t }));
    for (const s of ["网络", '<span class="mhead-note">Wi-Fi</span>', "120", "110", "KB/s", "只算 Wi-Fi 和以太网"]) expect(net).toContain(s);
    const wired = renderToStaticMarkup(createElement(NetCard, { m: machineSample({ netKind: "ethernet" }), hist: [], t }));
    expect(wired).toContain('<span class="mhead-note">以太网</span>');
    // 第一份采样还没有速度：写「—」，不写 -1，也不写连接方式
    const first = renderToStaticMarkup(createElement(NetCard, { m: machineSample({ netDown: -1, netUp: -1, netKind: "" }), hist: [], t }));
    expect(first).toContain("—");
    expect(first).not.toContain("mhead-note");
    expect(first).not.toMatch(/NaN|undefined|>-1/);
  } finally { setPref("system"); }
});

test("the macOS disk card adds the SSD and live read/write; without them it stays as before", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { DiskCard } = await import("../src/react/status/StatusPage");
  const { setPref } = await import("../src/i18n");
  const d = { total: 460 * GB, used: 424.7 * GB, free: 35.3 * GB };
  const html = (over: { ssd?: SSDInfo | null; live?: MachineSample | null }) =>
    renderToStaticMarkup(createElement(DiskCard, { d, t, onClean() {}, ...over }));
  try {
    setPref("zh");
    const mac = html({ ssd: ssd(), live: machineSample() });
    for (const s of ["APPLE SSD AP0512Z", "读取", "10.3 MB/s", "写入", "9.6 MB/s", "健康", "正常", "寿命已用", "26%", "已写入", "624 TB", "温度", "36 °C"]) {
      expect(mac).toContain(s);
    }
    expect(mac).not.toContain("需要留意");
    const worn = html({ ssd: ssd({ warning: 0x01 }), live: machineSample() });
    expect(worn).toContain("需要留意");
    expect(worn).toContain("备用块不足");
    // 没有 NAND 传感器时用 SMART 日志里的温度
    expect(html({ ssd: ssd(), live: machineSample({ diskTemp: 0 }) })).toContain("33 °C");
    // SMART 读不到、速度还没有：这些行都不出现
    const bare = html({ ssd: ssd({ smart: false }), live: machineSample({ diskRead: -1, diskWrite: -1, diskTemp: 0 }) });
    for (const s of ["健康", "寿命已用", "已写入", "读取", "写入", "温度"]) expect(bare).not.toContain(s);
    expect(bare).toContain("APPLE SSD AP0512Z");
    const plain = html({});
    for (const s of ["读取", "健康", "APPLE"]) expect(plain).not.toContain(s);
  } finally { setPref("system"); }
});

test("the battery card adds its temperature and the adapter's watts when known", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { BatteryCard } = await import("../src/react/status/StatusPage");
  const { setPref } = await import("../src/i18n");
  const b = { ...sample().battery!, temperature: 30.64, adapterWatts: 90 };
  try {
    setPref("zh");
    const html = renderToStaticMarkup(createElement(BatteryCard, { b, t }));
    for (const s of ["温度", "31 °C", "90 W"]) expect(html).toContain(s);
    expect(renderToStaticMarkup(createElement(BatteryCard, { b: { ...b, plugged: false, full: false }, t }))).not.toContain("90 W");
    const unknown = renderToStaticMarkup(createElement(BatteryCard, { b: { ...b, temperature: 0, adapterWatts: 0 }, t }));
    expect(unknown).not.toContain("°C");
    expect(unknown).not.toContain("90 W");
  } finally { setPref("system"); }
});

test("English machine strings", async () => {
  const { setPref } = await import("../src/i18n");
  try {
    setPref("en");
    expect(t("status.spec.cpuSplit", { n: 12, p: 8, e: 4 })).toBe("12-core CPU (8P + 4E)");
    expect(t("status.health.noFans")).toBe("No fans");
    expect(t("status.alert.viewInProcess")).toBe("See what's using memory");
    expect(t("status.ssd.warn.spare")).toBe("Low spare blocks");
  } finally { setPref("system"); }
});
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `bun test tests/status-react.test.ts`
Expected: FAIL. The new helpers, `HealthStrip`, `GpuCard`, `NetCard` and the exported `BatteryCard` don't exist yet, and macOS still renders `st-listcard`.

- [ ] **Step 3: `api.ts`**

Replace the whole of `src/react/status/api.ts` with:

```ts
import { Cleaner } from "../../mygo";
import type { MachineInfo, StatusSample, SystemInfo } from "../../mygo";

/** 系统状态页用到的全部 Cleaner 调用集中在此，组件不直接碰桥。 */
export const statusApi = {
  /** 机器信息（首次进入拉一次；失败留空，重进视图重试）。 */
  info: (): Promise<SystemInfo> => Cleaner.info(),
  /** 这台 Mac 是什么：型号、核数、GPU、显示器、SSD（macOS；进入首页读，显示器变化时再读）。 */
  machine: (): Promise<MachineInfo> => Cleaner.machineInfo(),
  /** 每 2 秒一条采样；Channel 关闭即 Go 侧取消 ctx。 */
  watch: (ch: import("mygo-runtime").Channel<StatusSample>): Promise<void> => Cleaner.watchStatus(ch),
  /** 退出应用；返回 false = 5s 超时仍在跑（多半在等未保存内容的确认）。 */
  quitApp: (path: string): Promise<boolean> => Cleaner.quitApp(path),
};
```

- [ ] **Step 4: `format.ts` helpers**

Replace the import line `import type { AppUsage, StatusSample } from "../../mygo";` with:

```ts
import type { AppUsage, DisplayInfo, StatusSample } from "../../mygo";
import type { Lang } from "../../i18n";
```

Append to `src/react/status/format.ts`:

```ts
// ---- 机器信息（设计 2026-10-28 §三） ----

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** 括号里的一段，中文界面改写尺寸和月份："16-inch" → "16 英寸"，"Nov 2023" → "2023 年 11 月"。 */
function modelPart(part: string, lang: Lang): string {
  if (lang !== "zh") return part;
  const inch = /^(\d+(?:\.\d+)?)-inch$/.exec(part);
  if (inch) return `${inch[1]} 英寸`;
  const dated = /^([A-Z][a-z]{2})[a-z]* (\d{4})$/.exec(part);
  const month = dated ? MONTHS.indexOf(dated[1]!) : -1;
  if (dated && month >= 0) return `${dated[2]} 年 ${month + 1} 月`;
  return part;
}

/** 型号标识推出的系列名，设备树读不到型号全名时用；Apple 芯片的新标识（Mac14,10）看不出系列，只能写 "Mac"。 */
const FAMILIES: ReadonlyArray<readonly [RegExp, string]> = [
  [/^MacBookPro/, "MacBook Pro"], [/^MacBookAir/, "MacBook Air"], [/^MacBook\d/, "MacBook"],
  [/^Macmini/, "Mac mini"], [/^MacPro/, "Mac Pro"], [/^iMac/, "iMac"],
];

/**
 * 页头标题（设计 §三.1）：设备树的型号全名拆成名字和括号里的说明。
 * "MacBook Pro (16-inch, 2023)" → 中文 { name: "MacBook Pro", note: "16 英寸 · 2023" }，英文 note 是 "16-inch, 2023"。
 * 没有型号全名时用型号标识：{ name: 系列名, note: "Mac14,10" }；都没有时两项都空，页头退回「系统状态」。
 */
export function modelLabel(model: string, modelId: string, lang: Lang): { name: string; note: string } {
  const full = model.trim();
  const m = /^(.+?)\s*\((.+)\)$/.exec(full);
  if (m) {
    const parts = m[2]!.split(",").map((p) => p.trim()).filter(Boolean).map((p) => modelPart(p, lang));
    return { name: m[1]!, note: parts.join(lang === "zh" ? " · " : ", ") };
  }
  if (full) return { name: full, note: "" };
  const id = modelId.trim();
  if (!id) return { name: "", note: "" };
  const family = FAMILIES.find(([re]) => re.test(id));
  return { name: family ? family[1] : "Mac", note: id };
}

/** 每秒字节 → [数, 单位]，十进制（和活动监视器一致）：<1 MB/s 用 KB/s 取整，以上一位小数；负数或 NaN = 读不到 → ["—", ""]。 */
export function fmtRate(bps: number): [string, string] {
  if (!(bps >= 0)) return ["—", ""];
  if (bps >= 1e9) return [(bps / 1e9).toFixed(1), "GB/s"];
  if (bps >= 1e6) return [(bps / 1e6).toFixed(1), "MB/s"];
  return [String(Math.round(bps / 1e3)), "KB/s"];
}

/** fmtRate 拼成一段文字，如 "10.3 MB/s"；读不到 → "—"。 */
export function fmtRateText(bps: number): string {
  const [v, unit] = fmtRate(bps);
  return unit ? `${v} ${unit}` : v;
}

/** 摄氏度取整，如 "52 °C"；0 = 读不到 → "—"。 */
export function fmtTemp(c: number): string {
  return c > 0 ? `${Math.round(c)} °C` : "—";
}

/** 十进制容量（SSD 容量、累计写入用厂商口径）：100 以上取整，以下一位小数（去掉 .0）；0 = 读不到 → "—"。 */
export function fmtDec(n: number): string {
  if (!(n > 0)) return "—";
  const [v, unit] = n >= 1e12 ? [n / 1e12, "TB"] : [n / 1e9, "GB"];
  const s = v >= 100 ? String(Math.round(v)) : v.toFixed(1).replace(/\.0$/, "");
  return `${s} ${unit}`;
}

/** NVMe 严重警告的各位（SMART 日志第 0 字节）：0 备用块不足、1 温度超限、2 可靠性下降、3 只读、4 断电保护失效。 */
const SSD_WARNINGS = [
  "status.ssd.warn.spare", "status.ssd.warn.temp", "status.ssd.warn.reliability",
  "status.ssd.warn.readonly", "status.ssd.warn.backup",
] as const;
export type SsdWarningKey = (typeof SSD_WARNINGS)[number] | "status.ssd.warn.other";

/** 警告位 → 文案 key；只有规范之外的位时给一条笼统的「报告了警告」。 */
export function ssdWarnings(bits: number): SsdWarningKey[] {
  const out: SsdWarningKey[] = SSD_WARNINGS.filter((_, i) => (bits >> i) & 1);
  if (out.length === 0 && bits !== 0) out.push("status.ssd.warn.other");
  return out;
}

/** 一台显示器："LS27D80xU 1920×1080 @60Hz"；没有名字时用 fallback（「内建显示器」「外接显示器」）。 */
export function displayText(d: DisplayInfo, fallback: string): string {
  const name = d.name.trim() || fallback;
  if (!(d.width > 0 && d.height > 0)) return name;
  return `${name} ${d.width}×${d.height}${d.hz > 0 ? ` @${d.hz}Hz` : ""}`;
}

/** 网络曲线纵轴的下限：空闲时几 KB/s 的波动不会被放大成满格。 */
export const NET_SPARK_FLOOR = 100_000;

/** 网络曲线的纵轴上限：最近一分钟的最大值，至少 NET_SPARK_FLOOR。 */
export function netSparkMax(hist: readonly number[]): number {
  return Math.max(NET_SPARK_FLOOR, ...hist);
}
```

- [ ] **Step 5: `StatusPage.tsx`**

Make these replacements, in order.

**(a) Imports.** Replace

```tsx
import type { AppUsage, BatteryStatus, GrowthSummary, StatusSample, SystemInfo } from "../../mygo";
```

with

```tsx
import type { AppUsage, BatteryStatus, GrowthSummary, MachineInfo, MachineSample, SSDInfo, StatusSample, SystemInfo } from "../../mygo";
import { currentLang } from "../../i18n";
```

and replace

```tsx
import {
  GiB, MiB, SPARK_POINTS, emptyQuit, fmtMem, fmtPct, iconColor, isSparkShift, pickAlerts, quitErrCode,
  refreshFrozenRows, sortApps, splitMem, syncQuitRows, trimSpark, uptimeParts,
  type QuitState, type SortBy,
} from "./format";
```

with

```tsx
import {
  GiB, MiB, SPARK_POINTS, displayText, emptyQuit, fmtDec, fmtMem, fmtPct, fmtRate, fmtRateText, fmtTemp, iconColor,
  isSparkShift, modelLabel, netSparkMax, pickAlerts, quitErrCode, refreshFrozenRows, sortApps, splitMem, ssdWarnings,
  syncQuitRows, trimSpark, uptimeParts,
  type QuitState, type SortBy,
} from "./format";
```

**(b) Icons.** After the `BatteryIcon` constant (it ends with `<path d="m10 10-2 3h2.2L10 16" />`, `</svg>`, `);`), add:

```tsx
const GpuIcon = () => (
  <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <rect x="2" y="6" width="20" height="12" rx="2" /><rect x="6" y="9.5" width="7" height="5" rx="1" /><path d="M16 10.5h2M16 13.5h2M6 18v2.5M10 18v2.5" />
  </svg>
);
const NetIcon = () => (
  <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M8 4v16M8 20l-3.5-3.5M8 20l3.5-3.5M16 20V4M16 4l-3.5 3.5M16 4l3.5 3.5" />
  </svg>
);
```

**(c) The chart takes a scale and a color.** Replace

```tsx
/** 30 点 0–100% 面积曲线；重开采样时缓冲清空，不足 2 点不画。 */
function SparkSvg({ hist }: { hist: number[] }) {
```

with

```tsx
/** 曲线配色：CPU 用主题色，GPU 紫、网络蓝（与内存条的联动、App 两色一致）。 */
const SPARK_TONE = {
  accent: { stroke: "var(--accent)", fill: "var(--accent-soft)", fillOpacity: undefined },
  gpu: { stroke: "var(--seg-wired)", fill: "var(--seg-wired)", fillOpacity: 0.14 },
  net: { stroke: "var(--seg-app)", fill: "var(--seg-app)", fillOpacity: 0.14 },
} as const;

/** 30 点面积曲线，纵轴 0–max（默认 0–100%）；重开采样时缓冲清空，不足 2 点不画。 */
function SparkSvg({ hist, max = 100, tone = "accent" }: { hist: number[]; max?: number; tone?: keyof typeof SPARK_TONE }) {
```

replace

```tsx
  const y = (v: number) => (28 - (Math.max(0, Math.min(100, v)) / 100) * 26).toFixed(1);
```

with

```tsx
  const top = max > 0 ? max : 100;
  const y = (v: number) => (28 - (Math.max(0, Math.min(top, v)) / top) * 26).toFixed(1);
  const c = SPARK_TONE[tone];
```

and replace

```tsx
          <path fill="var(--accent-soft)" d={area} />
          <path fill="none" stroke="var(--accent)" strokeWidth={1.5} strokeLinejoin="round" d={line} />
```

with

```tsx
          <path fill={c.fill} fillOpacity={c.fillOpacity} d={area} />
          <path fill="none" stroke={c.stroke} strokeWidth={1.5} strokeLinejoin="round" d={line} />
```

**(d) `CpuCard`.** Replace

```tsx
export function CpuCard({ cpu, cores, hist, t }: { cpu: StatusSample["cpu"]; cores: number; hist: number[]; t: T }) {
```

with

```tsx
export function CpuCard({ cpu, cores, hist, t, m, split }: {
  cpu: StatusSample["cpu"]; cores: number; hist: number[]; t: T;
  /** macOS：性能核、能效核各自的使用率（读不到为 -1） */
  m?: MachineSample | null;
  /** 性能核、能效核的个数（MachineInfo；0 = 还不知道） */
  split?: { perf: number; eff: number };
}) {
```

and replace

```tsx
      {!isWindows() && <div className="mloads num">
        {loads.map(([label, v], i) => <div key={i}><span>{label}</span><b>{v}</b></div>)}
      </div>}
```

with

```tsx
      {!isWindows() && <div className="mloads num">
        {loads.map(([label, v], i) => <div key={i}><span>{label}</span><b>{v}</b></div>)}
      </div>}
      {m && (m.perfCpu >= 0 || m.effCpu >= 0) && (
        <ul className="mlegend num">
          {m.perfCpu >= 0 && <li><span>{split?.perf ? t("status.cpu.perfN", { n: split.perf }) : t("status.cpu.perf")}</span><b>{fmtPct(m.perfCpu)}</b></li>}
          {m.effCpu >= 0 && <li><span>{split?.eff ? t("status.cpu.effN", { n: split.eff }) : t("status.cpu.eff")}</span><b>{fmtPct(m.effCpu)}</b></li>}
        </ul>
      )}
```

**(e) `DiskCard`.** Replace the whole function (from `export function DiskCard(` to its closing `}`) with:

```tsx
export function DiskCard({ d, t, onClean, ssd, live }: {
  d: StatusSample["disk"]; t: T; onClean: () => void;
  /** macOS：内置 SSD（MachineInfo）；没有时不写型号、健康、寿命和写入量 */
  ssd?: SSDInfo | null;
  /** macOS：读写速度和 NAND 温度（MachineSample） */
  live?: MachineSample | null;
}) {
  const usedPct = d.total ? Math.min(100, Math.round((d.used / d.total) * 100)) : 0;
  const freeUnit = d.free > 0 ? splitMem(d.free)[1] : "";
  const smart = ssd?.smart ? ssd : null;
  const warns = smart ? ssdWarnings(smart.warning) : [];
  // NAND 传感器优先；没有时用进入首页时 SMART 日志里的温度
  const temp = live && live.diskTemp > 0 ? live.diskTemp : smart ? smart.tempC : 0;
  return (
    <article className="mcard cz-fade-in">
      <div className="mhead">
        <DiskIcon /><span>{t("status.card.disk")}</span>
        {warns.length > 0 && <em className="mbadge crit" title={warns.map((k) => t(k)).join("、")}>{t("status.ssd.attention")}</em>}
      </div>
      <div className="mhero num"><NumberTicker as="b" value={d.free} format={(v) => (v > 0 ? splitMem(v)[0] : "—")} /><i>{freeUnit}</i><span className="mctx">{t("status.disk.avail")}</span></div>
      <div className="mhcapt">{t("status.disk.totalOnly", { total: fmtMem(d.total) })}{ssd?.model ? ` · ${ssd.model}` : ""}</div>
      <div className="mviz"><div className="mbar"><i style={{ width: `${usedPct}%` }} /></div></div>
      <ul className={isWindows() ? "mlegend num mlegend-win" : "mlegend num"}>
        <li><span>{t("status.disk.used")}</span><b>{usedPct}%</b></li>
        <li><span>{t("status.disk.avail")}</span><b>{fmtMem(d.free)}</b></li>
        {live && live.diskRead >= 0 && <li><span>{t("status.disk.read")}</span><b>{fmtRateText(live.diskRead)}</b></li>}
        {live && live.diskWrite >= 0 && <li><span>{t("status.disk.write")}</span><b>{fmtRateText(live.diskWrite)}</b></li>}
        {smart && <li><span>{t("status.ssd.health")}</span><b>{warns.length > 0 ? t(warns[0]!) : t("status.ssd.ok")}</b></li>}
        {smart && <li><span>{t("status.ssd.life")}</span><b>{`${smart.used}%`}</b></li>}
        {smart && smart.written > 0 && <li><span>{t("status.ssd.written")}</span><b>{fmtDec(smart.written)}</b></li>}
        {temp > 0 && <li><span>{t("status.disk.temp")}</span><b>{fmtTemp(temp)}</b></li>}
      </ul>
      <div className="mfoot">
        <span>{t("status.disk.footHint")}</span>
        <button className="mlink" type="button" onClick={onClean}>{t("status.disk.clean")} ›</button>
      </div>
    </article>
  );
}
```

**(f) `BatteryCard`.** Replace `function BatteryCard({ b, t }: { b: BatteryStatus; t: T }) {` with `export function BatteryCard({ b, t }: { b: BatteryStatus; t: T }) {`, and replace

```tsx
      <ul className="mlegend num">
        <li><span>{t("status.battery.maxCap")}</span><b>{healthPct > 0 ? `${healthPct}%` : "—"}</b></li>
        <li><span>{t("status.battery.condition")}</span><b>{condVal}</b></li>
      </ul>
```

with

```tsx
      <ul className="mlegend num">
        <li><span>{t("status.battery.maxCap")}</span><b>{healthPct > 0 ? `${healthPct}%` : "—"}</b></li>
        <li><span>{t("status.battery.condition")}</span><b>{condVal}</b></li>
        {b.temperature > 0 && <li><span>{t("status.battery.temp")}</span><b>{fmtTemp(b.temperature)}</b></li>}
        {b.plugged && b.adapterWatts > 0 && <li><span>{t("status.battery.adapter")}</span><b>{`${b.adapterWatts} W`}</b></li>}
      </ul>
```

**(g) The new GPU and network cards and the health strip.** Insert before the line `// ---- 骨架态（首份采样未到：禁 0 / 假值） ----`:

```tsx
export function GpuCard({ m, cores, displays, hist, t }: { m: MachineSample; cores: number; displays: number; hist: number[]; t: T }) {
  return (
    <article className="mcard cz-fade-in">
      <div className="mhead"><GpuIcon /><span>{t("status.card.gpu")}</span></div>
      {m.gpu >= 0
        ? <div className="mhero num"><NumberTicker as="b" value={m.gpu} format={(v) => String(Math.round(v))} /><i>%</i></div>
        : <div className="mhero num"><b className="ph">—</b></div>}
      <div className="mhcapt">{t("status.gpu.caption")}</div>
      <div className="mviz"><SparkSvg hist={hist} tone="gpu" /></div>
      <ul className="mlegend num">
        <li><span>{t("status.gpu.memory")}</span><b>{fmtMem(m.gpuMemory)}</b></li>
        <li><span>{t("status.gpu.displays")}</span><b>{displays > 0 ? t("status.gpu.displayCount", { n: displays }) : "—"}</b></li>
      </ul>
      <div className="mfoot num">{cores > 0 ? t("status.cpu.cores", { n: cores }) : "—"}</div>
    </article>
  );
}

export function NetCard({ m, hist, t }: { m: MachineSample; hist: number[]; t: T }) {
  const [down, downUnit] = fmtRate(m.netDown);
  const [up, upUnit] = fmtRate(m.netUp);
  const kind = m.netKind === "wifi" ? t("status.net.wifi") : m.netKind === "ethernet" ? t("status.net.ethernet") : "";
  return (
    <article className="mcard cz-fade-in">
      <div className="mhead"><NetIcon /><span>{t("status.card.network")}</span>{kind && <span className="mhead-note">{kind}</span>}</div>
      <div className="mhero mhero-duo num">
        <i>↓</i><b>{down}</b><i>{downUnit}</i>
        <i className="mgap">↑</i><b>{up}</b><i>{upUnit}</i>
      </div>
      <div className="mhcapt">{t("status.net.caption")}</div>
      <div className="mviz"><SparkSvg hist={hist} max={netSparkMax(hist)} tone="net" /></div>
      <div className="mfoot">{t("status.net.foot")}</div>
    </article>
  );
}

/** thermal state 四档（NSProcessInfo）：正常 / 偏热 / 很热 / 过热。 */
const THERMAL = [
  { key: "status.health.thermal0", tone: "ok" },
  { key: "status.health.thermal1", tone: "warn" },
  { key: "status.health.thermal2", tone: "crit" },
  { key: "status.health.thermal3", tone: "crit" },
] as const;

/** 健康条（设计 §三.2）：读不到的格子整格省掉，四格都读不到时整条不出现。 */
export function HealthStrip({ m, t }: { m: MachineSample; t: T }) {
  const tiles: Array<{ key: string; label: string; value: React.ReactNode; tone?: string }> = [];
  const thermal = m.thermal >= 0 ? THERMAL[Math.min(m.thermal, THERMAL.length - 1)] : undefined;
  if (thermal) tiles.push({ key: "thermal", label: t("status.health.thermal"), value: t(thermal.key), tone: thermal.tone });
  if (m.chipTemp > 0) tiles.push({ key: "chip", label: t("status.health.chipTemp"), value: fmtTemp(m.chipTemp) });
  if (m.fanCount === 0) {
    tiles.push({ key: "fans", label: t("status.health.fans"), value: t("status.health.noFans") });
  } else if (m.fanCount > 0 && m.fans.length > 0) {
    const value = m.fans.every((rpm) => rpm < 1)
      ? t("status.health.fansIdle")
      : <>{m.fans.join(" · ")} <small>rpm</small></>;
    tiles.push({ key: "fans", label: t("status.health.fans"), value });
  }
  if (m.power > 0) tiles.push({ key: "power", label: t("status.health.power"), value: `${Math.round(m.power)} W` });
  if (tiles.length === 0) return null;
  return (
    <section className="st-health cz-fade-in" aria-label={t("status.health.label")}>
      {tiles.map((x) => (
        <div key={x.key} className="st-htile">
          <span className="k">{x.label}</span>
          <b className={x.tone ? `v num ${x.tone}` : "v num"}>{x.value}</b>
        </div>
      ))}
    </section>
  );
}

```

**(h) The `goProcess` prop's comment.** Replace

```tsx
  /** 增长提示的「查看」、列表的「查看全部」：打开进程页的某个标签，可带磁盘占用标签里要定位的行（main.ts 注入）。 */
```

with

```tsx
  /** 增长提示的「查看」、内存提示的「看谁在占内存」（macOS）：打开进程页的某个标签，可带磁盘占用标签里要定位的行（main.ts 注入）。 */
```

**(i) State.** After `  const [cpuHist, setCpuHist] = React.useState<number[]>([]);` add:

```tsx
  /** 这台 Mac 是什么（macOS）：每次进入读一次，显示器变了再读；读到之前页头还是「系统状态」。 */
  const [machine, setMachine] = React.useState<MachineInfo | null>(null);
  const [gpuHist, setGpuHist] = React.useState<number[]>([]);
  const [netHist, setNetHist] = React.useState<number[]>([]);
```

and after

```tsx
  const sysInfoRef = React.useRef(sysInfo);
  sysInfoRef.current = sysInfo;
```

add

```tsx
  const machineRef = React.useRef(machine);
  machineRef.current = machine;
  const machineSeqRef = React.useRef(0);
```

**(j) Reading the identity and the new charts.** Replace the whole `startWatch` callback (from `  const startWatch = React.useCallback(async () => {` through `  }, [loadInfo]);`) with:

```tsx
  /** 型号、核数、GPU、显示器、SSD（macOS）：读不到时保留上一次的；从没读到时页头保持原样。 */
  const loadMachine = React.useCallback(async () => {
    if (isWindows()) return;
    const seq = ++machineSeqRef.current;
    try {
      const m = await statusApi.machine();
      if (seq === machineSeqRef.current) setMachine(m);
    } catch { /* 下次进入或显示器变化时再读 */ }
  }, []);

  const startWatch = React.useCallback(async () => {
    const myRun = ++runRef.current;
    stoppedRef.current = false;
    setError(false);
    setSample(null);
    setCpuHist([]);
    setGpuHist([]);
    setNetHist([]);
    setQuit(emptyQuit());
    frozenRowsRef.current = [];
    void loadInfo();
    void loadMachine();
    const ch = new Channel<StatusSample>();
    chanRef.current = ch;
    try {
      const done = statusApi.watch(ch);
      for await (const s of ch) {
        if (runRef.current !== myRun) continue; // 已切走 / 重开：丢弃旧采样
        setSample(s);
        setCpuHist((prev) => trimSpark(prev, s.cpu.usage));
        const m = s.machine;
        if (m) {
          if (m.gpu >= 0) setGpuHist((prev) => trimSpark(prev, m.gpu));
          if (m.netDown >= 0) setNetHist((prev) => trimSpark(prev, m.netDown));
          // 接上或拔掉显示器：采样里的签名和页头读到的不同，重读一次
          const known = machineRef.current;
          if (known?.displaySig && m.displaySig && m.displaySig !== known.displaySig) void loadMachine();
        }
      }
      await done;
    } catch (e) {
      if (runRef.current === myRun && !stoppedRef.current) {
        // 本轮采样已经结束；重试按钮等价于 stopWatch + startWatch 重建通道
        setError(true);
        setSample(null);
        setCpuHist([]);
        setGpuHist([]);
        setNetHist([]);
      }
    } finally {
      if (chanRef.current === ch) chanRef.current = null;
    }
  }, [loadInfo, loadMachine]);
```

**(k) The header's parts.** Replace

```tsx
  const machineParts: string[] = [];
  if (sysInfo) {
    if (sysInfo.chip) machineParts.push(sysInfo.chip);
    if (sysInfo.memory >= GiB) machineParts.push(t("status.machine.mem", { size: `${Math.round(sysInfo.memory / GiB)} GB` }));
    else if (sysInfo.memory) machineParts.push(t("status.machine.mem", { size: `${Math.round(sysInfo.memory / MiB)} MB` }));
    if (sysInfo.osVersion) machineParts.push(t("status.osVersion", { version: sysInfo.osVersion }));
    const up = sysInfo.bootTime > 0 ? uptimeParts(sysInfo.bootTime, Date.now() / 1000) : null;
    if (up) {
      machineParts.push(
        up.days >= 1 ? t("status.uptime.days", { d: up.days, h: up.hours })
          : up.hours >= 1 ? t("status.uptime.hours", { h: up.hours, m: up.mins })
            : t("status.uptime.mins", { m: up.mins }),
      );
    }
  }
```

with

```tsx
  let memPart = "", osPart = "", upPart = "";
  if (sysInfo) {
    if (sysInfo.memory >= GiB) memPart = t("status.machine.mem", { size: `${Math.round(sysInfo.memory / GiB)} GB` });
    else if (sysInfo.memory) memPart = t("status.machine.mem", { size: `${Math.round(sysInfo.memory / MiB)} MB` });
    if (sysInfo.osVersion) osPart = t("status.osVersion", { version: sysInfo.osVersion });
    const up = sysInfo.bootTime > 0 ? uptimeParts(sysInfo.bootTime, Date.now() / 1000) : null;
    if (up) {
      upPart = up.days >= 1 ? t("status.uptime.days", { d: up.days, h: up.hours })
        : up.hours >= 1 ? t("status.uptime.hours", { h: up.hours, m: up.mins })
          : t("status.uptime.mins", { m: up.mins });
    }
  }
  const machineParts = [sysInfo?.chip ?? "", memPart, osPart, upPart].filter(Boolean);
  // macOS 页头讲这台机器（设计 §三.1）：型号作标题；规格一行；系统、运行时长和显示器一行。
  const label = machine && !isWindows() ? modelLabel(machine.model, machine.modelId, currentLang()) : null;
  const specParts: string[] = [];
  const hwParts: string[] = [];
  if (machine && label?.name) {
    if (sysInfo?.chip) specParts.push(sysInfo.chip);
    const { perfCores: p, effCores: e } = machine;
    if (p > 0 && e > 0) specParts.push(t("status.spec.cpuSplit", { n: p + e, p, e }));
    else if (sysInfo?.cores) specParts.push(t("status.spec.cpu", { n: sysInfo.cores }));
    if (machine.gpuCores > 0) specParts.push(t("status.spec.gpu", { n: machine.gpuCores }));
    if (memPart) specParts.push(memPart);
    if (machine.ssd && machine.ssd.capacity > 0) specParts.push(t("status.spec.disk", { size: fmtDec(machine.ssd.capacity) }));
    if (osPart) hwParts.push(osPart);
    if (upPart) hwParts.push(upPart);
    const screens = machine.displays.map((d) => displayText(d, t(d.builtin ? "status.display.builtin" : "status.display.external")));
    if (screens.length > 0) hwParts.push(screens.join(" + "));
  }
```

**(l) The header and the health strip.** Replace

```tsx
      <header className="view-head">
        <div>
          <h2>{t("nav.status")}</h2>
          <p className="muted">{t("status.sub")}</p>
          {machineParts.length > 0 && <p className="st-machine num">{machineParts.join(" · ")}</p>}
        </div>
      </header>
```

with

```tsx
      <header className="view-head">
        {label?.name ? (
          <div>
            <h2 className="st-model">{label.name}{label.note && <span className="st-model-note">{label.note}</span>}</h2>
            {specParts.length > 0 && <p className="muted num">{specParts.join(" · ")}</p>}
            {hwParts.length > 0 && <p className="st-machine num">{hwParts.join(" · ")}</p>}
          </div>
        ) : (
          <div>
            <h2>{t("nav.status")}</h2>
            <p className="muted">{t("status.sub")}</p>
            {machineParts.length > 0 && <p className="st-machine num">{machineParts.join(" · ")}</p>}
          </div>
        )}
      </header>

      {!isWindows() && !error && sample?.machine && <HealthStrip m={sample.machine} t={t} />}
```

**(m) The memory alert's button.** Replace

```tsx
                  onClick={() => (a.action === "apps" ? viewApps() : goCleanRef.current())}
                >
                  {a.action === "apps" ? t("status.alert.viewApps") : t("status.disk.clean")}
```

with

```tsx
                  onClick={() => (a.action !== "apps" ? goCleanRef.current() : isWindows() ? viewApps() : goProcess("mem"))}
                >
                  {a.action !== "apps" ? t("status.disk.clean") : isWindows() ? t("status.alert.viewApps") : t("status.alert.viewInProcess")}
```

**(n) The cards.** Replace

```tsx
          {sample ? (
            <>
              <MemoryCard m={sample.memory} t={t} />
              <CpuCard cpu={sample.cpu} cores={sysInfo?.cores ?? 0} hist={cpuHist} t={t} />
              <DiskCard d={sample.disk} t={t} onClean={() => goCleanRef.current()} />
              {sample.battery && <BatteryCard b={sample.battery} t={t} />}
            </>
          ) : (
            Array.from({ length: 4 }, (_, i) => <SkCard key={i} />)
          )}
```

with

```tsx
          {sample ? (
            isWindows() ? (
              <>
                <MemoryCard m={sample.memory} t={t} />
                <CpuCard cpu={sample.cpu} cores={sysInfo?.cores ?? 0} hist={cpuHist} t={t} />
                <DiskCard d={sample.disk} t={t} onClean={() => goCleanRef.current()} />
                {sample.battery && <BatteryCard b={sample.battery} t={t} />}
              </>
            ) : (
              <>
                <CpuCard
                  cpu={sample.cpu} cores={sysInfo?.cores ?? 0} hist={cpuHist} t={t}
                  m={sample.machine} split={machine ? { perf: machine.perfCores, eff: machine.effCores } : undefined}
                />
                {sample.machine && <GpuCard m={sample.machine} cores={machine?.gpuCores ?? 0} displays={machine?.displays.length ?? 0} hist={gpuHist} t={t} />}
                <MemoryCard m={sample.memory} t={t} />
                <DiskCard d={sample.disk} t={t} onClean={() => goCleanRef.current()} ssd={machine?.ssd} live={sample.machine} />
                {sample.machine && <NetCard m={sample.machine} hist={netHist} t={t} />}
                {sample.battery && <BatteryCard b={sample.battery} t={t} />}
              </>
            )
          ) : (
            Array.from({ length: isWindows() ? 4 : 6 }, (_, i) => <SkCard key={i} />)
          )}
```

**(o) The app list and the quit dialog become Windows-only.**
- Replace `      <section className="st-listcard">` with:

```tsx
      {/* 「占用最多的应用」只在 Windows 首页：macOS 有「进程」页（设计 2026-10-28 §三.5）。 */}
      {isWindows() && <>
      <section className="st-listcard">
```

- Delete this block inside the list head:

```tsx
          {!isWindows() && (
            <button className="st-viewall" type="button" onClick={() => goProcess(sortBy)}>
              {t("status.apps.viewAll")} <span aria-hidden="true">›</span>
            </button>
          )}
```

- Replace the end of the component,

```tsx
      </Dialog>
    </div>
  );
}
```

  with

```tsx
      </Dialog>
      </>}
    </div>
  );
}
```

- Indent the lines between the new `{isWindows() && <>` and `</>}` by two more spaces.

- [ ] **Step 6: Strings in `src/i18n.ts`**

Delete the two `"status.apps.viewAll"` lines (`"查看全部"` in `zh`, `"View All"` in `en`).

In `zh`, after `    "status.uptime.mins": "已运行 {m} 分钟",` add:

```ts
    "status.card.gpu": "GPU",
    "status.card.network": "网络",
    "status.spec.cpu": "{n} 核 CPU",
    "status.spec.cpuSplit": "{n} 核 CPU（{p} 性能 + {e} 能效）",
    "status.spec.gpu": "{n} 核 GPU",
    "status.spec.disk": "{size} 磁盘",
    "status.display.builtin": "内建显示器",
    "status.display.external": "外接显示器",
    "status.health.label": "健康状况",
    "status.health.thermal": "散热状态",
    "status.health.thermal0": "正常",
    "status.health.thermal1": "偏热",
    "status.health.thermal2": "很热",
    "status.health.thermal3": "过热",
    "status.health.chipTemp": "芯片温度",
    "status.health.fans": "风扇",
    "status.health.noFans": "无风扇",
    "status.health.fansIdle": "未转动",
    "status.health.power": "整机功耗",
    "status.cpu.perf": "性能核",
    "status.cpu.perfN": "性能核（{n}）",
    "status.cpu.eff": "能效核",
    "status.cpu.effN": "能效核（{n}）",
    "status.gpu.caption": "使用率 · 最近 1 分钟",
    "status.gpu.memory": "占用的内存",
    "status.gpu.displays": "显示器",
    "status.gpu.displayCount": "{n} 台",
    "status.net.caption": "曲线是下载速度 · 最近 1 分钟",
    "status.net.wifi": "Wi-Fi",
    "status.net.ethernet": "以太网",
    "status.net.foot": "只算 Wi-Fi 和以太网",
    "status.disk.read": "读取",
    "status.disk.write": "写入",
    "status.disk.temp": "温度",
    "status.ssd.health": "健康",
    "status.ssd.ok": "正常",
    "status.ssd.life": "寿命已用",
    "status.ssd.written": "已写入",
    "status.ssd.attention": "需要留意",
    "status.ssd.warn.spare": "备用块不足",
    "status.ssd.warn.temp": "温度超限",
    "status.ssd.warn.reliability": "可靠性下降",
    "status.ssd.warn.readonly": "已变为只读",
    "status.ssd.warn.backup": "断电保护失效",
    "status.ssd.warn.other": "报告了警告",
    "status.battery.temp": "温度",
    "status.battery.adapter": "电源",
    "status.alert.viewInProcess": "看谁在占内存",
```

In `en`, after `    "status.uptime.mins": "Up {m}m",` add:

```ts
    "status.card.gpu": "GPU",
    "status.card.network": "Network",
    "status.spec.cpu": "{n}-core CPU",
    "status.spec.cpuSplit": "{n}-core CPU ({p}P + {e}E)",
    "status.spec.gpu": "{n}-core GPU",
    "status.spec.disk": "{size} disk",
    "status.display.builtin": "Built-in display",
    "status.display.external": "External display",
    "status.health.label": "Health",
    "status.health.thermal": "Thermal state",
    "status.health.thermal0": "Normal",
    "status.health.thermal1": "Warm",
    "status.health.thermal2": "Hot",
    "status.health.thermal3": "Critical",
    "status.health.chipTemp": "Chip temperature",
    "status.health.fans": "Fans",
    "status.health.noFans": "No fans",
    "status.health.fansIdle": "Not spinning",
    "status.health.power": "System power",
    "status.cpu.perf": "Performance cores",
    "status.cpu.perfN": "Performance ({n})",
    "status.cpu.eff": "Efficiency cores",
    "status.cpu.effN": "Efficiency ({n})",
    "status.gpu.caption": "utilization · last minute",
    "status.gpu.memory": "Memory in use",
    "status.gpu.displays": "Displays",
    "status.gpu.displayCount": "{n}",
    "status.net.caption": "chart: download · last minute",
    "status.net.wifi": "Wi-Fi",
    "status.net.ethernet": "Ethernet",
    "status.net.foot": "Wi-Fi and Ethernet only",
    "status.disk.read": "Read",
    "status.disk.write": "Write",
    "status.disk.temp": "Temperature",
    "status.ssd.health": "Health",
    "status.ssd.ok": "Normal",
    "status.ssd.life": "Life used",
    "status.ssd.written": "Written",
    "status.ssd.attention": "Needs attention",
    "status.ssd.warn.spare": "Low spare blocks",
    "status.ssd.warn.temp": "Over temperature",
    "status.ssd.warn.reliability": "Reliability degraded",
    "status.ssd.warn.readonly": "Read-only",
    "status.ssd.warn.backup": "Power-loss protection failed",
    "status.ssd.warn.other": "Warning reported",
    "status.battery.temp": "Temperature",
    "status.battery.adapter": "Power adapter",
    "status.alert.viewInProcess": "See what's using memory",
```

- [ ] **Step 7: Styles in `src/style.css`**

Delete the `.st-viewall { … }` rule.

After the line `.st-spark svg { display: block; width: 100%; height: 32px; overflow: hidden; }`, add:

```css
/* ---- macOS 首页：型号作标题、健康条（设计 2026-10-28 §三） ---- */
.view-head h2.st-model { display: flex; align-items: baseline; flex-wrap: wrap; column-gap: 10px; }
.st-model-note { font-size: 14px; font-weight: 500; letter-spacing: 0; color: var(--muted); }
#view-status .cz-react .st-health { margin-top: 20px; }
#view-status .cz-react .st-health + .st-alerts:not(:empty) { margin-top: 16px; }
.st-health { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 12px; }
.st-htile {
  min-width: 0;
  padding: 10px 14px;
  background: var(--card);
  border: 1px solid var(--border);
  border-radius: 10px;
  box-shadow: var(--shadow);
}
.st-htile .k { display: block; font-size: 12px; color: var(--muted); }
.st-htile .v {
  display: block;
  margin-top: 2px;
  overflow: hidden;
  font-size: 17px;
  font-weight: 600;
  color: var(--fg);
  font-variant-numeric: tabular-nums;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.st-htile .v small { font-size: 12px; font-weight: 400; color: var(--muted); }
.st-htile .v.ok { color: var(--accent-strong); }
.st-htile .v.warn { color: var(--warn-text); }
.st-htile .v.crit { color: var(--danger); }
/* 卡片标题右侧的说明（网络的连接方式） */
.mhead-note { margin-left: auto; font-size: 12px; font-weight: 500; color: var(--muted); }
/* 网络卡：下载、上传两个数并排 */
.mhero-duo b { font-size: 22px; }
.mhero .mgap { margin-left: 10px; }
```

- [ ] **Step 8: The dev mock shows this Mac**

In `src/dev/mock-mygo.ts`:
- Add `MachineInfo` to the `import type { … } from "../mygo";` list.
- After the `const info: SystemInfo = { … };` statement, add:

```ts
  const machineInfo: MachineInfo = {
    model: "MacBook Pro (16-inch, 2023)", modelId: "Mac14,10", perfCores: 8, effCores: 4, gpuCores: 19,
    displays: [
      { id: 1, name: "Built-in Retina Display", builtin: true, width: 1728, height: 1117, hz: 120 },
      { id: 2, name: "LS27D80xU", builtin: false, width: 1920, height: 1080, hz: 60 },
    ],
    displaySig: "1,2",
    ssd: { model: "APPLE SSD AP0512Z", capacity: 500_277_792_768, smart: true, warning: 0, tempC: 33, spare: 100, used: 26, written: 624.5e12 },
  };
```

- In `sample()`, replace `temperature: 0, adapterWatts: 0 }` with `temperature: 30.6, adapterWatts: 90 }`, and replace `      machine: null,` with:

```ts
      machine: {
        thermal: 0, chipTemp: 48 + drift(4) * 4, fanCount: 2, fans: [1333, 1464], power: 36 + drift(5) * 6,
        perfCpu: 31 + drift(6) * 20, effCpu: 58 + drift(7) * 20, gpu: 8 + Math.abs(drift(8)) * 20, gpuMemory: 1.1 * GB,
        diskRead: 10.3e6 * (1 + drift(9)), diskWrite: 9.6e6 * (1 + drift(10)), diskTemp: 36,
        netDown: 120_000 * (1 + drift(11)), netUp: 110_000 * (1 + drift(12)), netKind: "wifi", displaySig: "1,2",
      },
```

- In `handlers`, after `    "Cleaner.Info": async () => info,` add:

```ts
    "Cleaner.MachineInfo": async () => machineInfo,
```

- [ ] **Step 9: Run the tests to see them pass**

Run: `bun run typecheck && bun test tests/status-react.test.ts && bun run test:web`
Expected: PASS. `tests/i18n-windows.test.ts`, which runs within `test:web`, finds no macOS terms in the new strings.

- [ ] **Step 10: Docs**

`CHANGELOG.md`, under `## 未发布`:
- After the bullet that starts with `- 新增「进程」页（macOS）`, add:

```markdown
- 系统状态首页（macOS）改成只讲这台 Mac：标题是型号（如「MacBook Pro 16 英寸 · 2023」），下面一行写芯片、CPU 性能核和能效核、GPU 核数、内存和磁盘容量，再一行写系统版本、已运行时长和每台显示器的分辨率与刷新率。新增一条健康状况：散热状态、芯片温度、风扇转速、整机功耗；新增 GPU（使用率、占用的内存）和网络（下载、上传速度，Wi-Fi 还是以太网）两张卡；CPU 卡分开写性能核和能效核；磁盘卡加上 SSD 型号、健康、寿命已用、累计写入、温度和读写速度；电池卡加上电池温度和电源功率。这些都不需要管理员权限，只在首页开着时读
- macOS 首页去掉「占用最多的应用」：看谁在占 CPU 和内存去「进程」页，内存紧张提示上的按钮直接打开「进程」页的内存标签；首页也不再每 2 秒读一遍所有进程。Windows 首页不变
```

- In the bullet `- App 显示自己的图标（macOS）：…`, replace `系统状态「占用最多的应用」、应用卸载的已安装列表，以及「进程」页和「磁盘占用」` with `应用卸载的已安装列表、「进程」页和「磁盘占用」`.
- In the bullet `- 系统状态页：后台监控打开、…`, delete `「占用最多的应用」多了「查看全部」，打开「进程」页；`.
- Delete the bullet `- 修复：Apple 芯片的 Mac 上，系统状态「占用最多的应用」里每个 App 的 CPU 少算了约 40 倍（系统给的 CPU 时间是 Mach 时间单位，以前当成了纳秒）`.

`README.md`, in `## 功能`, before the bullet that starts with `- 进程（macOS）：`, add:

```markdown
- 系统状态（macOS）：首页只讲这台 Mac——型号、芯片和核数、GPU、内存、磁盘和显示器；散热状态、芯片温度、风扇转速和整机功耗；CPU（性能核、能效核分开）、GPU、内存、磁盘（SSD 型号、健康、寿命已用、累计写入、温度、读写速度）、网络（下载、上传速度）和电池（温度、电源功率）。都不需要管理员权限，只在首页开着时读；看谁在占 CPU 和内存去「进程」页
```

- [ ] **Step 11: Leave the changes uncommitted.**

---

## Reviewer verification (on the maintainer's Mac)

Run after Task 4 is on `feat/growth-monitor`.

1. **Suites:**
   - `gofmt -l .` prints nothing
   - `go vet ./... && GOOS=windows GOARCH=amd64 go vet ./...`
   - `bun run typecheck && bun run test:web`
   - `go test -race -count=1 ./...`
   - `CLEANZO_HW=1 go test -count=1 -run 'TestIOKitLoads|TestIOPropReadsTheDeviceTree|TestReadMachineInfoLive|TestInterfaceBytesLive|TestMachineReaderLive|TestSmartBatteryExtrasLive' -v .`
   - `CLEANZO_PERF=1 go test -count=1 -run TestStatusSamplingBudget -v .`: ≤ 1% of one core
   - `bun run build`
2. **Readings against the system**, production build with the home page in front:
   - **Activity Monitor:**
     - CPU %, from "User + System" at the bottom of the CPU tab;
     - GPU, from Window › GPU History;
     - memory;
     - disk "Data read/written per sec";
     - network "Data received/sent per sec".
     All should agree within sampling differences.
   - **System Information:**
     - model name, chip and cores (Hardware);
     - NVMe model and capacity (NVMExpress);
     - displays, resolutions and refresh rates (Graphics/Displays).
   - **Battery:** `ioreg -rn AppleSmartBattery | grep -E '"Temperature"|"Watts"'` gives the battery temperature (hundredths of °C) and the adapter's watts.
   - **SMART:** if `smartctl` is installed, `smartctl -a disk0` gives the critical warning, percentage used and data units written.
   - **Fans under load:**
     1. Run `for i in $(seq $(sysctl -n hw.ncpu)); do yes > /dev/null & done` for about two minutes.
     2. Chip temperature, fan speeds and power should rise.
     3. Then `killall yes`; they should fall back within a few minutes.
   - **Displays:** unplug the external display while on the home page. The header's display list should change within about 10 s.
3. **Budget** (spec §五), production build:
   - Alternate the home page and 设置 every 30 s, 3 rounds each.
   - Measure each window with `proc_pid_rusage` (`RUSAGE_INFO_V2`): the main pid's user+system at bytes 16 and 24 plus its children's at 96 and 104, and the WebContent pid.
   - The home page's cost is home minus 设置. Pass: main ≤ 1%, WebContent ≤ 1.5%.
   - Don't type during the windows (the input method costs CPU), and keep the screen unlocked.
4. **Captures**, light and dark: window-only (`screencapture -x -o -l <windowid>`) of the header, health strip and six cards. Nothing blank, 0, NaN or -1 where a reading is unknown.
5. **Memory alert:** when memory is under pressure, 「看谁在占内存」 opens 进程 → 内存.
6. **Smoke tour:** `bun test tests/smoke`. The home captures look right and `errors.json` is empty.
7. **Windows:** CI's Windows job passes, and the Windows home page still has 「占用最多的应用」.

Never press quit buttons through AX automation on this Mac, and capture only Cleanzo's own windows.

## Execution batches

| Run | Tool | Task | Where |
|-----|------|------|-------|
| A | traecli | Task 1 | main checkout, `feat/growth-monitor` |
| B | Codex (`codex:codex-rescue`, `task --write`) | Task 2 | main checkout, after A is committed |
| C | Codex (`codex:codex-rescue`, `task --write`) | Task 3 | main checkout, after B is committed |
| D | traecli | Task 4 | main checkout, after C is committed |

The runs go one after another, and each task is reviewed and committed before the next starts.

The executors' sandboxes block several reads:
- setuid exec, the `kern.proc.all` sysctl and Icon Services;
- probably the SMC, IOHID sensors, the SMART plug-in and the window server.

The live tests then skip, or check only what they read. Inside a sandbox, run the full Go suite with

```
-skip 'TestLargePhotosAreDecodedSmall|TestUsageGuardSeesSockets|TestNativeMachineInfo|TestNativeSysctlRawProbes|TestCheckAndUpgradeLockedSkill|TestUpgradeRefusesLocalChangesUnlessForced|TestUpgradeRejectsArchiveThatDoesNotMatch|TestCheckReportsMissingAndRateLimit'
```

The reviewer re-runs everything outside the sandbox, including the `CLEANZO_HW=1` and `CLEANZO_PERF=1` runs.

## Implementation notes (2026-10-28/29)

- **Executors:** Codex's provider failed once (503) and then took 3 h on Task 3; traecli wrote Tasks 1, 2 and 4 and every fix round.
- **Task 1:** `cfDictValue` also checks that its argument is a CFDictionary; CoreFoundation aborts the app when CFDictionaryGetValue gets anything else. `ioProp` returns 0 for a key that isn't valid UTF-8.
- **Task 2:** drives that say "Physical Interconnect Location" = "External" are skipped; a core split that doesn't add up to hw.physicalcpu reports 0/0; the test runs the NSScreen closure synchronously.
- **Task 3:** NaN and ±Inf from sensors and SMC floats are rejected (mygo's JSON encoder refuses them and the stream would end); a failed fan read makes all fan speeds unknown; missing SMC keys are cached.
- **Task 4:** the macOS grid is capped at three columns (minimum 280 px); the network numbers wrap per direction; a placeholder health strip holds the space before the first sample.
- **Final review:** the SMART plug-in admits one reader system-wide (overlapping opens get 0xe00002be, kIOReturnNoResources), so reads are serialized, retried and fall back to the last page; the machine reader opens after the battery read starts; unknown GPU rows are omitted; the built-in display uses the app's own label.
- **Budget** (production build, home vs 设置, 30 s × 3): the old home page was 5.56% / 3.38%; the first build was ~1.6% / ~2.3%. Width transitions on the bars and per-frame number rolls were the cost. Bars now move with transform, the chart glide animates the outer `<svg>`, and — the maintainer's choice on 2026-10-29 — macOS home numbers change in place (Windows keeps the roll). Final: main+children 0.83% (main alone 0.57%), web 1.39%.
