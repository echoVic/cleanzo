//go:build darwin

package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestReadMachineInfoLive(t *testing.T) {
	// NSScreen wants the main thread, which a test binary never serves; run
	// the closure on this goroutine instead, as TestWorkspaceListRuns does
	// for NSWorkspace.
	prev := runOnMain
	runOnMain = func(f func()) { f() }
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
	for _, d := range info.Displays {
		if d.Name == "" {
			t.Errorf("display %d has no name", d.ID)
		}
	}
	if s := info.SSD; s == nil || s.Model == "" || s.Capacity == 0 || !s.SMART || s.Written == 0 {
		t.Errorf("on a real Mac the SSD and its SMART log read: %+v", s)
	}
}

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
		if onThisMac() {
			t.Fatal("the interface list reads on a real Mac")
		}
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
		s.NetKind == "" || s.DiskTemp <= 0 || s.DisplaySig == "" || len(s.Fans) != s.FanCount {
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
	err := watchStatusWith(ctx, func(s StatusSample) error {
		samples++
		last = s.Machine
		_, err := json.Marshal(s)
		return err
	}, realStatusSource())
	used, wall := rusageCPU()-before, time.Since(start)
	pct := used.Seconds() / wall.Seconds() * 100
	t.Logf("%d samples, CPU %v in %v: %.2f%% of one core; last machine reading %+v",
		samples, used.Round(time.Millisecond), wall.Round(time.Second), pct, last)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the loop stopped early: %v", err)
	}
	if samples < 25 {
		t.Errorf("%d samples in 60 s, want about 30", samples)
	}
	if pct > 1 {
		t.Errorf("sampling used %.2f%% of one core, budget 1%%", pct)
	}
}

func TestNVMeSMARTLogSurvivesOverlappingReads(t *testing.T) {
	if !onThisMac() {
		t.Skip("needs the real drive: set CLEANZO_HW=1")
	}
	if !iokitInit() {
		t.Fatal("IOKit loads on a real Mac")
	}
	var wg sync.WaitGroup
	ok := make([]bool, 4)
	for i := range ok {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, ok[i] = nvmeSMARTLog()
		}(i)
	}
	wg.Wait()
	for i, read := range ok {
		if !read {
			t.Errorf("overlapping read %d lost the SMART log", i)
		}
	}
}
