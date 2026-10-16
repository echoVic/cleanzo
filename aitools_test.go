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

// aiFixture builds fake ~/.claude and ~/.codex folders in a temporary home.
func aiFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	savedPs, savedNow := aiProcesses, aiNow
	aiProcesses = func() (string, error) { return "/bin/zsh\n", nil }
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.Local)
	aiNow = func() time.Time { return now }
	t.Cleanup(func() { aiProcesses, aiNow = savedPs, savedNow })

	old := now.Add(-40 * 24 * time.Hour)
	recent := now.Add(-2 * time.Hour)
	write := func(rel string, size int, mod time.Time) {
		t.Helper()
		p := filepath.Join(home, rel)
		mustMkdir(t, filepath.Dir(p))
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	project := filepath.Join(home, "work", "app")
	mustMkdir(t, project)

	// Claude Code
	write(".claude/projects/-work-app/old.jsonl", 8192, old)
	if err := os.WriteFile(filepath.Join(home, ".claude/projects/-work-app/old.jsonl"),
		[]byte(`{"type":"user","cwd":"`+project+`","sessionId":"old"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(filepath.Join(home, ".claude/projects/-work-app/old.jsonl"), old, old)
	write(".claude/projects/-work-app/old/subagents/agent-1.jsonl", 4096, old)
	write(".claude/projects/-work-app/new.jsonl", 4096, recent)
	write(".claude/projects/-gone/s.jsonl", 4096, old)
	if err := os.WriteFile(filepath.Join(home, ".claude/projects/-gone/s.jsonl"),
		[]byte(`{"cwd":"/nowhere/gone"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(filepath.Join(home, ".claude/projects/-gone/s.jsonl"), old, old)
	write(".claude/debug/a.txt", 4096, old)
	write(".claude/file-history/x/1", 4096, old)
	write(".claude/history.jsonl", 4096, recent)
	write(".claude/settings.json", 100, old)
	write(".claude/skills/mine/SKILL.md", 4096, old)
	write(".claude/plugins/p/index.js", 4096, old)
	write(".claude/newthing/data.bin", 4096, old)

	// Codex
	write(".codex/sessions/2026/08/03/rollout-a.jsonl", 8192, old)
	write(".codex/sessions/2026/10/16/rollout-b.jsonl", 8192, recent)
	write(".codex/sessions/2026/10/01/rollout-c.jsonl", 8192, old)
	write(".codex/logs_2.sqlite", 8192, old)
	write(".codex/logs_2.sqlite-wal", 4096, old)
	write(".codex/..codex-global-state.json.tmp-1-abc", 4096, old)
	write(".codex/plugins/cache/x/y.bin", 4096, old)
	write(".codex/plugins/installed.json", 100, old)
	write(".codex/config.toml", 100, old)
	write(".codex/auth.json", 100, old)
	write(".codex/state_5.sqlite", 4096, old)
	write(".codex/thread_history_1.sqlite", 4096, old)
	daemon := ".codex/packages/app-server-daemon"
	write(daemon+"/releases/0.1.0-aarch64-apple-darwin/bin/codex", 8192, old)
	write(daemon+"/releases/0.2.0-aarch64-apple-darwin/bin/codex", 8192, old)
	if err := os.Symlink(filepath.Join(home, daemon, "releases/0.2.0-aarch64-apple-darwin"), filepath.Join(home, daemon, "current")); err != nil {
		t.Fatal(err)
	}
	return home
}

func aiTool_(t *testing.T, res AIScanResult, id string) AITool {
	t.Helper()
	for _, tool := range res.Tools {
		if tool.ID == id {
			return tool
		}
	}
	t.Fatalf("no tool %s", id)
	return AITool{}
}

func aiPart_(t *testing.T, tool AITool, id string) AIPart {
	t.Helper()
	for _, p := range tool.Parts {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no part %s in %s", id, tool.ID)
	return AIPart{}
}

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

func TestScanAIToolsMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	res, err := (Cleaner{}).ScanAITools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Installed || len(tool.Parts) != 0 {
			t.Fatalf("tool = %+v", tool)
		}
	}
	for _, info := range (Cleaner{}).ListAITools() {
		if info.Installed {
			t.Fatalf("listed as installed: %+v", info)
		}
	}
}

func TestListAITools(t *testing.T) {
	home := aiFixture(t)
	// Only Codex is installed now.
	if err := os.RemoveAll(filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	list := (Cleaner{}).ListAITools()
	if len(list) != 2 || list[0].ID != "claude" || list[0].Installed || list[1].ID != "codex" || !list[1].Installed || list[1].Name != "Codex" {
		t.Fatalf("list = %+v", list)
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

func trashed(home, name string) bool {
	_, err := os.Lstat(filepath.Join(home, ".Trash", name))
	return err == nil
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
