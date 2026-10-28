//go:build darwin

package main

import "testing"

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
