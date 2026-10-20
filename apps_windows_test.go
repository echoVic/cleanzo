package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// testUninstallRegistry isolates all writes under a unique HKCU CleanzoTest base.
func testUninstallRegistry(t *testing.T) []uninstallRegistryRoot {
	t.Helper()
	base := fmt.Sprintf(`Software\CleanzoTest\%d-%d`, os.Getpid(), time.Now().UnixNano())
	saved := uninstallRegistryRoots
	roots := []uninstallRegistryRoot{}
	for _, id := range []string{"hkcu", "hklm", "hklm32"} {
		root := uninstallRegistryRoot{id: id, hive: registry.CURRENT_USER, path: base + `\` + id, view: registry.WOW64_64KEY}
		key, _, err := registry.CreateKey(root.hive, root.path, registry.ALL_ACCESS|root.view)
		if err != nil {
			t.Fatal(err)
		}
		key.Close()
		roots = append(roots, root)
	}
	uninstallRegistryRoots = roots
	t.Cleanup(func() {
		uninstallRegistryRoots = saved
		for _, root := range roots {
			key, err := registry.OpenKey(root.hive, root.path, registry.ENUMERATE_SUB_KEYS|root.view)
			if err != nil {
				t.Error(err)
				continue
			}
			names, err := key.ReadSubKeyNames(-1)
			key.Close()
			if err != nil {
				t.Error(err)
			}
			for _, name := range names {
				if err := registry.DeleteKey(root.hive, root.path+`\`+name); err != nil {
					t.Error(err)
				}
			}
			if err := registry.DeleteKey(root.hive, root.path); err != nil {
				t.Error(err)
			}
		}
		if err := registry.DeleteKey(registry.CURRENT_USER, base); err != nil {
			t.Error(err)
		}
	})
	return roots
}

// seedUninstallKey writes fixtures only into the injected test base.
func seedUninstallKey(t *testing.T, root uninstallRegistryRoot, name string, values map[string]string) registry.Key {
	t.Helper()
	key, _, err := registry.CreateKey(root.hive, root.path+`\`+name, registry.ALL_ACCESS|root.view)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { key.Close() })
	for name, value := range values {
		if err := key.SetStringValue(name, value); err != nil {
			t.Fatal(err)
		}
	}
	return key
}

// TestWindowsListInstalledApps reads the runner's real installed-program registry.
func TestWindowsListInstalledApps(t *testing.T) {
	apps := (Cleaner{}).ListApps()
	if len(apps) == 0 {
		t.Fatal("no installed registry apps found")
	}
	for _, app := range apps {
		if app.ID == "" || app.Name == "" || app.LastOpened != 0 {
			t.Fatalf("bad app: %+v", app)
		}
	}
}

// TestWindowsInstalledAppRegistryMapping verifies filters, views, identity and display data.
func TestWindowsInstalledAppRegistryMapping(t *testing.T) {
	roots := testUninstallRegistry(t)
	values := map[string]string{"DisplayName": "Acme", "DisplayVersion": "2", "Publisher": "Acme Inc", "InstallLocation": t.TempDir(), "UninstallString": `C:\Missing\uninstall.exe`}
	for _, root := range roots {
		key := seedUninstallKey(t, root, "acme", values)
		if err := key.SetDWordValue("EstimatedSize", 123); err != nil {
			t.Fatal(err)
		}
	}
	seedUninstallKey(t, roots[0], "empty-location", map[string]string{"DisplayName": "Zulu", "UninstallString": "msiexec.exe /X{12345678-1234-1234-1234-123456789ABC}"})
	key := seedUninstallKey(t, roots[0], "system", map[string]string{"DisplayName": "System", "UninstallString": "x"})
	if err := key.SetDWordValue("SystemComponent", 1); err != nil {
		t.Fatal(err)
	}
	seedUninstallKey(t, roots[0], "child", map[string]string{"DisplayName": "Child", "UninstallString": "x", "ParentKeyName": ""})
	seedUninstallKey(t, roots[0], "no-command", map[string]string{"DisplayName": "No command"})
	seedUninstallKey(t, roots[0], "no-name", map[string]string{"UninstallString": "x"})
	apps := (Cleaner{}).ListApps()
	if len(apps) != 2 || apps[0].ID != "hkcu:acme" || apps[0].Publisher != "Acme Inc" || apps[0].Size != 123*1024 || apps[0].Version != "2" || apps[1].Path != "" {
		t.Fatalf("apps=%+v", apps)
	}
	detail := (Cleaner{}).AppUninstallDetail(apps[0].ID)
	if detail.App.ID != apps[0].ID || detail.Items == nil || len(detail.Items) != 0 || detail.TotalSize != apps[0].Size {
		t.Fatalf("detail=%+v", detail)
	}
	for _, root := range roots {
		if entry, err := registeredAppByID(root.id + ":acme"); err != nil || entry.app.ID != root.id+":acme" {
			t.Fatalf("view %s: %+v %v", root.id, entry, err)
		}
	}
}

// fakeUninstaller copies this test binary so the native launch needs no script shell.
func fakeUninstaller(t *testing.T, dir, marker, mode string) (string, string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "fake uninstaller.exe")
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatal(err)
	}
	args := "-test.run=^TestWindowsUninstallerHelper$ -- " + windows.EscapeArg(marker) + " " + mode
	return exe, args
}

// TestWindowsUninstallerHelper writes a marker only in the explicitly requested child.
func TestWindowsUninstallerHelper(t *testing.T) {
	args := os.Args
	if len(args) < 4 || args[len(args)-3] != "--" {
		return
	}
	if err := os.WriteFile(args[len(args)-2], []byte("launched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if args[len(args)-1] == "wait" {
		time.Sleep(30 * time.Second)
	}
}

// waitUninstallMarker waits for the native-launched helper rather than assuming launch means exit.
func waitUninstallMarker(t *testing.T, marker string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(marker); err == nil && string(data) == "launched" {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("uninstaller did not write %s", marker)
}

// TestWindowsUninstallLaunchNoTrash records both deletion hooks and preserves InstallLocation.
func TestWindowsUninstallLaunchNoTrash(t *testing.T) {
	roots := testUninstallRegistry(t)
	location, helperDir := t.TempDir(), t.TempDir()
	keep := filepath.Join(location, "keep.txt")
	writeFile(t, keep, 12)
	marker := filepath.Join(helperDir, "marker")
	exe, args := fakeUninstaller(t, helperDir, marker, "write")
	decoyMarker := filepath.Join(helperDir, "unpicked-marker")
	seedUninstallKey(t, roots[0], "not-picked", map[string]string{
		"DisplayName": "Not picked", "InstallLocation": location,
		"UninstallString": windows.EscapeArg(exe) + " -test.run=^TestWindowsUninstallerHelper$ -- " + windows.EscapeArg(decoyMarker) + " write",
	})
	key := seedUninstallKey(t, roots[0], "picked", map[string]string{"DisplayName": "Picked", "InstallLocation": location, "UninstallString": "garbage", "QuietUninstallString": `cmd.exe /c bad`})
	// The command is reread, rather than cached from the preceding list.
	_ = (Cleaner{}).ListApps()
	if err := key.SetStringValue("UninstallString", windows.EscapeArg(exe)+" "+args); err != nil {
		t.Fatal(err)
	}
	savedTrash, savedRemove := trashItem, removeAll
	calls := []string{}
	trashItem = func(p string) error { calls = append(calls, "trash:"+p); return nil }
	removeAll = func(p string) error { calls = append(calls, "remove:"+p); return nil }
	t.Cleanup(func() { trashItem, removeAll = savedTrash, savedRemove })
	result := (Cleaner{}).UninstallApp("hkcu:picked")
	if !result.Launched || result.Moved != 0 || result.Freed != 0 || len(result.Failed) != 0 {
		t.Fatalf("result=%+v", result)
	}
	waitUninstallMarker(t, marker)
	waitUninstallExit(t, exe)
	if _, err := os.Stat(decoyMarker); !os.IsNotExist(err) {
		t.Fatalf("unpicked key launched: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("deletion calls=%v", calls)
	}
	if info, err := os.Stat(keep); err != nil || info.Size() != 12 {
		t.Fatalf("InstallLocation changed: %v", err)
	}
}

// TestWindowsUninstallInUse refuses a real live executable inside InstallLocation.
func TestWindowsUninstallInUse(t *testing.T) {
	roots := testUninstallRegistry(t)
	location := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ready")
	exe, args := fakeUninstaller(t, location, marker, "wait")
	cmd := quietCommand(exec.Command(exe, "-test.run=^TestWindowsUninstallerHelper$", "--", marker, "wait"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	waitUninstallMarker(t, marker)
	seedUninstallKey(t, roots[0], "busy", map[string]string{"DisplayName": "Busy", "InstallLocation": location, "UninstallString": windows.EscapeArg(exe) + " " + args})
	if !(Cleaner{}).AppUninstallDetail("hkcu:busy").App.Running {
		t.Fatal("running app not detected")
	}
	result := (Cleaner{}).UninstallApp("hkcu:busy")
	if result.Launched || !reflect.DeepEqual(result.Failed, []string{"hkcu:busy: in-use"}) {
		t.Fatalf("result=%+v", result)
	}
}

// TestWindowsUninstallFailsClosed exercises removed keys, missing commands and failed probes.
func TestWindowsUninstallFailsClosed(t *testing.T) {
	roots := testUninstallRegistry(t)
	key := seedUninstallKey(t, roots[0], "bad", map[string]string{"DisplayName": "Bad", "UninstallString": "garbage"})
	for id, code := range map[string]string{"hkcu:missing": "gone", "hkcu:bad": "blocked", `hkcu:..\bad`: "blocked", `hklm64:bad`: "blocked", `C:\Apps`: "blocked"} {
		result := (Cleaner{}).UninstallApp(id)
		if result.Launched || !reflect.DeepEqual(result.Failed, []string{id + ": " + code}) {
			t.Fatalf("%s: %+v", id, result)
		}
	}
	if err := key.DeleteValue("UninstallString"); err != nil {
		t.Fatal(err)
	}
	if result := (Cleaner{}).UninstallApp("hkcu:bad"); result.Launched || !strings.HasSuffix(strings.Join(result.Failed, ""), ": blocked") {
		t.Fatalf("missing command: %+v", result)
	}
	if err := key.SetStringValue("UninstallString", "garbage"); err != nil {
		t.Fatal(err)
	}
	saved := snapshotProcesses
	snapshotProcesses = func() ([]procInfo, error) { return nil, errors.New("snapshot failed") }
	t.Cleanup(func() { snapshotProcesses = saved })
	if detail := (Cleaner{}).AppUninstallDetail("hkcu:bad"); detail.App.Running {
		t.Fatal("failed snapshot is not evidence of a running app")
	}
	if result := (Cleaner{}).UninstallApp("hkcu:bad"); result.Launched || !reflect.DeepEqual(result.Failed, []string{"hkcu:bad: usage-unknown"}) {
		t.Fatalf("failed probe: %+v", result)
	}
}

// TestWindowsUninstallNativeLayout checks every pointer-sensitive SHELLEXECUTEINFOW field.
func TestWindowsUninstallNativeLayout(t *testing.T) {
	info := shellExecuteInfo{}
	if unsafe.Sizeof(info) != 112 || unsafe.Offsetof(info.verb) != 16 || unsafe.Offsetof(info.parameters) != 32 || unsafe.Offsetof(info.show) != 48 || unsafe.Offsetof(info.instance) != 56 || unsafe.Offsetof(info.hotKey) != 88 || unsafe.Offsetof(info.process) != 104 {
		t.Fatalf("bad ShellExecuteExW layout: size=%d", unsafe.Sizeof(info))
	}
}

// TestWindowsOrphanScanIsEmpty ensures the disabled feature does not inspect app files.
func TestWindowsOrphanScanIsEmpty(t *testing.T) {
	if err := (Cleaner{}).ScanOrphans(context.Background(), (*mygo.Channel[OrphanItem])(nil)); err != nil {
		t.Fatal(err)
	}
}

// waitUninstallExit lets Windows release the executable image before temp cleanup.
func waitUninstallExit(t *testing.T, exe string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		procs, err := processSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		running := false
		for _, proc := range procs {
			if proc.Exe != "" && canonical(proc.Exe) == canonical(exe) {
				running = true
			}
		}
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake uninstaller did not exit")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestWindowsUninstallPreinitializedSTA accepts COM's successful S_FALSE result.
func TestWindowsUninstallPreinitializedSTA(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE)
	if err != nil && !errors.Is(err, syscall.Errno(windows.S_FALSE)) {
		t.Fatal(err)
	}
	defer windows.CoUninitialize()
	dir := t.TempDir()
	marker := filepath.Join(dir, "sta-marker")
	exe, args := fakeUninstaller(t, dir, marker, "write")
	if err := launchUninstaller(uninstallCommand{exe: exe, args: args}); err != nil {
		t.Fatal(err)
	}
	waitUninstallMarker(t, marker)
	waitUninstallExit(t, exe)
}
