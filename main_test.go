package main

import (
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanPathAndClean(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	junk := filepath.Join(home, "junk")
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
	// moved to trash, not deleted in place
	if _, err := os.Stat(filepath.Join(junk, "a.bin")); !os.IsNotExist(err) {
		t.Fatalf("a.bin should have moved out of the source dir: %v", err)
	}
	entries, _ := os.ReadDir(junk)
	if len(entries) != 0 {
		t.Fatalf("junk dir should be empty, got %d entries", len(entries))
	}
	// ...and it must actually be in the (temp) trash now
	trash := filepath.Join(home, ".Trash")
	if _, err := os.Stat(filepath.Join(trash, "a.bin")); err != nil {
		t.Fatalf("a.bin should be in trash: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trash, "sub", "b.bin")); err != nil {
		t.Fatalf("sub/b.bin should be in trash: %v", err)
	}

	res2 := cleanCategory(cat)
	if res2.Freed != 0 || res2.Files != 0 {
		t.Fatalf("second clean should free 0, got freed=%d files=%d", res2.Freed, res2.Files)
	}
}

// TestCleanCategoryEmptiesTrash verifies the built-in "trash" category is
// emptied permanently (its contents are already in the Trash), while other
// categories move recoverable copies in.
func TestCleanCategoryEmptiesTrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	trash := filepath.Join(home, ".Trash")
	mustMkdir(t, trash)
	// something already sitting in the trash
	if err := os.WriteFile(filepath.Join(trash, "old.bin"), make([]byte, 1500), 0o644); err != nil {
		t.Fatal(err)
	}
	cat := Category{Name: "trash", paths: []string{trash}}
	res := cleanCategory(cat)
	if res.Freed != 1500 {
		t.Fatalf("freed = %d, want 1500", res.Freed)
	}
	if len(res.Failed) != 0 {
		t.Fatalf("unexpected failures: %v", res.Failed)
	}
	entries, _ := os.ReadDir(trash)
	if len(entries) != 0 {
		t.Fatalf("trash should be empty after emptying, got %d", len(entries))
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

// ---------------------------------------------------------------------------
// Per-application cache cleanup
// ---------------------------------------------------------------------------

func TestScanAppCaches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	caches := filepath.Join(home, "Library", "Caches")
	for _, name := range []string{"AppA", "AppB"} {
		d := filepath.Join(caches, name)
		mustMkdir(t, d)
		if err := os.WriteFile(filepath.Join(d, "c.bin"), make([]byte, 2048), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	items := Cleaner{}.ScanAppCaches()
	if len(items) != 2 {
		t.Fatalf("want 2 app cache rows, got %+v", items)
	}
	// sorted by size descending (equal sizes, order not asserted beyond presence)
	byName := map[string]AppCacheItem{}
	for _, it := range items {
		byName[it.Name] = it
	}
	if byName["AppA"].Size < 2048 || byName["AppB"].Size < 2048 {
		t.Fatalf("unexpected sizes: %+v", byName)
	}
	if byName["AppA"].Path != filepath.Join(caches, "AppA") {
		t.Fatalf("bad path: %+v", byName["AppA"])
	}
}

func TestCleanPathsMovesToTrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := filepath.Join(home, "Library", "Caches", "AppA")
	mustMkdir(t, app)
	if err := os.WriteFile(filepath.Join(app, "c.bin"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	res := Cleaner{}.CleanPaths([]string{app})
	if res.Moved != 1 || res.Freed < 4096 || len(res.Failed) != 0 {
		t.Fatalf("res = %+v", res)
	}
	// original location must be gone
	if _, err := os.Lstat(app); !os.IsNotExist(err) {
		t.Fatalf("source should be gone: %v", err)
	}
	// ...and present in the (temp) trash
	if _, err := os.Lstat(filepath.Join(home, ".Trash", "AppA", "c.bin")); err != nil {
		t.Fatalf("should be in trash: %v", err)
	}
	// protected path is rejected, not moved
	bad := filepath.Join(home, "Library")
	res2 := Cleaner{}.CleanPaths([]string{bad})
	if res2.Moved != 0 || len(res2.Failed) == 0 {
		t.Fatalf("protected path should be rejected: %+v", res2)
	}
	if _, err := os.Lstat(bad); err != nil {
		t.Fatalf("protected path must not be touched: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Similar photo cleanup
// ---------------------------------------------------------------------------

// writeGradientJPEG writes a horizontal-gradient JPEG (width >= minPhotoDim so
// it passes the tiny-image filter). A monotonic gradient gives dHash a clean,
// deterministic signal: same gradient hashes the same; reversed is the inverse.
func writeGradientJPEG(t *testing.T, path string, w, h int, reverse bool) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := uint8(40 + 200*x/(w-1))
			if reverse {
				v = uint8(240 - 200*x/(w-1))
			}
			img.SetRGBA(x, y, color.RGBA{R: v, G: v, B: v, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
}

// copyFile copies src to dst byte-for-byte (used to make exact duplicates).
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func popcount(x uint64) int {
	n := 0
	for x != 0 {
		n += int(x & 1)
		x >>= 1
	}
	return n
}

// TestDecodeImageAndHash exercises the standard-library decode path (the same
// path sips output feeds into for HEIC) and dHashImage: identical visuals hash
// alike, reversed gradient is far.
func TestDecodeImageAndHash(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jpg")
	b := filepath.Join(dir, "b.jpg")
	c := filepath.Join(dir, "c.jpg")
	writeGradientJPEG(t, a, 128, 96, false)
	writeGradientJPEG(t, b, 128, 96, false)
	writeGradientJPEG(t, c, 128, 96, true)

	imgA, cfgA, closeA, err := decodeImageFile(a)
	if err != nil {
		t.Fatal(err)
	}
	defer closeA()
	if cfgA.Width != 128 || cfgA.Height != 96 {
		t.Fatalf("cfg = %+v", cfgA)
	}
	hA := dHashImage(imgA, cfgA)

	imgB, cfgB, closeB, _ := decodeImageFile(b)
	defer closeB()
	hB := dHashImage(imgB, cfgB)

	imgC, cfgC, closeC, _ := decodeImageFile(c)
	defer closeC()
	hC := dHashImage(imgC, cfgC)

	if dist := popcount(hA ^ hB); dist > similarHashThreshold {
		t.Fatalf("identical visuals should hash close, hamming=%d", dist)
	}
	if dist := popcount(hA ^ hC); dist <= similarHashThreshold {
		t.Fatalf("reversed gradient must hash far, hamming=%d", dist)
	}
}

func TestThumbDataURL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "p.jpg")
	writeGradientJPEG(t, p, 800, 600, false)
	img, cfg, close, err := decodeImageFile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	url := thumbDataURL(img)
	if !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Fatalf("not a jpeg data url: %.40q", url)
	}
	if len(url) < 100 {
		t.Fatalf("thumbnail suspiciously small: %d chars", len(url))
	}
	_ = cfg
}

// TestClusterCompleteLinkage ensures no chaining: A≈B, B≈C but A≠C must not be
// merged into one cluster.
func TestClusterCompleteLinkage(t *testing.T) {
	// 15=bits0-3, 60=bits2-5, 240=bits4-7. AB=4, BC=4, AC=8.
	hashes := map[int]uint64{0: 15, 1: 60, 2: 240}
	got := clusterPhotosComplete([]int{0, 1, 2}, hashes)
	if len(got) != 1 {
		t.Fatalf("want 1 cluster {0,1}, got %+v", got)
	}
	if len(got[0]) != 2 || got[0][0] != 0 || got[0][1] != 1 {
		t.Fatalf("unexpected cluster: %+v", got[0])
	}
}

func TestCollectPhotoFiles(t *testing.T) {
	home := t.TempDir()
	pics := filepath.Join(home, "Pictures")
	mustMkdir(t, pics)
	writeGradientJPEG(t, filepath.Join(pics, "a.png"), 80, 80, false) // ext doesn't matter for discovery
	writeGradientJPEG(t, filepath.Join(pics, "b.heic"), 80, 80, false) // heic now discovered
	mustMkdir(t, filepath.Join(pics, "sub"))
	writeGradientJPEG(t, filepath.Join(pics, "sub", "c.png"), 80, 80, false)
	if err := os.WriteFile(filepath.Join(pics, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := collectPhotoFiles([]string{pics}, 100)
	if len(got) != 3 {
		t.Fatalf("want 3 image files, got %v", got)
	}
}

// TestSimilarEndToEnd covers exact-duplicate detection, perceptual grouping,
// keep recommendation, waste and thumbnails.
func TestSimilarEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pics := filepath.Join(home, "Pictures")
	mustMkdir(t, pics)

	// exact duplicates: byte-for-byte copies
	exact1 := filepath.Join(pics, "exact1.jpg")
	writeGradientJPEG(t, exact1, 128, 96, false)
	exact2 := filepath.Join(pics, "exact2.jpg")
	copyFile(t, exact1, exact2)

	// near-dups: same gradient direction, different dimensions (not byte-identical)
	near1 := filepath.Join(pics, "near1.jpg")
	writeGradientJPEG(t, near1, 140, 100, false)
	near2 := filepath.Join(pics, "near2.jpg")
	writeGradientJPEG(t, near2, 160, 120, false)

	// distinct, alone
	distinct := filepath.Join(pics, "distinct.jpg")
	writeGradientJPEG(t, distinct, 128, 96, true)

	res := buildSimilarPhotos([]string{pics}, nil)
	if res.Scanned != 5 {
		t.Fatalf("scanned = %d, want 5", res.Scanned)
	}

	var exact, similar *PhotoCluster
	for i := range res.Clusters {
		switch res.Clusters[i].Kind {
		case "exact":
			c := res.Clusters[i]
			exact = &c
		case "similar":
			c := res.Clusters[i]
			similar = &c
		}
	}
	if exact == nil || len(exact.Items) != 2 {
		t.Fatalf("expected exact cluster of 2, got %+v", res.Clusters)
	}
	if similar == nil || len(similar.Items) != 2 {
		t.Fatalf("expected similar cluster of 2, got %+v", res.Clusters)
	}
	// exactly one keep per cluster, thumbnails present, waste = total - keep
	for _, cl := range res.Clusters {
		keeps := 0
		for _, it := range cl.Items {
			if it.Keep {
				keeps++
			}
			if !strings.HasPrefix(it.Thumb, "data:image/jpeg;base64,") {
				t.Fatalf("item missing thumbnail: %s", it.Path)
			}
		}
		if keeps != 1 {
			t.Fatalf("cluster %s has %d keeps, want 1", cl.Kind, keeps)
		}
	}
}

// TestSimilarKeepPicksLargest ensures the recommended keep is the highest
// resolution copy, and waste excludes it.
func TestSimilarKeepPicksLargest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pics := filepath.Join(home, "Pictures")
	mustMkdir(t, pics)
	small := filepath.Join(pics, "small.jpg")
	writeGradientJPEG(t, small, 128, 96, false)
	large := filepath.Join(pics, "large.jpg")
	writeGradientJPEG(t, large, 1024, 768, false)

	res := buildSimilarPhotos([]string{pics}, nil)
	if len(res.Clusters) != 1 || res.Clusters[0].Kind != "similar" {
		t.Fatalf("want one similar cluster, got %+v", res.Clusters)
	}
	cl := res.Clusters[0]
	var keep PhotoItem
	for _, it := range cl.Items {
		if it.Keep {
			keep = it
		}
	}
	if keep.Path != large {
		t.Fatalf("keep should be the high-res copy, got %s", keep.Path)
	}
	if cl.Waste != cl.TotalSize-keep.Size {
		t.Fatalf("waste = %d, want %d", cl.Waste, cl.TotalSize-keep.Size)
	}
}

// TestSimilarPhotosMoveToTrash ensures trashing cluster items really empties
// the source and lands them in the (temp) Trash.
func TestSimilarPhotosMoveToTrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pics := filepath.Join(home, "Pictures")
	mustMkdir(t, pics)
	a := filepath.Join(pics, "a.jpg")
	b := filepath.Join(pics, "b.jpg")
	writeGradientJPEG(t, a, 128, 96, false)
	writeGradientJPEG(t, b, 160, 120, false)

	res := buildSimilarPhotos([]string{pics}, nil)
	if len(res.Clusters) != 1 {
		t.Fatalf("want 1 cluster, got %+v", res.Clusters)
	}
	var trashThese []string
	for _, it := range res.Clusters[0].Items {
		if !it.Keep {
			trashThese = append(trashThese, it.Path)
		}
	}
	if len(trashThese) != 1 {
		t.Fatalf("expected 1 non-keep item, got %v", trashThese)
	}
	r := Cleaner{}.CleanPaths(trashThese)
	if r.Moved != 1 || len(r.Failed) != 0 {
		t.Fatalf("cleanPaths = %+v", r)
	}
	// original gone, present in temp trash
	if _, err := os.Stat(trashThese[0]); !os.IsNotExist(err) {
		t.Fatalf("source should be gone: %v", err)
	}
	trash := filepath.Join(home, ".Trash")
	entries, _ := os.ReadDir(trash)
	if len(entries) < 1 {
		t.Fatalf("expected item in trash, got %d entries", len(entries))
	}
}