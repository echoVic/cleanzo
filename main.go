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
	"io"
	"io/fs"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"log"
	"math"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Risk        string    `json:"risk"` // "safe" or "moderate"
	Size        int64     `json:"size"`
	Files       int64     `json:"files"`
	Missing     []string  `json:"missing"`
	Top         []TopItem `json:"top"`
	paths       []string
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

// CleanResult reports how much a clean pass freed.
type CleanResult struct {
	Name   string   `json:"name"`
	Freed  int64    `json:"freed"`
	Files  int64    `json:"files"`
	Failed []string `json:"failed"`
}

// CleanError reports a category whose clean pass had failures.
type CleanError struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// CleanSummary aggregates the results of a clean pass.
type CleanSummary struct {
	Results    []CleanResult `json:"results"`
	TotalFreed int64         `json:"totalFreed"`
	Errors     []CleanError  `json:"errors"`
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
		{Name: "temp", Description: "用户临时文件", Risk: "safe", Enabled: true, Paths: []string{os.TempDir()}},
		{Name: "trash", Description: "废纸篓", Risk: "safe", Enabled: true, Paths: []string{filepath.Join(h, ".Trash")}},
		{Name: "npm", Description: "npm / pnpm 缓存", Risk: "safe", Enabled: true, Paths: []string{filepath.Join(h, ".npm", "_cacache"), filepath.Join(h, "Library", "Caches", "pnpm")}},
		{Name: "go", Description: "Go 构建缓存", Risk: "safe", Enabled: true, Paths: []string{filepath.Join(h, "Library", "Caches", "go-build")}},
		{Name: "pip", Description: "pip 缓存", Risk: "safe", Enabled: true, Paths: []string{filepath.Join(h, "Library", "Caches", "pip")}},
		{Name: "homebrew", Description: "Homebrew 下载缓存", Risk: "safe", Enabled: true, Paths: []string{filepath.Join(h, "Library", "Caches", "Homebrew")}},
		{Name: "xcode", Description: "Xcode 编译缓存（清理后需重新编译）", Risk: "moderate", Enabled: true, Paths: []string{filepath.Join(h, "Library", "Developer", "Xcode", "DerivedData")}},
	}
}

func saveCategoryConfigs(cfg []CategoryConfig) error {
	dir := filepath.Dir(configPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), b, 0o644)
}

// loadCategories returns the effective categories: the persisted config when
// present, otherwise the defaults (which are then persisted on first run).
func loadCategories() []Category {
	cfg := defaultCategoryConfigs()
	if b, err := os.ReadFile(configPath()); err == nil {
		var saved []CategoryConfig
		if json.Unmarshal(b, &saved) == nil && len(saved) > 0 {
			cfg = saved
		}
	} else {
		_ = saveCategoryConfigs(cfg)
	}
	cats := make([]Category, 0, len(cfg))
	for _, c := range cfg {
		if !c.Enabled {
			continue
		}
		cats = append(cats, Category{Name: c.Name, Description: c.Description, Risk: c.Risk, paths: c.Paths})
	}
	return cats
}

// scanPath sums the size and count of regular files under p.
func scanPath(p string) (size, files int64) {
	filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || !d.Type().IsRegular() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			size += info.Size()
			files++
		}
		return nil
	})
	return size, files
}

// safeToClean guards against cleaning critical system paths.
func safeToClean(p string) bool {
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	h := homeDir()
	// Never allow cleaning the home dir or the user's Library itself.
	if abs == h || abs == filepath.Join(h, "Library") {
		return false
	}
	// Paths inside the user's home (other than ~/Library itself) are fine.
	if strings.HasPrefix(abs, h+string(filepath.Separator)) {
		return true
	}
	// Outside home: block critical system roots, exact or by prefix,
	// and other users' homes.
	for _, r := range []string{"/", "/System", "/Library", "/Applications", "/Volumes", "/private", "/Users"} {
		if abs == r {
			return false
		}
	}
	for _, r := range []string{"/System/", "/Library/", "/Applications/", "/Volumes/", "/private/", "/Users/"} {
		if strings.HasPrefix(abs, r) {
			return false
		}
	}
	return true
}

// scanConcurrency bounds how many directory walks run at once during a scan.
// WalkDir is I/O bound; keeping a small fixed pool avoids thrashing the disk.
const scanConcurrency = 8

// topItems returns the largest direct children of a category's paths. The
// per-child sizing runs concurrently (bounded by scanConcurrency); the
// result is still sorted by size descending and capped at 5, so the reported
// "top" is identical to the sequential version.
func topItems(cat Category) []TopItem {
	var (
		mu  sync.Mutex
		tops []TopItem
		wg  sync.WaitGroup
		sem = make(chan struct{}, scanConcurrency)
	)
	for _, p := range cat.paths {
		entries, err := os.ReadDir(p)
		if err != nil {
			continue
		}
		for _, e := range entries {
			child := filepath.Join(p, e.Name())
			wg.Add(1)
			sem <- struct{}{}
			go func(name, child string) {
				defer wg.Done()
				defer func() { <-sem }()
				size, _ := scanPath(child)
				mu.Lock()
				tops = append(tops, TopItem{Name: name, Path: child, Size: size})
				mu.Unlock()
			}(e.Name(), child)
		}
	}
	wg.Wait()
	sort.Slice(tops, func(i, j int) bool { return tops[i].Size > tops[j].Size })
	if len(tops) > 5 {
		tops = tops[:5]
	}
	return tops
}

