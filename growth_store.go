package main

import (
	"cmp"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// growthDir is where the growth monitor keeps its files, next to
// config.json in Cleanzo's folder of Application Support (spec §四).
func growthDir() string { return filepath.Join(filepath.Dir(configPath()), "growth") }

// growthKeep is how long records are kept (spec §四).
const growthKeep = 90 * 24 * time.Hour

// growthRecordAt is the one definition of a record: a regular file named
// <digits>.json whose number is a positive unix time. It returns that time.
// Any other file in records/, such as a hand-copied backup, is neither loaded
// nor pruned.
func growthRecordAt(e fs.DirEntry) (int64, bool) {
	digits, ok := strings.CutSuffix(e.Name(), ".json")
	if !ok || !e.Type().IsRegular() {
		return 0, false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	at, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || at <= 0 {
		return 0, false
	}
	return at, true
}

// writeGrowthFile replaces path only after a complete copy is written. The copy
// goes to a temporary file of its own, so two writers never share one, and
// that file is removed if anything fails. The file keeps mode 0o644.
func writeGrowthFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = f.Chmod(0o644) // CreateTemp makes the file 0o600
	if err == nil {
		_, err = f.Write(data)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp) // the function's own temporary file
	}
	return err
}

// saveGrowthSnapshot writes one record as records/<At>.json. It then
// removes the records older than growthKeep, the monitor's only deletion.
func saveGrowthSnapshot(dir string, s growthSnapshot, now time.Time) error {
	records := filepath.Join(dir, "records")
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := writeGrowthFile(filepath.Join(records, strconv.FormatInt(s.At, 10)+".json"), data); err != nil {
		return err
	}
	entries, _ := os.ReadDir(records)
	for _, e := range entries {
		if at, ok := growthRecordAt(e); ok && now.Sub(time.Unix(at, 0)) > growthKeep {
			_ = os.Remove(filepath.Join(records, e.Name()))
		}
	}
	return nil
}

// loadGrowthSnapshots reads every record, oldest first, skipping files that
// are not records (growthRecordAt) and files that do not parse.
func loadGrowthSnapshots(dir string) []growthSnapshot {
	records := filepath.Join(dir, "records")
	entries, _ := os.ReadDir(records)
	var out []growthSnapshot
	for _, e := range entries {
		if _, ok := growthRecordAt(e); !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(records, e.Name()))
		if err != nil {
			continue
		}
		var s growthSnapshot
		if json.Unmarshal(data, &s) == nil && s.At > 0 {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b growthSnapshot) int { return cmp.Compare(a.At, b.At) })
	return out
}

// lastGrowthAt is when the newest record was taken, read from file names
// only (growthRecordAt); zero without records. A record dated after now, which
// a clock set back leaves behind, does not count: it would stay the newest
// for as long, and every tick would find the next measurement due.
func lastGrowthAt(dir string, now time.Time) time.Time {
	entries, _ := os.ReadDir(filepath.Join(dir, "records"))
	var last int64
	for _, e := range entries {
		if at, ok := growthRecordAt(e); ok && at <= now.Unix() {
			last = max(last, at)
		}
	}
	if last == 0 {
		return time.Time{}
	}
	return time.Unix(last, 0)
}

// GrowthConfig is what the user chose for the growth monitor (spec §五, §六).
type GrowthConfig struct {
	Enabled     bool     `json:"enabled"`     // measure in the background, show the menu bar icon, stay open without windows
	OpenAtLogin bool     `json:"openAtLogin"` // only together with Enabled
	DailyGB     float64  `json:"dailyGB"`     // a day's growth that alerts
	WeeklyGB    float64  `json:"weeklyGB"`    // seven days' growth that alerts
	Ignored     []string `json:"ignored"`     // owner keys that never alert
	Asked       bool     `json:"asked"`       // the page has offered monitoring once
}

// The thresholds Settings offers, in GB.
var (
	growthDailyChoices  = []float64{1, 2, 5, 10}
	growthWeeklyChoices = []float64{5, 10, 20, 50}
)

// defaultGrowthConfig is the config before the user changes anything:
// monitoring off, 2 GB a day, 10 GB in seven days.
func defaultGrowthConfig() GrowthConfig {
	return GrowthConfig{DailyGB: 2, WeeklyGB: 10, Ignored: []string{}}
}

// loadGrowthConfig reads config.json in dir. A missing or broken file gives
// the defaults, and so does a threshold outside the choices.
func loadGrowthConfig(dir string) GrowthConfig {
	c := defaultGrowthConfig()
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil || json.Unmarshal(data, &c) != nil {
		return defaultGrowthConfig()
	}
	def := defaultGrowthConfig()
	if !slices.Contains(growthDailyChoices, c.DailyGB) {
		c.DailyGB = def.DailyGB
	}
	if !slices.Contains(growthWeeklyChoices, c.WeeklyGB) {
		c.WeeklyGB = def.WeeklyGB
	}
	if c.Ignored == nil {
		c.Ignored = []string{}
	}
	c.OpenAtLogin = c.Enabled && c.OpenAtLogin
	return c
}

// validateGrowthConfig returns a normalized copy, or a translatable error
// for thresholds outside the choices. Opening at login needs monitoring on.
func validateGrowthConfig(c GrowthConfig) (GrowthConfig, error) {
	if !slices.Contains(growthDailyChoices, c.DailyGB) || !slices.Contains(growthWeeklyChoices, c.WeeklyGB) {
		return GrowthConfig{}, codeError{Code: "growth-threshold"}
	}
	out := c
	out.OpenAtLogin = c.Enabled && c.OpenAtLogin
	out.Ignored = []string{}
	for _, k := range c.Ignored {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "" && !slices.Contains(out.Ignored, k) {
			out.Ignored = append(out.Ignored, k)
		}
	}
	return out, nil
}

// saveGrowthConfig writes config.json in dir.
func saveGrowthConfig(dir string, c GrowthConfig) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeGrowthFile(filepath.Join(dir, "config.json"), data)
}

// growthNotified remembers what was announced, to keep within the limits
// of spec §五. It also keeps the alerts of the last seven days for the page
// and the status page.
type growthNotified struct {
	Day    map[string]string `json:"day"`    // owner key → local date of its last notification
	Week   map[string]int64  `json:"week"`   // owner key → unix time of its last seven-day notification
	Count  map[string]int    `json:"count"`  // local date → notifications posted that day
	Recent []GrowthAlert     `json:"recent"` // alerts raised in the last seven days, one per owner, newest first
}

// loadGrowthNotified reads notified.json in dir, with every map ready.
func loadGrowthNotified(dir string) growthNotified {
	var n growthNotified
	if data, err := os.ReadFile(filepath.Join(dir, "notified.json")); err == nil {
		_ = json.Unmarshal(data, &n)
	}
	if n.Day == nil {
		n.Day = map[string]string{}
	}
	if n.Week == nil {
		n.Week = map[string]int64{}
	}
	if n.Count == nil {
		n.Count = map[string]int{}
	}
	if n.Recent == nil {
		n.Recent = []GrowthAlert{}
	}
	return n
}

// saveGrowthNotified writes notified.json in dir.
func saveGrowthNotified(dir string, n growthNotified) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return writeGrowthFile(filepath.Join(dir, "notified.json"), data)
}
