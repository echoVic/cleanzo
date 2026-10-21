package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// TestWindowsRecycleOperationLayout checks SHFILEOPSTRUCTW alignment on both supported architectures.
func TestWindowsRecycleOperationLayout(t *testing.T) {
	var op shellFileOperation
	if unsafe.Sizeof(op) != 56 || unsafe.Offsetof(op.window) != 0 || unsafe.Offsetof(op.operation) != 8 ||
		unsafe.Offsetof(op.from) != 16 || unsafe.Offsetof(op.to) != 24 || unsafe.Offsetof(op.flags) != 32 ||
		unsafe.Offsetof(op.aborted) != 36 || unsafe.Offsetof(op.mappings) != 40 || unsafe.Offsetof(op.title) != 48 {
		t.Fatalf("SHFILEOPSTRUCTW layout: %+v", op)
	}
}

// TestWindowsRecycleGuardsAndShell checks guard order and the public shell boundary.
func TestWindowsRecycleGuardsAndShell(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, "keep.bin")
	writeFile(t, path, 1)
	savedDrive, savedShell, savedSettings, savedOwner := getDriveType, shellRecycle, recycleSettings, recycleOwner
	t.Cleanup(func() {
		getDriveType, shellRecycle, recycleSettings, recycleOwner = savedDrive, savedShell, savedSettings, savedOwner
	})
	recycleSettings = func(*uint16) (uint32, uint32, bool) { return 0, 0, false }
	recycleOwner = func() uintptr { return 0x1234 }
	calls := 0
	shellRecycle = func(op *shellFileOperation) int32 {
		calls++
		if got := windows.UTF16PtrToString(op.from); got != path {
			t.Fatalf("path=%q want %q", got, path)
		}
		from := unsafe.Slice(op.from, len(windows.StringToUTF16(path))+1)
		if from[len(from)-2] != 0 || from[len(from)-1] != 0 {
			t.Fatal("shell path must be double NUL terminated")
		}
		if op.window != 0x1234 || op.operation != 3 || op.flags != 0x6454 || op.to != nil || op.aborted != 0 {
			t.Fatalf("unsafe shell operation: %+v", op)
		}
		return 0
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
	for _, failure := range []struct{ result, aborted int32 }{{1, 0}, {-1, 0}, {5, 0}, {0, 1}, {5, 1}} {
		calls = 0
		shellRecycle = func(op *shellFileOperation) int32 { calls++; op.aborted = failure.aborted; return failure.result }
		if err := recycle(path); !errors.Is(err, errNotRecycled) || err.Error() != "not-recycled" || calls != 1 || !exists(path) {
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

// TestWindowsRecycleMeasurement keeps slow walks on the shell's warning path, but refuses unreadable items.
func TestWindowsRecycleMeasurement(t *testing.T) {
	for _, tc := range []struct {
		name       string
		size       int64
		failures   int
		measureErr error
		cancel     bool
		wantShell  int
	}{
		{"timeout", 0, 0, context.DeadlineExceeded, false, 1},
		{"timeout with partial bytes", 4 << 20, 0, context.DeadlineExceeded, false, 1},
		{"read failures", 123, 1, nil, false, 0},
		{"timeout with read failures", 123, 1, context.DeadlineExceeded, false, 0},
		{"fit", 123, 0, nil, false, 1},
		{"no fit", 1 << 20, 0, nil, false, 0},
		{"measurement error", 123, 0, windows.ERROR_ACCESS_DENIED, false, 0},
		{"cancelled", 123, 0, context.Canceled, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t)
			path := filepath.Join(env.home, "measurement")
			writeFile(t, filepath.Join(path, "keep.bin"), 123)
			savedSettings, savedMeasure, savedShell := recycleSettings, recycleMeasure, shellRecycle
			defer func() { recycleSettings, recycleMeasure, shellRecycle = savedSettings, savedMeasure, savedShell }()
			recycleSettings = func(*uint16) (uint32, uint32, bool) { return 0, 1, true }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			measureCalls, shellCalls := 0, 0
			recycleMeasure = func(measureCtx context.Context, got string) (int64, int, error) {
				measureCalls++
				if got != path {
					t.Fatalf("measurement path=%q want %q", got, path)
				}
				deadline, ok := measureCtx.Deadline()
				if remaining := time.Until(deadline); !ok || remaining > dirSizeTimeout || remaining < dirSizeTimeout-time.Second {
					t.Errorf("measurement budget=%v want %v", remaining, dirSizeTimeout)
				}
				if tc.cancel {
					cancel()
					if !errors.Is(measureCtx.Err(), context.Canceled) {
						t.Fatal("measurement lost caller cancellation")
					}
				}
				return tc.size, tc.failures, tc.measureErr
			}
			shellRecycle = func(op *shellFileOperation) int32 {
				shellCalls++
				if op.flags != 0x6454 {
					t.Fatalf("shell safety flags=%#x", op.flags)
				}
				return 0
			}
			err := recycleWithContext(ctx, path)
			if measureCalls != 1 || shellCalls != tc.wantShell {
				t.Fatalf("measure calls=%d shell calls=%d want shell=%d err=%v", measureCalls, shellCalls, tc.wantShell, err)
			}
			if (tc.wantShell == 1 && err != nil) || (tc.wantShell == 0 && !errors.Is(err, errNotRecycled)) {
				t.Fatalf("unexpected recycle result: %v", err)
			}
			if !exists(filepath.Join(path, "keep.bin")) {
				t.Fatal("measurement changed source")
			}
		})
	}
}

// TestWindowsRecycleCallerContext preserves a caller's earlier deadline and stops cancelled work before the shell.
func TestWindowsRecycleCallerContext(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, "context.bin")
	writeFile(t, path, 123)
	savedSettings, savedMeasure, savedShell := recycleSettings, recycleMeasure, shellRecycle
	defer func() { recycleSettings, recycleMeasure, shellRecycle = savedSettings, savedMeasure, savedShell }()
	recycleSettings = func(*uint16) (uint32, uint32, bool) { return 0, 1, true }
	measureCalls := 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	wantDeadline, _ := ctx.Deadline()
	recycleMeasure = func(measureCtx context.Context, _ string) (int64, int, error) {
		measureCalls++
		if got, ok := measureCtx.Deadline(); !ok || !got.Equal(wantDeadline) {
			t.Fatalf("caller deadline lost: %v", got)
		}
		cancel()
		return 123, 0, nil // Even a measurement that misses cancellation must not start the shell.
	}
	shellRecycle = func(*shellFileOperation) int32 { t.Fatal("shell called after cancellation"); return 0 }
	if err := recycleWithContext(ctx, path); !errors.Is(err, errNotRecycled) || measureCalls != 1 || !exists(path) {
		t.Fatalf("cancel during measurement: err=%v measurements=%d", err, measureCalls)
	}
	if err := recycleWithContext(ctx, path); !errors.Is(err, errNotRecycled) || measureCalls != 1 || !exists(path) {
		t.Fatalf("pre-cancelled call: err=%v measurements=%d", err, measureCalls)
	}
}

