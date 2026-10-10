package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"log"
	"math"
	"math/bits"
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/egoist/mygo"
)

// Category describes one kind of junk the app can scan and clean.
type Category struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Risk        string `json:"risk"` // "safe" or "moderate"
	Size        int64  `json:"size"`
	Files       int64  `json:"files"`
	// Missing lists the paths that do not exist.
	Missing []string `json:"missing"`
	// Denied lists the paths macOS does not let the app read, such as the
	// Trash without Full Disk Access.
	Denied []string `json:"denied"`
	// Blocked lists the paths the safety rules keep the app away from.
	Blocked []string `json:"blocked"`
	// InUse counts the entries left alone because they are in use.
	InUse int64 `json:"inUse"`
	// Permanent reports that cleaning deletes the entries instead of moving
	// them to the Trash.
	Permanent bool      `json:"permanent"`
	Top       []TopItem `json:"top"`
	paths     []string
	exclude   []string // canonical paths of the categories nested in this one
}

// TopItem is one of the largest direct children inside a category.
type TopItem struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// CategoryConfig is the persisted, user-editable definition of a category.
type CategoryConfig struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Risk        string   `json:"risk"`
	Paths       []string `json:"paths"`
	Enabled     bool     `json:"enabled"`
}

// CleanResult reports what a clean pass did.
type CleanResult struct {
	Name    string   `json:"name"`
	Freed   int64    `json:"freed"`   // bytes deleted
	Trashed int64    `json:"trashed"` // bytes moved to the Trash
	Files   int64    `json:"files"`
	InUse   int64    `json:"inUse"` // entries left alone because they are in use
	Failed  []string `json:"failed"`
}

// CleanError reports a category whose clean pass had failures.
type CleanError struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// CleanSummary aggregates the results of a clean pass.
type CleanSummary struct {
	Results      []CleanResult `json:"results"`
	TotalFreed   int64         `json:"totalFreed"`   // bytes deleted
	TotalTrashed int64         `json:"totalTrashed"` // bytes moved to the Trash
	InUse        int64         `json:"inUse"`        // entries left alone because they are in use
	Errors       []CleanError  `json:"errors"`
}

// AppInfo describes an installed application.
type AppInfo struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	BundleID   string `json:"bundleId"`
	Version    string `json:"version"`
	Size       int64  `json:"size"`
	LastOpened int64  `json:"lastOpened"` // unix seconds; 0 = never opened
}

// UninstallItem is one file or folder associated with an app.
type UninstallItem struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Size int64  `json:"size"`
}

// UninstallDetail is everything that would be removed for an app.
type UninstallDetail struct {
	App       AppInfo         `json:"app"`
	Items     []UninstallItem `json:"items"`
	TotalSize int64           `json:"totalSize"`
}

// UninstallResult reports what a single uninstall pass did.
type UninstallResult struct {
	Path   string   `json:"path"`
	Moved  int64    `json:"moved"`
	Freed  int64    `json:"freed"`
	Failed []string `json:"failed"`
}

// LargeFile is a single large file discovered by the large-file scan.
type LargeFile struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Trashable bool   `json:"trashable"`
}

// LargeScanEvent streams per-file results and the final summary.
type LargeScanEvent struct {
	Kind      string           `json:"kind"` // "item" | "done"
	Path      string           `json:"path,omitempty"`
	Size      int64            `json:"size,omitempty"`
	Trashable bool             `json:"trashable,omitempty"`
	Result    *LargeScanResult `json:"result,omitempty"`
}

// LargeScanResult aggregates large-file scan results.
type LargeScanResult struct {
	Items     []LargeFile `json:"items"`
	Scanned   int64       `json:"scanned"`
	Skipped   int64       `json:"skipped"`
	ElapsedMs int64       `json:"elapsedMs"`
	Threshold int64       `json:"threshold"`
}

// OrphanItem is a leftover directory or file not owned by an installed app.
type OrphanItem struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Kind      string `json:"kind"`
	Trashable bool   `json:"trashable"`
}

// OrphanScanResult aggregates leftover detection results.
type OrphanScanResult struct {
	Items     []OrphanItem `json:"items"`
	TotalSize int64        `json:"totalSize"`
	ElapsedMs int64        `json:"elapsedMs"`
}

