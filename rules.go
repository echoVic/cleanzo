package main

import "time"

// junkGroup names a junk page group.
type junkGroup string

// Junk groups separate caches from items requiring review.
const (
	groupSystem   junkGroup = "system"
	groupApps     junkGroup = "apps"
	groupBrowsers junkGroup = "browsers"
	groupUpdates  junkGroup = "updates"
	groupDev      junkGroup = "dev"
	groupReview   junkGroup = "review"
	groupCustom   junkGroup = "custom"
)

// targetMode chooses directory contents or matched paths.
type targetMode int

// Target modes describe what a rule removes.
const (
	entriesOf targetMode = iota
	matched
)

// Target locates a rule's entries, optionally filtering their names.
type Target struct {
	Pattern string
	Mode    targetMode
	Names   []string
}

// detailKind chooses separately selectable item metadata.
type detailKind int

// Detail kinds describe the item's sub-items.
const (
	detailNone detailKind = iota
	detailFiles
	detailBackups
	detailArchives
	detailDeviceSupport
)

// Rule describes a known junk location and how to clean it.
type Rule struct {
	ID             string
	Group          junkGroup
	Targets        []Target
	Owners         []string
	Default, Trash bool
	MinAge         time.Duration
	Detail         detailKind
	Command        *toolCommand
	Reserve        []string // folders the rule cleans only in part; no other item takes the rest
}

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
		Targets: append(append([]Target{{Pattern: "Library/Caches/Google/Chrome", Mode: entriesOf}},
			chromiumProfileCaches("Library/Application Support/Google/Chrome")...),
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
	// Homebrew's API index and compiled code in the same folder cost a slow
	// first run to rebuild, so only downloads are cleaned.
	{ID: "dev.homebrew", Group: groupDev, Default: true, Targets: []Target{{Pattern: "Library/Caches/Homebrew/downloads", Mode: entriesOf}}, Reserve: []string{"Library/Caches/Homebrew"}},
	{ID: "dev.homebrew-old", Group: groupDev, Default: true, Command: &brewCleanup}, // Task 6
	{ID: "dev.pnpm-store", Group: groupDev, Default: true, Command: &pnpmPrune},     // Task 6

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
	for _, d := range []string{"Code Cache", "GPUCache", "ShaderCache", "DawnCache", "DawnGraphiteCache", "DawnWebGPUCache", "GrShaderCache", "GraphiteDawnCache"} {
		ts = append(ts, Target{Pattern: profiles + "/" + d, Mode: entriesOf})
	}
	return ts
}

// protectedPatterns are home-relative paths no rule and no generic scan
// cleans: macOS needs them, or they hold work that is not a cache.
var protectedPatterns = []string{
	"Library/Caches/com.apple.Spotlight*", "Library/Caches/com.apple.spotlight*",
	"Library/Caches/com.apple.FontRegistry*", "Library/Caches/com.apple.finder*",
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
