package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// Tests of the Running tab's native reads. The ones that read this Mac look
// only at the test process and the children it starts, though
// TestClaudeSnapshotSeesThisProcess lists every process to find them; the
// rest read a temporary home and stand-ins. TestClaudeRunningOnThisMac and
// TestClaudeRunningBudget read this Mac's sessions, and only when asked.

func TestNativeCwdAndFdPath(t *testing.T) {
	self := int32(os.Getpid())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := nativeCwd(self), resolvePath(wd); got != want {
		t.Errorf("nativeCwd(self) = %q, want %q", got, want)
	}

	f, err := os.CreateTemp(t.TempDir(), "open-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if got, want := nativeFdPath(self, int32(f.Fd())), resolvePath(f.Name()); got != want {
		t.Errorf("nativeFdPath(self, file) = %q, want %q", got, want)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if got := nativeFdPath(self, int32(r.Fd())); got != "" {
		t.Errorf("nativeFdPath(self, pipe) = %q, want empty", got)
	}

	if os.Getuid() != 0 {
		// launchd is root's: libproc tells this account nothing about it.
		if got := nativeCwd(1); got != "" {
			t.Errorf("nativeCwd(1) = %q, want empty", got)
		}
		if got := nativeFdPath(1, 1); got != "" {
			t.Errorf("nativeFdPath(1, 1) = %q, want empty", got)
		}
	}
	if got := nativeCwd(-1); got != "" {
		t.Errorf("nativeCwd(-1) = %q, want empty", got)
	}
}

func TestNativeFootprintAndProcName(t *testing.T) {
	self := int32(os.Getpid())
	if got := nativeFootprint(self); got == 0 {
		t.Error("nativeFootprint(self) = 0, want this process's footprint")
	}
	if got, want := nativeProcName(self), filepath.Base(os.Args[0]); got != want {
		t.Errorf("nativeProcName(self) = %q, want the test binary's name %q", got, want)
	}
	if nativeFootprint(-1) != 0 || nativeProcName(-1) != "" {
		t.Error("an invalid pid gave a footprint or a name")
	}
}

// viewProcargs is a kern.procargs2 buffer as the kernel lays it out: argc,
// the executable's path with its NUL padded to a multiple of 8 bytes, then
// args and env, each NUL-terminated; after is appended as it is.
func viewProcargs(argc int32, args, env []string, after string) []byte {
	return viewProcargsOf("/usr/local/bin/node", argc, args, env, after)
}

// viewProcargsOf is viewProcargs for the executable at path.
func viewProcargsOf(path string, argc int32, args, env []string, after string) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(argc))
	b = append(b, path...)
	b = append(b, make([]byte, 8-len(path)%8)...)
	for _, s := range append(slices.Clone(args), env...) {
		b = append(b, s...)
		b = append(b, 0)
	}
	return append(b, after...)
}

