package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// JunkItem is one rule, app cache or custom folder on the junk page.
type JunkItem struct {
	ID       string    `json:"id"`
	Group    string    `json:"group"`
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Files    int64     `json:"files"`
	Default  bool      `json:"default"`
	Trash    bool      `json:"trash"`
	Running  bool      `json:"running"`
	InUse    int       `json:"inUse"`
	Denied   bool      `json:"denied"`
	Estimate bool      `json:"estimate"`
	Paths    []string  `json:"paths"`
	Top      []TopItem `json:"top"`
	Subs     []SubItem `json:"subs"`
	Skipped  string    `json:"skipped,omitempty"`
}

// SubItem is a separately selectable part of an item.
type SubItem struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Detail  string `json:"detail"`
	Size    int64  `json:"size"`
	Default bool   `json:"default"`
}

// ScanSummary closes a junk scan.
type ScanSummary struct {
	FullDiskAccess bool  `json:"fullDiskAccess"`
	Containers     int   `json:"containers"`
	Checked        int   `json:"checked"`
	Ignored        int   `json:"ignored"`
	Leftovers      int   `json:"leftovers"`
	LeftoversSize  int64 `json:"leftoversSize"`
}

// junkEnv holds the machine state used by a junk pass.
type junkEnv struct {
	home, tmp    string
	apps         *appIndex
	guard        *usageGuard
	fda          bool
	settings     Settings
	findTool     func(string) string
	refreshGuard func() *usageGuard
	lstat        func(string) (fs.FileInfo, error)
}

// currentJunkEnv probes the settings, apps, usage and permissions for a pass.
func currentJunkEnv() junkEnv {
	return junkEnv{home: homeDir(), tmp: filepath.Clean(os.TempDir()), apps: buildAppIndex(appDirs()), guard: probeUsage(), fda: fullDiskAccess(), settings: loadSettings(), findTool: func(string) string { return "" }}
}

// junkCand is an entry to measure with its minimum age.
type junkCand struct {
	path   string
	minAge time.Duration
}

// itemSpec pairs a row with its current targets and candidates.
type itemSpec struct {
	item   JunkItem
	rule   *Rule
	roots  []string
	cands  []junkCand
	owners []string
}

// claimSet prevents overlapping rules from removing the same path.
type claimSet []string

// claimed reports whether a path is already owned by another item.
func (c claimSet) claimed(p string) bool {
	key := canonical(p)
	return slices.ContainsFunc(c, func(root string) bool { return within(key, root) })
}

// inside reports whether a path contains another item's target.
func (c claimSet) inside(p string) bool {
	key := canonical(p)
	return slices.ContainsFunc(c, func(root string) bool { return root != key && within(root, key) })
}

// claim records a path in comparison form.
func (c *claimSet) claim(p string) { *c = append(*c, canonical(p)) }

// protected reports hard protected paths and their descendants.
func (env junkEnv) protected(p string) bool {
	for path := canonical(p); within(path, canonical(env.home)); path = filepath.Dir(path) {
		for _, pattern := range protectedPatterns {
			if hit, _ := filepath.Match(strings.ToLower(filepath.Join(resolvePath(env.home), pattern)), path); hit {
				return true
			}
		}
		if path == canonical(env.home) {
			break
		}
	}
	return false
}

// resolve expands targets without traversing links or protected containers.
func (env junkEnv) resolve(pattern string) []string {
	return slices.DeleteFunc(env.globTargets(pattern), env.protected)
}

// globTargets expands one path component at a time behind the traversal guard.
func (env junkEnv) globTargets(pattern string) []string {
	root, relative := env.home, strings.TrimPrefix(strings.TrimPrefix(pattern, "~"), "/")
	if strings.HasPrefix(pattern, "$TMPDIR") {
		root, relative = env.tmp, strings.TrimPrefix(strings.TrimPrefix(pattern, "$TMPDIR"), "/")
	}
	paths := []string{root}
	for _, part := range strings.Split(relative, "/") {
		if part == "" {
			continue
		}
		var next []string
		for _, parent := range paths {
			if env.traversalBlocked(parent) {
				continue
			}
			if strings.ContainsAny(part, "*?[") {
				entries, _ := env.readDir(parent)
				for _, entry := range entries {
					if hit, _ := filepath.Match(part, entry.Name()); hit {
						child := filepath.Join(parent, entry.Name())
						if !env.traversalBlocked(child) {
							next = append(next, child)
						}
					}
				}
			} else {
				child := filepath.Join(parent, part)
				if !env.traversalBlocked(child) {
					if _, err := env.stat(child); err == nil || errors.Is(err, fs.ErrPermission) {
						next = append(next, child)
					}
				}
			}
		}
		paths = next
	}
	return paths
}

