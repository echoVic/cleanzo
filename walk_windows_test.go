package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWindowsWalkExactSizes checks all file pages against the same seeded logical byte counts.
func TestWindowsWalkExactSizes(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "Documents", "tree")
	writeFile(t, filepath.Join(root, "a.bin"), 123)
	writeFile(t, filepath.Join(root, "nested", "b.bin"), 456)
	writeFile(t, filepath.Join(root, "nested", "c.bin"), 789)
	if size, err := duSize(context.Background(), root, time.Second); err != nil || size != 1368 {
		t.Fatalf("du=%d, %v", size, err)
	}
	files, err := findLargeFiles(context.Background(), root, 456)
	if err != nil || len(files) != 2 {
		t.Fatalf("large=%+v, %v", files, err)
	}
	if size, err := duSize(context.Background(), files[0].Path, time.Second); err != nil || size != files[0].Size {
		t.Fatalf("file size=%d, %v", size, err)
	}
	listing, err := (Cleaner{}).DirChildren(context.Background(), root)
	if err != nil || listing.Total != 2 || listing.Items[0].Size != 1245 || listing.Items[1].Size != 123 {
		t.Fatalf("children=%+v, %v", listing, err)
	}
	app := filepath.Join(home, "AppData", "Local", "Cache", "big.bin")
	writeFile(t, app, 700)
	for _, p := range fullScanTargets() {
		if !within(p, home) || p == filepath.Join(home, "AppData") {
			t.Fatalf("invalid target %q", p)
		}
	}
	items, _, skipped := collectFullScan(context.Background(), nil)
	var sum int64
	for _, item := range items {
		sum += item.Size
		if within(item.Path, filepath.Join(home, "AppData")) && item.Trashable {
			t.Fatal("AppData trashable")
		}
	}
	if sum != 2068 || skipped != 0 {
		t.Fatalf("FullScan sum=%d skipped=%d items=%+v", sum, skipped, items)
	}
	large, _, _ := collectLargeScan(context.Background(), 600, nil)
	if len(large) != 2 {
		t.Fatalf("large=%+v", large)
	}
	for _, item := range large {
		if item.Path == app && item.Trashable {
			t.Fatal("AppData file trashable")
		}
	}
}

// TestWindowsWalkSkipsJunctions checks nested, legacy and explicitly selected junctions.
func TestWindowsWalkSkipsJunctions(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "outside.bin"), 1000)
	root := filepath.Join(home, "scan")
	writeFile(t, filepath.Join(root, "ordinary.bin"), 123)
	link := filepath.Join(root, "escape")
	makeJunction(t, link, outside)
	makeJunction(t, filepath.Join(home, "Application Data"), outside)
	if size, err := duSize(context.Background(), root, time.Second); err != nil || size != 123 {
		t.Fatalf("du=%d %v", size, err)
	}
	if _, err := duSize(context.Background(), filepath.Join(link, "outside.bin"), time.Second); err == nil {
		t.Fatal("followed root ancestor junction")
	}
	files, err := findLargeFiles(context.Background(), root, 1)
	if err != nil || len(files) != 1 || files[0].Size != 123 {
		t.Fatalf("large=%+v %v", files, err)
	}
	items, _, _ := collectFullScan(context.Background(), nil)
	for _, item := range items {
		if item.Path == root && item.Size != 123 {
			t.Fatalf("FullScan followed junction: %+v", item)
		}
		if filepath.Base(item.Path) == "Application Data" {
			t.Fatal("legacy junction targeted")
		}
	}
	listing, err := (Cleaner{}).DirChildren(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (Cleaner{}).DirChildren(context.Background(), link); !errors.Is(err, errBlocked) {
		t.Fatalf("selected junction listing=%v", err)
	}

	for _, item := range listing.Items {
		if item.Path == link && (item.Size != -1 || item.Trashable) {
			t.Fatalf("junction child=%+v", item)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "outside.bin")); err != nil {
		t.Fatal(err)
	}
}

// TestWindowsWalkCancellation checks canceled and expired requests on each walk entry point.
func TestWindowsWalkCancellation(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeFile(t, filepath.Join(home, "a"), 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := duSize(ctx, home, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("du err=%v", err)
	}
	if _, err := findLargeFiles(ctx, home, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("find err=%v", err)
	}
	if _, err := duSize(context.Background(), home, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline err=%v", err)
	}
	if _, err := (Cleaner{}).DirChildren(ctx, home); !errors.Is(err, context.Canceled) {
		t.Fatalf("children err=%v", err)
	}
	live, cancelLive := context.WithCancel(context.Background())
	visited := 0
	err := walkWindows(live, home, func(_ string, _ fs.FileInfo) error { visited++; cancelLive(); return nil })
	if !errors.Is(err, context.Canceled) || visited != 1 {
		t.Fatalf("in-flight cancellation: visits=%d err=%v", visited, err)
	}
	items, _, _ := collectFullScan(ctx, nil)
	if len(items) != 0 {
		t.Fatal("canceled full scan returned items")
	}
}
