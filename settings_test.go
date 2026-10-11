package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestSettingsMigrateFromCategories keeps edited folders and disabled built-ins.
func TestSettingsMigrateFromCategories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	builtins := legacyCategoryDefaults()
	var cfg []legacyCategoryConfig
	for _, c := range builtins {
		switch c.Name {
		case "trash", "npm", "caches":
			c.Enabled = false // the user had turned these off
		case "logs":
			c.Paths = []string{filepath.Join(home, "logs-elsewhere")} // edited paths: a custom folder now
		}
		cfg = append(cfg, c)
	}
	cfg = append(cfg, legacyCategoryConfig{Name: "旧安装包", Paths: []string{filepath.Join(home, "Downloads", "installers"), filepath.Join(home, "old")}, Enabled: true})
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

// TestSettingsLoadReadsV2AndWritesV3 round-trips a migrated category.
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

// TestSettingsValidation rejects invalid folders and normalizes settings.
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

// TestFullDiskAccessProbe requires a readable protected folder.
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

// TestSettingsLoadVersions reads legacy lists, v3 settings and unusable files.
func TestSettingsLoadVersions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mustMkdir(t, filepath.Dir(configPath()))
	if got := loadSettings(); !reflect.DeepEqual(got, Settings{}) {
		t.Fatalf("missing config = %+v", got)
	}
	for _, tc := range []struct {
		name, data string
		want       Settings
	}{
		{"v1", `[{"name":"old","paths":["~/old"],"enabled":false}]`, Settings{Custom: []CustomFolder{{Name: "old", Path: "~/old", Enabled: false}}}},
		{"v1 empty", `[]`, Settings{}},
		{"v2 empty", `{"version":2,"categories":[]}`, Settings{}},
		{"v3", `{"version":3,"disabledGroups":["browsers"],"ignored":["dev.gradle"],"custom":[{"name":"old","path":"~/old","enabled":false}]}`, Settings{DisabledGroups: []string{"browsers"}, Ignored: []string{"dev.gradle"}, Custom: []CustomFolder{{Name: "old", Path: "~/old", Enabled: false}}}},
		{"malformed", `{"version":3,`, Settings{}},
		{"wrong type", `{"version":3,"ignored":42}`, Settings{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(configPath(), []byte(tc.data), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := (Cleaner{}).GetSettings(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestSettingsMigrateDisabledRules keeps every disabled built-in choice.
func TestSettingsMigrateDisabledRules(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := legacyCategoryDefaults()
	for i := range cfg {
		cfg[i].Enabled = false
	}
	s := migrateCategories(cfg)
	want := []string{"sys.logs", "sys.temp", "sys.trash", "dev.npm", "dev.npx", "dev.go-build", "dev.pip", "dev.homebrew", "dev.xcode"}
	if !slices.Equal(s.Ignored, want) || !slices.Equal(s.DisabledGroups, []string{"apps"}) || len(s.Custom) != 0 {
		t.Fatalf("disabled built-ins = %+v", s)
	}
	cfg = []legacyCategoryConfig{{Name: "logs", Paths: []string{" ", "~/elsewhere"}, Enabled: false}}
	if got := migrateCategories(cfg); !slices.Equal(got.Custom, []CustomFolder{{Name: "logs", Path: "~/elsewhere", Enabled: false}}) {
		t.Fatalf("edited disabled category = %+v", got)
	}
}

// TestSettingsNormalizeCopy preserves caller data and detects path aliases.
func TestSettingsNormalizeCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
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

// TestSettingsSavePreservesConfig leaves the saved file intact on failure.
func TestSettingsSavePreservesConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := Settings{DisabledGroups: []string{"apps"}, Ignored: []string{"dev.npm"}}
	if err := (Cleaner{}).SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := (Cleaner{}).SaveSettings(Settings{DisabledGroups: []string{"unknown"}}); err == nil {
		t.Fatal("invalid settings saved")
	}
	mustMkdir(t, configPath()+".tmp")
	if err := writeSettings(Settings{}); err == nil {
		t.Fatal("write should fail when temporary file is a directory")
	}
	after, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("failed save changed config: %s", after)
	}
}

// TestSettingsPlainV3 reads settings without a legacy categories field.
func TestSettingsPlainV3(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mustMkdir(t, filepath.Dir(configPath()))
	if err := os.WriteFile(configPath(), []byte(`{"version":3,"ignored":["dev.npm"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (Cleaner{}).GetSettings(); !slices.Equal(got.Ignored, []string{"dev.npm"}) {
		t.Fatalf("plain v3 settings = %+v", got)
	}
}
