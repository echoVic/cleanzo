package main

// claudeProc is one process of a snapshot.
type claudeProc struct {
	Pid, Ppid, Pgid int32
	UID             uint32
	Start           int64    // unix microseconds
	Path            string   // the executable as proc_pidpath gives it; "" when unreadable
	Args            []string // nil when unreadable
	Env             claudeEnv
	Out             string // the session whose output file its stdout or stderr is; ""
}

// claudeEnv is all that is kept of a process's environment.
type claudeEnv struct {
	Seen    bool   // an environment could be read
	Marked  bool   // CLAUDECODE=1
	Session string // CLAUDE_CODE_SESSION_ID
	Pid     int32  // CLAUDE_PID; 0 when absent or not a number
}

// claudeRecord is ~/.claude/sessions/<pid>.json. Every field is optional.
type claudeRecord struct {
	SessionID, Cwd, Name, Status, ProcStart, Entrypoint string
	StatusUpdatedAt                                     int64 // milliseconds
}

// claudeSnapshot is everything the rules read, taken at one moment.
type claudeSnapshot struct {
	Procs   []claudeProc           // every account's, zombies left out; Path, Args, Env and Out only for UID's
	Records map[int32]claudeRecord // by the pid in the file name
	Apps    []workspaceApp
	Self    int32 // Cleanzo
	UID     uint32
}

// claudeRow is one listed process and what is folded into it.
type claudeRow struct {
	Lead   claudeProc
	Folded []claudeProc
}

// claudeOpen is an open session and the processes that belong to it.
type claudeOpen struct {
	Proc     claudeProc   // the Claude Code process
	Record   claudeRecord // zero when it has no valid one
	ID       string
	Members  []claudeProc // sub-agents and helpers, ended with the session
	Commands []claudeRow  // what it started, of any age
	Own      bool
}

// claudeLeftBehind is a closed session and the rows it left running.
type claudeLeftBehind struct {
	ID   string
	Rows []claudeRow
}

// claudeAttribution is what attributeClaude makes of a snapshot.
type claudeAttribution struct {
	Sessions  []claudeOpen
	Left      []claudeLeftBehind
	Uncertain int
	NoRecords bool
}

// attributeClaude sorts the processes of s into the open sessions, with what
// each started, and the closed sessions that left processes running.
func attributeClaude(s claudeSnapshot) claudeAttribution {
	return claudeAttribution{}
}

// claudeRecordValid reports whether r is the session record of p, a process
// of the account uid.
func claudeRecordValid(p claudeProc, r claudeRecord, uid uint32) bool {
	return false
}

// claudeWrapperCommand returns the command inside the shell Claude Code runs
// a command in; ok is false when args are not that shell.
func claudeWrapperCommand(args []string) (inner string, ok bool) {
	return "", false
}

// maskCommand is args as one line for display, with leading assignments and
// secret values removed.
func maskCommand(args []string) string {
	return ""
}
