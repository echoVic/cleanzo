package main

// monitor.go holds the system status page's rules: the sample types the
// frontend sees, the sampling loop, the per-app attribution, the battery
// parsing and the graceful quit protocol. System reads are injected through
// statusSource, like cleanHooks, so tests run without macOS.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// ---- types ----------------------------------------------------------------

// workspaceApp is a running GUI application as the system sees it.
type workspaceApp struct {
	Pid      int32
	Path     string // bundle directory ("" for faceless processes)
	BundleID string
	Name     string // localized name, falling back to the bundle name
	Regular  bool   // activationPolicy == regular, i.e. has a Dock icon
}

// MemoryStatus is the memory picture in Activity Monitor's terms.
// All byte values; zero means "could not be read" and shows as a dash.
type MemoryStatus struct {
	Total      uint64 `json:"total"`      // hw.memsize
	Used       uint64 `json:"used"`       // app + wired + compressed
	App        uint64 `json:"app"`        // internal − purgeable
	Wired      uint64 `json:"wired"`      // pages wired down
	Compressed uint64 `json:"compressed"` // pages the compressor holds in physical memory
	Cached     uint64 `json:"cached"`     // external + purgeable, not part of Used
	Pressure   uint64 `json:"pressure"`   // 1 normal, 2 warn, 4 critical; unknown → normal
	SwapUsed   uint64 `json:"swapUsed"`   // sysctl vm.swapusage used
	SwapTotal  uint64 `json:"swapTotal"`  // = disk space the swap files take
}

