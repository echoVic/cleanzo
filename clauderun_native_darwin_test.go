package main

import (
	"encoding/binary"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
