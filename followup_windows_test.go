package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestWindowsUsagePerProcessErrors keeps ordinary cleanup available despite inaccessible or malformed processes.
func TestWindowsUsagePerProcessErrors(t *testing.T) {
	env := testEnv(t)
	exe := filepath.Join(env.home, "Programs", "Fake", "fake.exe")
	data := filepath.Join(env.home, "data")
	writeFile(t, exe, 1)
	mustMkdir(t, data)
	savedSnap, savedFinal := snapshotProcesses, getFinalPathName
	t.Cleanup(func() { snapshotProcesses, getFinalPathName = savedSnap, savedFinal })
	snapshotProcesses = func() ([]procInfo, error) {
		return []procInfo{{Exe: exe, Name: "fake.exe", CmdLine: "bad\x00command"}, {Name: "docker.exe", CmdLine: `docker.exe --pipe=\\.\pipe\docker_engine \\server\share`}}, nil
	}
	guard := probeUsage()
	if guard.failed || !guard.inUse(filepath.Dir(exe)) || guard.inUse(data) {
		t.Fatalf("per-process parse error killed guard: %+v", guard)
	}
	snapshotProcesses = func() ([]procInfo, error) {
		return []procInfo{{Exe: exe, Name: "fake.exe", CmdLine: `fake.exe --user-data-dir="` + data + `"`}}, nil
	}
	getFinalPathName = func(windows.Handle, *uint16, uint32, uint32) (uint32, error) { return 0, windows.Errno(1920) }
	guard = probeUsage()
	if guard.failed || !guard.inUse(filepath.Join(data, "Cache")) {
		t.Fatalf("unresolvable alias lost lexical usage root: %+v", guard)
	}
}

