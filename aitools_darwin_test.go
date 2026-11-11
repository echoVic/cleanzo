package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestScanAITools(t *testing.T) {
	aiFixture(t)
	res, err := (Cleaner{}).ScanAITools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	claude := aiTool_(t, res, "claude")
	if !claude.Installed || claude.Running || claude.Size == 0 {
		t.Fatalf("claude = %+v", claude)
	}
	// Sessions are grouped by project, labeled with the project's path.
	sessions := aiPart_(t, claude, "claude.sessions")
	if sessions.Level != aiSensitive || len(sessions.Groups) != 2 {
		t.Fatalf("sessions = %+v", sessions)
	}
	var app, gone AIGroup
	for _, g := range sessions.Groups {
		switch g.Key {
		case "-work-app":
			app = g
		case "-gone":
			gone = g
		}
	}
	if app.Count != 2 || app.Recent != 1 || app.Gone || filepath.Base(app.Label) != "app" {
		t.Fatalf("app group = %+v", app)
	}
	if !gone.Gone || gone.Label != "/nowhere/gone" {
		t.Fatalf("gone group = %+v", gone)
	}
	if aiPart_(t, claude, "claude.debug").Level != aiSafe {
		t.Fatal("debug should be safe")
	}
	if p := aiPart_(t, claude, "claude.skills"); p.Level != aiProtected {
		t.Fatalf("skills = %+v", p)
	}
	// What the table does not know is listed but marked unknown.
	if p := aiPart_(t, claude, "unknown:newthing"); p.Level != aiUnknown || p.Size == 0 {
		t.Fatalf("unknown = %+v", p)
	}

	codex := aiTool_(t, res, "codex")
	months := aiPart_(t, codex, "codex.sessions")
	keys := []string{}
	for _, g := range months.Groups {
		keys = append(keys, g.Key)
	}
	if !slices.Equal(keys, []string{"2026/10", "2026/08"}) {
		t.Fatalf("months = %v", keys)
	}
	if months.Groups[0].Count != 2 || months.Groups[0].Recent != 1 {
		t.Fatalf("october = %+v", months.Groups[0])
	}
	// Only releases the current link does not point to are offered.
	rel := aiPart_(t, codex, "codex.oldReleases")
	if rel.Keep != "0.2.0-aarch64-apple-darwin" || len(rel.Groups) != 1 || rel.Groups[0].Key != "0.1.0-aarch64-apple-darwin" || rel.Groups[0].Label != "0.1.0" {
		t.Fatalf("releases = %+v", rel)
	}
	if logs := aiPart_(t, codex, "codex.logs"); logs.Files != 2 || logs.Level != aiSafe {
		t.Fatalf("logs = %+v", logs)
	}
	// plugins/cache is measured on its own; plugins reports the rest.
	cache := aiPart_(t, codex, "codex.pluginCache")
	plugins := aiPart_(t, codex, "codex.plugins")
	if cache.Files != 1 || plugins.Files != 1 {
		t.Fatalf("cache = %+v, plugins = %+v", cache, plugins)
	}
	for _, id := range []string{"codex.auth", "codex.config", "codex.state", "codex.threads"} {
		if p := aiPart_(t, codex, id); p.Level != aiProtected {
			t.Fatalf("%s = %+v", id, p)
		}
	}
	// The safe parts come first.
	if codex.Parts[0].Level != aiSafe {
		t.Fatalf("first part = %+v", codex.Parts[0])
	}
}

