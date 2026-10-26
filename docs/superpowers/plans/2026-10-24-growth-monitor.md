# Growth Monitor — Implementation Plan

> **For agentic workers:** In this project the tasks are executed by the Codex CLI, several tasks per run (see "Execution batches"). **Executors do not commit**: the reviewer checks and commits each task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Cleanzo records once a day how much every app's data takes, keeps 90 days of records, shows per-app size and growth on a new 「占用增长」 page, and posts a notification when an app grows past a threshold. While monitoring is on it stays in the menu bar and can open at login.

**Architecture:**
- **Go backend, seven new files.** Together they cover:
  - records and what to measure (`growth.go`);
  - which app owns a folder (`growth_attrib.go`);
  - measuring on background-QoS threads (`growth_measure.go`);
  - the record files (`growth_store.go`);
  - comparing records (`growth_eval.go`);
  - the schedule and bound methods (`growth_monitor.go`, `growth_api.go`);
  - the menu bar, windows and login item (`growth_app.go`).
- **Small platform files.** They hold the macOS root table, battery reading and thread QoS. Their Windows counterparts are stubs: `growthSupported()` is false on Windows.
- **Attribution happens when records are read**, not when they are stored. Records hold only paths, sizes and flags, so a better alias table applies to old records too.
- **The page** is `src/react/growth/`, wired like the other React pages. The settings page gains one section and the status page one alert. The apps page gains a request hook, so 「卸载」 reuses its dialog.
- **Nothing here deletes anything.** The only removal is pruning the monitor's own record files, which are older than 90 days.

**Tech Stack:** Go 1.27 + mygo 0.6.2 (tray, notifications, login item, events, `Power`) + purego (already a dependency). TypeScript 7 + React 19, bun test. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-24-growth-monitor-design.md` (Chinese). Section numbers (§三 etc.) refer to it.

## Global Constraints

- **No new dependencies**, Go or npm. Do not run commands that need the network.
- **No new deletion paths.** No new call to `trashItem`, `os.Remove*`, `cleanEntry` or a tool command.
  - The only exception is `saveGrowthSnapshot` pruning its own `*.json` records inside `growthDir()`.
  - Folders outside the home folder are only measured and revealed.
- **Never read `~/Library/Containers` or `~/Library/Group Containers` without Full Disk Access** (`fullDiskAccess()` false). Reading them makes macOS ask about other apps' data, and the monitor runs unattended.
- **Platform rules**, enforced by existing tests in `testenv_test.go`:
  - Exported Go names and `Cleaner` methods live in untagged files. `_darwin.go` / `_windows.go` files hold unexported helpers only (`TestBindingsArePlatformNeutral`).
  - Build paths with `filepath.Join`, never `p+"/"`, `"/"+p`, `"~/"` or `strings.Split(p, "/")` (`TestNoSlashPathLiterals`).
  - Windows must build and test green: `growthSupported()` is false there, the page's nav item is hidden, and no tray, login item or measuring starts.
- **Bindings are generated:** after changing bound methods, events or their types, run `bun run generate`. Never edit `src/mygo.ts` by hand.
- **Copy:**
  - Every new string has `zh` and `en`.
  - Page strings go in `src/i18n.ts`.
  - Strings Go shows (notifications, menu bar) go in `src/native-i18n.json`, which the page dictionary also spreads in. Use the keys and texts listed in Tasks 5–9 verbatim.
  - Sizes in Go use `growthSize`, which matches `human()` in `src/shared.ts`: 1024-based units, one decimal below 100.
- **Style:**
  - Go doc comments are full sentences, like the existing code.
  - TypeScript keeps terse Chinese comments, and every new export gets a one-line doc comment.
  - New CSS classes use the `cz-growth-` prefix and are not wrapped in `@layer`. `tests/css-tokens.test.ts` stays green; durations come from the motion tokens.
- **Do not edit** `交付/`, `build/`, `dist/`, `outputs/`.
- **After each task** these must pass:
  - Go: `gofmt -l .` (no output), `go vet ./...`, `go test -race -count=1 ./...`, `GOOS=windows GOARCH=amd64 go vet ./...`
  - Frontend (Tasks 7–10): `bun run typecheck`, `bun run test:web`, `bun run build:web -- --outDir "$TMPDIR/cleanzo-growth-web"`
  - Add every new frontend test file to the `test:web` script.

## Review Focus

1. **No access prompts and no deletion.** Without Full Disk Access the target list has no Containers or Group Containers entries, and nothing in the diff removes files outside `growthDir()/records`. *Tasks 1, 3.*
2. **Measuring:**
   - It never follows links or crosses volumes.
   - A timed-out entry is `Complete: false` with the bytes counted so far.
   - Each worker thread is pinned and dropped at background QoS, never unlocked. *Task 2.*
3. **Growth math matches §五:**
   - the day window is 20–28 h, the latest record in it;
   - the week window is 3–7 days, the earliest record in it;
   - the change is net;
   - a lower bound applies only when the base entry was complete;
   - records with different FDA never compare;
   - a cut base never makes new entries count;
   - the first record is only a baseline. *Task 4.*
4. **Notification limits:** one per app a day, a week alert once a week, three a day in all, largest first. *Task 4.*
5. **Monitoring off behaves exactly like today:**
   - closing the window quits;
   - there is no tray icon and no login item;
   - Windows never takes the new paths. *Task 6.*
6. **Background runs yield.** A user scan or clean cancels a background run, and nothing is saved from it; 「现在量一次」 is not cancelled. *Task 5.*
7. **Opened at login with monitoring on** shows no window, and the Dock icon returns when a window opens. *Task 6, checked by hand.*
8. **The page reveals and routes, never removes.** 「卸载」 opens the existing dialog on the apps page, and folders outside home show the note instead of an action. *Tasks 7–8.*

---

## File Structure

| File | Responsibility |
|---|---|
| `growth.go` (new) | Record types (`growthEntry`, `growthSnapshot`), roots/targets, `growthTargets` |
| `growth_attrib.go` (new) | `growthOwner`, `growthAttributor` (app index + aliases + vendor folders + Group Containers prefixes), one-minute cache |
| `growth_measure.go` (new) | `measureGrowthEntry` (walker with running total), `growthOrder`, `takeGrowthSnapshot` (four background workers, limits) |
| `growth_store.go` (new) | `growthDir`, records (save/prune/load/last), `GrowthConfig` (defaults/validate/save), `growthNotified` log |
| `growth_eval.go` (new) | `GrowthDelta`, `GrowthAlert`, `growthBase`, `growthChanges`, `evaluateGrowth`, `buildGrowthApps`, `pickGrowthNotifications` |
| `growth_monitor.go` (new) | `growthMonitor` (schedule, run, cancel), `growthNextRun`, `growthDeferral`, `userScan`, seams, `nativeTextFor`, `growthSize`, notification posting |
| `growth_api.go` (new) | Page types (`GrowthState`, `GrowthStatus`, `GrowthApp`, `GrowthFolder`), events, the three `Cleaner` methods |
| `growth_app.go` (new) | Main window open/reopen, lifecycle handlers, tray menu model + mygo menu, `applyGrowthLifecycle`, `focusGrowth`, `growthIconPNG` |
| `growth_roots_darwin.go` / `growth_roots_windows.go` (new) | `growthRoots`, `growthSupported`, `growthBattery` |
| `growth_qos_darwin.go` / `growth_qos_windows.go` (new) | `backgroundThread` |
| `fs_darwin.go`, `fs_windows.go` | add `deviceOf` |
| `main.go` | extract `newMainWindow`, call `registerGrowthLifecycle` and `startGrowthResident`; `userScan()` in `FullScan`, `LargeScan` |
| `junk.go` | `userScan()` in `ScanJunk`, `CleanJunk` |
| `smoke.go` | add `"growth"` to `smokeViews` |
| `src/native-i18n.json` | notification and menu bar strings |
| `src/react/growth/{api.ts,format.ts,hint.ts,GrowthPage.tsx,entry.ts,mount.tsx}` (new) | the page |
| `src/react/apps/{mount.tsx,AppsPage.tsx,InstalledTab.tsx,entry.ts}` | `requestAppsUninstall` |
| `src/react/settings/SettingsPage.tsx`, `src/react/settings/api.ts` | 「占用增长」 section |
| `src/react/status/{StatusPage.tsx,entry.ts}` | growth alert, `setGoGrowthHandler` |
| `index.html`, `src/shared.ts`, `src/main.ts`, `src/i18n.ts`, `src/style.css`, `src/dev/mock-mygo.ts` | nav item, view, wiring, strings, styles, mock |
| `tests/growth-react.test.ts` (new), `package.json` | frontend tests |
| `README.md`, `CHANGELOG.md` | docs |
| Go tests (new) | `growth_attrib_test.go`, `growth_darwin_test.go`, `growth_measure_test.go`, `growth_measure_darwin_test.go`, `growth_store_test.go`, `growth_eval_test.go`, `growth_monitor_test.go`, `growth_app_test.go` |

---

### Task 1: Records, targets and attribution

**Files:**
- Create: `growth.go`, `growth_attrib.go`, `growth_roots_darwin.go`, `growth_roots_windows.go`
- Test: `growth_attrib_test.go`, `growth_darwin_test.go`

**Interfaces:**
- Produces:
  - `type growthRootKind string`, with the constants `growthHome`, `growthDot` and `growthSystem`.
  - `type growthRoot struct{ path string; kind growthRootKind; needsFDA bool }`
  - `type growthTarget struct{ path string; kind growthRootKind }`
  - `type growthEntry struct{ Path string; Kind growthRootKind; Size int64; Complete, Denied bool; Children map[string]int64 }`
  - `type growthSnapshot struct{ At int64; FDA, Cut bool; Entries []growthEntry }`
  - `func growthTargets(roots []growthRoot, fda bool, self string) []growthTarget`
  - `func growthRoots(home, system string) []growthRoot`, `func growthSupported() bool` and `func growthBattery(ctx context.Context) (onBattery bool, percent int)`. All three are per platform.
  - `type growthOwner struct{ key, name, kind string; orphan bool; appPath string }`
  - `type growthAttributor`, with `func (a growthAttributor) owner(e growthEntry) growthOwner`, and `func currentGrowthAttributor() growthAttributor`.

- [ ] **Step 1: Write the failing tests**

`growth_attrib_test.go`:

```go
package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// testGrowthAttributor knows Lark (as 飞书's LarkShell folder), WeChat,
// Chrome and Trae CN, like an app index built from /Applications.
func testGrowthAttributor() growthAttributor {
	idx := &appIndex{keys: map[string]appRef{}}
	add := func(bid, name string, keys ...string) {
		ref := appRef{BundleID: bid, Name: name}
		for _, k := range append(keys, bid, name) {
			idx.keys[strings.ToLower(k)] = ref
		}
	}
	add("com.electron.lark", "Lark", "Feishu")
	add("com.tencent.xinWeChat", "微信", "WeChat")
	add("com.google.Chrome", "Google Chrome", "Chrome")
	add("com.trae.cn", "Trae CN")
	keys := newAppKeys()
	keys.add(appIdentity{folder: "Lark", name: "Lark", bid: "com.electron.lark"})
	keys.add(appIdentity{folder: "WeChat", name: "微信", bid: "com.tencent.xinWeChat"})
	keys.add(appIdentity{folder: "Google Chrome", name: "Google Chrome", bid: "com.google.Chrome"})
	return growthAttributor{idx: idx, keys: keys, paths: map[string]string{"com.electron.lark": filepath.Join("/Applications", "Lark.app")}}
}

func TestGrowthOwner(t *testing.T) {
	a := testGrowthAttributor()
	lib := filepath.Join("/h", "Library")
	dir := func(p string) string { return "dir:" + strings.ToLower(p) }
	cases := []struct {
		path    string
		kind    growthRootKind
		key     string
		name    string
		okind   string
		orphan  bool
		appPath string
	}{
		{filepath.Join(lib, "Application Support", "LarkShell"), growthHome, "app:com.electron.lark", "Lark", "app", false, filepath.Join("/Applications", "Lark.app")},
		{filepath.Join(lib, "Caches", "LarkShell"), growthHome, "app:com.electron.lark", "Lark", "app", false, filepath.Join("/Applications", "Lark.app")},
		{filepath.Join(lib, "Containers", "com.tencent.xinWeChat"), growthHome, "app:com.tencent.xinwechat", "微信", "app", false, ""},
		{filepath.Join(lib, "Group Containers", "5A4RE8SF68.com.tencent.xinWeChat"), growthHome, "app:com.tencent.xinwechat", "微信", "app", false, ""},
		{filepath.Join(lib, "Group Containers", "group.com.apple.notes"), growthHome, "macos:com.apple.notes", "com.apple.notes", "macos", false, ""},
		{filepath.Join(lib, "Caches", "com.apple.geod"), growthHome, "macos:com.apple.geod", "com.apple.geod", "macos", false, ""},
		{filepath.Join(lib, "Caches", "Google", "Chrome"), growthHome, "app:com.google.chrome", "Google Chrome", "app", false, ""},
		{filepath.Join(lib, "Caches", "Google", "DriveFS"), growthHome, dir(filepath.Join(lib, "Caches", "Google", "DriveFS")), filepath.Join("Google", "DriveFS"), "unknown", true, ""},
		{filepath.Join(lib, "Application Support", "Trae CN"), growthHome, "app:com.trae.cn", "Trae CN", "app", false, ""},
		{filepath.Join("/h", ".npm"), growthDot, dir(filepath.Join("/h", ".npm")), ".npm", "dot", false, ""},
		{filepath.Join(lib, "Application Support", "GoneApp"), growthHome, dir(filepath.Join(lib, "Application Support", "GoneApp")), "GoneApp", "unknown", true, ""},
		// an installed app claims it loosely (contains "wechat"): unknown, but not a leftover
		{filepath.Join(lib, "Application Support", "WeChatFiles"), growthHome, dir(filepath.Join(lib, "Application Support", "WeChatFiles")), "WeChatFiles", "unknown", false, ""},
		// /Library is never a leftover: Cleanzo does not clean there
		{filepath.Join("/", "Library", "Application Support", "GoneApp"), growthSystem, dir(filepath.Join("/", "Library", "Application Support", "GoneApp")), "GoneApp", "unknown", false, ""},
	}
	for _, c := range cases {
		got := a.owner(growthEntry{Path: c.path, Kind: c.kind})
		want := growthOwner{key: c.key, name: c.name, kind: c.okind, orphan: c.orphan, appPath: c.appPath}
		if got != want {
			t.Errorf("%s: got %+v, want %+v", c.path, got, want)
		}
	}
}
```

`growth_darwin_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -run 'TestGrowthOwner|TestGrowthRootsCoverSpec|TestGrowthTargets' -count=1 .`
Expected: FAIL to compile, because `growthAttributor`, `growthRoots` and the other new names are undefined.

- [ ] **Step 3: Write `growth.go`**

```go
package main

// The growth monitor (docs/superpowers/specs/2026-10-24-growth-monitor-design.md)
// records once a day how much every app's data folders take, keeps 90 days
// of records, and tells the user when an app grows fast.
//
// This file holds the record types and decides what gets measured. The other
// files cover the rest:
//   - growth_measure.go measures;
//   - growth_store.go keeps the files;
//   - growth_eval.go compares records;
//   - growth_monitor.go schedules;
//   - growth_app.go runs the menu bar side.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// growthRootKind tells where an entry lives.
type growthRootKind string

const (
	growthHome   growthRootKind = "home"   // a folder of ~/Library
	growthDot    growthRootKind = "dot"    // a dot folder of the home folder
	growthSystem growthRootKind = "system" // a folder of /Library: measured, never removed
)

// growthRoot is a folder whose first-level items are measured one by one.
type growthRoot struct {
	path     string
	kind     growthRootKind
	needsFDA bool // Containers and Group Containers: left out without Full Disk Access
}

// growthTarget is one entry to measure.
type growthTarget struct {
	path string
	kind growthRootKind
}

// growthEntry is one measured entry of a record.
type growthEntry struct {
	Path     string           `json:"path"`
	Kind     growthRootKind   `json:"kind"`
	Size     int64            `json:"size"`               // bytes on disk; a lower bound when !Complete
	Complete bool             `json:"complete"`           // measured to the end within its time limit
	Denied   bool             `json:"denied"`             // some folders inside could not be read
	Children map[string]int64 `json:"children,omitempty"` // first-level children, for big entries (growth_measure.go)
}

// growthSnapshot is one record: what one run measured.
type growthSnapshot struct {
	At      int64         `json:"at"`  // unix seconds when the run started
	FDA     bool          `json:"fda"` // Full Disk Access during the run
	Cut     bool          `json:"cut"` // the run ended at its time limit before every entry started
	Entries []growthEntry `json:"entries"`
}

// growthSkipDot are dot folders never measured: the Trash holds what the
// user deleted, and the others hold keys.
var growthSkipDot = map[string]bool{".trash": true, ".ssh": true, ".gnupg": true}

// growthVendors are folders named after a vendor rather than an app. Each of
// their children is an entry of its own, so Google/Chrome counts for Chrome.
var growthVendors = map[string]bool{"google": true, "microsoft": true, "jetbrains": true, "adobe": true, "bravesoftware": true}

// growthTargets lists what one run measures (spec §三):
//   - the first-level items of every root, with vendor folders one level deeper;
//   - dot folders only from the home folder.
//
// Links are never followed, and Cleanzo's own folder is left out. Roots that
// need Full Disk Access are skipped without it, so measuring never makes
// macOS ask about other apps' data.
func growthTargets(roots []growthRoot, fda bool, self string) []growthTarget {
	skip := func(e fs.DirEntry) bool {
		return e.Type()&fs.ModeSymlink != 0 || e.Name() == ".DS_Store" || e.Name() == ".localized"
	}
	var out []growthTarget
	for _, r := range roots {
		if r.needsFDA && !fda {
			continue
		}
		entries, err := os.ReadDir(r.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			p := filepath.Join(r.path, name)
			if skip(e) || canonical(p) == canonical(self) {
				continue
			}
			if r.kind == growthDot {
				if e.IsDir() && strings.HasPrefix(name, ".") && !growthSkipDot[strings.ToLower(name)] {
					out = append(out, growthTarget{path: p, kind: r.kind})
				}
				continue
			}
			if e.IsDir() && growthVendors[strings.ToLower(name)] {
				children, err := os.ReadDir(p)
				if err != nil {
					continue
				}
				for _, c := range children {
					if !skip(c) {
						out = append(out, growthTarget{path: filepath.Join(p, c.Name()), kind: r.kind})
					}
				}
				continue
			}
			out = append(out, growthTarget{path: p, kind: r.kind})
		}
	}
	return out
}
```

- [ ] **Step 4: Write `growth_attrib.go`**

```go
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
```

- [ ] **Step 5: Write the platform files**

`growth_roots_darwin.go`:

```go
package main

import (
	"context"
	"path/filepath"

	"github.com/egoist/mygo"
)

// growthRoots are the folders whose entries are measured on macOS (spec §三).
// system is "/" in the app and a temporary folder in tests.
func growthRoots(home, system string) []growthRoot {
	lib := filepath.Join(home, "Library")
	sys := filepath.Join(system, "Library")
	return []growthRoot{
		{path: filepath.Join(lib, "Application Support"), kind: growthHome},
		{path: filepath.Join(lib, "Caches"), kind: growthHome},
		{path: filepath.Join(lib, "Containers"), kind: growthHome, needsFDA: true},
		{path: filepath.Join(lib, "Group Containers"), kind: growthHome, needsFDA: true},
		{path: filepath.Join(lib, "Logs"), kind: growthHome},
		{path: filepath.Join(lib, "HTTPStorages"), kind: growthHome},
		{path: filepath.Join(lib, "WebKit"), kind: growthHome},
		{path: filepath.Join(lib, "Developer"), kind: growthHome},
		{path: home, kind: growthDot},
		{path: filepath.Join(sys, "Application Support"), kind: growthSystem},
		{path: filepath.Join(sys, "Caches"), kind: growthSystem},
		{path: filepath.Join(sys, "Logs"), kind: growthSystem},
	}
}

// growthSupported tells whether this platform has the growth monitor.
func growthSupported() bool { return true }

// growthBattery reports whether the Mac runs on battery and its charge. A
// desktop Mac, or a battery that cannot be read, counts as plugged in.
func growthBattery(ctx context.Context) (onBattery bool, percent int) {
	if !mygo.Power.IsOnBattery() {
		return false, 100
	}
	if b := statusBattery(ctx); b != nil {
		return true, b.Percent
	}
	return true, 100
}
```

`growth_roots_windows.go`:

```go
package main

import "context"

// growthRoots is empty: the first version of the growth monitor is macOS only.
func growthRoots(home, system string) []growthRoot { return nil }

// growthSupported tells whether this platform has the growth monitor.
func growthSupported() bool { return false }

