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
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// growthTray is the Cleanzo icon in the menu bar, shown while monitoring or
// Trash reminders are on and no reading shows: with readings, their items carry
// Cleanzo's menu instead (menubar spec §三.4). The main thread makes and
// removes it.
var growthTray atomic.Pointer[mygo.Tray]

// growthMenuShown is the model of Cleanzo's menu as last built: the icon's
// menu, or what the readings show on a right-click and in their panels. Only
// the main thread reads or writes it.
var growthMenuShown []growthMenuItem

// growthMenuOn tells whether Cleanzo's menu was last applied with monitoring
// on. Only the main thread reads or writes it: refreshGrowthTray drops a menu
// built for the other state, which a slow read of the records can deliver after
// monitoring was turned on or off.
var growthMenuOn bool

// growthApply is applyGrowthLifecycle, for the callers that tests must be able
// to replace. Its default is set in init, not in its declaration: the menu that
// applyGrowthLifecycle builds reaches turnOffGrowth, which calls growthApply,
// so Go would report an initialization cycle.
var growthApply func(GrowthConfig) error

func init() { growthApply = applyGrowthLifecycle }

// registerGrowthLifecycle installs the handlers that keep the app open without
// windows while monitoring, a menu bar reading, or Trash reminders are on
// (growth spec §六, menubar spec §七). It runs before Run, as App.OnNotificationClick must
// for the click that launched the app.
//
// With all three off, closing the last window quits (afterLastWindow). On
// Windows, growthSupported is false, so the app quits too.
func registerGrowthLifecycle() {
	mygo.App.OnWindowAllClosed(func() {
		switch afterLastWindow(growthSupported(), residentOn, holdingOpen()) {
		case "stay":
			mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
		case "quit":
			mygo.App.Quit()
		}
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
	// Quitting closes every window first: a panel held open for a quit under
	// way must not cancel it, nor the logout it may be part of.
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { markTrashReminderQuitting(); menuBarReleaseAll() })
	// The menu's labels, the tooltips and the readings' labels follow the
	// app's language. mygo calls this on the main thread.
	mygo.App.OnLocaleChanged(func(string) {
		go refreshGrowthTray()
		menuBarRelabel()
	})
}

// residentOn tells whether Cleanzo stays open without windows: monitoring is
// on, the menu bar shows a reading, or Trash reminders are enabled.
func residentOn() bool {
	return loadGrowthConfig(growthDir()).Enabled || menuBarOn() || trashReminderOn()
}

// growthKeepRunning tells whether closing the last window leaves the app
// running: it does where the monitor runs and resident says so. resident is
// asked only then, so a platform without the monitor never reads its settings.
func growthKeepRunning(supported bool, resident func() bool) bool {
	return supported && resident()
}

// afterLastWindow tells what Cleanzo does once no window is open:
//   - "stay" in the menu bar without its Dock icon, where the monitor runs and
//     resident says so (growthKeepRunning);
//   - "wait", Dock icon and all, while held, as work holds it open
//     (holdOpen): it quits when that work ends (quitIfNoWindow);
//   - "quit" otherwise.
func afterLastWindow(supported bool, resident func() bool, held bool) string {
	switch {
	case growthKeepRunning(supported, resident):
		return "stay"
	case held:
		return "wait"
	}
	return "quit"
}

// heldWork counts work under way that quitting would cut short, such as a
// brew upgrade (upgradeApp). While there is any, closing the last window
// leaves Cleanzo running (afterLastWindow).
var heldWork atomic.Int32

// heldWorkEnded runs when the last held work ends; tests replace it.
var heldWorkEnded = quitIfNoWindow

// holdOpen keeps Cleanzo running without windows until release is called.
// Calling release again does nothing.
func holdOpen() (release func()) {
	heldWork.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			if heldWork.Add(-1) == 0 {
				heldWorkEnded()
			}
		})
	}
}

// holdingOpen tells whether work holds Cleanzo open (holdOpen).
func holdingOpen() bool { return heldWork.Load() > 0 }

// quitIfNoWindow quits when held work has ended with no window open and
// nothing keeps Cleanzo in the menu bar: the last window closed while the work
// ran. It does nothing before the app runs, as in a test.
func quitIfNoWindow() {
	if !mygo.App.IsReady() {
		return
	}
	mygo.RunOnMain(func() {
		if len(mygo.Windows()) == 0 && afterLastWindow(growthSupported(), residentOn, holdingOpen()) == "quit" {
			mygo.App.Quit()
		}
	})
}

