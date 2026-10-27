# Process Page Fixes — Implementation Plan

> **For agentic workers:** In this project the tasks are executed by traecli and the Codex CLI (see "Execution batches"). **Executors do not commit**: the reviewer checks and commits each task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Fix what the real-machine test of the 「进程」 page found, and give apps their real icons.
- **CPU budget.** With the page open on the CPU tab, the production app used about 3.6% of a core (main process 3.04% plus `ps` about 0.6%). The cap is 3%.
- **Reopening.** Reopening Cleanzo from Finder, the Dock, Spotlight or `open` should always bring the main window forward.
- **Real icons.** The maintainer asked 「应用要显示真实的logo」. Apps show their real icon wherever a letter avatar stands for an app:
  - the 进程 page's CPU and 内存 rows;
  - the 磁盘占用 panel;
  - 系统状态's 「占用最多的应用」;
  - 应用卸载's installed list.

  Command-line programs and system processes keep the letter. So does any app whose icon can't be read.

**Architecture:**
- **Where the CPU went.** A 12-second `sample` of the running app with the page open found these costs:
  - **`fork`: 112 ms, about 56 ms per `ps` run.** Go's `os/exec` starts children with `fork`. In a process with WebKit loaded, `fork` first runs every `pthread_atfork` handler: Network.framework's `nw_path_prepare_fork` and malloc's `_malloc_fork_prepare`, which locks every zone. `ps` runs every 6 s, so this alone is about 0.9% of a core.
  - **`proc_pidpath`: 23 ms**, for other accounts' processes on every `ps` reading.
  - **The AppKit main thread: about 180 ms.** This is Core Animation commits and WebKit IPC for each update.
- **Task 1 (Go):**
  - `ps` is started with `posix_spawn` through purego, which runs no atfork handlers. A spike checked it in a scratch module: output, environment, exit status, cancel and reap, closed descriptors, no descriptor leak, and setuid `ps`.
  - Full paths of other accounts' processes are remembered per pid for the length of a sampling session.
  - Rows with a single process stop carrying a child list. That is about a quarter of each sample's JSON.
  - Reopening always raises the main window.
- **Task 2 (Go + web):**
  - A new bound method, `AppIcon(path)`, returns an 80 px PNG data URL of the app bundle's icon. It draws through NSWorkspace and NSBitmapImageRep on the calling goroutine's thread, not the main thread, because a cold icon takes up to ~300 ms. The spike measured 12–280 ms cold and 3–9 ms warm.
  - Each bundle is drawn once per run.
  - A shared React `AppIcon` component asks for an icon only when its row comes near the viewport. It keeps one cache for all pages and shows the letter until the icon arrives.
- **Task 3 (web + docs):**
  - The 进程 list sorts by the numbers it shows: whole percent, and 0.1 GB or 1 MB. Rows that show the same number stay in name order instead of swapping every sample.
  - CHANGELOG and spec.

**Tech Stack:** Go 1.27, mygo 0.6.2 and purego v0.11.1, all already dependencies. TypeScript and React 19 with bun test. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-26-process-page-design.md` (Chinese). §六 point 9 sets the budget:
- main process plus `ps` ≤ 3% of one core (getrusage SELF + CHILDREN);
- WebKit content process ≤ 3%.

The previous plan is `docs/superpowers/plans/2026-10-27-process-page.md`.

## Global Constraints

- **No new dependencies**, Go or npm. Do not run commands that need the network.
- **Platform rules** (enforced by `testenv_test.go`):
  - Exported Go names and `Cleaner` methods live in untagged files. `_darwin.go` and build-tagged files hold unexported helpers only (`TestBindingsArePlatformNeutral`).
  - Untagged non-test `.go` files build paths with `filepath`. `p+"/"`, `"/"+p`, `"~/"` and `strings.Split(p, "/")` are not allowed (`TestNoSlashPathLiterals`).
- **Bindings are generated.** After adding `AppIcon`, run `bun run generate`. Never edit `src/mygo.ts` by hand.
- **`ps` runs only inside the watch loop**, on the same cadence as now: at the start and every third sample (about 6 s), with a 3 s timeout.
- **Nothing else spawns differently.** Only `ps` moves to `posix_spawn`. Every other subprocess (plutil, du, mdls, sips…) stays on `os/exec`.
- **No visible change** besides the icons and the steadier order. Copy, layout, tabs, quit and force quit stay as they are.
- **Icons are decorative.** The `<img>` has `alt=""` and `aria-hidden="true"`; the app name next to it is the label.
- **Executors do not commit.** Leave the changes in the working tree.

## Review Focus

1. **`spawnOutput`** (Task 1):
   - Every exit path closes the pipe and reaps the child.
   - The kill on cancel happens before `Wait4`, so a reused pid is never signalled.
   - Only stdin, stdout and stderr reach the child (`POSIX_SPAWN_CLOEXEC_DEFAULT`).
   - `LC_ALL` is replaced, not duplicated: with a duplicate, the child's `getenv` returns the first one.
2. **The path memory** (Task 1):
   - A pid's path is reused only while `ps` lists that pid under the same short name.
   - Pids that disappear are forgotten.
   - A failed `ps` keeps the previous memory.
3. **Trimmed children** (Task 1): only rows with `Procs == 1`, and the JSON says `"children":[]`, never `null`.
4. **Icon threading and caching** (Task 2):
   - Drawing locks its OS thread for the autorelease pool and the graphics context.
   - Drawings are serialized.
   - Each bundle is drawn once, including under concurrent calls.
   - Non-`.app` and missing paths are refused before any drawing.
5. **The React side** (Task 2):
   - The letter stays until the icon is known.
   - Requests start only near the viewport.
   - One request per path across pages.
   - Existing markup and tests are unchanged while no icon is cached.
6. **Sorting** (Task 3): the comparison uses the same rounding as `fmtPct` and `fmtMem`, and ties fall back to the name.

## File Structure

| File | Task | Change |
|------|------|--------|
| `spawn_darwin.go` | 1 | **New.** `spawnOutput`, `posix_spawn` through purego |
| `spawn_darwin_test.go` | 1 | **New.** spawn behavior |
| `process_darwin.go` | 1 | `ps` through `processPsSpawn`; `otherProcsReader` remembers paths |
| `process_darwin_test.go` | 1 | the ps stand-in goes through `spawnOutput`; new path-memory and locale tests |
| `process.go` | 1 | `trimOneProcessRows` on each sample |
| `process_test.go` | 1 | the trimmed sample |
| `growth_app.go` | 1 | reopen always raises the main window |
| `appicon.go` | 2 | **New.** `Cleaner.AppIcon`, validation and cache |
| `appicon_darwin.go` | 2 | **New.** `nativeAppIcon`, `nsString`, `nsRect` |
| `appicon_other.go` | 2 | **New.** stub |
| `appicon_test.go`, `appicon_darwin_test.go` | 2 | **New.** |
| `src/mygo.ts` | 2 | regenerated |
| `src/react/icons/api.ts`, `cache.ts`, `AppIcon.tsx` | 2 | **New.** |
| `src/react/process/ProcessPage.tsx`, `src/react/status/StatusPage.tsx`, `src/react/growth/GrowthPage.tsx`, `src/react/apps/InstalledTab.tsx` | 2 | wrap the letter avatar in `AppIcon` |
| `src/style.css` | 2 | `img.cz-app-img` |
| `tests/app-icon.test.ts` | 2 | **New.** |
| `tests/process-react.test.ts`, `tests/apps-react.test.ts` | 2, 3 | icon rows (2); shown-value sorting (3) |
| `package.json` | 2 | `test:web` runs `tests/app-icon.test.ts` |
| `src/react/process/format.ts` | 3 | `sortRows` and `sortChildren` compare shown values |
| `CHANGELOG.md`, `docs/superpowers/specs/2026-10-26-process-page-design.md` | 3 | docs |

The three tasks touch disjoint files, so they can run in parallel worktrees.

---

### Task 1: Go — `ps` without fork, remembered paths, lighter samples, reopen

**Files:**
- Create: `spawn_darwin.go`, `spawn_darwin_test.go`
- Modify: `process_darwin.go`, `process_darwin_test.go`, `process.go`, `process_test.go`, `growth_app.go`

**Interfaces:**
- Produces:
  - `func spawnOutput(ctx context.Context, path string, argv, env []string) ([]byte, int32, error)`
  - `func withEnv(env []string, kv string) []string`
  - `var processPsSpawn = spawnOutput`
  - `func otherProcsReader() func(context.Context) []otherProc`
  - `func trimOneProcessRows(rows []ProcessRow) []ProcessRow`
- Removes: `processPsCommand` (the `os/exec` seam). Tests use `processPsSpawn` instead.

- [ ] **Step 1: Write `spawn_darwin.go`**

```go
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/ebitengine/purego"
)

