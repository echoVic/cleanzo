package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

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
		b, ok := readAppBundle(path)
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
	b, _ := readAppBundle(app)
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
	b, _ := readAppBundle(app)
	if u, _ := checkTarget(context.Background(), b, src, nil); u.State != "error" || u.Reason != "rate-limited" {
		t.Fatalf("%+v", u)
	}
	src.fetch = func(context.Context, string) ([]byte, error) { return []byte("<html/>"), nil }
	if u, _ := checkTarget(context.Background(), b, src, nil); u.State != "error" || u.Reason != "bad-feed" {
		t.Fatalf("%+v", u)
	}
	yml := fakeBundle(t, filepath.Join(home, "Applications"), "NoVer", map[string]string{"CFBundleShortVersionString": "1.0"}, false, "provider: generic\nurl: https://x.example.com/\n")
	src.fetch = func(context.Context, string) ([]byte, error) { return []byte("files: []\n"), nil }
	b, _ = readAppBundle(yml)
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
	if _, ok := appActionFor("/stale"); ok {
		t.Fatal("cache not reset")
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
			b, ok := readAppBundle(app)
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
