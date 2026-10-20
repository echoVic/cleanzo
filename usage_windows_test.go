package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// usageHelper selects the child role when this test binary is re-executed.
var usageHelper = flag.Bool("cleanzo-usage-helper", false, "run the usage snapshot helper")

// usageHelperDir keeps the helper's real command line representative of a Chromium process.
var usageHelperDir = flag.String("user-data-dir", "", "helper user data path")

// TestWindowsUsageHelper stays alive until the parent closes its stdin pipe.
func TestWindowsUsageHelper(t *testing.T) {
	if !*usageHelper {
		return
	}
	if *usageHelperDir == "" {
		t.Fatal("missing helper directory")
	}
	fmt.Fprintln(os.Stdout, "ready")
	io.Copy(io.Discard, os.Stdin)
}

// TestWindowsUsageChildProcess protects both ancestors and descendants of a live --user-data-dir path.
func TestWindowsUsageChildProcess(t *testing.T) {
	env := testEnv(t)
	dir := filepath.Join(env.home, "AppData", "Local", "Fake", "User Data")
	cache := filepath.Join(dir, "Default", "Cache")
	path := filepath.Join(cache, "share-delete.bin")
	writeFile(t, path, 123)
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(path), windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	// The open handle allows deletion/rename, as Chromium does; locking alone is insufficient.
	if err := retryWindowsFileOperation(func() error { return os.Rename(path, path+".moved") }); err != nil {
		t.Fatal(err)
	}
	if err := retryWindowsFileOperation(func() error { return os.Rename(path+".moved", path) }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsUsageHelper$", "-cleanzo-usage-helper", "--user-data-dir="+dir)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	readerStopped := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		input.Close()
		if !waited {
			cmd.Process.Kill()
			cmd.Wait()
		}
		<-readerStopped
	})
	ready := make(chan string, 1)
	go func() { defer close(readerStopped); line, _ := bufio.NewReader(output).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("child did not start: %q %s", line, stderr.String())
		}
	case <-ctx.Done():
		t.Fatal("child startup timed out")
	}
	procs, err := processSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range procs {
		if p.Pid == uint32(cmd.Process.Pid) {
			found = true
			if p.PPid != uint32(os.Getpid()) || p.Exe == "" || p.Name == "" || p.Created.IsZero() || !strings.Contains(p.CmdLine, "--user-data-dir=") {
				t.Fatalf("incomplete child snapshot: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("child absent from current-session snapshot")
	}
	// Keep the process object alive after Wait, as an external observer can do.
	retained, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(retained)
	guard := probeUsage()
	if guard.failed {
		t.Fatal("snapshot failed")
	}
	parent := filepath.Dir(dir)
	for _, p := range []string{path, cache, dir, parent} {
		if !guard.inUse(resolvePath(p)) {
			t.Errorf("live root unprotected: %s", p)
		}
	}
	env.guard = guard
	env.settings.Custom = []CustomFolder{{Name: "Fake", Path: parent, Enabled: true}}
	results := cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + parent}}, time.Time{}, cleanHooks{})
	if len(results) != 1 || results[0].Freed != 0 || !exists(path) {
		t.Fatalf("running process cache cleaned: %+v", results)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("child: %v %s", err, stderr.String())
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(retained, &exitCode); err != nil {
		t.Fatal(err)
	}
	if exitCode != 0 {
		t.Fatalf("helper exit code=%d after successful Wait", exitCode)
	}
	savedSnapshot := snapshotProcesses
	var after []procInfo
	var snapshotErr error
	snapshotProcesses = func() ([]procInfo, error) { after, snapshotErr = savedSnapshot(); return after, snapshotErr }
	guard = probeUsage()
	snapshotProcesses = savedSnapshot
	childPresent := false
	for _, p := range after {
		if p.Pid == uint32(cmd.Process.Pid) {
			childPresent = true
		}
	}
	if guard.failed || guard.inUse(resolvePath(cache)) || guard.inUse(resolvePath(parent)) || snapshotErr != nil || childPresent {
		logWindowsUsageGuard(t, guard, cache, parent)
		t.Logf("child PID=%d exitCode=%d retainedHandle=%#x snapshotErr=%v childPresent=%t", cmd.Process.Pid, exitCode, retained, snapshotErr, childPresent)
		logWindowsUsageProcesses(t, guard, after, uint32(cmd.Process.Pid), cache, parent)
		t.Fatalf("exited process still owns user data: guard.failed=%t childPresent=%t snapshotErr=%v", guard.failed, childPresent, snapshotErr)
	}
}

