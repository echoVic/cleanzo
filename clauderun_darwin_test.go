package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
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
	agent := ccProc(105, 100)
	s := ccSnap(agent)
	s.Records[105] = recordOf(agent, "S5")
	wantShape(t, attributeClaude(s), "100:S1 members[105] commands[]; uncertain 0")
}

func TestClaudeSessionInsideADockApp(t *testing.T) {
	// Session 100 runs a development build of an app on the Dock, and the
	// app runs a Claude Code session of its own through a shell.
	const app = "/p/out/Sample Studio.app"
	const executable = app + "/Contents/MacOS/Sample Studio"
	for _, c := range []struct {
		between                                 string
		studioParent, helperParent, shellParent int32
		studioPath                              string
	}{
		{"the app, its helper and a shell", 100, 200, 201, executable},
		{"the app, its path unreadable, and a shell", 100, 200, 200, ""},
		{"a helper of the app and a shell", 1, 100, 201, executable},
	} {
		t.Run(c.between, func(t *testing.T) {
			studio := marked(200, c.studioParent, 200, "S1")
			studio.Path = c.studioPath
			helper := marked(201, c.helperParent, 200, "S1")
			helper.Path = app + "/Contents/Frameworks/Sample Studio Helper.app/Contents/MacOS/Sample Studio Helper"
			inner := ccProc(300, 202)
			s := ccSnap(studio, helper, marked(202, c.shellParent, 202, "S1"), inner, marked(310, 300, 310, "S3"))
			s.Records[300] = recordOf(inner, "S3")
			s.Apps = []workspaceApp{{Pid: 200, Path: app, Name: "Sample Studio", Regular: true}}
			wantShape(t, attributeClaude(s), "100:S1 members[] commands[]; 300:S3 members[] commands[310]; uncertain 0")
		})
	}
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

func TestClaudeRelativeOfAnOpenCommandIsOpen(t *testing.T) {
	// Session 100 was S0 before it became S1. A process carrying S0 whose
	// CLAUDE_PID names 100 shows S0 is still open, for its relatives too.
	command := marked(120, 1, 120, "S0")
	command.Env.Pid = 100
	a := attributeClaude(ccSnap(command, hidden(121, 120, 120)))
	wantShape(t, a, "100:S1 members[] commands[120 121]; uncertain 0")

	child := marked(141, 140, 141, "S0")
	child.Env.Pid = 100
	a = attributeClaude(ccSnap(hidden(140, 1, 140), child))
	wantShape(t, a, "100:S1 members[] commands[140 141]; uncertain 0")
}

func TestClaudeOneClaudePidOpensTheWholeID(t *testing.T) {
	// Two trees carry S0, and only 120 says which Claude Code started it.
	command := marked(120, 1, 120, "S0")
	command.Env.Pid = 100
	a := attributeClaude(ccSnap(command, hidden(121, 120, 120), hidden(140, 1, 140), marked(141, 140, 141, "S0")))
	wantShape(t, a, "100:S1 members[] commands[120 121 140 141]; uncertain 0")
}

func TestClaudeClaudePidsIntoTwoSessions(t *testing.T) {
	// 120 and 130 carry S0 and name two different sessions, so 140, also
	// carrying S0 but naming none, may belong to either.
	first, second := ccProc(100, 1), ccProc(101, 1)
	records := map[int32]claudeRecord{100: recordOf(first, "S1"), 101: recordOf(second, "S2")}
	toFirst, toSecond := marked(120, 1, 120, "S0"), marked(130, 1, 130, "S0")
	toFirst.Env.Pid, toSecond.Env.Pid = 100, 101
	a := attributeClaude(snap(records, first, second, toFirst, toSecond, marked(140, 1, 140, "S0")))
	wantShape(t, a, "100:S1 members[] commands[120]; 101:S2 members[] commands[130]; uncertain 1")
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

func TestClaudeOlderProcessOnClaudePidMakesTheIDUncertain(t *testing.T) {
	// 300 may be the Claude Code that started 120, so neither 120 nor 121,
	// which carries the same id, is called left behind.
	p := marked(120, 1, 120, "S9")
	p.Env.Pid = 300
	other := plain(300, 1, 300)
	other.Start = p.Start - time.Second.Microseconds()
	wantShape(t, attributeClaude(ccSnap(p, hidden(121, 120, 120), other)), "100:S1 members[] commands[]; uncertain 2")
}

func TestClaudeSubAgentWithoutRecordMeansNothingIsLeftBehind(t *testing.T) {
	a := attributeClaude(ccSnap(ccProc(105, 100), marked(120, 1, 120, "S9")))
	wantShape(t, a, "100:S1 members[105] commands[]; uncertain 1 norecords")
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

func TestClaudeOddSnapshots(t *testing.T) {
	// No consistent process table has loops, duplicate pids or a pid 0
	// named claude, but nothing panics on them and what is odd is left out.
	const browser = "/p/out/Sample Browser.app/Contents/MacOS/Sample Browser"
	inBrowser := func(p claudeProc) claudeProc {
		p.Path = browser
		return p
	}
	withRecord := func(cc claudeProc, procs ...claudeProc) claudeSnapshot {
		return snap(map[int32]claudeRecord{cc.Pid: recordOf(cc, "S1")}, append([]claudeProc{cc}, procs...)...)
	}
	editor := hidden(200, 102, 200)
	editor.Path = "/Applications/Sample Editor.app/Contents/MacOS/Sample Editor"
	throughDock := withRecord(ccProc(100, 200), editor, inBrowser(hidden(101, 100, 101)), inBrowser(hidden(102, 101, 101)))
	throughDock.Apps = []workspaceApp{{Pid: 200, Path: "/Applications/Sample Editor.app", Name: "Sample Editor", Regular: true}}
	unreadable := hidden(110, 100, 110)
	unreadable.Args = nil
	for _, c := range []struct {
		odd  string
		s    claudeSnapshot
		want string
	}{
		{"a loop through a Claude Code process", withRecord(ccProc(100, 101), inBrowser(hidden(101, 100, 101))), "uncertain 0"},
		{"a Claude Code process that is its own parent", withRecord(ccProc(100, 100), inBrowser(hidden(101, 100, 101))), "uncertain 0"},
		{"a loop through an app on the Dock", throughDock, "uncertain 0"},
		{"a process that is its own parent", ccSnap(marked(120, 120, 120, "S9")), "100:S1 members[] commands[]; uncertain 0"},
		{"a pid listed twice", ccSnap(marked(120, 1, 120, "S9"), marked(120, 1, 120, "S9")), "100:S1 members[] commands[]; uncertain 0"},
		{"a Claude Code process listed twice", ccSnap(ccProc(100, 1)), "uncertain 0"},
		{"pid 0 named claude", ccSnap(ccProc(0, 0)), "100:S1 members[] commands[]; uncertain 0"},
		{"arguments that cannot be read", ccSnap(unreadable, marked(111, 110, 110, "S1")), "100:S1 members[] commands[110 111]; uncertain 0"},
	} {
		t.Run(c.odd, func(t *testing.T) {
			wantShape(t, attributeClaude(c.s), c.want)
		})
	}
}

// ---- records and one session id --------------------------------------------

func TestClaudeRecordMustMatchProcStart(t *testing.T) {
	p := ccProc(100, 1)
	if !claudeRecordValid(p, recordOf(p, "S1"), testUID) {
		t.Fatal("a record of the process's start is not valid")
	}
	r := recordOf(p, "S1")
	r.ProcStart = time.UnixMicro(p.Start).Add(time.Second).UTC().Format("Mon Jan _2 15:04:05 2006")
	if claudeRecordValid(p, r, testUID) {
		t.Error("a record of a start one second later is valid")
	}
	// The process is still Claude Code's, by its argv, with no session id.
	a := attributeClaude(snap(map[int32]claudeRecord{100: r}, p))
	wantShape(t, a, "100: members[] commands[]; uncertain 0 norecords")
	if len(a.Sessions) == 1 && a.Sessions[0].Record != (claudeRecord{}) {
		t.Errorf("record = %+v, want none", a.Sessions[0].Record)
	}
}

func TestClaudeRecordSqueezesSpaces(t *testing.T) {
	p := ccProc(100, 1)
	p.Start = time.Date(2026, time.November, 4, 9, 12, 33, 0, time.UTC).UnixMicro()
	for _, procStart := range []string{"Wed Nov  4 09:12:33 2026", "Wed Nov 4 09:12:33 2026"} {
		if !claudeRecordValid(p, claudeRecord{SessionID: "S1", ProcStart: procStart}, testUID) {
			t.Errorf("procStart %q is not valid", procStart)
		}
	}
}

func TestClaudeRecordWithoutClaudeArgv(t *testing.T) {
	p := ccProc(100, 1)
	p.Args = []string{"2.1.321"}
	wantShape(t, attributeClaude(snap(map[int32]claudeRecord{100: recordOf(p, "S1")}, p)), "100:S1 members[] commands[]; uncertain 0")
	wantShape(t, attributeClaude(snap(nil, p)), "uncertain 0")
}

func TestClaudeRecordFieldsAreOptional(t *testing.T) {
	p := ccProc(100, 1)
	r := claudeRecord{SessionID: "S1", ProcStart: recordOf(p, "").ProcStart}
	a := attributeClaude(snap(map[int32]claudeRecord{100: r}, p))
	wantShape(t, a, "100:S1 members[] commands[]; uncertain 0")
	if len(a.Sessions) == 1 && a.Sessions[0].Record != r {
		t.Errorf("record = %+v, want %+v", a.Sessions[0].Record, r)
	}
}

func TestClaudeOtherAccountIsNotASession(t *testing.T) {
	p := ccProc(100, 1)
	p.UID = 0
	wantShape(t, attributeClaude(snap(map[int32]claudeRecord{100: recordOf(p, "S1")}, p)), "uncertain 0")
}

func TestClaudeTwoProcessesOneSession(t *testing.T) {
	first, second := ccProc(100, 1), ccProc(101, 1)
	records := map[int32]claudeRecord{100: recordOf(first, "S1"), 101: recordOf(second, "S1")}
	p := marked(120, 1, 120, "S1")
	wantShape(t, attributeClaude(snap(records, first, second, p)), "100:S1 members[] commands[120]; 101:S1 members[] commands[]; uncertain 0")
	p.Env.Pid = 101
	wantShape(t, attributeClaude(snap(records, first, second, p)), "100:S1 members[] commands[]; 101:S1 members[] commands[120]; uncertain 0")
}

// ---- the command a row shows -----------------------------------------------

func TestClaudeWrapperCommand(t *testing.T) {
	script := `source /x/snap.sh && eval 'npm run dev -- --host '\''0.0.0.0'\''' < /dev/null && pwd -P >| /x/cwd`
	escaped := strings.Replace(script, `' < /dev/null`, `' \< /dev/null`, 1)
	const inner = `npm run dev -- --host '0.0.0.0'`
	for _, c := range []struct {
		args  []string
		inner string
		ok    bool
	}{
		{[]string{"/bin/zsh", "-c", "-l", script}, inner, true},
		{[]string{"/bin/zsh", "-c", "-l", escaped}, inner, true},
		{[]string{"-zsh", "-c", script}, inner, true},
		{[]string{"/bin/bash", "-c", script}, inner, true},
		{[]string{"sh", "-c", script}, inner, true},
		{wrapper(170, 1, "sort 'a b' < /dev/null").Args, "sort 'a b' < /dev/null", true},
		{[]string{"/bin/zsh", "-l"}, "", false},
		{[]string{"/opt/homebrew/bin/node", "-e", script}, "", false},
		{[]string{""}, "", false},
		{nil, "", false},
	} {
		if got, ok := claudeWrapperCommand(c.args); got != c.inner || ok != c.ok {
			t.Errorf("claudeWrapperCommand(%q) = %q, %v; want %q, %v", c.args, got, ok, c.inner, c.ok)
		}
	}
}

func TestMaskCommandKeepsThePlainCommand(t *testing.T) {
	got := maskCommand([]string{"/opt/homebrew/bin/node", "/p/node_modules/.bin/vite", "--port", "5173"})
	if want := "node /p/node_modules/.bin/vite --port 5173"; got != want {
		t.Errorf("maskCommand = %q, want %q", got, want)
	}
}

func TestMaskCommandHidesSecrets(t *testing.T) {
	got := maskCommand([]string{"curl", "--api-key=hunter2", "--Token", "abc", "-password=x", "https://example.invalid"})
	if want := "curl --api-key=… --Token … -password=… https://example.invalid"; got != want {
		t.Errorf("maskCommand = %q, want %q", got, want)
	}
	// Inside the shell, a quoted value is one word however many spaces it holds.
	got = maskCommand(wrapper(170, 1, `curl --token 'ab cd' --client-secret="ef gh" https://example.invalid`).Args)
	if want := "curl --token … --client-secret=… https://example.invalid"; got != want {
		t.Errorf("maskCommand = %q, want %q", got, want)
	}
}

func TestMaskCommandDropsLeadingAssignments(t *testing.T) {
	got := maskCommand(wrapper(170, 1, `API_KEY="a b" TOKEN='c d' node x.js`).Args)
	if want := "node x.js"; got != want {
		t.Errorf("maskCommand = %q, want %q", got, want)
	}
}

func TestMaskCommandHidesSecretsAnywhere(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{wrapper(170, 1, "cd /p && API_KEY=hunter2 node x.js").Args, "cd /p && API_KEY=… node x.js"},
		{[]string{"sh", "-c", "cd web && API_KEY=hunter2 node server.js"}, "sh -c cd web && API_KEY=… node server.js"},
		{[]string{"docker", "run", "-e", "API_KEY=hunter2", "img"}, "docker run -e API_KEY=… img"},
		{wrapper(170, 1, `docker run -e "API_KEY=hunter2" img`).Args, "docker run -e API_KEY=… img"},
		{[]string{"dd", "if=/dev/zero", "of=/tmp/x"}, "dd if=… of=…"},
		// The value of a secret flag is hidden even when it is a flag itself.
		{[]string{"curl", "--key", "--token", "hunter2"}, "curl --key … …"},
		{wrapper(170, 1, `curl "--token=hunter2" x`).Args, "curl --token=… x"},
		{[]string{"docker", "run", "--env=API_KEY=hunter2", "img"}, "docker run --env=API_KEY=… img"},
		{[]string{"psql", "postgres://app:hunter2@db.example.invalid/x"}, "psql postgres://…@db.example.invalid/x"},
		{[]string{"git", "clone", "https://app:hun@ter2@example.invalid/r.git"}, "git clone https://…@example.invalid/r.git"},
		{[]string{"node", "x.js", "--db=postgres://app:hunter2@db.example.invalid"}, "node x.js --db=postgres://…@db.example.invalid"},
	} {
		if got := maskCommand(c.args); got != c.want || strings.Contains(got, "hunter2") {
			t.Errorf("maskCommand(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestMaskCommandMasksAScriptWordByWord(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		// A script quoted inside the shell Claude Code runs a command in.
		{wrapper(170, 1, `bash -c 'cd web && API_KEY=hunter2 node server.js'`).Args, "bash -c cd web && API_KEY=… node server.js"},
		{[]string{"sh", "-c", `docker run -e "API_KEY=hunter2 x" img`}, "sh -c docker run -e API_KEY=… img"},
		{[]string{"node", "--require=cd web && API_KEY=hunter2"}, "node --require=cd web && API_KEY=…"},
		// Inside double quotes a backslash before a space is kept, so the
		// space stays in the assignment's value.
		{wrapper(170, 1, `sh -c "docker run -e API_KEY=hunter2\ x img"`).Args, "sh -c docker run -e API_KEY=… img"},
		// The value of a secret flag is one argument, script or not.
		{[]string{"curl", "--token", "hunter2 x"}, "curl --token …"},
		// A line continuation is no argument.
		{wrapper(170, 1, "curl --token \\\n  hunter2 https://example.invalid").Args, "curl --token … https://example.invalid"},
		// A secret flag that ends a script takes the next argument.
		{[]string{"sh", "-c", "curl --token", "hunter2"}, "sh -c curl --token …"},
		// A script with nothing to hide is shown as written.
		{wrapper(170, 1, `git commit -m 'fix the build'`).Args, "git commit -m 'fix the build'"},
	} {
		if got := maskCommand(c.args); got != c.want || strings.Contains(got, "hunter2") {
			t.Errorf("maskCommand(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestMaskCommandIsOneShortLine(t *testing.T) {
	long := maskCommand([]string{"node", strings.Repeat("x", 5000)})
	if n := utf8.RuneCountInString(long); n > 300 || !strings.HasSuffix(long, "…") {
		t.Errorf("a long command gives %d characters ending %q, want at most 300 ending in …", n, long[max(0, len(long)-8):])
	}
	odd := maskCommand([]string{"node", "a\nb\tc\x1bd", "  e  "})
	if strings.ContainsFunc(odd, unicode.IsControl) || strings.Contains(odd, "  ") || odd != "node a b cd e" {
		t.Errorf("maskCommand = %q, want %q", odd, "node a b cd e")
	}
}

func TestMaskCommandOddInput(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{""}, ""},
		{[]string{"", "--token"}, "--token"},
		{wrapper(170, 1, "").Args, ""},
		{wrapper(170, 1, `API_KEY="a b node x.js`).Args, ""}, // an unclosed quote runs to the end
		{wrapper(170, 1, `curl --token "a b`).Args, "curl --token …"},
		{[]string{"x", `a\`}, `x a\`},
		{[]string{"x", "'"}, "x '"},
		{[]string{"x", "a://@"}, "x a://…@"},
		{[]string{"x", `"a b`}, `x "a b`},
	} {
		if got := maskCommand(c.args); got != c.want {
			t.Errorf("maskCommand(%q) = %q, want %q", c.args, got, c.want)
		}
	}
}
