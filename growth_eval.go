package main

import (
	"cmp"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// GrowthDelta is growth over a window, a lower bound when AtLeast.
type GrowthDelta struct {
	Bytes   int64 `json:"bytes"`
	AtLeast bool  `json:"atLeast"`
	Days    int   `json:"days"` // the window's actual length, rounded
}

// GrowthAlert is an owner whose growth passed a threshold (spec §五).
type GrowthAlert struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Window  string `json:"window"` // "day" or "week"
	Days    int    `json:"days"`
	Bytes   int64  `json:"bytes"`
	AtLeast bool   `json:"atLeast"`
	Folder  string `json:"folder"` // where it grew most, down to a recorded child
	Total   int64  `json:"total"`  // what the owner takes now
	At      int64  `json:"at"`     // unix seconds of the record that raised it
}

// GrowthApp is one row of the growth page: an owner, what it takes now and
// how it grew.
type GrowthApp struct {
	Key     string         `json:"key"`
	Name    string         `json:"name"`
	Kind    string         `json:"kind"`    // "app", "macos", "dot" or "unknown"
	Orphan  bool           `json:"orphan"`  // probably a leftover: the leftovers tab can clean it
	AppPath string         `json:"appPath"` // the installed app, for Uninstall
	Size    int64          `json:"size"`
	Partial bool           `json:"partial"` // some folder was not measured to the end
	Ignored bool           `json:"ignored"`
	Day     *GrowthDelta   `json:"day"` // nil: no record to compare with
	Week    *GrowthDelta   `json:"week"`
	Month   *GrowthDelta   `json:"month"`
	Spark   []int64        `json:"spark"` // size on each of the last 30 days, oldest first; -1 without a record
	Folders []GrowthFolder `json:"folders"`
}

// GrowthFolder is one measured entry of a row.
type GrowthFolder struct {
	Path     string       `json:"path"`
	Size     int64        `json:"size"`
	Complete bool         `json:"complete"`
	Denied   bool         `json:"denied"`
	Outside  bool         `json:"outside"` // outside the home folder: Cleanzo never removes it
	Week     *GrowthDelta `json:"week"`
}

// growthBase finds the record cur is compared with (spec §五), or nil:
//   - "day": the latest record 20 to 28 hours older;
//   - "week": the earliest record at most 7 days older and at least 3 days;
//   - "month": the earliest record at most 30 days older and at least 8 days.
//
// Records taken with a different Full Disk Access never count.
func growthBase(history []growthSnapshot, cur growthSnapshot, window string) *growthSnapshot {
	var lo, hi time.Duration
	latest := false
	switch window {
	case "day":
		lo, hi, latest = 20*time.Hour, 28*time.Hour, true
	case "week":
		lo, hi = 3*24*time.Hour, 7*24*time.Hour
	case "month":
		lo, hi = 8*24*time.Hour, 30*24*time.Hour
	default:
		return nil
	}
	var found *growthSnapshot
	for i := range history {
		h := &history[i]
		age := time.Duration(cur.At-h.At) * time.Second
		if h.At >= cur.At || age < lo || age > hi || h.FDA != cur.FDA {
			continue
		}
		if found == nil || latest && h.At > found.At || !latest && h.At < found.At {
			found = h
		}
	}
	return found
}

// growthDays is a window's length in whole days.
func growthDays(base, cur growthSnapshot) int {
	return int(math.Round(float64(cur.At-base.At) / 86400))
}

// growthChange is how much one owner changed between two records.
type growthChange struct {
	bytes    int64
	atLeast  bool
	compared bool   // at least one of its entries compared
	folder   string // where it grew most
	grew     int64  // and by how much
}

// growthEntryChange compares one entry, reporting false when it cannot be
// compared:
//   - exact when both records measured it to the end;
//   - a lower bound when only the base did;
//   - nothing when the base did not finish it, or its readability changed.
//
// An entry missing on one side counts as 0 there, but only when that side's
// run measured everything (not Cut).
func growthEntryChange(a growthEntry, inBase bool, base growthSnapshot, b growthEntry) (delta int64, atLeast, ok bool) {
	if !inBase {
		if base.Cut {
			return 0, false, false
		}
		return b.Size, !b.Complete, true
	}
	if !a.Complete || a.Denied != b.Denied {
		return 0, false, false
	}
	return b.Size - a.Size, !b.Complete, true
}

// growthTopFolder names where an entry grew most: the child that grew most
// when a recorded child grew (against the base's, or 0), else the entry itself.
func growthTopFolder(a, b growthEntry) string {
	best, bestGrew := "", int64(math.MinInt64)
	for name, size := range b.Children {
		if g := size - a.Children[name]; g > bestGrew || g == bestGrew && name < best {
			best, bestGrew = name, g
		}
	}
	if best == "" || bestGrew <= 0 {
		return b.Path
	}
	return filepath.Join(b.Path, best)
}

