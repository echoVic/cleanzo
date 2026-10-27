package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/ebitengine/purego"
)

// spawnOutput exists because of what fork costs here. os/exec starts a
// child with fork, and fork first runs every pthread_atfork handler in this
// process. With WebKit loaded those lock every malloc zone and wait on
// Network.framework's path monitor: a sample of the running app showed
// about 56 ms of CPU per child, so ps every six seconds alone took about 1%
// of a core. posix_spawn starts the child without running them.

const (
	spawnSetSigDef      = 0x0004 // POSIX_SPAWN_SETSIGDEF
	spawnSetSigMask     = 0x0008 // POSIX_SPAWN_SETSIGMASK
	spawnCloexecDefault = 0x4000 // POSIX_SPAWN_CLOEXEC_DEFAULT: the child gets only the descriptors set up here
)

var (
	spawnOnce              sync.Once
	spawnErr               error
	posixSpawn             func(pid *int32, path string, actions, attr *uintptr, argv, envp **byte) int32
	spawnActionsInit       func(actions *uintptr) int32
	spawnActionsDestroy    func(actions *uintptr) int32
	spawnActionsAddOpen    func(actions *uintptr, fd int32, path string, oflag int32, mode uint16) int32
	spawnActionsAddDup2    func(actions *uintptr, fd, newfd int32) int32
	spawnAttrInit          func(attr *uintptr) int32
	spawnAttrDestroy       func(attr *uintptr) int32
	spawnAttrSetFlags      func(attr *uintptr, flags int16) int32
	spawnAttrSetSigMask    func(attr *uintptr, mask *uint32) int32
	spawnAttrSetSigDefault func(attr *uintptr, mask *uint32) int32
)

func spawnInit() error {
	spawnOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			spawnErr = err
			return
		}
		purego.RegisterLibFunc(&posixSpawn, lib, "posix_spawn")
		purego.RegisterLibFunc(&spawnActionsInit, lib, "posix_spawn_file_actions_init")
		purego.RegisterLibFunc(&spawnActionsDestroy, lib, "posix_spawn_file_actions_destroy")
		purego.RegisterLibFunc(&spawnActionsAddOpen, lib, "posix_spawn_file_actions_addopen")
		purego.RegisterLibFunc(&spawnActionsAddDup2, lib, "posix_spawn_file_actions_adddup2")
		purego.RegisterLibFunc(&spawnAttrInit, lib, "posix_spawnattr_init")
		purego.RegisterLibFunc(&spawnAttrDestroy, lib, "posix_spawnattr_destroy")
		purego.RegisterLibFunc(&spawnAttrSetFlags, lib, "posix_spawnattr_setflags")
		purego.RegisterLibFunc(&spawnAttrSetSigMask, lib, "posix_spawnattr_setsigmask")
		purego.RegisterLibFunc(&spawnAttrSetSigDefault, lib, "posix_spawnattr_setsigdefault")
	})
	return spawnErr
}

// spawnOutput runs path like exec.CommandContext(ctx, ...).Output(), but
// starts it with posix_spawn. argv includes argv[0]; env is passed exactly
// as given. Standard input and error are /dev/null. It returns the standard
// output and the child's pid. Canceling ctx kills the child, and a non-zero
// exit is an error. The child is always reaped before it returns.
func spawnOutput(ctx context.Context, path string, argv, env []string) ([]byte, int32, error) {
	if err := spawnInit(); err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	cArgv, err := cStrings(argv)
	if err != nil {
		return nil, 0, err
	}
	cEnv, err := cStrings(env)
	if err != nil {
		return nil, 0, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, 0, err
	}
	defer r.Close()
	pid, err := spawnChild(path, cArgv, cEnv, w)
	w.Close() // the child has its own copy: EOF comes when it exits
	if err != nil {
		return nil, 0, err
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			// Not reaped yet, so the pid is still this child's.
			_ = syscall.Kill(int(pid), syscall.SIGKILL)
		case <-stop:
		}
	}()
	out, readErr := io.ReadAll(r)
	close(stop)
	<-stopped
	var status syscall.WaitStatus
	for {
		_, err := syscall.Wait4(int(pid), &status, 0, nil)
		if err == nil {
			break
		}
		if err != syscall.EINTR {
			return nil, pid, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, pid, err
	}
	if readErr != nil {
		return nil, pid, readErr
	}
	if !status.Exited() || status.ExitStatus() != 0 {
		return nil, pid, fmt.Errorf("%s: %s", path, waitText(status))
	}
	return out, pid, nil
}

// spawnChild starts path with stdout on the pipe's write end, stdin and
// stderr on /dev/null, no other descriptors, no blocked signals, and
// SIGPIPE at its default.
func spawnChild(path string, argv, env []*byte, stdout *os.File) (int32, error) {
	var actions, attr uintptr
	if rc := spawnActionsInit(&actions); rc != 0 {
		return 0, syscall.Errno(rc)
	}
	defer spawnActionsDestroy(&actions)
	if rc := spawnAttrInit(&attr); rc != 0 {
		return 0, syscall.Errno(rc)
	}
	defer spawnAttrDestroy(&attr)
	var noSignals uint32
	pipeSignal := uint32(1) << (uint(syscall.SIGPIPE) - 1)
	for _, rc := range []int32{
		spawnActionsAddOpen(&actions, 0, os.DevNull, syscall.O_RDONLY, 0),
		// Fd also puts the write end in blocking mode; the child's stdout
		// shares that flag, and a non-blocking stdout would fail ps's writes.
		spawnActionsAddDup2(&actions, int32(stdout.Fd()), 1),
		spawnActionsAddOpen(&actions, 2, os.DevNull, syscall.O_WRONLY, 0),
		spawnAttrSetFlags(&attr, spawnSetSigDef|spawnSetSigMask|spawnCloexecDefault),
		spawnAttrSetSigMask(&attr, &noSignals),
		spawnAttrSetSigDefault(&attr, &pipeSignal),
	} {
		if rc != 0 {
			return 0, syscall.Errno(rc)
		}
	}
	var pid int32
	rc := posixSpawn(&pid, path, &actions, &attr, &argv[0], &env[0])
	// posix_spawn copies argv and envp before it returns.
	runtime.KeepAlive(argv)
	runtime.KeepAlive(env)
	if rc != 0 {
		return 0, &os.PathError{Op: "posix_spawn", Path: path, Err: syscall.Errno(rc)}
	}
	return pid, nil
}

// cStrings makes the NULL-terminated array of C strings that argv and envp
// are. A string with a NUL byte is an error.
func cStrings(ss []string) ([]*byte, error) {
	out := make([]*byte, 0, len(ss)+1)
	for _, s := range ss {
		p, err := syscall.BytePtrFromString(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return append(out, nil), nil
}

func waitText(s syscall.WaitStatus) string {
	if s.Signaled() {
		return "killed by " + s.Signal().String()
	}
	return fmt.Sprintf("exit status %d", s.ExitStatus())
}

// withEnv returns env with kv ("KEY=value") in place of any KEY already
// there. A duplicate key is not harmless: getenv returns the first one.
func withEnv(env []string, kv string) []string {
	key, _, _ := strings.Cut(kv, "=")
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if k, _, _ := strings.Cut(e, "="); k != key {
			out = append(out, e)
		}
	}
	return append(out, kv)
}