// TestWindowsUsageSnapshotFailure verifies native failure propagation through every deletion guard.
func TestWindowsUsageSnapshotFailure(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, "cleanup", "keep.bin")
	writeFile(t, path, 123)
	saved := createProcessSnapshot
	createProcessSnapshot = func(uint32, uint32) (windows.Handle, error) {
		return windows.InvalidHandle, windows.ERROR_ACCESS_DENIED
	}
	t.Cleanup(func() { createProcessSnapshot = saved })
	guard := probeUsage()
	if !guard.failed || !guard.inUse(resolvePath(path)) || !guard.inUse(`C:\anything`) || !toolBusy(nil) {
		t.Fatal("snapshot failure did not fail closed")
	}
	if _, _, err := runningApps(); err == nil {
		t.Fatal("runningApps ignored snapshot failure")
	}
	if _, err := psCommands(); err == nil {
		t.Fatal("psCommands ignored snapshot failure")
	}
	if _, err := (Cleaner{}).TrashPath(path); !errors.Is(err, errUsageUnknown) {
		t.Fatalf("TrashPath=%v", err)
	}
	if result := (Cleaner{}).CleanPaths([]string{path}); result.Moved != 0 || len(result.Failed) == 0 || !exists(path) {
		t.Fatalf("CleanPaths=%+v", result)
	}
	env.guard = guard
	env.settings.Custom = []CustomFolder{{Name: "cleanup", Path: filepath.Dir(path), Enabled: true}}
	result := cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + filepath.Dir(path)}}, time.Time{}, cleanHooks{})
	if len(result) != 1 || result[0].Freed != 0 || !exists(path) {
		t.Fatalf("CleanJunk=%+v", result)
	}
}

// TestWindowsUsageNamesAndRoots covers product names, partial entries and one shared snapshot.
func TestWindowsUsageNamesAndRoots(t *testing.T) {
	env := testEnv(t)
	exe := filepath.Join(env.home, "Programs", "Fake", "fake.exe")
	data := filepath.Join(env.home, "data", "User Data")
	writeFile(t, exe, 1)
	mustMkdir(t, data)
	saved := snapshotProcesses
	t.Cleanup(func() { snapshotProcesses = saved })
	calls := 0
	snapshotProcesses = func() ([]procInfo, error) {
		calls++
		return []procInfo{{Exe: exe, Name: "fake.exe", Product: "Fake Product", CmdLine: `fake.exe --user-data-dir="` + data + `"`}, {Name: "unreadable.exe"}}, nil
	}
	guard := probeUsage()
	if calls != 1 || guard.failed || !guard.inUse(filepath.Join(env.home, "Fake Product")) || !guard.inUse(filepath.Join(env.home, "unreadable")) || !guard.inUse(filepath.Join(data, "Cache")) || !guard.inUse(filepath.Dir(exe)) {
		t.Fatalf("incomplete guard: %+v calls=%d", guard, calls)
	}
	ids, apps, err := runningApps()
	if err != nil || !ids["fake"] || !ids["fake product"] || !ids["unreadable"] || len(apps) != 1 || apps[0] != exe {
		t.Fatalf("ids=%v apps=%v err=%v", ids, apps, err)
	}
	commands, err := psCommands()
	if err != nil || !strings.Contains(string(commands), "--user-data-dir=") {
		t.Fatalf("commands=%s err=%v", commands, err)
	}
}

// TestWindowsProcessProduct exercises real version-resource translation and string decoding.
func TestWindowsProcessProduct(t *testing.T) {
	exe := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	if product := processProduct(exe); product == "" {
		t.Fatal("cmd.exe has no readable FileDescription or ProductName")
	}
}