// SystemInfo describes the machine the app runs on.
type SystemInfo struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"goVersion"`
}

// Cleaner is callable from the frontend: `mygo generate` turns its methods
// into typed TypeScript functions in src/mygo.ts.
type Cleaner struct{}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// configPath returns where the category configuration is persisted.
func configPath() string {
	return filepath.Join(homeDir(), "Library", "Application Support", "Cleanzo", "config.json")
}

// defaultCategoryConfigs returns the built-in macOS junk categories.
func defaultCategoryConfigs() []CategoryConfig {
	h := homeDir()
	return []CategoryConfig{
		{Name: "caches", Description: "应用与浏览器缓存", Risk: "safe", Enabled: true, Paths: []string{filepath.Join(h, "Library", "Caches")}},
		{Name: "logs", Description: "系统与应用日志", Risk: "safe", Enabled: true, Paths: []string{filepath.Join(h, "Library", "Logs")}},
		{Name: "temp", Description: "7 天未改动的临时文件", Risk: "moderate", Enabled: true, Paths: []string{os.TempDir()}},
		{Name: "trash", Description: "废纸篓（清空后不可恢复）", Risk: "moderate", Enabled: true, Paths: []string{filepath.Join(h, ".Trash")}},
		{Name: "npm", Description: "npm / pnpm 下载缓存", Risk: "safe", Enabled: true, Paths: []string{filepath.Join(h, ".npm", "_cacache"), filepath.Join(h, "Library", "Caches", "pnpm")}},
		{Name: "go", Description: "Go 构建缓存", Risk: "safe", Enabled: true, Paths: []string{filepath.Join(h, "Library", "Caches", "go-build")}},
		{Name: "pip", Description: "pip 缓存", Risk: "safe", Enabled: true, Paths: []string{filepath.Join(h, "Library", "Caches", "pip")}},
		{Name: "homebrew", Description: "Homebrew 下载缓存", Risk: "safe", Enabled: true, Paths: []string{filepath.Join(h, "Library", "Caches", "Homebrew")}},
		{Name: "xcode", Description: "Xcode 编译缓存（清理后需重新编译）", Risk: "moderate", Enabled: true, Paths: []string{filepath.Join(h, "Library", "Developer", "Xcode", "DerivedData")}},
	}
}

// configVersion is the version of config.json. Version 1 was a bare array of
// categories.
const configVersion = 2

type configFile struct {
	Version    int              `json:"version"`
	Categories []CategoryConfig `json:"categories"`
}

func saveCategoryConfigs(cfg []CategoryConfig) error {
	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(configFile{Version: configVersion, Categories: cfg}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), b, 0o644)
}

// savedCategoryConfigs returns the saved categories, migrated to the current
// version, or nil when nothing usable is saved.
func savedCategoryConfigs() []CategoryConfig {
	b, err := os.ReadFile(configPath())
	if err != nil {
		return nil
	}
	var f configFile
	if json.Unmarshal(b, &f) == nil && f.Version >= configVersion {
		return f.Categories
	}
	var v1 []CategoryConfig
	if json.Unmarshal(b, &v1) != nil || len(v1) == 0 {
		return nil
	}
	cfg := migrateV1(v1)
	_ = saveCategoryConfigs(cfg)
	return cfg
}

// migrateV1 updates the built-in categories that are still as version 1
// shipped them: the temporary folder and the Trash are no longer picked by
// default.
func migrateV1(cfg []CategoryConfig) []CategoryConfig {
	for i, c := range cfg {
		for _, d := range defaultCategoryConfigs() {
			if (d.Name == "temp" || d.Name == "trash") && c.Name == d.Name && samePaths(c.Paths, d.Paths) && c.Risk == "safe" {
				cfg[i].Risk = d.Risk
			}
		}
	}
	return cfg
}

// loadCategories returns the effective categories: the saved config when
// present, otherwise the defaults (which are then saved on first run).
func loadCategories() []Category {
	cfg := savedCategoryConfigs()
	if len(cfg) == 0 {
		cfg = defaultCategoryConfigs()
		if _, err := os.Stat(configPath()); errors.Is(err, fs.ErrNotExist) {
			_ = saveCategoryConfigs(cfg)
		}
	}
	cats := make([]Category, 0, len(cfg))
	for _, c := range cfg {
		if !c.Enabled {
			continue
		}
		cats = append(cats, Category{Name: c.Name, Description: c.Description, Risk: c.Risk, Permanent: builtinPermanent(c), paths: c.Paths})
	}
	return excludeNested(cats)
}

// builtinPermanent reports whether c is a built-in category at its built-in
// paths. Their contents regenerate, so cleaning deletes them; whatever a
// user points a category at goes to the Trash instead.
func builtinPermanent(c CategoryConfig) bool {
	for _, d := range defaultCategoryConfigs() {
		if c.Name == d.Name && samePaths(c.Paths, d.Paths) {
			return true
		}
	}
	return false
}

// samePaths reports whether a and b list the same folders in order.
func samePaths(a, b []string) bool {
	return slices.EqualFunc(a, b, func(x, y string) bool { return filepath.Clean(x) == filepath.Clean(y) })
}

// excludeNested gives each category the paths of the categories nested in
// it, such as go-build inside ~/Library/Caches, so every file belongs to one
// category: it is counted once, and cleaned only with its own.
func excludeNested(cats []Category) []Category {
	for i := range cats {
		cats[i].exclude = nil
		for j := range cats {
			if i == j {
				continue
			}
			for _, inner := range cats[j].paths {
				in := canonical(inner)
				for _, outer := range cats[i].paths {
					if out := canonical(outer); in != out && within(in, out) {
						cats[i].exclude = append(cats[i].exclude, in)
					}
				}
			}
		}
	}
	return cats
}

// canonical returns p absolute, resolved and lower-cased, the form paths are
// compared in.
func canonical(p string) string {
	abs, _ := filepath.Abs(p)
	return strings.ToLower(resolvePath(abs))
}

// scanPath sums the size and count of regular files under p.
func scanPath(p string) (size, files int64) {
	size, files, _ = measure(p)
	return size, files
}

// measure sums the size and count of regular files under p, and finds when
// anything there last changed.
func measure(p string) (size, files int64, newest time.Time) {
	filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if t := info.ModTime(); t.After(newest) {
			newest = t
		}
		if d.Type().IsRegular() {
			size += info.Size()
			files++
		}
		return nil
	})
	return size, files, newest
}

// Inside the home folder, cleaning never reaches these folders themselves
// (their contents are the user's data), nor anything in the subtrees below.
var (
	protectedHomeDirs = []string{
		"/library", "/library/application support", "/library/containers",
		"/library/group containers", "/library/preferences",
		"/desktop", "/documents", "/downloads", "/movies", "/music", "/pictures",
	}
	protectedHomeTrees = []string{
		"/library/keychains", "/library/mobile documents", "/library/mail",
		"/library/messages", "/.ssh", "/.gnupg",
	}
)

// safeToClean guards against cleaning system paths and irreplaceable user
// data. It judges a path by where it really is: links are resolved, and
// paths are compared in lower case because macOS volumes are
// case-insensitive. Only the home folder and the user's temporary folder
// can be cleaned.
func safeToClean(p string) bool {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	path := strings.ToLower(resolvePath(abs))
	home := strings.ToLower(resolvePath(homeDir()))
	if within(path, home) {
		rel := strings.TrimPrefix(path, home)
		if rel == "" || slices.Contains(protectedHomeDirs, rel) {
			return false
		}
		for _, tree := range protectedHomeTrees {
			if within(rel, tree) {
				return false
			}
		}
		// Photos and iPhoto libraries are packages the apps manage themselves.
		for _, part := range strings.Split(rel, "/") {
			if strings.HasSuffix(part, ".photoslibrary") || strings.HasSuffix(part, ".photolibrary") {
				return false
			}
		}
		return true
	}
	return within(path, strings.ToLower(resolvePath(os.TempDir())))
}

// within reports whether path is dir or inside it.
func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

// resolvePath resolves the links of the longest existing prefix of p, an
// absolute path, and appends the rest.
func resolvePath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// usageGuard tells whether something on disk is in use: it holds a file a
// process has open, or it is named after an app that is running, such as
// ~/Library/Caches/com.google.Chrome. Deleting files under a running app
// can break it, so cleaning leaves those alone. When a probe fails the
// guard reports everything as in use, so nothing is cleaned blind.
type usageGuard struct {
	open    []string        // lower-cased paths of open files, sorted
	running map[string]bool // lower-cased bundle IDs of running apps
	failed  bool
}

// probeUsage takes a snapshot of the open files and the running apps.
func probeUsage() *usageGuard {
	open, err := openFiles()
	if err != nil {
		return &usageGuard{failed: true}
	}
	running, err := runningBundleIDs()
	if err != nil {
		return &usageGuard{failed: true}
	}
	return &usageGuard{open: open, running: running}
}

// inUse reports whether path, as resolved by resolvePath, is in use.
func (g *usageGuard) inUse(path string) bool {
	if g.failed {
		return true
	}
	p := strings.ToLower(path)
	if i := sort.SearchStrings(g.open, p); i < len(g.open) && g.open[i] == p {
		return true
	}
	if i := sort.SearchStrings(g.open, p+"/"); i < len(g.open) && strings.HasPrefix(g.open[i], p+"/") {
		return true
	}
	return ownedByRunningApp(filepath.Base(p), g.running)
}

// ownedByRunningApp reports whether name is a running app's bundle ID, or
// starts with one, as helpers do (com.microsoft.VSCode.ShipIt).
func ownedByRunningApp(name string, running map[string]bool) bool {
	name = strings.ToLower(name)
	for {
		if running[name] {
			return true
		}
		i := strings.LastIndexByte(name, '.')
		if i <= 0 {
			return false
		}
		name = name[:i]
	}
}

// openFiles lists the files the user's processes have open, lower-cased and
// sorted, from lsof.
func openFiles() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-w", "-Fn").Output()
	if err != nil {
		return nil, err
	}
	return parseLsofNames(out), nil
}

// parseLsofNames extracts the file paths from lsof -Fn output, whose name
// lines start with "n"; sockets and pipes have names that are not paths.
func parseLsofNames(out []byte) []string {
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n/") {
			paths = append(paths, strings.ToLower(line[1:]))
		}
	}
	sort.Strings(paths)
	return paths
}

// runningBundleIDs returns the lower-cased bundle IDs of the running apps.
func runningBundleIDs() (map[string]bool, error) {
	out, err := exec.Command("/usr/bin/lsappinfo", "list").Output()
	if err != nil {
		return nil, err
	}
	return parseBundleIDs(out), nil
}

var bundleIDRe = regexp.MustCompile(`bundleID="([^"]+)"`)

