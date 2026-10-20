package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

// windowsShellTest bounds native shell tests in a child so a shell dialog cannot hang CI.
func windowsShellTest(t *testing.T) bool {
	t.Helper()
	if os.Getenv("CLEANZO_SHELL_TEST") == t.Name() {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.v", "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
	cmd.Env = append(os.Environ(), "CLEANZO_SHELL_TEST="+t.Name())
	out, err := cmd.CombinedOutput()
	t.Logf("shell child:\n%s", out)
	if err != nil {
		t.Fatalf("shell child: %v (deadline: %v)", err, ctx.Err())
	}
	if strings.Contains(string(out), "--- SKIP: "+t.Name()+" ") {
		t.Skipf("native bin check skipped in child: %s", out)
	}
	if strings.Contains(string(out), "--- SKIP: "+t.Name()+"/bin ") {
		t.Run("bin", func(t *testing.T) { t.Skipf("native bin portion skipped in child: %s", out) })
	}
	return false
}

// recycleWindowsFixture retries a shell refusal once while the seeded source still exists.
// The shell maps transient sharing failures to blocked, so the syscall-only retry helper cannot see them.
func recycleWindowsFixture(path string) error {
	err := recycle(path)
	if err != nil && exists(path) {
		time.Sleep(150 * time.Millisecond)
		return recycle(path)
	}
	return err
}

// TestWindowsRecycleBinLayout asserts the manually declared shellapi.h layout on amd64 and arm64.
func TestWindowsRecycleBinLayout(t *testing.T) {
	var info shellBinInfo
	if unsafe.Sizeof(info) != 24 || unsafe.Offsetof(info.size) != 8 || unsafe.Offsetof(info.items) != 16 {
		t.Fatalf("SHQUERYRBINFO layout: %+v", info)
	}
}

// TestWindowsRecycleBinRule exercises native seed, scan, changed refusal and matching empty.
func TestWindowsRecycleBinRule(t *testing.T) {
	if !windowsShellTest(t) {
		return
	}
	env := testEnv(t)
	env.rules = []Rule{{ID: "sys.trash", Group: groupSystem, RecycleBin: true}}
	before, err := queryWindowsBin(env.home)
	if err != nil {
		t.Fatal(err)
	}
	if before.items != 0 {
		t.Skip("bin already contains unrelated items; refusing to empty them")
	}
	requireTestRecycleCapacity(t, env.home, before.size+8192)
	first := filepath.Join(env.home, "first.bin")
	second := filepath.Join(env.home, "second.bin")
	writeFile(t, first, 4096)
	if err := recycleWindowsFixture(first); err != nil {
		t.Fatal(err)
	}
	rows, _ := scanJunk(context.Background(), env, nil, nil)
	if len(rows) != 1 || rows[0].Size < 4096 || rows[0].Files < 1 || rows[0].Default {
		t.Fatalf("bin scan: %+v", rows)
	}
	writeFile(t, second, 4096)
	if err := recycleWindowsFixture(second); err != nil {
		t.Fatal(err)
	}
	seeded, err := queryWindowsBin(env.home)
	if err != nil || seeded.items < 2 || seeded.size < 8192 {
		t.Fatalf("seeded bin=%+v err=%v", seeded, err)
	}
	selection := []JunkSelection{{ID: "sys.trash"}}
	results := cleanJunk(context.Background(), env, selection, time.Now(), cleanHooks{})
	if len(results) != 1 || results[0].Skipped != "changed" {
		t.Fatalf("changed result: %+v", results)
	}
	unchanged, err := queryWindowsBin(env.home)
	if err != nil || unchanged != seeded {
		t.Fatalf("changed clean modified bin: %+v, %+v, %v", seeded, unchanged, err)
	}
	scanJunk(context.Background(), env, nil, nil)
	results = cleanJunk(context.Background(), env, selection, time.Now(), cleanHooks{})
	after, err := queryWindowsBin(env.home)
	if err != nil || after.items != 0 || results[0].Freed != seeded.size {
		t.Fatalf("empty result: %+v, bin=%+v err=%v", results, after, err)
	}
}

// TestWindowsRecycleBinOrdering checks new trash survives the same pass, even with reversed selections.
func TestWindowsRecycleBinOrdering(t *testing.T) {
	env := testEnv(t)
	download := filepath.Join(env.home, "Downloads", "setup.exe")
	writeFile(t, download, 123)
	env.rules = []Rule{
		{ID: "review.installers", Group: groupReview, Trash: true, Targets: []Target{{Pattern: "Downloads", Mode: entriesOf}}},
		{ID: "sys.trash", Group: groupSystem, RecycleBin: true},
	}
	savedQuery, savedEmpty, savedTrash := queryNativeBin, emptyNativeBin, trashItem
	t.Cleanup(func() { queryNativeBin, emptyNativeBin, trashItem = savedQuery, savedEmpty, savedTrash })
	state := binStats{size: 10, items: 1}
	queryNativeBin = func(string) (binStats, error) { return state, nil }
	emptyNativeBin = func(string) error { state = binStats{}; return nil }
	trashItem = func(p string) error {
		if state.items != 0 {
			t.Error("trashable item ran before sys.trash")
		}
		state = binStats{size: 123, items: 1}
		return retryWindowsFileOperation(func() error { return os.Rename(p, p+".trashed") })
	}
	scanJunk(context.Background(), env, nil, nil)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.installers"}, {ID: "sys.trash"}}, time.Now(), cleanHooks{})
	if len(res) != 2 || res[0].Name != "sys.trash" || res[0].Freed != 10 || res[1].Trashed != 123 || state.items != 1 {
		t.Fatalf("ordering: %+v state=%+v", res, state)
	}
	if userTrashDir() != "" {
		t.Fatal("Windows has no folder-based Trash")
	}
}

// TestWindowsTrashFolderIsNotBin prevents folder naming or an empty native Trash path from enabling permanent deletion.
func TestWindowsTrashFolderIsNotBin(t *testing.T) {
	env := testEnv(t)
	env.rules = []Rule{}
	dir := filepath.Join(env.home, ".Trash")
	file := filepath.Join(dir, "new.bin")
	writeFile(t, file, 123)
	env.settings.Custom = []CustomFolder{{Name: "fixture", Path: dir, Enabled: true}}
	saved := trashItem
	t.Cleanup(func() { trashItem = saved })
	called := false
	trashItem = func(string) error { called = true; return errBlocked }
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + dir}}, time.Now().Add(-time.Hour), cleanHooks{})
	if !called || !exists(file) || len(res) != 1 || res[0].Freed != 0 {
		t.Fatalf("folder treated as native bin: called=%t results=%+v", called, res)
	}
}

