package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/ebitengine/purego"
)

func TestMeasureGrowthEntryNeverFollowsLinks(t *testing.T) {
	root, big := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(big, "huge"), 50000)
	writeFile(t, filepath.Join(root, "small"), 10)
	if err := os.Symlink(big, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if e := measureGrowthEntry(context.Background(), growthTarget{path: root, kind: growthHome}); e.Size != 10 {
		t.Fatalf("size %d: the link was followed", e.Size)
	}
}

func TestMeasureGrowthEntryMarksUnreadableParts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok"), 10)
	locked := filepath.Join(root, "locked")
	writeFile(t, filepath.Join(locked, "secret"), 10)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	e := measureGrowthEntry(context.Background(), growthTarget{path: root, kind: growthHome})
	if !e.Denied || !e.Complete || e.Size != 10 {
		t.Fatalf("entry = %+v", e)
	}
}

func TestBackgroundThreadReturns(t *testing.T) {
	done := make(chan struct{})
	go func() {
		backgroundThread()
		close(done)
	}()
	<-done
}

// Arguments of getiopolicy_np, from <sys/resource.h>.
const (
	ioTypeDisk    = 0 // IOPOL_TYPE_DISK
	ioScopeThread = 1 // IOPOL_SCOPE_THREAD
)

// TestBackgroundThreadLowersPriority reads the state back on the thread that
// backgroundThread set: getpriority(PRIO_DARWIN_THREAD) must report it there,
// and a plain thread must not. getiopolicy_np probes the disk I/O policy of
// both threads; it is logged, and the probe is test-only.
func TestBackgroundThreadLowersPriority(t *testing.T) {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	var getIOPolicy func(iotype, scope int32) int32
	purego.RegisterLibFunc(&getIOPolicy, lib, "getiopolicy_np")
	read := func(background bool) (prio int, policy int32, err error) {
		done := make(chan struct{})
		go func() {
			defer close(done)
			if background {
				backgroundThread()
			} else {
				runtime.LockOSThread()
			}
			prio, err = syscall.Getpriority(prioDarwinThread, 0)
			policy = getIOPolicy(ioTypeDisk, ioScopeThread)
		}()
		<-done
		return prio, policy, err
	}
	bgPrio, bgPolicy, err := read(true)
	if err != nil {
		t.Fatal(err)
	}
	plainPrio, plainPolicy, err := read(false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("getpriority(PRIO_DARWIN_THREAD): background %d, plain %d", bgPrio, plainPrio)
	t.Logf("getiopolicy_np(IOPOL_TYPE_DISK, IOPOL_SCOPE_THREAD): background %d, plain %d", bgPolicy, plainPolicy)
	if bgPrio == 0 {
		t.Error("the background thread does not report the background state")
	}
	if plainPrio != 0 {
		t.Error("a plain thread reports the background state")
	}
}
