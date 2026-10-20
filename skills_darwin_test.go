package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestScanSkillsCopiesAndIssues(t *testing.T) {
	home := skillFixture(t)
	agents := filepath.Join(home, ".agents/skills")
	claude := filepath.Join(home, ".claude/skills")
	codex := filepath.Join(home, ".codex/skills")
	writeSkill(t, filepath.Join(agents, "shared"), "---\nname: shared\ndescription: >\n  Shared skill\n  over two lines\n---\nbody\n")
	if err := os.Symlink(filepath.Join(agents, "shared"), filepath.Join(claude, "shared")); err != nil {
		mustMkdir(t, claude)
		if err := os.Symlink(filepath.Join(agents, "shared"), filepath.Join(claude, "shared")); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill(t, filepath.Join(agents, "same"), "---\ndescription: \"Same\"\n---\nx\n")
	writeSkill(t, filepath.Join(codex, "same"), "---\ndescription: \"Same\"\n---\nx\n")
	writeSkill(t, filepath.Join(agents, "drift"), "v1")
	writeSkill(t, filepath.Join(claude, "drift"), "v2")
	if err := os.Symlink(filepath.Join(home, "nowhere"), filepath.Join(claude, "gone")); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(codex, "empty"))
	mustMkdir(t, filepath.Join(codex, ".system"))
	writeSkillFile(t, filepath.Join(claude, ".DS_Store"), "x")

	res, err := Cleaner{}.ScanSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Roots) != 3 {
		t.Fatalf("roots = %+v", res.Roots)
	}
	for _, s := range res.Skills {
		if s.Name == ".system" || s.Name == ".DS_Store" {
			t.Fatalf("hidden entry listed: %s", s.Name)
		}
	}

	shared := findSkill(t, res, "shared")
	if len(shared.Copies) != 2 || len(shared.Issues) != 0 {
		t.Fatalf("shared = %+v", shared)
	}
	if shared.Description != "Shared skill over two lines" {
		t.Fatalf("description = %q", shared.Description)
	}
	var dir SkillCopy
	for _, c := range shared.Copies {
		if c.Kind == "dir" {
			dir = c
		}
	}
	if len(dir.LinkedBy) != 1 || dir.LinkedBy[0] != filepath.Join(claude, "shared") {
		t.Fatalf("linkedBy = %v", dir.LinkedBy)
	}
	if got := findSkill(t, res, "same").Issues; !slices.Equal(got, []string{"duplicate"}) {
		t.Fatalf("same issues = %v", got)
	}
	if got := findSkill(t, res, "drift").Issues; !slices.Equal(got, []string{"diverged"}) {
		t.Fatalf("drift issues = %v", got)
	}
	if got := findSkill(t, res, "gone").Issues; !slices.Equal(got, []string{"broken"}) {
		t.Fatalf("gone issues = %v", got)
	}
	if got := findSkill(t, res, "empty").Issues; !slices.Equal(got, []string{"invalid"}) {
		t.Fatalf("empty issues = %v", got)
	}
}

