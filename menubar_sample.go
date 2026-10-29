package main

// menubar_sample.go reads what the menu bar readings show, every two seconds,
// while any shows (menubar spec §三.1, §六). It reads through menuBarSource,
// so the tests run on any platform; menubar_darwin.go wires the native reads.

import (
	"context"
	"slices"
	"time"
)

// menuBarSource is what the readings read. machine is opened only while the
// GPU, the network or the temperature shows.
type menuBarSource struct {
	cpu     func() (busy, total uint64) // cumulative ticks
	memory  func() MemoryStatus
	disk    func() DiskStat
	machine func() menuBarMachine // nil when it cannot be opened
	battery func() int            // % charge, -1 unknown
}

// menuBarMachine reads the live readings the menu bar can show; on macOS
// *machineReader has it.
type menuBarMachine interface {
	readSome(now time.Time, gpu, net, temp bool) *MachineSample
	close()
}

// Timing knobs, variables so tests can shrink them.
var (
	menuBarFirst = time.Second     // the first reading's interval, for the CPU and the rates
	menuBarEvery = 2 * time.Second // spec §三.1
)

// sampleMenuBar reads what items show, the first time after menuBarFirst and
// then every menuBarEvery, and hands each reading to show, until ctx ends. It
// reads only what the shown readings need (spec §六): no process, no child
// process. show runs on the sampler's goroutine.
func sampleMenuBar(ctx context.Context, items []string, src menuBarSource, show func(menuBarReading)) {
	has := func(item string) bool { return slices.Contains(items, item) }
	gpu, net, temp := has("gpu"), has("net"), has("temp")
	var machine menuBarMachine
	if (gpu || net || temp) && src.machine != nil {
		if machine = src.machine(); machine != nil {
			defer machine.close()
			machine.readSome(time.Now(), gpu, net, temp) // starts the rates' counters
		}
	}
	var prevBusy, prevTotal uint64
	if has("cpu") {
		prevBusy, prevTotal = src.cpu()
	}
	if sleepCtx(ctx, menuBarFirst) != nil {
		return
	}
	tick := time.NewTicker(menuBarEvery)
	defer tick.Stop()
	for {
		r := unknownMenuBarReading()
		if has("cpu") {
			busy, total := src.cpu()
			if total > prevTotal && prevTotal > 0 && busy >= prevBusy {
				r.CPU = clampPct(float64(busy-prevBusy) / float64(total-prevTotal) * 100)
			}
			prevBusy, prevTotal = busy, total
		}
		if has("mem") {
			m := src.memory()
			r.MemUsed, r.MemTotal, r.Pressure = m.Used, m.Total, m.Pressure
		}
		if has("disk") {
			r.Free = src.disk().Free
		}
		if machine != nil {
			s := machine.readSome(time.Now(), gpu, net, temp)
			r.GPU, r.NetDown, r.NetUp, r.ChipTemp = s.GPU, s.NetDown, s.NetUp, s.ChipTemp
		}
		if has("battery") {
			r.Battery = src.battery()
		}
		if ctx.Err() != nil {
			return
		}
		show(r)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if ctx.Err() != nil { // both were ready and select picked the tick
				return
			}
		}
	}
}