// growthBattery is never consulted on Windows.
func growthBattery(context.Context) (bool, int) { return false, 100 }
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -run 'TestGrowthOwner|TestGrowthRootsCoverSpec|TestGrowthTargets' -count=1 . && GOOS=windows GOARCH=amd64 go vet ./...`
Expected: PASS, and vet reports nothing.

- [ ] **Step 7: Leave the changes uncommitted for the reviewer**

---

### Task 2: Measuring entries on background threads

**Files:**
- Create: `growth_measure.go`, `growth_qos_darwin.go`, `growth_qos_windows.go`
- Modify: `fs_darwin.go`, `fs_windows.go` (add `deviceOf`)
- Test: `growth_measure_test.go`, `growth_measure_darwin_test.go`

**Interfaces:**
- Consumes: `growthTarget`, `growthEntry` and `growthSnapshot` (Task 1), and `onDisk` (fs_*.go).
- Produces:
  - `func measureGrowthEntry(ctx context.Context, t growthTarget) growthEntry`
  - `func growthOrder(targets []growthTarget, prev *growthSnapshot) []growthTarget`
  - `func takeGrowthSnapshot(ctx context.Context, targets []growthTarget, prev *growthSnapshot, fda bool, now time.Time) growthSnapshot`
  - The variables `growthEntryLimit` and `growthRunLimit` (`time.Duration`), and `growthChildEntry` and `growthChildFloor` (`int64`).
  - `func backgroundThread()`

- [ ] **Step 1: Write the failing tests**

`growth_measure_test.go`:

```go
package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMeasureGrowthEntrySumsAndRecordsChildren(t *testing.T) {
	entry, floor := growthChildEntry, growthChildFloor
	growthChildEntry, growthChildFloor = 1, 1
	t.Cleanup(func() { growthChildEntry, growthChildFloor = entry, floor })
	root := filepath.Join(t.TempDir(), "ToDesk")
	writeFile(t, filepath.Join(root, "dumps", "a.dmp"), 5000)
	writeFile(t, filepath.Join(root, "dumps", "b.dmp"), 5000)
	writeFile(t, filepath.Join(root, "logs", "x.log"), 100)
	e := measureGrowthEntry(context.Background(), growthTarget{path: root, kind: growthSystem})
	if !e.Complete || e.Denied || e.Kind != growthSystem || e.Path != root {
		t.Fatalf("entry = %+v", e)
	}
	if e.Size != 10100 || e.Children["dumps"] != 10000 || e.Children["logs"] != 100 {
		t.Fatalf("size %d, children %v", e.Size, e.Children)
	}
}

func TestMeasureGrowthEntryKeepsOnlyBigChildren(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "dumps", "a.dmp"), 5000)
	if e := measureGrowthEntry(context.Background(), growthTarget{path: root, kind: growthHome}); e.Children != nil {
		t.Fatalf("children recorded for a small entry: %v", e.Children)
	}
}

func TestMeasureGrowthEntryStopsAtItsDeadline(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a"), 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := measureGrowthEntry(ctx, growthTarget{path: root, kind: growthHome}); e.Complete {
		t.Fatalf("a stopped walk must be incomplete: %+v", e)
	}
}

func TestMeasureGrowthEntryFileAndVanished(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "loose.db")
	writeFile(t, f, 300)
	if e := measureGrowthEntry(context.Background(), growthTarget{path: f, kind: growthHome}); e.Size != 300 || !e.Complete {
		t.Fatalf("file = %+v", e)
	}
	if e := measureGrowthEntry(context.Background(), growthTarget{path: filepath.Join(dir, "gone"), kind: growthHome}); e.Size != 0 || !e.Complete || e.Denied {
		t.Fatalf("vanished = %+v", e)
	}
}

func TestGrowthOrderPutsLastRecordsLargestFirst(t *testing.T) {
	ts := []growthTarget{{path: "a"}, {path: "b"}, {path: "c"}, {path: "new"}}
	prev := &growthSnapshot{Entries: []growthEntry{{Path: "a", Size: 5}, {Path: "b", Size: 1}, {Path: "c", Size: 9}}}
	var got []string
	for _, t := range growthOrder(ts, prev) {
		got = append(got, t.path)
	}
	if want := []string{"c", "a", "b", "new"}; !slices.Equal(got, want) {
		t.Fatalf("order = %q, want %q", got, want)
	}
}

func TestTakeGrowthSnapshot(t *testing.T) {
	dir := t.TempDir()
	var ts []growthTarget
	for _, n := range []string{"a", "b", "c"} {
		p := filepath.Join(dir, n)
		writeFile(t, filepath.Join(p, "f"), 10)
		ts = append(ts, growthTarget{path: p, kind: growthHome})
	}
	s := takeGrowthSnapshot(context.Background(), ts, nil, true, time.Unix(1000, 0))
	if s.Cut || len(s.Entries) != 3 || s.At != 1000 || !s.FDA || !slices.IsSortedFunc(s.Entries, func(a, b growthEntry) int { return strings.Compare(a.Path, b.Path) }) {
		t.Fatalf("snapshot = %+v", s)
	}
	limit := growthRunLimit
	growthRunLimit = 0
	t.Cleanup(func() { growthRunLimit = limit })
	if cut := takeGrowthSnapshot(context.Background(), ts, nil, true, time.Unix(1000, 0)); !cut.Cut || len(cut.Entries) != 0 {
		t.Fatalf("a run out of time must be cut and keep nothing unstarted: %+v", cut)
	}
}
```

`growth_measure_darwin_test.go`:

```go
package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMeasureGrowthEntryNeverFollowsLinks(t *testing.T) {
	root, big := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(big, "huge"), 50000)
	writeFile(t, filepath.Join(root, "small"), 10)
	if err := os.Symlink(big, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if e := measureGrowthEntry(context.Background(), growthTarget{path: root, kind: growthHome}); e.Size != 10 {
		t.Fatalf("size %d: the link was followed", e.Size)
	}
}

func TestMeasureGrowthEntryMarksUnreadableParts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok"), 10)
	locked := filepath.Join(root, "locked")
	writeFile(t, filepath.Join(locked, "secret"), 10)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	e := measureGrowthEntry(context.Background(), growthTarget{path: root, kind: growthHome})
	if !e.Denied || !e.Complete || e.Size != 10 {
		t.Fatalf("entry = %+v", e)
	}
}

func TestBackgroundThreadReturns(t *testing.T) {
	done := make(chan struct{})
	go func() {
		backgroundThread()
		close(done)
	}()
	<-done
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -run 'MeasureGrowth|GrowthOrder|TakeGrowthSnapshot|BackgroundThread' -count=1 .`
Expected: FAIL to compile, because `measureGrowthEntry` and the other new names are undefined.

- [ ] **Step 3: Add `deviceOf`**

In `fs_darwin.go`, below `onDisk`, add the following. `syscall` is imported already if `changedAt` uses `Stat_t`; otherwise add the import.

```go
// deviceOf names the volume an entry lives on, to keep walks on one volume.
func deviceOf(info fs.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}
```

In `fs_windows.go`:

```go
// deviceOf is constant on Windows, where the growth monitor does not run.
func deviceOf(fs.FileInfo) uint64 { return 0 }
```

- [ ] **Step 4: Write `growth_measure.go`**

```go
package main

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Limits of spec §四. They are variables only so tests can shorten them.
var (
	growthEntryLimit = 5 * time.Minute  // one entry, as long as Space Analysis allows a big folder
	growthRunLimit   = 15 * time.Minute // the whole run
)

// growthWorkers measure at the same time (spec §四).
const growthWorkers = 4

// Entries of growthChildEntry bytes or more also record their first-level
// children of growthChildFloor bytes or more, so a notification can name
// ToDesk/dumps rather than ToDesk. These are variables for tests.
var (
	growthChildEntry int64 = 1 << 30
	growthChildFloor int64 = 64 << 20
)

// errGrowthStop ends a walk whose time ran out.
var errGrowthStop = errors.New("growth: time limit")

// measureGrowthEntry walks t without following links or leaving its volume,
// summing what files take on disk the way the rest of Cleanzo counts
// (onDisk).
//   - When ctx ends first, the sum so far comes back with Complete false:
//     a lower bound.
//   - Folders that cannot be read set Denied, and the walk goes on.
//   - An entry that vanished since it was listed measures as an empty,
//     complete one.
func measureGrowthEntry(ctx context.Context, t growthTarget) growthEntry {
	e := growthEntry{Path: t.path, Kind: t.kind}
	root, err := os.Lstat(t.path)
	if err != nil {
		e.Complete, e.Denied = true, !errors.Is(err, fs.ErrNotExist)
		return e
	}
	if !root.IsDir() {
		e.Size, e.Complete = onDisk(root), true
		return e
	}
	dev := deviceOf(root)
	children := map[string]int64{}
	walkErr := filepath.WalkDir(t.path, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return errGrowthStop
		}
		if err != nil {
			e.Denied = true
			return nil
		}
		if p == t.path {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			e.Denied = true
			return nil
		}
		if d.IsDir() {
			if deviceOf(info) != dev {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		size := onDisk(info)
		e.Size += size
		rel, _ := filepath.Rel(t.path, p)
		first, _, _ := strings.Cut(rel, string(filepath.Separator))
		children[first] += size
		return nil
	})
	e.Complete = !errors.Is(walkErr, errGrowthStop) && ctx.Err() == nil
	if e.Size >= growthChildEntry {
		for name, size := range children {
			if size >= growthChildFloor {
				if e.Children == nil {
					e.Children = map[string]int64{}
				}
				e.Children[name] = size
			}
		}
	}
	return e
}

// growthOrder sorts targets largest first by the last record. Entries it
// never saw go last, in their listed order. A run cut short then loses only
// small entries.
func growthOrder(targets []growthTarget, prev *growthSnapshot) []growthTarget {
	last := map[string]int64{}
	if prev != nil {
		for _, e := range prev.Entries {
			last[e.Path] = e.Size
		}
	}
	out := slices.Clone(targets)
	slices.SortStableFunc(out, func(a, b growthTarget) int { return cmp.Compare(last[b.path], last[a.path]) })
	return out
}

// takeGrowthSnapshot measures targets with growthWorkers background threads
// (spec §四):
//   - each entry gets at most growthEntryLimit;
//   - the run stops at growthRunLimit;
//   - entries measured until then are kept, and Cut tells that some never started.
//
// Entries come sorted by path.
func takeGrowthSnapshot(ctx context.Context, targets []growthTarget, prev *growthSnapshot, fda bool, now time.Time) growthSnapshot {
	order := growthOrder(targets, prev)
	run, cancel := context.WithTimeout(ctx, growthRunLimit)
	defer cancel()
	jobs := make(chan growthTarget)
	results := make(chan growthEntry)
	var wg sync.WaitGroup
	for range growthWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			backgroundThread() // never unlocked: Go retires the thread with its QoS
			for t := range jobs {
				ectx, ecancel := context.WithTimeout(run, growthEntryLimit)
				results <- measureGrowthEntry(ectx, t)
				ecancel()
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, t := range order {
			if run.Err() != nil {
				return
			}
			select {
			case jobs <- t:
			case <-run.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	s := growthSnapshot{At: now.Unix(), FDA: fda, Entries: []growthEntry{}}
	for e := range results {
		s.Entries = append(s.Entries, e)
	}
	s.Cut = len(s.Entries) < len(order)
	slices.SortFunc(s.Entries, func(a, b growthEntry) int { return strings.Compare(a.Path, b.Path) })
	return s
}
```

- [ ] **Step 5: Write the QoS files**

> Superseded: this QoS + iopolicy pair leaves the thread without its background QoS; the shipped `backgroundThread` makes one setpriority(2) call instead. See Implementation notes (2026-10-26) at the end.

`growth_qos_darwin.go`:

```go
package main

import (
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
)

const (
	qosClassBackground = 0x09 // QOS_CLASS_BACKGROUND, <sys/qos.h>
	iopolTypeDisk      = 0    // IOPOL_TYPE_DISK, <sys/resource.h>
	iopolScopeThread   = 1    // IOPOL_SCOPE_THREAD
	iopolThrottle      = 3    // IOPOL_THROTTLE
)

var (
	qosOnce     sync.Once
	setQoS      func(class uint32, relpri int32) int32
	setIOPolicy func(iotype, scope, policy int32) int32
)

// backgroundThread pins the calling goroutine to its OS thread. It lowers
// that thread to background QoS with throttled disk I/O, so measuring yields
// to everything the user does (spec §四).
//
// The goroutine must return without unlocking. Go then retires the thread
// and its policy instead of reusing it.
func backgroundThread() {
	runtime.LockOSThread()
	qosOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&setQoS, lib, "pthread_set_qos_class_self_np")
		purego.RegisterLibFunc(&setIOPolicy, lib, "setiopolicy_np")
	})
	if setQoS != nil {
		setQoS(qosClassBackground, 0)
	}
	if setIOPolicy != nil {
		setIOPolicy(iopolTypeDisk, iopolScopeThread, iopolThrottle)
	}
}
```

`growth_qos_windows.go`:

```go
package main

// backgroundThread does nothing on Windows, where the growth monitor does not run.
func backgroundThread() {}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race -run 'MeasureGrowth|GrowthOrder|TakeGrowthSnapshot|BackgroundThread' -count=1 . && GOOS=windows GOARCH=amd64 go vet ./...`
Expected: PASS.

- [ ] **Step 7: Leave the changes uncommitted for the reviewer**

---

### Task 3: Record files, config and the notification log

**Files:**
- Create: `growth_store.go`
- Test: `growth_store_test.go`

**Interfaces:**
- Consumes: `growthSnapshot` (Task 1) and `configPath`, `codeError` (main.go).
- Produces:
  - `func growthDir() string`
  - `const growthKeep = 90 * 24 * time.Hour`
  - `func saveGrowthSnapshot(dir string, s growthSnapshot, now time.Time) error`
  - `func loadGrowthSnapshots(dir string) []growthSnapshot`, oldest first
  - `func lastGrowthAt(dir string) time.Time`
  - `type GrowthConfig struct{ Enabled, OpenAtLogin bool; DailyGB, WeeklyGB float64; Ignored []string; Asked bool }`
  - `func defaultGrowthConfig() GrowthConfig`, `func loadGrowthConfig(dir string) GrowthConfig` and `func validateGrowthConfig(c GrowthConfig) (GrowthConfig, error)`
  - `func saveGrowthConfig(dir string, c GrowthConfig) error`
  - `type growthNotified struct{ Day map[string]string; Week map[string]int64; Count map[string]int; Recent []GrowthAlert }` (`GrowthAlert` is defined in Task 4)
  - `func loadGrowthNotified(dir string) growthNotified` and `func saveGrowthNotified(dir string, n growthNotified) error`

Task 3 and Task 4 go in the same run (A). Write `growth_eval.go`'s `GrowthAlert` type first (Task 4, Step 3, the type only) so that this file compiles.

- [ ] **Step 1: Write the failing tests**

`growth_store_test.go`:

```go
package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGrowthSnapshotsSaveLoadAndPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(200*86400, 0)
	old := growthSnapshot{At: now.Add(-91 * 24 * time.Hour).Unix(), Entries: []growthEntry{}}
	mid := growthSnapshot{At: now.Add(-24 * time.Hour).Unix(), FDA: true, Entries: []growthEntry{{Path: "/a", Kind: growthHome, Size: 7, Complete: true}}}
	cur := growthSnapshot{At: now.Unix(), FDA: true, Cut: true, Entries: []growthEntry{}}
	for _, s := range []growthSnapshot{old, mid, cur} {
		if err := saveGrowthSnapshot(dir, s, now); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(dir, "records", "garbage.json"), 3)
	got := loadGrowthSnapshots(dir)
	if len(got) != 2 || got[0].At != mid.At || got[1].At != cur.At || !got[1].Cut || got[0].Entries[0].Size != 7 {
		t.Fatalf("records = %+v", got)
	}
	if last := lastGrowthAt(dir); !last.Equal(now) {
		t.Fatalf("last = %v", last)
	}
	if last := lastGrowthAt(t.TempDir()); !last.IsZero() {
		t.Fatalf("no records: last = %v", last)
	}
}

func TestGrowthConfigDefaultsAndValidation(t *testing.T) {
	dir := t.TempDir()
	if c := loadGrowthConfig(dir); c.Enabled || c.DailyGB != 2 || c.WeeklyGB != 10 || c.Ignored == nil {
		t.Fatalf("defaults = %+v", c)
	}
	c, err := validateGrowthConfig(GrowthConfig{Enabled: false, OpenAtLogin: true, DailyGB: 5, WeeklyGB: 20, Ignored: []string{" APP:X ", "app:x", ""}})
	if err != nil || c.OpenAtLogin || len(c.Ignored) != 1 || c.Ignored[0] != "app:x" {
		t.Fatalf("validated = %+v, %v", c, err)
	}
	var ce codeError
	if _, err := validateGrowthConfig(GrowthConfig{DailyGB: 3, WeeklyGB: 10}); !errors.As(err, &ce) || ce.Code != "growth-threshold" {
		t.Fatalf("bad threshold err = %v", err)
	}
	if err := saveGrowthConfig(dir, c); err != nil {
		t.Fatal(err)
	}
	if got := loadGrowthConfig(dir); got.DailyGB != 5 || got.WeeklyGB != 20 {
		t.Fatalf("reloaded = %+v", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"dailyGB": 7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadGrowthConfig(dir); got.DailyGB != 2 {
		t.Fatalf("an unknown threshold must fall back to the default: %+v", got)
	}
}

func TestGrowthNotifiedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	n := loadGrowthNotified(dir)
	if n.Day == nil || n.Week == nil || n.Count == nil {
		t.Fatalf("maps must be ready: %+v", n)
	}
	n.Day["app:x"] = "2026-10-24"
	n.Recent = []GrowthAlert{{Key: "app:x", Bytes: 1}}
	if err := saveGrowthNotified(dir, n); err != nil {
		t.Fatal(err)
	}
	if got := loadGrowthNotified(dir); got.Day["app:x"] != "2026-10-24" || len(got.Recent) != 1 {
		t.Fatalf("reloaded = %+v", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -run 'GrowthSnapshotsSave|GrowthConfig|GrowthNotified' -count=1 .`
Expected: FAIL to compile, because `saveGrowthSnapshot` and the other new names are undefined.

- [ ] **Step 3: Write `growth_store.go`**

```go
package main

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// growthDir is where the growth monitor keeps its files, next to
// config.json in Cleanzo's folder of Application Support (spec §四).
func growthDir() string { return filepath.Join(filepath.Dir(configPath()), "growth") }

// growthKeep is how long records are kept (spec §四).
const growthKeep = 90 * 24 * time.Hour

// writeGrowthFile replaces path only after a complete copy is written.
func writeGrowthFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// saveGrowthSnapshot writes one record as records/<At>.json. It then
// removes the records older than growthKeep, the monitor's only deletion.
func saveGrowthSnapshot(dir string, s growthSnapshot, now time.Time) error {
	records := filepath.Join(dir, "records")
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := writeGrowthFile(filepath.Join(records, strconv.FormatInt(s.At, 10)+".json"), data); err != nil {
		return err
	}
	entries, _ := os.ReadDir(records)
	for _, e := range entries {
		at, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".json"), 10, 64)
		if err == nil && strings.HasSuffix(e.Name(), ".json") && now.Sub(time.Unix(at, 0)) > growthKeep {
			_ = os.Remove(filepath.Join(records, e.Name()))
		}
	}
	return nil
}

// loadGrowthSnapshots reads every record, oldest first, skipping files that
// do not parse.
func loadGrowthSnapshots(dir string) []growthSnapshot {
	records := filepath.Join(dir, "records")
	entries, _ := os.ReadDir(records)
	var out []growthSnapshot
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(records, e.Name()))
		if err != nil {
			continue
		}
		var s growthSnapshot
		if json.Unmarshal(data, &s) == nil && s.At > 0 {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b growthSnapshot) int { return cmp.Compare(a.At, b.At) })
	return out
}

// lastGrowthAt is when the newest record was taken, read from file names
// only; zero without records.
func lastGrowthAt(dir string) time.Time {
	entries, _ := os.ReadDir(filepath.Join(dir, "records"))
	var last int64
	for _, e := range entries {
		if at, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".json"), 10, 64); err == nil && strings.HasSuffix(e.Name(), ".json") {
			last = max(last, at)
		}
	}
	if last == 0 {
		return time.Time{}
	}
	return time.Unix(last, 0)
}

// GrowthConfig is what the user chose for the growth monitor (spec §五, §六).
type GrowthConfig struct {
	Enabled     bool     `json:"enabled"`     // measure in the background, show the menu bar icon, stay open without windows
	OpenAtLogin bool     `json:"openAtLogin"` // only together with Enabled
	DailyGB     float64  `json:"dailyGB"`     // a day's growth that alerts
	WeeklyGB    float64  `json:"weeklyGB"`    // seven days' growth that alerts
	Ignored     []string `json:"ignored"`     // owner keys that never alert
	Asked       bool     `json:"asked"`       // the page has offered monitoring once
}

