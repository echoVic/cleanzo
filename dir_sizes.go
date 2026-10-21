package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const dirSizeTimeout = 300 * time.Second

// Shared by the short child pass and long measurements; tests replace only this seam.
var dirMeasure = duSize

var dirSizeSlots = make(chan struct{}, 4)

// Keep every unknown folder for the long pass, reserving the remaining slots
// for the largest measured entries. Unknown folders may exceed the usual cap.
func childDirListing(items []DirItem) DirListing {
	sort.Slice(items, func(i, j int) bool { return items[i].Size > items[j].Size })
	unknown := 0
	for _, item := range items {
		if item.IsDir && item.Size < 0 {
			unknown++
		}
	}
	remaining := max(0, 60-unknown)
	kept := make([]DirItem, 0, min(len(items), max(60, unknown)))
	for _, item := range items {
		if item.IsDir && item.Size < 0 {
			kept = append(kept, item)
		} else if remaining > 0 {
			kept = append(kept, item)
			remaining--
		}
	}
	return DirListing{Items: kept, Total: len(items)}
}

// DirSize measures one folder without modifying it. Unlike the short child pass,
// it allows large trees to finish and never returns partial bytes as a final size.
func (Cleaner) DirSize(ctx context.Context, path string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !filepath.IsAbs(path) {
		return 0, fmt.Errorf("folder path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("path is not a folder: %s", path)
	}
	select {
	case dirSizeSlots <- struct{}{}:
		defer func() { <-dirSizeSlots }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, dirSizeTimeout)
	defer cancel()
	size, err := dirMeasure(ctx, path, dirSizeTimeout)
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if err != nil {
		return 0, err
	}
	return size, nil
}
