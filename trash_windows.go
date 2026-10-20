package main

import (
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

// getDriveType lets tests reject drive kinds without opening a shell dialog.
var getDriveType = windows.GetDriveType

// shellFileOperationW is missing from x/sys/windows; shellapi.h declares this shell32 entry point.
var shellFileOperationW = windows.NewLazySystemDLL("shell32.dll").NewProc("SHFileOperationW")

// shellRecycle invokes the Unicode shell operation; its return value is an operation error code.
var shellRecycle = func(op *shellFileOperation) int32 {
	result, _, _ := shellFileOperationW.Call(uintptr(unsafe.Pointer(op)))
	return int32(result)
}

// recycle moves only safe fixed-drive entries to the bin, with no silent permanent-delete fallback.
func recycle(path string) error {
	if !cleanable(path) || !platformRemovalSafe(path) {
		return errBlocked
	}
	root, err := windows.UTF16PtrFromString(filepath.VolumeName(resolvePath(path)) + `\`)
	if err != nil || getDriveType(root) != windows.DRIVE_FIXED {
		return errBlocked
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
	result := shellRecycle(&op)
	runtime.KeepAlive(from)
	if result != 0 || op.aborted != 0 {
		return errBlocked
	}
	return nil
}

// init replaces the framework's Windows trash operation with the guarded implementation.
func init() { trashItem = recycle }

// recycleOwner uses the first live top-level window, which is Cleanzo's main window in creation order.
var recycleOwner = func() uintptr {
	for _, window := range mygo.Windows() {
		if !window.IsDestroyed() && window.Parent() == nil {
			return window.NativeHandle()
		}
	}
	return 0
}
