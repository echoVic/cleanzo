package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// changedAt uses the newer of the last write and creation times.
func changedAt(info fs.FileInfo) time.Time {
	t := info.ModTime()
	if st, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		for _, stamp := range []syscall.Filetime{st.LastWriteTime, st.CreationTime} {
			if changed := time.Unix(0, stamp.Nanoseconds()); changed.After(t) {
				t = changed
			}
		}
	}
	return t
}

// onDisk conservatively reports the logical file size.
func onDisk(info fs.FileInfo) int64 { return info.Size() }

// removalRefused blocks reparse points, cloud placeholders and unreadable folders.
func removalRefused(path string, info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || st.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_OFFLINE|windows.FILE_ATTRIBUTE_RECALL_ON_OPEN|windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS) != 0 {
		return true
	}
	if info.IsDir() {
		f, err := os.Open(path)
		if err != nil {
			return errors.Is(err, fs.ErrPermission) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
		}
		f.Close()
	}
	return false
}

// volumePath returns the volume mount path containing path.
func volumePath(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, 32768)
	if err := windows.GetVolumePathName(p, &buf[0], uint32(len(buf))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf), nil
}

// mountPoint blocks reparse points, other volumes and failed volume probes.
func mountPoint(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return true
	}
	st, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok || st.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return true
	}
	volume, err := volumePath(path)
	parent, parentErr := volumePath(filepath.Dir(path))
	return err != nil || parentErr != nil || !strings.EqualFold(volume, parent)
}

// diskStat reads the capacity of the home folder's volume.
func diskStat() DiskStat {
	volume, err := volumePath(homeDir())
	if err != nil {
		return DiskStat{}
	}
	p, err := windows.UTF16PtrFromString(volume)
	if err != nil {
		return DiskStat{}
	}
	var free, total uint64
	if windows.GetDiskFreeSpaceEx(p, &free, &total, nil) != nil {
		return DiskStat{}
	}
	return DiskStat{Total: int64(total), Used: int64(total - free), Free: int64(free)}
}
