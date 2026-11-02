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
	kind   string // "brew" | "open-store" | "open-app"
	token  string // brew cask token
	path   string // the app bundle
	url    string // macappstore:// link
	latest string // what the check found, to confirm a brew upgrade
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

// ---- the last check's actions --------------------------------------------------

// appUpdateCache keeps what the last check found each app's Update button
// should do. UpgradeApp only acts on what is here.
var appUpdateCache = struct {
	sync.Mutex
	m map[string]appUpdateAction
}{m: map[string]appUpdateAction{}}

func rememberAppAction(id string, a appUpdateAction) {
	appUpdateCache.Lock()
	defer appUpdateCache.Unlock()
	appUpdateCache.m[id] = a
}

func appActionFor(id string) (appUpdateAction, bool) {
	appUpdateCache.Lock()
	defer appUpdateCache.Unlock()
	a, ok := appUpdateCache.m[id]
	return a, ok
}

func resetAppActions() {
	appUpdateCache.Lock()
	defer appUpdateCache.Unlock()
	appUpdateCache.m = map[string]appUpdateAction{}
}

// probeUsageFn is probeUsage's implementation; tests replace it.
var probeUsageFn = platformUsage

func forgetAppAction(id string) {
	appUpdateCache.Lock()
	defer appUpdateCache.Unlock()
	delete(appUpdateCache.m, id)
}
