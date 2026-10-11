package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// testEnv stages a junk scan in a temporary home with no apps, nothing in
// use and Full Disk Access granted.
func testEnv(t *testing.T) junkEnv {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tmp := filepath.Join(home, "tmp")
	mustMkdir(t, tmp)
	return junkEnv{home: home, tmp: tmp, apps: buildAppIndex(nil), guard: &usageGuard{running: map[string]bool{}}, fda: true, findTool: func(string) string { return "" }}
}

// itemIDs returns sorted item IDs for assertions.
func itemIDs(specs []itemSpec) []string {
	var ids []string
	for _, s := range specs {
		ids = append(ids, s.item.ID)
	}
	slices.Sort(ids)
	return ids
}

// TestExpandShowsOnlyWhatExists checks junk expansion.
func TestExpandShowsOnlyWhatExists(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/abc/pkg.tgz"), 1000)
	writeFile(t, filepath.Join(env.home, ".cache/uv/wheels/x.whl"), 1000)
	specs, sum := expandItems(env)
	got := itemIDs(specs)
	if !slices.Equal(got, []string{"dev.npx", "dev.uv"}) {
		t.Fatalf("items = %v", got)
	}
	if sum.Checked < len(junkRules)-4 { // every rule without a command was looked at
		t.Fatalf("checked = %d", sum.Checked)
	}
}

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
	specs, _ := expandItems(env)
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
	specs, _ := expandItems(env)
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
	specs, sum := expandItems(env)
	if sum.Containers != 1 || sum.FullDiskAccess {
		t.Fatalf("summary = %+v", sum)
	}
	for _, s := range specs {
		if strings.Contains(strings.Join(s.item.Paths, ","), "Containers") {
			t.Fatalf("%s reached into a container without access", s.item.ID)
		}
	}
}

// TestProtectedAndCustom checks junk expansion.
func TestProtectedAndCustom(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/com.apple.Spotlight/x"), 100)
	writeFile(t, filepath.Join(env.home, "Library/Caches/com.apple.akd/y"), 100)
	writeFile(t, filepath.Join(env.home, ".cache/huggingface/hub/model.bin"), 100)
	writeFile(t, filepath.Join(env.home, "stuff/old.bin"), 100)
	env.settings.Custom = []CustomFolder{{Name: "stuff", Path: filepath.Join(env.home, "stuff"), Enabled: true}}
	specs, _ := expandItems(env)
	for _, s := range specs {
		for _, c := range s.cands {
			if strings.Contains(c.path, "Spotlight") || strings.Contains(c.path, "huggingface") {
				t.Fatalf("%s reaches protected %s", s.item.ID, c.path)
			}
		}
	}
	ids := itemIDs(specs)
	if !slices.Contains(ids, "sys.apple") || !slices.Contains(ids, "custom:"+filepath.Join(env.home, "stuff")) {
		t.Fatalf("items = %v", ids)
	}
}

// TestFilteredRulesKeepDisjointDownloadTargets checks shared roots retain separate filters.
func TestFilteredRulesKeepDisjointDownloadTargets(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Downloads/old.dmg"), 100)
	writeFile(t, filepath.Join(env.home, "Downloads/incomplete.part"), 100)
	specs, _ := expandItems(env)
	ids := itemIDs(specs)
	if !slices.Contains(ids, "review.installers") || !slices.Contains(ids, "review.partial-downloads") {
		t.Fatalf("items = %v", ids)
	}
}

// TestCustomCannotEncloseProtectedCaches checks descent around protected subtrees.
func TestCustomCannotEncloseProtectedCaches(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".cache/huggingface/hub/model.bin"), 100)
	writeFile(t, filepath.Join(env.home, ".cache/unused/blob"), 100)
	env.settings.Custom = []CustomFolder{{Path: filepath.Join(env.home, ".cache"), Enabled: true}}
	specs, _ := expandItems(env)
	for _, s := range specs {
		for _, c := range s.cands {
			if within(canonical(filepath.Join(env.home, ".cache/huggingface")), canonical(c.path)) {
				t.Fatalf("candidate encloses protected tree: %s", c.path)
			}
		}
	}
}

