package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
)

// Seams. Tests replace these; the app uses them as they are.
var (
	growthNow = time.Now
	// growthMeasure takes one record of this Mac, on background threads when
	// background is set.
	growthMeasure = func(ctx context.Context, prev *growthSnapshot, background bool) growthSnapshot {
		fda := fullDiskAccess()
		targets, unlisted := growthTargets(growthRoots(homeDir(), string(filepath.Separator)), fda, filepath.Dir(configPath()))
		return takeGrowthSnapshot(ctx, targets, unlisted, prev, fda, background, growthNow())
	}
	growthNotify = postGrowthNotification
	// growthChanged tells the pages and the menu that something moved. Its
	// default is set in init.
	growthChanged func(GrowthStatus)
	// growthOwnerOf tells which owner an entry counts under, and since when it
	// has told so: rows built before that time are built again.
	growthOwnerOf = func() (func(growthEntry) growthOwner, time.Time) {
		a, at := currentGrowthAttributor()
		return a.owner, at
	}
	growthPower = growthBattery
)

// init sets the default of growthChanged. It cannot be the initializer of its
// declaration: it reaches refreshGrowthTray, and the menu bar's menu reaches
// growth.run, setNext and changed, which read growthChanged, so Go would
// report an initialization cycle.
func init() {
	growthChanged = func(s GrowthStatus) {
		GrowthUpdated.Broadcast(s)
		go refreshGrowthTray()
		go menuBarGrowthChanged()
	}
}

