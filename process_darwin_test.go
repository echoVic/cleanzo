package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNativeProcsCPUTimeMatchesGetrusage(t *testing.T) {
	processCPU := func() uint64 {
		for _, p := range nativeProcs() {
			if p.Pid == int32(os.Getpid()) {
				return p.UserNs + p.SysNs
			}
		}
		t.Fatal("nativeProcs did not report this process")
		return 0
	}
	usage := func() time.Duration {
		var r syscall.Rusage
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &r); err != nil {
			t.Fatal(err)
		}
		return time.Duration(r.Utime.Nano()) + time.Duration(r.Stime.Nano())
	}
	beforeProc, beforeUsage := processCPU(), usage()
	var work uint64 = 1
	for usage()-beforeUsage < 200*time.Millisecond {
		for range 100_000 {
			work = work*1664525 + 1013904223
		}
	}
	runtime.KeepAlive(work)
	procDelta, usageDelta := processCPU()-beforeProc, usage()-beforeUsage
	ratio := float64(procDelta) / float64(usageDelta)
	t.Logf("nativeProcs delta %v, getrusage delta %v, ratio %.4f", time.Duration(procDelta), usageDelta, ratio)
	if ratio < 0.8 || ratio > 1.2 {
		t.Fatalf("CPU deltas differ by more than 20%%: ratio %.4f", ratio)
	}
}

func TestNativeProcsMachTimeConversion(t *testing.T) {
	for _, tc := range []struct {
		name         string
		ticks        uint64
		numer, denom uint32
		want         uint64
	}{
		{"apple silicon", 3, 125, 3, 125},
		{"fraction", 4, 125, 3, 166},
		{"128-bit product", ^uint64(0), 2, 3, 12297829382473034410},
		{"saturate", ^uint64(0), 125, 3, ^uint64(0)},
		{"missing numerator", 123, 0, 3, 123},
		{"missing denominator", 123, 125, 0, 123},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := machTicksToNs(tc.ticks, tc.numer, tc.denom); got != tc.want {
				t.Fatalf("got %d ns, want %d", got, tc.want)
			}
		})
	}
}

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
// Opt in with CLEANZO_PERF=1; it runs for 60 seconds. Don't use -race,
// which inflates the CPU.
func TestProcessSamplingBudget(t *testing.T) {
	if os.Getenv("CLEANZO_PERF") == "" {
		t.Skip("set CLEANZO_PERF=1 to measure the sampling cost on this Mac")
	}
	src := realProcessSource()
	// NSWorkspace needs the app's main thread; calling it directly in a
	// test binary can crash because tests run on a non-main thread.
	src.apps = func() []workspaceApp { return nil }
	usage := func() time.Duration {
		var self, kids syscall.Rusage
		_ = syscall.Getrusage(syscall.RUSAGE_SELF, &self)
		_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &kids)
		tv := func(v syscall.Timeval) time.Duration { return time.Duration(v.Nano()) }
		return tv(self.Utime) + tv(self.Stime) + tv(kids.Utime) + tv(kids.Stime)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	samples, rows := 0, 0
	before, start := usage(), time.Now()
	_ = watchProcessesWith(ctx, func(s ProcessSample) error {
		samples++
		rows = len(s.Rows)
		_, err := json.Marshal(s)
		return err
	}, src)
	used, wall := usage()-before, time.Since(start)
	pct := used.Seconds() / wall.Seconds() * 100
	t.Logf("%d samples, %d rows in the last, CPU %v in %v: %.2f%% of one core", samples, rows, used.Round(time.Millisecond), wall.Round(time.Second), pct)
	if pct > 3 {
		t.Errorf("sampling used %.2f%% of one core, budget 3%%", pct)
	}
}

func TestGroupProcessesPreservesChineseBundle(t *testing.T) {
	const path = "/Applications/语雀.app/Contents/MacOS/语雀"
	cur := snapshotProcs([]procReading{{Pid: 1, Path: path, Footprint: 1024}})
	others := []otherProc{{Pid: 2, Path: path, Memory: 2048}}
	rows := groupProcesses(cur, cur, others, nil, func(p int32) int32 { return p }, systemExecutable, noBundleIDs, 0)
	if len(rows) != 1 || rows[0].Key != "app:/applications/语雀.app" || rows[0].Path != "/Applications/语雀.app" || rows[0].Procs != 2 || !rows[0].Estimated {
		t.Fatalf("Chinese bundle was split: %+v", rows)
	}
}