// displayable makes a target's home prefix readable in the UI.
func (env junkEnv) displayable(p string) string {
	home := resolvePath(env.home)
	if within(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// addRoot records a target once for display and selection checks.
func (s *itemSpec) addRoot(env junkEnv, p string) {
	key := canonical(p)
	if !slices.Contains(s.roots, key) {
		s.roots = append(s.roots, key)
		s.item.Paths = append(s.item.Paths, env.displayable(resolvePath(p)))
	}
}

// nameMatches accepts a target's optional name filters.
func nameMatches(name string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	return slices.ContainsFunc(patterns, func(pattern string) bool { hit, _ := filepath.Match(pattern, name); return hit })
}

// collect takes unclaimed entries, descending around nested claims.
func (env junkEnv) collect(p string, age time.Duration, claims claimSet, spec *itemSpec) {
	if env.traversalBlocked(p) || env.protected(p) || claims.claimed(p) || !safeToClean(p) {
		return
	}
	info, err := os.Lstat(p)
	if err != nil {
		spec.item.Denied = spec.item.Denied || errors.Is(err, fs.ErrPermission)
		return
	}
	if info.Mode()&(fs.ModeSocket|fs.ModeNamedPipe|fs.ModeDevice|fs.ModeCharDevice|fs.ModeIrregular) != 0 {
		return
	}
	if claims.inside(p) {
		if info.IsDir() {
			env.collectDir(p, nil, age, claims, spec)
		}
		return
	}
	spec.cands = append(spec.cands, junkCand{path: resolvePath(p), minAge: age})
}

// collectDir reads a target's entries with optional name filters.
func (env junkEnv) collectDir(dir string, names []string, age time.Duration, claims claimSet, spec *itemSpec) {
	if env.traversalBlocked(dir) {
		spec.item.Denied = true
		return
	}
	if (!safeToClean(dir) && !slices.Contains(protectedHomeDirs, strings.TrimPrefix(strings.ToLower(filepath.Clean(dir)), strings.ToLower(filepath.Clean(env.home))))) || env.protected(dir) {
		return
	}
	entries, err := env.readDir(dir)
	if err != nil {
		spec.item.Denied = spec.item.Denied || errors.Is(err, fs.ErrPermission)
		return
	}
	for _, e := range entries {
		if nameMatches(e.Name(), names) {
			env.collect(filepath.Join(dir, e.Name()), age, claims, spec)
		}
	}
}

// running reports whether any owner is running.
func (env junkEnv) running(owners []string) bool {
	return slices.ContainsFunc(owners, func(id string) bool { return env.guard.running[strings.ToLower(id)] })
}

// expandItems resolves the catalog, customs and per-app sources in claim order.
func expandItems(env junkEnv) ([]itemSpec, ScanSummary) {
	sum := ScanSummary{FullDiskAccess: env.fda}
	var specs []itemSpec
	var claims claimSet
	// Reserve protected descendants too, so an enclosing custom folder is split.
	for _, pattern := range protectedPatterns {
		paths := env.globTargets(pattern)
		for _, p := range paths {
			claims.claim(p)
		}
	}
	emit := func(spec itemSpec) {
		if len(spec.cands) == 0 && !spec.item.Denied {
			return
		}
		if slices.Contains(env.settings.Ignored, spec.item.ID) {
			sum.Ignored++
			return
		}
		if !slices.Contains(env.settings.DisabledGroups, spec.item.Group) {
			specs = append(specs, spec)
		}
	}
	for i := range junkRules {
		r := &junkRules[i]
		if r.Command != nil {
			continue
		}
		sum.Checked++
		spec := itemSpec{rule: r, item: JunkItem{ID: r.ID, Group: string(r.Group), Size: -1, Default: r.Default, Trash: r.Trash, Running: env.running(r.Owners)}}
		if !env.fda && slices.Contains([]string{"sys.trash", "review.ios-backups", "review.mail-downloads"}, r.ID) {
			spec.item.Denied = true
			emit(spec)
			continue
		}
		var reserve []string
		for _, target := range r.Targets {
			for _, root := range env.resolve(target.Pattern) {
				if claims.claimed(root) {
					continue
				}
				spec.addRoot(env, root)
				if target.Mode == matched {
					env.collect(root, r.MinAge, claims, &spec)
				} else {
					env.collectDir(root, target.Names, r.MinAge, claims, &spec)
				}
				// Filtered targets own matching entries, not unrelated files in the directory.
				if len(target.Names) == 0 {
					reserve = append(reserve, root)
				}
			}
		}
		for _, c := range spec.cands {
			claims.claim(c.path)
		}
		for _, root := range reserve {
			claims.claim(root)
		}
		for _, pattern := range r.Reserve {
			for _, root := range env.resolve(pattern) {
				claims.claim(root)
			}
		}
		emit(spec)
	}
	for _, folder := range env.settings.Custom {
		if !folder.Enabled || env.traversalBlocked(folder.Path) || !safeToClean(folder.Path) || env.protected(folder.Path) {
			continue
		}
		spec := itemSpec{item: JunkItem{ID: "custom:" + folder.Path, Group: string(groupCustom), Name: folder.Name, Size: -1, Default: true, Trash: true}}
		spec.addRoot(env, folder.Path)
		env.collectDir(folder.Path, nil, 0, claims, &spec)
		claims.claim(folder.Path)
		emit(spec)
	}
	containers, _ := env.readDir(filepath.Join(env.home, "Library/Containers"))
	if !env.fda {
		for _, e := range containers {
			if !strings.HasPrefix(e.Name(), "com.apple.") {
				sum.Containers++
			}
		}
	}
	if slices.Contains(env.settings.DisabledGroups, "apps") {
		return specs, sum
	}
	apps := map[string]*itemSpec{}
	var appOrder []string
	addApp := func(name, root string, whole bool) {
		if env.traversalBlocked(root) {
			return
		}
		var owner appRef
		if env.apps != nil {
			owner, _ = env.apps.lookup(name)
		}
		id, display := "app:dir:"+strings.ToLower(name), name
		if owner.BundleID != "" {
			id, display = "app:"+strings.ToLower(owner.BundleID), owner.Name
		}
		spec := itemSpec{owners: []string{owner.BundleID}, item: JunkItem{ID: id, Group: string(groupApps), Name: display, Size: -1, Default: !unselectedApps[strings.ToLower(owner.BundleID)], Running: env.running([]string{owner.BundleID})}}
		if whole {
			env.collect(root, 0, claims, &spec)
		} else {
			env.collectDir(root, nil, 0, claims, &spec)
		}
		if len(spec.cands) == 0 && !spec.item.Denied {
			return
		}
		spec.addRoot(env, root)
		for _, c := range spec.cands {
			claims.claim(c.path)
		}
		if prev := apps[id]; prev != nil {
			prev.cands = append(prev.cands, spec.cands...)
			prev.item.Denied = prev.item.Denied || spec.item.Denied
			prev.addRoot(env, root)
		} else {
			apps[id] = &spec
			appOrder = append(appOrder, id)
		}
	}
	caches, _ := env.readDir(filepath.Join(env.home, "Library/Caches"))
	for _, e := range caches {
		if !strings.HasPrefix(e.Name(), "com.apple.") {
			addApp(e.Name(), filepath.Join(env.home, "Library/Caches", e.Name()), true)
		}
	}
	if env.fda {
		for _, e := range containers {
			if e.IsDir() && !strings.HasPrefix(e.Name(), "com.apple.") {
				addApp(e.Name(), filepath.Join(env.home, "Library/Containers", e.Name(), "Data/Library/Caches"), false)
			}
		}
	}
	support, _ := env.readDir(filepath.Join(env.home, "Library/Application Support"))
	for _, e := range support {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "com.apple.") {
			continue
		}
		for _, name := range chromiumCacheDirs {
			addApp(e.Name(), filepath.Join(env.home, "Library/Application Support", e.Name(), name), false)
		}
	}
	for _, id := range appOrder {
		emit(*apps[id])
	}
	return specs, sum
}

// itemPlan holds what cleaning an item would remove now, largest first.
type itemPlan struct {
	item    JunkItem
	entries []cleanEntry
}

// planItem measures candidates using the shared pool and existing removal guards.
func planItem(ctx context.Context, spec itemSpec, env junkEnv, sem chan struct{}, measuring func(string)) itemPlan {
	plan := itemPlan{item: spec.item}
	plan.item.Size, plan.item.Files, plan.item.Top, plan.item.Subs = 0, 0, nil, nil
	// A running owner's item is measured only to show what quitting it would
	// free; it gets no entries, so nothing of it is removed.
	running := spec.item.Running
	measured := make([]cleanEntry, len(spec.cands))
	newest := make([]time.Time, len(spec.cands))
	var wg sync.WaitGroup
	started := make([]bool, len(spec.cands))
candidates:
	for i, c := range spec.cands {
		if ctx.Err() != nil {
			break
		}
		if env.traversalBlocked(c.path) || !running && env.guard.inUse(c.path) {
			plan.item.InUse++
			continue
		}
		info, err := os.Lstat(c.path)
		if err != nil || !safeToClean(c.path) || env.protected(c.path) || removalRefused(c.path, info) {
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break candidates
		}
		if ctx.Err() != nil {
			<-sem
			break
		}
		started[i] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if measuring != nil {
				measuring(c.path)
			}
			size, files, t := measure(c.path)
			measured[i], newest[i] = cleanEntry{c.path, size, files}, t
		}()
	}
	wg.Wait()
	t := now()
	for i, e := range measured {
		if !started[i] {
			continue
		}
		if age := spec.cands[i].minAge; age > 0 && t.Sub(newest[i]) < age {
			continue
		}
		plan.entries = append(plan.entries, e)
	}
	sort.SliceStable(plan.entries, func(i, j int) bool { return plan.entries[i].size > plan.entries[j].size })
	for _, e := range plan.entries {
		plan.item.Size += e.size
		plan.item.Files += e.files
	}
	for _, e := range plan.entries[:min(5, len(plan.entries))] {
		plan.item.Top = append(plan.item.Top, TopItem{Name: filepath.Base(e.path), Path: e.path, Size: e.size})
	}
	if running {
		plan.entries = nil
		return plan
	}
	if spec.rule != nil && spec.rule.Detail != detailNone {
		plan.item.Subs = itemSubs(ctx, spec.rule.Detail, plan.entries)
	}
	return plan
}

