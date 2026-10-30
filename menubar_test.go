package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// noMenuBar turns every reading off for the test, as Settings would.
func noMenuBar(t *testing.T) {
	t.Helper()
	if err := saveMenuBarConfig(menuBarPath(), menuBarConfig{Items: []string{}}); err != nil {
		t.Fatal(err)
	}
}

// withBattery gives the test's Mac a battery, or none.
func withBattery(t *testing.T, has bool) {
	t.Helper()
	old := menuBarBattery
	t.Cleanup(func() { menuBarBattery = old })
	menuBarBattery = func() bool { return has }
}

// stubResident records what SaveMenuBar applies instead of applying it.
func stubResident(t *testing.T) *[]menuBarConfig {
	t.Helper()
	old := residentApply
	t.Cleanup(func() { residentApply = old })
	var applied []menuBarConfig
	residentApply = func(_ GrowthConfig, mb menuBarConfig) error {
		applied = append(applied, mb)
		return nil
	}
	return &applied
}

func TestMenuBarConfigDefaultsAndNormalizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "menubar.json")
	if c := loadMenuBarConfig(path, true); !slices.Equal(c.Items, []string{"cpu", "mem"}) {
		t.Errorf("no file: %+v, want CPU and memory", c)
	}
	if got := normalizeMenuBarItems([]string{"battery", "net", "bogus", "cpu", "net"}, true); !slices.Equal(got, []string{"cpu", "net", "battery"}) {
		t.Errorf("normalized %v", got)
	}
	if got := normalizeMenuBarItems([]string{"battery"}, false); got == nil || len(got) != 0 {
		t.Errorf("a Mac without a battery shows %v", got)
	}
	if got := menuBarAvailable(false); slices.Contains(got, "battery") || len(got) != 6 {
		t.Errorf("available without a battery: %v", got)
	}
	if err := saveMenuBarConfig(path, menuBarConfig{Items: []string{}}); err != nil {
		t.Fatal(err)
	}
	if c := loadMenuBarConfig(path, true); c.Items == nil || len(c.Items) != 0 {
		t.Errorf("all off: %+v, want none", c)
	}
	for _, broken := range []string{`{`, `{"layout": 2}`, `{"items": null}`} {
		if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
			t.Fatal(err)
		}
		if c := loadMenuBarConfig(path, true); !slices.Equal(c.Items, menuBarDefault) {
			t.Errorf("%s: %+v, want the defaults", broken, c)
		}
	}
}

func TestMenuBarTooltipsAreTranslated(t *testing.T) {
	for _, item := range menuBarItems {
		for _, locale := range []string{"zh-Hans", "en"} {
			if nativeTextFor(locale, "menubar.tip."+item, nil) == "" {
				t.Errorf("no %s tooltip for %s", locale, item)
			}
		}
	}
	if got := nativeTextFor("zh-Hans", "menubar.tray.settings", nil); got != "菜单栏设置…" {
		t.Errorf("settings line %q", got)
	}
}

func TestMenuBarOnFollowsTheSavedReadings(t *testing.T) {
	if !menuBarSupported() {
		t.Skip("the menu bar readings are macOS only")
	}
	setHome(t, t.TempDir())
	withBattery(t, false)
	if !menuBarOn() {
		t.Error("the defaults show CPU and memory")
	}
	if err := saveMenuBarConfig(menuBarPath(), menuBarConfig{Items: []string{"battery"}}); err != nil {
		t.Fatal(err)
	}
	if menuBarOn() {
		t.Error("only the battery is chosen, and this Mac has none")
	}
	noMenuBar(t)
	if menuBarOn() {
		t.Error("all off")
	}
}

