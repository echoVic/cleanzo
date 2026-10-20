package main

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// openFiles is empty: Windows usage protection is based on process identities and roots, not share-delete locks.
func openFiles() ([]string, error) { return nil, nil }

// snapshotApps returns executable names, product names and executable paths from a single snapshot.
func snapshotApps(procs []procInfo) (map[string]bool, []string) {
	ids := map[string]bool{}
	var apps []string
	for _, p := range procs {
		name := strings.ToLower(p.Name)
		if p.Exe != "" {
			name = strings.ToLower(filepath.Base(p.Exe))
			apps = append(apps, p.Exe)
		}
		if name != "" {
			ids[strings.TrimSuffix(name, ".exe")] = true
		}
		product := p.Product
		if product == "" && p.Exe != "" {
			product = processProduct(p.Exe)
		}
		if product != "" {
			ids[strings.ToLower(product)] = true
		}
	}
	return ids, apps
}

// runningApps identifies all current-session processes, including entries with unreadable metadata.
func runningApps() (map[string]bool, []string, error) {
	procs, err := snapshotProcesses()
	if err != nil {
		return nil, nil, err
	}
	ids, apps := snapshotApps(procs)
	return ids, apps, nil
}

// platformUsage builds all Windows guards from one snapshot, failing closed on enumeration errors.
func platformUsage() *usageGuard {
	procs, err := snapshotProcesses()
	if err != nil {
		return &usageGuard{failed: true}
	}
	ids, _ := snapshotApps(procs)
	guard := &usageGuard{running: ids}
	roots := map[string]bool{}
	broad := map[string]bool{usagePathKey(homeDir()): true, usagePathKey(os.TempDir()): true}
	home := usagePathKey(homeDir())
	for _, dir := range environmentProtections().dirs {
		broad[dir] = true
	}
	for _, dir := range protectedHomeDirs {
		broad[home+dir] = true
	}
	for _, p := range procs {
		var paths []string
		if p.Exe != "" {
			paths = append(paths, filepath.Dir(p.Exe))
		}
		args, parseErr := windows.DecomposeCommandLine(p.CmdLine)
		if parseErr == nil {
			paths = append(paths, windowsArgumentPaths(args)...)
		}
		for _, path := range paths {
			path = windowsUsagePath(path)
			if path == "" {
				continue
			}
			root := usagePathKey(path)
			// Every cleanable path is below home; an ancestor root would block all cleanup.
			if !within(home, root) && !broad[root] && root != strings.ToLower(filepath.VolumeName(root)+string(filepath.Separator)) {
				roots[root] = true
			}
		}
	}
	for root := range roots {
		guard.roots = append(guard.roots, root)
	}
	return guard
}

// usagePathKey preserves useful lexical roots when an executable alias cannot be opened.
func usagePathKey(path string) string {
	if resolved := resolvePath(path); resolved != "" {
		path = resolved
	}
	return strings.ToLower(filepath.Clean(path))
}
