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
