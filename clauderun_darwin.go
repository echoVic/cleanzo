package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
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
		footprint: nativeFootprint,
		cwd:       nativeCwd,
		identity: func(pid int32) (string, string) {
			return nativePidPath(pid), nativeProcName(pid)
		},
		responsible: nativeResponsiblePid,
		system:      systemExecutable,
		ports:       claudeListenPorts,
		closed:      claudeClosedSession,
		transcripts: claudeTranscripts,
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
func claudeWorkingFolders() worktreeUsage {
	return claudeWorkingFoldersWith(claudeProcTable, nativeCwd)
}

// claudeWorkingFoldersWith is claudeWorkingFolders over the process table
// and current folders it is given: the current folder of each of this
// account's processes that has one, and the cwd of each valid session
// record.
func claudeWorkingFoldersWith(table func() ([]claudeProc, error), cwd func(pid int32) string) worktreeUsage {
	procs, err := table()
	// A reading that cannot tell this process's own current folder cannot
	// tell the others'.
	if err != nil || cwd(int32(os.Getpid())) == "" {
		return worktreeUsage{}
	}
	uid := uint32(os.Getuid())
	records := readClaudeRecords(filepath.Join(homeDir(), ".claude", "sessions"))
	use := worktreeUsage{OK: true}
	for _, p := range procs {
		if p.UID != uid {
			continue
		}
		if dir := cwd(p.Pid); dir != "" {
			use.Cwds = append(use.Cwds, dir)
		}
		if r, ok := records[p.Pid]; ok && r.Cwd != "" && claudeRecordValid(p, r, uid) {
			use.Sessions = append(use.Sessions, r.Cwd)
		}
	}
	return use
}

// claudeRunningApps lists the running apps for a snapshot. Tests replace it.
var claudeRunningApps = workspaceList

// procZombie is p_stat of a process that exited and is not reaped yet: it
// runs nothing.
const procZombie = 5 // SZOMB

// readClaudeSnapshot reads the process table natively, with the arguments,
// marker, executable and output file of each of this account's processes,
// then the session records and the running apps. Without the running apps
// nothing tells an app on the Dock from a command a session started, so a
// snapshot that cannot read them is an error.
func readClaudeSnapshot() (claudeSnapshot, error) {
	apps := claudeRunningApps()
	if len(apps) == 0 {
		return claudeSnapshot{}, errors.New("the running apps cannot be read")
	}
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
		Apps:    apps,
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
// int32, the executable's path with its NUL padded to a multiple of 8
// bytes, then argc arguments and the environment, each NUL-terminated, the
// environment ending at an empty string. The arguments are read from where
// the padding ends, so an empty argument stays an argument. A process that
// rewrote its argument area into fewer strings than argc, as a long process
// title does, cannot be told apart: its first environment strings are then
// read as arguments, which is why maskCommand hides the value of every
// name=value it shows. Of the environment it keeps Claude
// Code's three variables, the first of each name as getenv finds it, and
// whether any variable could be read; no other variable is copied out of
// buf. A buffer that is not laid out so, or does not hold argc arguments,
// gives no arguments and no environment.
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
	start := (path + 8) &^ 7 // the path and its NUL, rounded up to 8 bytes
	if start > len(rest) || len(bytes.Trim(rest[path:start], "\x00")) != 0 {
		return nil, claudeEnv{}
	}
	rest = rest[start:]
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

// claudeLsofSpawn runs lsof. Tests point it at a stand-in.
var claudeLsofSpawn = spawnOutput

// claudeLsofWait bounds one lsof run.
var claudeLsofWait = 3 * time.Second

// claudeListenPorts reads the TCP ports pids listen on with one lsof run;
// nil when lsof cannot say, as when it fails or runs out of time.
func claudeListenPorts(ctx context.Context, pids []int32) map[int32][]int {
	if len(pids) == 0 {
		return map[int32][]int{}
	}
	ctx, cancel := context.WithTimeout(ctx, claudeLsofWait)
	defer cancel()
	list := make([]string, len(pids))
	for i, pid := range pids {
		list[i] = strconv.Itoa(int(pid))
	}
	argv := []string{"/usr/sbin/lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-a", "-p", strings.Join(list, ","), "-Fpn"}
	out, _, err := claudeLsofSpawn(ctx, argv[0], argv, withEnv(os.Environ(), "LC_ALL=en_US.UTF-8"))
	// lsof exits with 1 when none of the pids listens, and when one of them
	// is gone; what it printed for the others still holds.
	var exit *spawnExitError
	if ctx.Err() != nil || err != nil && !(errors.As(err, &exit) && exit.status.Exited() && exit.status.ExitStatus() == 1) {
		return nil
	}
	return parseLsofListen(out)
}

// parseLsofListen reads lsof -Fpn output: a line "p<pid>" starts each
// process, and each "n<address>:<port>" after it is one of its listening
// sockets. The ports of each pid come without duplicates, ascending.
func parseLsofListen(out []byte) map[int32][]int {
	ports := map[int32][]int{}
	pid := int32(0)
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "p"):
			n, err := strconv.ParseInt(line[1:], 10, 32)
			pid = int32(n)
			if err != nil || n <= 0 {
				pid = 0
			}
		case strings.HasPrefix(line, "n") && pid > 0:
			port, err := strconv.Atoi(line[strings.LastIndexByte(line, ':')+1:])
			if err == nil && port > 0 && port <= 65535 && !slices.Contains(ports[pid], port) {
				ports[pid] = append(ports[pid], port)
			}
		}
	}
	for _, list := range ports {
		slices.Sort(list)
	}
	return ports
}

