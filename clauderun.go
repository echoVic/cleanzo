package main

// clauderun.go holds the Claude Code page's Running tab: the readings the
// page sees, the thresholds, the reads they come from and the bound methods.
// The attribution rules live in clauderun_rules.go and ending processes in
// clauderun_end.go. Every read and signal goes through claudeSource, so the
// rules are tested against made-up processes.

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	s, err := src.snapshot()
	if err != nil {
		return ClaudeRunning{Sessions: []ClaudeSession{}, Left: []ClaudeLeft{}}, codeError{Code: "unreadable"}
	}
	a := attributeClaude(s)
	now := src.now()
	commands := make([][]claudeRow, len(a.Sessions)) // what each open session lists
	var listed []int32
	for i, open := range a.Sessions {
		for _, r := range open.Commands {
			if now.Sub(time.UnixMicro(r.Lead.Start)) >= claudeCommandAge {
				commands[i] = append(commands[i], r)
				listed = append(listed, claudeRowPids(r)...)
			}
		}
	}
	for _, l := range a.Left {
		for _, r := range l.Rows {
			listed = append(listed, claudeRowPids(r)...)
		}
	}
	v := claudeView{src: src, ports: claudeListedPorts(ctx, src.ports, listed, now), apps: s.Apps, paths: map[int32]string{}, now: now}
	for _, p := range s.Procs {
		if p.Path != "" {
			v.paths[p.Pid] = p.Path
		}
	}
	out := ClaudeRunning{
		Sessions:  make([]ClaudeSession, 0, len(a.Sessions)),
		Left:      make([]ClaudeLeft, 0, len(a.Left)),
		Uncertain: a.Uncertain,
		NoRecords: a.NoRecords,
		At:        now.Unix(),
	}
	for i, open := range a.Sessions {
		out.Sessions = append(out.Sessions, v.session(open, commands[i]))
	}
	slices.SortFunc(out.Sessions, claudeSessionOrder)
	for _, l := range a.Left {
		left := ClaudeLeft{ID: l.ID, Procs: make([]ClaudeProc, 0, len(l.Rows))}
		left.Project, left.Title = src.closed(l.ID)
		for _, r := range l.Rows {
			left.Procs = append(left.Procs, v.row(r))
		}
		out.Left = append(out.Left, left)
	}
	slices.SortFunc(out.Left, func(x, y ClaudeLeft) int {
		return cmp.Or(cmp.Compare(claudeOldest(x), claudeOldest(y)), strings.Compare(x.ID, y.ID))
	})
	return out, nil
}

// claudeView turns what the attribution found into what the page shows.
type claudeView struct {
	src   claudeSource
	ports map[int32][]int // nil when they cannot be read
	apps  []workspaceApp
	paths map[int32]string // the executables of the snapshot's processes
	now   time.Time
}

// session is how the page lists an open session, with commands as the rows
// under it.
func (v claudeView) session(open claudeOpen, commands []claudeRow) ClaudeSession {
	cwd := open.Record.Cwd
	if cwd == "" {
		cwd = v.src.cwd(open.Proc.Pid)
	}
	s := ClaudeSession{
		ID:       open.ID,
		Pid:      open.Proc.Pid,
		Start:    open.Proc.Start,
		Project:  claudeFolderName(cwd),
		Cwd:      cwd,
		Title:    open.Record.Name,
		Host:     v.host(open.Proc.Pid),
		Entry:    open.Record.Entrypoint,
		Memory:   v.src.footprint(open.Proc.Pid),
		Own:      open.Own,
		Commands: make([]ClaudeProc, 0, len(commands)),
	}
	s.Status, s.Since = claudeStatus(open.Record, v.now)
	for _, m := range open.Members {
		s.Memory += v.src.footprint(m.Pid)
	}
	for _, r := range commands {
		s.Commands = append(s.Commands, v.row(r))
	}
	return s
}

