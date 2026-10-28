package main

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"
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
