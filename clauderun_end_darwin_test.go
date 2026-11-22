package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Ending is tested against endFake, a made-up process table: its snapshot
// is built from the attribution fixtures, each process answers signals as
// its endHow says, and sleeping only moves a fake clock. No real process
// gets a signal, with two exceptions: the test of the real signal function
// sends the null signal, which delivers nothing, and one test ends a child
// it starts itself through the real source.

// endMiB is the fake footprint unit: the process with pid p uses p MiB.
const endMiB = 1 << 20

// endNewer is how much later, in microseconds, a process that takes over a
// pid started than the one before it.
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
	start     int64 // as the snapshot has it
	how       endHow
	exited    bool
	exitAt    time.Duration // when it exits by itself; 0 for never
	exitLooks int           // it exits by itself right after this many looks at its pid; 0 for never
	looks     int           // at its pid so far
	reused    bool          // once it has exited, a newer process has its pid
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
	// transcripts is what the transcripts tell; nil for testTranscripts.
	transcripts map[string]claudeTranscript
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
		transcripts: func(sessions []string) map[string]claudeTranscript {
			if f.transcripts != nil {
				return f.transcripts
			}
			return testTranscripts(sessions)
		},
		signal: f.signal,
		now:    func() time.Time { return f.now.Add(f.clock) },
		sleep:  f.sleep,
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

