package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// TestWindowsRecycleSettingsDefaults exercises missing defaults and rejects unreadable settings in CI.
func TestWindowsRecycleSettingsDefaults(t *testing.T) {
	saved := testRecycleValue
	t.Cleanup(func() { testRecycleValue = saved })
	if os.Getenv("CLEANZO_TEST_BIN_UNKNOWN") == "1" {
		testRecycleValue = func(string, string) (uint64, error) { return 0, windows.ERROR_ACCESS_DENIED }
		requireTestRecycleCapacity(t, homeDir(), 16384)
		t.Fatal("unreadable settings were accepted")
	}
	t.Setenv("GITHUB_ACTIONS", "true")
	testRecycleValue = func(string, string) (uint64, error) { return 0, registry.ErrNotExist }
	requireTestRecycleCapacity(t, homeDir(), 16384)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.v", "-test.run=^TestWindowsRecycleSettingsDefaults$")
	cmd.Env = append(os.Environ(), "CLEANZO_TEST_BIN_UNKNOWN=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "cannot read NukeOnDelete") || strings.Contains(string(out), "--- SKIP:") {
		t.Fatalf("CI settings failure did not fail: %v %s", err, out)
	}
}

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
