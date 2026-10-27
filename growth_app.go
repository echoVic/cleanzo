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

// growthMenuShown is the model of the menu the icon shows. Only the main
// thread reads or writes it.
var growthMenuShown []growthMenuItem

// growthApply is applyGrowthLifecycle, for the callers that tests must be able
// to replace. Its default is set in init, not in its declaration: the menu that
// applyGrowthLifecycle builds reaches turnOffGrowth, which calls growthApply,
// so Go would report an initialization cycle.
var growthApply func(GrowthConfig) error

func init() { growthApply = applyGrowthLifecycle }

// registerGrowthLifecycle installs the handlers that keep the app open
// without windows while monitoring is on (spec §六). It runs before Run, as
// App.OnNotificationClick must for the click that launched the app.
//
// With monitoring off, closing the last window quits, as it always did. On
// Windows, growthSupported is false, so the app quits too.
func registerGrowthLifecycle() {
	mygo.App.OnWindowAllClosed(func() {
		if growthKeepRunning(growthSupported(), func() bool { return loadGrowthConfig(growthDir()).Enabled }) {
			mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
			return
		}
		mygo.App.Quit()
	})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows && mygo.App.IsReady() { // no window can be made before that
			openMainWindow()
		}
	})
	mygo.App.OnNotificationClick(func(id string) {
		if key, ok := strings.CutPrefix(id, "growth:"); ok {
			focusGrowth(key)
		}
	})
	// The labels and the tooltip of the icon follow the app's language.
	mygo.App.OnLocaleChanged(func(string) { go refreshGrowthTray() })
}

// growthKeepRunning tells whether closing the last window leaves the app
// running: it does where the monitor runs and monitoring is on. enabled is
// asked only then, so a platform without the monitor never reads its settings.
func growthKeepRunning(supported bool, enabled func() bool) bool {
	return supported && enabled()
}

// startGrowthResident runs in WhenReady, on the main thread, so it reads no
// records: they would hold up the first window. It starts the monitor's loop,
// and with monitoring on, makes the menu bar icon with the lines that act and
// no more, which refreshGrowthTray completes on another goroutine. It reports
// true when the app was opened at login with monitoring on, and the icon is
// there: then no window opens, and the Dock icon is dropped before anything
// else is made. Without the icon nothing would show that Cleanzo runs, so the
// window opens after all, with its Dock icon.
//
// It leaves the login item alone: only SaveGrowthConfig and the menu's "turn
// off" change it, so what the user set in System Settings stays.
func startGrowthResident() bool {
	if !growthSupported() {
		return false
	}
	cfg := loadGrowthConfig(growthDir())
	atLogin := cfg.Enabled && mygo.App.WasOpenedAtLogin()
	if atLogin {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
	if cfg.Enabled {
		showGrowthTray(growthMenuCommands(false, nativeText))
		go refreshGrowthTray()
	}
	growth.start()
	if atLogin && growthTray.Load() == nil {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular) // the window comes with its Dock icon
		return false
	}
	return atLogin
}

// windowRaiser is what raiseWindow asks of a window; *mygo.Window has it.
type windowRaiser interface {
	IsMinimized() bool
	Restore()
	Show()
	Focus()
}

// raiseWindow brings w in front of the user. Show and Focus leave a minimized
// window in the Dock (on macOS only Restore takes it out), so Restore comes
// first.
func raiseWindow(w windowRaiser) {
	if w.IsMinimized() {
		w.Restore()
	}
	w.Show()
	w.Focus()
}

// openMainWindow shows the main window, creating it again after it was
// closed, and brings the Dock icon back.
func openMainWindow() *mygo.Window {
	if mainWindow == nil || !slices.Contains(mygo.Windows(), mainWindow) {
		mainWindow = newMainWindow()
	} else {
		raiseWindow(mainWindow)
	}
	if growthSupported() {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	}
	mygo.App.Focus()
	return mainWindow
}

// focusGrowth opens the window on the process page's disk usage tab at one
// owner. The click of the notification that launched the app can come before
// the app is ready to make windows, so this waits for that.
func focusGrowth(key string) {
	mygo.App.WhenReady(func() {
		_ = GrowthFocus.Emit(openMainWindow(), key) // delivered once the page is ready
	})
}

// applyGrowthLifecycle puts the app in the state cfg asks for (spec §六):
//   - with monitoring on, a menu bar icon and the login item as chosen;
//   - with it off, no icon and no login item, and the app quits when no
//     window is open, a minimized or hidden one included.
//
// It does nothing before the app runs, as in a test: there is no main thread
// to wait for then.
func applyGrowthLifecycle(cfg GrowthConfig) error {
	if !growthSupported() || !mygo.App.IsReady() {
		return nil
	}
	var model []growthMenuItem
	if cfg.Enabled {
		// reading the records takes a moment: build the menu before going to the main thread
		model = growthMenuModel(Cleaner{}.GrowthState().Apps, growth.status().Running, nativeText)
	}
	var loginErr error
	mygo.RunOnMain(func() { // runs inline when already on the main thread
		if cfg.Enabled {
			showGrowthTray(model)
		} else {
			hideGrowthTray()
		}
		if want := cfg.Enabled && cfg.OpenAtLogin; mygo.App.OpenAtLogin() != want {
			loginErr = mygo.App.SetOpenAtLogin(want)
		}
		if !cfg.Enabled && len(mygo.Windows()) == 0 {
			mygo.App.Quit()
		}
	})
	if loginErr != nil {
		return codeError{Code: "growth-login", Args: []string{loginErr.Error()}}
	}
	return nil
}