// TestWindowsCommandPaths uses CommandLineToArgvW for quoted flag values, escaped quotes and trailing backslashes.
func TestWindowsCommandPaths(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{`"C:\Program Files\Fake\fake.exe" --user-data-dir="C:\Users\A\User Data" --cache C:\Users\A\cache`, []string{`C:\Program Files\Fake\fake.exe`, `C:\Users\A\User Data`, `C:\Users\A\cache`}},
		{`fake.exe "--user-data-dir=C:\Users\A\User Data" --x=relative https://example.com:80`, []string{`C:\Users\A\User Data`}},
		{`fake.exe --label="a\" b" --user-data-dir="C:\Users\A\User Data"`, []string{`C:\Users\A\User Data`}},
		{`fake.exe --user-data-dir="C:\Users\A\User Data\\" --cache="C:\Users\A\Cache"`, []string{`C:\Users\A\User Data\`, `C:\Users\A\Cache`}},
	} {
		args, err := windows.DecomposeCommandLine(tc.command)
		if err != nil {
			t.Fatal(err)
		}
		if got := windowsArgumentPaths(args); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("paths=%q want %q", got, tc.want)
		}
	}
}

// TestWindowsUsageNamespacePaths accepts local namespaces and ignores UNC and pipe arguments without disabling cleanup.
func TestWindowsUsageNamespacePaths(t *testing.T) {
	env := testEnv(t)
	dir := filepath.Join(env.home, "data")
	mustMkdir(t, dir)
	saved := snapshotProcesses
	t.Cleanup(func() { snapshotProcesses = saved })
	for _, prefix := range []string{`\\?\`, `\\.\`, `\??\`} {
		snapshotProcesses = func() ([]procInfo, error) {
			return []procInfo{{Name: "fake.exe", CmdLine: `fake.exe --user-data-dir="` + prefix + dir + `"`}}, nil
		}
		if guard := probeUsage(); guard.failed || !guard.inUse(filepath.Join(dir, "Cache")) {
			t.Fatalf("local namespace unprotected: %q %+v", prefix, guard)
		}
	}
	for _, arg := range []string{`\\server\share\data`, `\\.\pipe\docker_engine`} {
		snapshotProcesses = func() ([]procInfo, error) {
			return []procInfo{{Name: "fake.exe", CmdLine: `fake.exe --user-data-dir="` + arg + `"`}}, nil
		}
		if guard := probeUsage(); guard.failed || guard.inUse(filepath.Join(dir, "Cache")) {
			t.Fatalf("nonlocal argument blocks cleanup: %q %+v", arg, guard)
		}
	}
}

// logWindowsUsageGuard identifies each bidirectional root and app identity behind a failed usage assertion.
func logWindowsUsageGuard(t *testing.T, guard *usageGuard, paths ...string) {
	t.Helper()
	t.Logf("usage guard: failed=%t roots=%d runningIDs=%d", guard.failed, len(guard.roots), len(guard.running))
	for _, path := range paths {
		key := strings.ToLower(resolvePath(path))
		var roots, ids []string
		for _, root := range guard.roots {
			if within(key, root) || within(root, key) {
				roots = append(roots, fmt.Sprintf("%q (pathWithinRoot=%t rootWithinPath=%t)", root, within(key, root), within(root, key)))
			}
		}
		for id := range guard.running {
			if namedAfterBundle(filepath.Base(key), map[string]bool{id: true}) {
				ids = append(ids, id)
			}
		}
		sort.Strings(roots)
		sort.Strings(ids)
		t.Logf("path=%q resolved=%q inUse=%t matchingRoots=%v matchingRunningIDs=%q", path, key, guard.inUse(key), roots, ids)
	}
}

// logWindowsUsageProcesses reports relevant process identities and source paths without dumping unrelated command lines.
func logWindowsUsageProcesses(t *testing.T, guard *usageGuard, procs []procInfo, child uint32, paths ...string) {
	t.Helper()
	roots := map[string]bool{}
	for _, root := range guard.roots {
		roots[root] = true
	}
	for _, p := range procs {
		relevant := p.Pid == child
		var sources []string
		if p.Exe != "" {
			sources = append(sources, filepath.Dir(p.Exe))
		}
		if args, err := windows.DecomposeCommandLine(p.CmdLine); err == nil {
			sources = append(sources, windowsArgumentPaths(args)...)
		}
		var matches []string
		for _, source := range sources {
			if local := windowsUsagePath(source); local != "" {
				key := usagePathKey(local)
				if !roots[key] {
					continue
				}
				for _, path := range paths {
					target := usagePathKey(path)
					if within(key, target) || within(target, key) {
						matches = append(matches, key)
						relevant = true
						break
					}
				}
			}
		}
		ids, _ := snapshotApps([]procInfo{p})
		var matchingIDs []string
		for id := range ids {
			if !guard.running[id] {
				continue
			}
			for _, path := range paths {
				if namedAfterBundle(filepath.Base(path), map[string]bool{id: true}) {
					matchingIDs = append(matchingIDs, id)
					relevant = true
					break
				}
			}
		}
		if relevant {
			var exitCode uint32
			handle, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, p.Pid)
			var exitErr error
			if openErr == nil {
				exitErr = windows.GetExitCodeProcess(handle, &exitCode)
				windows.CloseHandle(handle)
			}
			sort.Strings(matches)
			sort.Strings(matchingIDs)
			t.Logf("matching process: PID=%d PPID=%d name=%q exe=%q exitCode=%d openErr=%v exitErr=%v matchingPaths=%q matchingIDs=%q", p.Pid, p.PPid, p.Name, p.Exe, exitCode, openErr, exitErr, matches, matchingIDs)
		}
	}
}

// TestWindowsUsageExitStatusUnreadable retains live metadata when an individual exit-code query fails.
func TestWindowsUsageExitStatusUnreadable(t *testing.T) {
	saved := getProcessExitCode
	getProcessExitCode = func(windows.Handle, *uint32) error { return windows.ERROR_ACCESS_DENIED }
	t.Cleanup(func() { getProcessExitCode = saved })
	procs, err := processSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range procs {
		if p.Pid == uint32(os.Getpid()) {
			found = true
			if p.Exe == "" || p.Name == "" {
				t.Fatalf("unreadable exit status discarded available live metadata: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("live current process was discarded after exit query failure")
	}
	if guard := probeUsage(); guard.failed {
		t.Fatal("per-process exit query failure closed the entire guard")
	}
}
