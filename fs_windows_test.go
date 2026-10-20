package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// windowsFileInfo supplies Windows attributes without altering a real fixture.
type windowsFileInfo struct {
	fs.FileInfo
	attributes syscall.Win32FileAttributeData
}

// Sys returns the attributes being exercised by a test.
func (info windowsFileInfo) Sys() any { return &info.attributes }

// TestWindowsFileMetadata blocks every placeholder flag and uses creation age.
func TestWindowsFileMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	writeFile(t, path, 123)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mod := info.ModTime()
	for _, tc := range []struct{ created, written, want time.Time }{
		{mod.Add(-time.Hour), mod, mod},
		{mod.Add(time.Hour), mod, mod.Add(time.Hour)},
		{mod, mod.Add(2 * time.Hour), mod.Add(2 * time.Hour)},
	} {
		data := windowsFileInfo{FileInfo: info, attributes: syscall.Win32FileAttributeData{
			CreationTime: syscall.NsecToFiletime(tc.created.UnixNano()), LastWriteTime: syscall.NsecToFiletime(tc.written.UnixNano()),
		}}
		if got := changedAt(data); !got.Equal(tc.want) {
			t.Errorf("changedAt=%v, want %v", got, tc.want)
		}
	}
	for _, flag := range []uint32{windows.FILE_ATTRIBUTE_REPARSE_POINT, windows.FILE_ATTRIBUTE_OFFLINE, windows.FILE_ATTRIBUTE_RECALL_ON_OPEN, windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS} {
		data := windowsFileInfo{FileInfo: info, attributes: syscall.Win32FileAttributeData{FileAttributes: flag}}
		if !removalRefused(path, data) {
			t.Errorf("attribute %#x was allowed", flag)
		}
	}
	if removalRefused(path, info) || onDisk(info) != 123 {
		t.Fatal("ordinary file metadata was not read")
	}
	if mountPoint(filepath.Dir(path)) {
		t.Fatal("ordinary sandbox directory was treated as another volume")
	}
}

// TestWindowsStubsCleanNothing keeps real entry points closed when the snapshot fails.
func TestWindowsStubsCleanNothing(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, "cleanup", "keep.bin")
	writeFile(t, path, 123)
	saved := snapshotProcesses
	snapshotProcesses = func() ([]procInfo, error) { return nil, errors.New("snapshot failed") }
	t.Cleanup(func() { snapshotProcesses = saved })
	if _, _, err := runningApps(); err == nil {
		t.Fatal("runningApps did not fail closed")
	}
	if _, err := psCommands(); err == nil || !toolBusy(nil) {
		t.Fatal("toolBusy did not fail closed")
	}
	if _, err := (Cleaner{}).TrashPath(path); !errors.Is(err, errUsageUnknown) {
		t.Fatalf("TrashPath error=%v", err)
	}
	result := (Cleaner{}).CleanPaths([]string{path})
	if result.Moved != 0 || len(result.Failed) == 0 || !exists(path) {
		t.Fatalf("CleanPaths did not preserve the file: %+v", result)
	}
	env.guard = probeUsage()
	env.settings.Custom = []CustomFolder{{Name: "cleanup", Path: filepath.Dir(path), Enabled: true}}
	results := cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + filepath.Dir(path)}}, time.Time{}, cleanHooks{})
	if len(results) != 1 || results[0].Freed != 0 || results[0].Trashed != 0 || !exists(path) {
		t.Fatalf("CleanJunk did not preserve the file: %+v", results)
	}
	source := realStatusSource()
	busy, total := source.cpu()
	if busy != 0 || total != 0 || source.memory() != (MemoryStatus{}) || source.disk() != (DiskStat{}) || source.battery(context.Background()) != nil || len(source.procs()) != 0 || len(source.apps()) != 0 || source.terminate(int32(os.Getpid())) {
		t.Fatal("unsupported status source returned a live reading or action")
	}
}

// TestWindowsHomeEnvironment checks the test sandbox and per-test directory overrides.
func TestWindowsHomeEnvironment(t *testing.T) {
	sandbox := homeDir()
	if !within(os.TempDir(), sandbox) {
		t.Fatal("test temp directory escaped the sandbox profile")
	}
	home := t.TempDir()
	setHome(t, home)
	if homeDir() != home || os.Getenv("USERPROFILE") != home || configPath() != filepath.Join(home, "AppData", "Roaming", "Cleanzo", "config.json") || skillUpdatesPath() != filepath.Join(home, "AppData", "Roaming", "Cleanzo", "skill-updates.json") {
		t.Fatal("per-test home or config directory escaped the fixture")
	}
	for _, key := range []string{"APPDATA", "LOCALAPPDATA"} {
		if info, err := os.Stat(os.Getenv(key)); err != nil || !info.IsDir() || !within(os.Getenv(key), home) {
			t.Fatalf("%s does not name a fixture directory", key)
		}
	}
	if expandHome(`~\cache\file`) != filepath.Join(home, "cache", "file") || !within(strings.ToUpper(filepath.Join(home, "cache")), strings.ToLower(home)) {
		t.Fatal("Windows home expansion or case-insensitive containment failed")
	}
}
