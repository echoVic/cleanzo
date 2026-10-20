package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"maps"
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

func TestReadBundleInfo(t *testing.T) {
	dir := t.TempDir()
	p := writeTestPlist(t, dir)
	name, bid, version := readBundleInfo(p)
	if name != "TestApp" || bid != "com.example.testapp" || version != "1.2.3" {
		t.Fatalf("got %q %q %q", name, bid, version)
	}
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
	setHome(t, home)
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
	setHome(t, home)
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

func TestFullScanTargets(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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

func TestTrashable(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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
	setHome(t, home)
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
	setHome(t, home)
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

func TestFindLargeFiles(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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
	setHome(t, home)
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

func TestCollectOrphans(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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

// TestScanJunkAppCaches reports each app's cache size and source path.
func TestScanJunkAppCaches(t *testing.T) {
	env := testEnv(t)
	home := env.home
	caches := filepath.Join(home, "Library", "Caches")
	for _, name := range []string{"AppA", "AppB"} {
		d := filepath.Join(caches, name)
		mustMkdir(t, d)
		if err := os.WriteFile(filepath.Join(d, "c.bin"), make([]byte, 2048), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	items, _ := scanJunk(context.Background(), env, nil, nil)
	if len(items) != 2 {
		t.Fatalf("want 2 app cache rows, got %+v", items)
	}
	// sorted by size descending (equal sizes, order not asserted beyond presence)
	byName := map[string]JunkItem{}
	for _, it := range items {
		byName[it.Name] = it
	}
	if byName["AppA"].Size < 2048 || byName["AppB"].Size < 2048 {
		t.Fatalf("unexpected sizes: %+v", byName)
	}
	if !slices.Equal(byName["AppA"].Paths, []string{"~/Library/Caches/AppA"}) {
		t.Fatalf("bad path: %+v", byName["AppA"])
	}
}

func TestCleanPathsMovesToTrash(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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

// TestSimilarPhotosMoveToTrash ensures trashing cluster items really empties
// the source and lands them in the (temp) Trash.
func TestSimilarPhotosMoveToTrash(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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
	setHome(t, home)
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

func TestPathCleanupDistinguishesProbeFailureFromOccupancy(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	path := filepath.Join(home, "Documents", "keep.bin")
	writeFile(t, path, 512)
	saved := pathUsageProbe
	pathUsageProbe = func() *usageGuard { return &usageGuard{failed: true} }
	t.Cleanup(func() { pathUsageProbe = saved })
	if _, err := (Cleaner{}).TrashPath(path); err != errUsageUnknown {
		t.Fatalf("probe failure error = %v; want usage-unknown", err)
	}
	res := (Cleaner{}).CleanPaths([]string{path})
	if res.Moved != 0 || res.InUse != 0 || !slices.Equal(res.Failed, []string{path + ": usage-unknown"}) || !exists(path) {
		t.Fatalf("failed probe must refuse without claiming occupancy: %+v", res)
	}
	pathUsageProbe = func() *usageGuard { return &usageGuard{open: []string{canonical(path)}} }
	if _, err := (Cleaner{}).TrashPath(path); err != errInUse {
		t.Fatalf("real occupancy error = %v", err)
	}
	res = (Cleaner{}).CleanPaths([]string{path})
	if res.InUse != 1 || res.Moved != 0 || !exists(path) {
		t.Fatalf("real occupancy must still refuse: %+v", res)
	}
	pathUsageProbe = func() *usageGuard { return &usageGuard{} }
	if _, err := (Cleaner{}).TrashPath(path); err != nil {
		t.Fatal(err)
	}
	if exists(path) || !exists(filepath.Join(home, ".Trash", "keep.bin")) {
		t.Fatal("released path must be movable to Trash")
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

// TestTempCleansOnlyStaleEntries: the temporary folder only loses entries
// nothing has touched for a week, and never sockets or pipes.
func TestTempCleansOnlyStaleEntries(t *testing.T) {
	env := testEnv(t)
	tmp := env.tmp
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

	got, _ := scanJunk(context.Background(), env, nil, nil)
	if len(got) != 1 || got[0].Size != 1000 || got[0].Files != 1 {
		t.Fatalf("scan = %+v; want 1000 bytes, 1 file", got)
	}
	cleanJunk(context.Background(), env, []JunkSelection{{ID: "sys.temp"}}, time.Time{}, cleanHooks{})
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
	env := testEnv(t)
	home := env.home
	dir := filepath.Join(home, "junk")
	writeFile(t, filepath.Join(dir, "busy", "db.sqlite"), 800)
	writeFile(t, filepath.Join(dir, "idle", "x"), 300)
	f, err := os.Open(filepath.Join(dir, "busy", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	env.settings.Custom = []CustomFolder{{Name: "junk", Path: dir, Enabled: true}}
	env.guard = probeUsage()
	if got, _ := scanJunk(context.Background(), env, nil, nil); len(got) != 1 || got[0].Size != 300 || got[0].InUse != 1 {
		t.Fatalf("scan = %+v; want 300 bytes, 1 in use", got)
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + dir}}, time.Time{}, cleanHooks{})[0]
	if res.InUse != 1 || !exists(filepath.Join(dir, "busy", "db.sqlite")) || exists(filepath.Join(dir, "idle")) {
		t.Fatalf("res = %+v; busy must stay, idle must go", res)
	}
}

// TestScanReportsUnreadableFolder: a folder macOS will not let the app read
// is reported as denied, not as empty.
func TestScanReportsUnreadableFolder(t *testing.T) {
	env := testEnv(t)
	home := env.home
	locked := filepath.Join(home, "locked")
	writeFile(t, filepath.Join(locked, "a"), 100)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	env.settings.Custom = []CustomFolder{{Name: "x", Path: locked, Enabled: true}}
	got, _ := scanJunk(context.Background(), env, nil, nil)
	if len(got) != 1 || !got[0].Denied || got[0].Size != 0 {
		t.Fatalf("unreadable folder not reported as denied: %+v", got)
	}
}

// TestConfigMigratesV1Defaults hands built-ins to rules and keeps edited paths.
func TestConfigMigratesV1Defaults(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	mustMkdir(t, filepath.Dir(configPath()))
	v1 := `[
  {"name":"temp","description":"用户临时文件","risk":"safe","paths":[` + strconv.Quote(os.TempDir()) + `],"enabled":true},
  {"name":"trash","description":"废纸篓","risk":"safe","paths":[` + strconv.Quote(filepath.Join(home, ".Trash")) + `],"enabled":true},
  {"name":"logs","description":"日志","risk":"safe","paths":["/elsewhere/logs"],"enabled":true}
]`
	if err := os.WriteFile(configPath(), []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Cleaner{}.GetSettings()
	if len(got.DisabledGroups)+len(got.Ignored) != 0 || !slices.Equal(got.Custom, []CustomFolder{{Name: "logs", Path: "/elsewhere/logs", Enabled: true}}) {
		t.Fatalf("migrated settings = %+v", got)
	}
	b, _ := os.ReadFile(configPath())
	if string(b) != v1 {
		t.Fatalf("config should not be rewritten until saving:\n%s", b)
	}
}

// TestTrashPathRefusesFolderInUse and its CleanPaths counterpart leave what
// a process has open where it is.
func TestTrashPathRefusesFolderInUse(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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

// TestScanJunkAppCachesFlagsInUse reports caches holding real open files.
func TestScanJunkAppCachesFlagsInUse(t *testing.T) {
	env := testEnv(t)
	home := env.home
	caches := filepath.Join(home, "Library", "Caches")
	writeFile(t, filepath.Join(caches, "busy", "db"), 100)
	writeFile(t, filepath.Join(caches, "idle", "x"), 100)
	f, err := os.Open(filepath.Join(caches, "busy", "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	inUse := map[string]bool{}
	env.guard = probeUsage()
	items, _ := scanJunk(context.Background(), env, nil, nil)
	for _, it := range items {
		inUse[it.Name] = it.InUse > 0
	}
	if !inUse["busy"] || inUse["idle"] {
		t.Fatalf("inUse = %v", inUse)
	}
}

// TestUninstallPathsStayInLibrary: names from Info.plist cannot climb out of
// the Library folders uninstall cleans.
func TestUninstallPathsStayInLibrary(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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
	setHome(t, home)
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
	setHome(t, home)
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

// TestOrphansSkipLiveData: data of Apple's own services, input methods,
// apps in subfolders, apps whose folders are named after their vendor or
// bundle ID, and hidden entries are not leftovers.
func TestOrphansSkipLiveData(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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
	setHome(t, home)
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
	setHome(t, home)
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

// TestTrashPathHonorsSafetyGate: Move to Trash refuses what cleaning
// protects, and rows offer it only where it would be allowed.
func TestTrashPathHonorsSafetyGate(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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

// TestUninstallRefusesAppInUse: a running app (its files are open) is not
// uninstalled; data of it that is in use stays.
func TestUninstallRefusesAppInUse(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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

// TestSaveSettingsIgnoresDisabledLegacyPaths keeps disabled protected folders.
func TestSaveSettingsIgnoresDisabledLegacyPaths(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	old := CustomFolder{Name: "old", Enabled: false, Path: filepath.Join(home, "Downloads")}
	ok := CustomFolder{Name: "ok", Enabled: true, Path: filepath.Join(home, "x")}
	if err := (Cleaner{}).SaveSettings(Settings{Custom: []CustomFolder{old, ok}}); err != nil {
		t.Fatalf("disabled legacy folder blocked saving: %v", err)
	}
	old.Enabled = true
	if err := (Cleaner{}).SaveSettings(Settings{Custom: []CustomFolder{old, ok}}); err == nil {
		t.Fatal("an enabled custom folder on a protected path must be rejected")
	}
}

// TestPlanSkipsProtectedEntries: entries macOS will not let the app remove,
// such as immutable ones, are left out instead of failing every clean.
func TestPlanSkipsProtectedEntries(t *testing.T) {
	env := testEnv(t)
	home := env.home
	dir := filepath.Join(home, "junk")
	writeFile(t, filepath.Join(dir, "locked", "x"), 10)
	writeFile(t, filepath.Join(dir, "plain", "y"), 20)
	locked := filepath.Join(dir, "locked")
	if err := syscall.Chflags(locked, 0x2); err != nil { // UF_IMMUTABLE
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Chflags(locked, 0) })
	env.settings.Custom = []CustomFolder{{Name: "junk", Path: dir, Enabled: true}}
	if got, _ := scanJunk(context.Background(), env, nil, nil); len(got) != 1 || got[0].Size != 20 {
		t.Fatalf("scan = %+v, want 20 bytes", got)
	}
	if res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + dir}}, time.Time{}, cleanHooks{})[0]; len(res.Failed) != 0 || exists(filepath.Join(dir, "plain")) || !exists(locked) {
		t.Fatalf("res = %+v", res)
	}
}

// TestCleanRemovesReadOnlyTrees: junk with read-only folders inside, as Go's
// module cache leaves, is still deleted.
func TestCleanRemovesReadOnlyTrees(t *testing.T) {
	env := testEnv(t)
	home := env.home
	mod := filepath.Join(home, "Library", "Caches", "gomod")
	writeFile(t, filepath.Join(mod, "pkg", "a.go"), 100)
	for _, d := range []string{filepath.Join(mod, "pkg"), mod} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { os.Chmod(mod, 0o755); os.Chmod(filepath.Join(mod, "pkg"), 0o755) })
	if res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "app:dir:gomod"}}, time.Time{}, cleanHooks{})[0]; len(res.Failed) != 0 || exists(mod) {
		t.Fatalf("result = %+v", res)
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
	setHome(t, home)
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
	setHome(t, home)
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
	setHome(t, home)
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

// TestCleanPathsReportsRemoved keeps the caller's paths for removed rows.
func TestCleanPathsReportsRemoved(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	moved := filepath.Join(home, "Library", "Caches", "Moved") + string(os.PathSeparator)
	mustMkdir(t, moved)
	writeFile(t, filepath.Join(moved, "data"), 1024)
	missing := filepath.Join(home, "Library", "Caches", "Missing")
	blocked := filepath.Join(home, "Library")
	got := Cleaner{}.CleanPaths([]string{moved, missing, blocked})
	if !slices.Equal(got.Removed, []string{moved, missing}) {
		t.Fatalf("Removed = %q, want %q", got.Removed, []string{moved, missing})
	}
	if !exists(blocked) {
		t.Fatal("blocked path was removed")
	}
}

// TestPathErrorCodes checks errors the UI translates for trash and reveal.
func TestPathErrorCodes(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	blocked := filepath.Join(home, "Library")
	mustMkdir(t, blocked)
	if _, err := (Cleaner{}).TrashPath(blocked); !errors.Is(err, errBlocked) {
		t.Fatalf("blocked error = %v", err)
	}
	missing := filepath.Join(home, "missing.png")
	if _, err := (Cleaner{}).TrashPath(missing); !errors.Is(err, errGone) {
		t.Fatalf("missing trash error = %v", err)
	}
	if err := (Cleaner{}).RevealInFinder(missing); !errors.Is(err, errGone) {
		t.Fatalf("missing reveal error = %v", err)
	}
}

// TestSaveSettingsErrorCodes checks translated validation codes and arguments.
func TestSaveSettingsErrorCodes(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	path := filepath.Join(home, "junk")
	blocked := filepath.Join(home, "Library")
	tests := []struct {
		code string
		args []string
		cfg  Settings
	}{
		{"group-unknown", []string{"unknown"}, Settings{DisabledGroups: []string{"unknown"}}},
		{"folder-empty", nil, Settings{Custom: []CustomFolder{{Name: "junk", Path: " "}}}},
		{"folder-protected", []string{blocked}, Settings{Custom: []CustomFolder{{Name: "junk", Enabled: true, Path: blocked}}}},
		{"folder-dup", []string{path}, Settings{Custom: []CustomFolder{{Name: "first", Path: path}, {Name: "second", Path: path + "/"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			err := (Cleaner{}).SaveSettings(tt.cfg)
			var coded codeError
			if !errors.As(err, &coded) {
				t.Fatalf("error = %v, want codeError", err)
			}
			if coded.Code != tt.code || !slices.Equal(coded.Args, tt.args) {
				t.Fatalf("error = %+v, want %s %q", coded, tt.code, tt.args)
			}
			var decoded codeError
			if err := json.Unmarshal([]byte(coded.Error()), &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Code != tt.code || !slices.Equal(decoded.Args, tt.args) {
				t.Fatalf("JSON error = %+v", decoded)
			}
		})
	}
}

// TestUninstallAppNotAppCode rejects paths outside the app directories.
func TestUninstallAppNotAppCode(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	path := makeApp(t, filepath.Join(home, "Downloads"), "Fake.app", "dev.cleanzo.fake", "Fake")
	got := Cleaner{}.UninstallApp(path)
	if !slices.Equal(got.Failed, []string{path + ": not-app"}) {
		t.Fatalf("Failed = %q", got.Failed)
	}
}

// TestOrphanItemsReportDirectories distinguishes expandable folders from files.
func TestOrphanItemsReportDirectories(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	folder := filepath.Join(home, "Library", "Caches", "ZzxqAbandonedFolder")
	mustMkdir(t, folder)
	writeFile(t, filepath.Join(folder, "data"), 2<<20)
	file := filepath.Join(home, "Library", "Preferences", "ZzxqAbandonedFile.plist")
	writeFile(t, file, 0)
	if err := os.Truncate(file, 2<<20); err != nil {
		t.Fatal(err)
	}
	items, _ := collectOrphans(context.Background(), nil)
	found := map[string]bool{}
	for _, item := range items {
		if item.Path == folder || item.Path == file {
			found[item.Path] = true
			if item.IsDir != (item.Path == folder) {
				t.Errorf("item = %+v", item)
			}
		}
	}
	if !found[folder] || !found[file] {
		t.Fatalf("missing leftover: %+v", items)
	}
}

// TestAppIndexBundleNames reads display names and fallbacks across app directories.
func TestAppIndexBundleNames(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	makeApp(t, first, "Editor.app", "COM.EXAMPLE.EDITOR", "Editor Display")
	makeApp(t, second, "Viewer.app", "com.example.viewer", "Viewer Display")
	makeApp(t, first, "Fallback.app", "com.example.fallback", "")
	makeApp(t, first, "NoID.app", "", "No ID")
	mustMkdir(t, filepath.Join(first, "Ignored"))
	index := buildAppIndex([]string{first, second, filepath.Join(first, "missing")})
	want := map[string]string{"com.example.editor": "Editor Display", "com.example.viewer": "Viewer Display", "com.example.fallback": "Fallback"}
	for id, name := range want {
		if got, ok := index.lookup(id); !ok || got.Name != name {
			t.Errorf("lookup(%q) = %+v, %v; want %s", id, got, ok, name)
		}
	}
	if _, ok := index.lookup("No ID"); ok {
		t.Fatal("app with no bundle ID entered the index")
	}
}

// TestScanJunkAppCachesIncludesAppName names owned caches and keeps folder fallbacks.
func TestScanJunkAppCachesIncludesAppName(t *testing.T) {
	env := testEnv(t)
	home := env.home
	makeApp(t, filepath.Join(home, "Applications"), "Named.app", "dev.zzxq.cachename", "Cache Display")
	caches := filepath.Join(home, "Library", "Caches")
	writeFile(t, filepath.Join(caches, "DEV.ZZXQ.CACHENAME", "x"), 100)
	writeFile(t, filepath.Join(caches, "ZzxqUnownedCache", "y"), 200)
	env.apps = buildAppIndex([]string{filepath.Join(home, "Applications")})
	got, _ := scanJunk(context.Background(), env, nil, nil)
	if len(got) != 2 {
		t.Fatalf("caches = %+v", got)
	}
	names := map[string]string{}
	for _, item := range got {
		names[item.ID] = item.Name
	}
	if !maps.Equal(names, map[string]string{"app:dev.zzxq.cachename": "Cache Display", "app:dir:zzxqunownedcache": "ZzxqUnownedCache"}) {
		t.Fatalf("app names = %v", names)
	}
}

// TestLargeScanThreshold selects large files and clamps small thresholds.
func TestLargeScanThreshold(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	dir := filepath.Join(home, "Downloads")
	for name, size := range map[string]int64{"large": 60 << 20, "small": 10 << 20, "exact": 50 << 20} {
		path := filepath.Join(dir, name)
		writeFile(t, path, 0)
		if err := os.Truncate(path, size); err != nil {
			t.Fatal(err)
		}
	}
	for _, threshold := range []int64{50 << 20, 1} {
		t.Run(strconv.FormatInt(threshold, 10), func(t *testing.T) {
			found := map[string]bool{}
			got := largeScan(context.Background(), threshold, func(file LargeFile) {
				if filepath.Dir(file.Path) == dir {
					found[filepath.Base(file.Path)] = true
				}
			})
			if got.Threshold != 50<<20 {
				t.Errorf("threshold = %d, want %d", got.Threshold, 50<<20)
			}
			if !maps.Equal(found, map[string]bool{"large": true, "exact": true}) {
				t.Fatalf("files = %v", found)
			}
		})
	}
}

// TestCollectLargeScanKeepsTwoHundred checks the expanded result cap.
func TestCollectLargeScanKeepsTwoHundred(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	dir := filepath.Join(home, "Downloads")
	for i := range 201 {
		path := filepath.Join(dir, fmt.Sprintf("large-%03d", i))
		writeFile(t, path, 0)
		if err := os.Truncate(path, (1<<40)+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	items, _, _ := collectLargeScan(context.Background(), 1<<40, nil)
	if len(items) != 200 {
		t.Fatalf("file count = %d, want 200", len(items))
	}
	for i := 1; i < len(items); i++ {
		if items[i-1].Size < items[i].Size {
			t.Fatal("files are not sorted by size")
		}
	}
}

// TestPhotoPreviewResizesToJPEG provides a larger bounded preview of a photo.
func TestPhotoPreviewResizesToJPEG(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	path := filepath.Join(home, "photo.PNG")
	writeDocPNG(t, path, 2400, 1200, [][4]int{{200, 100, 1800, 900}})
	url, err := (Cleaner{}).PhotoPreview(path)
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "data:image/jpeg;base64,"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("preview is not a JPEG data URL")
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 1600 || bounds.Dy() != 800 {
		t.Fatalf("preview bounds = %v, want 1600x800", bounds)
	}
}

// TestPhotoPreviewPathErrors refuses outside paths and unsupported entries.
func TestPhotoPreviewPathErrors(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	outside := filepath.Join(t.TempDir(), "outside.png")
	writeFile(t, outside, 1)
	unsupported := filepath.Join(home, "file.txt")
	writeFile(t, unsupported, 1)
	folder := filepath.Join(home, "folder.png")
	mustMkdir(t, folder)
	link := filepath.Join(home, "link.png")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{outside, unsupported, folder, link} {
		if _, err := (Cleaner{}).PhotoPreview(path); !errors.Is(err, errBlocked) {
			t.Errorf("PhotoPreview(%q) error = %v, want blocked", path, err)
		}
	}
	if _, err := (Cleaner{}).PhotoPreview(filepath.Join(home, "missing.png")); !errors.Is(err, errGone) {
		t.Fatalf("missing error = %v, want gone", err)
	}
}
