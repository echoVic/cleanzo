package main

import (
	"context"
	"errors"
	"slices"
	"syscall"
	"time"
)

// What ending did to one process, as ClaudeEnded.Result says.
const (
	claudeResultEnded   = "ended"   // it is gone without SIGKILL, also when it was gone before SIGTERM
	claudeResultForced  = "forced"  // it is gone after SIGKILL
	claudeResultRunning = "running" // it is still there
	claudeResultChanged = "changed" // it is not what the page listed any more, so it got no signal
)

// endClaudeProcsWith ends the targets that are still listed under session,
// each checked by pid and start time first: in a row of what an open session
// with that id started, of any age, or of what the closed session left. A
// target that is not is "changed" and gets no signal.
func endClaudeProcsWith(ctx context.Context, src claudeSource, session string, targets []ClaudeTarget) (ClaudeEndResult, error) {
	s, err := src.snapshot()
	if err != nil {
		return ClaudeEndResult{Procs: []ClaudeEnded{}}, codeError{Code: "unreadable"}
	}
	a := attributeClaude(s)
	listed := map[int32]claudeProc{} // what the rows under session hold now
	add := func(rows []claudeRow) {
		for _, r := range rows {
			listed[r.Lead.Pid] = r.Lead
			for _, p := range r.Folded {
				listed[p.Pid] = p
			}
		}
	}
	for _, open := range a.Sessions {
		if open.ID == session {
			add(open.Commands)
		}
	}
	for _, left := range a.Left {
		if left.ID == session {
			add(left.Rows)
		}
	}
	ends := make([]claudeEnding, 0, len(targets))
	named := map[int32]bool{}
	for _, target := range targets {
		if named[target.Pid] {
			continue // a pid named again is handled as the first target naming it
		}
		named[target.Pid] = true
		if p, ok := listed[target.Pid]; ok && p.Start == target.Start {
			ends = append(ends, newClaudeEnding(p, s.UID))
		} else {
			ends = append(ends, claudeEnding{pid: target.Pid, result: claudeResultChanged})
		}
	}
	return claudeEnd(ctx, src, ends), nil
}

// endClaudeSessionWith ends the open session whose Claude Code process is
// target, with its sub-agents and helpers, while it is still session in the
// status the user saw: seen idle, it must still be idle. What it started is
// left alone. The session Cleanzo runs under is refused with "blocked"; a
// target that is not such a session any more is "changed" and gets no
// signal.
func endClaudeSessionWith(ctx context.Context, src claudeSource, target ClaudeTarget, session, status string) (ClaudeEndResult, error) {
	s, err := src.snapshot()
	if err != nil {
		return ClaudeEndResult{Procs: []ClaudeEnded{}}, codeError{Code: "unreadable"}
	}
	a := attributeClaude(s)
	var open *claudeOpen
	for i := range a.Sessions {
		if p := a.Sessions[i].Proc; p.Pid == target.Pid && p.Start == target.Start && claudeEndable(p, s.UID) {
			open = &a.Sessions[i]
			break
		}
	}
	if open != nil && open.Own {
		return ClaudeEndResult{Procs: []ClaudeEnded{}}, codeError{Code: "blocked"}
	}
	if open == nil || open.ID != session || (status == "idle" && open.Record.Status != "idle") {
		return ClaudeEndResult{Procs: []ClaudeEnded{{Pid: target.Pid, Result: claudeResultChanged}}}, nil
	}
	ends := []claudeEnding{newClaudeEnding(open.Proc, s.UID)}
	for _, m := range open.Members {
		ends = append(ends, newClaudeEnding(m, s.UID))
	}
	return claudeEnd(ctx, src, ends), nil
}

// claudeEnding is one process to end: its pid and start in the fresh
// snapshot, its footprint before any signal, the last signal that reached
// it, and its result once that is known.
type claudeEnding struct {
	pid    int32
	start  int64
	freed  uint64
	sent   syscall.Signal // 0 until a signal reaches it
	result string         // "" until it is known
}