// parseBundleIDs extracts the bundle IDs from lsappinfo list output.
func parseBundleIDs(out []byte) map[string]bool {
	ids := map[string]bool{}
	for _, m := range bundleIDRe.FindAllSubmatch(out, -1) {
		ids[strings.ToLower(string(m[1]))] = true
	}
	return ids
}

// scanConcurrency bounds how many directory walks run at once during a scan.
// WalkDir is I/O bound; keeping a small fixed pool avoids thrashing the disk.
const scanConcurrency = 8

// tempMinAge is how long an entry of the temporary folder must sit untouched
// before cleaning takes it: apps keep files there while they run.
const tempMinAge = 7 * 24 * time.Hour

// cleanEntry is one item cleaning a category removes.
type cleanEntry struct {
	path        string
	size, files int64
}

// categoryPlan is what cleaning a category would do now.
type categoryPlan struct {
	entries                  []cleanEntry // largest first
	inUse                    int64
	missing, denied, blocked []string
}

// planCategory lists what cleaning cat would remove: the direct children of
// its paths, except the folders of nested categories (it descends into
// their parents instead), entries in use, sockets and other special files,
// and in the temporary folder, entries touched within tempMinAge. Sizing
// walks share sem.
func planCategory(cat Category, g *usageGuard, sem chan struct{}) categoryPlan {
	var plan categoryPlan
	type candidate struct {
		path   string
		minAge time.Duration
	}
	var cands []candidate
	var collect func(dir string, minAge time.Duration)
	collect = func(dir string, minAge time.Duration) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				plan.denied = append(plan.denied, dir)
			}
			return
		}
		for _, e := range entries {
			child := filepath.Join(dir, e.Name())
			key := strings.ToLower(child)
			switch {
			case slices.Contains(cat.exclude, key):
				// another category's folder
			case slices.ContainsFunc(cat.exclude, func(x string) bool { return within(x, key) }):
				collect(child, minAge)
			case e.Type()&(fs.ModeSocket|fs.ModeNamedPipe|fs.ModeDevice|fs.ModeCharDevice|fs.ModeIrregular) != 0:
				// live endpoints, not junk
			case g.inUse(child):
				plan.inUse++
			default:
				cands = append(cands, candidate{child, minAge})
			}
		}
	}
	tmp := canonical(os.TempDir())
	for _, p := range cat.paths {
		if !safeToClean(p) {
			plan.blocked = append(plan.blocked, p)
			continue
		}
		if _, err := os.Lstat(p); err != nil {
			if errors.Is(err, fs.ErrPermission) {
				plan.denied = append(plan.denied, p)
			} else {
				plan.missing = append(plan.missing, p)
			}
			continue
		}
		var minAge time.Duration
		if canonical(p) == tmp {
			minAge = tempMinAge
		}
		abs, _ := filepath.Abs(p)
		collect(resolvePath(abs), minAge)
	}

	measured := make([]cleanEntry, len(cands))
	newest := make([]time.Time, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			size, files, t := measure(c.path)
			measured[i], newest[i] = cleanEntry{c.path, size, files}, t
		}()
	}
	wg.Wait()
	now := time.Now()
	for i, e := range measured {
		if age := cands[i].minAge; age > 0 && now.Sub(newest[i]) < age {
			continue
		}
		plan.entries = append(plan.entries, e)
	}
	sort.Slice(plan.entries, func(i, j int) bool { return plan.entries[i].size > plan.entries[j].size })
	return plan
}

// scanCategories fills in what cleaning each category would remove now.
// Categories are planned concurrently; their walks share one bounded pool.
func scanCategories(cats []Category, g *usageGuard) []Category {
	sem := make(chan struct{}, scanConcurrency)
	var wg sync.WaitGroup
	for i := range cats {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plan := planCategory(cats[i], g, sem)
			c := &cats[i]
			c.Size, c.Files, c.Top = 0, 0, nil
			for _, e := range plan.entries {
				c.Size += e.size
				c.Files += e.files
			}
			for _, e := range plan.entries[:min(5, len(plan.entries))] {
				c.Top = append(c.Top, TopItem{Name: filepath.Base(e.path), Path: e.path, Size: e.size})
			}
			c.InUse, c.Missing, c.Denied, c.Blocked = plan.inUse, plan.missing, plan.denied, plan.blocked
		}()
	}
	wg.Wait()
	return cats
}

// Scan returns every category with what cleaning it would remove now.
func (Cleaner) Scan() []Category {
	return scanCategories(loadCategories(), probeUsage())
}

// userTrashDir is the macOS Trash directory for the current user.
func userTrashDir() string {
	return filepath.Join(homeDir(), ".Trash")
}

// trashItem moves a file or folder to the Trash the way Finder does: macOS
// picks the Trash of its volume and a free name there. Tests replace it.
var trashItem = mygo.Shell.TrashItem

// cleanCategory removes what planCategory lists. Built-in junk and the
// Trash's own contents are deleted; everything else moves to the Trash, so
// it stays recoverable.
func cleanCategory(cat Category, g *usageGuard) CleanResult {
	res := CleanResult{Name: cat.Name}
	plan := planCategory(cat, g, make(chan struct{}, scanConcurrency))
	res.InUse = plan.inUse
	for _, p := range plan.blocked {
		res.Failed = append(res.Failed, p+": blocked")
	}
	for _, p := range plan.denied {
		res.Failed = append(res.Failed, p+": "+fs.ErrPermission.Error())
	}
	trash := canonical(userTrashDir())
	for _, e := range plan.entries {
		permanent := cat.Permanent || within(strings.ToLower(e.path), trash)
		var err error
		if permanent {
			err = os.RemoveAll(e.path)
		} else {
			err = trashItem(e.path)
		}
		if err != nil {
			res.Failed = append(res.Failed, e.path+": "+err.Error())
			continue
		}
		if permanent {
			res.Freed += e.size
		} else {
			res.Trashed += e.size
		}
		res.Files += e.files
	}
	return res
}

// Clean cleans the named categories (all if names is empty). The Trash is
// emptied first, so what the other categories move there stays recoverable.
func (Cleaner) Clean(names []string) CleanSummary {
	selected := make(map[string]bool, len(names))
	for _, n := range names {
		selected[n] = true
	}
	g := probeUsage()
	cats := loadCategories()
	trash := canonical(userTrashDir())
	holdsTrash := func(c Category) bool {
		return slices.ContainsFunc(c.paths, func(p string) bool { return canonical(p) == trash })
	}
	slices.SortStableFunc(cats, func(a, b Category) int {
		switch ta, tb := holdsTrash(a), holdsTrash(b); {
		case ta && !tb:
			return -1
		case tb && !ta:
			return 1
		}
		return 0
	})
	summary := CleanSummary{}
	for _, cat := range cats {
		if len(names) > 0 && !selected[cat.Name] {
			continue
		}
		res := cleanCategory(cat, g)
		summary.Results = append(summary.Results, res)
		summary.TotalFreed += res.Freed
		summary.TotalTrashed += res.Trashed
		summary.InUse += res.InUse
		if len(res.Failed) > 0 {
			summary.Errors = append(summary.Errors, CleanError{Name: cat.Name, Error: strings.Join(res.Failed, "; ")})
		}
	}
	return summary
}

// OpenFullDiskAccessSettings opens Privacy & Security > Full Disk Access in
// System Settings, where the user can let Cleanzo read protected folders
// such as the Trash.
func (Cleaner) OpenFullDiskAccessSettings() error {
	return exec.Command("/usr/bin/open", "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles").Run()
}