// growthChanges sums each owner's entry changes between base and cur.
func growthChanges(base, cur growthSnapshot, owner func(growthEntry) growthOwner) map[string]growthChange {
	before := make(map[string]growthEntry, len(base.Entries))
	for _, e := range base.Entries {
		before[e.Path] = e
	}
	out := map[string]growthChange{}
	add := func(key string, delta int64, atLeast bool, folder string) {
		c := out[key]
		c.bytes += delta
		c.atLeast = c.atLeast || atLeast
		if !c.compared || delta > c.grew {
			c.folder, c.grew = folder, delta
		}
		c.compared = true
		out[key] = c
	}
	seen := make(map[string]bool, len(cur.Entries))
	for _, b := range cur.Entries {
		seen[b.Path] = true
		a, inBase := before[b.Path]
		if delta, atLeast, ok := growthEntryChange(a, inBase, base, b); ok {
			add(owner(b).key, delta, atLeast, growthTopFolder(a, b))
		}
	}
	if !cur.Cut {
		for _, a := range base.Entries {
			if !seen[a.Path] && a.Complete {
				add(owner(a).key, -a.Size, false, a.Path) // gone since the base
			}
		}
	}
	return out
}

// growthTotal is an owner's size in one record.
type growthTotal struct {
	owner growthOwner
	size  int64
}

// growthTotals sums each owner's entries in s.
func growthTotals(s growthSnapshot, owner func(growthEntry) growthOwner) map[string]growthTotal {
	out := map[string]growthTotal{}
	for _, e := range s.Entries {
		o := owner(e)
		t := out[o.key]
		t.owner, t.size = o, t.size+e.Size
		out[o.key] = t
	}
	return out
}

// memoGrowthOwner remembers owner per path, since comparing records asks
// for the same paths many times.
func memoGrowthOwner(owner func(growthEntry) growthOwner) func(growthEntry) growthOwner {
	memo := map[string]growthOwner{}
	return func(e growthEntry) growthOwner {
		k := string(e.Kind) + "\x00" + e.Path
		if o, ok := memo[k]; ok {
			return o
		}
		o := owner(e)
		memo[k] = o
		return o
	}
}

// evaluateGrowth lists the owners whose growth in cur passes the thresholds
// (spec §五):
//   - a day, against the latest record 20 to 28 hours older;
//   - seven days, against the earliest record within a week that is at least
//     three days old.
//
// The first record is a baseline only. Ignored owners and shrinking never
// alert. The alerts come largest first.
func evaluateGrowth(history []growthSnapshot, cur growthSnapshot, cfg GrowthConfig, owner func(growthEntry) growthOwner) []GrowthAlert {
	owner = memoGrowthOwner(owner)
	totals := growthTotals(cur, owner)
	out := []GrowthAlert{}
	check := func(window string, thresholdGB float64) {
		base := growthBase(history, cur, window)
		if base == nil {
			return
		}
		for key, c := range growthChanges(*base, cur, owner) {
			if slices.Contains(cfg.Ignored, key) || float64(c.bytes) < thresholdGB*float64(1<<30) {
				continue
			}
			out = append(out, GrowthAlert{
				Key: key, Name: totals[key].owner.name, Window: window, Days: growthDays(*base, cur),
				Bytes: c.bytes, AtLeast: c.atLeast, Folder: c.folder, Total: totals[key].size, At: cur.At,
			})
		}
	}
	check("day", cfg.DailyGB)
	check("week", cfg.WeeklyGB)
	slices.SortFunc(out, func(a, b GrowthAlert) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), strings.Compare(a.Key, b.Key), strings.Compare(a.Window, b.Window))
	})
	return out
}

