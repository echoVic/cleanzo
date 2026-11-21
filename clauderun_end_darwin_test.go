package main

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Ending is tested against endFake, a made-up process table: its snapshot
// is built from the attribution fixtures, each process answers signals as
// its endHow says, and sleeping only moves a fake clock. No real process
// gets a signal.

// endMiB is the fake footprint unit: the process with pid p uses p MiB.
const endMiB = 1 << 20

// endNewer is how much later than the process it replaces a process that
// takes over a pid started, in microseconds.
const endNewer = int64(time.Hour / time.Microsecond)

// endHow is how a process of an endFake answers signals.
type endHow int

const (
	endQuitsOnTerm  endHow = iota // it exits on SIGTERM
	endQuitsOnKill                // it ignores SIGTERM and exits on SIGKILL
	endNeverQuits                 // it survives both
	endGoneAtSignal               // it exits just before the first signal reaches it
	endRefuses                    // every signal to it fails with EPERM
)

// endProc is one process of an endFake.
type endProc struct {
	start  int64 // as the snapshot has it
	how    endHow
	exited bool
	exitAt time.Duration // when it exits by itself; 0 for never
	reused bool          // once it has exited, a newer process has its pid
}

// endLook is the last answer an endFake's started gave for a pid, and how
// many sleeps had come before it.
type endLook struct {
	start  int64
	ok     bool
	sleeps int
}

// endSent is one call of an endFake's signal.
type endSent struct {
	pid     int32
	sig     syscall.Signal
	at      time.Duration // the fake clock
	checked bool          // the last look at pid, with no sleep since, found the process the snapshot saw
}

// endFake is a made-up process table that ending reads and signals. Its
// clock starts at 0 and moves only when ending sleeps.
type endFake struct {
	t        *testing.T
	snap     claudeSnapshot
	err      error // what snapshot fails with; nil for none
	procs    map[int32]*endProc
	clock    time.Duration
	now      time.Time // what now gives while the clock is at 0
	sleeps   int
	late     int // sleeps after the context was canceled
	cancelAt time.Duration
	cancel   context.CancelFunc // called once the clock reaches cancelAt, when that is not 0
	looks    map[int32]endLook
	sent     []endSent
}

// endFakeOf is an endFake whose snapshot is s and whose processes are those
// of s, each exiting on SIGTERM. When t ends it checks what ending did.
func endFakeOf(t *testing.T, s claudeSnapshot) *endFake {
	f := &endFake{t: t, snap: s, procs: map[int32]*endProc{}, looks: map[int32]endLook{}}
	for _, p := range s.Procs {
		f.procs[p.Pid] = &endProc{start: p.Start}
	}
	t.Cleanup(f.check)
	return f
}

// source is the claudeSource ending reads. It has none of the reads that
// only the Running tab's listing uses, so ending calling one panics.
func (f *endFake) source() claudeSource {
	return claudeSource{
		snapshot:  func() (claudeSnapshot, error) { return f.snap, f.err },
		started:   f.started,
		footprint: f.footprint,
		signal:    f.signal,
		now:       func() time.Time { return f.now.Add(f.clock) },
		sleep:     f.sleep,
	}
}

// state is the start of the process that has pid at the fake clock; ok is
// false when none has it.
func (f *endFake) state(pid int32) (start int64, ok bool) {
	p := f.procs[pid]
	if p == nil {
		return 0, false
	}
	if p.exitAt > 0 && f.clock >= p.exitAt {
		p.exited = true
	}
	switch {
	case !p.exited:
		return p.start, true
	case p.reused:
		return p.start + endNewer, true
	}
	return 0, false
}

// started answers as the process that has pid does, and notes the look.
func (f *endFake) started(pid int32) (int64, bool) {
	start, ok := f.state(pid)
	f.looks[pid] = endLook{start: start, ok: ok, sleeps: f.sleeps}
	return start, ok
}

// footprint is pid MiB while the process the snapshot saw runs, and 0 once
// it is gone.
func (f *endFake) footprint(pid int32) uint64 {
	if start, ok := f.state(pid); ok && start == f.procs[pid].start {
		return uint64(pid) * endMiB
	}
	return 0
}

