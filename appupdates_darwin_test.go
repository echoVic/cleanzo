package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// lock takes m without giving up, as a test holding brew busy does.
func (m appBrewLock) lock() { m <- struct{}{} }

// iTerm2-style: two items, the newer one needs a newer system.
const testAppcastITerm = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle">
<channel>
<item><title>Version 3.6.11</title>
<sparkle:releaseNotesLink>
    https://iterm2.com/appcasts/full_changes.txt
</sparkle:releaseNotesLink>
<sparkle:minimumSystemVersion>12.4</sparkle:minimumSystemVersion>
<enclosure url="https://iterm2.com/downloads/stable/iTerm2-3_6_11.zip" sparkle:version="3.6.11" length="1" type="application/octet-stream"/></item>
<item><title>Version 3.7.6</title>
<sparkle:minimumSystemVersion>13</sparkle:minimumSystemVersion>
<enclosure url="https://iterm2.com/downloads/stable/iTerm2-3_7_6.zip" sparkle:version="3.7.6" length="1" type="application/octet-stream"/></item>
<item><title>Version 9.0.0 beta</title><sparkle:channel>beta</sparkle:channel>
<enclosure url="https://x/y.zip" sparkle:version="9.0.0"/></item>
</channel></rss>`

// Arc-style: the build number is sparkle:version; the display version has parentheses.
const testAppcastArc = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel>
<item><sparkle:version>89666</sparkle:version><sparkle:shortVersionString>1.171.0 (89666)</sparkle:shortVersionString>
<sparkle:minimumSystemVersion>13.0.0</sparkle:minimumSystemVersion>
<enclosure url="https://releases.arc.net/release/Arc-1.171.0-89666.zip" sparkle:version="89666"/></item>
<item><sparkle:version>89123</sparkle:version><sparkle:shortVersionString>1.170.0 (89123)</sparkle:shortVersionString>
<enclosure url="https://releases.arc.net/release/Arc-1.170.0-89123.zip" sparkle:version="89123"/></item>
</channel></rss>`

const testBrewInfo = `{"formulae":[],"casks":[
 {"token":"google-chrome","version":"156.0.8078.80","installed":"155.0.8059.85","artifacts":[{"app":["Google Chrome.app"]},{"zap":[]}]},
 {"token":"qq","version":"7.0.0_260812_01,9.9.33,126b7ce6","installed":"x","artifacts":[{"app":["QQ.app"]}]},
 {"token":"renamed","version":"2.0","installed":"1.0","artifacts":[{"app":["Orig.app",{"target":"Renamed.app"}]}]}
]}`

const testITunes = `{"resultCount":1,"results":[{"kind":"mac-software","trackId":836500024,"version":"4.2.0",
 "trackViewUrl":"https://apps.apple.com/cn/app/wechat/id836500024?mt=12","bundleId":"com.tencent.xinWeChat"}]}`

func TestParseAppcast(t *testing.T) {
	v, short, notes, ok := parseAppcast([]byte(testAppcastITerm), "26.6.2")
	if !ok || v != "3.7.6" || short != "3.7.6" || notes != "" {
		t.Fatalf("iTerm on 26: %q %q %q %v", v, short, notes, ok)
	}
	v, _, notes, ok = parseAppcast([]byte(testAppcastITerm), "12.6")
	if !ok || v != "3.6.11" || notes != "https://iterm2.com/appcasts/full_changes.txt" {
		t.Fatalf("iTerm on 12.6: %q %q %v", v, notes, ok)
	}
	v, short, _, ok = parseAppcast([]byte(testAppcastArc), "26.6.2")
	if !ok || v != "89666" || short != "1.171.0 (89666)" {
		t.Fatalf("Arc: %q %q %v", v, short, ok)
	}
	if _, _, _, ok := parseAppcast([]byte("<html>nope</html>"), "26"); ok {
		t.Fatal("html parsed as appcast")
	}
}

func TestParseAppcastReleaseNotesFallback(t *testing.T) {
	for _, tc := range []struct {
		name, links, want string
	}{
		{"full only", `<sparkle:fullReleaseNotesLink> https://github.com/manaflow-ai/cmux/releases/tag/v0.65.2.2 </sparkle:fullReleaseNotesLink>`, "https://github.com/manaflow-ai/cmux/releases/tag/v0.65.2.2"},
		{"prefer release", `<sparkle:releaseNotesLink>https://example.com/notes</sparkle:releaseNotesLink><sparkle:fullReleaseNotesLink>https://example.com/full</sparkle:fullReleaseNotesLink>`, "https://example.com/notes"},
		{"empty release falls back", `<sparkle:releaseNotesLink> </sparkle:releaseNotesLink><sparkle:fullReleaseNotesLink>https://example.com/full</sparkle:fullReleaseNotesLink>`, "https://example.com/full"},
		{"invalid release falls back", `<sparkle:releaseNotesLink>http://example.com/notes</sparkle:releaseNotesLink><sparkle:fullReleaseNotesLink>https://example.com/full</sparkle:fullReleaseNotesLink>`, "https://example.com/full"},
		{"http full rejected", `<sparkle:fullReleaseNotesLink>http://example.com/full</sparkle:fullReleaseNotesLink>`, ""},
		{"private full rejected", `<sparkle:fullReleaseNotesLink>https://127.0.0.1/full</sparkle:fullReleaseNotesLink>`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			feed := `<rss xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"><channel><item><sparkle:version>113</sparkle:version><sparkle:shortVersionString>0.65.2</sparkle:shortVersionString>` + tc.links + `</item></channel></rss>`
			v, short, notes, ok := parseAppcast([]byte(feed), "26.6.2")
			if !ok || v != "113" || short != "0.65.2" || notes != tc.want {
				t.Fatalf("got %q %q %q %v, want 113 / 0.65.2 / %q / true", v, short, notes, ok, tc.want)
			}
		})
	}
}

func TestParseYAMLScalars(t *testing.T) {
	m := parseYAMLScalars([]byte("owner: Molunerfinn\nrepo: PicGo\nprovider: github\nupdaterCacheDirName: picgo-updater\n"))
	if m["owner"] != "Molunerfinn" || m["repo"] != "PicGo" || m["provider"] != "github" {
		t.Fatalf("%v", m)
	}
	m = parseYAMLScalars([]byte("provider: generic\nurl: 'https://download.example.com/feeds/'\nchannel: \"beta\"\n"))
	if m["url"] != "https://download.example.com/feeds/" || m["channel"] != "beta" {
		t.Fatalf("%v", m)
	}
	m = parseYAMLScalars([]byte("version: 1.0.2\nfiles:\n  - url: Mixoul-1.0.2-arm64.zip\n    size: 1\npath: Mixoul-1.0.2-arm64.zip\n"))
	if m["version"] != "1.0.2" || m["url"] != "" || m["path"] != "Mixoul-1.0.2-arm64.zip" {
		t.Fatalf("nested keys must not leak: %v", m)
	}
	if m := parseYAMLScalars([]byte("url: ''\n")); m["url"] != "" {
		t.Fatalf("empty quoted: %v", m)
	}
}

func TestParseBrewCasks(t *testing.T) {
	casks := parseBrewCasks([]byte(testBrewInfo))
	if len(casks) != 3 || casks[0].token != "google-chrome" || casks[0].apps[0] != "Google Chrome.app" {
		t.Fatalf("%+v", casks)
	}
	if casks[2].apps[0] != "Renamed.app" {
		t.Fatalf("target rename: %+v", casks[2])
	}
	if parseBrewCasks([]byte("not json")) != nil {
		t.Fatal("bad json")
	}
}

func TestParseITunesLookup(t *testing.T) {
	v, id, notes, ok := parseITunesLookup([]byte(testITunes))
	if !ok || v != "4.2.0" || id != 836500024 || !strings.HasPrefix(notes, "https://apps.apple.com/") {
		t.Fatalf("%q %d %q %v", v, id, notes, ok)
	}
	if _, _, _, ok := parseITunesLookup([]byte(`{"resultCount":0,"results":[]}`)); ok {
		t.Fatal("empty lookup ok")
	}
}

