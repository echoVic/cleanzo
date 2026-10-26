package main

import (
	"errors"
	"time"

	"github.com/egoist/mygo"
)

// GrowthStatus tells whether measuring runs and when (spec §六).
type GrowthStatus struct {
	Running       bool   `json:"running"`
	LastAt        int64  `json:"lastAt"` // unix seconds; 0: never
	NextAt        int64  `json:"nextAt"` // unix seconds; 0: not planned (monitoring off)
	FDA           bool   `json:"fda"`
	Notifications string `json:"notifications"` // "unknown", "allowed", "denied" or "unsupported"
}

// GrowthState is everything the growth page shows.
type GrowthState struct {
	Supported bool          `json:"supported"` // false on Windows
	Config    GrowthConfig  `json:"config"`
	Status    GrowthStatus  `json:"status"`
	Records   int           `json:"records"`
	Apps      []GrowthApp   `json:"apps"`   // largest first
	Alerts    []GrowthAlert `json:"alerts"` // raised in the last seven days, newest first
}

// GrowthUpdated tells pages that measuring started or finished, or that the
// monitor's settings changed.
var GrowthUpdated = mygo.NewEvent[GrowthStatus]("growth:updated")

// GrowthFocus asks the page to show one owner, by key: a notification or a
// menu item was clicked.
var GrowthFocus = mygo.NewEvent[string]("growth:focus")

// growthLoginItem tells whether the system has Cleanzo as a login item, and
// whether it can say so at all: it cannot before the app runs, as in a test.
// Tests replace it.
var growthLoginItem = func() (on, known bool) {
	if !mygo.App.IsReady() {
		return false, false
	}
	return mygo.App.OpenAtLogin(), true
}

// GrowthState returns everything the growth page shows.
func (Cleaner) GrowthState() GrowthState {
	dir := growthDir()
	st := GrowthState{Supported: growthSupported(), Config: loadGrowthConfig(dir), Status: growth.status(), Apps: []GrowthApp{}, Alerts: []GrowthAlert{}}
	if !st.Supported {
		return st
	}
	// The login item is the system's: the user can remove or disable it in
	// System Settings, so the switch shows what the system has, not what was
	// saved (spec §七.6).
	if on, known := growthLoginItem(); known {
		st.Config.OpenAtLogin = on
	}
	history := loadGrowthSnapshots(dir)
	st.Records = len(history)
	st.Apps = buildGrowthApps(history, st.Config, growthOwnerOf(), homeDir(), time.Local)
	st.Alerts = loadGrowthNotified(dir).recent(growthNow())
	return st
}

// GrowthMeasureNow starts a measurement unless one is running. It works
// with monitoring off too, without notifications.
func (Cleaner) GrowthMeasureNow() error {
	if !growthSupported() {
		return errors.New("unsupported")
	}
	growth.run(true)
	return nil
}

// SaveGrowthConfig validates and saves the monitor's settings, then applies
// them: the menu bar icon, staying open without windows, the login item.
// Turning monitoring on takes a baseline record at once.
func (Cleaner) SaveGrowthConfig(c GrowthConfig) error {
	if !growthSupported() {
		return errors.New("unsupported")
	}
	clean, err := validateGrowthConfig(c)
	if err != nil {
		return err
	}
	// Held from loading the earlier settings to applying the new ones, so the
	// menu bar's "turn off", which takes it too, cannot slip in between.
	growthConfigMu.Lock()
	defer growthConfigMu.Unlock()
	dir := growthDir()
	was := loadGrowthConfig(dir).Enabled
	if err := saveGrowthConfig(dir, clean); err != nil {
		return err
	}
	// A login item that cannot be changed does not undo the rest: the settings
	// are saved, so the monitor follows them, and the page hears the error last.
	applyErr := growthApply(clean)
	if clean.Enabled && !was {
		go growth.tick(true)
	}
	if !clean.Enabled {
		growth.setNext(time.Time{}) // nothing is planned while monitoring is off
	}
	growth.changed()
	return applyErr
}
