package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A child of system_profiler that keeps its output open, while it runs past
// the read's timeout or after it exits, does not hold the read. Output it
// printed in full before exiting is still read.
func TestStatusBatteryEndsWithAChildHoldingItsOutput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		script  func(json string) string
		percent int // 0: no reading
	}{
		// exec: the leader is sleep itself, which the deadline kills.
		{"runs past the timeout", func(string) string { return "exec sleep 30" }, 0},
		{"exits", func(json string) string { return "cat " + shellPath(json) }, 84},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			pidFile, json := filepath.Join(bin, "child"), filepath.Join(bin, "battery.json")
			writeText(t, json, batteryJSON)
			fakeTool(t, bin, "system_profiler", `sleep 30 &
echo $! > `+shellPath(pidFile)+`
`+tc.script(json))
			saved := batteryProfiler
			batteryProfiler = filepath.Join(bin, "system_profiler")
			t.Cleanup(func() { batteryProfiler = saved })
			t.Cleanup(func() {
				var pid int
				if data, err := os.ReadFile(pidFile); err == nil {
					if _, err := fmt.Sscan(string(data), &pid); err == nil && pid > 0 {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan *BatteryStatus, 1)
			go func() { done <- statusBattery(ctx) }()
			select {
			case b := <-done:
				if tc.percent == 0 && b != nil {
					t.Fatalf("battery = %+v, want none", b)
				}
				if tc.percent != 0 && (b == nil || b.Percent != tc.percent) {
					t.Fatalf("battery = %+v, want %d%%", b, tc.percent)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the battery read outlived its timeout")
			}
		})
	}
}
