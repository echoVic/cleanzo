package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestClaimsKeepRuleFoldersOutOfAppItems checks junk expansion.
func TestClaimsKeepRuleFoldersOutOfAppItems(t *testing.T) {
	env := testEnv(t)
	caches := filepath.Join(env.home, "Library/Caches")
	writeFile(t, filepath.Join(caches, "Google/Chrome/Default/Cache/data_1"), 1000)
	writeFile(t, filepath.Join(caches, "Google/Other/blob"), 1000)
	writeFile(t, filepath.Join(caches, "cn.trae.app.ShipIt/update.zip"), 1000)
	writeFile(t, filepath.Join(caches, "go-build/ab/cd"), 1000)
	env.settings.Ignored = []string{"browser.chrome"} // ignored, still claims
	env.settings.DisabledGroups = []string{"updates"} // off, still claims
	specs, _ := expandItems(&env)
	for _, s := range specs {
		for _, c := range s.cands {
			if strings.Contains(c.path, "Google/Chrome") || strings.Contains(c.path, "ShipIt") {
				t.Fatalf("%s reaches %s, which belongs to an ignored or disabled rule", s.item.ID, c.path)
			}
		}
	}
	ids := itemIDs(specs)
	if !slices.Contains(ids, "app:dir:google") || !slices.Contains(ids, "dev.go-build") || slices.Contains(ids, "browser.chrome") {
		t.Fatalf("items = %v", ids)
	}
}

// TestAppItemsMergeSourcesByOwner checks junk expansion.
func TestAppItemsMergeSourcesByOwner(t *testing.T) {
	env := testEnv(t)
	appsDir := t.TempDir()
	makeApp(t, appsDir, "Trae CN.app", "cn.trae.app", "Trae CN")
	makeApp(t, appsDir, "Spotify.app", "com.spotify.client", "Spotify")
	env.apps = buildAppIndex([]string{appsDir})
	writeFile(t, filepath.Join(env.home, "Library/Caches/cn.trae.app/a"), 100)
	writeFile(t, filepath.Join(env.home, "Library/Application Support/Trae CN/Cache/b"), 100)
	writeFile(t, filepath.Join(env.home, "Library/Application Support/Trae CN/Service Worker/c"), 100) // not a cache folder
	writeFile(t, filepath.Join(env.home, "Library/Containers/com.spotify.client/Data/Library/Caches/d"), 100)
	specs, _ := expandItems(&env)
	var trae, spotify *itemSpec
	for i := range specs {
		switch specs[i].item.ID {
		case "app:cn.trae.app":
			trae = &specs[i]
		case "app:com.spotify.client":
			spotify = &specs[i]
		}
	}
	if trae == nil || trae.item.Name != "Trae CN" || len(trae.cands) != 2 {
		t.Fatalf("trae = %+v", trae)
	}
	if spotify == nil || spotify.item.Default {
		t.Fatalf("spotify should be listed and unselected: %+v", spotify)
	}
}

// TestNoFullDiskAccessStaysOutOfContainers checks junk expansion.
func TestNoFullDiskAccessStaysOutOfContainers(t *testing.T) {
	env := testEnv(t)
	env.fda = false
	data := filepath.Join(env.home, "Library/Containers/com.tencent.xinWeChat/Data")
	writeFile(t, filepath.Join(data, "Library/Caches/x"), 100)
	if err := os.Chmod(data, 0); err != nil { // a read would fail loudly
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(data, 0o755) })
	specs, sum := expandItems(&env)
	if sum.Containers != 1 || sum.FullDiskAccess {
		t.Fatalf("summary = %+v", sum)
	}
	for _, s := range specs {
		if strings.Contains(strings.Join(s.item.Paths, ","), "Containers") {
			t.Fatalf("%s reached into a container without access", s.item.ID)
		}
	}
}

