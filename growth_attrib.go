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
	appPath string // the installed app, for Uninstall; "" when it is not in an app folder
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
// menu ask often, and reading every Info.plist takes a few seconds on a Mac
// with many apps.
var growthAttributors struct {
	sync.Mutex
	at time.Time
	a  growthAttributor
}

// currentGrowthAttributor returns the cached attributor, rebuilding it when stale.
func currentGrowthAttributor() growthAttributor {
	growthAttributors.Lock()
	defer growthAttributors.Unlock()
	if growthAttributors.a.idx == nil || time.Since(growthAttributors.at) > time.Minute {
		dirs := appDirs()
		growthAttributors.a = growthAttributor{idx: buildAppIndex(dirs), keys: growthKeys(dirs), paths: growthAppPaths(dirs)}
		growthAttributors.at = time.Now()
	}
	return growthAttributors.a
}

// growthKeys collects the names, bundle IDs and bundle ID words of the apps in
// dirs, and the bundle IDs of the running apps. Unlike installedKeys it never
// opens the bundle of a running app: one started from Downloads, the Desktop
// or a volume would make macOS ask about that folder, and the monitor runs
// unattended.
func growthKeys(dirs []string) appKeys {
	var apps []string
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".app") {
				apps = append(apps, filepath.Join(dir, e.Name()))
			}
		}
	}
	k := newAppKeys()
	running, _, _ := runningApps() // the bundle paths it also returns are left alone
	for bid := range running {
		k.addBundleID(bid)
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 16)
	)
	for _, app := range apps {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			id := readAppIdentity(app)
			mu.Lock()
			defer mu.Unlock()
			k.add(id)
		}()
	}
	wg.Wait()
	return k
}

// growthAppPaths finds the bundle of every app in dirs by bundle ID; the
// first folder in dirs wins.
func growthAppPaths(dirs []string) map[string]string {
	out := map[string]string{}
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".app") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if bid := strings.ToLower(readAppIdentity(p).bid); bid != "" {
				if _, ok := out[bid]; !ok {
					out[bid] = p
				}
			}
		}
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