// Info returns basic system information.
func (Cleaner) Info() SystemInfo {
	return SystemInfo{OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version()}
}

// ---------------------------------------------------------------------------
// Per-application cache cleanup
// ---------------------------------------------------------------------------

// AppCacheItem is one application's cache footprint under ~/Library/Caches.
type AppCacheItem struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Files int64  `json:"files"`
	InUse bool   `json:"inUse"` // a running app uses it, so it is not cleanable
}

// PathsCleanResult reports what CleanPaths moved to the Trash.
type PathsCleanResult struct {
	Moved  int64    `json:"moved"`
	Freed  int64    `json:"freed"`
	Files  int64    `json:"files"`
	InUse  int64    `json:"inUse"` // paths left alone because they are in use
	Failed []string `json:"failed"`
}

// ScanAppCaches measures every direct subdirectory of ~/Library/Caches (one
// row per application) so the frontend can let the user pick apps to clean.
// Sizing runs concurrently under a bounded semaphore; results are sorted by
// size descending.
func (Cleaner) ScanAppCaches() []AppCacheItem {
	dir := filepath.Join(homeDir(), "Library", "Caches")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	g := probeUsage()
	var (
		mu    sync.Mutex
		items []AppCacheItem
		wg    sync.WaitGroup
		sem   = make(chan struct{}, scanConcurrency)
	)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		child := filepath.Join(dir, name)
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			size, files := scanPath(child)
			mu.Lock()
			items = append(items, AppCacheItem{Name: name, Path: child, Size: size, Files: files, InUse: g.inUse(resolvePath(child))})
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(items, func(i, j int) bool { return items[i].Size > items[j].Size })
	return items
}

// CleanPaths moves the given individual paths (e.g. selected photos) to the
// Trash, honoring the safeToClean gate and leaving alone what is in use. It
// is the fine-grained counterpart to Clean (which works on whole categories).
func (Cleaner) CleanPaths(paths []string) PathsCleanResult {
	g := probeUsage()
	res := PathsCleanResult{}
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			res.Failed = append(res.Failed, p+": "+err.Error())
			continue
		}
		if !safeToClean(abs) {
			res.Failed = append(res.Failed, abs+": blocked")
			continue
		}
		if _, err := os.Lstat(abs); err != nil {
			continue // already gone
		}
		if g.inUse(resolvePath(abs)) {
			res.InUse++
			continue
		}
		size, files := scanPath(abs)
		if err := trashItem(abs); err != nil {
			res.Failed = append(res.Failed, abs+": "+err.Error())
			continue
		}
		res.Moved++
		res.Freed += size
		res.Files += files
	}
	return res
}

// ---------------------------------------------------------------------------
// App uninstall support
// ---------------------------------------------------------------------------

// readBundleInfo extracts display name, bundle id and version from an app's
// Info.plist (XML or binary) using macOS's built-in plutil.
func readBundleInfo(plistPath string) (name, bid, version string) {
	out, err := exec.Command("/usr/bin/plutil", "-convert", "json", "-o", "-", plistPath).Output()
	if err != nil {
		return "", "", ""
	}
	var m map[string]any
	if json.Unmarshal(out, &m) != nil {
		return "", "", ""
	}
	if s, ok := m["CFBundleDisplayName"].(string); ok && s != "" {
		name = s
	} else if s, ok := m["CFBundleName"].(string); ok {
		name = s
	}
	if s, ok := m["CFBundleIdentifier"].(string); ok {
		bid = s
	}
	if s, ok := m["CFBundleShortVersionString"].(string); ok {
		version = s
	}
	return name, bid, version
}

// lastOpened returns the last-used unix time from Spotlight metadata.
func lastOpened(appPath string) int64 {
	out, err := exec.Command("/usr/bin/mdls", "-name", "kMDItemLastUsedDate", "-raw", appPath).Output()
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(out))
	if s == "" || s == "(null)" {
		return 0
	}
	if t, err := time.Parse("2006-01-02 15:04:05 -0700", s); err == nil {
		return t.Unix()
	}
	return 0
}

// listAppsIn enumerates .app bundles directly inside dir.
func listAppsIn(dir string) []AppInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var (
		mu   sync.Mutex
		apps []AppInfo
		wg   sync.WaitGroup
		sem  = make(chan struct{}, 16)
	)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), ".app") {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(e os.DirEntry) {
			defer wg.Done()
			defer func() { <-sem }()
			appPath := filepath.Join(dir, e.Name())
			name, bid, version := readBundleInfo(filepath.Join(appPath, "Contents", "Info.plist"))
			if name == "" {
				name = strings.TrimSuffix(e.Name(), ".app")
			}
			size, _ := scanPath(appPath)
			a := AppInfo{
				Name:       name,
				Path:       appPath,
				BundleID:   bid,
				Version:    version,
				Size:       size,
				LastOpened: lastOpened(appPath),
			}
			mu.Lock()
			apps = append(apps, a)
			mu.Unlock()
		}(e)
	}
	wg.Wait()
	return apps
}

// installedKeys returns the set of bundle ids and app names of installed apps.
func installedKeys() (bids, names map[string]bool) {
	bids = map[string]bool{}
	names = map[string]bool{}
	for _, dir := range []string{"/Applications", filepath.Join(homeDir(), "Applications")} {
		for _, a := range listAppsIn(dir) {
			if a.BundleID != "" {
				bids[a.BundleID] = true
			}
			names[strings.TrimSuffix(filepath.Base(a.Path), ".app")] = true
		}
	}
	return bids, names
}

func (Cleaner) ListApps() []AppInfo {
	seen := map[string]bool{}
	var apps []AppInfo
	for _, dir := range []string{"/Applications", filepath.Join(homeDir(), "Applications")} {
		for _, a := range listAppsIn(dir) {
			key := a.Path
			if a.BundleID != "" {
				key = a.BundleID
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			apps = append(apps, a)
		}
	}
	sort.Slice(apps, func(i, j int) bool { return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name) })
	return apps
}

// appInfoFromPath builds an AppInfo for a single app bundle path.
func appInfoFromPath(appPath string) AppInfo {
	name, bid, version := readBundleInfo(filepath.Join(appPath, "Contents", "Info.plist"))
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(appPath), ".app")
	}
	size, _ := scanPath(appPath)
	return AppInfo{Name: name, Path: appPath, BundleID: bid, Version: version, Size: size, LastOpened: lastOpened(appPath)}
}

// uninstallPaths collects the app bundle plus its user-level associated files.
func uninstallPaths(app AppInfo) []UninstallItem {
	h := homeDir()
	name := strings.TrimSuffix(filepath.Base(app.Path), ".app")
	bid := app.BundleID

	var items []UninstallItem
	add := func(p, kind string) {
		if p == "" {
			return
		}
		if _, err := os.Lstat(p); err != nil {
			return
		}
		size, _ := scanPath(p)
		items = append(items, UninstallItem{Path: p, Kind: kind, Size: size})
	}

	add(app.Path, "app")
	if bid != "" {
		add(filepath.Join(h, "Library", "Preferences", bid+".plist"), "prefs")
		add(filepath.Join(h, "Library", "Preferences", bid), "prefs")
		add(filepath.Join(h, "Library", "Caches", bid), "cache")
		add(filepath.Join(h, "Library", "WebKit", bid), "webkit")
		add(filepath.Join(h, "Library", "HTTPStorages", bid), "http")
		add(filepath.Join(h, "Library", "HTTPStorages", bid+".localstorage"), "http")
		add(filepath.Join(h, "Library", "Containers", bid), "container")
		add(filepath.Join(h, "Library", "Saved Application State", bid+".savedState"), "savedState")
		add(filepath.Join(h, "Library", "Application Scripts", bid), "scripts")
		add(filepath.Join(h, "Library", "LaunchAgents", bid+".plist"), "launchAgent")
		add(filepath.Join(h, "Library", "Logs", bid), "logs")
	}
	add(filepath.Join(h, "Library", "Application Support", name), "support")
	add(filepath.Join(h, "Library", "Caches", name), "cache")
	add(filepath.Join(h, "Library", "Logs", name), "logs")
	add(filepath.Join(h, "Library", "Preferences", name+".plist"), "prefs")
	add(filepath.Join(h, "Library", "Preferences", name), "prefs")
	return items
}

