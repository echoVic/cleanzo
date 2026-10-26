package main

import (
	"errors"
	"slices"
	"strings"
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

// withSystemLoginItem returns c with the login item the system has, when the
// system can say. The user can remove or disable the item in System Settings,
// so the page's switch shows what the system has, not what was saved (spec
// §七.6), and a save that leaves the switch alone keeps it that way.
func withSystemLoginItem(c GrowthConfig) GrowthConfig {
	if on, known := growthLoginItem(); known {
		c.OpenAtLogin = on
	}
	return c
}

// GrowthState returns everything the growth page shows.
func (Cleaner) GrowthState() GrowthState {
	dir := growthDir()
	st := GrowthState{Supported: growthSupported(), Config: loadGrowthConfig(dir), Status: growth.status(), Apps: []GrowthApp{}, Alerts: []GrowthAlert{}}
	if !st.Supported {
		return st
	}
	st.Config = withSystemLoginItem(st.Config)
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

// GrowthConfigPatch is a change to the monitor's settings: only the fields
// that are set change. A page sends what the user changed and nothing else,
// so a copy of the settings it loaded earlier can never undo a change made
// since, such as the menu bar turning monitoring off.
type GrowthConfigPatch struct {
	Enabled     *bool    `json:"enabled,omitempty"`
	OpenAtLogin *bool    `json:"openAtLogin,omitempty"`
	DailyGB     *float64 `json:"dailyGB,omitempty"`
	WeeklyGB    *float64 `json:"weeklyGB,omitempty"`
	Asked       *bool    `json:"asked,omitempty"`
	Ignore      string   `json:"ignore,omitempty"`   // an owner key to ignore
	Unignore    string   `json:"unignore,omitempty"` // an owner key to stop ignoring
}

// apply returns c with p's changes. Owner keys compare lower-cased, as
// validateGrowthConfig keeps them; Ignore is applied before Unignore.
func (p GrowthConfigPatch) apply(c GrowthConfig) GrowthConfig {
	set := func(dst *bool, v *bool) {
		if v != nil {
			*dst = *v
		}
	}
	set(&c.Enabled, p.Enabled)
	set(&c.OpenAtLogin, p.OpenAtLogin)
	set(&c.Asked, p.Asked)
	if p.DailyGB != nil {
		c.DailyGB = *p.DailyGB
	}
	if p.WeeklyGB != nil {
		c.WeeklyGB = *p.WeeklyGB
	}
	key := func(k string) string { return strings.ToLower(strings.TrimSpace(k)) }
	c.Ignored = slices.Clone(c.Ignored)
	if k := key(p.Ignore); k != "" {
		c.Ignored = append(c.Ignored, k) // validateGrowthConfig drops a repeat
	}
	if k := key(p.Unignore); k != "" {
		c.Ignored = slices.DeleteFunc(c.Ignored, func(have string) bool { return key(have) == k })
	}
	return c
}

// SaveGrowthConfig changes the monitor's settings as p asks, validates and
// saves them, then applies them: the menu bar icon, staying open without
// windows, the login item. Turning monitoring on takes a baseline record at
// once.
func (Cleaner) SaveGrowthConfig(p GrowthConfigPatch) error {
	if !growthSupported() {
		return errors.New("unsupported")
	}
	// Held from loading the current settings to applying the new ones, so the
	// menu bar's "turn off", which takes it too, cannot slip in between.
	growthConfigMu.Lock()
	defer growthConfigMu.Unlock()
	dir := growthDir()
	// The settings as the page shows them: a patch that leaves the login item
	// alone must not bring back one the user removed in System Settings.
	cur := withSystemLoginItem(loadGrowthConfig(dir))
	clean, err := validateGrowthConfig(p.apply(cur))
	if err != nil {
		return err
	}
	if err := saveGrowthConfig(dir, clean); err != nil {
		return err
	}
	// A login item that cannot be changed does not undo the rest: the settings
	// are saved, so the monitor follows them, and the page hears the error last.
	applyErr := growthApply(clean)
	if clean.Enabled && !cur.Enabled {
		go growth.tick(true)
	}
	if !clean.Enabled {
		growth.setNext(time.Time{}) // nothing is planned while monitoring is off
	}
	growth.changed()
	return applyErr
}
