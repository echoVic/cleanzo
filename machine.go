package main

// machine.go is the home page's machine side (design:
// docs/superpowers/specs/2026-10-28-home-machine-design.md).
// MachineInfo says what this Mac is; the page reads it when it opens and
// again when the displays change. MachineSample is the live side, taken
// with each status sample. The reads are in machine_darwin.go; the rules
// here are pure, so they test on any platform.

import (
	"encoding/binary"
	"slices"
	"strconv"
	"strings"
	"time"
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
	SSD        *SSDInfo      `json:"ssd"`        // nil: nothing about the internal drive could be read
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
