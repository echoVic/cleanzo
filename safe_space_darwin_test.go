//go:build darwin

package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// safeSpaceHome stages what a scan selects by itself next to what it leaves
// for the user: a costly cache, the Trash, an installer, a custom folder and
// the cache of a running browser.
func safeSpaceHome(t *testing.T) junkEnv {
	t.Helper()
	env := testEnv(t)
	home := env.home
	writeFile(t, filepath.Join(home, ".npm/_cacache/x"), 300_000)
	writeFile(t, filepath.Join(home, "Library/Caches/com.example.notes/blob"), 200_000)
	writeFile(t, filepath.Join(home, "Library/Logs/app.log"), 100_000)
	writeFile(t, filepath.Join(home, ".gradle/caches/modules/x.jar"), 5000)
	writeFile(t, filepath.Join(home, "Library/Caches/ms-playwright/chromium/chrome"), 6000)
	writeFile(t, filepath.Join(home, ".Trash/old"), 7000)
	writeFile(t, filepath.Join(home, "Downloads/setup.dmg"), 8000)
	writeFile(t, filepath.Join(home, "Library/Caches/Google/Chrome/Default/Cache/data"), 9000)
	custom := filepath.Join(home, "scratch")
	writeFile(t, filepath.Join(custom, "a"), 4000)
	env.settings.Custom = []CustomFolder{{Name: "scratch", Path: custom, Enabled: true}}
	env.guard.running["com.google.chrome"] = true
	return env
}

// TestSafeSpaceIsWhatAScanSelectsAndDeletes checks that the measure holds
// the rows a scan selects by itself and deletes outright, largest first,
// and none of the rest: not a costly cache, the Trash, an installer, a
// custom folder bound for the Trash, or the cache of a running app.
func TestSafeSpaceIsWhatAScanSelectsAndDeletes(t *testing.T) {
	env := safeSpaceHome(t)
	space := measureSafeSpace(context.Background(), env)
	var ids []string
	var sum int64
	for _, row := range space.Rows {
		ids = append(ids, row.ID)
		sum += row.Size
	}
	if !slices.Equal(ids, []string{"dev.npm", "app:dir:com.example.notes", "sys.logs"}) {
		t.Fatalf("rows = %+v", space.Rows)
	}
	if space.Bytes != sum || space.Bytes == 0 || space.At == 0 {
		t.Fatalf("space = %+v", space)
	}
	if space.Rows[1].Name != "com.example.notes" || space.Rows[0].Name != "" {
		t.Fatalf("names = %+v", space.Rows)
	}
	// What a full scan selects by itself and deletes is the same set.
	items, _ := scanJunk(context.Background(), env, nil, nil)
	var want []string
	for _, it := range items {
		if it.Default && !it.Trash && !it.Running && it.Size > 0 {
			want = append(want, it.ID)
		}
	}
	slices.Sort(want)
	slices.Sort(ids)
	if !slices.Equal(ids, want) {
		t.Fatalf("safe rows %v, a scan's own %v", ids, want)
	}
}

// TestSafeSpaceHoldsBackWhatARuleItSkipsOwns checks that a folder owned by
// a rule the measure skips is not offered as an app's cache instead.
func TestSafeSpaceHoldsBackWhatARuleItSkipsOwns(t *testing.T) {
	env := safeSpaceHome(t)
	for _, row := range measureSafeSpace(context.Background(), env).Rows {
		if row.ID == "app:dir:ms-playwright" || row.ID == "dev.playwright" {
			t.Fatalf("the Playwright browsers are offered as %s", row.ID)
		}
	}
}

// TestCleanSafeSpaceDeletesOnlySafeRows checks that the cleanup removes the
// measured rows, refuses any other row it is handed, and is recorded.
func TestCleanSafeSpaceDeletesOnlySafeRows(t *testing.T) {
	env := safeSpaceHome(t)
	home := env.home
	space := measureSafeSpace(context.Background(), env)
	ids := []string{"sys.trash", "dev.gradle", "dev.playwright", "review.installers", "custom:" + filepath.Join(home, "scratch"), "browser.chrome", "dev.go-mod"}
	for _, row := range space.Rows {
		ids = append(ids, row.ID)
	}
	results := cleanSafeSpace(context.Background(), env, ids, time.UnixMilli(space.At), cleanHooks{})
	var freed int64
	for _, res := range results {
		freed += res.Freed
		if res.Trashed != 0 {
			t.Fatalf("%s moved %d bytes to the Trash", res.Name, res.Trashed)
		}
		// A rule that runs a tool is no safe row: it is not even looked for.
		if res.Name == "dev.go-mod" && res.Skipped != "" {
			t.Fatalf("the Go module cache rule was reached: %+v", res)
		}
	}
	if freed != space.Bytes {
		t.Fatalf("freed %d, measured %d: %+v", freed, space.Bytes, results)
	}
	for _, gone := range []string{".npm/_cacache/x", "Library/Caches/com.example.notes/blob", "Library/Logs/app.log"} {
		if exists(filepath.Join(home, gone)) {
			t.Errorf("%s is still there", gone)
		}
	}
	for _, kept := range []string{".gradle/caches/modules/x.jar", "Library/Caches/ms-playwright/chromium/chrome", ".Trash/old", "Downloads/setup.dmg", "scratch/a", "Library/Caches/Google/Chrome/Default/Cache/data"} {
		if !exists(filepath.Join(home, kept)) {
			t.Errorf("%s was removed", kept)
		}
	}
	history := (Cleaner{}).CleanHistory()
	if len(history) != 1 || history[0].Source != "low-space" || history[0].Freed != space.Bytes || len(history[0].Rows) != len(space.Rows) {
		t.Fatalf("history = %+v", history)
	}
	// Nothing is left to offer.
	if again := measureSafeSpace(context.Background(), env); again.Bytes != 0 || len(again.Rows) != 0 {
		t.Fatalf("after the cleanup: %+v", again)
	}
}
