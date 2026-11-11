package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCloudStorageIsProtected: cloud providers keep their files in
// ~/Library/CloudStorage, online-only placeholders among them. Moving one to
// the Trash frees nothing on this Mac and deletes it from the cloud.
func TestCloudStorageIsProtected(t *testing.T) {
	env := testEnv(t)
	drive := filepath.Join(env.home, "Library", "CloudStorage", "OneDrive-Personal")
	file := filepath.Join(drive, "Videos", "big.mov")
	writeFile(t, file, 100)
	for _, p := range []string{drive, file, filepath.Join(env.home, "library", "cloudstorage")} {
		if safeToClean(p) || removable(p) {
			t.Errorf("%s is offered for cleaning", p)
		}
	}
	env.settings.Custom = []CustomFolder{{Name: "drive", Path: drive, Enabled: true}}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + drive}}, time.Time{}, cleanHooks{})
	if !exists(file) || len(res) != 1 || res[0].Trashed != 0 {
		t.Fatalf("a cloud folder was cleaned: %+v", res)
	}
}

// TestTempRootMustBeATempFolder: the temporary rule permanently removes
// what its root holds, so a root that is the disk, the home folder or a
// folder of the user's data offers nothing. Started from a shell with
// TMPDIR=~/Downloads, the rule would otherwise list week-old downloads.
func TestTempRootMustBeATempFolder(t *testing.T) {
	env := testEnv(t)
	for _, dir := range []string{"Downloads", "Documents/old", "Library/Caches/tmp", ".ssh", "Library/CloudStorage/Drive"} {
		mustMkdir(t, filepath.Join(env.home, dir))
	}
	if err := os.Symlink(filepath.Join(env.home, "Downloads"), filepath.Join(env.home, "dl")); err != nil {
		t.Fatal(err)
	}
	refused := []string{
		"/", env.home, env.home + "/",
		filepath.Join(env.home, "Downloads"), filepath.Join(env.home, "downloads"),
		filepath.Join(env.home, "dl"), // a link is judged by where it points
		filepath.Join(env.home, "Documents", "old"),
		filepath.Join(env.home, "Library"), filepath.Join(env.home, "Library", "Caches", "tmp"),
		filepath.Join(env.home, ".ssh"), filepath.Join(env.home, "Library", "CloudStorage", "Drive"),
	}
	for _, tmp := range refused {
		if allowedTemp(env.home, tmp) {
			t.Errorf("temp root %s accepted", tmp)
		}
	}
	for _, tmp := range []string{env.tmp, t.TempDir(), os.TempDir()} {
		if !allowedTemp(env.home, tmp) {
			t.Errorf("temp root %s refused", tmp)
		}
	}

	// The rule offers nothing from a refused root.
	old := filepath.Join(env.home, "Downloads", "report.pdf")
	writeFile(t, old, 100)
	week := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(old, week, week); err != nil {
		t.Fatal(err)
	}
	saved := now
	now = func() time.Time { return time.Now().Add(30 * 24 * time.Hour) }
	defer func() { now = saved }()
	env.tmp = filepath.Join(env.home, "Downloads")
	specs, _ := expandItems(&env)
	for _, s := range specs {
		if s.item.ID == "sys.temp" {
			t.Fatalf("temp rule listed %v", s.cands)
		}
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "sys.temp"}}, time.Time{}, cleanHooks{})
	if !exists(old) || len(res) != 1 || res[0].Freed != 0 {
		t.Fatalf("a download was removed as temporary: %+v", res)
	}
}

// TestTempRootOutsideTheHomeFolderIsJudgedToo: with TMPDIR=/, every path
// outside the home folder would count as temporary.
func TestTempRootOutsideTheHomeFolderIsJudgedToo(t *testing.T) {
	setHome(t, t.TempDir())
	outside := filepath.Join(t.TempDir(), "keep")
	writeFile(t, outside, 10)
	t.Setenv("TMPDIR", "/")
	if safeToClean(outside) || safeToClean("/Applications") {
		t.Fatal("TMPDIR=/ made paths outside the home folder cleanable")
	}
}