func TestParseProcargs2(t *testing.T) {
	args := []string{"node", "server.js"}
	env := []string{"PATH=/bin", "SECRET_TOKEN=hunter2", "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=S1", "CLAUDE_PID=4242"}
	gotArgs, gotEnv := parseProcargs2(viewProcargs(2, args, env, "\x00\x00\x00ptr_munge=\x00"))
	if !slices.Equal(gotArgs, args) {
		t.Errorf("args = %q, want %q", gotArgs, args)
	}
	if want := (claudeEnv{true, true, "S1", 4242}); gotEnv != want {
		t.Errorf("env = %+v, want %+v", gotEnv, want)
	}
	for _, kept := range []string{fmt.Sprintf("%+v", gotArgs), fmt.Sprintf("%+v", gotEnv)} {
		for _, secret := range []string{"hunter2", "SECRET_TOKEN", "PATH"} {
			if strings.Contains(kept, secret) {
				t.Errorf("%q is kept in %q", secret, kept)
			}
		}
	}

	for _, c := range []struct {
		name string
		buf  []byte
		args []string
		env  claudeEnv
	}{
		// A process that shows no environment: the buffer ends after the
		// arguments, or the environment starts with the empty string.
		{"no environment", viewProcargs(2, args, nil, ""), args, claudeEnv{}},
		{"environment ends at once", viewProcargs(2, args, nil, "\x00CLAUDECODE=1\x00"), args, claudeEnv{}},
		{"unmarked", viewProcargs(2, args, []string{"HOME=/Users/me"}, "\x00"), args, claudeEnv{Seen: true}},
		{"marker must be 1", viewProcargs(2, args, []string{"CLAUDECODE=true", "CLAUDE_PID=42x"}, "\x00"), args, claudeEnv{Seen: true}},
		{"the first of a name counts", viewProcargs(2, args, []string{"CLAUDE_CODE_SESSION_ID=S1", "CLAUDE_CODE_SESSION_ID=S2", "CLAUDECODE=1", "CLAUDECODE=0"}, "\x00"), args, claudeEnv{Seen: true, Marked: true, Session: "S1"}},
		{"an empty argument", viewProcargs(3, []string{"node", "", "x.js"}, nil, ""), []string{"node", "", "x.js"}, claudeEnv{}},
		// An empty first argument is an argument: the environment stays where it is.
		{"an empty first argument", viewProcargs(2, []string{"", "server.js"}, env, "\x00"), []string{"", "server.js"}, claudeEnv{true, true, "S1", 4242}},
		{"every argument empty", viewProcargs(2, []string{"", ""}, env, "\x00"), []string{"", ""}, claudeEnv{true, true, "S1", 4242}},
		{"a path one byte short of 8", viewProcargsOf("/"+strings.Repeat("p", 30), 2, []string{"", "x"}, env, "\x00"), []string{"", "x"}, claudeEnv{true, true, "S1", 4242}},
		{"a path of 8 bytes' multiple", viewProcargsOf("/"+strings.Repeat("p", 31), 2, []string{"", "x"}, env, "\x00"), []string{"", "x"}, claudeEnv{true, true, "S1", 4242}},
		{"a byte in the padding", append(binary.LittleEndian.AppendUint32(nil, 1), "/usr/local/bin/node\x00x\x00\x00\x00node\x00"...), nil, claudeEnv{}},
		{"the padding is cut short", append(binary.LittleEndian.AppendUint32(nil, 1), "/usr/local/bin/node\x00\x00"...), nil, claudeEnv{}},
		{"nil", nil, nil, claudeEnv{}},
		{"no room for argc", []byte{2, 0, 0}, nil, claudeEnv{}},
		{"negative argc", viewProcargs(-1, args, env, "\x00"), nil, claudeEnv{}},
		{"argc too large", viewProcargs(1<<30, args, env, "\x00"), nil, claudeEnv{}},
		{"fewer arguments than argc", viewProcargs(3, args, nil, ""), nil, claudeEnv{}},
		{"no executable path", binary.LittleEndian.AppendUint32(nil, 1), nil, claudeEnv{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			gotArgs, gotEnv := parseProcargs2(c.buf)
			if !slices.Equal(gotArgs, c.args) || (gotArgs == nil) != (c.args == nil) || gotEnv != c.env {
				t.Errorf("parseProcargs2 = %q (nil %v), %+v; want %q (nil %v), %+v", gotArgs, gotArgs == nil, gotEnv, c.args, c.args == nil, c.env)
			}
			// Whatever the layout, no other variable leaves the buffer.
			for _, secret := range []string{"hunter2", "SECRET_TOKEN", "PATH"} {
				if kept := fmt.Sprintf("%+v %+v", gotArgs, gotEnv); strings.Contains(kept, secret) {
					t.Errorf("%q is kept in %q", secret, kept)
				}
			}
		})
	}
}

