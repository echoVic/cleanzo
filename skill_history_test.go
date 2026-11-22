package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillHistoryRecoversRemovedFolder(t *testing.T) {
	home := skillFixture(t)
	noOpenFiles(t)
	folder := filepath.Join(home, ".agents", "skills", "example")
	writeSkill(t, folder, "locally edited content")
	c := Cleaner{}
	removed := c.TrashSkills([]string{folder})
	if removed.Moved != 1 {
		t.Fatalf("trash = %+v", removed)
	}
	entries, err := c.ListSkillHistory("")
	if err != nil || len(entries) != 1 || entries[0].Action != "remove" {
		t.Fatalf("history = %+v, %v", entries, err)
	}
	if err := c.RestoreSkillVersion(context.Background(), entries[0].ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(folder, "SKILL.md"))
	if err != nil || string(got) != "locally edited content" {
		t.Fatalf("restored = %q, %v", got, err)
	}
}

func TestSkillHistoryRollsBackUpgradeAndRecordsDisplacedVersion(t *testing.T) {
	home := skillFixture(t)
	noOpenFiles(t)
	gh := newFakeGitHub(t, map[string]string{"skills/example/SKILL.md": "initial"})
	repo := gh.materialize(t)
	tree, err := gitTreeHash(filepath.Join(repo, "skills", "example"))
	if err != nil {
		t.Fatal(err)
	}
	folder := installLocked(t, home, "example", map[string]string{"SKILL.md": "locally edited content"}, tree)
	gh.files["skills/example/SKILL.md"] = "new content"
	c := Cleaner{}
	out, err := c.UpgradeSkills(context.Background(), []SkillUpgradeRequest{{Path: folder, Force: true}})
	if err != nil || len(out) != 1 || out[0].Error != "" {
		t.Fatalf("upgrade = %+v, %v", out, err)
	}
	entries, err := c.ListSkillHistory(folder)
	if err != nil || len(entries) != 1 || entries[0].Action != "upgrade" {
		t.Fatalf("history = %+v, %v", entries, err)
	}
	if err := c.RestoreSkillVersion(context.Background(), entries[0].ID, false); err == nil {
		t.Fatal("overwrote an existing folder without confirmation")
	}
	if err := c.RestoreSkillVersion(context.Background(), entries[0].ID, true); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(folder, "SKILL.md"))
	if err != nil || string(got) != "locally edited content" {
		t.Fatalf("restored = %q, %v", got, err)
	}
	entries, err = c.ListSkillHistory(folder)
	if err != nil || len(entries) != 2 || entries[0].Action != "restore" {
		t.Fatalf("history after rollback = %+v, %v", entries, err)
	}
}

func TestSkillUpgradePreviewShowsBoundedFileChanges(t *testing.T) {
	home := skillFixture(t)
	local := map[string]string{"SKILL.md": "same header", "modified.md": "old", "removed.md": "old"}
	upstream := map[string]string{}
	for name, body := range local {
		upstream["skills/example/"+name] = body
	}
	gh := newFakeGitHub(t, upstream)
	repo := gh.materialize(t)
	tree, err := gitTreeHash(filepath.Join(repo, "skills", "example"))
	if err != nil {
		t.Fatal(err)
	}
	folder := installLocked(t, home, "example", local, tree)
	upstream["skills/example/modified.md"] = "new"
	upstream["skills/example/added.md"] = "new"
	delete(upstream, "skills/example/removed.md")
	preview, err := (Cleaner{}).PreviewSkillUpgrade(context.Background(), folder)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Total != 3 || len(preview.Changes) != 3 {
		t.Fatalf("preview = %+v", preview)
	}
	for i, kind := range []string{"added", "changed", "removed"} {
		if preview.Changes[i].Kind != kind {
			t.Fatalf("preview = %+v", preview)
		}
	}
}

func TestSkillHistoryRejectsTamperedSavedFiles(t *testing.T) {
	home := skillFixture(t)
	noOpenFiles(t)
	folder := filepath.Join(home, ".agents", "skills", "example")
	writeSkill(t, folder, "original")
	c := Cleaner{}
	if r := c.TrashSkills([]string{folder}); r.Moved != 1 {
		t.Fatalf("trash = %+v", r)
	}
	entries, err := c.ListSkillHistory("")
	if err != nil || len(entries) != 1 {
		t.Fatalf("history = %+v, %v", entries, err)
	}
	writeSkill(t, filepath.Join(skillHistoryDir(), entries[0].ID, "contents"), "tampered")
	if err := c.RestoreSkillVersion(context.Background(), entries[0].ID, false); err == nil {
		t.Fatal("restored edited backup")
	}
	if _, err := os.Lstat(folder); !os.IsNotExist(err) {
		t.Fatalf("destination changed: %v", err)
	}
}

func TestSkillHistoryIgnoresRemovalBlockedByTrash(t *testing.T) {
	home := skillFixture(t)
	noOpenFiles(t)
	folder := filepath.Join(home, ".agents", "skills", "example")
	writeSkill(t, folder, "original")
	originalTrash := trashItem
	t.Cleanup(func() { trashItem = originalTrash })
	trashItem = func(string) error { return fs.ErrPermission }
	c := Cleaner{}
	if r := c.TrashSkills([]string{folder}); r.Moved != 0 {
		t.Fatalf("trash = %+v", r)
	}
	entries, err := c.ListSkillHistory("")
	if err != nil || len(entries) != 0 {
		t.Fatalf("history = %+v, %v", entries, err)
	}
}
