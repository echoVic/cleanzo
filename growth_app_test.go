package main

import (
	"bytes"
	"context"
	"errors"
	"image/png"
	"log"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

func TestGrowthMenuModel(t *testing.T) {
	text := func(key string, vars map[string]string) string { return key + ":" + vars["name"] + vars["size"] }
	apps := []GrowthApp{
		{Key: "app:a", Name: "A", Week: &GrowthDelta{Bytes: 3 * gb}},
		{Key: "app:b", Name: "B", Week: &GrowthDelta{Bytes: 9 * gb, AtLeast: true}},
		{Key: "app:c", Name: "C", Week: &GrowthDelta{Bytes: 5 * gb}, Ignored: true},
		{Key: "app:d", Name: "D", Week: &GrowthDelta{Bytes: -gb}},
		{Key: "app:e", Name: "E"},
		{Key: "app:f", Name: "F", Week: &GrowthDelta{Bytes: gb}},
		{Key: "app:g", Name: "G", Week: &GrowthDelta{Bytes: 2 * gb}},
	}
	items := growthMenuModel(apps, false, text)
	want := []growthMenuItem{
		{label: "growth.tray.itemAtLeast:B9.0 GB", action: "focus", key: "app:b"},
		{label: "growth.tray.item:A3.0 GB", action: "focus", key: "app:a"},
		{label: "growth.tray.item:G2.0 GB", action: "focus", key: "app:g"},
		{},
		{label: "growth.tray.open:", action: "open"},
		{label: "growth.tray.measure:", action: "measure"},
		{label: "growth.tray.off:", action: "off"},
		{},
		{label: "menubar.tray.settings:", action: "settings"},
		{label: "growth.tray.quit:", action: "quit"},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %+v", items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, items[i], want[i])
		}
	}
	busy := growthMenuModel(nil, true, text)
	if busy[0] != (growthMenuItem{label: "growth.tray.none:", disabled: true}) || busy[3] != (growthMenuItem{label: "growth.tray.measuring:", disabled: true}) {
		t.Fatalf("busy, empty = %+v", busy)
	}
}

func TestGrowthIconIsATemplate(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(growthIconPNG()))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Fatalf("bounds = %v", b)
	}
	ink := 0
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			switch {
			case a == 0:
			case r == 0 && g == 0 && b == 0 && a == 0xffff:
				ink++
			default:
				t.Fatalf("pixel %d,%d is neither clear nor black", x, y)
			}
		}
	}
	if ink < 100 {
		t.Fatalf("only %d ink pixels", ink)
	}
}

// labelsOnly stands in for the translated texts.
func labelsOnly(key string, _ map[string]string) string { return key }

// The mygo menu has the lines of the model, a separator for each zero item,
// and a click on a line does what that line says.
func TestGrowthTrayMenuMirrorsTheModel(t *testing.T) {
	apps := []GrowthApp{
		{Key: "app:a", Name: "A", Week: &GrowthDelta{Bytes: 3 * gb}},
		{Key: "app:b", Name: "B", Week: &GrowthDelta{Bytes: 9 * gb, AtLeast: true}},
	}
	for name, model := range map[string][]growthMenuItem{
		"owners that grew": growthMenuModel(apps, false, labelsOnly),
		"measuring, none":  growthMenuModel(nil, true, labelsOnly),
	} {
		var acted []growthMenuItem
		items := growthTrayMenu(model, func(it growthMenuItem) { acted = append(acted, it) }).Items()
		if len(items) != len(model) {
			t.Fatalf("%s: %d menu items for %d lines", name, len(items), len(model))
		}
		for i, line := range model {
			it := items[i]
			if line == (growthMenuItem{}) {
				if it.Type != mygo.MenuItemSeparator {
					t.Errorf("%s, line %d: type %q, want a separator", name, i, it.Type)
				}
				continue
			}
			if it.Label != line.label || it.Disabled != line.disabled || it.Click == nil {
				t.Errorf("%s, line %d: label %q, disabled %v, click %v; want %+v", name, i, it.Label, it.Disabled, it.Click != nil, line)
				continue
			}
			if line.disabled {
				continue // it cannot be clicked
			}
			acted = nil
			it.Click(it, nil)
			if len(acted) != 1 || acted[0] != line {
				t.Errorf("%s, line %d: a click did %+v, want %+v", name, i, acted, line)
			}
		}
	}
}

