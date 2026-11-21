package main

import (
	"context"
	"time"
)

// Worktree states. Only wtClean and wtDirty can be removed.
const (
	wtClean      = "clean"
	wtDirty      = "dirty"
	wtInUse      = "in-use"
	wtSubmodule  = "submodules"
	wtMidOp      = "mid-operation"
	wtUnbranched = "unbranched-commits"
	wtNoGit      = "git-unavailable"
	wtIndexOnly  = "index-only"
	wtUnknown    = "unrecognized"
)

// Outcomes of removing one worktree, besides success.
const (
	wtFailChanged = "wt-changed" // it is not what the user saw any more; nothing was changed
	wtFailGit     = "wt-git"     // git's record could not be cleared; nothing was changed
	wtFailTrash   = "wt-trash"   // git's record is cleared, the folder is still in place
)

// worktreeTemp is appended to a worktree's folder name while git clears its record.
const worktreeTemp = ".cleanzo-removing"

// agentWorktree is one worktree an agent made.
type agentWorktree struct {
	Path, Name    string
	Repo          string // the repository's folder
	Admin, Common string // git's record of it, and the repository's git folder
	Branch        string
	State         string
	Changes       int
	Size, Last    int64
}

// worktreeUsage is what tells a worktree is in use. OK is false when it could
// not be read, and every worktree then counts as in use.
type worktreeUsage struct {
	Cwds     []string // working folders of this user's processes
	Sessions []string // working folders of the open Claude Code sessions
	OK       bool
}

var worktreeUse = claudeWorkingFolders // tests replace it

// worktreePidStart is when a process started; ok is false when it is not running. Tests replace it.
var worktreePidStart = func(pid int32) (time.Time, bool) {
	start, ok := realClaudeSource().started(pid)
	return time.UnixMicro(start), ok
}

// findWorktrees lists the worktrees the agents of kind ("claude" or "codex",
// as in aiTool.Worktrees) made, each with its state.
func findWorktrees(ctx context.Context, kind string) []agentWorktree {
	return nil
}

// removeWorktree clears git's record of w and moves its folder to the Trash,
// when its state is still seen. code is "" when it is gone, otherwise
// "in-use" or one of the wtFail outcomes; freed is measured before it moves.
func removeWorktree(ctx context.Context, w agentWorktree, seen string, guard *usageGuard) (freed int64, code string) {
	return 0, wtFailChanged
}
