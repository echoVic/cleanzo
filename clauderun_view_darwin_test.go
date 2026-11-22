package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Tests of the Running tab's reading and summary, against made-up machines.

// viewNow is the fake clock's time: eight days after the fixtures started.
var viewNow = testEpoch.Add(8 * 24 * time.Hour)

// viewFake is a made-up machine the reading and the summary read through
// its source.
type viewFake struct {
	t         *testing.T
	snap      claudeSnapshot
	fail      bool // the snapshot cannot be read
	now       time.Time
	memory    map[int32]uint64
	cwds      map[int32]string
	paths     map[int32]string // identity: proc_pidpath
	names     map[int32]string // identity: proc_name
	owners    map[int32]int32  // responsible; a process missing here is its own
	ports     map[int32][]int
	portsNil  bool      // ports cannot be read
	portCalls [][]int32 // the pids of each ports read
	closed    map[string][2]string
	closedFor []string // the session of each closed read
	// transcripts is what the transcripts tell; nil for testTranscripts.
	transcripts    map[string]claudeTranscript
	transcriptsFor [][]string // the sessions of each transcripts read
}

// viewFakeOf is a made-up machine showing s at viewNow, with no ports read
// before it.
func viewFakeOf(t *testing.T, s claudeSnapshot) *viewFake {
	viewResetPorts(t)
	return &viewFake{t: t, snap: s, now: viewNow}
}

