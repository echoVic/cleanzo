# Process Page — Implementation Plan

> **For agentic workers:** In this project the tasks are executed by traecli and the Codex CLI (see "Execution batches"). **Executors do not commit**: the reviewer checks and commits each task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** A 「进程」 page under 系统状态 with three tabs.
- **CPU and 内存** list live processes merged per app, including other accounts' processes as estimates. They have:
  - a diagnosis bar;
  - quit, and force quit when quit doesn't work;
  - a pause switch.
- **磁盘占用** is the growth list moved in. 「占用增长」 leaves the sidebar.

**Architecture:**
- **Go: three new files.**
  - `process.go`: the exported types, how processes are grouped into rows, the sampling loop, and the bound methods `WatchProcesses` and `ForceQuitApp`.
  - `process_darwin.go`: the real source. It uses libproc for this user's processes, `/bin/ps` for everyone else's, and Info.plist for bundle IDs.
  - `process_other.go`: the stub for everything else.
- **Go: small edits.** `native.go` gains `workspaceForceTerminate`, with stubs on the other platforms. The status page's sampling (`WatchStatus`, `mergeApps`) does not change.
- **Sampling only runs while someone is looking.** The page opens its channel only while all of these hold:
  - it is shown;
  - it is on the CPU or 内存 tab;
  - it is not paused;
  - the window is visible.

  Otherwise it closes the channel, and the Go loop, including `ps`, stops.
- **Web: the page.** `src/react/process/` holds the page: `api`, `format`, `ProcessPage`, `entry` and `mount`. It is wired like the other React pages.
- **Web: the growth page becomes a panel.** `GrowthPage` turns into `GrowthPanel`. It has no page header, and its 「现在量一次」 moves into its status line. The 磁盘占用 tab renders it.
- **Web: routing.** `main.ts` and `index.html` route `process` instead of `growth`. Every entry that opened the growth page now opens the disk tab at that row.
- **Web: status page.** It gets 「查看全部」.

