package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// makeJunction creates a directory junction without requiring symlink privileges.
func makeJunction(t *testing.T, path, target string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	out, err := exec.Command("cmd", "/c", "mklink", "/J", path, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			time.Sleep(100 * time.Millisecond)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove junction: %v", err)
			}
		}
	})
}

// TestWindowsJunctionBoundaries tests final targets, missing suffixes and scan/clean boundaries.
func TestWindowsJunctionBoundaries(t *testing.T) {
	env := testEnv(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "keep.bin")
	writeFile(t, target, 7654)
	cache := filepath.Join(env.home, "cache")
	junction := filepath.Join(cache, "bucket", "escape")
	makeJunction(t, junction, outside)
	if !mountPoint(junction) {
		t.Fatal("junction not recognized as a mount boundary")
	}
	for _, p := range []string{junction, filepath.Join(junction, "keep.bin"), filepath.Join(junction, "missing", "child")} {
		if safeToClean(p) || removable(p) || !env.traversalBlocked(p) {
			t.Errorf("allowed junction: %s", p)
		}
	}
	if got := resolvePath(filepath.Join(junction, "missing", "child")); !strings.EqualFold(got, filepath.Join(resolvePath(outside), "missing", "child")) {
		t.Fatalf("final target=%q", got)
	}
	if size, files := scanPath(filepath.Join(junction, "keep.bin")); size != 0 || files != 0 {
		t.Fatal("measured through junction ancestor")
	}
	if size, files := scanPath(cache); size != 0 || files != 0 {
		t.Fatalf("junction measured: %d/%d", size, files)
	}
	// A selected ordinary parent must not cause its nested junction to be removed either.
	env.settings.Custom = []CustomFolder{{Name: "cache", Path: cache, Enabled: true}}
	specs, _ := expandItems(&env)
	items, _ := measureJunk(context.Background(), specs, env, ScanSummary{}, nil, nil)
	for _, item := range items {
		if item.Size > 0 || item.Files > 0 {
			t.Errorf("junction offered: %+v", item)
		}
	}
	cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + cache}}, time.Time{}, cleanHooks{})
	if result := (Cleaner{}).CleanPaths([]string{cache}); result.Moved != 0 {
		t.Fatalf("junction parent removed: %+v", result)
	}
	if !exists(target) {
		t.Fatal("junction target removed")
	}
	if _, err := os.Lstat(junction); err != nil {
		t.Fatalf("junction removed: %v", err)
	}
	if _, err := (Cleaner{}).TrashPath(filepath.Join(junction, "keep.bin")); err == nil {
		t.Fatal("TrashPath crossed junction")
	}
}

// TestWindowsApplicationDataJunction blocks the legacy junction even though its target is in the profile.
func TestWindowsApplicationDataJunction(t *testing.T) {
	env := testEnv(t)
	target := filepath.Join(env.home, "AppData", "Roaming")
	writeFile(t, filepath.Join(target, "Fake", "Cache", "keep"), 100)
	junction := filepath.Join(env.home, "Application Data")
	makeJunction(t, junction, target)
	env.settings.Custom = []CustomFolder{{Name: "legacy", Path: junction, Enabled: true}}
	specs, _ := expandItems(&env)
	for _, s := range specs {
		if strings.HasPrefix(s.item.ID, "custom:") {
			t.Fatalf("legacy junction offered: %+v", s)
		}
	}
	if safeToClean(filepath.Join(junction, "Fake", "Cache")) || !env.traversalBlocked(junction) {
		t.Fatal("legacy junction allowed")
	}
	cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + junction}}, time.Time{}, cleanHooks{})
	if !exists(filepath.Join(target, "Fake", "Cache", "keep")) {
		t.Fatal("legacy target removed")
	}
}