// TestHomebrewKeepsItsIndex: only Homebrew's downloads are cleaned; its API
// index and compiled code stay, and no app row picks them up.
func TestHomebrewKeepsItsIndex(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/Homebrew/downloads/x.tar.gz"), 100)
	writeFile(t, filepath.Join(env.home, "Library/Caches/Homebrew/api/formula.jws.json"), 100)
	specs, _ := expandItems(&env)
	for _, s := range specs {
		for _, c := range s.cands {
			if strings.Contains(c.path, "Homebrew/api") {
				t.Fatalf("%s reaches %s", s.item.ID, c.path)
			}
		}
	}
	if ids := itemIDs(specs); !slices.Equal(ids, []string{"dev.homebrew"}) {
		t.Fatalf("items = %v", ids)
	}
}

// TestDeviceSupportKeepsNewestPerPlatform checks item measurement.
func TestDeviceSupportKeepsNewestPerPlatform(t *testing.T) {
	env := testEnv(t)
	ios := filepath.Join(env.home, "Library/Developer/Xcode/iOS DeviceSupport")
	for _, v := range []string{"17.2 (21C62)", "iPhone15,2 17.5.1 (21F90)", "18.0 (22A3354)"} {
		writeFile(t, filepath.Join(ios, v, "Symbols/x"), 100)
	}
	writeFile(t, filepath.Join(env.home, "Library/Developer/Xcode/watchOS DeviceSupport/10.4 (21T216)/Symbols/x"), 100)
	specs, _ := expandItems(&env)
	var p itemPlan
	for _, s := range specs {
		if s.item.ID == "review.device-support" {
			p = planItem(context.Background(), s, env, make(chan struct{}, 4), nil)
		}
	}
	keep := map[string]bool{}
	for _, s := range p.item.Subs {
		if !s.Default {
			keep[filepath.Base(s.Path)] = true
		}
	}
	if len(p.item.Subs) != 4 || !keep["18.0 (22A3354)"] || !keep["10.4 (21T216)"] || len(keep) != 2 {
		t.Fatalf("subs = %+v", p.item.Subs)
	}
	if p.item.Default {
		t.Fatal("review items start unselected")
	}
}

func TestInstallersNeverFollowLinks(t *testing.T) {
	env := testEnv(t)
	installerTestClock(t)
	real := filepath.Join(env.home, "Pictures/keep.dmg")
	writeFile(t, real, 100)
	writeFile(t, filepath.Join(env.home, "Downloads/ready.dmg"), 100)
	mustMkdir(t, filepath.Join(env.home, "Desktop"))
	mustMkdir(t, filepath.Join(env.home, "Documents"))
	for link, target := range map[string]string{"Downloads/link.dmg": real, "Desktop/linked": filepath.Dir(real), "Documents/loop": filepath.Join(env.home, "Documents")} {
		if err := os.Symlink(target, filepath.Join(env.home, link)); err != nil {
			t.Fatal(err)
		}
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.installers"}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Trashed != 100 || !exists(real) {
		t.Fatalf("link traversal: %+v", res)
	}
	for _, link := range []string{"Downloads/link.dmg", "Desktop/linked", "Documents/loop"} {
		if info, err := os.Lstat(filepath.Join(env.home, link)); err != nil || info.Mode()&fs.ModeSymlink == 0 {
			t.Fatalf("link was changed: %s", link)
		}
	}
}