func TestListAITools(t *testing.T) {
	home := aiFixture(t)
	// Only Codex is installed now.
	if err := os.RemoveAll(filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	list := (Cleaner{}).ListAITools()
	if len(list) != 3 || list[0].ID != "claude" || list[0].Installed || list[1].ID != "codex" || !list[1].Installed || list[1].Name != "Codex" || list[2].ID != "trae" || list[2].Installed {
		t.Fatalf("list = %+v", list)
	}
	// TRAE counts as installed with only its IDE.
	mustMkdir(t, filepath.Join(home, "Library/Application Support/Trae CN"))
	if list := (Cleaner{}).ListAITools(); !list[2].Installed {
		t.Fatalf("trae = %+v", list[2])
	}
}

func TestScanAIToolOne(t *testing.T) {
	aiFixture(t)
	res, err := (Cleaner{}).ScanAITool(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 1 || res.Tools[0].ID != "codex" || !res.Tools[0].Installed || res.ScannedAt == 0 {
		t.Fatalf("res = %+v", res)
	}
	aiPart_(t, res.Tools[0], "codex.sessions")
	if _, err := (Cleaner{}).ScanAITool(context.Background(), "nope"); err == nil || err.Error() != "unknown-tool" {
		t.Fatalf("unknown tool err = %v", err)
	}
}

func TestScanAIToolDetails(t *testing.T) {
	home := aiFixture(t)
	// More plugin cache folders than the page lists, sized so order is known.
	for i, size := range []int{9000, 1000, 5000, 3000, 7000, 2000, 6000, 4000, 8000, 500} {
		p := filepath.Join(home, ".codex/plugins/cache", "p"+string(rune('a'+i)), "f.bin")
		mustMkdir(t, filepath.Dir(p))
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, _ := (Cleaner{}).ScanAITool(context.Background(), "codex")
	codex := res.Tools[0]

	cache := aiPart_(t, codex, "codex.pluginCache")
	if len(cache.Top) != aiTopN || cache.More != 3 {
		t.Fatalf("top = %d, more = %d", len(cache.Top), cache.More)
	}
	for i := 1; i < len(cache.Top); i++ {
		if cache.Top[i].Size > cache.Top[i-1].Size {
			t.Fatalf("not sorted by size: %+v", cache.Top)
		}
	}
	if !cache.Top[0].IsDir || cache.Top[0].Path != filepath.Join(home, ".codex/plugins/cache/pa") {
		t.Fatalf("largest = %+v", cache.Top[0])
	}
	// Files a glob matched are listed by name.
	logs := aiPart_(t, codex, "codex.logs")
	names := []string{}
	for _, e := range logs.Top {
		names = append(names, e.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"logs_2.sqlite", "logs_2.sqlite-wal"}) || logs.Top[0].IsDir {
		t.Fatalf("logs top = %+v", logs.Top)
	}
	// plugins leaves its nested cache out of its own details.
	for _, e := range aiPart_(t, codex, "codex.plugins").Top {
		if e.Name == "cache" {
			t.Fatalf("plugins lists its nested cache: %+v", e)
		}
	}
	// Groups carry their folder; parts with groups have no top list.
	sessions := aiPart_(t, codex, "codex.sessions")
	if len(sessions.Top) != 0 || sessions.Groups[0].Path != filepath.Join(home, ".codex/sessions/2026/10") {
		t.Fatalf("sessions = %+v", sessions)
	}
	rel := aiPart_(t, codex, "codex.oldReleases")
	if rel.Groups[0].Path != filepath.Join(home, ".codex/packages/app-server-daemon/releases/0.1.0-aarch64-apple-darwin") {
		t.Fatalf("release path = %q", rel.Groups[0].Path)
	}
	claude, _ := (Cleaner{}).ScanAITool(context.Background(), "claude")
	for _, g := range aiPart_(t, claude.Tools[0], "claude.sessions").Groups {
		if g.Path != filepath.Join(home, ".claude/projects", g.Key) {
			t.Fatalf("project path = %q", g.Path)
		}
	}
}

func TestCleanAIToolsSafeParts(t *testing.T) {
	home := aiFixture(t)
	res, err := (Cleaner{}).CleanAITools(context.Background(), []AISelection{
		{Tool: "claude", Part: "claude.debug"},
		{Tool: "codex", Part: "codex.logs"},
		{Tool: "codex", Part: "codex.tmp"},
		{Tool: "codex", Part: "codex.pluginCache"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Freed == 0 {
		t.Fatalf("res = %+v", res)
	}
	for _, r := range res.Parts {
		if r.Skipped != "" || len(r.Failed) != 0 || r.Moved == 0 {
			t.Fatalf("part = %+v", r)
		}
	}
	for _, gone := range []string{".claude/debug", ".codex/logs_2.sqlite", ".codex/logs_2.sqlite-wal", ".codex/..codex-global-state.json.tmp-1-abc", ".codex/plugins/cache"} {
		if _, err := os.Lstat(filepath.Join(home, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s should be in the Trash: %v", gone, err)
		}
	}
	// Everything went to the Trash, nothing was deleted.
	if !trashed(home, "debug") || !trashed(home, "logs_2.sqlite") || !trashed(home, "cache") {
		t.Fatal("expected items in the Trash")
	}
	// The plugins folder around the cache stays.
	if !fileExists(filepath.Join(home, ".codex/plugins/installed.json")) {
		t.Fatal("plugins/installed.json should stay")
	}
}

func TestCleanAIToolsRefusesProtectedAndUnknown(t *testing.T) {
	home := aiFixture(t)
	res, _ := (Cleaner{}).CleanAITools(context.Background(), []AISelection{
		{Tool: "codex", Part: "codex.auth"},
		{Tool: "codex", Part: "codex.config"},
		{Tool: "codex", Part: "codex.threads"},
		{Tool: "claude", Part: "claude.skills"},
		{Tool: "claude", Part: "unknown:newthing"},
		{Tool: "claude", Part: "nope"},
		{Tool: "other", Part: "claude.debug"},
	})
	want := []string{"blocked", "blocked", "blocked", "blocked", "unknown", "unknown", "unknown"}
	for i, r := range res.Parts {
		if r.Skipped != want[i] || r.Moved != 0 {
			t.Fatalf("part %d = %+v, want %s", i, r, want[i])
		}
	}
	for _, kept := range []string{".codex/auth.json", ".codex/config.toml", ".codex/thread_history_1.sqlite", ".claude/skills/mine/SKILL.md", ".claude/newthing/data.bin"} {
		if !fileExists(filepath.Join(home, kept)) {
			t.Fatalf("%s should stay", kept)
		}
	}
}

func TestCleanAIToolsSkipsRunningTool(t *testing.T) {
	home := aiFixture(t)
	aiProcesses = func() (string, error) {
		return "/bin/zsh\n/Users/me/.local/bin/claude --resume\n", nil
	}
	res, _ := (Cleaner{}).CleanAITools(context.Background(), []AISelection{
		{Tool: "claude", Part: "claude.debug"},
		{Tool: "codex", Part: "codex.logs"},
		{Tool: "claude", Part: "claude.sessions", Groups: []string{"-gone"}},
	})
	if res.Parts[0].Skipped != "running" || res.Parts[1].Skipped != "" {
		t.Fatalf("res = %+v", res.Parts)
	}
	if !fileExists(filepath.Join(home, ".claude/debug/a.txt")) {
		t.Fatal("a running tool's files should stay")
	}
	// Old sessions are not in use by a running tool and can still go.
	if r := res.Parts[2]; r.Skipped != "" || r.Moved != 1 {
		t.Fatalf("sessions while running = %+v", r)
	}
	scan, _ := (Cleaner{}).ScanAITools(context.Background())
	if !aiTool_(t, scan, "claude").Running || aiTool_(t, scan, "codex").Running {
		t.Fatal("scan should report Claude Code running")
	}
}

func TestCleanAIToolsFailsClosedWithoutProcesses(t *testing.T) {
	home := aiFixture(t)
	aiProcesses = func() (string, error) { return "", errors.New("ps failed") }
	res, _ := (Cleaner{}).CleanAITools(context.Background(), []AISelection{{Tool: "codex", Part: "codex.logs"}})
	if res.Parts[0].Skipped != "running" {
		t.Fatalf("res = %+v", res.Parts)
	}
	if !fileExists(filepath.Join(home, ".codex/logs_2.sqlite")) {
		t.Fatal("nothing should move when processes cannot be listed")
	}
}

func TestCleanAIToolsSessionsKeepRecent(t *testing.T) {
	home := aiFixture(t)
	res, _ := (Cleaner{}).CleanAITools(context.Background(), []AISelection{
		{Tool: "claude", Part: "claude.sessions", Groups: []string{"-work-app"}},
		{Tool: "codex", Part: "codex.sessions", Groups: []string{"2026/10"}},
	})
	claude, codex := res.Parts[0], res.Parts[1]
	if claude.Skipped != "" || claude.Kept != 1 || claude.Moved != 2 {
		t.Fatalf("claude = %+v", claude)
	}
	// The old transcript and its subagent folder went; today's stays.
	if fileExists(filepath.Join(home, ".claude/projects/-work-app/old.jsonl")) || fileExists(filepath.Join(home, ".claude/projects/-work-app/old")) {
		t.Fatal("old session should be in the Trash")
	}
	if !fileExists(filepath.Join(home, ".claude/projects/-work-app/new.jsonl")) {
		t.Fatal("recent session should stay")
	}
	if !fileExists(filepath.Join(home, ".claude/projects/-gone/s.jsonl")) {
		t.Fatal("unselected project should stay")
	}
	if codex.Kept != 1 || codex.Moved != 1 {
		t.Fatalf("codex = %+v", codex)
	}
	if fileExists(filepath.Join(home, ".codex/sessions/2026/10/01/rollout-c.jsonl")) {
		t.Fatal("old rollout should be in the Trash")
	}
	if !fileExists(filepath.Join(home, ".codex/sessions/2026/10/16/rollout-b.jsonl")) || !fileExists(filepath.Join(home, ".codex/sessions/2026/08/03/rollout-a.jsonl")) {
		t.Fatal("recent and unselected rollouts should stay")
	}
}

func TestCleanAIToolsSessionsNeedGroups(t *testing.T) {
	home := aiFixture(t)
	res, _ := (Cleaner{}).CleanAITools(context.Background(), []AISelection{
		{Tool: "claude", Part: "claude.sessions"},
		{Tool: "codex", Part: "codex.sessions", Groups: []string{"../../auth.json"}},
		{Tool: "codex", Part: "codex.sessions", Groups: []string{"2026"}},
		{Tool: "claude", Part: "claude.sessions", Groups: []string{"-work-app/../../../.ssh"}},
		{Tool: "codex", Part: "codex.oldReleases", Groups: []string{"0.2.0-aarch64-apple-darwin"}},
		{Tool: "codex", Part: "codex.oldReleases"},
	})
	for i, r := range res.Parts {
		if r.Skipped != "blocked" || r.Moved != 0 {
			t.Fatalf("part %d = %+v", i, r)
		}
	}
	if !fileExists(filepath.Join(home, ".codex/auth.json")) || !fileExists(filepath.Join(home, ".codex/packages/app-server-daemon/releases/0.2.0-aarch64-apple-darwin/bin/codex")) {
		t.Fatal("refused selections should move nothing")
	}
}

func TestCleanAIToolsOldReleases(t *testing.T) {
	home := aiFixture(t)
	res, _ := (Cleaner{}).CleanAITools(context.Background(), []AISelection{
		{Tool: "codex", Part: "codex.oldReleases", Groups: []string{"0.1.0-aarch64-apple-darwin"}},
	})
	if r := res.Parts[0]; r.Skipped != "" || r.Moved != 1 || r.Freed == 0 {
		t.Fatalf("res = %+v", r)
	}
	rel := filepath.Join(home, ".codex/packages/app-server-daemon/releases")
	if fileExists(filepath.Join(rel, "0.1.0-aarch64-apple-darwin")) || !fileExists(filepath.Join(rel, "0.2.0-aarch64-apple-darwin/bin/codex")) {
		t.Fatal("only the old release should move")
	}
}

func TestCleanAIToolsNoCurrentLinkOffersNoRelease(t *testing.T) {
	home := aiFixture(t)
	if err := os.Remove(filepath.Join(home, ".codex/packages/app-server-daemon/current")); err != nil {
		t.Fatal(err)
	}
	scan, _ := (Cleaner{}).ScanAITools(context.Background())
	for _, p := range aiTool_(t, scan, "codex").Parts {
		if p.ID == "codex.oldReleases" {
			t.Fatalf("releases offered without a current link: %+v", p)
		}
	}
	res, _ := (Cleaner{}).CleanAITools(context.Background(), []AISelection{
		{Tool: "codex", Part: "codex.oldReleases", Groups: []string{"0.1.0-aarch64-apple-darwin"}},
	})
	if res.Parts[0].Skipped != "blocked" {
		t.Fatalf("res = %+v", res.Parts[0])
	}
}

func TestAIRemovableRefusesLinksOut(t *testing.T) {
	home := aiFixture(t)
	outside := filepath.Join(home, "Documents", "precious")
	mustMkdir(t, outside)
	debug := filepath.Join(home, ".claude", "debug")
	if err := os.RemoveAll(debug); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, debug); err != nil {
		t.Fatal(err)
	}
	res, _ := (Cleaner{}).CleanAITools(context.Background(), []AISelection{{Tool: "claude", Part: "claude.debug"}})
	if res.Parts[0].Moved != 0 {
		t.Fatalf("res = %+v", res.Parts[0])
	}
	if !fileExists(outside) {
		t.Fatal("a linked folder outside the tool must stay")
	}
}

func TestAIRunningMatches(t *testing.T) {
	saved := aiProcesses
	t.Cleanup(func() { aiProcesses = saved })
	cases := map[string][]string{
		"/Users/me/.local/bin/claude\n":                                                                              {"claude"},
		"node /usr/local/bin/claude-code-acp\n":                                                                      {"claude"},
		"/Users/me/.codex/packages/app-server-daemon/current/bin/codex app-server\n":                                 {"codex"},
		"/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex -c x app-server\n": {"codex"},
		"/opt/homebrew/bin/codex exec hi\n":                                                                          {"codex"},
		"/Applications/Obsidian.app/Contents/MacOS/Obsidian\n":                                                       {},
		"vim notes-about-claude.txt\n":                                                                               {},
		"/Users/me/.local/share/traex/current/traex\n":                                                               {"trae"},
		"/Applications/Trae CN.app/Contents/Frameworks/Trae CN Helper.app/Contents/MacOS/Trae CN Helper\n":           {"trae:ide"},
		"/Applications/Trae.app/Contents/MacOS/Electron\n":                                                           {"trae:ide"},
		"vim trae-notes.md\n":                                                                                        {},
	}
	for ps, want := range cases {
		aiProcesses = func() (string, error) { return ps, nil }
		got := []string{}
		for id, on := range aiRunning() {
			if on {
				got = append(got, id)
			}
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%q: running = %v, want %v", ps, got, want)
		}
	}
}

// traeFixture lays out TRAE's CLI and IDE folders in a test home.
func traeFixture(t *testing.T) (home string, project string) {
	t.Helper()
	home = aiFixture(t)
	now := aiNow()
	old, recent := now.Add(-40*24*time.Hour), now.Add(-2*time.Hour)
	write := func(rel, body string, size int, mod time.Time) {
		t.Helper()
		p := filepath.Join(home, rel)
		mustMkdir(t, filepath.Dir(p))
		data := []byte(body)
		if len(data) < size {
			data = append(data, make([]byte, size-len(data))...)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	project = filepath.Join(home, "work", "app")
	meta := func(cwd string) string { return `{"type":"session_meta","payload":{"cwd":"` + cwd + `"}}` + "\n" }
	// CLI
	day := ".trae/cli/sessions/2026/09/06/"
	write(day+"rollout-a.jsonl", meta(project), 8192, old)
	write(day+"rollout-a.artifacts/tool-results/r.txt", "", 4096, old)
	write(".trae/cli/sessions/2026/10/16/rollout-b.jsonl", meta(project), 8192, recent)
	write(".trae/cli/sessions/2026/09/07/rollout-c.jsonl", meta("/nowhere/gone"), 8192, old)
	write(".trae/cli/log/tui.log", "", 4096, old)
	write(".trae/cli/auth.json", "{}", 100, old)
	write(".trae/traecli.toml", "", 100, old)
	// IDE
	ide := "Library/Application Support/Trae CN/"
	write(ide+"ModularData/ai-agent/database.db", "", 16384, old)
	write(ide+"ModularData/ai-agent/database.db-wal", "", 4096, recent)
	write(ide+"ModularData/ai-agent/snapshot/s1/f", "", 4096, old)
	write(ide+"logs/20261001T100000/main.log", "", 8192, old)
	write(ide+"logs/20261016T100000/main.log", "", 4096, recent)
	write(ide+"CachedData/abc/chrome/js/x.code", "", 8192, old)
	write(ide+"Partitions/trae-webview/Cache/Cache_Data/data_0", "", 8192, old)
	write(ide+"Partitions/trae-webview/Cookies", "", 4096, old)
	write(ide+"User/settings.json", "{}", 100, old)
	write(ide+"Cookies", "", 4096, old)
	write(".trae-cn/extensions/e/package.json", "{}", 100, old)
	write(".trae-cn/trae-browser-screenshots/a.png", "", 4096, old)
	write("Library/Caches/Trae CN/update.zip", "", 8192, old)
	return home, project
}

func TestScanAIToolTRAE(t *testing.T) {
	home, project := traeFixture(t)
	res, err := (Cleaner{}).ScanAITool(context.Background(), "trae")
	if err != nil {
		t.Fatal(err)
	}
	trae := res.Tools[0]
	if !trae.Installed || trae.Running || trae.Size == 0 {
		t.Fatalf("trae = %+v", trae)
	}
	// CLI sessions are grouped by project; artifacts count with their session.
	sessions := aiPart_(t, trae, "trae.sessions")
	var app, gone AIGroup
	for _, g := range sessions.Groups {
		switch g.Key {
		case project:
			app = g
		case "/nowhere/gone":
			gone = g
		}
	}
	if app.Count != 2 || app.Recent != 1 || app.Gone || app.Size < 8192*2+4096 {
		t.Fatalf("app group = %+v", app)
	}
	if !gone.Gone || gone.Count != 1 {
		t.Fatalf("gone group = %+v", gone)
	}
	// IDE parts come from its own folders.
	ide := filepath.Join(home, "Library/Application Support/Trae CN")
	chats := aiPart_(t, trae, "trae.ide.chats")
	if chats.Level != aiSensitive || chats.Files != 2 || chats.Path != filepath.Join(ide, "ModularData/ai-agent/database.db*") {
		t.Fatalf("chats = %+v", chats)
	}
	if p := aiPart_(t, trae, "trae.ide.logs"); p.Level != aiSafe || p.Path != filepath.Join(ide, "logs") {
		t.Fatalf("logs = %+v", p)
	}
	if p := aiPart_(t, trae, "trae.ide.update"); p.Level != aiSafe || p.Path != filepath.Join(home, "Library/Caches/Trae CN/update.zip") {
		t.Fatalf("update = %+v", p)
	}
	// The webview cache is measured on its own; the partition reports the rest.
	if cache, data := aiPart_(t, trae, "trae.ide.webCache"), aiPart_(t, trae, "trae.ide.webData"); cache.Files != 1 || data.Files != 1 || data.Level != aiProtected {
		t.Fatalf("webCache = %+v, webData = %+v", cache, data)
	}
	for _, id := range []string{"trae.auth", "trae.config", "trae.ide.user", "trae.ide.extensions"} {
		if p := aiPart_(t, trae, id); p.Level != aiProtected {
			t.Fatalf("%s = %+v", id, p)
		}
	}
	// What the IDE folder holds besides the known parts is one unknown item.
	if p := aiPart_(t, trae, "unknown:ide"); p.Level != aiUnknown || p.Files != 1 {
		t.Fatalf("unknown ide = %+v", p)
	}
}

// TestAIPartsAddUp: every file under a tool's folders counts in one part, so
// the parts add up to the folders. The release in use counts in the protected
// packages part; what a folder holds next to known parts (TRAE's CLI binary
// beside its sessions, the IDE's other agent data) is listed as unknown.
func TestAIPartsAddUp(t *testing.T) {
	home, _ := traeFixture(t)
	for rel, size := range map[string]int{
		".trae/cli/bin/traecli":  16384,
		".trae/cli/version.json": 100,
		"Library/Application Support/Trae CN/ModularData/other/state.db": 4096,
	} {
		p := filepath.Join(home, rel)
		mustMkdir(t, filepath.Dir(p))
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	res, err := (Cleaner{}).ScanAITools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range aiTools {
		var want, got int64
		for _, key := range aiRootKeys(tool) {
			if root := aiRootDir(tool, key); root != "" {
				size, _, _ := measureAI(ctx, root, nil)
				want += size
			}
		}
		for _, p := range aiTool_(t, res, tool.ID).Parts {
			got += p.Size
		}
		if got != want {
			t.Errorf("%s: parts add up to %d, folders hold %d", tool.ID, got, want)
		}
	}
	codex := aiTool_(t, res, "codex")
	current, _, _ := measureAI(ctx, filepath.Join(home, ".codex/packages/app-server-daemon/releases/0.2.0-aarch64-apple-darwin"), nil)
	if p := aiPart_(t, codex, "codex.packages"); p.Level != aiProtected || p.Files != 1 || p.Size != current {
		t.Errorf("packages = %+v, want the release in use (%d bytes)", p, current)
	}
	trae := aiTool_(t, res, "trae")
	if p := aiPart_(t, trae, "unknown:cli"); p.Level != aiUnknown || p.Name != "cli" || p.Path != filepath.Join(home, ".trae/cli") || p.Files != 2 {
		t.Errorf("unknown cli = %+v", p)
	}
	if p := aiPart_(t, trae, "unknown:ide"); p.Files != 2 {
		t.Errorf("unknown ide = %+v", p)
	}
}

func TestCleanAIToolsTRAE(t *testing.T) {
	home, project := traeFixture(t)
	ide := filepath.Join(home, "Library/Application Support/Trae CN")
	res, _ := (Cleaner{}).CleanAITools(context.Background(), []AISelection{
		{Tool: "trae", Part: "trae.sessions", Groups: []string{project}},
		{Tool: "trae", Part: "trae.ide.logs"},
		{Tool: "trae", Part: "trae.ide.update"},
	})
	sessions, logs, update := res.Parts[0], res.Parts[1], res.Parts[2]
	// The old session goes with its artifacts; today's stays.
	if sessions.Skipped != "" || sessions.Kept != 1 || sessions.Moved != 2 {
		t.Fatalf("sessions = %+v", sessions)
	}
	if fileExists(filepath.Join(home, ".trae/cli/sessions/2026/09/06/rollout-a.jsonl")) || fileExists(filepath.Join(home, ".trae/cli/sessions/2026/09/06/rollout-a.artifacts")) {
		t.Fatal("old session should be in the Trash")
	}
	if !fileExists(filepath.Join(home, ".trae/cli/sessions/2026/10/16/rollout-b.jsonl")) || !fileExists(filepath.Join(home, ".trae/cli/sessions/2026/09/07/rollout-c.jsonl")) {
		t.Fatal("recent and unselected sessions should stay")
	}
	// IDE logs go run by run; the folder stays.
	if logs.Skipped != "" || logs.Moved != 2 || !fileExists(filepath.Join(ide, "logs")) || fileExists(filepath.Join(ide, "logs/20261001T100000")) {
		t.Fatalf("logs = %+v", logs)
	}
	if update.Moved != 1 || fileExists(filepath.Join(home, "Library/Caches/Trae CN/update.zip")) {
		t.Fatalf("update = %+v", update)
	}
	// A cwd outside the sessions folder is not a path to clean.
	bad, _ := (Cleaner{}).CleanAITools(context.Background(), []AISelection{{Tool: "trae", Part: "trae.sessions", Groups: []string{"relative/dir"}}})
	if bad.Parts[0].Skipped != "blocked" || !fileExists(project) {
		t.Fatalf("bad = %+v", bad.Parts[0])
	}
}

func TestAITargetsTRAEIDEChatsRetention(t *testing.T) {
	// Removing the family-wide age/safety check must expose the old DB
	// alongside a recent or unsafe sidecar, rather than keep every member.
	for _, tc := range []struct {
		name, member string
		age          time.Duration
		unsafe       string
		wantKept     int64
	}{
		{name: "old family"},
		{name: "recent DB", member: "database.db", age: 2 * time.Hour, wantKept: 3},
		{name: "old DB recent WAL", member: "database.db-wal", age: 2 * time.Hour, wantKept: 3},
		{name: "old DB recent SHM", member: "database.db-shm", age: 2 * time.Hour, wantKept: 3},
		{name: "WAL exactly at cutoff", member: "database.db-wal", age: 24 * time.Hour, wantKept: 3},
		{name: "SHM just before cutoff", member: "database.db-shm", age: 24*time.Hour + time.Nanosecond},
		{name: "future DB", member: "database.db", age: -time.Hour, wantKept: 3},
		{name: "dangling WAL", member: "database.db-wal", unsafe: "link", wantKept: 3},
		{name: "SHM directory", member: "database.db-shm", unsafe: "directory", wantKept: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := traeFixture(t)
			root := filepath.Join(home, "Library/Application Support/Trae CN")
			dir := filepath.Join(root, "ModularData/ai-agent")
			family := []string{
				filepath.Join(dir, "database.db"),
				filepath.Join(dir, "database.db-shm"),
				filepath.Join(dir, "database.db-wal"),
			}
			if err := os.WriteFile(family[1], []byte("shared memory"), 0o644); err != nil {
				t.Fatal(err)
			}
			old := aiNow().Add(-40 * 24 * time.Hour)
			for _, path := range family {
				mod := old
				if filepath.Base(path) == tc.member {
					mod = aiNow().Add(-tc.age)
				}
				if err := os.Chtimes(path, mod, mod); err != nil {
					t.Fatal(err)
				}
			}
			if tc.unsafe != "" {
				path := filepath.Join(dir, tc.member)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if tc.unsafe == "link" {
					if err := os.Symlink(filepath.Join(home, "missing"), path); err != nil {
						t.Fatal(err)
					}
				} else {
					mustMkdir(t, path)
					if err := os.Chtimes(path, old, old); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Chtimes(dir, old, old); err != nil {
					t.Fatal(err)
				}
			}
			tool, part, ok := findAIPart("trae", "trae.ide.chats")
			if !ok {
				t.Fatal("missing TRAE IDE chats part")
			}
			targets, kept, err := aiTargets(root, tool, part, nil)
			if err != nil || kept != tc.wantKept {
				t.Fatalf("targets=%v kept=%d err=%v; want kept=%d", targets, kept, err, tc.wantKept)
			}
			if tc.wantKept > 0 {
				if len(targets) != 0 {
					t.Fatalf("retained family has cleanup targets: %v", targets)
				}
			} else if !slices.Equal(targets, family) {
				t.Fatalf("old family targets=%v, want %v", targets, family)
			}
		})
	}
}

func TestCleanAIToolsTRAEIDEChatsRetention(t *testing.T) {
	for _, recent := range []bool{true, false} {
		name := "old family moves to temporary Trash"
		if recent {
			name = "recent WAL keeps old DB and SHM"
		}
		t.Run(name, func(t *testing.T) {
			home, _ := traeFixture(t)
			dir := filepath.Join(home, "Library/Application Support/Trae CN/ModularData/ai-agent")
			family := []string{"database.db", "database.db-wal", "database.db-shm"}
			if err := os.WriteFile(filepath.Join(dir, family[2]), []byte("shared memory"), 0o644); err != nil {
				t.Fatal(err)
			}
			old := aiNow().Add(-40 * 24 * time.Hour)
			for _, member := range family {
				if recent && member == "database.db-wal" {
					continue
				}
				if err := os.Chtimes(filepath.Join(dir, member), old, old); err != nil {
					t.Fatal(err)
				}
			}
			scan, err := (Cleaner{}).ScanAITool(context.Background(), "trae")
			if err != nil {
				t.Fatal(err)
			}
			chats := aiPart_(t, scan.Tools[0], "trae.ide.chats")
			if chats.Level != aiSensitive || chats.Files != 3 || len(chats.Groups) != 0 {
				t.Fatalf("database files must not claim a conversation count: %+v", chats)
			}
			res, err := (Cleaner{}).CleanAITools(context.Background(), []AISelection{{Tool: "trae", Part: "trae.ide.chats"}})
			if err != nil {
				t.Fatal(err)
			}
			r := res.Parts[0]
			if recent {
				if r.Kept != 3 || r.Moved != 0 || r.Freed != 0 || res.Freed != 0 || r.Skipped != "in-use" || len(r.Failed) != 0 {
					t.Fatalf("recent family = %+v", res)
				}
			} else if r.Kept != 0 || r.Moved != 3 || r.Freed != chats.Size || res.Freed != chats.Size || r.Skipped != "" || len(r.Failed) != 0 {
				t.Fatalf("old family = %+v; scanned bytes=%d", res, chats.Size)
			}
			for _, member := range family {
				if fileExists(filepath.Join(dir, member)) != recent || trashed(home, member) == recent {
					t.Fatalf("%s source/temporary Trash does not match retention=%v", member, recent)
				}
			}
			for _, protected := range []string{".trae/cli/auth.json", ".trae/traecli.toml", "Library/Application Support/Trae CN/User/settings.json", ".trae-cn/extensions/e/package.json"} {
				if !fileExists(filepath.Join(home, protected)) {
					t.Fatalf("protected file moved: %s", protected)
				}
			}
		})
	}
}

func TestCleanAIToolsTRAEIDEChatsSafety(t *testing.T) {
	for _, tc := range []struct {
		name, part, skipped string
		running, failed     bool
	}{
		{name: "running IDE", part: "trae.ide.chats", running: true, skipped: "running"},
		{name: "failed usage probe", part: "trae.ide.chats", failed: true, skipped: "in-use"},
		{name: "protected User", part: "trae.ide.user", skipped: "blocked"},
		{name: "unknown IDE data", part: "unknown:ide", skipped: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := traeFixture(t)
			dir := filepath.Join(home, "Library/Application Support/Trae CN/ModularData/ai-agent")
			old := aiNow().Add(-40 * 24 * time.Hour)
			for _, member := range []string{"database.db", "database.db-wal"} {
				if err := os.Chtimes(filepath.Join(dir, member), old, old); err != nil {
					t.Fatal(err)
				}
			}
			r := cleanAIPart(AISelection{Tool: "trae", Part: tc.part}, map[string]bool{"trae:ide": tc.running}, &usageGuard{failed: tc.failed})
			if r.Skipped != tc.skipped || r.Moved != 0 || r.Freed != 0 || len(r.Failed) != 0 {
				t.Fatalf("safety result = %+v, want skipped=%s", r, tc.skipped)
			}
			if tc.failed && r.Kept != 2 {
				t.Fatalf("failed usage probe kept=%d, want 2 file items", r.Kept)
			}
			for _, member := range []string{"database.db", "database.db-wal"} {
				if !fileExists(filepath.Join(dir, member)) || trashed(home, member) {
					t.Fatalf("%s moved despite safety guard", member)
				}
			}
		})
	}
}

func TestCleanAIToolsTRAEApps(t *testing.T) {
	home, _ := traeFixture(t)
	ide := filepath.Join(home, "Library/Application Support/Trae CN")
	// The IDE runs, the CLI does not.
	aiProcesses = func() (string, error) {
		return "/Applications/Trae CN.app/Contents/MacOS/Electron\n", nil
	}
	res, _ := (Cleaner{}).ScanAITool(context.Background(), "trae")
	trae := res.Tools[0]
	if !trae.Running || !slices.Equal(trae.RunningApps, []string{"ide"}) {
		t.Fatalf("trae = %+v", trae)
	}
	if !aiPart_(t, trae, "trae.ide.cachedData").Running || aiPart_(t, trae, "trae.logs").Running {
		t.Fatal("only the IDE's parts should be marked running")
	}
	clean, _ := (Cleaner{}).CleanAITools(context.Background(), []AISelection{
		{Tool: "trae", Part: "trae.ide.cachedData"},
		{Tool: "trae", Part: "trae.logs"},
	})
	if clean.Parts[0].Skipped != "running" || !fileExists(filepath.Join(ide, "CachedData")) {
		t.Fatalf("cachedData = %+v", clean.Parts[0])
	}
	if clean.Parts[1].Skipped != "" || clean.Parts[1].Moved != 1 {
		t.Fatalf("cli logs = %+v", clean.Parts[1])
	}
}

func TestMeasureAICountsHardLinksOnce(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "a", "traex")
	mustMkdir(t, filepath.Dir(bin))
	if err := os.WriteFile(bin, make([]byte, 64*1024), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"apply_patch", "applypatch"} {
		if err := os.Link(bin, filepath.Join(dir, "a", name)); err != nil {
			t.Fatal(err)
		}
	}
	size, files, _ := measureAI(context.Background(), dir, nil)
	if files != 3 || size > 64*1024 {
		t.Fatalf("size = %d, files = %d", size, files)
	}
}
