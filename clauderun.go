package main

// clauderun.go holds the Claude Code page's Running tab: the readings the
// page sees, the thresholds, the reads they come from and the bound methods.
// The attribution rules live in clauderun_rules.go and ending processes in
// clauderun_end.go. Every read and signal goes through claudeSource, so the
// rules are tested against made-up processes.

import (
	"context"
	"syscall"
	"time"
)

// Thresholds of the Claude Code page's Running tab.
const (
	claudeIdleNote   = 2 * time.Hour    // an idle session counts in the badge and the process page tag from here
	claudeCommandAge = 60 * time.Second // an open session lists what it started once that ran this long
	claudeTermWait   = 2 * time.Second  // how long a process gets after SIGTERM
	claudeKillWait   = time.Second      // how long it gets after SIGKILL
	claudePoll       = 100 * time.Millisecond
	claudePortsEvery = 30 * time.Second // ports are read again after this long, or when the listed pids change
)

// ClaudeRunning is one reading of the Claude Code page's Running tab.
type ClaudeRunning struct {
	Sessions  []ClaudeSession `json:"sessions"`  // open sessions: idle longest first, working ones last
	Left      []ClaudeLeft    `json:"left"`      // closed sessions with processes still running, oldest first
	Uncertain int             `json:"uncertain"` // marked processes left out: their session may still be open
	NoRecords bool            `json:"noRecords"` // a Claude Code process shows no session id, so nothing is called left behind
	At        int64           `json:"at"`        // unix seconds
}

// ClaudeSession is one open Claude Code session.
type ClaudeSession struct {
	ID       string       `json:"id"`      // "" when its record cannot be read
	Pid      int32        `json:"pid"`     // its Claude Code process
	Start    int64        `json:"start"`   // that process's start, unix microseconds; with Pid, its identity
	Project  string       `json:"project"` // folder name of Cwd
	Cwd      string       `json:"cwd"`
	Title    string       `json:"title"`
	Status   string       `json:"status"`   // "idle", "busy" or ""
	Since    int64        `json:"since"`    // when Status last changed, unix seconds; 0 when unknown
	Host     string       `json:"host"`     // the app it runs in; "" when unknown
	Entry    string       `json:"entry"`    // the record's entrypoint, "cli" for a terminal; "" when unknown
	Memory   uint64       `json:"memory"`   // its process, sub-agents and helpers
	Own      bool         `json:"own"`      // Cleanzo runs under it, so it cannot be ended here
	Commands []ClaudeProc `json:"commands"` // what it started that has run for a minute or longer, oldest first
}

// ClaudeLeft is a closed session and the processes it left running.
type ClaudeLeft struct {
	ID      string       `json:"id"`
	Project string       `json:"project"` // "" when unknown
	Title   string       `json:"title"`   // "" when unknown
	Procs   []ClaudeProc `json:"procs"`   // oldest first
}

// ClaudeProc is one listed process, with what is folded into its row.
type ClaudeProc struct {
	Pid      int32          `json:"pid"`
	Start    int64          `json:"start"`   // unix microseconds; with Pid, its identity
	Command  string         `json:"command"` // for display: assignments and secret values removed
	Cwd      string         `json:"cwd"`
	Worktree string         `json:"worktree"` // the linked worktree Cwd is in; "" for none
	Ports    []int          `json:"ports"`    // listening TCP ports, ascending
	Memory   uint64         `json:"memory"`   // footprint, folded processes included
	Folded   []ClaudeTarget `json:"folded"`   // the processes folded into this row
}

// ClaudeTarget names a process to end by pid and start time.
type ClaudeTarget struct {
	Pid   int32 `json:"pid"`
	Start int64 `json:"start"` // unix microseconds
}

// ClaudeEnded is what ending did to one process.
type ClaudeEnded struct {
	Pid    int32  `json:"pid"`
	Result string `json:"result"` // "ended", "forced", "running" or "changed"
}

// ClaudeEndResult is what EndClaudeProcesses and EndClaudeSession return.
type ClaudeEndResult struct {
	Procs []ClaudeEnded `json:"procs"`
	Freed uint64        `json:"freed"` // footprint, read before ending, of the processes that are gone
}