func TestClaudeOutputSession(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	for _, c := range []struct{ path, want string }{
		{"/private/tmp/claude-501/-Users-me-app/" + id + "/tasks/b1.output", id},
		{"/claude-0/p/" + id + "/tasks/x", id},
		{"/private/tmp/claude-501/" + id + "/tasks/b1.output", ""}, // no project
		{"/private/tmp/claude-501/-Users-me-app/" + id + "/tasks/", ""},
		{"/private/tmp/claude-501/-Users-me-app/" + id + "/logs/b1.output", ""},
		{"/private/tmp/claude-501/-Users-me-app/" + id + "/tasks/sub/b1.output", ""},
		{"/private/tmp/claude-me/-Users-me-app/" + id + "/tasks/b1.output", ""},
		{"/private/tmp/claude-/-Users-me-app/" + id + "/tasks/b1.output", ""},
		{"claude-501/-Users-me-app/" + id + "/tasks/b1.output", ""},
		{"/private/tmp/claude-501/-Users-me-app/11111111-2222-3333-4444-55555555555A/tasks/b1.output", ""},
		{"/private/tmp/claude-501/-Users-me-app/11111111-2222-3333-4444-5555555555555/tasks/b1.output", ""},
		{"/private/tmp/claude-501/-Users-me-app/11111111_2222-3333-4444-555555555555/tasks/b1.output", ""},
		{"/private/tmp/claude-501/-Users-me-app/S1/tasks/b1.output", ""},
		{"/dev/ttys003", ""},
		{"", ""},
	} {
		if got := claudeOutputSession(c.path); got != c.want {
			t.Errorf("claudeOutputSession(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestReadClaudeRecords(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	dir := filepath.Join(home, ".claude", "sessions")
	writeText(t, filepath.Join(dir, "100.json"), `{"pid":100,"sessionId":"S1","cwd":"/Users/me/sample-app","name":"Rename the widgets",`+
		`"status":"idle","statusUpdatedAt":1700000000000,"procStart":"Sat Nov 14 09:01:40 2026","entrypoint":"cli","kind":"interactive"}`)
	writeText(t, filepath.Join(dir, "abc.json"), `{"sessionId":"S3"}`)
	writeText(t, filepath.Join(dir, "101.json"), `{"sessionId":"S2","cwd":"/Users/`)
	writeText(t, filepath.Join(dir, "102.json"), `{"sessionId":"S4","statusUpdatedAt":"1700000000000","name":42,"status":["idle"],"cwd":null}`)
	writeText(t, filepath.Join(dir, "103.json"), `{"sessionId":"S5","name":"`+strings.Repeat("x", 70<<10)+`"}`)
	writeText(t, filepath.Join(dir, "104.json"), `{"sessionId":"S6","statusUpdatedAt":"soon"}`)
	writeText(t, filepath.Join(dir, "0.json"), `{"sessionId":"S7"}`)
	writeText(t, filepath.Join(dir, "105.txt"), `{"sessionId":"S8"}`)
	mustMkdir(t, filepath.Join(dir, "106.json"))

	got := readClaudeRecords(dir)
	want := map[int32]claudeRecord{
		100: {SessionID: "S1", Cwd: "/Users/me/sample-app", Name: "Rename the widgets", Status: "idle",
			ProcStart: "Sat Nov 14 09:01:40 2026", Entrypoint: "cli", StatusUpdatedAt: 1700000000000},
		102: {SessionID: "S4", StatusUpdatedAt: 1700000000000},
		104: {SessionID: "S6"},
	}
	if !maps.Equal(got, want) {
		t.Errorf("records = %+v\n   want %+v", got, want)
	}
	if got := readClaudeRecords(filepath.Join(home, "missing")); len(got) != 0 {
		t.Errorf("a missing folder gave %+v", got)
	}
}

// viewSampleApps gives the snapshot one made-up running app, which is no
// process of this Mac: NSWorkspace needs the main thread, which a test
// binary never serves.
func viewSampleApps(t *testing.T) {
	viewApps(t, workspaceApp{Pid: -1, Name: "Sample App"})
}

// viewApps makes apps the running apps the snapshot reads.
func viewApps(t *testing.T, apps ...workspaceApp) {
	prev := claudeRunningApps
	claudeRunningApps = func() []workspaceApp { return apps }
	t.Cleanup(func() { claudeRunningApps = prev })
}

// TestClaudeSnapshotHelper is the child TestClaudeSnapshotSeesThisProcess
// starts: it sleeps until it is killed. Run as a test it returns at once.
func TestClaudeSnapshotHelper(t *testing.T) {
	if os.Getenv("CLEANZO_CLAUDE_SNAPSHOT_HELPER") == "" {
		return
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestClaudeSnapshotSeesThisProcess(t *testing.T) {
	setHome(t, t.TempDir())
	viewSampleApps(t)
	// The child gets only these variables, none of this process's.
	child := exec.Command(os.Args[0], "-test.run=^TestClaudeSnapshotHelper$")
	child.Env = []string{"CLEANZO_CLAUDE_SNAPSHOT_HELPER=1", "CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=S-test"}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	// A process may start with an empty first argument: its arguments are
	// still its own, and nothing of its environment joins them.
	bare := &exec.Cmd{
		Path: os.Args[0],
		Args: []string{"", "-test.run=^TestClaudeSnapshotHelper$"},
		Env:  []string{"FAKE_FIRST=made-up-value", "CLEANZO_CLAUDE_SNAPSHOT_HELPER=1"},
	}
	if err := bare.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = bare.Process.Kill()
		_ = bare.Wait()
	})
	// A child that exited and is not reaped yet is a zombie.
	zombie := exec.Command("/usr/bin/true")
	if err := zombie.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = zombie.Wait() })
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, running := realClaudeSource().started(int32(zombie.Process.Pid)); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child that exits at once is still running")
		}
	}

	s, err := realClaudeSource().snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if s.Self != int32(os.Getpid()) || s.UID != uint32(os.Getuid()) {
		t.Errorf("snapshot taken by %d as %d, want %d as %d", s.Self, s.UID, os.Getpid(), os.Getuid())
	}
	byPid := map[int32]claudeProc{}
	for _, p := range s.Procs {
		byPid[p.Pid] = p
	}
	// Only what the test itself controls is printed: never the test
	// process's environment or another process's details.
	self, ok := byPid[int32(os.Getpid())]
	if !ok {
		t.Fatal("the snapshot misses the test process")
	}
	isTestBinary := len(self.Args) > 0 && self.Args[0] == os.Args[0]
	if self.Ppid != int32(os.Getppid()) || !isTestBinary || self.Path == "" || self.UID != uint32(os.Getuid()) || self.Start <= 0 {
		t.Errorf("test process: ppid %d (want %d), argv[0] is the test binary %v, path read %v, uid %d, start %d",
			self.Ppid, os.Getppid(), isTestBinary, self.Path != "", self.UID, self.Start)
	}
	kid, ok := byPid[int32(child.Process.Pid)]
	if !ok {
		t.Fatal("the snapshot misses the child")
	}
	if want := (claudeEnv{true, true, "S-test", 0}); kid.Ppid != int32(os.Getpid()) || kid.Env != want || len(kid.Args) == 0 || kid.Args[0] != os.Args[0] {
		t.Errorf("child: ppid %d (want %d), env %+v (want %+v), args %q", kid.Ppid, os.Getpid(), kid.Env, want, kid.Args)
	}
	odd, ok := byPid[int32(bare.Process.Pid)]
	if !ok {
		t.Fatal("the snapshot misses the child with an empty first argument")
	}
	if want := []string{"", "-test.run=^TestClaudeSnapshotHelper$"}; !slices.Equal(odd.Args, want) || odd.Env != (claudeEnv{Seen: true}) {
		t.Errorf("child with an empty first argument: args %q (want %q), env %+v", odd.Args, want, odd.Env)
	}
	if _, listed := byPid[int32(zombie.Process.Pid)]; listed {
		t.Error("the snapshot lists a zombie")
	}
}

