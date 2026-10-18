package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestGitTreeHashMatchesGit compares gitTreeHash with git itself on a
// folder with nested folders, an executable, a link, an empty folder and
// files git sorts differently from plain byte order (a.b vs a/).
func TestGitTreeHashMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	write := func(rel, body string, mode os.FileMode) {
		p := filepath.Join(dir, rel)
		mustMkdir(t, filepath.Dir(p))
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("SKILL.md", "---\nname: x\n---\n", 0o644)
	write("a/b.txt", "nested", 0o644)
	write("a.b", "dot", 0o644)
	write("scripts/run.sh", "#!/bin/sh\necho hi\n", 0o755)
	write(".DS_Store", "junk", 0o644)
	mustMkdir(t, filepath.Join(dir, "empty"))
	if err := os.Symlink("a/b.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	got, err := gitTreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+t.TempDir())
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, ".git/info/exclude"), []byte(".DS_Store\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	want := git("write-tree")
	if got != want {
		t.Fatalf("gitTreeHash = %s, git write-tree = %s", got, want)
	}
}

// fakeGitHub serves a repository's tree listing and archive.
type fakeGitHub struct {
	server    *httptest.Server
	files     map[string]string // path in repo -> content
	execs     map[string]bool
	treeCalls atomic.Int32
	tarCalls  atomic.Int32
	status    int // forced status for the tree API
	lie       bool
}

func newFakeGitHub(t *testing.T, files map[string]string) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{files: files, execs: map[string]bool{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	savedAPI, savedLoad := skillGitHubAPI, skillCodeload
	skillGitHubAPI, skillCodeload = f.server.URL+"/api", f.server.URL+"/load"
	t.Cleanup(func() { skillGitHubAPI, skillCodeload = savedAPI, savedLoad })
	return f
}

// materialize writes the repository to a folder, to hash its trees.
func (f *fakeGitHub) materialize(t *testing.T) string {
	dir := t.TempDir()
	for p, body := range f.files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		mustMkdir(t, filepath.Dir(full))
		mode := os.FileMode(0o644)
		if f.execs[p] {
			mode = 0o755
		}
		if err := os.WriteFile(full, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/repos/acme/skills/git/trees/"):
		f.treeCalls.Add(1)
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		dir, _ := os.MkdirTemp("", "fakegh")
		defer os.RemoveAll(dir)
		for p, body := range f.files {
			full := filepath.Join(dir, filepath.FromSlash(p))
			os.MkdirAll(filepath.Dir(full), 0o755)
			mode := os.FileMode(0o644)
			if f.execs[p] {
				mode = 0o755
			}
			os.WriteFile(full, []byte(body), mode)
		}
		type entry struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
		}
		var tree []entry
		root, _ := gitTreeHash(dir)
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if p == dir || !d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(dir, p)
			h, _ := gitTreeHash(p)
			if f.lie {
				h = strings.Repeat("0", 40)
			}
			tree = append(tree, entry{filepath.ToSlash(rel), "tree", h})
			return nil
		})
		json.NewEncoder(w).Encode(map[string]any{"sha": root, "tree": tree, "truncated": false})
	case strings.HasPrefix(r.URL.Path, "/load/acme/skills/tar.gz/"):
		f.tarCalls.Add(1)
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: "skills-main/", Typeflag: tar.TypeDir, Mode: 0o755})
		for p, body := range f.files {
			mode := int64(0o644)
			if f.execs[p] {
				mode = 0o755
			}
			tw.WriteHeader(&tar.Header{Name: "skills-main/" + p, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(body))})
			tw.Write([]byte(body))
		}
		tw.Close()
		gz.Close()
		w.Write(buf.Bytes())
	default:
		http.NotFound(w, r)
	}
}

// installLocked installs a skill folder and records it in the skills lock.
func installLocked(t *testing.T, home, name string, files map[string]string, tree string) string {
	t.Helper()
	dir := filepath.Join(home, ".agents/skills", name)
	for p, body := range files {
		writeSkillFile(t, filepath.Join(dir, p), body)
	}
	lock := map[string]any{"version": 3, "skills": map[string]any{name: map[string]any{
		"source": "acme/skills", "sourceType": "github", "sourceUrl": "https://github.com/acme/skills.git",
		"skillPath": "skills/" + name + "/SKILL.md", "skillFolderHash": tree,
	}}}
	data, _ := json.Marshal(lock)
	writeSkillFile(t, filepath.Join(home, ".agents/.skill-lock.json"), string(data))
	return dir
}

