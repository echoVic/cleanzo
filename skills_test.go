package main

import (
	"context"
	"os"
	"path/filepath"
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