// row is r as the page lists it: its lead's identity, command and folder,
// and the memory and ports of every process in it.
func (v claudeView) row(r claudeRow) ClaudeProc {
	p := ClaudeProc{
		Pid:     r.Lead.Pid,
		Start:   r.Lead.Start,
		Command: maskCommand(r.Lead.Args),
		Cwd:     v.src.cwd(r.Lead.Pid),
		Ports:   append([]int{}, v.ports[r.Lead.Pid]...),
		Memory:  v.src.footprint(r.Lead.Pid),
		Folded:  make([]ClaudeTarget, 0, len(r.Folded)),
	}
	p.Worktree = claudeWorktree(p.Cwd)
	for _, f := range r.Folded {
		p.Ports = append(p.Ports, v.ports[f.Pid]...)
		p.Memory += v.src.footprint(f.Pid)
		p.Folded = append(p.Folded, ClaudeTarget{Pid: f.Pid, Start: f.Start})
	}
	slices.Sort(p.Ports)
	p.Ports = slices.Compact(p.Ports)
	return p
}

// host is the name of the app the Claude Code process pid runs in: the app
// its responsible process is, else the app whose bundle holds that
// process's executable, the outermost when bundles nest. It is "" when
// there is none.
func (v claudeView) host(pid int32) string {
	owner := v.src.responsible(pid)
	if owner <= 0 {
		return ""
	}
	for _, app := range v.apps {
		if app.Pid == owner {
			return app.Name
		}
	}
	path := strings.ToLower(v.paths[owner])
	host, bundle := "", ""
	for _, app := range v.apps {
		b := strings.ToLower(app.Path)
		if path != "" && b != "" && within(path, b) && (bundle == "" || len(b) < len(bundle)) {
			host, bundle = app.Name, b
		}
	}
	return host
}

// claudeStatus is the status a session record gives, idle or busy, with
// when it began in unix seconds, no later than now. A status counts only
// with the time it was set: otherwise both are empty.
func claudeStatus(r claudeRecord, now time.Time) (status string, since int64) {
	if r.Status != "idle" && r.Status != "busy" || r.StatusUpdatedAt <= 0 {
		return "", 0
	}
	return r.Status, min(r.StatusUpdatedAt/1000, now.Unix())
}

// claudeSessionOrder orders the open sessions for the page: idle ones
// first, longest idle first, then those whose status is unknown, then busy
// ones; within each by pid.
func claudeSessionOrder(a, b ClaudeSession) int {
	rank := func(s ClaudeSession) int {
		switch s.Status {
		case "idle":
			return 0
		case "busy":
			return 2
		}
		return 1
	}
	idleSince := func(s ClaudeSession) int64 {
		if s.Status == "idle" {
			return s.Since
		}
		return 0
	}
	return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(idleSince(a), idleSince(b)), cmp.Compare(a.Pid, b.Pid))
}

// claudeOldest is the start of the oldest process a closed session left.
func claudeOldest(l ClaudeLeft) int64 {
	var oldest int64
	for i, p := range l.Procs {
		if i == 0 || p.Start < oldest {
			oldest = p.Start
		}
		for _, f := range p.Folded {
			oldest = min(oldest, f.Start)
		}
	}
	return oldest
}

// claudeRowPids is the pid of every process in r, its lead first.
func claudeRowPids(r claudeRow) []int32 {
	pids := []int32{r.Lead.Pid}
	for _, f := range r.Folded {
		pids = append(pids, f.Pid)
	}
	return pids
}