// TestWindowsJunkRecycleCancellation verifies the production cleanup context reaches capacity measurement.
func TestWindowsJunkRecycleCancellation(t *testing.T) {
	env := testEnv(t)
	noOpenFiles(t)
	path := filepath.Join(env.home, "Downloads", "keep.bin")
	writeFile(t, path, 123)
	env.rules = []Rule{{ID: "fixture", Group: groupSystem, Targets: []Target{{Pattern: "Downloads", Mode: entriesOf}}, Trash: true}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	savedTrash, savedSettings, savedMeasure, savedShell := trashWithContext, recycleSettings, recycleMeasure, shellRecycle
	defer func() {
		trashWithContext, recycleSettings, recycleMeasure, shellRecycle = savedTrash, savedSettings, savedMeasure, savedShell
	}()
	trashWithContext = recycleWithContext
	recycleSettings = func(*uint16) (uint32, uint32, bool) { return 0, 1, true }
	measureCalls := 0
	wantInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	recycleMeasure = func(measureCtx context.Context, target string) (int64, int, error) {
		measureCalls++
		gotInfo, err := os.Stat(target)
		if err != nil || !os.SameFile(wantInfo, gotInfo) {
			t.Fatalf("measurement path=%q", target)
		}
		cancel()
		if !errors.Is(measureCtx.Err(), context.Canceled) {
			t.Fatal("cleanup cancellation lost before capacity measurement")
		}
		return 123, 0, measureCtx.Err()
	}
	shellRecycle = func(*shellFileOperation) int32 { t.Fatal("shell called after cleanup cancellation"); return 0 }
	results := cleanJunk(ctx, env, []JunkSelection{{ID: "fixture"}}, time.Time{}, cleanHooks{})
	if measureCalls != 1 || len(results) != 1 || results[0].Trashed != 0 || results[0].Files != 0 || !exists(path) {
		t.Fatalf("cancelled cleanup: measurements=%d results=%+v exists=%v", measureCalls, results, exists(path))
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
	savedDrive, savedShell := getDriveType, shellRecycle
	t.Cleanup(func() { getDriveType, shellRecycle = savedDrive, savedShell })
	driveCalls := 0
	getDriveType = func(*uint16) uint32 { driveCalls++; return windows.DRIVE_FIXED }
	shellRecycle = func(*shellFileOperation) int32 { t.Fatal("shell called for connected files"); return 0 }
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

// TestWindowsRecycleConnectedFiles verifies both refusals with the real drive and SHFileOperationW installed.
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

// testRecycleSettings changes only sandbox-volume values and restores their exact integer types.
func testRecycleSettings(t *testing.T, path string, values map[string]*uint32) func() {
	t.Helper()
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
	type original struct {
		value   uint64
		kind    uint32
		missing bool
	}
	previous := map[string]original{}
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		for value, old := range previous {
			var err error
			switch {
			case old.missing:
				err = key.DeleteValue(value)
				if errors.Is(err, registry.ErrNotExist) {
					err = nil
				}
			case old.kind == registry.QWORD:
				err = key.SetQWordValue(value, old.value)
			default:
				err = key.SetDWordValue(value, uint32(old.value))
			}
			if err != nil {
				t.Errorf("restore %s: %v", value, err)
			}
		}
		if err := key.Close(); err != nil {
			t.Error(err)
		}
		if !existed {
			if err := registry.DeleteKey(registry.CURRENT_USER, name); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(restore) // Also restore if setup fails before the caller can defer it.
	for value := range values {
		v, kind, err := key.GetIntegerValue(value)
		if err != nil && !errors.Is(err, registry.ErrNotExist) {
			t.Fatal(err)
		}
		previous[value] = original{v, kind, errors.Is(err, registry.ErrNotExist)}
	}
	for value, setting := range values {
		var err error
		if setting == nil {
			err = key.DeleteValue(value)
			if errors.Is(err, registry.ErrNotExist) {
				err = nil
			}
		} else {
			err = key.SetDWordValue(value, *setting)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return restore
}

// TestWindowsRecycleRefusesPermanentDelete catches missing NukeOnDelete pre-flight, before the shell.
func TestWindowsRecycleRefusesPermanentDelete(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, "nuke-refusal.bin")
	writeFile(t, path, 4<<20)
	nuke := uint32(1)
	defer testRecycleSettings(t, path, map[string]*uint32{"NukeOnDelete": &nuke, "MaxCapacity": nil})()
	assertRecyclePreflightRefuses(t, path, 4<<20)
}

// TestWindowsRecycleRefusesOverCapacity catches W04 without risking a real permanent deletion.
func TestWindowsRecycleRefusesOverCapacity(t *testing.T) {
	for _, folder := range []bool{false, true} {
		t.Run(fmt.Sprintf("folder=%v", folder), func(t *testing.T) {
			env := testEnv(t)
			path := filepath.Join(env.home, "capacity-refusal")
			if folder {
				writeFile(t, filepath.Join(path, "a.bin"), 2<<20)
				writeFile(t, filepath.Join(path, "nested", "b.bin"), 2<<20)
			} else {
				writeFile(t, path, 4<<20)
			}
			nuke, capacity := uint32(0), uint32(1)
			defer testRecycleSettings(t, path, map[string]*uint32{"NukeOnDelete": &nuke, "MaxCapacity": &capacity})()
			assertRecyclePreflightRefuses(t, path, 4<<20)
		})
	}
}

// TestWindowsRecycleCapacityBoundaries checks the inclusive limit and summed folder bytes using real settings.
func TestWindowsRecycleCapacityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		capacity uint32
		partSize int
		folder   bool
	}{
		{"at limit", 1, 1 << 20, false},
		{"zero capacity", 0, 1, false},
		{"folder total", 1, 768 << 10, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t)
			path := filepath.Join(env.home, "boundary")
			size := int64(tc.partSize)
			if tc.folder {
				writeFile(t, filepath.Join(path, "a.bin"), tc.partSize)
				writeFile(t, filepath.Join(path, "nested", "b.bin"), tc.partSize)
				size *= 2
			} else {
				writeFile(t, path, tc.partSize)
			}
			nuke := uint32(0)
			defer testRecycleSettings(t, path, map[string]*uint32{"NukeOnDelete": &nuke, "MaxCapacity": &tc.capacity})()
			assertRecyclePreflightRefuses(t, path, size)
		})
	}
}

// TestWindowsRecycleUnknownSettingsReachesShell keeps WANTNUKEWARNING as the fallback when settings are absent.
func TestWindowsRecycleUnknownSettingsReachesShell(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, "unknown.bin")
	writeFile(t, path, 123)
	defer testRecycleSettings(t, path, map[string]*uint32{"NukeOnDelete": nil, "MaxCapacity": nil})()
	savedShell := shellRecycle
	defer func() { shellRecycle = savedShell }()
	calls := 0
	shellRecycle = func(op *shellFileOperation) int32 {
		calls++
		if op.flags&0x4000 == 0 {
			t.Fatal("missing WANTNUKEWARNING fallback")
		}
		op.aborted = 1
		return 0
	}
	if err := recycle(path); !errors.Is(err, errNotRecycled) || calls != 1 || !exists(path) {
		t.Fatalf("unknown settings: err=%v calls=%d exists=%v", err, calls, exists(path))
	}
}

// assertRecyclePreflightRefuses proves the registry is honoured even when Windows would ignore it.
func assertRecyclePreflightRefuses(t *testing.T, path string, size int64) {
	t.Helper()
	savedShell := shellRecycle
	defer func() { shellRecycle = savedShell }()
	calls := 0
	shellRecycle = func(*shellFileOperation) int32 { calls++; return 0 }
	err := recycle(path)
	if !errors.Is(err, errNotRecycled) || calls != 0 || !exists(path) {
		t.Fatalf("pre-flight must retain source without shell: err=%v calls=%d exists=%v", err, calls, exists(path))
	}
	got, _, _, failures := measureChecked(path)
	if got != size || failures != 0 {
		t.Fatalf("source changed: size=%d failures=%d", got, failures)
	}
}
