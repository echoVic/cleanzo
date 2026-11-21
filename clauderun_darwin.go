package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// claudeRunSupported reports whether the Running tab can read processes:
// macOS only.
func claudeRunSupported() bool { return true }

// realClaudeSource wires the Running tab to the native reads and signals.
func realClaudeSource() claudeSource {
	return claudeSource{
		snapshot: readClaudeSnapshot,
		started: func(pid int32) (int64, bool) {
			kp, err := unix.SysctlKinfoProc("kern.proc.pid", int(pid))
			if err != nil || kp.Proc.P_stat == procZombie {
				return 0, false
			}
			return kinfoStart(kp), true
		},
		footprint:   func(int32) uint64 { return 0 },
		cwd:         func(int32) string { return "" },
		identity:    func(int32) (string, string) { return "", "" },
		responsible: nativeResponsiblePid,
		system:      systemExecutable,
		ports:       func(context.Context, []int32) map[int32][]int { return nil },
		closed:      func(string) (string, string) { return "", "" },
		// One process at a time: a pid of 0 or below would reach a process
		// group or every process, and pid 1 is launchd.
		signal: func(pid int32, sig syscall.Signal) error {
			if pid <= 1 {
				return syscall.EINVAL
			}
			return syscall.Kill(int(pid), sig)
		},
		now:   time.Now,
		sleep: sleepCtx,
	}
}

// claudeWorkingFolders is the working folders of this user's processes and
// of the open Claude Code sessions; OK is false when they cannot be read.
func claudeWorkingFolders() worktreeUsage { return worktreeUsage{} }

// procZombie is p_stat of a process that exited and is not reaped yet: it
// runs nothing.
const procZombie = 5 // SZOMB

// readClaudeSnapshot reads the process table natively, with the arguments,
// marker, executable and output file of each of this account's processes,
// then the session records and the running apps.
func readClaudeSnapshot() (claudeSnapshot, error) {
	procs, err := claudeProcTable()
	if err != nil {
		return claudeSnapshot{}, err
	}
	uid := uint32(os.Getuid())
	for i := range procs {
		p := &procs[i]
		if p.UID != uid {
			continue
		}
		// The buffer holds the whole environment: only what
		// parseProcargs2 returns of it is kept.
		if buf, err := unix.SysctlRaw("kern.procargs2", int(p.Pid)); err == nil {
			p.Args, p.Env = parseProcargs2(buf)
		}
		p.Path = nativePidPath(p.Pid)
		if !p.Env.Seen {
			if p.Out = claudeOutputSession(nativeFdPath(p.Pid, 1)); p.Out == "" {
				p.Out = claudeOutputSession(nativeFdPath(p.Pid, 2))
			}
		}
	}
	return claudeSnapshot{
		Procs:   procs,
		Records: readClaudeRecords(filepath.Join(homeDir(), ".claude", "sessions")),
		Apps:    workspaceList(),
		Self:    int32(os.Getpid()),
		UID:     uid,
	}, nil
}

// claudeProcTable lists every account's processes, zombies left out, with
// their parent, process group, account and start.
func claudeProcTable() ([]claudeProc, error) {
	all, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	procs := make([]claudeProc, 0, len(all))
	for i := range all {
		kp := &all[i]
		if kp.Proc.P_stat == procZombie {
			continue
		}
		procs = append(procs, claudeProc{
			Pid: kp.Proc.P_pid, Ppid: kp.Eproc.Ppid, Pgid: kp.Eproc.Pgid,
			UID: kp.Eproc.Ucred.Uid, Start: kinfoStart(kp),
		})
	}
	return procs, nil
}

// kinfoStart is when the process kp describes started, in unix
// microseconds.
func kinfoStart(kp *unix.KinfoProc) int64 {
	return kp.Proc.P_starttime.Sec*1_000_000 + int64(kp.Proc.P_starttime.Usec)
}

