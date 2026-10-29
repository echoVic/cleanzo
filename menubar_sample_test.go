package main

import (
	"context"
	"slices"
	"testing"
	"time"
)

// fastMenuBar shrinks the sampler's waits for the test.
func fastMenuBar(t *testing.T) {
	t.Helper()
	first, every := menuBarFirst, menuBarEvery
	t.Cleanup(func() { menuBarFirst, menuBarEvery = first, every })
	menuBarFirst, menuBarEvery = time.Millisecond, time.Millisecond
}

// fakeMenuBarMachine records what it is asked to read.
type fakeMenuBarMachine struct {
	asks   [][3]bool
	closed int
}

func (f *fakeMenuBarMachine) readSome(_ time.Time, gpu, net, temp bool) *MachineSample {
	f.asks = append(f.asks, [3]bool{gpu, net, temp})
	s := newMachineSample()
	if gpu {
		s.GPU = 14
	}
	if net {
		s.NetDown, s.NetUp = 156_000, 55_000
	}
	if temp {
		s.ChipTemp = 43
	}
	return s
}

func (f *fakeMenuBarMachine) close() { f.closed++ }

// takeMenuBar runs the sampler until it has handed over n readings.
func takeMenuBar(t *testing.T, items []string, src menuBarSource, n int) []menuBarReading {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got []menuBarReading
	done := make(chan struct{})
	go func() {
		defer close(done)
		sampleMenuBar(ctx, items, src, func(r menuBarReading) {
			got = append(got, r)
			if len(got) == n {
				cancel()
			}
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sampler did not stop")
	}
	return got
}

func TestSampleMenuBarReadsOnlyWhatShows(t *testing.T) {
	fastMenuBar(t)
	var cpu, mem, disk, machine, battery int
	busy, total := uint64(100), uint64(1000)
	src := menuBarSource{
		cpu: func() (uint64, uint64) {
			cpu++
			busy, total = busy+30, total+100
			return busy, total
		},
		memory:  func() MemoryStatus { mem++; return MemoryStatus{Used: 24 << 30, Total: 32 << 30, Pressure: 2} },
		disk:    func() DiskStat { disk++; return DiskStat{Total: 500 << 30, Free: 26 << 30} },
		machine: func() menuBarMachine { machine++; return nil },
		battery: func() int { battery++; return 80 },
	}
	got := takeMenuBar(t, []string{"cpu", "mem"}, src, 3)
	if len(got) != 3 {
		t.Fatalf("%d readings, want 3", len(got))
	}
	r := got[0]
	if r.CPU != 30 || r.MemUsed != 24<<30 || r.MemTotal != 32<<30 || r.Pressure != 2 {
		t.Errorf("first reading %+v, want CPU 30%% and the memory", r)
	}
	if r.GPU != -1 || r.NetDown != -1 || r.NetUp != -1 || r.Free != 0 || r.DiskKnown || r.ChipTemp != 0 || r.Battery != -1 {
		t.Errorf("read what does not show: %+v", r)
	}
	if disk != 0 || machine != 0 || battery != 0 {
		t.Errorf("asked disk %d, machine %d, battery %d times; none shows", disk, machine, battery)
	}
	if cpu != 4 || mem != 3 {
		t.Errorf("asked the CPU %d and memory %d times, want 4 (one to start) and 3", cpu, mem)
	}
}

func TestSampleMenuBarOpensTheMachineOnlyForItsReadings(t *testing.T) {
	fastMenuBar(t)
	fake := &fakeMenuBarMachine{}
	opened := 0
	src := menuBarSource{
		cpu:     func() (uint64, uint64) { return 0, 0 },
		memory:  func() MemoryStatus { return MemoryStatus{} },
		disk:    func() DiskStat { return DiskStat{Total: 500 << 30, Free: 5 << 30} },
		machine: func() menuBarMachine { opened++; return fake },
		battery: func() int { return 55 },
	}
	got := takeMenuBar(t, []string{"disk", "temp", "battery"}, src, 2)
	if opened != 1 || fake.closed != 1 {
		t.Errorf("opened the machine %d times and closed it %d times, want once each", opened, fake.closed)
	}
	// one read to start the counters, then one per reading; only the temperature
	if len(fake.asks) != 3 || slices.ContainsFunc(fake.asks, func(a [3]bool) bool { return a != [3]bool{false, false, true} }) {
		t.Errorf("asked %v, want the temperature alone, three times", fake.asks)
	}
	if r := got[1]; r.ChipTemp != 43 || r.Free != 5<<30 || !r.DiskKnown || r.Battery != 55 || r.GPU != -1 || r.NetDown != -1 || r.CPU != -1 {
		t.Errorf("reading %+v", r)
	}
	opened = 0
	takeMenuBar(t, []string{"cpu", "mem", "disk", "battery"}, src, 1)
	if opened != 0 {
		t.Error("opened the machine with no GPU, network or temperature to show")
	}
}
