package main

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
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
	ID             string       `json:"id"`
	Group          string       `json:"group"`
	Name           string       `json:"name"`
	Size           int64        `json:"size"`
	Files          int64        `json:"files"`
	Default        bool         `json:"default"`
	Trash          bool         `json:"trash"`
	Running        bool         `json:"running"`
	InUse          int          `json:"inUse"`
	Denied         bool         `json:"denied"`
	Estimate       bool         `json:"estimate"`
	Paths          []string     `json:"paths"`
	Top            []TopItem    `json:"top"`
	Subs           []SubItem    `json:"subs"`
	Skipped        string       `json:"skipped,omitempty"`
	FoundSize      int64        `json:"foundSize,omitempty"` // measured candidates, including in-use entries
	ReadErrors     int          `json:"readErrors,omitempty"`
	Details        []JunkDetail `json:"details,omitempty"`
	DetailsDropped int          `json:"detailsDropped,omitempty"`
}

// JunkDetail records a candidate outcome; it never authorizes deletion.
type JunkDetail struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Reason string `json:"reason"` // eligible, running, in-use, too-new, boundary, read-error
}

func (it *JunkItem) recordDetail(path string, size int64, reason string) {
	if len(it.Details) < 200 {
		it.Details = append(it.Details, JunkDetail{Path: path, Size: size, Reason: reason})
	} else {
		it.DetailsDropped++
	}
}

// SubItem is a separately selectable part of an item.
type SubItem struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Detail  string `json:"detail"`
	Size    int64  `json:"size"`
	Files   int64  `json:"files,omitempty"` // files inside, for folders such as crash dump queues
	Default bool   `json:"default"`
	AgeDays *int64 `json:"ageDays,omitempty"` // installer age hint; nil when unavailable
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
	home, tmp             string
	localAppData, appData string
	windowsLayout         bool
	rules                 []Rule
	ctx                   context.Context
	apps                  *appIndex
	guard                 *usageGuard
	fda                   bool
	settings              Settings
	findTool              func(string) string
	refreshGuard          func() *usageGuard
	commandClaims         map[string]claimSet
	lstat                 func(string) (fs.FileInfo, error)
	observe               bool // scanning only: measure excluded capacity, never create extra deletion entries
}

// currentJunkEnv probes the settings, apps, usage and permissions for a pass.
func currentJunkEnv() junkEnv {
	env := junkEnv{windowsLayout: runtime.GOOS == "windows", home: homeDir(), tmp: filepath.Clean(os.TempDir()), apps: buildAppIndex(appDirs()), guard: probeUsage(), fda: fullDiskAccess(), settings: loadSettings(), findTool: findTool}
	if runtime.GOOS == "windows" {
		env.localAppData = env.appDataRoot(os.Getenv("LOCALAPPDATA"))
		env.appData = env.appDataRoot(os.Getenv("APPDATA"))
	}
	return env
}

// catalog permits portable table tests while production uses the host catalog.
func (env junkEnv) catalog() []Rule {
	if env.rules != nil {
		return env.rules
	}
	return junkRules
}

