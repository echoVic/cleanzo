package main

import "testing"

// TestRecycleFits catches permanent-delete mode and inclusive MiB capacity limits.
func TestRecycleFits(t *testing.T) {
	for _, tc := range []struct {
		name     string
		size     int64
		nuke     uint32
		capacity uint32
		known    bool
		want     bool
	}{
		{"nuke", 123, 1, 100, true, false},
		{"above", 4 << 20, 0, 1, true, false},
		{"at", 1 << 20, 0, 1, true, false},
		{"below", (1 << 20) - 1, 0, 1, true, true},
		{"unknown", 4 << 20, 0, 0, false, true},
		{"unknown values ignored", 4 << 20, 1, 1, false, true},
		{"zero capacity", 123, 0, 0, true, false},
		{"empty at zero capacity", 0, 0, 0, true, false},
		{"empty", 0, 0, 1, true, true},
		{"unmeasured", -1, 0, 1, true, false},
		{"large DWORD capacity", 4 << 30, 0, ^uint32(0), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := recycleFits(tc.size, tc.nuke, tc.capacity, tc.known); got != tc.want {
				t.Fatalf("recycleFits(%d, %d, %d, %v) = %v, want %v", tc.size, tc.nuke, tc.capacity, tc.known, got, tc.want)
			}
		})
	}
}