// CPUStatus is the overall CPU card: usage is a percentage of all cores,
// matching Activity Monitor's "User + System" at the bottom of its window.
// Loads are the 1/5/15-minute averages from sysctl vm.loadavg.
type CPUStatus struct {
	Usage  float64 `json:"usage"`
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

// BatteryStatus mirrors what System Settings shows. Fields the system
// does not report stay zero/empty and render as dashes.
type BatteryStatus struct {
	Percent     int    `json:"percent"`
	Charging    bool   `json:"charging"`
	Full        bool   `json:"full"`
	Plugged     bool   `json:"plugged"`
	Cycles      int    `json:"cycles"`
	MaxCapacity int    `json:"maxCapacity"`
	Condition   string `json:"condition"` // e.g. "Normal"; "" unknown
}

// AppUsage is one merged row: a bundle's processes summed up. CPU is a
// single-core percentage like Activity Monitor's CPU column, so it can
// exceed 100 while the card above stays below it.
type AppUsage struct {
	Key    string  `json:"key"`
	Name   string  `json:"name"`
	Path   string  `json:"path"`
	Memory uint64  `json:"memory"`
	CPU    float64 `json:"cpu"`
	Procs  int     `json:"procs"`
	Quit   bool    `json:"quit"`
}

// StatusSample is one complete reading, sent every two seconds.
type StatusSample struct {
	Memory  MemoryStatus   `json:"memory"`
	CPU     CPUStatus      `json:"cpu"`
	Disk    DiskStat       `json:"disk"`
	Battery *BatteryStatus `json:"battery"` // nil: desktop Mac, or not read yet
	Apps    []AppUsage     `json:"apps"`
	Procs   int            `json:"procs"` // processes counted, this user only
}

// procSample is the pid-keyed state the delta math needs. Start is
// ri_proc_start_abstime: when it changes the pid was reused, and the
// process counts as new (no CPU delta that round).
type procSample struct {
	Pid    int32
	Path   string
	Memory uint64
	UserNs uint64
	SysNs  uint64
	Start  uint64
}

// statusSource is everything the loop reads, injectable for tests.
type statusSource struct {
	procs     func() []procReading
	apps      func() []workspaceApp
	memory    func() MemoryStatus
	cpu       func() (busy, total uint64) // cumulative ticks
	battery   func(ctx context.Context) *BatteryStatus
	disk      func() DiskStat
	terminate func(pid int32) bool
}

// Timing knobs, variables so tests can shrink them.
var (
	statusFirstDelay   = 500 * time.Millisecond // second CPU reading
	statusFirstBudget  = 3 * time.Second        // first sample overall
	statusInterval     = 2 * time.Second
	statusBatteryEvery = 60 * time.Second
	statusQuitTimeout  = 5 * time.Second
	statusQuitPoll     = 100 * time.Millisecond
)

// memoryFromVM converts page counts into Activity Monitor's categories:
// App Memory = internal − purgeable, Wired, Compressed (the pages the
// compressor holds in physical memory, matching Activity Monitor's
// "Memory Used" breakdown), Used = the three summed; Cached Files =
// external + purgeable.
func memoryFromVM(totalBytes, page uint64, vm vmStats64, pressure, swapUsed, swapTotal uint64) MemoryStatus {
	if totalBytes == 0 || page == 0 {
		return MemoryStatus{}
	}
	app := uint64(vm.InternalPages - vm.Purgeable)
	mem := MemoryStatus{
		Total:      totalBytes,
		App:        app * page,
		Wired:      uint64(vm.Wire) * page,
		Compressed: uint64(vm.CompressorPages) * page,
		Pressure:   pressure,
		SwapUsed:   swapUsed,
		SwapTotal:  swapTotal,
	}
	mem.Used = mem.App + mem.Wired + mem.Compressed
	mem.Cached = (uint64(vm.ExternalPages) + uint64(vm.Purgeable)) * page
	return mem
}

// ---- parsing helpers (pure, table-tested) ---------------------------------

var bootTimeRe = regexp.MustCompile(`sec = (\d+)`)

// parseBootTime reads kern.boottime's "{ sec = 1728975540, usec = 0 } ...".
func parseBootTime(s string) int64 {
	m := bootTimeRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	v, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// bootTimeMax is unix seconds of 2100-01-01: kern.boottime is a struct
// timeval, so anything past that means the bytes were not a timeval.
const bootTimeMax = int64(4102444800)

// parseBootTimeBytes decodes kern.boottime's struct timeval. The seconds
// sit little-endian at offset 0, 8 bytes wide on 64-bit darwin (LP64);
// the sysctl command renders them as text, but the raw sysctl hands over
// the struct. Values outside the plausible unix range read as zero.
// (真机 verification P1-1: the text regex never matched the binary.)
func parseBootTimeBytes(b []byte) int64 {
	if len(b) < 8 {
		return 0
	}
	v := int64(binary.LittleEndian.Uint64(b[:8]))
	if v <= 0 || v > bootTimeMax {
		return 0
	}
	return v
}

// swapUnit converts "2048.00M" / "3.62G" to bytes, binary units.
func swapUnit(v string) uint64 {
	if len(v) == 0 {
		return 0
	}
	mult := uint64(1)
	switch v[len(v)-1] {
	case 'K':
		mult, v = 1024, v[:len(v)-1]
	case 'M':
		mult, v = 1024*1024, v[:len(v)-1]
	case 'G':
		mult, v = 1024*1024*1024, v[:len(v)-1]
	case 'T':
		mult, v = 1024*1024*1024*1024, v[:len(v)-1]
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		return 0
	}
	return uint64(f * float64(mult))
}

// parseSwapUsage reads "total = 2048.00M  used = 512.00M  free = ...",
// skipping the "=" separators the sysctl prints between name and value.
func parseSwapUsage(s string) (used, total uint64) {
	fields := strings.Fields(s)
	for i := 0; i+1 < len(fields); i++ {
		switch fields[i] {
		case "total", "used":
			j := i + 1
			if fields[j] == "=" {
				j++
			}
			if j >= len(fields) {
				continue
			}
			v := swapUnit(strings.TrimSuffix(fields[j], ","))
			if fields[i] == "total" {
				total = v
			} else {
				used = v
			}
		}
	}
	return used, total
}

// swapUsage mirrors XNU's struct xsw_usage (sys/sysctl.h), what the
// vm.swapusage sysctl hands over. It is CTLTYPE_STRUCT: the raw sysctl
// returns the struct, not the "total = 2048.00M" text the CLI renders —
// the text parser above reads zeros from those bytes (fix3, same class
// of bug as kern.boottime / vm.loadavg in fix2).
type swapUsage struct {
	Total     uint64 // xsu_total: total swap space in bytes
	Avail     uint64 // xsu_avail: swap space currently available
	Used      uint64 // xsu_used: swap space currently in use
	PageSize  uint32 // xsu_pagesize
	Encrypted bool   // xsu_encrypted
}

// parseSwapUsageBytes decodes the 32-byte xsw_usage: three little-endian
// uint64 (offsets 0/8/16), a uint32 pagesize at 24 and a boolean_t (int)
// at 28. The whole struct is required for the byte counts; a short or
// empty buffer reads as not-ok so the caller falls back to the text
// shape.
func parseSwapUsageBytes(b []byte) (swapUsage, bool) {
	var su swapUsage
	if len(b) < 24 { // the three uint64 are the whole byte story
		return su, false
	}
	su.Total = binary.LittleEndian.Uint64(b[0:8])
	su.Avail = binary.LittleEndian.Uint64(b[8:16])
	su.Used = binary.LittleEndian.Uint64(b[16:24])
	if len(b) >= 28 {
		su.PageSize = binary.LittleEndian.Uint32(b[24:28])
	}
	if len(b) >= 32 {
		su.Encrypted = int32(binary.LittleEndian.Uint32(b[28:32])) != 0
	}
	return su, true
}

// parseLoadAvg reads " 1.92 2.03 1.85 " into the 1/5/15-minute loads.
func parseLoadAvg(s string) (l1, l5, l15 float64) {
	fields := strings.Fields(s)
	vals := make([]float64, 0, 3)
	for _, f := range fields {
		if v, err := strconv.ParseFloat(f, 64); err == nil && len(vals) < 3 {
			vals = append(vals, v)
		}
	}
	if len(vals) > 0 {
		l1 = vals[0]
	}
	if len(vals) > 1 {
		l5 = vals[1]
	}
	if len(vals) > 2 {
		l15 = vals[2]
	}
	return
}

// loadAvgMax caps a sane load: anything past a million means the bytes
// were misread, not measured.
const loadAvgMax = 1e6

// loadScaleAt reads a candidate fscale: n bytes little-endian at off,
// zero when the buffer is too short. XNU's struct loadavg is three
// fixed-point ints plus the fscale they divide by; the long fscale lands
// at offset 16 (a 24-byte LP64 struct), while an int fscale or a packed
// struct puts it at 12. All three candidates are accepted, first valid
// one wins, so a layout change fails soft to zero instead of rendering
// garbage.
// (真机 verification P1-4: the text parser never matched the binary.)
func loadScaleAt(b []byte, off, n int) uint64 {
	if off+n > len(b) {
		return 0
	}
	if n == 4 {
		return uint64(binary.LittleEndian.Uint32(b[off : off+4]))
	}
	return binary.LittleEndian.Uint64(b[off : off+8])
}

// parseLoadAvgBytes decodes vm.loadavg's struct loadavg: the 1/5/15-min
// loads as little-endian int32 at offsets 0/4/8, divided by the fscale.
func parseLoadAvgBytes(b []byte) (l1, l5, l15 float64) {
	if len(b) < 12 {
		return
	}
	ld := [3]float64{
		float64(int32(binary.LittleEndian.Uint32(b[0:4]))),
		float64(int32(binary.LittleEndian.Uint32(b[4:8]))),
		float64(int32(binary.LittleEndian.Uint32(b[8:12]))),
	}
	for _, scale := range []uint64{
		loadScaleAt(b, 16, 8), // long fscale, padded LP64 struct
		loadScaleAt(b, 12, 4), // int fscale, packed struct
		loadScaleAt(b, 12, 8), // long fscale, packed struct
	} {
		if scale == 0 {
			continue
		}
		l1, l5, l15 = ld[0]/float64(scale), ld[1]/float64(scale), ld[2]/float64(scale)
		if l1 >= 0 && l1 <= loadAvgMax && l5 >= 0 && l5 <= loadAvgMax && l15 >= 0 && l15 <= loadAvgMax {
			return
		}
	}
	return 0, 0, 0
}

// readLoadAvg is the wiring: vm.loadavg is a fixed-point struct, not
// text, so the binary parse runs first; the text parse only remains as a
// fallback for the sysctl CLI's rendered shape, used when the raw read
// itself failed.
func readLoadAvg() (l1, l5, l15 float64) {
	if raw := nativeSysctlRaw("vm.loadavg"); len(raw) > 0 {
		return parseLoadAvgBytes(raw)
	}
	return parseLoadAvg(nativeSysctlString("vm.loadavg"))
}

// parseBool reads system_profiler's mixed bool spellings.
func parseBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "yes", "true", "1":
			return true
		}
	case float64:
		return x != 0
	}
	return false
}