// started answers as the process that has pid does, and records the look.
func (f *endFake) started(pid int32) (int64, bool) {
	start, ok := f.state(pid)
	f.looks[pid] = endLook{start: start, ok: ok, sleeps: f.sleeps}
	if p := f.procs[pid]; p != nil {
		if p.looks++; p.looks == p.exitLooks {
			p.exited = true
		}
	}
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
// endHow says, and records the call. A newer process that took over the pid
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

// endTarget names the fixture with pid as the page does: by pid and start.
func endTarget(pid int32) ClaudeTarget {
	return ClaudeTarget{Pid: pid, Start: testStart(pid)}
}

// endWantCode fails t unless err is the codeError code.
func endWantCode(t *testing.T, err error, code string) {
	t.Helper()
	if ce, ok := err.(codeError); !ok || ce.Code != code {
		t.Errorf("err = %v, want %s", err, code)
	}
}

// endWantNothing fails t unless res names no process and frees nothing, and
// f got no signal.
func endWantNothing(t *testing.T, f *endFake, res ClaudeEndResult) {
	t.Helper()
	if len(res.Procs) != 0 || res.Freed != 0 {
		t.Errorf("results = %s, want none", endText(res))
	}
	if len(f.sent) != 0 {
		t.Errorf("signals = %s, want none", f.sentText())
	}
}

// ---- ending processes ------------------------------------------------------

func TestEndClaudeProcsEndsWhatIsListed(t *testing.T) {
	// 120 is the shell Claude Code ran the command in: it folds into 121's row.
	s := ccSnap(wrapper(120, 1, "npm run dev"), marked(121, 120, 120, "S9"))
	wantShape(t, attributeClaude(s), "100:S1 members[] commands[]; left S9[121+120]; uncertain 0")
	f := endFakeOf(t, s)
	res, err := endClaudeProcsWith(context.Background(), f.source(), "S9", []ClaudeTarget{endTarget(121), endTarget(120)})
	endWant(t, f, res, err, "121:ended 120:ended; freed 241", "121 TERM@0s 120 TERM@0s")
}

func TestEndClaudeProcsEndsACommandOfAnOpenSession(t *testing.T) {
	// Ten seconds old by any clock: too young for the page to list it.
	young := marked(110, 100, 110, "S1")
	young.Start = time.Now().Add(-10 * time.Second).UnixMicro()
	s := ccSnap(young)
	wantShape(t, attributeClaude(s), "100:S1 members[] commands[110]; uncertain 0")
	f := endFakeOf(t, s)
	f.now = time.Now()
	res, err := endClaudeProcsWith(context.Background(), f.source(), "S1", []ClaudeTarget{{Pid: 110, Start: young.Start}})
	endWant(t, f, res, err, "110:ended; freed 110", "110 TERM@0s")
}

func TestEndClaudeProcsSkipsWhatChanged(t *testing.T) {
	other := marked(112, 100, 112, "S1")
	other.UID = 0
	second := ccProc(200, 1)
	s := ccSnap(
		marked(101, 100, 100, "S1"), // in the session's process group: its helper
		marked(110, 100, 110, "S1"),
		other,
		plain(113, 100, 113),
		marked(120, 1, 120, "S9"),
		second,
		marked(210, 200, 210, "S2"),
	)
	s.Records[200] = recordOf(second, "S2")
	wantShape(t, attributeClaude(s), "100:S1 members[101] commands[110]; 200:S2 members[] commands[210]; left S9[120]; uncertain 0")
	f := endFakeOf(t, s)
	restarted := endTarget(110)
	restarted.Start += time.Second.Microseconds()
	targets := []ClaudeTarget{
		restarted,      // its start is not the one listed
		endTarget(130), // gone
		endTarget(113), // running, not listed
		endTarget(120), // listed under a closed session
		endTarget(210), // listed under another open session
		endTarget(101), // the session's helper
		endTarget(1),
		endTarget(112), // another account's
	}
	res, err := endClaudeProcsWith(context.Background(), f.source(), "S1", targets)
	endWant(t, f, res, err, "110:changed 130:changed 113:changed 120:changed 210:changed 101:changed 1:changed 112:changed; freed 0", "")
}

func TestEndClaudeProcsLeavesAnEarlierSessionIDAlone(t *testing.T) {
	// The page listed 120 as left by S9. By now the transcripts show that
	// the Claude Code process 100 took its session id after 120 started, in
	// the folder S9 worked in: 120 may be what it started before /clear.
	s := transcriptSnap(nil, marked(120, 1, 120, "S9"))
	wantShape(t, attributeClaude(s), "100:S1 members[] commands[]; left S9[120]; uncertain 0")
	f := endFakeOf(t, s)
	f.transcripts = map[string]claudeTranscript{"S9": {Cwd: "/work/app"}, "S1": transcriptMade(130)}
	res, err := endClaudeProcsWith(context.Background(), f.source(), "S9", []ClaudeTarget{endTarget(120)})
	endWant(t, f, res, err, "120:changed; freed 0", "")
	// While 100 is seen to have had its session id before 120 started, 120
	// is ended.
	f.transcripts["S1"] = transcriptMade(110)
	res, err = endClaudeProcsWith(context.Background(), f.source(), "S9", []ClaudeTarget{endTarget(120)})
	endWant(t, f, res, err, "120:ended; freed 120", "120 TERM@0s")
}

func TestEndClaudeProcsEscalates(t *testing.T) {
	s := ccSnap(marked(120, 1, 120, "S9"), marked(121, 1, 121, "S9"), marked(122, 1, 122, "S9"))
	wantShape(t, attributeClaude(s), "100:S1 members[] commands[]; left S9[120 121 122]; uncertain 0")
	f := endFakeOf(t, s)
	f.procs[121].how = endQuitsOnKill
	f.procs[122].how = endNeverQuits
	res, err := endClaudeProcsWith(context.Background(), f.source(), "S9", []ClaudeTarget{endTarget(120), endTarget(121), endTarget(122)})
	endWant(t, f, res, err, "120:ended 121:forced 122:running; freed 241",
		"120 TERM@0s 121 TERM@0s 122 TERM@0s 121 KILL@2s 122 KILL@2s")
	if want := claudeTermWait + claudeKillWait; f.clock != want {
		t.Errorf("ending waited %v, want %v for the process that survives both signals", f.clock, want)
	}
}

func TestEndClaudeProcsRechecksBeforeKill(t *testing.T) {
	// 120 ignores SIGTERM, then exits by itself and a newer process takes
	// its pid: while ending waits, as the wait ends, or right after the
	// wait's last look, one every claudePoll.
	for _, c := range []struct {
		name      string
		exitAt    time.Duration
		exitLooks int
	}{
		{"while waiting", 1500 * time.Millisecond, 0},
		{"as the wait ends", claudeTermWait, 0},
		{"after the wait's last look", 0, int(claudeTermWait / claudePoll)},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := endFakeOf(t, ccSnap(marked(120, 1, 120, "S9")))
			p := f.procs[120]
			p.how, p.exitAt, p.exitLooks, p.reused = endQuitsOnKill, c.exitAt, c.exitLooks, true
			res, err := endClaudeProcsWith(context.Background(), f.source(), "S9", []ClaudeTarget{endTarget(120)})
			endWant(t, f, res, err, "120:ended; freed 120", "120 TERM@0s")
		})
	}
}

func TestEndClaudeProcsAlreadyGone(t *testing.T) {
	f := endFakeOf(t, ccSnap(marked(120, 1, 120, "S9")))
	f.procs[120].how = endGoneAtSignal
	res, err := endClaudeProcsWith(context.Background(), f.source(), "S9", []ClaudeTarget{endTarget(120)})
	endWant(t, f, res, err, "120:ended; freed 120", "120 TERM@0s")
	if f.clock != 0 {
		t.Errorf("ending waited %v for a process that was gone", f.clock)
	}
}

