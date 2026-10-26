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
	add("com.apple.dock", "Dock")
	keys := newAppKeys()
	keys.add(appIdentity{folder: "Lark", name: "Lark", bid: "com.electron.lark"})
	keys.add(appIdentity{folder: "WeChat", name: "微信", bid: "com.tencent.xinWeChat"})
	keys.add(appIdentity{folder: "Google Chrome", name: "Google Chrome", bid: "com.google.Chrome"})
	// Dock has a bundle here only to show that an Apple app never gets one.
	paths := map[string]string{
		"com.electron.lark": filepath.Join("/Applications", "Lark.app"),
		"com.apple.dock":    filepath.Join("/System", "Library", "CoreServices", "Dock.app"),
	}
	return growthAttributor{idx: idx, keys: keys, paths: paths}
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
		// an Apple app found by its folder name counts for macOS under its own name, with nothing to uninstall
		{filepath.Join(lib, "Application Support", "Dock"), growthHome, "macos:com.apple.dock", "Dock", "macos", false, ""},
		{filepath.Join(lib, "Caches", "Google", "Chrome"), growthHome, "app:com.google.chrome", "Google Chrome", "app", false, ""},
		// a vendor's child sits one level below the folders the leftovers tab scans: unknown, but not a leftover
		{filepath.Join(lib, "Caches", "Google", "DriveFS"), growthHome, dir(filepath.Join(lib, "Caches", "Google", "DriveFS")), filepath.Join("Google", "DriveFS"), "unknown", false, ""},
		{filepath.Join(lib, "Application Support", "Trae CN"), growthHome, "app:com.trae.cn", "Trae CN", "app", false, ""},
		{filepath.Join("/h", ".npm"), growthDot, dir(filepath.Join("/h", ".npm")), ".npm", "dot", false, ""},
		{filepath.Join(lib, "Application Support", "GoneApp"), growthHome, dir(filepath.Join(lib, "Application Support", "GoneApp")), "GoneApp", "unknown", true, ""},
		// the leftovers tab skips tool caches, Apple's own data and hidden items
		{filepath.Join(lib, "Caches", "Homebrew"), growthHome, dir(filepath.Join(lib, "Caches", "Homebrew")), "Homebrew", "unknown", false, ""},
		{filepath.Join(lib, "Application Support", "AddressBook"), growthHome, dir(filepath.Join(lib, "Application Support", "AddressBook")), "AddressBook", "unknown", false, ""},
		{filepath.Join(lib, "Application Support", ".tool"), growthHome, dir(filepath.Join(lib, "Application Support", ".tool")), ".tool", "unknown", false, ""},
		// and it scans neither Developer nor Group Containers, nor a Caches folder outside ~/Library
		{filepath.Join(lib, "Developer", "CoreSimulator"), growthHome, dir(filepath.Join(lib, "Developer", "CoreSimulator")), "CoreSimulator", "unknown", false, ""},
		{filepath.Join(lib, "Group Containers", "ABCDE12345.com.gone.app"), growthHome, dir(filepath.Join(lib, "Group Containers", "ABCDE12345.com.gone.app")), "ABCDE12345.com.gone.app", "unknown", false, ""},
		{filepath.Join("/h", "Caches", "GoneApp"), growthHome, dir(filepath.Join("/h", "Caches", "GoneApp")), "GoneApp", "unknown", false, ""},
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

// TestGrowthOwnerLeftoverCandidate: the leftovers tab skips com.apple. folders
// too. owner() sends those to macOS before it asks, so this asks directly.
func TestGrowthOwnerLeftoverCandidate(t *testing.T) {
	lib := filepath.Join("/h", "Library")
	for path, want := range map[string]bool{
		filepath.Join(lib, "Caches", "GoneApp"):        true,
		filepath.Join(lib, "Caches", "com.apple.geod"): false,
	} {
		if got := growthLeftoverCandidate(path); got != want {
			t.Errorf("growthLeftoverCandidate(%q) = %v, want %v", path, got, want)
		}
	}
}