// AppUninstallDetail returns everything that would be removed for an app.
func (Cleaner) AppUninstallDetail(path string) UninstallDetail {
	app := appInfoFromPath(path)
	items := uninstallPaths(app)
	var total int64
	for _, it := range items {
		total += it.Size
	}
	return UninstallDetail{App: app, Items: items, TotalSize: total}
}

// UninstallApp moves an app and its associated files to the Trash.
func (Cleaner) UninstallApp(path string) UninstallResult {
	app := appInfoFromPath(path)
	res := UninstallResult{Path: path}
	for _, item := range uninstallPaths(app) {
		size, _ := scanPath(item.Path)
		if err := trashItem(item.Path); err != nil {
			res.Failed = append(res.Failed, item.Path+": "+err.Error())
			continue
		}
		res.Moved++
		res.Freed += size
	}
	return res
}

// GetCategories returns the current (persisted or default) category config.
func (Cleaner) GetCategories() []CategoryConfig {
	if saved := savedCategoryConfigs(); len(saved) > 0 {
		return saved
	}
	return defaultCategoryConfigs()
}

// SaveCategories validates and persists the category configuration.
func (Cleaner) SaveCategories(cfg []CategoryConfig) error {
	names := map[string]bool{}
	owners := map[string]string{} // canonical folder -> category
	for _, c := range cfg {
		if strings.TrimSpace(c.Name) == "" {
			return fmt.Errorf("分类名称不能为空")
		}
		name := strings.ToLower(strings.TrimSpace(c.Name))
		if names[name] {
			return fmt.Errorf("分类名称重复：%s", c.Name)
		}
		names[name] = true
		if len(c.Paths) == 0 {
			return fmt.Errorf("分类 %s 至少需要一个目录", c.Name)
		}
		for _, p := range c.Paths {
			if strings.TrimSpace(p) == "" {
				return fmt.Errorf("分类 %s 存在空路径", c.Name)
			}
			if !safeToClean(p) {
				return fmt.Errorf("路径 %s 受保护，不能作为清理目录", p)
			}
			if owner, ok := owners[canonical(p)]; ok {
				return fmt.Errorf("目录 %s 重复出现在分类 %s 和 %s 中", p, owner, c.Name)
			}
			owners[canonical(p)] = c.Name
		}
	}
	return saveCategoryConfigs(cfg)
}

// ---------------------------------------------------------------------------
// Full disk scan
// ---------------------------------------------------------------------------

// DiskStat is the capacity usage of the boot volume.
type DiskStat struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"`
}

// BigItem is a large directory discovered by the full scan.
type BigItem struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Trashable bool   `json:"trashable"`
}

// FullScanResult is the outcome of a full disk scan.
type FullScanResult struct {
	Disk    DiskStat  `json:"disk"`
	Items   []BigItem `json:"items"`
	Scanned int64     `json:"scanned"`
	Skipped int64     `json:"skipped"`
	Elapsed int64     `json:"elapsedMs"`
}

// FullScanEvent streams per-directory results and the final summary.
type FullScanEvent struct {
	Kind      string          `json:"kind"` // "item" | "done"
	Path      string          `json:"path,omitempty"`
	Size      int64           `json:"size,omitempty"`
	Trashable bool            `json:"trashable,omitempty"`
	Scanned   int64           `json:"scanned,omitempty"`
	Result    *FullScanResult `json:"result,omitempty"`
}

// TrashResult reports what TrashPath moved to the Trash.
type TrashResult struct {
	Moved int64 `json:"moved"`
	Freed int64 `json:"freed"`
}

// diskStat returns the capacity of the boot volume.
func diskStat() DiskStat {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err != nil {
		return DiskStat{}
	}
	total := int64(st.Bsize) * int64(st.Blocks)
	free := int64(st.Bsize) * int64(st.Bavail)
	return DiskStat{Total: total, Used: total - free, Free: free}
}

// fullScanTargets returns the directories the full scan enumerates:
// the user's home with ~/Library expanded one level, plus system-level roots.
func fullScanTargets() []string {
	h := homeDir()
	var out []string
	entries, err := os.ReadDir(h)
	if err == nil {
		for _, e := range entries {
			name := e.Name()
			if name == ".Trash" {
				continue
			}
			p := filepath.Join(h, name)
			if name == "Library" {
				if lib, err := os.ReadDir(p); err == nil {
					for _, le := range lib {
						out = append(out, filepath.Join(p, le.Name()))
					}
				}
				continue
			}
			out = append(out, p)
		}
	}
	for _, root := range []string{"/Library", "/usr/local", "/opt", "/private/var/tmp", "/private/var/log"} {
		subs, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range subs {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}

// trashable reports whether path may be moved to the Trash: it must be a
// directory inside the user's home, excluding the home itself, ~/Library and
// the Trash.
func trashable(path string) bool {
	h := homeDir()
	if path == h || path == filepath.Join(h, "Library") || path == filepath.Join(h, ".Trash") {
		return false
	}
	return strings.HasPrefix(path, h+string(filepath.Separator))
}

// duSize returns the disk usage of path in bytes using /usr/bin/du.
// du is a C implementation and is several times faster than a Go walker on
// large trees. A timeout guards against pathological directories.
func duSize(path string, timeout time.Duration) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/du", "-sk", path).Output()
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty du output for %s", path)
	}
	kb, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	return kb * 1024, nil
}

// DirItem is a single child entry inside a directory, returned by DirChildren
// so the frontend can expand a row in place and show what it actually contains.
type DirItem struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	IsDir     bool   `json:"isDir"`
	Trashable bool   `json:"trashable"`
}

// DirChildren lists the direct children of path with sizes: directories are
// measured with du (8s timeout), files with a quick stat. Unreadable or
// timed-out entries report Size = -1 so the UI can show them as unknown.
// The result is sorted by size descending and capped at 60 entries.
func (Cleaner) DirChildren(path string) ([]DirItem, error) {
	ents, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	type job struct {
		name  string
		full  string
		isDir bool
	}
	jobs := make([]job, 0, len(ents))
	for _, e := range ents {
		full := filepath.Join(path, e.Name())
		jobs = append(jobs, job{e.Name(), full, e.IsDir()})
	}
	var mu sync.Mutex
	items := make([]DirItem, 0, len(jobs))
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			size := int64(-1)
			if j.isDir {
				if sz, err := duSize(j.full, 8*time.Second); err == nil {
					size = sz
				}
			} else if st, err := os.Stat(j.full); err == nil {
				size = st.Size()
			}
			mu.Lock()
			items = append(items, DirItem{Name: j.name, Path: j.full, Size: size, IsDir: j.isDir, Trashable: trashable(j.full)})
			mu.Unlock()
		}(j)
	}
	wg.Wait()
	sort.Slice(items, func(i, k int) bool { return items[i].Size > items[k].Size })
	if len(items) > 60 {
		items = items[:60]
	}
	return items, nil
}