func TestEndClaudeProcsStopsWaitingWhenCanceled(t *testing.T) {
	for _, c := range []struct {
		name     string
		cancelAt time.Duration
		how      endHow // 121's; 120 exits on SIGTERM
		sent     string
	}{
		{"waiting after SIGTERM", 500 * time.Millisecond, endQuitsOnKill, "120 TERM@0s 121 TERM@0s"},
		{"waiting after SIGKILL", 2500 * time.Millisecond, endNeverQuits, "120 TERM@0s 121 TERM@0s 121 KILL@2s"},
		// The page went away before ending began: SIGTERM still goes out, and nothing waits or escalates.
		{"before the call", 0, endQuitsOnKill, "120 TERM@0s 121 TERM@0s"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := endFakeOf(t, ccSnap(marked(120, 1, 120, "S9"), marked(121, 1, 121, "S9")))
			f.procs[121].how = c.how
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.cancelAt, f.cancel = c.cancelAt, cancel
			if c.cancelAt == 0 {
				cancel()
			}
			res, err := endClaudeProcsWith(ctx, f.source(), "S9", []ClaudeTarget{endTarget(120), endTarget(121)})
			endWant(t, f, res, err, "120:ended 121:running; freed 120", c.sent)
			if f.clock != c.cancelAt {
				t.Errorf("ending waited %v, want it to stop at %v", f.clock, c.cancelAt)
			}
		})
	}
}

func TestEndClaudeProcsHandlesEachPidOnce(t *testing.T) {
	f := endFakeOf(t, ccSnap(wrapper(120, 1, "npm run dev"), marked(121, 120, 120, "S9")))
	other := endTarget(120)
	other.Start += time.Second.Microseconds()
	targets := []ClaudeTarget{endTarget(121), endTarget(120), endTarget(121), other}
	res, err := endClaudeProcsWith(context.Background(), f.source(), "S9", targets)
	endWant(t, f, res, err, "121:ended 120:ended; freed 241", "121 TERM@0s 120 TERM@0s")
}

func TestEndClaudeProcsWhenASignalIsRefused(t *testing.T) {
	// 120 and 122 refuse every signal; 120 exits by itself while 121 is
	// waited for.
	f := endFakeOf(t, ccSnap(marked(120, 1, 120, "S9"), marked(121, 1, 121, "S9"), marked(122, 1, 122, "S9")))
	f.procs[120].how, f.procs[120].exitAt = endRefuses, 500*time.Millisecond
	f.procs[121].how = endQuitsOnKill
	f.procs[122].how = endRefuses
	res, err := endClaudeProcsWith(context.Background(), f.source(), "S9", []ClaudeTarget{endTarget(120), endTarget(121), endTarget(122)})
	endWant(t, f, res, err, "120:ended 121:forced 122:running; freed 241", "120 TERM@0s 121 TERM@0s 122 TERM@0s 121 KILL@2s")
}

func TestEndClaudeProcsUnreadableSnapshot(t *testing.T) {
	f := endFakeOf(t, ccSnap(marked(120, 1, 120, "S9")))
	f.err = errors.New("no process table")
	res, err := endClaudeProcsWith(context.Background(), f.source(), "S9", []ClaudeTarget{endTarget(120)})
	endWantCode(t, err, "unreadable")
	endWantNothing(t, f, res)
}

func TestEndClaudeChecksPidAndAccountItself(t *testing.T) {
	// The rules never list these; ending refuses them on its own as well.
	other := marked(120, 1, 120, "S9")
	other.UID = 0
	for _, p := range []claudeProc{other, {Pid: 1, UID: testUID}, {Pid: 0, UID: testUID}, {Pid: -1, UID: testUID}} {
		if e := newClaudeEnding(p, testUID); e.result != "changed" {
			t.Errorf("pid %d of account %d is %q, want changed", p.Pid, p.UID, e.result)
		}
	}
	f := endFakeOf(t, ccSnap())
	claudeEnd(context.Background(), f.source(), []claudeEnding{{pid: 1, start: testStart(1)}, {pid: 0}, {pid: -4242}})
	if len(f.sent) != 0 {
		t.Errorf("signals = %s, want none", f.sentText())
	}
}

// ---- ending a session ------------------------------------------------------