// TestPlanItemReadsBackupAndArchiveMetadata checks top-level strings and dates.
func TestPlanItemReadsBackupAndArchiveMetadata(t *testing.T) {
	for _, tc := range []struct{ id, path, nameKey, dateKey, name string }{
		{"review.ios-backups", "Library/Application Support/MobileSync/Backup/abc", "Device Name", "Last Backup Date", "小明的 iPhone"},
		{"review.xcode-archives", "Library/Developer/Xcode/Archives/2026-09-01/App.xcarchive", "Name", "CreationDate", "My App"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			env := testEnv(t)
			root := filepath.Join(env.home, tc.path)
			mustMkdir(t, root)
			data := `<?xml version="1.0"?><plist version="1.0"><dict><key>` + tc.nameKey + `</key><string>` + tc.name + `</string><key>` + tc.dateKey + `</key><date>2026-09-01T10:00:00Z</date><key>nested</key><dict><key>` + tc.nameKey + `</key><string>wrong</string></dict></dict></plist>`
			if err := os.WriteFile(filepath.Join(root, "Info.plist"), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
			specs, _ := expandItems(&env)
			for _, spec := range specs {
				if spec.item.ID == tc.id {
					p := planItem(context.Background(), spec, env, make(chan struct{}, 4), nil)
					if len(p.item.Subs) != 1 || p.item.Subs[0].Name != tc.name || !strings.HasPrefix(p.item.Subs[0].Detail, "2026-09-01") {
						t.Fatalf("subs = %+v", p.item.Subs)
					}
					return
				}
			}
			t.Fatal("item missing")
		})
	}
}

// TestNoFullDiskAccessBlocksCustomContainers checks custom roots cannot bypass permissions.
func TestNoFullDiskAccessBlocksCustomContainers(t *testing.T) {
	env := testEnv(t)
	data := filepath.Join(env.home, "Library/Containers/com.vendor.app/Data")
	writeFile(t, filepath.Join(data, "Library/Caches/x"), 100)
	env.fda = false
	env.settings.Custom = []CustomFolder{{Name: "custom", Path: data, Enabled: true}}
	specs, _ := expandItems(&env)
	for _, spec := range specs {
		for _, c := range spec.cands {
			if strings.Contains(c.path, "/Containers/") {
				t.Fatalf("custom enters container: %+v", spec)
			}
		}
	}
}

// TestNoFullDiskAccessBlocksContainerAliases checks symlinks cannot bypass the boundary.
func TestNoFullDiskAccessBlocksContainerAliases(t *testing.T) {
	env := testEnv(t)
	env.fda = false
	data := filepath.Join(env.home, "Library/Containers/com.vendor.app/Data")
	writeFile(t, filepath.Join(data, "Library/Caches/x"), 100)
	caches := filepath.Join(env.home, "Library/Caches")
	mustMkdir(t, caches)
	if err := os.Symlink(filepath.Join(data, "Library/Caches"), filepath.Join(caches, "Alias")); err != nil {
		t.Fatal(err)
	}
	specs, _ := expandItems(&env)
	for _, spec := range specs {
		for _, c := range spec.cands {
			if strings.Contains(c.path, "/Containers/") {
				t.Fatalf("alias entered container: %+v", spec)
			}
		}
	}
}

// TestRuleCannotFollowCacheIntoDocuments checks protected roots behind cache links.
func TestRuleCannotFollowCacheIntoDocuments(t *testing.T) {
	env := testEnv(t)
	docs := filepath.Join(env.home, "Documents")
	writeFile(t, filepath.Join(docs, "keep.bin"), 100)
	mustMkdir(t, filepath.Join(env.home, ".npm"))
	if err := os.Symlink(docs, filepath.Join(env.home, ".npm/_npx")); err != nil {
		t.Fatal(err)
	}
	specs, _ := expandItems(&env)
	for _, spec := range specs {
		if spec.item.ID == "dev.npx" && len(spec.cands) > 0 {
			t.Fatalf("rule enters Documents: %+v", spec)
		}
	}
}