// signal delivers sig to the process that has pid, which answers as its
// endHow says, and notes the call. A newer process that took over the pid
// takes the signal and keeps running.
func (f *endFake) signal(pid int32, sig syscall.Signal) error {
	p := f.procs[pid]
	look, looked := f.looks[pid]
	checked := looked && look.ok && look.sleeps == f.sleeps && p != nil && look.start == p.start
	f.sent = append(f.sent, endSent{pid: pid, sig: sig, at: f.clock, checked: checked})
	start, ok := f.state(pid)
	switch {
	case !ok:
		return syscall.ESRCH
	case start != p.start:
		return nil
	case p.how == endRefuses:
		return syscall.EPERM
	case p.how == endGoneAtSignal:
		p.exited = true
		return syscall.ESRCH
	case p.how == endQuitsOnTerm, p.how == endQuitsOnKill && sig == syscall.SIGKILL:
		p.exited = true
	}
	return nil
}

// sleep moves the fake clock by d, and cancels the context once the clock
// reaches cancelAt.
func (f *endFake) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		f.late++
		return err
	}
	f.clock += d
	f.sleeps++
	if f.cancelAt > 0 && f.clock >= f.cancelAt {
		f.cancel()
		return ctx.Err()
	}
	return nil
}

// check fails the test when ending broke a rule of signals: one went to
// pid 1 or below, which is launchd, a process group or every process; one
// was neither SIGTERM nor SIGKILL; a pid got either twice; a SIGKILL came
// sooner than claudeTermWait after the pid's SIGTERM, or without a look
// right before it that found the process the snapshot saw. Ending also
// never waits longer than both waits, nor sleeps once canceled.
func (f *endFake) check() {
	termed := map[int32]time.Duration{}
	killed := map[int32]bool{}
	for _, c := range f.sent {
		switch {
		case c.pid <= 1:
			f.t.Errorf("signal %d went to pid %d", c.sig, c.pid)
		case c.sig == syscall.SIGTERM:
			if _, twice := termed[c.pid]; twice {
				f.t.Errorf("pid %d got SIGTERM twice", c.pid)
			}
			termed[c.pid] = c.at
		case c.sig == syscall.SIGKILL:
			if killed[c.pid] {
				f.t.Errorf("pid %d got SIGKILL twice", c.pid)
			}
			killed[c.pid] = true
			if at, ok := termed[c.pid]; !ok || c.at-at < claudeTermWait {
				f.t.Errorf("pid %d got SIGKILL at %v without SIGTERM %v before it", c.pid, c.at, claudeTermWait)
			}
			if !c.checked {
				f.t.Errorf("pid %d got SIGKILL without a look right before it that found its process", c.pid)
			}
		default:
			f.t.Errorf("pid %d got signal %d", c.pid, c.sig)
		}
	}
	if limit := claudeTermWait + claudeKillWait; f.clock > limit {
		f.t.Errorf("ending waited %v, longer than %v", f.clock, limit)
	}
	if f.late > 0 {
		f.t.Errorf("ending slept %d more times after it was canceled", f.late)
	}
}

// sentText writes the signals f got in order, each as its pid, its name and
// the fake clock, such as "121 TERM@0s 121 KILL@2s".
func (f *endFake) sentText() string {
	names := map[syscall.Signal]string{syscall.SIGTERM: "TERM", syscall.SIGKILL: "KILL"}
	parts := make([]string, len(f.sent))
	for i, c := range f.sent {
		name, ok := names[c.sig]
		if !ok {
			name = fmt.Sprint(int(c.sig))
		}
		parts[i] = fmt.Sprintf("%d %s@%v", c.pid, name, c.at)
	}
	return strings.Join(parts, " ")
}

// endText writes res down for comparing, such as "121:ended 120:forced;
// freed 241": each process as its pid and result, then Freed in endMiB.
func endText(res ClaudeEndResult) string {
	parts := make([]string, len(res.Procs))
	for i, p := range res.Procs {
		parts[i] = fmt.Sprintf("%d:%s", p.Pid, p.Result)
	}
	return fmt.Sprintf("%s; freed %g", strings.Join(parts, " "), float64(res.Freed)/endMiB)
}

// endWant fails t unless ending returned no error and the results want,
// and f got the signals sent.
func endWant(t *testing.T, f *endFake, res ClaudeEndResult, err error, want, sent string) {
	t.Helper()
	if err != nil {
		t.Errorf("err = %v, want none", err)
	}
	if got := endText(res); got != want {
		t.Errorf("results = %s\n     want %s", got, want)
	}
	if got := f.sentText(); got != sent {
		t.Errorf("signals = %s\n     want %s", got, sent)
	}
}
