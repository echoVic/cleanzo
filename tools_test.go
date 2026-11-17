package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestToolEstimateFailureCarriesLastLine: an estimate whose command fails
// carries the tool's last non-empty output line, cut to 300 characters, or
// the error text when the tool printed nothing, and still reads as the
// command's error. A refused command carries nothing.
func TestToolEstimateFailureCarriesLastLine(t *testing.T) {
	exit := errors.New("exit status 1")
	for _, c := range []struct{ out, want string }{
		{"first\nlast line\n\n", "last line"},
		{"warning\r\nfailed here\r\n", "failed here"},
		{"", "exit status 1"},
		{" \n\t\n", "exit status 1"},
		{"x\n" + strings.Repeat("é", 301), strings.Repeat("é", 300)},
	} {
		err := estimateError(c.out, exit)
		var failure *toolFailure
		if !errors.As(err, &failure) || failure.line != c.want || !errors.Is(err, exit) || err.Error() != exit.Error() {
			t.Errorf("estimateError(%q) = %v (%+v), want line %q", c.out, err, failure, c.want)
		}
	}
	if err := estimateError("refused", errBlocked); err != errBlocked {
		t.Errorf("refused command = %v", err)
	}
	if err := estimateError("fine", nil); err != nil {
		t.Errorf("successful command = %v", err)
	}
}

// TestFailedToolEstimateMarksRow keeps a command row whose tool cannot run,
// on every platform: no size, no folders, the problem and the tool's line.
// A refused estimate, or one the scan's cancellation ends, marks nothing.
func TestFailedToolEstimateMarksRow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failed := func(junkEnv, string) (int64, bool, []string, error) {
		return 0, true, nil, estimateError("ERR_PNPM_BAD_PM_VERSION This project is configured to use v12 of pnpm\n", errors.New("exit status 1"))
	}
	refused := func(junkEnv, string) (int64, bool, []string, error) { return 0, true, nil, errBlocked }
	stopped := func(junkEnv, string) (int64, bool, []string, error) {
		cancel()
		return 0, true, nil, estimateError("Progress: resolved 1\n", errors.New("signal: killed"))
	}
	env := junkEnv{home: t.TempDir(), guard: &usageGuard{running: map[string]bool{}}, findTool: func(string) string { return "pnpm" }}
	for _, c := range []struct {
		name            string
		estimate        func(junkEnv, string) (int64, bool, []string, error)
		problem, detail string
	}{
		{"failed", failed, "tool-failed", "ERR_PNPM_BAD_PM_VERSION This project is configured to use v12 of pnpm"},
		{"refused", refused, "", ""},
		{"cancelled", stopped, "", ""},
	} {
		rule := &Rule{ID: "dev.pnpm-store", Group: groupDev, Default: true, Command: &toolCommand{Tool: "pnpm", Timeout: time.Minute, Estimate: c.estimate}}
		plan := planItem(ctx, itemSpec{rule: rule, item: JunkItem{ID: rule.ID, Group: string(rule.Group), Size: -1, Default: true}}, env, make(chan struct{}, 1), nil)
		if plan.item.Size != 0 || len(plan.item.Paths) != 0 || plan.item.Problem != c.problem || plan.item.ProblemDetail != c.detail {
			t.Errorf("%s estimate = %+v, want problem %q, detail %q", c.name, plan.item, c.problem, c.detail)
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
