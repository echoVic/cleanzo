package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReclaimTallySumsPlainFiles counts files that share nothing in full.
func TestReclaimTallySumsPlainFiles(t *testing.T) {
	dir := t.TempDir()
	var sum reclaimTally
	for name, size := range map[string]int{"a": 700, "b": 1300, "empty": 0} {
		p := filepath.Join(dir, name)
		writeFile(t, p, size)
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		sum.add(p, info)
	}
	if sum.size != 2000 {
		t.Fatalf("size = %d, want 2000", sum.size)
	}
}
