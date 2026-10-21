package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func stubDirMeasure(t *testing.T, measure func(context.Context, string, time.Duration) (int64, error)) {
	t.Helper()
	previous := dirMeasure
	dirMeasure = measure
	t.Cleanup(func() { dirMeasure = previous })
}

func TestDirChildrenKeepsUnknownFolders(t *testing.T) {
	for _, unknown := range []int{2, 65} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			root := filepath.Join(home, "scan")
			for i := range 70 {
				writeFile(t, filepath.Join(root, fmt.Sprintf("file%02d", i)), i+1)
			}
			for i := range unknown {
				mustMkdir(t, filepath.Join(root, fmt.Sprintf("slow%02d", i)))
			}
			stubDirMeasure(t, func(_ context.Context, _ string, timeout time.Duration) (int64, error) {
				if timeout != 8*time.Second {
					t.Errorf("first-pass budget = %v, want 8s", timeout)
				}
				return 99999, context.DeadlineExceeded // partial bytes must remain unknown
			})
			got, err := (Cleaner{}).DirChildren(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if got.Total != 70+unknown || len(got.Items) != max(60, unknown) {
				t.Fatalf("items=%d total=%d, want %d/%d", len(got.Items), got.Total, max(60, unknown), 70+unknown)
			}
			unknownCount, knownCount := 0, 0
			for _, item := range got.Items {
				if item.IsDir {
					unknownCount++
					if item.Size != -1 {
						t.Errorf("timed-out child = %+v", item)
					}
				} else {
					knownCount++
					if item.Size < 13 { // two unknowns leave room for the 58 largest files
						t.Errorf("kept a smaller file: %+v", item)
					}
				}
			}
			if unknownCount != unknown || knownCount != max(0, 60-unknown) {
				t.Fatalf("unknown=%d known=%d", unknownCount, knownCount)
			}
		})
	}
}

func TestDirSizeUsesLongTimeout(t *testing.T) {
	root := t.TempDir()
	stubDirMeasure(t, func(ctx context.Context, path string, timeout time.Duration) (int64, error) {
		if path != root || timeout != 300*time.Second {
			t.Errorf("measurement path=%q budget=%v", path, timeout)
			return 0, context.DeadlineExceeded
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 300*time.Second {
			t.Error("measurement context must also have the long deadline")
		}
		return 73100000000, nil
	})
	if size, err := (Cleaner{}).DirSize(context.Background(), root); err != nil || size != 73100000000 {
		t.Fatalf("DirSize=%d, %v", size, err)
	}
}

func TestDirSizeRejectsInvalidPaths(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	writeFile(t, file, 10)
	stubDirMeasure(t, func(context.Context, string, time.Duration) (int64, error) {
		t.Error("invalid path reached the measurer")
		return 1, nil
	})
	for _, path := range []string{"relative", "", filepath.Join(root, "missing"), file} {
		if _, err := (Cleaner{}).DirSize(context.Background(), path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	if _, err := (Cleaner{}).DirSize(context.Background(), filepath.Join(root, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing path error=%v", err)
	}
}

func TestDirSizeConcurrencyAndQueuedCancellation(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{}, 12)
	release := make(chan struct{})
	var active, peak atomic.Int32
	stubDirMeasure(t, func(ctx context.Context, _ string, _ time.Duration) (int64, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		entered <- struct{}{}
		select {
		case <-release:
			return 123, nil
		case <-ctx.Done():
			return 12, ctx.Err()
		}
	})
	done := make(chan error, 12)
	for range 12 {
		go func() { _, err := (Cleaner{}).DirSize(ctx, root); done <- err }()
	}
	for range 4 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Error("four measurements did not start")
		}
	}
	queued, cancelQueued := context.WithCancel(ctx)
	queuedDone := make(chan error, 1)
	go func() { _, err := (Cleaner{}).DirSize(queued, root); queuedDone <- err }()
	cancelQueued()
	select {
	case err := <-queuedDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("queued cancellation=%v", err)
		}
	case <-ctx.Done():
		t.Error("queued cancellation did not return")
	}
	select {
	case <-entered:
		t.Error("more than four measurements started before a slot was released")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for range 12 {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	if peak.Load() != 4 {
		t.Errorf("peak concurrency=%d, want 4", peak.Load())
	}
}

func TestDirSizeCancellationDiscardsPartialSize(t *testing.T) {
	root := t.TempDir()
	entered := make(chan struct{})
	stubDirMeasure(t, func(ctx context.Context, _ string, _ time.Duration) (int64, error) {
		close(entered)
		<-ctx.Done()
		return 42, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		size, err := (Cleaner{}).DirSize(ctx, root)
		if !errors.Is(err, context.Canceled) || size > 0 {
			t.Errorf("canceled DirSize=%d, %v", size, err)
		}
	}()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("DirSize did not stop after cancellation")
	}
}

func TestDirSizeTimeoutDiscardsPartialSize(t *testing.T) {
	stubDirMeasure(t, func(context.Context, string, time.Duration) (int64, error) {
		return 42, context.DeadlineExceeded
	})
	if size, err := (Cleaner{}).DirSize(context.Background(), t.TempDir()); !errors.Is(err, context.DeadlineExceeded) || size > 0 {
		t.Fatalf("timed-out DirSize=%d, %v", size, err)
	}
}
