package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWindowsTraversalDifferentProfileCase keeps every component guard active under a differently cased USERPROFILE.
func TestWindowsTraversalDifferentProfileCase(t *testing.T) {
	env := testEnv(t)
	onDisk := env.home
	env.home = strings.ToUpper(onDisk)
	setHome(t, env.home)
	cache := filepath.Join(onDisk, "cache")
	writeFile(t, filepath.Join(cache, "ordinary", "data"), 123)
	checked := false
	env.lstat = func(p string) (fs.FileInfo, error) {
		if strings.EqualFold(p, filepath.Join(cache, "ordinary")) {
			checked = true
			return nil, fs.ErrPermission
		}
		return os.Lstat(p)
	}
	if !env.traversalBlocked(filepath.Join(cache, "ordinary", "data")) || !checked {
		t.Fatal("case mismatch bypassed per-component checks")
	}
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "keep"), 123)
	link := filepath.Join(cache, "escape")
	makeJunction(t, link, outside)
	if !env.traversalBlocked(filepath.Join(link, "keep")) {
		t.Fatal("junction accepted under differently cased USERPROFILE")
	}
	if _, err := (Cleaner{}).TrashPath(link); !errors.Is(err, errBlocked) {
		t.Fatalf("junction removal=%v", err)
	}
}