func TestEndClaudeSessionLeavesItsCommands(t *testing.T) {
	agent := ccProc(102, 100) // a sub-agent
	s := ccSnap(marked(101, 100, 100, "S1"), agent, marked(111, 100, 111, "S1"))
	s.Records[102] = recordOf(agent, "S5")
	wantShape(t, attributeClaude(s), "100:S1 members[101 102] commands[111]; uncertain 0")
	f := endFakeOf(t, s)
	res, err := endClaudeSessionWith(context.Background(), f.source(), endTarget(100), "S1", "idle")
	endWant(t, f, res, err, "100:ended 101:ended 102:ended; freed 303", "100 TERM@0s 101 TERM@0s 102 TERM@0s")
}

func TestEndClaudeSessionRefusesItsOwn(t *testing.T) {
	cleanzo := marked(900, 100, 900, "S1")
	cleanzo.Args = []string{"cleanzo"}
	s := ccSnap(cleanzo)
	wantShape(t, attributeClaude(s), "100:S1 own members[] commands[]; uncertain 0")
	f := endFakeOf(t, s)
	res, err := endClaudeSessionWith(context.Background(), f.source(), endTarget(100), "S1", "idle")
	endWantCode(t, err, "blocked")
	endWantNothing(t, f, res)
}

func TestEndClaudeSessionThatChanged(t *testing.T) {
	// An ended session got SIGTERM; one that changed got nothing.
	const ended, changed = "100:ended; freed 100", "100:changed; freed 0"
	for _, c := range []struct {
		name               string
		id, status         string // its record's now; no record when id is ""
		seenID, seenStatus string // what the page saw
		want               string
	}{
		{"its session id is another now", "S2", "idle", "S1", "idle", changed},
		{"it was idle and is busy now", "S1", "busy", "S1", "idle", changed},
		{"it was idle and shows no status now", "S1", "", "S1", "idle", changed},
		{"it was busy and is idle now", "S1", "idle", "S1", "busy", ended},
		{"its status was unknown", "S1", "busy", "S1", "", ended},
		{"its session id was unknown and still is", "", "", "", "", ended},
		{"its session id was unknown and is known now", "S1", "idle", "", "", changed},
	} {
		t.Run(c.name, func(t *testing.T) {
			cc := ccProc(100, 1)
			records := map[int32]claudeRecord{}
			if c.id != "" {
				r := recordOf(cc, c.id)
				r.Status = c.status
				records[100] = r
			}
			f := endFakeOf(t, snap(records, cc))
			res, err := endClaudeSessionWith(context.Background(), f.source(), endTarget(100), c.seenID, c.seenStatus)
			sent := ""
			if c.want == ended {
				sent = "100 TERM@0s"
			}
			endWant(t, f, res, err, c.want, sent)
		})
	}
	t.Run("another process has its pid now", func(t *testing.T) {
		f := endFakeOf(t, ccSnap())
		seen := endTarget(100)
		seen.Start -= time.Second.Microseconds()
		res, err := endClaudeSessionWith(context.Background(), f.source(), seen, "S1", "idle")
		endWant(t, f, res, err, changed, "")
	})
}

func TestEndClaudeSessionSharingAnID(t *testing.T) {
	first, second := ccProc(100, 1), ccProc(101, 1)
	records := map[int32]claudeRecord{100: recordOf(first, "S1"), 101: recordOf(second, "S1")}
	s := snap(records, first, second, marked(110, 100, 100, "S1"), marked(111, 101, 101, "S1"))
	wantShape(t, attributeClaude(s), "100:S1 members[110] commands[]; 101:S1 members[111] commands[]; uncertain 0")
	f := endFakeOf(t, s)
	res, err := endClaudeSessionWith(context.Background(), f.source(), endTarget(100), "S1", "idle")
	endWant(t, f, res, err, "100:ended 110:ended; freed 210", "100 TERM@0s 110 TERM@0s")
	// The younger one: a session is found by its process, not as the first with that id.
	f = endFakeOf(t, s)
	res, err = endClaudeSessionWith(context.Background(), f.source(), endTarget(101), "S1", "idle")
	endWant(t, f, res, err, "101:ended 111:ended; freed 212", "101 TERM@0s 111 TERM@0s")
}

func TestEndClaudeSessionNotASession(t *testing.T) {
	agent := ccProc(105, 100)
	s := ccSnap(agent, marked(110, 100, 110, "S1"))
	s.Records[105] = recordOf(agent, "S5")
	wantShape(t, attributeClaude(s), "100:S1 members[105] commands[110]; uncertain 0")
	for _, c := range []struct {
		name    string
		pid     int32
		session string // the session id it carries
	}{
		{"a sub-agent", 105, "S5"},
		{"a command", 110, "S1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := endFakeOf(t, s)
			res, err := endClaudeSessionWith(context.Background(), f.source(), endTarget(c.pid), c.session, "idle")
			endWant(t, f, res, err, fmt.Sprintf("%d:changed; freed 0", c.pid), "")
		})
	}
}

