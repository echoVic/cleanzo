package main

import "testing"

// TestMemoryFromWindows checks byte categories, saturation and unknown readings on every host.
func TestMemoryFromWindows(t *testing.T) {
	for _, tt := range []struct {
		name                                      string
		total, avail, wired, cache, commit, limit uint64
		want                                      MemoryStatus
	}{
		{"normal", 1000, 400, 100, 200, 900, 1600, MemoryStatus{Total: 1000, Used: 600, App: 500, Wired: 100, Cached: 200, SwapTotal: 600, SwapUsed: 300, Pressure: 1}},
		{"no-pagefile", 1000, 400, 100, 200, 500, 1000, MemoryStatus{Total: 1000, Used: 600, App: 500, Wired: 100, Cached: 200, Pressure: 1}},
		{"underflow", 1000, 1200, 100, 0, 0, 0, MemoryStatus{Total: 1000, Wired: 100, Pressure: 1}},
		{"unknown", 0, 0, 0, 0, 0, 0, MemoryStatus{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := memoryFromWindows(tt.total, tt.avail, tt.wired, tt.cache, tt.commit, tt.limit)
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestMemoryFromWindowsPressure checks the strict boundaries and either source of pressure.
func TestMemoryFromWindowsPressure(t *testing.T) {
	for _, tt := range []struct{ avail, commit, limit, want uint64 }{
		{49, 0, 0, 4}, {50, 0, 0, 2}, {99, 0, 0, 2}, {100, 0, 0, 1},
		{500, 951, 1000, 4}, {500, 950, 1000, 2}, {500, 851, 1000, 2}, {500, 850, 1000, 1},
		{49, 850, 1000, 4}, {500, 1000, 0, 1},
	} {
		if got := memoryFromWindows(1000, tt.avail, 0, 0, tt.commit, tt.limit).Pressure; got != tt.want {
			t.Errorf("%+v: pressure=%d", tt, got)
		}
	}
}