// collectFullScan measures every full-scan target concurrently with du and
// invokes onItem for each completed target so the frontend can stream the
// ranking as it is discovered. It returns the sorted top items, the number of
// measured targets and the number of failed/skipped ones.
func collectFullScan(onItem func(path string, size int64, trashable bool)) ([]BigItem, int64, int64) {
	targets := fullScanTargets()
	items := make([]BigItem, 0, len(targets))
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 16)
	)
	var done, skipped int64
	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(target string) {
			defer wg.Done()
			defer func() { <-sem }()
			size, err := duSize(target, 120*time.Second)
			if err != nil {
				atomic.AddInt64(&skipped, 1)
				return
			}
			atomic.AddInt64(&done, 1)
			tr := trashable(target)
			it := BigItem{Path: target, Size: size, Trashable: tr}
			mu.Lock()
			items = append(items, it)
			mu.Unlock()
			if onItem != nil {
				onItem(target, size, tr)
			}
		}(t)
	}
	wg.Wait()
	sort.Slice(items, func(i, j int) bool { return items[i].Size > items[j].Size })
	if len(items) > 30 {
		items = items[:30]
	}
	return items, done, skipped
}

// FullScan streams per-directory results and the final summary of a full scan.
func (Cleaner) FullScan(ch *mygo.Channel[FullScanEvent]) error {
	start := time.Now()
	items, scanned, skipped := collectFullScan(func(path string, size int64, trashable bool) {
		_ = ch.Send(FullScanEvent{Kind: "item", Path: path, Size: size, Trashable: trashable})
	})
	res := FullScanResult{
		Disk:    diskStat(),
		Items:   items,
		Scanned: scanned,
		Skipped: skipped,
		Elapsed: time.Since(start).Milliseconds(),
	}
	return ch.Send(FullScanEvent{Kind: "done", Result: &res})
}

// largeFileThreshold is the minimum size for the large-file scan.
const largeFileThreshold int64 = 1 << 30 // 1GB

// findLargeFiles lists regular files under root that are larger than
// threshold, using BSD find (-size supports k/M/G suffixes on macOS).
func findLargeFiles(root string, threshold int64) ([]LargeFile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	kb := threshold / 1024
	out, err := exec.CommandContext(ctx, "/usr/bin/find", root, "-type", "f", "-size", fmt.Sprintf("+%dk", kb), "-print0").Output()
	if err != nil {
		return nil, err
	}
	var files []LargeFile
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if fi.Size() < threshold {
			continue
		}
		files = append(files, LargeFile{Path: p, Size: fi.Size(), Trashable: trashable(p)})
	}
	return files, nil
}

// collectLargeScan scans every full-scan target for large files concurrently
// and streams each discovered file through onItem.
func collectLargeScan(threshold int64, onItem func(f LargeFile)) ([]LargeFile, int64, int64) {
	targets := fullScanTargets()
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 16)
		all []LargeFile
	)
	var done, skipped int64
	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(target string) {
			defer wg.Done()
			defer func() { <-sem }()
			files, err := findLargeFiles(target, threshold)
			if err != nil {
				atomic.AddInt64(&skipped, 1)
				return
			}
			atomic.AddInt64(&done, 1)
			mu.Lock()
			all = append(all, files...)
			mu.Unlock()
			if onItem != nil {
				for _, f := range files {
					onItem(f)
				}
			}
		}(t)
	}
	wg.Wait()
	sort.Slice(all, func(i, j int) bool { return all[i].Size > all[j].Size })
	if len(all) > 50 {
		all = all[:50]
	}
	return all, done, skipped
}

// LargeScan streams per-file results and the final summary of a large-file
// scan across the same targets as the full scan.
func (Cleaner) LargeScan(ch *mygo.Channel[LargeScanEvent]) error {
	start := time.Now()
	items, scanned, skipped := collectLargeScan(largeFileThreshold, func(f LargeFile) {
		_ = ch.Send(LargeScanEvent{Kind: "item", Path: f.Path, Size: f.Size, Trashable: f.Trashable})
	})
	res := LargeScanResult{
		Items:     items,
		Scanned:   scanned,
		Skipped:   skipped,
		ElapsedMs: time.Since(start).Milliseconds(),
		Threshold: largeFileThreshold,
	}
	return ch.Send(LargeScanEvent{Kind: "done", Result: &res})
}

// libraryRoots are the ~/Library subdirectories inspected for leftovers.
var libraryRoots = []string{"Application Support", "Caches", "Logs", "Preferences", "Containers", "WebKit", "HTTPStorages", "Saved Application State", "Application Scripts", "LaunchAgents"}

// orphanKind maps a library root to the shared kind key used by the frontend.
func orphanKind(root string) string {
	switch root {
	case "Application Support":
		return "support"
	case "Caches":
		return "cache"
	case "Logs":
		return "logs"
	case "Preferences":
		return "prefs"
	case "Containers":
		return "container"
	case "WebKit":
		return "webkit"
	case "HTTPStorages":
		return "http"
	case "Saved Application State":
		return "savedState"
	case "Application Scripts":
		return "scripts"
	case "LaunchAgents":
		return "launchAgent"
	}
	return root
}

// toolCacheNames are well-known build/tool caches under ~/Library/Caches that
// are not tied to any single app and must never be listed as leftovers.
var toolCacheNames = map[string]bool{
	"go-build": true, "Homebrew": true, "pip": true, "bun": true, "yarn": true,
	"npm": true, "node-gyp": true, "ms-playwright": true, "electron-builder": true,
	"GeoServices": true, "OTFileCache": true, "CEF": true, "pnpm": true,
	"com.apple.bnns": true, "com.apple.helpd": true, "com.apple.metal": true,
	"com.apple.Safari": true, "com.apple.dt.Xcode": true,
}

// orphanMatch reports whether an orphan candidate is actually owned by an
// installed app. It matches exact names/bundle ids first, then falls back to
// substring matching between the candidate and the installed app name.
func orphanMatch(base string, bids, names map[string]bool, namesList []string) bool {
	if bids[base] || names[base] {
		return true
	}
	lb := strings.ToLower(base)
	for _, n := range namesList {
		ln := strings.ToLower(n)
		if len(n) >= 4 && (strings.Contains(lb, ln) || strings.Contains(ln, lb)) {
			return true
		}
	}
	return false
}