// newClaudeEnding is the ending of p, a process of a fresh snapshot of the
// account uid. Whatever the rules list, it is changed unless p may get a
// signal at all.
func newClaudeEnding(p claudeProc, uid uint32) claudeEnding {
	if !claudeEndable(p, uid) {
		return claudeEnding{pid: p.Pid, result: claudeResultChanged}
	}
	return claudeEnding{pid: p.Pid, start: p.Start}
}

// claudeEndable reports whether p may get a signal at all: it is a process
// of the account uid, and neither launchd nor a pid below it.
func claudeEndable(p claudeProc, uid uint32) bool {
	return p.Pid > 1 && p.UID == uid
}

// claudeEnd ends the processes of ends that have no result yet. It reads
// their footprints, sends each SIGTERM, waits up to claudeTermWait, sends
// SIGKILL to each still there with its start unchanged, and waits up to
// claudeKillWait. Once ctx ends a wait it waits no longer and signals
// nothing more. The results keep the order of ends, and Freed adds up the
// footprints of the processes that are gone.
func claudeEnd(ctx context.Context, src claudeSource, ends []claudeEnding) ClaudeEndResult {
	for i := range ends {
		if ends[i].result == "" {
			ends[i].freed = src.footprint(ends[i].pid)
		}
	}
	for i := range ends {
		if ends[i].result == "" {
			ends[i].send(src, syscall.SIGTERM)
		}
	}
	if claudeWait(ctx, src, ends, syscall.SIGTERM, claudeTermWait) {
		for i := range ends {
			if e := &ends[i]; e.result == "" && e.sent == syscall.SIGTERM {
				e.look(src)
				if e.result == "" {
					e.send(src, syscall.SIGKILL)
				}
			}
		}
		claudeWait(ctx, src, ends, syscall.SIGKILL, claudeKillWait)
	}
	res := ClaudeEndResult{Procs: make([]ClaudeEnded, len(ends))}
	for i := range ends {
		e := &ends[i]
		if e.result == "" {
			e.look(src)
		}
		if e.result == "" {
			e.result = claudeResultRunning
		}
		res.Procs[i] = ClaudeEnded{Pid: e.pid, Result: e.result}
		if e.result == claudeResultEnded || e.result == claudeResultForced {
			res.Freed += e.freed
		}
	}
	return res
}

// claudeWait looks every claudePoll, for at most limit, at the processes
// whose last signal was sig, until each is gone. It returns false when ctx
// ends the wait, or had ended before it.
func claudeWait(ctx context.Context, src claudeSource, ends []claudeEnding, sig syscall.Signal, limit time.Duration) bool {
	waiting := func(e claudeEnding) bool { return e.result == "" && e.sent == sig }
	for waited := time.Duration(0); ctx.Err() == nil && waited < limit && slices.ContainsFunc(ends, waiting); {
		step := min(claudePoll, limit-waited)
		if src.sleep(ctx, step) != nil {
			return false
		}
		waited += step
		for i := range ends {
			if waiting(ends[i]) {
				ends[i].look(src)
			}
		}
	}
	return ctx.Err() == nil
}

// send sends sig to e's one process: never to a process group, and never
// to launchd or a pid below it, whatever e holds. A process the signal
// finds gone has ended; one it cannot reach gets no other signal.
func (e *claudeEnding) send(src claudeSource, sig syscall.Signal) {
	if e.pid <= 1 {
		return
	}
	switch err := src.signal(e.pid, sig); {
	case err == nil:
		e.sent = sig
	case errors.Is(err, syscall.ESRCH):
		e.result = claudeResultEnded
	}
}

// look checks that e's process is still there with its start. Once it is
// gone, or another process has its pid, e has its result: forced when
// SIGKILL reached it, else ended.
func (e *claudeEnding) look(src claudeSource) {
	if start, ok := src.started(e.pid); ok && start == e.start {
		return
	}
	e.result = claudeResultEnded
	if e.sent == syscall.SIGKILL {
		e.result = claudeResultForced
	}
}