// parseInt is parseBool's number counterpart. macOS 26 spells the
// battery health capacity "83%", so a trailing percent reads as the
// number itself.
func parseInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		if n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(x), "%")); err == nil {
			return n
		}
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

// batteryStr reads a trimmed string field.
func batteryStr(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// firstVal returns the first present, non-nil value among the keys, so
// both the short (older) and the macOS 26 sppower_battery_* spellings
// can be probed in one call.
func firstVal(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

// powerScan collects the battery-relevant blocks of one system_profiler
// JSON, whatever their nesting, so parseBatteryJSON can apply them in a
// fixed order. macOS 26 renamed the fields inside the charge block to
// full sppower_battery_* names and moved the state of charge out of its
// nested map, while the health moved into sppower_battery_health_info
// and the AC state into sppower_ac_charger_information / the AC Power
// block (真机 verification P1-5); both shapes are collected here.
type powerScan struct {
	charge  map[string]any // sppower_battery_charge_info
	health  map[string]any // sppower_battery_health / sppower_battery_health_info
	acPower map[string]any // sppower_ac_power
	charger map[string]any // sppower_ac_charger_information
	acOn    bool           // "AC Power"."Current Power Source"
	cycles  int            // cycle_count / sppower_battery_cycle_count
}

func (s *powerScan) walk(v any) {
	switch x := v.(type) {
	case map[string]any:
		if m, ok := x["sppower_battery_charge_info"].(map[string]any); ok {
			s.charge = m
		}
		if m, ok := x["sppower_battery_health"].(map[string]any); ok {
			s.health = m
		}
		if m, ok := x["sppower_battery_health_info"].(map[string]any); ok {
			s.health = m
		}
		if m, ok := x["sppower_ac_power"].(map[string]any); ok {
			s.acPower = m
		}
		if m, ok := x["sppower_ac_charger_information"].(map[string]any); ok {
			s.charger = m
		}
		if ps, ok := x["AC Power"].(map[string]any); ok && parseBool(ps["Current Power Source"]) {
			s.acOn = true
		}
		if c := parseInt(firstVal(x, "cycle_count", "sppower_battery_cycle_count")); c > s.cycles {
			s.cycles = c
		}
		for _, val := range x {
			s.walk(val)
		}
	case []any:
		for _, item := range x {
			s.walk(item)
		}
	}
}

// readFlatBattery is the fallback for older macOS builds that flatten the
// fields straight into the items: first value per key wins, keys not
// paths, because nesting differs across versions.
func readFlatBattery(b *BatteryStatus, seen map[string]bool, v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if !seen[k] {
				seen[k] = true
				switch k {
				case "soc_percent", "current_capacity":
					if b.Percent == 0 {
						b.Percent = parseInt(val)
					}
				case "charging":
					b.Charging = parseBool(val)
				case "fully_charged":
					b.Full = parseBool(val)
				case "connected":
					b.Plugged = parseBool(val)
				case "cycle_count":
					if b.Cycles == 0 {
						b.Cycles = parseInt(val)
					}
				case "maximum_capacity", "max_capacity":
					if b.MaxCapacity == 0 {
						b.MaxCapacity = parseInt(val)
					}
				case "condition":
					if b.Condition == "" {
						b.Condition = batteryStr(val)
					}
				}
			}
			readFlatBattery(b, seen, val)
		}
	case []any:
		for _, item := range x {
			readFlatBattery(b, seen, item)
		}
	}
}

