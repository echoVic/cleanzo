package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// TestSettingsMigrateFromCategories keeps edited folders and disabled built-ins.
func TestSettingsMigrateFromCategories(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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

// TestSettingsLoadVersions reads legacy lists, v3 settings and unusable files.
func TestSettingsLoadVersions(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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
	setHome(t, t.TempDir())
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

// TestSettingsSavePreservesConfig leaves the saved file intact on failure.
func TestSettingsSavePreservesConfig(t *testing.T) {
	setHome(t, t.TempDir())
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
	setHome(t, t.TempDir())
	mustMkdir(t, filepath.Dir(configPath()))
	if err := os.WriteFile(configPath(), []byte(`{"version":3,"ignored":["dev.npm"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (Cleaner{}).GetSettings(); !slices.Equal(got.Ignored, []string{"dev.npm"}) {
		t.Fatalf("plain v3 settings = %+v", got)
	}
}
