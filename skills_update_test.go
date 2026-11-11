package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// untarSafe unpacks an archive the way an upgrade does, without recording
// modes.
func untarSafe(r io.Reader, dest string) (string, error) {
	return untarSafeModes(r, dest, nil)
}

// TestSkillDownloadsAreNotCutShort: GitHub must start answering within a
// minute, but an archive download has no total limit beyond the caller's
// context. A tree listing is still bounded as a whole.
func TestSkillDownloadsAreNotCutShort(t *testing.T) {
	tr, ok := skillUpdateHTTP.Transport.(*http.Transport)
	if skillUpdateHTTP.Timeout != 0 || !ok || tr.ResponseHeaderTimeout != 60*time.Second || skillListTimeout != 60*time.Second {
		t.Fatalf("client timeout %s, transport %T", skillUpdateHTTP.Timeout, skillUpdateHTTP.Transport)
	}
	saved := skillListTimeout
	skillListTimeout = 100 * time.Millisecond
	t.Cleanup(func() { skillListTimeout = saved })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(server.Close)
	savedAPI := skillGitHubAPI
	skillGitHubAPI = server.URL
	t.Cleanup(func() { skillGitHubAPI = savedAPI })
	start := time.Now()
	if _, _, err := fetchGitHubTree(context.Background(), "acme/skills", ""); err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("listing not bounded: %v after %s", err, time.Since(start))
	}
}

