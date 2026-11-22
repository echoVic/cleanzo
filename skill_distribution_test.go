package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillDistributionShowsPhysicalAndSharedAvailability(t *testing.T) {
	home := skillFixture(t)
	writeSkill(t, filepath.Join(home, ".agents", "skills", "sample"), "shared")
	writeSkill(t, filepath.Join(home, ".claude", "skills", "sample"), "local")
	rows, err := (Cleaner{}).ListSkillDistribution(context.Background(), "sample")
	if err != nil {
		t.Fatal(err)
	}
	byTool := map[string]SkillDeployment{}
	for _, row := range rows {
		byTool[row.Tool] = row
	}
	if got := byTool["codex"]; got.State != "absent" || !got.Shared || got.Availability != "shared" {
		t.Fatalf("Codex = %+v", got)
	}
	if got := byTool["claude"]; got.State != "directory" || got.Shared || got.Availability != "direct" {
		t.Fatalf("Claude = %+v", got)
	}
	if got := byTool["pi"]; got.State != "absent" || !got.Shared || got.Availability != "shared" {
		t.Fatalf("Pi = %+v", got)
	}
	if got := byTool["hermes"]; got.State != "absent" || got.Availability != "unknown" {
		t.Fatalf("Hermes = %+v", got)
	}
}

func TestDeploySkillCopiesAndProtectsExistingDestination(t *testing.T) {
	home := skillFixture(t)
	source := filepath.Join(home, ".agents", "skills", "sample")
	writeSkill(t, source, "new")
	request := SkillDeployRequest{Name: "sample", Source: source, Tool: "hermes", Mode: "copy"}
	if err := (Cleaner{}).DeploySkill(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(home, ".hermes", "skills", "sample", "SKILL.md")
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "new" {
		t.Fatalf("destination = %q, %v", data, err)
	}
	writeSkill(t, filepath.Dir(destination), "independent edits")
	if err := (Cleaner{}).DeploySkill(context.Background(), request); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("overwriting existing copy: %v", err)
	}
	data, err = os.ReadFile(destination)
	if err != nil || string(data) != "independent edits" {
		t.Fatalf("original destination = %q, %v", data, err)
	}
}

func TestDeploySkillRejectsForeignSourceAndTraversal(t *testing.T) {
	home := skillFixture(t)
	source := filepath.Join(home, "unrelated", "sample")
	writeSkill(t, source, "not a known skill entry")
	request := SkillDeployRequest{Name: "sample", Source: source, Tool: "codex", Mode: "copy"}
	if err := (Cleaner{}).DeploySkill(context.Background(), request); err == nil {
		t.Fatal("accepted a source outside the skills roots")
	}
	request.Name = "../sample"
	if _, err := (Cleaner{}).ListSkillDistribution(context.Background(), request.Name); err == nil {
		t.Fatal("accepted a path as a skill name")
	}
	if _, err := os.Lstat(filepath.Join(home, ".codex")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unexpected destination writes: %v", err)
	}
}
