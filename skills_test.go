package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// skillFixture builds skills folders and transcripts in a temporary home.
func skillFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	saved, savedQuery := skillNow, opencodeQuery
	skillNow = func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) }
	opencodeQuery = func(context.Context, string, string) ([]byte, error) { return []byte("[]"), nil }
	t.Cleanup(func() { skillNow, opencodeQuery = saved, savedQuery })
	return home
}

func writeSkill(t *testing.T, dir, body string) {
	t.Helper()
	mustMkdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSkillFile(t *testing.T, p, body string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(p))
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findSkill(t *testing.T, res SkillScanResult, name string) Skill {
	t.Helper()
	for _, s := range res.Skills {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("skill %q not listed; have %d", name, len(res.Skills))
	return Skill{}
}

// TestSkillRefsWindowsPaths: on Windows, Codex and OpenCode record the
// SKILL.md they read with backslashes (escaped once by JSON, or twice when
// the arguments are JSON in a JSON string), in any letter case, or with
// forward slashes after the drive. Only the known skills folders count.
func TestSkillRefsWindowsPaths(t *testing.T) {
	home := `C:\Users\me`
	roots := map[string]bool{`C:\Users\me\.agents\skills`: true, `C:\Users\me\.claude\skills`: true}
	for in, want := range map[string][]string{
		`{"command":"Get-Content C:\\Users\\me\\.agents\\skills\\pdf\\SKILL.md"}`:               {"pdf"},
		`"{\"command\":\"type C:\\\\Users\\\\me\\\\.claude\\\\skills\\\\doc\\\\SKILL.md\"}"`:    {"doc"},
		`{"path":"c:/users/ME/.agents/skills/pdf/SKILL.md"}`:                                    {"pdf"},
		`{"command":"cat ~\\.agents\\skills\\xlsx\\SKILL.md\nls"}`:                              {"xlsx"},
		`{"path":"D:\\other\\skills\\pdf\\SKILL.md"}`:                                           nil,
		`{"path":"C:\\Users\\me\\.claude\\plugins\\p\\skills\\pdf\\SKILL.md"}`:                  nil,
		`{"a":"C:\\Users\\me\\.agents\\skills\\a\\SKILL.md","b":"~/.claude/skills/b/SKILL.md"}`: {"a", "b"},
	} {
		if got := skillRefsIn(in, roots, home, true); !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", in, got, want)
		}
	}
	// A profile folder named after the user's full name holds a space. A path
	// earlier on the line, or a URL before the drive, does not swallow it.
	spaced := map[string]bool{`C:\Users\Ada Lovelace\.agents\skills`: true, `C:\Users\Ada Lovelace\.claude\skills`: true}
	for in, want := range map[string][]string{
		`{"command":"Get-Content 'C:\\Users\\Ada Lovelace\\.agents\\skills\\pdf\\SKILL.md'"}`:                          {"pdf"},
		`{"command":"type \"C:\\Users\\Ada Lovelace\\.claude\\skills\\doc\\SKILL.md\""}`:                               {"doc"},
		`"{\"command\":\"type \\\"C:\\\\Users\\\\Ada Lovelace\\\\.claude\\\\skills\\\\doc\\\\SKILL.md\\\"\"}"`:         {"doc"},
		`{"path":"c:/users/ada lovelace/.agents/skills/pdf/SKILL.md"}`:                                                 {"pdf"},
		`{"command":"copy C:\\tmp\\a b.txt D:\\x & type C:\\Users\\Ada Lovelace\\.agents\\skills\\pdf\\SKILL.md"}`:     {"pdf"},
		`{"url":"file:///C:/Users/Ada%20Lovelace/x","path":"C:\\Users\\Ada Lovelace\\.agents\\skills\\pdf\\SKILL.md"}`: {"pdf"},
		`{"command":"cat ~\\.claude\\skills\\xlsx\\SKILL.md"}`:                                                         {"xlsx"},
		`{"path":"C:\\Users\\Ada Lovelace\\.claude\\plugins\\p\\skills\\pdf\\SKILL.md"}`:                               nil,
		`{"path":"C:\\Users\\Ada\\.agents\\skills\\pdf\\SKILL.md"}`:                                                    nil,
	} {
		if got := skillRefsIn(in, spaced, `C:\Users\Ada Lovelace`, true); !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", in, got, want)
		}
	}
	// Elsewhere paths use slashes, and an escape before one is no separator.
	unix := map[string]bool{"/Users/me/.agents/skills": true}
	if got := skillRefsIn(`{"cmd":"ls\n/Users/me/.agents/skills/pdf/SKILL.md"}`, unix, "/Users/me", false); !slices.Equal(got, []string{"pdf"}) {
		t.Errorf("unix path: got %v", got)
	}
}

func TestSkillDescriptionForms(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"---\nname: x\ndescription: plain text\n---\n":     "plain text",
		"---\ndescription: 'quoted'\n---\n":                "quoted",
		"---\ndescription: |\n  line one\n  line two\n---": "line one line two",
		"no front matter\ndescription: nope\n":             "",
	}
	i := 0
	for body, want := range cases {
		i++
		p := filepath.Join(dir, strings.Repeat("s", i))
		writeSkillFile(t, p, body)
		if got := skillDescription(p); got != want {
			t.Errorf("%q: got %q, want %q", body, got, want)
		}
	}
}
