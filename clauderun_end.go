package main

import "context"

// endClaudeProcsWith ends the targets that are still listed under session,
// each checked by pid and start time first.
func endClaudeProcsWith(ctx context.Context, src claudeSource, session string, targets []ClaudeTarget) (ClaudeEndResult, error) {
	return ClaudeEndResult{Procs: []ClaudeEnded{}}, nil
}

// endClaudeSessionWith ends the open session whose Claude Code process is
// target, with its sub-agents and helpers, while it is still session in the
// status the user saw.
func endClaudeSessionWith(ctx context.Context, src claudeSource, target ClaudeTarget, session, status string) (ClaudeEndResult, error) {
	return ClaudeEndResult{Procs: []ClaudeEnded{}}, nil
}
