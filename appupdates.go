package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
)

// AppUpdate is one installed app's update state, streamed by
// CheckAppUpdates. The page shows Current → Latest and an Update button for
// State "update"; the button sends only ID, and the action stays in Go.
type AppUpdate struct {
	ID       string `json:"id"`       // AppInfo.ID; "__done" ends the stream
	Source   string `json:"source"`   // "mas" | "brew" | "sparkle" | "electron" | ""
	Current  string `json:"current"`  // the bundle's version
	Latest   string `json:"latest"`   // the source's newest version
	State    string `json:"state"`    // "update" | "current" | "unknown" | "error"
	Reason   string `json:"reason"`   // why unknown or error: no-source, bad-url, network, rate-limited, bad-feed, uncomparable, not-found
	NotesURL string `json:"notesUrl"` // release notes; https only
	Action   string `json:"action"`   // "brew" | "open-store" | "open-app" | ""
}

// AppUpgradeResult is what UpgradeApp did.
type AppUpgradeResult struct {
	ID      string `json:"id"`
	Done    bool   `json:"done"`    // brew: the bundle now has Latest; open-*: opened
	Version string `json:"version"` // the bundle's version afterwards (brew)
	Code    string `json:"code"`    // "in-use" | "brew-failed" | "not-checked" | "unsupported" | "busy" | ""
	Detail  string `json:"detail"`  // brew's last output line
	Command string `json:"command"` // the command to run in Terminal when brew failed
}

// appUpdateAction is what the Update button does for an app. It is only
// ever built in Go from what a check found.
type appUpdateAction struct {
	kind     string // "brew" | "open-store" | "open-app"
	token    string // brew cask token
	path     string // the app bundle
	url      string // macappstore:// link
	latest   string // the cask's version as the check found it, to confirm a brew upgrade
	revision uint64 // distinguishes even identical concurrent publications
}

// Platform implementations register hooks without putting bound methods in
// files with build constraints.
var (
	appUpdateCheck   func(context.Context, []string, *mygo.Channel[AppUpdate]) error
	appUpdateUpgrade func(context.Context, string) AppUpgradeResult
	appUpdateClean   func(string) UninstallResult
)

// CheckAppUpdates streams the update state of the apps listed by the page.
func (Cleaner) CheckAppUpdates(ctx context.Context, ids []string, ch *mygo.Channel[AppUpdate]) error {
	if appUpdateCheck == nil {
		return errUnsupported
	}
	return appUpdateCheck(ctx, ids, ch)
}

// UpgradeApp uses only the action remembered by the Go update check.
func (Cleaner) UpgradeApp(ctx context.Context, id string) (AppUpgradeResult, error) {
	if appUpdateUpgrade == nil {
		return AppUpgradeResult{ID: id, Code: "unsupported"}, nil
	}
	return appUpdateUpgrade(ctx, id), nil
}

// CleanAppCache delegates to the platform's cache cleanup implementation.
func (Cleaner) CleanAppCache(path string) UninstallResult {
	if appUpdateClean == nil {
		return UninstallResult{Path: path, Failed: []string{path + ": unsupported"}}
	}
	return appUpdateClean(path)
}

// ---- versions ---------------------------------------------------------------

// cleanVersion drops a leading v and everything from the first separator
// that ends the dotted part: "1.171.0 (89666)", "1.171.0,89666",
// "7.0.0_260812_01" and "3.0.0-beta.2" all keep their leading numbers.
func cleanVersion(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "v"), "V")
	if i := strings.IndexAny(s, "-+_,( "); i >= 0 {
		s = s[:i]
	}
	return s
}

// versionParts splits a cleaned version into numbers; nil when any part is
// not a plain number, so it cannot be compared.
func versionParts(s string) []int {
	if s == "" {
		return nil
	}
	fields := strings.Split(s, ".")
	parts := make([]int, 0, len(fields))
	for _, f := range fields {
		if f == "" || strings.Trim(f, "0123456789") != "" {
			return nil
		}
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return nil
		}
		parts = append(parts, n)
	}
	return parts
}

