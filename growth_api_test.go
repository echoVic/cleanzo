package main

import (
	"slices"
	"testing"
)

// A page sends only what the user changed. A copy of the settings it loaded
// before monitoring was turned off elsewhere, such as from the menu bar, must
// not turn it back on with its next change.
func TestSaveGrowthConfigMergesPatches(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	for name, off := range map[string]func(){
		"the menu bar": turnOffGrowth,
		"a patch": func() {
			if err := (Cleaner{}).SaveGrowthConfig(GrowthConfigPatch{Enabled: new(false)}); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run("turned off from "+name, func(t *testing.T) {
			now, size := t0, 10*gb
			var sent []GrowthAlert
			stubGrowth(t, &now, &size, &sent)
			old := growth
			t.Cleanup(func() { growth = old })
			growth = &growthMonitor{launched: t0, notifs: "unknown"}
			on := defaultGrowthConfig()
			on.Enabled, on.Asked = true, true
			if err := saveGrowthConfig(growthDir(), on); err != nil {
				t.Fatal(err)
			}

			off()
			if err := (Cleaner{}).SaveGrowthConfig(GrowthConfigPatch{DailyGB: new(5.0)}); err != nil {
				t.Fatal(err)
			}
			want := defaultGrowthConfig()
			want.DailyGB, want.Asked = 5, true
			if got := loadGrowthConfig(growthDir()); !slices.Equal(got.Ignored, want.Ignored) || got.Enabled || got.OpenAtLogin ||
				got.DailyGB != want.DailyGB || got.WeeklyGB != want.WeeklyGB || !got.Asked {
				t.Fatalf("after a threshold patch: %+v, want %+v", got, want)
			}
		})
	}
}

// Ignoring and no longer ignoring an owner change that owner alone. Keys are
// kept lower-cased and once, and a refused patch saves nothing.
func TestSaveGrowthConfigIgnoresAndUnignoresOneOwner(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	old := growth
	t.Cleanup(func() { growth = old })
	growth = &growthMonitor{launched: t0, notifs: "unknown"}
	start := defaultGrowthConfig()
	start.WeeklyGB = 20
	if err := saveGrowthConfig(growthDir(), start); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		patch GrowthConfigPatch
		want  []string
	}{
		{GrowthConfigPatch{Ignore: " App:ToDesk "}, []string{"app:todesk"}},
		{GrowthConfigPatch{Ignore: "app:todesk"}, []string{"app:todesk"}},
		{GrowthConfigPatch{Ignore: "dir:/h/.npm"}, []string{"app:todesk", "dir:/h/.npm"}},
		{GrowthConfigPatch{Unignore: "APP:TODESK"}, []string{"dir:/h/.npm"}},
		{GrowthConfigPatch{Unignore: "app:gone"}, []string{"dir:/h/.npm"}},
	} {
		if err := (Cleaner{}).SaveGrowthConfig(step.patch); err != nil {
			t.Fatal(err)
		}
		got := loadGrowthConfig(growthDir())
		if !slices.Equal(got.Ignored, step.want) || got.Enabled || got.DailyGB != 2 || got.WeeklyGB != 20 {
			t.Fatalf("after %+v: %+v, want ignored %q and the rest as it was", step.patch, got, step.want)
		}
	}
	if err := (Cleaner{}).SaveGrowthConfig(GrowthConfigPatch{WeeklyGB: new(7.0), Ignore: "app:x"}); err == nil {
		t.Fatal("a threshold outside the choices must be refused")
	}
	if got := loadGrowthConfig(growthDir()); got.WeeklyGB != 20 || !slices.Equal(got.Ignored, []string{"dir:/h/.npm"}) {
		t.Fatalf("a refused patch changed the settings: %+v", got)
	}
}

// A patch that leaves the login item alone keeps the one the system has: the
// user may have removed it in System Settings, and a threshold change must not
// bring it back. A patch that sets it is applied as it says.
func TestSaveGrowthConfigKeepsTheSystemLoginItem(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	old, oldItem, oldApply := growth, growthLoginItem, growthApply
	t.Cleanup(func() { growth, growthLoginItem, growthApply = old, oldItem, oldApply })
	growth = &growthMonitor{launched: t0, notifs: "unknown"}
	growthLoginItem = func() (bool, bool) { return false, true } // removed in System Settings
	var applied []GrowthConfig
	growthApply = func(c GrowthConfig) error { applied = append(applied, c); return nil }
	saved := defaultGrowthConfig()
	saved.Enabled, saved.OpenAtLogin = true, true
	if err := saveGrowthConfig(growthDir(), saved); err != nil {
		t.Fatal(err)
	}

	if err := (Cleaner{}).SaveGrowthConfig(GrowthConfigPatch{DailyGB: new(5.0)}); err != nil {
		t.Fatal(err)
	}
	if got := loadGrowthConfig(growthDir()); got.OpenAtLogin || !got.Enabled || got.DailyGB != 5 {
		t.Errorf("saved %+v, want the login item off as the system has it", got)
	}
	if err := (Cleaner{}).SaveGrowthConfig(GrowthConfigPatch{OpenAtLogin: new(true)}); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 || applied[0].OpenAtLogin || !applied[1].OpenAtLogin {
		t.Errorf("applied %+v, want the login item left off, then turned on", applied)
	}
}
