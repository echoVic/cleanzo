# Junk Rule Table, Sidebar and Settings Rework — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. In this project the tasks are executed by the Codex CLI, several tasks per run; **executors do not commit** — the reviewer commits each task after checking it.

**Goal:** Replace the nine fixed junk categories with a rule table that shows only what exists on this Mac (per-app caches, browser caches, update leftovers, developer caches, items to review, custom folders), regroup the sidebar, and rebuild the settings page — for release 0.1.3.

**Architecture:** A Go rule catalog (`rules.go`) is expanded against the machine into items (`junk.go`), with per-app items built from three cache sources and an installed-app index (`owners.go`); tools clean what they own (`tools.go`); settings v3 replace the category config (`settings.go`). Two streamed methods, `ScanJunk` and `CleanJunk`, replace `ScanStream`/`CleanStream`. The frontend splits shared helpers out of `src/main.ts` and gets a new junk page module and settings module.

**Tech Stack:** Go 1.27 (package `main`, mygo 0.3.6 bindings), TypeScript 7 + Vite 8, macOS tools (`lsof`, `lsappinfo`, `plutil`, `sips`, `du`, `brew`, `pnpm`, `go`, `xcrun simctl`).

**Spec:** `docs/superpowers/specs/2026-10-11-junk-rules-ia-settings-design.md` (read it first; section numbers below refer to it).

## Global Constraints

- macOS only. No new Go or npm dependencies.
- File operations only ever touch the home folder and the user temporary folder; every removal still passes `removable`/`safeToClean`. The only exception is `brew cleanup`, run by Homebrew itself.
- Existing guards stay in force: the usage guard (`lsof` + running bundle IDs, fails closed), `removalRefused`, special files skipped, the 7-day rule for `$TMPDIR`, the Trash cleaned first and only of what was there at `scannedAt`.
- Never edit `src/mygo.ts`; regenerate it with `bun run generate` after changing bound methods or their types.
- Every user-visible string goes through `t()` with both a `zh` and an `en` entry in `src/i18n.ts`; backend errors shown to users are codes (`in-use`, `blocked`, `gone`, `not-app`, or `codeError` JSON), translated by `errText`.
- Go: a short plain doc comment on every declaration; match the surrounding naming and idioms. TypeScript: terse Chinese comments like the existing code.
- After each task: `gofmt -l .` prints nothing, `go vet ./...` passes, `go test -race -count=1 ./...` passes, `bun run typecheck` and `bun run build:web` pass.
- Inside the Codex sandbox, `TestLargePhotosAreDecodedSmall` and `TestUsageGuardSeesSockets` fail for environmental reasons (sips temp files, unix-socket bind); the reviewer runs the full suite outside.

## Review Focus

1. **Ignoring or disabling must not move junk into another row.** If the user ignores `browser.chrome` or turns off the browsers group, Chrome's cache must not reappear inside the generic "Google" app item and get cleaned. Ignored/disabled rules still claim their paths; they are only not emitted. Test in Task 3.
2. **A selection can never reach outside its item.** `CleanJunk` must refuse sub-item paths that are not inside the item's targets as expanded now (stale UI, renamed folders, tampering), and touch nothing else. Test in Task 5.
3. **No Full Disk Access means no reads inside other apps' containers.** Not even a stat of `Library/Containers/<id>/Data`; only the container names are listed for the banner count. Test in Task 3.
4. **A tool that is missing, busy, failing or hanging must not stall or break the clean.** The item reports why it was skipped, the other items still run, and the command is killed at its timeout or when the user stops. Test in Task 6.
5. **An app that starts between scan and clean is left alone.** Each item gets a fresh usage probe right before it is cleaned; if its owner now runs, nothing is removed. Test in Task 5.

---

## File Structure

| File | Responsibility |
|---|---|
| `settings.go` (new) | Settings v3 types, load + migration from category config v1/v2, validation, atomic save, `GetSettings`, `SaveSettings`, `PickFolder`, `FullDiskAccess` |
| `owners.go` (new) | Installed-app index: bundle IDs and names → app; lookup used to name and group cache folders |
| `rules.go` (new) | Rule model and the ordered rule catalog; protected patterns; per-app defaults |
| `junk.go` (new) | Expansion of rules into items (claims, generic app sources, custom folders), measuring (`planItem`, sub-items), `ScanJunk`, `CleanJunk` |
| `tools.go` (new) | Finding tools outside `PATH`, busy checks, running tool cleanups with timeouts, size estimates |
| `main.go` | Keeps shared helpers; loses the category model and old junk APIs in Task 12 |
| `settings_test.go`, `owners_test.go`, `junk_test.go`, `tools_test.go` (new) | Tests per file; helpers from `main_test.go` (`writeFile`, `mustMkdir`, `exists`, `makeApp`) are reused |
| `src/shared.ts` (new) | Helpers used by several views, moved out of `src/main.ts` |
| `src/junk.ts` (new) | The junk cleaning page |
| `src/settings.ts` (new) | The settings page |
| `src/main.ts` | Navigation, Space pages, Photos, Apps (with the Leftovers tab) |
| `src/i18n.ts`, `src/style.css`, `index.html` | Strings, styles, markup for the above |

---

### Task 1: Settings v3 and migration

**Files:**
- Create: `settings.go`, `settings_test.go`
- Modify: none (old category config code stays until Task 12; `configPath()` is reused)

**Interfaces:**
- Produces:
  ```go
  type Settings struct {
      DisabledGroups []string       `json:"disabledGroups"`
      Ignored        []string       `json:"ignored"`
      Custom         []CustomFolder `json:"custom"`
  }
  type CustomFolder struct {
      Name    string `json:"name"`
      Path    string `json:"path"`
      Enabled bool   `json:"enabled"`
  }
  func loadSettings() Settings
  func migrateCategories(cfg []CategoryConfig) Settings
  func validateSettings(s Settings) (Settings, error)
  func writeSettings(s Settings) error
  func fullDiskAccess() bool
  func (Cleaner) GetSettings() Settings
  func (Cleaner) SaveSettings(s Settings) error
  func (Cleaner) PickFolder(ctx context.Context) (string, error)
  func (Cleaner) FullDiskAccess() bool
  ```
- Group names accepted in `DisabledGroups`: `system`, `apps`, `browsers`, `updates`, `dev`, `review` (custom folders are switched one by one).

- [ ] **Step 1: Write the failing tests** (`settings_test.go`)

```go
func TestSettingsMigrateFromCategories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	builtins := defaultCategoryConfigs()
	var cfg []CategoryConfig
	for _, c := range builtins {
		switch c.Name {
		case "trash", "npm", "caches":
			c.Enabled = false // the user had turned these off
		case "logs":
			c.Paths = []string{filepath.Join(home, "logs-elsewhere")} // edited paths: a custom folder now
		}
		cfg = append(cfg, c)
	}
	cfg = append(cfg, CategoryConfig{Name: "旧安装包", Paths: []string{filepath.Join(home, "Downloads", "installers"), filepath.Join(home, "old")}, Enabled: true})
	s := migrateCategories(cfg)
	if !slices.Contains(s.Ignored, "sys.trash") || !slices.Contains(s.Ignored, "dev.npm") || !slices.Contains(s.Ignored, "dev.npx") {
		t.Fatalf("disabled built-ins should become ignored rules, got %v", s.Ignored)
	}
	if !slices.Contains(s.DisabledGroups, "apps") {
		t.Fatalf("disabled caches should turn the apps group off, got %v", s.DisabledGroups)
	}
	want := []CustomFolder{
		{Name: "logs", Path: filepath.Join(home, "logs-elsewhere"), Enabled: true},
		{Name: "旧安装包", Path: filepath.Join(home, "Downloads", "installers"), Enabled: true},
		{Name: "旧安装包", Path: filepath.Join(home, "old"), Enabled: true},
	}
	if !slices.Equal(s.Custom, want) {
		t.Fatalf("custom folders = %+v, want %+v", s.Custom, want)
	}
}

func TestSettingsLoadReadsV2AndWritesV3(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Dir(configPath()))
	v2 := `{"version":2,"categories":[{"name":"mine","description":"","risk":"safe","paths":["` + filepath.Join(home, "mine") + `"],"enabled":true}]}`
	if err := os.WriteFile(configPath(), []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	s := loadSettings()
	if len(s.Custom) != 1 || s.Custom[0].Name != "mine" {
		t.Fatalf("v2 custom category not migrated: %+v", s)
	}
	if err := (Cleaner{}).SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(configPath())
	if !strings.Contains(string(data), `"version": 3`) {
		t.Fatalf("saved config is not v3: %s", data)
	}
	if got := loadSettings(); !slices.Equal(got.Custom, s.Custom) {
		t.Fatalf("round trip changed custom folders: %+v", got.Custom)
	}
}

func TestSettingsValidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Join(home, "a"))
	cases := []struct {
		s    Settings
		code string
	}{
		{Settings{Custom: []CustomFolder{{Name: "x", Path: " "}}}, "folder-empty"},
		{Settings{Custom: []CustomFolder{{Name: "x", Path: filepath.Join(home, "Library"), Enabled: true}}}, "folder-protected"},
		{Settings{Custom: []CustomFolder{{Name: "x", Path: filepath.Join(home, "a")}, {Name: "y", Path: filepath.Join(home, "a") + "/"}}}, "folder-dup"},
		{Settings{DisabledGroups: []string{"nope"}}, "group-unknown"},
	}
	for _, c := range cases {
		_, err := validateSettings(c.s)
		var ce codeError
		if !errors.As(err, &ce) || ce.Code != c.code {
			t.Errorf("validateSettings(%+v) = %v, want code %s", c.s, err, c.code)
		}
	}
	got, err := validateSettings(Settings{Ignored: []string{"dev.npm", "dev.npm"}, Custom: []CustomFolder{{Path: filepath.Join(home, "a") + "/", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Ignored) != 1 || got.Custom[0].Path != filepath.Join(home, "a") || got.Custom[0].Name != "a" {
		t.Fatalf("validateSettings should dedupe, clean paths and name folders: %+v", got)
	}
}

func TestFullDiskAccessProbe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if fullDiskAccess() {
		t.Fatal("nothing readable: no access")
	}
	mustMkdir(t, filepath.Join(home, "Library", "Safari"))
	if !fullDiskAccess() {
		t.Fatal("a readable protected folder means access")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -count=1 -run 'TestSettings|TestFullDiskAccessProbe' ./...`
