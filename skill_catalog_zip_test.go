package main

import (
	"archive/zip"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillCatalogZipInstallsOneSkillWithoutOverwrite(t *testing.T) {
	home := skillFixture(t)
	file := catalogZipFixture(t, map[string]string{"example/SKILL.md": "skill", "example/reference.md": "notes", "__MACOSX/example/._SKILL.md": "metadata"})
	c := Cleaner{}
	results, err := c.ImportCatalogSkillZIP(context.Background(), file, []string{"agents", "claude"})
	if err != nil || len(results) != 2 || results[0].Error != "" || results[1].Error != "" {
		t.Fatalf("ZIP installation = %+v, %v", results, err)
	}
	content, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "example", "reference.md"))
	if err != nil || string(content) != "notes" {
		t.Fatalf("installed reference = %q, %v", content, err)
	}
	results, err = c.ImportCatalogSkillZIP(context.Background(), file, []string{"agents"})
	if err != nil || len(results) != 1 || results[0].Error != errCatalogDestinationExists.Error() {
		t.Fatalf("repeated installation = %+v, %v", results, err)
	}
}

func TestSkillCatalogZipRefusesTraversalAndMultipleSkills(t *testing.T) {
	home := skillFixture(t)
	ctx := context.Background()
	c := Cleaner{}
	for _, entries := range []map[string]string{
		{"example/SKILL.md": "skill", "example/../../escaped": "outside"},
		{"example/SKILL.md": "skill", "second/SKILL.md": "ambiguous"},
		{"example/SKILL.md": "skill", "/outside": "absolute"},
	} {
		if _, err := c.ImportCatalogSkillZIP(ctx, catalogZipFixture(t, entries), []string{"agents"}); err == nil {
			t.Fatalf("accepted unsafe or ambiguous ZIP: %v", entries)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "example")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("invalid ZIP published a skill: %v", err)
	}
}

func catalogZipFixture(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "example.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, contents := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
