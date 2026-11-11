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

// TestBusyPatternsMatchCommandName: ps shows a command typed in a shell by its
// bare name, so `go build` blocks the module cache clean; a path that merely
// passes through a pnpm folder is not pnpm running.
func TestBusyPatternsMatchCommandName(t *testing.T) {
	for _, tc := range []struct {
		command  string
		patterns []string
		want     bool
	}{
		{"go build ./...", goModCleanup.Busy, true},
		{"go", goModCleanup.Busy, true},
		{"/opt/homebrew/bin/go test ./...", goModCleanup.Busy, true},
		{"/usr/local/go/bin/go", goModCleanup.Busy, true},
		{"/usr/libexec/gopls serve", goModCleanup.Busy, true},
		{"/usr/local/go/bin/gofmt -l .", goModCleanup.Busy, false},
		{"/Users/me/go/bin/staticcheck ./...", goModCleanup.Busy, false},
		{"gopher-server --port 8080", goModCleanup.Busy, false},
		{"pnpm install", pnpmPrune.Busy, true},
		{"/Users/me/Library/pnpm/pnpm store prune", pnpmPrune.Busy, true},
		{"node /opt/homebrew/lib/node_modules/pnpm/bin/pnpm.cjs install", pnpmPrune.Busy, true},
		{"node /opt/homebrew/lib/node_modules/corepack/dist/pnpm.js install", pnpmPrune.Busy, true},
		{"/Users/me/Library/pnpm/nodejs/22.1.0/bin/node server.js", pnpmPrune.Busy, false},
		{"vim /Users/me/notes/pnpm-store.md", pnpmPrune.Busy, false},
		{"/bin/bash /opt/homebrew/Library/Homebrew/brew.sh upgrade", brewCleanup.Busy, true},
		{"/Applications/Brewer.app/Contents/MacOS/Brewer", brewCleanup.Busy, false},
	} {
		out := "/usr/sbin/cfprefsd agent\n" + tc.command + "\n/usr/libexec/trustd"
		if got := commandBusy(out, tc.patterns, false); got != tc.want {
			t.Errorf("busy %q = %t, want %t", tc.command, got, tc.want)
		}
	}
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