// growthMonitor measures in the background while monitoring is on, and on
// request.
type growthMonitor struct {
	mu          sync.Mutex
	started     bool
	running     bool
	manual      bool // the running measurement was asked for
	cancel      context.CancelFunc
	launched    time.Time
	nextAt      time.Time
	lastAttempt time.Time      // when a background run last started, whether or not it left a record
	notifs      string         // "unknown", "allowed", "denied" or "unsupported"
	posting     sync.WaitGroup // the goroutines of post, until they end
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
//   - five minutes after launch, when the last record was already 20 hours
//     old at launch;
//   - otherwise 24 hours after the last record.
//
// The 20 hours are judged at launch, not now: a record taken during this run
// is due again 24 hours later, not 20. A last record dated after now, which a
// wrong clock leaves behind, counts as no record at all.
func growthNextRun(last, launched, now time.Time, justEnabled bool) time.Time {
	missing := last.IsZero() || last.After(now)
	if justEnabled && (missing || now.Sub(last) >= 20*time.Hour) {
		return now
	}
	if missing || launched.Sub(last) >= 20*time.Hour {
		if t := launched.Add(5 * time.Minute); t.After(now) {
			return t
		}
		return now
	}
	return last.Add(24 * time.Hour)
}

// growthDeferral tells whether a due measurement waits, and about how long
// (spec §四); tick asks again at its next 15-minute check either way:
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
// the schedule. It also checks once just after the five minutes that follow
// launch, so that the launch rule fires then and not at the next 15 minutes.
func (m *growthMonitor) start() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()
	mygo.Power.OnResume(func() { go m.tick(false) })
	time.AfterFunc(5*time.Minute+time.Second, func() { m.tick(false) })
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
	due := m.nextRun(now, justEnabled)
	if now.Before(due) {
		m.setNext(due)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	onBattery, percent := growthPower(ctx)
	cancel()
	if growthDeferral(onBattery, percent, userScans.Load()) > 0 {
		// The next tick asks again. A plan that is already due keeps its time,
		// so ticks that keep waiting move nothing and tell nobody.
		m.mu.Lock()
		waiting := !m.nextAt.IsZero() && !m.nextAt.After(now)
		m.mu.Unlock()
		if !waiting {
			m.setNext(due)
		}
		return
	}
	m.run(false)
}

// nextRun is when the next automatic measurement is due: growthNextRun, but
// not within an hour of the last attempt. A run that leaves no record, because
// its save failed or a user scan stopped it, would otherwise be tried again at
// every tick. Turning monitoring on does not wait for the hour. An attempt
// dated after now, which a clock set back leaves behind, holds nothing.
func (m *growthMonitor) nextRun(now time.Time, justEnabled bool) time.Time {
	due := growthNextRun(lastGrowthAt(growthDir(), now), m.launched, now, justEnabled)
	if justEnabled {
		return due
	}
	m.mu.Lock()
	attempt := m.lastAttempt
	m.mu.Unlock()
	if !attempt.After(now) {
		if again := attempt.Add(time.Hour); again.After(due) {
			return again
		}
	}
	return due
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
// started. A background run stops when the user starts a scan, and does not
// start while one runs; a manual one does neither.
func (m *growthMonitor) run(manual bool) bool {
	m.mu.Lock()
	// A user scan can begin after tick looked and before this point, when there
	// is no run yet to stop. userScan counts itself before it looks for a run,
	// so either it finds this one, or this check finds it.
	if m.running || !manual && userScans.Load() > 0 {
		m.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.running, m.manual, m.cancel = true, manual, cancel
	if !manual {
		m.lastAttempt = growthNow()
	}
	m.mu.Unlock()
	growthChanged(m.status())
	go func() {
		m.measureOnce(ctx)
		cancel()
		m.mu.Lock()
		m.running, m.manual, m.cancel = false, false, nil
		m.mu.Unlock()
		if loadGrowthConfig(growthDir()).Enabled {
			m.setNext(m.nextRun(growthNow(), false))
		}
		growthChanged(m.status())
	}()
	return true
}

// measureOnce takes one record and keeps the alerts it raises. With
// monitoring on, it also posts them within the limits of spec §五, after
// saving what it picked, on a goroutine of their own (post): a post can wait
// for the user to answer the permission prompt, and neither the run nor the
// log of what was picked waits for it. It saves nothing when ctx ends first.
// A run the user asked for measures at normal priority: the user waits for it,
// and big folders finish within their limit.
func (m *growthMonitor) measureOnce(ctx context.Context) {
	dir := growthDir()
	history := growthHistory(dir, growthNow())
	var prev *growthSnapshot
	if len(history) > 0 {
		prev = &history[len(history)-1]
	}
	m.mu.Lock()
	background := !m.manual
	m.mu.Unlock()
	snap := growthMeasure(ctx, prev, background)
	if ctx.Err() != nil {
		return
	}
	now := growthNow()
	if saveGrowthSnapshot(dir, snap, now) != nil {
		return
	}
	cfg := loadGrowthConfig(dir)
	owner, _ := growthOwnerOf()
	alerts := evaluateGrowth(history, snap, cfg, owner)
	n := loadGrowthNotified(dir)
	n.remember(alerts, now)
	var picks []GrowthAlert
	if cfg.Enabled {
		picks = pickGrowthNotifications(alerts, &n, now)
	}
	_ = saveGrowthNotified(dir, n)
	m.post(picks)
}

// post shows notes through growthNotify, in order, on a goroutine of its own,
// so that nobody waits for them: the first one can wait until the user
// answers macOS's permission prompt. What the posts learn about permission is
// kept, and the page is told when that changed. posting counts the goroutine
// until it ends.
func (m *growthMonitor) post(notes []GrowthAlert) {
	if len(notes) == 0 {
		return
	}
	notify := growthNotify
	m.posting.Add(1)
	go func() {
		defer m.posting.Done()
		changed := false
		for _, a := range notes {
			changed = m.noteNotify(notify(a)) || changed
		}
		if changed {
			m.changed()
		}
	}()
}

// noteNotify keeps whether notifications can be shown, for the page, and
// reports whether that changed.
func (m *growthMonitor) noteNotify(err error) bool {
	state := "allowed"
	switch {
	case errors.Is(err, mygo.ErrNotificationsDenied):
		state = "denied"
	case errors.Is(err, errGrowthNoNotifications):
		state = "unsupported"
	case err != nil:
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := m.notifs != state
	m.notifs = state
	return changed
}

// cancelBackground stops a background measurement under way.
func (m *growthMonitor) cancelBackground() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running && !m.manual && m.cancel != nil {
		m.cancel()
	}
}

// status is what the page shows about measuring. It reads the disk before it
// takes the lock, which userScan waits for.
func (m *growthMonitor) status() GrowthStatus {
	s := GrowthStatus{FDA: fullDiskAccess()}
	if last := lastGrowthAt(growthDir(), growthNow()); !last.IsZero() {
		s.LastAt = last.Unix()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.Running, s.Notifications = m.running, m.notifs
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

// growthOnNotice is posted through growthNotify when monitoring is turned on:
// an alert of no owner, which says that monitoring is on. macOS asks for
// permission at an app's first notification, and this way it asks while the
// user is at the page, not during a run nobody watches. Clicking it opens the
// growth page.
var growthOnNotice = GrowthAlert{}

// growthNoticeText is the title and body of the notification for a, an alert
// or growthOnNotice.
func growthNoticeText(locale string, a GrowthAlert) (title, body string) {
	if a == growthOnNotice {
		return nativeTextFor(locale, "growth.notify.enabled", nil), nativeTextFor(locale, "growth.notify.enabledBody", nil)
	}
	return growthTitle(locale, a), growthBody(locale, a)
}

// postGrowthNotification shows one alert, or growthOnNotice. Clicking it opens
// the growth page at the app, even after a relaunch (App.OnNotificationClick
// in growth_app.go).
func postGrowthNotification(a GrowthAlert) error {
	if !mygo.NotificationsSupported() {
		return errGrowthNoNotifications
	}
	title, body := growthNoticeText(mygo.App.Locale(), a)
	return mygo.NewNotification(mygo.NotificationOptions{ID: "growth:" + a.Key, Title: title, Body: body, Group: "growth"}).Show()
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
	return nativeTextFor(locale, "growth.notify.body", map[string]string{"folder": shortHome(a.Folder), "total": growthSize(a.Total)})
}

// nativeTexts are the texts of src/native-i18n.json, parsed once.
var nativeTexts = sync.OnceValue(func() map[string]map[string]string {
	var m map[string]map[string]string
	if err := json.Unmarshal(nativeMessages, &m); err != nil {
		panic(err) // embedded, validated by TestNativeTexts
	}
	return m
})

// nativePlural matches the page's plural placeholder, {n|one/many}: the value
// of n picks the form, as package i18n does for the page's strings.
var nativePlural = regexp.MustCompile(`\{([^{}|]+)\|([^{}/]*)/([^{}]*)\}`)

// fillNativePlural fills the plural placeholders of text from vars, leaving
// one alone whose n vars doesn't hold.
func fillNativePlural(text string, vars map[string]string) string {
	return nativePlural.ReplaceAllStringFunc(text, func(match string) string {
		parts := nativePlural.FindStringSubmatch(match)
		n, err := strconv.Atoi(vars[parts[1]])
		if err != nil {
			return match
		}
		if n == 1 {
			return parts[2]
		}
		return parts[3]
	})
}

// nativeTextFor translates key for locale, where zh-* is Chinese and
// anything else English, and fills {name}-style placeholders from vars. It
// fills them in one pass, so a value that looks like a placeholder stays as
// it is, whatever order the map is read in.
func nativeTextFor(locale, key string, vars map[string]string) string {
	lang := "en"
	if strings.HasPrefix(strings.ToLower(locale), "zh") {
		lang = "zh"
	}
	pairs := make([]string, 0, 2*len(vars))
	for k, v := range vars {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(fillNativePlural(nativeTexts()[lang][key], vars))
}

// nativeText translates key for the app's language.
func nativeText(key string, vars map[string]string) string {
	return nativeTextFor(i18n.Locale(), key, vars)
}

// growthSize writes a size like the page's human() in src/shared.ts:
// 1024-based units, with one decimal below 100. Halves round up, as toFixed
// does there: 1.25 KB is 1.3 KB and 100.5 KB is 101 KB.
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
		return fmt.Sprintf("%.0f %s", math.Round(v), units[i])
	}
	return fmt.Sprintf("%.1f %s", math.Round(v*10)/10, units[i])
}