// TestCleanJunkDeletesSelectedAndTrashesCustom checks delete and trash modes.
func TestCleanJunkDeletesSelectedAndTrashesCustom(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/a/x"), 300)
	writeFile(t, filepath.Join(env.home, "stuff/old.bin"), 200)
	env.settings.Custom = []CustomFolder{{Name: "stuff", Path: filepath.Join(env.home, "stuff"), Enabled: true}}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.npx"}, {ID: "custom:" + filepath.Join(env.home, "stuff")}}, time.Time{}, cleanHooks{})
	if len(res) != 2 || res[0].Freed != 300 || res[1].Trashed != 200 {
		t.Fatalf("results = %+v", res)
	}
	if exists(filepath.Join(env.home, ".npm/_npx/a")) || !exists(filepath.Join(env.home, ".Trash/old.bin")) {
		t.Fatal("cleaning result mismatch")
	}
	if !exists(filepath.Join(env.home, "stuff")) {
		t.Fatal("folder should stay")
	}
}

// TestCleanJunkRefusesPathsOutsideTheItem checks selection bounds.
func TestCleanJunkRefusesPathsOutsideTheItem(t *testing.T) {
	env := testEnv(t)
	old := filepath.Join(env.home, "Downloads/Old.dmg")
	writeFile(t, old, 100)
	other := filepath.Join(env.home, "Documents/keep.dmg")
	writeFile(t, other, 100)
	saved := now
	now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	defer func() { now = saved }()
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.installers", Paths: []string{other, old + "-renamed"}}}, time.Time{}, cleanHooks{})
	if !exists(other) || !exists(old) || res[0].Trashed != 0 {
		t.Fatalf("outside paths touched: %+v", res)
	}
}

// TestCleanJunkLeavesAnAppThatStartedRunning checks the fresh running guard.
func TestCleanJunkLeavesAnAppThatStartedRunning(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/Google/Chrome/Default/Cache/d"), 100)
	env.guard.running = map[string]bool{"com.google.chrome": true}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "browser.chrome"}}, time.Time{}, cleanHooks{})
	if len(res) == 0 || res[0].Skipped != "running" || !exists(filepath.Join(env.home, "Library/Caches/Google/Chrome/Default/Cache/d")) {
		t.Fatalf("result = %+v", res)
	}
}

// TestScanJunkStreamsItems checks one callback per measured item.
func TestScanJunkStreamsItems(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/a/x"), 300)
	writeFile(t, filepath.Join(env.home, ".cache/uv/b"), 100)
	var sized []string
	items, sum := scanJunk(context.Background(), env, nil, func(it JunkItem) { sized = append(sized, it.ID) })
	slices.Sort(sized)
	if len(items) != 2 || !slices.Equal(sized, []string{"dev.npx", "dev.uv"}) || !sum.FullDiskAccess {
		t.Fatalf("items %v sized %v summary %+v", items, sized, sum)
	}
}

// TestPlanItemMeasuresAndSkips checks item measurement.
func TestPlanItemMeasuresAndSkips(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/a/x"), 3000)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/b/y"), 1000)
	open := filepath.Join(env.home, ".npm/_npx/c")
	writeFile(t, filepath.Join(open, "z"), 500)
	env.guard.open = []string{strings.ToLower(resolvePath(filepath.Join(open, "z")))}
	specs, _ := expandItems(env)
	p := planItem(context.Background(), specs[0], env, make(chan struct{}, 4), nil)
	if p.item.Size != 4000 || p.item.InUse != 1 || len(p.entries) != 2 || p.entries[0].size != 3000 {
		t.Fatalf("plan = %+v", p)
	}
	if len(p.item.Top) != 2 || p.item.Top[0].Size != 3000 {
		t.Fatalf("top = %+v", p.item.Top)
	}
}

// TestPlanItemRunningOwnerTakesNothing checks item measurement.
func TestPlanItemRunningOwnerTakesNothing(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/Google/Chrome/Default/Cache/d"), 100)
	env.guard.running = map[string]bool{"com.google.chrome": true}
	specs, _ := expandItems(env)
	p := planItem(context.Background(), specs[0], env, make(chan struct{}, 4), nil)
	if !p.item.Running || len(p.entries) != 0 {
		t.Fatalf("a running browser keeps its cache: %+v", p)
	}
	if p.item.Size != 100 {
		t.Fatalf("a running item still shows how much quitting would free: %+v", p.item)
	}
}