// updateState compares the bundle's version with the source's. Versions
// that cannot be compared are never reported as an update.
func updateState(current, latest string) (state, reason string) {
	c, l := versionParts(cleanVersion(current)), versionParts(cleanVersion(latest))
	if c == nil || l == nil {
		return "unknown", "uncomparable"
	}
	if compareVersions(l, c) > 0 {
		return "update", ""
	}
	return "current", ""
}

// caskState compares a bundle with its Homebrew cask, whose version can be
// "version,build" (4.1.15.55,270135). It is updateState of the short
// versions, unless the cask's version only adds components to the bundle's
// short version (4.1.15). The short version cannot tell those releases apart,
// so CFBundleVersion decides, as for Sparkle feeds:
//   - a build that extends the short version (16.69.23010700), against the
//     cask's dotted version;
//   - any other build (270135), against the cask's part after the comma.
//
// With no build to compare on either side, the bundle is current.
func caskState(short, build, cask string) (state, reason string) {
	s, l := versionParts(cleanVersion(short)), versionParts(cleanVersion(cask))
	if s == nil || !extendsVersion(l, s) {
		return updateState(short, cask)
	}
	b := versionParts(cleanVersion(build))
	var against []int
	switch {
	case b == nil, compareVersions(b, s) == 0: // no build, or one that says no more than the short version
	case extendsVersion(b, s):
		against = l
	default:
		_, after, _ := strings.Cut(strings.TrimSpace(cask), ",")
		against = versionParts(cleanVersion(after))
	}
	if against != nil && compareVersions(against, b) > 0 {
		return "update", ""
	}
	return "current", ""
}

// extendsVersion reports whether v is base followed by more components.
func extendsVersion(v, base []int) bool {
	return len(v) > len(base) && compareVersions(v[:len(base)], base) == 0
}

// ---- fetching feeds -----------------------------------------------------------

const (
	appFeedLimit     = 2 << 20
	appFeedTimeout   = 10 * time.Second
	appFeedRedirects = 5
)

var (
	errFeedURL      = errors.New("bad-url")
	errFeedRate     = errors.New("rate-limited")
	errFeedBad      = errors.New("bad-feed")
	errFeedNotFound = errors.New("not-found")
)

// feedURLOK reports whether a feed may be fetched: https, a public host
// name or address, no credentials.
func feedURLOK(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.Contains(host, "%") {
		return false
	}
	// Reject raw Unicode hosts before the transport can normalize them via IDNA.
	for _, r := range host {
		if r > 127 {
			return false
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsMulticast())
	}
	return true
}

// newAppUpdateClient checks every redirect hop with ok.
func newAppUpdateClient(rt http.RoundTripper, ok func(string) bool) *http.Client {
	return &http.Client{
		Transport: rt,
		Timeout:   appFeedTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > appFeedRedirects || !ok(req.URL.String()) {
				return errFeedURL
			}
			return nil
		},
		Jar: nil,
	}
}

// appUpdateHTTP fetches update feeds; tests replace it.
var appUpdateHTTP = newAppUpdateClient(http.DefaultTransport, feedURLOK)

// fetchFeed reads at most appFeedLimit bytes from a checked https URL.
func fetchFeed(ctx context.Context, raw string) ([]byte, error) {
	return fetchFeedWith(ctx, raw, feedURLOK)
}

// fetchFeedWith is fetchFeed with the first hop's check injected, so tests
// can use a local TLS server.
func fetchFeedWith(ctx context.Context, raw string, ok func(string) bool) ([]byte, error) {
	if !ok(raw) {
		return nil, errFeedURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(raw), nil)
	if err != nil {
		return nil, errFeedURL
	}
	req.Header.Set("User-Agent", "Cleanzo")
	resp, err := appUpdateHTTP.Do(req)
	if err != nil {
		if errors.Is(err, errFeedURL) {
			return nil, errFeedURL
		}
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errFeedNotFound
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return nil, errFeedRate
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: status %d", errFeedBad, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, appFeedLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > appFeedLimit {
		return nil, errFeedBad
	}
	return data, nil
}

