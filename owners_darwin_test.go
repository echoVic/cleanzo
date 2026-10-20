package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppIndexLookup finds owners by ID, name and bundle ID prefix.
func TestAppIndexLookup(t *testing.T) {
	dir := t.TempDir()
	makeApp(t, dir, "Trae CN.app", "cn.trae.app", "Trae CN")
	makeApp(t, dir, "语雀.app", "com.yuque.app", "语雀")
	x := buildAppIndex([]string{dir})
	cases := map[string]string{
		"cn.trae.app":        "Trae CN", // bundle ID
		"CN.TRAE.APP":        "Trae CN", // any case
		"cn.trae.app.ShipIt": "Trae CN", // bundle ID prefix
		"Trae CN":            "Trae CN", // display name
		"语雀":                 "语雀",
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

// TestAppIndexLookupAliases reads all names and falls back to bundle or folder names.
func TestAppIndexLookupAliases(t *testing.T) {
	dir := t.TempDir()
	app := makeApp(t, dir, "Yuque Folder.app", "COM.YUQUE.APP", "语雀")
	plist := filepath.Join(app, "Contents", "Info.plist")
	data, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "</dict>", "<key>CFBundleName</key><string>Yuque Bundle</string><key>CFBundleExecutable</key><string>yuque-desktop</string></dict>", 1))
	if err := os.WriteFile(plist, data, 0o644); err != nil {
		t.Fatal(err)
	}
	fallback := makeApp(t, dir, "Other Folder.app", "com.other.app", "")
	plist = filepath.Join(fallback, "Contents", "Info.plist")
	data, err = os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "</dict>", "<key>CFBundleName</key><string>Other Bundle</string></dict>", 1))
	if err := os.WriteFile(plist, data, 0o644); err != nil {
		t.Fatal(err)
	}
	makeApp(t, dir, "Folder Only.app", "com.folder.app", "")
	makeApp(t, dir, "No ID.app", "", "No Owner")
	makeApp(t, dir, "Not An App", "com.not.app", "Not An App")
	makeApp(t, filepath.Join(dir, "Nested"), "Hidden.app", "com.nested.app", "Hidden")
	broken := makeApp(t, dir, "Broken.app", "com.broken.app", "Broken")
	if err := os.WriteFile(filepath.Join(broken, "Contents", "Info.plist"), []byte("not a plist"), 0o644); err != nil {
		t.Fatal(err)
	}
	x := buildAppIndex([]string{filepath.Join(dir, "missing"), dir})
	for _, key := range []string{"com.yuque.app", "语雀", "YUQUE BUNDLE", "yuque-desktop", "Yuque Folder"} {
		if got, ok := x.lookup(key); !ok || got != (appRef{BundleID: "COM.YUQUE.APP", Name: "语雀"}) {
			t.Errorf("lookup(%q) = %+v, %v", key, got, ok)
		}
	}
	for key, name := range map[string]string{"com.other.app": "Other Bundle", "Other Folder": "Other Bundle", "com.folder.app": "Folder Only"} {
		if got, ok := x.lookup(key); !ok || got.Name != name {
			t.Errorf("lookup(%q) = %+v, %v; want %q", key, got, ok, name)
		}
	}
	for _, key := range []string{"", "No Owner", "No ID", "com.not.app", "com.nested.app", "com.broken.app", "yuque-desktop.helper", "Yuque Bundle.helper", "Yuque Folder.helper"} {
		if got, ok := x.lookup(key); ok {
			t.Errorf("lookup(%q) found unexpected owner %+v", key, got)
		}
	}
	if got, ok := buildAppIndex(nil).lookup("unknown"); ok {
		t.Fatalf("empty index = %+v", got)
	}
}

// TestAppIndexLookupLongestBundleID matches the most specific dot-delimited ID.
func TestAppIndexLookupLongestBundleID(t *testing.T) {
	dir := t.TempDir()
	makeApp(t, dir, "Parent.app", "com.vendor.app", "Parent")
	makeApp(t, dir, "Child.app", "com.vendor.app.helper", "Child")
	makeApp(t, dir, "Exact.app", "com.exact.app", "com.vendor.app.helper.cache")
	x := buildAppIndex([]string{dir})
	for key, want := range map[string]string{
		"COM.VENDOR.APP.HELPER.DATA":  "Child",
		"com.vendor.app.helperish":    "Parent",
		"com.vendor.app.ShipIt":       "Parent",
		"com.vendor.app.helper.cache": "com.vendor.app.helper.cache",
	} {
		if got, ok := x.lookup(key); !ok || got.Name != want {
			t.Errorf("lookup(%q) = %+v, %v; want %s", key, got, ok, want)
		}
	}
	if got, ok := x.lookup("com.vendor.application"); ok {
		t.Fatalf("non-delimited prefix matched %+v", got)
	}
}

// TestAppIndexLookupDeterministic keeps the first sorted path when keys collide.
func TestAppIndexLookupDeterministic(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "a"), filepath.Join(root, "z")
	makeApp(t, first, "First.app", "com.shared.app", "First")
	makeApp(t, second, "Duplicate.app", "com.shared.app", "Second")
	for i := range 20 {
		makeApp(t, first, fmt.Sprintf("Shared%02d.app", i), fmt.Sprintf("com.shared.alias%d", i), "Shared")
	}
	for range 3 {
		x := buildAppIndex([]string{second, first})
		if got, ok := x.lookup("COM.SHARED.APP"); !ok || got.Name != "First" {
			t.Fatalf("duplicate bundle ID = %+v, %v", got, ok)
		}
		if got, ok := x.lookup("Shared"); !ok || got.BundleID != "com.shared.alias0" {
			t.Fatalf("duplicate alias = %+v, %v", got, ok)
		}
	}
}

// TestAppIndexSkipsSharedExecutable: Electron apps that keep the stock
// executable name must not claim folders named "Electron".
func TestAppIndexSkipsSharedExecutable(t *testing.T) {
	dir := t.TempDir()
	app := makeApp(t, dir, "Visual Studio Code.app", "com.microsoft.VSCode", "Code")
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>CFBundleDisplayName</key><string>Code</string>
<key>CFBundleExecutable</key><string>Electron</string>
<key>CFBundleIdentifier</key><string>com.microsoft.VSCode</string>
</dict></plist>`
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	x := buildAppIndex([]string{dir})
	if _, ok := x.lookup("Electron"); ok {
		t.Fatal(`"Electron" names no app`)
	}
	if got, ok := x.lookup("Code"); !ok || got.BundleID != "com.microsoft.VSCode" {
		t.Fatalf("lookup(Code) = %+v, %v", got, ok)
	}
}