// TestUntarSafeStopsAtLimits: an archive stops unpacking with an error once
// it has written more bytes, or held more entries, than an upgrade allows.
func TestUntarSafeStopsAtLimits(t *testing.T) {
	if skillUnpackLimit != 512<<20 || skillEntryLimit != 20000 {
		t.Fatalf("limits = %d bytes, %d entries", skillUnpackLimit, skillEntryLimit)
	}
	savedBytes, savedEntries := skillUnpackLimit, skillEntryLimit
	skillUnpackLimit, skillEntryLimit = 1000, 10
	t.Cleanup(func() { skillUnpackLimit, skillEntryLimit = savedBytes, savedEntries })
	archive := func(files ...int) []byte {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: "r/", Typeflag: tar.TypeDir, Mode: 0o755})
		for i, size := range files {
			tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("r/f%02d", i), Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(size)})
			tw.Write(bytes.Repeat([]byte("x"), size))
		}
		tw.Close()
		gz.Close()
		return buf.Bytes()
	}
	written := func(root string) (n int64) {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				info, _ := d.Info()
				n += info.Size()
			}
			return nil
		})
		return n
	}
	dest := filepath.Join(t.TempDir(), "x")
	if _, err := untarSafe(bytes.NewReader(archive(600, 600)), dest); err == nil {
		t.Fatal("1200 bytes unpacked past a 1000-byte limit")
	}
	if n := written(dest); n > 1000 {
		t.Fatalf("%d bytes written past the limit", n)
	}
	if _, err := untarSafe(bytes.NewReader(archive(1, 1, 1, 1, 1, 1, 1, 1, 1, 1)), filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("11 entries unpacked past a 10-entry limit")
	}
	root, err := untarSafe(bytes.NewReader(archive(500, 400, 1, 1, 1, 1, 1, 1, 1)), filepath.Join(t.TempDir(), "x"))
	if err != nil || written(root) != 907 {
		t.Fatalf("archive within the limits: %v", err)
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
	// listing, when set, lists the written repository instead of hashing it
	// with the code under test.
	listing func(dir string) (any, error)
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
		if f.listing != nil {
			body, err := f.listing(dir)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(body)
			return
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

// installLocked installs a skill folder and records it in the skills lock,
// next to the skills already there.
func installLocked(t *testing.T, home, name string, files map[string]string, tree string) string {
	t.Helper()
	dir := filepath.Join(home, ".agents/skills", name)
	for p, body := range files {
		writeSkillFile(t, filepath.Join(dir, p), body)
	}
	lockPath := filepath.Join(home, ".agents/.skill-lock.json")
	lock := struct {
		Version int            `json:"version"`
		Skills  map[string]any `json:"skills"`
	}{3, map[string]any{}}
	if data, err := os.ReadFile(lockPath); err == nil {
		json.Unmarshal(data, &lock)
	}
	lock.Skills[name] = map[string]any{
		"source": "acme/skills", "sourceType": "github", "sourceUrl": "https://github.com/acme/skills.git",
		"skillPath": "skills/" + name + "/SKILL.md", "skillFolderHash": tree,
	}
	data, _ := json.Marshal(lock)
	writeSkillFile(t, lockPath, string(data))
	return dir
}

// TestSkillUpdatesReadNoTranscripts: listing origins, checking and
// upgrading need only the skill folders' entries, not the usage records a
// full scan reads; an upgrade checks what is in use once for all folders.
func TestSkillUpdatesReadNoTranscripts(t *testing.T) {
	home := skillFixture(t)
	writeSkillFile(t, opencodeDB(), "")
	queries := 0
	opencodeQuery = func(context.Context, string, string) ([]byte, error) { queries++; return []byte("[]"), nil }
	probes := 0
	saved := openFilesHook
	openFilesHook = func() ([]string, error) { probes++; return nil, nil }
	t.Cleanup(func() { openFilesHook = saved })
	gh := newFakeGitHub(t, map[string]string{"skills/a/SKILL.md": "a1\n", "skills/b/SKILL.md": "b1\n"})
	repo := gh.materialize(t)
	treeA, _ := gitTreeHash(filepath.Join(repo, "skills/a"))
	treeB, _ := gitTreeHash(filepath.Join(repo, "skills/b"))
	a := installLocked(t, home, "a", map[string]string{"SKILL.md": "a1\n"}, treeA)
	b := installLocked(t, home, "b", map[string]string{"SKILL.md": "b1\n"}, treeB)
	gh.files["skills/a/SKILL.md"], gh.files["skills/b/SKILL.md"] = "a2\n", "b2\n"

	if ups, err := (Cleaner{}).ListSkillOrigins(context.Background()); err != nil || len(ups) != 2 {
		t.Fatalf("origins = %+v, %v", ups, err)
	}
	if ups, err := (Cleaner{}).CheckSkillUpdates(context.Background()); err != nil || len(ups) != 2 || ups[0].Status != "update" {
		t.Fatalf("check = %+v, %v", ups, err)
	}
	res, err := Cleaner{}.UpgradeSkills(context.Background(), []SkillUpgradeRequest{{Path: a}, {Path: b}})
	if err != nil || len(res) != 2 || res[0].Error != "" || res[1].Error != "" {
		t.Fatalf("upgrade = %+v, %v", res, err)
	}
	if queries != 0 {
		t.Fatalf("usage records read %d times", queries)
	}
	if probes != 1 {
		t.Fatalf("usage probed %d times for one upgrade", probes)
	}
}

func noOpenFiles(t *testing.T) {
	saved := openFilesHook
	openFilesHook = func() ([]string, error) { return nil, nil }
	t.Cleanup(func() { openFilesHook = saved })
}

// TestSkillEndpointsIgnoreEnvironment: skill downloads and the GitHub token
// go to GitHub whatever the environment names. The endpoints are read in a
// fresh process started with the variables set.
func TestSkillEndpointsIgnoreEnvironment(t *testing.T) {
	if os.Getenv("CLEANZO_ENDPOINT_PROBE") == "1" {
		fmt.Printf("endpoints=%s %s\n", skillGitHubAPI, skillCodeload)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSkillEndpointsIgnoreEnvironment$", "-test.count=1")
	cmd.Env = append(os.Environ(), "CLEANZO_ENDPOINT_PROBE=1",
		"CLEANZO_GITHUB_API=http://127.0.0.1:9/api", "CLEANZO_CODELOAD=http://127.0.0.1:9/load")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "endpoints=https://api.github.com https://codeload.github.com\n") {
		t.Fatalf("endpoints follow the environment:\n%s", out)
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

// TestIdenticalToUpstreamIsCurrent: a folder with the source's tree is
// current and unmodified, even when the tree recorded at install is older
// (the skill was brought up to date some other way).
func TestIdenticalToUpstreamIsCurrent(t *testing.T) {
	home := skillFixture(t)
	gh := newFakeGitHub(t, map[string]string{"skills/doc/SKILL.md": "v1\n"})
	tree1, _ := gitTreeHash(filepath.Join(gh.materialize(t), "skills/doc"))
	gh.files["skills/doc/SKILL.md"] = "v2\n"
	installLocked(t, home, "doc", map[string]string{"SKILL.md": "v2\n"}, tree1)
	ups, err := Cleaner{}.CheckSkillUpdates(context.Background())
	if err != nil || len(ups) != 1 {
		t.Fatalf("ups = %+v, %v", ups, err)
	}
	if u := ups[0]; u.Status != "current" || u.Modified || u.Remote != u.Local || u.Remote == tree1 {
		t.Fatalf("identical to upstream = %+v", u)
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

// fakeCCSwitch stands in for CC Switch's database with the given rows.
func fakeCCSwitch(t *testing.T, home, rows string) {
	t.Helper()
	writeSkillFile(t, filepath.Join(home, ".cc-switch/cc-switch.db"), "")
	saved := ccSwitchQuery
	ccSwitchQuery = func(context.Context, string) ([]byte, error) { return []byte(rows), nil }
	t.Cleanup(func() { ccSwitchQuery = saved })
}

// TestCCSwitchNameMatchNeedsForce: without a CC Switch store to tell its
// folders apart, a folder that only shares a CC Switch skill's name and
// differs from the source counts as modified, so upgrading it needs Force.
func TestCCSwitchNameMatchNeedsForce(t *testing.T) {
	home := skillFixture(t)
	noOpenFiles(t)
	newFakeGitHub(t, map[string]string{"skills/lark-doc/SKILL.md": "upstream\n"})
	dir := filepath.Join(home, ".agents/skills/lark-doc")
	writeSkill(t, dir, "my own\n")
	fakeCCSwitch(t, home, `[{"directory":"lark-doc","repo_owner":"acme","repo_name":"skills","repo_branch":"main","readme_url":"https://github.com/acme/skills/blob/main/skills/lark-doc/SKILL.md"}]`)
	ups, err := Cleaner{}.CheckSkillUpdates(context.Background())
	if err != nil || len(ups) != 1 || ups[0].Origin.Kind != originCCSwitch || ups[0].Status != "update" || !ups[0].Modified {
		t.Fatalf("name match = %+v, %v", ups, err)
	}
	res, _ := Cleaner{}.UpgradeSkills(context.Background(), []SkillUpgradeRequest{{Path: dir}})
	if res[0].Error != "modified" {
		t.Fatalf("unforced = %+v", res[0])
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(b) != "my own\n" {
		t.Fatal("a folder that only shares the name was replaced without Force")
	}
	res, _ = Cleaner{}.UpgradeSkills(context.Background(), []SkillUpgradeRequest{{Path: dir, Force: true}})
	if res[0].Error != "" {
		t.Fatalf("forced = %+v", res[0])
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(b) != "upstream\n" {
		t.Fatalf("forced upgrade left %q", b)
	}
}

// TestCCSwitchUnifiedStore: CC Switch's settings choose its store. With
// "unified" it keeps its skills as real folders in ~/.agents/skills, and an
// old ~/.cc-switch/skills left behind is not the store.
func TestCCSwitchUnifiedStore(t *testing.T) {
	home := skillFixture(t)
	noOpenFiles(t)
	newFakeGitHub(t, map[string]string{"skills/lark-doc/SKILL.md": "upstream\n", "skills/mine/SKILL.md": "upstream\n"})
	writeSkillFile(t, filepath.Join(home, ".cc-switch/settings.json"), `{"language":"zh","skillStorageLocation":"unified"}`)
	writeSkill(t, filepath.Join(home, ".cc-switch/skills/lark-doc"), "stale\n")
	dir := filepath.Join(home, ".agents/skills/lark-doc")
	writeSkill(t, dir, "older\n")
	writeSkill(t, filepath.Join(home, ".claude/skills/mine"), "my own\n")
	fakeCCSwitch(t, home, `[
		{"directory":"lark-doc","repo_owner":"acme","repo_name":"skills","readme_url":"https://github.com/acme/skills/blob/main/skills/lark-doc/SKILL.md"},
		{"directory":"mine","repo_owner":"acme","repo_name":"skills","readme_url":"https://github.com/acme/skills/blob/main/skills/mine/SKILL.md"}]`)
	ups, err := Cleaner{}.CheckSkillUpdates(context.Background())
	if err != nil || len(ups) != 1 || ups[0].Path != resolvePath(dir) || ups[0].Origin.Kind != originCCSwitch {
		t.Fatalf("unified store = %+v, %v", ups, err)
	}
	// A folder in the store is CC Switch's own: an update, not a local change.
	if ups[0].Status != "update" || ups[0].Modified {
		t.Fatalf("store folder = %+v", ups[0])
	}
}

// TestCCSwitchUnreadableSettings: when CC Switch's settings cannot be read,
// its store is unknown, so a row names a folder by name alone and a
// difference from the source counts as a local change.
func TestCCSwitchUnreadableSettings(t *testing.T) {
	for name, settings := range map[string]string{"truncated": `{"skillStorageLocation":`, "missing": ""} {
		t.Run(name, func(t *testing.T) {
			home := skillFixture(t)
			noOpenFiles(t)
			newFakeGitHub(t, map[string]string{"skills/mine/SKILL.md": "upstream\n"})
			if settings != "" {
				writeSkillFile(t, filepath.Join(home, ".cc-switch/settings.json"), settings)
			}
			writeSkill(t, filepath.Join(home, ".cc-switch/skills/other"), "x\n")
			dir := filepath.Join(home, ".claude/skills/mine")
			writeSkill(t, dir, "my own\n")
			fakeCCSwitch(t, home, `[{"directory":"mine","repo_owner":"acme","repo_name":"skills","readme_url":"https://github.com/acme/skills/blob/main/skills/mine/SKILL.md"}]`)
			ups, err := Cleaner{}.CheckSkillUpdates(context.Background())
			if err != nil || len(ups) != 1 || ups[0].Path != resolvePath(dir) || ups[0].Origin.Kind != originCCSwitch || !ups[0].Modified {
				t.Fatalf("unknown store = %+v, %v", ups, err)
			}
		})
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
}
