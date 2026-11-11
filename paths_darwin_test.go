package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

// TestTempRootIsOneOfTheSystems: the temporary rule permanently removes
// week-old entries, so its root must be one of the system's temporary
// folders. Started from a shell with TMPDIR=~/Projects, it would otherwise
// offer the projects that were not opened this week.
func TestTempRootIsOneOfTheSystems(t *testing.T) {
	env := testEnv(t)
	// A home that is not under the system's temporary folders, as a real
	// one is; it is only resolved, never read.
	home := "/Users/cleanzo-test-nobody"
	links := t.TempDir()
	for name, target := range map[string]string{"apps": "/Applications", "users": "/Users"} {
		if err := os.Symlink(target, filepath.Join(links, name)); err != nil {
			t.Fatal(err)
		}
	}
	refused := []string{
		"/Users", "/Applications", "/Library", "/usr/local", "/private", "/private/var", "/private/var/folders",
		filepath.Join(home, "Projects"), filepath.Join(home, "tmp"), filepath.Join(home, "Library", "Developer"),
		filepath.Join(links, "apps"), filepath.Join(links, "users", "Shared"), // a link is judged by where it points
	}
	for _, tmp := range refused {
		if allowedTemp(home, tmp) || allowedTemp(env.home, tmp) {
			t.Errorf("temp root %s accepted", tmp)
		}
	}
	for _, tmp := range []string{"/tmp", "/private/tmp", "/tmp/build", "/var/tmp", "/private/var/tmp/x", env.tmp, os.TempDir()} {
		if !allowedTemp(home, tmp) {
			t.Errorf("temp root %s refused", tmp)
		}
	}

	// A refused root hides the temporary rule and nothing else. The test's
	// temporary folders are all made before TMPDIR points at /Applications.
	writeFile(t, filepath.Join(env.home, "Library", "Logs", "app.log"), 100)
	t.Setenv("TMPDIR", "/Applications")
	if safeToClean("/Applications/Safari.app") || safeToClean("/Applications/Cleanzo-test-missing.app") {
		t.Error("TMPDIR=/Applications made apps cleanable")
	}
	env.tmp = "/Applications"
	specs, _ := expandItems(&env)
	ids := itemIDs(specs)
	if slices.Contains(ids, "sys.temp") || !slices.Contains(ids, "sys.logs") {
		t.Fatalf("items from a refused temp root: %v", ids)
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
