package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestRepairSkillLinkChecksExpectedTarget(t *testing.T) {
	home := skillFixture(t)
	source := filepath.Join(home, ".agents", "skills", "sample")
	writeSkill(t, source, "new")
	dest := filepath.Join(home, ".hermes", "skills", "sample")
	mustMkdir(t, filepath.Dir(dest))
	if err := os.Symlink("gone", dest); err != nil {
		t.Fatal(err)
	}
	req := SkillDeployRequest{Name: "sample", Source: source, Tool: "hermes", Mode: "link", Repair: true, PreviousTarget: "changed"}
	if err := (Cleaner{}).DeploySkill(context.Background(), req); !errors.Is(err, errSkillDiverged) {
		t.Fatalf("expected a changed target error, got %v", err)
	}
	if got, err := os.Readlink(dest); err != nil || got != "gone" {
		t.Fatalf("existing link changed = %q, %v", got, err)
	}
	req.PreviousTarget = "gone"
	if err := (Cleaner{}).DeploySkill(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(dest); err != nil || real != resolvePath(source) {
		t.Fatalf("repaired link = %q, %v", real, err)
	}
	if err := (Cleaner{}).DeploySkill(context.Background(), req); !errors.Is(err, errSkillDiverged) {
		t.Fatalf("overwriting a now-live link: %v", err)
	}
}

func TestDeploySkillRejectsSymlinkedTargetRoot(t *testing.T) {
	home := skillFixture(t)
	source := filepath.Join(home, ".agents", "skills", "sample")
	writeSkill(t, source, "new")
	foreign := t.TempDir()
	if err := os.Symlink(foreign, filepath.Join(home, ".hermes")); err != nil {
		t.Fatal(err)
	}
	req := SkillDeployRequest{Name: "sample", Source: source, Tool: "hermes", Mode: "link"}
	if err := (Cleaner{}).DeploySkill(context.Background(), req); err == nil {
		t.Fatal("wrote through a redirected tool root")
	}
	if _, err := os.Lstat(filepath.Join(foreign, "skills")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("destination root was modified: %v", err)
	}
}

func TestDeploySkillRejectsSourceTargetUnderRedirectedRoot(t *testing.T) {
	home := skillFixture(t)
	foreign := filepath.Join(home, "unrelated", "sample")
	writeSkill(t, foreign, "not a known skill entry")
	alternateRoot := filepath.Join(home, ".grok", "skills")
	mustMkdir(t, filepath.Dir(alternateRoot))
	if err := os.Symlink(filepath.Dir(foreign), alternateRoot); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, ".agents", "skills", "sample")
	mustMkdir(t, filepath.Dir(source))
	if err := os.Symlink(foreign, source); err != nil {
		t.Fatal(err)
	}
	req := SkillDeployRequest{Name: "sample", Source: source, Tool: "hermes", Mode: "link"}
	if err := (Cleaner{}).DeploySkill(context.Background(), req); !errors.Is(err, errSkillNotEntry) {
		t.Fatalf("accepted source under a redirected root: %v", err)
	}
}