// appDataRoot accepts only resolved, traversable roots inside the profile.
func (env junkEnv) appDataRoot(root string) string {
	if root == "" || !filepath.IsAbs(root) || env.traversalBlocked(root) {
		return ""
	}
	resolved, home := resolvePath(root), resolvePath(env.home)
	if resolved == "" || home == "" || !within(resolved, home) || canonical(resolved) == canonical(home) {
		return ""
	}
	return resolved
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

// claimSet holds the paths items have taken, in comparison form, so that
// overlapping rules never remove the same path. Lookups walk a path's
// ancestors instead of every claim: a scan meets tens of thousands of
// entries and claims.
type claimSet struct {
	roots map[string]bool // claimed paths
	below map[string]bool // folders with a claimed path somewhere inside
}

// claimed reports whether a path is already owned by another item.
func (c claimSet) claimed(p string) bool { return c.claimedKey(canonical(p)) }

// inside reports whether a path contains another item's target.
func (c claimSet) inside(p string) bool { return c.insideKey(canonical(p)) }

// overlaps reports a target that contains or is inside an earlier claim.
func (c claimSet) overlaps(roots []string) bool {
	return slices.ContainsFunc(roots, func(root string) bool { return c.claimed(root) || c.inside(root) })
}

// claimedKey is claimed for a path already in comparison form.
func (c claimSet) claimedKey(key string) bool {
	for p := key; ; p = filepath.Dir(p) {
		if c.roots[p] {
			return true
		}
		if parent := filepath.Dir(p); parent == p {
			return false
		}
	}
}

// insideKey is inside for a path already in comparison form.
func (c claimSet) insideKey(key string) bool { return c.below[key] }

// claim records a path in comparison form.
func (c *claimSet) claim(p string) { c.claimKey(canonical(p)) }

// claimKey records a path already in comparison form.
func (c *claimSet) claimKey(key string) {
	if c.roots == nil {
		c.roots, c.below = map[string]bool{}, map[string]bool{}
	}
	c.roots[key] = true
	for p := filepath.Dir(key); !c.below[p]; p = filepath.Dir(p) {
		c.below[p] = true
		if filepath.Dir(p) == p {
			break
		}
	}
}

// clone copies the set, for a snapshot that later claims do not change.
func (c claimSet) clone() claimSet {
	return claimSet{roots: maps.Clone(c.roots), below: maps.Clone(c.below)}
}

// protected reports hard protected paths and their descendants.
func (env junkEnv) protected(p string) bool { return env.protectedKey(canonical(p)) }

// protectedKey is protected for a path already in comparison form.
func (env junkEnv) protectedKey(key string) bool {
	for path := key; within(path, canonical(env.home)); path = filepath.Dir(path) {
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
	prefix, suffix, _ := strings.Cut(pattern, "/")
	switch prefix {
	case "$TMPDIR":
		if !allowedTemp(env.home, env.tmp) {
			return nil
		}
		root, relative = env.tmp, suffix
	case "$LOCALAPPDATA", "$APPDATA":
		root = env.localAppData
		if prefix == "$APPDATA" {
			root = env.appData
		}
		root = env.appDataRoot(root)
		if root == "" {
			return nil
		}
		relative = suffix
	}
	paths := []string{root}
	for _, part := range strings.Split(relative, "/") { // slash-ok: rule patterns always use slash-separated components
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
	// Entries are judged from the listing: the folder was checked above, so an
	// entry that is not a link resolves to the folder's resolved path plus its
	// name. Temporary folders hold tens of thousands of entries, and a system
	// call or two per entry made the first screen wait for seconds.
	parent := resolvePath(dir)
	for _, e := range entries {
		if !nameMatches(e.Name(), names) || e.Type()&(fs.ModeSymlink|fs.ModeSocket|fs.ModeNamedPipe|fs.ModeDevice|fs.ModeCharDevice|fs.ModeIrregular) != 0 {
			continue
		}
		if runtime.GOOS == "windows" {
			info, err := e.Info()
			if err != nil || !platformMeasureEntry(filepath.Join(dir, e.Name()), info) {
				continue
			}
		}
		resolved := filepath.Join(parent, e.Name())
		key := strings.ToLower(resolved)
		if env.protectedKey(key) || claims.claimedKey(key) || !safeResolved(key) {
			continue
		}
		if claims.insideKey(key) {
			if e.IsDir() {
				env.collectDir(filepath.Join(dir, e.Name()), nil, age, claims, spec)
			}
			continue
		}
		spec.cands = append(spec.cands, junkCand{path: resolved, minAge: age})
	}
}

// collectRecursiveFiles walks only ordinary visible directories. Matching regular
// files and legacy pkg/mpkg bundles are candidates; containers are never candidates,
// and bundles are not entered to look for embedded installers.
func (env junkEnv) collectRecursiveFiles(dir string, names []string, age time.Duration, claims claimSet, spec *itemSpec) {
	if env.toolContext().Err() != nil || env.protected(dir) || claims.claimed(dir) {
		return
	}
	if env.traversalBlocked(dir) {
		spec.item.Denied = true
		return
	}
	key := canonical(dir)
	home := canonical(env.home)
	if !safeResolved(key) && !slices.Contains(protectedHomeDirs, strings.TrimPrefix(key, home)) {
		return
	}
	entries, err := os.ReadDir(dir) // all prefixes were checked above
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			spec.item.Denied = true
		}
		return
	}
	parent := resolvePath(dir)
	for _, entry := range entries {
		if env.toolContext().Err() != nil {
			return
		}
		if strings.HasPrefix(entry.Name(), ".") || entry.Type()&(fs.ModeSymlink|fs.ModeSocket|fs.ModeNamedPipe|fs.ModeDevice|fs.ModeCharDevice|fs.ModeIrregular) != 0 {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		matches := nameMatches(strings.ToLower(entry.Name()), names)
		// A nonmatching file cannot become a candidate. Avoid resolving and
		// guarding every source, dependency and media file in large folders.
		if !entry.IsDir() && !matches {
			continue
		}
		if entry.IsDir() && (isPackage(entry.Name()) || ext == ".download" || ext == ".crdownload" || ext == ".part") {
			continue
		}
		path := filepath.Join(parent, entry.Name())
		key := strings.ToLower(path)
		if !safeResolved(key) || env.protectedKey(key) || claims.claimedKey(key) {
			continue
		}
		if entry.IsDir() {
			// Legacy installer bundles are moved whole; ordinary dmg/xip-named
			// folders do not qualify and are not traversed.
			if matches {
				if (ext == ".pkg" || ext == ".mpkg") && !claims.insideKey(key) {
					env.collect(path, age, claims, spec)
				}
				continue
			}
			env.collectRecursiveFiles(path, names, age, claims, spec)
		} else if matches && entry.Type().IsRegular() {
			env.collect(path, age, claims, spec)
		}
	}
}

// running reports whether any owner is running.
func (env junkEnv) running(owners []string) bool {
	return slices.ContainsFunc(owners, func(id string) bool { return env.guard.running[strings.ToLower(id)] })
}

// expandItems resolves the catalog, customs and per-app sources in claim order.
func expandItems(env *junkEnv) ([]itemSpec, ScanSummary) {
	defer startExpandMemo()()
	if env.commandClaims == nil {
		env.commandClaims = map[string]claimSet{}
	}
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
		if len(spec.cands) == 0 && !spec.item.Denied && !(spec.rule != nil && (spec.rule.RecycleBin || spec.rule.Command != nil && spec.item.Size == -1)) {
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
	rules := env.catalog()
	for i := range rules {
		r := &rules[i]
		if env.toolContext().Err() != nil {
			break
		}
		sum.Checked++
		if r.RecycleBin {
			emit(itemSpec{rule: r, item: JunkItem{ID: r.ID, Group: string(r.Group)}})
			continue
		}
		if r.Command != nil {
			env.commandClaims[r.ID] = claims.clone()
			emit(expandCommand(*env, r))
			continue
		}
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
				spec.addRoot(*env, root)
				switch target.Mode {
				case matched:
					env.collect(root, r.MinAge, claims, &spec)
				case recursiveFiles:
					env.collectRecursiveFiles(root, target.Names, r.MinAge, claims, &spec)
				default:
					env.collectDir(root, target.Names, r.MinAge, claims, &spec)
				}
				// Filtered targets own matching entries, not unrelated files in the directory.
				if len(target.Names) == 0 {
					reserve = append(reserve, root)
				}
			}
		}
		if len(r.ExcludeNames) > 0 {
			spec.cands = slices.DeleteFunc(spec.cands, func(c junkCand) bool { return nameMatches(strings.ToLower(filepath.Base(c.path)), r.ExcludeNames) })
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
		spec.addRoot(*env, folder.Path)
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
		owners := []string{owner.BundleID}
		if env.windowsLayout {
			owners = []string{strings.TrimSuffix(strings.ToLower(name), ".exe")}
		}
		spec := itemSpec{owners: owners, item: JunkItem{ID: id, Group: string(groupApps), Name: display, Size: -1, Default: !unselectedApps[strings.ToLower(owner.BundleID)], Running: env.running(owners)}}
		if whole {
			env.collect(root, 0, claims, &spec)
		} else {
			env.collectDir(root, nil, 0, claims, &spec)
		}
		if len(spec.cands) == 0 && !spec.item.Denied {
			return
		}
		spec.addRoot(*env, root)
		for _, c := range spec.cands {
			claims.claim(c.path)
		}
		if prev := apps[id]; prev != nil {
			prev.cands = append(prev.cands, spec.cands...)
			prev.item.Denied = prev.item.Denied || spec.item.Denied
			prev.addRoot(*env, root)
		} else {
			apps[id] = &spec
			appOrder = append(appOrder, id)
		}
	}
	if env.windowsLayout {
		for _, root := range env.globTargets("$APPDATA/*") {
			marker := filepath.Join(root, "Local State")
			if env.traversalBlocked(marker) {
				continue
			}
			info, err := env.stat(marker)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			for _, dir := range chromiumCacheDirs {
				addApp(filepath.Base(root), filepath.Join(root, dir), false)
			}
		}
	} else {
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
	if spec.rule != nil && spec.rule.RecycleBin {
		if env.observe {
			plan.item = scanNativeBin(env, plan.item)
		}
		return plan
	}
	if spec.rule != nil && spec.rule.Command != nil {
		plan.item.Size = 0
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return plan
		}
		defer func() { <-sem }()
		if ctx.Err() != nil {
			return plan
		}
		env.ctx = ctx
		tool := env.findTool(spec.rule.Command.Tool)
		if tool == "" {
			return plan
		}
		if measuring != nil {
			measuring(tool)
		}
		size, upper, roots, err := estimateCommand(env, spec.rule.Command, tool)
		if err != nil || ctx.Err() != nil || env.commandClaims[spec.rule.ID].overlaps(roots) {
			return plan
		}
		plan.item.Size, plan.item.Estimate = size, upper
		for _, root := range roots {
			spec.addRoot(env, root)
		}
		plan.item.Paths = spec.item.Paths
		return plan
	}
	plan.item.Size, plan.item.Files, plan.item.Top, plan.item.Subs = 0, 0, nil, nil
	plan.item.FoundSize, plan.item.ReadErrors, plan.item.Details, plan.item.DetailsDropped = 0, 0, nil, 0
	// Discovery may measure a busy candidate, but only eligible entries can
	// reach a deletion plan. Cleaning never enables observe.
	running := spec.item.Running
	// Crashpad folders stay while their handler writes into them: an open
	// dump inside is skipped when cleaning, not the whole folder.
	contents := spec.rule != nil && spec.rule.Contents
	measured := make([]cleanEntry, len(spec.cands))
	newest := make([]time.Time, len(spec.cands))
	errorsSeen := make([]int, len(spec.cands))
	reasons := make([]string, len(spec.cands))
	parentOK := map[string]bool{}
	var wg sync.WaitGroup
	started := make([]bool, len(spec.cands))
candidates:
	for i, c := range spec.cands {
		if ctx.Err() != nil {
			break
		}
		parent := filepath.Dir(c.path)
		ok, seen := parentOK[parent]
		if !seen {
			ok = !env.traversalBlocked(parent)
			parentOK[parent] = ok
		}
		if !ok {
			plan.item.InUse++
			plan.item.ReadErrors++
			plan.item.recordDetail(env.displayable(c.path), 0, "boundary")
			continue
		}
		busy := !running && !contents && env.guard.inUse(c.path)
		if busy {
			plan.item.InUse++
			if !env.observe {
				continue
			}
		}
		info, err := os.Lstat(c.path)
		if err != nil {
			plan.item.ReadErrors++
			plan.item.recordDetail(env.displayable(c.path), 0, "read-error")
			continue
		}
		if info.Mode()&linkModes != 0 || removalRefused(c.path, info) {
			plan.item.recordDetail(env.displayable(c.path), 0, "boundary")
			continue
		}
		if c.minAge > 0 && now().Sub(changedAt(info)) < c.minAge {
			plan.item.recordDetail(env.displayable(c.path), 0, "too-new")
			continue
		}
		reasons[i] = "eligible"
		if running {
			reasons[i] = "running"
		} else if busy {
			reasons[i] = "in-use"
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
			size, files, changed, failures := measureChecked(c.path, ctx)
			if contents {
				size, files, changed, failures = measureCrashpad(c.path)
			}
			measured[i], newest[i], errorsSeen[i] = cleanEntry{c.path, size, files}, changed, failures
		}()
	}
	wg.Wait()
	t := now()
	for i, e := range measured {
		if !started[i] {
			continue
		}
		reason := reasons[i]
		if age := spec.cands[i].minAge; age > 0 && t.Sub(newest[i]) < age {
			reason = "too-new"
		}
		if errorsSeen[i] > 0 {
			plan.item.ReadErrors += errorsSeen[i]
			reason = "read-error"
		}
		// Too-new data remains outside the rule's candidate-capacity metric.
		if reason != "too-new" {
			plan.item.FoundSize += e.size
		}
		plan.item.recordDetail(env.displayable(e.path), e.size, reason)
		if reason == "eligible" || reason == "running" {
			plan.entries = append(plan.entries, e)
		}
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
	if spec.rule != nil && spec.rule.Detail == detailCrashpad {
		// A few empty sidecar files are not worth a row, nor a selection.
		plan.entries = slices.DeleteFunc(plan.entries, func(e cleanEntry) bool { return e.size < crashpadMinSize })
		plan.item.Size, plan.item.Files = 0, 0
		for _, e := range plan.entries {
			plan.item.Size += e.size
			plan.item.Files += e.files
		}
		plan.item.Subs = crashpadSubs(env, plan.entries)
		// Each database with dumps is already a sub-item; details keep only
		// the ones left out, and empty databases are not worth a line.
		plan.item.Details = slices.DeleteFunc(plan.item.Details, func(d JunkDetail) bool { return d.Reason == "eligible" || d.Size == 0 && d.Reason != "read-error" })
	} else if spec.rule != nil && spec.rule.Detail != detailNone {
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
		case detailFiles, detailInstallers:
			if info, err := os.Lstat(e.path); err == nil {
				sub.Detail = info.ModTime().Format("2006-01-02")
				if kind == detailInstallers {
					age := max(int64(0), int64(now().Sub(changedAt(info))/(24*time.Hour)))
					sub.AgeDays = &age
				}
			}
		case detailBackups, detailArchives:
			out, err := quietCommand(exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", filepath.Join(e.path, "Info.plist"))).Output()
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
	env.ctx = ctx
	env.observe = true
	specs, summary := expandItems(&env)
	return measureJunk(ctx, specs, env, summary, measuring, sized)
}

// measureJunk collects leftovers alongside item measurements for the final summary.
func measureJunk(ctx context.Context, specs []itemSpec, env junkEnv, summary ScanSummary, measuring func(string), sized func(JunkItem)) ([]JunkItem, ScanSummary) {
	var wg sync.WaitGroup
	if ctx.Err() == nil && !slices.Contains(env.settings.DisabledGroups, string(groupReview)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			roots := slices.Clone(libraryRoots)
			if !env.fda {
				roots = slices.DeleteFunc(roots, func(root string) bool { return root == "Containers" })
			}
			items, total := collectOrphans(ctx, nil, roots...)
			if ctx.Err() == nil {
				summary.Leftovers, summary.LeftoversSize = len(items), total
			}
		}()
	}
	items := measureItems(ctx, specs, env, measuring, sized)
	wg.Wait()
	return items, summary
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
	env.ctx = ctx
	env.observe = false
	specs, _ := expandItems(&env)
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
		// A selected command still needs a result if its tool or estimate vanished.
		if !ok {
			rules := env.catalog()
			for i := range rules {
				rule := &rules[i]
				if rule.ID == sel.ID && rule.Command != nil && !slices.Contains(env.settings.Ignored, rule.ID) && !slices.Contains(env.settings.DisabledGroups, string(rule.Group)) {
					spec = itemSpec{rule: rule, item: JunkItem{ID: rule.ID}}
					ok = true
					break
				}
			}
		}
		res := CleanResult{Name: sel.ID}
		if ok {
			owners := spec.owners
			if spec.rule != nil {
				owners = commandOwners(spec.rule)
			}
			spec.item.Running = env.running(owners)
			if spec.item.Running {
				res.Skipped = "running"
			} else if spec.rule != nil && spec.rule.RecycleBin {
				res = cleanNativeBin(ctx, env, sel.Paths)
				cleaned += res.Freed
				if hooks.removed != nil && res.Freed > 0 {
					hooks.removed(sel.ID, "", cleaned)
				}
			} else if spec.rule != nil && spec.rule.Command != nil {
				if len(sel.Paths) > 0 {
					res.Skipped, res.Failed = "failed", []string{"blocked"}
				} else {
					res = cleanTool(ctx, spec.rule, env, hooks, &cleaned)
				}
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
	trash := userTrashDir()
	if trash != "" {
		trash = canonical(trash)
	}
	if plan.item.ReadErrors > 0 {
		res.Failed = append(res.Failed, "blocked")
	}
	for _, e := range plan.entries {
		if ctx.Err() != nil {
			break
		}
		key := canonical(e.path)
		if !slices.ContainsFunc(spec.roots, func(root string) bool { return within(key, root) }) || !env.removable(e.path, spec.item.Trash) || !platformRemovalSafe(e.path, ctx) {
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
		if spec.rule != nil && spec.rule.Contents {
			// Empty the report queues; the folders and settings.dat stay, and so
			// does any dump a process has open.
			if info.Mode()&linkModes != 0 || !info.IsDir() {
				res.Failed = append(res.Failed, "blocked")
				continue
			}
			base := *cleaned
			freed, files, inUse, err := emptyCrashpad(ctx, e.path, env.guard, func(b int64) {
				if hooks.removed != nil {
					hooks.removed(spec.item.ID, e.path, base+b)
				}
			})
			res.Freed += freed
			res.Files += files
			res.InUse += inUse
			*cleaned += freed
			if err != nil && ctx.Err() == nil {
				res.Failed = append(res.Failed, "blocked")
			}
			continue
		}
		if env.guard.inUse(e.path) {
			res.InUse++
			continue
		}
		inTrash := runtime.GOOS != "windows" && trash != "" && within(key, trash)
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
			code := "blocked"
			if runtime.GOOS == "windows" && !permanent {
				code = err.Error()
			}
			res.Failed = append(res.Failed, code)
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
		return cleanable(filepath.Join(env.home, strings.TrimPrefix(path, home)))
	}
	// Temporary junk is permanently removed; trashable intentionally only accepts home paths.
	return !trash && within(canonical(path), canonical(env.tmp)) && canonical(path) != canonical(env.tmp)
}

// Channel send seams let acceptance tests exercise the bound methods without a WebView.
var (
	junkScanSend  = func(ch *mygo.Channel[JunkScanEvent], event JunkScanEvent) error { return ch.Send(event) }
	junkCleanSend = func(ch *mygo.Channel[JunkCleanEvent], event JunkCleanEvent) error { return ch.Send(event) }
)

// ScanJunk streams an initial list, measured rows and a final summary.
func (Cleaner) ScanJunk(ctx context.Context, ch *mygo.Channel[JunkScanEvent]) error {
	env := currentJunkEnv()
	env.ctx = ctx
	env.observe = true
	specs, summary := expandItems(&env)
	initial := make([]JunkItem, len(specs))
	for i := range specs {
		initial[i] = specs[i].item
		initial[i].Size = -1
	}
	if err := junkScanSend(ch, JunkScanEvent{Kind: "start", Items: initial}); err != nil {
		return err
	}
	var last time.Time
	_, summary = measureJunk(ctx, specs, env, summary, func(path string) {
		if time.Since(last) >= progressInterval {
			last = time.Now()
			_ = junkScanSend(ch, JunkScanEvent{Kind: "scanning", Path: path})
		}
	}, func(item JunkItem) { _ = junkScanSend(ch, JunkScanEvent{Kind: "item", Item: &item}) })
	if ctx.Err() == nil {
		if err := junkScanSend(ch, JunkScanEvent{Kind: "done", Summary: &summary}); err != nil {
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
			_ = junkCleanSend(ch, event)
		}
	}
	cleanJunk(ctx, env, selections, since, cleanHooks{
		started:  func(id string) { _ = junkCleanSend(ch, JunkCleanEvent{Kind: "start", ID: id}) },
		checking: func(id, path string) { every(JunkCleanEvent{Kind: "checking", ID: id, Path: path}) },
		removed: func(id, path string, bytes int64) {
			cleaned = bytes
			every(JunkCleanEvent{Kind: "removed", ID: id, Path: path, Bytes: bytes})
		},
		finished: func(res CleanResult) {
			_ = junkCleanSend(ch, JunkCleanEvent{Kind: "done", ID: res.Name, Bytes: cleaned, Result: &res})
		},
	})
	return ctx.Err()
}

// traversalBlocked rejects links and checks permissions before inspecting descendants.
func (env junkEnv) traversalBlocked(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil || !platformCleanPath(path) || !platformCleanPath(abs) {
		return true
	}
	envHome, envTmp := filepath.Clean(env.home), filepath.Clean(env.tmp) // $TMPDIR ends in a slash
	home, tmp := resolvePath(envHome), resolvePath(envTmp)
	root := ""
	switch {
	case within(abs, envHome):
		suffix := strings.TrimPrefix(abs, envHome)
		if runtime.GOOS == "windows" {
			suffix = abs[len(envHome):]
		}
		root, abs = home, filepath.Join(home, suffix)
	case within(abs, home):
		root = home
	case within(abs, envTmp):
		suffix := strings.TrimPrefix(abs, envTmp)
		if runtime.GOOS == "windows" {
			suffix = abs[len(envTmp):]
		}
		root, abs = tmp, filepath.Join(tmp, suffix)
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
		if info.Mode()&linkModes != 0 {
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
	return memoLstat(path)
}

// expandMemo, while an expansion runs, remembers what paths resolve to and
// what lstat said about them: a scan meets thousands of entries, and
// resolving each from the root costs one lstat per path component, several
// times over (about 15 s on a real Mac). The memo is on only during
// expansion; what cleaning removes is checked again without it.
var expandMemo struct {
	sync.Mutex
	users    int
	resolved map[string]string
	lstat    map[string]lstatResult
}

// lstatResult is one remembered lstat.
type lstatResult struct {
	info fs.FileInfo
	err  error
}

// startExpandMemo turns the memo on until the returned function is called.
func startExpandMemo() (stop func()) {
	expandMemo.Lock()
	if expandMemo.users == 0 {
		expandMemo.resolved, expandMemo.lstat = map[string]string{}, map[string]lstatResult{}
	}
	expandMemo.users++
	expandMemo.Unlock()
	return func() {
		expandMemo.Lock()
		if expandMemo.users--; expandMemo.users == 0 {
			expandMemo.resolved, expandMemo.lstat = nil, nil
		}
		expandMemo.Unlock()
	}
}

// expandMemoActive reports whether an expansion's memo is on.
func expandMemoActive() bool {
	expandMemo.Lock()
	defer expandMemo.Unlock()
	return expandMemo.users > 0
}

// memoLstat is os.Lstat, remembered while the memo is on.
func memoLstat(path string) (fs.FileInfo, error) {
	expandMemo.Lock()
	on := expandMemo.users > 0
	if on {
		if r, ok := expandMemo.lstat[path]; ok {
			expandMemo.Unlock()
			return r.info, r.err
		}
	}
	expandMemo.Unlock()
	info, err := os.Lstat(path)
	if on {
		expandMemo.Lock()
		if expandMemo.lstat != nil {
			expandMemo.lstat[path] = lstatResult{info, err}
		}
		expandMemo.Unlock()
	}
	return info, err
}

// memoResolved answers resolvePath from the memo, building a path's answer
// from its parent's: a component that is not a link resolves to itself.
func memoResolved(p string) (string, bool) {
	expandMemo.Lock()
	if expandMemo.users == 0 {
		expandMemo.Unlock()
		return "", false
	}
	r, ok := expandMemo.resolved[p]
	expandMemo.Unlock()
	if ok {
		return r, true
	}
	parent := filepath.Dir(p)
	if runtime.GOOS == "windows" || parent == p {
		r = resolveUncached(p)
	} else if info, err := memoLstat(p); err == nil && info.Mode()&linkModes != 0 {
		r = resolveUncached(p) // a link: resolve it in full
	} else {
		r = filepath.Join(resolvePath(parent), filepath.Base(p))
	}
	expandMemo.Lock()
	if expandMemo.resolved != nil {
		expandMemo.resolved[p] = r
	}
	expandMemo.Unlock()
	return r, true
}

// readDir lists only targets whose prefixes permit traversal.
func (env junkEnv) readDir(path string) ([]os.DirEntry, error) {
	if env.traversalBlocked(path) {
		return nil, fs.ErrPermission
	}
	return os.ReadDir(path)
}
