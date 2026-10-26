package main

import (
	"context"
	"os"
	"sort"
	"syscall"
	"testing"
	"time"
)

// processFixture is one Mac's worth of processes for the grouping tests.
func processFixture() (cur map[int32]procReading, others []otherProc, apps []workspaceApp, responsible func(int32) int32) {
	const helper = "/Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Framework.framework/Versions/A/Helpers/Google Chrome Helper (Renderer).app/Contents/MacOS/Google Chrome Helper (Renderer)"
	const mdworker = "/System/Library/Frameworks/CoreServices.framework/Frameworks/Metadata.framework/Versions/A/Support/mdworker_shared"
	cur = snapshotProcs([]procReading{
		{Pid: 100, Path: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", Footprint: 900 << 20},
		{Pid: 101, Path: helper, Footprint: 600 << 20},
		{Pid: 102, Path: helper, Footprint: 400 << 20},
		{Pid: 200, Path: "/Applications/Safari.app/Contents/MacOS/Safari", Footprint: 300 << 20},
		{Pid: 201, Path: "/System/Library/Frameworks/WebKit.framework/Versions/A/XPCServices/com.apple.WebKit.WebContent.xpc/Contents/MacOS/com.apple.WebKit.WebContent", Footprint: 200 << 20},
		{Pid: 300, Path: "/System/Applications/Utilities/Terminal.app/Contents/MacOS/Terminal", Footprint: 100 << 20},
		{Pid: 301, Path: "/bin/zsh", Footprint: 5 << 20},
		{Pid: 302, Path: "/opt/homebrew/bin/node", Footprint: 700 << 20},
		{Pid: 303, Path: "/usr/bin/yes", Footprint: 1 << 20},
		{Pid: 400, Path: mdworker, Footprint: 30 << 20},
		{Pid: 401, Path: mdworker, Footprint: 20 << 20},
		{Pid: 500, Path: "/Applications/Microsoft Defender.app/Contents/MacOS/Microsoft Defender", Footprint: 200 << 20},
		{Pid: 600, Path: "/Applications/Cleanzo.app/Contents/MacOS/Cleanzo", Footprint: 80 << 20},
	})
	others = []otherProc{
		{Pid: 414, UID: 88, CPU: 40.2, Memory: 135 << 20, Path: "/System/Library/PrivateFrameworks/SkyLight.framework/Resources/WindowServer"},
		{Pid: 7045, UID: 0, CPU: 10.8, Memory: 50 << 20, Path: "/Applications/Microsoft Defender.app/Contents/MacOS/wdavdaemon"},
	}
	apps = []workspaceApp{
		{Pid: 100, Path: "/Applications/Google Chrome.app", BundleID: "com.google.Chrome", Name: "Google Chrome", Regular: true},
		{Pid: 200, Path: "/Applications/Safari.app", BundleID: "com.apple.Safari", Name: "Safari", Regular: true},
		{Pid: 300, Path: "/System/Applications/Utilities/Terminal.app", BundleID: "com.apple.Terminal", Name: "终端", Regular: true},
		{Pid: 500, Path: "/Applications/Microsoft Defender.app", BundleID: "com.microsoft.wdav", Name: "Microsoft Defender", Regular: true},
		{Pid: 600, Path: "/Applications/Cleanzo.app", BundleID: "com.cleanzo.app", Name: "Cleanzo", Regular: true},
	}
	responsible = func(pid int32) int32 {
		switch pid {
		case 201:
			return 200 // WebKit's web content works for Safari
		case 301, 302, 303:
			return 300 // started in Terminal
		}
		return pid
	}
	return cur, others, apps, responsible
}

func TestGroupProcessesPlacesAppsCommandLineAndSystem(t *testing.T) {
	cur, others, apps, responsible := processFixture()
	rows := groupProcesses(cur, cur, others, apps, responsible, testSystemPath, noBundleIDs, 0)
	got := rowsByKey(rows)
	want := map[string]struct {
		kind      string
		procs     int
		estimated bool
	}{
		"app:/applications/google chrome.app":             {"app", 3, false},
		"app:/applications/safari.app":                    {"app", 2, false}, // with WebKit's web content
		"app:/system/applications/utilities/terminal.app": {"app", 1, false}, // not the shell, node or yes
		"app:/applications/microsoft defender.app":        {"app", 2, true},  // its root daemon joins it
		"app:/applications/cleanzo.app":                   {"app", 1, false},
		"cli:zsh":                                         {"cli", 1, false},
		"cli:node":                                        {"cli", 1, false},
		"cli:yes":                                         {"cli", 1, false},
		"sys:mdworker_shared":                             {"system", 2, false},
		"sys:WindowServer":                                {"system", 1, true},
	}
	if len(got) != len(want) {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("rows = %v", keys)
	}
	for key, w := range want {
		r, ok := got[key]
		if !ok {
			t.Errorf("missing row %s", key)
			continue
		}
		if r.Kind != w.kind || r.Procs != w.procs || r.Estimated != w.estimated {
			t.Errorf("%s = kind %s, %d procs, estimated %v; want %+v", key, r.Kind, r.Procs, r.Estimated, w)
		}
	}
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Key > rows[i].Key {
			t.Fatalf("rows not ordered by key: %s before %s", rows[i-1].Key, rows[i].Key)
		}
	}
	chrome := got["app:/applications/google chrome.app"]
	if chrome.Memory != 1900<<20 || len(chrome.Children) != 2 ||
		chrome.Children[0] != (ProcessChild{Name: "Google Chrome", Count: 1, Memory: 900 << 20}) ||
		chrome.Children[1] != (ProcessChild{Name: "Google Chrome Helper (Renderer)", Count: 2, Memory: 1000 << 20}) {
		t.Errorf("chrome = %+v", chrome)
	}
	ws := got["sys:WindowServer"]
	if ws.Name != "WindowServer" || ws.CPU != 40.2 || ws.Memory != 135<<20 || ws.Path != "" || ws.Quit {
		t.Errorf("WindowServer = %+v", ws)
	}
	def := got["app:/applications/microsoft defender.app"]
	if def.CPU != 10.8 || len(def.Children) != 2 || def.Children[0].Estimated || !def.Children[1].Estimated || def.Children[1].Name != "wdavdaemon" {
		t.Errorf("Microsoft Defender = %+v", def)
	}
}

