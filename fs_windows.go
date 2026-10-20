package main

import (
	"context"
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
			return true
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

// linkModes includes junctions and other reparse entries reported as irregular.
const linkModes = fs.ModeSymlink | fs.ModeIrregular

// resolveFinal resolves the longest existing prefix through a file handle, including junctions and short names.
func resolveFinal(path string) string {
	rest := ""
	for {
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return ""
		}
		h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_NO_RECALL, 0)
		if err == nil {
			buf := make([]uint16, 32768)
			n, finalErr := getFinalPathName(h, &buf[0], uint32(len(buf)), 0)
			windows.CloseHandle(h)
			if finalErr != nil || n == 0 || n >= uint32(len(buf)) {
				return ""
			}
			final := windows.UTF16ToString(buf[:n])
			final = strings.TrimPrefix(final, `\\?\`)
			if !windowsLocalPath(final) {
				return ""
			}
			return filepath.Join(final, rest)
		}
		// Missing suffixes may be appended; unreadable existing prefixes never become lexical guesses.
		if !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return ""
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		rest = filepath.Join(filepath.Base(path), rest)
		path = parent
	}
}

// platformCleanPath refuses special forms and every reparse or placeholder component below the profile.
func platformCleanPath(path string) bool {
	if !windowsLocalPath(path) {
		return false
	}
	home := resolvePath(homeDir())
	if home == "" {
		return false
	}
	abs := filepath.Clean(path)
	rawHome := filepath.Clean(homeDir())
	root := ""
	switch {
	case within(abs, rawHome):
		root = rawHome
	case within(abs, home):
		root = home
	default:
		// Find the profile anchor in a full 8.3 spelling. Aliases above the
		// resolved profile are trusted; every component below it is inspected.
		prefix := filepath.VolumeName(abs) + string(filepath.Separator)
		relative, err := filepath.Rel(prefix, abs)
		if err != nil {
			return false
		}
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			prefix = filepath.Join(prefix, part)
			if resolved := resolvePath(prefix); resolved != "" && strings.EqualFold(resolved, home) {
				root = prefix
				break
			}
		}
	}
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	prefix := home
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." {
			continue
		}
		prefix = filepath.Join(prefix, part)
		info, err := windowsPathLstat(prefix)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				break
			}
			return false
		}
		if !windowsEntrySafe(info) {
			return false
		}
	}
	final := resolvePath(abs)
	return final != "" && within(final, home)
}

// getFinalPathName allows native path-probe failure and cost regressions to be exercised.
var getFinalPathName = windows.GetFinalPathNameByHandle

// windowsPathLstat reads only the components below the resolved profile.
var windowsPathLstat = os.Lstat

// windowsEntrySafe checks attributes without opening a cloud file's data stream.
func windowsEntrySafe(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && info.Mode()&linkModes == 0 && st.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_OFFLINE|windows.FILE_ATTRIBUTE_RECALL_ON_OPEN|windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS) == 0
}

// platformRemovalSafe refuses a whole directory if deleting it would remove any nested boundary.
func platformRemovalSafe(path string, contexts ...context.Context) bool {
	ctx := context.Background()
	if len(contexts) > 0 && contexts[0] != nil {
		ctx = contexts[0]
	}
	if ctx.Err() != nil || !platformCleanPath(path) {
		return false
	}
	root := strings.ToLower(resolvePath(path))
	if root == "" || !safeResolved(root) {
		return false
	}
	home := strings.ToLower(resolvePath(homeDir()))
	protections := environmentProtections()
	// Copy before appending: cached slices are shared immutable snapshots.
	dirs := append([]string(nil), protections.dirs...)
	trees := append([]string(nil), protections.trees...)
	for _, dir := range protectedHomeDirs {
		dirs = append(dirs, home+dir)
	}
	for _, tree := range protectedHomeTrees {
		trees = append(trees, home+tree)
	}
	path = filepath.Clean(path)
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if p == path && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !windowsEntrySafe(info) {
			return fs.ErrPermission
		}
		key := root + strings.ToLower(p[len(path):])
		first, _, _ := strings.Cut(strings.TrimPrefix(key, home+string(filepath.Separator)), string(filepath.Separator))
		if strings.HasPrefix(first, "onedrive") || strings.HasPrefix(first, "ntuser") || appLibrary(filepath.Base(key)) {
			return fs.ErrPermission
		}
		for _, dir := range dirs {
			if key == dir || within(dir, key) {
				return fs.ErrPermission
			}
		}
		for _, tree := range trees {
			if within(key, tree) || within(tree, key) {
				return fs.ErrPermission
			}
		}
		return nil
	})
	return err == nil
}

// platformMeasureEntry uses only attributes already read by the walk.
func platformMeasureEntry(path string, info fs.FileInfo) bool { return windowsEntrySafe(info) }

// userPickable reserves AppData for explicit rule cleanup, including redirected app data roots.
func userPickable(path string) bool {
	if !platformCleanPath(path) {
		return false
	}
	p := canonical(path)
	home := strings.ToLower(resolvePath(homeDir()))
	if within(p, filepath.Join(home, "appdata")) {
		return false
	}
	for _, root := range environmentProtections().dirs {
		if within(p, root) {
			return false
		}
	}

	return true
}
