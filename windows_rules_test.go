package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestWindowsRuleTable validates the portable Windows catalog and its translations.
func TestWindowsRuleTable(t *testing.T) {
	text, err := os.ReadFile("src/i18n.ts")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range windowsJunkRules {
		if r.ID == "" || seen[r.ID] {
			t.Fatalf("duplicate/empty ID %q", r.ID)
		}
		seen[r.ID] = true
		for _, mac := range darwinJunkRules {
			if r.ID == mac.ID && (r.Default != mac.Default || r.Trash != mac.Trash) {
				t.Errorf("%s changed shared default/trash flags", r.ID)
			}
		}
		if !slices.Contains([]junkGroup{groupSystem, groupBrowsers, groupUpdates, groupDev, groupReview}, r.Group) {
			t.Fatalf("group: %+v", r)
		}
		for _, target := range r.Targets {
			root, _, _ := strings.Cut(target.Pattern, "/")
			if !slices.Contains([]string{"~", "$TMPDIR", "$LOCALAPPDATA", "$APPDATA"}, root) {
				t.Errorf("%s root %s", r.ID, root)
			}
		}
		for _, suffix := range []string{"name", "desc"} {
			if strings.Count(string(text), "\"rule."+r.ID+"."+suffix+"\"") != 2 {
				t.Errorf("%s missing bilingual %s", r.ID, suffix)
			}
		}
	}
	if len(seen) < 35 {
		t.Fatalf("incomplete table: %d", len(seen))
	}
}

// TestWindowsRuleExpansion exercises Windows roots and generic apps on either host OS.
func TestWindowsRuleExpansion(t *testing.T) {
	env := testEnv(t)
	env.rules = windowsJunkRules
	env.windowsLayout = true
	env.localAppData = filepath.Join(env.home, "AppData", "Local")
	env.appData = filepath.Join(env.home, "AppData", "Roaming")
	fixtures := map[string]string{
		"browser.chrome":  "AppData/Local/Google/Chrome/User Data/Default/Cache/data",
		"dev.npm":         "AppData/Local/npm-cache/_cacache/data",
		"dev.pip":         "AppData/Local/pip/Cache/data",
		"sys.wer":         "AppData/Local/CrashDumps/test.dmp",
		"sys.shadercache": "AppData/Local/D3DSCache/data",
		"app:dir:fixture": "AppData/Roaming/Fixture/Code Cache/data",
	}
	for _, rel := range fixtures {
		writeFile(t, filepath.Join(env.home, filepath.FromSlash(rel)), 100)
	}
	writeFile(t, filepath.Join(env.appData, "Fixture", "Local State"), 2)
	for _, name := range []string{"Cookies", "History", "Login Data"} {
		writeFile(t, filepath.Join(env.localAppData, "Google", "Chrome", "User Data", "Default", name), 10)
	}
	specs, _ := expandItems(&env)
	for id := range fixtures {
		if !slices.Contains(itemIDs(specs), id) {
			t.Errorf("missing %s: %v", id, itemIDs(specs))
		}
	}
	for _, spec := range specs {
		for _, c := range spec.cands {
			if slices.Contains([]string{"Cookies", "History", "Login Data"}, filepath.Base(c.path)) {
				t.Fatal("persistent browser data offered", c.path)
			}
		}
	}
	env.localAppData = t.TempDir()
	writeFile(t, filepath.Join(env.localAppData, "npm-cache", "_cacache", "outside"), 100)
	if got := env.globTargets("$LOCALAPPDATA/npm-cache/_cacache"); len(got) != 0 {
		t.Fatalf("outside root accepted: %q", got)
	}
}

// TestWindowsInvalidAppDataDoesNotScanMacSources keeps rejected Windows roots from enabling a different catalog's app sources.
func TestWindowsInvalidAppDataDoesNotScanMacSources(t *testing.T) {
	env := testEnv(t)
	env.rules = windowsJunkRules
	env.windowsLayout = true
	writeFile(t, filepath.Join(env.home, "Library", "Caches", "Fixture", "data"), 100)
	specs, _ := expandItems(&env)
	for _, spec := range specs {
		if spec.item.Group == string(groupApps) {
			t.Fatalf("invalid Windows root enabled macOS source: %+v", spec.item)
		}
	}
}
