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
// the read's timeout or after it exits, does not hold the read.
func TestStatusBatteryEndsWithAChildHoldingItsOutput(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		{"runs past the timeout", "sleep 30"},
		{"exits", `echo '{"SPPowerDataType": []}'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			pidFile := filepath.Join(bin, "child")
			fakeTool(t, bin, "system_profiler", `sleep 30 &
echo $! > `+shellPath(pidFile)+`
`+tc.script)
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
				if b != nil {
					t.Fatalf("battery = %+v, want none", b)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the battery read outlived its timeout")
			}
		})
	}
}
