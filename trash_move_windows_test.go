package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// TestWindowsRecycleGuardsAndShell checks guard order and the public shell boundary.
func TestWindowsRecycleGuardsAndShell(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, "keep.bin")
	writeFile(t, path, 1)
	savedDrive, savedShell := getDriveType, shellTrashItem
	t.Cleanup(func() { getDriveType, shellTrashItem = savedDrive, savedShell })
	calls := 0
	shellTrashItem = func(got string) error {
		calls++
		if got != path {
			t.Fatalf("path=%q want %q", got, path)
		}
		return nil
	}
	getDriveType = func(root *uint16) uint32 {
		if got := windows.UTF16PtrToString(root); !strings.EqualFold(got, filepath.VolumeName(resolvePath(path))+`\`) {
			t.Fatalf("volume=%q", got)
		}
		return windows.DRIVE_FIXED
	}
	if err := recycle(path); err != nil || calls != 1 {
		t.Fatalf("success: calls=%d err=%v", calls, err)
	}
	for _, failure := range []error{errors.New("arbitrary failure"), windows.ERROR_ACCESS_DENIED} {
		calls = 0
		shellTrashItem = func(string) error { calls++; return failure }
		if err := recycle(path); err == nil || err.Error() != "not-recycled" || calls != 1 || !exists(path) {
			t.Fatalf("failure: calls=%d err=%v", calls, err)
		}
	}
	calls = 0
	getDriveType = func(*uint16) uint32 { t.Fatal("drive checked before cleanable"); return windows.DRIVE_FIXED }
	if err := recycle(env.home); !errors.Is(err, errBlocked) || calls != 0 {
		t.Fatalf("protected: %v calls=%d", err, calls)
	}
	// A protected descendant passes cleanable(parent), but fails platformRemovalSafe(parent).
	parent := filepath.Join(env.home, "cache")
	writeFile(t, filepath.Join(parent, "cloud", "keep"), 1)
	t.Setenv("OneDrive", filepath.Join(parent, "cloud"))
	if !cleanable(parent) {
		t.Fatal("fixture must reach platformRemovalSafe")
	}
	if err := recycle(parent); !errors.Is(err, errBlocked) || calls != 0 {
		t.Fatalf("nested boundary: %v calls=%d", err, calls)
	}
}

// TestWindowsRecycleConnectedFilesGuard keeps both halves without invoking the shell.
func TestWindowsRecycleConnectedFilesGuard(t *testing.T) {
	env := testEnv(t)
	page := filepath.Join(env.home, "page.htm")
	folder := filepath.Join(env.home, "page_files")
	css := filepath.Join(folder, "x.css")
	writeFile(t, page, 1)
	writeFile(t, css, 1)
	savedDrive, savedShell := getDriveType, shellTrashItem
	t.Cleanup(func() { getDriveType, shellTrashItem = savedDrive, savedShell })
	driveCalls := 0
	getDriveType = func(*uint16) uint32 { driveCalls++; return windows.DRIVE_FIXED }
	shellTrashItem = func(string) error { t.Fatal("shell called for connected files"); return nil }
	for _, path := range []string{page, folder} {
		if err := recycle(path); err == nil || err.Error() != "connected-files" {
			t.Fatalf("%s: %v", path, err)
		}
		if !exists(page) || !exists(css) {
			t.Fatal("connected files moved")
		}
	}
	if driveCalls != 2 {
		t.Fatalf("drive check order: calls=%d", driveCalls)
	}
}

// TestWindowsRecycleConnectedFiles verifies both refusals with the real drive and mygo shell installed.
func TestWindowsRecycleConnectedFiles(t *testing.T) {
	if !windowsShellTest(t) {
		return
	}
	env := testEnv(t)
	page := filepath.Join(env.home, "page.htm")
	folder := filepath.Join(env.home, "page_files")
	css := filepath.Join(folder, "x.css")
	writeFile(t, page, 1)
	writeFile(t, css, 1)
	for _, path := range []string{page, folder} {
		if err := recycle(path); err == nil || err.Error() != "connected-files" {
			t.Fatalf("%s: %v", path, err)
		}
		if !exists(page) || !exists(css) {
			t.Fatal("connected files moved")
		}
	}
}

// TestWindowsTrashFailuresReachCallers checks codes and unchanged counters at each cleanup entry point.
func TestWindowsTrashFailuresReachCallers(t *testing.T) {
	for _, code := range []string{"not-recycled", "connected-files"} {
		t.Run(code, func(t *testing.T) {
			env := testEnv(t)
			savedTrash, savedProbe := trashItem, pathUsageProbe
			t.Cleanup(func() { trashItem, pathUsageProbe = savedTrash, savedProbe })
			trashItem = func(string) error { return errors.New(code) }
			pathUsageProbe = func() *usageGuard { return env.guard }
			noOpenFiles(t)
			path := filepath.Join(env.home, "Downloads", "keep.bin")
			writeFile(t, path, 123)
			result := (Cleaner{}).CleanPaths([]string{path})
			if result.Moved != 0 || result.Freed != 0 || len(result.Failed) != 1 || !strings.HasSuffix(result.Failed[0], ": "+code) {
				t.Fatalf("CleanPaths: %+v", result)
			}
			if _, err := (Cleaner{}).TrashPath(path); err == nil || err.Error() != code {
				t.Fatalf("TrashPath: %v", err)
			}
			env.rules = []Rule{{ID: "fixture", Group: groupSystem, Targets: []Target{{Pattern: "Downloads", Mode: entriesOf}}, Trash: true}}
			results := cleanJunk(context.Background(), env, []JunkSelection{{ID: "fixture"}}, time.Time{}, cleanHooks{})
			if len(results) != 1 || results[0].Trashed != 0 || results[0].Files != 0 || len(results[0].Failed) != 1 || results[0].Failed[0] != code {
				t.Fatalf("junk: %+v", results)
			}
			skill := filepath.Join(env.home, ".agents", "skills", "fixture")
			writeFile(t, filepath.Join(skill, "SKILL.md"), 1)
			skills := (Cleaner{}).TrashSkills([]string{skill})
			if skills.Moved != 0 || skills.Freed != 0 || len(skills.Items) != 1 || skills.Items[0].Error != code {
				t.Fatalf("skills: %+v", skills)
			}
			// Use the declared Codex cache target without changing the AI tool table.
			ai := filepath.Join(env.home, ".codex", "cache", "fixture.bin")
			writeFile(t, ai, 1)
			part := cleanAIPart(AISelection{Tool: "codex", Part: "codex.cache"}, map[string]bool{}, env.guard)
			if part.Moved != 0 || part.Freed != 0 || len(part.Failed) == 0 || !strings.HasSuffix(part.Failed[0], ": "+code) {
				t.Fatalf("AI: %+v", part)
			}
			if !exists(path) || !exists(skill) || !exists(ai) {
				t.Fatal("failed move removed source")
			}
		})
	}
}

// TestWindowsRecycleRefusesPermanentDelete exercises mygo's progress sink with the real per-volume setting.
func TestWindowsRecycleRefusesPermanentDelete(t *testing.T) {
	if !windowsShellTest(t) {
		return
	}
	env := testEnv(t)
	path := filepath.Join(env.home, "nuke-refusal.bin")
	writeFile(t, path, 123)
	root := windows.StringToUTF16Ptr(filepath.VolumeName(resolvePath(path)) + `\`)
	buf := make([]uint16, 128)
	if err := windows.GetVolumeNameForVolumeMountPoint(root, &buf[0], uint32(len(buf))); err != nil {
		t.Fatal(err)
	}
	volume := windows.UTF16ToString(buf)
	if !strings.HasPrefix(volume, `\\?\Volume{`) || !strings.HasSuffix(volume, `}\`) {
		t.Fatalf("unexpected volume name %q", volume)
	}
	guid := strings.TrimSuffix(strings.TrimPrefix(volume, `\\?\Volume`), `\`)
	name := `Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\` + guid
	key, existed, err := registry.CreateKey(registry.CURRENT_USER, name, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer key.Close()
	previous, kind, readErr := key.GetIntegerValue("NukeOnDelete")
	if readErr != nil && !errors.Is(readErr, registry.ErrNotExist) {
		t.Fatal(readErr)
	}
	defer func() {
		var err error
		if !existed {
			err = registry.DeleteKey(registry.CURRENT_USER, name)
		} else if errors.Is(readErr, registry.ErrNotExist) {
			err = key.DeleteValue("NukeOnDelete")
		} else if kind == registry.QWORD {
			err = key.SetQWordValue("NukeOnDelete", previous)
		} else {
			err = key.SetDWordValue("NukeOnDelete", uint32(previous))
		}
		if err != nil {
			t.Errorf("restore NukeOnDelete: %v", err)
		}
	}()
	if err := key.SetDWordValue("NukeOnDelete", 1); err != nil {
		t.Fatal(err)
	}
	before := queryTestRecycleBin(t, path)
	err = recycle(path)
	if err == nil && !exists(path) {
		after := queryTestRecycleBin(t, path)
		if after.items < before.items+1 || after.size < before.size+123 {
			t.Fatalf("file disappeared without reaching bin: before=%+v after=%+v", before, after)
		}
		if strings.EqualFold(os.Getenv("GITHUB_ACTIONS"), "true") {
			t.Fatal("Windows ignored per-volume NukeOnDelete=1: file reached Recycle Bin; permanent-delete refusal was not exercised")
		}
		t.Skip("Windows ignored per-volume NukeOnDelete=1; file reached Recycle Bin")
	}
	if err == nil || err.Error() != "not-recycled" || !exists(path) {
		t.Fatalf("permanent delete must be refused: err=%v source exists=%v", err, exists(path))
	}
}
