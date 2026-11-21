package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Tests of the Running tab's native reads. Only TestNativeCwdAndFdPath and
// TestClaudeSnapshotSeesThisProcess read this machine's process table, and
// they look only at the test process and the child it starts.

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
	var want string
	for _, p := range nativeProcs() {
		if p.Pid == self {
			want = p.Name
		}
	}
	if got := nativeProcName(self); want == "" || got != want {
		t.Errorf("nativeProcName(self) = %q, want %q as nativeProcs reads it", got, want)
	}
	if nativeFootprint(-1) != 0 || nativeProcName(-1) != "" {
		t.Error("an invalid pid gave a footprint or a name")
	}
}

// viewProcargs is a kern.procargs2 buffer as the kernel lays it out: argc,
// the executable's path, NUL padding, then args and env, each
// NUL-terminated; after is appended as it is.
func viewProcargs(argc int32, args, env []string, after string) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(argc))
	b = append(b, "/usr/local/bin/node\x00\x00\x00\x00"...)
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

// viewNoApps makes the snapshot's app list empty: NSWorkspace needs the
// main thread, which a test binary never serves.
func viewNoApps(t *testing.T) {
	prev := runOnMain
	runOnMain = func(func()) {}
	t.Cleanup(func() { runOnMain = prev })
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
	viewNoApps(t)
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
	if _, listed := byPid[int32(zombie.Process.Pid)]; listed {
		t.Error("the snapshot lists a zombie")
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