// TestWindowsUsageBroadRoots drops broad CLI/executable roots while preserving narrow bidirectional protection.
func TestWindowsUsageBroadRoots(t *testing.T) {
	env := testEnv(t)
	tmp := filepath.Join(env.home, "Temp")
	mustMkdir(t, tmp)
	t.Setenv("TEMP", tmp)
	t.Setenv("TMP", tmp)
	unrelated := filepath.Join(env.home, "cache", "unrelated")
	saved := snapshotProcesses
	t.Cleanup(func() { snapshotProcesses = saved })
	cases := []struct {
		name string
		proc procInfo
		free string
		busy string
	}{
		{"code-profile", procInfo{Name: "code.exe", CmdLine: `code.exe "` + env.home + `"`}, unrelated, ""},
		{"robocopy-drive", procInfo{Name: "robocopy.exe", CmdLine: `robocopy.exe ` + filepath.VolumeName(env.home) + `\`}, unrelated, ""},
		{"downloads-putty", procInfo{Name: "putty.exe", Exe: filepath.Join(env.home, "Downloads", "putty.exe")}, filepath.Join(env.home, "Downloads", "old.zip"), ""},
		{"temp-setup", procInfo{Name: "setup.exe", Exe: filepath.Join(tmp, "setup.exe")}, filepath.Join(tmp, "unrelated"), ""},
		{"nested-temp-setup", procInfo{Name: "setup.exe", Exe: filepath.Join(tmp, "abc", "setup.exe")}, filepath.Join(tmp, "unrelated"), filepath.Join(tmp, "abc")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.proc.Exe != "" {
				writeFile(t, tc.proc.Exe, 1)
			}
			snapshotProcesses = func() ([]procInfo, error) { return []procInfo{tc.proc}, nil }
			guard := probeUsage()
			if guard.failed || guard.inUse(tc.free) {
				t.Fatalf("broad root blocks unrelated cleanup: %+v", guard)
			}
			if tc.busy != "" && (!guard.inUse(filepath.Join(tc.busy, "Cache")) || !guard.inUse(filepath.Dir(tc.busy))) {
				t.Fatal("narrow root lost bidirectional matching")
			}
		})
	}
	for _, suffix := range protectedHomeDirs {
		root := env.home + suffix
		snapshotProcesses = func() ([]procInfo, error) {
			return []procInfo{{Name: "fake.exe", CmdLine: `fake.exe "` + root + `"`}}, nil
		}
		if guard := probeUsage(); guard.failed || guard.inUse(unrelated) {
			t.Errorf("protected root retained: %s", root)
		}
	}
}

// TestWindowsTempProtectedRoots never promotes Downloads or AppData Local contents into sys.temp.
func TestWindowsTempProtectedRoots(t *testing.T) {
	env := testEnv(t)
	for _, part := range []string{"Downloads", "AppData/Local"} {
		tmp := filepath.Join(env.home, filepath.FromSlash(part))
		writeFile(t, filepath.Join(tmp, "keep"), 100)
		t.Setenv("TEMP", tmp)
		t.Setenv("TMP", tmp)
		env.tmp = tmp
		if allowedTemp(env.home, tmp) || len(env.resolve("$TMPDIR/*")) != 0 {
			t.Errorf("protected temp expanded: %s", part)
		}
		specs, _ := expandItems(&env)
		for _, s := range specs {
			if s.item.ID == "sys.temp" {
				t.Errorf("sys.temp offered for %s", part)
			}
		}
	}
}

// TestWindowsProfileThroughJunction accepts a relocated profile but still rejects links below it.
func TestWindowsProfileThroughJunction(t *testing.T) {
	target := t.TempDir()
	alias := filepath.Join(t.TempDir(), "profile")
	makeJunction(t, alias, target)
	setHome(t, alias)
	path := filepath.Join(alias, "cache", "file")
	writeFile(t, path, 1)
	if !safeToClean(path) || !cleanable(path) || !platformRemovalSafe(path) {
		t.Fatal("relocated profile refused")
	}
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "keep"), 1)
	link := filepath.Join(alias, "cache", "escape")
	makeJunction(t, link, outside)
	if safeToClean(filepath.Join(link, "keep")) || platformRemovalSafe(filepath.Dir(link)) {
		t.Fatal("relocated profile crossed nested junction")
	}
}

// TestWindowsMissingRemovalReportsGone preserves the existing distinction between a missing and blocked item.
func TestWindowsMissingRemovalReportsGone(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, "missing")
	if !platformRemovalSafe(path) {
		t.Fatal("missing root blocked")
	}
	if _, err := (Cleaner{}).TrashPath(path); !errors.Is(err, errGone) {
		t.Fatalf("TrashPath=%v", err)
	}
	if res := (Cleaner{}).CleanPaths([]string{path}); res.Moved != 0 || len(res.Failed) != 0 || len(res.Removed) != 1 {
		t.Fatalf("CleanPaths=%+v", res)
	}
}

// TestWindowsSelfExclusionNeedsResolvedPaths does not equate two failed resolutions.
func TestWindowsSelfExclusionNeedsResolvedPaths(t *testing.T) {
	saved := getFinalPathName
	t.Cleanup(func() { getFinalPathName = saved })
	getFinalPathName = func(windows.Handle, *uint16, uint32, uint32) (uint32, error) { return 0, windows.ERROR_ACCESS_DENIED }
	if statusProcessExcluded(int32(os.Getpid()+1), os.Args[0]) {
		t.Fatal("failed resolutions matched self")
	}
}

// TestWindowsRemovalWalkCost stays constant in path probes as the number of ordinary entries increases.
func TestWindowsRemovalWalkCost(t *testing.T) {
	env := testEnv(t)
	dir := filepath.Join(env.home, "cache")
	writeFile(t, filepath.Join(dir, "first"), 1)
	environmentProtections()
	savedFinal, savedStat := getFinalPathName, windowsPathLstat
	t.Cleanup(func() { getFinalPathName, windowsPathLstat = savedFinal, savedStat })
	probes := 0
	getFinalPathName = func(h windows.Handle, p *uint16, n, f uint32) (uint32, error) {
		probes++
		return savedFinal(h, p, n, f)
	}
	windowsPathLstat = func(p string) (fs.FileInfo, error) { probes++; return savedStat(p) }
	if !platformRemovalSafe(dir) {
		t.Fatal("ordinary root blocked")
	}
	first := probes
	for i := 0; i < 64; i++ {
		writeFile(t, filepath.Join(dir, strings.Repeat("x", i+1)), 1)
	}
	probes = 0
	if !platformRemovalSafe(dir) || probes != first {
		t.Fatalf("per-entry path probes: first=%d expanded=%d", first, probes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if platformRemovalSafe(dir, ctx) {
		t.Fatal("cancelled removal walk allowed")
	}
	probes = 0
	measureChecked(dir)
	small := probes
	writeFile(t, filepath.Join(dir, "another"), 1)
	probes = 0
	measureChecked(dir)
	if probes != small {
		t.Fatal("measurement performs per-file path chains")
	}
}

// TestWindowsListingFlagsAreShallow leaves nested-boundary validation to all cleanup actions.
func TestWindowsListingFlagsAreShallow(t *testing.T) {
	env := testEnv(t)
	root := filepath.Join(env.home, "cache")
	outside := t.TempDir()
	writeFile(t, filepath.Join(root, "bucket", "ordinary"), 1)
	junction := filepath.Join(root, "bucket", "escape")
	makeJunction(t, junction, outside)
	if !removable(root) || !cleanable(root) {
		t.Fatal("listing flags performed a nested walk")
	}
	if platformRemovalSafe(root) {
		t.Fatal("action allowed nested boundary")
	}
	if _, err := (Cleaner{}).TrashPath(root); !errors.Is(err, errBlocked) {
		t.Fatalf("TrashPath=%v", err)
	}
	if res := (Cleaner{}).CleanPaths([]string{root}); res.Moved != 0 || len(res.Failed) == 0 {
		t.Fatalf("CleanPaths=%+v", res)
	}
	env.settings.Custom = []CustomFolder{{Name: "cache", Path: root, Enabled: true}}
	cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + root}}, time.Time{}, cleanHooks{})
	if _, err := os.Lstat(junction); err != nil {
		t.Fatalf("junk removed boundary: %v", err)
	}
	if !exists(filepath.Join(root, "bucket", "ordinary")) {
		t.Fatal("junk removed parent of boundary")
	}
}

// TestWindowsEnvironmentProtectionCache reuses resolutions and invalidates them after environment or profile changes.
func TestWindowsEnvironmentProtectionCache(t *testing.T) {
	env := testEnv(t)
	cloud := filepath.Join(env.home, "Cloud Sync")
	mustMkdir(t, cloud)
	t.Setenv("OneDrive", cloud)
	saved := protectionResolver
	t.Cleanup(func() { protectionResolver = saved })
	calls := 0
	protectionResolver = func(p string) string { calls++; return saved(p) }
	if !platformProtected(filepath.Join(cloud, "keep"), strings.ToLower(resolvePath(env.home))) {
		t.Fatal("cloud target unprotected")
	}
	first := calls
	for i := 0; i < 50; i++ {
		platformProtected(filepath.Join(cloud, "keep"), strings.ToLower(env.home))
	}
	if calls != first || first == 0 {
		t.Fatalf("cache calls first=%d now=%d", first, calls)
	}
	next := filepath.Join(env.home, "Other Cloud")
	t.Setenv("OneDrive", next)
	if !platformProtected(filepath.Join(next, "keep"), strings.ToLower(resolvePath(env.home))) || calls == first {
		t.Fatal("environment change not refreshed")
	}
	beforeHome := calls
	newHome := t.TempDir()
	setHome(t, newHome)
	if !platformProtected(filepath.Join(newHome, "AppData", "Roaming", "Cleanzo", "keep"), strings.ToLower(resolvePath(newHome))) || calls == beforeHome {
		t.Fatal("setHome did not refresh environment roots")
	}
}

// TestWindowsProductCacheIsLazy caches version reads by exe modification time and avoids command-only probes.
func TestWindowsProductCacheIsLazy(t *testing.T) {
	env := testEnv(t)
	exe := filepath.Join(env.home, "Programs", "Fake", "fake.exe")
	writeFile(t, exe, 1)
	savedRead, savedSnap := readProcessProduct, snapshotProcesses
	t.Cleanup(func() { readProcessProduct, snapshotProcesses = savedRead, savedSnap })
	reads := 0
	readProcessProduct = func(string) string { reads++; return "Fake Product" }
	// Exercise the real enumerator before injecting the controlled exe snapshot.
	snapshotProcesses = savedSnap
	productCache.Lock()
	savedCache := productCache.entries
	productCache.entries = map[string]productRecord{}
	productCache.Unlock()
	defer func() { productCache.Lock(); productCache.entries = savedCache; productCache.Unlock() }()
	if _, err := processSnapshot(); err != nil {
		t.Fatal(err)
	}
	if _, err := psCommands(); err != nil {
		t.Fatal(err)
	}
	toolBusy(nil)
	aiProcesses()
	aiRunning()
	snapshotProcesses = func() ([]procInfo, error) {
		return []procInfo{{Exe: exe, Name: "fake.exe", CmdLine: `fake.exe --serve`}}, nil
	}
	if reads != 0 {
		t.Fatal("command-only probes read version resources")
	}
	for i := 0; i < 2; i++ {
		if _, _, err := runningApps(); err != nil {
			t.Fatal(err)
		}
		probeUsage()
	}
	if reads != 1 {
		t.Fatalf("repeated version reads=%d", reads)
	}
	changed := time.Now().Add(time.Hour)
	if err := retryWindowsFileOperation(func() error { return os.Chtimes(exe, changed, changed) }); err != nil {
		t.Fatal(err)
	}
	runningApps()
	if reads != 2 {
		t.Fatalf("mtime did not invalidate product cache: %d", reads)
	}
}

// assertWindowsLinkBoundary verifies that scans and all actions preserve a link and its target.
func assertWindowsLinkBoundary(t *testing.T, env junkEnv, cache, link, target string) {
	t.Helper()
	if safeToClean(link) || removable(link) || !env.traversalBlocked(link) {
		t.Fatal("reparse entry allowed")
	}
	if size, files := scanPath(cache); size != 0 || files != 0 {
		t.Fatalf("link measured: %d/%d", size, files)
	}
	if size, files := scanPath(link); size != 0 || files != 0 {
		t.Fatal("link target measured")
	}
	env.settings.Custom = []CustomFolder{{Name: "cache", Path: cache, Enabled: true}}
	specs, sum := expandItems(&env)
	items, _ := measureJunk(context.Background(), specs, env, sum, nil, nil)
	for _, item := range items {
		if item.Size > 0 || item.Files > 0 {
			t.Fatalf("link offered: %+v", item)
		}
	}
	cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + cache}}, time.Time{}, cleanHooks{})
	if _, err := (Cleaner{}).TrashPath(link); !errors.Is(err, errBlocked) {
		t.Fatalf("TrashPath=%v", err)
	}
	if result := (Cleaner{}).CleanPaths([]string{cache, link}); result.Moved != 0 {
		t.Fatalf("link removed: %+v", result)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("link removed: %v", err)
	}
	if info, err := os.Stat(target); err != nil {
		t.Fatalf("target changed: %v", err)
	} else if info.IsDir() && target != `D:\` {
		if safeToClean(filepath.Join(link, "keep")) {
			t.Fatal("link descendant permitted")
		}
		if size, files := scanPath(filepath.Join(link, "keep")); size != 0 || files != 0 {
			t.Fatal("measured through directory link")
		}
	}
}

// TestWindowsJunctionToDDrive never enumerates, measures or removes through the runner's second drive.
func TestWindowsJunctionToDDrive(t *testing.T) {
	if _, err := os.Stat(`D:\`); os.IsNotExist(err) {
		t.Skip("D: drive is unavailable")
	} else if err != nil {
		t.Fatal(err)
	}
	env := testEnv(t)
	cache := filepath.Join(env.home, "cache")
	link := filepath.Join(cache, "drive")
	makeJunction(t, link, `D:\`)
	assertWindowsLinkBoundary(t, env, cache, link, `D:\`)
}

// TestWindowsDirectoryAndFileSymlinks checks both native symlink forms inside caches without relying on their mode alone.
func TestWindowsDirectoryAndFileSymlinks(t *testing.T) {
	for _, directory := range []bool{true, false} {
		name := "file"
		if directory {
			name = "directory"
		}
		t.Run(name, func(t *testing.T) {
			env := testEnv(t)
			outside := t.TempDir()
			target := filepath.Join(outside, "keep")
			writeFile(t, target, 100)
			if directory {
				target = outside
			}
			cache := filepath.Join(env.home, "cache")
			mustMkdir(t, cache)
			link := filepath.Join(cache, "link")
			if err := os.Symlink(target, link); errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
				t.Skip("symlink creation requires privilege or Developer Mode")
			} else if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := retryWindowsFileOperation(func() error { return os.Remove(link) }); err != nil && !os.IsNotExist(err) {
					t.Error(err)
				}
			})
			assertWindowsLinkBoundary(t, env, cache, link, target)
			if !exists(filepath.Join(outside, "keep")) {
				t.Fatal("symlink target content removed")
			}
		})
	}
}
