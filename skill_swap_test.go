package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestWindowsSkillSwapRestoresOriginal checks install-rename and recycling failures before any old copy is lost.
func TestWindowsSkillSwapRestoresOriginal(t *testing.T) {
	for _, failure := range []string{"install", "recycle"} {
		t.Run(failure, func(t *testing.T) {
			env := testEnv(t)
			folder := filepath.Join(env.home, ".agents", "skills", "fixture")
			src := filepath.Join(env.home, "source")
			writeSkillFile(t, filepath.Join(folder, "SKILL.md"), "original")
			writeSkillFile(t, filepath.Join(src, "SKILL.md"), "replacement")
			savedRename, savedTrash := skillRename, trashItem
			t.Cleanup(func() { skillRename, trashItem = savedRename, savedTrash })
			injected := errors.New("injected sharing violation")
			skillRename = func(old, new string) error {
				if failure == "install" && old == filepath.Join(filepath.Dir(folder), ".fixture.cleanzo-new") {
					return injected
				}
				return os.Rename(old, new)
			}
			trashItem = func(string) error {
				if failure == "recycle" {
					return injected
				}
				t.Fatal("recycled old folder before install succeeded")
				return nil
			}
			if err := swapWindowsSkillFolder(folder, src); !errors.Is(err, injected) {
				t.Fatalf("swap error=%v", err)
			}
			data, err := os.ReadFile(filepath.Join(folder, "SKILL.md"))
			if err != nil || string(data) != "original" {
				t.Fatalf("original was not restored: %q %v", data, err)
			}
			if exists(filepath.Join(filepath.Dir(folder), ".fixture.cleanzo-new")) {
				t.Fatal("staging folder leaked")
			}
		})
	}
}