// claudeFolderName is the name of the folder at path, or "" for none.
func claudeFolderName(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

// claudeWorktreeDepth is how many folders above a working folder are
// looked at for the linked worktree it is in.
const claudeWorktreeDepth = 8

// claudeWorktree is the name of the linked git worktree folder cwd is in:
// cwd itself or one of the claudeWorktreeDepth folders above it, the
// nearest whose .git is a file pointing into a worktrees folder. It is ""
// when there is none.
func claudeWorktree(cwd string) string {
	if cwd == "" {
		return ""
	}
	dir := filepath.Clean(cwd)
	for range claudeWorktreeDepth + 1 {
		if info, err := os.Lstat(filepath.Join(dir, ".git")); err == nil && info.Mode().IsRegular() {
			if gitDir, found := gitDirOf(dir); found && gitDir != "" && filepath.Base(filepath.Dir(filepath.Clean(gitDir))) == "worktrees" {
				return filepath.Base(dir)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// claudePorts is the last read of listening ports: the pids it was for,
// sorted, and when it was taken.
var claudePorts struct {
	sync.Mutex
	pids  []int32
	at    time.Time
	ports map[int32][]int
}

// claudeListedPorts returns the ports pids listen on. It reads them with
// read only when the set of pids differs from the last read's, or when
// claudePortsEvery has passed since it; otherwise the last read stands.
func claudeListedPorts(ctx context.Context, read func(context.Context, []int32) map[int32][]int, pids []int32, now time.Time) map[int32][]int {
	pids = slices.Compact(slices.Sorted(slices.Values(pids)))
	if len(pids) == 0 {
		return nil
	}
	claudePorts.Lock()
	defer claudePorts.Unlock()
	if slices.Equal(pids, claudePorts.pids) && !now.Before(claudePorts.at) && now.Sub(claudePorts.at) < claudePortsEvery {
		return claudePorts.ports
	}
	ports := read(ctx, pids)
	if ctx.Err() == nil { // a read cut short is not remembered
		claudePorts.pids, claudePorts.at, claudePorts.ports = pids, now, ports
	}
	return ports
}

// claudeSummaryWith reads the Running tab's summary from src, without ports
// or titles. A row's tags count the Claude Code processes of the long-idle
// sessions and each process closed sessions left, placed in the process
// page's rows as the process page places them.
func claudeSummaryWith(src claudeSource) ClaudeRunSummary {
	sum := ClaudeRunSummary{Rows: []ClaudeRowTag{}}
	s, err := src.snapshot()
	if err != nil {
		return sum
	}
	a := attributeClaude(s)
	now := src.now()
	var tagged []int32
	idle := map[int32]bool{} // the tagged Claude Code processes of long-idle sessions
	for _, open := range a.Sessions {
		if status, since := claudeStatus(open.Record, now); status == "idle" && now.Sub(time.Unix(since, 0)) >= claudeIdleNote {
			sum.Idle++
			tagged = append(tagged, open.Proc.Pid)
			idle[open.Proc.Pid] = true
		}
	}
	for _, l := range a.Left {
		sum.Left += len(l.Rows)
		for _, r := range l.Rows {
			tagged = append(tagged, claudeRowPids(r)...)
		}
	}
	// The process page places a process by its executable, and a system one
	// by its responsible process's too.
	cur := make(map[int32]procReading, 2*len(tagged))
	read := func(pid int32) {
		if _, done := cur[pid]; !done {
			path, name := src.identity(pid)
			cur[pid] = procReading{Pid: pid, Path: path, Name: name}
		}
	}
	for _, pid := range tagged {
		read(pid)
		if owner := src.responsible(pid); owner > 0 && owner != pid {
			read(owner)
		}
	}
	tags := map[string]*ClaudeRowTag{}
	for _, pid := range tagged {
		key, _, _ := ownProcessKey(cur[pid], cur, src.responsible, src.system)
		tag := tags[key]
		if tag == nil {
			tag = &ClaudeRowTag{Key: key}
			tags[key] = tag
		}
		if idle[pid] {
			tag.Idle++
		} else {
			tag.Left++
		}
	}
	for _, tag := range tags {
		sum.Rows = append(sum.Rows, *tag)
	}
	slices.SortFunc(sum.Rows, func(x, y ClaudeRowTag) int { return strings.Compare(x.Key, y.Key) })
	return sum
}