func TestGroupProcessesNamesQuitAndBundleIDs(t *testing.T) {
	cur, others, apps, responsible := processFixture()
	ids := map[string]string{
		"/Applications/Google Chrome.app":             "com.google.Chrome",
		"/System/Applications/Utilities/Terminal.app": "com.apple.Terminal",
	}
	rows := groupProcesses(cur, cur, others, apps, responsible, testSystemPath, func(b string) string { return ids[b] }, 0)
	got := rowsByKey(rows)
	term := got["app:/system/applications/utilities/terminal.app"]
	if term.Name != "终端" || !term.Quit || term.BundleID != "com.apple.terminal" || term.Path != "/System/Applications/Utilities/Terminal.app" {
		t.Errorf("Terminal = %+v", term)
	}
	if c := got["app:/applications/google chrome.app"]; c.BundleID != "com.google.chrome" || !c.Quit {
		t.Errorf("Chrome = %+v", c)
	}
	if c := got["app:/applications/cleanzo.app"]; c.Quit {
		t.Error("Cleanzo must never offer quit")
	}
	if n := got["cli:node"]; n.Name != "node" || n.Quit || n.Path != "" || n.BundleID != "" {
		t.Errorf("node = %+v", n)
	}
	if m := got["sys:mdworker_shared"]; m.Name != "mdworker_shared" || m.Quit || len(m.Children) != 1 || m.Children[0].Count != 2 {
		t.Errorf("mdworker_shared = %+v", m)
	}
}

// TestProcessSamplingBudget measures spec §六.9 on this Mac. The loop
// together with the ps it starts may use 3% of one core on average.
// Opt in with CLEANZO_PERF=1; it runs for 30 seconds. Don't use -race,
// which inflates the CPU.
func TestProcessSamplingBudget(t *testing.T) {
	if os.Getenv("CLEANZO_PERF") == "" {
		t.Skip("set CLEANZO_PERF=1 to measure the sampling cost on this Mac")
	}
	src := realProcessSource()
	src.apps = func() []workspaceApp { return nil } // NSWorkspace needs the app's main thread
	usage := func() time.Duration {
		var self, kids syscall.Rusage
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &self)
		_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &kids)
		tv := func(v syscall.Timeval) time.Duration { return time.Duration(v.Nano()) }
		return tv(self.Utime) + tv(self.Stime) + tv(kids.Utime) + tv(kids.Stime)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	samples, rows := 0, 0
	before, start := usage(), time.Now()
	_ = watchProcessesWith(ctx, func(s ProcessSample) error { samples++; rows = len(s.Rows); return nil }, src)
	used, wall := usage()-before, time.Since(start)
	pct := used.Seconds() / wall.Seconds() * 100
	t.Logf("%d samples, %d rows in the last, CPU %v in %v: %.2f%% of one core", samples, rows, used.Round(time.Millisecond), wall.Round(time.Second), pct)
	if pct > 3 {
		t.Errorf("sampling used %.2f%% of one core, budget 3%%", pct)
	}
}
