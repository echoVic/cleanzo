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
		{`C:\tools\unrelated.exe`, windowsGoModCleanup.Busy, false},
	} {
		if got := commandBusy(tc.command, tc.patterns, true); got != tc.want {
			t.Errorf("busy %q=%t", tc.command, got)
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