// collectOrphans inspects ~/Library subdirectories one level deep and reports
// entries that do not correspond to an installed app. Orphan directories are
// measured concurrently with du (60s each); plain files use stat. Each
// completed measurement is streamed through onItem.
func collectOrphans(onItem func(o OrphanItem)) ([]OrphanItem, int64) {
	h := homeDir()
	bids, names := installedKeys()
	var namesList []string
	for n := range names {
		namesList = append(namesList, n)
	}
	type candidate struct {
		path  string
		kind  string
		isDir bool
	}
	var cands []candidate
	for _, root := range libraryRoots {
		dir := filepath.Join(h, "Library", root)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			base := name
			if root == "Preferences" || root == "LaunchAgents" {
				base = strings.TrimSuffix(name, ".plist")
			}
			if toolCacheNames[base] {
				continue // build/tool cache, not app leftovers
			}
			if strings.HasPrefix(base, "com.apple.") {
				continue // system data
			}
			if orphanMatch(base, bids, names, namesList) {
				continue // owned by an installed app
			}
			cands = append(cands, candidate{path: filepath.Join(dir, name), kind: orphanKind(root), isDir: e.IsDir()})
		}
	}
	items := make([]OrphanItem, 0, len(cands))
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 16)
	)
	for _, c := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func(c candidate) {
			defer wg.Done()
			defer func() { <-sem }()
			var size int64
			if c.isDir {
				size, _ = duSize(c.path, 60*time.Second)
			} else if fi, err := os.Stat(c.path); err == nil {
				size = fi.Size()
			}
			if size < 1<<20 {
				return // ignore trivial leftovers
			}
			it := OrphanItem{Path: c.path, Size: size, Kind: c.kind, Trashable: trashable(c.path)}
			mu.Lock()
			items = append(items, it)
			mu.Unlock()
			if onItem != nil {
				onItem(it)
			}
		}(c)
	}
	wg.Wait()
	sort.Slice(items, func(i, j int) bool { return items[i].Size > items[j].Size })
	var total int64
	for _, it := range items {
		total += it.Size
	}
	return items, total
}

// ScanOrphans streams leftover files from apps that are no longer installed.
func (Cleaner) ScanOrphans(ch *mygo.Channel[OrphanItem]) error {
	start := time.Now()
	_, total := collectOrphans(func(o OrphanItem) {
		_ = ch.Send(o)
	})
	// send a terminal sentinel so the frontend knows the scan finished
	_ = ch.Send(OrphanItem{Kind: "__done", Size: total, Path: fmt.Sprintf("%d", time.Since(start).Milliseconds())})
	return nil
}

// TrashPath moves a directory (inside the user's home) to the Trash.
func (Cleaner) TrashPath(path string) (TrashResult, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return TrashResult{}, err
	}
	if !trashable(abs) {
		return TrashResult{}, fmt.Errorf("路径受保护，不能移到废纸篓")
	}
	if _, err := os.Lstat(abs); err != nil {
		return TrashResult{}, fmt.Errorf("路径不存在")
	}
	if probeUsage().inUse(resolvePath(abs)) {
		return TrashResult{}, fmt.Errorf("正被运行中的应用使用，请先退出该应用")
	}
	freed, _ := scanPath(abs)
	if err := trashItem(abs); err != nil {
		return TrashResult{}, err
	}
	return TrashResult{Moved: 1, Freed: freed}, nil
}

// RevealInFinder opens path in Finder. Directories open in place; files are
// revealed (selected) in their parent folder. The call returns an error when
// the path does not exist.
func (Cleaner) RevealInFinder(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("路径不存在")
	}
	args := []string{}
	if fi.IsDir() {
		args = append(args, abs)
	} else {
		args = append(args, "-R", abs)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/open", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("open failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Similar photo cleanup (MVP)
// ---------------------------------------------------------------------------

// photoExts are the image formats the similar-photo scan understands. JPEG,
// PNG and GIF decode via the standard library; HEIC/HEIF are transcoded by
// macOS's /usr/bin/sips first.
var photoExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".heic": true, ".heif": true,
}

// similarPhotoCap bounds how many files the discovery step reads, so a huge
// ~/Pictures never blocks the app indefinitely.
const similarPhotoCap = 2000

// similarHashThreshold is the max hamming distance (out of 64 bits) for two
// photos to be considered near-identical, applied only within a fully
// consistent cluster (complete linkage). Tuned for near-duplicates, similar
// screenshots and burst shots, not semantic similarity.
const similarHashThreshold = 6

// thumbMaxEdge is the longest side of the generated preview thumbnail.
const thumbMaxEdge = 200

// minPhotoDim skips tiny placeholder / 1x1 images that are not real photos.
const minPhotoDim = 64

// PhotoItem is one image discovered by the similar-photo scan.
type PhotoItem struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Thumb  string `json:"thumb"` // data: URL JPEG preview
	Keep   bool   `json:"keep"`  // recommended to keep (best copy in the cluster)
}

// PhotoCluster groups visually near-identical photos together.
type PhotoCluster struct {
	ID        int         `json:"id"`
	Kind      string      `json:"kind"` // "exact" (byte-identical) | "similar"
	Items     []PhotoItem `json:"items"`
	TotalSize int64       `json:"totalSize"`
	Waste     int64       `json:"waste"` // space freed if the non-keep copies are trashed
}

// SimilarPhotosResult is the outcome of a similar-photo scan.
type SimilarPhotosResult struct {
	Clusters  []PhotoCluster `json:"clusters"`
	Scanned   int            `json:"scanned"`
	ElapsedMs int64          `json:"elapsedMs"`
}

// SimilarPhotoEvent streams scan progress and the final result.
type SimilarPhotoEvent struct {
	Kind      string               `json:"kind"` // "progress" | "done"
	Processed int                  `json:"processed,omitempty"`
	Total     int                  `json:"total,omitempty"`
	Result    *SimilarPhotosResult `json:"result,omitempty"`
}

// loadPhotoImage decodes an image into memory, transcoding HEIC via sips into
// a temporary JPEG first. The returned cleanup removes any temp file and must
// be run by the caller.
func loadPhotoImage(path string) (image.Image, image.Config, func(), error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".heic" || ext == ".heif" {
		tmp, err := os.CreateTemp("", "cleanzo-heic-*.jpg")
		if err != nil {
			return nil, image.Config{}, nil, err
		}
		tmp.Close()
		cleanup := func() { os.Remove(tmp.Name()) }
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/usr/bin/sips", "-s", "format", "jpeg", "--out", tmp.Name(), path).CombinedOutput()
		if err != nil {
			cleanup()
			return nil, image.Config{}, nil, fmt.Errorf("sips: %v: %s", err, strings.TrimSpace(string(out)))
		}
		img, cfg, close, err := decodeImageFile(tmp.Name())
		if err != nil {
			cleanup()
			return nil, image.Config{}, nil, err
		}
		return img, cfg, func() { close(); cleanup() }, nil
	}
	img, cfg, close, err := decodeImageFile(path)
	if err != nil {
		return nil, image.Config{}, nil, err
	}
	return img, cfg, close, nil
}

// decodeImageFile opens a registered-format image (jpeg/png/gif) and decodes
// both its config and full image, returning a cleanup that closes the handle.
func decodeImageFile(path string) (image.Image, image.Config, func(), error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, image.Config{}, nil, err
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		f.Close()
		return nil, image.Config{}, nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		f.Close()
		return nil, image.Config{}, nil, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		f.Close()
		return nil, image.Config{}, nil, err
	}
	return img, cfg, func() { f.Close() }, nil
}

// dHashImage computes a 64-bit difference hash by sampling the image on a 9x8
// grid of luminance values and recording whether each sample is brighter than
// the one to its right. Nearest-neighbor sampling avoids resampling deps.
func dHashImage(img image.Image, cfg image.Config) uint64 {
	const gw, gh = 9, 8
	var lum [gh][gw]float64
	for y := 0; y < gh; y++ {
		for x := 0; x < gw; x++ {
			sx := x * (cfg.Width - 1) / (gw - 1)
			sy := y * (cfg.Height - 1) / (gh - 1)
			r, g, b, _ := img.At(sx, sy).RGBA()
			lum[y][x] = (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 65535.0
		}
	}
	var hash uint64
	for y := 0; y < gh; y++ {
		for x := 0; x < gw-1; x++ {
			hash <<= 1
			if lum[y][x] < lum[y][x+1] {
				hash |= 1
			}
		}
	}
	return hash
}

// resizeMaxEdge returns a nearest-neighbor downscale of src whose longest edge
// is at most maxEdge.
func resizeMaxEdge(src image.Image, maxEdge int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return src
	}
	if w <= maxEdge && h <= maxEdge {
		return src
	}
	scale := float64(maxEdge) / math.Max(float64(w), float64(h))
	nw := max(int(round(float64(w)*scale)), 1)
	nh := max(int(round(float64(h)*scale)), 1)
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + y*h/nh
		for x := 0; x < nw; x++ {
			sx := b.Min.X + x*w/nw
			out.Set(x, y, src.At(sx, sy))
		}
	}
	return out
}

