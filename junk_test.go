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
	setHome(t, home)
	tmp := filepath.Join(home, "tmp")
	mustMkdir(t, tmp)
	return junkEnv{home: home, tmp: tmp, rules: darwinJunkRules, apps: buildAppIndex(nil), guard: &usageGuard{running: map[string]bool{}}, fda: true, findTool: func(string) string { return "" }}
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
	specs, sum := expandItems(&env)
	got := itemIDs(specs)
	if !slices.Equal(got, []string{"dev.npx", "dev.uv"}) {
		t.Fatalf("items = %v", got)
	}
	if sum.Checked < len(junkRules)-4 { // every rule without a command was looked at
		t.Fatalf("checked = %d", sum.Checked)
	}
}

func TestChromeShaderCachesIncludeRootAndProfilesButNeverStorage(t *testing.T) {
	env := testEnv(t)
	base := filepath.Join(env.home, "Library/Application Support/Google/Chrome")
	for _, path := range []string{"ShaderCache/data", "GrShaderCache/data", "GraphiteDawnCache/data", "Default/DawnGraphiteCache/data", "Profile 2/DawnWebGPUCache/data"} {
		writeFile(t, filepath.Join(base, path), 100)
	}
	for _, path := range []string{"Default/Service Worker/CacheStorage/keep", "Default/WebStorage/keep", "Default/IndexedDB/keep", "Default/Local Storage/keep", "Default/Cookies"} {
		writeFile(t, filepath.Join(base, path), 500)
	}
	specs, _ := expandItems(&env)
	for _, s := range specs {
		if s.item.ID != "browser.chrome" {
			continue
		}
		p := planItem(context.Background(), s, env, make(chan struct{}, 2), nil)
		if p.item.Size != 500 {
			t.Fatalf("chrome shader capacity = %d; want 500", p.item.Size)
		}
		for _, e := range p.entries {
			rel, err := filepath.Rel(resolvePath(base), e.path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(rel, "Storage") || strings.Contains(rel, "Cookies") || strings.Contains(rel, "Service Worker") {
				t.Fatalf("persistent data entered cleanup: %s", e.path)
			}
		}
		return
	}
	t.Fatal("Chrome cache rule missing")
}

// TestProtectedAndCustom checks junk expansion.
func TestProtectedAndCustom(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/com.apple.Spotlight/x"), 100)
	writeFile(t, filepath.Join(env.home, "Library/Caches/com.apple.akd/y"), 100)
	writeFile(t, filepath.Join(env.home, ".cache/huggingface/hub/model.bin"), 100)
	writeFile(t, filepath.Join(env.home, "stuff/old.bin"), 100)
	env.settings.Custom = []CustomFolder{{Name: "stuff", Path: filepath.Join(env.home, "stuff"), Enabled: true}}
	specs, _ := expandItems(&env)
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
	specs, _ := expandItems(&env)
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
	specs, _ := expandItems(&env)
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
	other := filepath.Join(env.home, "Pictures/keep.dmg")
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
	specs, _ := expandItems(&env)
	p := planItem(context.Background(), specs[0], env, make(chan struct{}, 4), nil)
	if p.item.Size != 4000 || p.item.InUse != 1 || len(p.entries) != 2 || p.entries[0].size != 3000 {
		t.Fatalf("plan = %+v", p)
	}
	if len(p.item.Top) != 2 || p.item.Top[0].Size != 3000 {
		t.Fatalf("top = %+v", p.item.Top)
	}
}

// TestObservedInUseCapacityKeepsDeletionPlanUnchanged separates discovery from eligibility.
func TestObservedInUseCapacityKeepsDeletionPlanUnchanged(t *testing.T) {
	env := testEnv(t)
	env.observe = true
	open := filepath.Join(env.home, ".npm/_npx/busy")
	writeFile(t, filepath.Join(open, "data"), 500)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/ready/data"), 1000)
	env.guard.open = []string{strings.ToLower(resolvePath(filepath.Join(open, "data")))}
	specs, _ := expandItems(&env)
	p := planItem(context.Background(), specs[0], env, make(chan struct{}, 4), nil)
	if p.item.FoundSize != 1500 || p.item.Size != 1000 || p.item.InUse != 1 || len(p.entries) != 1 {
		t.Fatalf("discovery must include busy capacity without granting deletion: %+v", p)
	}
	if len(p.item.Details) != 2 || p.item.Details[1].Reason == "" {
		t.Fatalf("candidate outcomes must remain inspectable: %+v", p.item.Details)
	}
}

func TestDiscoveryDoesNotAuthorizeBusyCleanup(t *testing.T) {
	env := testEnv(t)
	env.observe = true
	path := filepath.Join(env.home, ".npm/_npx/busy/data")
	writeFile(t, path, 500)
	env.guard.open = []string{strings.ToLower(resolvePath(path))}
	results := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.npx"}}, time.Time{}, cleanHooks{})
	if len(results) != 1 || results[0].Freed != 0 || !exists(path) {
		t.Fatalf("discovery capacity must never enter cleanup: %+v", results)
	}
}

func TestMeasurementFailureIsReportedNotEmpty(t *testing.T) {
	env := testEnv(t)
	env.observe = true
	missing := filepath.Join(env.home, "gone")
	p := planItem(context.Background(), itemSpec{item: JunkItem{ID: "test"}, cands: []junkCand{{path: missing}}}, env, make(chan struct{}, 1), nil)
	if p.item.ReadErrors != 1 || len(p.item.Details) == 0 || p.item.Details[0].Reason != "read-error" || len(p.entries) != 0 {
		t.Fatalf("missing candidate must be reported, not a clean empty result: %+v", p)
	}
}

// TestPlanItemRunningOwnerTakesNothing checks item measurement.
func TestPlanItemRunningOwnerTakesNothing(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/Google/Chrome/Default/Cache/d"), 100)
	env.guard.running = map[string]bool{"com.google.chrome": true}
	specs, _ := expandItems(&env)
	p := planItem(context.Background(), specs[0], env, make(chan struct{}, 4), nil)
	if !p.item.Running || len(p.entries) != 0 {
		t.Fatalf("a running browser keeps its cache: %+v", p)
	}
	if p.item.Size != 100 {
		t.Fatalf("a running item still shows how much quitting would free: %+v", p.item)
	}
}

// TestInstallersListAllAges checks that age is informational, not eligibility.
func TestInstallersListAllAges(t *testing.T) {
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
	specs, _ := expandItems(&env)
	for _, s := range specs {
		if s.item.ID == "review.installers" {
			p := planItem(context.Background(), s, env, make(chan struct{}, 4), nil)
			if len(p.item.Subs) != 2 || p.item.Default || !p.item.Trash || p.item.Size != 200 {
				t.Fatalf("all ages must be listed unselected and trashable: %+v", p.item)
			}
			for _, sub := range p.item.Subs {
				if sub.Detail == "" || sub.AgeDays == nil {
					t.Fatalf("age hint missing: %+v", sub)
				}
				wantAge := int64(31)
				if sub.Name == "New.dmg" {
					wantAge = 0
				}
				if *sub.AgeDays != wantAge {
					t.Fatalf("age for %s = %d; want %d", sub.Name, *sub.AgeDays, wantAge)
				}
			}
			fresh := filepath.Join(env.home, "Downloads/New.dmg")
			res := cleanJunk(context.Background(), env, []JunkSelection{{ID: s.item.ID, Paths: []string{fresh}}}, time.Time{}, cleanHooks{})
			if res[0].Trashed != 100 || exists(fresh) || !exists(old) || !exists(filepath.Join(env.home, ".Trash/New.dmg")) {
				t.Fatalf("young installer must allow explicit Trash selection: %+v", res)
			}
			return
		}
	}
	t.Fatal("no installers item")
}

// installerTestClock advances the age check without changing the real machine clock.
func installerTestClock(t *testing.T) {
	t.Helper()
	saved := now
	future := time.Now().Add(31 * 24 * time.Hour)
	now = func() time.Time { return future }
	t.Cleanup(func() { now = saved })
}

func TestInstallersTrashOnlySelectedNestedPackage(t *testing.T) {
	env := testEnv(t)
	installerTestClock(t)
	selected := filepath.Join(env.home, "Documents/tools/setup.pkg")
	keep := filepath.Join(env.home, "Documents/tools/keep.dmg")
	note := filepath.Join(env.home, "Documents/tools/notes.txt")
	for _, p := range []string{selected, keep, note} {
		writeFile(t, p, 100)
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.installers", Paths: []string{selected, filepath.Dir(selected), note}}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Trashed != 100 || res[0].Freed != 0 || exists(selected) || !exists(keep) || !exists(note) || !exists(filepath.Join(env.home, ".Trash/setup.pkg")) {
		t.Fatalf("nested selection violated file boundary: %+v", res)
	}
	// A stale selection must not remove the remaining package on retry.
	res = cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.installers", Paths: []string{selected}}}, time.Time{}, cleanHooks{})
	if res[0].Trashed != 0 || !exists(keep) {
		t.Fatalf("retry touched unrelated data: %+v", res)
	}
}

func TestInstallersRecheckUsageButAgeDoesNotBlockCleaning(t *testing.T) {
	env := testEnv(t)
	installerTestClock(t)
	busy := filepath.Join(env.home, "Desktop/nested/busy.pkg")
	fresh := filepath.Join(env.home, "Documents/nested/changed.dmg")
	writeFile(t, busy, 100)
	writeFile(t, fresh, 100)
	items, _ := scanJunk(context.Background(), env, nil, nil)
	if len(items) != 1 || len(items[0].Subs) != 2 {
		t.Fatalf("initial row = %+v", items)
	}
	env.guard.open = []string{canonical(busy)}
	if err := os.Chtimes(fresh, now(), now()); err != nil {
		t.Fatal(err)
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.installers", Paths: []string{busy, fresh}}}, time.Time{}, cleanHooks{})
	if res[0].Trashed != 100 || res[0].InUse != 1 || !exists(busy) || exists(fresh) || !exists(filepath.Join(env.home, ".Trash/changed.dmg")) {
		t.Fatalf("usage must block cleaning but age must not: %+v", res)
	}
}

func TestInstallersKeepBundlePackageWhole(t *testing.T) {
	env := testEnv(t)
	installerTestClock(t)
	bundle := filepath.Join(env.home, "Desktop/deep/Legacy.pkg")
	writeFile(t, filepath.Join(bundle, "Contents/inner.pkg"), 100)
	writeFile(t, filepath.Join(bundle, "Contents/metadata"), 100)
	items, _ := scanJunk(context.Background(), env, nil, nil)
	if len(items) != 1 || len(items[0].Subs) != 1 || items[0].Subs[0].Path != resolvePath(bundle) {
		t.Fatalf("bundle split or missing: %+v", items)
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.installers", Paths: []string{bundle}}}, time.Time{}, cleanHooks{})
	if res[0].Trashed != 200 || exists(bundle) || !exists(filepath.Join(env.home, ".Trash/Legacy.pkg/Contents/inner.pkg")) {
		t.Fatalf("bundle trash result: %+v", res)
	}
}

func TestInstallersReportBlockedSubfolderAndAllowOtherCandidates(t *testing.T) {
	env := testEnv(t)
	installerTestClock(t)
	blocked := filepath.Join(env.home, "Documents/blocked")
	writeFile(t, filepath.Join(blocked, "keep.dmg"), 100)
	ready := filepath.Join(env.home, "Documents/visible/ready.pkg")
	writeFile(t, ready, 100)
	blockedResolved := resolvePath(blocked)
	env.lstat = func(path string) (fs.FileInfo, error) {
		if path == blockedResolved {
			return nil, fs.ErrPermission
		}
		return os.Lstat(path)
	}
	items, _ := scanJunk(context.Background(), env, nil, nil)
	if len(items) != 1 || !items[0].Denied || len(items[0].Subs) != 1 || items[0].Subs[0].Path != resolvePath(ready) {
		t.Fatalf("blocked folder hidden or traversed: %+v", items)
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.installers", Paths: []string{ready, filepath.Join(blocked, "keep.dmg")}}}, time.Time{}, cleanHooks{})
	if res[0].Trashed != 100 || len(res[0].Failed) == 0 || !exists(filepath.Join(blocked, "keep.dmg")) {
		t.Fatalf("blocked selection result: %+v", res)
	}
}

func TestInstallersRecursiveWalkStopsWhenCanceled(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Downloads/deep/old.dmg"), 100)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	env.ctx = ctx
	var spec itemSpec
	env.collectRecursiveFiles(filepath.Join(env.home, "Downloads"), []string{"*.dmg"}, 0, claimSet{}, &spec)
	if len(spec.cands) != 0 {
		t.Fatal("canceled traversal added candidates")
	}
}

func TestInstallersClaimsDoNotTakeUnrelatedCustomContent(t *testing.T) {
	env := testEnv(t)
	root := filepath.Join(env.home, "Documents/tools")
	pkg := filepath.Join(root, "setup.pkg")
	note := filepath.Join(root, "notes.txt")
	writeFile(t, pkg, 100)
	writeFile(t, note, 100)
	env.settings.Custom = []CustomFolder{{Name: "tools", Path: root, Enabled: true}}
	env.settings.Ignored = []string{"review.installers"}
	specs, _ := expandItems(&env)
	for _, spec := range specs {
		if spec.item.ID == "custom:"+root {
			if len(spec.cands) != 1 || spec.cands[0].path != resolvePath(note) {
				t.Fatalf("installer claim leaked or took unrelated content: %+v", spec.cands)
			}
			return
		}
	}
	t.Fatal("custom content missing")
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

// TestProtectedFinderVariants checks every protected Finder cache suffix.
func TestProtectedFinderVariants(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/com.apple.finder.helper/x"), 100)
	specs, _ := expandItems(&env)
	if len(specs) != 0 {
		t.Fatalf("protected Finder cache emitted: %v", itemIDs(specs))
	}
}

// TestTemporaryCandidatesAlwaysNeedSevenDays checks custom targets cannot bypass temporary age.
func TestTemporaryCandidatesAlwaysNeedSevenDays(t *testing.T) {
	env := testEnv(t)
	root := filepath.Join(env.tmp, "custom")
	writeFile(t, filepath.Join(root, "new.bin"), 100)
	env.settings.Custom = []CustomFolder{{Path: root, Enabled: true}}
	specs, _ := expandItems(&env)
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
	specs, _ := expandItems(&env)
	p := planItem(context.Background(), specs[0], env, make(chan struct{}, 4), nil)
	if p.item.Size != 0 || p.item.InUse != 1 || len(p.entries) != 0 {
		t.Fatalf("failed guard removes entries: %+v", p)
	}
}

// TestPlanItemCanceledPoolStartsNoWalks checks cancellation while waiting for the pool.
func TestPlanItemCanceledPoolStartsNoWalks(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/a/x"), 100)
	specs, _ := expandItems(&env)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	p := planItem(ctx, specs[0], env, sem, func(string) { t.Fatal("walk started after cancellation") })
	if len(p.entries) != 0 || len(sem) != 1 {
		t.Fatalf("plan=%+v pool=%d", p, len(sem))
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

// TestScanJunkSummaryStopsWithContext checks cancellation and suppressed callbacks.
func TestScanJunkSummaryStopsWithContext(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Application Support/ZzxqAbandoned/data"), 2<<20)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, sum := scanJunk(ctx, env, nil, func(JunkItem) { t.Error("callback after cancellation") })
	if sum.Leftovers != 0 || sum.LeftoversSize != 0 {
		t.Fatalf("cancelled summary = %+v", sum)
	}
}

// TestRevealArgsExpandsHome: junk rows show and hand back paths as "~/…".
func TestRevealArgsExpandsHome(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	mustMkdir(t, filepath.Join(home, "Library", "Caches", "x"))
	args, err := revealArgs("~/Library/Caches/x")
	if err != nil || !slices.Equal(args, []string{filepath.Join(home, "Library", "Caches", "x")}) {
		t.Fatalf("revealArgs(~/…) = %q, %v", args, err)
	}
}
