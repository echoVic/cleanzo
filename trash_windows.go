package main

import (
	"errors"
	"path/filepath"
	"runtime"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

// getDriveType lets tests reject drive kinds before reaching the shell.
var getDriveType = windows.GetDriveType

// shellTrashItem uses mygo's main-thread dispatch and refusal of permanent deletion.
var shellTrashItem = mygo.Shell.TrashItem

// errNotRecycled means the shell could not recycle the item and kept it in place.
var errNotRecycled = errors.New("not-recycled")

// recycle moves only safe fixed-drive entries to the bin, with no silent permanent-delete fallback.
func recycle(path string) error {
	if !cleanable(path) || !platformRemovalSafe(path) {
		return errBlocked
	}
	root, err := windows.UTF16PtrFromString(filepath.VolumeName(resolvePath(path)) + `\`)
	if err != nil || getDriveType(root) != windows.DRIVE_FIXED {
		return errBlocked
	}
	if err := checkConnectedFiles(resolvePath(path)); err != nil {
		if errors.Is(err, errConnectedFiles) {
			return errConnectedFiles
		}
		return errBlocked
	}
	if err := shellTrashItem(path); err != nil {
		return errNotRecycled
	}
	return nil
}

// init replaces the framework's Windows trash operation with the guarded implementation.
func init() { trashItem = recycle }

// shellBinInfo matches SHQUERYRBINFO: DWORD cbSize followed by two aligned __int64 values.
type shellBinInfo struct {
	cbSize      uint32
	size, items int64
}

// The Unicode Recycle Bin functions are declared in shellapi.h, not wrapped by x/sys v0.49.0.
var (
	queryRecycleBinW = windows.NewLazySystemDLL("shell32.dll").NewProc("SHQueryRecycleBinW")
	emptyRecycleBinW = windows.NewLazySystemDLL("shell32.dll").NewProc("SHEmptyRecycleBinW")
)

// binVolume restricts native queries and emptying to the resolved profile's local volume.
func binVolume(home string) (*uint16, error) {
	resolved := resolvePath(home)
	if !windowsLocalPath(resolved) {
		return nil, errBlocked
	}
	return windows.UTF16PtrFromString(filepath.VolumeName(resolved) + `\`)
}

// queryWindowsBin observes the profile volume's bin without enumerating its protected filesystem.
func queryWindowsBin(home string) (binStats, error) {
	root, err := binVolume(home)
	if err != nil {
		return binStats{}, err
	}
	info := shellBinInfo{cbSize: uint32(unsafe.Sizeof(shellBinInfo{}))}
	result, _, _ := queryRecycleBinW.Call(uintptr(unsafe.Pointer(root)), uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(root)
	if int32(result) < 0 || info.size < 0 || info.items < 0 {
		return binStats{}, errBlocked
	}
	return binStats{size: info.size, items: info.items}, nil
}

// emptyWindowsBin requests no confirmation, progress UI or sound (SHERB_* from shellapi.h).
func emptyWindowsBin(home string) error {
	root, err := binVolume(home)
	if err != nil {
		return err
	}
	const flags = 0x1 | 0x2 | 0x4
	result, _, _ := emptyRecycleBinW.Call(0, uintptr(unsafe.Pointer(root)), flags)
	runtime.KeepAlive(root)
	if int32(result) != 0 {
		return errBlocked
	}
	return nil
}

// init installs the native rule operations without adding platform-dependent bound APIs.
func init() { queryNativeBin, emptyNativeBin = queryWindowsBin, emptyWindowsBin }