// spawnOutput exists because of what fork costs here. os/exec starts a
// child with fork, and fork first runs every pthread_atfork handler in this
// process. With WebKit loaded those lock every malloc zone and wait on
// Network.framework's path monitor: a sample of the running app showed
// about 56 ms of CPU per child, so ps every six seconds alone took about 1%
// of a core. posix_spawn starts the child without running them.

const (
	spawnSetSigDef      = 0x0004 // POSIX_SPAWN_SETSIGDEF
	spawnSetSigMask     = 0x0008 // POSIX_SPAWN_SETSIGMASK
	spawnCloexecDefault = 0x4000 // POSIX_SPAWN_CLOEXEC_DEFAULT: the child gets only the descriptors set up here
)

var (
	spawnOnce              sync.Once
	spawnErr               error
	posixSpawn             func(pid *int32, path string, actions, attr *uintptr, argv, envp **byte) int32
	spawnActionsInit       func(actions *uintptr) int32
	spawnActionsDestroy    func(actions *uintptr) int32
	spawnActionsAddOpen    func(actions *uintptr, fd int32, path string, oflag int32, mode uint16) int32
	spawnActionsAddDup2    func(actions *uintptr, fd, newfd int32) int32
	spawnAttrInit          func(attr *uintptr) int32
	spawnAttrDestroy       func(attr *uintptr) int32
	spawnAttrSetFlags      func(attr *uintptr, flags int16) int32
	spawnAttrSetSigMask    func(attr *uintptr, mask *uint32) int32
	spawnAttrSetSigDefault func(attr *uintptr, mask *uint32) int32
)

func spawnInit() error {
	spawnOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			spawnErr = err
			return
		}
		purego.RegisterLibFunc(&posixSpawn, lib, "posix_spawn")
		purego.RegisterLibFunc(&spawnActionsInit, lib, "posix_spawn_file_actions_init")
		purego.RegisterLibFunc(&spawnActionsDestroy, lib, "posix_spawn_file_actions_destroy")
		purego.RegisterLibFunc(&spawnActionsAddOpen, lib, "posix_spawn_file_actions_addopen")
		purego.RegisterLibFunc(&spawnActionsAddDup2, lib, "posix_spawn_file_actions_adddup2")
		purego.RegisterLibFunc(&spawnAttrInit, lib, "posix_spawnattr_init")
		purego.RegisterLibFunc(&spawnAttrDestroy, lib, "posix_spawnattr_destroy")
		purego.RegisterLibFunc(&spawnAttrSetFlags, lib, "posix_spawnattr_setflags")
		purego.RegisterLibFunc(&spawnAttrSetSigMask, lib, "posix_spawnattr_setsigmask")
		purego.RegisterLibFunc(&spawnAttrSetSigDefault, lib, "posix_spawnattr_setsigdefault")
	})
	return spawnErr
}

// spawnOutput runs path like exec.CommandContext(ctx, ...).Output(), but
// starts it with posix_spawn. argv includes argv[0]; env is passed exactly
// as given. Standard input and error are /dev/null. It returns the standard
// output and the child's pid. Canceling ctx kills the child, and a non-zero
// exit is an error. The child is always reaped before it returns.
func spawnOutput(ctx context.Context, path string, argv, env []string) ([]byte, int32, error) {
	if err := spawnInit(); err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	cArgv, err := cStrings(argv)
	if err != nil {
		return nil, 0, err
	}
	cEnv, err := cStrings(env)
	if err != nil {
		return nil, 0, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, 0, err
	}
	defer r.Close()
	pid, err := spawnChild(path, cArgv, cEnv, w)
	w.Close() // the child has its own copy: EOF comes when it exits
	if err != nil {
		return nil, 0, err
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			// Not reaped yet, so the pid is still this child's.
			_ = syscall.Kill(int(pid), syscall.SIGKILL)
		case <-stop:
		}
	}()
	out, readErr := io.ReadAll(r)
	close(stop)
	<-stopped
	var status syscall.WaitStatus
	for {
		_, err := syscall.Wait4(int(pid), &status, 0, nil)
		if err == nil {
			break
		}
		if err != syscall.EINTR {
			return nil, pid, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, pid, err
	}
	if readErr != nil {
		return nil, pid, readErr
	}
	if !status.Exited() || status.ExitStatus() != 0 {
		return nil, pid, fmt.Errorf("%s: %s", path, waitText(status))
	}
	return out, pid, nil
}

// spawnChild starts path with stdout on the pipe's write end, stdin and
// stderr on /dev/null, no other descriptors, no blocked signals, and
// SIGPIPE at its default.
func spawnChild(path string, argv, env []*byte, stdout *os.File) (int32, error) {
	var actions, attr uintptr
	if rc := spawnActionsInit(&actions); rc != 0 {
		return 0, syscall.Errno(rc)
	}
	defer spawnActionsDestroy(&actions)
	if rc := spawnAttrInit(&attr); rc != 0 {
		return 0, syscall.Errno(rc)
	}
	defer spawnAttrDestroy(&attr)
	var noSignals uint32
	pipeSignal := uint32(1) << (uint(syscall.SIGPIPE) - 1)
	for _, rc := range []int32{
		spawnActionsAddOpen(&actions, 0, os.DevNull, syscall.O_RDONLY, 0),
		// Fd also puts the write end in blocking mode; the child's stdout
		// shares that flag, and a non-blocking stdout would fail ps's writes.
		spawnActionsAddDup2(&actions, int32(stdout.Fd()), 1),
		spawnActionsAddOpen(&actions, 2, os.DevNull, syscall.O_WRONLY, 0),
		spawnAttrSetFlags(&attr, spawnSetSigDef|spawnSetSigMask|spawnCloexecDefault),
		spawnAttrSetSigMask(&attr, &noSignals),
		spawnAttrSetSigDefault(&attr, &pipeSignal),
	} {
		if rc != 0 {
			return 0, syscall.Errno(rc)
		}
	}
	var pid int32
	rc := posixSpawn(&pid, path, &actions, &attr, &argv[0], &env[0])
	// posix_spawn copies argv and envp before it returns.
	runtime.KeepAlive(argv)
	runtime.KeepAlive(env)
	if rc != 0 {
		return 0, &os.PathError{Op: "posix_spawn", Path: path, Err: syscall.Errno(rc)}
	}
	return pid, nil
}