// showGrowthTray makes the menu bar icon with the menu for model, or gives the
// existing icon that menu. It runs on the main thread.
func showGrowthTray(model []growthMenuItem) {
	if t := growthTray.Load(); t != nil {
		showGrowthMenu(t, model)
		return
	}
	t, err := mygo.NewTray(mygo.TrayOptions{ID: "growth", Icon: growthIconPNG(), IconIsTemplate: true, ToolTip: nativeText("growth.tray.tip", nil), Menu: growthTrayMenu(model, growthMenuAction)})
	if err != nil {
		log.Println("tray:", err)
		return
	}
	growthTray.Store(t)
	growthMenuShown = slices.Clone(model)
}

// hideGrowthTray removes the menu bar icon. It runs on the main thread.
func hideGrowthTray() {
	if t := growthTray.Swap(nil); t != nil {
		t.Destroy()
	}
	growthMenuShown = nil
}

// growthMenuSetter is what showGrowthMenu asks of the icon; *mygo.Tray has it.
type growthMenuSetter interface{ SetMenu(*mygo.Menu) }

// showGrowthMenu gives the icon the menu for model, unless it shows that menu
// already, so that a refresh with nothing new leaves an open menu alone. It
// runs on the main thread.
func showGrowthMenu(t growthMenuSetter, model []growthMenuItem) {
	if slices.Equal(model, growthMenuShown) {
		return
	}
	t.SetMenu(growthTrayMenu(model, growthMenuAction))
	growthMenuShown = slices.Clone(model)
}

// refreshGrowthTray rebuilds the menu from the records, if the icon shows, and
// sets the tooltip, which follows the language. The menu is only replaced when
// it changed.
func refreshGrowthTray() {
	if growthTray.Load() == nil {
		return
	}
	model := growthMenuModel(Cleaner{}.GrowthState().Apps, growth.status().Running, nativeText)
	tip := nativeText("growth.tray.tip", nil)
	mygo.RunOnMain(func() {
		if t := growthTray.Load(); t != nil {
			t.SetToolTip(tip)
			showGrowthMenu(t, model)
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
//   - then the lines of growthMenuCommands.
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
	return append(append(items, growthMenuItem{}), growthMenuCommands(running, text)...)
}

// growthMenuCommands lists the lines of the menu that act: open, measure now
// (or that it is measuring), turning monitoring off, and quit. It is also the
// menu the icon starts with, before the records are read.
func growthMenuCommands(running bool, text func(string, map[string]string) string) []growthMenuItem {
	measure := growthMenuItem{label: text("growth.tray.measure", nil), action: "measure"}
	if running {
		measure = growthMenuItem{label: text("growth.tray.measuring", nil), disabled: true}
	}
	return []growthMenuItem{
		{label: text("growth.tray.open", nil), action: "open"},
		measure,
		{label: text("growth.tray.off", nil), action: "off"},
		{},
		{label: text("growth.tray.quit", nil), action: "quit"},
	}
}

// growthTrayMenu turns the model into a mygo menu whose lines call act. Tests
// give it their own act to see which line does what.
func growthTrayMenu(items []growthMenuItem, act func(growthMenuItem)) *mygo.Menu {
	var out []*mygo.MenuItem
	for _, it := range items {
		if it == (growthMenuItem{}) {
			out = append(out, mygo.Separator())
			continue
		}
		out = append(out, &mygo.MenuItem{Label: it.label, Disabled: it.disabled, Click: func(*mygo.MenuItem, *mygo.Window) { act(it) }})
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
// "turn off" asks, and stops a background measurement under way. It takes
// growthConfigMu like SaveGrowthConfig, which holds it while
// applyGrowthLifecycle waits for the main thread, so it must not run on that
// thread.
func turnOffGrowth() {
	growthConfigMu.Lock()
	cfg := loadGrowthConfig(growthDir())
	cfg.Enabled, cfg.OpenAtLogin = false, false
	err := saveGrowthConfig(growthDir(), cfg)
	if err == nil {
		if applyErr := growthApply(cfg); applyErr != nil {
			log.Println("growth:", applyErr)
		}
	}
	growthConfigMu.Unlock()
	if err != nil {
		log.Println("growth:", err)
		return
	}
	growth.cancelBackground() // after the save, so the stopped run sees monitoring off
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
