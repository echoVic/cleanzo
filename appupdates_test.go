package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCleanVersion(t *testing.T) {
	for in, want := range map[string]string{
		"v1.2.3":                          "1.2.3",
		"V2.0":                            "2.0",
		"1.171.0,89666":                   "1.171.0",
		"1.171.0 (89666)":                 "1.171.0",
		"7.0.0_260812_01,9.9.33,126b7ce6": "7.0.0",
		"3.0.0-beta.2":                    "3.0.0",
		"1.2+build5":                      "1.2",
		"  4.1.15 ":                       "4.1.15",
		"":                                "",
	} {
		if got := cleanVersion(in); got != want {
			t.Errorf("cleanVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVersionParts(t *testing.T) {
	if got := versionParts("156.0.8078.80"); len(got) != 4 || got[3] != 80 {
		t.Fatalf("versionParts = %v", got)
	}
	for _, bad := range []string{"", "abc", "1.x", "1..2", "2026a"} {
		if versionParts(bad) != nil {
			t.Errorf("versionParts(%q) should be nil", bad)
		}
	}
}

func TestUpdateState(t *testing.T) {
	cases := []struct{ cur, latest, state, reason string }{
		{"156.0.8078.76", "156.0.8078.80", "update", ""},
		{"1.170.0", "1.171.0,89666", "update", ""},
		{"3.7.6", "3.7.6", "current", ""},
		{"1.2", "1.2.0", "current", ""},
		{"4.0.11", "3.8.3", "current", ""}, // The bundle is newer than the source (cc-switch).
		{"1.0", "nightly", "unknown", "uncomparable"},
		{"", "1.0", "unknown", "uncomparable"},
		{"1.0", "", "unknown", "uncomparable"},
	}
	for _, c := range cases {
		s, r := updateState(c.cur, c.latest)
		if s != c.state || r != c.reason {
			t.Errorf("updateState(%q, %q) = %q/%q, want %q/%q", c.cur, c.latest, s, r, c.state, c.reason)
		}
	}
}

func TestFeedURLOK(t *testing.T) {
	good := []string{"https://iterm2.com/appcasts/final_modern.xml", "https://github.com/a/b/releases/latest/download/latest-mac.yml"}
	bad := []string{"", "  ", "http://iterm2.com/x.xml", "https://127.0.0.1/x", "http://0.0.0.0:8000/MacApp.xml",
		"https://0.0.0.0/x", "https://localhost/x", "https://foo.localhost/x", "https://10.1.2.3/x", "https://192.168.1.1/x",
		"https://[::1]/x", "https://169.254.1.1/x", "file:///etc/passwd", "https://user:pw@example.com/x", "''"}
	for _, u := range good {
		if !feedURLOK(u) {
			t.Errorf("feedURLOK(%q) = false", u)
		}
	}
	for _, u := range bad {
		if feedURLOK(u) {
			t.Errorf("feedURLOK(%q) = true", u)
		}
	}
}

// The TLS test server uses loopback, which feedURLOK rejects.
// fetchFeedWith bypasses the first URL check while keeping the remaining logic.
func TestFetchFeedLimitsAndErrors(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("User-Agent") != "Cleanzo" || r.Header.Get("Cookie") != "" {
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("version: 1.0.2\n"))
		case "/big":
			w.Write([]byte(strings.Repeat("x", 2<<20+1)))
		case "/404":
			w.WriteHeader(404)
		case "/403":
			w.WriteHeader(403)
		case "/429":
			w.WriteHeader(429)
		case "/redirect":
			http.Redirect(w, r, "http://example.com/plain", http.StatusFound)
		}
	}))
	defer srv.Close()
	saved := appUpdateHTTP
	appUpdateHTTP = newAppUpdateClient(srv.Client().Transport, func(string) bool { return true })
	defer func() { appUpdateHTTP = saved }()
	allow := func(string) bool { return true }

	if b, err := fetchFeedWith(context.Background(), srv.URL+"/ok", allow); err != nil || string(b) != "version: 1.0.2\n" {
		t.Fatalf("ok: %q %v", b, err)
	}
	if _, err := fetchFeedWith(context.Background(), srv.URL+"/big", allow); !errors.Is(err, errFeedBad) {
		t.Fatalf("big: %v", err)
	}
	if _, err := fetchFeedWith(context.Background(), srv.URL+"/404", allow); !errors.Is(err, errFeedNotFound) {
		t.Fatalf("404: %v", err)
	}
	for _, p := range []string{"/403", "/429"} {
		if _, err := fetchFeedWith(context.Background(), srv.URL+p, allow); !errors.Is(err, errFeedRate) {
			t.Fatalf("%s: %v", p, err)
		}
	}
	// The redirect to HTTP must fail the next hop's URL check.
	appUpdateHTTP = newAppUpdateClient(srv.Client().Transport, feedURLOK)
	if _, err := fetchFeedWith(context.Background(), srv.URL+"/redirect", allow); !errors.Is(err, errFeedURL) {
		t.Fatalf("redirect: %v", err)
	}
	// Invalid URLs must not send requests.
	before := hits.Load()
	if _, err := fetchFeed(context.Background(), "http://0.0.0.0:8000/MacApp.xml"); !errors.Is(err, errFeedURL) {
		t.Fatalf("bad url: %v", err)
	}
	if hits.Load() != before {
		t.Fatal("bad url reached the server")
	}
	if feedReason(errFeedURL) != "bad-url" || feedReason(errFeedRate) != "rate-limited" || feedReason(errFeedBad) != "bad-feed" ||
		feedReason(errFeedNotFound) != "not-found" || feedReason(errors.New("dial")) != "network" {
		t.Fatal("feedReason mapping")
	}
}