// cStrings makes the NULL-terminated array of C strings that argv and envp
// are. A string with a NUL byte is an error.
func cStrings(ss []string) ([]*byte, error) {
	out := make([]*byte, 0, len(ss)+1)
	for _, s := range ss {
		p, err := syscall.BytePtrFromString(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return append(out, nil), nil
}

func waitText(s syscall.WaitStatus) string {
	if s.Signaled() {
		return "killed by " + s.Signal().String()
	}
	return fmt.Sprintf("exit status %d", s.ExitStatus())
}

// withEnv returns env with kv ("KEY=value") in place of any KEY already
// there. A duplicate key is not harmless: getenv returns the first one.
func withEnv(env []string, kv string) []string {
	key, _, _ := strings.Cut(kv, "=")
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if k, _, _ := strings.Cut(e, "="); k != key {
			out = append(out, e)
		}
	}
	return append(out, kv)
}
```

- [ ] **Step 2: Write `spawn_darwin_test.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSpawnOutputReadsStandardOutput(t *testing.T) {
	out, pid, err := spawnOutput(context.Background(), "/bin/echo", []string{"echo", "hello", "世界"}, nil)
	if err != nil || string(out) != "hello 世界\n" || pid <= 0 {
		t.Fatalf("got %q, pid %d, %v", out, pid, err)
	}
}

func TestSpawnOutputPassesTheEnvironmentAsGiven(t *testing.T) {
	env := withEnv([]string{"A=1", "LC_ALL=C", "B=two"}, "LC_ALL=en_US.UTF-8")
	out, _, err := spawnOutput(context.Background(), "/usr/bin/env", []string{"env"}, env)
	if err != nil || string(out) != "A=1\nB=two\nLC_ALL=en_US.UTF-8\n" {
		t.Fatalf("env printed %q, %v", out, err)
	}
}

func TestSpawnOutputFailures(t *testing.T) {
	if _, _, err := spawnOutput(context.Background(), "/usr/bin/false", []string{"false"}, nil); err == nil {
		t.Error("a non-zero exit was not an error")
	}
	if _, pid, err := spawnOutput(context.Background(), "/nonexistent/tool", []string{"tool"}, nil); err == nil || pid != 0 {
		t.Errorf("missing executable: pid %d, err %v", pid, err)
	}
	if _, _, err := spawnOutput(context.Background(), "/bin/echo", []string{"echo", "a\x00b"}, nil); err == nil {
		t.Error("an argument with a NUL byte was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, pid, err := spawnOutput(ctx, "/bin/echo", []string{"echo"}, nil); !errors.Is(err, context.Canceled) || pid != 0 {
		t.Errorf("canceled before the start: pid %d, err %v", pid, err)
	}
}

func TestSpawnOutputGivesTheChildNothingToRead(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, _, err := spawnOutput(context.Background(), "/bin/cat", []string{"cat"}, nil)
		if err != nil || len(out) != 0 {
			t.Errorf("cat got %q, %v", out, err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cat is still waiting for input")
	}
}

func TestSpawnOutputKillsTheChildWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, pid, err := spawnOutput(ctx, "/bin/sleep", []string{"sleep", "5"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
	if err := syscall.Kill(int(pid), 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("pid %d is still there (%v): not killed, or not reaped", pid, err)
	}
}

func TestSpawnOutputClosesOtherDescriptors(t *testing.T) {
	fd, err := syscall.Open(os.DevNull, syscall.O_RDONLY, 0) // no O_CLOEXEC on purpose
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	script := fmt.Sprintf("if [ -e /dev/fd/%d ]; then echo leaked; else echo closed; fi", fd)
	out, _, err := spawnOutput(context.Background(), "/bin/sh", []string{"sh", "-c", script}, nil)
	if err != nil || strings.TrimSpace(string(out)) != "closed" {
		t.Fatalf("descriptor %d in the child: %q, %v", fd, out, err)
	}
}

func TestSpawnOutputLeavesNoDescriptorsBehind(t *testing.T) {
	count := func() int { e, _ := os.ReadDir("/dev/fd"); return len(e) }
	before := count()
	for range 20 {
		if _, _, err := spawnOutput(context.Background(), "/usr/bin/true", []string{"true"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if after := count(); after != before {
		t.Fatalf("open descriptors %d -> %d", before, after)
	}
}

func TestSpawnOutputRunsSetuidPs(t *testing.T) {
	out, _, err := spawnOutput(context.Background(), "/bin/ps", []string{"/bin/ps", "-ax", "-o", "pid=,uid="}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "1" && f[1] == "0" {
			return
		}
	}
	t.Fatalf("ps did not list launchd (pid 1, uid 0); %d bytes of output", len(out))
}
```

- [ ] **Step 3: Run the spawn tests**

Run: `go test -count=1 -run 'Spawn' .`
Expected: PASS. If the sandbox refuses a test, for example one that needs `/bin/ps`, say which test and why in the report. Do not change the test.

- [ ] **Step 4: Move `ps` onto `spawnOutput` and remember paths (`process_darwin.go`)**

Replace the `os/exec` seam and `readOtherProcs`. Keep `processSupported`, `processPsTimeout`, `processPidPath` and `systemExecutable` as they are. Drop the `os/exec` import.

```go
// processPsSpawn runs ps. Tests point it at a stand-in.
var processPsSpawn = spawnOutput
```

In `realProcessSource`, replace `others: readOtherProcs,` with:

```go
		others:      otherProcsReader(),
```

Then replace `readOtherProcs` with:

```go
// otherPath is the full path proc_pidpath gave for a pid that ps listed
// under name.
type otherPath struct{ name, path string }

// otherProcsReader returns the other-accounts reader for one sampling
// session. It remembers each pid's full path while ps keeps listing that
// pid under the same short name, so proc_pidpath runs once per process
// rather than on every reading. A failed ps keeps the memory. The loop calls
// it from one goroutine.
func otherProcsReader() func(context.Context) []otherProc {
	var paths map[int32]otherPath
	return func(ctx context.Context) []otherProc {
		others, next := readOtherProcsWith(ctx, paths)
		if others != nil {
			paths = next
		}
		return others
	}
}

// readOtherProcs is one reading with nothing remembered.
func readOtherProcs(ctx context.Context) []otherProc {
	others, _ := readOtherProcsWith(ctx, nil)
	return others
}

// readOtherProcsWith runs ps, which is setuid root and so can report every
// account's processes; libproc refuses other accounts' (EPERM). The
// numbers are ps's, a decaying CPU average and resident memory, hence
// estimates. ps only supplies short names; libproc supplies full UTF-8
// paths, reused from known for a pid still listed under the same name.
// en_US.UTF-8 keeps a decimal dot and readable fallback names. It returns
// the reading and the paths to remember; nil when ps failed.
func readOtherProcsWith(ctx context.Context, known map[int32]otherPath) ([]otherProc, map[int32]otherPath) {
	ctx, cancel := context.WithTimeout(ctx, processPsTimeout)
	defer cancel()
	argv := []string{"/bin/ps", "-ax", "-o", "pid=,uid=,pcpu=,rss=,ucomm="}
	out, self, err := processPsSpawn(ctx, argv[0], argv, withEnv(os.Environ(), "LC_ALL=en_US.UTF-8"))
	if ctx.Err() != nil || err != nil {
		return nil, nil
	}
	procs := parsePsOutput(out, uint32(os.Getuid()))
	// A non-nil empty slice is a successful read with no other processes;
	// nil means unavailable and tells the loop to keep its last reading.
	others := make([]otherProc, 0, len(procs))
	paths := make(map[int32]otherPath, len(procs))
	for _, p := range procs {
		if p.Pid == self { // ps itself: setuid, so listed as root
			continue
		}
		seen, ok := known[p.Pid]
		if !ok || seen.name != p.Name {
			seen = otherPath{name: p.Name, path: processPidPath(p.Pid)}
		}
		paths[p.Pid] = seen
		if seen.path != "" {
			p.Path = seen.path
		}
		others = append(others, p)
	}
	return others, paths
}
```

(`parsePsOutput` sets `Name` to ps's short name, so `p.Name` is what ps listed.)

- [ ] **Step 5: Point the ps tests at the new seam (`process_darwin_test.go`)**

Replace `fakeProcessPs` with the version below. `TestProcessPsHelper` stays as it is. The stand-in still checks `LC_ALL` and still prints its own pid as a root `ps` line, so the self-filter is tested against the pid `spawnOutput` returns.

```go
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
```

Then add these tests (and `errors`, `fmt` and `slices` to the imports if missing):

```go
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
	step(both)                                       // the failure kept the memory
	step(fmt.Sprintf("10 %d 0.0 1 other\n", uid), 10) // a new name under pid 10; 11 is gone
	step(both, 11)                                   // 11 came back: looked up again
}
```

- [ ] **Step 6: Send no child list for one-process rows (`process.go`)**

In `watchProcessesWith`, wrap the grouping:

```go
			Rows:     trimOneProcessRows(groupProcesses(prev, cur, others, src.apps(), src.responsible, src.system, bundleID, elapsed)),
```

Add this after `snapshotProcs`:

```go
// trimOneProcessRows empties the child list of rows made of one process.
// The page lists children only for rows it can expand, which takes two
// processes or more, and these lists were about a quarter of each sample's
// JSON on a typical Mac. The list stays non-nil so the JSON says [].
func trimOneProcessRows(rows []ProcessRow) []ProcessRow {
	for i := range rows {
		if rows[i].Procs <= 1 {
			rows[i].Children = []ProcessChild{}
		}
	}
	return rows
}
```

Add to `process_test.go` (with `encoding/json` and `strings` imported if missing):

```go
func TestWatchProcessesSendsNoChildrenForOneProcessRows(t *testing.T) {
	defer setProcessTiming(time.Millisecond, time.Millisecond)()
	var ps atomic.Int32
	src := fakeProcessSource(&ps)
	src.procs = func() []procReading {
		return []procReading{
			{Pid: 1, Path: "/usr/bin/yes", Start: 1},
			{Pid: 2, Path: "/usr/local/bin/node", Start: 1},
			{Pid: 3, Path: "/usr/local/bin/node", Start: 1},
		}
	}
	var got ProcessSample
	stop := errors.New("one sample")
	if err := watchProcessesWith(context.Background(), func(s ProcessSample) error { got = s; return stop }, src); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	rows := rowsByKey(got.Rows)
	if yes := rows["cli:yes"]; yes.Procs != 1 || yes.Children == nil || len(yes.Children) != 0 {
		t.Fatalf("one-process row %+v, want an empty, non-nil child list", yes)
	}
	if node := rows["cli:node"]; node.Procs != 2 || len(node.Children) != 1 || node.Children[0].Count != 2 {
		t.Fatalf("two-process row %+v, want its child list kept", node)
	}
	data, err := json.Marshal(rows["cli:yes"])
	if err != nil || !strings.Contains(string(data), `"children":[]`) {
		t.Fatalf("JSON %s, %v; want \"children\":[]", data, err)
	}
}
```

- [ ] **Step 7: Reopen always raises the main window (`growth_app.go`)**

In `registerGrowthLifecycle`, replace the `OnActivate` handler with:

```go
	// Reopening the app (Finder, the Dock, Spotlight, `open`) brings the main
	// window forward every time, not only when AppKit counts no visible
	// window: one that is open but not in front still has to come back.
	mygo.App.OnActivate(func(bool) {
		if mygo.App.IsReady() { // no window can be made before that
			openMainWindow()
		}
	})
```

- [ ] **Step 8: Verify**

Run:
- `gofmt -l .` (expect no output)
- `go vet ./... && GOOS=windows GOARCH=amd64 go vet ./...`
- `go test -count=1 -run 'Spawn|OtherProcs|ParsePsCommand|ReadOtherProcs|WatchProcesses|GroupProcesses|Bindings|SlashPath' .`
- `go test -count=1 -skip 'TestLargePhotosAreDecodedSmall|TestUsageGuardSeesSockets|TestNativeMachineInfo|TestNativeSysctlRawProbes|TestCheckAndUpgradeLockedSkill|TestUpgradeRefusesLocalChangesUnlessForced|TestUpgradeRejectsArchiveThatDoesNotMatch|TestCheckReportsMissingAndRateLimit' ./...`

These eight tests fail inside the sandbox only, from network, sockets and sysctl limits. The reviewer runs the full suite outside.

Expected: all pass.

---

### Task 2: Real app icons

**Files:**
- Create: `appicon.go`, `appicon_darwin.go`, `appicon_other.go`, `appicon_test.go`, `appicon_darwin_test.go`, `src/react/icons/api.ts`, `src/react/icons/cache.ts`, `src/react/icons/AppIcon.tsx`, `tests/app-icon.test.ts`
- Modify: `src/mygo.ts` (generated), `src/react/process/ProcessPage.tsx`, `src/react/status/StatusPage.tsx`, `src/react/growth/GrowthPage.tsx`, `src/react/apps/InstalledTab.tsx`, `src/style.css`, `package.json`, `tests/process-react.test.ts`, `tests/apps-react.test.ts`

**Interfaces:**
- Consumes: `appKitInit`, `withPool`, `nsSel` (`native.go`).
- Produces:
  - `func (Cleaner) AppIcon(path string) (string, error)` → generated as `Cleaner.appIcon(path: string): Promise<string>`;
  - `const appIconPx = 80`;
  - `var appIconRender = nativeAppIcon`;
  - `func nativeAppIcon(path string, px int) []byte`;
  - `func nsString(s string) objc.ID`;
  - TS: `iconApi`, `hasAppIcon`, `peekIcon`, `loadIcon`, `resetIconsForTest`, `AppIcon`.

- [ ] **Step 1: Write `appicon.go`** (untagged; the bound method must live here)

```go
package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// appIconPx is the side of the icons the pages show. 80 px covers the 38 px
// avatar of the uninstall list on a Retina screen; the 24 and 28 px avatars
// elsewhere scale it down.
const appIconPx = 80

// appIconRender draws the icon of an app bundle as a PNG, nil when it
// can't. Tests replace it.
var appIconRender = nativeAppIcon

// appIcons holds every icon drawn so far, by bundle path, for the life of
// the app; "" records a bundle without one.
var appIcons = struct {
	sync.Mutex
	urls map[string]string
}{urls: map[string]string{}}

// appIconDrawing keeps to one drawing at a time: a cold icon takes up to a
// few hundred milliseconds of Icon Services work, and a page asking for
// twenty at once should not take twenty threads.
var appIconDrawing sync.Mutex

// AppIcon returns the icon Finder shows for the app bundle at path, as a
// PNG data URL, or "" when there is none: not an absolute path to an .app
// folder, gone, or off macOS. Pages show the first letter of the name until
// it arrives, and instead of it when there is none. Each bundle is drawn
// once.
func (Cleaner) AppIcon(path string) (string, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), ".app") {
		return "", nil
	}
	if url, ok := cachedAppIcon(path); ok {
		return url, nil
	}
	appIconDrawing.Lock()
	defer appIconDrawing.Unlock()
	if url, ok := cachedAppIcon(path); ok { // drawn while this call waited
		return url, nil
	}
	url := ""
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		if png := appIconRender(path, appIconPx); len(png) > 0 {
			url = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
	}
	appIcons.Lock()
	appIcons.urls[path] = url
	appIcons.Unlock()
	return url, nil
}

func cachedAppIcon(path string) (string, bool) {
	appIcons.Lock()
	defer appIcons.Unlock()
	url, ok := appIcons.urls[path]
	return url, ok
}
```

- [ ] **Step 2: Write `appicon_darwin.go`**

```go
package main

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego/objc"
)

// nsRect is NSRect, passed by value.
type nsRect struct{ X, Y, W, H float64 }

// nativeAppIcon draws the icon NSWorkspace gives for path into a px × px
// bitmap and encodes it as PNG; nil when it can't. It runs on the calling
// goroutine's thread, not the main thread: NSWorkspace's icons and drawing
// into an offscreen bitmap are safe there, and a cold icon can take a few
// hundred milliseconds that the window must not wait for.
func nativeAppIcon(path string, px int) []byte {
	if !appKitInit() {
		return nil
	}
	// The autorelease pool and the current graphics context belong to the
	// thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var out []byte
	withPool(func() {
		ws := objc.ID(objc.GetClass("NSWorkspace")).Send(nsSel("sharedWorkspace"))
		img := ws.Send(nsSel("iconForFile:"), nsString(path))
		if img == 0 {
			return
		}
		rep := objc.ID(objc.GetClass("NSBitmapImageRep")).Send(nsSel("alloc")).Send(
			nsSel("initWithBitmapDataPlanes:pixelsWide:pixelsHigh:bitsPerSample:samplesPerPixel:hasAlpha:isPlanar:colorSpaceName:bytesPerRow:bitsPerPixel:"),
			uintptr(0), px, px, 8, 4, true, false, nsString("NSDeviceRGBColorSpace"), 0, 0)
		if rep == 0 {
			return
		}
		defer rep.Send(nsSel("release"))
		ctx := objc.ID(objc.GetClass("NSGraphicsContext")).Send(nsSel("graphicsContextWithBitmapImageRep:"), rep)
		if ctx == 0 {
			return
		}
		gc := objc.ID(objc.GetClass("NSGraphicsContext"))
		gc.Send(nsSel("saveGraphicsState"))
		gc.Send(nsSel("setCurrentContext:"), ctx)
		side := float64(px)
		img.Send(nsSel("drawInRect:fromRect:operation:fraction:"),
			nsRect{0, 0, side, side}, nsRect{}, uint64(2) /* NSCompositingOperationSourceOver */, 1.0)
		gc.Send(nsSel("restoreGraphicsState"))
		dict := objc.ID(objc.GetClass("NSDictionary")).Send(nsSel("dictionary"))
		data := rep.Send(nsSel("representationUsingType:properties:"), uint64(4) /* NSBitmapImageFileTypePNG */, dict)
		if data == 0 {
			return
		}
		n := objc.Send[uint64](data, nsSel("length"))
		b := uintptr(data.Send(nsSel("bytes")))
		if n == 0 || b == 0 {
			return
		}
		// Taking the address and dereferencing keeps go vet from flagging
		// the uintptr→pointer conversion, as in nsGoString.
		ptr := *(*unsafe.Pointer)(unsafe.Pointer(&b))
		out = append([]byte(nil), unsafe.Slice((*byte)(ptr), n)...)
	})
	return out
}

// nsString makes an autoreleased NSString of s; purego passes s as a C
// string.
func nsString(s string) objc.ID {
	return objc.ID(objc.GetClass("NSString")).Send(nsSel("stringWithUTF8String:"), s)
}
```

- [ ] **Step 3: Write `appicon_other.go`**

```go
//go:build !darwin

package main

// nativeAppIcon: app bundles and their icons are macOS's; elsewhere the
// pages keep the letter.
func nativeAppIcon(string, int) []byte { return nil }
```

- [ ] **Step 4: Write the Go tests**

`appicon_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeAppIcons swaps the renderer for one that counts its calls and returns
// png, and starts from an empty cache; both are restored afterwards.
func fakeAppIcons(t *testing.T, png []byte) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	old := appIconRender
	appIconRender = func(string, int) []byte { calls.Add(1); return png }
	reset := func() { appIcons.Lock(); clear(appIcons.urls); appIcons.Unlock() }
	reset()
	t.Cleanup(func() { appIconRender = old; reset() })
	return &calls
}

func TestAppIconRefusesWhatIsNotAnAppBundle(t *testing.T) {
	calls := fakeAppIcons(t, []byte{1})
	dir := t.TempDir()
	file := filepath.Join(dir, "File.app")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", "Relative.app", dir, filepath.Join(dir, "Missing.app"), file} {
		if got, err := (Cleaner{}).AppIcon(p); got != "" || err != nil {
			t.Errorf("AppIcon(%q) = %q, %v; want no icon", p, got, err)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("drew %d icons for paths that are not app bundles", n)
	}
}

func TestAppIconDrawsEachBundleOnce(t *testing.T) {
	calls := fakeAppIcons(t, []byte{1, 2, 3})
	app := filepath.Join(t.TempDir(), "Acme.app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	urls := make([]string, 8)
	var wg sync.WaitGroup
	for i := range urls {
		wg.Add(1)
		go func() { defer wg.Done(); urls[i], _ = (Cleaner{}).AppIcon(app) }()
	}
	wg.Wait()
	for _, u := range urls {
		if u != "data:image/png;base64,AQID" {
			t.Fatalf("AppIcon = %q, want the PNG as a data URL", u)
		}
	}
	if got, _ := (Cleaner{}).AppIcon(app + string(filepath.Separator)); got != urls[0] {
		t.Fatalf("with a trailing separator: %q", got)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("drew %d times, want once", n)
	}
}

func TestAppIconRemembersBundlesWithoutAnIcon(t *testing.T) {
	calls := fakeAppIcons(t, nil)
	app := filepath.Join(t.TempDir(), "Blank.app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if got, err := (Cleaner{}).AppIcon(app); got != "" || err != nil {
			t.Fatalf("AppIcon = %q, %v; want no icon", got, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("drew %d times, want once", n)
	}
}
```

`appicon_darwin_test.go`:

```go
package main

import (
	"bytes"
	"image/png"
	"os"
	"strings"
	"testing"
)

const testIconApp = "/System/Applications/Calculator.app"

func TestNativeAppIconDrawsTheIcon(t *testing.T) {
	if _, err := os.Stat(testIconApp); err != nil {
		t.Skip("no Calculator.app on this Mac")
	}
	data := nativeAppIcon(testIconApp, appIconPx)
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a PNG (%d bytes): %v", len(data), err)
	}
	if b := img.Bounds(); b.Dx() != appIconPx || b.Dy() != appIconPx {
		t.Fatalf("size %v, want %d × %d", b, appIconPx, appIconPx)
	}
	if _, _, _, a := img.At(appIconPx/2, appIconPx/2).RGBA(); a == 0 {
		t.Fatal("the middle of the icon is transparent: nothing was drawn")
	}
}

func TestAppIconOfAnInstalledApp(t *testing.T) {
	if _, err := os.Stat(testIconApp); err != nil {
		t.Skip("no Calculator.app on this Mac")
	}
	fakeAppIcons(t, nil)  // only for the empty cache and its cleanup
	appIconRender = nativeAppIcon
	url, err := (Cleaner{}).AppIcon(testIconApp)
	if err != nil || !strings.HasPrefix(url, "data:image/png;base64,") || len(url) < 1000 {
		t.Fatalf("AppIcon = %.60q…, %v", url, err)
	}
}
```

Run: `go test -count=1 -run 'AppIcon|Bindings|SlashPath' .`
Expected: PASS. If the sandbox cannot draw (for example, Icon Services is unreachable), say so in the report. The reviewer runs it outside.

- [ ] **Step 5: Regenerate the bindings**

Run: `bun run generate`
Expected: `src/mygo.ts` gains `appIcon(path: string): Promise<string>` on `Cleaner`, and nothing else changes.

- [ ] **Step 6: Write the web side**

`src/react/icons/api.ts`:

```ts
import { Cleaner } from "../../mygo";

/** App 图标的桥调用集中在此，组件不直接碰桥。 */
export const iconApi = {
  /** .app 的图标（80 px PNG 的 data URL）；没有时为 ""。 */
  appIcon: (path: string): Promise<string> => Cleaner.appIcon(path),
};
```

`src/react/icons/cache.ts`:

```ts
// App 图标的缓存：同一个 .app 整个会话只向 Go 要一次，几个页面共用。不依赖 DOM / React，可单测。
import { iconApi } from "./api";

const icons = new Map<string, string>(); // .app 路径 → data URL；"" 表示没有图标
const pending = new Map<string, Promise<string>>();
let ask: (path: string) => Promise<string> = (path) => iconApi.appIcon(path);

/** 只有 .app 包才有图标可要：命令行、系统进程和 Windows 的路径都用首字母。 */
export function hasAppIcon(path: string): boolean {
  return path.startsWith("/") && /\.app\/?$/i.test(path);
}

/** 已经知道的图标（"" 表示没有）；还没要到时为 undefined。 */
export function peekIcon(path: string): string | undefined {
  return icons.get(path);
}

/** 要一个图标：同一个路径同时只问一次；出错按没有图标记下，不再重试。 */
export function loadIcon(path: string): Promise<string> {
  const known = icons.get(path);
  if (known !== undefined) return Promise.resolve(known);
  let p = pending.get(path);
  if (!p) {
    p = ask(path).catch(() => "").then((url) => {
      icons.set(path, url);
      pending.delete(path);
      return url;
    });
    pending.set(path, p);
  }
  return p;
}

/** 测试用：换掉向 Go 要图标的函数，清空缓存。 */
export function resetIconsForTest(fn: (path: string) => Promise<string>): void {
  icons.clear();
  pending.clear();
  ask = fn;
}
```

`src/react/icons/AppIcon.tsx`:

```tsx
import * as React from "react";
import { hasAppIcon, loadIcon, peekIcon } from "./cache";

// 首字母圆标到了视口附近（200px）才开始要图标：长列表里看不到的行不要。
let observer: IntersectionObserver | null = null;
const waiting = new Map<Element, () => void>();

function whenVisible(el: Element | null, fn: () => void): () => void {
  if (!el || typeof IntersectionObserver === "undefined") {
    fn();
    return () => {};
  }
  observer ??= new IntersectionObserver((entries) => {
    for (const e of entries) {
      if (!e.isIntersecting) continue;
      const go = waiting.get(e.target);
      waiting.delete(e.target);
      observer?.unobserve(e.target);
      go?.();
    }
  }, { rootMargin: "200px" });
  waiting.set(el, fn);
  observer.observe(el);
  return () => {
    waiting.delete(el);
    observer?.unobserve(el);
  };
}

export interface AppIconProps {
  /** App 的 .app 路径；不是 .app 时一直显示首字母。 */
  path: string;
  /** 首字母圆标那个框的类名（st-ico、cz-growth-icon、app-icon）：图标放进同样大小的框。 */
  box: string;
  /** 首字母圆标：图标到之前、或者没有图标时显示。 */
  children: React.ReactElement<{ ref?: React.Ref<HTMLElement> }>;
}

/** App 的真实图标；还没到或者没有时是首字母圆标。 */
export function AppIcon({ path, box, children }: AppIconProps) {
  const want = hasAppIcon(path);
  const url = want ? peekIcon(path) : undefined;
  const ref = React.useRef<HTMLElement>(null);
  const [, arrived] = React.useReducer((n: number) => n + 1, 0);
  const missing = want && url === undefined;
  React.useEffect(() => {
    if (!missing) return;
    let live = true;
    const stop = whenVisible(ref.current, () => {
      void loadIcon(path).then(() => {
        if (live) arrived();
      });
    });
    return () => {
      live = false;
      stop();
    };
  }, [path, missing]);
  if (url) return <img className={`${box} cz-app-img`} src={url} alt="" aria-hidden="true" draggable={false} />;
  return React.cloneElement(children, { ref });
}
```

If TypeScript rejects the `cloneElement` typing, adjust the types only. Do not change the behavior.

- [ ] **Step 7: Use it in the four lists**

Wrap each letter avatar in `AppIcon`. The letter element stays exactly as it is; it becomes the child. Add `import { AppIcon } from "../icons/AppIcon";` to each file.

- `src/react/process/ProcessPage.tsx`, in `ProcessRowView`'s `label`:

  ```tsx
      <AppIcon path={row.kind === "app" ? row.path : ""} box="st-ico">
        <span className={`st-ico${row.kind === "app" ? "" : " pr-ico-sys"}`} style={row.kind === "app" ? { background: iconColor(row.name) } : undefined} aria-hidden="true">
          {initial(row.name)}
        </span>
      </AppIcon>
  ```

- `src/react/status/StatusPage.tsx`, in the 「占用最多的应用」 row:

  ```tsx
                      <AppIcon path={r.path} box="st-ico">
                        <div className="st-ico" style={{ background: iconColor(r.name) }}>{r.name.charAt(0).toUpperCase()}</div>
                      </AppIcon>
  ```

- `src/react/growth/GrowthPage.tsx`, in the row's name:

  ```tsx
                      <AppIcon path={a.appPath} box="cz-growth-icon">
                        <span className="cz-growth-icon" style={{ background: iconColor(a.name) }} aria-hidden>
                          {(a.name.replace(/^\.+/, "") || a.name).charAt(0).toUpperCase()}
                        </span>
                      </AppIcon>
  ```

- `src/react/apps/InstalledTab.tsx`, in `InstalledAppRow`:

  ```tsx
        <AppIcon path={a.path} box="app-icon">
          <div className="app-icon">{a.name.charAt(0).toUpperCase()}</div>
        </AppIcon>
  ```

The prop is named `box`, not `className`. That is on purpose: `tests/growth-react.test.ts` finds the avatars by `className === "cz-growth-icon"`, and the wrapper must not match.

- [ ] **Step 8: Style the image (`src/style.css`)**

Add after the `.st-ico { … }` rule:

```css
/* App 的真实图标替换首字母圆标：用同一个框的尺寸；PNG 自带形状和阴影，不要底色和圆角。 */
img.cz-app-img { background: none; border-radius: 0; box-shadow: none; object-fit: contain; }
```

- [ ] **Step 9: Write `tests/app-icon.test.ts`** and add it to `test:web` in `package.json` (append ` && bun test tests/app-icon.test.ts`)

```ts
import { expect, test } from "bun:test";
import { createElement } from "react";

const { hasAppIcon, loadIcon, peekIcon, resetIconsForTest } = await import("../src/react/icons/cache");

test("hasAppIcon: only absolute .app bundle paths", () => {
  expect(hasAppIcon("/Applications/Safari.app")).toBe(true);
  expect(hasAppIcon("/Applications/Safari.APP/")).toBe(true);
  expect(hasAppIcon("")).toBe(false);
  expect(hasAppIcon("Safari.app")).toBe(false);
  expect(hasAppIcon("/usr/local/bin/node")).toBe(false);
  expect(hasAppIcon("/Applications/Safari.app/Contents/MacOS/Safari")).toBe(false);
  expect(hasAppIcon("C:\\Program Files\\Acme\\acme.exe")).toBe(false);
});

test("loadIcon asks once per path, shares the answer and remembers it", async () => {
  const asked: string[] = [];
  resetIconsForTest(async (p) => { asked.push(p); return "data:image/png;base64,QQ=="; });
  expect(peekIcon("/A.app")).toBeUndefined();
  const [a, b] = await Promise.all([loadIcon("/A.app"), loadIcon("/A.app")]);
  expect(a).toBe("data:image/png;base64,QQ==");
  expect(b).toBe(a);
  expect(await loadIcon("/A.app")).toBe(a);
  expect(peekIcon("/A.app")).toBe(a);
  expect(asked).toEqual(["/A.app"]);
});

test("a failed or empty answer is remembered as no icon", async () => {
  let calls = 0;
  resetIconsForTest(async (p) => { calls++; if (p === "/Bad.app") throw new Error("bridge"); return ""; });
  expect(await loadIcon("/Bad.app")).toBe("");
  expect(await loadIcon("/Bad.app")).toBe("");
  expect(await loadIcon("/None.app")).toBe("");
  expect(peekIcon("/None.app")).toBe("");
  expect(calls).toBe(2);
});

test("AppIcon shows the letter until the icon is known, then the image in the same box", async () => {
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppIcon } = await import("../src/react/icons/AppIcon");
  resetIconsForTest(async () => "data:image/png;base64,AAAA");
  const letter = createElement("div", { className: "st-ico", style: { background: "#f00" } }, "S");
  const render = (path: string) => renderToStaticMarkup(createElement(AppIcon, { path, box: "st-ico" }, letter));
  const before = render("/Applications/Safari.app");
  expect(before).toBe('<div class="st-ico" style="background:#f00">S</div>');
  await loadIcon("/Applications/Safari.app");
  const after = render("/Applications/Safari.app");
  expect(after).toContain('<img class="st-ico cz-app-img" src="data:image/png;base64,AAAA" alt=""');
  expect(after).toContain('aria-hidden="true"');
  expect(render("")).toBe(before);
});
```

Add to `tests/process-react.test.ts`:

```ts
test("rows: an app row shows its icon once known; other rows keep the letter", async () => {
  const { loadIcon, resetIconsForTest } = await import("../src/react/icons/cache");
  resetIconsForTest(async () => "data:image/png;base64,QUJD");
  await loadIcon(chrome.path);
  const app = await rowHtml({ row: chrome });
  expect(app).toContain('class="st-ico cz-app-img" src="data:image/png;base64,QUJD"');
  const cli = await rowHtml({ row: node });
  expect(cli).toContain('class="st-ico pr-ico-sys"');
  expect(cli).not.toContain("<img");
});
```

Add to `tests/apps-react.test.ts`, following the existing `InstalledAppRow` test's imports and `app(...)` helper:

```ts
test("installed app row shows the app's icon once known", async () => {
  await import("./setup-dom");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { InstalledAppRow } = await import("../src/react/apps/InstalledTab");
  const { loadIcon, resetIconsForTest } = await import("../src/react/icons/cache");
  const a = app({ id: "acme", name: "Acme", path: "/Applications/Acme.app" });
  expect(renderToStaticMarkup(InstalledAppRow({ app: a, moving: false, onDetail() {} }))).toContain('<div class="app-icon">A</div>');
  resetIconsForTest(async () => "data:image/png;base64,QUJD");
  await loadIcon("/Applications/Acme.app");
  expect(renderToStaticMarkup(InstalledAppRow({ app: a, moving: false, onDetail() {} }))).toContain('class="app-icon cz-app-img"');
});
```

- [ ] **Step 10: Verify**

Run:
- `gofmt -l .`
- `go vet ./... && GOOS=windows GOARCH=amd64 go vet ./...`
- `go test -count=1 -run 'AppIcon|Bindings|SlashPath' .`
- `bun run typecheck`
- `bun run test:web`

Expected: all pass, including the unchanged `tests/growth-react.test.ts` and `tests/status-react.test.ts`.

---

### Task 3: Steadier row order and the docs

**Files:**
- Modify: `src/react/process/format.ts`, `tests/process-react.test.ts`, `CHANGELOG.md`, `docs/superpowers/specs/2026-10-26-process-page-design.md`

**Interfaces:** `sortRows` and `sortChildren` keep their signatures.

- [ ] **Step 1: Write the failing test** (add to `tests/process-react.test.ts`; `GB` is already defined there)

```ts
test("sortRows compares the numbers the page shows; equal ones keep name order", () => {
  const MB = 2 ** 20;
  const a = row({ key: "a", name: "a", cpu: 0.2, memory: 512.4 * MB });
  const b = row({ key: "b", name: "b", cpu: 0.4, memory: 512.2 * MB });
  expect(sortRows([b, a], "cpu").map((r) => r.name)).toEqual(["a", "b"]); // both 0%
  expect(sortRows([b, a], "mem").map((r) => r.name)).toEqual(["a", "b"]); // both 512 MB
  const c = row({ key: "c", name: "c", cpu: 0.6 }); // 1%
  expect(sortRows([a, b, c], "cpu").map((r) => r.name)).toEqual(["c", "a", "b"]);
  const x = row({ key: "x", name: "x", memory: 1.01 * GB }); // 1 GB
  const y = row({ key: "y", name: "y", memory: 1.04 * GB }); // 1 GB
  const z = row({ key: "z", name: "z", memory: 1.06 * GB }); // 1.1 GB
  expect(sortRows([y, x, z], "mem").map((r) => r.name)).toEqual(["z", "x", "y"]);
  const kids = [
    { name: "q", count: 1, cpu: 0.3, memory: 0, estimated: false },
    { name: "p", count: 1, cpu: 0.1, memory: 0, estimated: false },
  ];
  expect(sortChildren(kids, "cpu").map((k) => k.name)).toEqual(["p", "q"]); // both 0%
});
```

Run: `bun test tests/process-react.test.ts`
Expected: FAIL. The raw numbers put `b` before `a` and `y` before `x`.

- [ ] **Step 2: Sort by the shown values (`src/react/process/format.ts`)**

Import the constants that `fmtMem` uses (`import { GiB, MiB } from "../status/format";`), add the two helpers, and change both comparators:

```ts
/** CPU 按显示的整数百分比比（fmtPct）：显示一样的行按名字排，不会每次采样都换位置。 */
function shownCpu(v: number): number {
  return Math.max(0, Math.round(v));
}

/** 内存按显示的精度比（fmtMem）：1 GB 以上到 0.1 GB，以下到 1 MB。 */
function shownMem(n: number): number {
  return n >= GiB ? (Math.round((n / GiB) * 10) / 10) * GiB : Math.round(n / MiB) * MiB;
}

/** 按 CPU 或内存从高到低，比的是页面上显示的数，同值按名字；返回新数组，不改采样。 */
export function sortRows(rows: readonly ProcessRow[], by: "cpu" | "mem"): ProcessRow[] {
  return [...rows].sort((a, b) => (by === "cpu" ? shownCpu(b.cpu) - shownCpu(a.cpu) : shownMem(b.memory) - shownMem(a.memory)) || a.name.localeCompare(b.name));
}

/** 展开后的进程：和表一样按当前标签、按显示的数排。 */
export function sortChildren(children: readonly ProcessChild[], by: "cpu" | "mem"): ProcessChild[] {
  return [...children].sort((a, b) => (by === "cpu" ? shownCpu(b.cpu) - shownCpu(a.cpu) : shownMem(b.memory) - shownMem(a.memory)) || a.name.localeCompare(b.name));
}
```

Run: `bun test tests/process-react.test.ts && bun run typecheck`
Expected: PASS.

- [ ] **Step 3: CHANGELOG** — in `## 未发布`, add this bullet after the first one (the 「进程」 bullet), verbatim:

```
- App 显示自己的图标（macOS）：系统状态「占用最多的应用」、应用卸载的已安装列表，以及「进程」页和「磁盘占用」；命令行程序、系统进程和读不到图标的 App 仍用首字母
```

The other fixes in this plan only change features that are themselves still unreleased, so they get no entry.

- [ ] **Step 4: Spec** — in `docs/superpowers/specs/2026-10-26-process-page-design.md`:

Replace the **样式** line in §二 with:

```
- **样式**：沿用系统状态「占用最多的应用」那一套：白底卡片、App 自己的图标、「N 个进程」、平时不显眼的「退出」按钮。命令行程序、系统进程和读不到图标的 App 用字母头像。（2026-10-27 真机测试后维护者要求「应用要显示真实的logo」；系统状态、磁盘占用和应用卸载的列表也一样。）
```

In §八, after the **实现时改的三处（2026-10-27）** list, add:

```
- **真机测试后改的（2026-10-27）**：正式版进程页开着、在 CPU 标签时，主进程 3.04% 加 `ps` 约 0.6%，超了 3% 的上限。
  - `ps` 改用 `posix_spawn` 启动。Go 的 `os/exec` 用 fork，而 fork 前要跑这个进程里所有的 atfork 处理：WebKit 带进来的 Network 框架要等路径监视器的锁，malloc 要锁住所有 zone。真机采样里每次约 56 毫秒 CPU，每 6 秒一次，单这一项就约 1%。其他调用外部命令的地方不变。
  - 其他账户进程的完整路径按 pid 记住：`ps` 还用同一个短名字列出这个 pid 时，不再问 `proc_pidpath`。
  - 只有一个进程的行不带子进程列表（展开要两个以上），每次采样的数据小了约四分之一。
  - 列表按显示出来的数排序（整数百分比，0.1 GB 或 1 MB），显示一样的按名字，不再随小数每两秒换位置。
  - 从访达、程序坞、聚焦或 `open` 重新打开时，总是把主窗口带到前面。
  - App 的真实图标：Go 在后台线程用 NSWorkspace 画 80 px 的 PNG，每个 .app 整个运行期间只画一次；页面只给滚到视口附近的行要图标。
```

- [ ] **Step 5: Verify**

Run: `bun run typecheck && bun run test:web`
Expected: PASS.

---

### Task 4: Go — a cheaper `ps` and remembered paths for this user's processes

Added after Tasks 1–3 were measured. The comparison alternated the page between running and paused in 30 s windows, with the production app in front. Running: main 2.71% plus `ps` 0.93% = 3.64%. Paused: 0.13%. Still over the 3% cap. A Go CPU profile of `TestProcessSamplingBudget` and timings on this Mac gave these costs:

| Cost | CPU |
|---|---|
| `ps -ax` | ~44 ms per run, all ~1300 processes |
| `ps -u <other accounts' effective uids>` | ~15 ms per run, the ~225 processes we need |
| `ps -p <pids>` | 76–89 ms per run, so it's not an option |
| `kern.proc.all` via `unix.SysctlKinfoProcSlice` | 0.39 ms; gives every process's effective uid |
| `nativeProcs` | 8.7 ms per sample |
| ↳ usage alone | 2.5 ms |
| ↳ proc_pidpath and proc_name per process | the remaining 6.2 ms |

Every process whose effective uid is this user's could be read by libproc, so `-u` by effective uid is the exact complement of `nativeProcs`.

**Files:**
- Modify: `native.go` (`nativeProcsKnown`), `process_darwin.go` (`foreignUIDs`, the `ps -u` command, `ownProcsReader`), `process_darwin_test.go`
- Modify: `native_other.go` and `native_windows.go` only if they must, to keep `nativeProcs` building. `nativeProcsKnown` is darwin-only and used only from `process_darwin.go`.

**Interfaces:**
- Produces:
  - `func nativeProcsKnown(known func(pid int32, start uint64) (path, name string, ok bool)) []procReading` (`native.go`);
  - `var processForeignUIDs = foreignUIDs`;
  - `var processOwnReadings = nativeProcsKnown`;
  - `func ownProcsReader() func() []procReading`;
  - `const processIdentityAge = 15`.
- Unchanged: `nativeProcs()` (the status page uses it). It becomes `nativeProcsKnown(nil)`.

- [ ] **Step 1: `nativeProcsKnown` (`native.go`)**

Rename the body of `nativeProcs` to `nativeProcsKnown(known)`, and make `nativeProcs` call `nativeProcsKnown(nil)`. Inside the loop, after reading `rusage` and filling `Pid`, `Footprint`, `UserNs`, `SysNs` and `Start`:

```go
		if known != nil {
			if path, name, ok := known(pid, r.Start); ok {
				r.Path, r.Name = path, name
				out = append(out, r)
				continue
			}
		}
```

Then the existing `procPidPathFn` and `procNameFn` reads follow, unchanged. Doc comment:

```go
// nativeProcsKnown is nativeProcs, except that a process known gives a path
// and name for, by pid and start time, keeps them instead of asking libproc
// again: those two lookups are most of a reading's cost.
```

- [ ] **Step 2: Remember this user's paths (`process_darwin.go`)**

```go
// processIdentityAge is how many readings a remembered path and name are
// kept before they're read again. A process can replace its program with
// exec and keep its pid and start time; 15 readings is 30 s.
const processIdentityAge = 15

// processOwnReadings reads this user's processes. Tests replace it.
var processOwnReadings = nativeProcsKnown

// ownProcsReader returns this user's processes for one sampling session.
// It remembers each process's path and name by pid and start time, and
// reads them again after processIdentityAge readings. The loop calls it
// from one goroutine.
func ownProcsReader() func() []procReading {
	type key struct {
		pid   int32
		start uint64
	}
	type entry struct {
		path, name string
		read       int // the reading that looked them up
	}
	known := map[key]entry{}
	n := 0
	return func() []procReading {
		n++
		readings := processOwnReadings(func(pid int32, start uint64) (string, string, bool) {
			e, ok := known[key{pid, start}]
			return e.path, e.name, ok && n-e.read < processIdentityAge
		})
		next := make(map[key]entry, len(readings))
		for _, r := range readings {
			k := key{r.Pid, r.Start}
			e, ok := known[k]
			if !ok || n-e.read >= processIdentityAge {
				e = entry{path: r.Path, name: r.Name, read: n}
			}
			next[k] = e
		}
		known = next
		return readings
	}
}
```

In `realProcessSource`, replace `procs: nativeProcs,` with `procs: ownProcsReader(),`.

- [ ] **Step 3: `ps` only for other accounts (`process_darwin.go`)**

```go
// processForeignUIDs lists the effective uids of other accounts' processes.
// Tests replace it.
var processForeignUIDs = foreignUIDs

// foreignUIDs returns, sorted, every effective uid other than this user's
// that a process runs as: the processes libproc can't read. One sysctl
// (kern.proc.all) of about 0.4 ms.
func foreignUIDs() ([]uint32, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	me := uint32(os.Geteuid())
	seen := map[uint32]bool{}
	var out []uint32
	for _, p := range procs {
		if u := p.Eproc.Ucred.Uid; u != me && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	slices.Sort(out)
	return out, nil
}

// psArgs is the ps command for the accounts in uids: -u by effective uid,
// which costs about a third of -ax because ps then inspects only those
// accounts' processes. Without uids it falls back to -ax.
func psArgs(uids []uint32) []string {
	args := []string{"/bin/ps", "-o", "pid=,uid=,pcpu=,rss=,ucomm="}
	if len(uids) == 0 {
		return append(args, "-ax")
	}
	list := make([]string, len(uids))
	for i, u := range uids {
		list[i] = strconv.FormatUint(uint64(u), 10)
	}
	return append(args, "-u", strings.Join(list, ","))
}
```

In `readOtherProcsWith`, replace the `argv := …` line with:

```go
	uids, err := processForeignUIDs()
	if err != nil {
		uids = nil // ps -ax then lists everyone; the uid filter below still applies
	}
	argv := psArgs(uids)
```

The rest of `readOtherProcsWith` is unchanged: the self-pid filter, the uid filter in `parsePsOutput`, and the path memory. Imports: `slices`, `strconv` and `golang.org/x/sys/unix`, which is already a module dependency. The fallback to `-ax` keeps today's behavior whenever the sysctl fails.

- [ ] **Step 4: Tests (`process_darwin_test.go`)**

- **`fakeProcessPs`:**
  - Also set `processForeignUIDs` to return `[]uint32{0, 7}` (restore it in the cleanup).
  - Expect the argv `"/bin/ps -o pid=,uid=,pcpu=,rss=,ucomm= -u 0,7"` instead of the `-ax` form.
  - The stand-in helper is unchanged.
- **New `TestPsArgsSelectsOtherAccounts`:**
  - `psArgs(nil)` ends in `-ax`;
  - `psArgs([]uint32{0, 88, 202})` ends in `-u 0,88,202`.
- **New `TestForeignUIDsListsOtherAccounts`** (real system):
  - the result is sorted and non-empty;
  - it contains 0 (launchd is root);
  - it doesn't contain `os.Geteuid()`.
- **New `TestReadOtherProcsFallsBackWithoutUIDs`:** with `processForeignUIDs` returning an error, the argv ends in `-ax` and the reading still succeeds. Use `fakeProcessPs` and override `processForeignUIDs` after it, with the argv check relaxed for this test. A small `fakeProcessPsArgs(t, mode, wantArgv)` helper is fine.
- **New `TestOwnProcsReaderRemembersPathsUntilTheyAge`:**
  - Replace `processOwnReadings` with a fake. It calls `known` for a fixed list of `(pid, start)` pairs, records which ones came back `ok == false` ("looked up"), and returns readings with a path built from the pid.
  - Check, in order:
    - the first call looks everything up;
    - calls 2 to 15 look nothing up;
    - call 16 looks everything up again, because of the age;
    - a pid whose start time changed is looked up at once, because the pid was reused;
    - a pid that vanished and came back is looked up again.
- **New `TestNativeProcsKnownKeepsWhatItIsGiven`** (real system):
  - With a `known` that answers `("/fake/self", "fake", true)` only for `os.Getpid()`: this process's reading has that path and name, and another process of this user (pick one from `nativeProcs()`) still has its real path.
  - `nativeProcs()` still reads this process's real path.

- [ ] **Step 5: Verify**

Run:
- `gofmt -l .`
- `go vet ./... && GOOS=windows GOARCH=amd64 go vet ./...`
- `go test -count=1 -run 'Spawn|OtherProcs|ParsePsCommand|ReadOtherProcs|WatchProcesses|GroupProcesses|PsArgs|ForeignUIDs|OwnProcs|NativeProcs|Bindings|SlashPath' .`
- `CLEANZO_PERF=1 go test -count=1 -run '^TestProcessSamplingBudget$' -v .`

Report the budget line. It was 2.88% before this task, inflated by the test's stubbed app list, which sends every bundle to plutil.

---

### Task 5: Go — ask NSWorkspace for the running apps every fifth reading

Added after Task 4. The alternating measurement of the build with Tasks 1–4 gave running minus paused = 3.26% (main ≈ 2.9% + ps 0.39%). A Go CPU profile of the production app on the 进程 CPU tab (60 s, `runtime/pprof` in a scratch build) gave 2.24 s, 3.73% of a core. **`main.workspaceList` took 790 ms of it (35%, about 1.3% of a core).** That is NSWorkspace's list of about 320 running apps, read on the main thread on every 2 s reading. The sampling loop itself took 390 ms: libproc 240 ms, grouping 110 ms. The list rarely changes, and groupProcesses uses it only for bundle IDs, localized names and whether a row can be quit. Quitting reads its own fresh list.

**Files:** Modify `process_darwin.go` and `process_darwin_test.go`.

- [ ] **Step 1: Read the list every fifth time (`process_darwin.go`)**

```go
// processAppsEvery is how often, in readings, NSWorkspace's running-app list
// is read again. With about 320 running apps a read took about 25 ms of
// main-thread time (in-app profile, 2026-10-27), about 1.3% of a core at one
// read per 2 s reading, and the list rarely changes. A just-launched app gets
// its name, bundle ID and quit button within 10 s; quitting reads its own
// fresh list.
const processAppsEvery = 5

// processAppList reads the running apps. Tests replace it.
var processAppList = workspaceList

// appsReader returns the running-app list for one sampling session: read at
// the first reading and every processAppsEvery-th after it, and retried at
// the next reading when a read fails (nil). The loop calls it from one
// goroutine.
func appsReader() func() []workspaceApp {
	var apps []workspaceApp
	age := processAppsEvery // the first call reads
	return func() []workspaceApp {
		if age >= processAppsEvery || apps == nil {
			if fresh := processAppList(); fresh != nil {
				apps, age = fresh, 0
			}
		}
		age++
		return apps
	}
}
```

In `realProcessSource`, replace `apps: workspaceList,` with `apps: appsReader(),`.

- [ ] **Step 2: Test (`process_darwin_test.go`)**

`TestAppsReaderReadsEveryFifthReading`:
- Replace `processAppList` (restore it in a cleanup) with a fake that counts calls and returns `[]workspaceApp{{Name: fmt.Sprint(call)}}`.
- Call the reader 11 times. Expect the fake to be called on calls 1, 6 and 11. Calls 2–5 return the list from call 1, and calls 7–10 the list from call 6.
- A second reader whose fake returns nil the first time and a list the second time: call 1 returns nil, call 2 reads again and returns the list, and call 3 reuses it.

- [ ] **Step 3: Verify**

Run:
- `gofmt -l .`
- `go vet ./... && GOOS=windows GOARCH=amd64 go vet ./...`
- `go test -count=1 -run 'AppsReader|WatchProcesses|GroupProcesses|OwnProcs|OtherProcs|ReadOtherProcs|ParsePsCommand|Bindings|SlashPath' .`

---

## Reviewer verification (on the maintainer's Mac)

Run after the three tasks are on `feat/growth-monitor`.

1. **Suites:**
   - `gofmt -l .`
   - `go vet ./... && GOOS=windows GOARCH=amd64 go vet ./...`
   - `bun run typecheck`
   - `bun run test:web`
   - `go test -race -count=1 ./...`
   - `bun run build`
2. **CPU, production build:**
   - Open the 进程 page on the CPU tab and let it warm up for 20 s, so the icons are drawn.
   - Measure 60 s with `proc_pid_rusage` (`RUSAGE_INFO_V2`) on the app's pid. Use user+system at bytes 16 and 24, plus the children's user+system at 96 and 104.
   - Pass: the main process plus children ≤ 3% of a core. WebContent, measured the same way on its pid, ≤ 3%.
   - Also record the main process alone, to compare with the 3.04% before the fix.
3. **`ps` still runs, gets reaped, and stops:**
   - While on the CPU tab, `pgrep -f '^/bin/ps -ax -o pid=,uid='` sees it about every 6 s.
   - No zombie children of Cleanzo (`ps -o ppid=,stat=` shows no `Z` with Cleanzo's pid).
   - On pause it stops, the same as before.
4. **Icons:** take window-only captures (`screencapture -x -o -l <id>`).
   - 进程 CPU and 内存 show app icons, with letters for command-line and system rows.
   - 系统状态 「占用最多的应用」 shows icons.
   - 磁盘占用 shows icons where an app is installed.
   - 应用卸载 → 已安装 shows icons, which fill in while scrolling.
5. **Reopen:**
   - Close the window with the menu bar icon showing, then `open` the app: the window comes back.
   - Minimize it, then `open`: it is restored.

## Execution batches

| Run | Tool | Task | Worktree |
|-----|------|------|----------|
| A | Codex (`codex:codex-rescue`, `task --write`) | Task 1 | `../cleanzo-fix-cpu`, branch `fix-cpu` from `9973f0e` |
| B | traecli | Task 2 | `../cleanzo-fix-icons`, branch `fix-icons` from `9973f0e`, with `node_modules` linked |
| C | traecli | Task 3 | `../cleanzo-fix-sort`, branch `fix-sort` from `9973f0e`, with `node_modules` linked |
| D | Codex (`codex:codex-rescue`, `task --write`) | Task 4 | `../cleanzo-fix-cpu2`, branch `fix-cpu2` from the integrated `feat/growth-monitor` head (Tasks 1–3 already on it) |
| E | traecli | Task 5 | the main checkout, on `feat/growth-monitor` after Task 4's fix round |

The runs touch disjoint files and run in parallel. Each task is reviewed in its worktree, then cherry-picked onto `feat/growth-monitor`.

## Implementation notes (2026-10-27)

- **Task 1:**
  - The reopen change (Step 7) was made and then reverted; `growth_app.go` is as before. The "no window after `open`" seen in testing was the screen being locked: System Events lists no windows and window capture fails for every app while locked.
  - The path-memory test's last step expects lookups of pids 10 and 11. Pid 10's name goes `other` → `tool`, so it is looked up again.
- **Task 2:** inside a sandbox without Icon Services, NSWorkspace returns a near-transparent placeholder. That is why the native test requires a fully opaque centre.
- **Task 3:**
  - The 样式 line is in §三, not §二.
  - The boundary test compares amounts parsed back from the text, because "1024 MB" and "1 GB" are both 1 GiB, and "—" and "0 MB" are both zero.