**Tech Stack:** Go 1.27, mygo 0.6.2 and purego, which are already dependencies. TypeScript, React 19, `@radix-ui/react-tabs` and `lucide-react`, also already used. Tests run with bun test. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-26-process-page-design.md` (Chinese). Section numbers (§三 etc.) refer to it. It builds on `docs/superpowers/specs/2026-10-24-growth-monitor-design.md`, which is already implemented on this branch.

## Global Constraints

- **No new dependencies**, Go or npm. Do not run commands that need the network.
- **macOS only in this version.**
  - `processSupported()` is false elsewhere, and `WatchProcesses` returns `codeError{Code: "unsupported"}` there.
  - On Windows the 进程 nav item is hidden. The page renders a one-line note, because the smoke tour still visits it and waits for `.cz-react`.
- **Platform rules**, enforced by tests in `testenv_test.go`:
  - Exported Go names and `Cleaner` methods live in untagged files. `_darwin.go` and build-tagged files hold unexported helpers only (`TestBindingsArePlatformNeutral`).
  - In untagged, non-test `.go` files, build paths with `filepath.Join` and `filepath.Separator`. Forms like `p+"/"`, `"/"+p` or `strings.Split(p, "/")` are not allowed (`TestNoSlashPathLiterals`). macOS path prefixes such as `"/System/"` live in `process_darwin.go`, which that lint skips.
- **Bindings are generated.** After changing bound methods or their types, run `bun run generate`. Never edit `src/mygo.ts` by hand.
- **Copy:**
  - Every new string has `zh` and `en` in `src/i18n.ts`.
  - Use the keys and texts in Task 4 verbatim.
  - Reuse the existing `status.*` strings where this plan says so.
- **No new deletion paths.**
- **Quitting and force quitting:**
  - The only new destructive action is `ForceQuitApp`. It calls `NSRunningApplication forceTerminate` through `quitAppWith`, so it uses `QuitApp`'s matching: this user's Dock apps only, excluding Finder and Cleanzo.
  - The page offers it only after `QuitApp` returned with the app still running, and only after a confirmation dialog.
  - Never quit or kill a system process, another account's process, or a single child process.
- **`ps` runs only inside the watch loop.** Never at launch, in the background, or for the status page.
- **Unchanged:** the status page's own sampling, the growth monitor's measuring, records, alerts, notifications, menu bar and settings.
- **Look:** Cleanzo's own styles. Reuse the status page's `st-*` list classes and the segmented control. Nothing is copied from the reference app the maintainer showed.

## Review Focus

1. **`ps` parsing.** The command column keeps its spaces. Malformed lines are skipped. This user's uid is dropped, and numbers are read in the C locale.
2. **Grouping** (spec §三):
   - Any process inside an `.app`, of any account, joins that app.
   - This user's OS processes or XPC services join their responsible app.
   - Other OS processes get a 「系统」 row by name.
   - Everything else gets a command-line row by name, so `yes` started in Terminal is not counted as Terminal.
   - Estimates are flagged on the row and on the child.
3. **The loop:**
   - The first sample comes after about 1 s.
   - `ps` runs at the start and then on every third sample.
   - Nothing runs after the context ends or `send` fails.
4. **Force quit** reuses `quitAppWith`; the only difference is the terminate function.
5. **The page's channel:**
   - It is open exactly while it is shown, on CPU or 内存, not paused, and visible.
   - Pausing keeps the last reading on screen.
   - Each visit resets the tab to CPU (or to the requested tab) and the switch to running.
6. **Freezing.** Row order freezes only while a quit or force-quit dialog is open or a quit is in progress. Hovering does not freeze it.
7. **The `GrowthPanel` refactor** changes no behavior, apart from moving 「现在量一次」 into the status line.
8. **Routing:**
   - A growth notification, a menu bar item or the status alert opens 进程 › 磁盘占用 and focuses that row.
   - 「查看全部」 opens CPU or 内存, following the status page's sort.

## File Structure

| File | What changes |
|---|---|
| `process.go` (new) | `ProcessRow`, `ProcessChild`, `ProcessSample`; `otherProc`, `processSource`; `groupProcesses`, `ownProcessKey`, `otherProcessKey`, `processName`; `parsePsOutput`, `psFields`; `watchProcessesWith`, `snapshotProcs`; bound `WatchProcesses`, `ForceQuitApp` |
| `process_darwin.go` (new) | `processSupported`, `realProcessSource`, `readOtherProcs` (runs `/bin/ps`), `systemExecutable` |
| `process_other.go` (new, `!darwin`) | `processSupported` false, an empty `realProcessSource` |
| `process_test.go` (new) | parsing, CPU deltas, loop cadence and stopping: every platform |
| `process_darwin_test.go` (new) | grouping with `.app` paths, names, quit and bundle IDs; the opt-in CPU budget check (`CLEANZO_PERF=1`) |
| `native.go` | `workspaceSend`, shared by `workspaceTerminate` and the new `workspaceForceTerminate` |
| `native_other.go`, `native_windows.go` | `workspaceForceTerminate` stub (false) |
| `status_darwin.go` | `cpuBusyTotal`, shared by the status and process sources |
| `src/mygo.ts` | regenerated |
| `src/react/growth/GrowthPage.tsx` | `GrowthPanel` without a page header; a temporary `GrowthPage` shell until Task 5 |
| `src/react/growth/entry.ts`, `mount.tsx` | deleted in Task 5 |
| `src/ui/components/seg.ts` (new) | `segTrigger`, moved out of `StatusPage.tsx` |
| `src/react/process/api.ts`, `format.ts`, `ProcessPage.tsx`, `entry.ts`, `mount.tsx` (new) | the page |
| `src/react/status/StatusPage.tsx`, `entry.ts`, `mount.tsx` | `goProcess` replaces `goGrowth`; 「查看全部」 |
| `src/main.ts`, `index.html`, `src/shared.ts` | the `process` view replaces `growth` |
| `src/i18n.ts`, `src/style.css`, `src/dev/mock-mygo.ts` | strings, styles, a mock stream |
| `smoke.go`, `tests/smoke/windows.ps1` | the tour visits `process` instead of `growth` |
| `tests/process-react.test.ts` (new), `tests/growth-react.test.ts`, `tests/status-react.test.ts`, `package.json` | tests |
| `README.md`, `CHANGELOG.md`, the spec | docs (Task 6) |

---

### Task 1: Go — sampling, grouping, `ps` and force quit

**Files:**
- Create: `process.go`, `process_darwin.go`, `process_other.go`, `process_test.go`, `process_darwin_test.go`
- Modify: `native.go` (around `workspaceTerminate`, lines ~363–390), `native_other.go`, `native_windows.go` (next to `workspaceTerminate`), `status_darwin.go` (`realStatusSource`)
- Regenerate: `src/mygo.ts`

**Interfaces:**
- **Consumes, already in the repo:**
  - `procReading{Pid, Path, Footprint, UserNs, SysNs, Start}` (`native.go` / `native_other.go` / `native_windows.go`)
  - `nativeProcs()`, `nativeResponsiblePid(pid)`, `nativeSysctlUint64(name)`, `nativeCPUTicks()`
  - `workspaceApp`, `workspaceList()`
  - `outermostBundle(path)`, `displayName(key, bundle, apps, procs)`, `canQuit(bundle, apps)`
  - `quitAppWith(path, list, terminate)`, `clampPct`, `sleepCtx`
  - `codeError{Code}`, `readAppIdentity(path).bid`
- **Produces (TypeScript via `bun run generate`):**
  - `ProcessRow {key, name, kind: "app"|"cli"|"system" (string), path, bundleId, cpu, memory, procs, estimated, quit, children: ProcessChild[]}`
  - `ProcessChild {name, count, cpu, memory, estimated}`
  - `ProcessSample {rows, cpu, pressure, own, others}`
  - `Cleaner.watchProcesses(ch: Channel<ProcessSample>): Promise<void>`
  - `Cleaner.forceQuitApp(path: string): Promise<boolean>`

- [ ] **Step 1: Write the failing tests.** Create `process_test.go`, which runs on every platform:

```go
package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParsePsOutputKeepsOtherAccountsWithFullPaths(t *testing.T) {
	out := []byte(strings.Join([]string{
		"    1     0   0.3  12288 /sbin/launchd",
		"  414    88  40.2 138240 /System/Library/PrivateFrameworks/SkyLight.framework/Resources/WindowServer",
		" 7045     0  10.8  53872 /Applications/Microsoft Defender.app/Contents/MacOS/wdavdaemon",
		" 9001   501  99.0   4096 /usr/bin/yes",
		"    0     0   5.0      0 kernel_task",
		"garbage line",
		"   12     0     x      1 /bad/cpu",
		"",
	}, "\n"))
	got := parsePsOutput(out, 501)
	want := []otherProc{
		{Pid: 1, UID: 0, CPU: 0.3, Memory: 12288 * 1024, Path: "/sbin/launchd"},
		{Pid: 414, UID: 88, CPU: 40.2, Memory: 138240 * 1024, Path: "/System/Library/PrivateFrameworks/SkyLight.framework/Resources/WindowServer"},
		{Pid: 7045, UID: 0, CPU: 10.8, Memory: 53872 * 1024, Path: "/Applications/Microsoft Defender.app/Contents/MacOS/wdavdaemon"},
		{Pid: 0, UID: 0, CPU: 5.0, Memory: 0, Path: "kernel_task"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d processes, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("process %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// testSystemPath stands in for systemExecutable, so the grouping tests run
// on every platform.
func testSystemPath(p string) bool {
	return strings.HasPrefix(p, "/System/") || strings.HasPrefix(p, "/usr/libexec/") || strings.HasPrefix(p, "/sbin/")
}

func noBundleIDs(string) string { return "" }

func rowsByKey(rows []ProcessRow) map[string]ProcessRow {
	out := make(map[string]ProcessRow, len(rows))
	for _, r := range rows {
		out[r.Key] = r
	}
	return out
}

func TestGroupProcessesCPUFromThePreviousReading(t *testing.T) {
	prev := snapshotProcs([]procReading{
		{Pid: 1, Path: "/usr/bin/yes", UserNs: 1e9, Start: 7},
		{Pid: 2, Path: "/opt/homebrew/bin/node", UserNs: 5e9, SysNs: 1e9, Start: 8},
		{Pid: 3, Path: "/bin/zsh", UserNs: 3e9, Start: 9},
	})
	cur := snapshotProcs([]procReading{
		{Pid: 1, Path: "/usr/bin/yes", UserNs: 3e9, Start: 7},            // 2 s of CPU in 2 s: 100%
		{Pid: 2, Path: "/opt/homebrew/bin/node", UserNs: 1e9, Start: 99}, // pid reused: none this round
		{Pid: 3, Path: "/bin/zsh", UserNs: 2e9, Start: 9},                // counters went back: none
		{Pid: 4, Path: "/usr/bin/top", UserNs: 9e9, Start: 10},           // new: none
	})
	rows := groupProcesses(prev, cur, nil, nil, func(p int32) int32 { return p }, testSystemPath, noBundleIDs, 2*time.Second)
	got := rowsByKey(rows)
	if c := got["cli:yes"].CPU; c < 99.9 || c > 100.1 {
		t.Errorf("yes CPU = %v, want 100", c)
	}
	for _, k := range []string{"cli:node", "cli:zsh", "cli:top"} {
		if got[k].CPU != 0 {
			t.Errorf("%s CPU = %v, want 0", k, got[k].CPU)
		}
	}
}

// setProcessTiming shrinks the loop's waits and returns the restore.
func setProcessTiming(first, interval time.Duration) func() {
	f, i := processFirstDelay, processInterval
	processFirstDelay, processInterval = first, interval
	return func() { processFirstDelay, processInterval = f, i }
}

// fakeProcessSource counts ps runs; its CPU is always half busy.
func fakeProcessSource(ps *atomic.Int32) processSource {
	var ticks atomic.Uint64
	return processSource{
		procs: func() []procReading { return []procReading{{Pid: 1, Path: "/usr/bin/yes", Start: 1}} },
		others: func(context.Context) []otherProc {
			ps.Add(1)
			return []otherProc{{Pid: 9, CPU: 1, Memory: 1, Path: "/sbin/launchd"}}
		},
		apps:        func() []workspaceApp { return nil },
		responsible: func(p int32) int32 { return p },
		system:      testSystemPath,
		bundleID:    noBundleIDs,
		pressure:    func() uint64 { return 2 },
		cpu: func() (uint64, uint64) {
			n := ticks.Add(10)
			return n / 2, n
		},
	}
}

func TestWatchProcessesRunsPsAtTheStartAndEveryThirdSample(t *testing.T) {
	defer setProcessTiming(time.Millisecond, time.Millisecond)()
	var ps atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var samples []ProcessSample
	err := watchProcessesWith(ctx, func(s ProcessSample) error {
		samples = append(samples, s)
		if len(samples) == 7 {
			cancel()
		}
		return nil
	}, fakeProcessSource(&ps))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(samples) != 7 {
		t.Fatalf("got %d samples, want 7", len(samples))
	}
	if n := ps.Load(); n != 3 {
		t.Errorf("ps ran %d times for 7 samples, want 3 (start, 4th, 7th)", n)
	}
	s := samples[0]
	if s.Own != 1 || s.Others != 1 || s.Pressure != 2 || len(s.Rows) != 2 {
		t.Errorf("first sample = %+v", s)
	}
	if s.CPU < 49.9 || s.CPU > 50.1 {
		t.Errorf("CPU = %v, want 50", s.CPU)
	}
	n := ps.Load()
	time.Sleep(20 * time.Millisecond)
	if ps.Load() != n {
		t.Error("ps ran after the watch returned")
	}
}

func TestWatchProcessesStopsWhenSendFails(t *testing.T) {
	defer setProcessTiming(time.Millisecond, time.Millisecond)()
	var ps atomic.Int32
	closed := errors.New("channel closed")
	err := watchProcessesWith(context.Background(), func(ProcessSample) error { return closed }, fakeProcessSource(&ps))
	if !errors.Is(err, closed) {
		t.Fatalf("err = %v, want the send error", err)
	}
	if n := ps.Load(); n != 1 {
		t.Errorf("ps ran %d times, want only the first", n)
	}
}

func TestWatchProcessesStopsDuringTheFirstWait(t *testing.T) {
	defer setProcessTiming(time.Hour, time.Millisecond)()
	var ps atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sent := false
	err := watchProcessesWith(ctx, func(ProcessSample) error { sent = true; return nil }, fakeProcessSource(&ps))
	if !errors.Is(err, context.Canceled) || sent {
		t.Fatalf("err = %v, sent = %v; want canceled before any sample", err, sent)
	}
}
```

Then create `process_darwin_test.go`. Like `monitor_darwin_test.go`, it holds the tests with `.app` paths, because `outermostBundle` looks for `.app` plus the platform separator and so only matches these paths on macOS. It also holds the opt-in CPU budget check that Step 9 runs.

```go
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
```

- [ ] **Step 2: Run the tests and see them fail**

Run: `go test -count=1 -run 'ParsePs|GroupProcesses|WatchProcesses' .`
Expected: build failure. `parsePsOutput`, `otherProc`, `snapshotProcs`, `groupProcesses`, `ProcessRow`, `processSource` and the other new names are undefined.

- [ ] **Step 3: Create `process.go`**

```go
package main

// process.go holds the process page's rules: the rows the page shows, how
// processes are grouped into rows, the sampling loop and the force quit
// binding. System reads come in through processSource, so the tests run on
// any platform. The status page's own sampling in monitor.go is unchanged.

import (
	"bufio"
	"bytes"
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// ---- types ----------------------------------------------------------------

// ProcessRow is one line of the process page's CPU and memory tabs: an
// app's processes, a command-line program's processes or a system
// program's processes, summed up. CPU is a single-core percentage like
// Activity Monitor's, so it can exceed 100.
type ProcessRow struct {
	Key       string         `json:"key"`       // "app:<bundle path>" lower-cased, "cli:<name>" or "sys:<name>"
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`      // "app", "cli" or "system"
	Path      string         `json:"path"`      // the .app; "" for cli and system rows
	BundleID  string         `json:"bundleId"`  // lower-cased; "" when unknown
	CPU       float64        `json:"cpu"`
	Memory    uint64         `json:"memory"`
	Procs     int            `json:"procs"`
	Estimated bool           `json:"estimated"` // some process was read through ps
	Quit      bool           `json:"quit"`      // a Dock app of this user that may be quit
	Children  []ProcessChild `json:"children"`  // by name, same-name processes merged
}

// ProcessChild is one name inside a row: its processes summed up.
type ProcessChild struct {
	Name      string  `json:"name"`
	Count     int     `json:"count"`
	CPU       float64 `json:"cpu"`
	Memory    uint64  `json:"memory"`
	Estimated bool    `json:"estimated"`
}

// ProcessSample is one reading of the process page.
type ProcessSample struct {
	Rows     []ProcessRow `json:"rows"`     // ordered by key; the page sorts
	CPU      float64      `json:"cpu"`      // the whole machine, 0–100
	Pressure uint64       `json:"pressure"` // kern.memorystatus_vm_pressure_level: 1 normal, 2 warning, 4 critical, 0 unknown
	Own      int          `json:"own"`      // this user's processes, read exactly
	Others   int          `json:"others"`   // other accounts' processes, estimated from ps
}

// otherProc is another account's process as ps reports it.
type otherProc struct {
	Pid    int32
	UID    uint32
	CPU    float64 // ps %cpu, a decaying average
	Memory uint64  // resident size in bytes
	Path   string  // the executable's full path, or a bare name like kernel_task
}

// processSource is everything the loop reads, injectable for tests.
type processSource struct {
	procs       func() []procReading                  // this user's processes, exact
	others      func(ctx context.Context) []otherProc // other accounts' processes, from ps
	apps        func() []workspaceApp
	responsible func(pid int32) int32
	system      func(path string) bool      // ships with the OS: daemons, agents, framework services
	bundleID    func(bundle string) string  // CFBundleIdentifier of an .app
	pressure    func() uint64
	cpu         func() (busy, total uint64) // cumulative ticks
}

// Timing knobs, variables so tests can shrink them.
var (
	processFirstDelay  = time.Second     // the first sample's CPU interval
	processInterval    = 2 * time.Second // one sample
	processOthersEvery = 3               // ps runs on every third sample, about every six seconds
)

// ---- bound methods --------------------------------------------------------

// WatchProcesses streams one ProcessSample every two seconds while the
// process page shows its CPU or memory tab and is not paused; the page
// closes the channel otherwise. The first sample comes after about a
// second, so CPU is measured over a real interval. Other accounts'
// processes are read through ps about every six seconds, and nothing is
// read after the channel closes. Off macOS it fails with "unsupported".
func (Cleaner) WatchProcesses(ctx context.Context, ch *mygo.Channel[ProcessSample]) error {
	if !processSupported() {
		return codeError{Code: "unsupported"}
	}
	return watchProcessesWith(ctx, ch.Send, realProcessSource())
}

// ForceQuitApp force-terminates the Dock apps at path, like Force Quit in
// the Dock menu: unsaved work is lost. The process page offers it only
// after QuitApp left the app running and the user confirmed. Matching,
// exclusions (Finder, Cleanzo) and results are QuitApp's.
func (Cleaner) ForceQuitApp(path string) (bool, error) {
	return quitAppWith(path, workspaceList, workspaceForceTerminate)
}

// ---- the sampling loop ----------------------------------------------------

// watchProcessesWith runs the loop against an injected sender and source.
func watchProcessesWith(ctx context.Context, send func(ProcessSample) error, src processSource) error {
	prev := snapshotProcs(src.procs())
	prevBusy, prevTotal := src.cpu()
	prevAt := time.Now()
	others := src.others(ctx)
	if err := sleepCtx(ctx, processFirstDelay); err != nil {
		return err
	}
	// One Info.plist read per app for the whole watch.
	ids := map[string]string{}
	bundleID := func(bundle string) string {
		id, ok := ids[bundle]
		if !ok {
			id = src.bundleID(bundle)
			ids[bundle] = id
		}
		return id
	}
	tick := time.NewTicker(processInterval)
	defer tick.Stop()
	for n := 0; ; n++ {
		if n > 0 && n%processOthersEvery == 0 {
			others = src.others(ctx)
		}
		elapsed := time.Since(prevAt)
		cur := snapshotProcs(src.procs())
		busy, total := src.cpu()
		prevAt = time.Now()
		sample := ProcessSample{
			Rows:     groupProcesses(prev, cur, others, src.apps(), src.responsible, src.system, bundleID, elapsed),
			Pressure: src.pressure(),
			Own:      len(cur),
			Others:   len(others),
		}
		if total > prevTotal && prevTotal > 0 && busy >= prevBusy {
			sample.CPU = clampPct(float64(busy-prevBusy) / float64(total-prevTotal) * 100)
		}
		if err := send(sample); err != nil {
			return err
		}
		prev, prevBusy, prevTotal = cur, busy, total
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// snapshotProcs keys this user's readings by pid for the CPU deltas.
func snapshotProcs(procs []procReading) map[int32]procReading {
	out := make(map[int32]procReading, len(procs))
	for _, r := range procs {
		out[r.Pid] = r
	}
	return out
}

// ---- grouping -------------------------------------------------------------

// processGroup accumulates one row while grouping.
type processGroup struct {
	row      ProcessRow
	children map[string]*ProcessChild
}

// groupProcesses turns one reading into the page's rows:
//   - a process inside an .app belongs to that app's row, whatever account
//     runs it (Microsoft Defender's root daemon joins Microsoft Defender);
//   - this user's process that ships with the OS or is an XPC service, and
//     whose responsible process is inside an .app, works for that app and
//     joins its row (WebKit's web content processes);
//   - this user's other processes that ship with the OS get a system row by
//     name, and everything else gets a command-line row by name: a node or
//     yes started in Terminal is its own row, not Terminal's;
//   - another account's process outside any .app gets a system row by name.
//
// This user's CPU is the change since prev over elapsed; a new or reused
// pid counts none this round. Other accounts' numbers are ps's, marked
// estimated. Rows come ordered by key, children by name.
func groupProcesses(prev, cur map[int32]procReading, others []otherProc, apps []workspaceApp, responsible func(int32) int32, system func(string) bool, bundleID func(string) string, elapsed time.Duration) []ProcessRow {
	groups := map[string]*processGroup{}
	add := func(key, kind, bundle, name string, cpu float64, memory uint64, estimated bool) {
		g := groups[key]
		if g == nil {
			g = &processGroup{row: ProcessRow{Key: key, Kind: kind, Path: bundle}, children: map[string]*ProcessChild{}}
			groups[key] = g
		}
		g.row.CPU += cpu
		g.row.Memory += memory
		g.row.Procs++
		g.row.Estimated = g.row.Estimated || estimated
		c := g.children[name]
		if c == nil {
			c = &ProcessChild{Name: name}
			g.children[name] = c
		}
		c.Count++
		c.CPU += cpu
		c.Memory += memory
		c.Estimated = c.Estimated || estimated
	}
	for pid, r := range cur {
		cpu := 0.0
		if p, ok := prev[pid]; ok && p.Start == r.Start && elapsed > 0 {
			if now, was := r.UserNs+r.SysNs, p.UserNs+p.SysNs; now > was {
				cpu = float64(now-was) / float64(elapsed) * 100
			}
		}
		key, kind, bundle := ownProcessKey(r, cur, responsible, system)
		add(key, kind, bundle, processName(r.Path, r.Pid), cpu, r.Footprint, false)
	}
	for _, o := range others {
		key, kind, bundle := otherProcessKey(o)
		add(key, kind, bundle, processName(o.Path, o.Pid), o.CPU, o.Memory, true)
	}
	rows := make([]ProcessRow, 0, len(groups))
	for _, g := range groups {
		row := g.row
		if row.Kind == "app" {
			row.Name = displayName(row.Key, row.Path, apps, nil)
			row.BundleID = strings.ToLower(bundleID(row.Path))
			row.Quit = canQuit(row.Path, apps)
		} else {
			row.Name = row.Key[strings.IndexByte(row.Key, ':')+1:]
		}
		row.Children = make([]ProcessChild, 0, len(g.children))
		for _, c := range g.children {
			row.Children = append(row.Children, *c)
		}
		sort.Slice(row.Children, func(i, j int) bool { return row.Children[i].Name < row.Children[j].Name })
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows
}

// ownProcessKey places one of this user's processes; see groupProcesses.
func ownProcessKey(r procReading, cur map[int32]procReading, responsible func(int32) int32, system func(string) bool) (key, kind, bundle string) {
	if b := outermostBundle(r.Path); b != "" {
		return "app:" + strings.ToLower(b), "app", b
	}
	sys := r.Path != "" && system(r.Path)
	if sys || strings.Contains(r.Path, ".xpc"+string(filepath.Separator)) {
		if rpid := responsible(r.Pid); rpid > 0 && rpid != r.Pid {
			if rp, ok := cur[rpid]; ok {
				if b := outermostBundle(rp.Path); b != "" {
					return "app:" + strings.ToLower(b), "app", b
				}
			}
		}
	}
	if sys {
		return "sys:" + processName(r.Path, r.Pid), "system", ""
	}
	return "cli:" + processName(r.Path, r.Pid), "cli", ""
}

// otherProcessKey places another account's process: its .app's row, else a
// system row by name.
func otherProcessKey(o otherProc) (key, kind, bundle string) {
	if b := outermostBundle(o.Path); b != "" {
		return "app:" + strings.ToLower(b), "app", b
	}
	return "sys:" + processName(o.Path, o.Pid), "system", ""
}

// processName is the executable's file name, or "pid N" when the path is
// unknown.
func processName(path string, pid int32) string {
	if path == "" {
		return "pid " + strconv.Itoa(int(pid))
	}
	return filepath.Base(path)
}

// ---- other accounts through ps --------------------------------------------

// parsePsOutput reads `ps -axww -o pid=,uid=,pcpu=,rss=,comm=` and keeps
// the processes of accounts other than self. The command is the last
// column and may contain spaces; malformed lines are skipped.
func parsePsOutput(out []byte, self uint32) []otherProc {
	var procs []otherProc
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		fields, path := psFields(sc.Text(), 4)
		if len(fields) < 4 || path == "" {
			continue
		}
		pid, errPid := strconv.ParseInt(fields[0], 10, 32)
		uid, errUID := strconv.ParseUint(fields[1], 10, 32)
		cpu, errCPU := strconv.ParseFloat(fields[2], 64)
		rss, errRSS := strconv.ParseUint(fields[3], 10, 64)
		if errPid != nil || errUID != nil || errCPU != nil || errRSS != nil || uint32(uid) == self {
			continue
		}
		procs = append(procs, otherProc{Pid: int32(pid), UID: uint32(uid), CPU: cpu, Memory: rss * 1024, Path: path})
	}
	return procs
}

// psFields splits off the first n whitespace-separated fields of a ps line
// and returns the rest with its inner spaces kept.
func psFields(line string, n int) ([]string, string) {
	fields := make([]string, 0, n)
	rest := strings.TrimLeft(line, " \t")
	for len(fields) < n && rest != "" {
		i := strings.IndexAny(rest, " \t")
		if i < 0 {
			return append(fields, rest), ""
		}
		fields = append(fields, rest[:i])
		rest = strings.TrimLeft(rest[i:], " \t")
	}
	return fields, strings.TrimRight(rest, " \t\r")
}
```

- [ ] **Step 4: Create `process_darwin.go`**

```go
package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// processSupported reports whether the process page can sample: macOS only
// in this version.
func processSupported() bool { return true }

// processPsTimeout bounds one ps run.
var processPsTimeout = 3 * time.Second

// realProcessSource wires the loop to the native reads: libproc for this
// user's processes, the setuid /bin/ps for everyone else's.
func realProcessSource() processSource {
	return processSource{
		procs:       nativeProcs,
		others:      readOtherProcs,
		apps:        workspaceList,
		responsible: nativeResponsiblePid,
		system:      systemExecutable,
		bundleID:    func(bundle string) string { return readAppIdentity(bundle).bid },
		pressure:    func() uint64 { return nativeSysctlUint64("kern.memorystatus_vm_pressure_level") },
		cpu:         cpuBusyTotal,
	}
}

// readOtherProcs runs ps, which is setuid root and so can report every
// account's processes; libproc refuses other accounts' (EPERM). The
// numbers are ps's, a decaying CPU average and resident memory, hence
// estimates. LC_ALL=C keeps the decimal point a dot.
func readOtherProcs(ctx context.Context) []otherProc {
	ctx, cancel := context.WithTimeout(ctx, processPsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/ps", "-axww", "-o", "pid=,uid=,pcpu=,rss=,comm=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if ctx.Err() != nil || (err != nil && len(out) == 0) {
		return nil
	}
	return parsePsOutput(out, uint32(os.Getuid()))
}

// systemExecutable reports programs that ship with macOS: daemons, agents
// and framework services. Command-line tools in /bin and /usr/bin are not
// counted, so a shell or yes started in Terminal gets a command-line row.
func systemExecutable(path string) bool {
	for _, prefix := range []string{"/System/", "/usr/libexec/", "/usr/sbin/", "/sbin/", "/Library/Apple/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Create `process_other.go`**

```go
//go:build !darwin

package main

import "context"

// processSupported is false off macOS: the page shows a note and
// WatchProcesses refuses.
func processSupported() bool { return false }

// realProcessSource reads nothing off macOS; WatchProcesses never uses it.
func realProcessSource() processSource {
	return processSource{
		procs:       func() []procReading { return nil },
		others:      func(context.Context) []otherProc { return nil },
		apps:        func() []workspaceApp { return nil },
		responsible: func(int32) int32 { return -1 },
		system:      func(string) bool { return false },
		bundleID:    func(string) string { return "" },
		pressure:    func() uint64 { return 0 },
		cpu:         func() (uint64, uint64) { return 0, 0 },
	}
}
```

- [ ] **Step 6: Share the CPU ticks read.** In `status_darwin.go`:
  - add `cpuBusyTotal` below `realStatusSource`;
  - replace the inline `cpu:` closure in `realStatusSource` with `cpu: cpuBusyTotal,`.

```go
// cpuBusyTotal is the whole CPU's cumulative busy (user+system+nice) and
// total ticks, or zeros when they cannot be read.
func cpuBusyTotal() (uint64, uint64) {
	t, ok := nativeCPUTicks()
	if !ok {
		return 0, 0
	}
	busy := uint64(t.Ticks[0]) + uint64(t.Ticks[1]) + uint64(t.Ticks[3]) // user+system+nice
	return busy, busy + uint64(t.Ticks[2])
}
```

- [ ] **Step 7: Force terminate.** In `native.go`, replace the whole `workspaceTerminate` function, keeping its doc comment, with:

```go
// workspaceTerminate asks one application to terminate, exactly like
// choosing Quit in the Dock. It never force-kills; an app that refuses (or
// has unsaved work) simply stays, and monitor.go decides the verdict.
func workspaceTerminate(pid int32) bool { return workspaceSend(pid, "terminate") }

// workspaceForceTerminate force-terminates one application, like Force Quit
// in the Dock menu: unsaved work is lost. Only ForceQuitApp calls it, after
// the user confirmed on the process page.
func workspaceForceTerminate(pid int32) bool { return workspaceSend(pid, "forceTerminate") }

// workspaceSend sends terminate or forceTerminate to the running
// application with pid, on the main thread. False when it is not running
// or refuses.
func workspaceSend(pid int32, selector string) bool {
	ok := false
	runOnMain(func() {
		if !appKitInit() {
			return
		}
		withPool(func() {
			ws := objc.ID(objc.GetClass("NSWorkspace")).Send(nsSel("sharedWorkspace"))
			apps := ws.Send(nsSel("runningApplications"))
			n := int(objc.Send[uint64](apps, nsSel("count")))
			for i := 0; i < n; i++ {
				app := apps.Send(nsSel("objectAtIndex:"), i)
				if int32(objc.Send[uint64](app, nsSel("processIdentifier"))) != pid {
					continue
				}
				ok = byte(app.Send(nsSel(selector))) != 0
				return
			}
		})
	})
	return ok
}
```

  In `native_other.go`, next to `workspaceTerminate`, add:

```go
func workspaceForceTerminate(pid int32) bool { return false }
```

  In `native_windows.go`, next to `workspaceTerminate`, add:

```go
// workspaceForceTerminate is never used on Windows: the process page is
// macOS only in this version.
func workspaceForceTerminate(pid int32) bool { return false }
```

- [ ] **Step 8: Run the tests and see them pass**

Run: `go test -count=1 -run 'ParsePs|GroupProcesses|WatchProcesses|BindingsArePlatformNeutral|SlashPath' .`
Expected: PASS.

- [ ] **Step 9: Check that the opt-in budget test skips by default.** It is in `process_darwin_test.go` (Step 1).

Run: `go test -count=1 -run TestProcessSamplingBudget -v .`
Expected: SKIP, with "set CLEANZO_PERF=1 …". The reviewer runs it with `CLEANZO_PERF=1` on the maintainer's Mac.

- [ ] **Step 10: Regenerate the bindings and check them**

Run: `bun run generate && bun run typecheck`
Expected:
- `src/mygo.ts` now has `export interface ProcessRow`, `ProcessChild` and `ProcessSample`;
- `Cleaner` has `watchProcesses(ch: Channel<ProcessSample>)` and `forceQuitApp(path: string)`;
- typecheck passes.

- [ ] **Step 11: Verify the whole Go side**

Run each command, then fix until all pass:
1. `gofmt -l .` prints nothing.
2. `go vet ./...`
3. `GOOS=windows GOARCH=amd64 go vet ./...`
4. `go test -race -count=1 ./...` (the executor's sandbox may need the `-skip` list in "Execution batches").

---

### Task 2: The growth page becomes a panel

The 磁盘占用 tab will render the growth list without its own page header. This task changes only `src/react/growth/` and keeps the 占用增长 view working: a temporary `GrowthPage` shell adds the header back, and Task 5 deletes it.

**Files:**
- Modify: `src/react/growth/GrowthPage.tsx`, `src/style.css` (next to `.cz-growth-status`), `tests/growth-react.test.ts`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `export interface GrowthPanelProps extends GrowthHandlers { active; visit; focusKey; focusNonce }`, the same fields `GrowthPageProps` had.
  - `export function GrowthPanel(props: GrowthPanelProps)`, which renders `null` while loading or when unsupported.
  - `GrowthHandlers` is unchanged.

- [ ] **Step 1: Point the tests at the panel.** In `tests/growth-react.test.ts`, inside `pageHarness`:
  - import `GrowthPanel` instead of `GrowthPage`;
  - render `GrowthPanel({ ...shown, focusKey, focusNonce: 1, goLeftovers() {}, goUninstall() {} })`;
  - replace the `text()` method with a harness-level `textOf` and add `has` and `statusLine`:

```ts
  /** 一个节点里所有的文字（含按钮）。 */
  const textOf = (node: React.ReactNode): string => Array.isArray(node) ? node.map(textOf).join("") :
    React.isValidElement<{ children?: React.ReactNode }>(node) ? textOf(node.props.children) :
    typeof node === "string" || typeof node === "number" ? String(node) : "";
```

```ts
    text() { return textOf(tree); },
    /** 有没有 className 正好是 cls 的元素。 */
    has(cls: string) { return nodes(tree).some((el) => el.props.className === cls); },
    /** 状态那一行的文字（含「现在量一次」）。 */
    statusLine() {
      const el = nodes(tree).find((n) => n.props.className === "cz-growth-statusline");
      return el ? textOf(el) : "";
    },
```

  Then add this test after the test 「没有记录时「现在量一次」只在空状态里…」:

```ts
test("面板没有页头；有记录时「现在量一次」在状态那一行里", async () => {
  const page = await pageHarness();
  try {
    await page.settle();
    page.requests[0]!.resolve(state({ records: 2, apps: [app({})] }));
    await page.settle();
    expect(page.has("view-head")).toBe(false);
    expect(page.statusLine()).toContain(t("growth.measureNow"));
  } finally { page.close(); }
});
```

- [ ] **Step 2: Run and see it fail**

Run: `bun test tests/growth-react.test.ts`
Expected: FAIL, because `GrowthPanel` is not exported (`undefined is not a function`).

- [ ] **Step 3: Turn the page into the panel.** In `src/react/growth/GrowthPage.tsx`:

  1. Rename `GrowthPageProps` to `GrowthPanelProps`, with the doc comment `/** 占用增长面板的显示状态、访问次数、定位请求和导航回调。 */`.
  2. Rename `GrowthPage` to `GrowthPanel`, with the doc comment `/** 按 App 汇总的占用、增长记录与目录明细。放在进程页的磁盘占用标签里，没有自己的页头。 */`. Its parameter type becomes `GrowthPanelProps`.
  3. Change `if (!state || view === "unsupported") return <div className="cz-react" />;` to `if (!state || view === "unsupported") return null;`.
  4. Replace the opening of the returned JSX, from `<div className="cz-react">` through `</header>`, with:

```tsx
    <div className="cz-growth-panel">
```

  5. Replace the status paragraph (`<p className="cz-growth-status muted">…</p>`) with this block. The paragraph's content stays as it is:

```tsx
      <div className="cz-growth-statusline">
        <p className="cz-growth-status muted">
          {state.status.lastAt ? t("growth.status.last", { time: fmtTime(state.status.lastAt) }) : t("growth.status.never")}
          {state.config.enabled && state.status.nextAt > 0 && <> · {t("growth.status.next", { time: fmtTime(state.status.nextAt) })}</>}
        </p>
        {/* 还没有记录时，入口是空状态里的大按钮，这里不再放一个。 */}
        {view !== "empty" && (
          <Button size="sm" onClick={() => void measure()} disabled={state.status.running}>
            <RefreshCw size={14} className={state.status.running ? "animate-spin" : undefined} />
            {state.status.running ? t("growth.measuring") : t("growth.measureNow")}
          </Button>
        )}
      </div>
```

  6. Append the temporary shell at the end of the file. `mount.tsx` keeps importing `GrowthPage`, so the 占用增长 view works until Task 5:

```tsx
/** 独立的「占用增长」页：页头 + 面板。进程页接好后（Task 5），连同 entry.ts、mount.tsx 一起删掉。 */
export function GrowthPage(props: GrowthPanelProps) {
  const t = useT();
  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h2>{t("nav.growth")}</h2>
          <p className="muted">{t("growth.sub")}</p>
        </div>
      </header>
      <GrowthPanel {...props} />
    </div>
  );
}
```

  In `src/style.css`, right after the rule `.cz-growth-status, .cz-growth-note { … }`, add:

```css
.cz-growth-statusline { display: flex; align-items: center; gap: 12px; margin: 0 0 8px; }
.cz-growth-statusline .cz-growth-status { flex: 1; min-width: 0; margin: 0; }
```

- [ ] **Step 4: Run and see it pass**

Run: `bun test tests/growth-react.test.ts && bun run typecheck && bun run test:web`
Expected: PASS, including every test that existed before.

---

### Task 3: The page's logic, API and mock

**Files:**
- Create: `src/react/process/format.ts`, `src/react/process/api.ts`, `tests/process-react.test.ts`
- Modify: `src/dev/mock-mygo.ts`, `package.json` (`test:web`)

**Interfaces:**
- **Consumes:** from Task 1's bindings, `ProcessRow`, `ProcessChild`, `ProcessSample`, `Cleaner.watchProcesses` and `Cleaner.forceQuitApp`; the existing `GrowthSummary` and `Cleaner.quitApp`.
- **Produces** (`src/react/process/format.ts`):
  - `type ProcessTab = "cpu" | "mem" | "disk"`
  - `interface ProcessRequest { tab: ProcessTab | null; focusKey: string | null; nonce: number }`
  - `PROCESS_DIAG`
  - `sortRows(rows, by)`, `sortChildren(children, by)`, `refreshFrozen(frozen, sample)`, `pushCpu(hist, v)`
  - `interface Diagnosis { kind: "mem" | "cpu"; top: ProcessRow | null }`, `diagnose(sample, cpuHist)`
  - `shouldWatch({ active, tab, running, hidden })`, `est(text, estimated)`
  - `interface GrowthTag { key; window: "day" | "week"; bytes; atLeast }`, `growthTags(summary)`, `rowTag(row, tags)`
  - `pruneStuck(stuck, rows)`
- **Produces** (`src/react/process/api.ts`): `processApi.watch(ch)`, `processApi.quitApp(path)`, `processApi.forceQuitApp(path)`

- [ ] **Step 1: Write the failing tests.** Create `tests/process-react.test.ts`:

```ts
import { expect, test } from "bun:test";
import type { GrowthSummary, ProcessRow, ProcessSample } from "../src/mygo";

// i18n 解析读 localStorage，这里钉住中文。
const storage = new Map([["cleanzo.lang", "zh"]]);
Object.assign(globalThis, {
  localStorage: { getItem: (k: string) => storage.get(k), setItem: (k: string, v: string) => storage.set(k, v) },
});

const {
  PROCESS_DIAG, diagnose, est, growthTags, pruneStuck, pushCpu, refreshFrozen, rowTag, shouldWatch, sortChildren, sortRows,
} = await import("../src/react/process/format");

const GB = 2 ** 30;

function row(p: Partial<ProcessRow> & { key: string }): ProcessRow {
  return { name: p.key, kind: "app", path: "", bundleId: "", cpu: 0, memory: 0, procs: 1, estimated: false, quit: false, children: [], ...p };
}

function sample(p: Partial<ProcessSample> = {}): ProcessSample {
  return { rows: [], cpu: 0, pressure: 1, own: 0, others: 0, ...p };
}

const chrome = row({ key: "app:/applications/google chrome.app", name: "Google Chrome", path: "/Applications/Google Chrome.app", bundleId: "com.google.chrome", cpu: 53.8, memory: 31.5 * GB, quit: true });
const ws = row({ key: "sys:WindowServer", name: "WindowServer", kind: "system", cpu: 40.2, memory: 0.13 * GB, estimated: true });
const node = row({ key: "cli:node", name: "node", kind: "cli", cpu: 12.6, memory: 2.9 * GB });
const lark = row({ key: "app:/applications/lark.app", name: "Lark", path: "/Applications/Lark.app", bundleId: "com.electron.lark", cpu: 21.4, memory: 4.28 * GB, quit: true });

test("sortRows: CPU or memory high to low, ties by name, without touching the sample", () => {
  const rows = [node, ws, chrome, lark];
  expect(sortRows(rows, "cpu").map((r) => r.name)).toEqual(["Google Chrome", "WindowServer", "Lark", "node"]);
  expect(sortRows(rows, "mem").map((r) => r.name)).toEqual(["Google Chrome", "Lark", "node", "WindowServer"]);
  const tie = [row({ key: "b", name: "b", cpu: 1 }), row({ key: "a", name: "a", cpu: 1 })];
  expect(sortRows(tie, "cpu").map((r) => r.name)).toEqual(["a", "b"]);
  expect(rows.map((r) => r.name)).toEqual(["node", "WindowServer", "Google Chrome", "Lark"]);
});

test("sortChildren follows the tab's order", () => {
  const kids = [
    { name: "Helper", count: 25, cpu: 5.5, memory: 5.3 * GB, estimated: false },
    { name: "Renderer", count: 61, cpu: 38.2, memory: 24.1 * GB, estimated: false },
    { name: "GPU", count: 1, cpu: 6.1, memory: 1.2 * GB, estimated: false },
  ];
  expect(sortChildren(kids, "cpu").map((c) => c.name)).toEqual(["Renderer", "GPU", "Helper"]);
  expect(sortChildren(kids, "mem").map((c) => c.name)).toEqual(["Renderer", "Helper", "GPU"]);
});

test("refreshFrozen keeps the rows and their order, updates numbers by key, keeps rows that left", () => {
  const rows = refreshFrozen([chrome, node], sample({ rows: [{ ...node, cpu: 90 }, ws] }));
  expect(rows.map((r) => r.key)).toEqual([chrome.key, node.key]);
  expect(rows[1]!.cpu).toBe(90);
  expect(rows[0]).toBe(chrome);
});

test("diagnose: memory pressure names the biggest memory user", () => {
  for (const pressure of [2, 4]) {
    expect(diagnose(sample({ pressure, rows: [node, chrome, ws] }), [])).toEqual({ kind: "mem", top: chrome });
  }
  expect(diagnose(sample({ pressure: 1, rows: [chrome] }), [])).toBeNull();
  expect(diagnose(null, [99, 99, 99])).toBeNull();
});

test("diagnose: CPU only after three readings at 80% or more, and memory wins", () => {
  const s = sample({ rows: [node, ws, chrome] });
  expect(diagnose(s, [80, 85, 99])).toEqual({ kind: "cpu", top: chrome });
  expect(diagnose(s, [85, 99])).toBeNull();
  expect(diagnose(s, [79.9, 99, 99])).toBeNull();
  expect(diagnose({ ...s, pressure: 2 }, [99, 99, 99])!.kind).toBe("mem");
  expect(diagnose(sample({ pressure: 4 }), [])).toEqual({ kind: "mem", top: null });
  expect(PROCESS_DIAG).toEqual({ cpuBusy: 80, cpuSamples: 3 });
});

test("pushCpu keeps the last three readings", () => {
  expect(pushCpu([1, 2, 3], 4)).toEqual([2, 3, 4]);
  expect(pushCpu([], 5)).toEqual([5]);
});

test("shouldWatch: only while shown, on CPU or memory, running and visible", () => {
  const on = { active: true, tab: "cpu" as const, running: true, hidden: false };
  expect(shouldWatch(on)).toBe(true);
  expect(shouldWatch({ ...on, tab: "mem" })).toBe(true);
  expect(shouldWatch({ ...on, tab: "disk" })).toBe(false);
  expect(shouldWatch({ ...on, active: false })).toBe(false);
  expect(shouldWatch({ ...on, running: false })).toBe(false);
  expect(shouldWatch({ ...on, hidden: true })).toBe(false);
});

test("est marks estimates but never a dash", () => {
  expect(est("40%", true)).toBe("≈ 40%");
  expect(est("40%", false)).toBe("40%");
  expect(est("—", true)).toBe("—");
});

function summary(p: Partial<GrowthSummary> = {}): GrowthSummary {
  return {
    supported: true,
    config: { enabled: true, openAtLogin: false, dailyGB: 2, weeklyGB: 10, ignored: [], asked: true },
    status: { running: false, lastAt: 0, nextAt: 0, fda: true, notifications: "allowed" },
    alerts: [], ...p,
  };
}
const alert = (key: string, bytes: number, window = "week") =>
  ({ key, name: key, window, days: 7, bytes, atLeast: false, folder: "", total: 0, at: 0 });

test("growthTags: the biggest alert per owner, without ignored ones, only while monitoring", () => {
  const s = summary({ alerts: [
    alert("app:com.electron.lark", 11 * GB), alert("app:com.electron.lark", 3 * GB, "day"),
    alert("app:com.youqu.todesk.mac", 5 * GB, "day"), alert("dir:/users/me/.npm", GB),
  ] });
  const tags = growthTags(s);
  expect([...tags.keys()].sort()).toEqual(["app:com.electron.lark", "app:com.youqu.todesk.mac", "dir:/users/me/.npm"]);
  expect(tags.get("app:com.electron.lark")).toEqual({ key: "app:com.electron.lark", window: "week", bytes: 11 * GB, atLeast: false });
  expect(tags.get("app:com.youqu.todesk.mac")!.window).toBe("day");
  expect(growthTags({ ...s, config: { ...s.config, ignored: ["app:com.youqu.todesk.mac"] } }).has("app:com.youqu.todesk.mac")).toBe(false);
  expect(growthTags({ ...s, config: { ...s.config, enabled: false } }).size).toBe(0);
  expect(growthTags({ ...s, supported: false }).size).toBe(0);
  expect(growthTags(null).size).toBe(0);
});

test("rowTag: only app rows, matched by bundle ID", () => {
  const tags = growthTags(summary({ alerts: [alert("app:com.electron.lark", 11 * GB)] }));
  expect(rowTag(lark, tags)?.bytes).toBe(11 * GB);
  expect(rowTag(chrome, tags)).toBeNull();
  expect(rowTag({ ...lark, kind: "system" }, tags)).toBeNull();
  expect(rowTag({ ...lark, bundleId: "" }, tags)).toBeNull();
});

test("pruneStuck drops apps that are gone and returns the same set when nothing changed", () => {
  expect([...pruneStuck(new Set([chrome.path, lark.path]), [chrome, node])]).toEqual([chrome.path]);
  const same = new Set([chrome.path]);
  expect(pruneStuck(same, [chrome])).toBe(same);
});
```

- [ ] **Step 2: Run and see it fail**

Run: `bun test tests/process-react.test.ts`
Expected: FAIL, because `../src/react/process/format` cannot be found.

- [ ] **Step 3: Create `src/react/process/format.ts`**

```ts
// 进程页的纯逻辑：排序、冻结、诊断、开不开采样、增长小标签、「强制退出」的对账。不依赖 DOM / React，可单测。
import type { GrowthSummary, ProcessChild, ProcessRow, ProcessSample } from "../../mygo";

/** 三个标签：CPU、内存是实时的进程表，磁盘占用是占用增长的列表。 */
export type ProcessTab = "cpu" | "mem" | "disk";

/** main.ts 的打开请求：tab 为 null 时按默认（CPU）；focusKey 是磁盘占用标签里要定位的行；nonce 变了才执行。 */
export interface ProcessRequest {
  tab: ProcessTab | null;
  focusKey: string | null;
  nonce: number;
}

/** 诊断条的 CPU 门槛：整机连续 3 次统计都在 80% 以上（spec §三）。 */
export const PROCESS_DIAG = { cpuBusy: 80, cpuSamples: 3 } as const;

/** 按 CPU 或内存从高到低，同值按名字；返回新数组，不改采样。 */
export function sortRows(rows: readonly ProcessRow[], by: "cpu" | "mem"): ProcessRow[] {
  return [...rows].sort((a, b) => (by === "cpu" ? b.cpu - a.cpu : b.memory - a.memory) || a.name.localeCompare(b.name));
}

/** 展开后的进程：和表一样按当前标签排。 */
export function sortChildren(children: readonly ProcessChild[], by: "cpu" | "mem"): ProcessChild[] {
  return [...children].sort((a, b) => (by === "cpu" ? b.cpu - a.cpu : b.memory - a.memory) || a.name.localeCompare(b.name));
}

/** 冻结期间（确认框开着、正在退出）行和顺序不变，按 key 换上新数字；这一轮没有的行停在上一轮。 */
export function refreshFrozen(frozen: readonly ProcessRow[], sample: ProcessSample): ProcessRow[] {
  const byKey = new Map(sample.rows.map((r) => [r.key, r]));
  return frozen.map((r) => byKey.get(r.key) ?? r);
}

/** 追加一次整机 CPU，只留最近 3 次。 */
export function pushCpu(hist: readonly number[], v: number): number[] {
  return [...hist, v].slice(-PROCESS_DIAG.cpuSamples);
}

/** 顶上的诊断条：卡在哪，点名占得最多的那一行（没有行时为 null）。 */
export interface Diagnosis {
  kind: "mem" | "cpu";
  top: ProcessRow | null;
}

/** 内存压力到警告（2）或严重（4）时说内存；否则整机 CPU 连续 3 次都在 80% 以上时说 CPU；两个都有只说内存。 */
export function diagnose(sample: ProcessSample | null, cpuHist: readonly number[]): Diagnosis | null {
  if (!sample) return null;
  if (sample.pressure === 2 || sample.pressure === 4) return { kind: "mem", top: sortRows(sample.rows, "mem")[0] ?? null };
  if (cpuHist.length >= PROCESS_DIAG.cpuSamples && cpuHist.every((v) => v >= PROCESS_DIAG.cpuBusy)) {
    return { kind: "cpu", top: sortRows(sample.rows, "cpu")[0] ?? null };
  }
  return null;
}

/** 开采样通道的条件（spec §三）：页面显示、在 CPU 或内存标签、没暂停、窗口没隐藏。 */
export function shouldWatch(o: { active: boolean; tab: ProcessTab; running: boolean; hidden: boolean }): boolean {
  return o.active && o.tab !== "disk" && o.running && !o.hidden;
}

/** 估算值前面带「≈ 」；读不到的「—」不带。 */
export function est(text: string, estimated: boolean): string {
  return estimated && text !== "—" ? `≈ ${text}` : text;
}

/** 一个 owner 的增长提醒，取最大的一条。 */
export interface GrowthTag {
  key: string;
  window: "day" | "week";
  bytes: number;
  atLeast: boolean;
}

/**
 * 近 7 天涨过提醒线的 owner：没被忽略的提醒按 key 取最大的一条；不支持或监控关着时没有。
 * 磁盘占用标签上的数字就是它的个数。口径和状态页的增长提示一样，只是那里另外去掉了崩溃转储提示已经提过的 Crashpad。
 */
export function growthTags(s: GrowthSummary | null): Map<string, GrowthTag> {
  const out = new Map<string, GrowthTag>();
  if (!s || !s.supported || !s.config.enabled) return out;
  const ignored = new Set(s.config.ignored);
  for (const a of s.alerts) {
    if (ignored.has(a.key)) continue;
    const prev = out.get(a.key);
    if (!prev || a.bytes > prev.bytes) {
      out.set(a.key, { key: a.key, window: a.window === "day" ? "day" : "week", bytes: a.bytes, atLeast: a.atLeast });
    }
  }
  return out;
}

/** 进程行上的增长小标签：只给 App 行，按 bundle ID 对上 app:<bundle id>。 */
export function rowTag(row: ProcessRow, tags: ReadonlyMap<string, GrowthTag>): GrowthTag | null {
  return row.kind === "app" && row.bundleId ? tags.get(`app:${row.bundleId}`) ?? null : null;
}

/** 「强制退出」只留给还在跑的 App：这一轮已经没有这个 .app 的行就去掉；没变时返回原集合。 */
export function pruneStuck(stuck: ReadonlySet<string>, rows: readonly ProcessRow[]): ReadonlySet<string> {
  if (stuck.size === 0) return stuck;
  const present = new Set(rows.map((r) => r.path));
  const next = new Set([...stuck].filter((p) => present.has(p)));
  return next.size === stuck.size ? stuck : next;
}
```

- [ ] **Step 4: Create `src/react/process/api.ts`**

```ts
import { Cleaner } from "../../mygo";
import type { ProcessSample } from "../../mygo";

/** 进程页用到的 Cleaner 调用集中在此，组件不直接碰桥。 */
export const processApi = {
  /** 每 2 秒一条采样（第一条约 1 秒后）；Channel 关闭即 Go 侧取消 ctx，不再读进程、不再调用 ps。 */
  watch: (ch: import("mygo-runtime").Channel<ProcessSample>): Promise<void> => Cleaner.watchProcesses(ch),
  /** 正常退出；false = 5 秒后还在运行。 */
  quitApp: (path: string): Promise<boolean> => Cleaner.quitApp(path),
  /** 强制退出：只在正常退出没成功、用户确认后调用。 */
  forceQuitApp: (path: string): Promise<boolean> => Cleaner.forceQuitApp(path),
};
```

- [ ] **Step 5: Mock the stream for the browser preview.** In `src/dev/mock-mygo.ts`:
  - Add `ProcessRow, ProcessSample` to the `import type { … } from "../mygo"` list at the top.
  - Right after `const quitted = new Set<string>();`, add the generator below. ToDesk's bundle ID matches the mock growth alert `app:com.youqu.todesk.mac`, so its row shows the disk tag, but only if the mock's `growthConfig.enabled` is true; leave the mock config as it is.

```ts
  // 进程页：数字每次抖一点；ToDesk 的 bundle ID 对上 growthSummary 里的提醒，挂「磁盘一天」小标签；系统进程是估算值。
  const jitter = (v: number) => Math.max(0, v * (0.85 + Math.random() * 0.3));
  const procRow = (p: Partial<ProcessRow> & Pick<ProcessRow, "key" | "name">): ProcessRow => {
    const r: ProcessRow = { kind: "app", path: "", bundleId: "", cpu: 0, memory: 0, procs: 1, estimated: false, quit: false, children: [], ...p };
    return r.children.length ? r : { ...r, children: [{ name: r.name, count: r.procs, cpu: r.cpu, memory: r.memory, estimated: r.estimated }] };
  };
  const processSample = (): ProcessSample => ({
    cpu: jitter(62), pressure: 2, own: 454, others: 285,
    rows: [
      procRow({
        key: "app:/applications/google chrome.app", name: "Google Chrome", path: "/Applications/Google Chrome.app", bundleId: "com.google.chrome",
        cpu: jitter(53.8), memory: 31.5 * GB, procs: 88, quit: true, children: [
          { name: "Google Chrome", count: 1, cpu: jitter(4), memory: 0.9 * GB, estimated: false },
          { name: "Google Chrome Helper", count: 25, cpu: jitter(5.5), memory: 5.3 * GB, estimated: false },
          { name: "Google Chrome Helper (GPU)", count: 1, cpu: jitter(6.1), memory: 1.2 * GB, estimated: false },
          { name: "Google Chrome Helper (Renderer)", count: 61, cpu: jitter(38.2), memory: 24.1 * GB, estimated: false },
        ],
      }),
      procRow({ key: "app:/applications/doubaowork.app", name: "DoubaoWork", path: "/Applications/DoubaoWork.app", cpu: jitter(40.9), memory: 6.96 * GB, procs: 38, quit: true }),
      procRow({ key: "sys:WindowServer", name: "WindowServer", kind: "system", cpu: jitter(40.2), memory: 135 * MB, estimated: true }),
      procRow({ key: "app:/applications/todesk.app", name: "ToDesk", path: "/Applications/ToDesk.app", bundleId: "com.youqu.todesk.mac", cpu: jitter(3.1), memory: 180 * MB, procs: 4, quit: true }),
      procRow({ key: "cli:node", name: "node", kind: "cli", cpu: jitter(12.6), memory: 2.9 * GB, procs: 8 }),
      procRow({ key: "sys:mdworker_shared", name: "mdworker_shared", kind: "system", cpu: jitter(1.2), memory: 60 * MB, procs: 6 }),
    ].filter((r) => !quitted.has(r.path)),
  });
```

  Then, in the methods map next to `"Cleaner.WatchStatus"`:
  - replace the `"Cleaner.QuitApp"` entry;
  - add the two entries below.

  The mock's DoubaoWork never quits normally, so the preview can show 「强制退出」:

```ts
    "Cleaner.QuitApp": async (path) => {
      // DoubaoWork 正常退出退不掉，用来看进程页的「强制退出」。
      if (path === "/Applications/DoubaoWork.app") { await wait(5000); return false; }
      await wait(700); quitted.add(path); return true;
    },
    "Cleaner.ForceQuitApp": async (path) => { await wait(500); quitted.add(path); return true; },
    "Cleaner.WatchProcesses": async (ch) => stream(ch, async (emit) => {
      await wait(1000);
      for (;;) { await emit(processSample()); await wait(2000); }
    }),
```

- [ ] **Step 6: Add the test file to `test:web`.** In `package.json`, append ` && bun test tests/process-react.test.ts` to the end of the `test:web` script.

- [ ] **Step 7: Run and see it pass**

Run: `bun test tests/process-react.test.ts && bun run typecheck && bun run test:web`
Expected: PASS.

---

### Task 4: The page

**Files:**
- Create: `src/ui/components/seg.ts`, `src/react/process/ProcessPage.tsx`, `src/react/process/entry.ts`, `src/react/process/mount.tsx`
- Modify: `src/react/status/StatusPage.tsx` (only to import `segTrigger`), `src/i18n.ts`, `src/style.css`, `tests/process-react.test.ts`

**Interfaces:**
- **Consumes:**
  - Task 2's `GrowthPanel` and `GrowthHandlers`.
  - Task 3's `format.ts` and `processApi`.
  - Existing helpers:
    - from `status/format`: `fmtMem`, `fmtPct`, `iconColor`, `emptyQuit`, `syncQuitRows`, `quitErrCode`, `QuitState`;
    - `growthApi.summary` and `growthApi.onUpdated`;
    - `useIndicator`, `Collapse`, the `Dialog` parts, `Button`, `toast`, `errText`, `human`, `isWindows`.
- **Produces:**
  - `export interface ProcessRowViewProps`, `export function ProcessRowView`
  - `export interface ProcessPageProps extends GrowthHandlers { active; visit; request: ProcessRequest }`, `export function ProcessPage`
  - `entry.ts`: `setProcessHandlers(h)`, `showReactProcess(active)`, `openReactProcess(tab, focusKey?)`
  - `mount.tsx`: `requestProcess(tab, focusKey)`, `mountProcess(el, handlers, active)`
  - `src/ui/components/seg.ts`: `segTrigger`

- [ ] **Step 1: Write the failing render tests.** First add `import type { ProcessRowViewProps } from "../src/react/process/ProcessPage";` to the imports at the top of `tests/process-react.test.ts`. Then append:

```ts
/** 渲染一行的静态 HTML；setup-dom 要先于 shared.ts 装好。 */
async function rowHtml(p: Partial<ProcessRowViewProps> & { row: ProcessRow }): Promise<string> {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { ProcessRowView } = await import("../src/react/process/ProcessPage");
  const { t } = await import("../src/i18n");
  return renderToStaticMarkup(createElement(ProcessRowView, {
    sortBy: "cpu", open: false, tag: null, busy: false, stuck: false, done: false, t,
    onToggle() {}, onQuit() {}, onForce() {}, onTag() {}, ...p,
  }));
}

test("rows: a system process is marked, its numbers are estimates, and it has no buttons", async () => {
  const { t } = await import("../src/i18n");
  const html = await rowHtml({ row: ws });
  expect(html).toContain("WindowServer");
  expect(html).toContain(t("process.system"));
  expect(html).toContain("≈ 40%");
  expect(html).not.toContain("st-quit");
  expect(html).not.toContain("pr-force");
});

test("rows: an app quits; while it is still running it offers force quit; a quit row has no buttons", async () => {
  const { t } = await import("../src/i18n");
  const app = { ...chrome, procs: 62, children: [
    { name: "Google Chrome", count: 1, cpu: 4, memory: 0.9 * GB, estimated: false },
    { name: "Google Chrome Helper (Renderer)", count: 61, cpu: 38.2, memory: 24.1 * GB, estimated: false },
  ] };
  const idle = await rowHtml({ row: app });
  expect(idle).toContain("st-quit");
  expect(idle).not.toContain("pr-force");
  expect(idle).toContain(t("status.apps.procs", { n: 62 }));
  const stuck = await rowHtml({ row: app, stuck: true });
  expect(stuck).toContain(t("process.stuck"));
  expect(stuck).toContain("pr-force");
  expect(stuck).not.toContain("st-quit");
  const done = await rowHtml({ row: app, done: true });
  expect(done).not.toContain("st-quit");
  expect(done).not.toContain("pr-force");
  expect(await rowHtml({ row: app, open: true })).toContain("Google Chrome Helper (Renderer) × 61");
  expect(await rowHtml({ row: app, open: false })).not.toContain("× 61");
});

test("rows: a growth tag shows its window and size; command-line rows have no quit", async () => {
  const { t } = await import("../src/i18n");
  const { human } = await import("../src/shared");
  const tag = { key: "app:com.electron.lark", window: "week" as const, bytes: 11.2 * GB, atLeast: false };
  expect(await rowHtml({ row: lark, tag })).toContain(t("process.tag.week", { size: `+${human(tag.bytes)}` }));
  expect(await rowHtml({ row: node })).not.toContain("st-quit");
});
```

- [ ] **Step 2: Run and see it fail**

Run: `bun test tests/process-react.test.ts`
Expected: FAIL, because `../src/react/process/ProcessPage` cannot be found.

- [ ] **Step 3: Share the segmented control's item style.** Create `src/ui/components/seg.ts`:

```ts
/** 分段切换里每一项的样式（系统状态的「按内存 / 按 CPU」、进程页的三个标签）；滑块是 cz-seg-thumb + useIndicator。 */
export const segTrigger =
  "relative z-[1] cursor-pointer rounded-[6px] px-3 py-1 text-xs leading-[18px] text-[var(--muted)] transition-colors " +
  "data-[state=active]:font-semibold data-[state=active]:text-[var(--fg)] " +
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]";
```

  In `src/react/status/StatusPage.tsx`, delete the local `const segTrigger = …` (three lines) and add `import { segTrigger } from "../../ui/components/seg";` next to the other `../../ui/components` imports.

- [ ] **Step 4: Add the strings.** In `src/i18n.ts`, add these keys to both dictionaries:
  - `nav.process` goes right after `nav.growth`;
  - `status.apps.viewAll` goes right after `status.apps.empty`;
  - the `process.*` keys go right after `status.quit.ok`.

  Use these texts verbatim:

| key | zh | en |
|---|---|---|
| `nav.process` | 进程 | Processes |
| `status.apps.viewAll` | 查看全部 | View All |
| `process.sub` | 按 App 看谁在占 CPU、内存和磁盘 | See which apps take CPU, memory and disk |
| `process.tabs` | 查看 | View |
| `process.tab.cpu` | CPU | CPU |
| `process.tab.mem` | 内存 | Memory |
| `process.tab.disk` | 磁盘占用 | Disk Usage |
| `process.col.cpu` | CPU | CPU |
| `process.col.mem` | 内存 | Memory |
| `process.pause` | 暂停 | Pause |
| `process.resume` | 继续 | Resume |
| `process.paused` | 已暂停 | Paused |
| `process.system` | 系统 | System |
| `process.footnote` | 统计了你账户下的 {own} 个进程，以及系统和其他账户的 {others} 个进程。标「系统」的是 macOS 自带的程序，数字前带「≈」的是估算值。CPU 按单核算，可以超过 100%。 | Counting {own} processes of your account and {others} of the system and other accounts. “System” marks programs that come with macOS; numbers with “≈” are estimates. CPU is per core, so it can exceed 100%. |
| `process.diag.mem` | 内存不够用了：系统正在压缩内存、写交换文件，所以会卡。占得最多的是 {name}（{size}）。 | Memory is running out: macOS is compressing memory and writing swap, so things slow down. {name} uses the most ({size}). |
| `process.diag.cpu` | CPU 很忙，占得最多的是 {name}（{pct}）。 | The CPU is busy. {name} uses the most ({pct}). |
| `process.diag.goMem` | 看内存 | Show Memory |
| `process.diag.goCpu` | 看 CPU | Show CPU |
| `process.stuck` | 还没退出 | Still running |
| `process.force` | 强制退出 | Force Quit |
| `process.force.title` | 强制退出「{name}」？ | Force quit “{name}”? |
| `process.force.body` | 它没有响应正常退出。强制退出会立刻关掉它，没保存的内容会丢失。 | It didn't respond to Quit. Force quitting closes it at once, and unsaved work is lost. |
| `process.force.ok` | 强制退出 | Force Quit |
| `process.toast.forceDone` | 已强制退出「{name}」 | Force quit “{name}” |
| `process.toast.forceFail` | 没能强制退出「{name}」 | Couldn't force quit “{name}” |
| `process.tag.day` | 磁盘一天 {size} | Disk {size} in a day |
| `process.tag.week` | 磁盘 7 天 {size} | Disk {size} in 7 days |
| `process.unsupported` | 进程页目前只支持 macOS。 | The process page is macOS only for now. |

- [ ] **Step 5: Create `src/react/process/ProcessPage.tsx`**

```tsx
import * as React from "react";
import { Pause, Play } from "lucide-react";
import { Root as TabsRoot, List as TabsList, Trigger as TabsTrigger } from "@radix-ui/react-tabs";
import { Channel } from "mygo-runtime";
import type { GrowthSummary, ProcessRow, ProcessSample } from "../../mygo";
import { Button } from "../../ui/components/button";
import {
  Dialog, DialogClose, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "../../ui/components/dialog";
import { segTrigger } from "../../ui/components/seg";
import { useT } from "../../ui/i18n-react";
import { Collapse } from "../../ui/motion/Collapse";
import { useIndicator } from "../../ui/motion/indicator";
import { isWindows } from "../../platform";
import { errText, human, toast } from "../../shared";
import { emptyQuit, fmtMem, fmtPct, iconColor, quitErrCode, syncQuitRows, type QuitState } from "../status/format";
import { growthApi } from "../growth/api";
import { GrowthPanel, type GrowthHandlers } from "../growth/GrowthPage";
import { processApi } from "./api";
import {
  diagnose, est, growthTags, pruneStuck, pushCpu, refreshFrozen, rowTag, shouldWatch, sortChildren, sortRows,
  type GrowthTag, type ProcessRequest, type ProcessTab,
} from "./format";

type T = ReturnType<typeof useT>;

/** 首字母圆标的字：点目录跳过开头的点。 */
function initial(name: string): string {
  return (name.replace(/^\.+/, "") || name).charAt(0).toUpperCase();
}

/** 增长小标签的文字：「磁盘 7 天 +11.2 GB」；量到一半的写「至少」。 */
function tagText(tag: GrowthTag, t: T): string {
  const size = `+${human(tag.bytes)}`;
  return t(tag.window === "day" ? "process.tag.day" : "process.tag.week", { size: tag.atLeast ? t("growth.atLeast", { size }) : size });
}

export interface ProcessRowViewProps {
  row: ProcessRow;
  /** 展开后的进程跟着当前标签排。 */
  sortBy: "cpu" | "mem";
  open: boolean;
  tag: GrowthTag | null;
  /** 正在退出或强制退出。 */
  busy: boolean;
  /** 正常退出后还在跑：显示「还没退出」和「强制退出」。 */
  stuck: boolean;
  /** 已经退出：置灰、去掉按钮。 */
  done: boolean;
  t: T;
  onToggle: () => void;
  onQuit: () => void;
  onForce: () => void;
  onTag: (key: string) => void;
}

/** 一行：App、命令行程序或系统程序；有不止一个进程时能展开，同名的合成一条。 */
export function ProcessRowView({ row, sortBy, open, tag, busy, stuck, done, t, onToggle, onQuit, onForce, onTag }: ProcessRowViewProps) {
  const expandable = row.children.length > 1 || (row.children[0]?.count ?? 0) > 1;
  const num = (estimated: boolean) => `st-col num${estimated ? " pr-est" : ""}`;
  return (
    <div className={`pr-item${done ? " quitted" : ""}`} data-key={row.key}>
      <div className="st-row pr-row">
        <div className="pr-app">
          <button type="button" className="pr-toggle" aria-expanded={expandable ? open : undefined} disabled={!expandable} onClick={onToggle}>
            <span className="pr-chev" aria-hidden="true">{expandable ? (open ? "▾" : "›") : ""}</span>
            <span className={`st-ico${row.kind === "app" ? "" : " pr-ico-sys"}`} style={row.kind === "app" ? { background: iconColor(row.name) } : undefined} aria-hidden="true">
              {initial(row.name)}
            </span>
            <span className="st-name" title={row.name}>{row.name}</span>
            {row.procs > 1 && <span className="st-procs">{t("status.apps.procs", { n: row.procs })}</span>}
            {row.kind === "system" && <span className="pr-sys">{t("process.system")}</span>}
          </button>
          {tag && <button type="button" className="pr-tag" onClick={() => onTag(tag.key)}>{tagText(tag, t)}</button>}
        </div>
        <span className={num(row.estimated)}>{est(fmtPct(row.cpu), row.estimated)}</span>
        <span className={num(row.estimated)}>{est(fmtMem(row.memory), row.estimated)}</span>
        <span className="st-act pr-act">
          {row.quit && !done && (stuck ? (
            <>
              <span className="pr-stuck">{t("process.stuck")}</span>
              <button type="button" className="pr-force" disabled={busy} onClick={onForce}>{t("process.force")}</button>
            </>
          ) : (
            <button type="button" className="st-quit" disabled={busy} onClick={onQuit}>
              {busy ? t("status.apps.quitting") : t("status.apps.quit")}
            </button>
          ))}
        </span>
      </div>
      {expandable && (
        <Collapse open={open}>
          <ul className="pr-children">
            {sortChildren(row.children, sortBy).map((c) => (
              <li key={c.name} className="pr-child">
                <span className="pr-child-name" title={c.name}>{c.count > 1 ? `${c.name} × ${c.count}` : c.name}</span>
                <span className={num(c.estimated)}>{est(fmtPct(c.cpu), c.estimated)}</span>
                <span className={num(c.estimated)}>{est(fmtMem(c.memory), c.estimated)}</span>
                <span className="st-act pr-act" />
              </li>
            ))}
          </ul>
        </Collapse>
      )}
    </div>
  );
}

/** 进程页的显示状态、访问次数、打开请求和磁盘占用标签的导航回调。 */
export interface ProcessPageProps extends GrowthHandlers {
  /** 是否正显示本视图：离开就关采样通道。 */
  active: boolean;
  /** 每次进入视图递增：标签回到 CPU（或请求的标签），开关回到「在走」。 */
  visit: number;
  /** main.ts 的打开请求；nonce 变了才执行。 */
  request: ProcessRequest;
}

/** 进程页：CPU、内存两个实时标签，加上磁盘占用（占用增长的列表）。spec 2026-10-26。 */
export function ProcessPage({ active, visit, request, goLeftovers, goUninstall }: ProcessPageProps) {
  const t = useT();
  const [tab, setTab] = React.useState<ProcessTab>("cpu");
  const [running, setRunning] = React.useState(true);
  const [hidden, setHidden] = React.useState(() => typeof document !== "undefined" && document.hidden);
  const [sample, setSample] = React.useState<ProcessSample | null>(null);
  const [error, setError] = React.useState(false);
  const [retry, setRetry] = React.useState(0);
  const [cpuHist, setCpuHist] = React.useState<number[]>([]);
  const [open, setOpen] = React.useState<string | null>(null);
  const [quit, setQuit] = React.useState<QuitState>(emptyQuit());
  const [stuck, setStuck] = React.useState<ReadonlySet<string>>(new Set());
  const [quitTarget, setQuitTarget] = React.useState<ProcessRow | null>(null);
  const [forceTarget, setForceTarget] = React.useState<ProcessRow | null>(null);
  const [growth, setGrowth] = React.useState<GrowthSummary | null>(null);
  const [focus, setFocus] = React.useState<{ key: string | null; nonce: number }>({ key: null, nonce: 0 });
  const [seen, setSeen] = React.useState({ visit: -1, nonce: 0 });
  const frozenRef = React.useRef<ProcessRow[]>([]);
  const segRef = React.useRef<HTMLDivElement>(null);
  const segStyle = useIndicator(segRef, '[data-state="active"]');

  // 每次进入：开关回到「在走」，清掉上次的读数和退出状态；有新的打开请求就按它切标签、定位行，否则回到 CPU。
  // 在渲染时调整而不是在 effect 里，采样通道第一次就按正确的标签决定开不开。
  if (active && seen.visit !== visit) {
    const fresh = request.nonce !== seen.nonce;
    setSeen({ visit, nonce: request.nonce });
    setTab(fresh && request.tab ? request.tab : "cpu");
    setRunning(true);
    setSample(null);
    setOpen(null);
    setQuit(emptyQuit());
    setStuck(new Set());
    frozenRef.current = [];
    if (fresh && request.focusKey) {
      const key = request.focusKey;
      setFocus((f) => ({ key, nonce: f.nonce + 1 }));
    }
  }

  // 窗口隐藏时停采样，回来接着按开关走。
  React.useEffect(() => {
    const onVis = () => setHidden(document.hidden);
    document.addEventListener("visibilitychange", onVis);
    return () => document.removeEventListener("visibilitychange", onVis);
  }, []);

  // 只在页面显示、在 CPU 或内存标签、没暂停、窗口没隐藏时开通道；关通道 Go 侧就不再读进程、不再调用 ps。
  const watching = !isWindows() && shouldWatch({ active, tab, running, hidden });
  React.useEffect(() => {
    if (!watching) return;
    let live = true;
    const ch = new Channel<ProcessSample>();
    setError(false);
    setCpuHist([]);
    void (async () => {
      try {
        const done = processApi.watch(ch);
        for await (const s of ch) {
          if (!live) continue; // 已关通道：丢弃迟到的采样
          setSample(s);
          setCpuHist((h) => pushCpu(h, s.cpu));
        }
        await done;
      } catch {
        if (live) setError(true);
      }
    })();
    return () => { live = false; ch.close(); };
  }, [watching, retry]);

  // 磁盘占用标签上的数字和行上的小标签：每次进入读一次，增长有变化时再读。
  const loadGrowth = React.useCallback(() => {
    growthApi.summary().then(setGrowth, () => setGrowth(null));
  }, []);
  React.useEffect(() => { if (active) loadGrowth(); }, [active, visit, loadGrowth]);
  React.useEffect(() => (active ? growthApi.onUpdated(() => loadGrowth()) : undefined), [active, loadGrowth]);

  // 离开磁盘占用标签就放下定位请求，回来时不再高亮旧的那一行。
  React.useEffect(() => {
    if (tab !== "disk") setFocus((f) => (f.key ? { key: null, nonce: f.nonce } : f));
  }, [tab]);

  // 每轮采样后对账：退出过的行消失记 gone、同 key 再现恢复；「强制退出」只留给还在跑的。
  React.useEffect(() => {
    if (!sample) return;
    setQuit((q) => syncQuitRows(q, new Set(sample.rows.map((r) => r.key))));
    setStuck((s) => pruneStuck(s, sample.rows));
  }, [sample]);

  const markDone = (row: ProcessRow) => {
    setQuit((q) => ({ ...q, done: new Set(q.done).add(row.key) }));
    setStuck((s) => { const next = new Set(s); next.delete(row.path); return next; });
  };
  const setBusy = (path: string, on: boolean) => setQuit((q) => {
    const busy = new Set(q.busy);
    if (on) busy.add(path); else busy.delete(path);
    return { ...q, busy };
  });

  // 正常退出：和系统状态一样；5 秒后还在跑（或被拒绝），这一行改成「还没退出」+「强制退出」。
  const doQuit = async (row: ProcessRow) => {
    setQuitTarget(null);
    setBusy(row.path, true);
    try {
      if (await processApi.quitApp(row.path)) {
        toast(t("status.toast.quitDone", { name: row.name }));
        markDone(row);
      } else {
        toast(t("status.toast.quitPending", { name: row.name }));
        setStuck((s) => new Set(s).add(row.path));
      }
    } catch (e) {
      const code = quitErrCode(e);
      if (code === "not-running") {
        toast(t("status.toast.notRunning", { name: row.name }));
        markDone(row);
      } else if (code === "cannot-quit") {
        toast(t("status.toast.cannotQuit", { name: row.name }), "err");
        setStuck((s) => new Set(s).add(row.path));
      } else {
        toast(`${t("status.error")}：${errText(e)}`, "err");
      }
    } finally {
      setBusy(row.path, false);
    }
  };

  // 强制退出：只在确认框里点了才调用。
  const doForce = async (row: ProcessRow) => {
    setForceTarget(null);
    setBusy(row.path, true);
    try {
      if (await processApi.forceQuitApp(row.path)) {
        toast(t("process.toast.forceDone", { name: row.name }));
        markDone(row);
      } else {
        toast(t("process.toast.forceFail", { name: row.name }), "err");
      }
    } catch (e) {
      if (quitErrCode(e) === "not-running") {
        toast(t("status.toast.notRunning", { name: row.name }));
        markDone(row);
      } else {
        toast(t("process.toast.forceFail", { name: row.name }), "err");
      }
    } finally {
      setBusy(row.path, false);
    }
  };

  const sortBy: "cpu" | "mem" = tab === "mem" ? "mem" : "cpu";
  // 只在确认框开着、正在退出时冻结行和顺序；鼠标停在列表上不冻结，想停下来看用「暂停」（spec §三）。
  const freezing = quit.busy.size > 0 || quitTarget !== null || forceTarget !== null;
  let rows: ProcessRow[] = [];
  if (sample) {
    rows = freezing && frozenRef.current.length > 0 ? refreshFrozen(frozenRef.current, sample) : sortRows(sample.rows, sortBy);
    if (!freezing || frozenRef.current.length === 0) frozenRef.current = rows;
  }
  const tags = growthTags(growth);
  const diag = tab === "disk" ? null : diagnose(sample, cpuHist);
  const goDisk = (key: string) => {
    setFocus((f) => ({ key, nonce: f.nonce + 1 }));
    setTab("disk");
  };

  if (isWindows()) {
    return (
      <div className="cz-react">
        <header className="view-head">
          <div>
            <h2>{t("nav.process")}</h2>
            <p className="muted">{t("process.unsupported")}</p>
          </div>
        </header>
      </div>
    );
  }

  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h2>{t("nav.process")}</h2>
          <p className="muted">{t("process.sub")}</p>
        </div>
        <div className="head-actions pr-head-actions">
          {tab !== "disk" && (
            <Button variant="ghost" size="sm" aria-pressed={!running} onClick={() => setRunning((r) => !r)}>
              {running ? <Pause size={14} /> : <Play size={14} />}
              {running ? t("process.pause") : t("process.resume")}
            </Button>
          )}
          <TabsRoot value={tab} onValueChange={(v) => setTab(v as ProcessTab)}>
            <TabsList
              ref={segRef}
              aria-label={t("process.tabs")}
              className="relative flex flex-none gap-[2px] rounded-[8px] bg-[var(--bg-subtle)] p-[2px]"
            >
              <span aria-hidden="true" className="cz-seg-thumb" style={segStyle} />
              <TabsTrigger value="cpu" className={segTrigger}>{t("process.tab.cpu")}</TabsTrigger>
              <TabsTrigger value="mem" className={segTrigger}>{t("process.tab.mem")}</TabsTrigger>
              <TabsTrigger value="disk" className={segTrigger}>
                {t("process.tab.disk")}
                {tags.size > 0 && <i className="pr-badge">{tags.size}</i>}
              </TabsTrigger>
            </TabsList>
          </TabsRoot>
        </div>
      </header>

      {tab === "disk" ? (
        <GrowthPanel active={active} visit={visit} focusKey={focus.key} focusNonce={focus.nonce} goLeftovers={goLeftovers} goUninstall={goUninstall} />
      ) : (
        <>
          {diag && (
            <div className="pr-diag" role="status">
              <span>
                {diag.kind === "mem"
                  ? t("process.diag.mem", { name: diag.top?.name ?? "—", size: est(fmtMem(diag.top?.memory ?? 0), !!diag.top?.estimated) })
                  : t("process.diag.cpu", { name: diag.top?.name ?? "—", pct: est(fmtPct(diag.top?.cpu ?? 0), !!diag.top?.estimated) })}
              </span>
              {diag.kind !== tab && (
                <Button variant="ghost" size="sm" onClick={() => setTab(diag.kind)}>
                  {t(diag.kind === "mem" ? "process.diag.goMem" : "process.diag.goCpu")}
                </Button>
              )}
            </div>
          )}
          {!running && <p className="pr-paused muted">{t("process.paused")}</p>}
          <section className="st-listcard pr-card">
            <div className="st-cols num pr-cols">
              <span className="c-app">{t("status.apps.appCol")}</span>
              <button type="button" className="pr-sort" aria-pressed={sortBy === "cpu"} onClick={() => setTab("cpu")}>
                {t("process.col.cpu")}{sortBy === "cpu" ? " ▾" : ""}
              </button>
              <button type="button" className="pr-sort" aria-pressed={sortBy === "mem"} onClick={() => setTab("mem")}>
                {t("process.col.mem")}{sortBy === "mem" ? " ▾" : ""}
              </button>
              <span className="c-act" />
            </div>
            <div className="st-list pr-list">
              {error ? (
                <div className="st-empty">
                  {t("status.error")}{" "}
                  <Button variant="ghost" size="sm" onClick={() => setRetry((n) => n + 1)}>{t("btn.retry")}</Button>
                </div>
              ) : !sample ? (
                Array.from({ length: 6 }, (_, i) => (
                  <div key={i} className="st-row st-skel-row"><div className="sk-line w40" /><div className="sk-line w60" /></div>
                ))
              ) : rows.map((r) => (
                <ProcessRowView
                  key={r.key}
                  row={r}
                  sortBy={sortBy}
                  open={open === r.key}
                  tag={rowTag(r, tags)}
                  busy={quit.busy.has(r.path)}
                  stuck={stuck.has(r.path)}
                  done={quit.done.has(r.key) && !quit.gone.has(r.key)}
                  t={t}
                  onToggle={() => setOpen((o) => (o === r.key ? null : r.key))}
                  onQuit={() => setQuitTarget(r)}
                  onForce={() => setForceTarget(r)}
                  onTag={goDisk}
                />
              ))}
            </div>
            <p className="st-footnote muted small">
              {sample ? t("process.footnote", { own: sample.own.toLocaleString(), others: sample.others.toLocaleString() }) : ""}
            </p>
          </section>
        </>
      )}

      <Dialog open={quitTarget !== null} onOpenChange={(v) => { if (!v) setQuitTarget(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("status.quit.title", { name: quitTarget?.name ?? "" })}</DialogTitle>
            <DialogDescription>{t("status.quit.body", { name: quitTarget?.name ?? "" })}</DialogDescription>
          </DialogHeader>
          <div className="mt-4 flex justify-end gap-2">
            <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
            {quitTarget && <Button onClick={() => void doQuit(quitTarget)}>{t("status.quit.ok")}</Button>}
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={forceTarget !== null} onOpenChange={(v) => { if (!v) setForceTarget(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("process.force.title", { name: forceTarget?.name ?? "" })}</DialogTitle>
            <DialogDescription>{t("process.force.body")}</DialogDescription>
          </DialogHeader>
          <div className="mt-4 flex justify-end gap-2">
            <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
            {forceTarget && <Button variant="destructive" onClick={() => void doForce(forceTarget)}>{t("process.force.ok")}</Button>}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
```

- [ ] **Step 6: Create `src/react/process/mount.tsx` and `src/react/process/entry.ts`**

```tsx
import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import type { GrowthHandlers } from "../growth/GrowthPage";
import { ProcessPage } from "./ProcessPage";
import type { ProcessRequest, ProcessTab } from "./format";
import "../../ui/index.css";

let root: Root | null = null;
let visit = 0;
let request: ProcessRequest = { tab: null, focusKey: null, nonce: 0 };

/** 记下下次进入时要打开的标签和磁盘占用里要定位的行。 */
export function requestProcess(tab: ProcessTab, focusKey: string | null) {
  request = { tab, focusKey, nonce: request.nonce + 1 };
}

/** 在原生视图的容器里挂载进程页；第一次进入才建根，之后复用。每次进入 visit 加一，隐藏时只传 active=false。 */
export function mountProcess(el: HTMLElement, handlers: GrowthHandlers, active: boolean) {
  if (!root) {
    if (!active) return;
    root = createRoot(el);
  }
  if (active) visit++;
  root.render(
    <React.StrictMode>
      <ProcessPage active={active} visit={visit} request={request} {...handlers} />
    </React.StrictMode>,
  );
}
```

```ts
// React 进程页与原生导航的桥接：main.ts 调 showReactProcess(active)、openReactProcess(tab, focusKey)，不直接接触 React。
import type { GrowthHandlers } from "../growth/GrowthPage";
import type { ProcessTab } from "./format";
import { mountProcess, requestProcess } from "./mount";

const handlers: GrowthHandlers = { goLeftovers: () => {}, goUninstall: () => {} };

/** main.ts 注入：磁盘占用标签里跳到应用卸载的残留页签 / 打开某个 App 的卸载对话框。 */
export function setProcessHandlers(h: GrowthHandlers) {
  Object.assign(handlers, h);
}

/** main.ts 每次切换视图都调用：active 表示是否切到了本视图（进入时开采样，离开关通道）。 */
export function showReactProcess(active: boolean) {
  const el = document.querySelector<HTMLElement>("#process-react-root");
  if (el) mountProcess(el, handlers, active);
}

/** 下次进入时打开哪个标签；focusKey 是磁盘占用标签里要定位的行。调用方随后切到本视图。 */
export function openReactProcess(tab: ProcessTab, focusKey: string | null = null) {
  requestProcess(tab, focusKey);
}
```

- [ ] **Step 7: Add the styles.** Append to the end of `src/style.css`:

```css
/* 进程页（spec 2026-10-26）：沿用系统状态的 st-* 列表 */
.pr-head-actions { align-items: center; }
.pr-badge { display: inline-flex; align-items: center; justify-content: center; min-width: 16px; height: 16px; margin-left: 5px; padding: 0 4px; border-radius: 8px; background: var(--warn-soft); color: var(--warn-text); font-size: 10.5px; font-style: normal; font-weight: 700; }
.pr-diag { display: flex; align-items: center; gap: 10px; margin: 0 0 12px; padding: 10px 14px; border-radius: 10px; background: var(--warn-soft); color: var(--warn-text); font-size: 13px; line-height: 1.5; }
.pr-diag > span { flex: 1; min-width: 0; }
.pr-paused { margin: 0 0 8px; font-size: 12px; }
.pr-cols .pr-sort { width: 96px; flex: none; padding: 0; border: 0; background: none; font: inherit; color: inherit; text-align: right; cursor: pointer; }
.pr-cols .pr-sort[aria-pressed="true"] { color: var(--fg); font-weight: 600; }
.pr-cols .c-act { width: 168px; }
.pr-row { content-visibility: auto; contain-intrinsic-size: auto 52px; }
.pr-app { flex: 1; min-width: 0; display: flex; align-items: center; gap: 8px; }
.pr-toggle { flex: 1; min-width: 0; display: flex; align-items: center; gap: 10px; padding: 0; border: 0; background: none; font: inherit; color: inherit; text-align: left; cursor: pointer; }
.pr-toggle:disabled { cursor: default; }
.pr-chev { width: 10px; flex: none; color: var(--muted); font-size: 11px; }
.pr-ico-sys { background: var(--bg-subtle); color: var(--muted); box-shadow: inset 0 0 0 1px var(--border); }
.pr-sys { flex: none; padding: 0 6px; border: 1px solid var(--border); border-radius: 6px; color: var(--muted); font-size: 11px; }
.pr-tag { flex: none; padding: 1px 6px; border: 0; border-radius: 6px; background: var(--warn-soft); color: var(--warn-text); font: inherit; font-size: 11px; cursor: pointer; }
.pr-est { color: var(--muted); }
.pr-act { display: flex; width: 168px; flex: none; align-items: center; justify-content: flex-end; gap: 8px; }
.pr-stuck { color: var(--muted); font-size: 12px; white-space: nowrap; }
.pr-force { height: 28px; padding: 0 12px; border: 1px solid var(--danger); border-radius: 7px; background: var(--card); color: var(--danger); font-size: 12px; font-weight: 600; cursor: pointer; }
.pr-force:disabled { opacity: 0.45; cursor: default; }
.pr-children { margin: 0; padding: 4px 0; list-style: none; background: var(--bg-subtle); }
.pr-child { display: flex; align-items: center; gap: 10px; height: 28px; padding: 0 20px 0 66px; color: var(--muted); font-size: 12px; }
.pr-child-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.st-viewall { margin-left: 8px; padding: 0; border: 0; background: none; color: var(--accent-strong); font: inherit; font-size: 12px; white-space: nowrap; cursor: pointer; }
```

- [ ] **Step 8: Run and see it pass**

Run: `bun test tests/process-react.test.ts && bun run typecheck && bun run test:web`
Expected: PASS. The page isn't routed yet; Task 5 wires it.

---

### Task 5: Routing, the status page and the smoke tour

**Files:**
- Modify: `index.html`, `src/shared.ts`, `src/main.ts`, `src/react/status/StatusPage.tsx`, `src/react/status/entry.ts`, `src/react/status/mount.tsx`, `src/react/growth/GrowthPage.tsx` (delete the shell), `smoke.go`, `tests/smoke/windows.ps1`, `tests/status-react.test.ts`
- Delete: `src/react/growth/entry.ts`, `src/react/growth/mount.tsx`

**Interfaces:**
- **Consumes:** Task 4's `showReactProcess`, `openReactProcess`, `setProcessHandlers` and `ProcessTab`.
- **Produces:**
  - `StatusPageProps.goProcess: (tab: ProcessTab, focusKey?: string) => void`, which replaces `goGrowth`;
  - `setGoProcessHandler(fn)`, which replaces `setGoGrowthHandler`;
  - the `process` view id, which replaces `growth`.

- [ ] **Step 1: Write the failing test.** Append to `tests/status-react.test.ts`:

```ts
test("the top-apps card links to the process page on macOS only", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { StatusPage } = await import("../src/react/status/StatusPage");
  const { setPlatformForTest } = await import("../src/platform");
  const props = { active: false, goClean() {}, goProcess() {} };
  try {
    expect(renderToStaticMarkup(createElement(StatusPage, props))).toContain(t("status.apps.viewAll"));
    setPlatformForTest("win32");
    expect(renderToStaticMarkup(createElement(StatusPage, props))).not.toContain(t("status.apps.viewAll"));
  } finally { setPlatformForTest(undefined); }
});
```

If `StatusPage` cannot render statically, do not change how it renders. Instead, move the list head (the `st-list-head` div) into an exported `AppsHead` component in the same file, and render that in the test.

- [ ] **Step 2: Run and see it fail**

Run: `bun test tests/status-react.test.ts`
Expected: FAIL, because 「查看全部」 is missing.

- [ ] **Step 3: The status page.** In `src/react/status/StatusPage.tsx`:
  - Add `import type { ProcessTab } from "../process/format";`.
  - In `StatusPageProps`, replace `goGrowth` and its doc comment with:

```ts
  /** 增长提示的「查看」、列表的「查看全部」：打开进程页的某个标签，可带磁盘占用标签里要定位的行（main.ts 注入）。 */
  goProcess: (tab: ProcessTab, focusKey?: string) => void;
```

  - Change the signature to `export function StatusPage({ active, goClean, goProcess }: StatusPageProps)`.
  - In the growth callout, change the button's `onClick={goGrowth}` to `onClick={() => goProcess("disk", growthTop.key)}`.
  - In the list head, right after `</TabsRoot>`, add:

```tsx
          {!isWindows() && (
            <button className="st-viewall" type="button" onClick={() => goProcess(sortBy)}>
              {t("status.apps.viewAll")} ›
            </button>
          )}
```

  Replace `src/react/status/entry.ts` with:

```ts
// React 系统状态页与原生导航的桥接：main.ts 调 showReactStatus(active) 即可，不直接接触 React。
import type { ProcessTab } from "../process/format";
import { mountStatus } from "./mount";

const goClean: { fn: () => void } = { fn: () => {} };
const goProcess: { fn: (tab: ProcessTab, focusKey?: string) => void } = { fn: () => {} };

/** 告警 / 磁盘卡的「去清理」：与点侧栏「垃圾清理」同一条路径（main.ts 注入）。 */
export function setGoCleanHandler(fn: () => void) {
  goClean.fn = fn;
}

/** 增长提示的「查看」、列表的「查看全部」：打开进程页的某个标签（main.ts 注入）。 */
export function setGoProcessHandler(fn: (tab: ProcessTab, focusKey?: string) => void) {
  goProcess.fn = fn;
}

/** 进入系统状态视图传 true（开 2s 采样流），离开传 false（关 Channel）。root 复用。 */
export function showReactStatus(active: boolean) {
  const el = document.querySelector<HTMLElement>("#status-react-root");
  if (el) mountStatus(el, active, goClean.fn, goProcess.fn);
}
```

  In `src/react/status/mount.tsx`:
  - change the last parameter to `goProcess: (tab: ProcessTab, focusKey?: string) => void`;
  - render `<StatusPage active={active} goClean={goClean} goProcess={goProcess} />`;
  - add `import type { ProcessTab } from "../process/format";`.

- [ ] **Step 4: The sidebar and the view.** In `index.html`:
  - Delete the `<button class="nav-item" data-view="growth" …>…</button>` block and the `<section id="view-growth" …>…</section>` block.
  - Right after the `data-view="status"` button, add:

```html
          <button class="nav-item" data-view="process" type="button">
            <svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2.5"/><path d="M7 9h10M7 13h6M7 17h8"/></svg>
            <span data-i18n="nav.process">进程</span>
          </button>
```

  - Right after the `#view-status` section, add:

```html
        <!-- 进程（React 页面，内容由 React 挂载） -->
        <section id="view-process" class="view hidden">
          <div id="process-react-root"></div>
        </section>
```

  In `src/shared.ts`, `VIEWS` becomes:

```ts
const VIEWS = ["clean", "status", "process", "aitools", "skills", "fullscan", "large", "photos", "apps", "settings"] as const;
```

- [ ] **Step 5: `src/main.ts`.** Make these changes:
  - Replace `import { showReactGrowth, focusReactGrowth, setGrowthHandlers } from "./react/growth/entry";` with `import { showReactProcess, openReactProcess, setProcessHandlers } from "./react/process/entry";`.
  - Replace `import { setGoCleanHandler, setGoGrowthHandler, showReactStatus } from "./react/status/entry";` with `import { setGoCleanHandler, setGoProcessHandler, showReactStatus } from "./react/status/entry";`.
  - In `switchView`, replace `showReactGrowth(v === "growth");` with `showReactProcess(v === "process");`.
  - Replace `setGoGrowthHandler(() => switchView("growth"));` with:

```ts
// 状态页的「查看全部」和增长提示：先记下要打开的标签和要定位的行，再切过去。
setGoProcessHandler((tab, key) => { openReactProcess(tab, key ?? null); switchView("process"); });
```

  - Replace `setGrowthHandlers({` with `setProcessHandlers({`. The object stays the same.
  - Replace the growth-focus line and its comment with:

```ts
// 通知 / 菜单栏点进来：打开进程页的磁盘占用标签并定位那一行。
if (window.mygo) events.growthFocus.on((key) => { openReactProcess("disk", key); switchView("process"); });
```

  - Replace the Windows line and its comment with:

```ts
// 进程页第一版只做 macOS：Windows 上隐藏入口。
if (isWindows()) document.querySelector<HTMLElement>('.nav-item[data-view="process"]')?.classList.add("hidden");
```

- [ ] **Step 6: Remove the old page.**
  - Delete `src/react/growth/entry.ts` and `src/react/growth/mount.tsx`.
  - Delete the temporary `GrowthPage` shell at the end of `src/react/growth/GrowthPage.tsx`, and drop the imports it alone used.
  - Then `grep -rn '"nav.growth"\|"growth.sub"' src tests`. Remove each key from both dictionaries in `src/i18n.ts` if nothing references it any more.

- [ ] **Step 7: The smoke tour.**
  - In `smoke.go`: `var smokeViews = []string{"status", "process", "clean", "aitools", "skills", "fullscan", "large", "photos", "apps", "settings"}`.
  - In `tests/smoke/windows.ps1`: `$views = 'status', 'process', 'clean', 'aitools', 'skills', 'fullscan', 'large', 'photos', 'apps', 'settings'`.

  On Windows the page renders the unsupported note inside `.cz-react`, so the tour's wait still passes.

- [ ] **Step 8: Run and see it pass**

Run:
- `bun run typecheck && bun run test:web`
- `go vet ./... && GOOS=windows GOARCH=amd64 go vet ./...`
- `go test -count=1 -run 'Smoke|Bindings|SlashPath' .`

Expected: PASS. `grep -rn "showReactGrowth\|focusReactGrowth\|setGoGrowthHandler\|view-growth\|data-view=\"growth\"" src index.html tests` prints nothing.

---

### Task 6: Docs and the whole-branch check (reviewer)

**Files:** `README.md`, `CHANGELOG.md`, `docs/superpowers/specs/2026-10-26-process-page-design.md`

- [ ] **Step 1: Spec.**
  - In §三, change 「其余的系统进程和其他账户的进程，每个单独一行，名字旁边标「系统」。」 to 「其余的系统进程和其他账户的进程，按程序名合成一行，名字旁边标「系统」。」. This matches the command-line rows, and §七 leaves merging to the implementation.
  - In §三 「刚打开时」, replace the two lines with 「第一组数字大约 1 秒后出来：CPU 要隔一会儿统计两次才算得出来，内存和它一起到，免得 CPU 一列先全是 0、按 0 排序。」.
  - In §六.7, replace 「标签上的数字，和状态页增长提示里的 App 数一致」 with 「标签上的数字是近 7 天有增长提醒、没被忽略的 App 数，只在后台监控开着时显示；口径和状态页的增长提示相同」. The status alert names only the biggest one, so a count cannot "match" it.
  - Add a line to §八 for each of these three changes.
- [ ] **Step 2: README.**
  - Replace the 「占用增长（macOS）：…」 bullet with a 「进程（macOS）」 bullet covering:
    - CPU and memory per app, with each app's processes merged and expandable;
    - system and other accounts' processes, as estimates;
    - the diagnosis line;
    - quit, and force quit after a quit that did not finish, with a confirmation;
    - sampling only while the page is open, and pause.
  - Keep the growth sentences, introduced as 「磁盘占用」标签就是占用增长：…
- [ ] **Step 3: CHANGELOG (`## 未发布`).**
  - Replace the 「新增「占用增长」」 bullet with a 「新增「进程」页」 bullet. Then say that 占用增长 lives in its 「磁盘占用」 tab, and keep the growth details.
  - In the status-page bullet:
    - 「查看」 now opens 进程 › 磁盘占用 at that app;
    - 「占用最多的应用」 gains 「查看全部」.
- [ ] **Step 4: The whole-branch check** (see "Reviewer verification").

---

## Reviewer verification (on the maintainer's Mac)

1. **The suites:**
   - `gofmt -l .` prints nothing;
   - `go vet ./...` and `GOOS=windows GOARCH=amd64 go vet ./...` pass;
   - `go test -race -count=1 ./...` passes;
   - `bun run typecheck` and `bun run test:web` pass.
2. **CPU budget, spec §六.9:**
   - Run `CLEANZO_PERF=1 go test -count=1 -run TestProcessSamplingBudget -v .`. It must pass at ≤ 3%; note the logged number in the ledger.
   - Then run Cleanzo Dev with the page open for a minute. Measure its web content process with `top -l 31 -s 2 -stats pid,command,cpu`. It must average ≤ 3%.
3. **On screen, Cleanzo Dev:**
   - 进程 sits under 系统状态, and 占用增长 is gone from 空间.
   - The CPU tab fills in about a second.
   - `yes > /dev/null` in Terminal shows up as its own 「yes」 row at the top within 2 s. It is not counted under 终端.
   - WindowServer carries 「系统」 and ≈, and has no button.
   - First establish a positive control on the running CPU tab: `for i in $(seq 500); do pgrep -f '^/bin/ps -ax -o pid=,uid=' && break; sleep 0.02; done` must find a matching process. If it finds nothing, the checks below are invalid.
   - 「暂停」 freezes the numbers and shows 已暂停. While paused, run `cleanzo_ps_result=OK; for i in $(seq 500); do if pgrep -f '^/bin/ps -ax -o pid=,uid='; then cleanzo_ps_result=FAIL; fi; sleep 0.02; done; echo "$cleanzo_ps_result"` for at least 10 s; it must print OK. 「继续」 resumes.
   - Resume sampling on the CPU tab and repeat the positive control, then switch to another page. Run `cleanzo_ps_result=OK; for i in $(seq 500); do if pgrep -f '^/bin/ps -ax -o pid=,uid='; then cleanzo_ps_result=FAIL; fi; sleep 0.02; done; echo "$cleanzo_ps_result"` for at least 10 s; it must print OK.
   - Return to the running CPU tab and repeat the positive control, then close the window. Run `cleanzo_ps_result=OK; for i in $(seq 500); do if pgrep -f '^/bin/ps -ax -o pid=,uid='; then cleanzo_ps_result=FAIL; fi; sleep 0.02; done; echo "$cleanzo_ps_result"` for at least 10 s; it must print OK.
   - TextEdit with unsaved text:
     - 「退出」 asks first, then TextEdit asks to save;
     - leave TextEdit's dialog open, and after 5 s the row shows 还没退出 and 「强制退出」;
     - 「强制退出」 asks; cancel does nothing; confirm closes TextEdit.
   - 磁盘占用 shows the growth list. Its 「现在量一次」 sits in the status line.
   - A growth notification, a menu bar app item, and the status page's growth alert each open 进程 › 磁盘占用 at that row.
   - 「查看全部」 on the status page opens CPU or 内存, matching that card's sort.
4. **The smoke tour** on macOS shows the `process` view in both themes. Windows CI passes after the push.

## Execution batches

The maintainer said traecli and Codex write the code (2026-10-26). Claude reviews and commits each task.

| Batch | Executor | Tasks | Where | Starts after |
|---|---|---|---|---|
| A | Codex (`codex:codex-rescue` → `codex-companion.mjs task --write`) | 1 | worktree `../cleanzo-proc-go`, branch `proc-go` from HEAD | — |
| B | traecli | 2 | worktree `../cleanzo-proc-panel`, branch `proc-panel` from HEAD | — (runs alongside A; no shared files) |
| C | traecli | 3 | main checkout | A and B are reviewed, cherry-picked and committed (C needs A's bindings) |
| D | traecli | 4 | main checkout | C |
| E | traecli | 5 | main checkout | D |
| — | Claude | 6 | main checkout | E |

- **Worktrees** symlink `node_modules` from the main checkout (`ln -s ../cleanzo/node_modules node_modules`). The symlink is excluded through `.git/info/exclude`.
- **traecli:** `traecli exec -C <dir> -s workspace-write --add-dir <repo>/.git --add-dir "$(go env GOCACHE)" --color never -o <ws>/task-N-traecli-last.md - < <ws>/task-N-traecli-prompt.md > <ws>/task-N-traecli.log 2>&1`
- **Codex's sandbox** fails eight existing tests for reasons outside this work: sips, Unix sockets and local ports, and `kern.boottime`. Inside the sandbox, run Go tests with
  `-skip 'TestLargePhotosAreDecodedSmall|TestUsageGuardSeesSockets|TestNativeMachineInfo|TestNativeSysctlRawProbes|TestCheckAndUpgradeLockedSkill|TestUpgradeRefusesLocalChangesUnlessForced|TestUpgradeRejectsArchiveThatDoesNotMatch|TestCheckReportsMissingAndRateLimit'`.
  The reviewer runs the full suite outside the sandbox before each commit.

## Implementation notes

One line per place where the shipped code differs from this plan; read these before rebuilding from the task code above.

- **Task 1, CPU units:** `nativeProcs` converts `proc_pid_rusage`'s CPU times from Mach ticks to nanoseconds with `mach_timebase_info`, read once in `nativeInit` and computed in 128-bit math.
  - On Apple silicon the ticks are 24 MHz (timebase 125/3). The plan's code took them as nanoseconds, so the status page's per-app CPU had been 41.67× too low.
  - `TestNativeProcsCPUTimeMatchesGetrusage` pins it.
- **Task 1, `ps`:** it runs `/bin/ps -ax -o pid=,uid=,pcpu=,rss=,ucomm=` under `LC_ALL=en_US.UTF-8` and skips its own pid.
  - Full paths come from `proc_pidpath` through `nativePidPath`. When that fails, the short name stands in.
  - A failed refresh keeps the last reading, and two failures in a row clear it.
- **Task 1, bundle IDs:** app rows take the running app's BundleID from NSWorkspace when it has one. Otherwise they read the plist once per path for the process's lifetime, through a per-path `sync.Once` cache.
- **Task 1, the loop:** it checks for cancellation at entry, at each iteration and after a `ps` refresh, and takes one timestamp just before each read.
- **Task 1, other:** `ForceQuitApp` returns `unsupported` off macOS. The budget test averages over 60 s. The tests gained seams for the `ps` command and the clock.
- **Task 2:** `.cz-growth-panel` is a flex column with a 16px gap, so the panel keeps the page's spacing.
- **Task 4, rows:** rows divide on `.pr-item` rather than `.st-row`, because the wrapper made every row `:last-child`. A quit greys the whole `.pr-item`, including its expanded processes. A row that can't expand is a `div`, not a disabled button.
- **Task 4, page:**
  - The `Channel` is created inside the `try`.
  - 「重试」 also resumes a paused page.
  - Quit dialogs clear on leaving and on each visit.
  - The diagnosis bar has no live-region role, and Pause has no `aria-pressed`.
  - A quit row hides its button by `done`, without checking `gone`.
- **Task 5:** none.
- **N, naming limitation:** On macOS, `proc_name` and ps's `ucomm` both record the name of the file a symlink points to, so Claude Code (`…/versions/2.1.304`) still appears as `2.1.304`. Showing `claude` would require reading `argv[0]` with `KERN_PROCARGS2` and caching it by `(pid, start)`; this version does not implement that. The `Name` field names live processes whose paths cannot be read.
