package main

import (
	"context"
	"maps"
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
	// Most roots do not exist in this home: a missing root is not one that
	// could not be listed.
	targets, unlisted := growthTargets(roots, false, self)
	if got := paths(targets); !slices.Equal(got, want) || unlisted {
		t.Fatalf("without FDA:\n got %q, unlisted %v\nwant %q, listed", got, unlisted, want)
	}
	if targets, _ := growthTargets(roots, true, self); !slices.Contains(paths(targets), filepath.Join(home, "Library", "Containers", "com.x")) {
		t.Fatalf("with FDA, Containers missing: %q", paths(targets))
	}
}

// TestGrowthTargetsReportsUnlistedFolders: a root or a vendor folder that
// exists but cannot be listed leaves its entries out of the run, which must
// then say so. Otherwise they would count as new from zero the next day.
func TestGrowthTargetsReportsUnlistedFolders(t *testing.T) {
	for _, locked := range [][]string{
		{"Library", "Caches"},                        // a root
		{"Library", "Application Support", "Google"}, // a vendor folder
	} {
		home, sys := t.TempDir(), t.TempDir()
		writeFile(t, filepath.Join(home, "Library", "Caches", "Foo", "f"), 10)
		writeFile(t, filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "f"), 10)
		dir := filepath.Join(append([]string{home}, locked...)...)
		if err := os.Chmod(dir, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		if _, unlisted := growthTargets(growthRoots(home, sys), false, ""); !unlisted {
			t.Errorf("%s could not be listed, and growthTargets did not say so", dir)
		}
	}
}

// TestGrowthAppPathsKeepsOnlyInstalledApps: Uninstall takes only a bundle
// directly in /Applications or ~/Applications (installedApp). An app in a
// suite folder, like those in Utilities, /System or an input method folder,
// gets no bundle, so its row offers no Uninstall.
func TestGrowthAppPathsKeepsOnlyInstalledApps(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	apps := filepath.Join(home, "Applications")
	suite := filepath.Join(apps, "Suite")
	foo := makeApp(t, apps, "Foo.app", "com.example.Foo", "Foo")
	makeApp(t, suite, "Bar.app", "com.example.bar", "Bar")
	got := growthAppPaths(readGrowthBundles([]string{apps, suite}))
	if want := map[string]string{"com.example.foo": foo}; !maps.Equal(got, want) {
		t.Fatalf("growthAppPaths = %v, want %v", got, want)
	}
}

// TestGrowthKeys: the apps in the given folders are known by bundle ID and by
// name. Which apps run is up to the machine, so that part is not asserted.
func TestGrowthKeys(t *testing.T) {
	dir := t.TempDir()
	makeApp(t, dir, "Foo.app", "com.example.foo", "Foo")
	keys := growthKeys(readGrowthBundles([]string{dir}))
	for _, name := range []string{"com.example.foo", "Foo"} {
		if !keys.owns(name) {
			t.Errorf("growthKeys does not know %q", name)
		}
	}
}

// readGrowthBundles reads each bundle once, also from a folder listed twice
// (appDirs lists Utilities as a suite too), and keeps the folders' order.
func TestReadGrowthBundlesReadsEachBundleOnce(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	makeApp(t, a, "Foo.app", "com.example.foo", "Foo")
	makeApp(t, b, "Bar.app", "com.example.bar", "Bar")
	got := readGrowthBundles([]string{a, b, a})
	if len(got) != 2 || got[0].path != filepath.Join(a, "Foo.app") || got[0].id.bid != "com.example.foo" ||
		got[1].path != filepath.Join(b, "Bar.app") || got[1].id.name != "Bar" {
		t.Fatalf("bundles = %+v", got)
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
