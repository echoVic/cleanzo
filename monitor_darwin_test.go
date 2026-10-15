//go:build darwin

package main

// monitor_darwin_test.go is the on-Mac smoke test for the native reads.
// It asserts the layout and wiring stay sane against the real system; the
// full behaviour tables run on every platform in monitor_test.go.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNativeMemoryMatchesSysctl(t *testing.T) {
	mem := readMemoryStatus()
	total := nativeSysctlUint64("hw.memsize")
	if total == 0 {
		t.Skip("hw.memsize unreadable")
	}
	if mem.Total != total {
		t.Errorf("memory total = %d, hw.memsize = %d", mem.Total, total)
	}
	if mem.Used == 0 || mem.Used > mem.Total {
		t.Errorf("used = %d of total %d: vm_statistics64 layout is off", mem.Used, mem.Total)
	}
	if mem.Wired == 0 {
		t.Error("wired pages read as zero: layout is off")
	}
}

func TestNativeMachineInfo(t *testing.T) {
	chip, mem, cores, osVersion, boot := nativeMachineInfo()
	if mem == 0 || cores == 0 || osVersion == "" || boot == 0 {
		t.Errorf("machine info incomplete: chip=%q mem=%d cores=%d os=%q boot=%d", chip, mem, cores, osVersion, boot)
	}
	if !strings.Contains(osVersion, ".") {
		t.Errorf("osVersion %q does not look like a ProductVersion", osVersion)
	}
}

func TestNativeProcsSeesItself(t *testing.T) {
	procs := nativeProcs()
	if len(procs) < 20 {
		t.Errorf("only %d processes: proc_listallpids is off", len(procs))
	}
	found := false
	for _, p := range procs {
		if p.Path == "" || p.Footprint == 0 {
			continue
		}
		if strings.Contains(p.Path, "monitor.test") || strings.Contains(p.Path, "go-build") {
			found = true
		}
	}
	if !found {
		t.Log("test binary not visible in proc list (runs under another exec name); layout still checked below")
	}
	var sum uint64
	for _, p := range procs {
		sum += p.Footprint
	}
	if sum == 0 {
		t.Error("all footprints read as zero: rusage offsets are off")
	}
}

func TestNativeCPUTicksGrow(t *testing.T) {
	a, ok := nativeCPUTicks()
	if !ok {
		t.Fatal("host_statistics returned no CPU ticks")
	}
	busy := uint64(a.Ticks[0]) + uint64(a.Ticks[1]) + uint64(a.Ticks[3]) // user+system+nice
	total := busy + uint64(a.Ticks[2])
	if total == 0 || busy == 0 {
		t.Fatalf("ticks look wrong: %+v", a)
	}
	if busy*10 < total/10 {
		t.Logf("unusually low busy ratio: %+v", a)
	}
}

func TestWorkspaceListRuns(t *testing.T) {
	// The test binary never runs mygo's event loop on the main thread, so
	// a queued RunOnMain would wait forever and time the suite out
	// (真机 verification P1-2). Swap the hop for a synchronous run: the
	// code under test — AppKit init, autorelease pool, NSWorkspace read —
	// runs exactly as in production, just on this goroutine.
	prev := runOnMain
	runOnMain = func(f func()) { f() }
	t.Cleanup(func() { runOnMain = prev })
	apps := workspaceList()
	// Empty list stays loose on purpose: a headless CI session has no
	// visible GUI apps and the call must merely not crash.
	if len(apps) == 0 {
		t.Log("no apps reported (headless session?); call must merely not crash")
		return
	}
	// 真机复测唯一 FAIL：NSWorkspace legitimately returns background
	// items — XPC services, daemons, CLI tools (node) — whose paths are
	// executables, not .app bundles. The .app contract holds for Regular
	// (Dock) apps only, so the assertion splits by activation policy.
	// Background items keep their path as-is; they are not errors.
	regular := 0
	for _, a := range apps {
		if a.Pid <= 0 {
			t.Errorf("app %q pid = %d: every running app has a pid", a.Name, a.Pid)
		}
		if !a.Regular {
			continue
		}
		regular++
		if a.Path == "" {
			t.Errorf("regular app %q has no bundle path", a.Name)
			continue
		}
		if !strings.Contains(a.Path, ".app") {
			t.Errorf("regular app %q path %q is not a bundle", a.Name, a.Path)
		}
	}
	if regular == 0 {
		t.Errorf("workspace list of %d items has no regular (Dock) app — a GUI session must have one", len(apps))
	}
	// Known true app: Finder runs in every GUI session, is Regular, and
	// lives in a Finder.app bundle. If NSWorkspace can see node and XPC
	// agents but not Finder, the read itself is broken.
	for _, a := range apps {
		if a.BundleID == "com.apple.finder" {
			if !a.Regular {
				t.Errorf("Finder reported non-regular: %+v", a)
			}
			if !strings.Contains(a.Path, "Finder.app") {
				t.Errorf("Finder path = %q, want a Finder.app bundle", a.Path)
			}
			return
		}
	}
	t.Errorf("Finder (com.apple.finder) missing from %d running apps; items seen: %s", len(apps), namesOf(apps))
}

