package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// TestCollectPhotoFilesStopsWhenCancelled: a similar-photo scan that ends
// stops walking the photo folders.
func TestCollectPhotoFilesStopsWhenCancelled(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.jpg"), 10)
	writeFile(t, filepath.Join(root, "sub", "b.png"), 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := platformCollectPhotoFiles(ctx, []string{root}, 100); len(got) != 0 {
		t.Fatalf("cancelled walk found %v", got)
	}
	if got := platformCollectPhotoFiles(context.Background(), []string{root}, 100); len(got) != 2 {
		t.Fatalf("walk found %v", got)
	}
}

// TestDirChildrenStartsAGoroutinePerFreeSlot: a folder with hundreds of
// subfolders is measured by a bounded number of goroutines, not one parked
// goroutine per entry.
func TestDirChildrenStartsAGoroutinePerFreeSlot(t *testing.T) {
	root := t.TempDir()
	const folders = 200
	for i := range folders {
		mustMkdir(t, filepath.Join(root, fmt.Sprintf("d%03d", i)))
	}
	var entered atomic.Int32
	release := make(chan struct{})
	stubDirMeasure(t, func(context.Context, string, time.Duration) (int64, error) {
		entered.Add(1)
		<-release
		return 1, nil
	})
	before := runtime.NumGoroutine()
	done := make(chan DirListing)
	go func() {
		listing, _ := platformDirChildren(context.Background(), root)
		done <- listing
	}()
	for deadline := time.Now().Add(5 * time.Second); entered.Load() < 16 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // any goroutines beyond the slots start meanwhile
	measuring, extra := entered.Load(), runtime.NumGoroutine()-before
	close(release)
	listing := <-done
	if measuring != 16 || extra > 40 {
		t.Fatalf("measuring=%d goroutines=%d for %d folders", measuring, extra, folders)
	}
	if listing.Total != folders {
		t.Fatalf("listing total %d", listing.Total)
	}
}
