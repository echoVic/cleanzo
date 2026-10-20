package main

// monitor_test.go covers the status page's rules with table-driven tests:
// attribution, CPU math, memory categories, battery parsing, the sampling
// loop with an injected source, and the graceful quit protocol. Everything
// here runs off macOS; the darwin-only smoke test sits at the bottom.

import (
	"context"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// ---- helpers --------------------------------------------------------------

func mustAppUsage(rows []AppUsage, key string) AppUsage {
	for _, r := range rows {
		if r.Key == key {
			return r
		}
	}
	return AppUsage{}
}

// ---- attribution ----------------------------------------------------------

func TestMergeAppsResponsibleUnavailable(t *testing.T) {
	// The private API missing: no responsibility info, plain exec merge.
	procs := []procReading{
		{Pid: 1, Path: "/usr/local/bin/node", Footprint: 10},
		{Pid: 2, Path: "/usr/local/bin/node", Footprint: 20},
	}
	rows := mergeApps(nil, procs, nil, func(int32) int32 { return -1 }, 0)
	if got := mustAppUsage(rows, "exec:node"); got.Memory != 30 || got.Procs != 2 {
		t.Errorf("exec merge = %+v, want memory 30 procs 2", got)
	}
}

// ---- CPU math -------------------------------------------------------------

func TestBuildSampleOverallCPU(t *testing.T) {
	src := statusSource{
		procs:  func() []procReading { return nil },
		apps:   func() []workspaceApp { return nil },
		memory: func() MemoryStatus { return MemoryStatus{} },
		cpu:    func() (uint64, uint64) { return 30, 100 }, // snapshot at build time
		disk:   func() DiskStat { return DiskStat{} },
	}
	prev, cur := cpuTicks{Busy: 0, Total: 100}, cpuTicks{Busy: 30, Total: 200}
	s := buildSample(nil, nil, prev, cur, src, 2*time.Second)
	if s.CPU.Usage != 30 { // (30-0)/(200-100) = 30% of all cores
		t.Errorf("usage = %v, want 30", s.CPU.Usage)
	}
}

// ---- memory categories ----------------------------------------------------

func TestMemoryFromVM(t *testing.T) {
	const page = 16384
	vm := vmStats64{Wire: 10, Purgeable: 2, InternalPages: 100, ExternalPages: 40, CompressorPages: 30}
	mem := memoryFromVM(200*page, page, vm, 2, 5<<30, 6<<30)
	wantApp := uint64(100-2) * page
	wantWired := uint64(10) * page
	wantCompressed := uint64(30) * page
	if mem.App != wantApp || mem.Wired != wantWired || mem.Compressed != wantCompressed {
		t.Errorf("categories: app=%d wired=%d compressed=%d", mem.App, mem.Wired, mem.Compressed)
	}
	if mem.Used != wantApp+wantWired+wantCompressed {
		t.Errorf("used = %d, want the three summed", mem.Used)
	}
	if mem.Cached != uint64(40+2)*page {
		t.Errorf("cached = %d, want external+purgeable", mem.Cached)
	}
	if mem.Used > mem.Total {
		t.Errorf("used %d exceeds hw.memsize %d — layout is wrong", mem.Used, mem.Total)
	}
	if mem.Pressure != 2 || mem.SwapUsed != 5<<30 || mem.SwapTotal != 6<<30 {
		t.Errorf("pressure/swap passthrough: %+v", mem)
	}
}

func TestMemoryFromVMZeroTotal(t *testing.T) {
	if memoryFromVM(0, 16384, vmStats64{}, 1, 0, 0) != (MemoryStatus{}) {
		t.Error("unreadable memory must come back all-zero so the page shows dashes")
	}
}

// ---- parsing --------------------------------------------------------------

func TestParseSwapUsage(t *testing.T) {
	used, total := parseSwapUsage("total = 2048.00M  used = 512.50M  free = 1535.50M  (encrypted)")
	if used != 512.5*1024*1024 || total != 2048*1024*1024 {
		t.Errorf("swap = %d/%d", used, total)
	}
	if u, _ := parseSwapUsage(""); u != 0 {
		t.Error("empty swap line must read zero")
	}
}

func TestParseSwapUsageBytes(t *testing.T) {
	// The schema itself, per XNU's struct xsw_usage (sys/sysctl.h) — the
	// bytes the vm.swapusage sysctl actually returns (CTLTYPE_STRUCT), not
	// a text fixture: xsu_total/avail/used are little-endian uint64 at
	// offsets 0/8/16, xsu_pagesize a uint32 at 24, xsu_encrypted a
	// boolean_t (4-byte int) at 28; 32 bytes total. The values are in the
	// verification Mac's ballpark (32 GiB swap, page size 16 KiB, swap
	// encrypted — the macOS default).
	b := make([]byte, 32)
	binary.LittleEndian.PutUint64(b[0:8], 34359738368) // total: 32 GiB
	binary.LittleEndian.PutUint64(b[8:16], 1073741824) // avail: 1 GiB
	binary.LittleEndian.PutUint64(b[16:24], 33285996544)
	binary.LittleEndian.PutUint32(b[24:28], 16384) // pagesize
	binary.LittleEndian.PutUint32(b[28:32], 1)     // encrypted
	su, ok := parseSwapUsageBytes(b)
	if !ok {
		t.Fatal("32-byte xsw_usage must parse")
	}
	if su.Total != 34359738368 || su.Avail != 1073741824 || su.Used != 33285996544 {
		t.Errorf("swap bytes = total %d avail %d used %d", su.Total, su.Avail, su.Used)
	}
	if su.PageSize != 16384 {
		t.Errorf("pagesize = %d, want 16384", su.PageSize)
	}
	if !su.Encrypted {
		t.Error("encrypted = false, want true")
	}
	// The memory card consumes used/total only, and used may exceed a
	// momentarily stale total on real machines (swap is dynamically
	// sized), so the parser passes them through unvalidated.
	// Short or empty buffers read as not-ok → the caller falls back.
	for _, bad := range [][]byte{nil, {}, make([]byte, 23), b[:16]} {
		if su, ok := parseSwapUsageBytes(bad); ok {
			t.Errorf("buffer of %d bytes must not parse", len(bad))
		} else if su != (swapUsage{}) {
			t.Errorf("failed parse must return the zero struct, got %+v", su)
		}
	}
	// 24 bytes (the three uint64) still count; pagesize/encrypted stay
	// zero when the tail is absent.
	su, ok = parseSwapUsageBytes(b[:24])
	if !ok || su.Total != 34359738368 || su.PageSize != 0 || su.Encrypted {
		t.Errorf("24-byte prefix = %+v ok=%v", su, ok)
	}
	// Unencrypted swaps read false.
	binary.LittleEndian.PutUint32(b[28:32], 0)
	if su, _ = parseSwapUsageBytes(b); su.Encrypted {
		t.Error("xsu_encrypted=0 must read false")
	}
}

func TestParseLoadAvg(t *testing.T) {
	l1, l5, l15 := parseLoadAvg(" 1.92 2.03 1.85 ")
	if l1 != 1.92 || l5 != 2.03 || l15 != 1.85 {
		t.Errorf("loads = %v %v %v", l1, l5, l15)
	}
}

func TestParseBootTime(t *testing.T) {
	if v := parseBootTime("{ sec = 1728975540, usec = 190631 } Thu Oct 17 09:39:00 2026"); v != 1728975540 {
		t.Errorf("bootTime = %d", v)
	}
	if v := parseBootTime(""); v != 0 {
		t.Errorf("unreadable boottime = %d", v)
	}
}

func TestParseBootTimeBytes(t *testing.T) {
	// kern.boottime is a struct timeval: little-endian seconds at offset
	// 0 (8 bytes on 64-bit darwin), microseconds after. The raw sysctl
	// hands over the struct, not the "{ sec = ... }" text the CLI prints.
	tv := make([]byte, 16)
	binary.LittleEndian.PutUint64(tv[0:8], 1728975540) // sec
	binary.LittleEndian.PutUint32(tv[8:12], 190631)    // usec
	if v := parseBootTimeBytes(tv); v != 1728975540 {
		t.Errorf("binary bootTime = %d, want 1728975540", v)
	}
	// Short buffer, garbage bytes and out-of-range values read as zero.
	garbage := make([]byte, 16)
	for i := range garbage {
		garbage[i] = 0xff
	}
	for _, b := range [][]byte{nil, []byte{1, 2, 3}, make([]byte, 16), garbage} {
		if v := parseBootTimeBytes(b); v != 0 {
			t.Errorf("invalid timeval % x read as %d, want 0", b, v)
		}
	}
}

func TestParseLoadAvgBytes(t *testing.T) {
	// vm.loadavg is a struct loadavg: three fixed-point int32 samples
	// (little-endian, offsets 0/4/8) plus the fscale they divide by. The
	// samples below are 9.45/11.85/12.68 scaled by 100, the loads the
	// verification Mac reported.
	ldavg := [3]uint32{945, 1185, 1268}
	// The LP64 struct: long fscale at offset 16 (24 bytes total).
	b := make([]byte, 24)
	for i, v := range ldavg {
		binary.LittleEndian.PutUint32(b[4*i:4*i+4], v)
	}
	binary.LittleEndian.PutUint64(b[16:24], 100)
	l1, l5, l15 := parseLoadAvgBytes(b)
	if math.Abs(l1-9.45) > 1e-6 || math.Abs(l5-11.85) > 1e-6 || math.Abs(l15-12.68) > 1e-6 {
		t.Errorf("LP64 loads = %v %v %v, want 9.45 11.85 12.68", l1, l5, l15)
	}
	// Packed 20-byte variant: long fscale right after the samples.
	p := make([]byte, 20)
	for i, v := range ldavg {
		binary.LittleEndian.PutUint32(p[4*i:4*i+4], v)
	}
	binary.LittleEndian.PutUint64(p[12:20], 100)
	l1, l5, l15 = parseLoadAvgBytes(p)
	if math.Abs(l1-9.45) > 1e-6 || math.Abs(l5-11.85) > 1e-6 || math.Abs(l15-12.68) > 1e-6 {
		t.Errorf("packed loads = %v %v %v, want 9.45 11.85 12.68", l1, l5, l15)
	}
	// Int32 fscale at offset 12, 16-byte struct.
	q := make([]byte, 16)
	for i, v := range ldavg {
		binary.LittleEndian.PutUint32(q[4*i:4*i+4], v)
	}
	binary.LittleEndian.PutUint32(q[12:16], 100)
	l1, l5, l15 = parseLoadAvgBytes(q)
	if math.Abs(l1-9.45) > 1e-6 || math.Abs(l5-11.85) > 1e-6 || math.Abs(l15-12.68) > 1e-6 {
		t.Errorf("int-scale loads = %v %v %v, want 9.45 11.85 12.68", l1, l5, l15)
	}
	// Short buffer and zero scale read as zero; garbage scale rejected.
	if l1, l5, l15 = parseLoadAvgBytes([]byte{1, 2, 3}); l1 != 0 || l5 != 0 || l15 != 0 {
		t.Errorf("short buffer = %v %v %v, want zeros", l1, l5, l15)
	}
	if l1, l5, l15 = parseLoadAvgBytes(make([]byte, 24)); l1 != 0 || l5 != 0 || l15 != 0 {
		t.Errorf("zero scale = %v %v %v, want zeros", l1, l5, l15)
	}
	g := make([]byte, 24)
	for i := range ldavg {
		binary.LittleEndian.PutUint32(g[4*i:4*i+4], 0xffff_ffff) // -1 samples
	}
	binary.LittleEndian.PutUint64(g[16:24], 1)
	if l1, l5, l15 = parseLoadAvgBytes(g); l1 != 0 || l5 != 0 || l15 != 0 {
		t.Errorf("negative loads = %v %v %v, want zeros", l1, l5, l15)
	}
}

// ---- battery --------------------------------------------------------------

const batteryJSON = `{"SPPowerDataType":[{"_items":[{"_name":" Battery Information",
	"amperage":943,"voltage":12263,
	"sppower_battery_model_info":{"maximum_capacity":101},
	"sppower_battery_charge_info":{"fully_charged":"No","charging":"Yes",
		"state_of_charge":{"soc_percent":84,"max_capacity":83,"current_capacity":84}},
	"sppower_battery_health":{"condition":"Normal","maximum_capacity":83},
	"cycle_count":170},
	{"_name":"AC Attached","sppower_ac_power":{"charging":"No","connected":"Yes","watts":96}}]}]}`

func TestParseBatteryCharging(t *testing.T) {
	b := parseBatteryJSON([]byte(batteryJSON))
	if b == nil {
		t.Fatal("expected a battery")
	}
	if b.Percent != 84 {
		t.Errorf("percent = %d, want 84", b.Percent)
	}
	if !b.Charging || b.Full {
		t.Errorf("charging/full = %v/%v", b.Charging, b.Full)
	}
	if b.Cycles != 170 || b.MaxCapacity != 83 || b.Condition != "Normal" {
		t.Errorf("health = cycles %d cap %d condition %q", b.Cycles, b.MaxCapacity, b.Condition)
	}
}

func TestParseBatteryNoBattery(t *testing.T) {
	if parseBatteryJSON([]byte(`{"SPPowerDataType":[{"_items":[]}]}`)) != nil {
		t.Error("desktop Mac reports no battery")
	}
	if parseBatteryJSON([]byte(`not json`)) != nil {
		t.Error("unparseable output is no battery, not an error")
	}
}

func TestParseBatteryMissingFields(t *testing.T) {
	// Old macOS shapes: bools instead of strings, missing capacity.
	b := parseBatteryJSON([]byte(`{"SPPowerDataType":[{"_items":[
		{"current_capacity":91,"charging":true,"fully_charged":false,"connected":true,
		 "cycle_count":12,"condition":"Service Recommended"}]}]}`))
	if b == nil || b.Percent != 91 || !b.Charging || !b.Plugged {
		t.Errorf("bool-shaped battery parsed wrong: %+v", b)
	}
	if b.Condition != "Service Recommended" {
		t.Errorf("condition = %q", b.Condition)
	}
	if b.MaxCapacity != 0 {
		t.Errorf("missing maximum capacity stays zero → dash on the page")
	}
}

func TestParseBatteryFullState(t *testing.T) {
	b := parseBatteryJSON([]byte(`{"SPPowerDataType":[{"_items":[
		{"state_of_charge":{"soc_percent":100},"fully_charged":"Yes","charging":"No",
		 "connected":"Yes","cycle_count":312,"maximum_capacity":92,"condition":"Normal"}]}]}`))
	if b == nil || !b.Full || !b.Plugged || b.Percent != 100 || b.Cycles != 312 {
		t.Errorf("full-charged state: %+v", b)
	}
}

// realDeviceBatteryJSON is the scrubbed SPPowerDataType output of the
// verification Mac (macOS 26.6.2, Apple M2 Pro): the charge block spells
// its fields sppower_battery_*-long, the state of charge is a direct
// int, the health sits in sppower_battery_health_info with the capacity
// as "83%", and the AC state in the charger / AC Power blocks.
// 真机 verification P1-5.
const realDeviceBatteryJSON = `{
  "SPPowerDataType": [
    {
      "_name": "spbattery_information",
      "sppower_battery_charge_info": {
        "sppower_battery_at_warn_level": "FALSE",
        "sppower_battery_fully_charged": "TRUE",
        "sppower_battery_is_charging": "FALSE",
        "sppower_battery_state_of_charge": 100
      },
      "sppower_battery_health_info": {
        "sppower_battery_cycle_count": 171,
        "sppower_battery_health": "Good",
        "sppower_battery_health_maximum_capacity": "83%"
      },
      "sppower_battery_model_info": {
        "sppower_battery_cell_revision": "4014",
        "sppower_battery_device_name": "bq40z651",
        "sppower_battery_hardware_revision": "0100"
      }
    },
    {
      "_name": "sppower_information",
      "AC Power": {
        "Current Power Source": "TRUE",
        "Disk Sleep Timer": 10,
        "Display Sleep Timer": 30,
        "Hibernate Mode": 3,
        "LowPowerMode": "No",
        "PrioritizeNetworkReachabilityOverSleep": "No",
        "Sleep On Power Button": "Yes",
        "System Sleep Timer": 0,
        "Wake On LAN": "Yes"
      },
      "Battery Power": {
        "Disk Sleep Timer": 10,
        "Display Sleep Timer": 10,
        "Hibernate Mode": 3,
        "LowPowerMode": "No",
        "PrioritizeNetworkReachabilityOverSleep": "No",
        "ReduceBrightness": "Yes",
        "Sleep On Power Button": "Yes",
        "System Sleep Timer": 1,
        "Wake On LAN": "No"
      }
    },
    {
      "_name": "sppower_hwconfig_information",
      "sppower_ups_installed": "FALSE"
    },
    {
      "_name": "sppower_ac_charger_information",
      "sppower_ac_charger_family": "0xe000400a",
      "sppower_ac_charger_hardware_version": "1.0",
      "sppower_ac_charger_ID": "0x7019",
      "sppower_ac_charger_manufacturer": "Apple Inc.",
      "sppower_ac_charger_name": "140W USB-C Power Adapter",
      "sppower_ac_charger_watts": "140",
      "sppower_battery_charger_connected": "TRUE",
      "sppower_battery_is_charging": "FALSE"
    },
    {
      "_items": [
        {
          "_items": [
            {
              "appPID": 889,
              "eventtype": "wake",
              "scheduledby": "com.apple.alarm.user-invisible-com.apple.calaccessd.travelEngine.periodicRefreshTimer",
              "time": "2026-10-15T16:10:59Z",
              "UserVisible": false
            },
            {
              "appPID": 342,
              "eventtype": "wake",
              "scheduledby": "com.apple.alarm.user-invisible-com.apple.osanalytics.hardhighengagementtimer",
              "time": "2026-10-15T23:52:33Z",
              "UserVisible": false
            }
          ],
          "_name": "sppower_scheduled_events_info"
        }
      ],
      "_name": "sppower_events_info"
    }
  ]
}`

func TestParseBatteryRealDeviceJSON(t *testing.T) {
	// Regression on the real scrubbed output from the verification Mac:
	// before P1-5 none of the long field names matched, everything read
	// zero and the battery card never rendered.
	b := parseBatteryJSON([]byte(realDeviceBatteryJSON))
	if b == nil {
		t.Fatal("real-device JSON must parse to a battery")
	}
	if b.Percent != 100 {
		t.Errorf("percent = %d, want 100 (direct sppower_battery_state_of_charge)", b.Percent)
	}
	if !b.Full {
		t.Error("fully charged = false, want TRUE")
	}
	if b.Charging {
		t.Error("charging = true, want FALSE")
	}
	if !b.Plugged {
		t.Error("plugged = false, want charger connected / AC current source")
	}
	if b.Cycles != 171 {
		t.Errorf("cycles = %d, want 171", b.Cycles)
	}
	if b.MaxCapacity != 83 {
		t.Errorf("max capacity = %d, want 83 (reads \"83%%\" through the percent sign)", b.MaxCapacity)
	}
	if b.Condition != "Good" {
		t.Errorf("condition = %q, want Good", b.Condition)
	}
}

// ---- the sampling loop ----------------------------------------------------

func fakeSource() statusSource {
	ticks := 0
	return statusSource{
		procs: func() []procReading {
			return []procReading{{Pid: 1, Path: "/Applications/A.app/Contents/MacOS/a", Footprint: 42}}
		},
		apps: func() []workspaceApp { return nil },
		memory: func() MemoryStatus {
			return memoryFromVM(32<<30, 16384, vmStats64{InternalPages: 100, Wire: 10, TotalUncompressed: 5}, 1, 0, 0)
		},
		cpu: func() (uint64, uint64) { // a growing counter, like real ticks
			ticks++
			return uint64(100 * ticks), uint64(400 * ticks)
		},
		battery: func(ctx context.Context) *BatteryStatus {
			return &BatteryStatus{Percent: 88, Charging: true, Cycles: 12, MaxCapacity: 95, Condition: "Normal"}
		},
		disk:      func() DiskStat { return DiskStat{Total: 1, Used: 2, Free: 3} },
		terminate: func(int32) bool { return true },
	}
}

func TestWatchStatusFirstSample(t *testing.T) {
	oldDelay, oldBudget, oldInterval := statusFirstDelay, statusFirstBudget, statusInterval
	statusFirstDelay, statusFirstBudget, statusInterval = 10*time.Millisecond, time.Second, time.Hour
	defer func() { statusFirstDelay, statusFirstBudget, statusInterval = oldDelay, oldBudget, oldInterval }()

	ctx, cancel := context.WithCancel(context.Background())
	var s StatusSample
	arrived := make(chan struct{}, 1)
	stopped := make(chan struct{})
	defer func() { cancel(); <-stopped }()
	go func() {
		defer close(stopped)
		_ = watchStatusWith(ctx, func(v StatusSample) error {
			s = v
			select {
			case arrived <- struct{}{}:
			default:
			}
			return nil
		}, fakeSource())
	}()
	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("no sample arrived")
	}
	if s.Battery == nil || s.Battery.Percent != 88 {
		t.Errorf("first sample should include the battery: %+v", s.Battery)
	}
	if s.Apps[0].Memory != 42 {
		t.Errorf("apps not merged: %+v", s.Apps)
	}
	if s.Memory.Total != 32<<30 {
		t.Errorf("memory not read: %+v", s.Memory)
	}
	if s.CPU.Usage != 25 { // (100-0)/(400-0)
		t.Errorf("usage = %v, want 25", s.CPU.Usage)
	}
	cancel()
}

