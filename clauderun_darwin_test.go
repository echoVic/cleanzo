package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// Fixtures for the Claude Code attribution rules. A made-up process with
// pid p starts p seconds after testEpoch, so of two fixtures the one with
// the lower pid started first.

// testUID is the account the fixtures run as.
const testUID = 501

// testEpoch is when a fixture with pid 0 would have started.
var testEpoch = time.Date(2026, time.November, 14, 9, 0, 0, 0, time.UTC)

// testStart is when the fixture with pid started, in unix microseconds.
func testStart(pid int32) int64 {
	return testEpoch.Add(time.Duration(pid) * time.Second).UnixMicro()
}

// ccProc is a Claude Code process of the account: argv[0] is claude, it
// leads its own process group and its environment cannot be read.
func ccProc(pid, ppid int32) claudeProc {
	return claudeProc{Pid: pid, Ppid: ppid, Pgid: pid, UID: testUID, Start: testStart(pid), Args: []string{"claude"}}
}

// recordOf is a record of session that is valid for p, with status idle.
func recordOf(p claudeProc, session string) claudeRecord {
	return claudeRecord{SessionID: session, Status: "idle", ProcStart: time.UnixMicro(p.Start).UTC().Format("Mon Jan _2 15:04:05 2006")}
}

// marked is a process of the account whose environment carries Claude
// Code's marker and session.
func marked(pid, ppid, pgid int32, session string) claudeProc {
	p := plain(pid, ppid, pgid)
	p.Env.Marked, p.Env.Session = true, session
	return p
}

// hidden is a process of the account whose environment cannot be read.
func hidden(pid, ppid, pgid int32) claudeProc {
	return claudeProc{Pid: pid, Ppid: ppid, Pgid: pgid, UID: testUID, Start: testStart(pid), Args: []string{"caffeinate", "-i"}}
}

// plain is a process of the account whose environment is read and carries
// no marker.
func plain(pid, ppid, pgid int32) claudeProc {
	return claudeProc{Pid: pid, Ppid: ppid, Pgid: pgid, UID: testUID, Start: testStart(pid), Args: []string{"node", "server.js"}, Env: claudeEnv{Seen: true}}
}

// wrapper is the shell Claude Code runs inner in: hidden, leading its own
// process group, with inner single-quoted inside its script.
func wrapper(pid, ppid int32, inner string) claudeProc {
	p := hidden(pid, ppid, pid)
	script := "source /x/snap.sh && eval '" + strings.ReplaceAll(inner, "'", `'\''`) + "' < /dev/null && pwd -P >| /x/cwd"
	p.Args = []string{"/bin/zsh", "-c", "-l", script}
	return p
}

// snap is a snapshot of procs and records for the account testUID, taken by
// Cleanzo as pid 900. It adds launchd (pid 1) and Cleanzo under launchd
// unless procs has its own process with that pid.
func snap(records map[int32]claudeRecord, procs ...claudeProc) claudeSnapshot {
	s := claudeSnapshot{Records: records, Self: 900, UID: testUID}
	given := func(pid int32) bool {
		return slices.ContainsFunc(procs, func(p claudeProc) bool { return p.Pid == pid })
	}
	if !given(1) {
		s.Procs = append(s.Procs, claudeProc{Pid: 1, Pgid: 1, Start: testStart(1)})
	}
	if !given(900) {
		cleanzo := plain(900, 1, 900)
		cleanzo.Args = []string{"cleanzo"}
		s.Procs = append(s.Procs, cleanzo)
	}
	s.Procs = append(s.Procs, procs...)
	return s
}

// ccSnap is snap of procs and the Claude Code process 100 under launchd,
// whose record says session S1.
func ccSnap(procs ...claudeProc) claudeSnapshot {
	cc := ccProc(100, 1)
	return snap(map[int32]claudeRecord{100: recordOf(cc, "S1")}, append([]claudeProc{cc}, procs...)...)
}

// pidsOf is the pid of each of procs, in order.
func pidsOf(procs []claudeProc) []int32 {
	pids := make([]int32, len(procs))
	for i, p := range procs {
		pids[i] = p.Pid
	}
	return pids
}

// shape writes a down for comparing, such as
// "100:S1 own members[105] commands[111+110]; left S9[120]; uncertain 0 norecords":
// each open session as its Claude Code process, id, members and command
// rows, then each closed session as its id and rows. A row is its lead
// followed by "+" and each process folded into it.
func shape(a claudeAttribution) string {
	rows := func(rows []claudeRow) string {
		parts := make([]string, len(rows))
		for i, r := range rows {
			parts[i] = fmt.Sprint(r.Lead.Pid)
			for _, f := range r.Folded {
				parts[i] += fmt.Sprintf("+%d", f.Pid)
			}
		}
		return "[" + strings.Join(parts, " ") + "]"
	}
	var b strings.Builder
	for _, s := range a.Sessions {
		fmt.Fprintf(&b, "%d:%s", s.Proc.Pid, s.ID)
		if s.Own {
			b.WriteString(" own")
		}
		fmt.Fprintf(&b, " members%v commands%s; ", pidsOf(s.Members), rows(s.Commands))
	}
	for _, l := range a.Left {
		fmt.Fprintf(&b, "left %s%s; ", l.ID, rows(l.Rows))
	}
	fmt.Fprintf(&b, "uncertain %d", a.Uncertain)
	if a.NoRecords {
		b.WriteString(" norecords")
	}
	return b.String()
}

