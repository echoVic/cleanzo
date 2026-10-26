package main

import (
	"bytes"
	"cmp"
	"image"
	"image/color"
	"image/png"
	"log"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// growthTray is the menu bar icon while monitoring is on. The main thread
// makes and removes it; refreshGrowthTray looks at it from other goroutines.
var growthTray atomic.Pointer[mygo.Tray]

// registerGrowthLifecycle installs the handlers that keep the app open
// without windows while monitoring is on (spec §六). It runs before Run, as
// App.OnNotificationClick must for the click that launched the app.
//
// With monitoring off, closing the last window quits, as it always did. On
// Windows, growthSupported is false, so the app quits too.
func registerGrowthLifecycle() {
	mygo.App.OnWindowAllClosed(func() {
		if growthSupported() && loadGrowthConfig(growthDir()).Enabled {
			mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
			return
		}
		mygo.App.Quit()
	})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			openMainWindow()
		}
	})
	mygo.App.OnNotificationClick(func(id string) {
		if key, ok := strings.CutPrefix(id, "growth:"); ok {
			focusGrowth(key)
		}
	})
}

// startGrowthResident starts the monitor's loop, and with monitoring on,
// the menu bar icon and login item. It reports true when the app was opened
// at login with monitoring on: then no window opens.
func startGrowthResident() bool {
	if !growthSupported() {
		return false
	}
	growth.start()
	cfg := loadGrowthConfig(growthDir())
	if !cfg.Enabled {
		return false
	}
	if err := applyGrowthLifecycle(cfg); err != nil {
		log.Println("growth:", err)
	}
	if mygo.App.WasOpenedAtLogin() {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
		return true
	}
	return false
}

// openMainWindow shows the main window, creating it again after it was
// closed, and brings the Dock icon back.
func openMainWindow() *mygo.Window {
	if mainWindow == nil || !slices.Contains(mygo.Windows(), mainWindow) {
		mainWindow = newMainWindow()
	} else {
		mainWindow.Show()
		mainWindow.Focus()
	}
	if growthSupported() {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	}
	mygo.App.Focus()
	return mainWindow
}

// focusGrowth opens the window on the growth page at one owner.
func focusGrowth(key string) {
	mygo.RunOnMain(func() {
		_ = GrowthFocus.Emit(openMainWindow(), key) // delivered once the page is ready
	})
}

// applyGrowthLifecycle puts the app in the state cfg asks for (spec §六):
//   - with monitoring on, a menu bar icon and the login item as chosen;
//   - with it off, no icon and no login item, and the app quits when no
//     window is visible.
//
// It does nothing before the app runs, as in a test: there is no main thread
// to wait for then.
func applyGrowthLifecycle(cfg GrowthConfig) error {
	if !growthSupported() || !mygo.App.IsReady() {
		return nil
	}
	var menu *mygo.Menu
	if cfg.Enabled {
		// reading the records takes a moment: build the menu before going to the main thread
		menu = growthTrayMenu(growthMenuModel(Cleaner{}.GrowthState().Apps, growth.status().Running, nativeText))
	}
	var loginErr error
	mygo.RunOnMain(func() { // runs inline when already on the main thread, as in a menu click
		if cfg.Enabled {
			showGrowthTray(menu)
		} else if t := growthTray.Swap(nil); t != nil {
			t.Destroy()
		}
		if want := cfg.Enabled && cfg.OpenAtLogin; mygo.App.OpenAtLogin() != want {
			loginErr = mygo.App.SetOpenAtLogin(want)
		}
		if !cfg.Enabled && !slices.ContainsFunc(mygo.Windows(), (*mygo.Window).IsVisible) {
			mygo.App.Quit()
		}
	})
	if loginErr != nil {
		return codeError{Code: "growth-login", Args: []string{loginErr.Error()}}
	}
	return nil
}

// showGrowthTray makes the menu bar icon with menu, or gives the existing
// icon that menu. It runs on the main thread.
func showGrowthTray(menu *mygo.Menu) {
	if t := growthTray.Load(); t != nil {
		t.SetMenu(menu)
		return
	}
	t, err := mygo.NewTray(mygo.TrayOptions{ID: "growth", Icon: growthIconPNG(), IconIsTemplate: true, ToolTip: nativeText("growth.tray.tip", nil), Menu: menu})
	if err != nil {
		log.Println("tray:", err)
		return
	}
	growthTray.Store(t)
}

// refreshGrowthTray rebuilds the menu from the records, if the icon shows.
func refreshGrowthTray() {
	if growthTray.Load() == nil {
		return
	}
	menu := growthTrayMenu(growthMenuModel(Cleaner{}.GrowthState().Apps, growth.status().Running, nativeText))
	mygo.RunOnMain(func() {
		if t := growthTray.Load(); t != nil {
			t.SetMenu(menu)
		}
	})
}