Expected: build failure — `undefined: migrateCategories` (and the others).

- [ ] **Step 3: Implement `settings.go`**

```go
// settingsVersion is the version of config.json since the rule table:
// version 1 was a bare array of categories, version 2 wrapped it.
const settingsVersion = 3

// junkGroupNames are the groups a user can turn off.
var junkGroupNames = []string{"system", "apps", "browsers", "updates", "dev", "review"}

// builtinRuleIDs maps a built-in category of config v1/v2 to the rules that
// took it over, for users who had turned it off.
var builtinRuleIDs = map[string][]string{
	"logs": {"sys.logs"}, "temp": {"sys.temp"}, "trash": {"sys.trash"},
	"npm": {"dev.npm", "dev.npx"}, "go": {"dev.go-build"}, "pip": {"dev.pip"},
	"homebrew": {"dev.homebrew"}, "xcode": {"dev.xcode"},
}

// migrateCategories turns the category config of v1/v2 into settings:
// built-ins at their built-in paths give way to the rule table (turned-off
// ones are ignored, and turned-off "caches" turns the apps group off);
// edited built-ins and the user's own categories become custom folders.
func migrateCategories(cfg []CategoryConfig) Settings {
	s := Settings{}
	for _, c := range cfg {
		if isBuiltin(c) {
			if !c.Enabled {
				if c.Name == "caches" {
					s.DisabledGroups = append(s.DisabledGroups, "apps")
				}
				s.Ignored = append(s.Ignored, builtinRuleIDs[c.Name]...)
			}
			continue
		}
		for _, p := range c.Paths {
			if strings.TrimSpace(p) != "" {
				s.Custom = append(s.Custom, CustomFolder{Name: c.Name, Path: p, Enabled: c.Enabled})
			}
		}
	}
	return s
}
```

`loadSettings`: read `configPath()`; if missing → `Settings{}`; if it unmarshals as `{"version":3,...}` use it; else fall back to `savedCategoryConfigs()` (which already handles v1 and v2) and `migrateCategories`. Return `Settings{}` on unreadable JSON (as `savedCategoryConfigs` does today).

`validateSettings` (returns the cleaned copy):
- every `DisabledGroups` entry must be in `junkGroupNames` → else `codeError{Code: "group-unknown", Args: []string{g}}`; dedupe.
- `Ignored`: trim, drop empty, dedupe (keep order).
- each custom folder: `Path` trimmed; empty → `folder-empty`; make absolute (`~` prefix expanded to `homeDir()`), `filepath.Clean`; an enabled folder must pass `safeToClean` → else `folder-protected` [path]; the same `canonical` path twice → `folder-dup` [path]; empty `Name` → `filepath.Base(path)`.

`writeSettings`: marshal `struct{ Version int `json:"version"`; Settings }` with `json.MarshalIndent(…, "", "  ")` and write it with the same temp-file-and-rename steps `saveCategoryConfigs` uses (factor that into `writeConfigAtomically(data []byte) error` and call it from both).

`SaveSettings`: `validateSettings` then `writeSettings`. `GetSettings`: `loadSettings()`.

`fullDiskAccess`:
```go
// fullDiskAccess reports whether the app can read folders macOS keeps behind
// Full Disk Access; reading them never prompts, it only fails.
func fullDiskAccess() bool {
	for _, p := range []string{"Library/Safari", "Library/Mail", "Library/Messages", ".Trash"} {
		if _, err := os.ReadDir(filepath.Join(homeDir(), p)); err == nil {
			return true
		}
	}
	return false
}
```

`PickFolder`:
```go
// PickFolder asks for a folder with the system's open panel, attached to the
// calling window; it returns "" when the user cancels.
func (Cleaner) PickFolder(ctx context.Context) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: mygo.CallerWindow(ctx), Directory: true, CreateDirectories: true, DefaultPath: homeDir(),
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}
```
(Check the exact names in `~/go/pkg/mod/github.com/egoist/mygo@v0.3.6/dialog.go` and `ipc.go`: `Dialog.Open(OpenDialogOptions)` and `CallerWindow(ctx)`.)

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test -count=1 -run 'TestSettings|TestFullDiskAccessProbe' ./...` → PASS. Then the full suite.

- [ ] **Step 5: Regenerate bindings** — `bun run generate`; `bun run typecheck` passes (nothing in the UI uses them yet).

---

### Task 2: Installed-app index

**Files:**
- Create: `owners.go`, `owners_test.go`

**Interfaces:**
- Produces:
  ```go
  type appRef struct{ BundleID, Name string }
  type appIndex struct{ keys map[string]appRef } // lower-cased key → app
  func buildAppIndex(dirs []string) *appIndex
  func (x *appIndex) lookup(name string) (appRef, bool)
  ```
- Consumes: `readBundleInfo`, `appDirs()` (both in `main.go`).

- [ ] **Step 1: Write the failing test** (`owners_test.go`)

```go
func TestAppIndexLookup(t *testing.T) {
	dir := t.TempDir()
	makeApp(t, dir, "Trae CN.app", "cn.trae.app", "Trae CN")
	makeApp(t, dir, "语雀.app", "com.yuque.app", "语雀")
	x := buildAppIndex([]string{dir})
	cases := map[string]string{
		"cn.trae.app":          "Trae CN", // bundle ID
		"CN.TRAE.APP":          "Trae CN", // any case
		"cn.trae.app.ShipIt":   "Trae CN", // bundle ID prefix
		"Trae CN":              "Trae CN", // display name
		"语雀":                   "语雀",
	}
	for key, want := range cases {
		if got, ok := x.lookup(key); !ok || got.Name != want {
			t.Errorf("lookup(%q) = %+v, %v; want %s", key, got, ok, want)
		}
	}
	if _, ok := x.lookup("cn.trae"); ok {
		t.Error("a shorter name must not match a bundle ID")
	}
	if _, ok := x.lookup("Citro"); ok {
		t.Error("an unknown folder has no owner")
	}
}
```

Check `makeApp`'s signature in `main_test.go` (around line 1531) and adapt the calls; it writes an `Info.plist` with `CFBundleIdentifier` and a display name. Extend it with `CFBundleExecutable` only if the test needs it.

- [ ] **Step 2: Run it to see it fail** — `go test -count=1 -run TestAppIndexLookup ./...` → `undefined: buildAppIndex`.

- [ ] **Step 3: Implement `owners.go`**

```go
// appRef names an installed app.
type appRef struct{ BundleID, Name string }

// appIndex finds the installed app a cache folder belongs to, by bundle ID
// or by any of the app's names.
type appIndex struct{ keys map[string]appRef }

