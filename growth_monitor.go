package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
)

// Seams. Tests replace these; the app uses them as they are.
var (
	growthNow = time.Now
	// growthMeasure takes one record of this Mac.
	growthMeasure = func(ctx context.Context, prev *growthSnapshot) growthSnapshot {
		fda := fullDiskAccess()
		targets := growthTargets(growthRoots(homeDir(), string(filepath.Separator)), fda, filepath.Dir(configPath()))
		return takeGrowthSnapshot(ctx, targets, prev, fda, growthNow())
	}
	growthNotify  = postGrowthNotification
	growthChanged = func(s GrowthStatus) {
		GrowthUpdated.Broadcast(s)
		go refreshGrowthTray()
	}
	growthOwnerOf = func() func(growthEntry) growthOwner { return currentGrowthAttributor().owner }
	growthPower   = growthBattery
)

// growthMonitor measures in the background while monitoring is on, and on
// request.
type growthMonitor struct {
	mu       sync.Mutex
	started  bool
	running  bool
	manual   bool // the running measurement was asked for
	cancel   context.CancelFunc
	launched time.Time
	nextAt   time.Time
	notifs   string // "unknown", "allowed", "denied" or "unsupported"
}

// growth is the app's monitor.
var growth = &growthMonitor{launched: time.Now(), notifs: "unknown"}

// growthConfigMu serializes every change of config.json: loading it, changing
// it and saving it again. SaveGrowthConfig and the menu bar's "turn off"
// action both hold it, so neither overwrites the other's change with a copy
// loaded before it.
var growthConfigMu sync.Mutex

// userScans counts the scans and cleans the user is running; background
// measuring waits for them (spec §四).
var userScans atomic.Int32

// userScan marks a user scan or clean for its duration, stopping a
// background measurement under way: defer userScan()().
func userScan() func() {
	userScans.Add(1)
	growth.cancelBackground()
	return func() { userScans.Add(-1) }
}

// growthNextRun is when the next background measurement is due (spec §四):
//   - right away, when monitoring was just turned on without a record from
//     the last 20 hours;
//   - five minutes after launch, once the last record is 20 hours old;
//   - otherwise 24 hours after the last record.
//
// A last record dated after now, which a wrong clock leaves behind, counts
// as no record at all.
func growthNextRun(last, launched, now time.Time, justEnabled bool) time.Time {
	stale := last.IsZero() || last.After(now) || now.Sub(last) >= 20*time.Hour
	if justEnabled && stale {
		return now
	}
	if stale {
		if t := launched.Add(5 * time.Minute); t.After(now) {
			return t
		}
		return now
	}
	return last.Add(24 * time.Hour)
}

// growthDeferral tells how long a due measurement waits (spec §四):
//   - on battery below 20%, half an hour;
//   - during a user scan, ten minutes;
//   - otherwise 0.
func growthDeferral(onBattery bool, percent int, scans int32) time.Duration {
	switch {
	case onBattery && percent < 20:
		return 30 * time.Minute
	case scans > 0:
		return 10 * time.Minute
	}
	return 0
}

// start begins the background loop once. It checks every 15 minutes, and
// whenever the Mac wakes, against wall-clock time, so sleep never stretches
// the schedule.
func (m *growthMonitor) start() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()
	mygo.Power.OnResume(func() { go m.tick(false) })
	go func() {
		m.tick(false)
		for range time.Tick(15 * time.Minute) {
			m.tick(false)
		}
	}()
}

