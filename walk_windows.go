package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// walkWindows stays inside the home volume and skips unsafe entries before WalkDir descends.
// Windows volume mount points are reparse points, so attribute checks avoid a volume syscall per file.
func walkWindows(ctx context.Context, root string, visit func(string, fs.FileInfo) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !platformCleanPath(root) {
		return errBlocked
	}
	volume, err := volumePath(root)
	if err != nil {
		return err
	}
	homeVolume, err := volumePath(homeDir())
	if err != nil {
		return err
	}
	if !strings.EqualFold(volume, homeVolume) {
		return errBlocked
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if path == root {
				return err
			}
			return nil
		} // match partial du/find reads
		info, err := d.Info()
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if !platformMeasureEntry(path, info) {
			if path == root {
				return errBlocked
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		return visit(path, info)
	})
}

// duSize measures logical file bytes, respecting the caller's deadline and cancellation.
func duSize(ctx context.Context, path string, timeout time.Duration) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var size int64
	err := walkWindows(ctx, path, func(_ string, info fs.FileInfo) error {
		if info.Mode().IsRegular() {
			size += onDisk(info)
		}
		return nil
	})
	return size, err
}

// fullScanTargets expands AppData one level and never enumerates system roots or unsafe home entries.
func fullScanTargets() []string {
	home := homeDir()
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	var targets []string
	add := func(path string) {
		if info, err := os.Lstat(path); err == nil && windowsEntrySafe(info) && !mountPoint(path) {
			targets = append(targets, path)
		}
	}
	for _, entry := range entries {
		path := filepath.Join(home, entry.Name())
		info, err := entry.Info()
		if err != nil || !windowsEntrySafe(info) {
			continue
		}
		if strings.EqualFold(entry.Name(), "AppData") && entry.IsDir() {
			children, err := os.ReadDir(path)
			if err == nil {
				for _, child := range children {
					add(filepath.Join(path, child.Name()))
				}
			}
		} else {
			add(path)
		}
	}
	return targets
}

// findLargeFiles uses the same attribute and volume boundaries as folder measurements.
func findLargeFiles(ctx context.Context, root string, threshold int64) ([]LargeFile, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	var files []LargeFile
	err := walkWindows(ctx, root, func(path string, info fs.FileInfo) error {
		if info.Mode().IsRegular() && info.Size() >= threshold {
			files = append(files, LargeFile{Path: path, Size: info.Size(), Trashable: removable(path)})
		}
		return nil
	})
	return files, err
}

// platformDirChildren uses a bounded worker pool and refuses traversal through a selected boundary.
func platformDirChildren(ctx context.Context, path string) (DirListing, error) {
	if err := ctx.Err(); err != nil {
		return DirListing{}, err
	}
	if !platformCleanPath(path) {
		return DirListing{}, errBlocked
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return DirListing{}, err
	}
	jobs := make(chan fs.DirEntry)
	var mu sync.Mutex
	var wg sync.WaitGroup
	items := make([]DirItem, 0, len(entries))
	for range min(16, len(entries)) {
		wg.Go(func() {
			for entry := range jobs {
				full := filepath.Join(path, entry.Name())
				size := int64(-1)
				info, err := entry.Info()
				safe := err == nil && windowsEntrySafe(info)
				if safe {
					if entry.IsDir() {
						if measured, err := dirMeasure(ctx, full, 8*time.Second); err == nil {
							size = measured
						}
					} else {
						size = info.Size()
					}
				}
				item := DirItem{Name: entry.Name(), Path: full, Size: size, IsDir: entry.IsDir(), Trashable: safe && removable(full)}
				mu.Lock()
				items = append(items, item)
				mu.Unlock()
			}
		})
	}
dispatch:
	for _, entry := range entries {
		select {
		case jobs <- entry:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	if ctx.Err() != nil {
		return DirListing{}, ctx.Err()
	}
	return childDirListing(items), nil
}
