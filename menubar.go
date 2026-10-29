package main

// menubar.go holds the menu bar readings' settings (design:
// docs/superpowers/specs/2026-10-29-menubar-design.md §三, §五, §七): which
// readings show, where that choice is kept, and the bound methods Settings
// calls. menubar_readout.go turns readings into text; menubar_darwin.go makes
// the icons and feeds them; elsewhere the readings are not supported.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo"
)

// menuBarItems are the readings in their order in the menu bar, left to right
// after the Cleanzo icon (spec §三.3).
var menuBarItems = []string{"cpu", "mem", "gpu", "net", "disk", "temp", "battery"}

// menuBarDefault are the readings shown until the user chooses (spec §三.2).
var menuBarDefault = []string{"cpu", "mem"}

// menuBarConfig is what menubar.json keeps. Layout numbers the icons'
// arrangement: it changes when the readings shown change, and the icons, named
// after it, are made again in menuBarItems' order instead of where macOS kept
// the old names (spec §三.3). While it stays, each icon keeps the place the user
// dragged it to with ⌘.
type menuBarConfig struct {
	Items  []string `json:"items"`
	Layout int      `json:"layout"`
}

// MenuBarState is what Settings shows of the menu bar.
type MenuBarState struct {
	Supported bool     `json:"supported"` // false on Windows
	Items     []string `json:"items"`     // the readings shown, in menuBarItems' order
	Available []string `json:"available"` // the readings this Mac can show: no battery without one
}

// MenuBarUpdated tells pages that the readings shown changed.
var MenuBarUpdated = mygo.NewEvent[MenuBarState]("menuBar:updated")

// MenuBarTarget is a page of the main window that the menu bar opens:
//   - "status", the home page;
//   - "process", with Tab "cpu", "mem" or "disk" and Key the row to show;
//   - "clean";
//   - "settings", with Tab "menubar" for its menu bar section.
type MenuBarTarget struct {
	Page string `json:"page"`
	Tab  string `json:"tab"`
	Key  string `json:"key"`
}

// MenuBarGo asks the main window to show a page: a link of a menu bar panel or
// a line of the Cleanzo icon's menu was clicked.
var MenuBarGo = mygo.NewEvent[MenuBarTarget]("menuBar:go")

// menuBarBattery tells whether this Mac has a battery; tests replace it.
var menuBarBattery = func() bool { return menuBarHasBattery() }

// menuBarPath is the file the choice is kept in, next to config.json in
// Cleanzo's folder of Application Support.
func menuBarPath() string { return filepath.Join(filepath.Dir(configPath()), "menubar.json") }

// menuBarAvailable lists the readings a Mac can show: all of them, the battery
// only with one (spec §三.5).
func menuBarAvailable(battery bool) []string {
	out := slices.Clone(menuBarItems)
	if !battery {
		out = slices.DeleteFunc(out, func(item string) bool { return item == "battery" })
	}
	return out
}

// normalizeMenuBarItems keeps the readings of items that a Mac can show, once
// each, in menuBarItems' order. It never returns nil.
func normalizeMenuBarItems(items []string, battery bool) []string {
	out := []string{}
	for _, item := range menuBarAvailable(battery) {
		if slices.Contains(items, item) {
			out = append(out, item)
		}
	}
	return out
}

// loadMenuBarConfig reads path. A missing file, a broken one or one without a
// list gives the default readings.
func loadMenuBarConfig(path string, battery bool) menuBarConfig {
	c := menuBarConfig{Items: slices.Clone(menuBarDefault)}
	if data, err := os.ReadFile(path); err == nil {
		var saved menuBarConfig
		if json.Unmarshal(data, &saved) == nil && saved.Items != nil {
			c = saved
		}
	}
	c.Items = normalizeMenuBarItems(c.Items, battery)
	return c
}

// saveMenuBarConfig writes path, replacing it only once written in full.
func saveMenuBarConfig(path string, c menuBarConfig) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeGrowthFile(path, data)
}

// menuBarConfigNow is the saved choice for this Mac; no reading where they are
// not supported.
func menuBarConfigNow() menuBarConfig {
	if !menuBarSupported() {
		return menuBarConfig{Items: []string{}}
	}
	return loadMenuBarConfig(menuBarPath(), menuBarBattery())
}

// menuBarOn tells whether the menu bar shows any reading.
func menuBarOn() bool { return len(menuBarConfigNow().Items) > 0 }

// menuBarState is MenuBarState for c.
func menuBarState(c menuBarConfig) MenuBarState {
	if !menuBarSupported() {
		return MenuBarState{Items: []string{}, Available: []string{}}
	}
	return MenuBarState{Supported: true, Items: c.Items, Available: menuBarAvailable(menuBarBattery())}
}

// menuBarIconName names the Cleanzo icon for layout: "growth", its name from
// before the readings, until the readings first change, so that it keeps the
// place the user gave it.
func menuBarIconName(layout int) string {
	if layout == 0 {
		return "growth"
	}
	return fmt.Sprintf("cleanzo.%d", layout)
}

// menuBarItemName names item's icon for layout.
func menuBarItemName(layout int, item string) string {
	return fmt.Sprintf("cleanzo.%d.%s", layout, item)
}

// MenuBar returns the readings the menu bar shows and those it can.
func (Cleaner) MenuBar() MenuBarState { return menuBarState(menuBarConfigNow()) }

// residentApply is applyResident, for the callers that tests must be able to
// replace. Its default is set in init, as growthApply's is.
var residentApply func(GrowthConfig, menuBarConfig) error

func init() { residentApply = applyResident }

// SaveMenuBar shows the readings in items, which Settings sends whole, and
// keeps them for the next launch; names it does not know, and the battery on a
// Mac without one, are dropped. Turning the last reading off while monitoring
// is off turns opening at login off too (spec §七.3). Pages hear
// MenuBarUpdated, and GrowthUpdated when the login item changed.
func (Cleaner) SaveMenuBar(items []string) error {
	if !menuBarSupported() {
		return codeError{Code: "unsupported"}
	}
	// Held from loading the settings to applying them, as SaveGrowthConfig
	// holds it: the login item and staying open follow both.
	growthConfigMu.Lock()
	defer growthConfigMu.Unlock()
	battery := menuBarBattery()
	cur := loadMenuBarConfig(menuBarPath(), battery)
	next := menuBarConfig{Items: normalizeMenuBarItems(items, battery), Layout: cur.Layout}
	if len(next.Items) > 0 && !slices.Equal(next.Items, cur.Items) {
		next.Layout++ // made again in their order (spec §三.3)
	}
	if err := saveMenuBarConfig(menuBarPath(), next); err != nil {
		return err
	}
	g := withSystemLoginItem(loadGrowthConfig(growthDir()))
	loginOff := len(next.Items) == 0 && !g.Enabled && g.OpenAtLogin
	if loginOff {
		g.OpenAtLogin = false
		if err := saveGrowthConfig(growthDir(), g); err != nil {
			return err
		}
	}
	applyErr := residentApply(g, next)
	_ = MenuBarUpdated.Broadcast(menuBarState(next))
	if loginOff {
		growth.changed() // Settings shows the login switch off
	}
	return applyErr
}

// menuBarGo shows t in the main window, making it again if it was closed,
// once the app can make windows.
func menuBarGo(t MenuBarTarget) {
	mygo.App.WhenReady(func() {
		_ = MenuBarGo.Emit(openMainWindow(), t) // delivered once the page is ready
	})
}