// startResident runs in WhenReady, on the main thread, so it reads no records:
// they would hold up the first window. It starts the monitor's loop and the
// Trash reminders, and shows the menu bar: the readings chosen, an item each,
// or else the Cleanzo icon while monitoring or Trash reminders are on, with
// the lines that act and no more, which refreshGrowthTray completes on another
// goroutine. It reports true when the app was opened at login and Cleanzo
// shows in the menu bar: then no window opens, and the Dock icon is dropped
// before anything else is made. Without either, nothing would show that
// Cleanzo runs, so the window opens after all, with its Dock icon.
//
// It leaves the login item alone: only SaveGrowthConfig, SaveMenuBar and the
// menu's "turn off" change it, so what the user set in System Settings stays.
func startResident() bool {
	if !growthSupported() {
		return false
	}
	cfg := loadGrowthConfig(growthDir())
	mb := menuBarConfigNow()
	resident := cfg.Enabled || len(mb.Items) > 0 || trashReminderOn()
	atLogin := resident && mygo.App.WasOpenedAtLogin()
	if atLogin {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
	menuBarShow(mb.Items) // first: with a reading, the readings carry the menu and no icon shows
	if resident {
		model, tip := menuBarOnlyMenu(nativeText), nativeText("menubar.tray.tip", nil)
		if cfg.Enabled {
			model, tip = growthMenuCommands(false, nativeText), nativeText("growth.tray.tip", nil)
		}
		growthMenuOn = cfg.Enabled
		showGrowthTray(model, tip)
		go refreshGrowthTray()
	}
	growth.start()
	startTrashReminder()
	if atLogin && growthTray.Load() == nil && !menuBarShowing() {
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

// applyGrowthLifecycle puts the app in the state cfg, just saved, asks for,
// with the menu bar's readings as they are (applyResident).
func applyGrowthLifecycle(cfg GrowthConfig) error {
	return applyResident(cfg, menuBarConfigNow())
}

// applyResident puts the app in the state the two settings ask for, with
// Trash reminders as they are (growth spec §六, menubar spec §三, §七):
//   - the readings in mb, an item each, which carry Cleanzo's menu;
//   - the Cleanzo icon while monitoring or Trash reminders are on and no
//     reading shows;
//   - opening at login as chosen, only while monitoring, a reading or Trash
//     reminders are on;
//   - quitting when none of them is on, no work holds the app open
//     (holdOpen) and no window is open, a minimized or hidden one included.
//
// It does nothing before the app runs, as in a test: there is no main thread
// to wait for then.
func applyResident(g GrowthConfig, mb menuBarConfig) error {
	if !growthSupported() || !mygo.App.IsReady() {
		return nil
	}
	resident := g.Enabled || len(mb.Items) > 0 || trashReminderOn()
	var model []growthMenuItem
	var tip string
	if resident {
		// reading the records takes a moment: build the menu before going to the main thread
		model, tip = residentMenu(g.Enabled)
	}
	var loginErr error
	mygo.RunOnMain(func() { // runs inline when already on the main thread
		menuBarShow(mb.Items)
		if resident {
			growthMenuOn = g.Enabled
			showGrowthTray(model, tip)
		} else {
			hideGrowthTray()
		}
		if want := resident && g.OpenAtLogin; mygo.App.OpenAtLogin() != want {
			loginErr = mygo.App.SetOpenAtLogin(want)
		}
		if !resident && !holdingOpen() && len(mygo.Windows()) == 0 {
			mygo.App.Quit()
		}
	})
	if loginErr != nil {
		return codeError{Code: "growth-login", Args: []string{loginErr.Error()}}
	}
	return nil
}

// showGrowthTray shows Cleanzo's menu for model (menubar spec §三.4). While a
// reading shows, the readings carry it, on a right-click and in their panels,
// and the Cleanzo icon goes. Otherwise it goes on the icon, made if missing,
// with the tooltip tip. It runs on the main thread.
func showGrowthTray(model []growthMenuItem, tip string) {
	if menuBarShowing() {
		hideGrowthTray()
		growthMenuShown = slices.Clone(model)
		return
	}
	if t := growthTray.Load(); t != nil {
		t.SetToolTip(tip)
		showGrowthMenu(t, model)
		return
	}
	t, err := mygo.NewTray(mygo.TrayOptions{ID: menuBarIconID, Icon: growthIconPNG(), IconIsTemplate: true, ToolTip: tip, Menu: growthTrayMenu(model, growthMenuAction)})
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

// refreshGrowthTray rebuilds Cleanzo's menu while Cleanzo is in the menu bar,
// and the icon's tooltip; both follow the language. The icon's menu is only
// replaced when it changed. A menu built for the other monitoring state than
// the one last applied is dropped.
func refreshGrowthTray() {
	if !mygo.App.IsReady() {
		return
	}
	on := loadGrowthConfig(growthDir()).Enabled
	if !on && !menuBarOn() && !trashReminderOn() {
		return
	}
	model, tip := residentMenu(on)
	mygo.RunOnMain(func() {
		if on != growthMenuOn {
			return
		}
		if t := growthTray.Load(); t != nil {
			t.SetToolTip(tip)
			showGrowthMenu(t, model)
		} else if menuBarShowing() {
			growthMenuShown = slices.Clone(model)
		}
	})
}

// residentModelNow is Cleanzo's menu as last built, for the readings to show.
// Before one is built, it is the lines that act, as startResident first shows
// them. It runs on the main thread.
func residentModelNow() []growthMenuItem {
	if growthMenuShown != nil {
		return growthMenuShown
	}
	if growthMenuOn {
		return growthMenuCommands(false, nativeText)
	}
	return menuBarOnlyMenu(nativeText)
}

// residentMenu is Cleanzo's menu and the icon's tooltip: the growth menu while
// monitoring is on, which reads the records, so not on the main thread;
// otherwise the lines that open, set up and quit (menubar spec §三.4).
func residentMenu(growthOn bool) ([]growthMenuItem, string) {
	if growthOn {
		return growthMenuModel(Cleaner{}.GrowthState().Apps, growth.status().Running, nativeText), nativeText("growth.tray.tip", nil)
	}
	return menuBarOnlyMenu(nativeText), nativeText("menubar.tray.tip", nil)
}

// growthMenuItem is one line of the menu bar menu. A zero item is a separator.
type growthMenuItem struct {
	label    string
	action   string // "focus", "open", "measure", "off", "settings" or "quit"
	key      string // the owner "focus" shows
	disabled bool
}

// growthMenuModel lists the menu while monitoring is on (spec §六):
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

// growthMenuCommands lists the lines of the growth menu that act: open, measure
// now (or that it is measuring) and turning monitoring off, then
// menuBarMenuTail. It is also the menu the icon starts with, before the records
// are read.
func growthMenuCommands(running bool, text func(string, map[string]string) string) []growthMenuItem {
	measure := growthMenuItem{label: text("growth.tray.measure", nil), action: "measure"}
	if running {
		measure = growthMenuItem{label: text("growth.tray.measuring", nil), disabled: true}
	}
	return append([]growthMenuItem{
		{label: text("growth.tray.open", nil), action: "open"},
		measure,
		{label: text("growth.tray.off", nil), action: "off"},
	}, menuBarMenuTail(text)...)
}

// menuBarOnlyMenu is Cleanzo's menu while monitoring is off and a reading
// shows or Trash reminders are on: open, then menuBarMenuTail.
func menuBarOnlyMenu(text func(string, map[string]string) string) []growthMenuItem {
	return append([]growthMenuItem{{label: text("growth.tray.open", nil), action: "open"}}, menuBarMenuTail(text)...)
}

// menuBarMenuTail ends every menu of Cleanzo: the menu bar's settings
// and quitting (menubar spec §三.4).
func menuBarMenuTail(text func(string, map[string]string) string) []growthMenuItem {
	return []growthMenuItem{
		{},
		{label: text("menubar.tray.settings", nil), action: "settings"},
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
	case "settings":
		menuBarGo(MenuBarTarget{Page: "settings", Tab: "menubar"})
	case "quit":
		mygo.App.Quit()
	}
}

// turnOffGrowth switches monitoring off, as the menu bar's "turn off" asks,
// with the login item unless a reading still keeps Cleanzo in the menu bar
// (menubar spec §七.3), and stops a background measurement under way. It starts
// from the login item the system has, as SaveGrowthConfig does, so it never
// brings back one the user removed. It takes growthConfigMu like
// SaveGrowthConfig, which holds it while applyGrowthLifecycle waits for the
// main thread, so it must not run on that thread.
func turnOffGrowth() {
	growthConfigMu.Lock()
	cfg := withSystemLoginItem(loadGrowthConfig(growthDir()))
	cfg.Enabled = false
	cfg.OpenAtLogin = cfg.OpenAtLogin && (menuBarOn() || trashReminderOn())
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
