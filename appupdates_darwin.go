package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/egoist/mygo"
)

// appBundle is what a check reads from an installed app.
type appBundle struct {
	id, path, bundleID string
	short, build       string // CFBundleShortVersionString, CFBundleVersion
	feed               string // SUFeedURL
	name, exec         string // CFBundleName (or display name), CFBundleExecutable
	mas                bool   // has an App Store receipt
	electron           map[string]string
}

// appBundlePlistOutput is the external command boundary; tests replace it.
var appBundlePlistOutput = func(cmd *exec.Cmd) ([]byte, error) { return cmd.Output() }

// readAppBundle reads Info.plist, the App Store receipt and
// Contents/Resources/app-update.yml; false when Info.plist cannot be read.
func readAppBundle(ctx context.Context, path string) (appBundle, bool) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := appBundlePlistOutput(quietCommand(exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", filepath.Join(path, "Contents", "Info.plist"))))
	if err != nil || ctx.Err() != nil {
		return appBundle{}, false
	}
	m := plistValues(out)
	b := appBundle{id: path, path: path, bundleID: m["CFBundleIdentifier"], short: m["CFBundleShortVersionString"],
		build: m["CFBundleVersion"], feed: strings.TrimSpace(m["SUFeedURL"]), name: m["CFBundleName"], exec: m["CFBundleExecutable"]}
	if b.name == "" {
		b.name = m["CFBundleDisplayName"]
	}
	_, err = os.Stat(filepath.Join(path, "Contents", "_MASReceipt", "receipt"))
	b.mas = err == nil
	if data, err := os.ReadFile(filepath.Join(path, "Contents", "Resources", "app-update.yml")); err == nil && len(data) < 64<<10 {
		b.electron = parseYAMLScalars(data)
	}
	return b, true
}

// ---- parsers ---------------------------------------------------------------------

// parseYAMLScalars reads the top-level "key: value" lines of a small YAML
// file, unquoting single or double quotes; nested and list lines are skipped.
func parseYAMLScalars(data []byte) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '-' || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '\'' && v[len(v)-1] == '\'' || v[0] == '"' && v[len(v)-1] == '"') {
			v = v[1 : len(v)-1]
		}
		m[strings.TrimSpace(k)] = v
	}
	return m
}

type appcastEnclosure struct {
	Version string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version,attr"`
	Short   string `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString,attr"`
}

type appcastItem struct {
	Title     string             `xml:"title"`
	Version   string             `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle version"`
	Short     string             `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle shortVersionString"`
	MinOS     string             `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle minimumSystemVersion"`
	Channel   string             `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle channel"`
	Notes     string             `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle releaseNotesLink"`
	FullNotes string             `xml:"http://www.andymatuschak.org/xml-namespaces/sparkle fullReleaseNotesLink"`
	Enclosure []appcastEnclosure `xml:"enclosure"`
}

// parseAppcast returns the newest item for this system: no channel, minimum
// system version met. version is sparkle:version, short the display version.
func parseAppcast(data []byte, osVersion string) (version, short, notes string, ok bool) {
	var feed struct {
		Items []appcastItem `xml:"channel>item"`
	}
	if xml.Unmarshal(data, &feed) != nil {
		return "", "", "", false
	}
	osParts := versionParts(cleanVersion(osVersion))
	var best []int
	for _, it := range feed.Items {
		if strings.TrimSpace(it.Channel) != "" {
			continue
		}
		if min := versionParts(cleanVersion(it.MinOS)); min != nil && osParts != nil && compareVersions(min, osParts) > 0 {
			continue
		}
		v, s := strings.TrimSpace(it.Version), strings.TrimSpace(it.Short)
		for _, e := range it.Enclosure {
			if v == "" {
				v = strings.TrimSpace(e.Version)
			}
			if s == "" {
				s = strings.TrimSpace(e.Short)
			}
		}
		if s == "" {
			if f := strings.Fields(it.Title); len(f) > 0 && versionParts(cleanVersion(f[len(f)-1])) != nil {
				s = f[len(f)-1]
			}
		}
		key := versionParts(cleanVersion(v))
		if key == nil {
			key = versionParts(cleanVersion(s))
		}
		if key == nil || (best != nil && compareVersions(key, best) <= 0) {
			continue
		}
		best, version, short, notes = key, v, s, ""
		n := strings.TrimSpace(it.Notes)
		if !feedURLOK(n) {
			n = strings.TrimSpace(it.FullNotes)
		}
		if feedURLOK(n) {
			notes = n
		}
	}
	if best == nil {
		return "", "", "", false
	}
	if short == "" {
		short = version
	}
	return version, short, notes, true
}