// itemSubs supplies selectable metadata for each measured entry.
func itemSubs(ctx context.Context, kind detailKind, entries []cleanEntry) []SubItem {
	subs := make([]SubItem, 0, len(entries))
	newest := map[string]int{}
	for _, e := range entries {
		sub := SubItem{Path: e.path, Name: filepath.Base(e.path), Size: e.size, Default: true}
		switch kind {
		case detailFiles:
			if info, err := os.Lstat(e.path); err == nil {
				sub.Detail = info.ModTime().Format("2006-01-02")
			}
		case detailBackups, detailArchives:
			out, err := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", filepath.Join(e.path, "Info.plist")).Output()
			if err == nil {
				values := plistValues(out)
				nameKey, dateKey := "Device Name", "Last Backup Date"
				if kind == detailArchives {
					nameKey, dateKey = "Name", "CreationDate"
				}
				if name := values[nameKey]; name != "" {
					sub.Name = name
				}
				sub.Detail = strings.SplitN(values[dateKey], "T", 2)[0]
			}
		case detailDeviceSupport:
			sub.Detail = strings.TrimSpace(strings.TrimSuffix(filepath.Base(filepath.Dir(e.path)), "DeviceSupport"))
			platform := filepath.Dir(e.path)
			prev, ok := newest[platform]
			if !ok || compareVersions(deviceSupportVersion(sub.Name), deviceSupportVersion(subs[prev].Name)) > 0 {
				newest[platform] = len(subs)
			}
		}
		subs = append(subs, sub)
	}
	if kind == detailDeviceSupport {
		for _, i := range newest {
			subs[i].Default = false
		}
	}
	return subs
}

