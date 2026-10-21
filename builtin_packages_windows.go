package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// getPackageFamilyName returns a Win32 status code directly, rather than through GetLastError (appmodel.h).
var getPackageFamilyName = statusKernel.NewProc("GetPackageFamilyName")

// processPackageFamilyName opens only a query handle; an empty successful result means unpackaged.
func processPackageFamilyName(pid int32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	return windowsPackageFamilyName(h)
}

// windowsPackageFamilyName uses appmodel.h's two-call UTF-16 buffer sizing protocol.
func windowsPackageFamilyName(h windows.Handle) (string, error) {
	if err := getPackageFamilyName.Find(); err != nil {
		return "", err
	}
	var size uint32
	result, _, _ := getPackageFamilyName.Call(uintptr(h), uintptr(unsafe.Pointer(&size)), 0)
	if syscall.Errno(result) == windows.APPMODEL_ERROR_NO_PACKAGE {
		return "", nil
	}
	if syscall.Errno(result) != windows.ERROR_INSUFFICIENT_BUFFER {
		return "", syscall.Errno(result)
	}
	if size == 0 || size > 32768 {
		return "", windows.ERROR_INVALID_DATA
	}
	buffer := make([]uint16, size)
	result, _, _ = getPackageFamilyName.Call(uintptr(h), uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buffer[0])))
	if syscall.Errno(result) == windows.APPMODEL_ERROR_NO_PACKAGE {
		return "", nil
	}
	if result != 0 {
		return "", syscall.Errno(result)
	}
	if size == 0 || size > uint32(len(buffer)) || buffer[size-1] != 0 {
		return "", windows.ERROR_INVALID_DATA
	}
	family := windows.UTF16ToString(buffer[:size])
	if windowsPackageFamilyPublisher(family) == "" {
		return "", windows.ERROR_INVALID_DATA
	}
	return family, nil
}
