package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestGrowthRootsCoverSpec(t *testing.T) {
	roots := growthRoots("/Users/u", "/")
	var got []string
	for _, r := range roots {
		got = append(got, r.path)
		if fda := strings.HasSuffix(r.path, "Containers"); r.needsFDA != fda {
			t.Errorf("%s needsFDA = %v", r.path, r.needsFDA)
		}
	}
	want := []string{
		"/Users/u/Library/Application Support", "/Users/u/Library/Caches",
		"/Users/u/Library/Containers", "/Users/u/Library/Group Containers",
		"/Users/u/Library/Logs", "/Users/u/Library/HTTPStorages", "/Users/u/Library/WebKit",
		"/Users/u/Library/Developer", "/Users/u",
		"/Library/Application Support", "/Library/Caches", "/Library/Logs",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("roots = %q", got)
	}
}

func TestGrowthTargets(t *testing.T) {
	home, sys := t.TempDir(), t.TempDir()
	support := filepath.Join(home, "Library", "Application Support")
	for _, d := range [][]string{{"LarkShell"}, {"Cleanzo"}, {"Google", "Chrome"}, {"Google", "DriveFS"}} {
		mustMkdir(t, filepath.Join(append([]string{support}, d...)...))
	}
	writeFile(t, filepath.Join(support, "loose.db"), 10)
	if err := os.Symlink(home, filepath.Join(support, "link")); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(home, "Library", "Containers", "com.x"))
	for _, d := range []string{".npm", ".Trash", ".ssh", "Documents"} {
		mustMkdir(t, filepath.Join(home, d))
	}
	writeFile(t, filepath.Join(home, ".zshrc"), 10)
	mustMkdir(t, filepath.Join(sys, "Library", "Application Support", "ToDesk"))

	roots := growthRoots(home, sys)
	self := filepath.Join(support, "Cleanzo")
	paths := func(ts []growthTarget) []string {
		var out []string
		for _, t := range ts {
			out = append(out, t.path)
		}
		slices.Sort(out)
		return out
	}
	want := []string{
		filepath.Join(home, ".npm"),
		filepath.Join(support, "Google", "Chrome"),
		filepath.Join(support, "Google", "DriveFS"),
		filepath.Join(support, "LarkShell"),
		filepath.Join(support, "loose.db"),
		filepath.Join(sys, "Library", "Application Support", "ToDesk"),
	}
	slices.Sort(want)
	if got := paths(growthTargets(roots, false, self)); !slices.Equal(got, want) {
		t.Fatalf("without FDA:\n got %q\nwant %q", got, want)
	}
	if got := paths(growthTargets(roots, true, self)); !slices.Contains(got, filepath.Join(home, "Library", "Containers", "com.x")) {
		t.Fatalf("with FDA, Containers missing: %q", got)
	}
}

// TestGrowthKeys: the apps in the given folders are known by bundle ID and by
// name. Which apps run is up to the machine, so that part is not asserted.
func TestGrowthKeys(t *testing.T) {
	dir := t.TempDir()
	makeApp(t, dir, "Foo.app", "com.example.foo", "Foo")
	keys := growthKeys([]string{dir})
	for _, name := range []string{"com.example.foo", "Foo"} {
		if !keys.owns(name) {
			t.Errorf("growthKeys does not know %q", name)
		}
	}
}

// TestGrowthBatteryEndsWithItsContext: the power source is read on mygo's main
// thread, which the test binary never runs, so that read never returns. The
// answer must still come when the context ends, and it must be plugged in and
// full, which never holds measuring back.
func TestGrowthBatteryEndsWithItsContext(t *testing.T) {
	type answer struct {
		onBattery bool
		percent   int
	}
	checkPlugged := func(t *testing.T, ctx context.Context) {
		t.Helper()
		got := make(chan answer, 1)
		go func() {
			on, pct := growthBattery(ctx)
			got <- answer{on, pct}
		}()
		select {
		case a := <-got:
			if want := (answer{false, 100}); a != want {
				t.Fatalf("growthBattery = %+v, want %+v", a, want)
			}
		case <-time.After(time.Second):
			t.Fatal("growthBattery still waits a second after its context ended")
		}
	}
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		checkPlugged(t, ctx)
	})
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		checkPlugged(t, ctx)
	})
}