// deviceVersion matches dotted version numbers, rather than model identifiers.
var deviceVersion = regexp.MustCompile(`\d+(?:\.\d+)+`)

// deviceSupportVersion returns the first dotted version not followed by a comma.
func deviceSupportVersion(name string) []int {
	for _, match := range deviceVersion.FindAllStringIndex(name, -1) {
		if match[1] < len(name) && name[match[1]] == ',' {
			continue
		}
		var version []int
		for _, part := range strings.Split(name[match[0]:match[1]], ".") {
			n, _ := strconv.Atoi(part)
			version = append(version, n)
		}
		return version
	}
	return nil
}

// compareVersions compares dotted versions with trailing zero components.
func compareVersions(a, b []int) int {
	for i := 0; i < max(len(a), len(b)); i++ {
		x, y := 0, 0
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

// JunkScanEvent reports a streamed junk scan.
type JunkScanEvent struct {
	Kind    string       `json:"kind"`
	Items   []JunkItem   `json:"items,omitempty"`
	Path    string       `json:"path,omitempty"`
	Item    *JunkItem    `json:"item,omitempty"`
	Summary *ScanSummary `json:"summary,omitempty"`
}

// JunkCleanEvent reports a streamed junk clean.
type JunkCleanEvent struct {
	Kind   string       `json:"kind"`
	ID     string       `json:"id"`
	Path   string       `json:"path,omitempty"`
	Bytes  int64        `json:"bytes,omitempty"`
	Result *CleanResult `json:"result,omitempty"`
}

// JunkSelection chooses an item and optionally its sub-items.
type JunkSelection struct {
	ID    string   `json:"id"`
	Paths []string `json:"paths,omitempty"`
}

// scanJunk expands and measures items with serialized callbacks.
func scanJunk(ctx context.Context, env junkEnv, measuring func(string), sized func(JunkItem)) ([]JunkItem, ScanSummary) {
	specs, summary := expandItems(env)
	return measureItems(ctx, specs, env, measuring, sized), summary
}

// measureItems measures expanded items once, sharing one bounded pool.
func measureItems(ctx context.Context, specs []itemSpec, env junkEnv, measuring func(string), sized func(JunkItem)) []JunkItem {
	items := make([]JunkItem, len(specs))
	var mu sync.Mutex
	report := func(fn func()) {
		mu.Lock()
		defer mu.Unlock()
		if ctx.Err() == nil {
			fn()
		}
	}
	var onMeasure func(string)
	if measuring != nil {
		onMeasure = func(path string) { report(func() { measuring(path) }) }
	}
	sem := make(chan struct{}, scanConcurrency)
	var wg sync.WaitGroup
	for i := range specs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			items[i] = planItem(ctx, specs[i], env, sem, onMeasure).item
			if sized != nil {
				report(func() { sized(items[i]) })
			}
		}()
	}
	wg.Wait()
	return items
}

