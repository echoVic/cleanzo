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

// nodeModuleNotFound is what Node prints when corepack cannot load a pnpm
// it fetched: the message is in the middle, frames and the version follow.
const nodeModuleNotFound = `node:internal/modules/cjs/loader:1433
  throw err;
  ^

Error: Cannot find module '/Users/me/.cache/node/corepack/v1/pnpm/12.0.0/bin/pnpm.cjs'
    at Function._resolveFilename (node:internal/modules/cjs/loader:1430:15)
    at Function._load (node:internal/modules/cjs/loader:1216:25)
    at Function.executeUserEntryPoint [as runMain] (node:internal/modules/run_main:171:5)
    at node:internal/main/run_main_module:36:49 {
  code: 'MODULE_NOT_FOUND',
  requireStack: []
}

Node.js v22.23.1
`

// nodeTypeError is what Node prints for an uncaught TypeError: the source
// line it quotes ends in "{", which opens nothing that a later line closes.
const nodeTypeError = `/Users/me/.cache/node/corepack/v1/pnpm/12.0.0/dist/pnpm.cjs:12
    x(function () {
    ^

TypeError: x is not a function
    at Object.<anonymous> (/Users/me/.cache/node/corepack/v1/pnpm/12.0.0/dist/pnpm.cjs:12:5)
    at Module._compile (node:internal/modules/cjs/loader:1554:14)

Node.js v22.23.1
`

// TestToolOutputLinePicksWhatWentWrong reads failed tool output as tools
// print it: the line naming the error wins wherever it is; otherwise the
// last line that is not a stack frame, a version trailer, a caret, a
// bracket or part of a printed object; otherwise nothing.
func TestToolOutputLinePicksWhatWentWrong(t *testing.T) {
	for _, c := range []struct{ name, out, want string }{
		{"node dump", nodeModuleNotFound, "Error: Cannot find module '/Users/me/.cache/node/corepack/v1/pnpm/12.0.0/bin/pnpm.cjs'"},
		{"quoted source line ending in a brace", nodeTypeError, "TypeError: x is not a function"},
		{"corepack usage error", `The package was not signed by any trusted keys: {
  "signatures": [
    {
      "sig": "MEQCIGx3",
      "keyid": "SHA256:DhQ8wR5APBvFHLF/+Tc+AYvPOdTpcIDqOhxsBHRwC7U"
    }
  ],
  "trustedKeys": [
    {
      "keyid": "SHA256:jl3bwswu80PjjokCgh0o2w5c2U4LhQAE57gj9cz1kzA",
      "key": "MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE1Olb3zMAFFxXKHiIkQO5cJ3Yhl5i6UPp"
    }
  ]
}
`, "The package was not signed by any trusted keys"},
		{"older corepack", `Internal Error: Cannot find matching keyid: {"signatures":[{"keyid":"SHA256:DhQ8wR5APBvFHLF"}],"keys":[]}
    at Engine.verifySignature (/Users/me/.nvm/versions/node/v20.11.0/lib/node_modules/corepack/dist/lib/corepack.cjs:22010:11)
    at async Engine.ensurePackageManager (/Users/me/.nvm/versions/node/v20.11.0/lib/node_modules/corepack/dist/lib/corepack.cjs:22301:5)
`, `Internal Error: Cannot find matching keyid: {"signatures":[{"keyid":"SHA256:DhQ8wR5APBvFHLF"}],"keys":[]}`},
		{"pnpm error code", " ERR_PNPM_BAD_PM_VERSION  This project is configured to use v12 of pnpm. Your current pnpm is v10.13.1\n\nIf you want to bypass this version check, set COREPACK_ENABLE_STRICT to 0\n", "ERR_PNPM_BAD_PM_VERSION  This project is configured to use v12 of pnpm. Your current pnpm is v10.13.1"},
		{"error word in a frame", "Something went wrong\n    at new ApiError (/x/errors.js:1:1)\n", "Something went wrong"},
		{"single line", "go: cannot find GOROOT directory: /usr/local/go\n", "go: cannot find GOROOT directory: /usr/local/go"},
		{"no error word", "first\n  last failure  \n\n", "last failure"},
		{"crlf", "warning\r\nfailed here\r\n", "failed here"},
		{"only noise", "    at main (/x/y.js:1:1)\n  ^\n}\n\nNode.js v22.23.1\n", ""},
		{"killed by the timeout", "go: downloading example.com/mod v1.0.0\n/Users/me/bin/go: line 2:  6527 Killed: 9               sleep 5\n", "go: downloading example.com/mod v1.0.0"},
		{"empty", "", ""},
	} {
		if got := toolOutputLine(c.out); got != c.want {
			t.Errorf("%s: toolOutputLine = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestToolEstimateFailureCarriesTheLine: an estimate whose command fails
// carries the line that says what went wrong, cut to 300 characters, and
// never Go's own error text; it still reads as the command's error. A
// refused command carries nothing.
func TestToolEstimateFailureCarriesTheLine(t *testing.T) {
	exit := errors.New("exit status 1")
	for _, c := range []struct {
		out  string
		err  error
		want string
	}{
		{nodeModuleNotFound, exit, "Error: Cannot find module '/Users/me/.cache/node/corepack/v1/pnpm/12.0.0/bin/pnpm.cjs'"},
		{"", exit, ""},
		{" \n\t\n", errors.New("signal: killed"), ""},
		{"", context.DeadlineExceeded, ""},
		{"x\n" + strings.Repeat("é", 301), exit, strings.Repeat("é", 300)},
	} {
		err := estimateError(c.out, c.err)
		var failure *toolFailure
		if !errors.As(err, &failure) || failure.line != c.want || !errors.Is(err, c.err) || err.Error() != c.err.Error() {
			t.Errorf("estimateError(%q, %v) = %v (%+v), want line %q", c.out, c.err, err, failure, c.want)
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
// on every platform: no size, no folders, the problem and the tool's line;
// a command that ran out of its own time is marked as timed out. A refused
// estimate, or one the scan's cancellation ends, marks nothing.
func TestFailedToolEstimateMarksRow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failed := func(junkEnv, string) (int64, bool, []string, error) {
		return 0, true, nil, estimateError("ERR_PNPM_BAD_PM_VERSION This project is configured to use v12 of pnpm\n", errors.New("exit status 1"))
	}
	refused := func(junkEnv, string) (int64, bool, []string, error) { return 0, true, nil, errBlocked }
	slow := func(junkEnv, string) (int64, bool, []string, error) {
		return 0, true, nil, estimateError("", context.DeadlineExceeded)
	}
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
		{"timeout", slow, "tool-timeout", ""},
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
