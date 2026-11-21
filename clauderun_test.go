package main

import (
	"context"
	"testing"
)

func TestClaudeRunningUnsupportedOffMacOS(t *testing.T) {
	if claudeRunSupported() {
		t.Skip("non-macOS binding contract")
	}
	ctx := context.Background()
	target := ClaudeTarget{Pid: 4242, Start: 1700000000000000}
	_, runningErr := (Cleaner{}).ClaudeRunning(ctx)
	_, procsErr := (Cleaner{}).EndClaudeProcesses(ctx, "S1", []ClaudeTarget{target})
	_, sessionErr := (Cleaner{}).EndClaudeSession(ctx, target, "S1", "idle")
	for _, c := range []struct {
		method string
		err    error
	}{
		{"ClaudeRunning", runningErr},
		{"EndClaudeProcesses", procsErr},
		{"EndClaudeSession", sessionErr},
	} {
		ce, isCode := c.err.(codeError)
		if !isCode || ce.Code != "unsupported" {
			t.Errorf("%s: got %v, want unsupported", c.method, c.err)
		}
	}
	summary := (Cleaner{}).ClaudeRunSummary()
	if summary.Idle != 0 || summary.Left != 0 || summary.Rows == nil || len(summary.Rows) != 0 {
		t.Errorf("summary = %+v (nil rows: %v), want zero counts and an empty, non-nil Rows", summary, summary.Rows == nil)
	}
}