// cleanJunk cleans selections in order, taking a fresh look before each item.
func cleanJunk(ctx context.Context, env junkEnv, selections []JunkSelection, scannedAt time.Time, hooks cleanHooks) []CleanResult {
	specs, _ := expandItems(env)
	byID := make(map[string]itemSpec, len(specs))
	for _, s := range specs {
		byID[s.item.ID] = s
	}
	ordered := append([]JunkSelection(nil), selections...)
	slices.SortStableFunc(ordered, func(a, b JunkSelection) int {
		if a.ID == "sys.trash" && b.ID != "sys.trash" {
			return -1
		}
		if b.ID == "sys.trash" && a.ID != "sys.trash" {
			return 1
		}
		return 0
	})
	var results []CleanResult
	var cleaned int64
	seen := map[string]bool{}
	for _, sel := range ordered {
		if ctx.Err() != nil {
			break
		}
		if seen[sel.ID] {
			continue
		}
		seen[sel.ID] = true
		if hooks.started != nil {
			hooks.started(sel.ID)
		}
		if ctx.Err() != nil {
			break
		}
		if env.refreshGuard != nil {
			env.guard = env.refreshGuard()
		}
		spec, ok := byID[sel.ID]
		res := CleanResult{Name: sel.ID}
		if ok {
			owners := spec.owners
			if spec.rule != nil {
				owners = spec.rule.Owners
			}
			spec.item.Running = env.running(owners)
			if spec.item.Running {
				res.Skipped = "running"
			} else {
				res = cleanItem(ctx, spec, env, sel.Paths, scannedAt, hooks, &cleaned)
			}
		}
		results = append(results, res)
		if hooks.finished != nil {
			hooks.finished(res)
		}
	}
	return results
}