// brewCask is one installed cask: its token, newest version and app names.
type brewCask struct {
	token, version string
	apps           []string
}

// parseBrewCasks reads `brew info --cask --installed --json=v2`. An app
// artifact is a name, optionally followed by {"target": "New Name.app"}.
func parseBrewCasks(data []byte) []brewCask {
	var info struct {
		Casks []struct {
			Token     string                       `json:"token"`
			Version   string                       `json:"version"`
			Artifacts []map[string]json.RawMessage `json:"artifacts"`
		} `json:"casks"`
	}
	if json.Unmarshal(data, &info) != nil {
		return nil
	}
	out := []brewCask{}
	for _, c := range info.Casks {
		bc := brewCask{token: c.Token, version: c.Version}
		for _, art := range c.Artifacts {
			raw, ok := art["app"]
			if !ok {
				continue
			}
			var parts []json.RawMessage
			if json.Unmarshal(raw, &parts) != nil {
				continue
			}
			for _, p := range parts {
				var name string
				var target struct {
					Target string `json:"target"`
				}
				switch {
				case json.Unmarshal(p, &name) == nil:
					bc.apps = append(bc.apps, filepath.Base(name))
				case json.Unmarshal(p, &target) == nil && target.Target != "" && len(bc.apps) > 0:
					bc.apps[len(bc.apps)-1] = filepath.Base(target.Target)
				}
			}
		}
		out = append(out, bc)
	}
	return out
}

// caskIndex maps lower-cased app bundle names to their cask.
func caskIndex(casks []brewCask) map[string]brewCask {
	m := map[string]brewCask{}
	for _, c := range casks {
		for _, a := range c.apps {
			m[strings.ToLower(a)] = c
		}
	}
	return m
}

// parseITunesLookup reads the first result of an iTunes Lookup response.
func parseITunesLookup(data []byte) (version string, trackID int64, notesURL string, ok bool) {
	var r struct {
		ResultCount int `json:"resultCount"`
		Results     []struct {
			TrackID      int64  `json:"trackId"`
			Version      string `json:"version"`
			TrackViewURL string `json:"trackViewUrl"`
		} `json:"results"`
	}
	if json.Unmarshal(data, &r) != nil || r.ResultCount == 0 || len(r.Results) == 0 || r.Results[0].Version == "" || r.Results[0].TrackID == 0 {
		return "", 0, "", false
	}
	first := r.Results[0]
	if feedURLOK(first.TrackViewURL) {
		notesURL = first.TrackViewURL
	}
	return first.Version, first.TrackID, notesURL, true
}

// ---- one app ----------------------------------------------------------------------

// appUpdateSources are the outside world a check reads; tests replace them.
type appUpdateSources struct {
	brew      func(ctx context.Context) []brewCask
	fetch     func(ctx context.Context, url string) ([]byte, error)
	region    string // App Store country, lower case
	osVersion string
}

// realAppUpdateSources asks Homebrew and the network.
func realAppUpdateSources() appUpdateSources {
	return appUpdateSources{brew: installedCasks, fetch: fetchFeed, region: storeRegion(), osVersion: nativeSysctlString("kern.osproductversion")}
}

// installedCasks runs `brew info --cask --installed --json=v2` once; nil
// without Homebrew or when it fails.
func installedCasks(ctx context.Context) []brewCask {
	tool := findTool("brew")
	if tool == "" {
		return nil
	}
	if !appUpgradeMu.lockContext(ctx) {
		return nil
	}
	defer appUpgradeMu.unlock()
	out, err := runTool(ctx, tool, []string{"info", "--cask", "--installed", "--json=v2"}, time.Minute)
	if err != nil {
		return nil
	}
	if i := strings.IndexByte(out, '{'); i > 0 {
		out = out[i:] // runTool merges stderr; anything printed before the JSON is dropped
	}
	return parseBrewCasks([]byte(out))
}

// storeRegion is the App Store country from the system locale (zh_CN → cn).
func storeRegion() string {
	out, err := quietCommand(exec.Command("/usr/bin/defaults", "read", "-g", "AppleLocale")).Output()
	if err == nil {
		loc := strings.TrimSpace(string(out))
		if i := strings.IndexAny(loc, "@"); i >= 0 {
			loc = loc[:i]
		}
		if _, region, ok := strings.Cut(loc, "_"); ok && len(region) == 2 {
			return strings.ToLower(region)
		}
	}
	return "us"
}