// wantShape fails t unless a has the shape want.
func wantShape(t *testing.T, a claudeAttribution, want string) {
	t.Helper()
	if got := shape(a); got != want {
		t.Errorf("attribution = %s\n          want %s", got, want)
	}
}

// ---- processes under a session ---------------------------------------------

func TestClaudeCommandUnderItsSession(t *testing.T) {
	a := attributeClaude(ccSnap(wrapper(110, 100, "npm run dev"), marked(111, 110, 110, "S1")))
	wantShape(t, a, "100:S1 members[] commands[111+110]; uncertain 0")
}

func TestClaudeOldSessionIDStaysWithItsParent(t *testing.T) {
	a := attributeClaude(ccSnap(wrapper(110, 100, "npm run dev"), marked(111, 110, 110, "S0")))
	wantShape(t, a, "100:S1 members[] commands[111+110]; uncertain 0")
}

func TestClaudeHelpersJoinTheSession(t *testing.T) {
	a := attributeClaude(ccSnap(marked(101, 100, 100, "S1"), marked(102, 100, 100, "S1"), hidden(103, 100, 100)))
	wantShape(t, a, "100:S1 members[101 102 103] commands[]; uncertain 0")
}

func TestClaudeSubAgentIsNotASession(t *testing.T) {
	a := attributeClaude(ccSnap(ccProc(105, 100)))
	wantShape(t, a, "100:S1 members[105] commands[]; uncertain 0")
}

func TestClaudeUnmarkedIsNeverListed(t *testing.T) {
	// 161 and 162 share the session's process group but hang off launchd:
	// a process group is no clue.
	a := attributeClaude(ccSnap(plain(160, 100, 100), plain(161, 1, 100), hidden(162, 1, 100)))
	wantShape(t, a, "100:S1 members[] commands[]; uncertain 0")
}

func TestClaudeCleanzoFamilyIsLeftOut(t *testing.T) {
	// Cleanzo (900), started from the session, and what it starts carry
	// the session's marker.
	a := attributeClaude(ccSnap(hidden(890, 100, 890), marked(900, 890, 900, "S1"), marked(901, 900, 901, "S1")))
	wantShape(t, a, "100:S1 own members[] commands[]; uncertain 0")
}

// ---- detached processes ----------------------------------------------------

func TestClaudeDetachedOpenByID(t *testing.T) {
	a := attributeClaude(ccSnap(marked(120, 1, 120, "S1")))
	wantShape(t, a, "100:S1 members[] commands[120]; uncertain 0")
}

func TestClaudeDetachedOpenByClaudePid(t *testing.T) {
	p := marked(120, 1, 120, "S0")
	p.Env.Pid = 100
	wantShape(t, attributeClaude(ccSnap(p)), "100:S1 members[] commands[120]; uncertain 0")
}

func TestClaudeDetachedClosed(t *testing.T) {
	a := attributeClaude(ccSnap(marked(120, 1, 120, "S9")))
	wantShape(t, a, "100:S1 members[] commands[]; left S9[120]; uncertain 0")
}

func TestClaudeOutputFileClue(t *testing.T) {
	p := hidden(130, 1, 130)
	p.Out = "S9"
	wantShape(t, attributeClaude(ccSnap(p)), "100:S1 members[] commands[]; left S9[130]; uncertain 0")
}

func TestClaudeHiddenParentFollowsChild(t *testing.T) {
	a := attributeClaude(ccSnap(hidden(140, 1, 140), marked(141, 140, 141, "S9")))
	wantShape(t, a, "100:S1 members[] commands[]; left S9[140 141]; uncertain 0")
}

func TestClaudeHiddenChildFollowsParent(t *testing.T) {
	a := attributeClaude(ccSnap(marked(150, 1, 150, "S9"), hidden(151, 150, 151)))
	wantShape(t, a, "100:S1 members[] commands[]; left S9[150 151]; uncertain 0")
}

func TestClaudeFollowsChildrenFiveRoundsOnly(t *testing.T) {
	for _, c := range []struct {
		above int32 // hidden processes in a row above 149, the topmost under launchd
		want  string
	}{
		{5, "100:S1 members[] commands[]; left S9[144 145 146 147 148 149]; uncertain 0"},
		{6, "100:S1 members[] commands[]; uncertain 0"},
	} {
		t.Run(fmt.Sprint(c.above, " above"), func(t *testing.T) {
			procs := []claudeProc{marked(149, 148, 149, "S9")}
			for pid := int32(148); pid >= 149-c.above; pid-- {
				parent := pid - 1
				if pid == 149-c.above {
					parent = 1
				}
				procs = append(procs, hidden(pid, parent, pid))
			}
			wantShape(t, attributeClaude(ccSnap(procs...)), c.want)
		})
	}
}