// parseBatteryJSON reads system_profiler's SPPowerDataType JSON. The
// scan collects the charge, health and power blocks wherever they sit,
// then they are applied in a fixed order — map iteration cannot change
// the result, and battery health wins for maximum capacity because the
// model info block reuses the same key for the design capacity in mAh.
// When no block was found the flat key-based pass runs for older macOS
// shapes. A machine without a battery yields nil; missing fields stay at
// their zero values and render as dashes.
func parseBatteryJSON(data []byte) *BatteryStatus {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}
	b := &BatteryStatus{}
	var scan powerScan
	scan.walk(root)
	if scan.charge != nil {
		b.Full = parseBool(firstVal(scan.charge, "fully_charged", "sppower_battery_fully_charged"))
		b.Charging = parseBool(firstVal(scan.charge, "charging", "sppower_battery_is_charging"))
		if soc, ok := scan.charge["state_of_charge"].(map[string]any); ok {
			if p := parseInt(soc["soc_percent"]); p > 0 {
				b.Percent = p
			} else {
				b.Percent = parseInt(soc["current_capacity"])
			}
			b.MaxCapacity = parseInt(soc["max_capacity"])
		}
		// macOS 26 keeps the state of charge as a direct value.
		if p := parseInt(scan.charge["sppower_battery_state_of_charge"]); p > 0 {
			b.Percent = p
		}
	}
	if scan.health != nil {
		b.Condition = batteryStr(firstVal(scan.health, "condition", "sppower_battery_health"))
		if m := parseInt(firstVal(scan.health, "maximum_capacity", "sppower_battery_health_maximum_capacity")); m > 0 {
			b.MaxCapacity = m
		}
		if c := parseInt(firstVal(scan.health, "cycle_count", "sppower_battery_cycle_count")); c > 0 {
			scan.cycles = c
		}
	}
	b.Cycles = scan.cycles
	if scan.acPower != nil {
		b.Plugged = parseBool(scan.acPower["connected"])
	}
	if scan.charger != nil {
		b.Plugged = parseBool(scan.charger["sppower_battery_charger_connected"])
	}
	if scan.acOn {
		b.Plugged = true
	}
	if scan.charge == nil && scan.health == nil {
		readFlatBattery(b, map[string]bool{}, root)
	}
	// No battery installed, or a UPS report only: nothing to show.
	if b.Percent == 0 && b.Cycles == 0 && b.MaxCapacity == 0 && !b.Plugged && !b.Charging {
		return nil
	}
	if b.Percent > 100 { // "percent" picked up something else, e.g. watts
		return nil
	}
	return b
}

