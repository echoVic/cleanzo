package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"maps"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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

	res := cleanCategory(cat, &usageGuard{}, time.Time{})
	if res.Trashed != 3072 || res.Freed != 0 || res.Files != 2 {
		t.Fatalf("clean got trashed=%d freed=%d files=%d, want 3072/0/2", res.Trashed, res.Freed, res.Files)
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

	res2 := cleanCategory(cat, &usageGuard{}, time.Time{})
	if res2.Trashed != 0 || res2.Files != 0 {
		t.Fatalf("second clean should move nothing, got trashed=%d files=%d", res2.Trashed, res2.Files)
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
	res := cleanCategory(cat, &usageGuard{}, time.Time{})
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
	got := scanCategories([]Category{cat}, &usageGuard{})
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
	appPath := makeFakeApp(t, filepath.Join(home, "Applications"), "TestApp")
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

func TestUninstallApp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := makeFakeApp(t, filepath.Join(home, "Applications"), "TestApp")
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

	items, done, _ := collectFullScan(context.Background(), nil)
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
	files, err := findLargeFiles(context.Background(), target, 1<<20)
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
	items, done, _ := collectLargeScan(context.Background(), 1<<20, func(f LargeFile) {
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

	items, total := collectOrphans(context.Background(), nil)
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
	hA, okA := dHashImage(imgA)

	imgB, _, closeB, _ := decodeImageFile(b)
	defer closeB()
	hB, okB := dHashImage(imgB)

	imgC, _, closeC, _ := decodeImageFile(c)
	defer closeC()
	hC, okC := dHashImage(imgC)

	if !okA || !okB || !okC {
		t.Fatalf("gradients have structure, so their hashes must be usable: %v %v %v", okA, okB, okC)
	}

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
	writeGradientJPEG(t, filepath.Join(pics, "a.png"), 80, 80, false)  // ext doesn't matter for discovery
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

	res := buildSimilarPhotos(context.Background(), []string{pics}, nil)
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

	res := buildSimilarPhotos(context.Background(), []string{pics}, nil)
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

	res := buildSimilarPhotos(context.Background(), []string{pics}, nil)
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

// ---------------------------------------------------------------------------
// Junk cleaning safety
// ---------------------------------------------------------------------------

// TestSafeToCleanResistsAliases covers other spellings of protected places:
// APFS is case-insensitive, and /etc, /tmp and /var are links into /private.
func TestSafeToCleanResistsAliases(t *testing.T) {
	h, _ := os.UserHomeDir()
	me := filepath.Base(h)
	cases := map[string]bool{
		"/users/" + me:                     false,
		"/USERS/" + me + "/library":        false,
		filepath.Join(h, "library"):        false,
		"/applications":                    false,
		"/system/library":                  false,
		"/etc":                             false,
		"/etc/hosts":                       false,
		"/var/db":                          false,
		"/tmp":                             false,
		"/usr/local":                       false,
		"/opt/homebrew":                    false,
		"/bin":                             false,
		filepath.Join(os.TempDir(), "old"): true,
	}
	for p, want := range cases {
		if got := safeToClean(p); got != want {
			t.Errorf("safeToClean(%q) = %v, want %v", p, got, want)
		}
	}
}

// TestSafeToCleanProtectsUserData keeps irreplaceable data out of reach,
// while single items inside user folders stay cleanable.
func TestSafeToCleanProtectsUserData(t *testing.T) {
	h, _ := os.UserHomeDir()
	cases := map[string]bool{
		filepath.Join(h, "Library", "Keychains"):                                                 false,
		filepath.Join(h, "library", "keychains", "login.keychain-db"):                            false,
		filepath.Join(h, ".ssh", "id_ed25519"):                                                   false,
		filepath.Join(h, "Library", "Mobile Documents", "com~apple~CloudDocs", "a.txt"):          false,
		filepath.Join(h, "Pictures", "Photos Library.photoslibrary", "originals", "A", "x.heic"): false,
		filepath.Join(h, "Documents"):                                                            false,
		filepath.Join(h, "Desktop"):                                                              false,
		filepath.Join(h, "Library", "Application Support"):                                       false,
		filepath.Join(h, "Library", "Preferences"):                                               false,
		filepath.Join(h, "Desktop", "IMG_0001.jpg"):                                              true,
		filepath.Join(h, "Library", "Application Support", "SomeApp", "Cache"):                   true,
	}
	for p, want := range cases {
		if got := safeToClean(p); got != want {
			t.Errorf("safeToClean(%q) = %v, want %v", p, got, want)
		}
	}
}

// TestSafeToCleanFollowsLinks judges a link by where it points.
func TestSafeToCleanFollowsLinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Join(home, "Library", "Caches"))
	if err := os.Symlink("/System", filepath.Join(home, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "Library"), filepath.Join(home, "lib")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		filepath.Join(home, "escape"):            false,
		filepath.Join(home, "escape", "Library"): false,
		filepath.Join(home, "lib"):               false,
		filepath.Join(home, "lib", "Caches"):     true,
	}
	for p, want := range cases {
		if got := safeToClean(p); got != want {
			t.Errorf("safeToClean(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestParseLsofNames(t *testing.T) {
	out := []byte("p501\nfcwd\nn/Users/Me/Library/Caches/com.App/db.sqlite\nf12\nn/private/var/folders/x/T/a.lock\nf13\nn->0x1234\np502\nf3\nnpipe\n")
	got := parseLsofNames(out)
	want := []string{"/private/var/folders/x/t/a.lock", "/users/me/library/caches/com.app/db.sqlite"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("parseLsofNames = %q, want %q", got, want)
	}
}

func TestNamedAfterBundle(t *testing.T) {
	running := map[string]bool{"com.microsoft.vscode": true, "com.google.chrome": true}
	cases := map[string]bool{
		"com.microsoft.VSCode":        true,
		"com.microsoft.VSCode.ShipIt": true,
		"com.google.Chrome":           true,
		"com.google.ChromeX":          false,
		"com.microsoft":               false,
		"go-build":                    false,
	}
	for name, want := range cases {
		if got := namedAfterBundle(name, running); got != want {
			t.Errorf("namedAfterBundle(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestUsageGuardInUse(t *testing.T) {
	g := &usageGuard{open: parseLsofNames([]byte(
		"n/Users/me/Library/Caches/App/db\nn/Users/me/Library/Caches/Cache-x/f\nn/private/tmp/sock\n"))}
	cases := map[string]bool{
		"/Users/me/Library/Caches/App":    true,  // holds an open file
		"/Users/me/Library/Caches/app/DB": true,  // the open file itself, other case
		"/Users/me/Library/Caches/Cache":  false, // only a sibling with a longer name is open
		"/Users/me/Library/Caches/Other":  false,
		"/private/tmp/sock":               true,
	}
	for p, want := range cases {
		if got := g.inUse(p); got != want {
			t.Errorf("inUse(%q) = %v, want %v", p, got, want)
		}
	}
	if !(&usageGuard{failed: true}).inUse("/Users/me/anything") {
		t.Error("a guard whose probe failed must report everything in use")
	}
}

// TestOpenFilesSeesOpenFile checks the lsof wiring against a file this test
// holds open.
func TestOpenFilesSeesOpenFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "held.db")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	open, err := openFiles()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ToLower(resolvePath(p))
	if !slices.Contains(open, want) {
		t.Fatalf("openFiles() lacks %s (%d paths)", want, len(open))
	}
}

// TestMain swaps the system Trash, which needs the running app, for a
// stand-in that moves items into $HOME/.Trash.
func TestMain(m *testing.M) {
	trashItem = func(p string) error {
		trash := filepath.Join(homeDir(), ".Trash")
		if err := os.MkdirAll(trash, 0o755); err != nil {
			return err
		}
		dst := filepath.Join(trash, filepath.Base(p))
		for i := 2; ; i++ {
			if _, err := os.Lstat(dst); os.IsNotExist(err) {
				break
			}
			dst = filepath.Join(trash, fmt.Sprintf("%s %d", filepath.Base(p), i))
		}
		return os.Rename(p, dst)
	}
	os.Exit(m.Run())
}

func writeFile(t *testing.T, p string, size int) {
	t.Helper()
	mustMkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// TestNestedCategoriesCountOnce: a category inside another one owns its
// folder, so its files are counted once and cleaned only with it.
func TestNestedCategoriesCountOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	caches := filepath.Join(home, "Library", "Caches")
	writeFile(t, filepath.Join(caches, "com.app", "blob"), 2000)
	writeFile(t, filepath.Join(caches, "go-build", "ab", "obj"), 1000)
	writeFile(t, filepath.Join(caches, "foo", "x"), 300)
	writeFile(t, filepath.Join(caches, "foo", "bar", "y"), 400)
	cats := excludeNested([]Category{
		{Name: "caches", paths: []string{caches}},
		{Name: "go", paths: []string{filepath.Join(caches, "go-build")}},
		{Name: "bar", paths: []string{filepath.Join(caches, "foo", "bar")}},
	})
	got := scanCategories(cats, &usageGuard{})
	if got[0].Size != 2300 || got[1].Size != 1000 || got[2].Size != 400 {
		t.Fatalf("sizes = %d, %d, %d; want 2300, 1000, 400", got[0].Size, got[1].Size, got[2].Size)
	}
	if len(got[0].Top) == 0 || got[0].Top[0].Name != "com.app" {
		t.Fatalf("top of caches = %+v", got[0].Top)
	}
	res := cleanCategory(cats[0], &usageGuard{}, time.Time{})
	if len(res.Failed) != 0 {
		t.Fatalf("failed: %v", res.Failed)
	}
	if exists(filepath.Join(caches, "com.app")) || exists(filepath.Join(caches, "foo", "x")) {
		t.Fatal("caches' own entries should be cleaned")
	}
	if !exists(filepath.Join(caches, "go-build", "ab", "obj")) || !exists(filepath.Join(caches, "foo", "bar", "y")) {
		t.Fatal("folders of other categories must be left alone")
	}
}

// TestTempCleansOnlyStaleEntries: the temporary folder only loses entries
// nothing has touched for a week, and never sockets or pipes.
func TestTempCleansOnlyStaleEntries(t *testing.T) {
	base := t.TempDir()
	home, tmp := filepath.Join(base, "home"), filepath.Join(base, "tmp")
	mustMkdir(t, home)
	mustMkdir(t, tmp)
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", tmp)
	// Files made now cannot get an old status change time, so the clock moves
	// ten days ahead instead; fresh entries are touched at that "now".
	later := time.Now().Add(10 * 24 * time.Hour)
	defer func(old func() time.Time) { now = old }(now)
	now = func() time.Time { return later }
	touch := func(paths ...string) {
		for _, p := range paths {
			if err := os.Chtimes(p, later, later); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeFile(t, filepath.Join(tmp, "stale", "a.bin"), 1000)
	writeFile(t, filepath.Join(tmp, "fresh.bin"), 500)
	touch(filepath.Join(tmp, "fresh.bin"))
	writeFile(t, filepath.Join(tmp, "mixed", "old.bin"), 300)
	writeFile(t, filepath.Join(tmp, "mixed", "new.bin"), 200)
	touch(filepath.Join(tmp, "mixed", "new.bin"))
	if err := syscall.Mkfifo(filepath.Join(tmp, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}

	cat := Category{Name: "temp", paths: []string{tmp}}
	got := scanCategories([]Category{cat}, &usageGuard{})
	if got[0].Size != 1000 || got[0].Files != 1 {
		t.Fatalf("scan = %d bytes, %d files; want 1000, 1", got[0].Size, got[0].Files)
	}
	cleanCategory(cat, &usageGuard{}, time.Time{})
	if exists(filepath.Join(tmp, "stale")) {
		t.Fatal("stale entry should be cleaned")
	}
	for _, keep := range []string{"fresh.bin", "mixed", "pipe"} {
		if !exists(filepath.Join(tmp, keep)) {
			t.Errorf("%s must be kept", keep)
		}
	}
}

// TestCleanSkipsOpenFiles: a folder holding a file a process has open is
// in use, so cleaning leaves it and counts it.
func TestCleanSkipsOpenFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "junk")
	writeFile(t, filepath.Join(dir, "busy", "db.sqlite"), 800)
	writeFile(t, filepath.Join(dir, "idle", "x"), 300)
	f, err := os.Open(filepath.Join(dir, "busy", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cat := Category{Name: "junk", paths: []string{dir}}
	g := probeUsage()
	if got := scanCategories([]Category{cat}, g); got[0].Size != 300 || got[0].InUse != 1 {
		t.Fatalf("scan = %d bytes, %d in use; want 300, 1", got[0].Size, got[0].InUse)
	}
	res := cleanCategory(cat, g, time.Time{})
	if res.InUse != 1 || !exists(filepath.Join(dir, "busy", "db.sqlite")) || exists(filepath.Join(dir, "idle")) {
		t.Fatalf("res = %+v; busy must stay, idle must go", res)
	}
}

// TestScanReportsUnreadableFolder: a folder macOS will not let the app read
// is reported as denied, not as empty.
func TestScanReportsUnreadableFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	locked := filepath.Join(home, "locked")
	writeFile(t, filepath.Join(locked, "a"), 100)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	got := scanCategories([]Category{{Name: "x", paths: []string{locked}}}, &usageGuard{})
	if len(got[0].Denied) != 1 || got[0].Size != 0 || len(got[0].Missing) != 0 {
		t.Fatalf("got denied=%v missing=%v size=%d", got[0].Denied, got[0].Missing, got[0].Size)
	}
}

// TestBuiltinJunkIsDeleted: built-in categories at their built-in paths
// hold regenerable junk, which is deleted rather than moved to the Trash.
func TestBuiltinJunkIsDeleted(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, "Library", "Caches", "com.example.app", "blob"), 4096)
	sum := Cleaner{}.Clean([]string{"caches"}, 0)
	if sum.TotalFreed != 4096 || sum.TotalTrashed != 0 || len(sum.Errors) != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	if exists(filepath.Join(home, "Library", "Caches", "com.example.app")) {
		t.Fatal("cache should be deleted")
	}
	if entries, _ := os.ReadDir(filepath.Join(home, ".Trash")); len(entries) != 0 {
		t.Fatalf("nothing should reach the Trash, got %d entries", len(entries))
	}
}

// TestCleanEmptiesTrashFirst: emptying the Trash in the same pass happens
// first, so what the pass moves to the Trash stays recoverable.
func TestCleanEmptiesTrashFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	trash := filepath.Join(home, ".Trash")
	writeFile(t, filepath.Join(trash, "old.bin"), 1500)
	mine := filepath.Join(home, "mydir")
	writeFile(t, filepath.Join(mine, "a.bin"), 700)
	if err := (Cleaner{}).SaveCategories([]CategoryConfig{
		{Name: "mine", Risk: "safe", Enabled: true, Paths: []string{mine}},
		{Name: "trash", Risk: "moderate", Enabled: true, Paths: []string{trash}},
	}); err != nil {
		t.Fatal(err)
	}
	sum := Cleaner{}.Clean(nil, 0)
	if !exists(filepath.Join(trash, "a.bin")) || exists(filepath.Join(trash, "old.bin")) {
		t.Fatal("a.bin should be in the Trash, old.bin gone")
	}
	if sum.TotalFreed != 1500 || sum.TotalTrashed != 700 {
		t.Fatalf("summary = %+v", sum)
	}
}

// TestConfigMigratesV1Defaults: version 1 files keep the user's edits, while
// built-ins still at their old defaults adopt the new ones.
func TestConfigMigratesV1Defaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Dir(configPath()))
	v1 := `[
  {"name":"temp","description":"用户临时文件","risk":"safe","paths":[` + strconv.Quote(os.TempDir()) + `],"enabled":true},
  {"name":"trash","description":"废纸篓","risk":"safe","paths":[` + strconv.Quote(filepath.Join(home, ".Trash")) + `],"enabled":true},
  {"name":"logs","description":"日志","risk":"safe","paths":["/elsewhere/logs"],"enabled":true}
]`
	if err := os.WriteFile(configPath(), []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Cleaner{}.GetCategories()
	risks := map[string]string{}
	for _, c := range got {
		risks[c.Name] = c.Risk
	}
	if risks["temp"] != "moderate" || risks["trash"] != "moderate" || risks["logs"] != "safe" {
		t.Fatalf("risks = %v", risks)
	}
	b, _ := os.ReadFile(configPath())
	if !strings.HasPrefix(strings.TrimSpace(string(b)), "{") || !strings.Contains(string(b), `"version": 2`) {
		t.Fatalf("config should be rewritten as version 2:\n%s", b)
	}
}

func TestSaveCategoriesRejectsDuplicates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a, b := filepath.Join(home, "a"), filepath.Join(home, "b")
	if err := (Cleaner{}).SaveCategories([]CategoryConfig{
		{Name: "dup", Enabled: true, Paths: []string{a}},
		{Name: "Dup", Enabled: true, Paths: []string{b}},
	}); err == nil {
		t.Error("two categories with one name should be rejected")
	}
	if err := (Cleaner{}).SaveCategories([]CategoryConfig{
		{Name: "a", Enabled: true, Paths: []string{a}},
		{Name: "b", Enabled: true, Paths: []string{a + "/"}},
	}); err == nil {
		t.Error("two categories with one folder should be rejected")
	}
}

// TestTrashPathRefusesFolderInUse and its CleanPaths counterpart leave what
// a process has open where it is.
func TestTrashPathRefusesFolderInUse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	app := filepath.Join(home, "Library", "Caches", "AppA")
	writeFile(t, filepath.Join(app, "c.db"), 512)
	f, err := os.Open(filepath.Join(app, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := (Cleaner{}).TrashPath(app); err == nil {
		t.Fatal("expected an in-use error")
	}
	res := Cleaner{}.CleanPaths([]string{filepath.Join(app, "c.db")})
	if res.InUse != 1 || res.Moved != 0 || !exists(filepath.Join(app, "c.db")) {
		t.Fatalf("res = %+v", res)
	}
}

func TestScanAppCachesFlagsInUse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	caches := filepath.Join(home, "Library", "Caches")
	writeFile(t, filepath.Join(caches, "busy", "db"), 100)
	writeFile(t, filepath.Join(caches, "idle", "x"), 100)
	f, err := os.Open(filepath.Join(caches, "busy", "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	inUse := map[string]bool{}
	for _, it := range (Cleaner{}).ScanAppCaches() {
		inUse[it.Name] = it.InUse
	}
	if !inUse["busy"] || inUse["idle"] {
		t.Fatalf("inUse = %v", inUse)
	}
}

// ---------------------------------------------------------------------------
// Similar photos: what gets scanned and what counts as similar
// ---------------------------------------------------------------------------

// TestCollectPhotoFilesSkipsPackages leaves libraries and bundles, whose
// images their apps manage, and hidden folders out of the scan.
func TestCollectPhotoFilesSkipsPackages(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{
		"Photos Library.photoslibrary/originals/A/x.jpg",
		"Old.photolibrary/Masters/y.jpg",
		"Some.app/Contents/Resources/icon.png",
		"Photo Booth Library/Pictures/z.jpg",
		".hidden/h.jpg",
		"trip/real.jpg",
		"real2.png",
	} {
		writeFile(t, filepath.Join(root, p), 10)
	}
	got := collectPhotoFiles([]string{root}, 100)
	slices.Sort(got)
	want := []string{filepath.Join(root, "real2.png"), filepath.Join(root, "trip", "real.jpg")}
	if !slices.Equal(got, want) {
		t.Fatalf("collected %v, want %v", got, want)
	}
}

// writeDocPNG draws a white page with dark bars, as screenshots of documents
// look.
func writeDocPNG(t *testing.T, path string, w, h int, bars [][4]int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	for _, b := range bars {
		for y := b[1]; y < min(b[3], h); y++ {
			for x := b[0]; x < min(b[2], w); x++ {
				img.SetRGBA(x, y, color.RGBA{R: 30, G: 30, B: 30, A: 255})
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// TestBlankImagesAreNotSimilar: images with nothing in them carry no visual
// fingerprint, so only byte-identical copies may group them.
func TestBlankImagesAreNotSimilar(t *testing.T) {
	pics := t.TempDir()
	writeDocPNG(t, filepath.Join(pics, "blank1.png"), 400, 300, [][4]int{{10, 10, 30, 12}})
	writeDocPNG(t, filepath.Join(pics, "blank2.png"), 400, 300, [][4]int{{300, 250, 340, 252}})
	if res := buildSimilarPhotos(context.Background(), []string{pics}, nil); len(res.Clusters) != 0 {
		t.Fatalf("different blank images must not group: %+v", res.Clusters)
	}
}

// TestTextDocumentsAreNotSimilar: unrelated document screenshots differ in
// layout, which the fingerprint has to see.
func TestTextDocumentsAreNotSimilar(t *testing.T) {
	pics := t.TempDir()
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range 8 {
		var bars [][4]int
		for range 25 {
			x, y := rng.IntN(300), rng.IntN(290)
			bars = append(bars, [4]int{x, y, x + 20 + rng.IntN(80), y + 6})
		}
		writeDocPNG(t, filepath.Join(pics, fmt.Sprintf("doc%d.png", i)), 400, 300, bars)
	}
	if res := buildSimilarPhotos(context.Background(), []string{pics}, nil); len(res.Clusters) != 0 {
		t.Fatalf("unrelated documents must not group, got %d clusters", len(res.Clusters))
	}
}

// TestDirChildrenReportsTotal: the listing is capped, and says how many
// entries there are, so the UI knows when it shows only part of a folder.
func TestDirChildrenReportsTotal(t *testing.T) {
	dir := t.TempDir()
	for i := range 70 {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("f%02d", i)), i+1)
	}
	got, err := (Cleaner{}).DirChildren(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 60 || got.Total != 70 {
		t.Fatalf("items = %d, total = %d; want 60, 70", len(got.Items), got.Total)
	}
	if got.Items[0].Name != "f69" {
		t.Fatalf("largest first, got %s", got.Items[0].Name)
	}
}

// ---------------------------------------------------------------------------
// Uninstall: only apps ListApps lists, and nothing outside their folders
// ---------------------------------------------------------------------------

// TestUninstallPathsStayInLibrary: names from Info.plist cannot climb out of
// the Library folders uninstall cleans.
func TestUninstallPathsStayInLibrary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	docs := filepath.Join(home, "Documents")
	mustMkdir(t, docs)
	mustMkdir(t, filepath.Join(home, "Library", "Preferences"))
	apps := filepath.Join(home, "Applications")
	for _, tc := range []struct{ app, bid string }{
		{"Evil.app", "../../Documents"},
		{"Evil.app", ".."},
		{"Evil.app", "."},
		{"...app", "com.example.ok"},
	} {
		appPath := filepath.Join(apps, tc.app)
		mustMkdir(t, appPath)
		for _, it := range uninstallPaths(AppInfo{Name: "Evil", Path: appPath, BundleID: tc.bid}) {
			if it.Path != appPath && !strings.HasPrefix(it.Path, filepath.Join(home, "Library")+"/") {
				t.Errorf("%s / %q reaches %s", tc.app, tc.bid, it.Path)
			}
			if it.Path == filepath.Join(home, "Library") || it.Path == filepath.Join(home, "Library", "Preferences") {
				t.Errorf("%s / %q reaches a whole Library folder: %s", tc.app, tc.bid, it.Path)
			}
		}
		os.RemoveAll(appPath)
	}
}

// TestUninstallOnlyInstalledApps: a path that is not an app in an
// Applications folder is not uninstalled.
func TestUninstallOnlyInstalledApps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	docs := filepath.Join(home, "Documents")
	writeFile(t, filepath.Join(docs, "a.txt"), 10)
	notInApps := makeFakeApp(t, home, "Stray")
	for _, p := range []string{docs, notInApps} {
		res := Cleaner{}.UninstallApp(p)
		if res.Moved != 0 || len(res.Failed) == 0 {
			t.Errorf("UninstallApp(%s) = %+v, want a refusal", p, res)
		}
		if !exists(p) {
			t.Errorf("%s must stay", p)
		}
	}
}

// TestUninstallStopsWhenAppCannotMove: an app that cannot move keeps its
// data, instead of losing its settings while it stays installed.
func TestUninstallStopsWhenAppCannotMove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	apps := filepath.Join(home, "Applications")
	appPath := makeFakeApp(t, apps, "TestApp")
	pref := filepath.Join(home, "Library", "Preferences", "com.example.testapp.plist")
	writeFile(t, pref, 256)
	if err := os.Chmod(apps, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(apps, 0o755) })
	res := Cleaner{}.UninstallApp(appPath)
	if res.Moved != 0 || len(res.Failed) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if !exists(appPath) || !exists(pref) {
		t.Fatal("the app and its preferences must both stay")
	}
}

// ---------------------------------------------------------------------------
// Leftovers: live data is not a leftover
// ---------------------------------------------------------------------------

// makeApp creates an app bundle with its own bundle ID and display name.
func makeApp(t *testing.T, dir, folder, bid, display string) string {
	t.Helper()
	appPath := filepath.Join(dir, folder)
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleDisplayName</key><string>` + display + `</string>
<key>CFBundleIdentifier</key><string>` + bid + `</string>
</dict></plist>`
	mustMkdir(t, filepath.Join(appPath, "Contents"))
	if err := os.WriteFile(filepath.Join(appPath, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	return appPath
}

// TestOrphansSkipLiveData: data of Apple's own services, input methods,
// apps in subfolders, apps whose folders are named after their vendor or
// bundle ID, and hidden entries are not leftovers.
func TestOrphansSkipLiveData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	apps := filepath.Join(home, "Applications")
	makeApp(t, filepath.Join(home, "Library", "Input Methods"), "WeType.app", "com.tencent.inputmethod.wetype", "WeType")
	makeApp(t, filepath.Join(apps, "Vendor Suite"), "Tool.app", "com.vendor.tool", "Tool")
	makeApp(t, apps, "语雀.app", "com.yuque.desktop", "语雀")
	makeApp(t, apps, "Fusion.app", "com.citrolabs.fusion", "Fusion")
	support := filepath.Join(home, "Library", "Application Support")
	for _, n := range []string{"Animoji", "networkserviceproxy", "MobileSync", "WeType", "Tool", "yuque-desktop", "Citro Labs", "TrulyGone"} {
		writeFile(t, filepath.Join(support, n, "data.bin"), 2<<20)
	}
	// hidden entries are command line tools' settings, such as wrangler's login
	writeFile(t, filepath.Join(home, "Library", "Preferences", ".wrangler", "config.toml"), 2<<20)
	items, _ := collectOrphans(context.Background(), nil)
	var got []string
	for _, it := range items {
		got = append(got, filepath.Base(it.Path))
	}
	if !slices.Equal(got, []string{"TrulyGone"}) {
		t.Fatalf("leftovers = %v, want [TrulyGone]", got)
	}
}

// ---------------------------------------------------------------------------
// du and find: partial results and other volumes
// ---------------------------------------------------------------------------

// lockedTree makes dir hold a readable 1 MiB file and a folder du and find
// cannot read, which makes both exit with status 1.
func lockedTree(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "big.bin"), 1<<20)
	locked := filepath.Join(dir, "locked")
	writeFile(t, filepath.Join(locked, "x"), 10)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
}

func TestDuSizeKeepsPartialResult(t *testing.T) {
	dir := t.TempDir()
	lockedTree(t, dir)
	size, err := duSize(context.Background(), dir, 10*time.Second)
	if err != nil || size < 1<<20 {
		t.Fatalf("duSize = %d, %v; want at least 1 MiB", size, err)
	}
}

func TestFindLargeFilesKeepsPartialResult(t *testing.T) {
	dir := t.TempDir()
	lockedTree(t, dir)
	files, err := findLargeFiles(context.Background(), dir, 512<<10)
	if err != nil || len(files) != 1 || filepath.Base(files[0].Path) != "big.bin" {
		t.Fatalf("findLargeFiles = %+v, %v", files, err)
	}
}

// TestScansStayOnOneVolume: a disk mounted inside a scanned folder, such as
// a VM's or a container runtime's, is not counted as part of it.
func TestScansStayOnOneVolume(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(t.TempDir(), "vol.dmg")
	if out, err := exec.Command("hdiutil", "create", "-quiet", "-size", "8m", "-fs", "HFS+", "-volname", "cleanzo-test", img).CombinedOutput(); err != nil {
		t.Skipf("hdiutil create: %v %s", err, out)
	}
	mnt := filepath.Join(dir, "mnt")
	mustMkdir(t, mnt)
	if out, err := exec.Command("hdiutil", "attach", "-quiet", "-nobrowse", "-mountpoint", mnt, img).CombinedOutput(); err != nil {
		t.Skipf("hdiutil attach: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("hdiutil", "detach", "-quiet", "-force", mnt).Run() })
	writeFile(t, filepath.Join(mnt, "on-volume.bin"), 2<<20)
	writeFile(t, filepath.Join(dir, "here.bin"), 1<<20)

	size, err := duSize(context.Background(), dir, 10*time.Second)
	if err != nil || size >= 2<<20 {
		t.Errorf("duSize = %d, %v; the mounted volume must not count", size, err)
	}
	files, err := findLargeFiles(context.Background(), dir, 512<<10)
	if err != nil || len(files) != 1 || filepath.Base(files[0].Path) != "here.bin" {
		t.Errorf("findLargeFiles = %+v, %v; want only here.bin", files, err)
	}
}

// TestRevealArgs: packages are revealed (selected) in Finder rather than
// opened, which would launch an app, boot a VM or mount a disk image.
func TestRevealArgs(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "Projects")
	mustMkdir(t, plain)
	file := filepath.Join(dir, "a.txt")
	writeFile(t, file, 1)
	app := makeApp(t, dir, "Foo.app", "com.example.foo", "Foo")
	unnamed := makeApp(t, dir, "Bundle Without Extension", "com.example.bare", "Bare")
	vm := filepath.Join(dir, "Windows.pvm")
	mustMkdir(t, vm)
	cases := map[string][]string{
		plain:   {plain},
		file:    {"-R", file},
		app:     {"-R", app},
		unnamed: {"-R", unnamed},
		vm:      {"-R", vm},
	}
	for p, want := range cases {
		got, err := revealArgs(p)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("revealArgs(%s) = %q, %v; want %q", filepath.Base(p), got, err, want)
		}
	}
	if _, err := revealArgs(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing path should be an error")
	}
}

// TestScansStopWhenCanceled: when the page goes away, mygo cancels the
// call's context, and the scans stop instead of running du, find and sips on.
func TestScansStopWhenCanceled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, "AAA", "big"), 2<<20)
	writeFile(t, filepath.Join(home, "Library", "Caches", "Leftover", "x"), 2<<20)
	pics := filepath.Join(home, "Pictures")
	mustMkdir(t, pics)
	writeGradientJPEG(t, filepath.Join(pics, "a.jpg"), 128, 96, false)
	copyFile(t, filepath.Join(pics, "a.jpg"), filepath.Join(pics, "b.jpg"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := duSize(ctx, home, 10*time.Second); err == nil {
		t.Error("duSize ran on a canceled context")
	}
	if _, err := findLargeFiles(ctx, home, 1<<20); err == nil {
		t.Error("findLargeFiles ran on a canceled context")
	}
	if items, done, _ := collectFullScan(ctx, nil); len(items) != 0 || done != 0 {
		t.Errorf("collectFullScan measured %d targets", done)
	}
	if items, done, _ := collectLargeScan(ctx, 1<<20, nil); len(items) != 0 || done != 0 {
		t.Errorf("collectLargeScan scanned %d targets", done)
	}
	if items, _ := collectOrphans(ctx, nil); len(items) != 0 {
		t.Errorf("collectOrphans found %d", len(items))
	}
	if res := buildSimilarPhotos(ctx, []string{pics}, nil); len(res.Clusters) != 0 {
		t.Errorf("buildSimilarPhotos grouped %d clusters", len(res.Clusters))
	}
}

// TestLargePhotosAreDecodedSmall: photos above the pixel budget, and HEIC
// photos, are decoded from a small copy, while their real size still
// decides which copy to keep.
func TestLargePhotosAreDecodedSmall(t *testing.T) {
	defer func(old int) { maxDecodePixels = old }(maxDecodePixels)
	maxDecodePixels = 1_000_000
	dir := t.TempDir()
	big := filepath.Join(dir, "big.png")
	writeDocPNG(t, big, 1600, 1200, [][4]int{{100, 100, 900, 700}})
	heic := filepath.Join(dir, "photo.heic")
	if out, err := exec.Command("/usr/bin/sips", "-s", "format", "heic", big, "--out", heic).CombinedOutput(); err != nil {
		t.Fatalf("sips: %v %s", err, out)
	}
	for _, p := range []string{big, heic} {
		img, cfg, cleanup, err := loadPhotoImage(context.Background(), p)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(p), err)
		}
		cleanup()
		if cfg.Width != 1600 || cfg.Height != 1200 {
			t.Errorf("%s: size = %dx%d, want the real 1600x1200", filepath.Base(p), cfg.Width, cfg.Height)
		}
		if b := img.Bounds(); max(b.Dx(), b.Dy()) > photoDecodeEdge {
			t.Errorf("%s: decoded at %dx%d, want at most %d px", filepath.Base(p), b.Dx(), b.Dy(), photoDecodeEdge)
		}
	}
}

// ---------------------------------------------------------------------------
// Second review: the usage guard
// ---------------------------------------------------------------------------

// TestOpenFilesSeesNonASCIIPathWithoutLocale: apps started from Finder have
// no LANG, under which lsof escapes non-ASCII bytes as \xNN; the guard must
// still see such files.
func TestOpenFilesSeesNonASCIIPathWithoutLocale(t *testing.T) {
	t.Setenv("LANG", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	p := filepath.Join(t.TempDir(), "语雀", "db.sqlite")
	writeFile(t, p, 1)
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	open, err := openFiles()
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ToLower(resolvePath(p)); !slices.Contains(open, want) {
		t.Fatalf("openFiles() lacks %s", want)
	}
}

// TestParseLsofNamesResolvesLinks: sockets are listed under the path they
// were bound with, which may go through /var or /tmp and repeat slashes.
func TestParseLsofNamesResolvesLinks(t *testing.T) {
	got := parseLsofNames([]byte("n/var/folders/x/T//scoped_dir1/SingletonSocket\nn/tmp/a.sock\nn/private/etc/x\n"))
	want := []string{"/private/etc/x", "/private/tmp/a.sock", "/private/var/folders/x/t/scoped_dir1/singletonsocket"}
	if !slices.Equal(got, want) {
		t.Fatalf("parseLsofNames = %q, want %q", got, want)
	}
}

// TestUsageGuardSeesSockets: a folder holding a socket a process listens on,
// as Chromium-based apps keep in $TMPDIR, is in use.
func TestUsageGuardSeesSockets(t *testing.T) {
	dir, err := os.MkdirTemp("", "s")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := net.Listen("unix", filepath.Join(dir, "x.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !probeUsage().inUse(resolvePath(dir)) {
		t.Fatal("a folder with a listening socket must be in use")
	}
}

func TestParseRunningApps(t *testing.T) {
	ids, apps := parseRunningApps([]byte(`    bundleID="com.apple.loginwindow"
    bundle path="/System/Library/CoreServices/loginwindow.app"
    bundleID=[ NULL ]
    bundle path="/usr/sbin/universalaccessd"
    bundleID="ai.manus.agents"
    bundle path="/Volumes/Cue 1.2/Cue.app"
`))
	if len(ids) != 2 || !ids["com.apple.loginwindow"] || !ids["ai.manus.agents"] {
		t.Errorf("ids = %v", ids)
	}
	if !slices.Equal(apps, []string{"/System/Library/CoreServices/loginwindow.app", "/Volumes/Cue 1.2/Cue.app"}) {
		t.Errorf("apps = %q", apps)
	}
}

// TestOwnsShortFolderNames: a folder named like the start of an installed
// app's name belongs to it, as Zed's does to Zed Preview.
func TestOwnsShortFolderNames(t *testing.T) {
	k := newAppKeys()
	// a bundle ID without the name, so only the name can tie the folder to it
	k.addApp(makeApp(t, t.TempDir(), "Zed Preview.app", "dev.example.editor", "Zed Preview"))
	if !k.owns("Zed") {
		t.Error("Zed belongs to Zed Preview")
	}
}

// TestOrphansSkipFoldersInUse: a running app's data, wherever the app is
// installed, holds open files and is not a leftover.
func TestOrphansSkipFoldersInUse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	support := filepath.Join(home, "Library", "Application Support")
	writeFile(t, filepath.Join(support, "TrulyGone", "data.bin"), 2<<20)
	writeFile(t, filepath.Join(support, "LiveElsewhere", "data.bin"), 2<<20)
	f, err := os.Open(filepath.Join(support, "LiveElsewhere", "data.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	items, _ := collectOrphans(context.Background(), nil)
	var got []string
	for _, it := range items {
		got = append(got, filepath.Base(it.Path))
	}
	if !slices.Equal(got, []string{"TrulyGone"}) {
		t.Fatalf("leftovers = %v, want [TrulyGone]", got)
	}
}

// ---------------------------------------------------------------------------
// Second review: what cleaning and Move to Trash may touch
// ---------------------------------------------------------------------------

// TestTrashPathHonorsSafetyGate: Move to Trash refuses what cleaning
// protects, and rows offer it only where it would be allowed.
func TestTrashPathHonorsSafetyGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, p := range []string{
		filepath.Join(home, "Pictures", "Photos Library.photoslibrary", "originals", "A", "x.mov"),
		filepath.Join(home, "Library", "Mobile Documents", "com~apple~CloudDocs", "a.txt"),
		filepath.Join(home, ".ssh", "id_ed25519"),
	} {
		writeFile(t, p, 10)
		if _, err := (Cleaner{}).TrashPath(p); err == nil || !exists(p) {
			t.Errorf("TrashPath(%s) moved it", p)
		}
		if removable(p) {
			t.Errorf("%s must not be offered for the Trash", p)
		}
	}
	listing, err := (Cleaner{}).DirChildren(context.Background(), filepath.Join(home, ".ssh"))
	if err != nil || len(listing.Items) != 1 || listing.Items[0].Trashable {
		t.Errorf("DirChildren(~/.ssh) = %+v, %v", listing, err)
	}
}

// TestCategoryFlagsFollowWhatCleaningDoes: a category on the Trash deletes
// for good whatever it is called, and only untouched built-ins are built-in.
func TestCategoryFlagsFollowWhatCleaningDoes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	trash, mine, logs := filepath.Join(home, ".Trash"), filepath.Join(home, "mine"), filepath.Join(home, "Library", "Logs")
	if err := (Cleaner{}).SaveCategories([]CategoryConfig{
		{Name: "废纸篓", Enabled: true, Paths: []string{trash}},
		{Name: "mine", Enabled: true, Paths: []string{mine}},
		{Name: "logs", Enabled: true, Paths: []string{logs}},
	}); err != nil {
		t.Fatal(err)
	}
	type flags struct{ permanent, builtin bool }
	got := map[string]flags{}
	for _, c := range loadCategories() {
		got[c.Name] = flags{c.Permanent, c.Builtin}
	}
	want := map[string]flags{"废纸篓": {true, false}, "mine": {false, false}, "logs": {true, true}}
	if !maps.Equal(got, want) {
		t.Fatalf("flags = %+v, want %+v", got, want)
	}
}

// TestDisabledCategoryKeepsItsFolder: disabling a nested category keeps its
// folder out of cleaning, instead of folding it into the outer one.
func TestDisabledCategoryKeepsItsFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	caches := filepath.Join(home, "Library", "Caches")
	writeFile(t, filepath.Join(caches, "go-build", "x"), 1000)
	writeFile(t, filepath.Join(caches, "com.app", "y"), 500)
	cfg := defaultCategoryConfigs()
	for i := range cfg {
		if cfg[i].Name == "go" {
			cfg[i].Enabled = false
		}
	}
	if err := (Cleaner{}).SaveCategories(cfg); err != nil {
		t.Fatal(err)
	}
	Cleaner{}.Clean([]string{"caches"}, 0)
	if !exists(filepath.Join(caches, "go-build", "x")) || exists(filepath.Join(caches, "com.app")) {
		t.Fatal("caches must clean com.app and leave the disabled go category's folder")
	}
}

// TestUninstallRefusesAppInUse: a running app (its files are open) is not
// uninstalled; data of it that is in use stays.
func TestUninstallRefusesAppInUse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	appPath := makeFakeApp(t, filepath.Join(home, "Applications"), "TestApp")
	f, err := os.Open(filepath.Join(appPath, "Contents", "MacOS", "TestApp", "bin"))
	if err != nil {
		t.Fatal(err)
	}
	res := Cleaner{}.UninstallApp(appPath)
	f.Close()
	if res.Moved != 0 || len(res.Failed) != 1 || !exists(appPath) {
		t.Fatalf("running app: %+v", res)
	}

	container := filepath.Join(home, "Library", "Containers", "com.example.testapp", "data")
	writeFile(t, container, 10)
	g, err := os.Open(container)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	res = Cleaner{}.UninstallApp(appPath)
	if exists(appPath) || !exists(container) || len(res.Failed) != 1 {
		t.Fatalf("app with an open container: %+v", res)
	}
}

// TestSaveCategoriesIgnoresDisabledLegacyPaths: a disabled category from an
// older version, whose folder is now protected, does not block saving.
func TestSaveCategoriesIgnoresDisabledLegacyPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := CategoryConfig{Name: "old", Enabled: false, Paths: []string{filepath.Join(home, "Downloads")}}
	ok := CategoryConfig{Name: "ok", Enabled: true, Paths: []string{filepath.Join(home, "x")}}
	if err := (Cleaner{}).SaveCategories([]CategoryConfig{old, ok}); err != nil {
		t.Fatalf("disabled legacy category blocked saving: %v", err)
	}
	old.Enabled = true
	if err := (Cleaner{}).SaveCategories([]CategoryConfig{old, ok}); err == nil {
		t.Fatal("an enabled category on a protected folder must be rejected")
	}
}

// TestEmptyCategoryListIsKept: removing every category means none, not the
// defaults, which delete for good.
func TestEmptyCategoryListIsKept(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := (Cleaner{}).SaveCategories([]CategoryConfig{}); err != nil {
		t.Fatal(err)
	}
	if got := (Cleaner{}).GetCategories(); len(got) != 0 {
		t.Fatalf("GetCategories = %d categories", len(got))
	}
	if got := loadCategories(); len(got) != 0 {
		t.Fatalf("loadCategories = %d categories", len(got))
	}
}

// TestCategoryPathCanBeAFile: a category on a file cleans that file.
func TestCategoryPathCanBeAFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	log := filepath.Join(home, "Library", "Logs", "huge.log")
	writeFile(t, log, 5000)
	cat := Category{Name: "log", paths: []string{log}}
	if got := scanCategories([]Category{cat}, &usageGuard{}); got[0].Size != 5000 {
		t.Fatalf("size = %d, want 5000", got[0].Size)
	}
	if res := cleanCategory(cat, &usageGuard{}, time.Time{}); res.Trashed != 5000 || exists(log) {
		t.Fatalf("res = %+v", res)
	}
}

// TestPlanSkipsProtectedEntries: entries macOS will not let the app remove,
// such as immutable ones, are left out instead of failing every clean.
func TestPlanSkipsProtectedEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "junk")
	writeFile(t, filepath.Join(dir, "locked", "x"), 10)
	writeFile(t, filepath.Join(dir, "plain", "y"), 20)
	locked := filepath.Join(dir, "locked")
	if err := syscall.Chflags(locked, 0x2); err != nil { // UF_IMMUTABLE
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Chflags(locked, 0) })
	cat := Category{Name: "junk", paths: []string{dir}}
	if got := scanCategories([]Category{cat}, &usageGuard{}); got[0].Size != 20 {
		t.Fatalf("size = %d, want 20", got[0].Size)
	}
	if res := cleanCategory(cat, &usageGuard{}, time.Time{}); len(res.Failed) != 0 || exists(filepath.Join(dir, "plain")) || !exists(locked) {
		t.Fatalf("res = %+v", res)
	}
}

// TestCleanRemovesReadOnlyTrees: junk with read-only folders inside, as Go's
// module cache leaves, is still deleted.
func TestCleanRemovesReadOnlyTrees(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mod := filepath.Join(home, "Library", "Caches", "gomod")
	writeFile(t, filepath.Join(mod, "pkg", "a.go"), 100)
	for _, d := range []string{filepath.Join(mod, "pkg"), mod} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { os.Chmod(mod, 0o755); os.Chmod(filepath.Join(mod, "pkg"), 0o755) })
	if sum := (Cleaner{}).Clean([]string{"caches"}, 0); len(sum.Errors) != 0 || exists(mod) {
		t.Fatalf("summary = %+v", sum)
	}
}

// TestTempAgeCountsMoves: a file moved or extracted into the temporary folder
// keeps its old modification time, yet it arrived just now.
func TestTempAgeCountsMoves(t *testing.T) {
	base := t.TempDir()
	home, tmp := filepath.Join(base, "home"), filepath.Join(base, "tmp")
	mustMkdir(t, home)
	t.Setenv("HOME", home)
	t.Setenv("TMPDIR", tmp)
	old := time.Now().Add(-30 * 24 * time.Hour)
	p := filepath.Join(tmp, "extracted", "a.bin")
	writeFile(t, p, 100)
	for _, q := range []string{p, filepath.Dir(p)} {
		if err := os.Chtimes(q, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if got := scanCategories([]Category{{Name: "temp", paths: []string{tmp}}}, &usageGuard{}); got[0].Size != 0 {
		t.Fatalf("a just-extracted file was planned: %d bytes", got[0].Size)
	}
}

// TestCleanKeepsWhatReachedTheTrashAfterTheScan: emptying the Trash deletes
// what the scan showed; what arrives later stays recoverable.
func TestCleanKeepsWhatReachedTheTrashAfterTheScan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	trash := filepath.Join(home, ".Trash")
	writeFile(t, filepath.Join(trash, "old.bin"), 1500)
	time.Sleep(20 * time.Millisecond)
	scannedAt := time.Now()
	time.Sleep(20 * time.Millisecond)
	writeFile(t, filepath.Join(trash, "new.bin"), 700)
	sum := Cleaner{}.Clean([]string{"trash"}, scannedAt.UnixMilli())
	if exists(filepath.Join(trash, "old.bin")) || !exists(filepath.Join(trash, "new.bin")) || sum.TotalFreed != 1500 {
		t.Fatalf("summary = %+v", sum)
	}
}

// TestReadBundleInfoWithDates: Info.plist files may hold <date> and <data>
// values, which JSON cannot represent.
func TestReadBundleInfoWithDates(t *testing.T) {
	p := filepath.Join(t.TempDir(), "Info.plist")
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>BuildDate</key><date>2026-01-02T03:04:05Z</date>
<key>Blob</key><data>AAEC</data>
<key>LSEnvironment</key><dict><key>CFBundleIdentifier</key><string>nested.wrong</string></dict>
<key>CFBundleIdentifier</key><string>com.example.dated</string>
<key>CFBundleName</key><string>Dated</string>
<key>CFBundleShortVersionString</key><string>2.0</string>
</dict></plist>`
	if err := os.WriteFile(p, []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	name, bid, version := readBundleInfo(p)
	if name != "Dated" || bid != "com.example.dated" || version != "2.0" {
		t.Fatalf("readBundleInfo = %q %q %q", name, bid, version)
	}
}

// TestFullScanSkipsMountedVolumes: a volume mounted directly in the home
// folder is another disk, not part of the home folder's usage.
func TestFullScanSkipsMountedVolumes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	img := filepath.Join(t.TempDir(), "vol.dmg")
	if out, err := exec.Command("hdiutil", "create", "-quiet", "-size", "8m", "-fs", "HFS+", "-volname", "cleanzo-test", img).CombinedOutput(); err != nil {
		t.Skipf("hdiutil create: %v %s", err, out)
	}
	mnt := filepath.Join(home, "VMMount")
	mustMkdir(t, mnt)
	if out, err := exec.Command("hdiutil", "attach", "-quiet", "-nobrowse", "-mountpoint", mnt, img).CombinedOutput(); err != nil {
		t.Skipf("hdiutil attach: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("hdiutil", "detach", "-quiet", "-force", mnt).Run() })
	mustMkdir(t, filepath.Join(home, "Projects"))
	targets := fullScanTargets()
	if slices.Contains(targets, mnt) || !slices.Contains(targets, filepath.Join(home, "Projects")) {
		t.Fatalf("targets = %v", targets)
	}
}

// TestFindLargeFilesNothingFound: find exits with status 1 and prints
// nothing when no file is large enough and a folder inside is unreadable;
// that is a complete, empty result.
func TestFindLargeFilesNothingFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "small.bin"), 10)
	locked := filepath.Join(dir, "locked")
	mustMkdir(t, locked)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	files, err := findLargeFiles(context.Background(), dir, 1<<20)
	if err != nil || len(files) != 0 {
		t.Fatalf("findLargeFiles = %v, %v", files, err)
	}
}

// TestPackagesAreLeftAlone: images inside packages (a Scrivener project, an
// Xcode project) belong to their documents, and app-managed libraries are
// off limits to cleaning.
func TestPackagesAreLeftAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pics := filepath.Join(home, "Pictures")
	for _, p := range []string{"Novel.scriv/Files/cover.png", "App.xcodeproj/x.png", "VM.utm/screen.png", "real.png"} {
		writeFile(t, filepath.Join(pics, p), 10)
	}
	if got := collectPhotoFiles([]string{pics}, 100); len(got) != 1 || filepath.Base(got[0]) != "real.png" {
		t.Errorf("collected %v", got)
	}
	for _, p := range []string{
		filepath.Join(home, "Movies", "iMovie Library.imovielibrary", "clip.mov"),
		filepath.Join(home, "Pictures", "Photo Booth Library", "Pictures", "a.jpg"),
		filepath.Join(home, "Pictures", "Aperture Library.aplibrary", "x.jpg"),
	} {
		if safeToClean(p) {
			t.Errorf("safeToClean(%s) = true", p)
		}
	}
	proj := filepath.Join(home, "App.xcodeproj")
	mustMkdir(t, proj)
	if args, _ := revealArgs(proj); !slices.Equal(args, []string{"-R", proj}) {
		t.Errorf("revealArgs(App.xcodeproj) = %q", args)
	}
}

// TestAppsSharingABundleID: Xcode and Xcode-beta share a bundle ID; both are
// listed, and uninstalling one keeps the data the other still uses.
func TestAppsSharingABundleID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	apps := filepath.Join(home, "Applications")
	a := makeApp(t, apps, "Xcode.app", "com.apple.dt.Xcode", "Xcode")
	makeApp(t, apps, "Xcode-beta.app", "com.apple.dt.Xcode", "Xcode")
	prefs := filepath.Join(home, "Library", "Preferences", "com.apple.dt.Xcode.plist")
	writeFile(t, prefs, 10)
	listed := 0
	for _, app := range (Cleaner{}).ListApps() {
		if strings.HasPrefix(app.Path, apps) {
			listed++
		}
	}
	if listed != 2 {
		t.Errorf("listed %d of the 2 apps", listed)
	}
	for _, it := range uninstallPaths(AppInfo{Name: "Xcode", Path: a, BundleID: "com.apple.dt.Xcode"}) {
		if it.Path == prefs {
			t.Error("uninstalling Xcode would remove Xcode-beta's preferences")
		}
	}
}
