package main

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// skillModesTransport serves a GitHub tree without a listener or network connection.
type skillModesTransport func(*http.Request) (*http.Response, error)

// RoundTrip returns a fixture response for the requested source tree.
func (f skillModesTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestWindowsSkillSourceModes matches skills-CLI and CC Switch hashes using GitHub's executable modes.
func TestWindowsSkillSourceModes(t *testing.T) {
	env := testEnv(t)
	folder := filepath.Join(env.home, ".agents", "skills", "doc")
	writeSkillFile(t, filepath.Join(folder, "SKILL.md"), "# test\n")
	writeSkillFile(t, filepath.Join(folder, "run.sh"), "#!/bin/sh\necho hello\n")
	const recorded = "b773fc9bb12c86a85a061e833035c7f5e0676efb"
	saved := skillUpdateHTTP
	calls := 0
	skillUpdateHTTP = &http.Client{Transport: skillModesTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"sha":"root","tree":[{"path":"skills/doc","type":"tree","sha":"` + recorded + `"},{"path":"skills/doc/SKILL.md","type":"blob","mode":"100644"},{"path":"skills/doc/run.sh","type":"blob","mode":"100755"}],"truncated":false}`)), Header: http.Header{}}, nil
	})}
	t.Cleanup(func() { skillUpdateHTTP = saved })
	cache := &treeCache{trees: map[string]*githubTree{}}
	offline := checkOneForPlatform(context.Background(), folder, "doc", SkillOrigin{Kind: originSkillsCLI, Repo: "acme/skills", Path: "skills/doc", Tree: recorded}, cache, false, 1, true)
	if offline.Modified || offline.Status != "unknown" || calls != 0 {
		t.Fatalf("offline mode information was guessed: %+v calls=%d", offline, calls)
	}
	for _, origin := range []SkillOrigin{
		{Kind: originSkillsCLI, Repo: "acme/skills", Path: "skills/doc", Tree: recorded},
		{Kind: originCCSwitch, Repo: "acme/skills", Path: "skills/doc"},
	} {
		u := checkOneForPlatform(context.Background(), folder, "doc", origin, cache, true, 1, true)
		if u.Local != recorded || u.Modified || u.Status != "current" {
			t.Fatalf("%s: %+v", origin.Kind, u)
		}
	}
	if calls != 1 {
		t.Fatalf("source fetched %d times", calls)
	}
}

// TestWindowsSkillInstalledModesSurviveUpstreamChanges keeps an unmodified installed script upgradable.
func TestWindowsSkillInstalledModesSurviveUpstreamChanges(t *testing.T) {
	for _, change := range []string{"executable-mode", "removed-script"} {
		t.Run(change, func(t *testing.T) {
			env := testEnv(t)
			folder := filepath.Join(env.home, ".agents", "skills", "doc")
			writeSkillFile(t, filepath.Join(folder, "SKILL.md"), "# test\n")
			writeSkillFile(t, filepath.Join(folder, "run.sh"), "#!/bin/sh\necho hello\n")
			installed := "113a94fbb21c3035f8c2631f36715162f4a6cd43"
			installedMode, remoteMode := "100644", "100755"
			if change == "removed-script" {
				installed = "b773fc9bb12c86a85a061e833035c7f5e0676efb"
				installedMode = "100755"
			}
			saved := skillUpdateHTTP
			calls := 0
			skillUpdateHTTP = &http.Client{Transport: skillModesTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body := `{"sha":"repo","tree":[{"path":"skills/doc","type":"tree","sha":"upstream"},{"path":"skills/doc/SKILL.md","type":"blob","mode":"100644"}`
				if change != "removed-script" {
					body += `,{"path":"skills/doc/run.sh","type":"blob","mode":"` + remoteMode + `"}`
				}
				body += `]}`
				if strings.Contains(r.URL.Path, installed) {
					body = `{"sha":"` + installed + `","tree":[{"path":"SKILL.md","type":"blob","mode":"100644"},{"path":"run.sh","type":"blob","mode":"` + installedMode + `"}]}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
			})}
			t.Cleanup(func() { skillUpdateHTTP = saved })
			u := checkOneForPlatform(context.Background(), folder, "doc", SkillOrigin{Kind: originSkillsCLI, Repo: "acme/skills", Path: "skills/doc", Tree: installed}, &treeCache{trees: map[string]*githubTree{}}, true, 1, true)
			if u.Local != installed || u.Modified || u.Status != "update" {
				t.Fatalf("upstream mode change mislabeled local copy: %+v", u)
			}
			if calls != 2 {
				t.Fatalf("expected current and installed trees, got %d calls", calls)
			}
		})
	}
}
