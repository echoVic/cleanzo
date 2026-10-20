package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCleanerDropsLegacyJunkMethods keeps the bound API on the rule engine.
func TestCleanerDropsLegacyJunkMethods(t *testing.T) {
	service := reflect.TypeOf(Cleaner{})
	for _, name := range []string{"Scan", "Clean", "ScanStream", "CleanStream", "ScanAppCaches", "GetCategories", "SaveCategories"} {
		if _, ok := service.MethodByName(name); ok {
			t.Errorf("legacy method %s is still bound", name)
		}
	}
}

// TestSettingsSaveDropsLegacyCategories writes only the v3 settings model.
func TestSettingsSaveDropsLegacyCategories(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	mustMkdir(t, filepath.Dir(configPath()))
	old := strconv.Quote(filepath.Join(home, "old"))
	for _, data := range []string{
		"", `[{"name":"old","paths":[` + old + `],"enabled":false}]`,
		`{"version":2,"categories":[{"name":"old","paths":[` + old + `],"enabled":false}]}`,
		`{"version":3,"ignored":["dev.npm"],"categories":[{"name":"old","paths":[` + old + `],"enabled":false}]}`,
	} {
		if err := os.WriteFile(configPath(), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		settings := (Cleaner{}).GetSettings()
		if err := (Cleaner{}).SaveSettings(settings); err != nil {
			t.Fatal(err)
		}
		saved, err := os.ReadFile(configPath())
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(saved, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["categories"]; ok {
			t.Errorf("v3 save kept the old categories: %s", saved)
		}
		if string(fields["version"]) != "3" || !reflect.DeepEqual((Cleaner{}).GetSettings(), settings) {
			t.Fatalf("v3 settings did not round trip: %s", saved)
		}
	}
}

// TestSettingsMigrationDoesNotRewriteConfig waits until saving to write v3.
func TestSettingsMigrationDoesNotRewriteConfig(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	mustMkdir(t, filepath.Dir(configPath()))
	data := `[{"name":"logs","risk":"safe","paths":[` + strconv.Quote(filepath.Join(home, "Library/Logs")) + `],"enabled":false}]`
	if err := os.WriteFile(configPath(), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (Cleaner{}).GetSettings(); !slices.Equal(got.Ignored, []string{"sys.logs"}) {
		t.Fatalf("legacy choice was not migrated: %+v", got)
	}
	saved, err := os.ReadFile(configPath())
	if err != nil || string(saved) != data {
		t.Fatalf("reading legacy settings rewrote them: %s, %v", saved, err)
	}
}

// TestScanPathAndClean measures and trashes custom contents, keeping their root.
func TestScanPathAndClean(t *testing.T) {
	env := testEnv(t)
	home := env.home
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

	env.settings.Custom = []CustomFolder{{Name: "test", Path: junk, Enabled: true}}
	sel := []JunkSelection{{ID: "custom:" + junk}}

	size, files := scanPath(junk)
	if size != 3072 || files != 2 {
		t.Fatalf("scanPath got size=%d files=%d, want 3072/2", size, files)
	}

	res := cleanJunk(context.Background(), env, sel, time.Time{}, cleanHooks{})[0]
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

	res2 := cleanJunk(context.Background(), env, sel, time.Time{}, cleanHooks{})[0]
	if res2.Trashed != 0 || res2.Files != 0 {
		t.Fatalf("second clean should move nothing, got trashed=%d files=%d", res2.Trashed, res2.Files)
	}
}

// TestCleanJunkEmptiesTrash permanently removes existing Trash contents.
func TestCleanJunkEmptiesTrash(t *testing.T) {
	env := testEnv(t)
	home := env.home
	trash := filepath.Join(home, ".Trash")
	mustMkdir(t, trash)
	// something already sitting in the trash
	if err := os.WriteFile(filepath.Join(trash, "old.bin"), make([]byte, 1500), 0o644); err != nil {
		t.Fatal(err)
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "sys.trash"}}, time.Time{}, cleanHooks{})[0]
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

// TestScanJunkMissingFolder hides absent custom folders without counting them.
func TestScanJunkMissingFolder(t *testing.T) {
	env := testEnv(t)
	env.settings.Custom = []CustomFolder{{Name: "x", Path: filepath.Join(env.home, "nope"), Enabled: true}}
	if got, _ := scanJunk(context.Background(), env, nil, nil); len(got) != 0 {
		t.Fatalf("missing folder should contribute no items: %+v", got)
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

// ---------------------------------------------------------------------------
// Settings persistence
// ---------------------------------------------------------------------------

// TestConfigRoundtrip preserves custom settings and their effective targets.
func TestConfigRoundtrip(t *testing.T) {
	env := testEnv(t)
	home := env.home

	path := filepath.Join(home, "mydir")
	writeFile(t, filepath.Join(path, "old"), 100)
	settings := Settings{Custom: []CustomFolder{{Name: "custom", Path: path, Enabled: true}}}
	if err := (Cleaner{}).SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	re := Cleaner{}.GetSettings()
	if !reflect.DeepEqual(re, settings) {
		t.Fatalf("unexpected reload: %+v", re)
	}
	env.settings = re
	loaded, _ := expandItems(&env)
	if len(loaded) != 1 || loaded[0].item.ID != "custom:"+path || loaded[0].cands[0].path != resolvePath(filepath.Join(path, "old")) {
		t.Fatalf("unexpected effective settings: %v", itemIDs(loaded))
	}
}

// TestSaveSettingsRejectsBlocked rejects enabled folders outside the safety gate.
func TestSaveSettingsRejectsBlocked(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	err := Cleaner{}.SaveSettings(Settings{Custom: []CustomFolder{{Name: "x", Enabled: true, Path: "/"}}})
	if err == nil {
		t.Fatal("expected protected path to be rejected")
	}
}

// TestSettingsDefaultsNeedNoConfig leaves a fresh install unchanged until saving.
func TestSettingsDefaultsNeedNoConfig(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if got := loadSettings(); !reflect.DeepEqual(got, Settings{}) || exists(configPath()) {
		t.Fatalf("default settings should not write config: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// Full disk scan & trash
// ---------------------------------------------------------------------------

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDiskStat(t *testing.T) {
	d := diskStat()
	if d.Total <= 0 || d.Free <= 0 || d.Used <= 0 {
		t.Fatalf("bad disk stat: %+v", d)
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

// ---------------------------------------------------------------------------
// Per-application cache cleanup
// ---------------------------------------------------------------------------

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
	setHome(t, home)
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
	setHome(t, home)
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

// ---------------------------------------------------------------------------
// Junk cleaning safety
// ---------------------------------------------------------------------------

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

// TestMain swaps the system Trash, which needs the running app, for a
// stand-in that moves items into $HOME/.Trash.
func TestMain(m *testing.M) {
	cleanup, err := sandboxWindowsTests()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Windows test sandbox:", err)
		os.Exit(2)
	}
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
	code := m.Run()
	cleanup()
	os.Exit(code)
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

// TestNestedJunkItemsCountOnce keeps nested targets out of other items' cleans.
func TestNestedJunkItemsCountOnce(t *testing.T) {
	env := testEnv(t)
	home := env.home
	caches := filepath.Join(home, "Library", "Caches")
	writeFile(t, filepath.Join(caches, "com.app", "blob"), 2000)
	writeFile(t, filepath.Join(caches, "go-build", "ab", "obj"), 1000)
	writeFile(t, filepath.Join(caches, "foo", "x"), 300)
	writeFile(t, filepath.Join(caches, "foo", "bar", "y"), 400)
	bar := filepath.Join(caches, "foo", "bar")
	env.settings.Custom = []CustomFolder{{Name: "bar", Path: bar, Enabled: true}}
	items, _ := scanJunk(context.Background(), env, nil, nil)
	got := map[string]JunkItem{}
	for _, item := range items {
		got[item.ID] = item
	}
	if len(got) != 4 || got["app:dir:com.app"].Size != 2000 || got["app:dir:foo"].Size != 300 || got["dev.go-build"].Size != 1000 || got["custom:"+bar].Size != 400 {
		t.Fatalf("items counted nested targets twice: %+v", got)
	}
	if top := got["app:dir:com.app"].Top; len(top) != 1 || top[0].Name != "com.app" || top[0].Size != 2000 {
		t.Fatalf("top of app cache = %+v", top)
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "app:dir:com.app"}, {ID: "app:dir:foo"}}, time.Time{}, cleanHooks{})
	if len(res) != 2 || len(res[0].Failed)+len(res[1].Failed) != 0 || res[0].Freed+res[1].Freed != 2300 {
		t.Fatalf("results: %+v", res)
	}
	if exists(filepath.Join(caches, "com.app")) || exists(filepath.Join(caches, "foo", "x")) {
		t.Fatal("caches' own entries should be cleaned")
	}
	if !exists(filepath.Join(caches, "go-build", "ab", "obj")) || !exists(filepath.Join(caches, "foo", "bar", "y")) {
		t.Fatal("folders of other categories must be left alone")
	}
}

// TestBuiltinJunkIsDeleted permanently removes regenerable app caches.
func TestBuiltinJunkIsDeleted(t *testing.T) {
	env := testEnv(t)
	home := env.home
	writeFile(t, filepath.Join(home, "Library", "Caches", "com.example.app", "blob"), 4096)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "app:dir:com.example.app"}}, time.Time{}, cleanHooks{})[0]
	if res.Freed != 4096 || res.Trashed != 0 || len(res.Failed) != 0 {
		t.Fatalf("result = %+v", res)
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
	env := testEnv(t)
	home := env.home
	trash := filepath.Join(home, ".Trash")
	writeFile(t, filepath.Join(trash, "old.bin"), 1500)
	mine := filepath.Join(home, "mydir")
	writeFile(t, filepath.Join(mine, "a.bin"), 700)
	env.settings.Custom = []CustomFolder{{Name: "mine", Path: mine, Enabled: true}}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + mine}, {ID: "sys.trash"}}, time.Time{}, cleanHooks{})
	if !exists(filepath.Join(trash, "a.bin")) || exists(filepath.Join(trash, "old.bin")) {
		t.Fatal("a.bin should be in the Trash, old.bin gone")
	}
	if len(res) != 2 || res[0].Name != "sys.trash" || res[0].Freed != 1500 || res[1].Trashed != 700 {
		t.Fatalf("results = %+v", res)
	}
}

// TestSaveSettingsRejectsDuplicates rejects two names for the same folder.
func TestSaveSettingsRejectsDuplicates(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	a := filepath.Join(home, "a")
	if err := (Cleaner{}).SaveSettings(Settings{Custom: []CustomFolder{
		{Name: "a", Enabled: true, Path: a},
		{Name: "b", Enabled: true, Path: a + "/"},
	}}); err == nil {
		t.Error("two custom folders with one path should be rejected")
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

// ---------------------------------------------------------------------------
// Second review: the usage guard
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Second review: what cleaning and Move to Trash may touch
// ---------------------------------------------------------------------------

// TestJunkFlagsFollowWhatCleaningDoes marks only user folders as recoverable.
func TestJunkFlagsFollowWhatCleaningDoes(t *testing.T) {
	env := testEnv(t)
	home := env.home
	trash, mine, logs := filepath.Join(home, ".Trash"), filepath.Join(home, "mine"), filepath.Join(home, "Library", "Logs")
	for _, dir := range []string{trash, mine, logs} {
		writeFile(t, filepath.Join(dir, "x"), 10)
	}
	env.settings.Custom = []CustomFolder{{Name: "mine", Path: mine, Enabled: true}}
	got := map[string]bool{}
	items, _ := scanJunk(context.Background(), env, nil, nil)
	for _, item := range items {
		got[item.ID] = item.Trash
	}
	want := map[string]bool{"sys.trash": false, "custom:" + mine: true, "sys.logs": false}
	if !maps.Equal(got, want) {
		t.Fatalf("flags = %+v, want %+v", got, want)
	}
}

// TestIgnoredRuleKeepsItsFolder keeps ignored developer caches out of app rows.
func TestIgnoredRuleKeepsItsFolder(t *testing.T) {
	env := testEnv(t)
	home := env.home
	caches := filepath.Join(home, "Library", "Caches")
	writeFile(t, filepath.Join(caches, "go-build", "x"), 1000)
	writeFile(t, filepath.Join(caches, "com.app", "y"), 500)
	env.settings.Ignored = []string{"dev.go-build"}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "app:dir:com.app"}, {ID: "dev.go-build"}}, time.Time{}, cleanHooks{})
	if !exists(filepath.Join(caches, "go-build", "x")) || exists(filepath.Join(caches, "com.app")) {
		t.Fatal("app caches must clean com.app and leave the ignored rule's folder")
	}
	if len(res) != 2 || res[0].Freed != 500 || res[1].Freed != 0 {
		t.Fatalf("results = %+v", res)
	}
}

// TestDisabledJunkGroupsStayDisabled does not restore rules on save or reload.
func TestDisabledJunkGroupsStayDisabled(t *testing.T) {
	env := testEnv(t)
	settings := Settings{DisabledGroups: slices.Clone(junkGroupNames)}
	if err := (Cleaner{}).SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if got := (Cleaner{}).GetSettings(); !reflect.DeepEqual(got, settings) {
		t.Fatalf("GetSettings = %+v", got)
	}
	writeFile(t, filepath.Join(env.home, "Library/Caches/App/x"), 100)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/x"), 100)
	env.settings = loadSettings()
	if got, _ := scanJunk(context.Background(), env, nil, nil); len(got) != 0 {
		t.Fatalf("disabled groups produced items: %+v", got)
	}
}

// TestJunkTargetCanBeAFile measures and deletes a rule's direct file target.
func TestJunkTargetCanBeAFile(t *testing.T) {
	env := testEnv(t)
	home := env.home
	log := filepath.Join(home, "Library", "Logs", "huge.log")
	writeFile(t, log, 5000)
	if got, _ := scanJunk(context.Background(), env, nil, nil); len(got) != 1 || got[0].Size != 5000 {
		t.Fatalf("scan = %+v, want 5000 bytes", got)
	}
	if res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "sys.logs"}}, time.Time{}, cleanHooks{})[0]; res.Freed != 5000 || exists(log) {
		t.Fatalf("res = %+v", res)
	}
}