// growthMenuItem is one line of the menu bar menu. A zero item is a separator.
type growthMenuItem struct {
	label    string
	action   string // "focus", "open", "measure", "off" or "quit"
	key      string // the owner "focus" shows
	disabled bool
}

// growthMenuModel lists the menu (spec §六):
//   - the three owners that grew most in seven days, ignored ones left out;
//   - then open, measure now, turning monitoring off, and quit.
func growthMenuModel(apps []GrowthApp, running bool, text func(string, map[string]string) string) []growthMenuItem {
	var top []GrowthApp
	for _, a := range apps {
		if a.Week != nil && a.Week.Bytes > 0 && !a.Ignored {
			top = append(top, a)
		}
	}
	slices.SortFunc(top, func(x, y GrowthApp) int {
		return cmp.Or(cmp.Compare(y.Week.Bytes, x.Week.Bytes), strings.Compare(x.Key, y.Key))
	})
	var items []growthMenuItem
	for _, a := range top[:min(3, len(top))] {
		key := "growth.tray.item"
		if a.Week.AtLeast {
			key = "growth.tray.itemAtLeast"
		}
		items = append(items, growthMenuItem{label: text(key, map[string]string{"name": a.Name, "size": growthSize(a.Week.Bytes)}), action: "focus", key: a.Key})
	}
	if len(items) == 0 {
		items = append(items, growthMenuItem{label: text("growth.tray.none", nil), disabled: true})
	}
	measure := growthMenuItem{label: text("growth.tray.measure", nil), action: "measure"}
	if running {
		measure = growthMenuItem{label: text("growth.tray.measuring", nil), disabled: true}
	}
	return append(items,
		growthMenuItem{},
		growthMenuItem{label: text("growth.tray.open", nil), action: "open"},
		measure,
		growthMenuItem{label: text("growth.tray.off", nil), action: "off"},
		growthMenuItem{},
		growthMenuItem{label: text("growth.tray.quit", nil), action: "quit"},
	)
}

// growthTrayMenu turns the model into a mygo menu.
func growthTrayMenu(items []growthMenuItem) *mygo.Menu {
	var out []*mygo.MenuItem
	for _, it := range items {
		if it == (growthMenuItem{}) {
			out = append(out, mygo.Separator())
			continue
		}
		out = append(out, &mygo.MenuItem{Label: it.label, Disabled: it.disabled, Click: func(*mygo.MenuItem, *mygo.Window) { growthMenuAction(it) }})
	}
	return mygo.NewMenu(out)
}

// growthMenuAction does what a menu line says. mygo calls it on the main
// thread.
func growthMenuAction(it growthMenuItem) {
	switch it.action {
	case "focus":
		focusGrowth(it.key)
	case "open":
		openMainWindow()
	case "measure":
		growth.run(true)
	case "off":
		go turnOffGrowth() // it waits for growthConfigMu, which the main thread must not
	case "quit":
		mygo.App.Quit()
	}
}

// turnOffGrowth switches monitoring and the login item off, as the menu bar's
// "turn off" asks. It takes growthConfigMu like SaveGrowthConfig, which holds
// it while applyGrowthLifecycle waits for the main thread, so it must not run
// on that thread.
func turnOffGrowth() {
	growthConfigMu.Lock()
	cfg := loadGrowthConfig(growthDir())
	cfg.Enabled, cfg.OpenAtLogin = false, false
	err := saveGrowthConfig(growthDir(), cfg)
	if err == nil {
		if applyErr := applyGrowthLifecycle(cfg); applyErr != nil {
			log.Println("growth:", applyErr)
		}
	}
	growthConfigMu.Unlock()
	if err != nil {
		log.Println("growth:", err)
		return
	}
	growth.setNext(time.Time{})
	growth.changed()
}

// growthIconPNG draws the menu bar icon: a drive with an arrow rising above
// it. It is a template image, black on transparent, 32×32 pixels for 16
// points at 2×; macOS tints it for light and dark menu bars.
func growthIconPNG() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	ink := color.NRGBA{A: 255}
	fill := func(x0, y0, x1, y1 int) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				img.SetNRGBA(x, y, ink)
			}
		}
	}
	// drive: a 2 px outline from (4,19) to (28,29), with a light at the right
	fill(4, 19, 28, 21)
	fill(4, 27, 28, 29)
	fill(4, 19, 6, 29)
	fill(26, 19, 28, 29)
	fill(21, 23, 24, 25)
	// arrow: a shaft and a head pointing up from (16,3)
	fill(15, 3, 17, 16)
	for i := 0; i <= 6; i++ {
		fill(15-i, 3+i, 17-i, 5+i)
		fill(15+i, 3+i, 17+i, 5+i)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