// The thresholds Settings offers, in GB.
var (
	growthDailyChoices  = []float64{1, 2, 5, 10}
	growthWeeklyChoices = []float64{5, 10, 20, 50}
)

// defaultGrowthConfig is the config before the user changes anything:
// monitoring off, 2 GB a day, 10 GB in seven days.
func defaultGrowthConfig() GrowthConfig {
	return GrowthConfig{DailyGB: 2, WeeklyGB: 10, Ignored: []string{}}
}

// loadGrowthConfig reads config.json in dir. A missing or broken file gives
// the defaults, and so does a threshold outside the choices.
func loadGrowthConfig(dir string) GrowthConfig {
	c := defaultGrowthConfig()
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || json.Unmarshal(data, &c) != nil {
		return defaultGrowthConfig()
	}
	def := defaultGrowthConfig()
	if !slices.Contains(growthDailyChoices, c.DailyGB) {
		c.DailyGB = def.DailyGB
	}
	if !slices.Contains(growthWeeklyChoices, c.WeeklyGB) {
		c.WeeklyGB = def.WeeklyGB
	}
	if c.Ignored == nil {
		c.Ignored = []string{}
	}
	c.OpenAtLogin = c.Enabled && c.OpenAtLogin
	return c
}

// validateGrowthConfig returns a normalized copy, or a translatable error
// for thresholds outside the choices. Opening at login needs monitoring on.
func validateGrowthConfig(c GrowthConfig) (GrowthConfig, error) {
	if !slices.Contains(growthDailyChoices, c.DailyGB) || !slices.Contains(growthWeeklyChoices, c.WeeklyGB) {
		return GrowthConfig{}, codeError{Code: "growth-threshold"}
	}
	out := c
	out.OpenAtLogin = c.Enabled && c.OpenAtLogin
	out.Ignored = []string{}
	for _, k := range c.Ignored {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" && !slices.Contains(out.Ignored, k) {
			out.Ignored = append(out.Ignored, k)
		}
	}
	return out, nil
}

// saveGrowthConfig writes config.json in dir.
func saveGrowthConfig(dir string, c GrowthConfig) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeGrowthFile(filepath.Join(dir, "config.json"), data)
}

// growthNotified remembers what was announced, to keep within the limits
// of spec §五. It also keeps the alerts of the last seven days for the page
// and the status page.
type growthNotified struct {
	Day    map[string]string `json:"day"`    // owner key → local date of its last notification
	Week   map[string]int64  `json:"week"`   // owner key → unix time of its last seven-day notification
	Count  map[string]int    `json:"count"`  // local date → notifications posted that day
	Recent []GrowthAlert     `json:"recent"` // alerts raised in the last seven days, one per owner, newest first
}

// loadGrowthNotified reads notified.json in dir, with every map ready.
func loadGrowthNotified(dir string) growthNotified {
	var n growthNotified
	if data, err := os.ReadFile(filepath.Join(dir, "notified.json")); err == nil {
		_ = json.Unmarshal(data, &n)
	}
	if n.Day == nil {
		n.Day = map[string]string{}
	}
	if n.Week == nil {
		n.Week = map[string]int64{}
	}
	if n.Count == nil {
		n.Count = map[string]int{}
	}
	if n.Recent == nil {
		n.Recent = []GrowthAlert{}
	}
	return n
}

// saveGrowthNotified writes notified.json in dir.
func saveGrowthNotified(dir string, n growthNotified) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return writeGrowthFile(filepath.Join(dir, "notified.json"), data)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -run 'GrowthSnapshotsSave|GrowthConfig|GrowthNotified' -count=1 .`
Expected: PASS.

- [ ] **Step 5: Leave the changes uncommitted for the reviewer**

---

### Task 4: Comparing records

**Files:**
- Create: `growth_eval.go`
- Test: `growth_eval_test.go`

**Interfaces:**
- Consumes: `growthSnapshot`, `growthEntry` and `growthOwner` (Task 1); `GrowthConfig` and `growthNotified` (Task 3); `within` (paths.go).
- Produces:
  - `type GrowthDelta struct{ Bytes int64; AtLeast bool; Days int }`
  - `type GrowthAlert struct{ Key, Name, Window string; Days int; Bytes int64; AtLeast bool; Folder string; Total, At int64 }`
  - `type GrowthApp` and `type GrowthFolder`. Their fields are given below; they are declared here and used by Task 5.
  - `func growthBase(history []growthSnapshot, cur growthSnapshot, window string) *growthSnapshot`
  - `func growthChanges(base, cur growthSnapshot, owner func(growthEntry) growthOwner) map[string]growthChange`
  - `func evaluateGrowth(history []growthSnapshot, cur growthSnapshot, cfg GrowthConfig, owner func(growthEntry) growthOwner) []GrowthAlert`
  - `func buildGrowthApps(history []growthSnapshot, cfg GrowthConfig, owner func(growthEntry) growthOwner, home string, loc *time.Location) []GrowthApp`
  - `func pickGrowthNotifications(alerts []GrowthAlert, n *growthNotified, now time.Time) []GrowthAlert`
  - `func (n *growthNotified) remember(alerts []GrowthAlert, now time.Time)`
  - `func (n growthNotified) recent(now time.Time) []GrowthAlert`

- [ ] **Step 1: Write the failing tests**

`growth_eval_test.go`:

```go
package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const gb = int64(1 << 30)

// t0 is a fixed morning; records are placed relative to it.
var t0 = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)

// byBase owns an entry by its folder name: Caches/ToDesk and Application
// Support/ToDesk both count for "app:todesk".
func byBase(e growthEntry) growthOwner {
	b := filepath.Base(e.Path)
	return growthOwner{key: "app:" + strings.ToLower(b), name: b, kind: "app"}
}

func at(d time.Duration, fda bool, entries ...growthEntry) growthSnapshot {
	return growthSnapshot{At: t0.Add(d).Unix(), FDA: fda, Entries: entries}
}

func done(path string, size int64) growthEntry {
	return growthEntry{Path: path, Kind: growthHome, Size: size, Complete: true}
}

var (
	todesk = filepath.Join("/h", "Library", "Application Support", "ToDesk")
	todeskCache = filepath.Join("/h", "Library", "Caches", "ToDesk")
	codex  = filepath.Join("/h", "Library", "Application Support", "Codex")
)

func cfg2() GrowthConfig { return defaultGrowthConfig() }

func TestFirstRecordIsOnlyABaseline(t *testing.T) {
	if a := evaluateGrowth(nil, at(0, true, done(todesk, 100*gb)), cfg2(), byBase); len(a) != 0 {
		t.Fatalf("alerts = %+v", a)
	}
}

func TestDayAlertAgainstARecord20To28HoursOlder(t *testing.T) {
	h := []growthSnapshot{at(-24*time.Hour, true, done(todesk, 10*gb), done(todeskCache, gb))}
	cur := at(0, true, done(todesk, 12*gb), done(todeskCache, 2*gb))
	a := evaluateGrowth(h, cur, cfg2(), byBase)
	if len(a) != 1 || a[0].Window != "day" || a[0].Bytes != 3*gb || a[0].AtLeast || a[0].Days != 1 || a[0].Total != 14*gb || a[0].Key != "app:todesk" || a[0].At != cur.At {
		t.Fatalf("alerts = %+v", a)
	}
}

func TestDayNeedsARecordInItsWindow(t *testing.T) {
	h := []growthSnapshot{at(-50*time.Hour, true, done(todesk, 10*gb))}
	if a := evaluateGrowth(h, at(0, true, done(todesk, 20*gb)), cfg2(), byBase); len(a) != 0 {
		t.Fatalf("a two-day gap is neither a day nor a week: %+v", a)
	}
}

func TestWeekAlertUsesTheEarliestRecordAtLeastThreeDaysOld(t *testing.T) {
	h := []growthSnapshot{
		at(-8*24*time.Hour, true, done(todesk, 0)),
		at(-5*24*time.Hour, true, done(todesk, 10*gb)),
		at(-4*24*time.Hour, true, done(todesk, 15*gb)),
	}
	a := evaluateGrowth(h, at(0, true, done(todesk, 21*gb)), cfg2(), byBase)
	if len(a) != 1 || a[0].Window != "week" || a[0].Bytes != 11*gb || a[0].Days != 5 {
		t.Fatalf("alerts = %+v", a)
	}
}

func TestLowerBoundsAndUncomparableEntries(t *testing.T) {
	h := []growthSnapshot{at(-24*time.Hour, true, done(todesk, 10*gb), growthEntry{Path: codex, Kind: growthHome, Size: 9 * gb})}
	partial := growthEntry{Path: todesk, Kind: growthHome, Size: 12*gb + gb/2}
	a := evaluateGrowth(h, at(0, true, partial, done(codex, 30*gb)), cfg2(), byBase)
	if len(a) != 1 || a[0].Key != "app:todesk" || !a[0].AtLeast || a[0].Bytes != 2*gb+gb/2 {
		t.Fatalf("only ToDesk, as a lower bound; Codex's base was unfinished: %+v", a)
	}
}

func TestFullDiskAccessChangeAndDeniedFlipNeverCompare(t *testing.T) {
	h := []growthSnapshot{at(-24*time.Hour, false, done(todesk, gb))}
	if a := evaluateGrowth(h, at(0, true, done(todesk, 20*gb)), cfg2(), byBase); len(a) != 0 {
		t.Fatalf("FDA changed: %+v", a)
	}
	h = []growthSnapshot{at(-24*time.Hour, true, done(todesk, gb))}
	denied := done(todesk, 20*gb)
	denied.Denied = true
	if a := evaluateGrowth(h, at(0, true, denied), cfg2(), byBase); len(a) != 0 {
		t.Fatalf("readability changed: %+v", a)
	}
}

func TestNewEntriesCountFromZeroOnlyAgainstAWholeRun(t *testing.T) {
	whole := []growthSnapshot{at(-24*time.Hour, true)}
	if a := evaluateGrowth(whole, at(0, true, done(todesk, 3*gb)), cfg2(), byBase); len(a) != 1 || a[0].Bytes != 3*gb {
		t.Fatalf("new since a whole run: %+v", a)
	}
	cut := at(-24*time.Hour, true)
	cut.Cut = true
	if a := evaluateGrowth([]growthSnapshot{cut}, at(0, true, done(todesk, 3*gb)), cfg2(), byBase); len(a) != 0 {
		t.Fatalf("new since a cut run: %+v", a)
	}
}

func TestIgnoredAndShrinkingNeverAlert(t *testing.T) {
	h := []growthSnapshot{at(-24*time.Hour, true, done(todesk, 10*gb), done(codex, 50*gb))}
	c := cfg2()
	c.Ignored = []string{"app:todesk"}
	if a := evaluateGrowth(h, at(0, true, done(todesk, 20*gb), done(codex, 10*gb)), c, byBase); len(a) != 0 {
		t.Fatalf("alerts = %+v", a)
	}
}

func TestAlertNamesTheChildThatGrewMost(t *testing.T) {
	base := done(todesk, 10*gb)
	base.Children = map[string]int64{"dumps": 9 * gb, "logs": gb}
	cur := done(todesk, 16*gb)
	cur.Children = map[string]int64{"dumps": 15 * gb, "logs": gb}
	a := evaluateGrowth([]growthSnapshot{at(-24*time.Hour, true, base)}, at(0, true, cur), cfg2(), byBase)
	if len(a) != 1 || a[0].Folder != filepath.Join(todesk, "dumps") {
		t.Fatalf("alerts = %+v", a)
	}
}

func TestPickGrowthNotifications(t *testing.T) {
	now := t0
	n := loadGrowthNotified(t.TempDir())
	alerts := []GrowthAlert{
		{Key: "app:a", Window: "week", Bytes: 50 * gb},
		{Key: "app:a", Window: "day", Bytes: 5 * gb},
		{Key: "app:b", Window: "day", Bytes: 9 * gb},
		{Key: "app:c", Window: "day", Bytes: 3 * gb},
		{Key: "app:d", Window: "day", Bytes: 2 * gb},
	}
	got := pickGrowthNotifications(alerts, &n, now)
	if len(got) != 3 || got[0].Key != "app:b" || got[1].Key != "app:a" || got[1].Window != "day" || got[2].Key != "app:c" {
		t.Fatalf("first picks = %+v", got)
	}
	if again := pickGrowthNotifications(alerts, &n, now.Add(time.Hour)); len(again) != 0 {
		t.Fatalf("three a day in all, one a day per app: %+v", again)
	}
	tomorrow := now.Add(24 * time.Hour)
	week := []GrowthAlert{{Key: "app:e", Window: "week", Bytes: 20 * gb}}
	if got := pickGrowthNotifications(week, &n, tomorrow); len(got) != 1 {
		t.Fatalf("a new day has room: %+v", got)
	}
	if got := pickGrowthNotifications(week, &n, tomorrow.Add(48*time.Hour)); len(got) != 0 {
		t.Fatalf("a seven-day alert waits a week: %+v", got)
	}
}

func TestBuildGrowthApps(t *testing.T) {
	sys := filepath.Join("/", "Library", "Application Support", "ToDesk")
	h := []growthSnapshot{
		at(-10*24*time.Hour, true, done(todesk, gb), done(sys, gb)),
		at(-5*24*time.Hour, true, done(todesk, 2*gb), done(sys, 3*gb)),
		at(-24*time.Hour, true, done(todesk, 4*gb), done(sys, 5*gb)),
		at(0, true, done(todesk, 5*gb), done(sys, 9*gb), done(codex, gb)),
	}
	c := cfg2()
	c.Ignored = []string{"app:codex"}
	apps := buildGrowthApps(h, c, byBase, "/h", time.UTC)
	if len(apps) != 2 || apps[0].Key != "app:todesk" || apps[0].Size != 14*gb || apps[1].Key != "app:codex" || !apps[1].Ignored {
		t.Fatalf("apps = %+v", apps)
	}
	td := apps[0]
	if td.Day == nil || td.Day.Bytes != 5*gb || td.Week == nil || td.Week.Bytes != 9*gb || td.Week.Days != 5 || td.Month == nil || td.Month.Bytes != 12*gb || td.Month.Days != 10 {
		t.Fatalf("deltas day=%+v week=%+v month=%+v", td.Day, td.Week, td.Month)
	}
	if len(td.Spark) != 30 || td.Spark[29] != 14*gb || td.Spark[28] != 9*gb || td.Spark[27] != -1 || td.Spark[24] != 5*gb {
		t.Fatalf("spark = %v", td.Spark)
	}
	var outside bool
	for _, f := range td.Folders {
		if f.Path == sys {
			outside = f.Outside
		}
	}
	if !outside {
		t.Fatalf("a /Library folder must be marked outside home: %+v", td.Folders)
	}
	// Codex appears only now; the week base (5 days ago) measured everything, so Codex counts from 0.
	if apps[1].Week == nil || apps[1].Week.Bytes != gb {
		t.Fatalf("Codex is new since the week base: %+v", apps[1].Week)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -run 'Baseline|DayAlert|DayNeeds|WeekAlert|LowerBounds|FullDiskAccessChange|NewEntries|IgnoredAnd|AlertNames|PickGrowth|BuildGrowthApps' -count=1 .`
Expected: FAIL to compile, because `evaluateGrowth` and the other new names are undefined.

- [ ] **Step 3: Write `growth_eval.go`**

```go
package main

import (
	"cmp"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// GrowthDelta is growth over a window, a lower bound when AtLeast.
type GrowthDelta struct {
	Bytes   int64 `json:"bytes"`
	AtLeast bool  `json:"atLeast"`
	Days    int   `json:"days"` // the window's actual length, rounded
}

// GrowthAlert is an owner whose growth passed a threshold (spec §五).
type GrowthAlert struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Window  string `json:"window"` // "day" or "week"
	Days    int    `json:"days"`
	Bytes   int64  `json:"bytes"`
	AtLeast bool   `json:"atLeast"`
	Folder  string `json:"folder"` // where it grew most, down to a recorded child
	Total   int64  `json:"total"`  // what the owner takes now
	At      int64  `json:"at"`     // unix seconds of the record that raised it
}

// GrowthApp is one row of the growth page: an owner, what it takes now and
// how it grew.
type GrowthApp struct {
	Key     string         `json:"key"`
	Name    string         `json:"name"`
	Kind    string         `json:"kind"`    // "app", "macos", "dot" or "unknown"
	Orphan  bool           `json:"orphan"`  // probably a leftover: the leftovers tab can clean it
	AppPath string         `json:"appPath"` // the installed app, for Uninstall
	Size    int64          `json:"size"`
	Partial bool           `json:"partial"` // some folder was not measured to the end
	Ignored bool           `json:"ignored"`
	Day     *GrowthDelta   `json:"day"` // nil: no record to compare with
	Week    *GrowthDelta   `json:"week"`
	Month   *GrowthDelta   `json:"month"`
	Spark   []int64        `json:"spark"` // size on each of the last 30 days, oldest first; -1 without a record
	Folders []GrowthFolder `json:"folders"`
}

// GrowthFolder is one measured entry of a row.
type GrowthFolder struct {
	Path     string       `json:"path"`
	Size     int64        `json:"size"`
	Complete bool         `json:"complete"`
	Denied   bool         `json:"denied"`
	Outside  bool         `json:"outside"` // outside the home folder: Cleanzo never removes it
	Week     *GrowthDelta `json:"week"`
}

// growthBase finds the record cur is compared with (spec §五), or nil:
//   - "day": the latest record 20 to 28 hours older;
//   - "week": the earliest record at most 7 days older and at least 3 days;
//   - "month": the earliest record at most 30 days older and at least 8 days.
//
// Records taken with a different Full Disk Access never count.
func growthBase(history []growthSnapshot, cur growthSnapshot, window string) *growthSnapshot {
	var lo, hi time.Duration
	latest := false
	switch window {
	case "day":
		lo, hi, latest = 20*time.Hour, 28*time.Hour, true
	case "week":
		lo, hi = 3*24*time.Hour, 7*24*time.Hour
	case "month":
		lo, hi = 8*24*time.Hour, 30*24*time.Hour
	default:
		return nil
	}
	var found *growthSnapshot
	for i := range history {
		h := &history[i]
		age := time.Duration(cur.At-h.At) * time.Second
		if h.At >= cur.At || age < lo || age > hi || h.FDA != cur.FDA {
			continue
		}
		if found == nil || latest && h.At > found.At || !latest && h.At < found.At {
			found = h
		}
	}
	return found
}

// growthDays is a window's length in whole days.
func growthDays(base, cur growthSnapshot) int {
	return int(math.Round(float64(cur.At-base.At) / 86400))
}

// growthChange is how much one owner changed between two records.
type growthChange struct {
	bytes    int64
	atLeast  bool
	compared bool   // at least one of its entries compared
	folder   string // where it grew most
	grew     int64  // and by how much
}

// growthEntryChange compares one entry, reporting false when it cannot be
// compared:
//   - exact when both records measured it to the end;
//   - a lower bound when only the base did;
//   - nothing when the base did not finish it, or its readability changed.
//
// An entry missing on one side counts as 0 there, but only when that side's
// run measured everything (not Cut).
func growthEntryChange(a growthEntry, inBase bool, base growthSnapshot, b growthEntry) (delta int64, atLeast, ok bool) {
	if !inBase {
		if base.Cut {
			return 0, false, false
		}
		return b.Size, !b.Complete, true
	}
	if !a.Complete || a.Denied != b.Denied {
		return 0, false, false
	}
	return b.Size - a.Size, !b.Complete, true
}

// growthTopFolder names where an entry grew most: the child that grew most
// when the entry records children (against the base's, or 0), else the
// entry itself.
func growthTopFolder(a, b growthEntry) string {
	best, bestGrew := "", int64(math.MinInt64)
	for name, size := range b.Children {
		if g := size - a.Children[name]; g > bestGrew || g == bestGrew && name < best {
			best, bestGrew = name, g
		}
	}
	if best == "" {
		return b.Path
	}
	return filepath.Join(b.Path, best)
}

// growthChanges sums each owner's entry changes between base and cur.
func growthChanges(base, cur growthSnapshot, owner func(growthEntry) growthOwner) map[string]growthChange {
	before := make(map[string]growthEntry, len(base.Entries))
	for _, e := range base.Entries {
		before[e.Path] = e
	}
	out := map[string]growthChange{}
	add := func(key string, delta int64, atLeast bool, folder string) {
		c := out[key]
		c.bytes += delta
		c.atLeast = c.atLeast || atLeast
		if !c.compared || delta > c.grew {
			c.folder, c.grew = folder, delta
		}
		c.compared = true
		out[key] = c
	}
	seen := make(map[string]bool, len(cur.Entries))
	for _, b := range cur.Entries {
		seen[b.Path] = true
		a, inBase := before[b.Path]
		if delta, atLeast, ok := growthEntryChange(a, inBase, base, b); ok {
			add(owner(b).key, delta, atLeast, growthTopFolder(a, b))
		}
	}
	if !cur.Cut {
		for _, a := range base.Entries {
			if !seen[a.Path] && a.Complete {
				add(owner(a).key, -a.Size, false, a.Path) // gone since the base
			}
		}
	}
	return out
}

// growthTotal is an owner's size in one record.
type growthTotal struct {
	owner growthOwner
	size  int64
}

// growthTotals sums each owner's entries in s.
func growthTotals(s growthSnapshot, owner func(growthEntry) growthOwner) map[string]growthTotal {
	out := map[string]growthTotal{}
	for _, e := range s.Entries {
		o := owner(e)
		t := out[o.key]
		t.owner, t.size = o, t.size+e.Size
		out[o.key] = t
	}
	return out
}

// memoGrowthOwner remembers owner per path, since comparing records asks
// for the same paths many times.
func memoGrowthOwner(owner func(growthEntry) growthOwner) func(growthEntry) growthOwner {
	memo := map[string]growthOwner{}
	return func(e growthEntry) growthOwner {
		k := string(e.Kind) + "\x00" + e.Path
		if o, ok := memo[k]; ok {
			return o
		}
		o := owner(e)
		memo[k] = o
		return o
	}
}

// evaluateGrowth lists the owners whose growth in cur passes the thresholds
// (spec §五):
//   - a day, against the latest record 20 to 28 hours older;
//   - seven days, against the earliest record within a week that is at least
//     three days old.
//
// The first record is a baseline only. Ignored owners and shrinking never
// alert. The alerts come largest first.
func evaluateGrowth(history []growthSnapshot, cur growthSnapshot, cfg GrowthConfig, owner func(growthEntry) growthOwner) []GrowthAlert {
	owner = memoGrowthOwner(owner)
	totals := growthTotals(cur, owner)
	out := []GrowthAlert{}
	check := func(window string, thresholdGB float64) {
		base := growthBase(history, cur, window)
		if base == nil {
			return
		}
		for key, c := range growthChanges(*base, cur, owner) {
			if slices.Contains(cfg.Ignored, key) || float64(c.bytes) < thresholdGB*float64(1<<30) {
				continue
			}
			out = append(out, GrowthAlert{
				Key: key, Name: totals[key].owner.name, Window: window, Days: growthDays(*base, cur),
				Bytes: c.bytes, AtLeast: c.atLeast, Folder: c.folder, Total: totals[key].size, At: cur.At,
			})
		}
	}
	check("day", cfg.DailyGB)
	check("week", cfg.WeeklyGB)
	slices.SortFunc(out, func(a, b GrowthAlert) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), strings.Compare(a.Key, b.Key), strings.Compare(a.Window, b.Window))
	})
	return out
}