// TestTempAgeCountsMoves: a file moved or extracted into the temporary folder
// keeps its old modification time, yet it arrived just now.
func TestTempAgeCountsMoves(t *testing.T) {
	env := testEnv(t)
	tmp := env.tmp
	t.Setenv("TMPDIR", tmp)
	old := time.Now().Add(-30 * 24 * time.Hour)
	p := filepath.Join(tmp, "extracted", "a.bin")
	writeFile(t, p, 100)
	for _, q := range []string{p, filepath.Dir(p)} {
		if err := os.Chtimes(q, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := scanJunk(context.Background(), env, nil, nil); len(got) != 1 || got[0].Size != 0 {
		t.Fatalf("a just-extracted file was planned: %+v", got)
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "sys.temp"}}, time.Time{}, cleanHooks{})[0]
	if res.Freed != 0 || !exists(p) {
		t.Fatalf("a just-extracted file was cleaned: %+v", res)
	}
}

// TestCleanKeepsWhatReachedTheTrashAfterTheScan: emptying the Trash deletes
// what the scan showed; what arrives later stays recoverable.
func TestCleanKeepsWhatReachedTheTrashAfterTheScan(t *testing.T) {
	env := testEnv(t)
	home := env.home
	trash := filepath.Join(home, ".Trash")
	writeFile(t, filepath.Join(trash, "old.bin"), 1500)
	time.Sleep(20 * time.Millisecond)
	scannedAt := time.Now()
	time.Sleep(20 * time.Millisecond)
	writeFile(t, filepath.Join(trash, "new.bin"), 700)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "sys.trash"}}, time.UnixMilli(scannedAt.UnixMilli()), cleanHooks{})[0]
	if exists(filepath.Join(trash, "old.bin")) || !exists(filepath.Join(trash, "new.bin")) || res.Freed != 1500 {
		t.Fatalf("result = %+v", res)
	}
}