// buildGrowthApps turns the records into the page's rows, largest first
// (spec §六). The last record is "now"; the growth columns compare it with
// the records growthBase picks. Spark has the owner's size in the last
// record of each of the last 30 days (in loc), or -1 for a day without one.
func buildGrowthApps(history []growthSnapshot, cfg GrowthConfig, owner func(growthEntry) growthOwner, home string, loc *time.Location) []GrowthApp {
	out := []GrowthApp{}
	if len(history) == 0 {
		return out
	}
	owner = memoGrowthOwner(owner)
	cur := history[len(history)-1]
	earlier := history[:len(history)-1]
	rows := map[string]*GrowthApp{}
	var order []string
	for _, e := range cur.Entries {
		o := owner(e)
		r := rows[o.key]
		if r == nil {
			r = &GrowthApp{Key: o.key, Name: o.name, Kind: o.kind, Orphan: o.orphan, AppPath: o.appPath,
				Ignored: slices.Contains(cfg.Ignored, o.key), Spark: []int64{}, Folders: []GrowthFolder{}}
			rows[o.key] = r
			order = append(order, o.key)
		}
		r.Size += e.Size
		r.Partial = r.Partial || !e.Complete
		r.Folders = append(r.Folders, GrowthFolder{Path: e.Path, Size: e.Size, Complete: e.Complete, Denied: e.Denied, Outside: !within(e.Path, home)})
	}
	for _, w := range []string{"day", "week", "month"} {
		base := growthBase(earlier, cur, w)
		if base == nil {
			continue
		}
		days := growthDays(*base, cur)
		for key, c := range growthChanges(*base, cur, owner) {
			r := rows[key]
			if r == nil || !c.compared {
				continue
			}
			d := &GrowthDelta{Bytes: c.bytes, AtLeast: c.atLeast, Days: days}
			switch w {
			case "day":
				r.Day = d
			case "week":
				r.Week = d
			case "month":
				r.Month = d
			}
		}
		if w == "week" {
			before := map[string]growthEntry{}
			for _, e := range base.Entries {
				before[e.Path] = e
			}
			for _, r := range rows {
				for i, f := range r.Folders {
					a, inBase := before[f.Path]
					b := growthEntry{Path: f.Path, Size: f.Size, Complete: f.Complete, Denied: f.Denied}
					if delta, atLeast, ok := growthEntryChange(a, inBase, *base, b); ok {
						r.Folders[i].Week = &GrowthDelta{Bytes: delta, AtLeast: atLeast, Days: days}
					}
				}
			}
		}
	}
	// spark: the last record of each local day
	byDay := map[string]map[string]int64{}
	for _, s := range history {
		if cur.At-s.At > 31*86400 {
			continue
		}
		sizes := map[string]int64{}
		for _, e := range s.Entries {
			sizes[owner(e).key] += e.Size
		}
		byDay[time.Unix(s.At, 0).In(loc).Format(time.DateOnly)] = sizes // history is oldest first: later records win
	}
	today := time.Unix(cur.At, 0).In(loc)
	for _, r := range rows {
		r.Spark = make([]int64, 30)
		for i := range r.Spark {
			r.Spark[i] = -1
			if sizes, ok := byDay[today.AddDate(0, 0, i-29).Format(time.DateOnly)]; ok {
				if v, ok := sizes[r.Key]; ok {
					r.Spark[i] = v
				}
			}
		}
		slices.SortFunc(r.Folders, func(a, b GrowthFolder) int {
			return cmp.Or(cmp.Compare(b.Size, a.Size), strings.Compare(a.Path, b.Path))
		})
	}
	for _, key := range order {
		out = append(out, *rows[key])
	}
	slices.SortFunc(out, func(a, b GrowthApp) int { return cmp.Or(cmp.Compare(b.Size, a.Size), strings.Compare(a.Name, b.Name)) })
	return out
}

// pickGrowthNotifications chooses the alerts to post now (spec §五):
//   - one a day per owner, where the day's alert beats the seven-day one;
//   - a seven-day alert at most once a week per owner;
//   - three a day in all, largest first.
//
// It records the choice in n.
func pickGrowthNotifications(alerts []GrowthAlert, n *growthNotified, now time.Time) []GrowthAlert {
	today := now.Format(time.DateOnly)
	byKey := map[string]GrowthAlert{}
	for _, a := range alerts {
		if a.Window == "week" && now.Unix()-n.Week[a.Key] < 7*86400 {
			continue // its seven-day alert went out this week
		}
		if prev, ok := byKey[a.Key]; ok && prev.Window == "day" {
			continue // the day's alert says more
		}
		byKey[a.Key] = a
	}
	var picks []GrowthAlert
	for key, a := range byKey {
		if n.Day[key] != today {
			picks = append(picks, a)
		}
	}
	slices.SortFunc(picks, func(a, b GrowthAlert) int {
		return cmp.Or(cmp.Compare(b.Bytes, a.Bytes), strings.Compare(a.Key, b.Key))
	})
	picks = picks[:min(len(picks), max(0, 3-n.Count[today]))]
	for _, a := range picks {
		n.Day[a.Key] = today
		if a.Window == "week" {
			n.Week[a.Key] = now.Unix()
		}
		n.Count[today]++
	}
	for d := range n.Count {
		if t, err := time.Parse(time.DateOnly, d); err != nil || now.Sub(t) > 8*24*time.Hour {
			delete(n.Count, d)
		}
	}
	for k, t := range n.Week {
		if now.Unix()-t > 30*86400 {
			delete(n.Week, k)
		}
	}
	return picks
}

// remember keeps the newest alert per owner for seven days, for the page
// and the status page.
func (n *growthNotified) remember(alerts []GrowthAlert, now time.Time) {
	keep := []GrowthAlert{}
	have := map[string]bool{}
	for _, a := range alerts { // newest first: the record that just ran
		if !have[a.Key] {
			keep, have[a.Key] = append(keep, a), true
		}
	}
	for _, a := range n.Recent {
		if !have[a.Key] && now.Unix()-a.At <= 7*86400 {
			keep, have[a.Key] = append(keep, a), true
		}
	}
	n.Recent = keep
}

// recent returns the remembered alerts that are still within seven days.
func (n growthNotified) recent(now time.Time) []GrowthAlert {
	out := []GrowthAlert{}
	for _, a := range n.Recent {
		if now.Unix()-a.At <= 7*86400 {
			out = append(out, a)
		}
	}
	return out
}