// TestTempFolderWithTrailingSlash: os.TempDir() ends in a slash on macOS;
// the temporary folder must still be cleanable, not reported as denied.
func TestTempFolderWithTrailingSlash(t *testing.T) {
	env := testEnv(t)
	env.tmp = t.TempDir() // outside the home folder, as $TMPDIR is
	writeFile(t, filepath.Join(env.tmp, "old.log"), 100)
	env.tmp += "/"
	saved := now
	now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	defer func() { now = saved }()
	specs, _ := expandItems(env)
	for _, s := range specs {
		if s.item.ID == "sys.temp" {
			p := planItem(context.Background(), s, env, make(chan struct{}, 4), nil)
			if s.item.Denied || len(p.entries) != 1 {
				t.Fatalf("temp item = %+v, entries %d", s.item, len(p.entries))
			}
			return
		}
	}
	t.Fatal("no temp item")
}

// TestHomebrewKeepsItsIndex: only Homebrew's downloads are cleaned; its API
// index and compiled code stay, and no app row picks them up.
func TestHomebrewKeepsItsIndex(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/Homebrew/downloads/x.tar.gz"), 100)
	writeFile(t, filepath.Join(env.home, "Library/Caches/Homebrew/api/formula.jws.json"), 100)
	specs, _ := expandItems(env)
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
	specs, _ := expandItems(env)
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

// TestInstallersNeedThirtyDays checks item measurement.
func TestInstallersNeedThirtyDays(t *testing.T) {
	env := testEnv(t)
	old := filepath.Join(env.home, "Downloads/Old.dmg")
	writeFile(t, old, 100)
	writeFile(t, filepath.Join(env.home, "Downloads/New.dmg"), 100)
	writeFile(t, filepath.Join(env.home, "Downloads/notes.txt"), 100)
	saved := now
	now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	defer func() { now = saved }()
	if err := os.Chtimes(filepath.Join(env.home, "Downloads/New.dmg"), now(), now()); err != nil {
		t.Fatal(err)
	}
	specs, _ := expandItems(env)
	for _, s := range specs {
		if s.item.ID == "review.installers" {
			p := planItem(context.Background(), s, env, make(chan struct{}, 4), nil)
			if len(p.item.Subs) != 1 || p.item.Subs[0].Path != resolvePath(old) {
				t.Fatalf("subs = %+v", p.item.Subs)
			}
			return
		}
	}
	t.Fatal("no installers item")
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
			specs, _ := expandItems(env)
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

// TestCleanJunkTrashFirstAndScannedAt checks recoverability and scan cutoffs.
func TestCleanJunkTrashFirstAndScannedAt(t *testing.T) {
	env := testEnv(t)
	old := filepath.Join(env.home, ".Trash/old.bin")
	fresh := filepath.Join(env.home, ".Trash/fresh.bin")
	writeFile(t, old, 100)
	writeFile(t, fresh, 200)
	scanTime := time.Now().Add(time.Hour)
	if err := os.Chtimes(fresh, scanTime.Add(time.Hour), scanTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(env.home, "stuff/recover.bin"), 300)
	env.settings.Custom = []CustomFolder{{Name: "stuff", Path: filepath.Join(env.home, "stuff"), Enabled: true}}
	var started []string
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + filepath.Join(env.home, "stuff")}, {ID: "sys.trash"}}, scanTime, cleanHooks{started: func(id string) { started = append(started, id) }})
	if len(res) != 2 || res[0].Name != "sys.trash" || res[0].Freed != 100 || res[1].Trashed != 300 || started[0] != "sys.trash" {
		t.Fatalf("results = %+v, started = %v", res, started)
	}
	if exists(old) || !exists(fresh) || !exists(filepath.Join(env.home, ".Trash/recover.bin")) {
		t.Fatal("trash ordering or cutoff lost")
	}
}

// TestCleanJunkStopsAfterOneEntry checks cancellation retains remaining work.
func TestCleanJunkStopsAfterOneEntry(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/a/x"), 300)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/b/y"), 200)
	writeFile(t, filepath.Join(env.home, ".cache/uv/z"), 100)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var started, removed []string
	res := cleanJunk(ctx, env, []JunkSelection{{ID: "dev.npx"}, {ID: "dev.uv"}}, time.Time{}, cleanHooks{
		started: func(id string) { started = append(started, id) },
		removed: func(id, path string, bytes int64) { removed = append(removed, path); cancel() },
	})
	if len(res) != 1 || len(started) != 1 || len(removed) != 1 || res[0].Freed != 300 {
		t.Fatalf("results = %+v starts = %v removed = %v", res, started, removed)
	}
	if !exists(filepath.Join(env.home, ".npm/_npx/b")) || !exists(filepath.Join(env.home, ".cache/uv/z")) {
		t.Fatal("canceled clean removed further entries")
	}
}