// tick measures when monitoring is on and a measurement is due and allowed.
// The power source answers through the app's main thread, so it is asked
// with a deadline and a tick never waits longer than 30 seconds for it.
func (m *growthMonitor) tick(justEnabled bool) {
	if !growthSupported() {
		return
	}
	dir := growthDir()
	if !loadGrowthConfig(dir).Enabled {
		m.setNext(time.Time{})
		return
	}
	now := growthNow()
	due := growthNextRun(lastGrowthAt(dir), m.launched, now, justEnabled)
	if now.Before(due) {
		m.setNext(due)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	onBattery, percent := growthPower(ctx)
	cancel()
	if wait := growthDeferral(onBattery, percent, userScans.Load()); wait > 0 {
		m.setNext(now.Add(wait))
		return
	}
	m.run(false)
}

// setNext records when the next measurement is planned, for the page.
func (m *growthMonitor) setNext(t time.Time) {
	m.mu.Lock()
	changed := !m.nextAt.Equal(t)
	m.nextAt = t
	m.mu.Unlock()
	if changed {
		growthChanged(m.status())
	}
}

// run starts one measurement unless one is under way, reporting whether it
// started. A background run stops when the user starts a scan; a manual one
// does not.
func (m *growthMonitor) run(manual bool) bool {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.running, m.manual, m.cancel = true, manual, cancel
	m.mu.Unlock()
	growthChanged(m.status())
	go func() {
		m.measureOnce(ctx)
		cancel()
		m.mu.Lock()
		m.running, m.manual, m.cancel = false, false, nil
		m.mu.Unlock()
		if loadGrowthConfig(growthDir()).Enabled {
			m.setNext(growthNextRun(lastGrowthAt(growthDir()), m.launched, growthNow(), false))
		}
		growthChanged(m.status())
	}()
	return true
}

// measureOnce takes one record and keeps the alerts it raises. With
// monitoring on, it also posts them within the limits of spec §五. It saves
// nothing when ctx ends first.
func (m *growthMonitor) measureOnce(ctx context.Context) {
	dir := growthDir()
	history := loadGrowthSnapshots(dir)
	var prev *growthSnapshot
	if len(history) > 0 {
		prev = &history[len(history)-1]
	}
	snap := growthMeasure(ctx, prev)
	if ctx.Err() != nil {
		return
	}
	now := growthNow()
	if saveGrowthSnapshot(dir, snap, now) != nil {
		return
	}
	cfg := loadGrowthConfig(dir)
	alerts := evaluateGrowth(history, snap, cfg, growthOwnerOf())
	n := loadGrowthNotified(dir)
	n.remember(alerts, now)
	if cfg.Enabled {
		for _, a := range pickGrowthNotifications(alerts, &n, now) {
			m.noteNotify(growthNotify(a))
		}
	}
	_ = saveGrowthNotified(dir, n)
}

// noteNotify keeps whether notifications can be shown, for the page.
func (m *growthMonitor) noteNotify(err error) {
	state := "allowed"
	switch {
	case errors.Is(err, mygo.ErrNotificationsDenied):
		state = "denied"
	case errors.Is(err, errGrowthNoNotifications):
		state = "unsupported"
	case err != nil:
		return
	}
	m.mu.Lock()
	m.notifs = state
	m.mu.Unlock()
}

// cancelBackground stops a background measurement under way.
func (m *growthMonitor) cancelBackground() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running && !m.manual && m.cancel != nil {
		m.cancel()
	}
}

// status is what the page shows about measuring.
func (m *growthMonitor) status() GrowthStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := GrowthStatus{Running: m.running, FDA: fullDiskAccess(), Notifications: m.notifs}
	if last := lastGrowthAt(growthDir()); !last.IsZero() {
		s.LastAt = last.Unix()
	}
	if !m.nextAt.IsZero() {
		s.NextAt = m.nextAt.Unix()
	}
	return s
}

// changed tells the pages and the menu that something moved.
func (m *growthMonitor) changed() { growthChanged(m.status()) }

// errGrowthNoNotifications means this build cannot show notifications, like
// an unpackaged `go run`.
var errGrowthNoNotifications = errors.New("notifications unsupported")

// postGrowthNotification shows one alert. Clicking it opens the growth page
// at the app, even after a relaunch (App.OnNotificationClick in growth_app.go).
func postGrowthNotification(a GrowthAlert) error {
	if !mygo.NotificationsSupported() {
		return errGrowthNoNotifications
	}
	locale := mygo.App.Locale()
	return mygo.NewNotification(mygo.NotificationOptions{
		ID: "growth:" + a.Key, Title: growthTitle(locale, a), Body: growthBody(locale, a), Group: "growth",
	}).Show()
}

// growthTitle says who grew how much, and over which window.
func growthTitle(locale string, a GrowthAlert) string {
	key := "growth.notify." + a.Window
	if a.AtLeast {
		key += "AtLeast"
	}
	return nativeTextFor(locale, key, map[string]string{"name": a.Name, "size": growthSize(a.Bytes), "days": strconv.Itoa(a.Days)})
}

// growthBody says where it grew, with the home folder shown as ~.
func growthBody(locale string, a GrowthAlert) string {
	folder := a.Folder
	if h := homeDir(); within(folder, h) {
		folder = filepath.Join("~", strings.TrimPrefix(folder, h))
	}
	return nativeTextFor(locale, "growth.notify.body", map[string]string{"folder": folder, "total": growthSize(a.Total)})
}

// nativeTexts are the texts of src/native-i18n.json, parsed once.
var nativeTexts = sync.OnceValue(func() map[string]map[string]string {
	var m map[string]map[string]string
	if err := json.Unmarshal(nativeMessages, &m); err != nil {
		panic(err) // embedded, validated by TestNativeTexts
	}
	return m
})

// nativeTextFor translates key for locale, where zh-* is Chinese and
// anything else English, and fills {name}-style placeholders from vars.
func nativeTextFor(locale, key string, vars map[string]string) string {
	lang := "en"
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		lang = "zh"
	}
	s := nativeTexts()[lang][key]
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

// nativeText translates key for the app's language.
func nativeText(key string, vars map[string]string) string {
	return nativeTextFor(mygo.App.Locale(), key, vars)
}

// growthSize writes a size like the page's human() in src/shared.ts:
// 1024-based units, with one decimal below 100.
func growthSize(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v, i := float64(n), -1
	for {
		v /= 1024
		i++
		if v < 1024 || i == len(units)-1 {
			break
		}
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
