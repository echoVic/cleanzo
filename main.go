package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
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
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/egoist/mygo"
	_ "github.com/egoist/mygo/locales/zh-Hans"
	"github.com/egoist/mygo/plugins/updater"
)

// TopItem is one of the largest entries inside a junk item.
type TopItem struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// CleanResult reports what a clean pass did.
type CleanResult struct {
	Name    string   `json:"name"`
	Freed   int64    `json:"freed"`   // bytes deleted
	Trashed int64    `json:"trashed"` // bytes moved to the Trash
	Files   int64    `json:"files"`
	InUse   int64    `json:"inUse"` // entries left alone because they are in use
	Failed  []string `json:"failed"`
	Skipped string   `json:"skipped,omitempty"`
}

// AppInfo describes an installed application.
type AppInfo struct {
	ID         string `json:"id"`
	Publisher  string `json:"publisher"`
	Name       string `json:"name"`
	Path       string `json:"path"`
	BundleID   string `json:"bundleId"`
	Version    string `json:"version"`
	Size       int64  `json:"size"`
	LastOpened int64  `json:"lastOpened"` // unix seconds; 0 = never opened
	Running    bool   `json:"running"`    // its bundle ID or bundle is running
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
	Launched bool     `json:"launched"`
	Path     string   `json:"path"`
	Moved    int64    `json:"moved"`
	Freed    int64    `json:"freed"`
	Failed   []string `json:"failed"`
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
	IsDir     bool   `json:"isDir"`
}

// OrphanScanResult aggregates leftover detection results.
type OrphanScanResult struct {
	Items     []OrphanItem `json:"items"`
	TotalSize int64        `json:"totalSize"`
	ElapsedMs int64        `json:"elapsedMs"`
}

