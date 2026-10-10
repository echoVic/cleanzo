package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
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
	Path string `json:"path"`
	Size int64  `json:"size"`
	Kind string `json:"kind"`
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

// topItems returns the largest direct children of a category's paths.
func topItems(cat Category) []TopItem {
	var tops []TopItem
	for _, p := range cat.paths {
		entries, err := os.ReadDir(p)
		if err != nil {
			continue
		}
		for _, e := range entries {
			child := filepath.Join(p, e.Name())
			size, _ := scanPath(child)
			tops = append(tops, TopItem{Name: e.Name(), Path: child, Size: size})
		}
	}
	sort.Slice(tops, func(i, j int) bool { return tops[i].Size > tops[j].Size })
	if len(tops) > 5 {
		tops = tops[:5]
	}
	return tops
}

// scanCategories fills in Size/Files/Missing/Top for every category.
func scanCategories(cats []Category) []Category {
	for i := range cats {
		for _, p := range cats[i].paths {
			if _, err := os.Lstat(p); err != nil {
				cats[i].Missing = append(cats[i].Missing, p)
				continue
			}
			size, files := scanPath(p)
			cats[i].Size += size
			cats[i].Files += files
		}
		cats[i].Top = topItems(cats[i])
	}
	return cats
}

// Scan returns every category with its current footprint.
func (Cleaner) Scan() []Category {
	return scanCategories(loadCategories())
}

// cleanCategory removes the contents of a category's paths.
func cleanCategory(cat Category) CleanResult {
	res := CleanResult{Name: cat.Name}
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
		for _, e := range entries {
			child := filepath.Join(p, e.Name())
			size, files := scanPath(child)
			if err := os.RemoveAll(child); err != nil {
				res.Failed = append(res.Failed, child+": "+err.Error())
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
			it := OrphanItem{Path: c.path, Size: size, Kind: c.kind}
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