// ClaudeRunSummary is what the tab's badge and the process page's tags show.
type ClaudeRunSummary struct {
	Idle int            `json:"idle"` // open sessions idle for claudeIdleNote or longer
	Left int            `json:"left"` // rows of processes closed sessions left behind
	Rows []ClaudeRowTag `json:"rows"` // per process page row, ordered by key
}

// ClaudeRowTag is the tag of one process page row.
type ClaudeRowTag struct {
	Key  string `json:"key"`  // ProcessRow.Key
	Idle int    `json:"idle"` // long-idle sessions whose Claude Code process is in the row
	Left int    `json:"left"` // processes in the row that closed sessions left behind
}

// claudeSource is everything the Running tab reads and does, injectable for tests.
type claudeSource struct {
	snapshot    func() (claudeSnapshot, error)
	started     func(pid int32) (start int64, ok bool) // one process's start, unix microseconds; ok false when it is gone or a zombie
	footprint   func(pid int32) uint64
	cwd         func(pid int32) string
	identity    func(pid int32) (path, name string) // proc_pidpath and proc_name, as the process page reads them
	responsible func(pid int32) int32
	system      func(path string) bool
	ports       func(ctx context.Context, pids []int32) map[int32][]int // nil when they cannot be read
	closed      func(session string) (project, title string)            // a closed session, from its transcript
	signal      func(pid int32, sig syscall.Signal) error
	now         func() time.Time
	sleep       func(ctx context.Context, d time.Duration) error
}

// claudeSourceFn gives the bound methods their reads. Tests replace it.
var claudeSourceFn = realClaudeSource

// ClaudeRunning reads the Claude Code page's Running tab: the open sessions
// with what they started, and the processes closed sessions left running.
// Off macOS it fails with "unsupported".
func (Cleaner) ClaudeRunning(ctx context.Context) (ClaudeRunning, error) {
	if !claudeRunSupported() {
		return ClaudeRunning{}, codeError{Code: "unsupported"}
	}
	return claudeRunningWith(ctx, claudeSourceFn())
}

// ClaudeRunSummary counts the long-idle sessions and what closed sessions
// left running, for the Running tab's badge and the process page's tags.
// Off macOS, and when Claude Code is not installed, it is empty and reads
// nothing.
func (Cleaner) ClaudeRunSummary() ClaudeRunSummary {
	if claudeRunSupported() {
		for _, tool := range aiTools {
			if tool.ID == "claude" && aiToolInstalled(tool) {
				return claudeSummaryWith(claudeSourceFn())
			}
		}
	}
	return ClaudeRunSummary{Rows: []ClaudeRowTag{}}
}

// EndClaudeProcesses ends processes listed under session, each named by pid
// and start time; a target that is no longer listed there is left alone.
// Off macOS it fails with "unsupported".
func (Cleaner) EndClaudeProcesses(ctx context.Context, session string, targets []ClaudeTarget) (ClaudeEndResult, error) {
	if !claudeRunSupported() {
		return ClaudeEndResult{}, codeError{Code: "unsupported"}
	}
	return endClaudeProcsWith(ctx, claudeSourceFn(), session, targets)
}

// EndClaudeSession ends the open session whose Claude Code process is target,
// with its sub-agents and helpers, while it is still session in the status
// the user saw. What it started keeps running. Off macOS it fails with
// "unsupported".
func (Cleaner) EndClaudeSession(ctx context.Context, target ClaudeTarget, session, status string) (ClaudeEndResult, error) {
	if !claudeRunSupported() {
		return ClaudeEndResult{}, codeError{Code: "unsupported"}
	}
	return endClaudeSessionWith(ctx, claudeSourceFn(), target, session, status)
}

// claudeRunningWith reads the Running tab from src.
func claudeRunningWith(ctx context.Context, src claudeSource) (ClaudeRunning, error) {
	return ClaudeRunning{Sessions: []ClaudeSession{}, Left: []ClaudeLeft{}}, nil
}

// claudeSummaryWith reads the Running tab's summary from src, without ports
// or titles.
func claudeSummaryWith(src claudeSource) ClaudeRunSummary {
	return ClaudeRunSummary{Rows: []ClaudeRowTag{}}
}