// fakeBundle makes an .app under dir: Info.plist keys, an optional MAS receipt and app-update.yml.
func fakeBundle(t *testing.T, dir, name string, plist map[string]string, mas bool, updateYML string) string {
	t.Helper()
	app := filepath.Join(dir, name+".app")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>`)
	for k, v := range plist {
		b.WriteString("<key>" + k + "</key><string>" + v + "</string>")
	}
	b.WriteString("</dict></plist>")
	writeText(t, filepath.Join(app, "Contents", "Info.plist"), b.String())
	if mas {
		writeText(t, filepath.Join(app, "Contents", "_MASReceipt", "receipt"), "r")
	}
	if updateYML != "" {
		writeText(t, filepath.Join(app, "Contents", "Resources", "app-update.yml"), updateYML)
	}
	return app
}

func writeText(t *testing.T, p, s string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testSources(feeds map[string]string, casks []brewCask) (appUpdateSources, *[]string) {
	var mu sync.Mutex
	var asked []string
	return appUpdateSources{
		brew: func(context.Context) []brewCask { return casks },
		fetch: func(_ context.Context, u string) ([]byte, error) {
			mu.Lock()
			asked = append(asked, u)
			mu.Unlock()
			if !feedURLOK(u) {
				return nil, errFeedURL
			}
			if s, ok := feeds[u]; ok {
				return []byte(s), nil
			}
			return nil, errFeedNotFound
		},
		region:    "cn",
		osVersion: "26.6.2",
	}, &asked
}

func TestCheckTargetSources(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	apps := filepath.Join(home, "Applications")
	chrome := fakeBundle(t, apps, "Google Chrome", map[string]string{"CFBundleIdentifier": "com.google.Chrome", "CFBundleShortVersionString": "156.0.8078.76", "SUFeedURL": "https://should-not-be-used/x.xml"}, false, "")
	iterm := fakeBundle(t, apps, "iTerm", map[string]string{"CFBundleIdentifier": "com.googlecode.iterm2", "CFBundleShortVersionString": "3.7.6", "CFBundleVersion": "3.7.6", "SUFeedURL": "https://iterm2.com/appcasts/final_modern.xml"}, false, "")
	arc := fakeBundle(t, apps, "Arc", map[string]string{"CFBundleIdentifier": "company.thebrowser.Browser", "CFBundleShortVersionString": "1.170.0", "CFBundleVersion": "89123", "SUFeedURL": "https://releases.arc.net/updates.xml"}, false, "")
	ima := fakeBundle(t, apps, "ima.copilot", map[string]string{"CFBundleShortVersionString": "1.0", "SUFeedURL": "http://0.0.0.0:8000/MacApp.xml"}, false, "")
	picgo := fakeBundle(t, apps, "PicGo", map[string]string{"CFBundleShortVersionString": "3.0.2"}, false, "owner: Molunerfinn\nrepo: PicGo\nprovider: github\n")
	gen := fakeBundle(t, apps, "Mixoul", map[string]string{"CFBundleShortVersionString": "1.0.2"}, false, "provider: generic\nurl: https://lf3.example.com/releases\n")
	trae := fakeBundle(t, apps, "Trae CN", map[string]string{"CFBundleShortVersionString": "1.0"}, false, "provider: custom\nurl: ''\n")
	wechat := fakeBundle(t, apps, "WeChat", map[string]string{"CFBundleIdentifier": "com.tencent.xinWeChat", "CFBundleShortVersionString": "4.1.15"}, true, "")
	plain := fakeBundle(t, apps, "Plain", map[string]string{"CFBundleShortVersionString": "1.0"}, false, "")

	src, asked := testSources(map[string]string{
		"https://iterm2.com/appcasts/final_modern.xml":                                                 testAppcastITerm,
		"https://releases.arc.net/updates.xml":                                                         testAppcastArc,
		"https://github.com/Molunerfinn/PicGo/releases/latest/download/latest-mac.yml":                 "version: 3.0.3\n",
		"https://lf3.example.com/releases/latest-mac.yml":                                              "version: 1.0.2\n",
		"https://itunes.apple.com/lookup?bundleId=com.tencent.xinWeChat&country=cn&entity=macSoftware": testITunes,
	}, parseBrewCasks([]byte(testBrewInfo)))
	casks := caskIndex(src.brew(context.Background()))

	check := func(path string) (AppUpdate, appUpdateAction) {
		b, ok := readAppBundle(context.Background(), path)
		if !ok {
			t.Fatalf("readAppBundle(%s) failed", path)
		}
		return checkTarget(context.Background(), b, src, casks)
	}
	type want struct{ source, state, reason, latest, action string }
	for path, w := range map[string]want{
		chrome: {"brew", "update", "", "156.0.8078.80", "brew"}, // brew's recorded 155 is ignored; the bundle has 156.0.8078.76
		iterm:  {"sparkle", "current", "", "3.7.6", ""},
		arc:    {"sparkle", "update", "", "1.171.0 (89666)", "open-app"},
		ima:    {"sparkle", "unknown", "bad-url", "", ""},
		picgo:  {"electron", "update", "", "3.0.3", "open-app"},
		gen:    {"electron", "current", "", "1.0.2", ""},
		trae:   {"", "unknown", "no-source", "", ""},
		wechat: {"mas", "update", "", "4.2.0", "open-store"},
		plain:  {"", "unknown", "no-source", "", ""},
	} {
		u, a := check(path)
		if u.ID != path || u.Source != w.source || u.State != w.state || u.Reason != w.reason || u.Latest != w.latest || u.Action != w.action || a.kind != w.action {
			t.Errorf("%s: got %+v / %+v, want %+v", filepath.Base(path), u, a, w)
		}
	}
	u, a := check(chrome)
	if u.Current != "156.0.8078.76" || a.token != "google-chrome" || a.path != chrome || a.latest != "156.0.8078.80" {
		t.Errorf("chrome detail: %+v %+v", u, a)
	}
	u, a = check(wechat)
	if a.url != "macappstore://apps.apple.com/app/id836500024" || !strings.HasPrefix(u.NotesURL, "https://apps.apple.com/") {
		t.Errorf("wechat: %+v %+v", u, a)
	}
	for _, q := range *asked {
		if strings.Contains(q, "0.0.0.0") || strings.Contains(q, "should-not-be-used") {
			t.Errorf("fetched %s", q)
		}
	}
}

func TestCheckTargetMASFallsBackToUS(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	app := fakeBundle(t, filepath.Join(home, "Applications"), "Things", map[string]string{"CFBundleIdentifier": "com.culturedcode.ThingsMac", "CFBundleShortVersionString": "3.21"}, true, "")
	src, _ := testSources(map[string]string{
		"https://itunes.apple.com/lookup?bundleId=com.culturedcode.ThingsMac&country=cn&entity=macSoftware": `{"resultCount":0,"results":[]}`,
		"https://itunes.apple.com/lookup?bundleId=com.culturedcode.ThingsMac&country=us&entity=macSoftware": `{"resultCount":1,"results":[{"trackId":904280696,"version":"3.21","trackViewUrl":"https://apps.apple.com/us/app/things-3/id904280696"}]}`,
	}, nil)
	b, _ := readAppBundle(context.Background(), app)
	u, _ := checkTarget(context.Background(), b, src, nil)
	if u.Source != "mas" || u.State != "current" || u.Latest != "3.21" {
		t.Fatalf("%+v", u)
	}
}

func TestCheckTargetFeedErrors(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	app := fakeBundle(t, filepath.Join(home, "Applications"), "Gone", map[string]string{"CFBundleShortVersionString": "1.0", "SUFeedURL": "https://gone.example.com/x.xml"}, false, "")
	src, _ := testSources(nil, nil)
	src.fetch = func(context.Context, string) ([]byte, error) { return nil, errFeedRate }
	b, _ := readAppBundle(context.Background(), app)
	if u, _ := checkTarget(context.Background(), b, src, nil); u.State != "error" || u.Reason != "rate-limited" {
		t.Fatalf("%+v", u)
	}
	src.fetch = func(context.Context, string) ([]byte, error) { return []byte("<html/>"), nil }
	if u, _ := checkTarget(context.Background(), b, src, nil); u.State != "error" || u.Reason != "bad-feed" {
		t.Fatalf("%+v", u)
	}
	yml := fakeBundle(t, filepath.Join(home, "Applications"), "NoVer", map[string]string{"CFBundleShortVersionString": "1.0"}, false, "provider: generic\nurl: https://x.example.com/\n")
	src.fetch = func(context.Context, string) ([]byte, error) { return []byte("files: []\n"), nil }
	b, _ = readAppBundle(context.Background(), yml)
	if u, _ := checkTarget(context.Background(), b, src, nil); u.State != "error" || u.Reason != "bad-feed" {
		t.Fatalf("%+v", u)
	}
}

func TestCheckAppUpdatesStreamsRemembersAndEnds(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	apps := filepath.Join(home, "Applications")
	chrome := fakeBundle(t, apps, "Google Chrome", map[string]string{"CFBundleShortVersionString": "156.0.8078.76"}, false, "")
	plain := fakeBundle(t, apps, "Plain", map[string]string{"CFBundleShortVersionString": "1.0"}, false, "")
	outside := filepath.Join(home, "Desktop", "Loose.app")
	brewCalls := 0
	src, _ := testSources(nil, nil)
	src.brew = func(context.Context) []brewCask { brewCalls++; return parseBrewCasks([]byte(testBrewInfo)) }
	rememberAppAction("/stale", appUpdateAction{kind: "brew"})

	var mu sync.Mutex
	got := map[string]AppUpdate{}
	var order []string
	err := checkAppUpdates(context.Background(), []string{chrome, plain, outside}, src, func(u AppUpdate) error {
		mu.Lock()
		defer mu.Unlock()
		got[u.ID] = u
		order = append(order, u.ID)
		return nil
	})
	if err != nil || brewCalls != 1 || len(got) != 4 || order[len(order)-1] != "__done" {
		t.Fatalf("err=%v brew=%d got=%v order=%v", err, brewCalls, got, order)
	}
	if got[outside].Reason != "no-source" {
		t.Fatalf("outside: %+v", got[outside])
	}
	if _, ok := appActionFor("/stale"); !ok {
		t.Fatal("recheck removed an unvisited action")
	}
	if a, ok := appActionFor(chrome); !ok || a.kind != "brew" {
		t.Fatalf("chrome action %+v %v", a, ok)
	}
	if _, ok := appActionFor(plain); ok {
		t.Fatal("non-update remembered")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := checkAppUpdates(ctx, []string{chrome}, src, func(AppUpdate) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
}

func TestReadAppBundleHonorsParentCancellation(t *testing.T) {
	app := fakeBundle(t, t.TempDir(), "Canceled", map[string]string{"CFBundleShortVersionString": "1.0"}, false, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, ok := readAppBundle(ctx, app)
	if ok {
		t.Fatal("canceled parent still read the bundle")
	}
}

func TestCheckAppUpdatesCancelsActiveBundleCommand(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	var ids []string
	for _, name := range []string{"Active", "Remaining"} {
		ids = append(ids, fakeBundle(t, filepath.Join(home, "Applications"), name, map[string]string{"CFBundleShortVersionString": "1.0"}, false, ""))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var calls, canceled atomic.Int32
	saved := appBundlePlistOutput
	appBundlePlistOutput = func(cmd *exec.Cmd) ([]byte, error) {
		if calls.Add(1) > 1 {
			return nil, errors.New("remaining bundle command started")
		}
		// Keep CommandContext's cancellation, but run a harmless controlled child.
		cmd.Path, cmd.Args = "/bin/sleep", []string{"/bin/sleep", "30"}
		kill := cmd.Cancel
		cmd.Cancel = func() error { canceled.Add(1); return kill() }
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		close(started)
		return nil, cmd.Wait()
	}
	t.Cleanup(func() { appBundlePlistOutput = saved })
	src, _ := testSources(nil, nil)
	done := make(chan error, 1)
	var emitted atomic.Int32
	go func() {
		done <- checkAppUpdates(ctx, ids, src, func(AppUpdate) error { emitted.Add(1); return nil })
	}()
	<-started
	before := time.Now()
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) || time.Since(before) > time.Second || calls.Load() != 1 || canceled.Load() != 1 || emitted.Load() != 0 {
		t.Fatalf("active cancellation: err=%v duration=%v commands=%d killed=%d emitted=%d", err, time.Since(before), calls.Load(), canceled.Load(), emitted.Load())
	}
}

func TestCheckAppUpdatesStopsDuringPreparation(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	app := fakeBundle(t, filepath.Join(home, "Applications"), "Later", map[string]string{"CFBundleShortVersionString": "1.0"}, false, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got []string
	brewCalls := 0
	src, _ := testSources(nil, nil)
	src.brew = func(context.Context) []brewCask { brewCalls++; return nil }
	err := checkAppUpdates(ctx, []string{"/first", "/second", app}, src, func(u AppUpdate) error {
		got = append(got, u.ID)
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || strings.Join(got, "|") != "/first" || brewCalls != 0 {
		t.Fatalf("preparation after cancel: err=%v results=%v brew=%d", err, got, brewCalls)
	}
}

func TestCheckAppUpdatesRetainedActionUsableBeforeResumedResult(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	resetAppActions()
	t.Cleanup(resetAppActions)
	opened := stubOpen(t)
	app := fakeBundle(t, filepath.Join(home, "Applications"), "Retained", map[string]string{
		"CFBundleShortVersionString": "1.0", "SUFeedURL": "https://example.com/feed",
	}, false, "")
	src, _ := testSources(map[string]string{"https://example.com/feed": testAppcastITerm}, nil)
	if err := checkAppUpdates(context.Background(), []string{app}, src, func(AppUpdate) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := checkAppUpdates(ctx, []string{app}, src, func(AppUpdate) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertUpgrade := func() {
		t.Helper()
		res, err := (Cleaner{}).UpgradeApp(context.Background(), app)
		if err != nil || !res.Done || res.Code != "" {
			t.Errorf("retained update before resumed result: %+v %v", res, err)
		}
	}
	assertUpgrade()
	src.fetch = func(ctx context.Context, _ string) ([]byte, error) {
		assertUpgrade()
		cancel()
		return nil, ctx.Err()
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	emitted := 0
	if err := checkAppUpdates(ctx, []string{app}, src, func(AppUpdate) error { emitted++; return nil }); !errors.Is(err, context.Canceled) || emitted != 0 {
		t.Fatalf("canceled resume: err=%v emitted=%d", err, emitted)
	}
	assertUpgrade()
	if len(*opened) != 3 {
		t.Errorf("retained UpgradeApp calls opened %v, want three opens", *opened)
	}
}

func TestCheckAppUpdatesReplacesOnlyDeliveredAppActions(t *testing.T) {
	for _, state := range []string{"update", "current", "no-source", "unreadable", "network"} {
		t.Run(state, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			resetAppActions()
			t.Cleanup(resetAppActions)
			app := fakeBundle(t, filepath.Join(home, "Applications"), "Fresh", map[string]string{
				"CFBundleShortVersionString": "1.0", "SUFeedURL": "https://example.com/feed",
			}, false, "")
			old := appUpdateAction{kind: "open-store", url: "macappstore://old"}
			rememberAppAction(app, old)
			rememberAppAction("/unvisited", old)
			if state == "unreadable" {
				if err := os.Remove(filepath.Join(app, "Contents", "Info.plist")); err != nil {
					t.Fatal(err)
				}
			}
			if state == "no-source" {
				writeText(t, filepath.Join(app, "Contents", "Info.plist"), `<plist><dict><key>CFBundleShortVersionString</key><string>1</string></dict></plist>`)
			}
			src, _ := testSources(map[string]string{"https://example.com/feed": testAppcastITerm}, nil)
			if state == "current" {
				src.fetch = func(context.Context, string) ([]byte, error) {
					return []byte(`<rss><channel><item><title>1</title></item></channel></rss>`), nil
				}
			} else if state == "network" {
				src.fetch = func(context.Context, string) ([]byte, error) { return nil, errors.New("network") }
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := checkAppUpdates(ctx, []string{app}, src, func(u AppUpdate) error {
				a, ok := appActionFor(app)
				if state == "update" {
					if !ok || a.kind != "open-app" || a.path != app || u.Action != "open-app" {
						t.Errorf("fresh update was not actionable at delivery: %+v %+v", u, a)
					}
				} else if ok || u.Action != "" {
					t.Errorf("fresh %s retained obsolete action: %+v %+v", state, u, a)
				}
				cancel()
				return nil
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if a, ok := appActionFor("/unvisited"); !ok || a.kind != old.kind {
				t.Errorf("cancel destroyed unvisited action: %+v %v", a, ok)
			}
		})
	}
}

func TestCheckAppUpdatesFailedDeliveryPreservesAction(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	resetAppActions()
	t.Cleanup(resetAppActions)
	app := fakeBundle(t, filepath.Join(home, "Applications"), "Undelivered", map[string]string{
		"CFBundleShortVersionString": "1.0", "SUFeedURL": "https://example.com/feed",
	}, false, "")
	rememberAppAction(app, appUpdateAction{kind: "open-store", url: "macappstore://old"})
	src, _ := testSources(map[string]string{"https://example.com/feed": testAppcastITerm}, nil)
	closed := errors.New("channel closed")
	if err := checkAppUpdates(context.Background(), []string{app}, src, func(AppUpdate) error { return closed }); !errors.Is(err, closed) {
		t.Fatal(err)
	}
	if a, ok := appActionFor(app); !ok || a.kind != "open-store" {
		t.Fatalf("undelivered update replaced retained action: %+v %v", a, ok)
	}
}

func TestCheckAppUpdatesStaleRunCannotPublish(t *testing.T) {
	for _, freshState := range []string{"update", "current"} {
		t.Run(freshState, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			resetAppActions()
			t.Cleanup(resetAppActions)
			app := fakeBundle(t, filepath.Join(home, "Applications"), "Overlap", map[string]string{
				"CFBundleShortVersionString": "1.0", "SUFeedURL": "https://example.com/feed",
			}, false, "")
			started, release := make(chan struct{}), make(chan struct{})
			src, _ := testSources(nil, nil)
			src.fetch = func(context.Context, string) ([]byte, error) {
				close(started)
				<-release
				return []byte(testAppcastITerm), nil
			}
			oldDone := make(chan error, 1)
			var oldResults []AppUpdate
			go func() {
				oldDone <- checkAppUpdates(context.Background(), []string{app}, src, func(u AppUpdate) error {
					oldResults = append(oldResults, u)
					return nil
				})
			}()
			<-started
			fresh, _ := testSources(map[string]string{"https://example.com/feed": testAppcastITerm}, nil)
			fresh.brew = func(context.Context) []brewCask {
				version := "2.0"
				if freshState == "current" {
					version = "1.0"
				}
				return []brewCask{{token: "fresh", version: version, apps: []string{"Overlap.app"}}}
			}
			err := checkAppUpdates(context.Background(), []string{app}, fresh, func(AppUpdate) error { return nil })
			close(release)
			oldErr := <-oldDone
			if err != nil || !errors.Is(oldErr, context.Canceled) || len(oldResults) != 0 {
				t.Errorf("overlap: fresh err=%v old err=%v old results=%v", err, oldErr, oldResults)
			}
			a, ok := appActionFor(app)
			if freshState == "update" && (!ok || a.kind != "brew" || a.token != "fresh") {
				t.Errorf("stale check overwrote fresh action: %+v %v", a, ok)
			}
			if freshState == "current" && ok {
				t.Errorf("stale check recreated removed action: %+v", a)
			}
		})
	}
}

func TestInstalledCasksWaitsForUpgradeAndCancels(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	fakeTool(t, bin, "brew", `echo "$@" >> `+shellPath(log)+`
echo '`+testBrewInfo+`'`)
	saved := toolDirs
	toolDirs = func() []string { return []string{bin} }
	t.Cleanup(func() { toolDirs = saved })
	appUpgradeMu.lock()
	locked := true
	defer func() {
		if locked {
			appUpgradeMu.unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan []brewCask, 1)
	go func() { done <- installedCasks(ctx) }()
	got := <-done
	cancel()
	appUpgradeMu.unlock()
	locked = false
	if len(got) != 0 {
		t.Errorf("brew info ran under upgrade lock: %+v", got)
	}
	if data, _ := os.ReadFile(log); len(data) != 0 {
		t.Errorf("canceled brew info ran: %q", data)
	}
	if got := installedCasks(context.Background()); len(got) != 3 {
		t.Errorf("brew info did not run after release: %+v", got)
	}
}

func TestInstalledCasksSerializesWithUpgrade(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	resetAppActions()
	t.Cleanup(resetAppActions)
	app := fakeBundle(t, filepath.Join(home, "Applications"), "Busy", map[string]string{"CFBundleShortVersionString": "1.0"}, false, "")
	rememberAppAction(app, appUpdateAction{kind: "brew", token: "busy", path: app, latest: "2.0"})
	bin := t.TempDir()
	started, release := filepath.Join(bin, "started"), filepath.Join(bin, "release")
	fakeTool(t, bin, "brew", `touch `+shellPath(started)+`
while [ ! -f `+shellPath(release)+` ]; do sleep 0.01; done
echo '`+testBrewInfo+`'`)
	saved := toolDirs
	toolDirs = func() []string { return []string{bin} }
	t.Cleanup(func() { toolDirs = saved })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan []brewCask, 1)
	go func() { done <- installedCasks(ctx) }()
	deadline := time.Now().Add(time.Second)
	for !exists(started) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !exists(started) {
		cancel()
		<-done
		t.Fatal("fixture brew info did not start")
	}
	// Fail closed if broken locking permits upgrade; never run the fixture as
	// upgrade and never probe native processes.
	stubUsage(t, &usageGuard{failed: true})
	res, err := (Cleaner{}).UpgradeApp(context.Background(), app)
	writeText(t, release, "release")
	casks := <-done
	if err != nil || res.Code != "busy" || len(casks) != 3 {
		t.Fatalf("info/upgrade serialization: %+v %v casks=%d", res, err, len(casks))
	}
}

func TestUpgradeAppCompletionPreservesConcurrentFreshAction(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	resetAppActions()
	t.Cleanup(resetAppActions)
	app := fakeBundle(t, filepath.Join(home, "Applications"), "Concurrent", map[string]string{"CFBundleShortVersionString": "2.0"}, false, "")
	bin := t.TempDir()
	fakeTool(t, bin, "brew", `echo "fixture upgrade"`)
	savedDirs, savedRead := toolDirs, appBundlePlistOutput
	toolDirs = func() []string { return []string{bin} }
	t.Cleanup(func() { toolDirs, appBundlePlistOutput = savedDirs, savedRead })
	stubUsage(t, &usageGuard{})
	action := appUpdateAction{kind: "brew", token: "concurrent", path: app, latest: "2.0"}
	rememberAppAction(app, action)
	reads := 0
	appBundlePlistOutput = func(cmd *exec.Cmd) ([]byte, error) {
		out, err := cmd.Output()
		reads++
		if reads == 2 {
			// Even an identical fresh action is a new publication.
			rememberAppAction(app, action)
		}
		return out, err
	}
	res, err := (Cleaner{}).UpgradeApp(context.Background(), app)
	if err != nil || !res.Done {
		t.Fatalf("upgrade fixture: %+v %v", res, err)
	}
	if a, ok := appActionFor(app); !ok || a.token != "concurrent" {
		t.Fatalf("old upgrade removed concurrent fresh action: %+v %v", a, ok)
	}
}

func TestUpgradeAppConsumedActionNotRestoredByFailedRecheck(t *testing.T) {
	for _, upgradeFinishesFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("upgrade-first=%v", upgradeFinishesFirst), func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			resetAppActions()
			t.Cleanup(resetAppActions)
			app := fakeBundle(t, filepath.Join(home, "Applications"), "Consumed", map[string]string{"CFBundleShortVersionString": "2.0"}, false, "")
			bin := t.TempDir()
			log := filepath.Join(bin, "log")
			fakeTool(t, bin, "brew", `echo "$@" >> `+shellPath(log))
			savedDirs, savedRead := toolDirs, appBundlePlistOutput
			toolDirs = func() []string { return []string{bin} }
			t.Cleanup(func() { toolDirs, appBundlePlistOutput = savedDirs, savedRead })
			stubUsage(t, &usageGuard{})
			rememberAppAction(app, appUpdateAction{kind: "brew", token: "committed", path: app, latest: "2.0"})
			original, _ := appActionFor(app)
			upgradeStarted, releaseUpgrade := make(chan struct{}), make(chan struct{})
			var reads atomic.Int32
			appBundlePlistOutput = func(cmd *exec.Cmd) ([]byte, error) {
				out, err := cmd.Output()
				if reads.Add(1) == 1 {
					close(upgradeStarted)
					<-releaseUpgrade
				}
				return out, err
			}
			upgradeDone := make(chan AppUpgradeResult, 1)
			go func() {
				res, _ := (Cleaner{}).UpgradeApp(context.Background(), app)
				upgradeDone <- res
			}()
			<-upgradeStarted
			src, _ := testSources(nil, []brewCask{{token: "pending", version: "3.0", apps: []string{"Consumed.app"}}})
			sendStarted, releaseSend := make(chan struct{}), make(chan struct{})
			checkDone := make(chan error, 1)
			closed := errors.New("channel closed")
			go func() {
				checkDone <- checkAppUpdates(context.Background(), []string{app}, src, func(AppUpdate) error {
					close(sendStarted)
					<-releaseSend
					return closed
				})
			}()
			<-sendStarted
			var res AppUpgradeResult
			var checkErr error
			if upgradeFinishesFirst {
				close(releaseUpgrade)
				res = <-upgradeDone
				if a, ok := appActionFor(app); !ok || a.token != "pending" {
					t.Errorf("old upgrade removed newer pending action: %+v %v", a, ok)
				}
				close(releaseSend)
				checkErr = <-checkDone
			} else {
				close(releaseSend)
				checkErr = <-checkDone
				if a, ok := appActionFor(app); !ok || a.revision != original.revision {
					t.Errorf("failed recheck changed committed action identity: %+v %v; original revision=%d", a, ok, original.revision)
				}
				close(releaseUpgrade)
				res = <-upgradeDone
			}
			if !res.Done || !errors.Is(checkErr, closed) {
				t.Fatalf("upgrade=%+v recheck=%v", res, checkErr)
			}
			if a, ok := appActionFor(app); ok {
				t.Errorf("failed recheck restored consumed action: %+v", a)
			}
			again, _ := (Cleaner{}).UpgradeApp(context.Background(), app)
			data, _ := os.ReadFile(log)
			if again.Code != "not-checked" || string(data) != "upgrade --cask committed\n" {
				t.Errorf("consumed update ran again: %+v commands=%q", again, data)
			}
		})
	}
}

const testElectronMixoul = `version: 1.2
files:
  - url: Mixoul-1.2-arm64.zip
  - url: Mixoul-1.2-x64.zip
path: Mixoul-1.2-arm64.zip
`

func TestCheckAppUpdatesElectronRejectsUnrelatedFeed(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Cleanup(resetAppActions)
	apps := filepath.Join(home, "Applications")
	var ids []string
	for _, app := range []struct{ name, version string }{{"Aime", "0.10.3"}, {"Flux Island", "0.1.37"}} {
		ids = append(ids, fakeBundle(t, apps, app.name, map[string]string{
			"CFBundleName":               app.name,
			"CFBundleExecutable":         app.name,
			"CFBundleShortVersionString": app.version,
		}, false, "provider: generic\nurl: https://lf3-cdn-tos.bytegoofy.com/obj/tron-demo/releases/\n"))
	}
	src, _ := testSources(map[string]string{
		"https://lf3-cdn-tos.bytegoofy.com/obj/tron-demo/releases/latest-mac.yml": testElectronMixoul,
	}, nil)
	got := map[string]AppUpdate{}
	if err := checkAppUpdates(context.Background(), ids, src, func(u AppUpdate) error {
		got[u.ID] = u
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		u := got[id]
		if u.Source != "electron" || u.State != "unknown" || u.Reason != "bad-feed" || u.Latest != "" || u.Action != "" {
			t.Errorf("%s: got %+v, want unknown/bad-feed without latest or action", filepath.Base(id), u)
		}
		if a, ok := appActionFor(id); ok {
			t.Errorf("%s: unrelated feed cached action %+v", filepath.Base(id), a)
		}
	}
}

func TestCheckTargetElectronMatchingFeed(t *testing.T) {
	for _, tc := range []struct {
		label, bundle, name, display, executable, current, feed, latest string
	}{
		{"spaces and dashes", "Flux Island", "Flux Island", "", "Flux Island", "0.1.37", "version: 1.0.2\nfiles:\n  - url: Flux-Island-1.0.2-arm64.zip\n", "1.0.2"},
		{"PicGo files", "PicGo", "", "", "", "3.0.2", "version: 3.0.3\nfiles:\n  - url: PicGo-3.0.3-arm64.dmg\n", "3.0.3"},
		{"PicGo version only", "PicGo", "", "", "", "3.0.2", "version: 3.0.3\n", "3.0.3"},
		{"PicGo empty files", "PicGo", "", "", "", "3.0.2", "version: 3.0.3\nfiles: []\n", "3.0.3"},
		{"bundle name", "Renamed", "PicGo", "Other", "Other", "3.0.2", "version: 3.0.3\npath: PicGo-3.0.3-arm64.dmg\n", "3.0.3"},
		{"display name fallback", "Renamed", "", "Pilion", "Other", "0.1.9", "version: 0.1.10\npath: Pilion-0.1.10-mac-arm64.zip\n", "0.1.10"},
		{"executable", "Renamed", "Other", "", "Pilion", "0.1.9", "version: 0.1.10\npath: Pilion-0.1.10-mac-arm64.zip\n", "0.1.10"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			app := fakeBundle(t, t.TempDir(), tc.bundle, map[string]string{
				"CFBundleName":               tc.name,
				"CFBundleDisplayName":        tc.display,
				"CFBundleExecutable":         tc.executable,
				"CFBundleShortVersionString": tc.current,
			}, false, "provider: generic\nurl: https://download.example.com/releases/\n")
			b, ok := readAppBundle(context.Background(), app)
			if !ok {
				t.Fatalf("readAppBundle(%s) failed", app)
			}
			src, _ := testSources(map[string]string{
				"https://download.example.com/releases/latest-mac.yml": tc.feed,
			}, nil)
			u, a := checkTarget(context.Background(), b, src, nil)
			if u.Source != "electron" || u.State != "update" || u.Reason != "" || u.Latest != tc.latest || u.Action != "open-app" || a.kind != "open-app" || a.path != app {
				t.Fatalf("got %+v / %+v, want update to %s with open-app action", u, a, tc.latest)
			}
		})
	}
}

func TestElectronFeedNames(t *testing.T) {
	got := electronFeedNames([]byte("version: 1.0.2\nfiles:\n  - url: Mixoul-1.0.2-arm64.zip\n    sha512: x\n  - url: https://cdn.example.com/a/Mixoul-1.0.2-x64.zip\npath: >-\n  Mixoul-1.0.2-arm64.zip\n"))
	if strings.Join(got, "|") != "Mixoul-1.0.2-arm64.zip|Mixoul-1.0.2-x64.zip|Mixoul-1.0.2-arm64.zip" {
		t.Fatalf("names %q", got)
	}
	if got := electronFeedNames([]byte("version: 3.0.3\nfiles: []\n")); len(got) != 0 {
		t.Fatalf("empty: %q", got)
	}
}

func TestFeedMatchesApp(t *testing.T) {
	b := appBundle{path: "/Applications/Flux Island.app", name: "Flux Island", exec: "Flux Island"}
	if !feedMatchesApp([]string{"Flux-Island-1.0.2-arm64.zip"}, b) {
		t.Fatal("spaces and dashes should not matter")
	}
	if feedMatchesApp([]string{"Mixoul-1.0.2-arm64.zip"}, appBundle{path: "/Applications/Aime.app", name: "Aime", exec: "Aime"}) {
		t.Fatal("another product's files matched")
	}
	if !feedMatchesApp(nil, b) {
		t.Fatal("a feed without files must not hide updates")
	}
}

func stubOpen(t *testing.T) *[]string {
	t.Helper()
	var opened []string
	saved := openURLCommand
	openURLCommand = func(target string) error { opened = append(opened, target); return nil }
	t.Cleanup(func() { openURLCommand = saved })
	return &opened
}

func stubUsage(t *testing.T, g *usageGuard) {
	t.Helper()
	saved := probeUsageFn
	probeUsageFn = func() *usageGuard { return g }
	t.Cleanup(func() { probeUsageFn = saved })
}

func TestUpgradeAppOnlyActsOnCheckedApps(t *testing.T) {
	resetAppActions()
	t.Cleanup(resetAppActions)
	opened := stubOpen(t)
	res, err := Cleaner{}.UpgradeApp(context.Background(), "/Applications/Evil.app")
	if err != nil || res.Code != "not-checked" || len(*opened) != 0 {
		t.Fatalf("%+v %v %v", res, err, *opened)
	}
	rememberAppAction("/Applications/WeChat.app", appUpdateAction{kind: "open-store", url: "macappstore://apps.apple.com/app/id836500024"})
	rememberAppAction("/Applications/iTerm.app", appUpdateAction{kind: "open-app", path: "/Applications/iTerm.app"})
	for _, id := range []string{"/Applications/WeChat.app", "/Applications/iTerm.app"} {
		if res, _ := (Cleaner{}).UpgradeApp(context.Background(), id); !res.Done {
			t.Fatalf("%s: %+v", id, res)
		}
	}
	if strings.Join(*opened, "|") != "macappstore://apps.apple.com/app/id836500024|/Applications/iTerm.app" {
		t.Fatalf("opened %v", *opened)
	}
}

func TestUpgradeAppBrew(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	app := fakeBundle(t, filepath.Join(home, "Applications"), "Ghostty", map[string]string{"CFBundleIdentifier": "com.mitchellh.ghostty", "CFBundleShortVersionString": "1.3.1"}, false, "")
	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	plist := filepath.Join(app, "Contents", "Info.plist")
	// The fake brew records arguments and upgrades the bundle to 1.4.0.
	fakeTool(t, bin, "brew", `echo "$@" >> `+shellPath(log)+`
sed -i '' 's/1.3.1/1.4.0/' `+shellPath(plist)+`
echo "==> Upgrading ghostty"`)
	savedDirs := toolDirs
	toolDirs = func() []string { return []string{bin} }
	t.Cleanup(func() { toolDirs = savedDirs })

	resetAppActions()
	t.Cleanup(resetAppActions)
	stubUsage(t, &usageGuard{running: map[string]bool{"com.mitchellh.ghostty": true}})
	rememberAppAction(app, appUpdateAction{kind: "brew", token: "ghostty", path: app, latest: "1.4.0"})
	if res, _ := (Cleaner{}).UpgradeApp(context.Background(), app); res.Code != "in-use" {
		t.Fatalf("running: %+v", res)
	}
	if data, _ := os.ReadFile(log); len(data) != 0 {
		t.Fatalf("brew ran while app was running: %q", data)
	}
	stubUsage(t, &usageGuard{failed: true})
	if res, _ := (Cleaner{}).UpgradeApp(context.Background(), app); res.Code != "in-use" {
		t.Fatalf("probe failed: %+v", res)
	}

	stubUsage(t, &usageGuard{running: map[string]bool{}})
	res, err := Cleaner{}.UpgradeApp(context.Background(), app)
	data, _ := os.ReadFile(log)
	if err != nil || !res.Done || res.Version != "1.4.0" || string(data) != "upgrade --cask ghostty\n" {
		t.Fatalf("%+v %v %q", res, err, data)
	}
	if _, ok := appActionFor(app); ok {
		t.Fatal("done action still cached")
	}

	// A failed brew returns its last output line and the full command.
	fakeTool(t, bin, "brew", `echo "==> Upgrading x"; echo "sudo: a terminal is required" >&2; exit 1`)
	rememberAppAction(app, appUpdateAction{kind: "brew", token: "x", path: app, latest: "9.0"})
	res, _ = Cleaner{}.UpgradeApp(context.Background(), app)
	if res.Done || res.Code != "brew-failed" || res.Detail != "sudo: a terminal is required" || res.Command != "brew upgrade --cask x" {
		t.Fatalf("failed: %+v", res)
	}

	// Only one brew upgrade may run at a time.
	appUpgradeMu.lock()
	res, _ = Cleaner{}.UpgradeApp(context.Background(), app)
	appUpgradeMu.unlock()
	if res.Code != "busy" {
		t.Fatalf("busy: %+v", res)
	}
}

// WeChat's cask is 4.1.15.55,270135 while the bundle shows 4.1.15 with build
// 270135: the same release. The check offers the update before the upgrade
// only, and the upgrade counts as done.
func TestBrewCaskWithAFourPartVersion(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	app := fakeBundle(t, filepath.Join(home, "Applications"), "WeChat", map[string]string{
		"CFBundleIdentifier": "com.tencent.xinWeChat", "CFBundleShortVersionString": "4.1.14", "CFBundleVersion": "260000",
	}, false, "")
	bin := t.TempDir()
	plist := filepath.Join(app, "Contents", "Info.plist")
	fakeTool(t, bin, "brew", `sed -i '' 's/4.1.14/4.1.15/; s/260000/270135/' `+shellPath(plist)+`
echo "==> Upgrading wechat"`)
	savedDirs := toolDirs
	toolDirs = func() []string { return []string{bin} }
	t.Cleanup(func() { toolDirs = savedDirs })
	resetAppActions()
	t.Cleanup(resetAppActions)
	stubUsage(t, &usageGuard{running: map[string]bool{}})
	src, _ := testSources(nil, nil)
	casks := caskIndex([]brewCask{{token: "wechat", version: "4.1.15.55,270135", apps: []string{"WeChat.app"}}})
	check := func() (AppUpdate, appUpdateAction) {
		t.Helper()
		b, ok := readAppBundle(context.Background(), app)
		if !ok {
			t.Fatal("readAppBundle failed")
		}
		return checkTarget(context.Background(), b, src, casks)
	}
	u, a := check()
	if u.Source != "brew" || u.State != "update" || u.Latest != "4.1.15.55" || a.kind != "brew" {
		t.Fatalf("before the upgrade: %+v %+v", u, a)
	}
	rememberAppAction(app, a)
	res, err := (Cleaner{}).UpgradeApp(context.Background(), app)
	if err != nil || !res.Done || res.Code != "" || res.Version != "4.1.15" {
		t.Fatalf("upgrade: %+v %v", res, err)
	}
	if u, a := check(); u.State != "current" || u.Action != "" || a.kind != "" {
		t.Fatalf("after the upgrade: %+v %+v", u, a)
	}
}

// When the bundle's build says nothing caskState can compare, the cask's
// version can read as an update even once installed. brew exiting zero with
// the bundle's short version or build changed is then a done upgrade; an
// unchanged bundle, or brew failing, is not.
func TestUpgradeAppAcceptsAChangedBundle(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		short, build, cask     string
		afterShort, afterBuild string
		exit                   int
		done                   bool
	}{
		{"build changed", "4.1.15", "260000", "4.1.15.55", "4.1.15", "270135", 0, true},
		{"short version changed", "1.2.2", "4567", "1.2.3.4567", "1.2.3", "4567", 0, true},
		{"unchanged", "4.1.15", "260000", "4.1.15.55", "4.1.15", "260000", 0, false},
		{"brew failed", "4.1.15", "260000", "4.1.15.55", "4.1.15", "270135", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if state, _ := caskState(tc.afterShort, tc.afterBuild, tc.cask); state != "update" {
				t.Fatalf("caskState after the upgrade = %s, want the shape that still reads as an update", state)
			}
			home := t.TempDir()
			setHome(t, home)
			app := fakeBundle(t, filepath.Join(home, "Applications"), "WeChat", map[string]string{
				"CFBundleIdentifier": "com.tencent.xinWeChat", "CFBundleShortVersionString": tc.short, "CFBundleVersion": tc.build,
			}, false, "")
			bin := t.TempDir()
			plist := filepath.Join(app, "Contents", "Info.plist")
			fakeTool(t, bin, "brew", fmt.Sprintf(`sed -i '' 's/>%s</>%s</; s/>%s</>%s</' %s
echo "==> Upgrading wechat"
exit %d`, tc.short, tc.afterShort, tc.build, tc.afterBuild, shellPath(plist), tc.exit))
			savedDirs := toolDirs
			toolDirs = func() []string { return []string{bin} }
			t.Cleanup(func() { toolDirs = savedDirs })
			resetAppActions()
			t.Cleanup(resetAppActions)
			stubUsage(t, &usageGuard{running: map[string]bool{}})
			rememberAppAction(app, appUpdateAction{kind: "brew", token: "wechat", path: app, latest: tc.cask})
			res, err := Cleaner{}.UpgradeApp(context.Background(), app)
			if err != nil || res.Version != tc.afterShort {
				t.Fatalf("%+v %v", res, err)
			}
			if tc.done && (!res.Done || res.Code != "" || res.Command != "") {
				t.Fatalf("a changed bundle after brew succeeded: %+v", res)
			}
			if !tc.done && (res.Done || res.Code != "brew-failed" || res.Detail != "==> Upgrading wechat" || res.Command != "brew upgrade --cask wechat") {
				t.Fatalf("want brew-failed: %+v", res)
			}
			if _, cached := appActionFor(app); cached == tc.done {
				t.Fatalf("action cached = %v after done = %v", cached, res.Done)
			}
		})
	}
}

// Closing the window cancels the page's context while brew runs. brew must
// still finish, and the bundle must still be read afterwards: killing it can
// leave the old app removed and the new one not yet moved in.
func TestUpgradeAppBrewOutlivesTheCaller(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	app := fakeBundle(t, filepath.Join(home, "Applications"), "Ghostty", map[string]string{"CFBundleIdentifier": "com.mitchellh.ghostty", "CFBundleShortVersionString": "1.3.1"}, false, "")
	bin := t.TempDir()
	started, release, finished := filepath.Join(bin, "started"), filepath.Join(bin, "release"), filepath.Join(bin, "finished")
	plist := filepath.Join(app, "Contents", "Info.plist")
	// The fake brew waits for the test, then upgrades the bundle to 1.4.0.
	fakeTool(t, bin, "brew", `touch `+shellPath(started)+`
i=0
while [ ! -f `+shellPath(release)+` ] && [ $i -lt 1000 ]; do sleep 0.01; i=$((i+1)); done
sed -i '' 's/1.3.1/1.4.0/' `+shellPath(plist)+`
touch `+shellPath(finished)+`
echo "==> Upgrading ghostty"`)
	savedDirs := toolDirs
	toolDirs = func() []string { return []string{bin} }
	t.Cleanup(func() { toolDirs = savedDirs })
	resetAppActions()
	t.Cleanup(resetAppActions)
	stubUsage(t, &usageGuard{running: map[string]bool{}})
	rememberAppAction(app, appUpdateAction{kind: "brew", token: "ghostty", path: app, latest: "1.4.0"})
	var ended atomic.Int32
	savedEnded := heldWorkEnded
	heldWorkEnded = func() { ended.Add(1) }
	t.Cleanup(func() { heldWorkEnded = savedEnded })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, returned := make(chan AppUpgradeResult, 1), make(chan struct{})
	go func() {
		defer close(returned)
		res, _ := (Cleaner{}).UpgradeApp(ctx, app)
		done <- res
	}()
	// However the test ends, brew is let go and UpgradeApp returns, so its
	// hold on Cleanzo does not reach the tests after this one.
	t.Cleanup(func() {
		_ = os.WriteFile(release, []byte("release"), 0o644)
		select {
		case <-returned:
		case <-time.After(15 * time.Second):
			t.Error("UpgradeApp did not return, and its hold stays")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for !exists(started) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !exists(started) {
		t.Fatal("fixture brew did not start")
	}
	// Closing the last window now must leave Cleanzo running, or brew would
	// lose its parent mid-upgrade.
	if !holdingOpen() {
		t.Fatal("brew runs, and nothing holds Cleanzo open")
	}
	cancel()
	time.Sleep(200 * time.Millisecond) // time for a kill to land, were there one
	writeText(t, release, "release")
	var res AppUpgradeResult
	select {
	case res = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("UpgradeApp did not return")
	}
	if !res.Done || res.Version != "1.4.0" || res.Code != "" || !exists(finished) {
		t.Fatalf("upgrade after the caller went away: %+v; brew finished=%v", res, exists(finished))
	}
	// Its end lets Cleanzo quit, if the last window closed meanwhile.
	if holdingOpen() || ended.Load() != 1 {
		t.Fatalf("after the upgrade: holding %v, ended %d times", holdingOpen(), ended.Load())
	}
}

// Cleanzo quits (Cmd+Q, Quit in the menu) while brew runs. brew writes on
// after that and must still finish: an upgrade cut short can leave the old
// app removed and the new one not yet moved in. A helper process plays
// Cleanzo and exits while the fake brew waits; brew then writes to its
// stdout and stderr and upgrades the bundle.
func TestUpgradeAppBrewOutlivesCleanzo(t *testing.T) {
	dir := t.TempDir()
	home, bin, tmp := filepath.Join(dir, "home"), filepath.Join(dir, "bin"), filepath.Join(dir, "tmp")
	mustMkdir(t, bin)
	mustMkdir(t, tmp)
	app := fakeBundle(t, filepath.Join(home, "Applications"), "Ghostty", map[string]string{"CFBundleIdentifier": "com.mitchellh.ghostty", "CFBundleShortVersionString": "1.3.1"}, false, "")
	started, release, finished := filepath.Join(bin, "started"), filepath.Join(bin, "release"), filepath.Join(bin, "finished")
	plist := filepath.Join(app, "Contents", "Info.plist")
	fakeTool(t, bin, "brew", `touch `+shellPath(started)+`
i=0
while [ ! -f `+shellPath(release)+` ] && [ $i -lt 1000 ]; do sleep 0.01; i=$((i+1)); done
echo "==> Upgrading ghostty"
echo "==> Removing App '/Applications/Ghostty.app'" >&2
sed -i '' 's/1.3.1/1.4.0/' `+shellPath(plist)+`
echo "==> Moving App 'Ghostty.app' to '/Applications/Ghostty.app'"
touch `+shellPath(finished))
	t.Cleanup(func() { _ = os.WriteFile(release, []byte("release"), 0o644) })

	helper := exec.Command(os.Args[0], "-test.run=^TestUpgradeAppBrewQuitHelper$")
	helper.Env = append(os.Environ(), "CLEANZO_UPGRADE_HELPER="+dir, "TMPDIR="+tmp)
	out, err := helper.CombinedOutput()
	if err != nil || !exists(started) {
		t.Fatalf("helper: %v, brew started=%v\n%s", err, exists(started), out)
	}
	if exists(finished) {
		t.Fatal("brew finished before Cleanzo quit")
	}
	writeText(t, release, "release")
	deadline := time.Now().Add(10 * time.Second)
	for !exists(finished) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !exists(finished) {
		t.Fatal("brew died after Cleanzo quit")
	}
	if data, _ := os.ReadFile(plist); !strings.Contains(string(data), "1.4.0") {
		t.Fatalf("bundle after brew: %s", data)
	}
	// brew's output file is not left behind.
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("left in the temporary folder: %v", left)
	}
}

// TestUpgradeAppBrewQuitHelper is Cleanzo for TestUpgradeAppBrewOutlivesCleanzo:
// it starts the upgrade and exits once brew runs.
func TestUpgradeAppBrewQuitHelper(t *testing.T) {
	dir := os.Getenv("CLEANZO_UPGRADE_HELPER")
	if dir == "" {
		return
	}
	home, bin := filepath.Join(dir, "home"), filepath.Join(dir, "bin")
	setHome(t, home)
	toolDirs = func() []string { return []string{bin} }
	probeUsageFn = func() *usageGuard { return &usageGuard{running: map[string]bool{}} }
	app := filepath.Join(home, "Applications", "Ghostty.app")
	rememberAppAction(app, appUpdateAction{kind: "brew", token: "ghostty", path: app, latest: "1.4.0"})
	go upgradeApp(context.Background(), app)
	started := filepath.Join(bin, "started")
	deadline := time.Now().Add(5 * time.Second)
	for !exists(started) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !exists(started) {
		t.Fatal("fixture brew did not start")
	}
	os.Exit(0) // Cleanzo quits while brew runs
}

// While Cleanzo runs, the timeout stops brew and every process it started.
func TestUpgradeAppBrewTimeoutStopsItsGroup(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	app := fakeBundle(t, filepath.Join(home, "Applications"), "Ghostty", map[string]string{"CFBundleIdentifier": "com.mitchellh.ghostty", "CFBundleShortVersionString": "1.3.1"}, false, "")
	bin := t.TempDir()
	pidFile := filepath.Join(bin, "child")
	fakeTool(t, bin, "brew", `sleep 30 &
echo $! > `+shellPath(pidFile)+`
echo "==> Downloading"
sleep 30`)
	savedDirs, savedTimeout := toolDirs, brewUpgradeTimeout
	toolDirs = func() []string { return []string{bin} }
	brewUpgradeTimeout = 2 * time.Second
	t.Cleanup(func() { toolDirs, brewUpgradeTimeout = savedDirs, savedTimeout })
	resetAppActions()
	t.Cleanup(resetAppActions)
	stubUsage(t, &usageGuard{running: map[string]bool{}})
	rememberAppAction(app, appUpdateAction{kind: "brew", token: "ghostty", path: app, latest: "1.4.0"})

	start := time.Now()
	res, _ := Cleaner{}.UpgradeApp(context.Background(), app)
	if res.Done || res.Code != "brew-failed" || res.Detail != "timeout" || res.Command != "brew upgrade --cask ghostty" || time.Since(start) > 10*time.Second {
		t.Fatalf("timed out: %+v after %v", res, time.Since(start))
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Sscan(string(data), &pid); err != nil || pid <= 0 {
		t.Fatalf("child pid %q: %v", data, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if syscall.Kill(pid, 0) == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("brew's child outlived the timeout")
	}
}

func TestCleanAppCacheOnlyMovesCaches(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	app := makeFakeApp(t, filepath.Join(home, "Applications"), "TestApp")
	cache := filepath.Join(home, "Library", "Caches", "com.example.testapp")
	cacheByName := filepath.Join(home, "Library", "Caches", "TestApp")
	support := filepath.Join(home, "Library", "Application Support", "TestApp")
	prefs := filepath.Join(home, "Library", "Preferences", "com.example.testapp.plist")
	writeFile(t, filepath.Join(cache, "a"), 1000)
	writeFile(t, filepath.Join(cacheByName, "b"), 500)
	writeFile(t, filepath.Join(support, "c"), 100)
	writeFile(t, prefs, 10)

	stubUsage(t, &usageGuard{running: map[string]bool{"com.example.testapp": true}})
	res := Cleaner{}.CleanAppCache(app)
	if res.Moved != 0 || len(res.Failed) != 1 || res.Failed[0] != app+": in-use" || !exists(cache) {
		t.Fatalf("running: %+v", res)
	}

	stubUsage(t, &usageGuard{running: map[string]bool{}})
	res = Cleaner{}.CleanAppCache(app)
	if res.Moved != 2 || res.Freed < 1500 || len(res.Failed) != 0 {
		t.Fatalf("%+v", res)
	}
	if exists(cache) || exists(cacheByName) || !exists(support) || !exists(prefs) || !exists(app) {
		t.Fatal("moved the wrong things")
	}

	if res := (Cleaner{}).CleanAppCache(filepath.Join(home, "Desktop", "X.app")); len(res.Failed) != 1 || !strings.HasSuffix(res.Failed[0], ": not-app") {
		t.Fatalf("outside: %+v", res)
	}
}

func TestUpgradeAppOpenFailure(t *testing.T) {
	resetAppActions()
	t.Cleanup(resetAppActions)
	stubOpen(t)
	openURLCommand = func(string) error { return errors.New("cannot open") }
	rememberAppAction("/Applications/iTerm.app", appUpdateAction{kind: "open-app", path: "/Applications/iTerm.app"})
	res, err := Cleaner{}.UpgradeApp(context.Background(), "/Applications/iTerm.app")
	if err != nil || res.Done || res.Code != "open-failed" || res.Detail != "cannot open" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestUpgradeAppBrewRefusesOpenBundleAndMissingTool(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	app := makeFakeApp(t, filepath.Join(home, "Applications"), "TestApp")
	resetAppActions()
	t.Cleanup(resetAppActions)
	rememberAppAction(app, appUpdateAction{kind: "brew", token: "testapp", path: app, latest: "2.0"})
	savedDirs := toolDirs
	toolDirs = func() []string { return nil }
	t.Cleanup(func() { toolDirs = savedDirs })

	stubUsage(t, &usageGuard{open: []string{strings.ToLower(resolvePath(filepath.Join(app, "Contents", "Info.plist")))}})
	res, _ := Cleaner{}.UpgradeApp(context.Background(), app)
	if res.Code != "in-use" || res.Done {
		t.Fatalf("open bundle: %+v", res)
	}

	stubUsage(t, &usageGuard{})
	res, _ = Cleaner{}.UpgradeApp(context.Background(), app)
	if res.Done || res.Code != "brew-failed" || res.Detail != "no-tool" || res.Command != "brew upgrade --cask testapp" {
		t.Fatalf("missing tool: %+v", res)
	}
}

func TestCleanAppCacheRefusesUsageAndSkipsBusyCache(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	app := makeFakeApp(t, filepath.Join(home, "Applications"), "TestApp")
	cache := filepath.Join(home, "Library", "Caches", "com.example.testapp")
	cacheByName := filepath.Join(home, "Library", "Caches", "TestApp")
	writeFile(t, filepath.Join(cache, "a"), 1000)
	writeFile(t, filepath.Join(cacheByName, "b"), 500)

	// A probe that failed cannot tell whether the app runs: the cache stays,
	// and the answer says usage is unknown rather than that the app is in use.
	for _, c := range []struct {
		g    *usageGuard
		want string
	}{
		{&usageGuard{failed: true}, app + ": usage-unknown"},
		{&usageGuard{open: []string{strings.ToLower(resolvePath(filepath.Join(app, "Contents", "Info.plist")))}}, app + ": in-use"},
	} {
		stubUsage(t, c.g)
		res := Cleaner{}.CleanAppCache(app)
		if res.Moved != 0 || res.Freed != 0 || len(res.Failed) != 1 || res.Failed[0] != c.want || !exists(cache) || !exists(cacheByName) {
			t.Fatalf("unsafe probe: %+v, want failed %q", res, c.want)
		}
	}

	stubUsage(t, &usageGuard{open: []string{strings.ToLower(resolvePath(filepath.Join(cache, "a")))}})
	res := Cleaner{}.CleanAppCache(app)
	if res.Moved != 1 || res.Freed < 500 || len(res.Failed) != 1 || res.Failed[0] != cache+": in-use" || !exists(cache) || exists(cacheByName) || !exists(app) {
		t.Fatalf("busy cache: %+v", res)
	}
}

func TestCleanAppCacheRechecksUsageAfterMeasuring(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	app := makeFakeApp(t, filepath.Join(home, "Applications"), "TestApp")
	cache := filepath.Join(home, "Library", "Caches", "com.example.testapp")
	writeFile(t, filepath.Join(cache, "a"), 1000)

	// The app starts while associated folders are being measured. Each usage
	// probe returns a snapshot, so an earlier guard cannot see that launch.
	started := false
	savedUsage, savedPaths := probeUsageFn, appCachePaths
	probeUsageFn = func() *usageGuard {
		return &usageGuard{running: map[string]bool{"com.example.testapp": started}}
	}
	appCachePaths = func(info AppInfo) []UninstallItem {
		items := uninstallPaths(info)
		started = true
		return items
	}
	t.Cleanup(func() { probeUsageFn, appCachePaths = savedUsage, savedPaths })

	res := Cleaner{}.CleanAppCache(app)
	if !started || res.Moved != 0 || res.Freed != 0 || len(res.Failed) != 1 || res.Failed[0] != app+": in-use" || !exists(cache) {
		t.Fatalf("app started during measurement: %+v; cache exists=%v", res, exists(cache))
	}
}
