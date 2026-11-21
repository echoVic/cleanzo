package main

import (
	"os"
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