func TestEndClaudeSessionUnreadableSnapshot(t *testing.T) {
	f := endFakeOf(t, ccSnap())
	f.err = errors.New("no process table")
	res, err := endClaudeSessionWith(context.Background(), f.source(), endTarget(100), "S1", "idle")
	endWantCode(t, err, "unreadable")
	endWantNothing(t, f, res)
}

func TestEndClaudeResultsHoldNoNull(t *testing.T) {
	ctx := context.Background()
	results := map[string]ClaudeEndResult{}
	f := endFakeOf(t, ccSnap())
	results["no targets"], _ = endClaudeProcsWith(ctx, f.source(), "S1", nil)
	results["a session that changed"], _ = endClaudeSessionWith(ctx, f.source(), endTarget(100), "S2", "idle")
	cleanzo := marked(900, 100, 900, "S1")
	cleanzo.Args = []string{"cleanzo"}
	results["Cleanzo's own session"], _ = endClaudeSessionWith(ctx, endFakeOf(t, ccSnap(cleanzo)).source(), endTarget(100), "S1", "idle")
	broken := endFakeOf(t, ccSnap())
	broken.err = errors.New("no process table")
	results["processes, unreadable"], _ = endClaudeProcsWith(ctx, broken.source(), "S1", []ClaudeTarget{endTarget(110)})
	results["a session, unreadable"], _ = endClaudeSessionWith(ctx, broken.source(), endTarget(100), "S1", "idle")
	for name, res := range results {
		if data, err := json.Marshal(res); err != nil || strings.Contains(string(data), "null") {
			t.Errorf("%s: %s, %v; want no null", name, data, err)
		}
	}
}

// ---- the real signal -------------------------------------------------------

func TestRealSignalRefusesLowPids(t *testing.T) {
	signal := realClaudeSource().signal
	for _, pid := range []int32{1, 0, -1, -4242} {
		// The null signal delivers nothing, even where the guard would let it through.
		if err := signal(pid, 0); !errors.Is(err, syscall.EINVAL) {
			t.Errorf("signal(%d, 0) = %v, want EINVAL", pid, err)
		}
	}
}

// TestEndClaudeHelper is the child TestEndClaudeEndsARealChild starts: it
// ignores SIGTERM, says so, and sleeps until it is killed. Run as a test it
// returns at once.
func TestEndClaudeHelper(t *testing.T) {
	if os.Getenv("CLEANZO_END_CLAUDE_HELPER") == "" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	fmt.Println("ready")
	time.Sleep(time.Minute)
	os.Exit(0)
}

// TestEndClaudeEndsARealChild ends a child of this test through the real
// source: the start the real snapshot gives the child is the start the looks
// compare, SIGTERM leaves the child running, and SIGKILL ends it once
// claudeTermWait has passed.
func TestEndClaudeEndsARealChild(t *testing.T) {
	setHome(t, t.TempDir())
	viewSampleApps(t)
	child := exec.Command(os.Args[0], "-test.run=^TestEndClaudeHelper$")
	child.Env = []string{"CLEANZO_END_CLAUDE_HELPER=1"}
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("the child said %q, %v, want ready", line, err)
	}
	pid := int32(child.Process.Pid)

	src := realClaudeSource()
	s, err := src.snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var kid *claudeProc
	for i := range s.Procs {
		if s.Procs[i].Pid == pid {
			kid = &s.Procs[i]
		}
	}
	if kid == nil {
		t.Fatal("the snapshot does not hold the child")
	}
	if start, ok := src.started(pid); !ok || start != kid.Start {
		t.Fatalf("the snapshot starts the child at %d, a look says %d, %v", kid.Start, start, ok)
	}

	began := time.Now()
	res := claudeEnd(context.Background(), src, []claudeEnding{newClaudeEnding(*kid, s.UID)})
	if len(res.Procs) != 1 || res.Procs[0] != (ClaudeEnded{Pid: pid, Result: claudeResultForced}) || res.Freed == 0 {
		t.Fatalf("ending the child gave %+v, want it forced with its footprint freed", res)
	}
	if waited := time.Since(began); waited < claudeTermWait {
		t.Errorf("SIGKILL came after %v, want it no sooner than %v", waited, claudeTermWait)
	}
	var exit *exec.ExitError
	if err := child.Wait(); !errors.As(err, &exit) {
		t.Fatalf("the child ended with %v, want it killed", err)
	}
	if ws, ok := exit.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
		t.Errorf("the child ended with %v, want SIGKILL", exit)
	}
}
