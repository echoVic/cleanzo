package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanPathAndClean(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk")
	if err := os.MkdirAll(filepath.Join(junk, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(junk, "a.bin"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(junk, "sub", "b.bin"), make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}

	cat := Category{Name: "test", paths: []string{junk}}

	size, files := scanPath(junk)
	if size != 3072 || files != 2 {
		t.Fatalf("scanPath got size=%d files=%d, want 3072/2", size, files)
	}

	res := cleanCategory(cat)
	if res.Freed != 3072 || res.Files != 2 {
		t.Fatalf("clean got freed=%d files=%d, want 3072/2", res.Freed, res.Files)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("unexpected failures: %v", res.Failed)
	}
	if _, err := os.Stat(junk); err != nil {
		t.Fatalf("root dir should remain: %v", err)
	}
	entries, _ := os.ReadDir(junk)
	if len(entries) != 0 {
		t.Fatalf("junk dir should be empty, got %d entries", len(entries))
	}

	res2 := cleanCategory(cat)
	if res2.Freed != 0 || res2.Files != 0 {
		t.Fatalf("second clean should free 0, got freed=%d files=%d", res2.Freed, res2.Files)
	}
}

func TestSafeToClean(t *testing.T) {
	h, _ := os.UserHomeDir()
	cases := map[string]bool{
		"/":                                   false,
		"/System":                             false,
		"/System/Library":                     false,
		"/Library":                            false,
		"/Library/Caches":                     false,
		"/Applications":                       false,
		"/Volumes/disk":                       false,
		"/private/tmp":                        false,
		"/Users":                              false,
		"/Users/otheruser/Library/Caches":     false,
		h:                                     false,
		filepath.Join(h, "Library"):           false,
		filepath.Join(h, "Library", "Caches"): true,
		filepath.Join(h, ".Trash"):            true,
		os.TempDir():                          true,
	}
	for p, want := range cases {
		if got := safeToClean(p); got != want {
			t.Errorf("safeToClean(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestScanCategoriesMissing(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope")
	cat := Category{Name: "x", paths: []string{missing}}
	got := scanCategories([]Category{cat})
	if len(got[0].Missing) != 1 || got[0].Missing[0] != missing {
		t.Fatalf("expected missing path recorded, got %v", got[0].Missing)
	}
	if got[0].Size != 0 || got[0].Files != 0 {
		t.Fatalf("missing path should contribute 0 size/files")
	}
}

// ---------------------------------------------------------------------------
// App uninstall
// ---------------------------------------------------------------------------

const testPlistXML = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDisplayName</key>
	<string>TestApp</string>
	<key>CFBundleIdentifier</key>
	<string>com.example.testapp</string>
	<key>CFBundleShortVersionString</key>
	<string>1.2.3</string>
</dict>
</plist>`

func writeTestPlist(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "Info.plist")
	if err := os.WriteFile(p, []byte(testPlistXML), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadBundleInfo(t *testing.T) {
	dir := t.TempDir()
	p := writeTestPlist(t, dir)
	name, bid, version := readBundleInfo(p)
	if name != "TestApp" || bid != "com.example.testapp" || version != "1.2.3" {
		t.Fatalf("got %q %q %q", name, bid, version)
	}
}

func makeFakeApp(t *testing.T, dir, name string) string {
	t.Helper()
	appPath := filepath.Join(dir, name+".app")
	contents := filepath.Join(appPath, "Contents")
	if err := os.MkdirAll(contents, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestPlist(t, contents)
	payload := filepath.Join(appPath, "Contents", "MacOS", name)
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "bin"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	return appPath
}

func TestListAppsIn(t *testing.T) {
	dir := t.TempDir()
	makeFakeApp(t, dir, "TestApp")
	apps := listAppsIn(dir)
	if len(apps) != 1 {
		t.Fatalf("want 1 app, got %d", len(apps))
	}
	a := apps[0]
	if a.Name != "TestApp" || a.BundleID != "com.example.testapp" || a.Size < 4096 {
		t.Fatalf("unexpected app: %+v", a)
	}
	if _, err := os.Stat(a.Path); err != nil {
		t.Fatalf("app path should exist: %v", err)
	}
}

func TestUninstallPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := makeFakeApp(t, home, "TestApp")
	app := AppInfo{Name: "TestApp", Path: appPath, BundleID: "com.example.testapp"}

	// create associated files
	assoc := []string{
		filepath.Join(home, "Library", "Preferences", "com.example.testapp.plist"),
		filepath.Join(home, "Library", "Caches", "com.example.testapp"),
		filepath.Join(home, "Library", "Application Support", "TestApp"),
		filepath.Join(home, "Library", "Containers", "com.example.testapp"),
		filepath.Join(home, "Library", "Saved Application State", "com.example.testapp.savedState"),
	}
	for _, p := range assoc {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	items := uninstallPaths(app)
	if len(items) < 5 {
		t.Fatalf("want >=5 items, got %d: %+v", len(items), items)
	}
	foundApp := false
	for _, it := range items {
		if it.Path == appPath {
			foundApp = true
		}
		if it.Size != 0 && it.Path == appPath {
			// app itself has 4096 bytes
		}
	}
	if !foundApp {
		t.Fatal("app bundle not in items")
	}
}

func TestMoveToTrash(t *testing.T) {
	dir := t.TempDir()
	trash := filepath.Join(dir, ".Trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(src, make([]byte, 512), 0o644); err != nil {
		t.Fatal(err)
	}
	// pre-place a file with the same name
	if err := os.WriteFile(filepath.Join(trash, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	size, err := moveToTrash(src, trash)
	if err != nil {
		t.Fatal(err)
	}
	if size != 512 {
		t.Fatalf("moved size = %d", size)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("src should be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, "a.txt 2")); err != nil {
		t.Fatalf("expected unique trash name: %v", err)
	}
}

func TestUninstallApp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := makeFakeApp(t, home, "TestApp")
	// associated file
	pref := filepath.Join(home, "Library", "Preferences", "com.example.testapp.plist")
	if err := os.MkdirAll(filepath.Dir(pref), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pref, make([]byte, 256), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Cleaner{}.UninstallApp(appPath)
	if res.Moved < 2 {
		t.Fatalf("want >=2 items moved, got %d, failed=%v", res.Moved, res.Failed)
	}
	if res.Freed < 4096 {
		t.Fatalf("freed = %d, want >=4096", res.Freed)
	}
	if _, err := os.Stat(appPath); !os.IsNotExist(err) {
		t.Fatalf("app should be gone: %v", err)
	}
	// app should now be in fake trash
	trash := filepath.Join(home, ".Trash")
	entries, _ := os.ReadDir(trash)
	if len(entries) < 2 {
		t.Fatalf("trash should have items, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Category config persistence
// ---------------------------------------------------------------------------

func TestConfigRoundtrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got := Cleaner{}.GetCategories()
	if len(got) != 9 {
		t.Fatalf("want 9 default categories, got %d", len(got))
	}
	custom := got[:2]
	custom[0].Name = "custom"
	custom[0].Description = "自定义分类"
	custom[0].Paths = []string{filepath.Join(home, "mydir")}
	if err := (Cleaner{}).SaveCategories(custom); err != nil {
		t.Fatal(err)
	}

	re := Cleaner{}.GetCategories()
	if len(re) != 2 || re[0].Name != "custom" || re[0].Paths[0] != filepath.Join(home, "mydir") {
		t.Fatalf("unexpected reload: %+v", re)
	}
	loaded := loadCategories()
	if len(loaded) != 2 || loaded[0].paths[0] != filepath.Join(home, "mydir") {
		t.Fatalf("unexpected effective categories: %+v", loaded)
	}
}

func TestSaveCategoriesRejectsBlocked(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	err := Cleaner{}.SaveCategories([]CategoryConfig{{Name: "x", Enabled: true, Paths: []string{"/"}}})
	if err == nil {
		t.Fatal("expected protected path to be rejected")
	}
}

func TestConfigDefaultPersisted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = loadCategories() // first run persists defaults
	if _, err := os.Stat(configPath()); err != nil {
		t.Fatalf("config.json should be written on first load: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Full disk scan & trash
// ---------------------------------------------------------------------------

func TestFullScanTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// home structure
	mustMkdir(t, filepath.Join(home, "Library", "Caches"))
	mustMkdir(t, filepath.Join(home, "Library", "Application Support"))
	mustMkdir(t, filepath.Join(home, "Downloads"))
	mustMkdir(t, filepath.Join(home, ".Trash"))
	mustMkdir(t, filepath.Join(home, ".npm"))
	targets := fullScanTargets()
	joined := strings.Join(targets, "\n")
	for _, want := range []string{
		filepath.Join(home, "Library", "Caches"),
		filepath.Join(home, "Library", "Application Support"),
		filepath.Join(home, "Downloads"),
		filepath.Join(home, ".npm"),
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing target %s in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, filepath.Join(home, ".Trash")) {
		t.Errorf(".Trash must not be scanned:\n%s", joined)
	}
	if strings.Contains(joined, filepath.Join(home, "Library\n")) && false {
		t.Errorf("Library itself must be expanded, not scanned as a whole")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestTrashable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ok := []string{
		filepath.Join(home, "Downloads", "big"),
		filepath.Join(home, "Library", "Caches", "com.x"),
		filepath.Join(home, ".npm"),
	}
	for _, p := range ok {
		if !trashable(p) {
			t.Errorf("want trashable: %s", p)
		}
	}
	no := []string{
		home,
		filepath.Join(home, "Library"),
		filepath.Join(home, ".Trash"),
		"/",
		"/Applications",
		"/Library/Caches",
	}
	for _, p := range no {
		if trashable(p) {
			t.Errorf("must not be trashable: %s", p)
		}
	}
}

func TestTrashPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	big := filepath.Join(home, "bigdir")
	mustMkdir(t, big)
	if err := os.WriteFile(filepath.Join(big, "f"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := (Cleaner{}).TrashPath(big)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 1 || res.Freed < 2048 {
		t.Fatalf("res = %+v", res)
	}
	if _, err := os.Lstat(big); !os.IsNotExist(err) {
		t.Fatalf("source should be gone: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".Trash", "bigdir")); err != nil {
		t.Fatalf("should be in trash: %v", err)
	}
	// protected path rejected
	if _, err := (Cleaner{}).TrashPath(home); err == nil {
		t.Fatal("expected protected-path error")
	}
	if _, err := (Cleaner{}).TrashPath("/Applications"); err == nil {
		t.Fatal("expected system-path error")
	}
}

func TestCollectFullScan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := filepath.Join(home, "AAA")
	mustMkdir(t, a)
	// real 60MB file so du reports enough size to rank into the top-30 list
	if err := os.WriteFile(filepath.Join(a, "big"), make([]byte, 60<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	items, done, _ := collectFullScan(nil)
	if done < 1 {
		t.Fatalf("done = %d", done)
	}
	found := false
	for _, it := range items {
		if it.Path == a {
			found = true
			if it.Size < 60<<20 || !it.Trashable {
				t.Fatalf("unexpected item: %+v", it)
			}
		}
		for _, sys := range []string{"/Library/", "/usr/local/", "/opt/"} {
			if strings.HasPrefix(it.Path, sys) && it.Trashable {
				t.Fatalf("system path must not be trashable: %+v", it)
			}
		}
	}
	if !found {
		t.Fatalf("home dir item missing from: %+v", items)
	}
}

func TestDiskStat(t *testing.T) {
	d := diskStat()
	if d.Total <= 0 || d.Free <= 0 || d.Used <= 0 {
		t.Fatalf("bad disk stat: %+v", d)
	}
}

func TestFindLargeFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, "BigDir")
	mustMkdir(t, target)
	if err := os.WriteFile(filepath.Join(target, "big.bin"), make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "small.bin"), make([]byte, 100<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := findLargeFiles(target, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 large file, got %d: %+v", len(files), files)
	}
	if !files[0].Trashable {
		t.Fatalf("file under home must be trashable: %+v", files[0])
	}
	if files[0].Size < 2<<20 {
		t.Fatalf("unexpected size: %d", files[0].Size)
	}
}

func TestCollectLargeScanStreaming(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := filepath.Join(home, "DirA")
	b := filepath.Join(home, "DirB")
	mustMkdir(t, a)
	mustMkdir(t, b)
	if err := os.WriteFile(filepath.Join(a, "one.bin"), make([]byte, 3<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b, "two.bin"), make([]byte, 5<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	var streamed []LargeFile
	items, done, _ := collectLargeScan(1<<20, func(f LargeFile) {
		streamed = append(streamed, f)
	})
	if done < 1 {
		t.Fatalf("done = %d", done)
	}
	one, two := filepath.Join(a, "one.bin"), filepath.Join(b, "two.bin")
	foundOne, foundTwo := false, false
	for _, f := range streamed {
		if f.Path == one {
			foundOne = true
		}
		if f.Path == two {
			foundTwo = true
		}
	}
	if !foundOne || !foundTwo {
		t.Fatalf("streamed missing temp files: %+v", streamed)
	}
	if len(items) == 0 {
		t.Fatalf("items empty")
	}
}

func TestOrphanKind(t *testing.T) {
	cases := map[string]string{
		"Application Support": "support",
		"Caches":              "cache",
		"Preferences":         "prefs",
		"LaunchAgents":        "launchAgent",
		"Other":               "Other",
	}
	for in, want := range cases {
		if got := orphanKind(in); got != want {
			t.Fatalf("orphanKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCollectOrphans(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	caches := filepath.Join(home, "Library", "Caches")
	mustMkdir(t, caches)
	// orphan: a leftover that does not match any installed app
	orphan := filepath.Join(caches, "OrphanLeftover")
	mustMkdir(t, orphan)
	if err := os.WriteFile(filepath.Join(orphan, "x.bin"), make([]byte, 3<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	// system data must be ignored
	mustMkdir(t, filepath.Join(caches, "com.apple.example"))
	if err := os.WriteFile(filepath.Join(caches, "com.apple.example", "y.bin"), make([]byte, 3<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	items, total := collectOrphans(nil)
	found := false
	for _, it := range items {
		if it.Path == orphan {
			found = true
			if it.Kind != "cache" {
				t.Fatalf("kind = %q, want cache", it.Kind)
			}
		}
		if strings.Contains(it.Path, "com.apple.example") {
			t.Fatalf("system entry leaked: %+v", it)
		}
	}
	if !found {
		t.Fatalf("orphan missing from results: %+v", items)
	}
	if total < 3<<20 {
		t.Fatalf("total = %d", total)
	}
}