// buildAppIndex reads the apps directly inside dirs: their bundle IDs,
// display names, bundle names, executables and folder names.
func buildAppIndex(dirs []string) *appIndex
```
- Enumerate `*.app` in each dir; read `Contents/Info.plist` with `plutil -convert xml1` + `plistStrings` (as `readBundleInfo` does) to get `CFBundleIdentifier`, `CFBundleDisplayName`, `CFBundleName`, `CFBundleExecutable`. Use a bounded pool of 16 like `appNamesByBundleID`.
- Display name = `CFBundleDisplayName`, else `CFBundleName`, else the folder name without `.app`.
- Keys (lower-cased): bundle ID, display name, bundle name, executable, folder name. Skip apps without a bundle ID. First writer wins for a key (deterministic: sort the app paths first).
- `lookup(name)`: lower-case; exact key → hit. Else, for bundle-ID keys only, the longest key `k` with `strings.HasPrefix(name, k+".")` → hit. Else miss.
- Replace `appNamesByBundleID` (Task 1 of the previous round) with `buildAppIndex` where it is used (`ScanAppCaches`), keeping its test meaning, or leave it until Task 12 deletes `ScanAppCaches` — either is fine, but do not keep two copies of the plist-reading pool: make `appNamesByBundleID` a thin wrapper over `buildAppIndex`.

- [ ] **Step 4: Run the test to see it pass**, then the full suite.

---

### Task 3: Rule catalog and expansion into items

**Files:**
- Create: `rules.go`, `junk.go` (expansion part), `junk_test.go`

**Interfaces:**
- Consumes: `Settings` (Task 1), `appIndex` (Task 2), `usageGuard`, `canonical`, `within`, `resolvePath`, `safeToClean`, `removable`, `homeDir`.
- Produces:
  ```go
  type junkGroup string
  const (groupSystem junkGroup = "system"; groupApps = "apps"; groupBrowsers = "browsers"; groupUpdates = "updates"; groupDev = "dev"; groupReview = "review"; groupCustom = "custom")
  type targetMode int
  const (entriesOf targetMode = iota; matched)
  type Target struct { Pattern string; Mode targetMode; Names []string }
  type detailKind int
  const (detailNone detailKind = iota; detailFiles; detailBackups; detailArchives; detailDeviceSupport)
  type Rule struct {
      ID string; Group junkGroup; Targets []Target; Owners []string
      Default, Trash bool; MinAge time.Duration; Detail detailKind; Command *toolCommand
  }
  var junkRules []Rule          // ordered: earlier rules claim paths first
  var protectedPatterns []string // home-relative globs never cleaned
  var unselectedApps map[string]bool // lower-cased bundle IDs off by default

  type JunkItem struct { /* see below */ }
  // SubItem is a separately selectable part of an item (a backup, an archive, an installer…).
  type SubItem struct {
      Path    string `json:"path"`
      Name    string `json:"name"`
      Detail  string `json:"detail"`  // device name, date, platform
      Size    int64  `json:"size"`
      Default bool   `json:"default"` // selected when the item is
  }
  // ScanSummary closes a junk scan.
  type ScanSummary struct {
      FullDiskAccess bool  `json:"fullDiskAccess"`
      Containers     int   `json:"containers"`    // sandboxed apps whose caches need Full Disk Access
      Checked        int   `json:"checked"`       // rules looked at
      Ignored        int   `json:"ignored"`       // items left out by the ignore list
      Leftovers      int   `json:"leftovers"`     // leftovers of uninstalled apps (Task 7)
      LeftoversSize  int64 `json:"leftoversSize"`
  }
  type junkEnv struct {
      home, tmp string
      apps      *appIndex
      guard     *usageGuard
      fda       bool
      settings  Settings
      findTool  func(name string) string
  }
  func currentJunkEnv() junkEnv
  type junkCand struct { path string; minAge time.Duration }
  type itemSpec struct {
      item  JunkItem   // ID, Group, Name, Default, Trash, Running, Denied, Paths
      rule  *Rule      // nil for app and custom items
      roots []string   // canonical target roots: what a selection may reach
      cands []junkCand // entries to measure
  }
  func expandItems(env junkEnv) ([]itemSpec, ScanSummary)
  ```
- `toolCommand` is declared in Task 6; for this task declare `type toolCommand struct{}` in `tools.go` as a placeholder type with a doc comment ("filled in by the tool cleanups") so `Rule` compiles — Task 6 replaces it.

`JunkItem` (exported, bound to the UI):
```go
// JunkItem is one row of the junk page: a rule, an app's caches or a custom
// folder, with what cleaning it would remove now.
type JunkItem struct {
	ID       string    `json:"id"`       // rule ID, "app:<bundle ID>", "app:dir:<name>" or "custom:<path>"
	Group    string    `json:"group"`
	Name     string    `json:"name"`     // apps and custom folders; the UI names rules from their ID
	Size     int64     `json:"size"`     // -1 until measured
	Files    int64     `json:"files"`
	Default  bool      `json:"default"`  // selected after a scan
	Trash    bool      `json:"trash"`    // moved to the Trash rather than deleted
	Running  bool      `json:"running"`  // its app runs, so it is left alone
	InUse    int       `json:"inUse"`    // entries left alone because they are in use
	Denied   bool      `json:"denied"`   // macOS keeps it behind Full Disk Access
	Estimate bool      `json:"estimate"` // Size is a tool's estimate or an upper bound
	Paths    []string  `json:"paths"`    // where it lives
	Top      []TopItem `json:"top"`      // its largest entries
	Subs     []SubItem `json:"subs"`     // separately selectable parts
	Skipped  string    `json:"skipped,omitempty"` // why a tool cleanup cannot run: "no-tool" | "busy"
}
```

- [ ] **Step 1: Write the catalog** (`rules.go`) — the table below is the data; keep this order (claim precedence: review, browsers, updates, dev, then system; custom folders and the generic app sources come after all rules).

```go
// junkRules is the rule table, in claim order: a path matched by an earlier
// rule is left out of every later rule and of the generic app caches.
var junkRules = []Rule{
	// review: user data or costly to fetch again; never selected by default
	{ID: "review.ios-backups", Group: groupReview, Trash: true, Detail: detailBackups,
		Targets: []Target{{Pattern: "Library/Application Support/MobileSync/Backup", Mode: entriesOf}}},
	{ID: "review.ipsw", Group: groupReview, Detail: detailFiles,
		Targets: []Target{{Pattern: "Library/iTunes/iPhone Software Updates", Mode: entriesOf, Names: []string{"*.ipsw"}},
			{Pattern: "Library/iTunes/iPad Software Updates", Mode: entriesOf, Names: []string{"*.ipsw"}}}},
	{ID: "review.xcode-archives", Group: groupReview, Trash: true, Detail: detailArchives,
		Targets: []Target{{Pattern: "Library/Developer/Xcode/Archives/*", Mode: entriesOf, Names: []string{"*.xcarchive"}}}},
	{ID: "review.device-support", Group: groupReview, Detail: detailDeviceSupport,
		Targets: []Target{{Pattern: "Library/Developer/Xcode/*DeviceSupport", Mode: entriesOf}}},
	{ID: "review.simulators", Group: groupReview, Command: &simctlCleanup}, // Task 6
	{ID: "review.installers", Group: groupReview, Trash: true, Detail: detailFiles, MinAge: 30 * 24 * time.Hour,
		Targets: []Target{{Pattern: "Downloads", Mode: entriesOf, Names: []string{"*.dmg", "*.pkg", "*.mpkg", "*.xip"}}}},
	{ID: "review.partial-downloads", Group: groupReview, Trash: true, Detail: detailFiles, MinAge: 24 * time.Hour,
		Targets: []Target{{Pattern: "Downloads", Mode: entriesOf, Names: []string{"*.download", "*.crdownload", "*.part"}}}},
	{ID: "review.mail-downloads", Group: groupReview, Trash: true,
		Targets: []Target{{Pattern: "Library/Containers/com.apple.mail/Data/Library/Mail Downloads", Mode: entriesOf}}},

	// browsers: caches only, never profiles, cookies or history
	{ID: "browser.chrome", Group: groupBrowsers, Default: true, Owners: []string{"com.google.Chrome"},
		Targets: append([]Target{{Pattern: "Library/Caches/Google/Chrome", Mode: entriesOf}},
			chromiumProfileCaches("Library/Application Support/Google/Chrome/*")...)},
	{ID: "browser.chromium", Group: groupBrowsers, Default: true, Owners: []string{"org.chromium.Chromium"},
		Targets: []Target{{Pattern: "Library/Caches/Chromium", Mode: entriesOf}}},
	{ID: "browser.edge", Group: groupBrowsers, Default: true, Owners: []string{"com.microsoft.edgemac"},
		Targets: append([]Target{{Pattern: "Library/Caches/com.microsoft.edgemac", Mode: entriesOf}, {Pattern: "Library/Caches/Microsoft Edge", Mode: entriesOf}},
			chromiumProfileCaches("Library/Application Support/Microsoft Edge/*")...)},
	{ID: "browser.brave", Group: groupBrowsers, Default: true, Owners: []string{"com.brave.Browser"},
		Targets: append([]Target{{Pattern: "Library/Caches/BraveSoftware/Brave-Browser", Mode: entriesOf}},
			chromiumProfileCaches("Library/Application Support/BraveSoftware/Brave-Browser/*")...)},
	{ID: "browser.arc", Group: groupBrowsers, Default: true, Owners: []string{"company.thebrowser.Browser"},
		Targets: append([]Target{{Pattern: "Library/Caches/company.thebrowser.Browser", Mode: entriesOf}},
			chromiumProfileCaches("Library/Application Support/Arc/User Data/*")...)},
	{ID: "browser.dia", Group: groupBrowsers, Default: true, Owners: []string{"company.thebrowser.dia"},
		Targets: []Target{{Pattern: "Library/Caches/company.thebrowser.dia", Mode: entriesOf},
			{Pattern: "Library/Caches/Dia/User Data/*/Cache", Mode: entriesOf}, {Pattern: "Library/Caches/Dia/User Data/*/Code Cache", Mode: entriesOf}}},
	{ID: "browser.safari", Group: groupBrowsers, Default: true, Owners: []string{"com.apple.Safari"},
		Targets: []Target{{Pattern: "Library/Caches/com.apple.Safari", Mode: entriesOf}}},
	{ID: "browser.firefox", Group: groupBrowsers, Default: true, Owners: []string{"org.mozilla.firefox", "app.zen-browser.zen"},
		Targets: []Target{{Pattern: "Library/Caches/Firefox/Profiles/*/cache2", Mode: entriesOf}, {Pattern: "Library/Caches/zen/Profiles/*/cache2", Mode: entriesOf}}},
	{ID: "browser.opera", Group: groupBrowsers, Default: true, Owners: []string{"com.operasoftware.Opera"},
		Targets: []Target{{Pattern: "Library/Caches/com.operasoftware.Opera", Mode: entriesOf}}},
	{ID: "browser.vivaldi", Group: groupBrowsers, Default: true, Owners: []string{"com.vivaldi.Vivaldi"},
		Targets: []Target{{Pattern: "Library/Caches/com.vivaldi.Vivaldi", Mode: entriesOf}}},
	{ID: "browser.qq", Group: groupBrowsers, Default: true, Owners: []string{"com.tencent.QQBrowser", "com.tencent.QQBrowser3"},
		Targets: []Target{{Pattern: "Library/Caches/com.tencent.QQBrowser*", Mode: entriesOf}, {Pattern: "Library/Caches/QQBrowser2", Mode: entriesOf}}},

	// updates: installers apps downloaded for their own updates
	{ID: "update.shipit", Group: groupUpdates, Default: true,
		Targets: []Target{{Pattern: "Library/Caches/*.ShipIt", Mode: matched}}},
	{ID: "update.sparkle", Group: groupUpdates, Default: true,
		Targets: []Target{{Pattern: "Library/Caches/*/org.sparkle-project.Sparkle", Mode: matched}}},
	{ID: "update.updaters", Group: groupUpdates, Default: true,
		Targets: []Target{{Pattern: "Library/Caches/*Updater", Mode: matched}, {Pattern: "Library/Caches/*.updater", Mode: matched}, {Pattern: "Library/Caches/*-updater", Mode: matched}}},

	// dev: caches developer tools fetch again on demand
	{ID: "dev.npm", Group: groupDev, Default: true, Targets: []Target{{Pattern: ".npm/_cacache", Mode: entriesOf}, {Pattern: ".npm/_logs", Mode: entriesOf}}},
	{ID: "dev.npx", Group: groupDev, Default: true, Targets: []Target{{Pattern: ".npm/_npx", Mode: entriesOf}}},
	{ID: "dev.pnpm", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/pnpm", Mode: entriesOf}}}, // the store: Task 6
	{ID: "dev.yarn", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/Yarn", Mode: entriesOf}, {Pattern: ".yarn/berry/cache", Mode: entriesOf}}},
	{ID: "dev.bun", Group: groupDev, Default: true, Targets: []Target{{Pattern: ".bun/install/cache", Mode: entriesOf}}},
	{ID: "dev.corepack", Group: groupDev, Default: true, Targets: []Target{{Pattern: ".cache/node/corepack", Mode: entriesOf}}},
	{ID: "dev.node-gyp", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/node-gyp", Mode: entriesOf}, {Pattern: ".cache/node-gyp", Mode: entriesOf}}},
	{ID: "dev.electron", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/electron", Mode: entriesOf}, {Pattern: "Library/Caches/electron-builder", Mode: entriesOf}, {Pattern: ".cache/electron", Mode: entriesOf}}},
	{ID: "dev.typescript", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/typescript", Mode: entriesOf}}},
	{ID: "dev.puppeteer", Group: groupDev, Targets: []Target{{Pattern: ".cache/puppeteer", Mode: entriesOf}}},
	{ID: "dev.playwright", Group: groupDev, Targets: []Target{{Pattern: "Library/Caches/ms-playwright", Mode: entriesOf}}},
	{ID: "dev.pip", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/pip", Mode: entriesOf}}},
	{ID: "dev.uv", Group: groupDev, Default: true, Targets: []Target{{Pattern: ".cache/uv", Mode: entriesOf}}},
	{ID: "dev.poetry", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/pypoetry/cache", Mode: entriesOf}, {Pattern: "Library/Caches/pypoetry/artifacts", Mode: entriesOf}}},
	{ID: "dev.conda", Group: groupDev, Default: true, Targets: []Target{
		{Pattern: "miniconda3/pkgs", Mode: entriesOf, Names: []string{"*.conda", "*.tar.bz2"}},
		{Pattern: "anaconda3/pkgs", Mode: entriesOf, Names: []string{"*.conda", "*.tar.bz2"}},
		{Pattern: "miniforge3/pkgs", Mode: entriesOf, Names: []string{"*.conda", "*.tar.bz2"}}}},
	{ID: "dev.cargo", Group: groupDev, Default: true, Targets: []Target{{Pattern: ".cargo/registry/cache", Mode: entriesOf}, {Pattern: ".cargo/registry/src", Mode: entriesOf}, {Pattern: ".cargo/git/checkouts", Mode: entriesOf}}},
	{ID: "dev.go-build", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/go-build", Mode: entriesOf}}},
	{ID: "dev.go-mod", Group: groupDev, Command: &goModCleanup}, // Task 6
	{ID: "dev.xcode", Group: groupDev, Default: true, Owners: []string{"com.apple.dt.Xcode"},
		Targets: []Target{{Pattern: "Library/Developer/Xcode/DerivedData", Mode: entriesOf}, {Pattern: "Library/Caches/com.apple.dt.Xcode", Mode: entriesOf}}},
	{ID: "dev.simulator-caches", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Developer/CoreSimulator/Caches", Mode: entriesOf}}},
	{ID: "dev.cocoapods", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/CocoaPods", Mode: entriesOf}}},
	{ID: "dev.swiftpm", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/org.swift.swiftpm", Mode: entriesOf}}},
	{ID: "dev.carthage", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/org.carthage.CarthageKit", Mode: entriesOf}}},
	{ID: "dev.android", Group: groupDev, Default: true, Targets: []Target{{Pattern: ".android/cache", Mode: entriesOf}, {Pattern: ".android/build-cache", Mode: entriesOf}, {Pattern: "Library/Caches/Google/AndroidStudio*", Mode: entriesOf}}},
	{ID: "dev.gradle", Group: groupDev, Targets: []Target{{Pattern: ".gradle/caches", Mode: entriesOf}}},
	{ID: "dev.maven", Group: groupDev, Targets: []Target{{Pattern: ".m2/repository", Mode: entriesOf}}},
	{ID: "dev.homebrew", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/Homebrew/downloads", Mode: entriesOf}}},
	{ID: "dev.homebrew-old", Group: groupDev, Default: true, Command: &brewCleanup}, // Task 6
	{ID: "dev.pnpm-store", Group: groupDev, Default: true, Command: &pnpmPrune},    // Task 6

	// system
	{ID: "sys.logs", Group: groupSystem, Default: true, Targets: []Target{{Pattern: "Library/Logs", Mode: entriesOf}}},
	{ID: "sys.temp", Group: groupSystem, Default: true, MinAge: tempMinAge, Targets: []Target{{Pattern: "$TMPDIR", Mode: entriesOf}}},
	{ID: "sys.trash", Group: groupSystem, Targets: []Target{{Pattern: ".Trash", Mode: entriesOf}}},
	{ID: "sys.apple", Group: groupSystem, Default: true, Targets: []Target{{Pattern: "Library/Caches/com.apple.*", Mode: matched}}},
}

// chromiumProfileCaches are the cache folders Chromium keeps in each profile
// under profiles, a pattern such as ".../Google/Chrome/*".
func chromiumProfileCaches(profiles string) []Target {
	var ts []Target
	for _, d := range []string{"Code Cache", "GPUCache", "DawnCache", "GrShaderCache", "GraphiteDawnCache"} {
		ts = append(ts, Target{Pattern: profiles + "/" + d, Mode: entriesOf})
	}
	return ts
}

// protectedPatterns are home-relative paths no rule and no generic scan
// cleans: macOS needs them, or they hold work that is not a cache.
var protectedPatterns = []string{
	"Library/Caches/com.apple.Spotlight*", "Library/Caches/com.apple.spotlight*",
	"Library/Caches/com.apple.FontRegistry*", "Library/Caches/com.apple.finder",
	"Library/Caches/CloudKit*", "Library/Caches/pypoetry/virtualenvs*",
	"Library/Caches/org.R-project.R/R/renv*", "Library/Caches/JetBrains*",
	"Library/Caches/com.jetbrains.toolbox*", "Library/Mobile Documents*",
	".ollama/models", ".cache/huggingface", ".cache/codex-runtimes",
}

// unselectedApps keep offline content in their caches (downloaded songs), so
// their cache rows start unselected.
var unselectedApps = map[string]bool{"com.spotify.client": true, "com.netease.163music": true, "com.tencent.qqmusicmac": true}

// chromiumCacheDirs are the cache folders Electron and Chromium apps keep in
// their Application Support folder.
var chromiumCacheDirs = []string{"Cache", "Code Cache", "GPUCache", "DawnCache", "DawnGraphiteCache", "DawnWebGPUCache", "GrShaderCache", "CachedData", "logs"}
```

`dev.pnpm-store`, `dev.homebrew-old`, `dev.go-mod` and `review.simulators` reference command values declared in Task 6. Until then, declare these four variables in `tools.go` as `var brewCleanup, pnpmPrune, goModCleanup, simctlCleanup toolCommand` with a doc comment, and have `expandItems` skip rules with a `Command` (Task 6 turns them on).

- [ ] **Step 2: Write the failing expansion tests** (`junk_test.go`)

Use a helper that stages an environment:
```go
// testEnv stages a junk scan in a temporary home with no apps, nothing in
// use and Full Disk Access granted.
func testEnv(t *testing.T) junkEnv {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tmp := filepath.Join(home, "tmp")
	mustMkdir(t, tmp)
	return junkEnv{home: home, tmp: tmp, apps: buildAppIndex(nil), guard: &usageGuard{running: map[string]bool{}}, fda: true, findTool: func(string) string { return "" }}
}

func itemIDs(specs []itemSpec) []string {
	var ids []string
	for _, s := range specs {
		ids = append(ids, s.item.ID)
	}
	slices.Sort(ids)
	return ids
}

func TestExpandShowsOnlyWhatExists(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/abc/pkg.tgz"), 1000)
	writeFile(t, filepath.Join(env.home, ".cache/uv/wheels/x.whl"), 1000)
	specs, sum := expandItems(env)
	got := itemIDs(specs)
	if !slices.Equal(got, []string{"dev.npx", "dev.uv"}) {
		t.Fatalf("items = %v", got)
	}
	if sum.Checked < len(junkRules)-4 { // every rule without a command was looked at
		t.Fatalf("checked = %d", sum.Checked)
	}
}

func TestClaimsKeepRuleFoldersOutOfAppItems(t *testing.T) {
	env := testEnv(t)
	caches := filepath.Join(env.home, "Library/Caches")
	writeFile(t, filepath.Join(caches, "Google/Chrome/Default/Cache/data_1"), 1000)
	writeFile(t, filepath.Join(caches, "Google/Other/blob"), 1000)
	writeFile(t, filepath.Join(caches, "cn.trae.app.ShipIt/update.zip"), 1000)
	writeFile(t, filepath.Join(caches, "go-build/ab/cd"), 1000)
	env.settings.Ignored = []string{"browser.chrome"}         // ignored, still claims
	env.settings.DisabledGroups = []string{"updates"}         // off, still claims
	specs, _ := expandItems(env)
	for _, s := range specs {
		for _, c := range s.cands {
			if strings.Contains(c.path, "Google/Chrome") || strings.Contains(c.path, "ShipIt") {
				t.Fatalf("%s reaches %s, which belongs to an ignored or disabled rule", s.item.ID, c.path)
			}
		}
	}
	ids := itemIDs(specs)
	if !slices.Contains(ids, "app:dir:google") || !slices.Contains(ids, "dev.go-build") || slices.Contains(ids, "browser.chrome") {
		t.Fatalf("items = %v", ids)
	}
}

func TestAppItemsMergeSourcesByOwner(t *testing.T) {
	env := testEnv(t)
	appsDir := t.TempDir()
	makeApp(t, appsDir, "Trae CN.app", "cn.trae.app", "Trae CN")
	makeApp(t, appsDir, "Spotify.app", "com.spotify.client", "Spotify")
	env.apps = buildAppIndex([]string{appsDir})
	writeFile(t, filepath.Join(env.home, "Library/Caches/cn.trae.app/a"), 100)
	writeFile(t, filepath.Join(env.home, "Library/Application Support/Trae CN/Cache/b"), 100)
	writeFile(t, filepath.Join(env.home, "Library/Application Support/Trae CN/Service Worker/c"), 100) // not a cache folder
	writeFile(t, filepath.Join(env.home, "Library/Containers/com.spotify.client/Data/Library/Caches/d"), 100)
	specs, _ := expandItems(env)
	var trae, spotify *itemSpec
	for i := range specs {
		switch specs[i].item.ID {
		case "app:cn.trae.app":
			trae = &specs[i]
		case "app:com.spotify.client":
			spotify = &specs[i]
		}
	}
	if trae == nil || trae.item.Name != "Trae CN" || len(trae.cands) != 2 {
		t.Fatalf("trae = %+v", trae)
	}
	if spotify == nil || spotify.item.Default {
		t.Fatalf("spotify should be listed and unselected: %+v", spotify)
	}
}

func TestNoFullDiskAccessStaysOutOfContainers(t *testing.T) {
	env := testEnv(t)
	env.fda = false
	data := filepath.Join(env.home, "Library/Containers/com.tencent.xinWeChat/Data")
	writeFile(t, filepath.Join(data, "Library/Caches/x"), 100)
	if err := os.Chmod(data, 0); err != nil { // a read would fail loudly
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(data, 0o755) })
	specs, sum := expandItems(env)
	if sum.Containers != 1 || sum.FullDiskAccess {
		t.Fatalf("summary = %+v", sum)
	}
	for _, s := range specs {
		if strings.Contains(strings.Join(s.item.Paths, ","), "Containers") {
			t.Fatalf("%s reached into a container without access", s.item.ID)
		}
	}
}

func TestProtectedAndCustom(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/com.apple.Spotlight/x"), 100)
	writeFile(t, filepath.Join(env.home, "Library/Caches/com.apple.akd/y"), 100)
	writeFile(t, filepath.Join(env.home, ".cache/huggingface/hub/model.bin"), 100)
	writeFile(t, filepath.Join(env.home, "stuff/old.bin"), 100)
	env.settings.Custom = []CustomFolder{{Name: "stuff", Path: filepath.Join(env.home, "stuff"), Enabled: true}}
	specs, _ := expandItems(env)
	for _, s := range specs {
		for _, c := range s.cands {
			if strings.Contains(c.path, "Spotlight") || strings.Contains(c.path, "huggingface") {
				t.Fatalf("%s reaches protected %s", s.item.ID, c.path)
			}
		}
	}
	ids := itemIDs(specs)
	if !slices.Contains(ids, "sys.apple") || !slices.Contains(ids, "custom:"+filepath.Join(env.home, "stuff")) {
		t.Fatalf("items = %v", ids)
	}
}
```

- [ ] **Step 3: Run them to see them fail** — `go test -count=1 -run 'TestExpand|TestClaims|TestAppItems|TestNoFullDisk|TestProtectedAndCustom' ./...` → build failure on the new names.

- [ ] **Step 4: Implement `expandItems`** in `junk.go`

Algorithm (keep each step a small function):
1. `resolve(pattern)`: replace a leading `$TMPDIR` with `env.tmp`, else join with `env.home`; `filepath.Glob`; drop matches under a protected pattern (`protected(path)` = some `filepath.Match(home+"/"+pattern, path)` hit for the path or one of its ancestors).
2. A `claimSet` of canonical paths with `claimed(p)` (p equals or is under a claimed path) and `inside(p)` (some claimed path is strictly under p).
3. For each rule in order (Command rules are skipped in this task): resolve every target; for `matched` the matches are the entries; for `entriesOf` read each match's directory (`os.ReadDir`; permission error → item `Denied`, no entries) and take the entries whose name passes `Names` (`filepath.Match`, any). Skip special files (sockets, pipes, devices). Entries already claimed are left out; an entry with claims inside it is replaced by its children (repeat until no claim is inside — reuse the shape of `planCategory`'s `collect`). Then claim every target root and every entry, **whether or not the rule is ignored or its group is off**. Emit an item only when the rule is not ignored, its group is on, and it has entries or is `Denied`.
4. Custom folders (enabled ones): each is an item `custom:<path>`, group `custom`, `Trash: true`, `Default: true`, entries = the folder's entries minus claims; then claim it.
5. Generic app sources (only if the apps group is on, but **claims from steps 3–4 apply regardless**):
   - A: entries of `Library/Caches` except names starting with `com.apple.`, claimed paths (with descent), protected paths.
   - B: only when `env.fda`: for each `Library/Containers/<id>` with `id` not starting `com.apple.`, entries of `<id>/Data/Library/Caches`. Without access: `sum.Containers` = number of entries of `Library/Containers` not starting with `com.apple.` (a listing only).
   - C: for each `Library/Application Support/<dir>` not starting with `com.apple.`, each existing `<dir>/<name>` for `name` in `chromiumCacheDirs` contributes its entries.
   - Owner: `env.apps.lookup(name)` with `name` = the Caches entry name (A), the container ID (B) or the Application Support folder name (C). Hit → ID `app:` + lower-cased bundle ID, Name = app name; miss → ID `app:dir:` + lower-cased name, Name = the folder name. Merge items with the same ID (append candidates and `Paths`).
   - `Default` = not `unselectedApps[lower(bundle ID)]`; `Running` = the owner's bundle ID is in `env.guard.running`.
6. For rules with `Owners`: `Running` when any owner's lower-cased bundle ID is in `env.guard.running`.
7. `roots` of each item = canonical target roots (the resolved targets for rules, the sources' folders for apps, the folder for custom items). `Paths` = the same roots, made home-relative with `~` for display (`displayable`).
8. `sum.Checked` = number of non-command rules looked at; `sum.Ignored` = ignored items that had entries; `sum.FullDiskAccess = env.fda`.
9. `currentJunkEnv()`: `homeDir()`, `os.TempDir()`, `buildAppIndex(appDirs())`, `probeUsage()`, `fullDiskAccess()`, `loadSettings()`, `findTool` (Task 6; until then `func(string) string { return "" }`).

- [ ] **Step 5: Run the tests to see them pass**, then the full suite.

---

### Task 4: Measuring items and their sub-items

**Files:**
- Modify: `junk.go`, `main.go` (extend `plistStrings` to also return `<date>` values), `junk_test.go`

**Interfaces:**
- Consumes: `itemSpec` (Task 3), `measure`, `removalRefused`, `changedAt`, `now`, `scanConcurrency`.
- Produces:
  ```go
  type itemPlan struct {
      item    JunkItem
      entries []cleanEntry // largest first
  }
  func planItem(ctx context.Context, spec itemSpec, env junkEnv, sem chan struct{}, measuring func(string)) itemPlan
  func deviceSupportVersion(name string) []int
  func plistValues(data []byte) map[string]string // strings and dates of the top-level dictionary
  ```

- [ ] **Step 1: Write the failing tests**

```go
func TestPlanItemMeasuresAndSkips(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/a/x"), 3000)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/b/y"), 1000)
	open := filepath.Join(env.home, ".npm/_npx/c")
	writeFile(t, filepath.Join(open, "z"), 500)
	env.guard.open = []string{strings.ToLower(resolvePath(filepath.Join(open, "z")))}
	specs, _ := expandItems(env)
	p := planItem(context.Background(), specs[0], env, make(chan struct{}, 4), nil)
	if p.item.Size != 4000 || p.item.InUse != 1 || len(p.entries) != 2 || p.entries[0].size != 3000 {
		t.Fatalf("plan = %+v", p)
	}
	if len(p.item.Top) != 2 || p.item.Top[0].Size != 3000 {
		t.Fatalf("top = %+v", p.item.Top)
	}
}

func TestPlanItemRunningOwnerTakesNothing(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/Google/Chrome/Default/Cache/d"), 100)
	env.guard.running = map[string]bool{"com.google.chrome": true}
	specs, _ := expandItems(env)
	p := planItem(context.Background(), specs[0], env, make(chan struct{}, 4), nil)
	if !p.item.Running || len(p.entries) != 0 {
		t.Fatalf("a running browser keeps its cache: %+v", p)
	}
}

func TestDeviceSupportKeepsNewestPerPlatform(t *testing.T) {
	env := testEnv(t)
	ios := filepath.Join(env.home, "Library/Developer/Xcode/iOS DeviceSupport")
	for _, v := range []string{"17.2 (21C62)", "iPhone15,2 17.5.1 (21F90)", "18.0 (22A3354)"} {
		writeFile(t, filepath.Join(ios, v, "Symbols/x"), 100)
	}
	writeFile(t, filepath.Join(env.home, "Library/Developer/Xcode/watchOS DeviceSupport/10.4 (21T216)/Symbols/x"), 100)
	specs, _ := expandItems(env)
	var p itemPlan
	for _, s := range specs {
		if s.item.ID == "review.device-support" {
			p = planItem(context.Background(), s, env, make(chan struct{}, 4), nil)
		}
	}
	keep := map[string]bool{}
	for _, s := range p.item.Subs {
		if !s.Default {
			keep[filepath.Base(s.Path)] = true
		}
	}
	if len(p.item.Subs) != 4 || !keep["18.0 (22A3354)"] || !keep["10.4 (21T216)"] || len(keep) != 2 {
		t.Fatalf("subs = %+v", p.item.Subs)
	}
	if p.item.Default {
		t.Fatal("review items start unselected")
	}
}

func TestInstallersNeedThirtyDays(t *testing.T) {
	env := testEnv(t)
	old := filepath.Join(env.home, "Downloads/Old.dmg")
	writeFile(t, old, 100)
	writeFile(t, filepath.Join(env.home, "Downloads/New.dmg"), 100)
	writeFile(t, filepath.Join(env.home, "Downloads/notes.txt"), 100)
	saved := now
	now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	defer func() { now = saved }()
	if err := os.Chtimes(filepath.Join(env.home, "Downloads/New.dmg"), now(), now()); err != nil {
		t.Fatal(err)
	}
	specs, _ := expandItems(env)
	for _, s := range specs {
		if s.item.ID == "review.installers" {
			p := planItem(context.Background(), s, env, make(chan struct{}, 4), nil)
			if len(p.item.Subs) != 1 || p.item.Subs[0].Path != resolvePath(old) {
				t.Fatalf("subs = %+v", p.item.Subs)
			}
			return
		}
	}
	t.Fatal("no installers item")
}
```

(`os.Chtimes` cannot move the status-change time; `changedAt` takes the later of the two, so the "new" file's ctime stays at creation — which is fine because `now` is shifted 31 days ahead and the "new" file gets an mtime of `now()`, keeping it younger than 30 days.)

Add a backup test with an `Info.plist` holding `<key>Device Name</key><string>小明的 iPhone</string>` and `<key>Last Backup Date</key><date>2026-09-01T10:00:00Z</date>` → `SubItem.Name == "小明的 iPhone"`, `Detail` starts with `2026-09-01`; and an archive test with `Info.plist` keys `Name` and `CreationDate`.

- [ ] **Step 2: Run them to see them fail.**

- [ ] **Step 3: Implement**
- `planItem`: if `spec.item.Running` → return with no entries. Measure each candidate under `sem` (stop starting new walks once `ctx` is done, as `planCategory` does); apply `env.guard.inUse` (count `InUse`), `removalRefused`, the rule's `MinAge` against `measure`'s newest change; sort entries largest first; `Size`/`Files` = sums; `Top` = up to 5 largest as `TopItem{Name: base, Path, Size}`.
- Sub-items when `rule.Detail != detailNone`: one `SubItem` per entry. `detailFiles`: `Name` = base, `Detail` = modification date `2006-01-02`; `detailBackups`: read `<entry>/Info.plist` through `plutil -convert xml1 -o -` and `plistValues`: `Name` = `Device Name` (fallback: base), `Detail` = the date part of `Last Backup Date`; `detailArchives`: `Info.plist` `Name` and `CreationDate`; `detailDeviceSupport`: `Name` = base, `Detail` = platform (`iOS`, `watchOS`… from the parent folder name), `Default` = false for the newest version per platform, true otherwise. Other details: `Default` = true.
- `deviceSupportVersion(name)`: the first match of `\d+(?:\.\d+)+` that is not followed by `,` (skips model identifiers like `iPhone15,2`) split into ints; compare element-wise.
- `plistValues`: like `plistStrings` but also records `<date>` text. Implement by generalizing `plistStrings`' decoder loop; keep `plistStrings` as a wrapper so existing callers do not change.

- [ ] **Step 4: Run the tests to see them pass**, then the full suite.

---

### Task 5: `ScanJunk` and `CleanJunk`

**Files:**
- Modify: `junk.go`, `junk_test.go`

**Interfaces:**
- Consumes: `expandItems`, `planItem`, `cleanHooks`, `trashItem`, `removeAll`, `changedAt`, `userTrashDir`, `progressInterval`.
- Produces (bound to the UI):
  ```go
  type JunkScanEvent struct {
      Kind    string       `json:"kind"`              // "start" | "scanning" | "item" | "done"
      Items   []JunkItem   `json:"items,omitempty"`   // start: every item, not yet measured (Size -1)
      Path    string       `json:"path,omitempty"`    // scanning: an entry being measured
      Item    *JunkItem    `json:"item,omitempty"`    // item: an item, once measured
      Summary *ScanSummary `json:"summary,omitempty"` // done
  }
  type JunkCleanEvent struct {
      Kind   string       `json:"kind"`             // "start" | "checking" | "removed" | "done"
      ID     string       `json:"id"`
      Path   string       `json:"path,omitempty"`
      Bytes  int64        `json:"bytes,omitempty"`  // cleaned so far in the pass
      Result *CleanResult `json:"result,omitempty"` // done: CleanResult.Name is the item ID
  }
  type JunkSelection struct {
      ID    string   `json:"id"`
      Paths []string `json:"paths,omitempty"` // sub-item paths; empty = the whole item
  }
  func scanJunk(ctx context.Context, env junkEnv, measuring func(string), sized func(JunkItem)) ([]JunkItem, ScanSummary)
  func cleanJunk(ctx context.Context, env junkEnv, sel []JunkSelection, scannedAt time.Time, hooks cleanHooks) []CleanResult
  func (Cleaner) ScanJunk(ctx context.Context, ch *mygo.Channel[JunkScanEvent]) error
  func (Cleaner) CleanJunk(ctx context.Context, sel []JunkSelection, scannedAt int64, ch *mygo.Channel[JunkCleanEvent]) error
  ```
- `CleanResult` gains `Skipped string \`json:"skipped,omitempty"\`` ("running" | "no-tool" | "busy" | "timeout" | "failed").
- The legacy `Scan`, `Clean`, `ScanStream`, `CleanStream` stay until Task 12.

- [ ] **Step 1: Write the failing tests**

```go
func TestCleanJunkDeletesSelectedAndTrashesCustom(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/a/x"), 300)
	writeFile(t, filepath.Join(env.home, "stuff/old.bin"), 200)
	env.settings.Custom = []CustomFolder{{Name: "stuff", Path: filepath.Join(env.home, "stuff"), Enabled: true}}
	var events []string
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.npx"}, {ID: "custom:" + filepath.Join(env.home, "stuff")}}, time.Time{}, cleanHooks{
		removed: func(id, path string, bytes int64) { events = append(events, id) },
	})
	if len(res) != 2 || res[0].Freed != 300 || res[1].Trashed != 200 {
		t.Fatalf("results = %+v", res)
	}
	if exists(filepath.Join(env.home, ".npm/_npx/a")) || !exists(filepath.Join(env.home, ".Trash/old.bin")) {
		t.Fatal("npx entry should be deleted, the custom folder's entry trashed")
	}
	if !exists(filepath.Join(env.home, "stuff")) {
		t.Fatal("a cleaned folder stays")
	}
}

func TestCleanJunkRefusesPathsOutsideTheItem(t *testing.T) {
	env := testEnv(t)
	old := filepath.Join(env.home, "Downloads/Old.dmg")
	writeFile(t, old, 100)
	other := filepath.Join(env.home, "Documents/keep.dmg")
	writeFile(t, other, 100)
	saved := now
	now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	defer func() { now = saved }()
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.installers", Paths: []string{other, old + "-renamed"}}}, time.Time{}, cleanHooks{})
	if !exists(other) || !exists(old) || res[0].Trashed != 0 {
		t.Fatalf("paths outside the item, or no longer in it, must stay: %+v", res)
	}
}

func TestCleanJunkLeavesAnAppThatStartedRunning(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/Google/Chrome/Default/Cache/d"), 100)
	env.guard.running = map[string]bool{"com.google.chrome": true} // started after the scan
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "browser.chrome"}}, time.Time{}, cleanHooks{})
	if res[0].Skipped != "running" || !exists(filepath.Join(env.home, "Library/Caches/Google/Chrome/Default/Cache/d")) {
		t.Fatalf("results = %+v", res)
	}
}

func TestScanJunkStreamsItems(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/a/x"), 300)
	writeFile(t, filepath.Join(env.home, ".cache/uv/b"), 100)
	var sized []string
	items, sum := scanJunk(context.Background(), env, nil, func(it JunkItem) { sized = append(sized, it.ID) })
	slices.Sort(sized)
	if len(items) != 2 || !slices.Equal(sized, []string{"dev.npx", "dev.uv"}) || !sum.FullDiskAccess {
		t.Fatalf("items %v sized %v summary %+v", items, sized, sum)
	}
}
```

Also port the Trash rule: an entry in `.Trash` changed after `scannedAt` stays (adapt `TestCleanCategoryEmptiesTrash`'s setup to `sys.trash`), and a stop test: cancel `ctx` from the `removed` hook after the first entry → the second entry of the item remains and no further item starts.

- [ ] **Step 2: Run them to see them fail.**

- [ ] **Step 3: Implement**
- `scanJunk`: `expandItems`; report ignored/disabled via the summary; plan all items concurrently over one `sem` of size `scanConcurrency`; call `measuring`/`sized` under one mutex and only while `ctx.Err() == nil` (as `scanStream` does).
- `ScanJunk`: send `start` with every item (`Size: -1`); throttle `scanning` to `progressInterval`; send `item` per measured item; send `done` with the summary; return `ctx.Err()`.
- `cleanJunk`: expand again (fresh env passed in); index specs by ID; order the selection with `sys.trash` first, then as given; for each selected ID (stop when `ctx` is done): `hooks.started(id)`; refresh the guard (`probeUsage()` in `CleanJunk`; tests pass their own env and keep it); a running owner → `CleanResult{Name: id, Skipped: "running"}`; otherwise `planItem`; if the selection has `Paths`, keep only entries whose canonical path is within one of the selected paths **and** within one of `spec.roots` (paths outside are ignored); remove each entry (`removeAll` when the rule deletes, `trashItem` when `Trash`; entries inside the Trash: skip those changed after `scannedAt`), count `Freed`/`Trashed`/`Files`, report `removed` with the running byte total; collect failures in `Failed`; `hooks.finished(res)`.
- `CleanJunk`: like `CleanStream`: build hooks that send events (throttle `checking`/`removed`), call `cleanJunk(ctx, currentJunkEnv(), …)`, return `ctx.Err()`.

- [ ] **Step 4: Run the tests to see them pass**, then the full suite; `bun run generate`; `bun run typecheck`.

---

### Task 6: Tool cleanups

**Files:**
- Modify: `tools.go`, `junk.go` (expansion and cleaning of `Command` rules), `tools_test.go` (new)

**Interfaces:**
- Produces:
  ```go
  type toolCommand struct {
      Tool     string        // executable, found by findTool
      Args     []string      // the cleanup
      Busy     []string      // substrings of `ps -axo command=` lines that mean the tool is at work
      Timeout  time.Duration
      Estimate func(env junkEnv, tool string) (size int64, upper bool, roots []string, err error)
  }
  var brewCleanup, pnpmPrune, goModCleanup, simctlCleanup toolCommand
  var toolDirs func() []string
  var psCommands func() ([]byte, error)
  func findTool(name string) string
  func toolBusy(patterns []string) bool
  func runTool(ctx context.Context, tool string, args []string, timeout time.Duration) (string, error)
  func parseBrewFreed(out string) int64
  func parseSimctlUnavailable(out []byte) []string
  ```

| Command | `Tool`, `Args` | `Busy` | `Timeout` | Estimate |
|---|---|---|---|---|
| `brewCleanup` | `brew cleanup --prune=30` | `Homebrew/brew.rb`, `Homebrew/brew.sh` | 5 min | `brew cleanup --prune=30 --dry-run` → `parseBrewFreed` (matches `(?:would free|has freed) approximately ([\d.]+)\s?(B|KB|MB|GB|TB)`, 1024-based; the same parser reads the real run's output); `upper` false; `roots` none |
| `pnpmPrune` | `pnpm store prune` | `/pnpm`, `pnpm.cjs`, `pnpm.mjs` | 5 min | `pnpm store path`; the path must be inside the home folder; size of that folder; `upper` true; `roots` = [store path] |
| `goModCleanup` | `go clean -modcache` | `bin/go `, `gopls` | 5 min | `go env GOMODCACHE`; inside home; size; `upper` false |
| `simctlCleanup` | `xcrun simctl delete unavailable` | none | 2 min | `xcrun simctl list devices unavailable -j` → `parseSimctlUnavailable` returns the `dataPath`s → sum of their sizes; `upper` false |

- `toolDirs()`: `/opt/homebrew/bin`, `/usr/local/bin`, `/usr/bin`, `~/.local/bin`, `~/.cargo/bin`, `~/.bun/bin`, `~/.volta/bin`, `~/Library/pnpm`, `/usr/local/go/bin`, and the `bin` of the newest `~/.nvm/versions/node/*` (sort by version numbers).
- `runTool`: `exec.CommandContext` with the timeout; `PATH` = the tool's folder + `/usr/bin:/bin:/usr/sbin:/sbin`; for brew add `HOMEBREW_NO_AUTO_UPDATE=1`, `HOMEBREW_NO_ANALYTICS=1`, `HOMEBREW_NO_ENV_HINTS=1`; returns combined output.
- `planItem` returns a command item as expanded (its size is the estimate; it has no entries).
- Expansion: a `Command` rule produces an item when `env.findTool(Tool)` is non-empty and the estimate succeeds with a size > 0; `Estimate` true when `upper`; `Paths`/`roots` = the estimate's roots. No tool → no item.
- Cleaning: tool gone → `Skipped: "no-tool"`; `toolBusy` → `"busy"`; deadline exceeded → `"timeout"`; non-zero exit → `"failed"` with the last output line in `Failed`. Freed = for brew `parseBrewFreed` of the real run's output; for the others the root's size before minus after.

- [ ] **Step 1: Write the failing tests** — stage fake tools in a temp dir and point `toolDirs` at it:

```go
func fakeTool(t *testing.T, dir, name, script string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestBrewCleanupEstimateAndRun(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	fakeTool(t, bin, "brew", `echo "$@" >> `+log+`
case "$*" in *--dry-run*) echo "==> This operation would free approximately 1.5GB of disk space." ;;
*) echo "==> This operation has freed approximately 1.5GB of disk space." ;; esac`)
	saved := toolDirs
	toolDirs = func() []string { return []string{bin} }
	defer func() { toolDirs = saved }()
	env.findTool = findTool
	specs, _ := expandItems(env)
	var found bool
	for _, s := range specs {
		if s.item.ID == "dev.homebrew-old" {
			p := planItem(context.Background(), s, env, make(chan struct{}, 1), nil)
			found = p.item.Size == int64(1.5*(1<<30))
		}
	}
	if !found {
		t.Fatal("brew estimate missing")
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.homebrew-old"}}, time.Time{}, cleanHooks{})
	data, _ := os.ReadFile(log)
	if res[0].Freed != int64(1.5*(1<<30)) || !strings.Contains(string(data), "cleanup --prune=30\n") {
		t.Fatalf("result %+v, calls %q", res[0], data)
	}
}

func TestToolCleanupSkipsWhenBusyMissingOrHanging(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	fakeTool(t, bin, "go", `case "$*" in "env GOMODCACHE") echo `+filepath.Join(env.home, "go/pkg/mod")+`;; *) sleep 5;; esac`)
	writeFile(t, filepath.Join(env.home, "go/pkg/mod/cache/x"), 100)
	saved, savedPs := toolDirs, psCommands
	toolDirs = func() []string { return []string{bin} }
	psCommands = func() ([]byte, error) { return []byte("/usr/libexec/gopls serve\n"), nil }
	defer func() { toolDirs, psCommands = saved, savedPs }()
	env.findTool = findTool
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.go-mod"}}, time.Time{}, cleanHooks{})
	if res[0].Skipped != "busy" {
		t.Fatalf("gopls at work: %+v", res[0])
	}
	psCommands = func() ([]byte, error) { return nil, nil }
	goModCleanup.Timeout = 200 * time.Millisecond
	defer func() { goModCleanup.Timeout = 5 * time.Minute }()
	res = cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.go-mod"}, {ID: "dev.npx"}}, time.Time{}, cleanHooks{})
	if res[0].Skipped != "timeout" {
		t.Fatalf("a hanging tool must time out: %+v", res[0])
	}
	toolDirs = func() []string { return nil }
	res = cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.go-mod"}}, time.Time{}, cleanHooks{})
	if len(res) == 0 || res[0].Skipped != "no-tool" {
		t.Fatalf("no go: %+v", res)
	}
}

func TestParseSimctlUnavailable(t *testing.T) {
	out := []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-16-0":[{"udid":"A","dataPath":"/Users/x/Library/Developer/CoreSimulator/Devices/A/data","isAvailable":false}]}}`)
	if got := parseSimctlUnavailable(out); !slices.Equal(got, []string{"/Users/x/Library/Developer/CoreSimulator/Devices/A"}) {
		t.Fatalf("got %v", got)
	}
}
```
(`parseSimctlUnavailable` returns each device folder — the parent of `dataPath`.) For the `no-tool` case `cleanJunk` must still produce a result for a selected command item whose tool vanished since the scan: look the rule up by ID when expansion no longer yields the item.

- [ ] **Step 2: Run them to see them fail. Step 3: implement. Step 4: run them to see them pass**, then the full suite; `bun run generate`.

---

### Task 7: Leftovers in the scan summary

**Files:** Modify `junk.go`, `junk_test.go`

**Interfaces:** `scanJunk` fills `ScanSummary.Leftovers` and `LeftoversSize` by running `collectOrphans(ctx, nil)` concurrently with the measuring (skip it when the review group is off); `ScanJunk` sends them in `done`.

- [ ] **Step 1: Failing test** — stage one leftover the way `TestOrphanItemsReportDirectories` does (a ≥ 1 MB folder under `Library/Application Support` named after no installed app) and assert `sum.Leftovers == 1` and `sum.LeftoversSize >= 1<<20`; with `DisabledGroups: ["review"]` → `0`.
- [ ] **Step 2–4:** run (fail), implement, run (pass); full suite; `bun run generate`.

---

### Task 8: Split shared frontend helpers into `src/shared.ts`

**Files:** Create `src/shared.ts`; modify `src/main.ts`

**Interfaces (exports of `src/shared.ts`, moved verbatim from `src/main.ts`):**
`$`, `msg`, `errText`, `human`, `esc`, `fmtDate`, `displayPath`, `baseName`, `toast`, `emptyState`, `errorState`, `skeletonRows`, `setBusy`, `setStoppable`, `stopScan`, `endScan`, `stoppedScans`, `scanChans`, `markRescan`, `setNavBusy`, `View`, `VIEWS`, the modal (`overlay`, `modalTitle`, `modalBody`, `modalOk`, `modalCancel`, `showModal`, `hideModal`, `modalSeq` via `currentModalSeq()`), and a setter for the pending confirmation `setPending(fn: (() => Promise<void>) | null)` replacing direct writes to `pending`.

- [ ] **Step 1:** Move the code; replace every `pending = …` in `main.ts` with `setPending(…)`; keep behaviour identical.
- [ ] **Step 2:** `bun run typecheck` and `bun run build:web` pass. (The reviewer re-runs the browser checks of every page.)

---

### Task 9: The junk page on `ScanJunk`/`CleanJunk`

**Files:** Create `src/junk.ts`; modify `src/main.ts` (remove the old junk section: `phase`, `scan`, `render`, `renderHero`, `rowHtml`, `clean`, `askCleanAll`, `CAT_GROUPS`, `CAT_ICONS`…), `src/i18n.ts`, `src/style.css`, `index.html` (only if the hero markup needs ids)

**Interfaces:**
- Exports: `initJunk(): void`, `refreshJunkI18n(): void`, `markJunkStale(): void`, `openLeftovers: (() => void) | null` (set by `main.ts`).
- Consumes: `Cleaner.scanJunk(ch)`, `Cleaner.cleanJunk(selections, scannedAt, ch)`, `Cleaner.getSettings()`, `Cleaner.saveSettings(s)`, `Cleaner.revealInFinder(path)`, `Cleaner.openFullDiskAccessSettings()`, shared helpers.

State model (keep the phase machine of the current page):
```ts
type JunkPhase = "idle" | "scanning" | "scanned" | "cleaning" | "cleaned";
let phase: JunkPhase = "idle";
let items: JunkItem[] = [];               // in the order of the start event
const pending = new Set<string>();         // not measured yet
const selected = new Set<string>();        // item IDs
const subSel = new Map<string, Set<string>>(); // item ID → chosen sub-item paths
const expanded = new Set<string>();        // item IDs showing details
const collapsed = new Set<string>();       // group IDs folded
let summary: ScanSummary | null = null;
let stale = false;                          // settings changed after the scan
let showAllApps = false;
```

Behaviour (spec §7):
- Groups in order `system, apps, browsers, updates, dev, review, custom`; a group appears only if it has items; header: tri-state checkbox, title `t("junk.group.<id>")`, `共 X · 已选 Y`, fold caret.
- Item row: checkbox (disabled when `running` or `denied` or phase ≠ scanned), name (`t("rule.<id>.name")` for rule IDs, `item.name` otherwise), tags (`运行中` / `需要授权` / `移到废纸篓` for `trash` / `最多` for `estimate`), one-line description (`t("rule.<id>.desc")`; for app items the first path; for custom items the path), size (spinner while pending), `⋯` button opening a small menu with 「在 Finder 中显示」 (first path) and 「不再清理」 (append the ID to `settings.ignored`, `saveSettings`, drop the row, toast), expand caret when there is `top` or `subs`.
- Expanded: `subs` → one row each with its own checkbox (in `subSel`), name, detail, size, and a 「保留最新」 tag for unselected device-support versions; otherwise `top` entries with sizes.
- Selection defaults after a scan: `item.default && !running && !denied && size > 0`; for items with `subs`, `subSel` starts as the subs with `default`.
- Apps group: sort by size; show the first 15 and a row 「其余 N 个应用（共 X）」 that sets `showAllApps`.
- Hero: totals exclude the review group; when the review group has a total, the sub-line adds 「另有 X 需要你确认」. Scanning and cleaning heroes, progress bar, Stop, completion summary and Rescan behave as today.
- Banner above the list when `summary && !summary.fullDiskAccess`: 「授予完全磁盘访问权限后，还能扫描：废纸篓、N 个沙盒应用的缓存、iOS 备份、邮件附件」 with a 「去授权」 button.
- Stale bar when `stale && phase === "scanned"`: 「设置已更改 · 重新扫描」.
- Review group extra row when `summary.leftovers > 0`: 「已卸载应用的残留 N 项 · X」 with 「去查看」 calling `openLeftovers`.
- Footer: 「已检查 N 项，没有发现的不显示」 + (if ignored) 「· 已忽略 N 项 管理」 linking to settings.
- Cleaning: `cleanJunk(selections)` where each selection is `{id}` or `{id, paths}` for items with subs; row states as today (waiting, cleaning, done ✓ with freed/trashed, failed count, skipped reasons: `running` 「运行中，已跳过」, `busy` 「工具正在运行，已跳过」, `no-tool` 「没有找到工具」, `timeout` 「超时，已跳过」).

i18n (`src/i18n.ts`, zh and en): `junk.group.{system,apps,browsers,updates,dev,review,custom}`; `rule.<id>.name` and `rule.<id>.desc` for every rule in Task 3's table and Task 6's commands (wording from spec §5.3, e.g. `rule.dev.npx.name` 「npx 缓存」/"npx cache", `rule.dev.npx.desc` 「npx 下载的包，下次运行时重新下载」/"Packages npx fetched; fetched again next run"); tags; banner; stale bar; leftovers row; footer; skip reasons; menu items; 「其余 N 个应用（共 X）」. Add `ruleName(id)`/`ruleDesc(id)` helpers that fall back to the ID when a key is missing.

- [ ] **Step 1:** Implement `src/junk.ts` and wire `initJunk()` from `main.ts` (replacing `initIdleClean()` and the old listeners).
- [ ] **Step 2:** `bun run generate` (if needed), `bun run typecheck`, `bun run build:web` pass. The reviewer checks the page in the browser harness (all phases, zh/en, dark mode).

---

### Task 10: Grouped sidebar and the Apps page tabs

**Files:** Modify `index.html`, `src/main.ts`, `src/i18n.ts`, `src/style.css`

- Sidebar (spec §4): 垃圾清理; section label 「空间」: 空间分析 (`data-view="fullscan"`, renamed), 大文件, 相似照片; section label 「应用」: 应用卸载; separator; 设置; version label at the bottom. Section labels are not buttons. Remove the 应用缓存 and 残留清理 nav items and their views (`#view-appcaches`, `#view-orphans` move into the Apps view).
- Apps view: tabs 「已安装」 | 「残留（N）」 under the header. 「已安装」 = today's list (search, sort, rows). 「残留」 = today's leftovers UI (summary card, progress, toolbar, list) moved inside the view; the first switch to the tab starts the leftovers scan; the nav spinner for a leftovers scan shows on 应用卸载. `openLeftovers` (from `src/junk.ts`) = switch to Apps and select the 残留 tab.
- Delete the App Caches page code (`startAppCacheScan`, `renderAppCacheRows`, `acItems`…) and `lists.appcaches`.
- i18n: `nav.section.space` 「空间」/"Space", `nav.section.apps` 「应用」/"Apps", `nav.fullscan` 「空间分析」/"Space Analysis", `apps.tab.installed` 「已安装」/"Installed", `apps.tab.leftovers` 「残留」/"Leftovers"; remove strings only the deleted page used.
- [ ] **Step 1:** Implement. **Step 2:** `bun run typecheck`, `bun run build:web` pass.

---

### Task 11: The settings page

**Files:** Create `src/settings.ts`; modify `index.html` (`#view-settings` markup), `src/main.ts` (remove the old category editor, `catConfig`, `saveCatsBtn`, `loadCatConfig`, `renderCatEditor`), `src/i18n.ts`, `src/style.css`

**Interfaces:** exports `showSettings(): void` (called when the view opens; loads `getSettings()` and `fullDiskAccess()`), `refreshSettingsI18n(): void`. Calls `markJunkStale()` after a saved change.

Layout (spec §9), sections with headings: 通用 (language select — moved here, still toasts 「界面语言已切换」); 垃圾清理: 扫描范围 (six checkboxes bound to `disabledGroups`), 不再清理的项目 (list of ignored IDs shown with `ruleName`/app name fallback to ID, each with 「恢复」; empty text 「在垃圾清理里点「⋯ → 不再清理」加入」), 自定义目录 (rows: name input, path text, 启用 checkbox, 移除; 「＋ 添加目录…」 calls `pickFolder()`; note 「清理时移到废纸篓」); 权限 (完全磁盘访问权限: 已授权/未授权 dot + 「打开系统设置」, re-checked on window focus); 关于 (版本).
- Every change calls `saveSettings(next)`; on error show `errText(e)` under the affected row and restore the previous value; on success `markJunkStale()`. Name inputs save on blur/Enter. No 「保存设置」 button.
- i18n for every label above plus the new codes `folder-empty`, `folder-protected`, `folder-dup`, `folder-missing`, `group-unknown` in `errText`.
- [ ] **Step 1:** Implement. **Step 2:** `bun run typecheck`, `bun run build:web` pass.

---

### Task 12: Remove the old model; docs and version

**Files:** Modify `main.go`, `main_test.go`, `src/mygo.ts` (regenerated), `README.md`, `mygo.config.ts`

- Delete `Scan`, `Clean`, `ScanStream`, `CleanStream`, `ScanAppCaches`, `GetCategories`, `SaveCategories`, `ScanEvent`, `CleanEvent`, `AppCacheItem`, `CleanSummary`/`CleanError` if unused, `Category`, `scanCategories`, `scanStream`, `cleanCategory*`, `cleanStream`, `planCategory`, `categoryPlan`, `loadCategories`, `excludeNested`, `defaultCategoryConfigs` (keep a private copy for `isBuiltin` in migration, or move `isBuiltin` + the defaults into `settings.go`), `appNamesByBundleID` if unused. Port any still-meaningful test to the new engine (Trash ordering, `scannedAt`, read-only trees, protected flags, special files, nested exclusions) — no lost coverage.
- `bun run generate`; `bun run typecheck`; `bun run build:web`.
- README: features list per the spec; version `0.1.3` in `mygo.config.ts` and the README examples.
- [ ] **Step 1:** Implement. **Step 2:** full verification (all five commands). The reviewer runs the real-machine read-only probe (spec §11) and the browser checks, then asks the user before building and releasing.

---

## Execution batches (Codex runs)

| Run | Tasks |
|---|---|
| A | 1, 2 |
| B | 3, 4, 5 |
| C | 6, 7 |
| D | 8, 9 |
| E | 10, 11 |
| F | 12 |