// checkTarget finds an app's update source and asks it (spec section 4).
func checkTarget(ctx context.Context, b appBundle, src appUpdateSources, casks map[string]brewCask) (AppUpdate, appUpdateAction) {
	u := AppUpdate{ID: b.id, Current: b.short}
	fail := func(err error) (AppUpdate, appUpdateAction) {
		u.State, u.Reason = "error", feedReason(err)
		if u.Reason == "bad-url" {
			u.State = "unknown"
		}
		return u, appUpdateAction{}
	}
	settle := func(current, latest string, a appUpdateAction) (AppUpdate, appUpdateAction) {
		u.State, u.Reason = updateState(current, latest)
		if u.State == "update" {
			u.Action = a.kind
			return u, a
		}
		return u, appUpdateAction{}
	}
	switch {
	case b.mas:
		u.Source = "mas"
		if b.bundleID == "" {
			u.State, u.Reason = "unknown", "no-source"
			return u, appUpdateAction{}
		}
		var (
			version, notes string
			id             int64
			ok             bool
		)
		for _, region := range uniq(src.region, "us") {
			q := url.Values{"bundleId": {b.bundleID}, "entity": {"macSoftware"}, "country": {region}}
			data, err := src.fetch(ctx, "https://itunes.apple.com/lookup?"+q.Encode())
			if err != nil {
				return fail(err)
			}
			if version, id, notes, ok = parseITunesLookup(data); ok {
				break
			}
		}
		if !ok {
			return fail(errFeedNotFound)
		}
		u.Latest, u.NotesURL = version, notes
		return settle(b.short, version, appUpdateAction{kind: "open-store", path: b.path, url: fmt.Sprintf("macappstore://apps.apple.com/app/id%d", id)})
	case casks[strings.ToLower(filepath.Base(b.path))].token != "":
		c := casks[strings.ToLower(filepath.Base(b.path))]
		u.Source, u.Latest = "brew", cleanVersion(c.version)
		return settle(b.short, c.version, appUpdateAction{kind: "brew", token: c.token, path: b.path, latest: cleanVersion(c.version)})
	case b.feed != "":
		u.Source = "sparkle"
		if !feedURLOK(b.feed) {
			return fail(errFeedURL)
		}
		data, err := src.fetch(ctx, b.feed)
		if err != nil {
			return fail(err)
		}
		version, short, notes, ok := parseAppcast(data, src.osVersion)
		if !ok {
			return fail(errFeedBad)
		}
		u.Latest, u.NotesURL = short, notes
		a := appUpdateAction{kind: "open-app", path: b.path}
		if versionParts(cleanVersion(version)) != nil && versionParts(cleanVersion(b.build)) != nil {
			return settle(b.build, version, a)
		}
		return settle(b.short, short, a)
	case b.electron != nil:
		u.Source = "electron"
		feed := electronFeed(b.electron)
		if feed == "" {
			u.Source, u.State, u.Reason = "", "unknown", "no-source"
			if p := b.electron["provider"]; p == "github" || p == "generic" {
				u.Source, u.Reason = "electron", "bad-url"
			}
			return u, appUpdateAction{}
		}
		data, err := src.fetch(ctx, feed)
		if err != nil {
			return fail(err)
		}
		latest := parseYAMLScalars(data)["version"]
		if latest == "" {
			return fail(errFeedBad)
		}
		// A shared download folder can hold another product's manifest
		// (Aime and Flux Island both point at Mixoul's): its files must be
		// named after this app.
		if !feedMatchesApp(electronFeedNames(data), b) {
			u.State, u.Reason = "unknown", "bad-feed"
			return u, appUpdateAction{}
		}
		u.Latest = latest
		return settle(b.short, latest, appUpdateAction{kind: "open-app", path: b.path})
	}
	u.State, u.Reason = "unknown", "no-source"
	return u, appUpdateAction{}
}

// electronFeed is the electron-updater manifest URL, or "" when the
// provider is not github or generic or its address is unusable.
func electronFeed(y map[string]string) string {
	channel := y["channel"]
	if channel == "" || strings.ContainsAny(channel, "/?#") {
		channel = "latest"
	}
	switch y["provider"] {
	case "github":
		if !fileName(y["owner"]) || !fileName(y["repo"]) {
			return ""
		}
		return "https://github.com/" + url.PathEscape(y["owner"]) + "/" + url.PathEscape(y["repo"]) + "/releases/latest/download/" + channel + "-mac.yml"
	case "generic":
		base := strings.TrimSpace(y["url"])
		if !feedURLOK(base) {
			return ""
		}
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		return base + channel + "-mac.yml"
	}
	return ""
}

