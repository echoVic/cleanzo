package main

import (
	"os"
	"testing"

	"github.com/egoist/mygo"
)

func TestThemePreferencePersistsAndFallsBackToSystem(t *testing.T) {
	setHome(t, t.TempDir())
	if got := loadThemePreference(); got != themeSystem {
		t.Fatalf("missing appearance file = %q, want %q", got, themeSystem)
	}
	for _, want := range []string{themeDark, themeLight, themeSystem} {
		if err := saveThemePreference(want); err != nil {
			t.Fatalf("saveThemePreference(%q): %v", want, err)
		}
		if got := loadThemePreference(); got != want {
			t.Fatalf("loadThemePreference() = %q, want %q", got, want)
		}
	}
	if err := saveThemePreference("sepia"); err == nil {
		t.Fatal("unsupported appearance saved")
	}
	if got := loadThemePreference(); got != themeSystem {
		t.Fatalf("invalid save changed preference to %q", got)
	}
	for _, data := range []string{`{"theme":"sepia"}`, `{"theme":42}`, `{`} {
		if err := os.WriteFile(themePath(), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := loadThemePreference(); got != themeSystem {
			t.Fatalf("appearance %q = %q, want %q", data, got, themeSystem)
		}
	}
}

func TestThemeSettingsPersistsAndAppliesNativeSource(t *testing.T) {
	setHome(t, t.TempDir())
	previous := setThemeSource
	t.Cleanup(func() { setThemeSource = previous })
	var applied []mygo.ThemeSource
	setThemeSource = func(source mygo.ThemeSource) { applied = append(applied, source) }

	settings := ThemeSettings{}
	if err := settings.SetTheme(themeDark); err != nil {
		t.Fatal(err)
	}
	if got := settings.GetTheme(); got != themeDark {
		t.Fatalf("GetTheme() = %q, want %q", got, themeDark)
	}
	if len(applied) != 1 || applied[0] != mygo.ThemeDark {
		t.Fatalf("native theme sources = %v, want [dark]", applied)
	}
	if err := settings.SetTheme("sepia"); err == nil {
		t.Fatal("unsupported appearance applied")
	}
	if len(applied) != 1 {
		t.Fatalf("invalid appearance reached native theme: %v", applied)
	}
	if got := settings.GetTheme(); got != themeDark {
		t.Fatalf("invalid appearance changed saved mode to %q", got)
	}
}