// ---------------------------------------------------------------------------
// Streamed scan and clean
// ---------------------------------------------------------------------------

// TestScanJunkReportsEachItem reports each item once and every measured entry.
func TestScanJunkReportsEachItem(t *testing.T) {
	env := testEnv(t)
	home := env.home
	a, b := filepath.Join(home, "a"), filepath.Join(home, "b")
	writeFile(t, filepath.Join(a, "x", "1"), 100)
	writeFile(t, filepath.Join(b, "y", "2"), 200)
	env.settings.Custom = []CustomFolder{{Name: "a", Path: a, Enabled: true}, {Name: "b", Path: b, Enabled: true}}
	var measured []string
	sizes := map[string]int64{}
	scanJunk(context.Background(), env,
		func(p string) { measured = append(measured, filepath.Base(p)) },
		func(item JunkItem) { sizes[item.Name] += item.Size + 1000 }) // +1000 counts the reports
	if !maps.Equal(sizes, map[string]int64{"a": 1100, "b": 1200}) {
		t.Errorf("reported %v", sizes)
	}
	slices.Sort(measured)
	if !slices.Equal(measured, []string{"x", "y"}) {
		t.Errorf("measured %v", measured)
	}
}

// TestScanJunkStopsWhenCanceled reports nothing after cancellation.
func TestScanJunkStopsWhenCanceled(t *testing.T) {
	env := testEnv(t)
	home := env.home
	writeFile(t, filepath.Join(home, "a", "x", "1"), 100)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reported := 0
	env.settings.Custom = []CustomFolder{{Name: "a", Path: filepath.Join(home, "a"), Enabled: true}}
	scanJunk(ctx, env, func(string) { reported++ }, func(JunkItem) { reported++ })
	if reported != 0 {
		t.Errorf("a stopped scan reported %d times", reported)
	}
}

