package main

import (
	"bytes"
	"image/png"
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

// The mygo menu has the lines of the model, a separator for each zero item.
func TestGrowthTrayMenuMirrorsTheModel(t *testing.T) {
	model := []growthMenuItem{
		{label: "A  +3.0 GB", action: "focus", key: "app:a"},
		{},
		{label: "Measuring…", disabled: true},
		{label: "Quit Cleanzo", action: "quit"},
	}
	items := growthTrayMenu(model).Items()
	if len(items) != len(model) {
		t.Fatalf("%d menu items for %d lines", len(items), len(model))
	}
	for i, line := range model {
		it := items[i]
		if line == (growthMenuItem{}) {
			if it.Type != mygo.MenuItemSeparator {
				t.Errorf("line %d: type %q, want a separator", i, it.Type)
			}
			continue
		}
		if it.Label != line.label || it.Disabled != line.disabled || it.Click == nil {
			t.Errorf("line %d: label %q, disabled %v, click %v; want %+v", i, it.Label, it.Disabled, it.Click != nil, line)
		}
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

// The "turn off" line of the menu is clicked on the app's main thread, and
// SaveGrowthConfig holds growthConfigMu while it waits for that thread, so
// the click must not wait for the lock itself: it hands the work to another
// goroutine, which does what SaveGrowthConfig(off) does.
func TestGrowthMenuOffDoesNotHoldItsCaller(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	old := growth
	t.Cleanup(func() { growth = old })
	growth = &growthMonitor{launched: t0, notifs: "unknown", nextAt: t0.Add(5 * time.Hour)}
	updates := make(chan GrowthStatus, 8)
	growthChanged = func(s GrowthStatus) { updates <- s }

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
	if next := growth.status().NextAt; next != 0 {
		t.Errorf("monitoring is off but the plan says %d", next)
	}
}