// TestCleanJunkLeavesCacheSymlinkTargets checks resolved cache links never delete work.
func TestCleanJunkLeavesCacheSymlinkTargets(t *testing.T) {
	env := testEnv(t)
	work := filepath.Join(env.home, "Documents/work")
	writeFile(t, filepath.Join(work, "keep.txt"), 100)
	caches := filepath.Join(env.home, "Library/Caches")
	mustMkdir(t, caches)
	if err := os.Symlink(work, filepath.Join(caches, "Alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(work, filepath.Join(caches, "Example.ShipIt")); err != nil {
		t.Fatal(err)
	}
	cleanJunk(context.Background(), env, []JunkSelection{{ID: "app:dir:alias"}, {ID: "update.shipit"}}, time.Time{}, cleanHooks{})
	if !exists(filepath.Join(work, "keep.txt")) {
		t.Fatal("cache symlink target was removed")
	}
}

// TestExpansionDoesNotStatThroughContainerAliases checks the permission boundary before globbing.
func TestExpansionDoesNotStatThroughContainerAliases(t *testing.T) {
	env := testEnv(t)
	env.fda = false
	data := filepath.Join(env.home, "Library/Containers/com.vendor.app/Data")
	writeFile(t, filepath.Join(data, "uv/wheel"), 100)
	if err := os.Symlink(data, filepath.Join(env.home, ".cache")); err != nil {
		t.Fatal(err)
	}
	env.lstat = func(path string) (fs.FileInfo, error) {
		if strings.Contains(path, "/.cache/") || strings.Contains(path, "/com.vendor.app/Data") {
			t.Fatalf("stat crossed alias or container: %s", path)
		}
		return os.Lstat(path)
	}
	expandItems(&env)
}

// TestPlanItemRejectsAnAncestorChangedToContainerAlias checks the boundary after expansion.
func TestPlanItemRejectsAnAncestorChangedToContainerAlias(t *testing.T) {
	env := testEnv(t)
	env.fda = false
	root := filepath.Join(env.home, ".npm/_npx")
	writeFile(t, filepath.Join(root, "entry/x"), 100)
	specs, _ := expandItems(&env)
	data := filepath.Join(env.home, "Library/Containers/com.vendor.app/Data")
	writeFile(t, filepath.Join(data, "entry/x"), 100)
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(data, root); err != nil {
		t.Fatal(err)
	}
	for _, spec := range specs {
		if spec.item.ID == "dev.npx" {
			p := planItem(context.Background(), spec, env, make(chan struct{}, 4), nil)
			if len(p.entries) != 0 || p.item.Size != 0 {
				t.Fatalf("measured through changed ancestor: %+v", p)
			}
		}
	}
}

// TestExplicitSelectionChecksCandidatePrefixes checks the filter guards every candidate before resolution.
func TestExplicitSelectionChecksCandidatePrefixes(t *testing.T) {
	env := testEnv(t)
	env.fda = false
	root := filepath.Join(env.home, ".npm/_npx")
	writeFile(t, filepath.Join(root, "a/x"), 100)
	writeFile(t, filepath.Join(root, "b/y"), 100)
	specs, _ := expandItems(&env)
	data := filepath.Join(env.home, "Library/Containers/com.vendor.app/Data")
	writeFile(t, filepath.Join(data, "x"), 100)
	if err := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "a-old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(data, filepath.Join(root, "a")); err != nil {
		t.Fatal(err)
	}
	checkedAlias := false
	env.lstat = func(path string) (fs.FileInfo, error) {
		if path == filepath.Join(resolvePath(env.home), ".npm/_npx/a") {
			checkedAlias = true
		}
		return os.Lstat(path)
	}
	for _, spec := range specs {
		if spec.item.ID == "dev.npx" {
			var bytes int64
			cleanItem(context.Background(), spec, env, []string{filepath.Join(root, "b")}, time.Time{}, cleanHooks{}, &bytes)
		}
	}
	if !checkedAlias {
		t.Fatal("candidate filter did not guard the changed prefix before resolving it")
	}
	if !exists(filepath.Join(data, "x")) {
		t.Fatal("container target changed")
	}
}