// claudeTitleTail is how much of the end of a closed session's transcript
// is read for its title.
const claudeTitleTail = 2 << 20

// claudeClosedSession reads the project and title of a closed session from
// its transcript, ~/.claude/projects/*/<session>.jsonl: the folder name of
// the cwd it records, and the last title Claude Code gave the session
// within the transcript's last claudeTitleTail bytes. Nothing is read for a
// session id that is not a UUID.
func claudeClosedSession(session string) (project, title string) {
	if !claudeSessionUUID(session) {
		return "", ""
	}
	dir := filepath.Join(homeDir(), ".claude", "projects")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), session+".jsonl")
		if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		if cwd := jsonlField(path, "cwd"); cwd != "" {
			project = filepath.Base(cwd)
		}
		return project, claudeLastTitle(path)
	}
	return "", ""
}

// claudeTranscripts reads what the transcripts of sessions tell: of each,
// ~/.claude/projects/*/<session>.jsonl, the cwd it records first and when
// its file was made, which is when the session wrote its first line. A
// session is left out when its id is not a UUID, or when its transcript is
// not a regular file in exactly one folder.
func claudeTranscripts(sessions []string) map[string]claudeTranscript {
	found := map[string]claudeTranscript{}
	dir := filepath.Join(homeDir(), ".claude", "projects")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return found
	}
	for _, session := range sessions {
		if _, done := found[session]; done || !claudeSessionUUID(session) {
			continue
		}
		var at string
		var made int64
		folders := 0
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name(), session+".jsonl")
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			folders++
			at = path
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				made = time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec).UnixMicro()
			}
		}
		if folders == 1 {
			found[session] = claudeTranscript{Cwd: jsonlField(at, "cwd"), Made: made}
		}
	}
	return found
}

// claudeLastTitle is the aiTitle of the last line of type ai-title within
// the last claudeTitleTail bytes of the transcript at path, or "".
func claudeLastTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	from := max(0, info.Size()-claudeTitleTail)
	tail, err := io.ReadAll(io.NewSectionReader(f, from, claudeTitleTail))
	if err != nil {
		return ""
	}
	if from > 0 { // the first line is cut short
		_, tail, _ = bytes.Cut(tail, []byte("\n"))
	}
	for end := len(tail); end > 0; {
		start := bytes.LastIndexByte(tail[:end], '\n') + 1
		if line := tail[start:end]; bytes.Contains(line, []byte("ai-title")) {
			var fields map[string]any
			if json.Unmarshal(line, &fields) == nil && fields["type"] == "ai-title" {
				title, _ := fields["aiTitle"].(string)
				return title
			}
		}
		end = start - 1
	}
	return ""
}