// feedReason turns a fetch error into the reason code the page translates.
func feedReason(err error) string {
	switch {
	case errors.Is(err, errFeedURL):
		return "bad-url"
	case errors.Is(err, errFeedRate):
		return "rate-limited"
	case errors.Is(err, errFeedNotFound):
		return "not-found"
	case errors.Is(err, errFeedBad):
		return "bad-feed"
	default:
		return "network"
	}
}

// ---- completed per-app actions ------------------------------------------------

type pendingAppAction struct {
	id         string
	generation uint64
	action     appUpdateAction
}

// m holds successfully delivered actions, including empty consumed results.
// Pending sends are separately visible to immediate UpgradeApp calls. A failed
// send never becomes another send's fallback or changes a committed identity.
var appUpdateCache = struct {
	sync.Mutex
	m          map[string]appUpdateAction
	pending    map[uint64]*pendingAppAction
	generation uint64
	next       uint64
}{m: map[string]appUpdateAction{}, pending: map[uint64]*pendingAppAction{}}

func beginAppUpdateCheck() uint64 {
	appUpdateCache.Lock()
	defer appUpdateCache.Unlock()
	appUpdateCache.generation++
	return appUpdateCache.generation
}

func nextAppActionLocked(a appUpdateAction) appUpdateAction {
	appUpdateCache.next++
	a.revision = appUpdateCache.next
	return a
}

func rememberAppAction(id string, a appUpdateAction) {
	appUpdateCache.Lock()
	defer appUpdateCache.Unlock()
	appUpdateCache.m[id] = nextAppActionLocked(a)
}

// publishAppUpdate makes the action available before Send, so a received update
// can immediately invoke UpgradeApp. Failed delivery discards only its pending
// result. Never hold the cache lock across Send: it may wait on the page.
func publishAppUpdate(ctx context.Context, generation uint64, u AppUpdate, a appUpdateAction, send func(AppUpdate) error) error {
	appUpdateCache.Lock()
	if err := ctx.Err(); err != nil {
		appUpdateCache.Unlock()
		return err
	}
	if generation != appUpdateCache.generation {
		appUpdateCache.Unlock()
		return context.Canceled
	}
	var pending *pendingAppAction
	if u.ID != "__done" {
		a = nextAppActionLocked(a)
		pending = &pendingAppAction{id: u.ID, generation: generation, action: a}
		appUpdateCache.pending[a.revision] = pending
	}
	appUpdateCache.Unlock()
	err := send(u)
	if pending != nil {
		appUpdateCache.Lock()
		delete(appUpdateCache.pending, a.revision)
		if err == nil && generation == appUpdateCache.generation && a.revision > appUpdateCache.m[u.ID].revision {
			appUpdateCache.m[u.ID] = pending.action
		}
		appUpdateCache.Unlock()
	}
	return err
}

func appActionFor(id string) (appUpdateAction, bool) {
	appUpdateCache.Lock()
	defer appUpdateCache.Unlock()
	a := appUpdateCache.m[id]
	for _, pending := range appUpdateCache.pending {
		if pending.id == id && pending.generation == appUpdateCache.generation && pending.action.revision > a.revision {
			a = pending.action
		}
	}
	return a, a.kind != ""
}

func resetAppActions() {
	appUpdateCache.Lock()
	defer appUpdateCache.Unlock()
	appUpdateCache.generation++
	appUpdateCache.m = map[string]appUpdateAction{}
	appUpdateCache.pending = map[uint64]*pendingAppAction{}
}

// probeUsageFn is probeUsage's implementation; tests replace it.
var probeUsageFn = platformUsage

func forgetAppAction(id string, revision uint64) {
	appUpdateCache.Lock()
	defer appUpdateCache.Unlock()
	a := appUpdateCache.m[id]
	// The upgrade may finish after its pending send has already failed and
	// been removed. Its revision still consumes any older committed fallback,
	// while a successfully delivered newer result remains intact.
	if a.revision <= revision {
		appUpdateCache.m[id] = appUpdateAction{revision: revision}
	}
	if pending := appUpdateCache.pending[revision]; pending != nil && pending.id == id {
		pending.action = appUpdateAction{revision: revision}
	}
}