func TestAppActionCacheOnlyHoldsWhatGoRemembered(t *testing.T) {
	resetAppActions()
	if _, ok := appActionFor("/Applications/X.app"); ok {
		t.Fatal("empty cache returned an action")
	}
	rememberAppAction("/Applications/X.app", appUpdateAction{kind: "brew", token: "x"})
	a, ok := appActionFor("/Applications/X.app")
	if !ok || a.kind != "brew" || a.token != "x" {
		t.Fatalf("got %+v %v", a, ok)
	}
	resetAppActions()
	if _, ok := appActionFor("/Applications/X.app"); ok {
		t.Fatal("reset kept the action")
	}
}

func TestVersionPartsRejectsSignedSegments(t *testing.T) {
	for _, version := range []string{"+1.2", "1.+2", "-0.2", "1.-0"} {
		if got := versionParts(version); got != nil {
			t.Errorf("versionParts(%q) = %v, want nil for non-digit segments", version, got)
		}
	}
}

func TestFeedURLOKRejectsLocalHostVariants(t *testing.T) {
	for _, raw := range []string{
		"https://localhost./feed", "https://foo.localhost./feed",
		"https://127.0.0.1./feed", "https://10.1.2.3./feed",
		"https://[::1%25lo0]/feed", "https://[fe80::1%25en0]/feed",
	} {
		if feedURLOK(raw) {
			t.Errorf("feedURLOK(%q) accepted a local host", raw)
		}
	}
}

func TestFeedURLOKRejectsUnicodeHosts(t *testing.T) {
	for _, raw := range []string{
		"https://127。0。0。1/feed", "https://10．1．2．3/feed",
		"https://ｌｏｃａｌｈｏｓｔ/feed", "https://foo。localhost/feed",
	} {
		if feedURLOK(raw) {
			t.Errorf("feedURLOK(%q) accepted a host that IDNA normalizes to a local address", raw)
		}
	}
}

type appFeedTestTransport func(*http.Request) (*http.Response, error)