func TestClaudeWrapperAloneIsARow(t *testing.T) {
	w := wrapper(170, 1, "npm run dev")
	w.Out = "S9"
	wantShape(t, attributeClaude(ccSnap(w)), "100:S1 members[] commands[]; left S9[170]; uncertain 0")
}

func TestClaudeAppHelpersFoldIntoTheApp(t *testing.T) {
	const app = "/Applications/Sample Browser.app" // not on the Dock
	browser := marked(180, 1, 180, "S9")
	browser.Path = app + "/Contents/MacOS/Sample Browser"
	helper := marked(181, 180, 180, "S9")
	helper.Path = app + "/Contents/Frameworks/Sample Browser Helper.app/Contents/MacOS/Sample Browser Helper"
	wantShape(t, attributeClaude(ccSnap(browser, helper)), "100:S1 members[] commands[]; left S9[180+181]; uncertain 0")
}

func TestClaudeHiddenParentOfTwoSessions(t *testing.T) {
	// The children of 140 carry different ids, so it takes neither.
	children := []claudeProc{marked(141, 140, 141, "S8"), marked(142, 140, 142, "S9")}
	detached := attributeClaude(ccSnap(append([]claudeProc{hidden(140, 1, 140)}, children...)...))
	wantShape(t, detached, "100:S1 members[] commands[]; uncertain 0")
	// Under the session it still follows its parent, and its children,
	// carrying other ids, are not listed.
	attached := attributeClaude(ccSnap(append([]claudeProc{hidden(140, 100, 140)}, children...)...))
	wantShape(t, attached, "100:S1 members[] commands[140]; uncertain 0")
}

// ---- what is never listed, and what is uncertain ---------------------------

func TestClaudeDockAppIsNeverListed(t *testing.T) {
	const app = "/Applications/Sample Editor.app"
	editor := marked(200, 1, 200, "S1") // opened from the session, so it carries the marker
	editor.Path = app + "/Contents/MacOS/Sample Editor"
	helper := hidden(201, 200, 200)
	helper.Path = app + "/Contents/Frameworks/Sample Editor Helper.app/Contents/MacOS/Sample Editor Helper"
	terminal := marked(202, 201, 202, "S1") // a shell in the editor's terminal
	job := hidden(203, 202, 203)
	apps := []workspaceApp{{Pid: 200, Path: app, Name: "Sample Editor", Regular: true}}

	open := ccSnap(editor, helper, terminal, job)
	open.Apps = apps
	wantShape(t, attributeClaude(open), "100:S1 members[] commands[]; uncertain 0")
	closed := snap(nil, editor, helper, terminal, job)
	closed.Apps = apps
	wantShape(t, attributeClaude(closed), "uncertain 0")
}

func TestClaudeChainThroughAnotherSession(t *testing.T) {
	a := attributeClaude(ccSnap(marked(181, 1, 181, "S2"), marked(180, 181, 180, "S1")))
	wantShape(t, a, "100:S1 members[] commands[]; left S2[181]; uncertain 0")
}

func TestClaudeMissingParent(t *testing.T) {
	a := attributeClaude(ccSnap(marked(190, 999, 190, "S9")))
	wantShape(t, a, "100:S1 members[] commands[]; uncertain 0")
}

func TestClaudeNoRecordMeansNothingIsLeftBehind(t *testing.T) {
	a := attributeClaude(snap(nil, ccProc(100, 1), marked(120, 1, 120, "S9")))
	wantShape(t, a, "100: members[] commands[]; uncertain 1 norecords")
}

func TestClaudePidTakenByAnOlderProcess(t *testing.T) {
	p := marked(120, 1, 120, "S9")
	p.Env.Pid = 300
	other := plain(300, 1, 300)
	other.Start = p.Start - time.Second.Microseconds()
	wantShape(t, attributeClaude(ccSnap(p, other)), "100:S1 members[] commands[]; uncertain 1")
	// A process that started after 120 cannot be the Claude Code that
	// started it.
	other.Start = p.Start + time.Second.Microseconds()
	wantShape(t, attributeClaude(ccSnap(p, other)), "100:S1 members[] commands[]; left S9[120]; uncertain 0")
}

func TestClaudeMarkedWithoutSessionID(t *testing.T) {
	// A marker without a session id carries no id: neither 110 under the
	// session nor 120 under launchd is listed, and no chain passes them.
	a := attributeClaude(ccSnap(
		marked(110, 100, 110, ""), marked(111, 110, 110, "S1"),
		marked(120, 1, 120, ""), marked(121, 120, 121, "S9"),
	))
	wantShape(t, a, "100:S1 members[] commands[]; uncertain 0")
	// Nor is such a process uncertain while a session id is unknown.
	a = attributeClaude(snap(nil, ccProc(100, 1), marked(120, 1, 120, "")))
	wantShape(t, a, "100: members[] commands[]; uncertain 0 norecords")
}