// TestCleanJunkWholeDeviceSupportKeepsNewest checks whole-item default sub-selection.
func TestCleanJunkWholeDeviceSupportKeepsNewest(t *testing.T) {
	env := testEnv(t)
	root := filepath.Join(env.home, "Library/Developer/Xcode/iOS DeviceSupport")
	writeFile(t, filepath.Join(root, "17.2/Symbols/x"), 100)
	writeFile(t, filepath.Join(root, "18.0/Symbols/x"), 200)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.device-support"}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Freed != 100 || !exists(filepath.Join(root, "18.0/Symbols/x")) || exists(filepath.Join(root, "17.2")) {
		t.Fatalf("whole selection lost latest: %+v", res)
	}
}

// TestNoFullDiskAccessBlocksCustomContainers checks custom roots cannot bypass permissions.
func TestNoFullDiskAccessBlocksCustomContainers(t *testing.T) {
	env := testEnv(t)
	data := filepath.Join(env.home, "Library/Containers/com.vendor.app/Data")
	writeFile(t, filepath.Join(data, "Library/Caches/x"), 100)
	env.fda = false
	env.settings.Custom = []CustomFolder{{Name: "custom", Path: data, Enabled: true}}
	specs, _ := expandItems(env)
	for _, spec := range specs {
		for _, c := range spec.cands {
			if strings.Contains(c.path, "/Containers/") {
				t.Fatalf("custom enters container: %+v", spec)
			}
		}
	}
}

// TestCleanJunkRejectsParentSelections checks a parent path does not select children.
func TestCleanJunkRejectsParentSelections(t *testing.T) {
	env := testEnv(t)
	file := filepath.Join(env.home, "Downloads/Old.dmg")
	writeFile(t, file, 100)
	saved := now
	now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	defer func() { now = saved }()
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.installers", Paths: []string{filepath.Dir(file), env.home}}}, time.Time{}, cleanHooks{})
	if res[0].Trashed != 0 || !exists(file) {
		t.Fatalf("parent granted child selection: %+v", res)
	}
}

