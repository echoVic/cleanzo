package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTool writes a tool script in a temporary executable directory.
func fakeTool(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// stageTools isolates tool lookup and process listings for a test.
func stageTools(t *testing.T, env *junkEnv, bin string) {
	t.Helper()
	saved, savedPs := toolDirs, psCommands
	toolDirs = func() []string { return []string{bin} }
	psCommands = func() ([]byte, error) { return nil, nil }
	t.Cleanup(func() { toolDirs, psCommands = saved, savedPs })
	env.findTool = findTool
}

// shellPath quotes a test path for the fake tools' shell scripts.
func shellPath(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

// TestParseBrewFreed checks binary units and both estimate and cleanup text.
func TestParseBrewFreed(t *testing.T) {
	for _, c := range []struct {
		out  string
		want int64
	}{
		{"would free approximately 1.5GB", 1610612736},
		{"has freed approximately 2 MB of disk space.", 2097152},
		{"would free approximately 1 TB", 1099511627776},
		{"has freed approximately 42B", 42},
		{"unrelated 25 GB", 0},
	} {
		if got := parseBrewFreed(c.out); got != c.want {
			t.Errorf("parseBrewFreed(%q) = %d, want %d", c.out, got, c.want)
		}
	}
}