// The icon starts with the lines that act, which end the full menu too.
func TestGrowthMenuCommandsEndTheModel(t *testing.T) {
	apps := []GrowthApp{{Key: "app:a", Name: "A", Week: &GrowthDelta{Bytes: gb}}}
	for _, running := range []bool{false, true} {
		commands := growthMenuCommands(running, labelsOnly)
		model := growthMenuModel(apps, running, labelsOnly)
		if !slices.Equal(model[len(model)-len(commands):], commands) {
			t.Errorf("running %v: the model ends %+v, not %+v", running, model[len(model)-len(commands):], commands)
		}
		var actions []string
		for _, it := range commands {
			actions = append(actions, it.action)
		}
		measure := "measure"
		if running {
			measure = "" // "measuring" cannot be clicked
		}
		if want := []string{"open", measure, "off", "", "settings", "quit"}; !slices.Equal(actions, want) {
			t.Errorf("running %v: actions %v, want %v", running, actions, want)
		}
	}
}

// With monitoring off and a reading shown, the icon's menu opens, sets up and
// quits; its last lines are the growth menu's last lines.
func TestMenuBarOnlyMenu(t *testing.T) {
	model := menuBarOnlyMenu(labelsOnly)
	var actions []string
	for _, it := range model {
		actions = append(actions, it.action)
	}
	if want := []string{"open", "", "settings", "quit"}; !slices.Equal(actions, want) {
		t.Errorf("actions %v, want %v", actions, want)
	}
	tail := menuBarMenuTail(labelsOnly)
	commands := growthMenuCommands(false, labelsOnly)
	if !slices.Equal(model[len(model)-len(tail):], tail) || !slices.Equal(commands[len(commands)-len(tail):], tail) {
		t.Errorf("menus end %+v and %+v, want %+v", model, commands, tail)
	}
}

// menuRecorder is an icon that keeps the menus it is given.
type menuRecorder struct{ menus []*mygo.Menu }

func (r *menuRecorder) SetMenu(m *mygo.Menu) { r.menus = append(r.menus, m) }

// A refresh whose model is the one shown leaves the menu alone, and a changed
// model, such as another language's labels, replaces it. A new icon has no
// menu yet.
func TestShowGrowthMenuLeavesAnEqualMenuAlone(t *testing.T) {
	growthMenuShown = nil
	t.Cleanup(func() { growthMenuShown = nil })
	var icon menuRecorder
	check := func(what string, model []growthMenuItem, sets int) {
		t.Helper()
		showGrowthMenu(&icon, model)
		if len(icon.menus) != sets {
			t.Errorf("%s: the menu was set %d times, want %d", what, len(icon.menus), sets)
		}
	}
	idle := growthMenuModel(nil, false, labelsOnly)
	busy := growthMenuModel(nil, true, labelsOnly)
	check("first model", idle, 1)
	check("the same model built again", growthMenuModel(nil, false, labelsOnly), 1)
	check("a run starts", busy, 2)
	check("still running", slices.Clone(busy), 2)
	check("the run ends", idle, 3)
	check("another language", growthMenuModel(nil, false, func(key string, _ map[string]string) string { return key + "!" }), 4)
	growthMenuShown = nil // the icon was removed and made again
	check("a new icon", idle, 5)
}

// windowRecorder is a window that keeps what it is asked, and stays minimized
// until Restore, as a window does on macOS.
type windowRecorder struct {
	minimized bool
	calls     []string
}

func (w *windowRecorder) IsMinimized() bool { return w.minimized }
func (w *windowRecorder) Restore()          { w.calls = append(w.calls, "restore"); w.minimized = false }
func (w *windowRecorder) Show()             { w.calls = append(w.calls, "show") }
func (w *windowRecorder) Focus()            { w.calls = append(w.calls, "focus") }