// TestCleanJunkRefreshesUsagePerItem checks an app starting after the first clean.
func TestCleanJunkRefreshesUsagePerItem(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/a/x"), 100)
	chrome := filepath.Join(env.home, "Library/Caches/Google/Chrome/Default/Cache/d")
	writeFile(t, chrome, 100)
	probes := 0
	env.refreshGuard = func() *usageGuard {
		probes++
		if probes > 1 {
			return &usageGuard{running: map[string]bool{"com.google.chrome": true}}
		}
		return &usageGuard{}
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.npx"}, {ID: "browser.chrome"}}, time.Time{}, cleanHooks{})
	if len(res) != 2 || probes != 2 || res[0].Freed != 100 || res[1].Skipped != "running" || !exists(chrome) {
		t.Fatalf("results = %+v probes = %d", res, probes)
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
	specs, _ := expandItems(env)
	for _, spec := range specs {
		for _, c := range spec.cands {
			if strings.Contains(c.path, "/Containers/") {
				t.Fatalf("alias entered container: %+v", spec)
			}
		}
	}
}

// TestProtectedFinderVariants checks every protected Finder cache suffix.
func TestProtectedFinderVariants(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/com.apple.finder.helper/x"), 100)
	specs, _ := expandItems(env)
	if len(specs) != 0 {
		t.Fatalf("protected Finder cache emitted: %v", itemIDs(specs))
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
	specs, _ := expandItems(env)
	for _, spec := range specs {
		if spec.item.ID == "dev.npx" && len(spec.cands) > 0 {
			t.Fatalf("rule enters Documents: %+v", spec)
		}
	}
}

// TestRuleCatalogMeasuresEachTarget checks every non-command rule with staged paths.
func TestRuleCatalogMeasuresEachTarget(t *testing.T) {
	for _, rule := range junkRules {
		if rule.Command != nil {
			continue
		}
		t.Run(rule.ID, func(t *testing.T) {
			env := testEnv(t)
			for _, target := range rule.Targets {
				root := filepath.Join(env.home, strings.ReplaceAll(target.Pattern, "*", "Fixture"))
				if target.Pattern == "$TMPDIR" {
					root = env.tmp
				}
				path := root
				if target.Mode == entriesOf {
					name := "entry.bin"
					if len(target.Names) > 0 {
						name = strings.ReplaceAll(target.Names[0], "*", "Fixture")
					}
					path = filepath.Join(root, name)
				} else {
					path = filepath.Join(root, "entry.bin")
				}
				writeFile(t, path, 100)
			}
			saved := now
			now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
			defer func() { now = saved }()
			specs, _ := expandItems(env)
			for _, spec := range specs {
				if spec.item.ID == rule.ID {
					plan := planItem(context.Background(), spec, env, make(chan struct{}, 4), nil)
					if plan.item.Size != int64(len(rule.Targets))*100 {
						t.Fatalf("size = %d, want %d", plan.item.Size, len(rule.Targets)*100)
					}
					return
				}
			}
			t.Fatalf("rule %s missing, got %v", rule.ID, itemIDs(specs))
		})
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
	expandItems(env)
}

// TestTemporaryCandidatesAlwaysNeedSevenDays checks custom targets cannot bypass temporary age.
func TestTemporaryCandidatesAlwaysNeedSevenDays(t *testing.T) {
	env := testEnv(t)
	root := filepath.Join(env.tmp, "custom")
	writeFile(t, filepath.Join(root, "new.bin"), 100)
	env.settings.Custom = []CustomFolder{{Path: root, Enabled: true}}
	specs, _ := expandItems(env)
	for _, spec := range specs {
		p := planItem(context.Background(), spec, env, make(chan struct{}, 4), nil)
		if p.item.Size != 0 {
			t.Fatalf("recent temporary entry measured: %+v", p)
		}
	}
}

// TestPlanItemFailsClosed checks a failed usage probe leaves every candidate alone.
func TestPlanItemFailsClosed(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/a/x"), 100)
	env.guard.failed = true
	specs, _ := expandItems(env)
	p := planItem(context.Background(), specs[0], env, make(chan struct{}, 4), nil)
	if p.item.Size != 0 || p.item.InUse != 1 || len(p.entries) != 0 {
		t.Fatalf("failed guard removes entries: %+v", p)
	}
}

// TestPlanItemCanceledPoolStartsNoWalks checks cancellation while waiting for the pool.
func TestPlanItemCanceledPoolStartsNoWalks(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/a/x"), 100)
	specs, _ := expandItems(env)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	p := planItem(ctx, specs[0], env, sem, func(string) { t.Fatal("walk started after cancellation") })
	if len(p.entries) != 0 || len(sem) != 1 {
		t.Fatalf("plan=%+v pool=%d", p, len(sem))
	}
}

// TestPlanItemRejectsAnAncestorChangedToContainerAlias checks the boundary after expansion.
func TestPlanItemRejectsAnAncestorChangedToContainerAlias(t *testing.T) {
	env := testEnv(t)
	env.fda = false
	root := filepath.Join(env.home, ".npm/_npx")
	writeFile(t, filepath.Join(root, "entry/x"), 100)
	specs, _ := expandItems(env)
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

// TestCleanJunkIgnoresContainerSubpathsWithoutResolving checks supplied paths at the boundary.
func TestCleanJunkIgnoresContainerSubpathsWithoutResolving(t *testing.T) {
	env := testEnv(t)
	env.fda = false
	writeFile(t, filepath.Join(env.home, "Downloads/Old.dmg"), 100)
	data := filepath.Join(env.home, "Library/Containers/com.vendor.app/Data")
	writeFile(t, filepath.Join(data, "keep.dmg"), 100)
	saved := now
	now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	defer func() { now = saved }()
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.installers", Paths: []string{filepath.Join(data, "keep.dmg")}}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Trashed != 0 || !exists(filepath.Join(data, "keep.dmg")) {
		t.Fatalf("container path touched: %+v", res)
	}
}

// TestExplicitSelectionChecksCandidatePrefixes checks the filter guards every candidate before resolution.
func TestExplicitSelectionChecksCandidatePrefixes(t *testing.T) {
	env := testEnv(t)
	env.fda = false
	root := filepath.Join(env.home, ".npm/_npx")
	writeFile(t, filepath.Join(root, "a/x"), 100)
	writeFile(t, filepath.Join(root, "b/y"), 100)
	specs, _ := expandItems(env)
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
