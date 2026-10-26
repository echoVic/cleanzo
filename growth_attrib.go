package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// growthOwner is what an entry counts under on the page.
type growthOwner struct {
	key     string // "app:<bundle id>", "macos:<bundle id>" or "dir:<path>", lower-cased
	name    string
	kind    string // "app", "macos", "dot" or "unknown"
	orphan  bool   // unknown, no installed app claims it, and the leftovers tab would list it: probably a leftover
	appPath string // the installed app, for Uninstall; "" unless Uninstall takes it (growthAppPaths)
}

// growthAliases map folder names that an app's own names do not match to
// names the app index knows the app by. 飞书 and Lark keep their data in
// LarkShell.
var growthAliases = map[string][]string{"larkshell": {"lark", "feishu", "飞书"}}

// growthTeamPrefix is the team ID that starts the names of Group Containers,
// as in UBF8T346G9.Office.
var growthTeamPrefix = regexp.MustCompile(`^[A-Z0-9]{10}\.`)

// growthAttributor tells which app an entry belongs to.
type growthAttributor struct {
	idx   *appIndex
	keys  appKeys
	paths map[string]string // lower-cased bundle ID → app bundle
}

// growthAttributors caches the installed apps for a minute. The page and the
// menu ask often, and reading every Info.plist takes a moment on a Mac with
// many apps.
var growthAttributors struct {
	sync.Mutex
	at time.Time
	a  growthAttributor
}

// currentGrowthAttributor returns the cached attributor and when it was built,
// building it again when it is more than a minute old.
func currentGrowthAttributor() (growthAttributor, time.Time) {
	growthAttributors.Lock()
	defer growthAttributors.Unlock()
	if growthAttributors.a.idx == nil || time.Since(growthAttributors.at) > time.Minute {
		dirs := appDirs()
		bundles := readGrowthBundles(dirs)
		growthAttributors.a = growthAttributor{idx: buildAppIndex(dirs), keys: growthKeys(bundles), paths: growthAppPaths(bundles)}
		growthAttributors.at = time.Now()
	}
	return growthAttributors.a, growthAttributors.at
}

// growthBundle is an app bundle in one of the app folders, and what names it.
type growthBundle struct {
	path string
	id   appIdentity
}

// readGrowthBundles lists the app bundles in dirs, folder by folder, and reads
// each one's Info.plist once, 16 at a time. A folder listed twice adds nothing
// the second time. growthKeys and growthAppPaths both work from this pass.
func readGrowthBundles(dirs []string) []growthBundle {
	var out []growthBundle
	seen := map[string]bool{}
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if strings.HasSuffix(e.Name(), ".app") && !seen[p] {
				seen[p] = true
				out = append(out, growthBundle{path: p})
			}
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i := range out {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i].id = readAppIdentity(out[i].path) // each goroutine fills its own bundle
		}()
	}
	wg.Wait()
	return out
}

// growthKeys collects the names, bundle IDs and bundle ID words of bundles,
// and the bundle IDs of the running apps. Unlike installedKeys it never opens
// the bundle of a running app: one started from Downloads, the Desktop or a
// volume would make macOS ask about that folder, and the monitor runs
// unattended.
func growthKeys(bundles []growthBundle) appKeys {
	k := newAppKeys()
	running, _, _ := runningApps() // the bundle paths it also returns are left alone
	for bid := range running {
		k.addBundleID(bid)
	}
	for _, b := range bundles {
		k.add(b.id)
	}
	return k
}

// growthAppPaths finds by bundle ID the bundles that Uninstall takes: one
// directly in /Applications or ~/Applications (installedApp). Apps in suite
// folders, Utilities, /System or input method folders get none, so their rows
// offer no Uninstall, which would refuse them. The first bundle wins.
func growthAppPaths(bundles []growthBundle) map[string]string {
	out := map[string]string{}
	for _, b := range bundles {
		bid := strings.ToLower(b.id.bid)
		if _, ok := out[bid]; ok || bid == "" || !installedApp(b.path) {
			continue
		}
		out[bid] = b.path
	}
	return out
}

// growthLeftoverCandidate reports whether collectOrphans (main.go), which fills
// the leftovers tab, looks at the entry at path: it sits directly in one of
// the ~/Library folders that tab scans, and is not a tool cache, a hidden
// item or Apple's own data. Only such an entry can be called a leftover on the
// page, because only on that tab can the user clean it.
func growthLeftoverCandidate(path string) bool {
	base, parent := filepath.Base(path), filepath.Dir(path)
	return slices.Contains(libraryRoots, filepath.Base(parent)) && filepath.Base(filepath.Dir(parent)) == "Library" &&
		!toolCacheNames[base] && !strings.HasPrefix(base, ".") &&
		!strings.HasPrefix(base, "com.apple.") && !appleDataNames[normalized(base)]
}

// owner names what an entry counts under. It never guesses: what the app
// index cannot place keeps its own folder name (spec §三).
func (a growthAttributor) owner(e growthEntry) growthOwner {
	base := filepath.Base(e.Path)
	if e.Kind == growthDot {
		return growthOwner{key: "dir:" + strings.ToLower(e.Path), name: base, kind: "dot"}
	}
	id := strings.TrimPrefix(growthTeamPrefix.ReplaceAllString(base, ""), "group.")
	if strings.HasPrefix(strings.ToLower(id), "com.apple.") {
		return growthOwner{key: "macos:" + strings.ToLower(id), name: id, kind: "macos"}
	}
	for _, n := range append([]string{id}, growthAliases[strings.ToLower(base)]...) {
		if app, ok := a.idx.lookup(n); ok {
			bid := strings.ToLower(app.BundleID)
			if strings.HasPrefix(bid, "com.apple.") {
				// Apple's own app, such as Dock or Music, found by its folder name:
				// macOS, under the app's name, and never uninstalled.
				return growthOwner{key: "macos:" + bid, name: app.Name, kind: "macos"}
			}
			return growthOwner{key: "app:" + bid, name: app.Name, kind: "app", appPath: a.paths[bid]}
		}
	}
	name := base
	if parent := filepath.Base(filepath.Dir(e.Path)); growthVendors[strings.ToLower(parent)] {
		name = filepath.Join(parent, base)
	}
	return growthOwner{
		key: "dir:" + strings.ToLower(e.Path), name: name, kind: "unknown",
		orphan: e.Kind == growthHome && growthLeftoverCandidate(e.Path) && !a.keys.owns(base),
	}
}
