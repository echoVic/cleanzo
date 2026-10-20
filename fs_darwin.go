package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// changedAt is when a file last changed: its modification time, or its
// status change time, which moving, copying and extracting set, while they
// keep the modification time of the original.
func changedAt(info fs.FileInfo) time.Time {
	t := info.ModTime()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if c := time.Unix(st.Ctimespec.Unix()); c.After(t) {
			return c
		}
	}
	return t
}

// onDisk is a file's length, or the space its blocks take when that is less.
func onDisk(info fs.FileInfo) int64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return min(info.Size(), st.Blocks*512)
	}
	return info.Size()
}

// protectedFlags are the file flags under which macOS refuses to remove an
// entry: immutable, append-only, and the restricted and no-unlink flags of
// System Integrity Protection.
const protectedFlags = 0x2 | 0x4 | 0x20000 | 0x40000 | 0x80000 | 0x100000

// removalRefused reports whether macOS would refuse to remove the entry:
// its flags forbid it, or, for a folder, privacy protection forbids reading
// it (EPERM, unlike the EACCES of mere permissions, which removeAll fixes).
func removalRefused(path string, info fs.FileInfo) bool {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && st.Flags&protectedFlags != 0 {
		return true
	}
	if info.IsDir() {
		f, err := os.Open(path)
		if err != nil {
			return errors.Is(err, syscall.EPERM)
		}
		f.Close()
	}
	return false
}

// diskStat returns the capacity of the boot volume.
func diskStat() DiskStat {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return DiskStat{}
	}
	total := int64(st.Bsize) * int64(st.Blocks)
	free := int64(st.Bsize) * int64(st.Bavail)
	return DiskStat{Total: total, Used: total - free, Free: free}
}

// mountPoint reports whether path is where another volume is mounted, such
// as a disk image or a VM's share in the home folder: that is another disk,
// whose usage is not the home folder's.
func mountPoint(path string) bool {
	var st, parent syscall.Stat_t
	if syscall.Lstat(path, &st) != nil || syscall.Lstat(filepath.Dir(path), &parent) != nil {
		return false
	}
	return st.Dev != parent.Dev
}

// linkModes preserves Darwin's existing symlink checks exactly.
const linkModes = fs.ModeSymlink

// resolveFinal retains Darwin's longest-existing-prefix EvalSymlinks behavior.
func resolveFinal(p string) string {
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// platformCleanPath adds no extra path restrictions on Darwin.
func platformCleanPath(string) bool { return true }

// platformRemovalSafe leaves Darwin removal checks unchanged.
func platformRemovalSafe(string, ...context.Context) bool { return true }

// platformMeasureEntry leaves Darwin measurements unchanged.
func platformMeasureEntry(string, fs.FileInfo) bool { return true }

// userPickable leaves Darwin user-picked paths unchanged.
func userPickable(string) bool { return true }

// platformUsage retains the existing Darwin probes.
func platformUsage() *usageGuard { return probeFileUsage() }