// Show does not take a minimized window out of the Dock, so the menu bar's
// "Open Cleanzo" and a notification click restore it first.
func TestRaiseWindowRestoresAMinimizedWindowFirst(t *testing.T) {
	for _, tc := range []struct {
		minimized bool
		want      []string
	}{
		{true, []string{"restore", "show", "focus"}},
		{false, []string{"show", "focus"}},
	} {
		w := &windowRecorder{minimized: tc.minimized}
		raiseWindow(w)
		if !slices.Equal(w.calls, tc.want) {
			t.Errorf("minimized %v: calls %v, want %v", tc.minimized, w.calls, tc.want)
		}
	}
}

// Closing the last window leaves the app running only where the monitor runs
// and is on, and a platform without the monitor never reads its settings.
func TestGrowthKeepRunning(t *testing.T) {
	for _, tc := range []struct{ supported, enabled, want bool }{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	} {
		asked := false
		got := growthKeepRunning(tc.supported, func() bool { asked = true; return tc.enabled })
		if got != tc.want {
			t.Errorf("supported %v, enabled %v: keep running %v, want %v", tc.supported, tc.enabled, got, tc.want)
		}
		if asked != tc.supported {
			t.Errorf("supported %v: the settings were read: %v", tc.supported, asked)
		}
	}
}

