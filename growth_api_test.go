package main

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"
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

// countOwners replaces growthOwnerOf, counting how often it is asked and how
// often the owner it gives is asked. builtAt stands for when the attributor was
// built.
func countOwners(t *testing.T, builtAt *time.Time) (asked, owners *int) {
	t.Helper()
	asked, owners = new(0), new(0)
	growthOwnerOf = func() (func(growthEntry) growthOwner, time.Time) {
		*asked++
		return func(e growthEntry) growthOwner { *owners++; return byBase(e) }, *builtAt
	}
	return asked, owners
}

// Building the attributor reads the Info.plist of every app, a few seconds on
// a Mac with many apps. The growth page asks for it only when it has rows to
// build, and the status page and Settings, which show no rows, never do.
func TestGrowthStateAndSummaryBuildNoAttributorWithoutNeed(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	var builtAt time.Time
	asked, _ := countOwners(t, &builtAt)

	if st := (Cleaner{}).GrowthState(); st.Records != 0 || len(st.Apps) != 0 || *asked != 0 {
		t.Fatalf("no records: %d records, %d rows, the attributor asked %d times", st.Records, len(st.Apps), *asked)
	}
	(&growthMonitor{launched: t0, notifs: "unknown"}).measureOnce(context.Background())
	*asked = 0
	sum := (Cleaner{}).GrowthSummary()
	if !sum.Supported || sum.Alerts == nil || sum.Config.DailyGB != 2 || sum.Status.LastAt != t0.Unix() || *asked != 0 {
		t.Fatalf("summary %+v, the attributor asked %d times", sum, *asked)
	}
	if st := (Cleaner{}).GrowthState(); st.Records != 1 || len(st.Apps) != 1 || *asked != 1 {
		t.Fatalf("one record: %d records, %d rows, the attributor asked %d times", st.Records, len(st.Apps), *asked)
	}
}

// The summary shows the login item the system has, as GrowthState does.
func TestGrowthSummaryShowsTheLoginItemTheSystemHas(t *testing.T) {
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
	growthLoginItem = func() (bool, bool) { return false, true }
	if got := (Cleaner{}).GrowthSummary().Config; got.OpenAtLogin || !got.Enabled {
		t.Errorf("summary config %+v, want the login item off as the system has it", got)
	}
}

// The page and the menu bar ask for the rows at every update. While the
// records, the ignored owners and the attributor stay the same, the rows built
// last time are reused; a change in any of them builds them again.
func TestGrowthStateReusesRowsWhileNothingChanged(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	builtAt := t0
	_, owners := countOwners(t, &builtAt)
	m := &growthMonitor{launched: t0, notifs: "unknown"}
	m.measureOnce(context.Background())
	now, size = t0.Add(24*time.Hour), 14*gb
	m.measureOnce(context.Background())

	first := (Cleaner{}).GrowthState().Apps
	built := *owners
	if built == 0 || len(first) != 1 || first[0].Day == nil || first[0].Day.Bytes != 4*gb {
		t.Fatalf("rows %+v, owners asked %d times", first, built)
	}
	if again := (Cleaner{}).GrowthState().Apps; *owners != built || !reflect.DeepEqual(again, first) {
		t.Fatalf("nothing changed, yet the rows were built again (owners asked %d times, was %d): %+v", *owners, built, again)
	}
	rebuilt := func(what string) {
		t.Helper()
		before := *owners
		(Cleaner{}).GrowthState()
		if *owners == before {
			t.Errorf("%s: the rows were not built again", what)
		}
	}
	c := loadGrowthConfig(growthDir())
	c.Ignored = []string{"app:todesk"}
	if err := saveGrowthConfig(growthDir(), c); err != nil {
		t.Fatal(err)
	}
	rebuilt("an owner was ignored")
	if st := (Cleaner{}).GrowthState(); !st.Apps[0].Ignored {
		t.Errorf("the reused rows do not show the owner ignored: %+v", st.Apps[0])
	}
	builtAt = builtAt.Add(time.Minute)
	rebuilt("the attributor was built again")
	now, size = t0.Add(48*time.Hour), 15*gb
	m.measureOnce(context.Background())
	rebuilt("a record was added")
}

// A record dated after now, which a clock set back leaves behind, is not the
// newest record: the page counts and shows the records up to now.
func TestGrowthStateLeavesOutRecordsFromTheFuture(t *testing.T) {
	if !growthSupported() {
		t.Skip("the growth monitor does not run on this platform")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	for _, s := range []growthSnapshot{
		at(-24*time.Hour, true, done(todesk, 5*gb)),
		at(2*time.Hour, true, done(todesk, 50*gb)),
	} {
		if err := saveGrowthSnapshot(growthDir(), s, now); err != nil {
			t.Fatal(err)
		}
	}
	st := (Cleaner{}).GrowthState()
	if st.Records != 1 || len(st.Apps) != 1 || st.Apps[0].Size != 5*gb || st.Status.LastAt != t0.Add(-24*time.Hour).Unix() {
		t.Fatalf("records %d, rows %+v, last %d: want the record from yesterday only", st.Records, st.Apps, st.Status.LastAt)
	}
}
