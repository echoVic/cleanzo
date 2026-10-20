package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo"
)

// Settings holds the junk groups, ignored items and custom folders.
type Settings struct {
	DisabledGroups []string       `json:"disabledGroups"`
	Ignored        []string       `json:"ignored"`
	Custom         []CustomFolder `json:"custom"`
}

// CustomFolder is a user-selected folder whose contents go to the Trash.
type CustomFolder struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
}

// settingsVersion is the config version used by the rule table.
const settingsVersion = 3

// junkGroupNames are the groups a user can turn off.
var junkGroupNames = []string{"system", "apps", "browsers", "updates", "dev", "review"}

// builtinRuleIDs maps old built-in categories to the rules that replace them.
var builtinRuleIDs = map[string][]string{
	"logs": {"sys.logs"}, "temp": {"sys.temp"}, "trash": {"sys.trash"},
	"npm": {"dev.npm", "dev.npx"}, "go": {"dev.go-build"}, "pip": {"dev.pip"},
	"homebrew": {"dev.homebrew"}, "xcode": {"dev.xcode"},
}

// settingsFile is the persisted v3 settings model.
type settingsFile struct {
	Version int `json:"version"`
	Settings
}

// legacyCategoryConfig holds only the fields needed to migrate v1 and v2.
type legacyCategoryConfig struct {
	Name    string   `json:"name"`
	Paths   []string `json:"paths"`
	Enabled bool     `json:"enabled"`
}

// legacyConfigFile is the v2 category wrapper read during migration.
type legacyConfigFile struct {
	Version    int                    `json:"version"`
	Categories []legacyCategoryConfig `json:"categories"`
}

// legacyCategoryDefaults identifies unedited built-ins without restoring them.
func legacyCategoryDefaults() []legacyCategoryConfig {
	h := homeDir()
	return []legacyCategoryConfig{
		{Name: "caches", Enabled: true, Paths: []string{filepath.Join(h, "Library/Caches")}},
		{Name: "logs", Enabled: true, Paths: []string{filepath.Join(h, "Library/Logs")}},
		{Name: "temp", Enabled: true, Paths: []string{os.TempDir()}},
		{Name: "trash", Enabled: true, Paths: []string{filepath.Join(h, ".Trash")}},
		{Name: "npm", Enabled: true, Paths: []string{filepath.Join(h, ".npm/_cacache"), filepath.Join(h, "Library/Caches/pnpm")}},
		{Name: "go", Enabled: true, Paths: []string{filepath.Join(h, "Library/Caches/go-build")}},
		{Name: "pip", Enabled: true, Paths: []string{filepath.Join(h, "Library/Caches/pip")}},
		{Name: "homebrew", Enabled: true, Paths: []string{filepath.Join(h, "Library/Caches/Homebrew")}},
		{Name: "xcode", Enabled: true, Paths: []string{filepath.Join(h, "Library/Developer/Xcode/DerivedData")}},
	}
}

// isBuiltin reports whether a legacy category still has its built-in paths.
func isBuiltin(c legacyCategoryConfig) bool {
	for _, d := range legacyCategoryDefaults() {
		if c.Name == d.Name && samePaths(c.Paths, d.Paths) {
			return true
		}
	}
	return false
}

// samePaths compares legacy paths in order, ignoring trailing separators.
func samePaths(a, b []string) bool {
	return slices.EqualFunc(a, b, func(x, y string) bool { return filepath.Clean(x) == filepath.Clean(y) })
}

// loadSettings reads v3 settings or migrates a saved category config.
func loadSettings() Settings {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return Settings{}
	}
	var f settingsFile
	if json.Unmarshal(data, &f) == nil && f.Version == settingsVersion {
		return f.Settings
	}
	var legacy legacyConfigFile
	if json.Unmarshal(data, &legacy) == nil && legacy.Version == 2 {
		return migrateCategories(legacy.Categories)
	}
	var v1 []legacyCategoryConfig
	if json.Unmarshal(data, &v1) == nil {
		return migrateCategories(v1)
	}
	return Settings{}
}

// migrateCategories keeps disabled choices and turns edited paths into folders.
func migrateCategories(cfg []legacyCategoryConfig) Settings {
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

// validateSettings returns a normalized copy or a translatable validation error.
func validateSettings(s Settings) (Settings, error) {
	out := Settings{}
	for _, g := range s.DisabledGroups {
		if !slices.Contains(junkGroupNames, g) {
			return Settings{}, codeError{Code: "group-unknown", Args: []string{g}}
		}
		if !slices.Contains(out.DisabledGroups, g) {
			out.DisabledGroups = append(out.DisabledGroups, g)
		}
	}
	for _, id := range s.Ignored {
		id = strings.TrimSpace(id)
		if id != "" && !slices.Contains(out.Ignored, id) {
			out.Ignored = append(out.Ignored, id)
		}
	}
	paths := map[string]bool{}
	for _, c := range s.Custom {
		p := strings.TrimSpace(c.Path)
		if p == "" {
			return Settings{}, codeError{Code: "folder-empty"}
		}
		p = expandHome(p)
		abs, err := filepath.Abs(p)
		if err != nil {
			return Settings{}, err
		}
		c.Path = filepath.Clean(abs)
		if c.Enabled && !safeToClean(c.Path) {
			return Settings{}, codeError{Code: "folder-protected", Args: []string{c.Path}}
		}
		key := canonical(c.Path)
		if paths[key] {
			return Settings{}, codeError{Code: "folder-dup", Args: []string{c.Path}}
		}
		paths[key] = true
		if strings.TrimSpace(c.Name) == "" {
			c.Name = filepath.Base(c.Path)
		}
		out.Custom = append(out.Custom, c)
	}
	return out, nil
}

// writeSettings saves only v3 settings with one atomic replacement.
func writeSettings(s Settings) error {
	data, err := json.MarshalIndent(settingsFile{Version: settingsVersion, Settings: s}, "", "  ")
	if err != nil {
		return err
	}
	return writeConfigAtomically(data)
}

// writeConfigAtomically replaces the config only after writing a complete copy.
func writeConfigAtomically(data []byte) error {
	if err := os.MkdirAll(filepath.Dir(configPath()), 0o755); err != nil {
		return err
	}
	tmp := configPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, configPath())
}

// GetSettings returns the saved settings, migrating old categories when needed.
func (Cleaner) GetSettings() Settings { return loadSettings() }

// SaveSettings validates and persists the settings.
func (Cleaner) SaveSettings(s Settings) error {
	clean, err := validateSettings(s)
	if err != nil {
		return err
	}
	return writeSettings(clean)
}

// fullDiskAccess reports whether a protected folder can be read without a prompt.
func fullDiskAccess() bool {
	for _, p := range []string{"Library/Safari", "Library/Mail", "Library/Messages", ".Trash"} {
		if _, err := os.ReadDir(filepath.Join(homeDir(), p)); err == nil {
			return true
		}
	}
	return false
}

// FullDiskAccess reports whether macOS allows reading protected folders.
func (Cleaner) FullDiskAccess() bool { return fullDiskAccess() }

// PickFolder opens a folder panel on the calling window, returning empty on cancel.
func (Cleaner) PickFolder(ctx context.Context) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: mygo.CallerWindow(ctx), Directory: true, CreateDirectories: true, DefaultPath: homeDir(),
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}