// cleanItem measures and removes an item's currently eligible entries.
func cleanItem(ctx context.Context, spec itemSpec, env junkEnv, paths []string, scannedAt time.Time, hooks cleanHooks, cleaned *int64) CleanResult {
	res := CleanResult{Name: spec.item.ID}
	// Select only current candidates; a parent path supplied by the UI grants nothing.
	if len(paths) > 0 {
		keys := map[string]bool{}
		for _, path := range paths {
			if env.traversalBlocked(path) {
				continue
			}
			key := canonical(path)
			if slices.ContainsFunc(spec.roots, func(root string) bool { return within(key, root) }) {
				keys[key] = true
			}
		}
		spec.cands = slices.DeleteFunc(append([]junkCand(nil), spec.cands...), func(c junkCand) bool { return env.traversalBlocked(c.path) || !keys[canonical(c.path)] })
	}
	var mu sync.Mutex
	checking := func(path string) {
		mu.Lock()
		defer mu.Unlock()
		if ctx.Err() == nil && hooks.checking != nil {
			hooks.checking(spec.item.ID, path)
		}
	}
	plan := planItem(ctx, spec, env, make(chan struct{}, scanConcurrency), checking)
	if len(paths) == 0 && len(plan.item.Subs) > 0 {
		selected := map[string]bool{}
		for _, sub := range plan.item.Subs {
			if sub.Default {
				selected[canonical(sub.Path)] = true
			}
		}
		plan.entries = slices.DeleteFunc(plan.entries, func(e cleanEntry) bool { return !selected[canonical(e.path)] })
	}
	res.InUse = int64(plan.item.InUse)
	if plan.item.Denied {
		res.Failed = append(res.Failed, "blocked")
	}
	trash := canonical(userTrashDir())
	for _, e := range plan.entries {
		if ctx.Err() != nil {
			break
		}
		key := canonical(e.path)
		if !slices.ContainsFunc(spec.roots, func(root string) bool { return within(key, root) }) || !env.removable(e.path, spec.item.Trash) {
			res.Failed = append(res.Failed, "blocked")
			continue
		}
		info, err := os.Lstat(e.path)
		if err != nil {
			res.Failed = append(res.Failed, "gone")
			continue
		}
		if removalRefused(e.path, info) {
			res.Failed = append(res.Failed, "blocked")
			continue
		}
		if env.guard.inUse(e.path) {
			res.InUse++
			continue
		}
		inTrash := within(key, trash)
		if inTrash && !scannedAt.IsZero() && changedAt(info).After(scannedAt) {
			continue
		}
		permanent := !spec.item.Trash || inTrash
		if permanent {
			err = removeAll(e.path)
		} else {
			err = trashItem(e.path)
		}
		if err != nil {
			res.Failed = append(res.Failed, "blocked")
			continue
		}
		if permanent {
			res.Freed += e.size
		} else {
			res.Trashed += e.size
		}
		res.Files += e.files
		*cleaned += e.size
		if hooks.removed != nil {
			hooks.removed(spec.item.ID, e.path, *cleaned)
		}
	}
	return res
}

// removable applies the shared guards to resolved home and temporary paths.
func (env junkEnv) removable(path string, trash bool) bool {
	if env.traversalBlocked(path) || env.protected(path) || !safeToClean(path) {
		return false
	}
	home := resolvePath(env.home)
	if within(path, home) {
		return removable(filepath.Join(env.home, strings.TrimPrefix(path, home)))
	}
	// Temporary junk is permanently removed; trashable intentionally only accepts home paths.
	return !trash && within(canonical(path), canonical(env.tmp)) && canonical(path) != canonical(env.tmp)
}