// TestMeasureCountsSpaceOnDisk: a sparse file counts what it occupies on
// disk, not its length, so a preallocated cache does not report gigabytes it
// never used (as Quark's video cache did).
func TestMeasureCountsSpaceOnDisk(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "video.cache"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(8 << 30); err != nil {
		t.Fatal(err)
	}
	f.Close()
	writeFile(t, filepath.Join(dir, "real.bin"), 4096)
	size, files := scanPath(dir)
	if files != 2 || size < 4096 || size > 1<<20 {
		t.Fatalf("size = %d, files = %d; want the 4 KB actually stored", size, files)
	}
}

// TestScanJunkSummaryCountsLeftovers checks the review summary and its group switch.
func TestScanJunkSummaryCountsLeftovers(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Application Support/ZzxqAbandoned/data"), 2<<20)
	_, sum := scanJunk(context.Background(), env, nil, nil)
	if sum.Leftovers != 1 || sum.LeftoversSize < 1<<20 {
		t.Fatalf("leftover summary = %+v", sum)
	}
	env.settings.DisabledGroups = []string{"review"}
	_, sum = scanJunk(context.Background(), env, nil, nil)
	if sum.Leftovers != 0 || sum.LeftoversSize != 0 {
		t.Fatalf("disabled review summary = %+v", sum)
	}
}

// TestScanJunkSummaryWithoutFDAExcludesContainers keeps unprotected leftovers visible.
func TestScanJunkSummaryWithoutFDAExcludesContainers(t *testing.T) {
	env := testEnv(t)
	env.fda = false
	writeFile(t, filepath.Join(env.home, "Library/Application Support/ZzxqAbandoned/data"), 2<<20)
	writeFile(t, filepath.Join(env.home, "Library/Containers/com.zzxq.abandoned/Data/data"), 4<<20)
	_, sum := scanJunk(context.Background(), env, nil, nil)
	if sum.Leftovers != 1 || sum.LeftoversSize < 1<<20 || sum.LeftoversSize >= 4<<20 {
		t.Fatalf("must count support leftovers without reading containers: %+v", sum)
	}
}

// TestScanJunkSummaryWithoutFDASkipsLinks rejects aliases into another app's data.
func TestScanJunkSummaryWithoutFDASkipsLinks(t *testing.T) {
	for _, kind := range []string{"entry", "root"} {
		t.Run(kind, func(t *testing.T) {
			env := testEnv(t)
			env.fda = false
			container := filepath.Join(env.home, "Library/Containers/com.zzxq.abandoned/Data")
			file := filepath.Join(container, "ZzxqAbandoned/data")
			writeFile(t, file, 2<<20)
			link, target := filepath.Join(env.home, "Library/Preferences/ZzxqAbandoned.plist"), file
			if kind == "root" {
				link, target = filepath.Join(env.home, "Library/Application Support"), container
			}
			mustMkdir(t, filepath.Dir(link))
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			_, sum := scanJunk(context.Background(), env, nil, nil)
			if sum.Leftovers != 0 || sum.LeftoversSize != 0 {
				t.Fatalf("container alias was read: %+v", sum)
			}
		})
	}
}

// TestExpansionMemoResolvesLikeResolvePath: while an expansion runs, paths
// resolve through a memo; it must give the same answers as plain
// resolution, through linked folders and for paths that do not exist.
func TestExpansionMemoResolvesLikeResolvePath(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	writeFile(t, filepath.Join(real, "sub", "f"), 10)
	if err := os.Symlink(real, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "dangling")); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		filepath.Join(dir, "link", "sub", "f"),
		filepath.Join(dir, "link", "sub", "missing", "x"),
		filepath.Join(dir, "real", "sub"),
		filepath.Join(dir, "dangling", "y"),
		filepath.Join(dir, "absent", "z"),
	}
	var want []string
	for _, p := range paths {
		want = append(want, resolvePath(p))
	}
	stop := startExpandMemo()
	var got []string
	for range 2 { // the second round answers from the memo
		got = got[:0]
		for _, p := range paths {
			got = append(got, resolvePath(p))
		}
	}
	stop()
	if !slices.Equal(got, want) {
		t.Fatalf("memo resolved %v, want %v", got, want)
	}
	if expandMemoActive() {
		t.Fatal("the memo must switch off when the expansion ends")
	}
}