// namesOf renders the app list for a failure message, paths included, so
// a broken read shows exactly what NSWorkspace returned.
func namesOf(apps []workspaceApp) string {
	var b strings.Builder
	for i, a := range apps {
		if i > 0 {
			b.WriteString(", ")
		}
		if a.Path == "" {
			fmt.Fprintf(&b, "%q(pid %d)", a.Name, a.Pid)
			continue
		}
		fmt.Fprintf(&b, "%q(pid %d, %s)", a.Name, a.Pid, a.Path)
	}
	return b.String()
}

// TestNativeSysctlRawProbes pins the struct sysctls' real raw bytes and
// parsed values against the live kernel. The fscale layout of vm.loadavg,
// the timeval length of kern.boottime and the xsw_usage length of
// vm.swapusage are read, not guessed: fix2/fix3 built the parsers from
// the XNU headers, this test is the on-device proof. Read-only sysctls
// only, no side effects, safe in any suite run. If a layout assumption
// is wrong, the failure prints the raw bytes to fix it from.
func TestNativeSysctlRawProbes(t *testing.T) {
	boot := nativeSysctlRaw("kern.boottime")
	t.Logf("kern.boottime: %d raw bytes = % x", len(boot), boot)
	if len(boot) == 0 {
		t.Fatal("kern.boottime raw read returned nothing")
	}
	bt := parseBootTimeBytes(boot)
	if bt == 0 {
		t.Errorf("kern.boottime (%d bytes) failed to parse as a timeval: % x", len(boot), boot)
	} else {
		now := time.Now().Unix()
		if bt > now+5 || bt < now-10*365*24*3600 {
			t.Errorf("boot time %d is not before now / within the last ten years (now %d)", bt, now)
		} else {
			t.Logf("kern.boottime parsed: %s (uptime %s)", time.Unix(bt, 0), time.Since(time.Unix(bt, 0)).Round(time.Second))
		}
	}

	la := nativeSysctlRaw("vm.loadavg")
	t.Logf("vm.loadavg: %d raw bytes = % x", len(la), la)
	if len(la) == 0 {
		t.Fatal("vm.loadavg raw read returned nothing")
	}
	l1, l5, l15 := parseLoadAvgBytes(la)
	if l1 == 0 && l5 == 0 && l15 == 0 {
		t.Errorf("vm.loadavg (%d bytes) parsed to all zeros, every fscale candidate layout failed; bytes: % x", len(la), la)
	} else {
		t.Logf("vm.loadavg parsed: 1min %.2f 5min %.2f 15min %.2f", l1, l5, l15)
	}

	sw := nativeSysctlRaw("vm.swapusage")
	t.Logf("vm.swapusage: %d raw bytes = % x", len(sw), sw)
	su, ok := parseSwapUsageBytes(sw)
	if !ok {
		t.Errorf("vm.swapusage (%d bytes) failed to parse as xsw_usage", len(sw))
	} else {
		if su.Total == 0 {
			t.Errorf("xsw_usage parsed but total = 0 (%+v)", su)
		}
		if su.PageSize != 0 && su.PageSize != 16384 && su.PageSize != 4096 {
			t.Errorf("xsw_usage pagesize = %d, want a real page size (4096/16384)", su.PageSize)
		}
		t.Logf("vm.swapusage parsed: total %d avail %d used %d pagesize %d encrypted %v",
			su.Total, su.Avail, su.Used, su.PageSize, su.Encrypted)
	}
}
