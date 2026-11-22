package main

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillCatalogBrowseBindAndInstall(t *testing.T) {
	ctx := context.Background()
	home := skillFixture(t)
	gh := newFakeGitHub(t, map[string]string{"skills/demo/SKILL.md": "---\ndescription: Example\n---\n", "other/example/SKILL.md": "example"})
	gh.listing = catalogTestListing
	c := Cleaner{}
	entries, err := c.BrowseSkillRepository(ctx, "https://github.com/acme/skills.git", "", "skills")
	if err != nil || len(entries) != 1 || entries[0].Name != "demo" || entries[0].Origin.Path != "skills/demo" || entries[0].Origin.Ref != "" {
		t.Fatalf("browse = %+v, %v", entries, err)
	}
	if gh.tarCalls.Load() != 0 {
		t.Fatal("browse downloaded a repository archive")
	}
	origin := entries[0].Origin
	installed, err := c.InstallCatalogSkill(ctx, origin, []string{"agents", "claude"})
	if err != nil || len(installed) != 2 || installed[0].Error != "" || installed[1].Error != "" {
		t.Fatalf("install = %+v, %v", installed, err)
	}
	a := filepath.Join(home, ".agents", "skills", "demo")
	if got, err := os.ReadFile(filepath.Join(a, "SKILL.md")); err != nil || string(got) != gh.files["skills/demo/SKILL.md"] {
		t.Fatalf("installed = %q, %v", got, err)
	}
	preview, err := c.BrowseSkillRepository(ctx, "acme/skills", "", "skills")
	if err != nil || len(preview) != 1 || len(preview[0].Installed) != 2 {
		t.Fatalf("known installs = %+v, %v", preview, err)
	}
	installed, err = c.InstallCatalogSkill(ctx, origin, []string{"agents"})
	if err != nil || len(installed) != 1 || installed[0].Error == "" {
		t.Fatalf("existing = %+v, %v", installed, err)
	}
	existing := filepath.Join(home, ".codex", "skills", "my-demo")
	writeSkill(t, existing, "local edits")
	result, err := c.BindCatalogSkillSource(ctx, existing, origin)
	if err != nil || !result.Modified || result.Status != "update" {
		t.Fatalf("binding = %+v, %v", result, err)
	}
	if got, err := os.ReadFile(filepath.Join(existing, "SKILL.md")); err != nil || string(got) != "local edits" {
		t.Fatalf("binding edited source: %q %v", got, err)
	}
	next, err := c.UpgradeSkills(ctx, []SkillUpgradeRequest{{Path: existing}})
	if err != nil || len(next) != 1 || next[0].Error != errSkillModified.Error() {
		t.Fatalf("upgrade not guarded = %+v %v", next, err)
	}
}

func catalogTestListing(dir string) (any, error) {
	tree := []map[string]string{}
	root, err := gitTreeHash(dir)
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(dir, func(p string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if e.IsDir() {
			hash, err := gitTreeHash(p)
			if err != nil {
				return err
			}
			tree = append(tree, map[string]string{"path": rel, "type": "tree", "sha": hash})
		} else {
			tree = append(tree, map[string]string{"path": rel, "type": "blob", "mode": "100644", "sha": strings.Repeat("a", 40)})
		}
		return nil
	})
	return map[string]any{"sha": root, "tree": tree}, err
}

func TestSkillCatalogRejectsUnsafeCoordinatesAndDestination(t *testing.T) {
	for _, id := range []string{"https://github.com.evil.test/acme/skills", "https://user@github.com/acme/skills", "../skills", "acme/..", "acme/skills?x=y"} {
		if _, err := catalogRepo(id); err == nil {
			t.Errorf("accepted repository %q", id)
		}
	}
	for _, subdir := range []string{"/skills/demo", "skills/../demo", "skills\\demo", "skills//demo", ".git/hooks"} {
		if err := catalogRelativePath(subdir); err == nil {
			t.Errorf("accepted path %q", subdir)
		}
	}
	if _, err := (Cleaner{}).InstallCatalogSkill(context.Background(), SkillOrigin{Repo: "acme/skills"}, []string{"../../tmp"}); err == nil {
		t.Fatal("unknown install target accepted")
	}
	first, second := filepath.Join(t.TempDir(), "staged"), filepath.Join(t.TempDir(), "live")
	writeSkill(t, first, "source")
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := publishCatalogSkill(first, second); err == nil {
		t.Fatal("overwrote existing empty directory")
	}
	if _, err := os.Stat(filepath.Join(first, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(second, "SKILL.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestSkillCatalogDiscoveryKeepsRepositoryAndAlias(t *testing.T) {
	skillFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "test api" {
			t.Error("search query was not encoded")
		}
		_, _ = w.Write([]byte(`{"skills":[{"id":"acme/skills/demo","skillId":"demo","name":"Demo","source":"acme/skills","installs":42},{"id":"bad","skillId":"bad","source":"p/../other"}]}`))
	}))
	defer server.Close()
	results, err := searchSkillCatalog(context.Background(), server.URL, "test api")
	if err != nil || len(results) != 1 || results[0].Repo != "acme/skills" || results[0].Slug != "demo" || results[0].Installs != 42 {
		t.Fatalf("results = %+v %v", results, err)
	}
}

func TestSkillCatalogImportPreservesOriginalAndRefusesLinks(t *testing.T) {
	ctx := context.Background()
	home := skillFixture(t)
	local := filepath.Join(t.TempDir(), "demo")
	writeSkill(t, local, "original")
	c := Cleaner{}
	results, err := c.ImportCatalogSkillFolder(ctx, local, []string{"agents"})
	if err != nil || len(results) != 1 || results[0].Error != "" {
		t.Fatalf("import = %+v, %v", results, err)
	}
	if _, err := os.Stat(filepath.Join(local, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "outer")
	writeSkill(t, external, "outside")
	if err := os.Symlink(external, filepath.Join(local, "outside")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := c.ImportCatalogSkillFolder(ctx, local, []string{"codex"}); err == nil {
		t.Fatal("import accepted external link")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "demo")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("failed import published a folder: %v", err)
	}
}

func TestSkillCatalogDeclinesSymlinkedInstallRoot(t *testing.T) {
	ctx := context.Background()
	home := skillFixture(t)
	local := filepath.Join(t.TempDir(), "demo")
	writeSkill(t, local, "original")
	other := filepath.Join(home, "elsewhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(home, ".claude")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	results, err := (Cleaner{}).ImportCatalogSkillFolder(ctx, local, []string{"claude"})
	if err != nil || len(results) != 1 || results[0].Error == "" {
		t.Fatalf("import at symlinked root = %+v, %v", results, err)
	}
	if _, err := os.Stat(filepath.Join(other, "skills")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("redirected directory was created: %v", err)
	}
}