// TestCleanJunkReportsProgress reports starts, cumulative bytes and final results.
func TestCleanJunkReportsProgress(t *testing.T) {
	env := testEnv(t)
	home := env.home
	mine := filepath.Join(home, "mine")
	writeFile(t, filepath.Join(mine, "a"), 100)
	writeFile(t, filepath.Join(mine, "b"), 200)
	env.settings.Custom = []CustomFolder{{Name: "mine", Path: mine, Enabled: true}}
	var events []string
	id := "custom:" + mine
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: id}}, time.Time{}, cleanHooks{
		started: func(name string) { events = append(events, "start "+name) },
		removed: func(name, path string, bytes int64) {
			events = append(events, fmt.Sprintf("removed %s/%s %d", name, filepath.Base(path), bytes))
		},
		finished: func(r CleanResult) { events = append(events, fmt.Sprintf("done %s %d", r.Name, r.Trashed)) },
	})
	want := []string{"start " + id, "removed " + id + "/b 200", "removed " + id + "/a 300", "done " + id + " 300"}
	if !slices.Equal(events, want) || len(res) != 1 || res[0].Trashed != 300 {
		t.Fatalf("events = %q, results = %+v", events, res)
	}
}

// TestCleanJunkStops leaves remaining entries and starts no further item.
func TestCleanJunkStops(t *testing.T) {
	env := testEnv(t)
	home := env.home
	one, two := filepath.Join(home, "one"), filepath.Join(home, "two")
	for _, n := range []string{"a", "b", "c"} {
		writeFile(t, filepath.Join(one, n), 10)
	}
	writeFile(t, filepath.Join(two, "z"), 10)
	env.settings.Custom = []CustomFolder{{Name: "one", Path: one, Enabled: true}, {Name: "two", Path: two, Enabled: true}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var started []string
	removed := 0
	cleanJunk(ctx, env, []JunkSelection{{ID: "custom:" + one}, {ID: "custom:" + two}}, time.Time{}, cleanHooks{
		started: func(name string) { started = append(started, name) },
		removed: func(string, string, int64) { removed++; cancel() },
	})
	left, _ := os.ReadDir(one)
	if removed != 1 || len(left) != 2 || !slices.Equal(started, []string{"custom:" + one}) || !exists(filepath.Join(two, "z")) {
		t.Fatalf("removed %d, %d left, started %v", removed, len(left), started)
	}
}

// TestDiskReportsCapacity gets disk usage without scanning files.
func TestDiskReportsCapacity(t *testing.T) {
	got := Cleaner{}.Disk()
	if got.Total <= 0 || got.Free <= 0 {
		t.Fatalf("Disk = %+v, want positive total and free", got)
	}
}

// TestMarkRunning matches bundle IDs without case and bundle paths exactly.
func TestMarkRunning(t *testing.T) {
	apps := []AppInfo{
		{BundleID: "COM.EXAMPLE.EDITOR", Path: "/Applications/Editor.app"},
		{BundleID: "dev.other.preview", Path: "/Applications/Preview.app"},
		{BundleID: "dev.other.idle", Path: "/Applications/Idle.app"},
	}
	markRunning(apps, map[string]bool{"com.example.editor": true}, []string{"/Applications/Preview.app"})
	for i, want := range []bool{true, true, false} {
		if apps[i].Running != want {
			t.Errorf("apps[%d].Running = %v, want %v", i, apps[i].Running, want)
		}
	}
	markRunning(apps, nil, nil)
	for _, app := range apps {
		if app.Running {
			t.Errorf("app still marked running: %+v", app)
		}
	}
}