// buildGrowthApps turns the records into the page's rows, largest first
// (spec §六). The last record is "now"; the growth columns compare it with
// the records growthBase picks. Spark has the owner's size in the last
// record of each of the last 30 days (in loc), or -1 for a day without one.
func buildGrowthApps(history []growthSnapshot, cfg GrowthConfig, owner func(growthEntry) growthOwner, home string, loc *time.Location) []GrowthApp {
	out := []GrowthApp{}
	if len(history) == 0 {
		return out
	}
	owner = memoGrowthOwner(owner)
	cur := history[len(history)-1]
	earlier := history[:len(history)-1]
	rows := map[string]*GrowthApp{}
	var order []string
	for _, e := range cur.Entries {
		o := owner(e)
		r := rows[o.key]
		if r == nil {
			r = &GrowthApp{Key: o.key, Name: o.name, Kind: o.kind, Orphan: o.orphan, AppPath: o.appPath,
				Ignored: slices.Contains(cfg.Ignored, o.key), Spark: []int64{}, Folders: []GrowthFolder{}}
			rows[o.key] = r
			order = append(order, o.key)
		}
		r.Size += e.Size
		r.Partial = r.Partial || !e.Complete
		r.Folders = append(r.Folders, GrowthFolder{Path: e.Path, Size: e.Size, Complete: e.Complete, Denied: e.Denied, Outside: !within(e.Path, home)})
	}
	for _, w := range []string{"day", "week", "month"} {
		base := growthBase(earlier, cur, w)
		if base == nil {
			continue
		}
		days := growthDays(*base, cur)
		for key, c := range growthChanges(*base, cur, owner) {
			r := rows[key]
			if r == nil || !c.compared {
				continue
			}
			d := &GrowthDelta{Bytes: c.bytes, AtLeast: c.atLeast, Days: days}
			switch w {
			case "day":
				r.Day = d
			case "week":
				r.Week = d
			case "month":
				r.Month = d
			}
		}
		if w == "week" {
			before := map[string]growthEntry{}
			for _, e := range base.Entries {
				before[e.Path] = e
			}
			for _, r := range rows {
				for i, f := range r.Folders {
					a, inBase := before[f.Path]
					b := growthEntry{Path: f.Path, Size: f.Size, Complete: f.Complete, Denied: f.Denied}
					if delta, atLeast, ok := growthEntryChange(a, inBase, *base, b); ok {
						r.Folders[i].Week = &GrowthDelta{Bytes: delta, AtLeast: atLeast, Days: days}
					}
				}
			}
		}
	}
	// spark: the last record of each local day
	byDay := map[string]map[string]int64{}
	for _, s := range history {
		if cur.At-s.At > 31*86400 {
			continue
		}
		sizes := map[string]int64{}
		for _, e := range s.Entries {
			sizes[owner(e).key] += e.Size
		}
		byDay[time.Unix(s.At, 0).In(loc).Format(time.DateOnly)] = sizes // history is oldest first: later records win
	}
	today := time.Unix(cur.At, 0).In(loc)
	for _, r := range rows {
		r.Spark = make([]int64, 30)
		for i := range r.Spark {
			r.Spark[i] = -1
			if sizes, ok := byDay[today.AddDate(0, 0, i-29).Format(time.DateOnly)]; ok {
				if v, ok := sizes[r.Key]; ok {
					r.Spark[i] = v
				}
			}
		}
		slices.SortFunc(r.Folders, func(a, b GrowthFolder) int { return cmp.Or(cmp.Compare(b.Size, a.Size), strings.Compare(a.Path, b.Path)) })
	}
	for _, key := range order {
		out = append(out, *rows[key])
	}
	slices.SortFunc(out, func(a, b GrowthApp) int { return cmp.Or(cmp.Compare(b.Size, a.Size), strings.Compare(a.Name, b.Name)) })
	return out
}

// pickGrowthNotifications chooses the alerts to post now (spec §五):
//   - one a day per owner, where the day's alert beats the seven-day one;
//   - a seven-day alert at most once a week per owner;
//   - three a day in all, largest first.
//
// It records the choice in n.
func pickGrowthNotifications(alerts []GrowthAlert, n *growthNotified, now time.Time) []GrowthAlert {
	today := now.Format(time.DateOnly)
	byKey := map[string]GrowthAlert{}
	for _, a := range alerts {
		if a.Window == "week" && now.Unix()-n.Week[a.Key] < 7*86400 {
			continue // its seven-day alert went out this week
		}
		if prev, ok := byKey[a.Key]; ok && prev.Window == "day" {
			continue // the day's alert says more
		}
		byKey[a.Key] = a
	}
	var picks []GrowthAlert
	for key, a := range byKey {
		if n.Day[key] != today {
			picks = append(picks, a)
		}
	}
	slices.SortFunc(picks, func(a, b GrowthAlert) int { return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), strings.Compare(a.Key, b.Key)) })
	picks = picks[:min(len(picks), max(0, 3-n.Count[today]))]
	for _, a := range picks {
		n.Day[a.Key] = today
		if a.Window == "week" {
			n.Week[a.Key] = now.Unix()
		}
		n.Count[today]++
	}
	for d := range n.Count {
		if t, err := time.Parse(time.DateOnly, d); err != nil || now.Sub(t) > 8*24*time.Hour {
			delete(n.Count, d)
		}
	}
	for k, t := range n.Week {
		if now.Unix()-t > 30*86400 {
			delete(n.Week, k)
		}
	}
	return picks
}

// remember keeps the newest alert per owner for seven days, for the page
// and the status page.
func (n *growthNotified) remember(alerts []GrowthAlert, now time.Time) {
	keep := []GrowthAlert{}
	have := map[string]bool{}
	for _, a := range alerts { // newest first: the record that just ran
		if !have[a.Key] {
			keep, have[a.Key] = append(keep, a), true
		}
	}
	for _, a := range n.Recent {
		if !have[a.Key] && now.Unix()-a.At <= 7*86400 {
			keep, have[a.Key] = append(keep, a), true
		}
	}
	n.Recent = keep
}

// recent returns the remembered alerts that are still within seven days.
func (n growthNotified) recent(now time.Time) []GrowthAlert {
	out := []GrowthAlert{}
	for _, a := range n.Recent {
		if now.Unix()-a.At <= 7*86400 {
			out = append(out, a)
		}
	}
	return out
}
```

`within(path, dir)` from paths.go treats a path equal to `dir` as inside. Check its signature before relying on it; if it differs, wrap it here as `growthInHome(path, home string) bool`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race -run 'Baseline|DayAlert|DayNeeds|WeekAlert|LowerBounds|FullDiskAccessChange|NewEntries|IgnoredAnd|AlertNames|PickGrowth|BuildGrowthApps|GrowthNotified' -count=1 .`
Expected: PASS.

- [ ] **Step 5: Leave the changes uncommitted for the reviewer**

---

### Task 5: Schedule, bound methods, events and texts

**Files:**
- Create: `growth_monitor.go`, `growth_api.go`
- Modify:
  - `junk.go`: add `defer userScan()()` as the first statement of `ScanJunk` and `CleanJunk`.
  - `main.go`: the same in `FullScan` and `LargeScan`.
  - `src/native-i18n.json`