func TestSaveMenuBarKeepsAndAppliesTheReadings(t *testing.T) {
	if !menuBarSupported() {
		t.Skip("the menu bar readings are macOS only")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	withBattery(t, true)
	applied := stubResident(t)
	save := func(items ...string) menuBarConfig {
		t.Helper()
		if err := (Cleaner{}).SaveMenuBar(items); err != nil {
			t.Fatal(err)
		}
		return loadMenuBarConfig(menuBarPath(), true)
	}
	for _, tc := range []struct {
		save []string
		want []string
	}{
		{[]string{"mem", "cpu", "cpu", "bogus"}, []string{"cpu", "mem"}},
		{[]string{"gpu", "cpu"}, []string{"cpu", "gpu"}},
		{nil, []string{}},
		{[]string{"temp"}, []string{"temp"}},
	} {
		if c := save(tc.save...); !slices.Equal(c.Items, tc.want) {
			t.Errorf("save %v: kept %v, want %v", tc.save, c.Items, tc.want)
		}
	}
	if len(*applied) != 4 || !slices.Equal((*applied)[3].Items, []string{"temp"}) {
		t.Errorf("applied %+v", *applied)
	}
	if got := (Cleaner{}).MenuBar(); !got.Supported || !slices.Equal(got.Items, []string{"temp"}) || !slices.Contains(got.Available, "battery") {
		t.Errorf("MenuBar() = %+v", got)
	}
}

func TestSaveMenuBarTurnsTheLoginItemOffWithTheLastReading(t *testing.T) {
	if !menuBarSupported() {
		t.Skip("the menu bar readings are macOS only")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	withBattery(t, true)
	old := growth
	t.Cleanup(func() { growth = old })
	growth = &growthMonitor{launched: t0, notifs: "unknown"}
	told := 0
	growthChanged = func(GrowthStatus) { told++ }
	stubResident(t)
	g := defaultGrowthConfig()
	g.OpenAtLogin = true // monitoring off: the readings keep the login item
	if err := saveGrowthConfig(growthDir(), g); err != nil {
		t.Fatal(err)
	}

	if err := (Cleaner{}).SaveMenuBar([]string{"cpu"}); err != nil {
		t.Fatal(err)
	}
	if !loadGrowthConfig(growthDir()).OpenAtLogin || told != 0 {
		t.Fatal("a reading still shows, but the login item went")
	}
	if err := (Cleaner{}).SaveMenuBar(nil); err != nil {
		t.Fatal(err)
	}
	if got := loadGrowthConfig(growthDir()); got.OpenAtLogin || told != 1 {
		t.Errorf("the last reading went with monitoring off: login %v, pages told %d times, want off and once", got.OpenAtLogin, told)
	}

	g.Enabled, g.OpenAtLogin = true, true
	if err := saveGrowthConfig(growthDir(), g); err != nil {
		t.Fatal(err)
	}
	if err := (Cleaner{}).SaveMenuBar([]string{"mem"}); err != nil {
		t.Fatal(err)
	}
	if err := (Cleaner{}).SaveMenuBar(nil); err != nil {
		t.Fatal(err)
	}
	if !loadGrowthConfig(growthDir()).OpenAtLogin {
		t.Error("monitoring is on, but the login item went with the readings")
	}
}

// With a reading in the menu bar, turning monitoring off from the menu keeps
// the login item (menubar spec §七.3).
func TestGrowthMenuOffKeepsTheLoginItemForTheReadings(t *testing.T) {
	if !growthSupported() || !menuBarSupported() {
		t.Skip("the growth monitor and the readings do not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	old, oldApply := growth, growthApply
	t.Cleanup(func() { growth, growthApply = old, oldApply })
	growth = &growthMonitor{launched: t0, notifs: "unknown", nextAt: t0.Add(5 * time.Hour)}
	var applied []GrowthConfig
	growthApply = func(c GrowthConfig) error { applied = append(applied, c); return nil }
	on := defaultGrowthConfig()
	on.Enabled, on.OpenAtLogin = true, true
	if err := saveGrowthConfig(growthDir(), on); err != nil {
		t.Fatal(err)
	}

	turnOffGrowth()

	if got := loadGrowthConfig(growthDir()); got.Enabled || !got.OpenAtLogin {
		t.Errorf("config %+v, want monitoring off and the login item kept", got)
	}
	if len(applied) != 1 || !applied[0].OpenAtLogin || applied[0].Enabled {
		t.Errorf("applied %+v", applied)
	}
}

// Turning monitoring off from Settings keeps the login item while a reading
// shows, and a later save without readings lets it go (menubar spec §七.3).
func TestSaveGrowthConfigKeepsTheLoginItemForTheReadings(t *testing.T) {
	if !growthSupported() || !menuBarSupported() {
		t.Skip("the growth monitor and the readings do not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	old, oldApply := growth, growthApply
	t.Cleanup(func() { growth, growthApply = old, oldApply })
	growth = &growthMonitor{launched: t0, notifs: "unknown"}
	growthApply = func(GrowthConfig) error { return nil }
	on := defaultGrowthConfig()
	on.Enabled, on.OpenAtLogin = true, true
	if err := saveGrowthConfig(growthDir(), on); err != nil {
		t.Fatal(err)
	}
	if err := (Cleaner{}).SaveGrowthConfig(GrowthConfigPatch{Enabled: new(false)}); err != nil {
		t.Fatal(err)
	}
	if got := loadGrowthConfig(growthDir()); got.Enabled || !got.OpenAtLogin {
		t.Errorf("readings on, monitoring turned off: %+v, want the login item kept", got)
	}
	noMenuBar(t)
	if err := (Cleaner{}).SaveGrowthConfig(GrowthConfigPatch{DailyGB: new(5.0)}); err != nil {
		t.Fatal(err)
	}
	if loadGrowthConfig(growthDir()).OpenAtLogin {
		t.Error("no reading and monitoring off, but the login item stayed")
	}
}

func TestMenuBarPanelSourceReadsWhatThePanelShows(t *testing.T) {
	profiled := 0
	base := statusSource{
		battery: func(context.Context) *BatteryStatus { profiled++; return &BatteryStatus{Percent: 80} },
		machine: func() machineSampler { return nil },
	}
	for _, tc := range []struct {
		item              string
		machine, profiler bool
	}{
		{"cpu", true, false}, {"mem", false, false}, {"gpu", true, false}, {"net", true, false},
		{"disk", true, false}, {"temp", true, false}, {"battery", false, true},
	} {
		profiled = 0
		src := menuBarPanelSource(base, tc.item)
		if (src.machine != nil) != tc.machine {
			t.Errorf("%s: machine reader %v, want %v", tc.item, src.machine != nil, tc.machine)
		}
		b := src.battery(context.Background())
		if (profiled == 1) != tc.profiler {
			t.Errorf("%s: system_profiler ran %d times", tc.item, profiled)
		}
		if tc.item != "battery" && tc.item != "temp" && b != nil {
			t.Errorf("%s: read the battery %+v", tc.item, b)
		}
	}
}

func TestWatchMenuBarPanelRefusesAnUnknownReading(t *testing.T) {
	want := "unknown-item"
	if !menuBarSupported() {
		want = "unsupported"
	}
	var ce codeError
	if err := (Cleaner{}).WatchMenuBarPanel(context.Background(), "fans", nil); !errors.As(err, &ce) || ce.Code != want {
		t.Errorf("err = %v, want %s", err, want)
	}
}

func TestMenuBarHolds(t *testing.T) {
	w := &mygo.Window{}
	if menuBarHeld(w) {
		t.Fatal("held before anything asked")
	}
	(Cleaner{}).MenuBarHold(context.Background(), true) // no calling window: nothing to hold
	menuBarHolds.Lock()
	n := len(menuBarHolds.held)
	menuBarHolds.held[w] = time.Now()
	menuBarHolds.Unlock()
	if n != 0 {
		t.Errorf("%d panels held without a caller", n)
	}
	if !menuBarHeld(w) {
		t.Error("not held")
	}
	menuBarRelease(w)
	if menuBarHeld(w) {
		t.Error("still held after release")
	}
	menuBarHolds.Lock()
	menuBarHolds.held[w] = time.Now().Add(-menuBarHoldMax)
	menuBarHolds.Unlock()
	if menuBarHeld(w) {
		t.Error("a hold older than menuBarHoldMax still holds")
	}
	menuBarHolds.Lock()
	menuBarHolds.held[w] = time.Now()
	menuBarHolds.Unlock()
	menuBarReleaseAll()
	if menuBarHeld(w) {
		t.Error("quitting did not release the hold")
	}
}

// The items' names never change: macOS keeps under each where the user
// dragged it with ⌘, and a new name would lose that place.
func TestMenuBarNamesStay(t *testing.T) {
	if menuBarIconID != "cleanzo" {
		t.Errorf("icon %q", menuBarIconID)
	}
	for item, want := range map[string]string{"cpu": "cleanzo.cpu", "mem": "cleanzo.mem", "battery": "cleanzo.battery"} {
		if got := menuBarReadingID(item); got != want {
			t.Errorf("%s: %q, want %q", item, got, want)
		}
	}
}