// Without the running apps nothing tells an app on the Dock from a command
// a session started, so the snapshot is unreadable and nothing is listed.
func TestClaudeSnapshotNeedsTheRunningApps(t *testing.T) {
	setHome(t, t.TempDir())
	viewApps(t)
	viewResetPorts(t)
	if _, err := realClaudeSource().snapshot(); err == nil {
		t.Error("a snapshot without the running apps gave no error")
	}
	_, err := claudeRunningWith(context.Background(), realClaudeSource())
	if ce, ok := err.(codeError); !ok || ce.Code != "unreadable" {
		t.Errorf("the reading gave %v, want unreadable", err)
	}
	if sum := claudeSummaryWith(realClaudeSource()); sum.Idle != 0 || sum.Left != 0 || sum.Rows == nil || len(sum.Rows) != 0 {
		t.Errorf("the summary is %+v, want it empty", sum)
	}
}

func TestParseLsofListen(t *testing.T) {
	got := parseLsofListen([]byte("p123\nn*:5173\nn127.0.0.1:3000\nn*:5173\np456\nn[::1]:8000\n"))
	if want := map[int32][]int{123: {3000, 5173}, 456: {8000}}; !maps.EqualFunc(got, want, slices.Equal) {
		t.Errorf("ports = %v, want %v", got, want)
	}
	if got := parseLsofListen(nil); got == nil || len(got) != 0 {
		t.Errorf("no output gave %v (nil %v), want an empty map", got, got == nil)
	}
	// As lsof prints it: a descriptor line before each name. A name that is
	// not a port, and the names of a pid that cannot be read, are skipped.
	got = parseLsofListen([]byte("p77\nf3\nn*:8080\nf4\nn*:http\nnx\nq\npabc\nf5\nn*:9\n"))
	if want := map[int32][]int{77: {8080}}; !maps.EqualFunc(got, want, slices.Equal) {
		t.Errorf("ports = %v, want %v", got, want)
	}
}

