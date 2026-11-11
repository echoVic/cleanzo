package main

import (
	"fmt"
	"path/filepath"
	"testing"
)

// resetUninstallListed forgets every uninstall detail on record.
func resetUninstallListed() {
	uninstallListed.Lock()
	defer uninstallListed.Unlock()
	uninstallListed.apps = nil
}

// The confirm lists what the uninstall detail found. A folder of the app's
// that appears after that stays where it is and is not counted; with no
// detail on record, as after that uninstall, everything found then is moved.
func TestUninstallMovesOnlyWhatItsDetailListed(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	stubUsage(t, &usageGuard{running: map[string]bool{}})
	resetUninstallListed()
	t.Cleanup(resetUninstallListed)
	app := makeFakeApp(t, filepath.Join(home, "Applications"), "TestApp")
	pref := filepath.Join(home, "Library", "Preferences", "com.example.testapp.plist")
	writeFile(t, pref, 256)

	d := Cleaner{}.AppUninstallDetail(app)
	if len(d.Items) != 2 || d.Items[0].Path != app || d.Items[1].Path != pref {
		t.Fatalf("detail items = %+v", d.Items)
	}
	cache := filepath.Join(home, "Library", "Caches", "com.example.testapp")
	writeFile(t, filepath.Join(cache, "a"), 8192)
	res := Cleaner{}.UninstallApp(app)
	if res.Moved != 2 || res.Freed != d.TotalSize || len(res.Failed) != 0 {
		t.Fatalf("uninstall = %+v, want the 2 listed items, %d bytes", res, d.TotalSize)
	}
	if exists(app) || exists(pref) || !exists(cache) {
		t.Fatalf("after: app %v, prefs %v, cache appeared since %v", exists(app), exists(pref), exists(cache))
	}

	// The uninstall dropped the record: the next one, without a detail, moves
	// what it finds.
	app = makeFakeApp(t, filepath.Join(home, "Applications"), "TestApp")
	res = Cleaner{}.UninstallApp(app)
	if res.Moved != 2 || len(res.Failed) != 0 || exists(app) || exists(cache) {
		t.Fatalf("without a detail: %+v, cache exists %v", res, exists(cache))
	}
}

// An uninstall refused because the app runs, or because its use could not be
// told, moves nothing and keeps its detail: trying again without reading the
// detail still leaves a folder that appeared since in place.
func TestRefusedUninstallKeepsItsDetail(t *testing.T) {
	for _, tc := range []struct {
		name   string
		refuse *usageGuard
	}{
		{"in use", &usageGuard{running: map[string]bool{"com.example.testapp": true}}},
		{"usage unknown", &usageGuard{failed: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			resetUninstallListed()
			t.Cleanup(resetUninstallListed)
			app := makeFakeApp(t, filepath.Join(home, "Applications"), "TestApp")
			pref := filepath.Join(home, "Library", "Preferences", "com.example.testapp.plist")
			writeFile(t, pref, 256)
			stubUsage(t, &usageGuard{running: map[string]bool{}})
			d := Cleaner{}.AppUninstallDetail(app)
			cache := filepath.Join(home, "Library", "Caches", "com.example.testapp")
			writeFile(t, filepath.Join(cache, "a"), 8192)

			stubUsage(t, tc.refuse)
			if res := (Cleaner{}).UninstallApp(app); res.Moved != 0 || len(res.Failed) != 1 || !exists(app) {
				t.Fatalf("refused: %+v", res)
			}
			stubUsage(t, &usageGuard{running: map[string]bool{}})
			res := Cleaner{}.UninstallApp(app)
			if res.Moved != 2 || res.Freed != d.TotalSize || len(res.Failed) != 0 || exists(app) || exists(pref) || !exists(cache) {
				t.Fatalf("again: %+v, want the 2 listed items, %d bytes; cache appeared since exists %v", res, d.TotalSize, exists(cache))
			}
		})
	}
}

// Only the last details' paths are kept, the latest detail of an app
// replacing its earlier one, and a detail that lists no bundle leaves none.
func TestUninstallListedIsBounded(t *testing.T) {
	resetUninstallListed()
	t.Cleanup(resetUninstallListed)
	listed := func(app string) []UninstallItem {
		return []UninstallItem{{Path: app, Kind: "app"}, {Path: app + ".plist", Kind: "prefs"}}
	}
	apps := make([]string, uninstallListedMax+1)
	for i := range apps {
		apps[i] = fmt.Sprintf("/Applications/A%d.app", i)
	}
	for _, a := range apps[:uninstallListedMax] {
		rememberUninstallListed(a, listed(a))
	}
	rememberUninstallListed(apps[0], listed(apps[0])) // read again: now the latest
	rememberUninstallListed(apps[uninstallListedMax], listed(apps[uninstallListedMax]))
	if _, ok := takeUninstallListed(apps[1]); ok {
		t.Fatal("the oldest record was kept past the bound")
	}
	for _, a := range []string{apps[0], apps[2], apps[uninstallListedMax]} {
		paths, ok := takeUninstallListed(a)
		if !ok || len(paths) != 2 || !paths[a] || !paths[a+".plist"] {
			t.Fatalf("%s: %v %v", a, paths, ok)
		}
		if _, ok := takeUninstallListed(a); ok {
			t.Fatalf("%s: still on record after it was taken", a)
		}
	}
	rememberUninstallListed(apps[2], listed(apps[2]))
	rememberUninstallListed(apps[2], nil)
	if _, ok := takeUninstallListed(apps[2]); ok {
		t.Fatal("a detail without the bundle kept the earlier record")
	}
}
