package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// ThemeSettings is callable from the frontend to read and change the app's
// appearance.
type ThemeSettings struct{}

const (
	themeSystem = "system"
	themeLight  = "light"
	themeDark   = "dark"
)

// themePath returns where the appearance preference is persisted. It lives
// beside the cleaner settings, in the app's config folder.
func themePath() string {
	return filepath.Join(filepath.Dir(configPath()), "appearance.json")
}

// themeFile is the persisted appearance preference.
type themeFile struct {
	Theme string `json:"theme"`
}

// normalizeTheme accepts only the three appearance modes.
func normalizeTheme(preference string) (string, error) {
	switch preference {
	case themeSystem, themeLight, themeDark:
		return preference, nil
	default:
		return "", errors.New("unsupported theme preference")
	}
}

// loadThemePreference reads the saved mode, falling back to the system.
func loadThemePreference() string {
	data, err := os.ReadFile(themePath())
	if err != nil {
		return themeSystem
	}
	var f themeFile
	if json.Unmarshal(data, &f) != nil {
		return themeSystem
	}
	preference, err := normalizeTheme(f.Theme)
	if err != nil {
		return themeSystem
	}
	return preference
}

// saveThemePreference validates and persists an appearance mode.
func saveThemePreference(preference string) error {
	preference, err := normalizeTheme(preference)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(themeFile{Theme: preference}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomically(themePath(), data)
}

// setThemeSource is a variable so tests can observe the native change.
var setThemeSource = mygo.Theme.SetSource

// GetTheme returns the saved appearance mode.
func (ThemeSettings) GetTheme() string { return loadThemePreference() }

// SetTheme persists and applies a new appearance mode. "system" follows the
// desktop, while "light" and "dark" also update the window controls and the
// page's prefers-color-scheme.
func (ThemeSettings) SetTheme(preference string) error {
	if err := saveThemePreference(preference); err != nil {
		return err
	}
	setThemeSource(mygo.ThemeSource(preference))
	return nil
}
