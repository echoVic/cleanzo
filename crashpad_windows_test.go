package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestCrashDumpsFindBrowsersInLocalAppData: the status page measures the
// Crashpad databases Windows browsers keep under LOCALAPPDATA.
func TestCrashDumpsFindBrowsersInLocalAppData(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	chrome := filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "User Data", "Crashpad")
	writeFile(t, filepath.Join(chrome, "settings.dat"), 40)
	writeFile(t, filepath.Join(chrome, "pending", "d.dmp"), 64*1024)
	sum, err := Cleaner{}.CrashDumps(context.Background())
	if err != nil || len(sum.Dirs) != 1 || sum.Dirs[0].App != "Chrome" || sum.Dirs[0].Size < 64*1024 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
}