// viewResetPorts forgets the ports read so far, now and after the test.
func viewResetPorts(t *testing.T) {
	reset := func() {
		claudePorts.Lock()
		claudePorts.pids, claudePorts.at, claudePorts.ports = nil, time.Time{}, nil
		claudePorts.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func (f *viewFake) source() claudeSource {
	return claudeSource{
		snapshot: func() (claudeSnapshot, error) {
			if f.fail {
				return claudeSnapshot{}, errors.New("no process table")
			}
			return f.snap, nil
		},
		started: func(int32) (int64, bool) {
			f.t.Error("the reading checked a start")
			return 0, false
		},
		footprint: func(pid int32) uint64 { return f.memory[pid] },
		cwd:       func(pid int32) string { return f.cwds[pid] },
		identity:  func(pid int32) (string, string) { return f.paths[pid], f.names[pid] },
		responsible: func(pid int32) int32 {
			if owner, ok := f.owners[pid]; ok {
				return owner
			}
			return pid
		},
		system: func(path string) bool { return strings.HasPrefix(path, "/System/") },
		ports: func(_ context.Context, pids []int32) map[int32][]int {
			f.portCalls = append(f.portCalls, slices.Clone(pids))
			if f.portsNil {
				return nil
			}
			return f.ports
		},
		closed: func(session string) (string, string) {
			f.closedFor = append(f.closedFor, session)
			c := f.closed[session]
			return c[0], c[1]
		},
		transcripts: func(sessions []string) map[string]claudeTranscript {
			f.transcriptsFor = append(f.transcriptsFor, slices.Clone(sessions))
			if f.transcripts != nil {
				return f.transcripts
			}
			return testTranscripts(sessions)
		},
		signal: func(int32, syscall.Signal) error {
			f.t.Error("the reading sent a signal")
			return nil
		},
		now: func() time.Time { return f.now },
		sleep: func(context.Context, time.Duration) error {
			f.t.Error("the reading slept")
			return nil
		},
	}
}

// read is one reading of f; it fails the test on an error.
func (f *viewFake) read() ClaudeRunning {
	f.t.Helper()
	r, err := claudeRunningWith(context.Background(), f.source())
	if err != nil {
		f.t.Fatalf("reading: %v", err)
	}
	return r
}

// viewRecord is a valid record of p for session whose status became status
// at.
func viewRecord(p claudeProc, session, status string, at time.Time) claudeRecord {
	r := recordOf(p, session)
	r.Status, r.StatusUpdatedAt = status, at.UnixMilli()
	return r
}

// viewOnly is the only open session of r; it fails the test when r has
// another number of them.
func viewOnly(t *testing.T, r ClaudeRunning) ClaudeSession {
	t.Helper()
	if len(r.Sessions) != 1 {
		t.Fatalf("%d open sessions, want 1", len(r.Sessions))
	}
	return r.Sessions[0]
}

// viewPids is the pid of each session of r, in order.
func viewPids(r ClaudeRunning) []int32 {
	var pids []int32
	for _, s := range r.Sessions {
		pids = append(pids, s.Pid)
	}
	return pids
}

func TestClaudeRunningOrdersSessions(t *testing.T) {
	busy, unknown, week, hour, hourToo := ccProc(101, 1), ccProc(102, 1), ccProc(103, 1), ccProc(104, 1), ccProc(105, 1)
	records := map[int32]claudeRecord{
		101: viewRecord(busy, "S1", "busy", viewNow.Add(-7*24*time.Hour-time.Hour)),
		102: viewRecord(unknown, "S2", "waiting", viewNow.Add(-7*24*time.Hour-time.Hour)),
		103: viewRecord(week, "S3", "idle", viewNow.Add(-7*24*time.Hour)),
		104: viewRecord(hour, "S4", "idle", viewNow.Add(-time.Hour)),
		105: viewRecord(hourToo, "S5", "idle", viewNow.Add(-time.Hour)),
	}
	r := viewFakeOf(t, snap(records, hourToo, busy, hour, unknown, week)).read()
	if got, want := viewPids(r), []int32{103, 104, 105, 102, 101}; !slices.Equal(got, want) {
		t.Errorf("sessions %v, want %v: idle longest first, then unknown, then busy, each by pid", got, want)
	}
	status := map[int32]string{}
	since := map[int32]int64{}
	for _, s := range r.Sessions {
		status[s.Pid], since[s.Pid] = s.Status, s.Since
	}
	wantStatus := map[int32]string{101: "busy", 102: "", 103: "idle", 104: "idle", 105: "idle"}
	wantSince := map[int32]int64{
		101: viewNow.Add(-7*24*time.Hour - time.Hour).Unix(), 102: 0,
		103: viewNow.Add(-7 * 24 * time.Hour).Unix(), 104: viewNow.Add(-time.Hour).Unix(), 105: viewNow.Add(-time.Hour).Unix(),
	}
	if !reflect.DeepEqual(status, wantStatus) || !reflect.DeepEqual(since, wantSince) {
		t.Errorf("status %v since %v\nwant   %v since %v", status, since, wantStatus, wantSince)
	}
}

func TestClaudeRunningStatusNeedsItsTime(t *testing.T) {
	// A status without the time it was set says nothing about how long.
	cc := ccProc(100, 1)
	r := viewFakeOf(t, snap(map[int32]claudeRecord{100: recordOf(cc, "S1")}, cc)).read()
	if s := viewOnly(t, r); s.Status != "" || s.Since != 0 {
		t.Errorf("status %q since %d, want neither", s.Status, s.Since)
	}
}

func TestClaudeRunningListsCommandsAfterAMinute(t *testing.T) {
	cc := ccProc(100, 1)
	s := snap(map[int32]claudeRecord{100: recordOf(cc, "S1")},
		cc, marked(121, 100, 121, "S1"), marked(118, 100, 118, "S1"), marked(120, 100, 120, "S1"))
	f := viewFakeOf(t, s)
	f.now = time.UnixMicro(testStart(121)).Add(59 * time.Second) // 120 has run 60 seconds, 121 59
	var got []int32
	for _, p := range viewOnly(t, f.read()).Commands {
		got = append(got, p.Pid)
	}
	if want := []int32{118, 120}; !slices.Equal(got, want) {
		t.Errorf("commands %v, want %v: a minute or older, oldest first", got, want)
	}
	if want := [][]int32{{118, 120}}; !reflect.DeepEqual(f.portCalls, want) {
		t.Errorf("ports read for %v, want %v", f.portCalls, want)
	}
}

func TestClaudeRunningRowDetails(t *testing.T) {
	// A linked worktree whose folder and git record are named differently,
	// in a repository whose .git is a folder.
	repo := filepath.Join(t.TempDir(), "sample-app")
	record := filepath.Join(repo, ".git", "worktrees", "fix-login1")
	mustMkdir(t, record)
	tree := filepath.Join(repo, ".claude", "worktrees", "fix-login")
	writeText(t, filepath.Join(tree, ".git"), "gitdir: "+record+"\n")
	mustMkdir(t, filepath.Join(tree, "web"))

	cc := ccProc(100, 1)
	shell := wrapper(110, 100, "npm run dev -- --api-key=hunter2")
	dev := marked(111, 110, 110, "S1")
	dev.Args = []string{"/opt/homebrew/bin/node", "/p/node_modules/.bin/vite", "--api-key=hunter2"}
	tool := marked(120, 100, 120, "S1")
	f := viewFakeOf(t, snap(map[int32]claudeRecord{100: recordOf(cc, "S1")}, cc, shell, dev, tool))
	f.memory = map[int32]uint64{100: 300 << 20, 110: 2 << 20, 111: 200 << 20, 120: 50 << 20}
	f.cwds = map[int32]string{110: tree, 111: filepath.Join(tree, "web"), 120: repo}
	f.ports = map[int32][]int{110: {3000}, 111: {5173, 3000}}
	r := f.read()

	want := []ClaudeProc{
		{Pid: 111, Start: testStart(111), Command: maskCommand(dev.Args), Cwd: filepath.Join(tree, "web"), Worktree: "fix-login",
			Ports: []int{3000, 5173}, Memory: 202 << 20, Folded: []ClaudeTarget{{Pid: 110, Start: testStart(110)}}},
		{Pid: 120, Start: testStart(120), Command: maskCommand(tool.Args), Cwd: repo, Worktree: "",
			Ports: []int{}, Memory: 50 << 20, Folded: []ClaudeTarget{}},
	}
	session := viewOnly(t, r)
	if got := session.Commands; !reflect.DeepEqual(got, want) {
		t.Errorf("rows %+v\nwant %+v", got, want)
	}
	if got := session.Memory; got != 300<<20 {
		t.Errorf("session memory %d, want the Claude Code process's alone, %d", got, 300<<20)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("hunter2")) {
		t.Errorf("the reading shows a secret: %s", data)
	}
}

func TestClaudeRunningReadsPortsOncePerPidSet(t *testing.T) {
	cc := ccProc(100, 1)
	records := map[int32]claudeRecord{100: recordOf(cc, "S1")}
	one := snap(records, cc, marked(120, 100, 120, "S1"))
	two := snap(records, cc, marked(120, 100, 120, "S1"), marked(121, 100, 121, "S1"))
	f := viewFakeOf(t, one)
	f.ports = map[int32][]int{120: {5173}}
	calls := func(want ...[]int32) {
		t.Helper()
		if !reflect.DeepEqual(f.portCalls, want) {
			t.Errorf("ports read for %v, want %v", f.portCalls, want)
		}
	}
	if got := viewOnly(t, f.read()).Commands; len(got) != 1 || !slices.Equal(got[0].Ports, []int{5173}) {
		t.Errorf("rows %+v, want one with port 5173", got)
	}
	f.read()
	calls([]int32{120})
	f.snap = two
	f.read()
	f.read()
	calls([]int32{120}, []int32{120, 121})
	f.now = f.now.Add(claudePortsEvery)
	f.read()
	calls([]int32{120}, []int32{120, 121}, []int32{120, 121})
	f.now = f.now.Add(claudePortsEvery)
	f.portsNil = true
	r := f.read()
	calls([]int32{120}, []int32{120, 121}, []int32{120, 121}, []int32{120, 121})
	for _, p := range viewOnly(t, r).Commands {
		if p.Ports == nil || len(p.Ports) != 0 {
			t.Errorf("row %d: ports %v (nil %v) when they cannot be read, want none", p.Pid, p.Ports, p.Ports == nil)
		}
	}
	// A clock set back reads again too.
	f.now = viewNow
	f.read()
	if len(f.portCalls) != 5 {
		t.Errorf("ports read %d times after the clock went back, want 5", len(f.portCalls))
	}
}

func TestClaudeRunningClosedSession(t *testing.T) {
	left := marked(150, 1, 150, "S9")
	f := viewFakeOf(t, ccSnap(left))
	f.closed = map[string][2]string{"S9": {"sample-app", "Rename the widgets"}}
	f.memory = map[int32]uint64{150: 40 << 20}
	f.cwds = map[int32]string{150: "/Users/me/sample-app"}
	f.ports = map[int32][]int{150: {8000}}
	r := f.read()
	want := []ClaudeLeft{{ID: "S9", Project: "sample-app", Title: "Rename the widgets", Procs: []ClaudeProc{
		{Pid: 150, Start: testStart(150), Command: maskCommand(left.Args), Cwd: "/Users/me/sample-app",
			Ports: []int{8000}, Memory: 40 << 20, Folded: []ClaudeTarget{}},
	}}}
	if !reflect.DeepEqual(r.Left, want) {
		t.Errorf("left %+v\nwant %+v", r.Left, want)
	}
	if !slices.Equal(f.closedFor, []string{"S9"}) {
		t.Errorf("closed sessions read: %q, want only S9", f.closedFor)
	}
}

func TestClaudeRunningOrdersLeftByOldestProcess(t *testing.T) {
	// S9's oldest process is the shell its command runs in, folded into the
	// command's row; it started before S8's only process.
	shell := wrapper(140, 1, "npm run dev")
	shell.Out = "S9"
	r := viewFakeOf(t, ccSnap(shell, marked(145, 140, 140, "S9"), marked(142, 1, 142, "S8"))).read()
	var got []string
	for _, l := range r.Left {
		got = append(got, l.ID)
	}
	if want := []string{"S9", "S8"}; !slices.Equal(got, want) {
		t.Errorf("left %q, want %q", got, want)
	}
}

func TestClaudeRunningHoldsBackAnEarlierSessionID(t *testing.T) {
	// The Claude Code process 100 took its session id S1 after 150 started,
	// in the folder S9's transcript records: 150 may be what it started
	// before /clear. It is counted, not listed, and no title is read for it.
	cc := ccProc(100, 1)
	record := recordOf(cc, "S1")
	record.Cwd = "/Users/me/sample-app"
	f := viewFakeOf(t, snap(map[int32]claudeRecord{100: record}, cc, marked(150, 1, 150, "S9")))
	f.transcripts = map[string]claudeTranscript{
		"S9": {Cwd: "/Users/me/sample-app", Made: testStart(140)},
		"S1": {Cwd: "/Users/me/sample-app", Made: testStart(160)},
	}
	r := f.read()
	if len(r.Left) != 0 || r.Uncertain != 1 {
		t.Errorf("left %+v, uncertain %d; want nothing left and 1 uncertain", r.Left, r.Uncertain)
	}
	if want := [][]string{{"S1", "S9"}}; !reflect.DeepEqual(f.transcriptsFor, want) {
		t.Errorf("transcripts read for %q, want %q", f.transcriptsFor, want)
	}
	if len(f.closedFor) != 0 || len(f.portCalls) != 0 {
		t.Errorf("the reading read titles %q and ports %v for a process it does not list", f.closedFor, f.portCalls)
	}
	if sum := claudeSummaryWith(f.source()); sum.Left != 0 || len(sum.Rows) != 0 {
		t.Errorf("summary %+v, want nothing left", sum)
	}
	// Once 100 is seen to have had S1 before 150 started, S9 is closed.
	f.transcripts["S1"] = claudeTranscript{Cwd: "/Users/me/sample-app", Made: testStart(120)}
	if r := f.read(); len(r.Left) != 1 || r.Left[0].ID != "S9" || r.Uncertain != 0 {
		t.Errorf("left %+v, uncertain %d; want S9 left and 0 uncertain", r.Left, r.Uncertain)
	}
}

func TestClaudeRunningReadsTranscriptsOnlyForWhatWouldBeLeft(t *testing.T) {
	// Nothing here would be called left behind, so no transcript is looked
	// for, by the reading or by the summary.
	f := viewFakeOf(t, ccSnap(marked(110, 100, 110, "S1")))
	f.read()
	claudeSummaryWith(f.source())
	if len(f.transcriptsFor) != 0 {
		t.Errorf("transcripts read for %q, want none", f.transcriptsFor)
	}
}

func TestClaudeRunningSessionDetails(t *testing.T) {
	const terminal, editor = "/Applications/Sample Terminal.app", "/Applications/Sample Editor.app"
	cc := ccProc(100, 1)
	helper := marked(101, 100, 100, "S1")
	r100 := viewRecord(cc, "S1", "idle", viewNow.Add(-3*time.Hour))
	r100.Cwd, r100.Name, r100.Entrypoint = "/Users/me/sample-app", "Rename the widgets", "cli"
	noRecord := ccProc(200, 1)
	editorHelper := hidden(510, 1, 510)
	editorHelper.Path = editor + "/Contents/Frameworks/Sample Editor Helper.app/Contents/MacOS/Sample Editor Helper"
	inApp := ccProc(300, 1)
	r300 := recordOf(inApp, "S3")
	r300.Entrypoint = "sample-app"
	cleanzo := plain(900, 300, 900) // Cleanzo runs under session 300
	cleanzo.Args = []string{"cleanzo"}
	s := snap(map[int32]claudeRecord{100: r100, 300: r300}, cc, helper, noRecord, editorHelper, inApp, cleanzo)
	s.Apps = []workspaceApp{
		{Pid: 500, Path: terminal, Name: "Sample Terminal", Regular: true},
		{Pid: 600, Path: editor, Name: "Sample Editor", Regular: true},
	}
	f := viewFakeOf(t, s)
	f.owners = map[int32]int32{100: 500, 200: 510, 300: -1}
	f.memory = map[int32]uint64{100: 300 << 20, 101: 20 << 20, 200: 100 << 20, 300: 80 << 20}
	f.cwds = map[int32]string{100: "/Users/me/elsewhere", 200: "/Users/me/other-app", 300: "/Users/me/third-app"}
	r := f.read()
	want := []ClaudeSession{
		{ID: "S1", Pid: 100, Start: testStart(100), Project: "sample-app", Cwd: "/Users/me/sample-app", Title: "Rename the widgets",
			Status: "idle", Since: viewNow.Add(-3 * time.Hour).Unix(), Host: "Sample Terminal", Entry: "cli", Memory: 320 << 20, Commands: []ClaudeProc{}},
		{Pid: 200, Start: testStart(200), Project: "other-app", Cwd: "/Users/me/other-app", Host: "Sample Editor", Memory: 100 << 20, Commands: []ClaudeProc{}},
		{ID: "S3", Pid: 300, Start: testStart(300), Project: "third-app", Cwd: "/Users/me/third-app", Entry: "sample-app",
			Memory: 80 << 20, Own: true, Commands: []ClaudeProc{}},
	}
	if !reflect.DeepEqual(r.Sessions, want) {
		t.Errorf("sessions %+v\nwant     %+v", r.Sessions, want)
	}
	if !r.NoRecords || r.At != viewNow.Unix() {
		t.Errorf("noRecords %v at %d, want true at %d", r.NoRecords, r.At, viewNow.Unix())
	}
}

func TestClaudeRunningClampsFutureStatusTime(t *testing.T) {
	cc := ccProc(100, 1)
	r := viewFakeOf(t, snap(map[int32]claudeRecord{100: viewRecord(cc, "S1", "idle", viewNow.Add(time.Hour))}, cc)).read()
	if s := viewOnly(t, r); s.Status != "idle" || s.Since != viewNow.Unix() {
		t.Errorf("status %q since %d, want idle since now, %d", s.Status, s.Since, viewNow.Unix())
	}
}

// A status set before its process started tells no time: a record that
// counted in another unit, say seconds, would read as idle for decades.
func TestClaudeRunningStatusBeforeTheProcessStartedIsUnknown(t *testing.T) {
	cc := ccProc(100, 1)
	record := viewRecord(cc, "S1", "idle", viewNow)
	record.StatusUpdatedAt = viewNow.Unix() // seconds where milliseconds belong
	f := viewFakeOf(t, snap(map[int32]claudeRecord{100: record}, cc))
	if s := viewOnly(t, f.read()); s.Status != "" || s.Since != 0 {
		t.Errorf("status %q since %d, want unknown", s.Status, s.Since)
	}
	if sum := claudeSummaryWith(f.source()); sum.Idle != 0 {
		t.Errorf("the summary counts %d idle sessions, want 0", sum.Idle)
	}
}

func TestClaudeRunningSnapshotFailure(t *testing.T) {
	f := viewFakeOf(t, claudeSnapshot{})
	f.fail = true
	_, err := claudeRunningWith(context.Background(), f.source())
	if ce, ok := err.(codeError); !ok || ce.Code != "unreadable" {
		t.Errorf("error %v, want unreadable", err)
	}
	if len(f.portCalls) != 0 || len(f.closedFor) != 0 {
		t.Error("a reading without a snapshot read more")
	}
}

// viewFullSnap has a long-idle session with a helper and a command, and a
// closed session that left a browser with its helper.
func viewFullSnap() claudeSnapshot {
	const app = "/Applications/Sample Browser.app" // not on the Dock
	cc := ccProc(100, 1)
	browser := marked(180, 1, 180, "S9")
	browser.Path = app + "/Contents/MacOS/Sample Browser"
	helper := marked(181, 180, 180, "S9")
	helper.Path = app + "/Contents/Frameworks/Sample Browser Helper.app/Contents/MacOS/Sample Browser Helper"
	return snap(map[int32]claudeRecord{100: viewRecord(cc, "S1", "idle", viewNow.Add(-3*time.Hour))},
		cc, marked(101, 100, 100, "S1"), marked(120, 100, 120, "S1"), browser, helper)
}

func TestClaudeRunningJSONHasNoNull(t *testing.T) {
	full := viewFakeOf(t, viewFullSnap())
	full.portsNil = true
	unreadable := viewFakeOf(t, claudeSnapshot{})
	unreadable.fail = true
	readings := map[string]any{
		"empty reading":      viewFakeOf(t, claudeSnapshot{}).read(),
		"full reading":       full.read(),
		"empty summary":      claudeSummaryWith(viewFakeOf(t, claudeSnapshot{}).source()),
		"full summary":       claudeSummaryWith(full.source()),
		"unreadable summary": claudeSummaryWith(unreadable.source()),
	}
	for name, v := range readings {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("null")) {
			t.Errorf("%s: %s", name, data)
		}
	}
	if r := readings["full reading"].(ClaudeRunning); len(r.Sessions) != 1 || len(r.Sessions[0].Commands) != 1 || len(r.Left) != 1 {
		t.Errorf("the full reading is not full: %+v", r)
	}
	if s := readings["full summary"].(ClaudeRunSummary); s.Idle != 1 || s.Left != 1 || len(s.Rows) == 0 {
		t.Errorf("the full summary is not full: %+v", s)
	}
}

func TestClaudeSummary(t *testing.T) {
	const cli = "/Users/me/.local/share/claude/versions/2.1.321"
	const app = "/Applications/Sample Browser.app" // not on the Dock
	first, second, third := ccProc(100, 1), ccProc(101, 1), ccProc(102, 1)
	records := map[int32]claudeRecord{
		100: viewRecord(first, "S1", "idle", viewNow.Add(-3*time.Hour)),
		101: viewRecord(second, "S2", "idle", viewNow.Add(-3*time.Hour)),
		102: viewRecord(third, "S3", "idle", viewNow.Add(-time.Hour)),
	}
	browser := marked(180, 1, 180, "S9")
	browser.Path = app + "/Contents/MacOS/Sample Browser"
	helper := marked(181, 180, 180, "S9")
	helper.Path = app + "/Contents/Frameworks/Sample Browser Helper.app/Contents/MacOS/Sample Browser Helper"
	f := viewFakeOf(t, snap(records, first, second, third, marked(150, 1, 150, "S9"), marked(151, 1, 151, "S9"), browser, helper))
	f.paths = map[int32]string{100: cli, 101: cli, 102: cli, 150: "/opt/homebrew/bin/node", 151: "/opt/homebrew/bin/node", 180: browser.Path, 181: helper.Path}
	f.names = map[int32]string{100: "2.1.321", 101: "2.1.321", 102: "2.1.321", 150: "node", 151: "node", 180: "Sample Browser", 181: "Sample Browser He"}
	got := claudeSummaryWith(f.source())
	want := ClaudeRunSummary{Idle: 2, Left: 3, Rows: []ClaudeRowTag{
		{Key: "app:/applications/sample browser.app", Left: 2},
		{Key: "cli:claude", Idle: 2},
		{Key: "cli:node", Left: 2},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("summary %+v\nwant    %+v", got, want)
	}
	if len(f.portCalls) != 0 || len(f.closedFor) != 0 {
		t.Errorf("the summary read ports %v and titles %q", f.portCalls, f.closedFor)
	}
}

func TestClaudeSummaryPlacesSystemProcessesByTheirApp(t *testing.T) {
	// A system process that works for an app counts in the app's row, as on
	// the process page, though the app itself is not left behind.
	const app = "/Applications/Sample Browser.app"
	service := marked(160, 1, 160, "S9")
	service.Path = "/System/Library/Frameworks/WebKit.framework/Versions/A/XPCServices/com.apple.WebKit.WebContent.xpc/Contents/MacOS/com.apple.WebKit.WebContent"
	f := viewFakeOf(t, ccSnap(service))
	f.owners = map[int32]int32{160: 700}
	f.paths = map[int32]string{160: service.Path, 700: app + "/Contents/MacOS/Sample Browser"}
	f.names = map[int32]string{160: "com.apple.WebKi", 700: "Sample Browser"}
	got := claudeSummaryWith(f.source())
	want := ClaudeRunSummary{Left: 1, Rows: []ClaudeRowTag{{Key: "app:/applications/sample browser.app", Left: 1}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("summary %+v, want %+v", got, want)
	}
}

func TestClaudeSummarySkipsWithoutClaudeCode(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	prev := claudeSourceFn
	t.Cleanup(func() { claudeSourceFn = prev })
	claudeSourceFn = func() claudeSource {
		t.Error("the summary reads processes while Claude Code is not installed")
		return viewFakeOf(t, claudeSnapshot{}).source()
	}
	if got := (Cleaner{}).ClaudeRunSummary(); got.Idle != 0 || got.Left != 0 || got.Rows == nil || len(got.Rows) != 0 {
		t.Errorf("summary %+v (nil rows %v), want zero counts and no rows", got, got.Rows == nil)
	}
	// Once Claude Code keeps its folder here, the summary reads.
	mustMkdir(t, filepath.Join(home, ".claude"))
	read := false
	claudeSourceFn = func() claudeSource {
		read = true
		return viewFakeOf(t, claudeSnapshot{}).source()
	}
	(Cleaner{}).ClaudeRunSummary()
	if !read {
		t.Error("the summary does not read with Claude Code installed")
	}
}
