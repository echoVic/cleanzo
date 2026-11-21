package main

import (
	"encoding/binary"
	"fmt"
	"os"
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