func (f appFeedTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// These tests exercise the real client and feed reader without opening sockets.
func TestFetchFeedInMemory(t *testing.T) {
	var hits int
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("User-Agent") != "Cleanzo" || r.Header.Get("Cookie") != "" {
			t.Errorf("unexpected request headers: %v", r.Header)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/ok":
			fmt.Fprint(w, "version: 1.0.2\n")
		case "/limit":
			fmt.Fprint(w, strings.Repeat("x", 2<<20))
		case "/big":
			fmt.Fprint(w, strings.Repeat("x", (2<<20)+1))
		case "/404":
			w.WriteHeader(http.StatusNotFound)
		case "/403":
			w.WriteHeader(http.StatusForbidden)
		case "/429":
			w.WriteHeader(http.StatusTooManyRequests)
		case "/500":
			w.WriteHeader(http.StatusInternalServerError)
		case "/redirect-http":
			http.Redirect(w, r, "http://example.com/plain", http.StatusFound)
		case "/redirect-local":
			http.Redirect(w, r, "https://127.0.0.1/feed", http.StatusFound)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})
	saved := appUpdateHTTP
	appUpdateHTTP = newAppUpdateClient(appFeedTestTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Result(), nil
	}), feedURLOK)
	t.Cleanup(func() { appUpdateHTTP = saved })
	for _, c := range []struct {
		path    string
		wantLen int
		wantErr error
	}{
		{"/ok", 15, nil}, {"/limit", 2 << 20, nil}, {"/big", 0, errFeedBad},
		{"/404", 0, errFeedNotFound}, {"/403", 0, errFeedRate}, {"/429", 0, errFeedRate},
		{"/500", 0, errFeedBad}, {"/redirect-http", 0, errFeedURL}, {"/redirect-local", 0, errFeedURL},
	} {
		t.Run(c.path, func(t *testing.T) {
			before := hits
			b, err := fetchFeed(context.Background(), "https://example.com"+c.path)
			if !errors.Is(err, c.wantErr) || len(b) != c.wantLen {
				t.Fatalf("got %d bytes, %v; want %d bytes, %v", len(b), err, c.wantLen, c.wantErr)
			}
			if c.path == "/ok" && string(b) != "version: 1.0.2\n" {
				t.Fatalf("unexpected feed: %q", b)
			}
			if hits != before+1 {
				t.Fatalf("sent %d requests, want 1", hits-before)
			}
		})
	}
	before := hits
	for _, raw := range []string{"", "http://example.com/feed", "https://localhost/feed", "https://127.0.0.1/feed"} {
		if _, err := fetchFeed(context.Background(), raw); !errors.Is(err, errFeedURL) {
			t.Errorf("fetchFeed(%q): %v", raw, err)
		}
	}
	if hits != before {
		t.Fatal("invalid URL reached the transport")
	}
	for _, c := range []struct {
		err  error
		want string
	}{
		{errFeedURL, "bad-url"}, {errFeedRate, "rate-limited"}, {errFeedBad, "bad-feed"},
		{errFeedNotFound, "not-found"}, {errors.New("dial"), "network"},
	} {
		if got := feedReason(fmt.Errorf("wrapped: %w", c.err)); got != c.want {
			t.Errorf("feedReason(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestFetchFeedAllowsFiveRedirects(t *testing.T) {
	var hits int
	saved := appUpdateHTTP
	appUpdateHTTP = newAppUpdateClient(appFeedTestTransport(func(r *http.Request) (*http.Response, error) {
		hits++
		remaining, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		if remaining > 0 {
			http.Redirect(w, r, fmt.Sprintf("/%d", remaining-1), http.StatusFound)
		} else {
			fmt.Fprint(w, "version: 1.0.2\n")
		}
		return w.Result(), nil
	}), feedURLOK)
	t.Cleanup(func() { appUpdateHTTP = saved })
	b, err := fetchFeed(context.Background(), "https://example.com/5")
	if err != nil || string(b) != "version: 1.0.2\n" || hits != 6 {
		t.Errorf("five redirects: %q, %v, %d requests; want a feed after 6 requests", b, err, hits)
	}
	hits = 0
	if _, err := fetchFeed(context.Background(), "https://example.com/6"); !errors.Is(err, errFeedURL) || hits != 6 {
		t.Errorf("six redirects: %v, %d requests; want bad-url after 6 requests", err, hits)
	}
}

func TestFetchFeedTimeoutAndCancellation(t *testing.T) {
	saved := appUpdateHTTP
	appUpdateHTTP = newAppUpdateClient(appFeedTestTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	}), feedURLOK)
	appUpdateHTTP.Timeout = 20 * time.Millisecond
	t.Cleanup(func() { appUpdateHTTP = saved })
	if _, err := fetchFeed(context.Background(), "https://example.com/slow"); !errors.Is(err, context.DeadlineExceeded) || feedReason(err) != "network" {
		t.Fatalf("timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetchFeed(ctx, "https://example.com/slow"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}
