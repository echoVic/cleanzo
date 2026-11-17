package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// cloneFile makes dst an APFS clone of src, sharing its blocks. The test is
// skipped on a volume that cannot clone.
func cloneFile(t *testing.T, src, dst string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(dst))
	if err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW); err != nil {
		t.Skipf("this volume cannot clone files: %v", err)
	}
}

// tallyOf adds the given files to one tally and returns its size.
func tallyOf(t *testing.T, paths ...string) int64 {
	t.Helper()
	var sum reclaimTally
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		sum.add(p, info)
	}
	return sum.size
}

// TestCloneRefs reads the clone count macOS keeps for a file.
func TestCloneRefs(t *testing.T) {
	dir := t.TempDir()
	a, b, plain := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "plain")
	writeFile(t, a, 2000)
	writeFile(t, plain, 2000)
	if _, refs, ok := cloneRefs(plain); !ok || refs != 1 {
		t.Fatalf("plain: refs = %d, ok = %v", refs, ok)
	}
	cloneFile(t, a, b)
	ida, ra, oka := cloneRefs(a)
	idb, rb, okb := cloneRefs(b)
	if !oka || !okb || ra != 2 || rb != 2 || ida == 0 || ida != idb {
		t.Fatalf("a = %d/%d/%v, b = %d/%d/%v", ida, ra, oka, idb, rb, okb)
	}
	if idp, _, _ := cloneRefs(plain); idp == ida {
		t.Fatalf("plain shares clone id %d", idp)
	}
	if _, _, ok := cloneRefs(filepath.Join(dir, "missing")); ok {
		t.Fatal("a missing file reported clone data")
	}
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	if _, refs, ok := cloneRefs(a); !ok || refs != 1 {
		t.Fatalf("after removing the clone: refs = %d, ok = %v", refs, ok)
	}
}

// TestReclaimTallyCountsClonesOnce counts shared blocks once, and only when
// every file sharing them is in the tally.
func TestReclaimTallyCountsClonesOnce(t *testing.T) {
	dir := t.TempDir()
	kept := filepath.Join(dir, "kept")
	b, c, plain := filepath.Join(dir, "in/b"), filepath.Join(dir, "in/c"), filepath.Join(dir, "in/plain")
	writeFile(t, kept, 3000)
	writeFile(t, plain, 700)
	cloneFile(t, kept, b)
	cloneFile(t, kept, c)
	if got := tallyOf(t, b, c, plain); got != 700 {
		t.Fatalf("clones of a file kept elsewhere = %d, want 700", got)
	}
	if got := tallyOf(t, kept, b, c, plain); got != 3700 {
		t.Fatalf("the whole family = %d, want 3700", got)
	}
}