// SystemInfo describes the app and the machine it runs on.
type SystemInfo struct {
	Version   string `json:"version"` // the app's, from mygo.config.ts; empty under go test
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"goVersion"`
	Chip      string `json:"chip"`      // e.g. "Apple M2 Pro"; empty under go test
	Memory    uint64 `json:"memory"`    // bytes
	Cores     uint64 `json:"cores"`     // logical cores
	OSVersion string `json:"osVersion"` // e.g. "26.6.2"
	BootTime  int64  `json:"bootTime"`  // unix seconds
}

// Cleaner is callable from the frontend: `mygo generate` turns its methods
// into typed TypeScript functions in src/mygo.ts.
type Cleaner struct{}

// homeDir returns the current user's home folder.
func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// configPath returns where Cleanzo settings are persisted.
func configPath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "Cleanzo", "config.json")
}

// canonical returns p absolute, resolved and lower-cased, the form paths are
// compared in.
func canonical(p string) string {
	abs, _ := filepath.Abs(p)
	return strings.ToLower(resolvePath(abs))
}

// removeAll deletes path like os.RemoveAll, also when folders inside are
// read-only, as Go's module cache leaves them: it makes them writable and
// tries again.
var removeAll = func(path string) error {
	err := os.RemoveAll(path)
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			if info, err := d.Info(); err == nil && info.Mode().Perm()&0o700 != 0o700 {
				os.Chmod(p, info.Mode().Perm()|0o700)
			}
		}
		return nil
	})
	return os.RemoveAll(path)
}

// scanPath sums the size and count of regular files under p.
func scanPath(p string) (size, files int64) {
	size, files, _ = measure(p)
	return size, files
}

// measure sums the size and count of regular files under p, and finds when
// anything there last changed (see changedAt). A file counts what it takes
// on disk when that is less than its length (sparse or compressed files), so
// sizes say what removing it frees.
func measure(p string) (size, files int64, newest time.Time) {
	size, files, newest, _ = measureChecked(p)
	return
}

// measureChecked preserves partial byte counts and reports every traversal failure.
func measureChecked(p string, contexts ...context.Context) (size, files int64, newest time.Time, readErrors int) {
	if runtime.GOOS == "windows" && !platformCleanPath(p) {
		return 0, 0, time.Time{}, 1
	}
	filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
		if runtime.GOOS == "windows" && len(contexts) > 0 && contexts[0] != nil && contexts[0].Err() != nil {
			return contexts[0].Err()
		}
		if err != nil || d == nil {
			readErrors++
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			readErrors++
			return nil
		}
		if !platformMeasureEntry(path, info) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if t := changedAt(info); t.After(newest) {
			newest = t
		}
		if d.Type().IsRegular() {
			size += onDisk(info)
			files++
		}
		return nil
	})
	return size, files, newest, readErrors
}

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
	return platformCleanPath(p) && platformCleanPath(abs) && safeResolved(strings.ToLower(resolvePath(abs)))
}

// safeResolved is safeToClean for a path already resolved and lower-cased.
func safeResolved(path string) bool {
	home := strings.ToLower(resolvePath(homeDir()))
	if runtime.GOOS == "windows" && (home == "" || path == "") {
		return false
	}
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
		if platformProtected(path, home) || slices.ContainsFunc(strings.Split(rel, string(filepath.Separator)), appLibrary) {
			return false
		}
		return true
	}
	return allowedTemp(homeDir(), os.TempDir()) && within(path, strings.ToLower(resolvePath(os.TempDir())))
}

// fileExists reports whether p exists.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// resolvePath resolves the links of the longest existing prefix of p, an
// absolute path, and appends the rest.
func resolvePath(p string) string {
	p = filepath.Clean(p)
	if r, ok := memoResolved(p); ok {
		return r
	}
	return resolveUncached(p)
}

// resolveUncached is resolvePath without the expansion memo.
func resolveUncached(p string) string { return resolveFinal(p) }

// usageGuard tells whether something on disk is in use: it holds a file a
// process has open, or it is named after an app that is running, such as
// ~/Library/Caches/com.google.Chrome. Deleting files under a running app
// can break it, so cleaning leaves those alone. When a probe fails the
// guard reports everything as in use, so nothing is cleaned blind.
type usageGuard struct {
	open    []string        // lower-cased paths of open files, sorted
	running map[string]bool // lower-cased identities of running apps
	roots   []string        // lower-cased process paths whose contents are in use
	failed  bool
}

// probeUsage takes a snapshot of the open files and the running apps.
func probeUsage() *usageGuard { return platformUsage() }

// probeFileUsage keeps the Darwin lsof and app probes unchanged.
func probeFileUsage() *usageGuard {
	open, err := openFiles()
	if err != nil {
		return &usageGuard{failed: true}
	}
	running, _, err := runningApps()
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
	for _, root := range g.roots {
		if within(p, root) || within(root, p) {
			return true
		}
	}
	if i := sort.SearchStrings(g.open, p); i < len(g.open) && g.open[i] == p {
		return true
	}
	if i := sort.SearchStrings(g.open, p+string(filepath.Separator)); i < len(g.open) && strings.HasPrefix(g.open[i], p+string(filepath.Separator)) {
		return true
	}
	return namedAfterBundle(filepath.Base(p), g.running)
}

// namedAfterBundle reports whether name is one of the lower-cased bundle IDs,
// or starts with one, as helpers' folders do (com.microsoft.VSCode.ShipIt).
func namedAfterBundle(name string, ids map[string]bool) bool {
	name = strings.ToLower(name)
	for {
		if ids[name] {
			return true
		}
		i := strings.LastIndexByte(name, '.')
		if i <= 0 {
			return false
		}
		name = name[:i]
	}
}

// scanConcurrency bounds how many directory walks run at once during a scan.
// WalkDir is I/O bound; keeping a small fixed pool avoids thrashing the disk.
const scanConcurrency = 8

// tempMinAge is how long an entry of the temporary folder must sit untouched
// before cleaning takes it: apps keep files there while they run.
const tempMinAge = 7 * 24 * time.Hour

// now is the clock entry ages are measured against. Tests move it.
var now = time.Now

// cleanEntry is one entry removed by a junk clean.
type cleanEntry struct {
	path        string
	size, files int64
}

// progressInterval is how often streams report what they are working on.
const progressInterval = 100 * time.Millisecond

// userTrashDir is the macOS Trash directory for the current user.
func userTrashDir() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return filepath.Join(homeDir(), ".Trash")
}

// trashItem moves a file or folder to the Trash the way Finder does: macOS
// picks the Trash of its volume and a free name there. Tests replace it.
var trashItem = mygo.Shell.TrashItem

// cleanHooks report a clean pass's progress. Each may be nil; they are
// called one at a time.
type cleanHooks struct {
	started  func(name string)                    // an item starts
	checking func(name, path string)              // an entry is measured before removal
	removed  func(name, path string, bytes int64) // an entry is gone; bytes: cleaned so far in the pass
	finished func(res CleanResult)                // an item is done, or stopped
}

// OpenFullDiskAccessSettings opens Privacy & Security > Full Disk Access in
// System Settings, where the user can let Cleanzo read protected folders
// such as the Trash.
func (Cleaner) OpenFullDiskAccessSettings() error {
	if runtime.GOOS == "windows" {
		return errUnsupported
	}
	return quietCommand(exec.Command("/usr/bin/open", "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles")).Run()
}

// Info returns basic system information, including the About This Mac
// line the status page's header shows.
func (Cleaner) Info() SystemInfo {
	chip, mem, cores, ver, boot := nativeMachineInfo()
	return SystemInfo{
		Version: mygo.App.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(),
		Chip: chip, Memory: mem, Cores: cores, OSVersion: ver, BootTime: boot,
	}
}

// CheckForUpdates opens the update window, which says when Cleanzo is up to
// date or offers the new version.
func (Cleaner) CheckForUpdates() {
	updater.CheckForUpdates()
}

// ---------------------------------------------------------------------------
// Individual path cleanup
// ---------------------------------------------------------------------------

// PathsCleanResult reports what CleanPaths moved to the Trash.
type PathsCleanResult struct {
	Moved   int64    `json:"moved"`
	Freed   int64    `json:"freed"`
	Files   int64    `json:"files"`
	InUse   int64    `json:"inUse"` // paths left alone because they are in use
	Failed  []string `json:"failed"`
	Removed []string `json:"removed"` // the given paths no longer there: moved now, or gone before
}

// CleanPaths moves the given individual paths (e.g. selected photos) to the
// Trash, honoring the safeToClean gate and leaving alone what is in use. It
// is the fine-grained counterpart to CleanJunk (which works on junk items).
func (Cleaner) CleanPaths(paths []string) PathsCleanResult {
	g := pathUsageProbe()
	res := PathsCleanResult{}
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			res.Failed = append(res.Failed, p+": "+err.Error())
			continue
		}
		if !removable(abs) || !platformRemovalSafe(abs) {
			res.Failed = append(res.Failed, abs+": blocked")
			continue
		}
		if _, err := os.Lstat(abs); err != nil {
			if os.IsNotExist(err) {
				res.Removed = append(res.Removed, p)
			} else {
				res.Failed = append(res.Failed, abs+": "+err.Error())
			}
			continue
		}
		if g.failed {
			res.Failed = append(res.Failed, abs+": "+errUsageUnknown.Error())
			continue
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
		if _, err := os.Lstat(abs); os.IsNotExist(err) {
			res.Removed = append(res.Removed, p)
		}
	}
	return res
}

// ---------------------------------------------------------------------------
// App uninstall support
// ---------------------------------------------------------------------------

// readBundleInfo extracts display name, bundle id and version from an app's
// Info.plist (XML or binary) using macOS's built-in plutil.
func readBundleInfo(plistPath string) (name, bid, version string) {
	// XML rather than JSON: Info.plist files may hold <date> and <data>
	// values, which JSON cannot represent, and plutil then fails outright.
	out, err := quietCommand(exec.Command("/usr/bin/plutil", "-convert", "xml1", "-o", "-", plistPath)).Output()
	if err != nil {
		return "", "", ""
	}
	m := plistStrings(out)
	name = m["CFBundleDisplayName"]
	if name == "" {
		name = m["CFBundleName"]
	}
	return name, m["CFBundleIdentifier"], m["CFBundleShortVersionString"]
}

// plistStrings returns the string values of the top-level dictionary of an
// XML property list.
func plistStrings(data []byte) map[string]string { return plistValues(data) }

// plistValues reads strings and dates from the top-level plist dictionary.
func plistValues(data []byte) map[string]string {
	m := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(data))
	depth := 0 // of dict and array elements; the top-level dictionary is 1
	key := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			return m
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch name := t.Name.Local; {
			case name == "dict" || name == "array":
				depth++
				key = ""
			case depth != 1:
			case name == "key":
				_ = dec.DecodeElement(&key, &t)
			case (name == "string" || name == "date") && key != "":
				var v string
				if dec.DecodeElement(&v, &t) == nil {
					m[key] = v
				}
				key = ""
			default:
				key = "" // another kind of value
			}
		case xml.EndElement:
			if t.Name.Local == "dict" || t.Name.Local == "array" {
				depth--
			}
		}
	}
}

// lastOpened returns the last-used unix time from Spotlight metadata.
func lastOpened(appPath string) int64 {
	out, err := quietCommand(exec.Command("/usr/bin/mdls", "-name", "kMDItemLastUsedDate", "-raw", appPath)).Output()
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
				ID:         appPath,
				Publisher:  "",
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

// appDirs are the folders installed apps live in: the Applications folders
// and the suites one level down in them (/Applications/Adobe Photoshop 2026),
// Apple's own apps and services, and input methods.
func appDirs() []string {
	h := homeDir()
	dirs := []string{
		"/Applications", filepath.Join(h, "Applications"), "/Applications/Utilities",
		"/System/Applications", "/System/Applications/Utilities",
		"/System/Library/CoreServices", "/System/Library/CoreServices/Applications",
		"/Library/Input Methods", filepath.Join(h, "Library", "Input Methods"), "/System/Library/Input Methods",
	}
	for _, root := range []string{"/Applications", filepath.Join(h, "Applications")} {
		entries, _ := os.ReadDir(root)
		for _, e := range entries {
			if e.IsDir() && !strings.HasSuffix(e.Name(), ".app") {
				dirs = append(dirs, filepath.Join(root, e.Name()))
			}
		}
	}
	return dirs
}

// appKeys identifies installed apps in the names of their ~/Library entries.
type appKeys struct {
	bids  map[string]bool // lower-cased bundle IDs
	names map[string]bool // normalized app and bundle folder names
	words map[string]bool // normalized words of bundle IDs, such as "yuque" in com.yuque.desktop
}

// genericBundleWords are bundle ID words too common to tie a folder to an app.
var genericBundleWords = map[string]bool{"desktop": true, "macos": true, "client": true, "helper": true, "agent": true, "apps": true, "inputmethod": true, "launcher": true}

// normalized lower-cases s and drops all but letters and digits, so that
// "Citro Labs" and "citrolabs" compare equal.
func normalized(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

func newAppKeys() appKeys {
	return appKeys{bids: map[string]bool{}, names: map[string]bool{}, words: map[string]bool{}}
}

// appIdentity is what names an app bundle: its folder name, display name
// and bundle ID.
type appIdentity struct{ folder, name, bid string }

func readAppIdentity(path string) appIdentity {
	name, bid, _ := readBundleInfo(filepath.Join(path, "Contents", "Info.plist"))
	return appIdentity{strings.TrimSuffix(filepath.Base(path), ".app"), name, bid}
}

func (k appKeys) add(a appIdentity) {
	k.addName(a.folder)
	k.addName(a.name)
	k.addBundleID(a.bid)
}

// addApp adds the app bundle at path.
func (k appKeys) addApp(path string) { k.add(readAppIdentity(path)) }

func (k appKeys) addName(name string) {
	if n := normalized(name); n != "" {
		k.names[n] = true
	}
}

func (k appKeys) addBundleID(bid string) {
	if bid == "" {
		return
	}
	k.bids[strings.ToLower(bid)] = true
	for i, part := range strings.Split(bid, ".") {
		if w := normalized(part); i > 0 && len(w) >= 4 && !genericBundleWords[w] {
			k.words[w] = true
		}
	}
}

// installedKeys collects the bundle IDs, names and bundle ID words of the
// installed apps, and of the running ones, wherever they live (a running app
// may be on a disk image or in Downloads).
func installedKeys() appKeys {
	var apps []string
	for _, dir := range appDirs() {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".app") {
				apps = append(apps, filepath.Join(dir, e.Name()))
			}
		}
	}
	ids, running, _ := runningApps()
	apps = append(apps, running...)
	k := newAppKeys()
	for bid := range ids {
		k.addBundleID(bid)
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 16)
	)
	for _, app := range apps {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			id := readAppIdentity(app)
			mu.Lock()
			defer mu.Unlock()
			k.add(id)
		}()
	}
	wg.Wait()
	return k
}

// owns reports whether the ~/Library entry base belongs to an installed app:
// it is the app's bundle ID or one of its helpers' (com.vendor.app.helper),
// or it shares a distinctive name or bundle ID word with the app.
func (k appKeys) owns(base string) bool {
	if namedAfterBundle(base, k.bids) {
		return true
	}
	b := normalized(base)
	if b == "" {
		return false
	}
	if k.names[b] {
		return true
	}
	for n := range k.names {
		if len(n) >= 4 && strings.Contains(b, n) || strings.Contains(n, b) {
			return true
		}
	}
	for w := range k.words {
		if strings.Contains(b, w) || strings.Contains(w, b) {
			return true
		}
	}
	return false
}

// markRunning marks apps whose bundle ID or bundle path is running.
func markRunning(apps []AppInfo, ids map[string]bool, paths []string) {
	for i := range apps {
		apps[i].Running = ids[strings.ToLower(apps[i].BundleID)] || slices.Contains(paths, apps[i].Path)
	}
}

// ListApps returns installed applications and whether they are running.
func (Cleaner) ListApps() []AppInfo {
	return listInstalledApps()
}

// appInfoFromPath builds an AppInfo for a single app bundle path.
func appInfoFromPath(appPath string) AppInfo {
	name, bid, version := readBundleInfo(filepath.Join(appPath, "Contents", "Info.plist"))
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(appPath), ".app")
	}
	size, _ := scanPath(appPath)
	return AppInfo{ID: appPath, Publisher: "", Name: name, Path: appPath, BundleID: bid, Version: version, Size: size, LastOpened: lastOpened(appPath)}
}

// installedApp reports whether path is an app ListApps lists: a .app bundle
// directly in /Applications or ~/Applications.
func installedApp(path string) bool {
	p := canonical(path)
	dir := filepath.Dir(p)
	return strings.HasSuffix(p, ".app") &&
		(dir == canonical("/Applications") || dir == canonical(filepath.Join(homeDir(), "Applications")))
}

// sharesBundleID reports whether another installed app, such as Xcode-beta
// next to Xcode, has app's bundle ID.
func sharesBundleID(app AppInfo) bool {
	if app.BundleID == "" {
		return false
	}
	for _, dir := range []string{"/Applications", filepath.Join(homeDir(), "Applications")} {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			other := filepath.Join(dir, e.Name())
			if strings.HasSuffix(e.Name(), ".app") && canonical(other) != canonical(app.Path) &&
				strings.EqualFold(readAppIdentity(other).bid, app.BundleID) {
				return true
			}
		}
	}
	return false
}

// fileName reports whether s can name a single file in a folder. The names
// uninstall builds paths from come from the app's bundle, which must not
// lead them out of the Library folders it cleans.
func fileName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\x00")
}

// uninstallPaths collects the app bundle plus its user-level associated
// files; nothing for paths that are not installed apps.
func uninstallPaths(app AppInfo) []UninstallItem {
	if !installedApp(app.Path) {
		return nil
	}
	h := homeDir()
	name := strings.TrimSuffix(filepath.Base(app.Path), ".app")
	bid := app.BundleID
	if !fileName(name) {
		name = ""
	}
	if !fileName(bid) || sharesBundleID(app) {
		bid = "" // the data under the bundle ID is another installed copy's too
	}

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
	if name != "" {
		add(filepath.Join(h, "Library", "Application Support", name), "support")
		add(filepath.Join(h, "Library", "Caches", name), "cache")
		add(filepath.Join(h, "Library", "Logs", name), "logs")
		add(filepath.Join(h, "Library", "Preferences", name+".plist"), "prefs")
		add(filepath.Join(h, "Library", "Preferences", name), "prefs")
	}
	return items
}

// AppUninstallDetail returns the selected app and its platform uninstall details.
func (Cleaner) AppUninstallDetail(path string) UninstallDetail {
	return appUninstallDetail(path)
}

// UninstallApp uninstalls the selected app: macOS moves its bundle and related
// files to the Trash; Windows opens only its registry-declared uninstaller.
func (Cleaner) UninstallApp(path string) UninstallResult {
	return uninstallApp(path)
}

// codeError is an error the UI translates: its text is the JSON object
// {"code": ..., "args": [...]}.
type codeError struct {
	Code string   `json:"code"`
	Args []string `json:"args,omitempty"`
}

// Error returns the code and arguments as JSON.
func (e codeError) Error() string {
	data, _ := json.Marshal(e)
	return string(data)
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

// Disk returns the boot volume's capacity without scanning files.
func (Cleaner) Disk() DiskStat {
	return diskStat()
}

// removable reports shallow safety for a Trash row; actions also check nested boundaries.
func removable(path string) bool {
	return cleanable(path) && userPickable(path)
}

// cleanable applies shallow rule safety without enabling user-picked AppData rows.
func cleanable(path string) bool {
	return trashable(path) && safeToClean(path)
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

// DirItem is a single child entry inside a directory, returned by DirChildren
// so the frontend can expand a row in place and show what it actually contains.
type DirItem struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	IsDir     bool   `json:"isDir"`
	Trashable bool   `json:"trashable"`
}

// DirListing is the largest children of a folder, and how many it has.
type DirListing struct {
	Items []DirItem `json:"items"`
	Total int       `json:"total"` // more than len(Items) when the list is capped
}

// DirChildren lists the direct children of path with sizes: directories are
// measured with du (8s timeout), files with a quick stat. Unreadable or
// timed-out entries report Size = -1 so the UI can show them as unknown.
// The items are sorted by size descending and capped at 60 entries.
func (Cleaner) DirChildren(ctx context.Context, path string) (DirListing, error) {
	return platformDirChildren(ctx, path)
}

// collectFullScan measures every full-scan target concurrently with du and
// invokes onItem for each completed target, one call at a time, so the
// frontend can stream the ranking as it is discovered. It returns the sorted
// top items, the number of measured targets and the number of failed or
// skipped ones. It stops when ctx is canceled.
func collectFullScan(ctx context.Context, onItem func(path string, size int64, trashable bool)) ([]BigItem, int64, int64) {
	targets := fullScanTargets()
	items := make([]BigItem, 0, len(targets))
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 16)
	)
	var done, skipped int64
	for _, t := range targets {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(target string) {
			defer wg.Done()
			defer func() { <-sem }()
			size, err := duSize(ctx, target, 120*time.Second)
			if err != nil {
				atomic.AddInt64(&skipped, 1)
				return
			}
			atomic.AddInt64(&done, 1)
			tr := removable(target)
			it := BigItem{Path: target, Size: size, Trashable: tr}
			mu.Lock()
			if onItem != nil {
				onItem(target, size, tr)
			}
			items = append(items, it)
			mu.Unlock()
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
// ctx is canceled when the page goes away, which stops the scan.
func (Cleaner) FullScan(ctx context.Context, ch *mygo.Channel[FullScanEvent]) error {
	start := time.Now()
	items, scanned, skipped := collectFullScan(ctx, func(path string, size int64, trashable bool) {
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

// partialRun reports whether a du or find that failed still produced its
// result: both exit with status 1 when some folder inside is unreadable, as
// privacy-protected ones are, yet print what they could read. A timeout is
// a real failure.
func partialRun(ctx context.Context, err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && ctx.Err() == nil
}

// collectLargeScan scans every full-scan target for large files concurrently
// and streams each discovered file through onItem, one call at a time. It
// stops when ctx is canceled.
func collectLargeScan(ctx context.Context, threshold int64, onItem func(f LargeFile)) ([]LargeFile, int64, int64) {
	targets := fullScanTargets()
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, 16)
		all []LargeFile
	)
	var done, skipped int64
	for _, t := range targets {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(target string) {
			defer wg.Done()
			defer func() { <-sem }()
			files, err := findLargeFiles(ctx, target, threshold)
			if err != nil {
				atomic.AddInt64(&skipped, 1)
				return
			}
			atomic.AddInt64(&done, 1)
			mu.Lock()
			if onItem != nil {
				for _, f := range files {
					onItem(f)
				}
			}
			all = append(all, files...)
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	sort.Slice(all, func(i, j int) bool { return all[i].Size > all[j].Size })
	if len(all) > 200 {
		all = all[:200]
	}
	return all, done, skipped
}

// largeScan collects large files and reports the threshold used.
func largeScan(ctx context.Context, minSize int64, onItem func(LargeFile)) LargeScanResult {
	start := time.Now()
	minSize = max(minSize, 50<<20)
	items, scanned, skipped := collectLargeScan(ctx, minSize, onItem)
	return LargeScanResult{
		Items:     items,
		Scanned:   scanned,
		Skipped:   skipped,
		ElapsedMs: time.Since(start).Milliseconds(),
		Threshold: minSize,
	}
}

// LargeScan streams per-file results and the final summary of a large-file
// scan across the same targets as the full scan.
func (Cleaner) LargeScan(ctx context.Context, minSize int64, ch *mygo.Channel[LargeScanEvent]) error {
	res := largeScan(ctx, minSize, func(f LargeFile) {
		_ = ch.Send(LargeScanEvent{Kind: "item", Path: f.Path, Size: f.Size, Trashable: f.Trashable})
	})
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

// appleDataNames are folders of Apple's own services that do not carry the
// com.apple. prefix, normalized.
var appleDataNames = map[string]bool{
	"animoji": true, "networkserviceproxy": true, "addressbook": true, "clouddocs": true,
	"knowledge": true, "callhistorydb": true, "callhistorytransactions": true, "mobilesync": true,
	"crashreporter": true, "icloud": true, "fileprovider": true, "coreparsec": true,
	"differentialprivacy": true, "accounts": true, "syncservices": true, "icdd": true,
	"dock": true, "familycircled": true, "cloudkit": true, "quicklook": true,
}

// collectOrphans inspects ~/Library subdirectories one level deep and reports
// entries that do not correspond to an installed app. Orphan directories are
// measured concurrently with du (60s each); plain files use stat. Each
// completed measurement is streamed through onItem, one call at a time. It
// stops when ctx is canceled. With no roots specified, it checks libraryRoots.
func collectOrphans(ctx context.Context, onItem func(o OrphanItem), roots ...string) ([]OrphanItem, int64) {
	if len(roots) == 0 {
		roots = libraryRoots
	}
	h := homeDir()
	traversal := junkEnv{home: h, tmp: filepath.Clean(os.TempDir()), fda: true}
	keys := installedKeys()
	g := probeUsage()
	type candidate struct {
		path  string
		kind  string
		isDir bool
	}
	var cands []candidate
	for _, root := range roots {
		dir := filepath.Join(h, "Library", root)
		if traversal.traversalBlocked(dir) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() && !e.IsDir() {
				continue // never resolve links into another app's data
			}
			name := e.Name()
			base := name
			if root == "Preferences" || root == "LaunchAgents" {
				base = strings.TrimSuffix(name, ".plist")
			}
			if toolCacheNames[base] || strings.HasPrefix(base, ".") {
				continue // build/tool caches and command line tools' settings, not app leftovers
			}
			if strings.HasPrefix(base, "com.apple.") || appleDataNames[normalized(base)] {
				continue // system data
			}
			if keys.owns(base) || g.inUse(resolvePath(filepath.Join(dir, name))) {
				continue // owned by an installed app, or in use
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
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(c candidate) {
			defer wg.Done()
			defer func() { <-sem }()
			var size int64
			if c.isDir {
				size, _ = duSize(ctx, c.path, 60*time.Second)
			} else if fi, err := os.Stat(c.path); err == nil {
				size = fi.Size()
			}
			if size < 1<<20 {
				return // ignore trivial leftovers
			}
			it := OrphanItem{Path: c.path, Size: size, Kind: c.kind, IsDir: c.isDir, Trashable: removable(c.path)}
			mu.Lock()
			if onItem != nil {
				onItem(it)
			}
			items = append(items, it)
			mu.Unlock()
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
func (Cleaner) ScanOrphans(ctx context.Context, ch *mygo.Channel[OrphanItem]) error {
	return scanOrphans(ctx, ch)
}

// errInUse is the error for a path a running app uses; the UI translates it.
var errInUse = errors.New("in-use")

// A failed usage probe is not evidence that the target application is running.
var errUsageUnknown = errors.New("usage-unknown")

// pathUsageProbe is the shared usage snapshot for individual path actions.
var pathUsageProbe = probeUsage

// errBlocked is the error for a path the safety rules protect.
var errBlocked = errors.New("blocked")

// errUnsupported reports a platform operation that is not implemented.
var errUnsupported = errors.New("unsupported")

// errGone is the error for a path that no longer exists.
var errGone = errors.New("gone")

// TrashPath moves a directory (inside the user's home) to the Trash.
func (Cleaner) TrashPath(path string) (TrashResult, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return TrashResult{}, err
	}
	if !removable(abs) || !platformRemovalSafe(abs) {
		return TrashResult{}, errBlocked
	}
	if _, err := os.Lstat(abs); err != nil {
		if os.IsNotExist(err) {
			return TrashResult{}, errGone
		}
		return TrashResult{}, err
	}
	g := pathUsageProbe()
	if g.failed {
		return TrashResult{}, errUsageUnknown
	}
	if g.inUse(resolvePath(abs)) {
		return TrashResult{}, errInUse
	}
	// Sized like the rows offering it, by the space it takes on disk.
	freed, err := duSize(context.Background(), abs, 60*time.Second)
	if err != nil {
		freed, _ = scanPath(abs)
	}
	if err := trashItem(abs); err != nil {
		return TrashResult{}, err
	}
	return TrashResult{Moved: 1, Freed: freed}, nil
}

// revealArgs returns the arguments of open that show path in Finder. Plain
// folders open in place; files and packages (apps, VMs, disk images,
// libraries), which open would launch or mount, are revealed (selected) in
// their parent folder.
func revealArgs(path string) ([]string, error) {
	path = expandHome(path)
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errGone
		}
		return nil, err
	}
	if fi.IsDir() && !isPackage(fi.Name()) && !fileExists(filepath.Join(abs, "Contents", "Info.plist")) {
		return []string{abs}, nil
	}
	return []string{"-R", abs}, nil
}

// RevealInFinder shows path in Finder: plain folders open in place; files
// and packages, which opening would launch or mount, are selected in their
// parent folder. The call returns an error when the path does not exist.
func (Cleaner) RevealInFinder(path string) error {
	return platformReveal(path)
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

// PhotoPreview returns a JPEG data URL of a photo in the home folder, at most
// 1600 pixels on its longest side, for a closer look than the thumbnails give.
func (Cleaner) PhotoPreview(path string) (string, error) {
	return platformPhotoPreview(path)
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

// Photos are decoded at full size only up to maxDecodePixels. Past that, and
// for HEIC, which Go cannot decode, sips first writes a copy at most
// photoDecodeEdge pixels on its longest side, which is all hashing and
// thumbnails need: a 12000x12000 PNG would otherwise take over half a GB.
var maxDecodePixels = 24_000_000

const photoDecodeEdge = 1024

// loadPhotoImage decodes a photo for hashing and its thumbnail. cfg is the
// photo's real size, while img may be a smaller copy (see maxDecodePixels).
// The returned cleanup removes any temporary file and must be run by the
// caller.
func loadPhotoImage(ctx context.Context, path string) (image.Image, image.Config, func(), error) {
	return platformLoadPhotoImage(ctx, path)
}

// decodeSmallCopy has sips write path as a JPEG, shrunk to photoDecodeEdge
// pixels on its longest side when shrink is set, and decodes that copy.
func decodeSmallCopy(ctx context.Context, path string, shrink bool) (image.Image, func(), error) {
	tmp, err := os.CreateTemp("", "cleanzo-photo-*.jpg")
	if err != nil {
		return nil, nil, err
	}
	tmp.Close()
	cleanup := func() { os.Remove(tmp.Name()) }
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"-s", "format", "jpeg", path, "--out", tmp.Name()}
	if shrink {
		args = append([]string{"-Z", strconv.Itoa(photoDecodeEdge)}, args...)
	}
	if out, err := quietCommand(exec.CommandContext(ctx, "/usr/bin/sips", args...)).CombinedOutput(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("sips: %v: %s", err, strings.TrimSpace(string(out)))
	}
	img, _, close, err := decodeImageFile(tmp.Name())
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return img, func() { close(); cleanup() }, nil
}

// sipsSize returns the pixel size of an image sips reads, such as HEIC.
func sipsSize(ctx context.Context, path string) (w, h int, err error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := quietCommand(exec.CommandContext(ctx, "/usr/bin/sips", "-g", "pixelWidth", "-g", "pixelHeight", path)).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("sips: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), ": ")
		if !ok {
			continue
		}
		n, _ := strconv.Atoi(val)
		switch key {
		case "pixelWidth":
			w = n
		case "pixelHeight":
			h = n
		}
	}
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("sips: no pixel size for %s", path)
	}
	return w, h, nil
}

// decodeConfigFile reads the size and format of an image from its header.
func decodeConfigFile(path string) (image.Config, error) {
	f, err := openPhotoFile(path)
	if err != nil {
		return image.Config{}, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	return cfg, err
}

// decodeImageFile opens a registered-format image (jpeg/png/gif) and decodes
// both its config and full image, returning a cleanup that closes the handle.
func decodeImageFile(path string) (image.Image, image.Config, func(), error) {
	f, err := openPhotoFile(path)
	if err != nil {
		return nil, image.Config{}, nil, err
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		f.Close()
		return nil, image.Config{}, nil, err
	}
	if runtime.GOOS == "windows" && !photoDecodeAllowed(cfg) {
		f.Close()
		return nil, image.Config{}, nil, errUnsupported
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

// A difference of minLumStep in average luminance between neighbouring cells
// is visible; a hash needs minSignificantBits such differences to describe a
// picture at all.
const (
	minLumStep         = 0.02
	minSignificantBits = 10
)

// dHashImage computes a 64-bit difference hash: it reduces the image to a 9x8
// grid of average luminance, each cell averaging a 6x6 lattice of samples,
// and sets a bit where a cell is visibly brighter than the one to its left.
// ok is false when too few neighbours differ visibly, as in blank pages and
// flat screenshots: such hashes say nothing about the picture, so only
// byte-identical copies may group those images.
func dHashImage(img image.Image) (hash uint64, ok bool) {
	const gw, gh, sub = 9, 8, 6
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var lum [gh][gw]float64
	for y := 0; y < gh; y++ {
		for x := 0; x < gw; x++ {
			var sum float64
			for sy := 0; sy < sub; sy++ {
				py := b.Min.Y + ((y*sub+sy)*h+h/2)/(gh*sub)
				for sx := 0; sx < sub; sx++ {
					px := b.Min.X + ((x*sub+sx)*w+w/2)/(gw*sub)
					r, g, bl, _ := img.At(px, py).RGBA()
					sum += (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 65535.0
				}
			}
			lum[y][x] = sum / (sub * sub)
		}
	}
	significant := 0
	for y := 0; y < gh; y++ {
		for x := 0; x < gw-1; x++ {
			hash <<= 1
			d := lum[y][x+1] - lum[y][x]
			if d >= minLumStep {
				hash |= 1
			}
			if math.Abs(d) >= minLumStep {
				significant++
			}
		}
	}
	return hash, significant >= minSignificantBits
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
	nw := max(int(math.Round(float64(w)*scale)), 1)
	nh := max(int(math.Round(float64(h)*scale)), 1)
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
	f, err := openPhotoFile(path)
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

// packageExts are folder extensions macOS shows as single files. The images
// inside belong to the app that manages them, such as the originals and
// previews of a Photos library, so the photo scan leaves them alone.
var packageExts = map[string]bool{
	".photoslibrary": true, ".photolibrary": true, ".aplibrary": true, ".migratedphotolibrary": true,
	".app": true, ".bundle": true, ".framework": true, ".plugin": true, ".appex": true, ".xpc": true,
	".kext": true, ".prefpane": true, ".qlgenerator": true, ".mdimporter": true, ".saver": true, ".component": true,
	".imovielibrary": true, ".fcpbundle": true, ".theater": true, ".tvlibrary": true, ".musiclibrary": true,
	".logicx": true, ".band": true, ".pages": true, ".numbers": true, ".key": true, ".rtfd": true, ".scriv": true,
	".xcodeproj": true, ".xcworkspace": true, ".playground": true, ".xcarchive": true, ".dsym": true, ".docset": true,
	".sparsebundle": true, ".pvm": true, ".vmwarevm": true, ".utm": true,
}

// libraryExts are packages whose apps keep a database of what is inside, so
// removing anything from them by hand corrupts the library.
var libraryExts = map[string]bool{
	".photoslibrary": true, ".photolibrary": true, ".aplibrary": true, ".migratedphotolibrary": true,
	".imovielibrary": true, ".fcpbundle": true, ".theater": true, ".tvlibrary": true, ".musiclibrary": true,
}

// appLibrary reports whether a folder name is an app-managed library's, such
// as "Photos Library.photoslibrary" or Photo Booth's.
func appLibrary(name string) bool {
	return libraryExts[strings.ToLower(filepath.Ext(name))] || strings.EqualFold(name, "Photo Booth Library")
}

// isPackage reports whether a folder name is a package's, such as
// "Photos Library.photoslibrary" or Photo Booth's library.
func isPackage(name string) bool {
	return packageExts[strings.ToLower(filepath.Ext(name))] || name == "Photo Booth Library"
}

// collectPhotoFiles walks the given roots recursively and collects known image
// files, stopping once cap files are found. Packages and hidden folders are
// skipped.
func collectPhotoFiles(roots []string, cap int) []string {
	return platformCollectPhotoFiles(context.Background(), roots, cap)
}

// clusterIdx is an internal list of decoded-photo indices forming one cluster.
type clusterIdx struct {
	kind string
	idxs []int
}

// decodeRow is one concurrently-processed photo.
type decodeRow struct {
	item   PhotoItem
	hash   uint64
	hashOK bool // the hash describes the picture (see dHashImage)
	sha    string
	size   int64
	mod    time.Time
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
func (Cleaner) ScanSimilarPhotos(ctx context.Context, ch *mygo.Channel[SimilarPhotoEvent]) error {
	h := homeDir()
	roots := []string{
		filepath.Join(h, "Pictures"),
		filepath.Join(h, "Desktop"),
	}
	res := buildSimilarPhotos(ctx, roots, func(done, total int) {
		_ = ch.Send(SimilarPhotoEvent{Kind: "progress", Processed: done, Total: total})
	})
	return ch.Send(SimilarPhotoEvent{Kind: "done", Result: &res})
}

// buildSimilarPhotos is the testable core of ScanSimilarPhotos: it discovers,
// decodes, hashes, thumbnails and clusters photos under roots, calling
// onProgress after each file.
func buildSimilarPhotos(ctx context.Context, roots []string, onProgress func(done, total int)) SimilarPhotosResult {
	start := time.Now()
	files := platformCollectPhotoFiles(ctx, roots, similarPhotoCap)
	total := len(files)

	var (
		mu        sync.Mutex
		rows      []decodeRow
		processed int64
		wg        sync.WaitGroup
		sem       = make(chan struct{}, scanConcurrency)
	)
	for _, p := range files {
		if ctx.Err() != nil {
			break
		}
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
			img, cfg, cleanup, err := loadPhotoImage(ctx, p)
			if err != nil {
				return
			}
			defer cleanup()
			if cfg.Width < minPhotoDim || cfg.Height < minPhotoDim {
				return // tiny placeholder / 1x1
			}
			hash, hashOK := dHashImage(img)
			thumb := thumbDataURL(img)
			sha, err := fileSHA1(p)
			if err != nil {
				sha = "unreadable:" + p // never an exact copy of another file
			}
			mu.Lock()
			rows = append(rows, decodeRow{
				item: PhotoItem{Path: p, Size: fi.Size(), Width: cfg.Width, Height: cfg.Height, Thumb: thumb},
				hash: hash, hashOK: hashOK, sha: sha, size: fi.Size(), mod: fi.ModTime(),
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

	// 2) Perceptual clusters among the remaining files whose hashes say
	// something about the picture.
	hashMap := map[int]uint64{}
	var rest []int
	for i := range rows {
		if used[i] || !rows[i].hashOK {
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
	mygo.App.SetLanguages("en", "zh-Hans")
	// 自动检查更新的频率：4 小时一次（默认一天），新发布的版本当天就能被看到。
	mygo.Use(updater.New(updater.Options{Interval: 4 * time.Hour}))
	mygo.Bind(Cleaner{})
	mygo.Bind(LanguageSettings{})

	mygo.App.WhenReady(func() {
		if runtime.GOOS == "windows" && !mygo.WebViewAvailable() {
			go offerWebView()
			return
		}
		opts := mygo.WindowOptions{
			Title:           "Cleanzo",
			Width:           1000,
			Height:          700,
			MinWidth:        560,
			MinHeight:       420,
			BackgroundColor: "light-dark(#f6f7f9, #0f1115)", // the page's --bg in src/style.css
			StateKey:        "main",
			URL:             "/",
		}
		smokeDir := os.Getenv("CLEANZO_SMOKE_DIR")
		if smokeDir != "" {
			opts.Page.PreloadScript = smokeScript
		}
		w := mygo.NewWindow(opts)
		if runtime.GOOS == "windows" {
			w.Page().SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
				if req.URL == "ms-settings:appsfeatures" || strings.HasPrefix(req.URL, "https://") || strings.HasPrefix(req.URL, "http://") {
					go mygo.Shell.OpenExternal(req.URL)
				}
				return nil
			})
		}
		if smokeDir != "" {
			startSmoke(w, smokeDir)
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// photoFileOpen allows tests to observe every photo data stream after platform safety checks.
var photoFileOpen = os.Open

// photoDecodeAllowed bounds the decoded allocation without overflowing the pixel product.
func photoDecodeAllowed(cfg image.Config) bool {
	return cfg.Width > 0 && cfg.Height > 0 && cfg.Width <= maxDecodePixels/cfg.Height
}