func TestSystemExecutable(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"/System/Library/Frameworks/service", true},
		{"/usr/libexec/service", true},
		{"/usr/sbin/service", true},
		{"/sbin/service", true},
		{"/Library/Apple/service", true},
		{"/bin/zsh", false},
		{"/usr/bin/yes", false},
		{"/opt/homebrew/bin/node", false},
		{"/Applications/X.app/Contents/MacOS/X", false},
		{"/Systematic/service", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if got := systemExecutable(tc.path); got != tc.want {
				t.Fatalf("systemExecutable(%q)=%v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// Replace only the ps command boundary: these tests run a stand-in for
// /bin/ps through the real spawnOutput, never /bin/ps itself.
func fakeProcessPs(t *testing.T, mode string) {
	t.Helper()
	old := processPsSpawn
	oldPath := processPidPath
	t.Cleanup(func() { processPsSpawn, processPidPath = old, oldPath })
	processPidPath = func(pid int32) string {
		if pid == 12345 {
			return "/Applications/语雀.app/Contents/MacOS/语雀"
		}
		return ""
	}
	t.Setenv("CLEANZO_PROCESS_PS_HELPER", mode)
	processPsSpawn = func(ctx context.Context, path string, argv, env []string) ([]byte, int32, error) {
		if path != "/bin/ps" || strings.Join(argv, " ") != "/bin/ps -ax -o pid=,uid=,pcpu=,rss=,ucomm=" {
			t.Errorf("unexpected ps command: %s %v", path, argv)
		}
		return spawnOutput(ctx, os.Args[0], []string{os.Args[0], "-test.run=^TestProcessPsHelper$"}, env)
	}
}

func TestProcessPsHelper(t *testing.T) {
	mode := os.Getenv("CLEANZO_PROCESS_PS_HELPER")
	if mode == "" {
		return
	}
	if os.Getenv("LC_ALL") != "en_US.UTF-8" {
		fmt.Fprintln(os.Stderr, "ps needs en_US.UTF-8 to preserve Unicode paths")
		os.Exit(2)
	}
	if mode == "empty" {
		os.Exit(0)
	}
	// Use another uid even when the parent is root, so self-filtering must
	// depend on the command's PID rather than the parser's uid filter.
	uid := uint32(os.Getuid()) + 1
	fmt.Printf("%d %d 0.1 1 ps\n", os.Getpid(), uid)
	if mode == "self-only" {
		os.Exit(0)
	}
	fmt.Printf("12345 %d 2.5 1024 Google Chrome He\n", uid)
	if mode == "fail" {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestParsePsCommandPreservesUnicodeAndSkipsItself(t *testing.T) {
	fakeProcessPs(t, "success")
	got := readOtherProcs(context.Background())
	if len(got) != 1 || got[0].Pid != 12345 || got[0].Path != "/Applications/语雀.app/Contents/MacOS/语雀" || got[0].CPU != 2.5 {
		t.Fatalf("ps reading = %+v, want only the unchanged Chinese path", got)
	}
}

func TestParsePsCommandFailureIsUnavailable(t *testing.T) {
	fakeProcessPs(t, "fail")
	if got := readOtherProcs(context.Background()); got != nil {
		t.Fatalf("failed ps returned a partial successful reading: %+v", got)
	}
}

func TestParsePsCommandSuccessCanBeEmpty(t *testing.T) {
	for _, mode := range []string{"empty", "self-only"} {
		t.Run(mode, func(t *testing.T) {
			fakeProcessPs(t, mode)
			if got := readOtherProcs(context.Background()); got == nil || len(got) != 0 {
				t.Fatalf("successful empty ps reading = %+v; want a non-nil empty slice", got)
			}
		})
	}
}

func TestParsePsCommandCanceledIsUnavailable(t *testing.T) {
	fakeProcessPs(t, "success")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := readOtherProcs(ctx); got != nil {
		t.Fatalf("canceled ps returned a successful reading: %+v", got)
	}
}

func TestOtherProcsResolvesFullPathsOrKeepsShortName(t *testing.T) {
	for _, tc := range []struct {
		name, resolved, want string
	}{
		{"full path", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome Helper (Renderer)", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome Helper (Renderer)"},
		{"short name fallback", "", "Google Chrome He"},
		{"Chinese path", "/Applications/语雀.app/Contents/MacOS/语雀", "/Applications/语雀.app/Contents/MacOS/语雀"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeProcessPs(t, "success")
			var lookedUp []int32
			processPidPath = func(pid int32) string {
				lookedUp = append(lookedUp, pid)
				return tc.resolved
			}
			got := readOtherProcs(context.Background())
			if len(got) != 1 || got[0].Path != tc.want {
				t.Fatalf("got %+v, want Path %q", got, tc.want)
			}
			if len(lookedUp) != 1 || lookedUp[0] != 12345 {
				t.Fatalf("path lookups = %v, want only the other process, excluding ps", lookedUp)
			}
		})
	}
}

func TestNativeProcsPidPathReadsItself(t *testing.T) {
	want, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want, err = filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}
	got := nativePidPath(int32(os.Getpid()))
	got, err = filepath.EvalSymlinks(got)
	if err != nil || got != want {
		t.Fatalf("nativePidPath self = %q, err=%v, want %q", got, err, want)
	}
	if got := nativePidPath(-1); got != "" {
		t.Fatalf("nativePidPath invalid PID = %q, want empty", got)
	}
}

func TestNativePidPathReadsLaunchd(t *testing.T) {
	got := nativePidPath(1)
	if got == "" {
		t.Skip("libproc cannot read launchd's path")
	}
	if got != "/sbin/launchd" {
		t.Fatalf("nativePidPath(1) = %q, want /sbin/launchd", got)
	}
}

func TestForceQuitAppRejectsNonAppBeforeWorkspace(t *testing.T) {
	old := runOnMain
	t.Cleanup(func() { runOnMain = old })
	runOnMain = func(func()) { t.Fatal("non-app path reached workspace") }
	for _, path := range []string{"/Applications", "/Applications/", "", "/Applications/Test.app/Contents/MacOS/Test"} {
		ok, err := (Cleaner{}).ForceQuitApp(path)
		ce, isCode := err.(codeError)
		if ok || !isCode || ce.Code != "not-app" {
			t.Fatalf("ForceQuitApp(%q) = (%v, %v), want false and not-app", path, ok, err)
		}
	}
}

func TestForceQuitAppAcceptsAppSuffixCaseAndTrailingSeparators(t *testing.T) {
	old := runOnMain
	t.Cleanup(func() { runOnMain = old })
	// An empty workspace prevents any native reads or app termination.
	runOnMain = func(func()) {}
	for _, path := range []string{"/Applications/Test.app", "/Applications/Test.APP///"} {
		ok, err := (Cleaner{}).ForceQuitApp(path)
		ce, isCode := err.(codeError)
		if ok || !isCode || ce.Code != "not-running" {
			t.Fatalf("ForceQuitApp(%q) = (%v, %v), want false and not-running", path, ok, err)
		}
	}
}

func TestGroupProcessesUsesKernelNames(t *testing.T) {
	for _, tc := range []struct {
		name, path, short, key, row, child string
	}{
		{"symlinked CLI", "/Users/me/.claude/versions/2.1.304", "claude", "cli:claude", "claude", "claude"},
		{"truncated system name", "/System/Library/PrivateFrameworks/EcosystemAnalytics.framework/Support/ecosystemanalyticsd", "ecosystemanalyt", "sys:ecosystemanalyticsd", "ecosystemanalyticsd", "ecosystemanalyticsd"},
		{"Chrome helper", "/Applications/Google Chrome.app/Contents/Frameworks/Google Chrome Helper (Renderer).app/Contents/MacOS/Google Chrome Helper (Renderer)", "Google Chrome He", "app:/applications/google chrome.app", "Google Chrome", "Google Chrome Helper (Renderer)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cur := snapshotProcs([]procReading{{Pid: 1, Path: tc.path, Name: tc.short}})
			rows := groupProcesses(cur, cur, nil, nil, func(p int32) int32 { return p }, testSystemPath, noBundleIDs, 0)
			if len(rows) != 1 || rows[0].Key != tc.key || rows[0].Name != tc.row || len(rows[0].Children) != 1 || rows[0].Children[0].Name != tc.child {
				t.Fatalf("own rows = %+v, want key %q, row %q, child %q", rows, tc.key, tc.row, tc.child)
			}
			others := []otherProc{{Pid: 2, Path: tc.path, Name: tc.short}}
			rows = groupProcesses(nil, nil, others, nil, func(p int32) int32 { return p }, testSystemPath, noBundleIDs, 0)
			key := tc.key
			if strings.HasPrefix(key, "cli:") {
				key = "sys:" + tc.row
			}
			if len(rows) != 1 || rows[0].Key != key || rows[0].Name != tc.row || len(rows[0].Children) != 1 || rows[0].Children[0].Name != tc.child {
				t.Fatalf("other rows = %+v, want key %q, row %q, child %q", rows, key, tc.row, tc.child)
			}
		})
	}
}

func TestOtherProcsKeepsShortNameAfterPathResolution(t *testing.T) {
	fakeProcessPs(t, "success")
	got := readOtherProcs(context.Background())
	if len(got) != 1 || got[0].Name != "Google Chrome He" {
		t.Fatalf("other processes = %+v, want the ps short name preserved", got)
	}
}

func TestNativeProcsReadsKernelName(t *testing.T) {
	for _, p := range nativeProcs() {
		if p.Pid == int32(os.Getpid()) {
			if p.Name == "" || strings.ContainsRune(p.Name, '\x00') {
				t.Fatalf("self kernel name = %q, want a nonempty name without a NUL", p.Name)
			}
			return
		}
	}
	t.Fatal("nativeProcs did not report this process")
}

func TestReadOtherProcsSetsItsOwnLocale(t *testing.T) {
	t.Setenv("LC_ALL", "C")
	fakeProcessPs(t, "success") // the stand-in exits 2 unless LC_ALL is en_US.UTF-8
	if got := readOtherProcs(context.Background()); len(got) != 1 {
		t.Fatalf("got %+v; ps did not get LC_ALL=en_US.UTF-8 over the inherited C", got)
	}
}

func TestOtherProcsReaderRemembersPaths(t *testing.T) {
	old, oldPath := processPsSpawn, processPidPath
	t.Cleanup(func() { processPsSpawn, processPidPath = old, oldPath })
	uid := os.Getuid() + 1
	lines, fail := "", false
	processPsSpawn = func(context.Context, string, []string, []string) ([]byte, int32, error) {
		if fail {
			return nil, 0, errors.New("ps failed")
		}
		return []byte(lines), 1, nil
	}
	var lookups []int32
	processPidPath = func(pid int32) string {
		lookups = append(lookups, pid)
		return fmt.Sprintf("/opt/p%d/tool", pid)
	}
	read := otherProcsReader()
	step := func(ps string, want ...int32) {
		t.Helper()
		lines, lookups = ps, nil
		got := read(context.Background())
		if got == nil {
			t.Fatalf("no reading for %q", ps)
		}
		for _, p := range got {
			if p.Path != fmt.Sprintf("/opt/p%d/tool", p.Pid) {
				t.Fatalf("pid %d path %q", p.Pid, p.Path)
			}
		}
		if !slices.Equal(lookups, want) {
			t.Fatalf("after %q: lookups %v, want %v", ps, lookups, want)
		}
	}
	both := fmt.Sprintf("10 %d 0.0 1 tool\n11 %d 0.0 1 tool\n", uid, uid)
	step(both, 10, 11) // the first reading looks both up
	step(both)         // same pids under the same names: none
	fail = true
	if got := read(context.Background()); got != nil {
		t.Fatalf("a failed ps gave %+v", got)
	}
	fail = false
	step(both)                                        // the failure kept the memory
	step(fmt.Sprintf("10 %d 0.0 1 other\n", uid), 10) // a new name under pid 10; 11 is gone
	step(both, 10, 11)                                // 10 changed name again; 11 came back: both looked up
}
