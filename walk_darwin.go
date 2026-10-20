package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// fullScanTargets returns the directories the full scan enumerates:
// the user's home with ~/Library expanded one level, plus system-level roots.
func fullScanTargets() []string {
	h := homeDir()
	var out []string
	defer func() { out = slices.DeleteFunc(out, mountPoint) }()
	entries, err := os.ReadDir(h)
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			if name == ".Trash" {
				continue
			}
			p := filepath.Join(h, name)
			if name == "Library" {
				if lib, err := os.ReadDir(p); err == nil {
					for _, le := range lib {
						out = append(out, filepath.Join(p, le.Name()))
					}
				}
				continue
			}
			out = append(out, p)
		}
	}
	for _, root := range []string{"/Library", "/usr/local", "/opt", "/private/var/tmp", "/private/var/log"} {
		subs, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range subs {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}

// duSize returns the disk usage of path in bytes using /usr/bin/du, staying
// on path's volume. du is a C implementation and is several times faster
// than a Go walker on large trees. A timeout guards against pathological
// directories.
func duSize(ctx context.Context, path string, timeout time.Duration) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := quietCommand(exec.CommandContext(ctx, "/usr/bin/du", "-skx", path)).Output()
	if err != nil && !partialRun(ctx, err) {
		return 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty du output for %s", path)
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	return kb * 1024, nil
}

// findLargeFiles lists regular files under root that are larger than
// threshold, using BSD find (-size supports k/M/G suffixes on macOS) without
// leaving root's volume.
func findLargeFiles(ctx context.Context, root string, threshold int64) ([]LargeFile, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	kb := (threshold - 1) / 1024
	out, err := quietCommand(exec.CommandContext(ctx, "/usr/bin/find", "-x", root, "-type", "f", "-size", fmt.Sprintf("+%dk", kb), "-print0")).Output()
	if err != nil && !partialRun(ctx, err) {
		return nil, err
	}
	var files []LargeFile
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if fi.Size() < threshold {
			continue
		}
		files = append(files, LargeFile{Path: p, Size: fi.Size(), Trashable: removable(p)})
	}
	return files, nil
}
