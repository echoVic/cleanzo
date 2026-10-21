package main

import (
	"context"
	"path/filepath"
	"testing"
)

func TestWindowsDirSizeExactSizeAndJunctions(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	root := filepath.Join(home, "Documents", "tree")
	writeFile(t, filepath.Join(root, "a.bin"), 123)
	writeFile(t, filepath.Join(root, "nested", "b.bin"), 456)
	writeFile(t, filepath.Join(root, "nested", "c.bin"), 789)
	if size, err := (Cleaner{}).DirSize(context.Background(), root); err != nil || size != 1368 {
		t.Fatalf("DirSize=%d, %v", size, err)
	}
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "outside.bin"), 10000)
	link := filepath.Join(root, "escape")
	makeJunction(t, link, outside)
	if size, err := (Cleaner{}).DirSize(context.Background(), root); err != nil || size != 1368 {
		t.Fatalf("DirSize with junction=%d, %v", size, err)
	}
	if _, err := (Cleaner{}).DirSize(context.Background(), link); err == nil {
		t.Fatal("followed explicitly selected junction")
	}
}