// ---- per-app attribution --------------------------------------------------

// outermostBundle returns the outermost .app directory containing path:
// "/Applications/Foo.app/Contents/MacOS/Foo" → "/Applications/Foo.app".
// Nested bundles (Foo.app/Bar.app/...) belong to the outer one. The match
// needs ".app/" so "Arcade.app.helper" does not count.
func outermostBundle(path string) string {
	idx := strings.Index(path, ".app"+string(filepath.Separator))
	if idx <= 0 {
		return ""
	}
	if !strings.Contains(path[:idx], string(filepath.Separator)) {
		return "" // "Foo.app/..." without a directory is not a bundle path
	}
	return path[:idx+4]
}

// procGroupKey decides which app a process belongs to:
//  1. its own outermost .app;
//  2. its responsible process's outermost .app (or executable name);
//  3. its executable file name.
func procGroupKey(r procReading, byPid map[int32]procReading, responsible func(int32) int32) (key, bundle string) {
	if b := outermostBundle(r.Path); b != "" {
		return "app:" + strings.ToLower(b), b
	}
	if rpid := responsible(r.Pid); rpid > 0 && rpid != r.Pid {
		if rp, ok := byPid[rpid]; ok {
			if b := outermostBundle(rp.Path); b != "" {
				return "app:" + strings.ToLower(b), b
			}
			return "exec:" + filepath.Base(rp.Path), ""
		}
	}
	return "exec:" + filepath.Base(r.Path), ""
}