func TestScanSkillsUsage(t *testing.T) {
	home := skillFixture(t)
	agents := filepath.Join(home, ".agents/skills")
	writeSkill(t, filepath.Join(agents, "alpha"), "a")
	writeSkill(t, filepath.Join(agents, "beta"), "b")
	writeSkill(t, filepath.Join(agents, "idle"), "i")
	writeSkill(t, filepath.Join(home, ".gemini/skills/geminionly"), "g")

	// Claude: a Skill tool call, the same call copied into a resumed
	// session, a slash command, and a Skill mention that is not a call.
	call := `{"type":"assistant","timestamp":"2026-10-10T08:00:00Z","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Skill","input":{"skill":"alpha"}}]}}`
	writeSkillFile(t, filepath.Join(home, ".claude/projects/p/a.jsonl"), call+"\n")
	writeSkillFile(t, filepath.Join(home, ".claude/projects/p/b.jsonl"), call+"\n"+
		`{"type":"user","uuid":"u1","timestamp":"2026-10-12T08:00:00Z","message":{"content":"<command-name>/alpha</command-name>"}}`+"\n"+
		`{"type":"user","uuid":"u2","timestamp":"2026-10-12T08:00:00Z","message":{"content":[{"type":"text","text":"\"name\":\"Skill\" beta"}]}}`+"\n")

	// Codex: a read of beta's SKILL.md in a tool call; the prompt's skill
	// list and a plugin skill with the same name do not count.
	writeSkillFile(t, filepath.Join(home, ".codex/sessions/2026/10/01/r.jsonl"),
		`{"timestamp":"2026-10-01T00:00:00Z","type":"response_item","payload":{"type":"function_call","call_id":"c1","name":"exec_command","arguments":"{\"cmd\":\"cat `+agents+`/beta/SKILL.md\"}"}}`+"\n"+
			`{"timestamp":"2026-10-02T00:00:00Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"`+agents+`/idle/SKILL.md"}]}}`+"\n"+
			`{"timestamp":"2026-10-03T00:00:00Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c2","name":"exec","input":"cat ~/.codex/plugins/cache/x/skills/idle/SKILL.md"}}`+"\n"+
			`{"timestamp":"2026-10-04T00:00:00Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"c3","name":"exec","input":"cat ~/.agents/skills/beta/SKILL.md"}}`+"\n")

	res, err := Cleaner{}.ScanSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	alpha := findSkill(t, res, "alpha")
	if alpha.Count != 2 || alpha.Last != parseTS("2026-10-12T08:00:00Z") || !alpha.Observed {
		t.Fatalf("alpha = %+v", alpha)
	}
	beta := findSkill(t, res, "beta")
	if beta.Count != 2 || len(beta.Uses) != 1 || beta.Uses[0].Tool != "codex" || beta.Last != parseTS("2026-10-04T00:00:00Z") {
		t.Fatalf("beta = %+v", beta)
	}
	idle := findSkill(t, res, "idle")
	if idle.Count != 0 || idle.Last != 0 || !idle.Observed {
		t.Fatalf("idle = %+v", idle)
	}
	// Installed today, after the records start: not idle yet.
	if idle.Idle || idle.Since == 0 {
		t.Fatalf("new skill marked idle: %+v", idle)
	}
	old := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, n := range []string{"idle", "alpha"} {
		if err := os.Chtimes(filepath.Join(agents, n), old, old); err != nil {
			t.Fatal(err)
		}
	}
	// Records reach back to the oldest transcript in each tool.
	for _, f := range []string{".claude/projects/p/a.jsonl", ".codex/sessions/2026/10/01/r.jsonl"} {
		if err := os.Chtimes(filepath.Join(home, f), old.Add(24*time.Hour), old.Add(24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	res, err = Cleaner{}.ScanSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if idle = findSkill(t, res, "idle"); !idle.Idle {
		t.Fatalf("old unused skill not idle: %+v", idle)
	}
	// Records covering only a few days are too short to call anything idle.
	recent := skillNow().Add(-5 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(home, ".codex/sessions/2026/10/01/r.jsonl"), recent, recent); err != nil {
		t.Fatal(err)
	}
	res, err = Cleaner{}.ScanSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if idle = findSkill(t, res, "idle"); idle.Idle || idle.Since != recent.Unix() {
		t.Fatalf("idle on a 5-day window: %+v", idle)
	}
	if err := os.Chtimes(filepath.Join(home, ".codex/sessions/2026/10/01/r.jsonl"), old.Add(24*time.Hour), old.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if a := findSkill(t, res, "alpha"); a.Idle {
		t.Fatalf("used skill marked idle: %+v", a)
	}
	if g := findSkill(t, res, "geminionly"); g.Observed {
		t.Fatalf("gemini skill marked observed: %+v", g)
	}
	status := map[string]string{}
	for _, s := range res.Sources {
		status[s.Tool] = s.Status
	}
	if status["claude"] != "ok" || status["codex"] != "ok" || status["gemini"] != "unsupported" {
		t.Fatalf("sources = %+v", res.Sources)
	}
}

func TestTrashSkills(t *testing.T) {
	home := skillFixture(t)
	agents := filepath.Join(home, ".agents/skills")
	claude := filepath.Join(home, ".claude/skills")
	writeSkill(t, filepath.Join(agents, "linked"), "l")
	mustMkdir(t, claude)
	if err := os.Symlink(filepath.Join(agents, "linked"), filepath.Join(claude, "linked")); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, filepath.Join(agents, "lonely"), "x")
	writeSkill(t, filepath.Join(home, "work/notaskill"), "n")
	savedOpen := openFilesHook
	openFilesHook = func() ([]string, error) { return nil, nil }
	t.Cleanup(func() { openFilesHook = savedOpen })

	// A folder a link points to stays while the link stays.
	res := Cleaner{}.TrashSkills([]string{filepath.Join(agents, "linked")})
	if res.Moved != 0 || res.Items[0].Error != "linked" || !fileExists(filepath.Join(agents, "linked/SKILL.md")) {
		t.Fatalf("linked folder: %+v", res)
	}
	// Paths outside the skills folders, hidden entries and the folders
	// themselves are refused.
	for _, p := range []string{filepath.Join(home, "work/notaskill"), agents, filepath.Join(agents, ".."), filepath.Join(agents, ".x"), "relative/x"} {
		if r := (Cleaner{}).TrashSkills([]string{p}); r.Moved != 0 || r.Items[0].Error != "not-a-skill" {
			t.Fatalf("%s: %+v", p, r)
		}
	}
	// Moving the link alone keeps the target.
	res = Cleaner{}.TrashSkills([]string{filepath.Join(claude, "linked")})
	if res.Moved != 1 || isLink(filepath.Join(claude, "linked")) || !fileExists(filepath.Join(agents, "linked/SKILL.md")) {
		t.Fatalf("link: %+v", res)
	}
	// Folder and its links together; links go first.
	if err := os.Symlink(filepath.Join(agents, "lonely"), filepath.Join(claude, "lonely")); err != nil {
		t.Fatal(err)
	}
	res = Cleaner{}.TrashSkills([]string{filepath.Join(agents, "lonely"), filepath.Join(claude, "lonely")})
	if res.Moved != 2 || fileExists(filepath.Join(agents, "lonely")) || isLink(filepath.Join(claude, "lonely")) || res.Freed == 0 {
		t.Fatalf("folder with link: %+v", res)
	}
	// The link went first and took the name; the folder is "lonely 2".
	if !isLink(filepath.Join(home, ".Trash/lonely")) || !fileExists(filepath.Join(home, ".Trash/lonely 2/SKILL.md")) {
		t.Fatal("folder not in the Trash")
	}
}

func TestLinkSkillCopy(t *testing.T) {
	home := skillFixture(t)
	agents := filepath.Join(home, ".agents/skills")
	claude := filepath.Join(home, ".claude/skills")
	writeSkill(t, filepath.Join(agents, "dup"), "same")
	writeSkill(t, filepath.Join(claude, "dup"), "same")
	writeSkill(t, filepath.Join(agents, "drift"), "one")
	writeSkill(t, filepath.Join(claude, "drift"), "two")
	writeSkill(t, filepath.Join(agents, "other"), "same")
	savedOpen := openFilesHook
	openFilesHook = func() ([]string, error) { return nil, nil }
	t.Cleanup(func() { openFilesHook = savedOpen })

	if err := (Cleaner{}).LinkSkillCopy(filepath.Join(claude, "drift"), filepath.Join(agents, "drift")); err == nil || err.Error() != "diverged" {
		t.Fatalf("drifted copies linked: %v", err)
	}
	if isLink(filepath.Join(claude, "drift")) {
		t.Fatal("drifted copy replaced")
	}
	if err := (Cleaner{}).LinkSkillCopy(filepath.Join(claude, "dup"), filepath.Join(agents, "other")); err == nil {
		t.Fatal("linked to a skill with another name")
	}
	if err := (Cleaner{}).LinkSkillCopy(filepath.Join(claude, "dup"), filepath.Join(agents, "dup")); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(filepath.Join(claude, "dup"))
	if err != nil || target != filepath.Join(agents, "dup") {
		t.Fatalf("link = %q, %v", target, err)
	}
	if !fileExists(filepath.Join(home, ".Trash/dup/SKILL.md")) {
		t.Fatal("replaced copy not in the Trash")
	}
	if fileExists(filepath.Join(claude, ".dup.cleanzo-link")) {
		t.Fatal("temporary link left behind")
	}
	// Linking the link again is refused.
	if err := (Cleaner{}).LinkSkillCopy(filepath.Join(claude, "dup"), filepath.Join(agents, "dup")); err == nil {
		t.Fatal("linked a link")
	}
}

func TestCodexSkillUsageZstd(t *testing.T) {
	home := skillFixture(t)
	old := skillNow().AddDate(0, -4, 0)
	dir := filepath.Join(home, ".codex/archived_sessions")
	zst := dir + "/rollout-2026-06-01T00-00-00-x.jsonl.zst"
	line := `{"timestamp":"2026-06-01T00:00:00Z","type":"response_item","payload":{"type":"function_call","call_id":"z1","name":"exec_command","arguments":"{\"cmd\":\"cat ` + filepath.Join(home, ".codex/skills/alpha") + `/SKILL.md\"}"}}` + "\n"
	mustMkdir(t, dir)
	writeSkill(t, filepath.Join(home, ".codex/skills/alpha"), "a")
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zst, enc.EncodeAll([]byte(line), nil), 0o644); err != nil {
		t.Fatal(err)
	}
	enc.Close()
	if err := os.Chtimes(zst, old, old); err != nil {
		t.Fatal(err)
	}
	tally, since, n, err := codexSkillUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || tally["alpha"].Count != 1 || tally["alpha"].Last != parseTS("2026-06-01T00:00:00Z") {
		t.Fatalf("archive not read: %+v n=%d", tally["alpha"], n)
	}
	if since != old.Unix() {
		t.Fatalf("since = %d, want %d", since, old.Unix())
	}
}