// scanCategories fills in Size/Files/Missing/Top for every category. Each
// (category, path) walk runs concurrently under a bounded semaphore; results
// are accumulated behind a mutex. The per-category totals and the top list are
// numerically identical to the sequential scan, just much faster.
func scanCategories(cats []Category) []Category {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, scanConcurrency)
	)
	for i := range cats {
		for _, p := range cats[i].paths {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, p string) {
				defer wg.Done()
				defer func() { <-sem }()
				if _, err := os.Lstat(p); err != nil {
					mu.Lock()
					cats[i].Missing = append(cats[i].Missing, p)
					mu.Unlock()
					return
				}
				size, files := scanPath(p)
				mu.Lock()
				cats[i].Size += size
				cats[i].Files += files
				mu.Unlock()
			}(i, p)
		}
	}
	wg.Wait()
	for i := range cats {
		cats[i].Top = topItems(cats[i])
	}
	return cats
}

// Scan returns every category with its current footprint.
func (Cleaner) Scan() []Category {
	return scanCategories(loadCategories())
}

// userTrashDir is the macOS Trash directory for the current user. Files moved
// here are recoverable until the user empties the Trash.
func userTrashDir() string {
	return filepath.Join(homeDir(), ".Trash")
}

// cleanCategory moves the contents of a category's paths into the Trash so they
// stay recoverable (they are NOT permanently deleted). The Trash directory
// itself (the built-in "trash" category) is emptied permanently, because its
// contents are already in the Trash and emptying it is what actually frees
// space. The safeToClean gate and the per-category failure bookkeeping are
// unchanged.
func cleanCategory(cat Category) CleanResult {
	res := CleanResult{Name: cat.Name}
	trash := userTrashDir()
	_ = os.MkdirAll(trash, 0o755)
	for _, p := range cat.paths {
		if !safeToClean(p) {
			res.Failed = append(res.Failed, p+": blocked")
			continue
		}
		if _, err := os.Lstat(p); err != nil {
			continue // already missing
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			res.Failed = append(res.Failed, p+": "+err.Error())
			continue
		}
		// Cleaning the Trash itself means emptying it: free the space now.
		emptyTrash := p == trash
		for _, e := range entries {
			child := filepath.Join(p, e.Name())
			size, files := scanPath(child)
			var moveErr error
			if emptyTrash {
				moveErr = os.RemoveAll(child)
			} else {
				_, moveErr = moveToTrash(child, trash)
			}
			if moveErr != nil {
				res.Failed = append(res.Failed, child+": "+moveErr.Error())
				continue
			}
			res.Freed += size
			res.Files += files
		}
	}
	return res
}

// Clean deletes the contents of the named categories (all if names is empty).
func (Cleaner) Clean(names []string) CleanSummary {
	selected := make(map[string]bool, len(names))
	for _, n := range names {
		selected[n] = true
	}
	summary := CleanSummary{}
	for _, cat := range loadCategories() {
		if len(names) > 0 && !selected[cat.Name] {
			continue
		}
		res := cleanCategory(cat)
		summary.Results = append(summary.Results, res)
		summary.TotalFreed += res.Freed
		if len(res.Failed) > 0 {
			summary.Errors = append(summary.Errors, CleanError{Name: cat.Name, Error: strings.Join(res.Failed, "; ")})
		}
	}
	return summary
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
}

// PathsCleanResult reports what CleanPaths moved to the Trash.
type PathsCleanResult struct {
	Moved  int64    `json:"moved"`
	Freed  int64    `json:"freed"`
	Files  int64    `json:"files"`
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
			items = append(items, AppCacheItem{Name: name, Path: child, Size: size, Files: files})
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(items, func(i, j int) bool { return items[i].Size > items[j].Size })
	return items
}

// CleanPaths moves the given individual paths (e.g. one app's cache folder,
// or selected photos) into the Trash, honoring the safeToClean gate. It is
// the fine-grained counterpart to Clean (which works on whole categories).
func (Cleaner) CleanPaths(paths []string) PathsCleanResult {
	trash := userTrashDir()
	_ = os.MkdirAll(trash, 0o755)
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
		size, files := scanPath(abs)
		if _, err := moveToTrash(abs, trash); err != nil {
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

// moveToTrash moves src into trashDir, picking a unique name if needed.
func moveToTrash(src, trashDir string) (int64, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return 0, err
	}
	_ = info
	size, _ := scanPath(src)
	base := filepath.Base(src)
	dst := filepath.Join(trashDir, base)
	for i := 2; ; i++ {
		if _, err := os.Lstat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(trashDir, fmt.Sprintf("%s %d", base, i))
	}
	if err := os.Rename(src, dst); err != nil {
		return 0, err
	}
	return size, nil
}

// UninstallApp moves an app and its associated files to the Trash.
func (Cleaner) UninstallApp(path string) UninstallResult {
	app := appInfoFromPath(path)
	trash := filepath.Join(homeDir(), ".Trash")
	res := UninstallResult{Path: path}
	_ = os.MkdirAll(trash, 0o755)
	for _, item := range uninstallPaths(app) {
		size, err := moveToTrash(item.Path, trash)
		if err != nil {
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
	cfg := defaultCategoryConfigs()
	if b, err := os.ReadFile(configPath()); err == nil {
		var saved []CategoryConfig
		if json.Unmarshal(b, &saved) == nil && len(saved) > 0 {
			return saved
		}
	}
	return cfg
}

// SaveCategories validates and persists the category configuration.
func (Cleaner) SaveCategories(cfg []CategoryConfig) error {
	for _, c := range cfg {
		if strings.TrimSpace(c.Name) == "" {
			return fmt.Errorf("分类名称不能为空")
		}
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
	trash := filepath.Join(homeDir(), ".Trash")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return TrashResult{}, err
	}
	freed, err := moveToTrash(abs, trash)
	if err != nil {
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
