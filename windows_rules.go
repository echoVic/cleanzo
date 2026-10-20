package main

import "time"

// windowsJunkRules claims explicit caches before generic Electron app caches.
var windowsJunkRules = []Rule{
	{ID: "review.installers", Group: groupReview, Trash: true, Detail: detailInstallers, Targets: windowsInstallerTargets()},
	{ID: "review.partial-downloads", Group: groupReview, Trash: true, Detail: detailFiles, MinAge: 24 * time.Hour,
		Targets: []Target{{Pattern: "~/Downloads", Mode: entriesOf, Names: []string{"*.crdownload", "*.part", "*.partial"}}}},
	{ID: "browser.chrome", Group: groupBrowsers, Default: true, Owners: []string{"chrome"}, Targets: windowsChromiumCaches("$LOCALAPPDATA/Google/Chrome/User Data/*")},
	{ID: "browser.edge", Group: groupBrowsers, Default: true, Owners: []string{"msedge"}, Targets: windowsChromiumCaches("$LOCALAPPDATA/Microsoft/Edge/User Data/*")},
	{ID: "browser.brave", Group: groupBrowsers, Default: true, Owners: []string{"brave"}, Targets: windowsChromiumCaches("$LOCALAPPDATA/BraveSoftware/Brave-Browser/User Data/*")},
	{ID: "browser.vivaldi", Group: groupBrowsers, Default: true, Owners: []string{"vivaldi"}, Targets: windowsChromiumCaches("$LOCALAPPDATA/Vivaldi/User Data/*")},
	{ID: "browser.opera", Group: groupBrowsers, Default: true, Owners: []string{"opera"}, Targets: []Target{
		{Pattern: "$LOCALAPPDATA/Opera Software/Opera Stable/Cache", Mode: entriesOf},
		{Pattern: "$APPDATA/Opera Software/Opera Stable/Code Cache", Mode: entriesOf},
		{Pattern: "$APPDATA/Opera Software/Opera Stable/GPUCache", Mode: entriesOf}}},
	{ID: "browser.firefox", Group: groupBrowsers, Default: true, Owners: []string{"firefox"}, Targets: []Target{{Pattern: "$LOCALAPPDATA/Mozilla/Firefox/Profiles/*/cache2", Mode: entriesOf}}},
	{ID: "browser.qq", Group: groupBrowsers, Default: true, Owners: []string{"qqbrowser"}, Targets: windowsChromiumCaches("$LOCALAPPDATA/Tencent/QQBrowser/User Data/*")},
	{ID: "update.updaters", Group: groupUpdates, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/*-updater/pending", Mode: entriesOf}}},
	{ID: "dev.npm", Group: groupDev, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/npm-cache/_cacache", Mode: entriesOf}, {Pattern: "$LOCALAPPDATA/npm-cache/_logs", Mode: entriesOf}}},
	{ID: "dev.npx", Group: groupDev, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/npm-cache/_npx", Mode: entriesOf}}},
	{ID: "dev.pnpm", Group: groupDev, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/pnpm-cache", Mode: entriesOf}}},
	{ID: "dev.pnpm-store", Group: groupDev, Default: true, Command: &windowsPnpmPrune},
	{ID: "dev.yarn", Group: groupDev, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/Yarn/Cache", Mode: entriesOf}, {Pattern: "~/.yarn/berry/cache", Mode: entriesOf}}},
	{ID: "dev.bun", Group: groupDev, Default: true, Targets: []Target{{Pattern: "~/.bun/install/cache", Mode: entriesOf}}},
	{ID: "dev.node-gyp", Group: groupDev, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/node-gyp/Cache", Mode: entriesOf}}},
	{ID: "dev.electron", Group: groupDev, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/electron/Cache", Mode: entriesOf}, {Pattern: "$LOCALAPPDATA/electron-builder/Cache", Mode: entriesOf}}},
	{ID: "dev.typescript", Group: groupDev, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/Microsoft/TypeScript", Mode: entriesOf}}},
	{ID: "dev.playwright", Group: groupDev, Targets: []Target{{Pattern: "$LOCALAPPDATA/ms-playwright", Mode: entriesOf}}},
	{ID: "dev.pip", Group: groupDev, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/pip/Cache", Mode: entriesOf}}},
	{ID: "dev.uv", Group: groupDev, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/uv/cache", Mode: entriesOf}}},
	{ID: "dev.poetry", Group: groupDev, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/pypoetry/Cache/cache", Mode: entriesOf}, {Pattern: "$LOCALAPPDATA/pypoetry/Cache/artifacts", Mode: entriesOf}}},
	{ID: "dev.cargo", Group: groupDev, Default: true, Targets: []Target{{Pattern: "~/.cargo/registry/cache", Mode: entriesOf}, {Pattern: "~/.cargo/registry/src", Mode: entriesOf}, {Pattern: "~/.cargo/git/checkouts", Mode: entriesOf}}},
	{ID: "dev.go-build", Group: groupDev, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/go-build", Mode: entriesOf}}},
	{ID: "dev.go-mod", Group: groupDev, Command: &windowsGoModCleanup},
	{ID: "dev.android", Group: groupDev, Default: true, Targets: []Target{{Pattern: "~/.android/cache", Mode: entriesOf}, {Pattern: "~/.android/build-cache", Mode: entriesOf}}},
	{ID: "dev.gradle", Group: groupDev, Targets: []Target{{Pattern: "~/.gradle/caches", Mode: entriesOf}}},
	{ID: "dev.maven", Group: groupDev, Targets: []Target{{Pattern: "~/.m2/repository", Mode: entriesOf}}},
	{ID: "dev.nuget", Group: groupDev, Default: true, Targets: []Target{{Pattern: "$LOCALAPPDATA/NuGet/v3-cache", Mode: entriesOf}}},
	{ID: "sys.crashdumps", Group: groupSystem, Default: true, Detail: detailCrashpad, Contents: true, Targets: []Target{
		{Pattern: "$LOCALAPPDATA/*/Crashpad", Mode: matched}, {Pattern: "$LOCALAPPDATA/*/*/Crashpad", Mode: matched},
		{Pattern: "$LOCALAPPDATA/*/User Data/Crashpad", Mode: matched}, {Pattern: "$LOCALAPPDATA/*/*/User Data/Crashpad", Mode: matched},
		{Pattern: "$APPDATA/*/Crashpad", Mode: matched}}},
	{ID: "sys.wer", Group: groupSystem, Default: true, Targets: []Target{
		{Pattern: "$LOCALAPPDATA/Microsoft/Windows/WER/ReportArchive", Mode: entriesOf},
		{Pattern: "$LOCALAPPDATA/Microsoft/Windows/WER/ReportQueue", Mode: entriesOf},
		{Pattern: "$LOCALAPPDATA/CrashDumps", Mode: entriesOf, Names: []string{"*.dmp"}}}},
	{ID: "sys.shadercache", Group: groupSystem, Default: true, Targets: []Target{
		{Pattern: "$LOCALAPPDATA/D3DSCache", Mode: entriesOf}, {Pattern: "$LOCALAPPDATA/NVIDIA/DXCache", Mode: entriesOf},
		{Pattern: "$LOCALAPPDATA/NVIDIA/GLCache", Mode: entriesOf}, {Pattern: "$LOCALAPPDATA/AMD/DxCache", Mode: entriesOf}}},
	{ID: "sys.temp", Group: groupSystem, Default: true, MinAge: tempMinAge, Targets: []Target{{Pattern: "$TMPDIR", Mode: entriesOf}}},
	{ID: "sys.trash", Group: groupSystem, RecycleBin: true},
}

// windowsChromiumCaches adds Cache without changing the macOS helper's target list.
func windowsChromiumCaches(profiles string) []Target {
	return append([]Target{{Pattern: profiles + "/Cache", Mode: entriesOf}}, chromiumProfileCaches(profiles)...)
}

// windowsInstallerTargets restricts recursive review to installer filenames.
func windowsInstallerTargets() []Target {
	var targets []Target
	for _, folder := range []string{"~/Downloads", "~/Desktop", "~/Documents"} {
		targets = append(targets, Target{Pattern: folder, Mode: recursiveFiles, Names: []string{"*.msi", "*.msix", "*.msixbundle", "*.appx", "*.appxbundle", "*setup*.exe", "*install*.exe"}})
	}
	return targets
}