// TestClaudeLsofHelper stands in for lsof in TestClaudeListenPorts. Run as
// a test it returns at once.
func TestClaudeLsofHelper(t *testing.T) {
	mode := os.Getenv("CLEANZO_CLAUDE_LSOF_HELPER")
	if mode == "" {
		return
	}
	if os.Getenv("LC_ALL") != "en_US.UTF-8" {
		os.Exit(3)
	}
	switch mode {
	case "ports":
		fmt.Print("p120\nf3\nn*:5173\nf4\nn[::1]:5173\np121\nf4\nn127.0.0.1:3000\n")
	case "one gone": // lsof exits with 1 when one of the pids is gone
		fmt.Print("p120\nf3\nn*:5173\n")
		os.Exit(1)
	case "none": // and when none listens
		os.Exit(1)
	case "broken":
		fmt.Print("p120\nf3\nn*:5173\n")
		os.Exit(2)
	case "slow":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

// viewFakeLsof points the port reads at TestClaudeLsofHelper in mode and
// returns where the command it was given is written.
func viewFakeLsof(t *testing.T, mode string) *[]string {
	t.Helper()
	prev := claudeLsofSpawn
	t.Cleanup(func() { claudeLsofSpawn = prev })
	t.Setenv("CLEANZO_CLAUDE_LSOF_HELPER", mode)
	var command []string
	claudeLsofSpawn = func(ctx context.Context, path string, argv, env []string) ([]byte, int32, error) {
		command = append([]string{path}, argv...)
		return spawnOutput(ctx, os.Args[0], []string{os.Args[0], "-test.run=^TestClaudeLsofHelper$"}, env)
	}
	return &command
}

func TestClaudeListenPorts(t *testing.T) {
	for _, c := range []struct {
		mode string
		want map[int32][]int
	}{
		{"ports", map[int32][]int{120: {5173}, 121: {3000}}},
		{"one gone", map[int32][]int{120: {5173}}},
		{"none", map[int32][]int{}},
		{"broken", nil},
		{"slow", nil},
	} {
		t.Run(c.mode, func(t *testing.T) {
			command := viewFakeLsof(t, c.mode)
			if c.mode == "slow" {
				prev := claudeLsofWait
				claudeLsofWait = 200 * time.Millisecond
				t.Cleanup(func() { claudeLsofWait = prev })
			}
			got := claudeListenPorts(context.Background(), []int32{120, 121})
			if (got == nil) != (c.want == nil) || !maps.EqualFunc(got, c.want, slices.Equal) {
				t.Errorf("ports = %v (nil %v), want %v (nil %v)", got, got == nil, c.want, c.want == nil)
			}
			if want := "/usr/sbin/lsof /usr/sbin/lsof -nP -iTCP -sTCP:LISTEN -a -p 120,121 -Fpn"; strings.Join(*command, " ") != want {
				t.Errorf("ran %q, want %q", strings.Join(*command, " "), want)
			}
		})
	}
	command := viewFakeLsof(t, "ports")
	if got := claudeListenPorts(context.Background(), nil); got == nil || len(got) != 0 || *command != nil {
		t.Errorf("no pids gave %v and ran %q; want an empty map and no lsof", got, *command)
	}
}

func TestClosedSessionProjectAndTitle(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	projects := filepath.Join(home, ".claude", "projects")
	const (
		titled  = "11111111-2222-3333-4444-555555555555"
		early   = "22222222-3333-4444-5555-666666666666"
		missing = "33333333-4444-5555-6666-777777777777"
	)
	line := func(fields string) string { return "{" + fields + "}\n" }
	writeText(t, filepath.Join(projects, "-Users-me-sample-app", titled+".jsonl"),
		line(`"type":"user","cwd":"/Users/me/sample-app","sessionId":"`+titled+`"`)+
			line(`"type":"ai-title","aiTitle":"First title"`)+
			line(`"type":"assistant","message":{"content":"an ai-title is"}`)+
			line(`"type":"ai-title","aiTitle":"Second title"`)+
			line(`"type": "ai-title", "aiTitle": "Rename the widgets"`)+
			line(`"type":"user","message":{"content":"thanks"}`))
	// The title is more than 2 MB before the end of this one.
	var long strings.Builder
	long.WriteString(line(`"type":"user","cwd":"/Users/me/other-app"`))
	long.WriteString(line(`"type":"ai-title","aiTitle":"Too early"`))
	filler := line(`"type":"assistant","message":{"content":"` + strings.Repeat("x", 1000) + `"}`)
	for long.Len() < 2<<20+len(filler) {
		long.WriteString(filler)
	}
	writeText(t, filepath.Join(projects, "-Users-me-other-app", early+".jsonl"), long.String())
	// What the session ids "../x" and "../../x" would reach from a project folder.
	for _, outside := range []string{filepath.Join(projects, "x.jsonl"), filepath.Join(home, ".claude", "x.jsonl")} {
		writeText(t, outside, line(`"type":"user","cwd":"/Users/me/elsewhere"`)+line(`"type":"ai-title","aiTitle":"Elsewhere"`))
	}

	for _, c := range []struct{ session, project, title string }{
		{titled, "sample-app", "Rename the widgets"},
		{early, "other-app", ""},
		{missing, "", ""},
		{"../x", "", ""},
		{"../../x", "", ""},
		{"S1", "", ""},
	} {
		if project, title := claudeClosedSession(c.session); project != c.project || title != c.title {
			t.Errorf("claudeClosedSession(%q) = %q, %q; want %q, %q", c.session, project, title, c.project, c.title)
		}
	}
}

func TestRealClaudeSourceReadsThisProcess(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	src := realClaudeSource()
	self := int32(os.Getpid())
	if src.footprint(self) == 0 {
		t.Error("footprint(self) = 0")
	}
	if got := src.cwd(self); got == "" || got != nativeCwd(self) {
		t.Errorf("cwd(self) = %q, want %q", got, nativeCwd(self))
	}
	if path, name := src.identity(self); path == "" || path != nativePidPath(self) || name != nativeProcName(self) {
		t.Errorf("identity(self) = %q, %q; want %q, %q", path, name, nativePidPath(self), nativeProcName(self))
	}
	viewFakeLsof(t, "ports")
	if got, want := src.ports(context.Background(), []int32{120, 121}), (map[int32][]int{120: {5173}, 121: {3000}}); !maps.EqualFunc(got, want, slices.Equal) {
		t.Errorf("ports = %v, want %v", got, want)
	}
	const id = "11111111-2222-3333-4444-555555555555"
	writeText(t, filepath.Join(home, ".claude", "projects", "-Users-me-sample-app", id+".jsonl"),
		`{"type":"user","cwd":"/Users/me/sample-app"}`+"\n"+`{"type":"ai-title","aiTitle":"Rename the widgets"}`+"\n")
	if project, title := src.closed(id); project != "sample-app" || title != "Rename the widgets" {
		t.Errorf("closed = %q, %q; want the transcript's project and title", project, title)
	}
}

func TestClaudeWorkingFolders(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	uid := uint32(os.Getuid())
	proc := func(pid int32, uid uint32) claudeProc {
		return claudeProc{Pid: pid, Ppid: 1, Pgid: pid, UID: uid, Start: testStart(pid)}
	}
	procs := []claudeProc{proc(100, uid), proc(101, uid+1), proc(102, uid), proc(103, uid)}
	record := func(pid int32, start int64, cwd string) {
		data, err := json.Marshal(map[string]any{"sessionId": "S1", "cwd": cwd, "procStart": time.UnixMicro(start).UTC().Format("Mon Jan _2 15:04:05 2006")})
		if err != nil {
			t.Fatal(err)
		}
		writeText(t, filepath.Join(home, ".claude", "sessions", fmt.Sprintf("%d.json", pid)), string(data))
	}
	record(100, testStart(100), "/Users/me/sample-app")
	record(101, testStart(101), "/Users/me/their-app")                      // another account's process
	record(102, testStart(102)+time.Second.Microseconds(), "/Users/me/old") // pid 102 is another process now
	record(104, testStart(104), "/Users/me/gone-app")                       // no process 104
	cwds := map[int32]string{100: "/Users/me/sample-app/web", 101: "/Users/me/their-app", 102: "/Users/me/old"}
	cwd := func(pid int32) string { return cwds[pid] }

	got := claudeWorkingFoldersWith(func() ([]claudeProc, error) { return procs, nil }, cwd)
	want := worktreeUsage{Cwds: []string{"/Users/me/sample-app/web", "/Users/me/old"}, Sessions: []string{"/Users/me/sample-app"}, OK: true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("working folders %+v\n            want %+v", got, want)
	}
	if got := claudeWorkingFoldersWith(func() ([]claudeProc, error) { return nil, errors.New("no process table") }, cwd); got.OK {
		t.Errorf("working folders %+v from no process table, want OK false", got)
	}
}

// viewMainHere runs what needs the main thread on the calling goroutine:
// the test binary never serves the main thread, so a queued run would wait
// forever.
func viewMainHere(t *testing.T) {
	prev := runOnMain
	runOnMain = func(f func()) { f() }
	t.Cleanup(func() { runOnMain = prev })
}

// TestClaudeRunningOnThisMac reads the Running tab once on this Mac. It
// prints counts, and each session's status and idle time, never a command,
// path, title or session id. Opt in with CLEANZO_PROBE=1.
func TestClaudeRunningOnThisMac(t *testing.T) {
	if os.Getenv("CLEANZO_PROBE") == "" {
		t.Skip("set CLEANZO_PROBE=1 to read the Running tab on this Mac")
	}
	viewMainHere(t)
	viewResetPorts(t)
	r, err := claudeRunningWith(context.Background(), realClaudeSource())
	if err != nil {
		t.Fatal(err)
	}
	idle, rows := 0, 0
	for _, s := range r.Sessions {
		if s.Status == "idle" && time.Duration(r.At-s.Since)*time.Second >= claudeIdleNote {
			idle++
		}
	}
	for _, l := range r.Left {
		rows += len(l.Procs)
	}
	t.Logf("%d open sessions, %d idle for %v or longer, %d rows left by closed sessions, %d uncertain, noRecords %v",
		len(r.Sessions), idle, claudeIdleNote, rows, r.Uncertain, r.NoRecords)
	for i, s := range r.Sessions {
		idleFor := "-"
		if s.Status == "idle" {
			idleFor = (time.Duration(r.At-s.Since) * time.Second).String()
		}
		t.Logf("session %d: status %q, idle for %s", i+1, s.Status, idleFor)
	}
}

// TestClaudeRunningBudget measures the Running tab on this Mac: a reading
// every five seconds, lsof included, may use 3% of one core. Opt in with
// CLEANZO_PERF=1; it runs for 60 seconds. Don't use -race, which inflates
// the CPU.
func TestClaudeRunningBudget(t *testing.T) {
	if os.Getenv("CLEANZO_PERF") == "" {
		t.Skip("set CLEANZO_PERF=1 to measure the Running tab's cost on this Mac")
	}
	viewMainHere(t)
	viewResetPorts(t)
	src := realClaudeSource()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	readings := 0
	before, start := rusageCPU(), time.Now()
	for ctx.Err() == nil {
		if r, err := claudeRunningWith(ctx, src); err == nil {
			readings++
			if _, err := json.Marshal(r); err != nil {
				t.Fatal(err)
			}
		}
		_ = sleepCtx(ctx, 5*time.Second)
	}
	used, wall := rusageCPU()-before, time.Since(start)
	pct := used.Seconds() / wall.Seconds() * 100
	t.Logf("%d readings, CPU %v in %v: %.2f%% of one core", readings, used.Round(time.Millisecond), wall.Round(time.Second), pct)
	if pct > 3 {
		t.Errorf("the Running tab used %.2f%% of one core, budget 3%%", pct)
	}
}