// TestClaimSet: a path is claimed by itself or an ancestor, and a folder
// with a claim somewhere below it is told apart from a claimed one.
func TestClaimSet(t *testing.T) {
	var c claimSet
	c.claimKey("/u/a/b")
	if !c.claimedKey("/u/a/b") || !c.claimedKey("/u/a/b/c") || c.claimedKey("/u/a") || c.claimedKey("/u/a/bb") {
		t.Fatal("claimedKey is off")
	}
	if !c.insideKey("/u/a") || !c.insideKey("/u") || c.insideKey("/u/a/b") || c.insideKey("/u/x") {
		t.Fatal("insideKey is off")
	}
	d := c.clone()
	d.claimKey("/u/x")
	if c.claimedKey("/u/x") || !d.claimedKey("/u/x") {
		t.Fatal("clone shares state")
	}
}

func TestInstallersScanCommonFoldersRecursively(t *testing.T) {
	env := testEnv(t)
	installerTestClock(t)
	want := []string{"Downloads/old.dmg", "Downloads/nested/deep/disk.DMG", "Desktop/packages/setup.pkg", "Documents/tools/archive.xip", "Documents/Legacy.mpkg/Contents/payload"}
	for _, path := range want {
		writeFile(t, filepath.Join(env.home, path), 100)
	}
	for _, path := range []string{"Downloads/archive.zip", "Desktop/App.app/Contents/embedded.dmg", "Documents/Book.pages/embedded.pkg", "Documents/.hidden/hidden.dmg", "Documents/not-a-package.dmg/keep.txt", "Pictures/outside.dmg"} {
		writeFile(t, filepath.Join(env.home, path), 500)
	}
	writeFile(t, filepath.Join(env.home, "Documents/new.pkg"), 100)
	if err := os.Chtimes(filepath.Join(env.home, "Documents/new.pkg"), now(), now()); err != nil {
		t.Fatal(err)
	}
	items, _ := scanJunk(context.Background(), env, nil, nil)
	for _, it := range items {
		if it.ID != "review.installers" {
			continue
		}
		if it.Default || !it.Trash || it.Size != 600 || len(it.Subs) != 6 {
			t.Fatalf("recursive installer row = %+v", it)
		}
		var got []string
		for _, sub := range it.Subs {
			rel, _ := filepath.Rel(resolvePath(env.home), sub.Path)
			got = append(got, rel)
		}
		want[4] = "Documents/Legacy.mpkg"
		want = append(want, "Documents/new.pkg")
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("subs = %v; want %v", got, want)
		}
		return
	}
	t.Fatal("no installers row")
}

// TestExpansionScalesWithManyEntries: a real $TMPDIR held 52,000 entries,
// and checking each against every earlier claim one by one took 12 s before
// the first screen. Checks must not grow with the number of claims.
func TestExpansionScalesWithManyEntries(t *testing.T) {
	if raceDetector {
		t.Skip("timing means nothing under the race detector")
	}
	env := testEnv(t)
	for i := range 3000 { // earlier claims: a big bun cache
		mustMkdir(t, filepath.Join(env.home, ".bun/install/cache", fmt.Sprintf("pkg%05d", i)))
	}
	for i := range 20000 {
		mustMkdir(t, filepath.Join(env.tmp, fmt.Sprintf("t%05d", i)))
	}
	start := time.Now()
	specs, _ := expandItems(&env)
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("expansion took %s", took)
	}
	var temp int
	for _, s := range specs {
		if s.item.ID == "sys.temp" {
			temp = len(s.cands)
		}
	}
	if temp != 20000 {
		t.Fatalf("temp candidates = %d", temp)
	}
}
