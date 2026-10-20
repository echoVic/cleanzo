package main

import (
	"strings"
	"testing"
)

// TestWindowsBusyPatterns uses representative native and Node command lines on every host.
func TestWindowsBusyPatterns(t *testing.T) {
	for _, tc := range []struct {
		command  string
		patterns []string
		want     bool
	}{
		{`"C:\Program Files\Go\bin\GO.EXE" clean`, windowsGoModCleanup.Busy, true},
		{`C:\tools\gopls.exe`, windowsGoModCleanup.Busy, true},
		{`"C:\Program Files\nodejs\node.exe" C:\pnpm\pnpm.cjs store prune`, windowsPnpmPrune.Busy, true},
		{`C:\pnpm\PNPM.EXE install`, windowsPnpmPrune.Busy, true},
		{`C:\pnpm\pnpm.exe pnpm install`, windowsPnpmPrune.Busy, true},
		{`C:\Program Files\nodejs\node.exe pnpm install C:\node\corepack\dist\pnpm.js`, windowsPnpmPrune.Busy, true},
		{`node C:\npm\pnpm.js install`, windowsPnpmPrune.Busy, true},
		{`C:\tools\unrelated.exe`, windowsGoModCleanup.Busy, false},
	} {
		if got := commandBusy(tc.command, tc.patterns, true); got != tc.want {
			t.Errorf("busy %q=%t", tc.command, got)
		}
	}
}

// TestWindowsClaudeDesktopIsNotCLI distinguishes the known desktop install from Claude Code.
func TestWindowsClaudeDesktopIsNotCLI(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\a\AppData\Local`)
	var claude aiTool
	for _, tool := range aiTools {
		if tool.ID == "claude" {
			claude = tool
		}
	}
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{`C:\Users\a\AppData\Local\AnthropicClaude\app-1.2.3\claude.exe "C:\Users\a\AppData\Local\AnthropicClaude\app-1.2.3\claude.exe"`, false},
		{`C:\Users\a\.local\bin\claude.exe claude`, true},
		{`node.exe C:\npm\@anthropic-ai\claude-code\cli.js`, true},
		{`C:\unknown\claude.exe`, true},
		{`C:\tools\AnthropicClaude\app-local\claude.exe`, true},
		{`C:\tools\AnthropicClaude\app-1.2.3\claude.exe`, true},
		{`C:\Users\a\AppData\Local\AnthropicClaude\app-local\claude.exe`, true},
		{`C:\tools\claude.exe --desktop-path=C:\AnthropicClaude\app-1.0\claude.exe`, true},
	} {
		if got := aiCommandRunning(tc.command, claude, true); got != tc.want {
			t.Errorf("%q running=%t want %t", tc.command, got, tc.want)
		}
	}
}

// TestWindowsAICommandLines checks Windows command detection without relying on executable availability.
func TestWindowsAICommandLines(t *testing.T) {
	for _, tc := range []struct{ line, id string }{
		{`"C:\Program Files\nodejs\node.exe" C:\npm\node_modules\@anthropic-ai\claude-code\cli.js`, "claude"},
		{`C:\Users\a\bin\CODEX.EXE app-server`, "codex"},
		{`"C:\Users\a\bin\claude.exe" --help`, "claude"},
	} {
		for _, tool := range aiTools {
			if tool.ID == tc.id && !aiCommandRunning(tc.line, tool, true) {
				t.Errorf("missed %s: %s", tc.id, tc.line)
			}
		}
	}
	for _, tool := range aiTools {
		if aiCommandRunning(strings.Repeat("unrelated ", 2), tool, true) {
			t.Fatal("unrelated command matched")
		}
	}
}
