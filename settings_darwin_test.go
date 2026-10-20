package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestSettingsValidation rejects invalid folders and normalizes settings.
func TestSettingsValidation(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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

// TestFullDiskAccessProbe requires a readable protected folder.
func TestFullDiskAccessProbe(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if fullDiskAccess() {
		t.Fatal("nothing readable: no access")
	}
	mustMkdir(t, filepath.Join(home, "Library", "Safari"))
	if !fullDiskAccess() {
		t.Fatal("a readable protected folder means access")
	}
}

// TestSettingsNormalizeCopy preserves caller data and detects path aliases.
func TestSettingsNormalizeCopy(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	mustMkdir(t, filepath.Join(home, "a"))
	if err := os.Symlink(filepath.Join(home, "a"), filepath.Join(home, "alias")); err != nil {
		t.Fatal(err)
	}
	input := Settings{DisabledGroups: []string{"apps", "apps", "dev"}, Ignored: []string{" dev.npm ", "", "dev.npm", "dev.pip"}, Custom: []CustomFolder{{Path: " ~/a/ ", Enabled: true}, {Path: filepath.Join(home, "Library"), Enabled: false}}}
	before, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := validateSettings(input)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.DisabledGroups, []string{"apps", "dev"}) || !slices.Equal(got.Ignored, []string{"dev.npm", "dev.pip"}) || got.Custom[0].Path != filepath.Join(home, "a") || got.Custom[0].Name != "a" {
		t.Fatalf("normalized = %+v", got)
	}
	after, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("validation modified input: %s -> %s", before, after)
	}
	_, err = validateSettings(Settings{Custom: []CustomFolder{{Path: filepath.Join(home, "a")}, {Path: filepath.Join(home, "alias")}}})
	var ce codeError
	if !errors.As(err, &ce) || ce.Code != "folder-dup" {
		t.Fatalf("alias duplicate = %v", err)
	}
	t.Chdir(home)
	got, err = validateSettings(Settings{Custom: []CustomFolder{{Path: "a/../a", Enabled: true}}})
	if err != nil || got.Custom[0].Path != filepath.Join(home, "a") {
		t.Fatalf("relative path = %+v, %v", got, err)
	}
}

// TestSettingsLoadReadsV2AndWritesV3 round-trips a migrated category.
func TestSettingsLoadReadsV2AndWritesV3(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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