func noOpenFiles(t *testing.T) {
	saved := openFilesHook
	openFilesHook = func() ([]string, error) { return nil, nil }
	t.Cleanup(func() { openFilesHook = saved })
}

func TestCheckAndUpgradeLockedSkill(t *testing.T) {
	home := skillFixture(t)
	noOpenFiles(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	v1 := map[string]string{"SKILL.md": "---\nname: pdf\n---\nv1\n"}
	gh := newFakeGitHub(t, map[string]string{"skills/pdf/SKILL.md": v1["SKILL.md"], "README.md": "x"})
	tree1, _ := gitTreeHash(filepath.Join(gh.materialize(t), "skills/pdf"))
	dir := installLocked(t, home, "pdf", v1, tree1)
	mustMkdir(t, filepath.Join(home, ".claude/skills"))
	if err := os.Symlink(dir, filepath.Join(home, ".claude/skills/pdf")); err != nil {
		t.Fatal(err)
	}

	// Offline: origin known, nothing fetched.
	ups, err := Cleaner{}.ListSkillOrigins(context.Background())
	if err != nil || len(ups) != 1 || ups[0].Status != "unknown" || gh.treeCalls.Load() != 0 {
		t.Fatalf("offline = %+v, %v, calls %d", ups, err, gh.treeCalls.Load())
	}
	if ups[0].Origin.Kind != originSkillsCLI || ups[0].Origin.Repo != "acme/skills" || ups[0].Origin.Path != "skills/pdf" || ups[0].Modified {
		t.Fatalf("origin = %+v", ups[0])
	}

	// Same version upstream: current.
	ups, _ = Cleaner{}.CheckSkillUpdates(context.Background())
	if ups[0].Status != "current" {
		t.Fatalf("status = %+v", ups[0])
	}

	// Upstream moves on: update, then upgrade.
	gh.files["skills/pdf/SKILL.md"] = "---\nname: pdf\n---\nv2\n"
	gh.files["skills/pdf/scripts/run.sh"] = "#!/bin/sh\n"
	gh.execs["skills/pdf/scripts/run.sh"] = true
	ups, _ = Cleaner{}.CheckSkillUpdates(context.Background())
	if ups[0].Status != "update" || ups[0].Remote == tree1 {
		t.Fatalf("status = %+v", ups[0])
	}
	res, err := Cleaner{}.UpgradeSkills(context.Background(), []SkillUpgradeRequest{{Path: dir}})
	if err != nil || len(res) != 1 || res[0].Error != "" {
		t.Fatalf("upgrade = %+v, %v", res, err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if !strings.Contains(string(body), "v2") {
		t.Fatalf("SKILL.md = %q", body)
	}
	if info, err := os.Stat(filepath.Join(dir, "scripts/run.sh")); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("executable lost: %v %v", info, err)
	}
	// The link from Claude Code still resolves to the new contents.
	if b, _ := os.ReadFile(filepath.Join(home, ".claude/skills/pdf/SKILL.md")); !strings.Contains(string(b), "v2") {
		t.Fatal("link no longer resolves")
	}
	// The old folder is in the Trash, and no staging folder is left.
	if old, _ := os.ReadFile(filepath.Join(home, ".Trash/pdf/SKILL.md")); !strings.Contains(string(old), "v1") {
		t.Fatal("old version not in the Trash")
	}
	if fileExists(filepath.Join(home, ".agents/skills/.pdf.cleanzo-new")) {
		t.Fatal("staging folder left")
	}
	// Cleanzo now records the folder; it is current and unmodified.
	ups, _ = Cleaner{}.CheckSkillUpdates(context.Background())
	if ups[0].Origin.Kind != originCleanzo || ups[0].Status != "current" || ups[0].Modified {
		t.Fatalf("after upgrade = %+v", ups[0])
	}
	if gh.tarCalls.Load() != 1 {
		t.Fatalf("archive downloaded %d times", gh.tarCalls.Load())
	}
}

func TestUpgradeRefusesLocalChangesUnlessForced(t *testing.T) {
	home := skillFixture(t)
	noOpenFiles(t)
	v1 := map[string]string{"SKILL.md": "v1\n"}
	gh := newFakeGitHub(t, map[string]string{"skills/doc/SKILL.md": "v1\n"})
	tree1, _ := gitTreeHash(filepath.Join(gh.materialize(t), "skills/doc"))
	dir := installLocked(t, home, "doc", v1, tree1)
	writeSkillFile(t, filepath.Join(dir, "SKILL.md"), "v1 with my notes\n")
	gh.files["skills/doc/SKILL.md"] = "v2\n"

	ups, _ := Cleaner{}.CheckSkillUpdates(context.Background())
	if !ups[0].Modified || ups[0].Status != "update" {
		t.Fatalf("check = %+v", ups[0])
	}
	res, _ := Cleaner{}.UpgradeSkills(context.Background(), []SkillUpgradeRequest{{Path: dir}})
	if res[0].Error != "modified" {
		t.Fatalf("unforced = %+v", res[0])
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(b) != "v1 with my notes\n" {
		t.Fatal("local changes overwritten without force")
	}
	res, _ = Cleaner{}.UpgradeSkills(context.Background(), []SkillUpgradeRequest{{Path: dir, Force: true}})
	if res[0].Error != "" {
		t.Fatalf("forced = %+v", res[0])
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".Trash/doc/SKILL.md")); string(b) != "v1 with my notes\n" {
		t.Fatal("local changes not kept in the Trash")
	}
}

func TestUpgradeRejectsArchiveThatDoesNotMatch(t *testing.T) {
	home := skillFixture(t)
	noOpenFiles(t)
	gh := newFakeGitHub(t, map[string]string{"skills/x/SKILL.md": "v1\n"})
	tree1, _ := gitTreeHash(filepath.Join(gh.materialize(t), "skills/x"))
	dir := installLocked(t, home, "x", map[string]string{"SKILL.md": "v1\n"}, tree1)
	gh.files["skills/x/SKILL.md"] = "v2\n"
	gh.lie = true // the listing reports a tree the archive does not have
	res, _ := Cleaner{}.UpgradeSkills(context.Background(), []SkillUpgradeRequest{{Path: dir}})
	if res[0].Error != "mismatch" {
		t.Fatalf("res = %+v", res[0])
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(b) != "v1\n" {
		t.Fatal("folder replaced by an unverified archive")
	}
}

func TestCheckReportsMissingAndRateLimit(t *testing.T) {
	home := skillFixture(t)
	gh := newFakeGitHub(t, map[string]string{"other/SKILL.md": "x"})
	installLocked(t, home, "gone", map[string]string{"SKILL.md": "x"}, "")
	ups, _ := Cleaner{}.CheckSkillUpdates(context.Background())
	if ups[0].Status != "missing" {
		t.Fatalf("missing = %+v", ups[0])
	}
	gh.status = http.StatusForbidden
	ups, _ = Cleaner{}.CheckSkillUpdates(context.Background())
	if ups[0].Status != "error" || ups[0].Error != "rate-limited" {
		t.Fatalf("rate = %+v", ups[0])
	}
}

func TestCCSwitchOriginIsReadOnly(t *testing.T) {
	home := skillFixture(t)
	writeSkill(t, filepath.Join(home, ".agents/skills/lark-doc"), "local")
	writeSkillFile(t, filepath.Join(home, ".cc-switch/cc-switch.db"), "")
	saved := ccSwitchQuery
	var queries []string
	ccSwitchQuery = func(_ context.Context, db string) ([]byte, error) {
		queries = append(queries, db)
		return []byte(`[{"directory":"lark-doc","repo_owner":"larksuite","repo_name":"cli","repo_branch":"main","readme_url":"https://github.com/larksuite/cli/blob/HEAD/skills/lark-doc/SKILL.md"},{"directory":"../evil","repo_owner":"a","repo_name":"b"}]`), nil
	}
	t.Cleanup(func() { ccSwitchQuery = saved })
	ups, err := Cleaner{}.ListSkillOrigins(context.Background())
	if err != nil || len(ups) != 1 {
		t.Fatalf("ups = %+v %v", ups, err)
	}
	o := ups[0].Origin
	if o.Kind != originCCSwitch || o.Repo != "larksuite/cli" || o.Ref != "" || o.Path != "skills/lark-doc" || o.Tree != "" || ups[0].Modified {
		t.Fatalf("origin = %+v", o)
	}
	if len(queries) != 1 {
		t.Fatalf("queries = %v", queries)
	}
}

func TestUntarSafeRejectsEscapes(t *testing.T) {
	build := func(hdrs ...*tar.Header) []byte {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for _, h := range hdrs {
			tw.WriteHeader(h)
			if h.Size > 0 {
				tw.Write(bytes.Repeat([]byte("x"), int(h.Size)))
			}
		}
		tw.Close()
		gz.Close()
		return buf.Bytes()
	}
	cases := map[string][]*tar.Header{
		"dotdot":   {{Name: "r/", Typeflag: tar.TypeDir}, {Name: "r/../../evil", Typeflag: tar.TypeReg, Size: 1}},
		"absolute": {{Name: "/etc/evil", Typeflag: tar.TypeReg, Size: 1}},
		"link-out": {{Name: "r/", Typeflag: tar.TypeDir}, {Name: "r/l", Typeflag: tar.TypeSymlink, Linkname: "../../etc"}},
		"link-abs": {{Name: "r/", Typeflag: tar.TypeDir}, {Name: "r/l", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}},
		"hardlink": {{Name: "r/", Typeflag: tar.TypeDir}, {Name: "r/h", Typeflag: tar.TypeLink, Linkname: "r/x"}},
		"two-tops": {{Name: "a/x", Typeflag: tar.TypeReg, Size: 1}, {Name: "b/y", Typeflag: tar.TypeReg, Size: 1}},
	}
	for name, hdrs := range cases {
		dest := filepath.Join(t.TempDir(), "x")
		if _, err := untarSafe(bytes.NewReader(build(hdrs...)), dest); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	dest := filepath.Join(t.TempDir(), "x")
	root, err := untarSafe(bytes.NewReader(build(
		&tar.Header{Name: "r/", Typeflag: tar.TypeDir},
		&tar.Header{Name: "r/a/f", Typeflag: tar.TypeReg, Size: 3, Mode: 0o644},
		&tar.Header{Name: "r/l", Typeflag: tar.TypeSymlink, Linkname: "a/f"},
	)), dest)
	if err != nil || filepath.Base(root) != "r" || !fileExists(filepath.Join(root, "l")) {
		t.Fatalf("good archive: %v %s", err, root)
	}
}

func TestGitCloneOriginAndUpgrade(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := skillFixture(t)
	noOpenFiles(t)
	gitEnv := append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+home,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	upstream := filepath.Join(t.TempDir(), "up")
	mustMkdir(t, upstream)
	run(upstream, "init", "-q", "-b", "main")
	writeSkillFile(t, filepath.Join(upstream, "SKILL.md"), "v1\n")
	run(upstream, "add", "-A")
	run(upstream, "commit", "-qm", "v1")
	clone := filepath.Join(home, ".agents/skills/flow")
	run(filepath.Dir(upstream), "clone", "-q", upstream, clone)
	savedGit := skillGitCommand
	skillGitCommand = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir, cmd.Env = dir, gitEnv
		return cmd.CombinedOutput()
	}
	t.Cleanup(func() { skillGitCommand = savedGit })

	ups, _ := Cleaner{}.CheckSkillUpdates(context.Background())
	if len(ups) != 1 || ups[0].Origin.Kind != originGit || ups[0].Status != "current" {
		t.Fatalf("clone = %+v", ups)
	}
	writeSkillFile(t, filepath.Join(upstream, "SKILL.md"), "v2\n")
	run(upstream, "commit", "-qam", "v2")
	ups, _ = Cleaner{}.CheckSkillUpdates(context.Background())
	if ups[0].Status != "update" {
		t.Fatalf("after push = %+v", ups[0])
	}
	// A local edit blocks the upgrade until forced; forcing stashes it.
	writeSkillFile(t, filepath.Join(clone, "notes.md"), "mine\n")
	res, _ := Cleaner{}.UpgradeSkills(context.Background(), []SkillUpgradeRequest{{Path: clone}})
	if res[0].Error != "modified" {
		t.Fatalf("unforced = %+v", res[0])
	}
	res, _ = Cleaner{}.UpgradeSkills(context.Background(), []SkillUpgradeRequest{{Path: clone, Force: true}})
	if res[0].Error != "" {
		t.Fatalf("forced = %+v", res[0])
	}
	if b, _ := os.ReadFile(filepath.Join(clone, "SKILL.md")); string(b) != "v2\n" {
		t.Fatalf("SKILL.md = %q", b)
	}
	cmd := exec.Command("git", "stash", "list")
	cmd.Dir, cmd.Env = clone, gitEnv
	if out, _ := cmd.Output(); !strings.Contains(string(out), "cleanzo: before upgrade") {
		t.Fatalf("local change not stashed: %s", out)
	}
	_ = fmt.Sprint
}