func TestWatchStatusCancelStops(t *testing.T) {
	oldDelay, oldInterval := statusFirstDelay, statusInterval
	statusFirstDelay, statusInterval = 5*time.Millisecond, 10*time.Millisecond
	defer func() { statusFirstDelay, statusInterval = oldDelay, oldInterval }()

	ctx, cancel := context.WithCancel(context.Background())
	arrived := make(chan struct{}, 1)
	done := make(chan error, 1)
	stopped := make(chan struct{})
	defer func() { cancel(); <-stopped }()
	go func() {
		defer close(stopped)
		done <- watchStatusWith(ctx, func(StatusSample) error {
			select {
			case arrived <- struct{}{}:
			default:
			}
			return nil
		}, fakeSource())
	}()
	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("no sample arrived")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("want context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel must stop the loop")
	}
}

// ---- the graceful quit protocol -------------------------------------------

func quitForTest(list func() []workspaceApp, terminate func(int32) bool) (bool, error) {
	oldTimeout, oldPoll := statusQuitTimeout, statusQuitPoll
	statusQuitTimeout, statusQuitPoll = 20*time.Millisecond, 2*time.Millisecond
	defer func() { statusQuitTimeout, statusQuitPoll = oldTimeout, oldPoll }()
	return quitAppWith("/Applications/Arc.app", list, terminate)
}