// ScanJunk streams an initial list, measured rows and a final summary.
func (Cleaner) ScanJunk(ctx context.Context, ch *mygo.Channel[JunkScanEvent]) error {
	env := currentJunkEnv()
	specs, summary := expandItems(env)
	initial := make([]JunkItem, len(specs))
	for i := range specs {
		initial[i] = specs[i].item
		initial[i].Size = -1
	}
	if err := ch.Send(JunkScanEvent{Kind: "start", Items: initial}); err != nil {
		return err
	}
	var last time.Time
	measureItems(ctx, specs, env, func(path string) {
		if time.Since(last) >= progressInterval {
			last = time.Now()
			_ = ch.Send(JunkScanEvent{Kind: "scanning", Path: path})
		}
	}, func(item JunkItem) { _ = ch.Send(JunkScanEvent{Kind: "item", Item: &item}) })
	if ctx.Err() == nil {
		if err := ch.Send(JunkScanEvent{Kind: "done", Summary: &summary}); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// CleanJunk streams progress, refreshing file usage before each selected item.
func (Cleaner) CleanJunk(ctx context.Context, selections []JunkSelection, scannedAt int64, ch *mygo.Channel[JunkCleanEvent]) error {
	env := currentJunkEnv()
	env.refreshGuard = probeUsage
	var last time.Time
	var cleaned int64
	since := time.Time{}
	if scannedAt > 0 {
		since = time.UnixMilli(scannedAt)
	}
	every := func(event JunkCleanEvent) {
		if time.Since(last) >= progressInterval {
			last = time.Now()
			_ = ch.Send(event)
		}
	}
	cleanJunk(ctx, env, selections, since, cleanHooks{
		started:  func(id string) { _ = ch.Send(JunkCleanEvent{Kind: "start", ID: id}) },
		checking: func(id, path string) { every(JunkCleanEvent{Kind: "checking", ID: id, Path: path}) },
		removed: func(id, path string, bytes int64) {
			cleaned = bytes
			every(JunkCleanEvent{Kind: "removed", ID: id, Path: path, Bytes: bytes})
		},
		finished: func(res CleanResult) {
			_ = ch.Send(JunkCleanEvent{Kind: "done", ID: res.Name, Bytes: cleaned, Result: &res})
		},
	})
	return ctx.Err()
}

// traversalBlocked rejects links and checks permissions before inspecting descendants.
func (env junkEnv) traversalBlocked(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	envHome, envTmp := filepath.Clean(env.home), filepath.Clean(env.tmp) // $TMPDIR ends in a slash
	home, tmp := resolvePath(envHome), resolvePath(envTmp)
	root := ""
	switch {
	case within(abs, envHome):
		root, abs = home, filepath.Join(home, strings.TrimPrefix(abs, envHome))
	case within(abs, home):
		root = home
	case within(abs, envTmp):
		root, abs = tmp, filepath.Join(tmp, strings.TrimPrefix(abs, envTmp))
	case within(abs, tmp):
		root = tmp
	default:
		return true
	}
	containers := strings.ToLower(filepath.Join(home, "Library/Containers"))
	if !env.fda && strings.ToLower(abs) != containers && within(strings.ToLower(abs), containers) {
		return true
	}
	relative, err := filepath.Rel(root, abs)
	if err != nil {
		return true
	}
	prefix := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." {
			continue
		}
		prefix = filepath.Join(prefix, part)
		info, err := env.stat(prefix)
		if err != nil {
			return !errors.Is(err, fs.ErrNotExist)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// stat reads entry metadata, with a scan-local hook for boundary tests.
func (env junkEnv) stat(path string) (fs.FileInfo, error) {
	if env.lstat != nil {
		return env.lstat(path)
	}
	return os.Lstat(path)
}

// readDir lists only targets whose prefixes permit traversal.
func (env junkEnv) readDir(path string) ([]os.DirEntry, error) {
	if env.traversalBlocked(path) {
		return nil, fs.ErrPermission
	}
	return os.ReadDir(path)
}