- Run: `bun run generate`, which regenerates `src/mygo.ts`.
- Test: `growth_monitor_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:
  - `type GrowthStatus struct{ Running bool; LastAt, NextAt int64; FDA bool; Notifications string }`
  - `type GrowthState struct{ Supported bool; Config GrowthConfig; Status GrowthStatus; Records int; Apps []GrowthApp; Alerts []GrowthAlert }`
  - `var GrowthUpdated = mygo.NewEvent[GrowthStatus]("growth:updated")`, which becomes `events.growthUpdated` on the page.
  - `var GrowthFocus = mygo.NewEvent[string]("growth:focus")`, which becomes `events.growthFocus`.
  - `func (Cleaner) GrowthState() GrowthState`, `func (Cleaner) GrowthMeasureNow() error` and `func (Cleaner) SaveGrowthConfig(c GrowthConfig) error`. These become `Cleaner.growthState()`, `Cleaner.growthMeasureNow()` and `Cleaner.saveGrowthConfig(c)`.
  - `var growth *growthMonitor`, with these methods:
    - `start()`
    - `tick(justEnabled bool)`
    - `run(manual bool) bool`
    - `measureOnce(ctx)`
    - `cancelBackground()`
    - `status() GrowthStatus`
    - `changed()`
  - `func growthNextRun(last, launched, now time.Time, justEnabled bool) time.Time` and `func growthDeferral(onBattery bool, percent int, scans int32) time.Duration`
  - `func userScan() func()`
  - `func nativeTextFor(locale, key string, vars map[string]string) string`, `func nativeText(key string, vars map[string]string) string` and `func growthSize(n int64) string`
  - The seams `growthNow`, `growthMeasure`, `growthNotify`, `growthChanged`, `growthOwnerOf` and `growthPower`.
  - `applyGrowthLifecycle`, which `SaveGrowthConfig` calls, is defined in Task 6. Until then, a one-line stub in `growth_api.go` returns nil; Task 6 replaces it.

- [ ] **Step 1: Add the native strings**

Add these keys to both languages in `src/native-i18n.json`, keeping the existing keys:

| key | zh | en |
|---|---|---|
| `growth.notify.day` | `{name} 一天涨了 {size}` | `{name} grew {size} in a day` |
| `growth.notify.dayAtLeast` | `{name} 一天至少涨了 {size}` | `{name} grew at least {size} in a day` |
| `growth.notify.week` | `{name} {days} 天涨了 {size}` | `{name} grew {size} in {days} days` |
| `growth.notify.weekAtLeast` | `{name} {days} 天至少涨了 {size}` | `{name} grew at least {size} in {days} days` |
| `growth.notify.body` | `主要在 {folder}，现在一共 {total}` | `Mostly in {folder}; {total} in all now` |
| `growth.tray.tip` | `Cleanzo · 占用增长` | `Cleanzo · Storage Growth` |
| `growth.tray.item` | `{name}  +{size}` | `{name}  +{size}` |
| `growth.tray.itemAtLeast` | `{name}  ≥ {size}` | `{name}  ≥ {size}` |
| `growth.tray.none` | `近 7 天没有明显增长` | `No notable growth in 7 days` |
| `growth.tray.open` | `打开 Cleanzo` | `Open Cleanzo` |
| `growth.tray.measure` | `现在量一次` | `Measure Now` |
| `growth.tray.measuring` | `正在量…` | `Measuring…` |
| `growth.tray.off` | `关闭后台监控` | `Turn Off Background Monitoring` |
| `growth.tray.quit` | `退出 Cleanzo` | `Quit Cleanzo` |

- [ ] **Step 2: Write the failing tests**

`growth_monitor_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGrowthNextRun(t *testing.T) {
	launched := t0
	cases := []struct {
		name        string
		last, now   time.Time
		justEnabled bool
		want        time.Time
	}{
		{"enabling without records measures now", time.Time{}, t0, true, t0},
		{"enabling after a recent record waits for its day", t0.Add(-2 * time.Hour), t0, true, t0.Add(22 * time.Hour)},
		{"launch after 20 hours waits five minutes", t0.Add(-21 * time.Hour), t0, false, t0.Add(5 * time.Minute)},
		{"later than five minutes after launch goes now", t0.Add(-30 * time.Hour), t0.Add(time.Hour), false, t0.Add(time.Hour)},
		{"otherwise 24 hours after the last record", t0.Add(-3 * time.Hour), t0, false, t0.Add(21 * time.Hour)},
	}
	for _, c := range cases {
		if got := growthNextRun(c.last, launched, c.now, c.justEnabled); !got.Equal(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGrowthDeferral(t *testing.T) {
	if d := growthDeferral(true, 20, 0); d != 30*time.Minute {
		t.Errorf("low battery: %v", d)
	}
	if d := growthDeferral(true, 21, 0); d != 0 {
		t.Errorf("battery above 20%%: %v", d)
	}
	if d := growthDeferral(false, 5, 1); d != 10*time.Minute {
		t.Errorf("user scan: %v", d)
	}
	if d := growthDeferral(false, 100, 0); d != 0 {
		t.Errorf("plugged in, idle: %v", d)
	}
}

// stubGrowth replaces the monitor's seams for one test: a clock under the
// test's control, ToDesk measuring as size, posts recorded in sent.
func stubGrowth(t *testing.T, now *time.Time, size *int64, sent *[]GrowthAlert) {
	t.Helper()
	setHome(t, t.TempDir())
	n, m, notify, changed, owner := growthNow, growthMeasure, growthNotify, growthChanged, growthOwnerOf
	t.Cleanup(func() { growthNow, growthMeasure, growthNotify, growthChanged, growthOwnerOf = n, m, notify, changed, owner })
	growthNow = func() time.Time { return *now }
	growthMeasure = func(ctx context.Context, prev *growthSnapshot) growthSnapshot {
		return growthSnapshot{At: now.Unix(), FDA: true, Entries: []growthEntry{done(todesk, *size)}}
	}
	growthNotify = func(a GrowthAlert) error { *sent = append(*sent, a); return nil }
	growthChanged = func(GrowthStatus) {}
	growthOwnerOf = func() func(growthEntry) growthOwner { return byBase }
}

func TestMeasureOnceAlertsOnlyWhileMonitoring(t *testing.T) {
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	m := &growthMonitor{launched: t0, notifs: "unknown"}
	m.measureOnce(context.Background())
	now, size = t0.Add(24*time.Hour), 14*gb
	m.measureOnce(context.Background())
	if len(sent) != 0 || len(loadGrowthSnapshots(growthDir())) != 2 {
		t.Fatalf("monitoring off: sent %+v", sent)
	}
	c := defaultGrowthConfig()
	c.Enabled = true
	if err := saveGrowthConfig(growthDir(), c); err != nil {
		t.Fatal(err)
	}
	now, size = t0.Add(48*time.Hour), 18*gb
	m.measureOnce(context.Background())
	if len(sent) != 1 || sent[0].Key != "app:todesk" || sent[0].Bytes != 4*gb || sent[0].Window != "day" {
		t.Fatalf("sent = %+v", sent)
	}
	if recent := loadGrowthNotified(growthDir()).recent(now); len(recent) != 1 {
		t.Fatalf("recent = %+v", recent)
	}
}

func TestStoppedRunSavesNothing(t *testing.T) {
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	(&growthMonitor{}).measureOnce(ctx)
	if got := loadGrowthSnapshots(growthDir()); len(got) != 0 {
		t.Fatalf("records = %+v", got)
	}
}

func TestUserScanCancelsOnlyBackgroundRuns(t *testing.T) {
	old := growth
	t.Cleanup(func() { growth = old })
	ctx, cancel := context.WithCancel(context.Background())
	growth = &growthMonitor{running: true, cancel: cancel}
	userScan()()
	if ctx.Err() == nil {
		t.Fatal("a background run must stop for a user scan")
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	growth = &growthMonitor{running: true, manual: true, cancel: cancel2}
	userScan()()
	if ctx2.Err() != nil {
		t.Fatal("Measure Now keeps going")
	}
}

func TestNativeTexts(t *testing.T) {
	var m map[string]map[string]string
	if err := json.Unmarshal(nativeMessages, &m); err != nil {
		t.Fatal(err)
	}
	for key := range m["zh"] {
		if strings.HasPrefix(key, "growth.") && m["en"][key] == "" {
			t.Errorf("%s has no English text", key)
		}
	}
	if got := nativeTextFor("zh-Hans", "growth.notify.day", map[string]string{"name": "ToDesk", "size": "5.6 GB"}); got != "ToDesk 一天涨了 5.6 GB" {
		t.Errorf("zh = %q", got)
	}
	if got := nativeTextFor("en-US", "growth.notify.week", map[string]string{"name": "ToDesk", "size": "12 GB", "days": "5"}); got != "ToDesk grew 12 GB in 5 days" {
		t.Errorf("en = %q", got)
	}
}

func TestGrowthSizeMatchesThePage(t *testing.T) {
	for n, want := range map[int64]string{1023: "1023 B", 512 << 20: "512 MB", 6012954214: "5.6 GB", 152 * gb: "152 GB", 2 << 40: "2.0 TB"} {
		if got := growthSize(n); got != want {
			t.Errorf("growthSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestGrowthAlertTexts(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	a := GrowthAlert{Name: "ToDesk", Window: "day", Bytes: 6012954214, Folder: filepath.Join(home, "Library", "Caches", "ToDesk"), Total: 152 * gb}
	if got := growthTitle("en", a); got != "ToDesk grew 5.6 GB in a day" {
		t.Errorf("title = %q", got)
	}
	if got := growthBody("en", a); got != "Mostly in "+filepath.Join("~", "Library", "Caches", "ToDesk")+"; 152 GB in all now" {
		t.Errorf("body = %q", got)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test -run 'GrowthNextRun|GrowthDeferral|MeasureOnce|StoppedRun|UserScanCancels|NativeTexts|GrowthSize|GrowthAlertTexts' -count=1 .`
Expected: FAIL to compile.

- [ ] **Step 4: Write `growth_monitor.go`**

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// Seams. Tests replace these; the app uses them as they are.
var (
	growthNow = time.Now
	// growthMeasure takes one record of this Mac.
	growthMeasure = func(ctx context.Context, prev *growthSnapshot) growthSnapshot {
		fda := fullDiskAccess()
		targets := growthTargets(growthRoots(homeDir(), string(filepath.Separator)), fda, filepath.Dir(configPath()))
		return takeGrowthSnapshot(ctx, targets, prev, fda, growthNow())
	}
	growthNotify  = postGrowthNotification
	growthChanged = func(s GrowthStatus) {
		GrowthUpdated.Broadcast(s)
		go refreshGrowthTray()
	}
	growthOwnerOf = func() func(growthEntry) growthOwner { return currentGrowthAttributor().owner }
	growthPower   = growthBattery
)

// growthMonitor measures in the background while monitoring is on, and on
// request.
type growthMonitor struct {
	mu       sync.Mutex
	started  bool
	running  bool
	manual   bool // the running measurement was asked for
	cancel   context.CancelFunc
	launched time.Time
	nextAt   time.Time
	notifs   string // "unknown", "allowed", "denied" or "unsupported"
}

// growth is the app's monitor.
var growth = &growthMonitor{launched: time.Now(), notifs: "unknown"}

// userScans counts the scans and cleans the user is running; background
// measuring waits for them (spec §四).
var userScans atomic.Int32

// userScan marks a user scan or clean for its duration, stopping a
// background measurement under way: defer userScan()().
func userScan() func() {
	userScans.Add(1)
	growth.cancelBackground()
	return func() { userScans.Add(-1) }
}

// growthNextRun is when the next background measurement is due (spec §四):
//   - right away, when monitoring was just turned on without a record from
//     the last 20 hours;
//   - five minutes after launch, once the last record is 20 hours old;
//   - otherwise 24 hours after the last record.
func growthNextRun(last, launched, now time.Time, justEnabled bool) time.Time {
	stale := last.IsZero() || now.Sub(last) >= 20*time.Hour
	if justEnabled && stale {
		return now
	}
	if stale {
		if t := launched.Add(5 * time.Minute); t.After(now) {
			return t
		}
		return now
	}
	return last.Add(24 * time.Hour)
}

// growthDeferral tells how long a due measurement waits (spec §四):
//   - on battery at 20% or less, half an hour;
//   - during a user scan, ten minutes;
//   - otherwise 0.
func growthDeferral(onBattery bool, percent int, scans int32) time.Duration {
	switch {
	case onBattery && percent <= 20:
		return 30 * time.Minute
	case scans > 0:
		return 10 * time.Minute
	}
	return 0
}

// start begins the background loop once. It checks every 15 minutes, and
// whenever the Mac wakes, against wall-clock time, so sleep never stretches
// the schedule.
func (m *growthMonitor) start() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()
	mygo.Power.OnResume(func() { go m.tick(false) })
	go func() {
		m.tick(false)
		for range time.Tick(15 * time.Minute) {
			m.tick(false)
		}
	}()
}

// tick measures when monitoring is on and a measurement is due and allowed.
func (m *growthMonitor) tick(justEnabled bool) {
	if !growthSupported() {
		return
	}
	dir := growthDir()
	if !loadGrowthConfig(dir).Enabled {
		m.setNext(time.Time{})
		return
	}
	now := growthNow()
	due := growthNextRun(lastGrowthAt(dir), m.launched, now, justEnabled)
	if now.Before(due) {
		m.setNext(due)
		return
	}
	onBattery, percent := growthPower(context.Background())
	if wait := growthDeferral(onBattery, percent, userScans.Load()); wait > 0 {
		m.setNext(now.Add(wait))
		return
	}
	m.run(false)
}

// setNext records when the next measurement is planned, for the page.
func (m *growthMonitor) setNext(t time.Time) {
	m.mu.Lock()
	changed := !m.nextAt.Equal(t)
	m.nextAt = t
	m.mu.Unlock()
	if changed {
		growthChanged(m.status())
	}
}

// run starts one measurement unless one is under way, reporting whether it
// started. A background run stops when the user starts a scan; a manual one
// does not.
func (m *growthMonitor) run(manual bool) bool {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.running, m.manual, m.cancel = true, manual, cancel
	m.mu.Unlock()
	growthChanged(m.status())
	go func() {
		m.measureOnce(ctx)
		cancel()
		m.mu.Lock()
		m.running, m.manual, m.cancel = false, false, nil
		m.mu.Unlock()
		if loadGrowthConfig(growthDir()).Enabled {
			m.setNext(growthNextRun(lastGrowthAt(growthDir()), m.launched, growthNow(), false))
		}
		growthChanged(m.status())
	}()
	return true
}

// measureOnce takes one record and keeps the alerts it raises. With
// monitoring on, it also posts them within the limits of spec §五. It saves
// nothing when ctx ends first.
func (m *growthMonitor) measureOnce(ctx context.Context) {
	dir := growthDir()
	history := loadGrowthSnapshots(dir)
	var prev *growthSnapshot
	if len(history) > 0 {
		prev = &history[len(history)-1]
	}
	snap := growthMeasure(ctx, prev)
	if ctx.Err() != nil {
		return
	}
	now := growthNow()
	if saveGrowthSnapshot(dir, snap, now) != nil {
		return
	}
	cfg := loadGrowthConfig(dir)
	alerts := evaluateGrowth(history, snap, cfg, growthOwnerOf())
	n := loadGrowthNotified(dir)
	n.remember(alerts, now)
	if cfg.Enabled {
		for _, a := range pickGrowthNotifications(alerts, &n, now) {
			m.noteNotify(growthNotify(a))
		}
	}
	_ = saveGrowthNotified(dir, n)
}

// noteNotify keeps whether notifications can be shown, for the page.
func (m *growthMonitor) noteNotify(err error) {
	state := "allowed"
	switch {
	case errors.Is(err, mygo.ErrNotificationsDenied):
		state = "denied"
	case errors.Is(err, errGrowthNoNotifications):
		state = "unsupported"
	case err != nil:
		return
	}
	m.mu.Lock()
	m.notifs = state
	m.mu.Unlock()
}

// cancelBackground stops a background measurement under way.
func (m *growthMonitor) cancelBackground() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running && !m.manual && m.cancel != nil {
		m.cancel()
	}
}

// status is what the page shows about measuring.
func (m *growthMonitor) status() GrowthStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := GrowthStatus{Running: m.running, FDA: fullDiskAccess(), Notifications: m.notifs}
	if last := lastGrowthAt(growthDir()); !last.IsZero() {
		s.LastAt = last.Unix()
	}
	if !m.nextAt.IsZero() {
		s.NextAt = m.nextAt.Unix()
	}
	return s
}

// changed tells the pages and the menu that something moved.
func (m *growthMonitor) changed() { growthChanged(m.status()) }

// errGrowthNoNotifications means this build cannot show notifications, like
// an unpackaged `go run`.
var errGrowthNoNotifications = errors.New("notifications unsupported")

// postGrowthNotification shows one alert. Clicking it opens the growth page
// at the app, even after a relaunch (App.OnNotificationClick in growth_app.go).
func postGrowthNotification(a GrowthAlert) error {
	if !mygo.NotificationsSupported() {
		return errGrowthNoNotifications
	}
	locale := mygo.App.Locale()
	return mygo.NewNotification(mygo.NotificationOptions{
		ID: "growth:" + a.Key, Title: growthTitle(locale, a), Body: growthBody(locale, a), Group: "growth",
	}).Show()
}

// growthTitle says who grew how much, and over which window.
func growthTitle(locale string, a GrowthAlert) string {
	key := "growth.notify." + a.Window
	if a.AtLeast {
		key += "AtLeast"
	}
	return nativeTextFor(locale, key, map[string]string{"name": a.Name, "size": growthSize(a.Bytes), "days": strconv.Itoa(a.Days)})
}

// growthBody says where it grew, with the home folder shown as ~.
func growthBody(locale string, a GrowthAlert) string {
	folder := a.Folder
	if h := homeDir(); within(folder, h) {
		folder = filepath.Join("~", strings.TrimPrefix(folder, h))
	}
	return nativeTextFor(locale, "growth.notify.body", map[string]string{"folder": folder, "total": growthSize(a.Total)})
}

// nativeTexts are the texts of src/native-i18n.json, parsed once.
var nativeTexts = sync.OnceValue(func() map[string]map[string]string {
	var m map[string]map[string]string
	if err := json.Unmarshal(nativeMessages, &m); err != nil {
		panic(err) // embedded, validated by TestNativeTexts
	}
	return m
})

// nativeTextFor translates key for locale, where zh-* is Chinese and
// anything else English, and fills {name}-style placeholders from vars.
func nativeTextFor(locale, key string, vars map[string]string) string {
	lang := "en"
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		lang = "zh"
	}
	s := nativeTexts()[lang][key]
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// nativeText translates key for the app's language.
func nativeText(key string, vars map[string]string) string {
	return nativeTextFor(mygo.App.Locale(), key, vars)
}

// growthSize writes a size like the page's human() in src/shared.ts:
// 1024-based units, with one decimal below 100.
func growthSize(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v, i := float64(n), -1
	for {
		v /= 1024
		i++
		if v < 1024 || i == len(units)-1 {
			break
		}
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
```

`growthBody` turns `/Users/u/Library/Caches/ToDesk` into `~/Library/Caches/ToDesk`. `filepath.Join("~", "/Library/Caches/ToDesk")` gives `~/Library/Caches/ToDesk`, and TrimPrefix leaves the leading separator, which Join cleans away. `TestGrowthAlertTexts` covers this.

- [ ] **Step 5: Write `growth_api.go`**

```go
package main

import (
	"errors"
	"time"

	"github.com/egoist/mygo"
)

// GrowthStatus tells whether measuring runs and when (spec §六).
type GrowthStatus struct {
	Running       bool   `json:"running"`
	LastAt        int64  `json:"lastAt"` // unix seconds; 0: never
	NextAt        int64  `json:"nextAt"` // unix seconds; 0: not planned (monitoring off)
	FDA           bool   `json:"fda"`
	Notifications string `json:"notifications"` // "unknown", "allowed", "denied" or "unsupported"
}

// GrowthState is everything the growth page shows.
type GrowthState struct {
	Supported bool          `json:"supported"` // false on Windows
	Config    GrowthConfig  `json:"config"`
	Status    GrowthStatus  `json:"status"`
	Records   int           `json:"records"`
	Apps      []GrowthApp   `json:"apps"`   // largest first
	Alerts    []GrowthAlert `json:"alerts"` // raised in the last seven days, newest first
}

// GrowthUpdated tells pages that measuring started or finished, or that the
// monitor's settings changed.
var GrowthUpdated = mygo.NewEvent[GrowthStatus]("growth:updated")

// GrowthFocus asks the page to show one owner, by key: a notification or a
// menu item was clicked.
var GrowthFocus = mygo.NewEvent[string]("growth:focus")

// GrowthState returns everything the growth page shows.
func (Cleaner) GrowthState() GrowthState {
	dir := growthDir()
	st := GrowthState{Supported: growthSupported(), Config: loadGrowthConfig(dir), Status: growth.status(), Apps: []GrowthApp{}, Alerts: []GrowthAlert{}}
	if !st.Supported {
		return st
	}
	history := loadGrowthSnapshots(dir)
	st.Records = len(history)
	st.Apps = buildGrowthApps(history, st.Config, growthOwnerOf(), homeDir(), time.Local)
	st.Alerts = loadGrowthNotified(dir).recent(growthNow())
	return st
}

// GrowthMeasureNow starts a measurement unless one is running. It works
// with monitoring off too, without notifications.
func (Cleaner) GrowthMeasureNow() error {
	if !growthSupported() {
		return errors.New("unsupported")
	}
	growth.run(true)
	return nil
}

// SaveGrowthConfig validates and saves the monitor's settings, then applies
// them: the menu bar icon, staying open without windows, the login item.
// Turning monitoring on takes a baseline record at once.
func (Cleaner) SaveGrowthConfig(c GrowthConfig) error {
	clean, err := validateGrowthConfig(c)
	if err != nil {
		return err
	}
	dir := growthDir()
	was := loadGrowthConfig(dir).Enabled
	if err := saveGrowthConfig(dir, clean); err != nil {
		return err
	}
	if err := applyGrowthLifecycle(clean); err != nil {
		return err
	}
	if clean.Enabled && !was {
		go growth.tick(true)
	}
	growth.changed()
	return nil
}

// applyGrowthLifecycle is replaced by growth_app.go in Task 6.
func applyGrowthLifecycle(GrowthConfig) error { return nil }

// refreshGrowthTray is replaced by growth_app.go in Task 6.
func refreshGrowthTray() {}
```

- [ ] **Step 6: Hook user scans**

Add `defer userScan()()` as the first line of `func (Cleaner) ScanJunk`, `func (Cleaner) CleanJunk` (junk.go), `func (Cleaner) FullScan` and `func (Cleaner) LargeScan` (main.go).

- [ ] **Step 7: Regenerate the bindings and run everything**

Run:
1. `bun run generate`
2. `go test -race -count=1 ./...`
3. `GOOS=windows GOARCH=amd64 go vet ./...`
4. `bun run typecheck`

Expected:
- `src/mygo.ts` now has `growthState`, `growthMeasureNow` and `saveGrowthConfig` under `Cleaner`, and `growthUpdated` and `growthFocus` under `events`, plus the `Growth*` types.
- All tests pass.

- [ ] **Step 8: Leave the changes uncommitted for the reviewer**

---

### Task 6: Menu bar, windows, login item and notification clicks

**Files:**
- Create: `growth_app.go`
- Modify:
  - `growth_api.go`: delete the two stubs, `applyGrowthLifecycle` and `refreshGrowthTray`.
  - `main.go`: extract `newMainWindow`, then call `registerGrowthLifecycle` and `startGrowthResident`.
- Test: `growth_app_test.go`

**Interfaces:**
- Consumes: Task 5.
- Produces:
  - `var mainWindow *mygo.Window` and `func newMainWindow() *mygo.Window` (main.go)
  - `func openMainWindow() *mygo.Window`
  - `func registerGrowthLifecycle()` and `func startGrowthResident() bool`
  - `func applyGrowthLifecycle(cfg GrowthConfig) error` and `func refreshGrowthTray()`
  - `func focusGrowth(key string)`
  - `type growthMenuItem` and `func growthMenuModel(apps []GrowthApp, running bool, text func(string, map[string]string) string) []growthMenuItem`
  - `func growthIconPNG() []byte`

- [ ] **Step 1: Write the failing tests**

`growth_app_test.go`:

```go
package main

import (
	"bytes"
	"image/png"
	"testing"
)

func TestGrowthMenuModel(t *testing.T) {
	text := func(key string, vars map[string]string) string { return key + ":" + vars["name"] + vars["size"] }
	apps := []GrowthApp{
		{Key: "app:a", Name: "A", Week: &GrowthDelta{Bytes: 3 * gb}},
		{Key: "app:b", Name: "B", Week: &GrowthDelta{Bytes: 9 * gb, AtLeast: true}},
		{Key: "app:c", Name: "C", Week: &GrowthDelta{Bytes: 5 * gb}, Ignored: true},
		{Key: "app:d", Name: "D", Week: &GrowthDelta{Bytes: -gb}},
		{Key: "app:e", Name: "E"},
		{Key: "app:f", Name: "F", Week: &GrowthDelta{Bytes: gb}},
		{Key: "app:g", Name: "G", Week: &GrowthDelta{Bytes: 2 * gb}},
	}
	items := growthMenuModel(apps, false, text)
	want := []growthMenuItem{
		{label: "growth.tray.itemAtLeast:B9.0 GB", action: "focus", key: "app:b"},
		{label: "growth.tray.item:A3.0 GB", action: "focus", key: "app:a"},
		{label: "growth.tray.item:G2.0 GB", action: "focus", key: "app:g"},
		{},
		{label: "growth.tray.open:", action: "open"},
		{label: "growth.tray.measure:", action: "measure"},
		{label: "growth.tray.off:", action: "off"},
		{},
		{label: "growth.tray.quit:", action: "quit"},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %+v", items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, items[i], want[i])
		}
	}
	busy := growthMenuModel(nil, true, text)
	if busy[0] != (growthMenuItem{label: "growth.tray.none:", disabled: true}) || busy[3] != (growthMenuItem{label: "growth.tray.measuring:", disabled: true}) {
		t.Fatalf("busy, empty = %+v", busy)
	}
}

func TestGrowthIconIsATemplate(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(growthIconPNG()))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("bounds = %v", b)
	}
	ink := 0
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			switch {
			case a == 0:
			case r == 0 && g == 0 && b == 0 && a == 0xffff:
				ink++
			default:
				t.Fatalf("pixel %d,%d is neither clear nor black", x, y)
			}
		}
	}
	if ink < 100 {
		t.Fatalf("only %d ink pixels", ink)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test -run 'GrowthMenuModel|GrowthIcon' -count=1 .`
Expected: FAIL to compile.

- [ ] **Step 3: Extract `newMainWindow` in `main.go`**

Move four pieces from the body of `WhenReady` into a new function, unchanged: the `opts` literal, the smoke preload, the Windows `SetWindowOpenHandler` call and `startSmoke`. The comments below mark where each moved piece goes:

```go
// mainWindow is the app's window while it exists.
var mainWindow *mygo.Window

// newMainWindow creates the main window with the options Cleanzo always
// used, including the smoke tour hooks.
func newMainWindow() *mygo.Window {
	opts := mygo.WindowOptions{ /* exactly the fields main() sets today */ }
	smokeDir := os.Getenv("CLEANZO_SMOKE_DIR")
	if smokeDir != "" {
		opts.Page.PreloadScript = smokeScript
	}
	w := mygo.NewWindow(opts)
	if runtime.GOOS == "windows" {
		w.Page().SetWindowOpenHandler( /* unchanged */ )
	}
	if smokeDir != "" {
		startSmoke(w, smokeDir)
	}
	return w
}
```

`main()` becomes:

```go
func main() {
	mygo.App.SetLanguages("en", "zh-Hans")
	// 自动检查更新的频率：4 小时一次（默认一天），新发布的版本当天就能被看到。
	mygo.Use(updater.New(updater.Options{Interval: 4 * time.Hour}))
	mygo.Bind(Cleaner{})
	mygo.Bind(LanguageSettings{})
	registerGrowthLifecycle() // before Run, to hear the notification click that launched the app

	mygo.App.WhenReady(func() {
		if runtime.GOOS == "windows" && !mygo.WebViewAvailable() {
			go offerWebView()
			return
		}
		if startGrowthResident() {
			return // opened at login with monitoring on: menu bar icon only (spec §六)
		}
		mainWindow = newMainWindow()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 4: Write `growth_app.go`**

```go
package main

import (
	"bytes"
	"cmp"
	"image"
	"image/color"
	"image/png"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// growthTray is the menu bar icon while monitoring is on.
var growthTray *mygo.Tray

// registerGrowthLifecycle installs the handlers that keep the app open
// without windows while monitoring is on (spec §六). It runs before Run, as
// App.OnNotificationClick must for the click that launched the app.
//
// With monitoring off, closing the last window quits, as it always did. On
// Windows, growthSupported is false, so the app quits too.
func registerGrowthLifecycle() {
	mygo.App.OnWindowAllClosed(func() {
		if growthSupported() && loadGrowthConfig(growthDir()).Enabled {
			mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
			return
		}
		mygo.App.Quit()
	})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			openMainWindow()
		}
	})
	mygo.App.OnNotificationClick(func(id string) {
		if key, ok := strings.CutPrefix(id, "growth:"); ok {
			focusGrowth(key)
		}
	})
}

// startGrowthResident starts the monitor's loop, and with monitoring on,
// the menu bar icon and login item. It reports true when the app was opened
// at login with monitoring on: then no window opens.
func startGrowthResident() bool {
	if !growthSupported() {
		return false
	}
	growth.start()
	cfg := loadGrowthConfig(growthDir())
	if !cfg.Enabled {
		return false
	}
	if err := applyGrowthLifecycle(cfg); err != nil {
		log.Println("growth:", err)
	}
	if mygo.App.WasOpenedAtLogin() {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
		return true
	}
	return false
}

// openMainWindow shows the main window, creating it again after it was
// closed, and brings the Dock icon back.
func openMainWindow() *mygo.Window {
	if mainWindow == nil || !slices.Contains(mygo.Windows(), mainWindow) {
		mainWindow = newMainWindow()
	} else {
		mainWindow.Show()
		mainWindow.Focus()
	}
	if growthSupported() {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	}
	mygo.App.Focus()
	return mainWindow
}

// focusGrowth opens the window on the growth page at one owner.
func focusGrowth(key string) {
	mygo.RunOnMain(func() {
		_ = GrowthFocus.Emit(openMainWindow(), key) // delivered once the page is ready
	})
}

// applyGrowthLifecycle puts the app in the state cfg asks for (spec §六):
//   - with monitoring on, a menu bar icon and the login item as chosen;
//   - with it off, no icon and no login item, and the app quits when no
//     window is visible.
func applyGrowthLifecycle(cfg GrowthConfig) error {
	if !growthSupported() {
		return nil
	}
	var menu *mygo.Menu
	if cfg.Enabled {
		// reading the records takes a moment: build the menu before going to the main thread
		menu = growthTrayMenu(growthMenuModel(Cleaner{}.GrowthState().Apps, growth.status().Running, nativeText))
	}
	var loginErr error
	mygo.RunOnMain(func() { // runs inline when already on the main thread, as in a menu click
		if cfg.Enabled {
			showGrowthTray(menu)
		} else if growthTray != nil {
			growthTray.Destroy()
			growthTray = nil
		}
		if want := cfg.Enabled && cfg.OpenAtLogin; mygo.App.OpenAtLogin() != want {
			loginErr = mygo.App.SetOpenAtLogin(want)
		}
		if !cfg.Enabled && !slices.ContainsFunc(mygo.Windows(), (*mygo.Window).IsVisible) {
			mygo.App.Quit()
		}
	})
	if loginErr != nil {
		return codeError{Code: "growth-login", Args: []string{loginErr.Error()}}
	}
	return nil
}

// showGrowthTray makes the menu bar icon with menu, or gives the existing
// icon that menu. It runs on the main thread.
func showGrowthTray(menu *mygo.Menu) {
	if growthTray != nil {
		growthTray.SetMenu(menu)
		return
	}
	t, err := mygo.NewTray(mygo.TrayOptions{ID: "growth", Icon: growthIconPNG(), IconIsTemplate: true, ToolTip: nativeText("growth.tray.tip", nil), Menu: menu})
	if err != nil {
		log.Println("tray:", err)
		return
	}
	growthTray = t
}

// refreshGrowthTray rebuilds the menu from the records, if the icon shows.
func refreshGrowthTray() {
	if growthTray == nil {
		return
	}
	menu := growthTrayMenu(growthMenuModel(Cleaner{}.GrowthState().Apps, growth.status().Running, nativeText))
	mygo.RunOnMain(func() {
		if growthTray != nil {
			growthTray.SetMenu(menu)
		}
	})
}

// growthMenuItem is one line of the menu bar menu. A zero item is a separator.
type growthMenuItem struct {
	label    string
	action   string // "focus", "open", "measure", "off" or "quit"
	key      string // the owner "focus" shows
	disabled bool
}

// growthMenuModel lists the menu (spec §六):
//   - the three owners that grew most in seven days, ignored ones left out;
//   - then open, measure now, turning monitoring off, and quit.
func growthMenuModel(apps []GrowthApp, running bool, text func(string, map[string]string) string) []growthMenuItem {
	var top []GrowthApp
	for _, a := range apps {
		if a.Week != nil && a.Week.Bytes > 0 && !a.Ignored {
			top = append(top, a)
		}
	}
	slices.SortFunc(top, func(x, y GrowthApp) int { return cmp.Or(cmp.Compare(y.Week.Bytes, x.Week.Bytes), strings.Compare(x.Key, y.Key)) })
	var items []growthMenuItem
	for _, a := range top[:min(3, len(top))] {
		key := "growth.tray.item"
		if a.Week.AtLeast {
			key = "growth.tray.itemAtLeast"
		}
		items = append(items, growthMenuItem{label: text(key, map[string]string{"name": a.Name, "size": growthSize(a.Week.Bytes)}), action: "focus", key: a.Key})
	}
	if len(items) == 0 {
		items = append(items, growthMenuItem{label: text("growth.tray.none", nil), disabled: true})
	}
	measure := growthMenuItem{label: text("growth.tray.measure", nil), action: "measure"}
	if running {
		measure = growthMenuItem{label: text("growth.tray.measuring", nil), disabled: true}
	}
	return append(items,
		growthMenuItem{},
		growthMenuItem{label: text("growth.tray.open", nil), action: "open"},
		measure,
		growthMenuItem{label: text("growth.tray.off", nil), action: "off"},
		growthMenuItem{},
		growthMenuItem{label: text("growth.tray.quit", nil), action: "quit"},
	)
}

// growthTrayMenu turns the model into a mygo menu.
func growthTrayMenu(items []growthMenuItem) *mygo.Menu {
	var out []*mygo.MenuItem
	for _, it := range items {
		if it == (growthMenuItem{}) {
			out = append(out, mygo.Separator())
			continue
		}
		out = append(out, &mygo.MenuItem{Label: it.label, Disabled: it.disabled, Click: func(*mygo.MenuItem, *mygo.Window) { growthMenuAction(it) }})
	}
	return mygo.NewMenu(out)
}

// growthMenuAction does what a menu line says.
func growthMenuAction(it growthMenuItem) {
	switch it.action {
	case "focus":
		focusGrowth(it.key)
	case "open":
		openMainWindow()
	case "measure":
		growth.run(true)
	case "off":
		cfg := loadGrowthConfig(growthDir())
		cfg.Enabled, cfg.OpenAtLogin = false, false
		if err := saveGrowthConfig(growthDir(), cfg); err == nil {
			_ = applyGrowthLifecycle(cfg)
			growth.setNext(time.Time{})
			growth.changed()
		}
	case "quit":
		mygo.App.Quit()
	}
}

// growthIconPNG draws the menu bar icon: a drive with an arrow rising above
// it. It is a template image, black on transparent, 32×32 pixels for 16
// points at 2×; macOS tints it for light and dark menu bars.
func growthIconPNG() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	ink := color.NRGBA{A: 255}
	fill := func(x0, y0, x1, y1 int) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				img.SetNRGBA(x, y, ink)
			}
		}
	}
	// drive: a 2 px outline from (4,19) to (28,29), with a light at the right
	fill(4, 19, 28, 21)
	fill(4, 27, 28, 29)
	fill(4, 19, 6, 29)
	fill(26, 19, 28, 29)
	fill(21, 23, 24, 25)
	// arrow: a shaft and a head pointing up from (16,3)
	fill(15, 3, 17, 16)
	for i := 0; i <= 6; i++ {
		fill(15-i, 3+i, 17-i, 5+i)
		fill(15+i, 3+i, 17+i, 5+i)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
```

`mygo.RunOnMain` runs its function inline when called on the main thread (`loop.go` in mygo 0.6.2), so menu clicks do not deadlock. From other goroutines it waits for the main thread, which is why `applyGrowthLifecycle` can read `loginErr` afterwards.

- [ ] **Step 5: Run the tests and the whole suite**

Run:
1. `go test -race -count=1 ./...`
2. `GOOS=windows GOARCH=amd64 go vet ./...`
3. `bun run generate`; the bindings must not change.
4. `bun run typecheck`

Expected: PASS.

- [ ] **Step 6: Leave the changes uncommitted for the reviewer**

The reviewer checks the lifecycle by hand in Task 10.

---

### Task 7: The growth page

**Files:**
- Create: `src/react/growth/api.ts`, `src/react/growth/format.ts`, `src/react/growth/GrowthPage.tsx`, `src/react/growth/entry.ts`, `src/react/growth/mount.tsx`
- Modify:
  - `index.html`: the nav item and the view.
  - `src/shared.ts`: `VIEWS`.
  - `src/main.ts`: switching, events, and hiding on Windows.
  - `src/i18n.ts`, `src/style.css`, `src/dev/mock-mygo.ts`.
- Test: `tests/growth-react.test.ts` (new); add it to `test:web` in `package.json`.

**Interfaces:**
- Consumes: the generated `Cleaner.growthState`, `Cleaner.growthMeasureNow`, `Cleaner.saveGrowthConfig`, `Cleaner.appUninstallDetail`, `Cleaner.revealInFinder`, `events.growthUpdated`, `events.growthFocus` and the `Growth*` types.
- Produces:
  - In `format.ts`: `growthView`, `growthBanner`, `fmtDelta`, `sortApps`, `sparkPath` and `uninstallGap`.
  - In `entry.ts`: `showReactGrowth()`, `focusReactGrowth(key: string)` and `setGrowthHandlers(h: { goLeftovers(): void; goUninstall(appPath: string): void })`.
  - Task 9 uses the exported `growthApi`.

- [ ] **Step 1: Write the failing tests**

`tests/growth-react.test.ts`:

```ts
import { expect, test } from "bun:test";
import type { GrowthApp, GrowthState, UninstallDetail } from "../src/mygo";
import { t } from "../src/i18n";
import { fmtDelta, growthBanner, growthView, sortApps, sparkPath, uninstallGap } from "../src/react/growth/format";

const storage = new Map([["cleanzo.lang", "zh"]]);
Object.assign(globalThis, {
  localStorage: { getItem: (k: string) => storage.get(k), setItem: (k: string, v: string) => storage.set(k, v) },
});

const GB = 2 ** 30;

function app(p: Partial<GrowthApp>): GrowthApp {
  return { key: "app:x", name: "X", kind: "app", orphan: false, appPath: "", size: 0, partial: false, ignored: false,
    day: null, week: null, month: null, spark: [], folders: [], ...p };
}

function state(p: Partial<GrowthState>): GrowthState {
  return {
    supported: true, records: 0, apps: [], alerts: [],
    config: { enabled: false, openAtLogin: false, dailyGB: 2, weeklyGB: 10, ignored: [], asked: false },
    status: { running: false, lastAt: 0, nextAt: 0, fda: true, notifications: "unknown" }, ...p,
  };
}

test("页面状态：不支持 / 没有记录 / 只有一次 / 列表", () => {
  expect(growthView(null)).toBe("loading");
  expect(growthView(state({ supported: false }))).toBe("unsupported");
  expect(growthView(state({ records: 0 }))).toBe("empty");
  expect(growthView(state({ records: 1 }))).toBe("baseline");
  expect(growthView(state({ records: 3 }))).toBe("list");
});

test("横幅：没开过就询问，问过没开就提示，开着不显示", () => {
  expect(growthBanner(state({}))).toBe("optin");
  expect(growthBanner(state({ config: { ...state({}).config, asked: true } }))).toBe("off");
  expect(growthBanner(state({ config: { ...state({}).config, enabled: true } }))).toBeNull();
});

test("增长文案：正负、下限、没有可比记录", () => {
  const atLeast = (s: string) => t("growth.atLeast", { size: s });
  expect(fmtDelta({ bytes: 5.6 * GB, atLeast: false, days: 1 }, atLeast)).toBe("+5.6 GB");
  expect(fmtDelta({ bytes: -GB, atLeast: false, days: 1 }, atLeast)).toBe("−1.0 GB");
  expect(fmtDelta({ bytes: 3 * GB, atLeast: true, days: 1 }, atLeast)).toBe("至少 +3.0 GB");
  expect(fmtDelta(null, atLeast)).toBe("—");
});

test("排序：按 7 天增长时没有数据的排后面，再按占用", () => {
  const apps = [app({ key: "a", size: 1, week: { bytes: GB, atLeast: false, days: 7 } }), app({ key: "b", size: 9 }),
    app({ key: "c", size: 2, week: { bytes: 5 * GB, atLeast: false, days: 7 } }), app({ key: "d", size: 5 })];
  expect(sortApps(apps, "week").map((a) => a.key)).toEqual(["c", "a", "b", "d"]);
  expect(sortApps(apps, "size").map((a) => a.key)).toEqual(["b", "d", "c", "a"]);
  expect(apps[0].key).toBe("a"); // 不改原数组
});

test("迷你曲线：-1 处断开，少于两个点不画", () => {
  expect(sparkPath([-1, 5, -1], 100, 20)).toBe("");
  expect(sparkPath([0, 10, -1, 10], 30, 10)).toBe("M0.0 10.0L10.0 0.0M30.0 0.0");
});

test("卸载能移走多少、另外剩多少（不算 App 本身）", () => {
  const detail = { app: {} as UninstallDetail["app"], totalSize: 4 * GB,
    items: [{ path: "/A.app", kind: "app", size: GB }, { path: "/c", kind: "cache", size: 3 * GB }] } as UninstallDetail;
  expect(uninstallGap(30 * GB, detail)).toEqual({ freed: 4 * GB, rest: 27 * GB });
  expect(uninstallGap(GB, detail)).toEqual({ freed: 4 * GB, rest: 0 });
  expect(uninstallGap(GB, null)).toBeNull();
});

test("新文案中英文都有", () => {
  for (const key of ["nav.growth", "growth.sub", "growth.optin.body", "growth.uninstallGap", "growth.outside"] as const) {
    storage.set("cleanzo.lang", "en");
    expect(t(key)).not.toBe(key);
    storage.set("cleanzo.lang", "zh");
    expect(t(key)).not.toBe(key);
  }
});
```

If `t()` reads the language once at import rather than on every call, follow how `tests/crash-dumps.test.ts` switches languages instead.

- [ ] **Step 2: Run them to verify they fail**

Run: `bun test tests/growth-react.test.ts`
Expected: FAIL, because `../src/react/growth/format` is not found.

- [ ] **Step 3: Write `src/react/growth/format.ts`**

```ts
// 占用增长页的纯函数：页面状态、横幅、增长文案、排序、迷你曲线、卸载差额。测试直接覆盖。
import type { GrowthApp, GrowthDelta, GrowthState, UninstallDetail } from "../../mygo";
import { human } from "../../shared";

/** 列表排序方式。 */
export type GrowthSort = "week" | "size";

/** 页面主体显示哪一块。 */
export type GrowthView = "loading" | "unsupported" | "empty" | "baseline" | "list";

/** 页面主体：不支持 / 没有记录 / 只有一次记录（还看不到增长）/ 列表。 */
export function growthView(s: GrowthState | null): GrowthView {
  if (!s) return "loading";
  if (!s.supported) return "unsupported";
  if (s.records === 0) return "empty";
  return s.records === 1 ? "baseline" : "list";
}

/** 顶部横幅：没问过、没开监控时请用户打开；问过还没开就提示一句；开着不显示。 */
export function growthBanner(s: GrowthState): "optin" | "off" | null {
  if (s.config.enabled) return null;
  return s.config.asked ? "off" : "optin";
}

/** 增长文案：+5.6 GB；变小写 −；下限交给 atLeast 包一层（「至少 …」）；没有可比的记录为 —。 */
export function fmtDelta(d: GrowthDelta | null | undefined, atLeast: (s: string) => string): string {
  if (!d) return "—";
  const s = (d.bytes < 0 ? "−" : "+") + human(Math.abs(d.bytes));
  return d.atLeast ? atLeast(s) : s;
}

/** 按 7 天增长（没有可比数据的排在后面，再按占用）或按占用排序；返回新数组。 */
export function sortApps(apps: GrowthApp[], by: GrowthSort): GrowthApp[] {
  const bySize = (a: GrowthApp, b: GrowthApp) => b.size - a.size || a.name.localeCompare(b.name);
  if (by === "size") return [...apps].sort(bySize);
  return [...apps].sort((a, b) => {
    const wa = a.week?.bytes ?? Number.NEGATIVE_INFINITY;
    const wb = b.week?.bytes ?? Number.NEGATIVE_INFINITY;
    return wb - wa || bySize(a, b);
  });
}

/** 30 天迷你曲线的 SVG path：-1（那天没有记录）处断开；不到两个点返回 ""。 */
export function sparkPath(values: number[], w: number, h: number): string {
  const known = values.filter((v) => v >= 0);
  if (known.length < 2) return "";
  const lo = Math.min(...known);
  const span = Math.max(...known) - lo || 1;
  const step = values.length > 1 ? w / (values.length - 1) : 0;
  let d = "";
  let pen = false;
  values.forEach((v, i) => {
    if (v < 0) { pen = false; return; }
    d += `${pen ? "L" : "M"}${(i * step).toFixed(1)} ${(h - ((v - lo) / span) * h).toFixed(1)}`;
    pen = true;
  });
  return d;
}

/** 卸载能移走多少（freed，含 App 本身），这一行的数据里还剩多少（rest，spec §六）。 */
export function uninstallGap(rowSize: number, detail: UninstallDetail | null): { freed: number; rest: number } | null {
  if (!detail) return null;
  const data = detail.items.filter((it) => it.kind !== "app").reduce((n, it) => n + it.size, 0);
  return { freed: detail.totalSize, rest: Math.max(0, rowSize - data) };
}
```

- [ ] **Step 4: Write `api.ts`, `entry.ts` and `mount.tsx`**

`src/react/growth/api.ts`:

```ts
import { Cleaner, events } from "../../mygo";
import type { GrowthConfig, GrowthState, GrowthStatus, UninstallDetail } from "../../mygo";

/** 占用增长页用到的全部 Cleaner 调用与事件集中在此，组件不直接碰桥。 */
export const growthApi = {
  state: (): Promise<GrowthState> => Cleaner.growthState(),
  /** 开始量一次（监控关着也能用）；结果经 growthUpdated 事件通知。 */
  measureNow: (): Promise<void> => Cleaner.growthMeasureNow(),
  saveConfig: (c: GrowthConfig): Promise<void> => Cleaner.saveGrowthConfig(c),
  reveal: (path: string): Promise<void> => Cleaner.revealInFinder(path),
  uninstallDetail: (path: string): Promise<UninstallDetail> => Cleaner.appUninstallDetail(path),
  /** 量开始、结束或设置变了；返回取消订阅。 */
  onUpdated: (fn: (s: GrowthStatus) => void): (() => void) => events.growthUpdated.on(fn),
};
```

`src/react/growth/mount.tsx`:

```tsx
import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { GrowthPage, type GrowthHandlers } from "./GrowthPage";
import "../../ui/index.css";

let root: Root | null = null;
let visit = 0;
let focus: { key: string | null; nonce: number } = { key: null, nonce: 0 };

/** 通知 / 菜单栏要定位的那一行；下次渲染时展开并高亮。 */
export function requestGrowthFocus(key: string) {
  focus = { key, nonce: focus.nonce + 1 };
}

/** 在原生视图的容器里挂载占用增长页；root 复用，每次进入视图重新拉一次状态。 */
export function mountGrowth(el: HTMLElement, handlers: GrowthHandlers) {
  if (!root) root = createRoot(el);
  visit++;
  root.render(
    <React.StrictMode>
      <GrowthPage visit={visit} focusKey={focus.key} focusNonce={focus.nonce} {...handlers} />
    </React.StrictMode>,
  );
}
```

`src/react/growth/entry.ts`:

```ts
// React 占用增长页与原生导航的桥接：main.ts 调 showReactGrowth() / focusReactGrowth(key)，不直接接触 React。
import type { GrowthHandlers } from "./GrowthPage";
import { mountGrowth, requestGrowthFocus } from "./mount";

const handlers: GrowthHandlers = { goLeftovers: () => {}, goUninstall: () => {} };

/** main.ts 注入：跳到应用卸载的残留页签 / 打开某个 App 的卸载对话框。 */
export function setGrowthHandlers(h: GrowthHandlers) {
  Object.assign(handlers, h);
}

/** 每次切到占用增长视图都挂载 / 复用 React 根。 */
export function showReactGrowth() {
  const el = document.querySelector<HTMLElement>("#growth-react-root");
  if (el) mountGrowth(el, handlers);
}

/** 通知或菜单栏点进来：定位到这一行（调用方负责先切到本视图）。 */
export function focusReactGrowth(key: string) {
  requestGrowthFocus(key);
  showReactGrowth();
}
```

- [ ] **Step 5: Write `src/react/growth/GrowthPage.tsx`**

```tsx
import * as React from "react";
import { FolderOpen, RefreshCw } from "lucide-react";
import type { GrowthApp, GrowthConfig, GrowthState, UninstallDetail } from "../../mygo";
import { Badge } from "../../ui/components/badge";
import { Button } from "../../ui/components/button";
import { EmptyGuide } from "../../ui/components/empty-guide";
import { Collapse } from "../../ui/motion/Collapse";
import { useT } from "../../ui/i18n-react";
import { errText, human, toast } from "../../shared";
import { shortHome } from "../../platform";
import { growthApi } from "./api";
import { fmtDelta, growthBanner, growthView, sortApps, sparkPath, uninstallGap, type GrowthSort } from "./format";

/** main.ts 注入的跳转：残留页签、卸载对话框（都走现有流程，本页不删任何东西）。 */
export interface GrowthHandlers {
  goLeftovers: () => void;
  goUninstall: (appPath: string) => void;
}

export interface GrowthPageProps extends GrowthHandlers {
  /** 每次进入视图递增：重新拉状态。 */
  visit: number;
  /** 要展开并高亮的行；focusNonce 变了才执行。 */
  focusKey: string | null;
  focusNonce: number;
}

export function GrowthPage({ visit, focusKey, focusNonce, goLeftovers, goUninstall }: GrowthPageProps) {
  const t = useT();
  const [state, setState] = React.useState<GrowthState | null>(null);
  const [sort, setSort] = React.useState<GrowthSort>("week");
  const [open, setOpen] = React.useState<string | null>(null);
  const [details, setDetails] = React.useState<Record<string, UninstallDetail | null>>({});
  const [hl, setHl] = React.useState<string | null>(null);

  const load = React.useCallback(async () => {
    try { setState(await growthApi.state()); }
    catch (e) { toast(t("growth.loadFail", { msg: errText(e) }), "err"); }
  }, [t]);
  React.useEffect(() => { void load(); }, [load, visit]);
  React.useEffect(() => growthApi.onUpdated(() => { void load(); }), [load]);

  // 通知 / 菜单栏定位：展开该行、滚到中间、高亮 900ms（spec 动效例外同状态页）。
  React.useEffect(() => {
    if (!focusKey) return;
    setOpen(focusKey);
    setHl(focusKey);
    const raf = requestAnimationFrame(() =>
      document.querySelector(`[data-growth-key="${CSS.escape(focusKey)}"]`)?.scrollIntoView({ block: "center" }));
    const id = window.setTimeout(() => setHl(null), 900);
    return () => { cancelAnimationFrame(raf); window.clearTimeout(id); };
  }, [focusKey, focusNonce]);

  // 展开已安装 App 时取卸载明细，写「卸载能移走多少、另外多少不会」。
  React.useEffect(() => {
    const row = state?.apps.find((a) => a.key === open);
    if (!row?.appPath || row.key in details) return;
    growthApi.uninstallDetail(row.appPath).then(
      (d) => setDetails((m) => ({ ...m, [row.key]: d })),
      () => setDetails((m) => ({ ...m, [row.key]: null })),
    );
  }, [open, state, details]);

  const save = async (patch: Partial<GrowthConfig>) => {
    if (!state) return;
    try { await growthApi.saveConfig({ ...state.config, ...patch }); await load(); }
    catch (e) { toast(errText(e), "err"); }
  };
  const measure = async () => {
    try { await growthApi.measureNow(); await load(); }
    catch (e) { toast(errText(e), "err"); }
  };
  const reveal = (path: string) => growthApi.reveal(path).catch((e) => toast(t("fs.revealFail", { msg: errText(e) }), "err"));
  const toggleIgnore = (a: GrowthApp) => {
    if (!state) return;
    const ignored = a.ignored ? state.config.ignored.filter((k) => k !== a.key) : [...state.config.ignored, a.key];
    void save({ ignored });
  };

  const view = growthView(state);
  if (!state || view === "unsupported") return <div className="cz-react" />;
  const banner = growthBanner(state);
  const atLeast = (s: string) => t("growth.atLeast", { size: s });
  const fmtTime = (sec: number) => new Date(sec * 1000).toLocaleString();

  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h2>{t("nav.growth")}</h2>
          <p className="muted">{t("growth.sub")}</p>
        </div>
        <Button onClick={() => void measure()} disabled={state.status.running}>
          <RefreshCw size={14} className={state.status.running ? "spin" : undefined} />
          {state.status.running ? t("growth.measuring") : t("growth.measureNow")}
        </Button>
      </header>

      {banner === "optin" && (
        <div className="cz-growth-banner">
          <strong>{t("growth.optin.title")}</strong>
          <p className="muted">{t("growth.optin.body")}</p>
          <div className="cz-growth-actions">
            <Button onClick={() => void save({ enabled: true, asked: true })}>{t("growth.optin.enable")}</Button>
            <Button variant="ghost" onClick={() => void save({ asked: true })}>{t("growth.optin.later")}</Button>
          </div>
        </div>
      )}
      {banner === "off" && (
        <div className="cz-growth-banner cz-growth-banner-quiet">
          <span className="muted">{t("growth.off")}</span>
          <Button variant="ghost" size="sm" onClick={() => void save({ enabled: true })}>{t("growth.off.enable")}</Button>
        </div>
      )}

      <p className="cz-growth-status muted">
        {state.status.lastAt ? t("growth.status.last", { time: fmtTime(state.status.lastAt) }) : t("growth.status.never")}
        {state.config.enabled && state.status.nextAt > 0 && <> · {t("growth.status.next", { time: fmtTime(state.status.nextAt) })}</>}
      </p>
      {!state.status.fda && <p className="cz-growth-note muted">{t("growth.fda")}</p>}
      {state.config.enabled && state.status.notifications === "denied" && <p className="cz-growth-note muted">{t("growth.notifDenied")}</p>}

      {view === "empty" && (
        <EmptyGuide gradId="growth-empty" title={t("growth.empty.title")} desc={t("growth.empty.desc")}
          ctaText={t("growth.measureNow")} onCta={() => void measure()} />
      )}

      {(view === "baseline" || view === "list") && (
        <>
          {view === "baseline" && <p className="cz-growth-note muted">{t("growth.baseline")}</p>}
          <div className="cz-growth-toolbar">
            <select className="select" value={view === "baseline" ? "size" : sort} disabled={view === "baseline"}
              onChange={(e) => setSort(e.target.value as GrowthSort)} aria-label={t("growth.sort.week")}>
              <option value="week">{t("growth.sort.week")}</option>
              <option value="size">{t("growth.sort.size")}</option>
            </select>
          </div>
          <ul className="cz-growth-list">
            <li className="cz-growth-row cz-growth-head muted" aria-hidden>
              <span />
              <span>{t("growth.col.size")}</span>
              <span>{t("growth.col.day")}</span>
              <span>{t("growth.col.week")}</span>
              <span>{t("growth.col.month")}</span>
              <span />
            </li>
            {sortApps(state.apps, view === "baseline" ? "size" : sort).map((a) => {
              const isOpen = open === a.key;
              const gap = a.appPath ? uninstallGap(a.size, details[a.key] ?? null) : null;
              const spark = sparkPath(a.spark, 72, 18);
              return (
                <li key={a.key} data-growth-key={a.key} className={`cz-growth-item${hl === a.key ? " cz-growth-hl" : ""}`}>
                  <button type="button" className="cz-growth-row" aria-expanded={isOpen} onClick={() => setOpen(isOpen ? null : a.key)}>
                    <span className="cz-growth-name">
                      {a.name}
                      {a.kind === "macos" && <Badge>{t("growth.badge.macos")}</Badge>}
                      {a.kind === "unknown" && <Badge>{a.orphan ? t("growth.badge.orphan") : t("growth.badge.unknown")}</Badge>}
                      {a.ignored && <Badge>{t("growth.badge.ignored")}</Badge>}
                    </span>
                    <span className="num">{human(a.size)}{a.partial && " *"}</span>
                    <span className="num">{fmtDelta(a.day, atLeast)}</span>
                    <span className="num">{fmtDelta(a.week, atLeast)}</span>
                    <span className="num">{fmtDelta(a.month, atLeast)}</span>
                    <svg className="cz-growth-spark" viewBox="0 0 72 18" width="72" height="18" aria-hidden>
                      {spark && <path d={spark} />}
                    </svg>
                  </button>
                  <Collapse open={isOpen}>
                    <div className="cz-growth-detail">
                      {a.partial && <p className="muted">{t("growth.partial")}</p>}
                      <ul>
                        {a.folders.map((f) => (
                          <li key={f.path} className="cz-growth-folder">
                            <span className="cz-growth-path" title={f.path}>{shortHome(f.path)}</span>
                            <span className="num">{human(f.size)}{!f.complete && " *"}</span>
                            <span className="num">{fmtDelta(f.week, atLeast)}</span>
                            {f.denied && <span className="muted">{t("growth.denied")}</span>}
                            {f.outside && <span className="muted">{t("growth.outside")}</span>}
                            <Button variant="ghost" size="sm" onClick={() => void reveal(f.path)} aria-label={t("growth.reveal")}>
                              <FolderOpen size={14} />
                            </Button>
                          </li>
                        ))}
                      </ul>
                      {gap && <p className="muted">{t("growth.uninstallGap", { freed: human(gap.freed), rest: human(gap.rest) })}</p>}
                      <div className="cz-growth-actions">
                        {a.appPath && <Button variant="ghost" size="sm" onClick={() => goUninstall(a.appPath)}>{t("growth.uninstall")}</Button>}
                        {a.orphan && <Button variant="ghost" size="sm" onClick={goLeftovers}>{t("growth.goLeftovers")}</Button>}
                        <Button variant="ghost" size="sm" onClick={() => toggleIgnore(a)}>{a.ignored ? t("growth.unignore") : t("growth.ignore")}</Button>
                      </div>
                    </div>
                  </Collapse>
                </li>
              );
            })}
          </ul>
        </>
      )}
    </div>
  );
}
```

The props `size="sm"` and `variant="ghost"` exist on `Button` (`src/ui/components/button.tsx`). If the `Badge` default variant renders too loud, use the variant `InstalledTab.tsx` uses for its running badge.

- [ ] **Step 6: Wire the view, the nav item, the events and the strings**

**`index.html`.** Add the nav item after the `fullscan` button:

```html
          <button class="nav-item" data-view="growth" type="button">
            <svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 3v16a2 2 0 0 0 2 2h16"/><path d="m7 14 4-4 3 3 5-6"/></svg>
            <span data-i18n="nav.growth">占用增长</span>
          </button>
```

Then add the view after `view-fullscan`:

```html
        <!-- 占用增长（React 页面，内容由 React 挂载） -->
        <section id="view-growth" class="view hidden">
          <div id="growth-react-root"></div>
        </section>
```

**`src/shared.ts`.** `VIEWS` becomes `["clean", "status", "aitools", "skills", "fullscan", "growth", "large", "photos", "apps", "settings"]`.

**`src/main.ts`:**
1. Import `showReactGrowth`, `focusReactGrowth` and `setGrowthHandlers` from `./react/growth/entry`, `events` from `./mygo`, and `isWindows` from `./platform`.
2. In `switchView`, add `if (v === "growth") showReactGrowth();`.
3. After `setCrashGoClean(...)`:

```ts
setGrowthHandlers({
  goLeftovers: () => { switchView("apps"); showReactApps("leftovers"); },
  goUninstall: (appPath) => { switchView("apps"); requestAppsUninstall(appPath); },
});
// 通知 / 菜单栏点进来：切到占用增长并定位那一行。
if (window.mygo) events.growthFocus.on((key) => { switchView("growth"); focusReactGrowth(key); });
// 第一版只做 macOS：Windows 上隐藏入口。
if (isWindows()) document.querySelector<HTMLElement>('.nav-item[data-view="growth"]')?.classList.add("hidden");
```

`requestAppsUninstall` comes from Task 8. Until Task 8 lands, pass `goUninstall: () => switchView("apps")`.

**`src/i18n.ts`.** Add to `zh` and `en`:

| key | zh | en |
|---|---|---|
| `nav.growth` | 占用增长 | Storage Growth |
| `growth.sub` | 每天记一次各个 App 占了多少，涨得异常时提醒你 | Records what each app takes once a day and tells you when one grows fast |
| `growth.measureNow` | 现在量一次 | Measure Now |
| `growth.measuring` | 正在量… | Measuring… |
| `growth.loadFail` | 读取记录失败：{msg} | Couldn't read the records: {msg} |
| `growth.optin.title` | 在后台监控占用增长？ | Watch storage growth in the background? |
| `growth.optin.body` | 打开后，Cleanzo 每天在后台量一次各个 App 的数据，某个 App 涨得异常就发通知。它不会删除任何东西，随时可以在这里或设置里关掉。打开后关掉窗口，Cleanzo 会留在菜单栏。 | Cleanzo will measure each app's data once a day in the background and notify you when one grows fast. It never deletes anything, and you can turn it off here or in Settings at any time. While it's on, Cleanzo stays in the menu bar when you close the window. |
| `growth.optin.enable` | 打开后台监控 | Turn On |
| `growth.optin.later` | 先不用 | Not Now |
| `growth.off` | 后台监控已关闭：只在你点「现在量一次」时记录，也不发通知。 | Background monitoring is off: records are taken only when you click Measure Now, and no notifications are sent. |
| `growth.off.enable` | 打开 | Turn On |
| `growth.status.last` | 上次量的时间：{time} | Last measured: {time} |
| `growth.status.next` | 下次：{time} | Next: {time} |
| `growth.status.never` | 还没有量过 | Not measured yet |
| `growth.fda` | 没有完全磁盘访问权限，Containers 和 Group Containers 没有量。 | Without Full Disk Access, Containers and Group Containers aren't measured. |
| `growth.notifDenied` | 通知被关掉了，涨得异常时只会显示在这里。可以在系统设置 › 通知里打开。 | Notifications are off, so fast growth only shows here. Turn them on in System Settings › Notifications. |
| `growth.empty.title` | 还没有记录 | No records yet |
| `growth.empty.desc` | 量一次就能看到各个 App 占了多少，第二天起能看到增长。 | Measure once to see what each app takes; growth shows from the next day. |
| `growth.baseline` | 这是第一次记录，明天起能看到增长。 | This is the first record. Growth shows from tomorrow. |
| `growth.sort.week` | 按 7 天增长 | By 7-Day Growth |
| `growth.sort.size` | 按占用 | By Size |
| `growth.col.size` | 占用 | Size |
| `growth.col.day` | 今天 | Today |
| `growth.col.week` | 7 天 | 7 Days |
| `growth.col.month` | 30 天 | 30 Days |
| `growth.atLeast` | 至少 {size} | at least {size} |
| `growth.badge.macos` | macOS | macOS |
| `growth.badge.unknown` | 未找到对应的 App | No matching app |
| `growth.badge.orphan` | 可能是残留 | Possibly left over |
| `growth.badge.ignored` | 已忽略 | Ignored |
| `growth.partial` | 有的目录没量完，带 * 的数字是下限。 | Some folders weren't measured to the end; figures with * are lower bounds. |
| `growth.denied` | 部分无法读取 | Partly unreadable |
| `growth.outside` | 在主目录以外，Cleanzo 不会删除 | Outside your home folder; Cleanzo never removes it |
| `growth.reveal` | 在 Finder 中显示 | Show in Finder |
| `growth.uninstall` | 卸载 | Uninstall |
| `growth.uninstallGap` | 卸载能移走 {freed}；另有 {rest} 不会被移走，主目录里的卸载后大多会出现在「残留」页签。 | Uninstalling frees {freed}; {rest} stays behind. What stays in your home folder mostly shows up under Leftovers afterwards. |
| `growth.ignore` | 忽略 | Ignore |
| `growth.unignore` | 取消忽略 | Stop Ignoring |
| `growth.goLeftovers` | 去「残留」页签 | Open Leftovers |

**`src/style.css`.** Add the following. It uses the existing tokens `--card`, `--border` and `--bg-subtle`, and the motion tokens:

```css
.cz-growth-banner { display: grid; gap: 8px; padding: 14px 16px; margin: 0 0 12px; border-radius: 10px; background: var(--card); border: 1px solid var(--border); }
.cz-growth-banner-quiet { display: flex; align-items: center; justify-content: space-between; }
.cz-growth-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.cz-growth-status, .cz-growth-note { margin: 0 0 8px; font-size: 12px; }
.cz-growth-toolbar { display: flex; justify-content: flex-end; margin: 4px 0 8px; }
.cz-growth-list { list-style: none; margin: 0; padding: 0; }
.cz-growth-row { display: grid; grid-template-columns: minmax(0, 1fr) 84px 92px 92px 92px 72px; gap: 8px; align-items: center; width: 100%; padding: 8px 10px; text-align: left; background: none; border: 0; border-radius: 8px; }
.cz-growth-head { font-size: 12px; }
.cz-growth-item { transition: background-color var(--t-state) var(--e-out); border-radius: 8px; }
.cz-growth-item:hover, .cz-growth-hl { background: var(--bg-subtle); }
.cz-growth-name { display: flex; gap: 6px; align-items: center; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cz-growth-spark path { fill: none; stroke: currentColor; stroke-width: 1.5; opacity: 0.7; }
.cz-growth-detail { padding: 4px 10px 12px 18px; display: grid; gap: 8px; }
.cz-growth-detail ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 4px; }
.cz-growth-folder { display: grid; grid-template-columns: minmax(0, 1fr) 84px 92px auto auto; gap: 8px; align-items: center; font-size: 12px; }
.cz-growth-path { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
```

**`src/dev/mock-mygo.ts`.** Next to `let settings`, add two sample apps: ToDesk, with a `/Library` folder and a growing week, and 飞书 with `LarkShell`.

```ts
  // 占用增长：ToDesk（/Library 下的 dumps 在涨）和飞书（LarkShell）
  let growthConfig = { enabled: true, openAtLogin: false, dailyGB: 2, weeklyGB: 10, ignored: [] as string[], asked: true };
  const nowSec = () => Math.floor(Date.now() / 1000);
  const growthApps = () => [
    { key: "app:com.youqu.todesk.mac", name: "ToDesk", kind: "app", orphan: false, appPath: "/Applications/ToDesk.app",
      size: 152 * GB, partial: false, ignored: growthConfig.ignored.includes("app:com.youqu.todesk.mac"),
      day: { bytes: 5.6 * GB, atLeast: false, days: 1 }, week: { bytes: 38 * GB, atLeast: false, days: 7 }, month: { bytes: 140 * GB, atLeast: false, days: 30 },
      spark: Array.from({ length: 30 }, (_, i) => (i < 6 ? -1 : (i - 5) * 5.6 * GB)),
      folders: [
        { path: "/Library/Application Support/ToDesk/dumps", size: 150 * GB, complete: true, denied: false, outside: true, week: { bytes: 38 * GB, atLeast: false, days: 7 } },
        { path: "/Users/mock/Library/Caches/ToDesk", size: 2 * GB, complete: true, denied: false, outside: false, week: null },
      ] },
    { key: "app:com.electron.lark", name: "飞书", kind: "app", orphan: false, appPath: "/Applications/Lark.app",
      size: 27.8 * GB, partial: false, ignored: growthConfig.ignored.includes("app:com.electron.lark"),
      day: { bytes: 0.2 * GB, atLeast: false, days: 1 }, week: { bytes: 1.1 * GB, atLeast: false, days: 7 }, month: null,
      spark: Array.from({ length: 30 }, (_, i) => 26 * GB + i * 0.06 * GB),
      folders: [{ path: "/Users/mock/Library/Application Support/LarkShell", size: 25.1 * GB, complete: true, denied: false, outside: false, week: { bytes: GB, atLeast: false, days: 7 } }] },
  ];
```

Then add these handlers:

```ts
    "Cleaner.GrowthState": async () => ({
      supported: true, config: growthConfig, records: 5, apps: growthApps(),
      status: { running: false, lastAt: nowSec() - 3600, nextAt: nowSec() + 82800, fda: true, notifications: "allowed" },
      alerts: [{ key: "app:com.youqu.todesk.mac", name: "ToDesk", window: "day", days: 1, bytes: 5.6 * GB, atLeast: false,
        folder: "/Library/Application Support/ToDesk/dumps", total: 152 * GB, at: nowSec() - 3600 }],
    }),
    "Cleaner.GrowthMeasureNow": async () => {},
    "Cleaner.SaveGrowthConfig": async (c) => { growthConfig = structuredClone(c); },
```

The mock's `on` already returns a no-op unsubscribe, so `events.growthUpdated.on` works under `?mock`.

**`package.json`.** Append `&& bun test tests/growth-react.test.ts` to `test:web`.

- [ ] **Step 7: Run the frontend checks**

Run:
1. `bun test tests/growth-react.test.ts`
2. `bun run typecheck`
3. `bun run test:web`
4. `bun run build:web -- --outDir "$TMPDIR/cleanzo-growth-web"`

Expected: PASS.

- [ ] **Step 8: Leave the changes uncommitted for the reviewer**

---

### Task 8: Uninstall from the growth page through the apps page

**Files:**
- Create: `src/react/apps/requests.ts`. It holds the requests from other pages, with no React and no CSS, so tests can import it.
- Modify: `src/react/apps/mount.tsx`, `src/react/apps/AppsPage.tsx`, `src/react/apps/InstalledTab.tsx`, `src/react/apps/entry.ts`, `src/main.ts`
- Test: `tests/apps-react.test.ts` (add one case)

**Interfaces:**
- Produces: `requestAppsUninstall(path: string)`, exported from `src/react/apps/entry.ts`. It switches the apps page to 「已安装」 and opens the existing uninstall dialog for that app.
- `requests.ts` exports `requestAppsTab`, `requestAppsUninstall` and `appsRequest()`. `mount.tsx` re-exports `requestAppsTab` for its existing importers.

- [ ] **Step 1: Write the failing test**

Append to `tests/apps-react.test.ts`:

```ts
import { appsRequest, requestAppsTab, requestAppsUninstall } from "../src/react/apps/requests";

test("增长页请求卸载：切到已安装、记下要打开的 App，两个 nonce 都递增", () => {
  requestAppsTab("leftovers");
  const before = appsRequest();
  requestAppsUninstall("/Applications/Lark.app");
  const after = appsRequest();
  expect(after.tab).toBe("installed");
  expect(after.tabNonce).toBe(before.tabNonce + 1);
  expect(after.uninstallPath).toBe("/Applications/Lark.app");
  expect(after.uninstallNonce).toBe(before.uninstallNonce + 1);
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun test tests/apps-react.test.ts`
Expected: FAIL, because `../src/react/apps/requests` does not exist.

- [ ] **Step 3: Implement the request**

`src/react/apps/requests.ts`:

```ts
// 其他页面对应用卸载页的请求（垃圾页「残留」、占用增长页「卸载」）；不含 React，测试可直接引入。
import type { AppsTab } from "./AppsPage";

/** 最近一次请求：要切到的 tab、要打开卸载对话框的 App；两个 nonce 变了才执行。 */
export interface AppsRequest {
  tab: AppsTab;
  tabNonce: number;
  uninstallPath: string | null;
  uninstallNonce: number;
}

let req: AppsRequest = { tab: "installed", tabNonce: 0, uninstallPath: null, uninstallNonce: 0 };

/** 外部入口请求切到指定 tab。 */
export function requestAppsTab(tab: AppsTab) {
  req = { ...req, tab, tabNonce: req.tabNonce + 1 };
}

/** 占用增长页「卸载」：切到已安装，并打开该 App 的卸载对话框（走现有流程）。 */
export function requestAppsUninstall(path: string) {
  req = { ...req, tab: "installed", tabNonce: req.tabNonce + 1, uninstallPath: path, uninstallNonce: req.uninstallNonce + 1 };
}

/** 当前请求，mountApps 渲染时读取。 */
export function appsRequest(): AppsRequest {
  return req;
}
```

In `src/react/apps/mount.tsx`:
- Drop `pendingTab`, `nonce` and the local `requestAppsTab`.
- Add `export { requestAppsTab } from "./requests";`.
- Render `<AppsPage initialTab={r.tab} nonce={r.tabNonce} uninstallPath={r.uninstallPath} uninstallNonce={r.uninstallNonce} />` with `const r = appsRequest();`.

`AppsPage` forwards `uninstallPath` and `uninstallNonce` to `InstalledTab`.

In `InstalledTab`, add the following after `openDetail` is defined:

```tsx
  // 占用增长页请求卸载某个 App：nonce 变了就打开它的卸载对话框（与点行内「卸载」同一路径）。
  const firstUninstall = React.useRef(uninstallNonce);
  React.useEffect(() => {
    if (uninstallNonce !== firstUninstall.current && uninstallPath) {
      firstUninstall.current = uninstallNonce;
      void openDetail(uninstallPath);
    }
  }, [uninstallNonce, uninstallPath, openDetail]);
```

`src/react/apps/entry.ts` re-exports `requestAppsUninstall` after making sure the root is mounted:

```ts
import { mountApps, requestAppsTab } from "./mount";
import { requestAppsUninstall as request } from "./requests";

/** 占用增长页「卸载」：请求后挂载 / 复用根，InstalledTab 据 nonce 打开对话框。 */
export function requestAppsUninstall(path: string) {
  request(path);
  const el = document.querySelector<HTMLElement>("#apps-react-root");
  if (el) mountApps(el);
}
```

In `src/main.ts`, `goUninstall` becomes `(appPath) => { switchView("apps"); requestAppsUninstall(appPath); }`.

- [ ] **Step 4: Run the checks**

Run: `bun run typecheck && bun run test:web && bun run build:web -- --outDir "$TMPDIR/cleanzo-growth-web"`
Expected: PASS.

- [ ] **Step 5: Leave the changes uncommitted for the reviewer**

---

### Task 9: Settings section and the status page alert

**Files:**
- Create: `src/react/growth/hint.ts`
- Modify:
  - `src/react/settings/SettingsPage.tsx` and `src/react/settings/api.ts`;
  - `src/react/status/StatusPage.tsx`, `src/react/status/entry.ts` and `src/react/status/mount.tsx`;
  - `src/main.ts`, `src/shared.ts` (`errText` codes) and `src/i18n.ts`.
- Test: `tests/growth-react.test.ts` (add cases)

**Interfaces:**
- Produces: `growthHint(s: GrowthState | null, crashApps: string[]): GrowthAlert | null` and `setGoGrowthHandler(fn: () => void)` (status entry).

- [ ] **Step 1: Write the failing tests**

Append to `tests/growth-react.test.ts`:

```ts
import { growthHint } from "../src/react/growth/hint";

test("状态页提示：监控开着时取最大的一条；崩溃转储提示已覆盖的同一 App 不重复", () => {
  const alert = (name: string, bytes: number, folder: string) =>
    ({ key: "app:" + name, name, window: "day", days: 1, bytes, atLeast: false, folder, total: bytes, at: 1 });
  const on = { ...state({}).config, enabled: true };
  const s = state({ config: on, alerts: [alert("ToDesk", 5 * GB, "/Library/Application Support/ToDesk/dumps"),
    alert("Codex", 40 * GB, "/Users/u/Library/Application Support/Codex/Crashpad")] });
  expect(growthHint(s, [])?.name).toBe("Codex");
  expect(growthHint(s, ["Codex"])?.name).toBe("ToDesk");
  expect(growthHint(state({ alerts: s.alerts }), [])).toBeNull(); // 监控关着
  expect(growthHint(null, [])).toBeNull();
});
```

- [ ] **Step 2: Run them to verify they fail**

Run: `bun test tests/growth-react.test.ts`
Expected: FAIL, because `hint` is not found.

- [ ] **Step 3: Write `src/react/growth/hint.ts`**

```ts
// 状态页的增长提示：监控开着、近 7 天有提醒时显示最大的一条（spec §六）。
import type { GrowthAlert, GrowthState } from "../../mygo";

/** 取最大的一条；同一 App 涨在 Crashpad 里、崩溃转储提示已经在显示它时跳过，不重复提示。 */
export function growthHint(s: GrowthState | null, crashApps: string[]): GrowthAlert | null {
  if (!s || !s.supported || !s.config.enabled) return null;
  const crashed = new Set(crashApps.map((a) => a.toLowerCase()));
  return [...s.alerts]
    .sort((a, b) => b.bytes - a.bytes)
    .find((a) => !(crashed.has(a.name.toLowerCase()) && /[\\/]Crashpad([\\/]|$)/i.test(a.folder))) ?? null;
}
```

- [ ] **Step 4: Add the status page alert**

- `src/react/status/entry.ts`:
  - add a `goGrowth` holder and `setGoGrowthHandler(fn)`, exactly like `goClean`;
  - pass it through `mountStatus` to `StatusPage` as `goGrowth`.
- `StatusPage`:
  - next to `crashSum`, load `growthApi.state()` once when `active` turns true, the same way as `crashDumps()`. A failure leaves it null.
  - compute `const growthTop = growthHint(growthState, bigDumps.map((d) => d.app));`
  - add `...(growthTop ? [{ key: "growth", a: null }] : [])` to `alertItems`;
  - render it like the crash-dump alert:
    - the text is `t(growthTop.window === "day" ? "status.alert.growthDay" : "status.alert.growthWeek", { app: growthTop.name, days: growthTop.days, size: fmtDelta-like text })`. Use `human(growthTop.bytes)`, wrapped in `t("growth.atLeast", …)` when `atLeast`;
    - the button is `t("status.alert.viewGrowth")`, calling `goGrowth`.
- `src/main.ts`: add `setGoGrowthHandler(() => switchView("growth"));`.

New strings:

| key | zh | en |
|---|---|---|
| `status.alert.growthDay` | {app} 一天涨了 {size} | {app} grew {size} in a day |
| `status.alert.growthWeek` | {app} {days} 天涨了 {size} | {app} grew {size} in {days} days |
| `status.alert.viewGrowth` | 查看 | View |

- [ ] **Step 5: Add the settings section**

- `src/react/settings/api.ts`: add `growthState: () => Cleaner.growthState()` and `saveGrowthConfig: (c: GrowthConfig) => Cleaner.saveGrowthConfig(c)`.
- `SettingsPage`: insert a section after the junk-clean section, hidden when `isWindows()` or when `state.supported` is false:

```tsx
      {growth && (
        <section className="settings-section">
          <h3>{t("settings.growth")}</h3>
          <div className="setting-block card">
            <div className="setting-row setting-stack">
              <label className="check">
                <Checkbox checked={growth.enabled} disabled={growthBusy} onCheckedChange={(v) => void saveGrowth({ enabled: v === true, asked: true })} />
                <span>{t("settings.growth.enable")}</span>
              </label>
              <p className="muted small">{t("settings.growth.enableNote")}</p>
              <label className="check">
                <Checkbox checked={growth.openAtLogin} disabled={growthBusy || !growth.enabled} onCheckedChange={(v) => void saveGrowth({ openAtLogin: v === true })} />
                <span>{t("settings.growth.login")}</span>
              </label>
            </div>
            <div className="setting-row">
              <label className="setting-label" htmlFor="growth-daily">{t("settings.growth.daily")}</label>
              <select id="growth-daily" className="select" value={growth.dailyGB} disabled={growthBusy}
                onChange={(e) => void saveGrowth({ dailyGB: Number(e.target.value) })}>
                {[1, 2, 5, 10].map((n) => <option key={n} value={n}>{n} GB</option>)}
              </select>
            </div>
            <div className="setting-row">
              <label className="setting-label" htmlFor="growth-weekly">{t("settings.growth.weekly")}</label>
              <select id="growth-weekly" className="select" value={growth.weeklyGB} disabled={growthBusy}
                onChange={(e) => void saveGrowth({ weeklyGB: Number(e.target.value) })}>
                {[5, 10, 20, 50].map((n) => <option key={n} value={n}>{n} GB</option>)}
              </select>
            </div>
          </div>
        </section>
      )}
```

The markup follows the general and junk sections (`setting-block card`, `setting-row`, `label.check`, `select.select`). `growth` is the `GrowthConfig | null` state, loaded with `settingsApi.growthState()` when the page shows. It stays null on Windows, where `supported` is false, and on failure.

`saveGrowth(patch)` does the following:
- sets `growthBusy`;
- saves `{ ...growth, ...patch }` and reloads on success;
- on error, calls `toast(errText(e), "err")` and keeps the old values.

- `src/shared.ts` `errText`: add `"growth-login": "settings.growth.loginFail"` and `"growth-threshold": "settings.growth.thresholdBad"` to the code map. `growth-login` passes `args[0]` as `name`.

New strings:

| key | zh | en |
|---|---|---|
| `settings.growth` | 占用增长 | Storage Growth |
| `settings.growth.enable` | 在后台监控占用增长 | Watch storage growth in the background |
| `settings.growth.enableNote` | 每天量一次，涨得异常时发通知；关掉窗口后留在菜单栏。 | Measures once a day and notifies on fast growth; stays in the menu bar when the window closes. |
| `settings.growth.login` | 开机时启动（只显示菜单栏图标） | Open at login (menu bar icon only) |
| `settings.growth.daily` | 一天涨超过 | Alert when a day's growth exceeds |
| `settings.growth.weekly` | 7 天涨超过 | Alert when 7 days' growth exceeds |
| `settings.growth.loginFail` | 无法设置开机启动：{name} | Couldn't set Open at Login: {name} |
| `settings.growth.thresholdBad` | 提醒阈值无效 | Invalid alert threshold |

- [ ] **Step 6: Run the checks**

Run: `bun run typecheck && bun run test:web && bun run build:web -- --outDir "$TMPDIR/cleanzo-growth-web"`
Expected: PASS.

- [ ] **Step 7: Leave the changes uncommitted for the reviewer**

---

### Task 10: Smoke tour, README and CHANGELOG

**Files:**
- Modify: `smoke.go` (and any smoke test that lists the views), `README.md`, `CHANGELOG.md`

- [ ] **Step 1: Add the view to the smoke tour**

In `smoke.go`, `smokeViews` becomes `[]string{"status", "clean", "aitools", "skills", "fullscan", "growth", "large", "photos", "apps", "settings"}`. Update `smoke_test.go` or `tests/smoke/*` if they assert the list.

- [ ] **Step 2: README**

In 「功能」, after the 空间分析 bullet, add:

```markdown
- 占用增长（macOS）：把每个 App 散在 `~/Library`、主目录下的点目录和 `/Library` 里的数据加起来，每天记一次，按今天、7 天、30 天的增长排序；某个 App 一天涨超过 2 GB 或 7 天涨超过 10 GB 时发通知，写出是哪个目录（阈值可在设置里改）。打开后台监控后，关掉窗口 Cleanzo 会留在菜单栏，也可以开机启动；只量不删，主目录以外的目录只在 Finder 里显示，卸载走应用卸载页现有的流程。没有完全磁盘访问权限时不量 Containers 和 Group Containers
```

- [ ] **Step 3: CHANGELOG**

Add at the top. The release commit renames the heading to the version.

```markdown
## 未发布

- 新增「占用增长」（macOS）：每天记一次各个 App 的数据占了多少（`~/Library` 各处、主目录下的点目录和 `/Library` 合在一起算），列出今天、7 天、30 天涨了多少，展开能看到是哪个目录在涨、30 天的曲线，可以在 Finder 里显示
- 某个 App 一天涨超过 2 GB、或 7 天涨超过 10 GB 时发系统通知，写出 App 和涨得最多的目录（比如 ToDesk 的 dumps）；同一个 App 一天最多一条，所有 App 加起来一天最多三条。阈值和要忽略的 App 可以在设置和页面里改
- 后台监控默认关闭，第一次打开页面时询问；打开后关掉窗口 Cleanzo 留在菜单栏，菜单里有近 7 天涨得最多的 App、现在量一次、关闭后台监控；可选开机启动（只显示菜单栏图标）
- 只量不删：主目录以外的目录只显示位置，「卸载」打开应用卸载页现有的对话框，并写明卸载移不走的部分；量的时候用后台优先级，你在扫描或清理时自动让开
```

- [ ] **Step 4: Run every check**

Run:
1. `gofmt -l .`
2. `go vet ./...`
3. `go test -race -count=1 ./...`
4. `GOOS=windows GOARCH=amd64 go vet ./...`
5. `bun run generate`; the bindings must not change.
6. `bun run typecheck`
7. `bun run test:web`
8. `bun run build:web -- --outDir "$TMPDIR/cleanzo-growth-web"`

Expected: all green, no gofmt output.

- [ ] **Step 5: Leave the changes uncommitted for the reviewer**

---

## Reviewer verification (on the maintainer's Mac, after Run D)

The executor cannot do these; the reviewer runs them before the release.

1. **Real measurement.**
   - `bun run dev`, then on the page click 现在量一次 with monitoring off.
   - The run finishes within 15 minutes, and the UI stays responsive meanwhile.
   - The page lists 飞书 (with LarkShell), 微信, Trae CN and Chrome (from `Google/Chrome`) near the top, matching the spec's appendix.
2. **No prompts.** With Cleanzo removed from Full Disk Access, a run raises no system dialog, and the page shows the FDA note.
3. **Growth and notification.**
   - Fake a day: copy the newest record in `~/Library/Application Support/Cleanzo/growth/records/`, renaming it and its `at` to 24 h earlier.
   - Grow a test folder in `~/Library/Application Support/GrowthTest` by 3 GB with `mkfile 3g`.
   - Turn monitoring on and measure. One notification names GrowthTest and the folder.
   - Clicking it opens the page at that row.
   - Measuring again the same day posts nothing.
   - Remove the test folder afterwards.
4. **Lifecycle:**
   - With monitoring on, closing the window keeps the menu bar icon and the Dock icon goes away.
   - The menu's 打开 Cleanzo brings both back.
   - 关闭后台监控 with no window quits the app.
   - 退出 Cleanzo quits.
   - With monitoring off, closing the window quits, exactly like 0.2.5.
5. **Login item.**
   - Turn on 开机时启动. The item appears in System Settings › General › Login Items.
   - Log out and back in: only the menu bar icon appears, with no window.
   - Turn it off: the item disappears.
6. **User scans win.** Start 现在量一次 from the menu bar, then a junk scan on the page. The manual run keeps going. A background run (force one by setting `nextAt` due) stops, and no record is saved.
7. **Smoke tour** with a seeded HOME, as in `docs/RELEASING.md`: the new `growth` screenshots in light and dark have no page errors.
8. **Windows CI** stays green, and the growth nav item is hidden there.
9. **飞书国内版** (spec §三):
   - Download the 飞书 DMG and read its `Info.plist` without running it. Note `CFBundleIdentifier`, `CFBundleName`, `CFBundleDisplayName` and the bundle's folder name.
   - If none of them is `lark`, `feishu` or `飞书` lower-cased, add the missing name to `growthAliases["larkshell"]`.
   - Check whether `installedKeys().owns("LarkShell")` would be false for that build. If so, the 「残留」 tab would list 飞书's data as a leftover: raise it with the maintainer as a separate fix.

## Execution batches (Codex runs)

| Run | Tasks | Done when |
|---|---|---|
| A | 1–4 | Go core compiles on macOS and Windows; attribution, targets, measuring, store and comparison tests pass |
| B | 5–6 | Bindings regenerated; monitor, texts, menu model and icon tests pass; `main()` still opens the window as before with monitoring off |
| C | 7–9 | Page, settings section and status alert work in `bun run dev:web` with `?mock`; frontend checks green |
| D | 10 | Smoke list, README and CHANGELOG updated; every check green; the reviewer runs "Reviewer verification" |

Each run ends with the executor's output of the checks listed in Global Constraints. The reviewer then reviews the diff against the spec and this plan and runs the relevant verification items. The reviewer commits each task separately on `feat/growth-monitor`.

## Implementation notes (2026-10-26)

Where the shipped code differs from the code in this plan. Build from the code on `feat/growth-monitor`, not from the blocks above.

- **Background threads** (`growth_qos_darwin.go`, 6110c14): `backgroundThread` puts the thread in Darwin's background state with one `syscall.Setpriority(PRIO_DARWIN_THREAD, 0, PRIO_DARWIN_BG)`, its error checked. The plan's `pthread_set_qos_class_self_np` + `setiopolicy_np` pair does not hold: the iopolicy call resets the QoS to unspecified, and the reverse order makes the QoS call fail, so only the disk throttle was ever in force.
- **Vanished files** (`measureGrowthEntry`, 6110c14): an item removed during the walk (`fs.ErrNotExist`) is skipped without setting Denied.
- **Record files** (`growth_store.go`, 6709bb4): `growthRecordAt` is the one definition of a record (a regular `<digits>.json` with a positive time), used by save, prune, load and `lastGrowthAt`; `writeGrowthFile` writes through its own `os.CreateTemp` file and removes it when a write fails.
- **Where it grew** (`growthTopFolder`, a6e5ebb): a recorded child is named only when it grew; otherwise the entry itself.
- **Schedule** (`growthNextRun`, `nextRun`, 64a77c1): the 20-hour staleness is judged at launch, so a running app measures every 24 hours; records dated after now do not count; automatic attempts keep an hour apart (`lastAttempt`), turning monitoring on excepted. A measurement held back by the battery or a user scan keeps the time it fell due instead of moving it on (5a6abd4).
- **Seams set in `init`** (64a77c1, d0dbcf3): `growthChanged` and `growthApply` get their defaults in `init()`, since their initializers would make an initialization cycle through the menu bar's menu.
- **Patch-based saves** (bbf0880): `SaveGrowthConfig` takes a `GrowthConfigPatch` (only the set fields, plus `ignore` / `unignore` of one owner key) and merges it into the current config under `growthConfigMu`; pages send only what changed.
- **GrowthSummary** (5a6abd4): a light binding with the config, status and recent alerts, used by the status page and Settings. `GrowthState` builds the attributor only when there is a record, keeps its rows until the records, the ignored owners or the attributor change, and reads each Info.plist once for the app keys and the Uninstall paths.
- **Owners** (830b3f0): an app-index hit on a `com.apple.*` bundle is a `macos:` owner with the app's name and no bundle; `appPath` is set only for a bundle directly in /Applications or ~/Applications (`installedApp`), the only ones the uninstall flow takes.
- **Apps page requests** (`src/react/apps/requests.ts`, eec38fb): the tab and uninstall requests live in a module with nonces; the apps page's handled-uninstall marker starts at 0, so a request made before the page first mounts still opens the dialog.
- **Settings follow the monitor** (bbf0880): the Settings section reloads on `growthUpdated`; a reload that arrives during a save runs after it, and a failed save is read back too, since a login item error leaves the settings saved and applied.
- **Menu bar off the main thread** (e403814, d0dbcf3): the menu's "turn off" works on a goroutine, because it takes `growthConfigMu`, which `SaveGrowthConfig` holds while it waits for the main thread; the icon starts with the lines that act, and `refreshGrowthTray` fills in the rest from another goroutine. The tray is an `atomic.Pointer`.
- **Login item from the system** (d0dbcf3, bbf0880): the login item is not applied at launch; the pages show `App.OpenAtLogin()` when the app runs, and a save that leaves the switch alone keeps what System Settings has.
- **Notifications** (7505f5d): a run posts its picks on a goroutine of its own after saving the log, so it never waits for the permission prompt; turning monitoring on posts one "monitoring is on" notification so macOS asks while the user is there. Turning monitoring off stops a background run; a login launch whose icon cannot be made opens the window.
- **Unreadable roots and future records** (830b3f0, 5a6abd4): a root or vendor folder that cannot be listed marks the record Cut; records dated after now are left out of a run's history and of the page.
