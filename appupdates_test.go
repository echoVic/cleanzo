package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rememberAppAction commits a as the delivered action for id, as a check
// whose send succeeded does.
func rememberAppAction(id string, a appUpdateAction) {
	appUpdateCache.Lock()
	defer appUpdateCache.Unlock()
	appUpdateCache.m[id] = nextAppActionLocked(a)
}

// resetAppActions forgets every action and ends the running check.
func resetAppActions() {
	appUpdateCache.Lock()
	defer appUpdateCache.Unlock()
	appUpdateCache.generation++
	appUpdateCache.m = map[string]appUpdateAction{}
	appUpdateCache.pending = map[uint64]*pendingAppAction{}
}

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

func TestCaskState(t *testing.T) {
	cases := []struct{ short, build, cask, state, reason string }{
		// The cask only adds components to the short version, and the bundle's
		// build compares with the cask's: CFBundleVersion decides.
		{"4.1.15", "270135", "4.1.15.55,270135", "current", ""},        // WeChat, its build after the comma
		{"4.1.15", "270135", "4.1.15.60,270200", "update", ""},         // a newer build of the same short version
		{"16.69", "16.69.23010700", "16.69.23010700", "current", ""},   // Office: a build that is the full dotted version
		{"16.69", "16.69.23010700", "16.69.23011000", "update", ""},    // and a newer one
		{"16.69", "16.69.23011000", "16.69.23010700", "current", ""},   // the bundle is newer
		{"6.4.6 (56145)", "6.4.6.56145", "6.4.6.56145", "current", ""}, // Zoom: the short version names the build too
		{"6.4.6 (56145)", "6.4.6.56145", "6.4.6.56200", "update", ""},  // and a newer one
		{"133.0", "133.0.1", "133.0.3", "update", ""},                  // a build that extends the short version, older
		// Otherwise the short versions compare, as for every other source:
		// X.Y to X.Y.Z is an update whatever the bundle's build says, also
		// when the cask has a build that the bundle's cannot be compared with.
		{"4.1.15", "", "4.1.15.55,270135", "update", ""},                       // no build in the bundle
		{"4.1.15", "4.1.15", "4.1.15.55,270135", "update", ""},                 // a build that says no more than the short version
		{"4.1.15", "270135", "4.1.15.55,abc", "update", ""},                    // a cask build that is not a number
		{"4.1.15", "270135", "4.1.15.55,", "update", ""},                       // an empty cask build is none
		{"2024.3", "IU-243.21565.193", "2024.3.1,243.22562.145", "update", ""}, // JetBrains: a build that is not a number
		{"2.0", "2.0", "2.0.1,a1b2c3", "update", ""},
		{"2.0", "100", "2.0.1,a1b2c3", "update", ""},
		{"2.0", "100", "2.0.1", "update", ""},
		{"2.0", "2.0", "2.0.1", "update", ""},
		{"2.0", "", "2.0.1", "update", ""},
		{"0.10.3", "0.10.3", "0.10.3.1", "update", ""},
		{"133.0", "13324.11.25", "133.0.3", "update", ""},
		{"133.0", "", "133.0.3", "update", ""},
		{"133.0", "133.0", "133.0.3", "update", ""},
		{"156.0.8078.76", "8078.76", "156.0.8078.80", "update", ""},
		{"4.1.15", "270135", "4.1.16.10,271000", "update", ""},
		{"4.1.16", "271000", "4.1.15.55,270135", "current", ""},
		{"7.0.0", "52194", "7.0.0_260812_01,9.9.33,126b7ce6", "current", ""}, // QQ
		{"1.2", "45", "1.2.0", "current", ""},
		{"1.0", "", "nightly", "unknown", "uncomparable"},
		{"", "1", "1.0", "unknown", "uncomparable"},
	}
	for _, c := range cases {
		s, r := caskState(c.short, c.build, c.cask)
		if s != c.state || r != c.reason {
			t.Errorf("caskState(%q, %q, %q) = %q/%q, want %q/%q", c.short, c.build, c.cask, s, r, c.state, c.reason)
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

func TestAppActionFailedOldDeliveryCannotRollbackFreshResult(t *testing.T) {
	for _, freshKind := range []string{"brew", ""} {
		t.Run(freshKind, func(t *testing.T) {
			resetAppActions()
			t.Cleanup(resetAppActions)
			id := "/Applications/Overlap.app"
			rememberAppAction(id, appUpdateAction{kind: "open-store", url: "macappstore://old"})
			generation := beginAppUpdateCheck()
			started, release := make(chan struct{}), make(chan struct{})
			closed := errors.New("channel closed")
			done := make(chan error, 1)
			go func() {
				done <- publishAppUpdate(context.Background(), generation, AppUpdate{ID: id}, appUpdateAction{kind: "open-app"}, func(AppUpdate) error {
					close(started)
					<-release
					return closed
				})
			}()
			<-started
			fresh := beginAppUpdateCheck()
			err := publishAppUpdate(context.Background(), fresh, AppUpdate{ID: id}, appUpdateAction{kind: freshKind, token: "fresh"}, func(AppUpdate) error { return nil })
			close(release)
			oldErr := <-done
			if err != nil || !errors.Is(oldErr, closed) {
				t.Fatalf("fresh=%v old=%v", err, oldErr)
			}
			a, ok := appActionFor(id)
			if freshKind == "" && ok || freshKind != "" && (!ok || a.kind != "brew" || a.token != "fresh") {
				t.Fatalf("old send rolled back fresh result: %+v %v", a, ok)
			}
		})
	}
}

func TestAppActionFailedDeliveryCannotResurrectConsumedAction(t *testing.T) {
	resetAppActions()
	t.Cleanup(resetAppActions)
	id := "/Applications/Consumed.app"
	rememberAppAction(id, appUpdateAction{kind: "brew", token: "old"})
	generation := beginAppUpdateCheck()
	closed := errors.New("channel closed")
	err := publishAppUpdate(context.Background(), generation, AppUpdate{ID: id}, appUpdateAction{kind: "brew", token: "new"}, func(AppUpdate) error {
		a, _ := appActionFor(id)
		forgetAppAction(id, a.revision)
		return closed
	})
	if !errors.Is(err, closed) {
		t.Fatal(err)
	}
	if a, ok := appActionFor(id); ok {
		t.Fatalf("failed send resurrected consumed action: %+v", a)
	}
}

func TestAppActionPendingConsumptionSurvivesFailedDelivery(t *testing.T) {
	for _, consumeFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("consume-first=%v", consumeFirst), func(t *testing.T) {
			resetAppActions()
			t.Cleanup(resetAppActions)
			id := "/Applications/PendingConsumed.app"
			rememberAppAction(id, appUpdateAction{kind: "brew", token: "committed"})
			generation := beginAppUpdateCheck()
			started, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			closed := errors.New("channel closed")
			go func() {
				done <- publishAppUpdate(context.Background(), generation, AppUpdate{ID: id}, appUpdateAction{kind: "brew", token: "pending"}, func(AppUpdate) error {
					close(started)
					<-release
					return closed
				})
			}()
			<-started
			consumed, ok := appActionFor(id)
			if !ok || consumed.token != "pending" {
				t.Errorf("upgrade did not capture pending action: %+v %v", consumed, ok)
			}
			if consumeFirst {
				forgetAppAction(id, consumed.revision)
			}
			close(release)
			if err := <-done; !errors.Is(err, closed) {
				t.Fatal(err)
			}
			if !consumeFirst {
				forgetAppAction(id, consumed.revision)
			}
			if a, ok := appActionFor(id); ok {
				t.Fatalf("failed send restored older action after pending upgrade completed: %+v", a)
			}
		})
	}
}

func TestAppActionOverlappingFailedDeliveriesRestoreCommittedAction(t *testing.T) {
	for _, pendingKind := range []string{"brew", ""} {
		for _, newestFailsFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("pending=%s/newest-first=%v", pendingKind, newestFailsFirst), func(t *testing.T) {
				resetAppActions()
				t.Cleanup(resetAppActions)
				id := "/Applications/DoubleFailure.app"
				rememberAppAction(id, appUpdateAction{kind: "brew", token: "committed"})
				committed, _ := appActionFor(id)
				startedB, releaseB := make(chan struct{}), make(chan struct{})
				startedC, releaseC := make(chan struct{}), make(chan struct{})
				doneB, doneC := make(chan error, 1), make(chan error, 1)
				closed := errors.New("channel closed")
				g1 := beginAppUpdateCheck()
				go func() {
					doneB <- publishAppUpdate(context.Background(), g1, AppUpdate{ID: id}, appUpdateAction{kind: pendingKind, token: "undelivered-b"}, func(AppUpdate) error {
						close(startedB)
						<-releaseB
						return closed
					})
				}()
				<-startedB
				g2 := beginAppUpdateCheck()
				go func() {
					doneC <- publishAppUpdate(context.Background(), g2, AppUpdate{ID: id}, appUpdateAction{kind: "brew", token: "undelivered-c"}, func(AppUpdate) error {
						close(startedC)
						<-releaseC
						return closed
					})
				}()
				<-startedC
				var errB, errC error
				if newestFailsFirst {
					close(releaseC)
					errC = <-doneC
					close(releaseB)
					errB = <-doneB
				} else {
					close(releaseB)
					errB = <-doneB
					close(releaseC)
					errC = <-doneC
				}
				if !errors.Is(errB, closed) || !errors.Is(errC, closed) {
					t.Fatalf("send errors: B=%v C=%v", errB, errC)
				}
				a, ok := appActionFor(id)
				if !ok || a.token != "committed" || a.revision != committed.revision {
					t.Fatalf("two failed sends did not restore original committed action: %+v %v; original revision=%d", a, ok, committed.revision)
				}
			})
		}
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
		case "/redirect-github":
			http.Redirect(w, r, "https://objects.githubusercontent.com/403", http.StatusFound)
		case "/redirect-elsewhere":
			http.Redirect(w, r, "https://example.com/403", http.StatusFound)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})
	saved := appUpdateHTTP
	appUpdateHTTP = newAppUpdateClient(appFeedTestTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		res := w.Result()
		res.Request = r // as http.Transport does
		return res, nil
	}), feedURLOK)
	t.Cleanup(func() { appUpdateHTTP = saved })
	for _, c := range []struct {
		url     string
		wantLen int
		wantErr error
	}{
		{"https://example.com/ok", 15, nil}, {"https://example.com/limit", 2 << 20, nil}, {"https://example.com/big", 0, errFeedBad},
		{"https://example.com/404", 0, errFeedNotFound}, {"https://example.com/429", 0, errFeedRate},
		{"https://example.com/500", 0, errFeedBad}, {"https://example.com/redirect-http", 0, errFeedURL},
		{"https://example.com/redirect-local", 0, errFeedURL},
		// Only GitHub answers a rate limit with 403; elsewhere it means the
		// feed is not there for us, as S3 answers a missing object.
		{"https://example.com/403", 0, errFeedNotFound}, {"https://notgithub.com/403", 0, errFeedNotFound},
		{"https://github.com.example.com/403", 0, errFeedNotFound},
		{"https://github.com/403", 0, errFeedRate}, {"https://api.github.com/403", 0, errFeedRate},
		{"https://objects.githubusercontent.com/403", 0, errFeedRate},
		{"https://notgithub.com/429", 0, errFeedRate},
	} {
		t.Run(c.url, func(t *testing.T) {
			before := hits
			b, err := fetchFeed(context.Background(), c.url)
			if !errors.Is(err, c.wantErr) || len(b) != c.wantLen {
				t.Fatalf("got %d bytes, %v; want %d bytes, %v", len(b), err, c.wantLen, c.wantErr)
			}
			if strings.HasSuffix(c.url, "/ok") && string(b) != "version: 1.0.2\n" {
				t.Fatalf("unexpected feed: %q", b)
			}
			if hits != before+1 {
				t.Fatalf("sent %d requests, want 1", hits-before)
			}
		})
	}
	// The host that answered 403 decides, not the one first asked.
	if _, err := fetchFeed(context.Background(), "https://example.com/redirect-github"); !errors.Is(err, errFeedRate) {
		t.Errorf("403 from GitHub after a redirect: %v, want rate-limited", err)
	}
	if _, err := fetchFeed(context.Background(), "https://github.com/redirect-elsewhere"); !errors.Is(err, errFeedNotFound) {
		t.Errorf("403 from another host after a GitHub redirect: %v, want not-found", err)
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