func round(v float64) float64 { return math.Floor(v + 0.5) }
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// thumbDataURL renders a JPEG data URL preview (longest edge thumbMaxEdge).
func thumbDataURL(img image.Image) string {
	thumb := resizeMaxEdge(img, thumbMaxEdge)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 55}); err != nil {
		return ""
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// fileSHA1 returns the hex sha1 of a file's contents.
func fileSHA1(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// collectPhotoFiles walks the given roots recursively and collects known image
// files, stopping once cap files are found.
func collectPhotoFiles(roots []string, cap int) []string {
	var out []string
	stop := errors.New("stop")
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil
			}
			if photoExts[strings.ToLower(filepath.Ext(p))] {
				out = append(out, p)
				if len(out) >= cap {
					return stop
				}
			}
			return nil
		})
		if len(out) >= cap {
			break
		}
	}
	return out
}

// clusterIdx is an internal list of decoded-photo indices forming one cluster.
type clusterIdx struct {
	kind string
	idxs []int
}

// decodeRow is one concurrently-processed photo.
type decodeRow struct {
	item PhotoItem
	hash uint64
	sha  string
	size int64
	mod  time.Time
}

// clusterPhotosComplete groups photos by perceptual hash using complete
// linkage: a photo joins a cluster only when its hash is within
// similarHashThreshold of EVERY member already in the cluster. This prevents
// A≈B≈C chains where A and C are actually unrelated.
func clusterPhotosComplete(idxs []int, hashes map[int]uint64) [][]int {
	var clusters [][]int
	for _, i := range idxs {
		h := hashes[i]
		placed := false
		for ci := range clusters {
			ok := true
			for _, m := range clusters[ci] {
				if bits.OnesCount64(h^hashes[m]) > similarHashThreshold {
					ok = false
					break
				}
			}
			if ok {
				clusters[ci] = append(clusters[ci], i)
				placed = true
				break
			}
		}
		if !placed {
			clusters = append(clusters, []int{i})
		}
	}
	out := [][]int{}
	for _, c := range clusters {
		if len(c) >= 2 {
			out = append(out, c)
		}
	}
	return out
}

// ScanSimilarPhotos streams progress and finishes with near-duplicate photo
// clusters. Exact byte-identical copies are grouped first, then remaining photos
// are grouped by perceptual hash. Each cluster marks its recommended keep copy.
func (Cleaner) ScanSimilarPhotos(ch *mygo.Channel[SimilarPhotoEvent]) error {
	h := homeDir()
	roots := []string{
		filepath.Join(h, "Pictures"),
		filepath.Join(h, "Desktop"),
	}
	res := buildSimilarPhotos(roots, func(done, total int) {
		_ = ch.Send(SimilarPhotoEvent{Kind: "progress", Processed: done, Total: total})
	})
	return ch.Send(SimilarPhotoEvent{Kind: "done", Result: &res})
}

// buildSimilarPhotos is the testable core of ScanSimilarPhotos: it discovers,
// decodes, hashes, thumbnails and clusters photos under roots, calling
// onProgress after each file.
func buildSimilarPhotos(roots []string, onProgress func(done, total int)) SimilarPhotosResult {
	start := time.Now()
	files := collectPhotoFiles(roots, similarPhotoCap)
	total := len(files)

	var (
		mu        sync.Mutex
		rows      []decodeRow
		processed int64
		wg        sync.WaitGroup
		sem       = make(chan struct{}, scanConcurrency)
	)
	for _, p := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				n := atomic.AddInt64(&processed, 1)
				if onProgress != nil {
					onProgress(int(n), total)
				}
			}()
			fi, err := os.Stat(p)
			if err != nil {
				return
			}
			img, cfg, cleanup, err := loadPhotoImage(p)
			if err != nil {
				return
			}
			defer cleanup()
			if cfg.Width < minPhotoDim || cfg.Height < minPhotoDim {
				return // tiny placeholder / 1x1
			}
			hash := dHashImage(img, cfg)
			thumb := thumbDataURL(img)
			sha, _ := fileSHA1(p)
			mu.Lock()
			rows = append(rows, decodeRow{
				item: PhotoItem{Path: p, Size: fi.Size(), Width: cfg.Width, Height: cfg.Height, Thumb: thumb},
				hash: hash, sha: sha, size: fi.Size(), mod: fi.ModTime(),
			})
			mu.Unlock()
		}(p)
	}
	wg.Wait()

	// 1) Exact duplicates: same size + same sha1.
	type exactKey struct {
		size int64
		sha  string
	}
	byExact := map[exactKey][]int{}
	for i := range rows {
		k := exactKey{rows[i].size, rows[i].sha}
		byExact[k] = append(byExact[k], i)
	}
	used := map[int]bool{}
	var builders []clusterIdx
	for _, idxs := range byExact {
		if len(idxs) < 2 {
			continue
		}
		for _, i := range idxs {
			used[i] = true
		}
		builders = append(builders, clusterIdx{kind: "exact", idxs: idxs})
	}

	// 2) Perceptual clusters among the remaining files.
	hashMap := map[int]uint64{}
	var rest []int
	for i := range rows {
		if used[i] {
			continue
		}
		hashMap[i] = rows[i].hash
		rest = append(rest, i)
	}
	for _, c := range clusterPhotosComplete(rest, hashMap) {
		builders = append(builders, clusterIdx{kind: "similar", idxs: c})
	}

	// 3) Materialize clusters, pick the recommended keep copy, compute waste.
	clusters := make([]PhotoCluster, 0, len(builders))
	for _, b := range builders {
		best := 0
		bestArea := -1
		for k, di := range b.idxs {
			it := rows[di].item
			area := it.Width * it.Height
			bi := b.idxs[best]
			better := area > bestArea ||
				(area == bestArea && it.Size > rows[bi].item.Size) ||
				(area == bestArea && it.Size == rows[bi].item.Size && rows[di].mod.After(rows[bi].mod))
			if better {
				best = k
				bestArea = area
			}
		}
		cl := PhotoCluster{Kind: b.kind}
		for k, di := range b.idxs {
			it := rows[di].item
			it.Keep = k == best
			cl.Items = append(cl.Items, it)
			cl.TotalSize += it.Size
		}
		cl.Waste = cl.TotalSize - cl.Items[best].Size
		clusters = append(clusters, cl)
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Waste > clusters[j].Waste })
	for i := range clusters {
		clusters[i].ID = i + 1
	}

	return SimilarPhotosResult{
		Clusters:  clusters,
		Scanned:   total,
		ElapsedMs: time.Since(start).Milliseconds(),
	}
}

func main() {
	mygo.Bind(Cleaner{})

	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:           "Cleanzo",
			Width:           1000,
			Height:          700,
			MinWidth:        560,
			MinHeight:       420,
			BackgroundColor: "light-dark(#f6f7f9, #0f1115)", // the page's --bg in src/style.css
			StateKey:        "main",
			URL:             "/",
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