// startWindowsJunkUser keeps a real --user-data-dir process alive during the acceptance clean.
func startWindowsJunkUser(t *testing.T, ctx context.Context, dir string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsUsageHelper$", "-cleanzo-usage-helper", "--user-data-dir="+dir)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { input.Close(); cmd.Process.Kill(); cmd.Wait() })
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(output).ReadString('\n'); ready <- line }()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("usage helper: %q %s", line, stderr.String())
		}
	case <-ctx.Done():
		t.Fatal("usage helper timed out")
	}
}

// windowsFixtureSnapshot records all files in an external fixture without following links.
func windowsFixtureSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[p] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestWindowsJunkSeededClean exercises ScanJunk and CleanJunk with the native safety and usage guards.
func TestWindowsJunkSeededClean(t *testing.T) {
	if !windowsShellTest(t) {
		return
	}
	env := testEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	local := filepath.Join(env.home, "AppData", "Local")
	roaming := filepath.Join(env.home, "AppData", "Roaming")
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "keep"), 123)
	before := windowsFixtureSnapshot(t, outside)
	userData := filepath.Join(local, "Google", "Chrome", "User Data")
	chrome := filepath.Join(userData, "Default", "Cache", "share-delete.bin")
	code := filepath.Join(userData, "Default", "Code Cache", "keep.bin")
	writeFile(t, chrome, 4096)
	writeFile(t, code, 4096)
	protected := []string{
		filepath.Join(userData, "Default", "Cookies"), filepath.Join(userData, "Default", "History"),
		filepath.Join(env.home, "OneDrive", "keep"), filepath.Join(env.home, "NTUSER.DAT"),
		filepath.Join(roaming, "Microsoft", "Credentials", "x"), filepath.Join(local, "Cleanzo", "WebView2", "x"),
		filepath.Join(local, "Packages", "x"),
		filepath.Join(roaming, "Microsoft", "Crashpad", "pending", "d.dmp"),
		filepath.Join(local, "Cleanzo", "EBWebView", "Crashpad", "pending", "d.dmp"),
		filepath.Join(local, "Packages", "App", "Crashpad", "pending", "d.dmp"),
		filepath.Join(roaming, "Microsoft", "Local State"),
		filepath.Join(roaming, "Microsoft", "Cache", "keep"),
		filepath.Join(roaming, "Microsoft", "ElectronApp", "Local State"),
		filepath.Join(roaming, "Microsoft", "ElectronApp", "Code Cache", "keep"),
		filepath.Join(local, "pip", "Cache", "guarded", "offline.bin"),
	}
	for _, p := range protected {
		size := 123
		if filepath.Ext(p) == ".dmp" {
			size = 8192
		}
		writeFile(t, p, size)
	}
	offline := protected[len(protected)-1]
	if err := windows.SetFileAttributes(windows.StringToUTF16Ptr(offline), windows.FILE_ATTRIBUTE_OFFLINE); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.SetFileAttributes(windows.StringToUTF16Ptr(offline), windows.FILE_ATTRIBUTE_NORMAL) })
	junction := filepath.Join(local, "npm-cache", "_cacache", "guarded", "escape")
	legacy := filepath.Join(env.home, "Application Data")
	makeJunction(t, junction, outside)
	makeJunction(t, legacy, outside)
	caches := []string{
		filepath.Join(local, "npm-cache", "_cacache", "disposable", "data"),
		filepath.Join(local, "pip", "Cache", "disposable", "data"),
		filepath.Join(roaming, "FixtureElectron", "Code Cache", "data"),
		filepath.Join(local, "Microsoft", "Windows", "WER", "ReportArchive", "fixture", "data"),
		filepath.Join(local, "Microsoft", "Windows", "WER", "ReportQueue", "fixture", "data"),
		filepath.Join(local, "CrashDumps", "fixture.dmp"),
	}
	for _, p := range caches {
		writeFile(t, p, 4096)
	}
	writeFile(t, filepath.Join(roaming, "FixtureElectron", "Local State"), 2)
	binEnabled := false
	installer := filepath.Join(env.home, "Downloads", "fixture-setup.exe")
	t.Run("bin", func(t *testing.T) {
		stats, err := queryWindowsBin(env.home)
		if err != nil {
			t.Fatal(err)
		}
		if stats.items != 0 {
			t.Skip("bin contains unrelated entries")
		}
		requireTestRecycleCapacity(t, env.home, 16384)
		seed := filepath.Join(env.home, "seed-bin.bin")
		writeFile(t, seed, 4096)
		if err := recycleWindowsFixture(seed); err != nil {
			t.Fatal(err)
		}
		writeFile(t, installer, 4096)
		binEnabled = true
	})
	handle, err := windows.CreateFile(windows.StringToUTF16Ptr(chrome), windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	startWindowsJunkUser(t, ctx, userData)
	if guard := probeUsage(); guard.failed || !guard.inUse(chrome) {
		t.Fatal("real child did not protect Chrome")
	}
	// Only the framework transport is replaced: both bound methods build their real production environments.
	savedScan, savedClean, savedTrash := junkScanSend, junkCleanSend, trashItem
	t.Cleanup(func() { junkScanSend, junkCleanSend, trashItem = savedScan, savedClean, savedTrash })
	trashItem = func(p string) error { return recycleWindowsFixture(p) }
	var rows []JunkItem
	var results []CleanResult
	junkScanSend = func(_ *mygo.Channel[JunkScanEvent], event JunkScanEvent) error {
		if event.Item != nil {
			rows = append(rows, *event.Item)
		}
		return nil
	}
	junkCleanSend = func(_ *mygo.Channel[JunkCleanEvent], event JunkCleanEvent) error {
		if event.Result != nil {
			results = append(results, *event.Result)
		}
		return nil
	}
	scannedAt := time.Now().UnixMilli()
	if err := (Cleaner{}).ScanJunk(ctx, &mygo.Channel[JunkScanEvent]{}); err != nil {
		t.Fatal(err)
	}
	var selected []JunkSelection
	for _, row := range rows {
		if row.ID == "sys.trash" && !binEnabled {
			continue
		}
		paths := []string{}
		for _, sub := range row.Subs {
			paths = append(paths, sub.Path)
		}
		selected = append(selected, JunkSelection{ID: row.ID, Paths: paths})
	}
	for _, id := range []string{"browser.chrome", "dev.npm", "dev.pip", "app:dir:fixtureelectron", "sys.wer"} {
		if !slices.ContainsFunc(selected, func(s JunkSelection) bool { return s.ID == id }) {
			t.Fatalf("missing row %s: %+v", id, rows)
		}
	}
	if err := (Cleaner{}).CleanJunk(ctx, selected, scannedAt, &mygo.Channel[JunkCleanEvent]{}); err != nil {
		t.Fatal(err)
	}
	// Defender can briefly hold newly created files. Retry the remaining cache actions once, never the bin.
	if slices.ContainsFunc(caches, exists) {
		retry := slices.DeleteFunc(slices.Clone(selected), func(s JunkSelection) bool { return s.ID == "sys.trash" })
		time.Sleep(150 * time.Millisecond)
		if err := (Cleaner{}).CleanJunk(ctx, retry, scannedAt, &mygo.Channel[JunkCleanEvent]{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range append(protected, chrome, code) {
		want := 123
		if filepath.Ext(p) == ".dmp" {
			want = 8192
		}
		if p == chrome || p == code {
			want = 4096
		}
		data, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(data, make([]byte, want)) {
			t.Errorf("protected fixture changed: %s (%v)", p, err)
		}
	}
	for _, p := range []string{junction, legacy} {
		attrs, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(p))
		if err != nil || attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
			t.Errorf("junction changed: %s (%v)", p, err)
		}
	}
	attrs, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(offline))
	if err != nil || attrs&windows.FILE_ATTRIBUTE_OFFLINE == 0 {
		t.Error("offline attribute changed")
	}
	for _, p := range caches {
		if exists(p) {
			t.Errorf("cache survived: %s; results=%+v", p, results)
		}
	}
	if after := windowsFixtureSnapshot(t, outside); !reflect.DeepEqual(after, before) {
		t.Fatalf("outside profile changed: %v -> %v", before, after)
	}
	if binEnabled {
		if len(results) == 0 || results[0].Name != "sys.trash" || results[0].Freed < 4096 || exists(installer) {
			t.Fatalf("native bin ordering: %+v", results)
		}
		stats, err := queryWindowsBin(env.home)
		if err != nil || stats.items < 1 || stats.size < 4096 {
			t.Fatalf("newly recycled installer lost: %+v %v", stats, err)
		}
	}
	t.Logf("selected %d rows; native bin enabled=%t", len(selected), binEnabled)
}

// TestWindowsRealProfileScanGate refuses every unsandboxed test selection except the exact read-only test.
func TestWindowsRealProfileScanGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^NoSuchTest$")
	cmd.Env = append(os.Environ(), "CLEANZO_REAL_PROFILE_SCAN=1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "requires exactly") {
		t.Fatalf("unsafe escape hatch accepted: %v %s", err, out)
	}
}