// parseProcargs2 reads a kern.procargs2 buffer: argc as a little-endian
// int32, the executable's path, NUL padding, then argc arguments and the
// environment, each NUL-terminated, the environment ending at an empty
// string. Of the environment it keeps Claude Code's three variables, the
// first of each name as getenv finds it, and whether any variable could be
// read; no other variable is copied out of buf. A buffer that does not hold
// argc arguments gives no arguments and no environment.
func parseProcargs2(buf []byte) (args []string, env claudeEnv) {
	if len(buf) < 4 {
		return nil, claudeEnv{}
	}
	argc := int32(binary.LittleEndian.Uint32(buf))
	rest := buf[4:]
	path := bytes.IndexByte(rest, 0)
	if argc < 0 || int(argc) > len(rest) || path < 0 {
		return nil, claudeEnv{}
	}
	rest = bytes.TrimLeft(rest[path:], "\x00")
	args = make([]string, 0, argc)
	for range argc {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			return nil, claudeEnv{}
		}
		args = append(args, string(rest[:end]))
		rest = rest[end+1:]
	}
	var marker, session, pid bool // the name was met
	for {
		end := bytes.IndexByte(rest, 0)
		if end <= 0 {
			return args, env
		}
		env.Seen = true
		name, value, _ := bytes.Cut(rest[:end], []byte("="))
		rest = rest[end+1:]
		switch {
		case string(name) == "CLAUDECODE" && !marker:
			marker, env.Marked = true, string(value) == "1"
		case string(name) == "CLAUDE_CODE_SESSION_ID" && !session:
			session, env.Session = true, string(value)
		case string(name) == "CLAUDE_PID" && !pid:
			pid = true
			if n, err := strconv.ParseUint(string(value), 10, 31); err == nil {
				env.Pid = int32(n)
			}
		}
	}
}

// claudeOutputSession is the session whose command output the file at path
// is: Claude Code writes it to /claude-<uid>/<project>/<session>/tasks/<name>
// in the temporary folder. It is "" for any other path.
func claudeOutputSession(path string) string {
	parts := strings.Split(path, "/")
	n := len(parts)
	if n < 6 || parts[n-1] == "" || parts[n-2] != "tasks" || parts[n-4] == "" || !claudeSessionUUID(parts[n-3]) {
		return ""
	}
	uid, ok := strings.CutPrefix(parts[n-5], "claude-")
	if !ok || uid == "" || strings.Trim(uid, "0123456789") != "" {
		return ""
	}
	return parts[n-3]
}

// claudeSessionUUID reports whether s is written as Claude Code writes a
// session id: a UUID in lower-case hexadecimal.
func claudeSessionUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range []byte(s) {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}

// claudeRecordMax is how much of a session record is read.
const claudeRecordMax = 64 << 10

// readClaudeRecords reads the session records in dir, ~/.claude/sessions,
// by the pid each file is named after. A record that is not one JSON object
// within claudeRecordMax bytes is left out; a field of another type than
// expected is left empty.
func readClaudeRecords(dir string) map[int32]claudeRecord {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	records := make(map[int32]claudeRecord, len(entries))
	for _, e := range entries {
		name, isJSON := strings.CutSuffix(e.Name(), ".json")
		pid, err := strconv.ParseInt(name, 10, 32)
		if !isJSON || err != nil || pid <= 0 || !e.Type().IsRegular() {
			continue
		}
		if r, ok := readClaudeRecord(filepath.Join(dir, e.Name())); ok {
			records[int32(pid)] = r
		}
	}
	return records
}

// readClaudeRecord reads one session record; ok is false when it cannot be
// read or parsed.
func readClaudeRecord(path string) (r claudeRecord, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return claudeRecord{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, claudeRecordMax))
	var fields map[string]any
	if err != nil || json.Unmarshal(data, &fields) != nil {
		return claudeRecord{}, false
	}
	text := func(key string) string {
		s, _ := fields[key].(string)
		return s
	}
	r = claudeRecord{
		SessionID: text("sessionId"), Cwd: text("cwd"), Name: text("name"),
		Status: text("status"), ProcStart: text("procStart"), Entrypoint: text("entrypoint"),
	}
	// Milliseconds, written as a number or as a string of digits.
	switch v := fields["statusUpdatedAt"].(type) {
	case float64:
		if v > 0 && v < 1<<53 {
			r.StatusUpdatedAt = int64(v)
		}
	case string:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			r.StatusUpdatedAt = n
		}
	}
	return r, true
}
