package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// gitListing lists a folder as GitHub's tree API does, from git itself: every
// tree and blob with its hash and mode. Nothing is ignored, so a .DS_Store
// in the folder is part of the listing as it is of a commit.
func gitListing(dir string) (any, error) {
	home, err := os.MkdirTemp("", "gitlisting")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(home)
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+home, "XDG_CONFIG_HOME="+home)
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := git("init", "-q"); err != nil {
		return nil, err
	}
	if _, err := git("add", "-A"); err != nil {
		return nil, err
	}
	root, err := git("write-tree")
	if err != nil {
		return nil, err
	}
	out, err := git("ls-tree", "-r", "-t", "-z", root)
	if err != nil {
		return nil, err
	}
	tree := []map[string]string{}
	for _, line := range strings.Split(out, "\x00") {
		meta, name, ok := strings.Cut(line, "\t")
		if f := strings.Fields(meta); ok && len(f) == 3 {
			tree = append(tree, map[string]string{"mode": f[0], "type": f[1], "sha": f[2], "path": name})
		}
	}
	return map[string]any{"sha": root, "tree": tree, "truncated": false}, nil
}

// TestDSStoreCountsWhereTheSourceHasOne: a .DS_Store the repository commits
// is part of the skill, so the folder is unmodified and its upgrade matches;
// one Finder left where the source has none is still left out.
func TestDSStoreCountsWhereTheSourceHasOne(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := skillFixture(t)
	noOpenFiles(t)
	gh := newFakeGitHub(t, map[string]string{
		"skills/doc/SKILL.md":       "v1\n",
		"skills/doc/.DS_Store":      "committed\n",
		"skills/doc/scripts/run.sh": "#!/bin/sh\n",
	})
	gh.listing = gitListing
	listed, err := gitListing(gh.materialize(t))
	if err != nil {
		t.Fatal(err)
	}
	tree1 := ""
	for _, e := range listed.(map[string]any)["tree"].([]map[string]string) {
		if e["path"] == "skills/doc" {
			tree1 = e["sha"]
		}
	}
	dir := installLocked(t, home, "doc", map[string]string{
		"SKILL.md": "v1\n", ".DS_Store": "committed\n", "scripts/run.sh": "#!/bin/sh\n", "scripts/.DS_Store": "finder\n",
	}, tree1)

	ups, _ := Cleaner{}.CheckSkillUpdates(context.Background())
	if len(ups) != 1 || ups[0].Status != "current" || ups[0].Modified || ups[0].Local != tree1 {
		t.Fatalf("committed .DS_Store: %+v (want local %s)", ups, tree1)
	}
	gh.files["skills/doc/SKILL.md"] = "v2\n"
	ups, _ = Cleaner{}.CheckSkillUpdates(context.Background())
	if ups[0].Status != "update" || ups[0].Modified {
		t.Fatalf("after upstream change: %+v", ups[0])
	}
	res, err := Cleaner{}.UpgradeSkills(context.Background(), []SkillUpgradeRequest{{Path: dir}})
	if err != nil || len(res) != 1 || res[0].Error != "" || res[0].Tree != ups[0].Remote {
		t.Fatalf("upgrade = %+v, %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".DS_Store")); string(b) != "committed\n" {
		t.Fatalf(".DS_Store = %q", b)
	}
	ups, _ = Cleaner{}.CheckSkillUpdates(context.Background())
	if ups[0].Origin.Kind != originCleanzo || ups[0].Status != "current" || ups[0].Modified {
		t.Fatalf("after upgrade = %+v", ups[0])
	}
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
		// Each link stays inside the archive on paper; followed one after the
		// other they climb out, and the file lands above dest.
		"through-links": {{Name: "r/", Typeflag: tar.TypeDir}, {Name: "r/up", Typeflag: tar.TypeSymlink, Linkname: ".."},
			{Name: "r/up/up2", Typeflag: tar.TypeSymlink, Linkname: ".."}, {Name: "r/up/up2/escaped", Typeflag: tar.TypeReg, Size: 1}},
		"file-under-link": {{Name: "r/", Typeflag: tar.TypeDir}, {Name: "r/d/", Typeflag: tar.TypeDir},
			{Name: "r/l", Typeflag: tar.TypeSymlink, Linkname: "d"}, {Name: "r/l/f", Typeflag: tar.TypeReg, Size: 1}},
	}
	for name, hdrs := range cases {
		base := t.TempDir()
		dest := filepath.Join(base, "in", "x")
		if _, err := untarSafe(bytes.NewReader(build(hdrs...)), dest); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if fileExists(filepath.Join(base, "escaped")) || fileExists(filepath.Join(base, "in", "escaped")) {
			t.Errorf("%s: wrote outside the destination", name)
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
