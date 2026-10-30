package main

// menubar.go holds the menu bar readings' settings (design:
// docs/superpowers/specs/2026-10-29-menubar-design.md §三, §五, §七): which
// readings show, where that choice is kept, and the bound methods Settings
// calls. menubar_readout.go turns readings into text; menubar_darwin.go makes
// the readings' item and feeds it; elsewhere the readings are not supported.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// menuBarItems are the readings in their order in the readings' item, left to
// right (spec §三.3).
var menuBarItems = []string{"cpu", "mem", "gpu", "net", "disk", "temp", "battery"}

// menuBarDefault are the readings shown until the user chooses (spec §三.2).
var menuBarDefault = []string{"cpu", "mem"}

// menuBarConfig is what menubar.json keeps: the readings shown.
type menuBarConfig struct {
	Items []string `json:"items"`
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
// a line of Cleanzo's menu was clicked.
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

// menuBarItemID names Cleanzo's place in the menu bar: the readings' item, or
// the Cleanzo icon while monitoring is on and no reading shows (spec §三.4).
// The two never show together, so they share it: each comes where the other
// was, or where the user dragged it with ⌘.
const menuBarItemID = "cleanzo"

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
	next := menuBarConfig{Items: normalizeMenuBarItems(items, battery)}
	if err := saveMenuBarConfig(menuBarPath(), next); err != nil {
		return err
	}
	g := withSystemLoginItem(loadGrowthConfig(growthDir()))
	loginOff := len(next.Items) == 0 && !g.Enabled && g.OpenAtLogin
	var saveErr error
	if loginOff {
		g.OpenAtLogin = false
		saveErr = saveGrowthConfig(growthDir(), g)
	}
	// Applied even when that save failed: with nothing keeping Cleanzo in the
	// menu bar, the login item goes, whatever the file says.
	applyErr := residentApply(g, next)
	_ = MenuBarUpdated.Broadcast(menuBarState(next))
	if loginOff && saveErr == nil {
		growth.changed() // Settings shows the login switch off
	}
	if saveErr != nil {
		return saveErr
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

// WatchMenuBarPanel streams what a menu bar panel shows while it is open, as
// the status page's samples: the first after about half a second, then one
// every two seconds (spec §四, §六). It reads only what item's panel needs:
//   - the machine's live readings, but not for the memory and battery panels;
//   - the battery through system_profiler, once a minute, for its own panel
//     only;
//   - for the temperature panel, the battery's temperature alone.
//
// The panel closes the channel as it closes. It fails with "unsupported" off
// macOS and "unknown-item" for a reading it does not know.
func (Cleaner) WatchMenuBarPanel(ctx context.Context, item string, ch *mygo.Channel[StatusSample]) error {
	if !menuBarSupported() {
		return codeError{Code: "unsupported"}
	}
	if !slices.Contains(menuBarItems, item) {
		return codeError{Code: "unknown-item"}
	}
	return watchStatusWith(ctx, ch.Send, menuBarPanelSource(realStatusSource(), item))
}

// menuBarPanelSource is base without the reads item's panel does not show.
func menuBarPanelSource(base statusSource, item string) statusSource {
	src := base
	switch item {
	case "battery": // system_profiler, as the home page: health and condition come only from it
	case "temp":
		src.battery = func(context.Context) *BatteryStatus { return menuBarBatteryTemp() }
	default:
		src.battery = func(context.Context) *BatteryStatus { return nil }
	}
	if item == "mem" || item == "battery" {
		src.machine = nil
	}
	return src
}

// menuBarHolds are the panels a quit under way keeps open (spec §四.5), with
// when each hold began.
var menuBarHolds = struct {
	sync.Mutex
	held map[*mygo.Window]time.Time
}{held: map[*mygo.Window]time.Time{}}

// menuBarHoldMax bounds a hold. A quit gives up after five seconds, so a hold
// older than this is one whose page went away or whose release was lost, and
// the panel closes as usual again.
const menuBarHoldMax = 15 * time.Second

// menuBarHeld tells whether a quit under way keeps w open.
func menuBarHeld(w *mygo.Window) bool {
	menuBarHolds.Lock()
	defer menuBarHolds.Unlock()
	at, ok := menuBarHolds.held[w]
	return ok && time.Since(at) < menuBarHoldMax
}

// menuBarRelease lets w close again.
func menuBarRelease(w *mygo.Window) {
	menuBarHolds.Lock()
	delete(menuBarHolds.held, w)
	menuBarHolds.Unlock()
}

// menuBarReleaseAll lets every panel close, as quitting needs: a held panel
// would cancel the quit, and a logout with it.
func menuBarReleaseAll() {
	menuBarHolds.Lock()
	clear(menuBarHolds.held)
	menuBarHolds.Unlock()
}

// MenuBarHold keeps the panel that calls it open while hold is true: a quit is
// under way, and its result must show (spec §四.5). Clicking elsewhere, or on
// the reading, leaves the panel open until the quit is done, or menuBarHoldMax
// at most; quitting Cleanzo releases it. Only a panel can be held, and a call
// from a page that has gone releases.
func (Cleaner) MenuBarHold(ctx context.Context, hold bool) {
	w := mygo.CallerWindow(ctx)
	if w == nil || !w.IsFlyout() {
		return
	}
	menuBarHolds.Lock()
	defer menuBarHolds.Unlock()
	if !hold || ctx.Err() != nil {
		delete(menuBarHolds.held, w)
		return
	}
	menuBarHolds.held[w] = time.Now()
}

// MenuBarOpen closes the panel that calls it, held or not, and shows the page
// t names in the main window, making the window again if it was closed
// (spec §四.6).
func (Cleaner) MenuBarOpen(ctx context.Context, t MenuBarTarget) {
	if w := mygo.CallerWindow(ctx); w != nil && w.IsFlyout() {
		menuBarRelease(w)
		w.Destroy()
	}
	menuBarGo(t)
}

// MenuBarMenu shows Cleanzo's menu at x, y in the panel that calls it, in
// points from the top left of its page: the lines of the Cleanzo icon, which
// does not show while a reading does (spec §三.4). mygo runs the line chosen
// before the menu returns; a line chosen then closes the panel, held or not.
func (Cleaner) MenuBarMenu(ctx context.Context, x, y int) {
	if !menuBarSupported() {
		return
	}
	w := mygo.CallerWindow(ctx)
	if w == nil || !w.IsFlyout() {
		return
	}
	var menu *mygo.Menu
	chosen := false
	mygo.RunOnMain(func() { // the model is the main thread's
		menu = growthTrayMenu(residentModelNow(), func(it growthMenuItem) {
			chosen = true
			growthMenuAction(it)
		})
	})
	menu.PopupAt(w, x, y)
	if chosen {
		menuBarRelease(w)
		w.Destroy()
	}
}
