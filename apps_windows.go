package main

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// uninstallRegistryRoot selects one read-only uninstall view; tests replace the bases.
type uninstallRegistryRoot struct {
	id   string
	hive registry.Key
	path string
	view uint32
}

// uninstallRegistryRoots lists user, machine-native and machine-WOW64 uninstall keys.
var uninstallRegistryRoots = []uninstallRegistryRoot{
	{"hkcu", registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall`, registry.WOW64_64KEY},
	{"hklm", registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Uninstall`, registry.WOW64_64KEY},
	{"hklm32", registry.LOCAL_MACHINE, `Software\Microsoft\Windows\CurrentVersion\Uninstall`, registry.WOW64_32KEY},
}

// registeredApp contains only the selected key's public data and declared command.
type registeredApp struct {
	app     AppInfo
	command string
}

// registryString reads optional registry text, expanding REG_EXPAND_SZ values only.
func registryString(key registry.Key, name string) string {
	value, kind, err := key.GetStringValue(name)
	if err != nil {
		return ""
	}
	if kind == registry.EXPAND_SZ {
		value, err = registry.ExpandString(value)
		if err != nil {
			return ""
		}
	}
	return strings.TrimSpace(value)
}

// readRegisteredApp ignores components, child entries and keys without an uninstaller.
func readRegisteredApp(root uninstallRegistryRoot, subkey string) (registeredApp, error) {
	key, err := registry.OpenKey(root.hive, root.path+`\`+subkey, registry.QUERY_VALUE|root.view)
	if err != nil {
		return registeredApp{}, errGone
	}
	defer key.Close()
	system, _, _ := key.GetIntegerValue("SystemComponent")
	_, _, parentErr := key.GetValue("ParentKeyName", nil)
	name, command := registryString(key, "DisplayName"), registryString(key, "UninstallString")
	if system == 1 || !errors.Is(parentErr, registry.ErrNotExist) || name == "" || command == "" {
		return registeredApp{}, errBlocked
	}
	size, _, _ := key.GetIntegerValue("EstimatedSize")
	if size > math.MaxInt64/1024 {
		size = 0
	}
	return registeredApp{app: AppInfo{
		ID: root.id + ":" + subkey, Name: name, Publisher: registryString(key, "Publisher"),
		Path: registryString(key, "InstallLocation"), Version: registryString(key, "DisplayVersion"), Size: int64(size) * 1024,
	}, command: command}, nil
}

// registeredAppByID reopens exactly one immediate child of an allowed registry base.
func registeredAppByID(id string) (registeredApp, error) {
	prefix, subkey, ok := strings.Cut(id, ":")
	if !ok || subkey == "" || subkey == "." || subkey == ".." || strings.ContainsAny(subkey, "\\/\x00") {
		return registeredApp{}, errBlocked
	}
	for _, root := range uninstallRegistryRoots {
		if root.id == prefix {
			return readRegisteredApp(root, subkey)
		}
	}
	return registeredApp{}, errBlocked
}

// installedAppRunning compares live executable paths with the display-only location.
func installedAppRunning(app AppInfo, processes []procInfo) bool {
	if app.Path == "" || !filepath.IsAbs(app.Path) {
		return false
	}
	location := canonical(app.Path)
	for _, p := range processes {
		if p.Exe != "" && within(canonical(p.Exe), location) {
			return true
		}
	}
	return false
}

// listInstalledApps enumerates registry uninstall entries without requesting write access.
func listInstalledApps() []AppInfo {
	apps := make([]AppInfo, 0)
	seen := map[string]bool{}
	processes, _ := snapshotProcesses()
	for _, root := range uninstallRegistryRoots {
		key, err := registry.OpenKey(root.hive, root.path, registry.ENUMERATE_SUB_KEYS|root.view)
		if err != nil {
			continue
		}
		names, err := key.ReadSubKeyNames(-1)
		key.Close()
		if err != nil {
			continue
		}
		sort.Strings(names)
		for _, name := range names {
			entry, err := readRegisteredApp(root, name)
			if err != nil {
				continue
			}
			app := entry.app
			// Identical metadata and launch targets in multiple views describe one product.
			identity := strings.ToLower(strings.Join([]string{app.Name, app.Publisher, app.Version, app.Path, entry.command}, "\x00"))
			if seen[identity] {
				continue
			}
			seen[identity] = true
			app.Running = installedAppRunning(app, processes)
			apps = append(apps, app)
		}
	}
	sort.Slice(apps, func(i, j int) bool { return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name) })
	return apps
}

// appUninstallDetail has no related-file cleanup on Windows.
func appUninstallDetail(id string) UninstallDetail {
	detail := UninstallDetail{App: AppInfo{ID: id}, Items: []UninstallItem{}}
	entry, err := registeredAppByID(id)
	if err != nil {
		return detail
	}
	detail.App = entry.app
	processes, err := snapshotProcesses()
	detail.App.Running = err == nil && installedAppRunning(entry.app, processes)
	detail.TotalSize = entry.app.Size
	return detail
}

// uninstallApp launches only the freshly reread registry command, never deleting files.
func uninstallApp(id string) UninstallResult {
	result := UninstallResult{Path: id, Failed: []string{}}
	fail := func(err error) UninstallResult {
		result.Failed = append(result.Failed, id+": "+err.Error())
		return result
	}
	entry, err := registeredAppByID(id)
	if err != nil {
		return fail(err)
	}
	processes, err := snapshotProcesses()
	if err != nil {
		return fail(errUsageUnknown)
	}
	if installedAppRunning(entry.app, processes) {
		return fail(errInUse)
	}
	command, err := parseUninstallString(entry.command, func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && info.Mode().IsRegular()
	})
	if err != nil {
		return fail(errBlocked)
	}
	if err := launchUninstaller(command); err != nil {
		return fail(errBlocked)
	}
	result.Launched = true
	return result
}

// shellExecuteInfo mirrors SHELLEXECUTEINFOW; Windows tests assert its 64-bit layout.
type shellExecuteInfo struct {
	size       uint32
	mask       uint32
	window     windows.Handle
	verb       *uint16
	file       *uint16
	parameters *uint16
	directory  *uint16
	show       int32
	instance   windows.Handle
	idList     uintptr
	class      *uint16
	classKey   windows.Handle
	hotKey     uint32
	icon       windows.Handle
	process    windows.Handle
}

// shellExecuteEx is the Unicode structured launch API, without shell command evaluation.
var shellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// launchUninstaller keeps the uninstaller UI visible, while never spawning a command shell.
func launchUninstaller(command uninstallCommand) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil && !errors.Is(err, syscall.Errno(windows.S_FALSE)) {
		return err
	}
	defer windows.CoUninitialize()
	exe := command.exe
	if !filepath.IsAbs(exe) {
		dir, err := windows.GetSystemDirectory()
		if err != nil {
			return err
		}
		exe = filepath.Join(dir, exe)
	}
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	args, err := windows.UTF16PtrFromString(command.args)
	if err != nil {
		return err
	}
	// SEE_MASK_NOCLOSEPROCESS | SEE_MASK_NOASYNC | SEE_MASK_NO_CONSOLE.
	// Keep GUI prompts visible without creating a separate console or waiting for completion.
	info := shellExecuteInfo{mask: 0x40 | 0x100 | 0x8000, verb: verb, file: file, parameters: args, show: 1}
	info.size = uint32(unsafe.Sizeof(info))
	ok, _, callErr := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if info.process != 0 {
		windows.CloseHandle(info.process)
	}
	if ok == 0 {
		return callErr
	}
	return nil
}

// scanOrphans closes naturally when the bound call returns, emitting no Windows items.
func scanOrphans(context.Context, *mygo.Channel[OrphanItem]) error { return nil }
