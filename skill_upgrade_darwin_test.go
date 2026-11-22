package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseSkillArchiveOpenFiles(t *testing.T) {
	out := []byte("p12\nfcwd\nn/skills/cwd\nftxt\nn/skills/executable\nf3\nar\nn/skills/read-only\nf4\naw\nn/skills/writing\nf5\nau\nn/skills/reading-and-writing\nf6\nn/skills/unknown\np15\nf7\nar\nn/skills/reading-and-writing\n")
	want := []string{"/skills/cwd", "/skills/executable", "/skills/reading-and-writing", "/skills/unknown", "/skills/writing"}
	if got := parseSkillArchiveOpenFiles(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("blocking paths = %v, want %v", got, want)
	}
}

func TestSkillArchiveUpgradeWithOpenFiles(t *testing.T) {
	home := skillFixture(t)
	gh := newFakeGitHub(t, map[string]string{"skills/reader/SKILL.md": "old", "skills/writer/SKILL.md": "old"})
	repo := gh.materialize(t)
	oldTree, err := gitTreeHash(filepath.Join(repo, "skills/reader"))
	if err != nil {
		t.Fatal(err)
	}
	reader := installLocked(t, home, "reader", map[string]string{"SKILL.md": "old"}, oldTree)
	writer := installLocked(t, home, "writer", map[string]string{"SKILL.md": "old"}, oldTree)
	gh.files["skills/reader/SKILL.md"], gh.files["skills/writer/SKILL.md"] = "new", "new"

	read, err := os.Open(filepath.Join(reader, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	type writeHandle struct {
		file *os.File
		err  error
	}
	handles := make(chan writeHandle, 1)
	gh.beforeArchive = func() {
		file, err := os.OpenFile(filepath.Join(writer, "SKILL.md"), os.O_WRONLY, 0)
		if err == nil {
			t.Cleanup(func() { file.Close() })
		}
		handles <- writeHandle{file, err}
	}

	res, err := (Cleaner{}).UpgradeSkills(context.Background(), []SkillUpgradeRequest{
		{Path: reader}, {Path: writer, Force: true},
	})
	if err != nil || len(res) != 2 || res[0].Error != "" || res[1].Error != errSkillInUse.Error() {
		t.Fatalf("upgrade = %+v, %v", res, err)
	}
	if handle := <-handles; handle.err != nil || handle.file == nil {
		t.Fatalf("could not open writer during download: %v", handle.err)
	}
	var old [3]byte
	if _, err := read.ReadAt(old[:], 0); err != nil || string(old[:]) != "old" {
		t.Fatalf("open handle = %q, %v", old, err)
	}
	assertSkillBody(t, reader, "new")
	assertSkillBody(t, writer, "old")
	assertSkillBody(t, filepath.Join(home, ".Trash", "reader"), "old")
}

func TestDarwinSkillSwapRestoresOriginalWhenTrashFails(t *testing.T) {
	root := t.TempDir()
	live, source := filepath.Join(root, "skill"), filepath.Join(root, "source")
	writeSkillFile(t, filepath.Join(live, "SKILL.md"), "old")
	writeSkillFile(t, filepath.Join(source, "SKILL.md"), "new")
	refused := errors.New("trash refused")
	oldTrash := trashItem
	t.Cleanup(func() { trashItem = oldTrash })
	var displaced string
	trashItem = func(backup string) error {
		displaced = backup
		assertSkillBody(t, live, "new")
		assertSkillBody(t, backup, "old")
		writeSkillFile(t, filepath.Join(live, "SKILL.md"), "new version with an edit")
		return refused
	}
	if err := swapSkillFolder(context.Background(), live, source, nil); !errors.Is(err, refused) {
		t.Fatalf("swap error = %v, want %v", err, refused)
	}
	assertSkillBody(t, live, "old")
	assertSkillBody(t, displaced, "new version with an edit")
}

func assertSkillBody(t *testing.T, directory, body string) {
	t.Helper()
	if got, err := os.ReadFile(filepath.Join(directory, "SKILL.md")); err != nil || string(got) != body {
		t.Fatalf("%s: got %q, %v, want %q", directory, got, err, body)
	}
}