// displayName picks the row's name: the localized name of the running Dock
// app at that bundle, else the .app folder name, else the executable name.
func displayName(key, bundle string, apps []workspaceApp, procs []procReading) string {
	if bundle != "" {
		lower := strings.ToLower(bundle)
		for _, a := range apps {
			if a.Path != "" && strings.ToLower(a.Path) == lower && a.Name != "" {
				return a.Name
			}
		}
		return strings.TrimSuffix(filepath.Base(bundle), ".app")
	}
	for _, p := range procs {
		if "exec:"+filepath.Base(p.Path) == key {
			return filepath.Base(p.Path)
		}
	}
	if i := strings.Index(key, "exec:"); i == 0 {
		return key[len("exec:"):]
	}
	return key
}

// canQuit reports whether the group has a Dock app that may be terminated:
// regular activation policy, bundle equal to or inside the group's .app,
// and neither Finder nor Cleanzo itself.
func canQuit(bundle string, apps []workspaceApp) bool {
	if bundle == "" {
		return false
	}
	lower := strings.ToLower(bundle)
	for _, a := range apps {
		if !a.Regular || a.Path == "" {
			continue
		}
		if a.BundleID == "com.apple.finder" || a.BundleID == "com.cleanzo.app" {
			continue
		}
		ap := strings.ToLower(a.Path)
		if within(ap, lower) {
			return true
		}
	}
	return false
}

// appGroup accumulates one row while merging.
type appGroup struct {
	key    string
	bundle string
	memory uint64
	cpuNs  uint64
	procs  int
}

