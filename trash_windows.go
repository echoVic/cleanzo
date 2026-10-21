package main

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

// shellFileOperation matches SHFILEOPSTRUCTW in Windows SDK shellapi.h.
type shellFileOperation struct {
	window    uintptr
	operation uint32
	from, to  *uint16
	flags     uint16
	aborted   int32
	mappings  uintptr
	title     *uint16
}

// getDriveType lets tests reject drive kinds before reaching the shell.
var getDriveType = windows.GetDriveType

// shellFileOperationW is missing from x/sys/windows; shellapi.h declares this shell32 entry point.
var shellFileOperationW = windows.NewLazySystemDLL("shell32.dll").NewProc("SHFileOperationW")

// shellRecycle invokes the Unicode shell operation; its return value is an operation error code.
var shellRecycle = func(op *shellFileOperation) int32 {
	result, _, _ := shellFileOperationW.Call(uintptr(unsafe.Pointer(op)))
	return int32(result)
}

// recycleMeasure retains checked-walk failures and exposes cancellation separately from byte counts.
var recycleMeasure = func(ctx context.Context, path string) (size int64, failures int, err error) {
	size, _, _, failures = measureChecked(path, ctx)
	return size, failures, ctx.Err()
}

// errNotRecycled means the shell could not recycle the item and kept it in place.
var errNotRecycled = errors.New("not-recycled")

// recycle moves only safe fixed-drive entries to the bin, with no silent permanent-delete fallback.
func recycle(path string) error {
	return recycleWithContext(context.Background(), path)
}

// recycleWithContext lets internal callers cancel the guards and capacity measurement.
func recycleWithContext(ctx context.Context, path string) error {
	if ctx.Err() != nil {
		return errNotRecycled
	}
	if !cleanable(path) || !platformRemovalSafe(path, ctx) {
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
	nuke, capacity, known := recycleSettings(root)
	if known {
		if !recycleFits(0, nuke, capacity, true) {
			return errNotRecycled
		}
		measureCtx, cancel := context.WithTimeout(ctx, dirSizeTimeout)
		size, failures, measureErr := recycleMeasure(measureCtx, path)
		if measureErr == nil {
			measureErr = measureCtx.Err()
		}
		cancel()
		if ctx.Err() != nil || failures != 0 ||
			(measureErr != nil && !errors.Is(measureErr, context.DeadlineExceeded)) ||
			(measureErr == nil && !recycleFits(size, nuke, capacity, true)) {
			return errNotRecycled
		}
		// A measurement deadline leaves capacity unknown; WANTNUKEWARNING protects the shell move.
	}
	from, err := windows.UTF16FromString(path)
	if err != nil {
		return errBlocked
	}
	from = append(from, 0) // SHFileOperationW requires a double NUL terminated list.
	// FO_DELETE and FOF_* values are defined in Windows SDK shellapi.h.
	const foDelete = 3
	const flags = 0x0040 | 0x0010 | 0x0004 | 0x0400 | 0x4000 | 0x2000 // ALLOWUNDO, NOCONFIRMATION, SILENT, NOERRORUI, WANTNUKEWARNING, NO_CONNECTED_ELEMENTS.
	op := shellFileOperation{window: recycleOwner(), operation: foDelete, from: &from[0], flags: flags}
	if ctx.Err() != nil {
		return errNotRecycled
	}
	// mygo 0.6.2 TrashItem can permanently delete over-capacity items; keep WANTNUKEWARNING.
	result := shellRecycle(&op)
	runtime.KeepAlive(from)
	if result != 0 || op.aborted != 0 {
		return errNotRecycled
	}
	return nil
}

// init replaces the framework's Windows trash operation with the guarded implementation.
func init() { trashItem, trashWithContext = recycle, recycleWithContext }

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

// recycleOwner uses the first live top-level window, which is Cleanzo's main window in creation order.
var recycleOwner = func() uintptr {
	for _, window := range mygo.Windows() {
		if !window.IsDestroyed() && window.Parent() == nil {
			return window.NativeHandle()
		}
	}
	return 0
}
