package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// aiFixture builds fake ~/.claude and ~/.codex folders in a temporary home.
func aiFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
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

func TestScanAIToolsMissing(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
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

func trashed(home, name string) bool {
	_, err := os.Lstat(filepath.Join(home, ".Trash", name))
	return err == nil
}

// TestAIGroupKeysUseSlashes: the page sends a month as yyyy/mm on every
// platform, and a key never reaches deeper than its part's own groups,
// whichever separator spells it.
func TestAIGroupKeysUseSlashes(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-40 * 24 * time.Hour)
	session := filepath.Join(root, "sessions", "2026", "10", "01", "rollout.jsonl")
	mustMkdir(t, filepath.Dir(session))
	if err := os.WriteFile(session, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(session, old, old); err != nil {
		t.Fatal(err)
	}
	tool, part, ok := findAIPart("codex", "codex.sessions")
	if !ok {
		t.Fatal("codex.sessions is not a known part")
	}
	targets, _, err := aiTargets(root, tool, part, []string{"2026/10"})
	if err != nil || len(targets) != 1 || targets[0] != session {
		t.Fatalf("month 2026/10: targets %v, err %v", targets, err)
	}

	mustMkdir(t, filepath.Join(root, "projects", "a", "b"))
	tool, part, ok = findAIPart("claude", "claude.sessions")
	if !ok {
		t.Fatal("claude.sessions is not a known part")
	}
	nested := []string{"a/b", filepath.Join("a", "b")}
	for _, key := range nested {
		if _, _, err := aiTargets(root, tool, part, []string{key}); err == nil {
			t.Errorf("key %q reaches below a project folder and was accepted", key)
		}
	}
}
