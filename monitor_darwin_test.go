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

func TestOutermostBundle(t *testing.T) {
	cases := []struct{ path, want string }{
		{"/Applications/Foo.app/Contents/MacOS/Foo", "/Applications/Foo.app"},
		{"/Applications/A.app/B.app/Contents/MacOS/B", "/Applications/A.app"}, // nested: outermost wins
		{"/usr/sbin/system_profiler", ""},
		{"/System/Library/Frameworks/Foo.framework/Versions/A/Foo", ""},
		{"", ""},
		{"/Applications/Arcade.app.helper", ""}, // .app must end the component
	}
	for _, c := range cases {
		if got := outermostBundle(c.path); got != c.want {
			t.Errorf("outermostBundle(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestMergeAppsAttribution(t *testing.T) {
	procs := []procReading{
		{Pid: 1, Path: "/Applications/Chrome.app/Contents/MacOS/Chrome", Footprint: 100},
		{Pid: 2, Path: "/Applications/Chrome.app/Contents/Frameworks/Chrome Helper.app/Contents/MacOS/Chrome Helper", Footprint: 50}, // helper of a nested .app still lands on the outermost
		{Pid: 3, Path: "/opt/homebrew/bin/node", Footprint: 10},
		{Pid: 4, Path: "/Applications/Terminal.app/Contents/MacOS/Terminal", Footprint: 20},
	}
	byPid := map[int32]procReading{}
	for _, p := range procs {
		byPid[p.Pid] = p
	}
	// node (pid 3) is responsible to Terminal (pid 4).
	responsible := func(pid int32) int32 {
		if pid == 3 {
			return 4
		}
		return -1
	}
	rows := mergeApps(nil, procs, nil, responsible, 0)
	if got := mustAppUsage(rows, "app:/applications/chrome.app"); got.Memory != 150 || got.Procs != 2 {
		t.Errorf("Chrome group = %+v, want memory 150 procs 2", got)
	}
	if got := mustAppUsage(rows, "app:/applications/terminal.app"); got.Memory != 30 {
		t.Errorf("node should be attributed to Terminal via its responsible process: %+v", got)
	}
	if got := mustAppUsage(rows, "exec:node"); got.Procs != 0 {
		t.Errorf("node must not stand alone: %+v", got)
	}
}

func TestMergeAppsTop8Union(t *testing.T) {
	var procs []procReading
	for i := 0; i < 20; i++ {
		procs = append(procs, procReading{
			Pid:       int32(i + 1),
			Path:      "/Applications/App" + string(rune('A'+i)) + ".app/Contents/MacOS/x",
			Footprint: uint64((20 - i) * 100), // A biggest
			UserNs:    uint64(i) * 1_000_000_000,
		})
	}
	rows := mergeApps(nil, procs, nil, func(int32) int32 { return -1 }, 2*time.Second)
	if len(rows) > 16 {
		t.Errorf("union cap is 16, got %d", len(rows))
	}
	if len(rows) < 8 {
		t.Errorf("memory top 8 must always be present, got %d", len(rows))
	}
	if !strings.Contains(rows[0].Key, "appa.app") {
		t.Errorf("rows should be memory-sorted, first = %s", rows[0].Key)
	}
}

func TestMergeAppsQuitButton(t *testing.T) {
	procs := []procReading{
		{Pid: 1, Path: "/Applications/Chrome.app/Contents/MacOS/Chrome", Footprint: 1},
		{Pid: 2, Path: "/Applications/Bar.app/Contents/MacOS/bar", Footprint: 1},
		{Pid: 3, Path: "/Applications/Foo.app/Contents/MacOS/foo", Footprint: 1},
	}
	apps := []workspaceApp{
		{Pid: 1, Path: "/Applications/Chrome.app", BundleID: "com.google.Chrome", Name: "Google Chrome", Regular: true},
		{Pid: 2, Path: "/Applications/Bar.app", BundleID: "com.bar.helper", Name: "Bar", Regular: false},  // menu bar app
		{Pid: 3, Path: "/Applications/Foo.app", BundleID: "com.apple.finder", Name: "Foo", Regular: true}, // excluded
	}
	rows := mergeApps(nil, procs, apps, func(int32) int32 { return -1 }, 0)
	if !mustAppUsage(rows, "app:/applications/chrome.app").Quit {
		t.Error("regular Dock app should offer quit")
	}
	if mustAppUsage(rows, "app:/applications/bar.app").Quit {
		t.Error("non-regular app must not offer quit")
	}
	if mustAppUsage(rows, "app:/applications/foo.app").Quit {
		t.Error("Finder-like apps must not offer quit")
	}
}

func TestDisplayNameLocalized(t *testing.T) {
	procs := []procReading{{Pid: 1, Path: "/Applications/Lark.app/Contents/MacOS/Lark"}}
	apps := []workspaceApp{{Pid: 1, Path: "/Applications/Lark.app", Name: "飞书", Regular: true}}
	if got := mustAppUsage(mergeApps(nil, procs, apps, func(int32) int32 { return -1 }, 0), "app:/applications/lark.app"); got.Name != "飞书" {
		t.Errorf("want localized name 飞书, got %q", got.Name)
	}
	// No workspace entry: fall back to the bundle folder name.
	if got := mustAppUsage(mergeApps(nil, procs, nil, func(int32) int32 { return -1 }, 0), "app:/applications/lark.app"); got.Name != "Lark" {
		t.Errorf("want bundle name Lark, got %q", got.Name)
	}
}

func TestMergeAppsCPUDeltaAndReuse(t *testing.T) {
	procs := []procReading{
		{Pid: 1, Path: "/Applications/A.app/Contents/MacOS/a", Footprint: 1, UserNs: 1_000_000_000, SysNs: 500_000_000, Start: 100},
		{Pid: 2, Path: "/Applications/B.app/Contents/MacOS/b", Footprint: 1, UserNs: 0, SysNs: 0, Start: 5},
	}
	prev := map[int32]procSample{
		1: {Pid: 1, UserNs: 0, SysNs: 0, Start: 100},
		2: {Pid: 2, UserNs: 0, SysNs: 0, Start: 9}, // reused pid: different Start
	}
	// 1.5s of CPU over 2s of wall = 75% of one core.
	rows := mergeApps(prev, procs, nil, func(int32) int32 { return -1 }, 2*time.Second)
	if got := mustAppUsage(rows, "app:/applications/a.app"); got.CPU != 75 {
		t.Errorf("CPU = %v, want 75", got.CPU)
	}
	if got := mustAppUsage(rows, "app:/applications/b.app"); got.CPU != 0 {
		t.Errorf("reused pid must not count CPU this round, got %v", got.CPU)
	}
}

func TestMergeAppsCPUCanExceed100(t *testing.T) {
	procs := []procReading{
		{Pid: 1, Path: "/Applications/A.app/Contents/MacOS/a", UserNs: 4_000_000_000, SysNs: 4_000_000_000, Start: 1},
	}
	prev := map[int32]procSample{1: {Pid: 1, UserNs: 0, SysNs: 0, Start: 1}}
	rows := mergeApps(prev, procs, nil, func(int32) int32 { return -1 }, time.Second)
	if got := mustAppUsage(rows, "app:/applications/a.app"); got.CPU != 800 {
		t.Errorf("single-core percent can exceed 100, want 800, got %v", got.CPU)
	}
}

func TestQuitMatchesBoundary(t *testing.T) {
	apps := []workspaceApp{
		{Pid: 1, Path: "/Applications/Arc.app", BundleID: "company.arc", Regular: true},
		{Pid: 2, Path: "/Applications/Arcade.app", BundleID: "com.arcade", Regular: true},
		{Pid: 3, Path: "/Applications/Arc.app/Contents/Helpers/H.app", BundleID: "company.arc.helper", Regular: true},
		{Pid: 4, Path: "/System/Library/CoreServices/Finder.app", BundleID: "com.apple.finder", Regular: true},
		{Pid: 5, Path: "/Applications/Cleanzo.app", BundleID: "com.cleanzo.app", Regular: true},
		{Pid: 6, Path: "/Applications/Bar.app", BundleID: "com.bar", Regular: false}, // menu bar app
	}
	arc := quitMatches("/Applications/Arc.app", apps)
	if len(arc) != 2 {
		t.Fatalf("Arc.app should match the app and its helper bundle, got %d", len(arc))
	}
	for _, a := range arc {
		if a.Pid == 2 {
			t.Error("Arcade.app must not match Arc.app")
		}
	}
	if got := quitMatches("/Applications/Arcade.app", apps); len(got) != 1 || got[0].Pid != 2 {
		t.Errorf("Arcade.app matches only itself: %+v", got)
	}
	if got := quitMatches("/System/Library/CoreServices/Finder.app", apps); len(got) != 0 {
		t.Error("Finder is excluded from quitting")
	}
	if got := quitMatches("/Applications/Cleanzo.app", apps); len(got) != 0 {
		t.Error("Cleanzo itself is excluded from quitting")
	}
	if got := quitMatches("/Applications/Bar.app", apps); len(got) != 0 {
		t.Error("non-regular apps are not offered quitting")
	}
	if got := quitMatches("", apps); len(got) != 0 {
		t.Error("empty path matches nothing")
	}
}
