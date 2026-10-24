package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMeasureGrowthEntryNeverFollowsLinks(t *testing.T) {
	root, big := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(big, "huge"), 50000)
	writeFile(t, filepath.Join(root, "small"), 10)
	if err := os.Symlink(big, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if e := measureGrowthEntry(context.Background(), growthTarget{path: root, kind: growthHome}); e.Size != 10 {
		t.Fatalf("size %d: the link was followed", e.Size)
	}
}

func TestMeasureGrowthEntryMarksUnreadableParts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok"), 10)
	locked := filepath.Join(root, "locked")
	writeFile(t, filepath.Join(locked, "secret"), 10)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	e := measureGrowthEntry(context.Background(), growthTarget{path: root, kind: growthHome})
	if !e.Denied || !e.Complete || e.Size != 10 {
		t.Fatalf("entry = %+v", e)
	}
}

func TestBackgroundThreadReturns(t *testing.T) {
	done := make(chan struct{})
	go func() {
		backgroundThread()
		close(done)
	}()
	<-done
}