// mergeApps attributes every sampled process to an app row and computes
// per-app CPU from the previous sample. New or reused pids get no CPU that
// round. The result holds the memory top 8 union the CPU top 8, deduped.
func mergeApps(prev map[int32]procSample, procs []procReading, apps []workspaceApp, responsible func(int32) int32, elapsed time.Duration) []AppUsage {
	byPid := make(map[int32]procReading, len(procs))
	for _, r := range procs {
		byPid[r.Pid] = r
	}
	groups := map[string]*appGroup{}
	order := []string{}
	for _, r := range procs {
		key, bundle := procGroupKey(r, byPid, responsible)
		g := groups[key]
		if g == nil {
			g = &appGroup{key: key, bundle: bundle}
			groups[key] = g
			order = append(order, key)
		}
		g.memory += r.Footprint
		g.procs++
		p, ok := prev[r.Pid]
		if ok && p.Start == r.Start && elapsed > 0 {
			g.cpuNs += (r.UserNs - p.UserNs) + (r.SysNs - p.SysNs)
		}
	}
	rows := make([]AppUsage, 0, len(groups))
	for _, key := range order {
		g := groups[key]
		cpu := 0.0
		if elapsed > 0 {
			cpu = float64(g.cpuNs) / float64(elapsed) * 100
			if cpu < 0 {
				cpu = 0 // counters can jitter backwards on sample edges
			}
		}
		rows = append(rows, AppUsage{
			Key:    key,
			Name:   displayName(key, g.bundle, apps, procs),
			Path:   g.bundle,
			Memory: g.memory,
			CPU:    cpu,
			Procs:  g.procs,
			Quit:   canQuit(g.bundle, apps),
		})
	}
	// Memory top 8 ∪ CPU top 8, deduped, memory-sorted as the base order.
	byMemory := append([]AppUsage(nil), rows...)
	sort.Slice(byMemory, func(i, j int) bool {
		if byMemory[i].Memory != byMemory[j].Memory {
			return byMemory[i].Memory > byMemory[j].Memory
		}
		return byMemory[i].Key < byMemory[j].Key
	})
	if len(byMemory) > 8 {
		byMemory = byMemory[:8]
	}
	byCPU := append([]AppUsage(nil), rows...)
	sort.Slice(byCPU, func(i, j int) bool {
		if byCPU[i].CPU != byCPU[j].CPU {
			return byCPU[i].CPU > byCPU[j].CPU
		}
		return byCPU[i].Key < byCPU[j].Key
	})
	if len(byCPU) > 8 {
		byCPU = byCPU[:8]
	}
	merged := map[string]AppUsage{}
	for _, r := range byMemory {
		merged[r.Key] = r
	}
	for _, r := range byCPU {
		merged[r.Key] = r
	}
	out := make([]AppUsage, 0, len(merged))
	for _, r := range merged {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Memory != out[j].Memory {
			return out[i].Memory > out[j].Memory
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// ---- the sampling loop ----------------------------------------------------

// cpuTicks is one overall-CPU reading: cumulative busy (user+system+nice)
// and total ticks since boot.
type cpuTicks struct{ Busy, Total uint64 }

// WatchStatus streams one StatusSample every two seconds while the status
// page is open. The first sample waits for the CPU's second reading (so
// usage is real, not a since-boot average) and for the first battery read,
// three seconds at most; afterwards battery refreshes once a minute in the
// background and rides along with each sample. Cancel the context (the
// frontend closes the channel) to stop.
func (Cleaner) WatchStatus(ctx context.Context, ch *mygo.Channel[StatusSample]) error {
	return watchStatusWith(ctx, ch.Send, realStatusSource())
}

// watchStatusWith runs the loop against an injected sender, so tests drive
// it without the real channel.
func watchStatusWith(ctx context.Context, send func(StatusSample) error, src statusSource) error {
	// The first battery read shares the three-second budget.
	batCtx, cancelBat := context.WithTimeout(ctx, statusFirstBudget)
	defer cancelBat()
	batDone := make(chan *BatteryStatus, 1)
	go func() {
		b := src.battery(batCtx)
		select {
		case batDone <- b:
		case <-ctx.Done():
		}
	}()

	// The previous snapshots are taken before the half-second wait, so the
	// first sample's usage and per-app CPU are measured over a real 500ms
	// interval, not two adjacent reads.
	prevProcs := readProcState(src)
	prevCPU := readCPUTicks(src)
	prevAt := time.Now()
	if err := sleepCtx(ctx, statusFirstDelay); err != nil {
		return err
	}

	batTimer := time.NewTimer(statusBatteryEvery)
	defer batTimer.Stop()
	var latest *BatteryStatus

	tick := time.NewTicker(statusInterval)
	defer tick.Stop()
	for first := true; ; first = false {
		elapsed := time.Since(prevAt)
		curProcs := readProcState(src)
		curCPU := readCPUTicks(src)
		prevAt = time.Now()
		sample := buildSample(prevProcs, curProcs, prevCPU, curCPU, src, elapsed)
		sample.Battery = latest

		// A battery result that arrived while sampling is used next round;
		// drain what is here now so the first sample has it if it won the
		// budget race.
		waited := false
		if first {
			// The first sample may still wait the rest of the budget.
			deadline := time.NewTimer(time.Until(deadlineOf(batCtx)))
			select {
			case b := <-batDone:
				latest = b
				sample.Battery = b
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
			}
			waited = true
		} else {
			select {
			case b := <-batDone:
				latest = b
				sample.Battery = b
			default:
			}
		}
		_ = waited

		if err := send(sample); err != nil {
			return err
		}
		prevProcs, prevCPU = curProcs, curCPU

		select {
		case <-ctx.Done():
			return ctx.Err()
		case b := <-batDone: // a late result waits for the next round
			latest = b
		case <-batTimer.C: // refresh in the background, never blocking sampling
			go func() {
				b := src.battery(ctx)
				select {
				case batDone <- b:
				case <-ctx.Done():
				}
			}()
			batTimer.Reset(statusBatteryEvery)
		case <-tick.C:
		}
	}
}

func readProcState(src statusSource) map[int32]procSample {
	out := map[int32]procSample{}
	for _, r := range src.procs() {
		out[r.Pid] = procSample{Pid: r.Pid, Path: r.Path, Memory: r.Footprint, UserNs: r.UserNs, SysNs: r.SysNs, Start: r.Start}
	}
	return out
}

func readCPUTicks(src statusSource) cpuTicks {
	busy, total := src.cpu()
	return cpuTicks{Busy: busy, Total: total}
}

// buildSample assembles one reading from the fresh process and tick
// snapshots and the previous ones.
func buildSample(prevProcs, curProcs map[int32]procSample, prevCPU, curCPU cpuTicks, src statusSource, elapsed time.Duration) StatusSample {
	curReadings := make([]procReading, 0, len(curProcs))
	for pid, p := range curProcs {
		curReadings = append(curReadings, procReading{Pid: pid, Path: p.Path, Footprint: p.Memory, UserNs: p.UserNs, SysNs: p.SysNs, Start: p.Start})
	}
	sample := StatusSample{
		Memory: src.memory(),
		Disk:   src.disk(),
		Apps:   mergeApps(prevProcs, curReadings, src.apps(), nativeResponsiblePid, elapsed),
		Procs:  len(curProcs),
	}
	if d := curCPU.Total - prevCPU.Total; d > 0 && prevCPU.Total > 0 {
		sample.CPU.Usage = clampPct(float64(curCPU.Busy-prevCPU.Busy) / float64(d) * 100)
	}
	l1, l5, l15 := readLoadAvg()
	sample.CPU.Load1, sample.CPU.Load5, sample.CPU.Load15 = l1, l5, l15
	return sample
}

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func deadlineOf(ctx context.Context) time.Time {
	d, ok := ctx.Deadline()
	if !ok {
		return time.Now().Add(statusFirstBudget)
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ---- the graceful quit protocol -------------------------------------------

// QuitApp asks the apps at path to terminate, exactly like choosing Quit
// in the Dock: it re-checks the running list at call time, never force
// kills, excludes Finder and Cleanzo itself, and waits up to five seconds
// for them to finish. Returns true when they are all gone. (false, nil)
// means the quit was sent but an app was still running after the wait
// (typically an unsaved-work dialog); the errors carry not-running and
// cannot-quit, the latter when terminate was refused outright.
func (Cleaner) QuitApp(path string) (bool, error) {
	return quitAppWith(path, workspaceList, workspaceTerminate)
}

func quitAppWith(path string, list func() []workspaceApp, terminate func(int32) bool) (bool, error) {
	matches := quitMatches(path, list())
	if len(matches) == 0 {
		return false, codeError{Code: "not-running"}
	}
	sent := false
	for _, a := range matches {
		if terminate(a.Pid) {
			sent = true
		}
	}
	deadline := time.Now().Add(statusQuitTimeout)
	for time.Now().Before(deadline) {
		if len(quitMatches(path, list())) == 0 {
			return true, nil
		}
		time.Sleep(statusQuitPoll)
	}
	if !sent {
		// terminate was refused outright, not merely slow: an error.
		return false, codeError{Code: "cannot-quit"}
	}
	// Sent, but still running after the wait; the frontend reports it as
	// "hasn't quit yet" — likely waiting on unsaved work.
	return false, nil
}

// quitMatches finds the Dock apps to terminate: bundle equal to path or
// inside it, excluding Finder and Cleanzo itself. Arc.app does not match
// Arcade.app because the prefix check keeps the "/" boundary.
func quitMatches(path string, apps []workspaceApp) []workspaceApp {
	if path == "" {
		return nil
	}
	lower := strings.ToLower(strings.TrimRight(path, string(filepath.Separator)))
	var out []workspaceApp
	for _, a := range apps {
		if !a.Regular || a.Path == "" {
			continue
		}
		if a.BundleID == "com.apple.finder" || a.BundleID == "com.cleanzo.app" {
			continue
		}
		ap := strings.ToLower(strings.TrimRight(a.Path, string(filepath.Separator)))
		if within(ap, lower) {
			out = append(out, a)
		}
	}
	return out
}

var _ = fmt.Sprintf