// TestWindowsSpecialPaths refuses streams, UNC and device namespaces through every public gate.
func TestWindowsSpecialPaths(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, "cache", "file")
	writeFile(t, path, 123)
	for _, p := range []string{path + ":stream", `\\server\share\file`, `\\?\` + path, `\\.\` + path} {
		if safeToClean(p) || removable(p) || !env.traversalBlocked(p) {
			t.Errorf("allowed special path %q", p)
		}
	}
}

// TestWindowsOfflineFileSurvivesClean sets the real offline attribute without reading or hydrating the file.
func TestWindowsOfflineFileSurvivesClean(t *testing.T) {
	env := testEnv(t)
	dir := filepath.Join(env.home, "cache")
	path := filepath.Join(dir, "offline")
	writeFile(t, path, 123)
	p := windows.StringToUTF16Ptr(path)
	if err := windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_OFFLINE); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_NORMAL); err != nil {
			t.Error(err)
		}
	})
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !removalRefused(path, info) || platformRemovalSafe(dir) {
		t.Fatal("offline file or containing directory allowed")
	}
	env.settings.Custom = []CustomFolder{{Name: "cache", Path: dir, Enabled: true}}
	cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + dir}}, time.Time{}, cleanHooks{})
	if !exists(path) {
		t.Fatal("offline file removed")
	}
}

// TestWindowsRecycleMovesToBin exercises the SDK structure layout and real fixed-volume shell operation.
func TestWindowsRecycleMovesToBin(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, "page.htm")
	connected := filepath.Join(env.home, "page_files", "keep")
	writeFile(t, connected, 1)
	writeFile(t, path, 123)
	// Only an ordinary, tiny, local fixed-drive file reaches the real shell; never test a nuke case.
	if getDriveType(windows.StringToUTF16Ptr(filepath.VolumeName(path)+`\`)) != windows.DRIVE_FIXED {
		t.Fatal("sandbox must be on a fixed drive")
	}
	if unsafe.Sizeof(shellFileOperation{}) != 56 || unsafe.Offsetof(shellFileOperation{}.aborted) != 36 {
		t.Fatal("SHFILEOPSTRUCTW layout differs from shellapi.h (64-bit)")
	}
	before := queryTestRecycleBin(t, path)
	requireTestRecycleCapacity(t, path, before.size+123)
	done := make(chan error, 1)
	go func() {
		err := recycle(path)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			err = recycle(path)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("Recycle Bin operation exceeded 60 seconds")
	}
	if !exists(connected) {
		t.Fatal("connected page_files directory was recycled")
	}

	if exists(path) {
		t.Fatal("Recycle Bin move left source on disk")
	}
	after := queryTestRecycleBin(t, path)
	if after.items < before.items+1 || after.size < before.size+123 {
		t.Fatalf("source disappeared without a bin entry: before=%+v after=%+v", before, after)
	}
}

// TestWindowsRecycleRefusesNonFixed rejects all non-fixed drive kinds before any shell call.
func TestWindowsRecycleRefusesNonFixed(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, "keep")
	writeFile(t, path, 123)
	if !cleanable(path) || !platformRemovalSafe(path) {
		t.Fatal("fixture must reach the drive check")
	}
	driveCalls := 0
	savedDrive, savedOp := getDriveType, shellRecycle
	t.Cleanup(func() { getDriveType, shellRecycle = savedDrive, savedOp })
	shellRecycle = func(*shellFileOperation) int32 { t.Fatal("shell called for non-fixed drive"); return 0 }
	for _, kind := range []uint32{windows.DRIVE_UNKNOWN, windows.DRIVE_NO_ROOT_DIR, windows.DRIVE_REMOVABLE, windows.DRIVE_REMOTE, windows.DRIVE_CDROM, windows.DRIVE_RAMDISK} {
		getDriveType = func(*uint16) uint32 { driveCalls++; return kind }
		if err := recycle(path); err == nil || !exists(path) {
			t.Fatalf("drive %d allowed: %v", kind, err)
		}
		if driveCalls == 0 {
			t.Fatal("drive probe was not reached")
		}
		driveCalls = 0
	}
}

// TestWindowsRecycleFlagsAndFailures checks double NUL, nuke warning, errors and cancellation without dialogs.
func TestWindowsRecycleFlagsAndFailures(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, "keep")
	writeFile(t, path, 1)
	saved, savedOwner := shellRecycle, recycleOwner
	recycleOwner = func() uintptr { return 0x1234 }
	t.Cleanup(func() { shellRecycle, recycleOwner = saved, savedOwner })
	for _, tc := range []struct {
		code    int32
		aborted int32
	}{{5, 0}, {-1, 0}, {0, 1}, {0, 0}} {
		shellRecycle = func(op *shellFileOperation) int32 {
			if op.window != 0x1234 || op.operation != 3 || op.flags != 0x0454|0x4000|0x2000 {
				t.Fatalf("unsafe shell flags: %#x", op.flags)
			}
			units := unsafe.Slice(op.from, len(windows.StringToUTF16(path))+1)
			if units[len(units)-1] != 0 || units[len(units)-2] != 0 {
				t.Fatal("source not double NUL terminated")
			}
			op.aborted = tc.aborted
			return tc.code
		}
		if err := recycle(path); (err != nil) != (tc.code != 0 || tc.aborted != 0) {
			t.Fatalf("result=%v for %+v", err, tc)
		}
	}
}

// TestWindowsUserPickable excludes AppData while rule-only caches remain cleanable.
func TestWindowsUserPickable(t *testing.T) {
	env := testEnv(t)
	for _, p := range []string{filepath.Join(env.home, "AppData", "Local", "wsl", "x", "ext4.vhdx"), filepath.Join(env.home, "AppData", "Local", "Fake", "Cache", "old")} {
		writeFile(t, p, 1)
		if userPickable(p) || removable(p) {
			t.Errorf("user-pickable AppData: %s", p)
		}
	}
	existing := filepath.Join(env.home, "AppData", "Local", "Fake", "Cache", "old")
	if _, err := (Cleaner{}).TrashPath(existing); !errors.Is(err, errBlocked) {
		t.Fatalf("AppData TrashPath=%v", err)
	}
	if res := (Cleaner{}).CleanPaths([]string{existing}); res.Moved != 0 || len(res.Failed) == 0 || !exists(existing) {
		t.Fatalf("AppData CleanPaths=%+v", res)
	}
	path := filepath.Join(env.home, "AppData", "Local", "Fake", "Cache")
	writeFile(t, filepath.Join(path, "old"), 1)
	if !cleanable(path) || !env.removable(path, false) {
		t.Fatal("rule cache refused")
	}
}

// TestWindowsProtectedAreas enumerates each protected root and subtree, including environment targets.
func TestWindowsProtectedAreas(t *testing.T) {
	env := testEnv(t)
	roots := []string{"Desktop", "Documents", "Downloads", "Pictures", "Music", "Videos", "Favorites", "Contacts", "Links", "Searches", "Saved Games", "AppData", "AppData/Local", "AppData/Roaming", "AppData/LocalLow"}
	trees := []string{"OneDrive", "OneDrive - Work", "onedriveExtra", "NTUSER.DAT", "ntuser.ini", ".ssh", ".gnupg", "AppData/Roaming/Microsoft", "AppData/Local/Packages", "AppData/Local/Programs", "AppData/Local/Microsoft/WindowsApps", "AppData/Local/Microsoft/Credentials", "AppData/Local/Microsoft/Vault", "AppData/Local/wsl", "AppData/Local/Docker", "AppData/Roaming/Cleanzo", "AppData/Local/Cleanzo"}
	for _, entry := range roots {
		p := filepath.Join(env.home, filepath.FromSlash(entry))
		if safeToClean(p) {
			t.Errorf("root allowed: %s", entry)
		}
	}
	for _, entry := range trees {
		for _, suffix := range []string{"", "/child"} {
			p := filepath.Join(env.home, filepath.FromSlash(entry+suffix))
			if safeToClean(p) {
				t.Errorf("tree allowed: %s%s", entry, suffix)
			}
		}
	}
	for _, key := range []string{"OneDrive", "OneDriveConsumer", "OneDriveCommercial"} {
		p := filepath.Join(env.home, "redirected-"+key)
		t.Setenv(key, p)
		if safeToClean(filepath.Join(p, "cache")) {
			t.Errorf("%s target allowed", key)
		}
	}
	for _, key := range []string{"APPDATA", "LOCALAPPDATA"} {
		p := filepath.Join(env.home, "redirected-"+key)
		t.Setenv(key, p)
		if safeToClean(p) || safeToClean(filepath.Join(p, "Microsoft", "Credentials", "keep")) {
			t.Errorf("%s redirected protected area allowed", key)
		}
		if safeToClean(filepath.Join(p, "Cleanzo", "cache")) {
			t.Errorf("%s Cleanzo allowed", key)
		}
	}
	if !safeToClean(filepath.Join(env.home, "Documents", "ordinary.txt")) {
		t.Fatal("known-folder contents should remain eligible")
	}
}

// TestWindowsExternalTempAndOneDrive checks actual external folders and foreign drive strings without creating them.
func TestWindowsExternalTempAndOneDrive(t *testing.T) {
	env := testEnv(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "keep"), 100)
	t.Setenv("TEMP", outside)
	t.Setenv("TMP", outside)
	env.tmp = os.TempDir()
	if safeToClean(filepath.Join(outside, "keep")) || len(env.resolve("$TMPDIR/*")) != 0 {
		t.Fatal("external temp allowed")
	}
	specs, _ := expandItems(&env)
	for _, s := range specs {
		if s.item.ID == "sys.temp" {
			t.Fatal("external temp offered")
		}
	}
	t.Setenv("TEMP", `C:\CleanzoTempOutside`)
	t.Setenv("TMP", `C:\CleanzoTempOutside`)
	env.tmp = os.TempDir()
	if len(env.resolve("$TMPDIR/*")) != 0 {
		t.Fatal("foreign temp expanded")
	}
	cloud := filepath.Join(env.home, "Cloud Sync")
	writeFile(t, filepath.Join(cloud, "keep"), 1)
	t.Setenv("OneDrive", cloud)
	if safeToClean(filepath.Join(cloud, "keep")) || platformRemovalSafe(cloud) {
		t.Fatal("in-profile OneDrive env target allowed")
	}
	t.Setenv("OneDrive", `D:\X`)
	if safeToClean(`D:\X\keep`) {
		t.Fatal("external OneDrive allowed")
	}
	if !exists(filepath.Join(outside, "keep")) {
		t.Fatal("external file removed")
	}
}

// TestWindowsSmallPlatformSwitches covers FDA and self exclusion without invoking native UI.
func TestWindowsSmallPlatformSwitches(t *testing.T) {
	if !fullDiskAccess() || !errors.Is((Cleaner{}).OpenFullDiskAccessSettings(), errUnsupported) {
		t.Fatal("Windows FDA behavior")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []workspaceApp{{Pid: int32(os.Getpid()), Path: `C:\elsewhere\other.exe`, Regular: true}, {Pid: 123, Path: strings.ToUpper(exe), Regular: true}, {Pid: 124, Path: `C:\Windows\explorer.exe`, Regular: true}} {
		if !statusAppExcluded(a) || canQuit(a.Path, []workspaceApp{a}) || len(quitMatches(a.Path, []workspaceApp{a})) != 0 {
			t.Errorf("protected app matched: %+v", a)
		}
	}
}

// TestWindowsShortPathsResolveFinal checks 8.3 aliases both with and without the expansion memo.
func TestWindowsShortPathsResolveFinal(t *testing.T) {
	env := testEnv(t)
	dir := filepath.Join(env.home, "A long cache folder")
	writeFile(t, filepath.Join(dir, "data"), 1)
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(windows.StringToUTF16Ptr(dir), &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || n >= uint32(len(buf)) {
		t.Fatalf("short path: %d %v", n, err)
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, dir) {
		t.Skip("volume did not generate a distinct 8.3 name")
	}
	check := func() {
		if !strings.EqualFold(resolvePath(short), resolvePath(dir)) {
			t.Fatalf("short alias not resolved: %q vs %q", resolvePath(short), resolvePath(dir))
		}
		if !safeToClean(filepath.Join(short, "data")) {
			t.Fatal("ordinary short alias refused")
		}
	}
	check()
	stop := startExpandMemo()
	defer stop()
	check()
}

// TestWindowsNestedBoundarySurvivesUserClean refuses a parent that would remove a protected environment target.
func TestWindowsNestedBoundarySurvivesUserClean(t *testing.T) {
	env := testEnv(t)
	parent := filepath.Join(env.home, "cache")
	cloud := filepath.Join(parent, "cloud")
	writeFile(t, filepath.Join(cloud, "keep"), 100)
	t.Setenv("OneDrive", cloud)
	if !cleanable(parent) || !removable(parent) {
		t.Fatal("listing flag used a nested walk")
	}
	if platformRemovalSafe(parent) {
		t.Fatal("protected nested cloud parent allowed")
	}
	result := (Cleaner{}).CleanPaths([]string{parent})
	if result.Moved != 0 || !exists(filepath.Join(cloud, "keep")) {
		t.Fatalf("protected nested cloud removed: %+v", result)
	}
}

// retryWindowsFileOperation tolerates one brief Defender sharing/access-denied hold.
func retryWindowsFileOperation(op func() error) error {
	err := op()
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		time.Sleep(100 * time.Millisecond)
		return op()
	}
	return err
}

// TestWindowsTempCannotBeProfileRoot refuses a TEMP that would promote every home child into system junk.
func TestWindowsTempCannotBeProfileRoot(t *testing.T) {
	env := testEnv(t)
	env.tmp = env.home
	t.Setenv("TEMP", env.home)
	t.Setenv("TMP", env.home)
	if allowedTemp(env.home, env.tmp) || len(env.resolve("$TMPDIR/*")) != 0 {
		t.Fatal("profile root accepted as temp")
	}
}

// testRecycleBinInfo matches SHQUERYRBINFO from Windows SDK shellapi.h, used only for Task 8 verification.
type testRecycleBinInfo struct {
	cbSize      uint32
	size, items int64
}

// queryTestRecycleBin observes the source volume's bin without emptying it or showing dialogs.
func queryTestRecycleBin(t *testing.T, path string) testRecycleBinInfo {
	t.Helper()
	info := testRecycleBinInfo{}
	if unsafe.Sizeof(info) != 24 || unsafe.Offsetof(info.size) != 8 || unsafe.Offsetof(info.items) != 16 {
		t.Fatal("SHQUERYRBINFO layout differs from shellapi.h")
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	root := windows.StringToUTF16Ptr(filepath.VolumeName(path) + `\`)
	query := windows.NewLazySystemDLL("shell32.dll").NewProc("SHQueryRecycleBinW")
	result, _, _ := query.Call(uintptr(unsafe.Pointer(root)), uintptr(unsafe.Pointer(&info)))
	if int32(result) < 0 {
		t.Fatalf("SHQueryRecycleBinW HRESULT %#x", uint32(int32(result)))
	}
	return info
}

// TestWindowsAIToolsAndSkillsRefuseReparsePoints keeps every cleanup entry point behind the same boundary.
func TestWindowsAIToolsAndSkillsRefuseReparsePoints(t *testing.T) {
	env := testEnv(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "SKILL.md"), 100)
	skill := filepath.Join(env.home, ".agents", "skills", "escape")
	makeJunction(t, skill, outside)
	if err := skillEntryOK(skill); err == nil {
		t.Fatal("skill reparse entry accepted")
	}
	result := (Cleaner{}).TrashSkills([]string{skill})
	if result.Moved != 0 || !exists(filepath.Join(outside, "SKILL.md")) {
		t.Fatalf("skill boundary removed: %+v", result)
	}
	root := filepath.Join(env.home, ".codex")
	cache := filepath.Join(root, "cache")
	makeJunction(t, filepath.Join(cache, "escape"), outside)
	if aiRemovable(root, aiTool{}, cache) {
		t.Fatal("AI cache containing reparse point accepted")
	}
}

// requireTestRecycleCapacity reads global and source-volume BitBucket settings without changing them.
func requireTestRecycleCapacity(t *testing.T, path string, required int64) {
	t.Helper()
	const base = `Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket`
	root := filepath.VolumeName(path) + `\`
	buf := make([]uint16, 128)
	if err := windows.GetVolumeNameForVolumeMountPoint(windows.StringToUTF16Ptr(root), &buf[0], uint32(len(buf))); err != nil {
		t.Skipf("cannot establish Recycle Bin volume settings: %v", err)
	}
	guid := strings.TrimSuffix(strings.TrimPrefix(windows.UTF16ToString(buf), `\\?\Volume`), `\`)
	enabled, capacity := false, uint64(0)
	for _, name := range []string{base, base + `\Volume\` + guid} {
		key, err := registry.OpenKey(registry.CURRENT_USER, name, registry.QUERY_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Skipf("cannot read Recycle Bin settings: %v", err)
		}
		nuke, _, nukeErr := key.GetIntegerValue("NukeOnDelete")
		maximum, _, capacityErr := key.GetIntegerValue("MaxCapacity")
		key.Close()
		if nukeErr == nil {
			if nuke != 0 {
				t.Skip("Recycle Bin is disabled (NukeOnDelete)")
			}
			enabled = true
		} else if !errors.Is(nukeErr, registry.ErrNotExist) {
			t.Skipf("cannot read NukeOnDelete: %v", nukeErr)
		}
		if capacityErr == nil {
			if maximum == 0 || maximum > 1<<40 {
				t.Skip("Recycle Bin capacity is disabled or unknown")
			}
			bytes := maximum * 1024 * 1024 // Explorer's MaxCapacity setting is in MiB.
			if capacity == 0 || bytes < capacity {
				capacity = bytes
			}
		} else if !errors.Is(capacityErr, registry.ErrNotExist) {
			t.Skipf("cannot read MaxCapacity: %v", capacityErr)
		}
	}
	if !enabled || capacity == 0 {
		t.Skip("Recycle Bin enabled state/capacity is not explicit; refusing a possible nuke prompt")
	}
	if required < 0 || capacity < uint64(required)+64*1024 {
		t.Skip("Recycle Bin is too small for the test without evicting existing entries")
	}
}