func TestQuitAppSuccess(t *testing.T) {
	running := []workspaceApp{{Pid: 7, Path: "/Applications/Arc.app", BundleID: "company.arc", Regular: true}}
	list := func() []workspaceApp {
		out := running
		running = nil // it quits before the first poll
		return out
	}
	ok, err := quitForTest(list, func(int32) bool { return true })
	if !ok || err != nil {
		t.Errorf("want (true, nil), got (%v, %v)", ok, err)
	}
}

func TestQuitAppNotRunning(t *testing.T) {
	_, err := quitForTest(func() []workspaceApp { return nil }, func(int32) bool { return true })
	ce, isCode := err.(codeError)
	if !isCode || ce.Code != "not-running" {
		t.Errorf("want codeError not-running, got %v", err)
	}
}

func TestQuitAppTimeoutStillRunning(t *testing.T) {
	running := []workspaceApp{{Pid: 7, Path: "/Applications/Arc.app", BundleID: "company.arc", Regular: true}}
	ok, err := quitForTest(func() []workspaceApp { return running }, func(int32) bool { return true })
	if ok || err != nil {
		t.Errorf("sent but still running after the wait must be (false, nil), got (%v, %v)", ok, err)
	}
}

func TestQuitAppRefused(t *testing.T) {
	running := []workspaceApp{{Pid: 7, Path: "/Applications/Arc.app", BundleID: "company.arc", Regular: true}}
	ok, err := quitForTest(func() []workspaceApp { return running }, func(int32) bool { return false })
	ce, isCode := err.(codeError)
	if ok || !isCode || ce.Code != "cannot-quit" {
		t.Errorf("terminate refused must be cannot-quit, got (%v, %v)", ok, err)
	}
}