// Closing the window keeps Cleanzo in the menu bar while a reading shows or
// monitoring is on, and quits otherwise (menubar spec §七.1).
func TestResidentOnFollowsTheReadingsAndMonitoring(t *testing.T) {
	if !growthSupported() || !menuBarSupported() {
		t.Skip("the growth monitor and the readings do not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	if !residentOn() {
		t.Error("the default readings must keep Cleanzo in the menu bar")
	}
	noMenuBar(t)
	if residentOn() {
		t.Error("no reading and monitoring off: closing the window must quit")
	}
	on := defaultGrowthConfig()
	on.Enabled = true
	if err := saveGrowthConfig(growthDir(), on); err != nil {
		t.Fatal(err)
	}
	if !residentOn() {
		t.Error("monitoring on must keep Cleanzo in the menu bar")
	}
}

// Before the app runs, as in a test or while the bindings are generated,
// there is no main thread to wait for, so applying the settings must return
// at once. The tests of SaveGrowthConfig, which applies them, rely on that.
func TestApplyGrowthLifecycleLeavesAnAppThatIsNotRunning(t *testing.T) {
	setHome(t, t.TempDir())
	done := make(chan error, 1)
	go func() { done <- applyGrowthLifecycle(GrowthConfig{Enabled: true, OpenAtLogin: true}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("applyGrowthLifecycle: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("applyGrowthLifecycle waits for a main thread that is not running")
	}
}

// The switch for opening at login shows the login item the system has, which
// the user may have removed or disabled in System Settings, not the choice
// that was saved. Where the system cannot say, as before the app runs, the
// saved choice stands. Reading the state saves nothing.
func TestGrowthStateShowsTheLoginItemTheSystemHas(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	saved := defaultGrowthConfig()
	saved.Enabled, saved.OpenAtLogin = true, true
	if err := saveGrowthConfig(growthDir(), saved); err != nil {
		t.Fatal(err)
	}
	old := growthLoginItem
	t.Cleanup(func() { growthLoginItem = old })
	for _, tc := range []struct {
		what      string
		on, known bool
		want      bool
	}{
		{"removed in System Settings", false, true, false},
		{"still there", true, true, true},
		{"the system cannot say", false, false, true},
	} {
		growthLoginItem = func() (bool, bool) { return tc.on, tc.known }
		if got := (Cleaner{}).GrowthState().Config.OpenAtLogin; got != tc.want {
			t.Errorf("%s: the page shows openAtLogin %v, want %v", tc.what, got, tc.want)
		}
	}
	if got := loadGrowthConfig(growthDir()); !got.OpenAtLogin {
		t.Errorf("reading the state changed the saved settings: %+v", got)
	}
}

// failLogin makes applying the settings fail, as a login item the system
// refuses does, for the test's length.
func failLogin(t *testing.T) error {
	t.Helper()
	err := errors.New("the login item cannot be changed")
	old := growthApply
	t.Cleanup(func() { growthApply = old })
	growthApply = func(GrowthConfig) error { return err }
	return err
}

// A login item that cannot be changed does not stop the rest of a save: the
// settings are kept and followed, the page is told, and the error is the last
// thing that happens.
func TestSaveGrowthConfigGoesOnAfterALoginError(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	t.Run("turning monitoring on takes the baseline", func(t *testing.T) {
		now, size := t0, 10*gb
		var sent []GrowthAlert
		stubGrowth(t, &now, &size, &sent)
		loginErr := failLogin(t)
		old := growth
		t.Cleanup(func() { growth = old })
		growth = &growthMonitor{launched: t0, notifs: "unknown"}
		updates := make(chan GrowthStatus, 8)
		growthChanged = func(s GrowthStatus) { updates <- s }
		asked := make(chan struct{}, 1)
		// A low battery holds the baseline back, so the tick is seen and nothing measures.
		growthPower = func(context.Context) (bool, int) { asked <- struct{}{}; return true, 19 }

		on := GrowthConfigPatch{Enabled: new(true), OpenAtLogin: new(true)}
		if err := (Cleaner{}).SaveGrowthConfig(on); !errors.Is(err, loginErr) {
			t.Fatalf("SaveGrowthConfig returned %v, want the login error", err)
		}
		if !loadGrowthConfig(growthDir()).Enabled {
			t.Error("the settings were not saved")
		}
		select {
		case <-asked:
		case <-time.After(10 * time.Second):
			t.Fatal("turning monitoring on never started the baseline")
		}
		// The tick plans its retry, the save tells the page, and the
		// notification that monitoring is on tells what it learned about
		// permission: three updates.
		for range 3 {
			select {
			case <-updates:
			case <-time.After(10 * time.Second):
				t.Fatal("the page was not told")
			}
		}
		growth.posting.Wait()
		if got, want := growth.status().NextAt, now.Unix(); got != want {
			t.Errorf("next measurement %d, want %d, the time it fell due", got, want)
		}
	})
	t.Run("turning monitoring off clears the plan", func(t *testing.T) {
		now, size := t0, 10*gb
		var sent []GrowthAlert
		stubGrowth(t, &now, &size, &sent)
		loginErr := failLogin(t)
		old := growth
		t.Cleanup(func() { growth = old })
		growth = &growthMonitor{launched: t0, notifs: "unknown", nextAt: t0.Add(5 * time.Hour)}
		var told []GrowthStatus
		growthChanged = func(s GrowthStatus) { told = append(told, s) }
		on := defaultGrowthConfig()
		on.Enabled = true
		if err := saveGrowthConfig(growthDir(), on); err != nil {
			t.Fatal(err)
		}

		if err := (Cleaner{}).SaveGrowthConfig(GrowthConfigPatch{Enabled: new(false)}); !errors.Is(err, loginErr) {
			t.Fatalf("SaveGrowthConfig returned %v, want the login error", err)
		}
		if loadGrowthConfig(growthDir()).Enabled {
			t.Error("the settings were not saved")
		}
		if got := growth.status().NextAt; got != 0 {
			t.Errorf("monitoring is off but the plan says %d", got)
		}
		if len(told) == 0 || told[len(told)-1].NextAt != 0 {
			t.Errorf("the page was told %+v, want the cleared plan last", told)
		}
	})
}

// The "turn off" line of the menu is clicked on the app's main thread, and
// SaveGrowthConfig holds growthConfigMu while it waits for that thread, so
// the click must not wait for the lock itself: it hands the work to another
// goroutine, which does what SaveGrowthConfig(off) does, with the lock held
// while the settings are applied and not after.
func TestGrowthMenuOffDoesNotHoldItsCaller(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	noMenuBar(t)
	old := growth
	t.Cleanup(func() { growth = old })
	growth = &growthMonitor{launched: t0, notifs: "unknown", nextAt: t0.Add(5 * time.Hour)}
	updates := make(chan GrowthStatus, 8)
	growthChanged = func(s GrowthStatus) { updates <- s }
	var applied []GrowthConfig
	var appliedWithoutLock bool
	oldApply := growthApply
	t.Cleanup(func() { growthApply = oldApply })
	growthApply = func(cfg GrowthConfig) error {
		if growthConfigMu.TryLock() {
			growthConfigMu.Unlock()
			appliedWithoutLock = true
		}
		applied = append(applied, cfg)
		return nil
	}

	on := defaultGrowthConfig()
	on.Enabled, on.OpenAtLogin, on.DailyGB = true, true, 5
	if err := saveGrowthConfig(growthDir(), on); err != nil {
		t.Fatal(err)
	}

	growthConfigMu.Lock()
	returned := make(chan struct{})
	go func() {
		growthMenuAction(growthMenuItem{label: "off", action: "off"})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		growthConfigMu.Unlock()
		t.Fatal("the menu action waited for the config lock on its caller's thread")
	}
	select {
	case s := <-updates:
		growthConfigMu.Unlock()
		t.Fatalf("turned off while the lock was held: %+v", s)
	case <-time.After(100 * time.Millisecond):
	}
	if !loadGrowthConfig(growthDir()).Enabled {
		growthConfigMu.Unlock()
		t.Fatal("saved while the lock was held")
	}
	growthConfigMu.Unlock()

	// setNext clears the plan, and changed tells the page and the menu.
	for range 2 {
		select {
		case <-updates:
		case <-time.After(10 * time.Second):
			t.Fatal("monitoring was never turned off")
		}
	}
	got := loadGrowthConfig(growthDir())
	if got.Enabled || got.OpenAtLogin || got.DailyGB != 5 {
		t.Errorf("config after turning off = %+v, want it off and otherwise as it was", got)
	}
	if len(applied) != 1 || applied[0].Enabled || applied[0].OpenAtLogin {
		t.Errorf("applied %+v, want the off settings once", applied)
	}
	if appliedWithoutLock {
		t.Error("the settings were applied without the config lock")
	}
	if next := growth.status().NextAt; next != 0 {
		t.Errorf("monitoring is off but the plan says %d", next)
	}
}

// An error from the login item, which the menu has no page to show, is logged
// and does not undo the rest.
func TestGrowthMenuOffGoesOnAfterALoginError(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	noMenuBar(t)
	old := growth
	t.Cleanup(func() { growth = old })
	growth = &growthMonitor{launched: t0, notifs: "unknown", nextAt: t0.Add(5 * time.Hour)}
	updates := make(chan GrowthStatus, 8)
	growthChanged = func(s GrowthStatus) { updates <- s }
	loginErr := failLogin(t)
	var logged bytes.Buffer
	logWas := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(logWas) })
	on := defaultGrowthConfig()
	on.Enabled, on.OpenAtLogin = true, true
	if err := saveGrowthConfig(growthDir(), on); err != nil {
		t.Fatal(err)
	}

	turnOffGrowth()

	if !strings.Contains(logged.String(), loginErr.Error()) {
		t.Errorf("the login error was not logged: %q", logged.String())
	}

	for range 2 {
		select {
		case <-updates:
		case <-time.After(10 * time.Second):
			t.Fatal("the page was not told")
		}
	}
	if got := loadGrowthConfig(growthDir()); got.Enabled || got.OpenAtLogin {
		t.Errorf("config after turning off = %+v", got)
	}
	if next := growth.status().NextAt; next != 0 {
		t.Errorf("monitoring is off but the plan says %d", next)
	}
}

// The readings' item shows the menu last built for the icon or for itself;
// before one is built, the lines that act for the monitoring state applied.
func TestResidentModelNow(t *testing.T) {
	shown, on := growthMenuShown, growthMenuOn
	t.Cleanup(func() { growthMenuShown, growthMenuOn = shown, on })
	growthMenuShown, growthMenuOn = nil, false
	if got, want := residentModelNow(), menuBarOnlyMenu(nativeText); !slices.Equal(got, want) {
		t.Errorf("readings only: %+v, want %+v", got, want)
	}
	growthMenuOn = true
	if got, want := residentModelNow(), growthMenuCommands(false, nativeText); !slices.Equal(got, want) {
		t.Errorf("monitoring on, nothing built: %+v, want %+v", got, want)
	}
	built := growthMenuModel(nil, true, labelsOnly)
	growthMenuShown = built
	if got := residentModelNow(); !slices.Equal(got, built) {
		t.Errorf("built: %+v, want %+v", got, built)
	}
}
