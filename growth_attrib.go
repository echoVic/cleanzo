package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// growthOwner is what an entry counts under on the page.
type growthOwner struct {
	key     string // "app:<bundle id>", "macos:<bundle id>" or "dir:<path>", lower-cased
	name    string
	kind    string // "app", "macos", "dot" or "unknown"
	orphan  bool   // unknown, in ~/Library, and no installed app claims it: probably a leftover
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
// menu ask often, and reading every Info.plist takes about a second.
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
		growthAttributors.a = growthAttributor{idx: buildAppIndex(dirs), keys: installedKeys(), paths: growthAppPaths(dirs)}
		growthAttributors.at = time.Now()
	}
	return growthAttributors.a
}

// growthAppPaths finds the bundle of every app in dirs by bundle ID; the
// first folder wins, like the app index.
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
		orphan: e.Kind == growthHome && !a.keys.owns(base),
	}
}