func uniq(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x != "" && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// ---- the whole list ---------------------------------------------------------------

const appUpdateWorkers = 6

// checkAppUpdates checks ids (the page's AppInfo IDs), sending each result
// as it is known and {ID: "__done"} last. Only installed apps are read.
func checkAppUpdates(ctx context.Context, ids []string, src appUpdateSources, send func(AppUpdate) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	generation := beginAppUpdateCheck()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	bundles := make([]appBundle, 0, len(ids))
	needBrew := false
	var sendMu sync.Mutex
	emit := func(u AppUpdate, a appUpdateAction) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return publishAppUpdate(ctx, generation, u, a, send)
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		b, ok := appBundle{}, false
		if installedApp(id) {
			b, ok = readAppBundle(ctx, id)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !ok {
			if err := emit(AppUpdate{ID: id, State: "unknown", Reason: "no-source"}, appUpdateAction{}); err != nil {
				return err
			}
			continue
		}
		bundles = append(bundles, b)
		needBrew = needBrew || !b.mas
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var casks map[string]brewCask
	if needBrew && src.brew != nil {
		casks = caskIndex(src.brew(ctx))
	}
	jobs := make(chan appBundle)
	errs := make(chan error, 1)
	var wg sync.WaitGroup
	for range appUpdateWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range jobs {
				if ctx.Err() != nil {
					return
				}
				u, a := checkTarget(ctx, b, src, casks)
				if ctx.Err() != nil {
					return
				}
				if err := emit(u, a); err != nil {
					select {
					case errs <- err:
					default:
					}
					cancel()
					return
				}
			}
		}()
	}
feed:
	for _, b := range bundles {
		select {
		case jobs <- b:
		case <-ctx.Done():
			break feed
		case err := <-errs:
			close(jobs)
			wg.Wait()
			return err
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errs:
		return err
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return emit(AppUpdate{ID: "__done"}, appUpdateAction{})
}

// Register the macOS checker; the bound method stays platform-neutral.
func init() {
	appUpdateCheck = func(ctx context.Context, ids []string, ch *mygo.Channel[AppUpdate]) error {
		return checkAppUpdates(ctx, ids, realAppUpdateSources(), ch.Send)
	}
}

// electronFeedNames lists the file names an electron-updater manifest
// offers: the top-level path and every files[].url. A folded value (">-")
// is read from the next line.
func electronFeedNames(data []byte) []string {
	var names []string
	add := func(v string) {
		v = strings.Trim(strings.TrimSpace(v), `'"`)
		if v != "" && v != ">-" && v != "|" {
			if i := strings.LastIndexByte(v, '/'); i >= 0 {
				v = v[i+1:]
			}
			names = append(names, v)
		}
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		var v string
		switch {
		case strings.HasPrefix(line, "path:"):
			v = strings.TrimSpace(strings.TrimPrefix(line, "path:"))
		case strings.HasPrefix(trimmed, "- url:") && line != trimmed:
			v = strings.TrimSpace(strings.TrimPrefix(trimmed, "- url:"))
		default:
			continue
		}
		if (v == ">-" || v == "|" || v == ">") && i+1 < len(lines) {
			v = lines[i+1]
		}
		add(v)
	}
	return names
}

// feedMatchesApp reports whether a manifest's files are named after the
// app: its name, executable or bundle name, ignoring case and anything but
// letters and digits. A manifest without files cannot be told apart and
// counts as a match.
func feedMatchesApp(names []string, b appBundle) bool {
	if len(names) == 0 {
		return true
	}
	norm := func(s string) string {
		var out strings.Builder
		for _, r := range strings.ToLower(s) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 && unicode.IsLetter(r) {
				out.WriteRune(r)
			}
		}
		return out.String()
	}
	var want []string
	for _, c := range []string{b.name, b.exec, strings.TrimSuffix(filepath.Base(b.path), ".app")} {
		if n := norm(c); n != "" {
			want = append(want, n)
		}
	}
	for _, name := range names {
		n := norm(name)
		for _, w := range want {
			if strings.Contains(n, w) {
				return true
			}
		}
	}
	return false
}

// openURLCommand opens a URL or an app with /usr/bin/open; tests replace it.
var openURLCommand = func(target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := quietCommand(exec.CommandContext(ctx, "/usr/bin/open", target)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("open failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// appBrewLock serializes info with upgrades. Checks wait with cancellation;
// competing upgrades retain the immediate busy response.
type appBrewLock chan struct{}

func (m appBrewLock) tryLock() bool {
	select {
	case m <- struct{}{}:
		return true
	default:
		return false
	}
}

func (m appBrewLock) lock()   { m <- struct{}{} }
func (m appBrewLock) unlock() { <-m }
func (m appBrewLock) lockContext(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case m <- struct{}{}:
		if ctx.Err() != nil {
			m.unlock()
			return false
		}
		return true
	case <-ctx.Done():
		return false
	}
}

var appUpgradeMu = make(appBrewLock, 1)

// upgradeApp does what the last check found for id (spec section 5).
func upgradeApp(ctx context.Context, id string) AppUpgradeResult {
	res := AppUpgradeResult{ID: id}
	a, ok := appActionFor(id)
	if !ok {
		res.Code = "not-checked"
		return res
	}
	switch a.kind {
	case "open-store", "open-app":
		target := a.url
		if a.kind == "open-app" {
			target = a.path
		}
		if err := openURLCommand(target); err != nil {
			res.Code, res.Detail = "open-failed", err.Error()
			return res
		}
		res.Done = true
		return res
	case "brew":
	default:
		res.Code = "not-checked"
		return res
	}
	if !appUpgradeMu.tryLock() {
		res.Code = "busy"
		return res
	}
	defer appUpgradeMu.unlock()
	b, ok := readAppBundle(ctx, a.path)
	if !ok {
		res.Code = "not-checked"
		return res
	}
	// A running app would be replaced under itself.
	g := probeUsage()
	if g.failed || g.inUse(resolvePath(a.path)) || (b.bundleID != "" && g.running[strings.ToLower(b.bundleID)]) {
		res.Code = errInUse.Error()
		return res
	}
	res.Command = "brew upgrade --cask " + a.token
	tool := findTool("brew")
	if tool == "" {
		res.Code, res.Detail = "brew-failed", "no-tool"
		return res
	}
	// Once brew starts, the page closing does not stop it: a kill can land
	// between removing the old app and moving the new one in. Only the timeout
	// bounds it, and the bundle is read afterwards either way.
	run := context.WithoutCancel(ctx)
	out, err := runTool(run, tool, []string{"upgrade", "--cask", a.token}, 10*time.Minute)
	if after, ok := readAppBundle(run, a.path); ok {
		res.Version = after.short
	}
	if v, l := versionParts(cleanVersion(res.Version)), versionParts(cleanVersion(a.latest)); v != nil && l != nil && compareVersions(v, l) >= 0 {
		forgetAppAction(id, a.revision)
		res.Done, res.Command = true, ""
		return res
	}
	res.Code = "brew-failed"
	res.Detail = lastLine(out)
	if res.Detail == "" && err != nil {
		res.Detail = err.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		res.Detail = "timeout"
	}
	return res
}

// lastLine is the last non-empty line of a command's output.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// cleanAppCache moves an installed app's cache folders to the Trash.
func cleanAppCache(path string) UninstallResult {
	res := UninstallResult{Path: path}
	if !installedApp(path) {
		res.Failed = append(res.Failed, path+": not-app")
		return res
	}
	app := appInfoFromPath(path)
	items := appCachePaths(app)
	// Probe after measuring so an app launched during the scan is protected.
	g := probeUsage()
	if g.failed || g.inUse(resolvePath(path)) || (app.BundleID != "" && g.running[strings.ToLower(app.BundleID)]) {
		res.Failed = append(res.Failed, path+": "+errInUse.Error())
		return res
	}
	for _, item := range items {
		if item.Kind != "cache" {
			continue
		}
		if g.inUse(resolvePath(item.Path)) {
			res.Failed = append(res.Failed, item.Path+": "+errInUse.Error())
			continue
		}
		if err := trashItem(item.Path); err != nil {
			res.Failed = append(res.Failed, item.Path+": "+err.Error())
			continue
		}
		res.Moved++
		res.Freed += item.Size
	}
	return res
}

func init() {
	appUpdateUpgrade = upgradeApp
	appUpdateClean = cleanAppCache
}

// appCachePaths collects and measures associated folders; tests replace it
// to simulate an app starting while those measurements are in progress.
var appCachePaths = uninstallPaths
