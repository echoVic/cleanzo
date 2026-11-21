package main

import (
	"cmp"
	"iter"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

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

// Limits of the attribution rules.
const (
	claudeIDRounds        = 5                          // generations a session id passes up from a child to a parent that shows no environment
	claudeCommandWidth    = 300                        // characters of a displayed command
	claudeProcStartLayout = "Mon Jan _2 15:04:05 2006" // how a session record writes its process's start, in UTC
)

// attributeClaude sorts the processes of s into the open sessions, with what
// each started, and the closed sessions that left processes running.
func attributeClaude(s claudeSnapshot) claudeAttribution {
	t := newClaudeTable(s)
	above := map[int32]bool{} // Cleanzo's ancestors
	for q := range t.ancestors(s.Self) {
		above[q.Pid] = true
	}
	t.findCandidates(s, above)
	t.carryIDs()

	var a claudeAttribution
	for pid, head := range t.claude {
		if pid == head && t.records[pid].SessionID == "" {
			a.NoRecords = true
		}
	}
	type place struct {
		open   int32  // the Claude Code process heading the open session it belongs to
		closed string // or the closed session that left it
	}
	places := map[int32]place{}
	for pid, id := range t.id {
		cc, ok := t.climb(pid)
		switch p := t.procs[pid]; {
		case !ok:
		case cc != 0:
			places[pid] = place{open: t.claude[cc]}
		default:
			if head, open := t.openSession(p, id); open {
				places[pid] = place{open: head}
			} else if !a.NoRecords && !t.startedBefore(p.Env.Pid, p.Start) {
				places[pid] = place{closed: id}
			} else {
				a.Uncertain++
			}
		}
	}

	groups := map[int32][]int32{} // the process groups of each session's Claude Code processes
	members := map[int32][]claudeProc{}
	for pid, head := range t.claude {
		groups[head] = append(groups[head], t.procs[pid].Pgid)
		if pid == head {
			a.Sessions = append(a.Sessions, claudeOpen{Proc: t.procs[pid], Record: t.records[pid], ID: t.records[pid].SessionID, Own: above[pid]})
		} else {
			members[head] = append(members[head], t.procs[pid])
		}
	}
	commands := map[int32][]claudeProc{}
	left := map[string][]claudeProc{}
	for pid, at := range places {
		switch p := t.procs[pid]; {
		case at.closed != "":
			left[at.closed] = append(left[at.closed], p)
		case slices.Contains(groups[at.open], p.Pgid):
			members[at.open] = append(members[at.open], p)
		default:
			commands[at.open] = append(commands[at.open], p)
		}
	}
	slices.SortFunc(a.Sessions, func(x, y claudeOpen) int { return claudeByStart(x.Proc, y.Proc) })
	for i := range a.Sessions {
		head := a.Sessions[i].Proc.Pid
		slices.SortFunc(members[head], claudeByStart)
		a.Sessions[i].Members = members[head]
		a.Sessions[i].Commands = t.rows(commands[head])
	}
	for id, procs := range left {
		a.Left = append(a.Left, claudeLeftBehind{ID: id, Rows: t.rows(procs)})
	}
	slices.SortFunc(a.Left, func(x, y claudeLeftBehind) int {
		return cmp.Or(cmp.Compare(x.Rows[0].Lead.Start, y.Rows[0].Lead.Start), strings.Compare(x.ID, y.ID))
	})
	return a
}

// claudeTable is a snapshot indexed for the attribution rules.
type claudeTable struct {
	procs    map[int32]claudeProc
	children map[int32][]int32
	claude   map[int32]int32        // each Claude Code process to the one heading its session
	records  map[int32]claudeRecord // the valid records of Claude Code processes
	cand     map[int32]bool         // the processes that may be listed
	id       map[int32]string       // the session id each candidate carries; never ""
}

// newClaudeTable indexes s and finds its Claude Code processes: those with a
// valid record, and the account's whose argv[0] is claude. One with another
// above it is part of the topmost one's session.
func newClaudeTable(s claudeSnapshot) *claudeTable {
	t := &claudeTable{
		procs:    make(map[int32]claudeProc, len(s.Procs)),
		children: map[int32][]int32{},
		claude:   map[int32]int32{},
		records:  map[int32]claudeRecord{},
		cand:     map[int32]bool{},
		id:       map[int32]string{},
	}
	for _, p := range s.Procs {
		if _, dup := t.procs[p.Pid]; !dup {
			t.procs[p.Pid] = p
			t.children[p.Ppid] = append(t.children[p.Ppid], p.Pid)
		}
	}
	for pid, p := range t.procs {
		if r := s.Records[pid]; claudeRecordValid(p, r, s.UID) {
			t.records[pid] = r
			t.claude[pid] = pid
		} else if p.UID == s.UID && len(p.Args) > 0 && filepath.Base(p.Args[0]) == "claude" {
			t.claude[pid] = pid
		}
	}
	for pid := range t.claude {
		for q := range t.ancestors(pid) {
			if _, ok := t.claude[q.Pid]; ok {
				t.claude[pid] = q.Pid
			}
		}
	}
	return t
}

// findCandidates marks the processes that may be listed: the account's,
// except its Claude Code processes, Cleanzo with its ancestors (above) and
// descendants, and the Dock's apps with the processes inside their bundles.
func (t *claudeTable) findCandidates(s claudeSnapshot, above map[int32]bool) {
	family := maps.Clone(above)
	family[s.Self] = true
	for queue := []int32{s.Self}; len(queue) > 0; queue = queue[1:] {
		for _, child := range t.children[queue[0]] {
			if !family[child] {
				family[child] = true
				queue = append(queue, child)
			}
		}
	}
	dock := map[int32]bool{}
	var bundles []string
	for _, app := range s.Apps {
		if app.Regular && app.Path != "" {
			dock[app.Pid] = true
			bundles = append(bundles, strings.ToLower(app.Path))
		}
	}
	inBundle := func(path string) bool {
		path = strings.ToLower(path)
		return path != "" && slices.ContainsFunc(bundles, func(b string) bool { return within(path, b) })
	}
	for pid, p := range t.procs {
		if _, claude := t.claude[pid]; pid > 1 && p.UID == s.UID && !claude && !family[pid] && !dock[pid] && !inBundle(p.Path) {
			t.cand[pid] = true
		}
	}
}

// carryIDs gives the candidates the session ids they carry: a marked one its
// marker's; one that shows no environment the one of its output file, else
// the one its children agree on, passed up claudeIDRounds generations, else
// its parent's.
func (t *claudeTable) carryIDs() {
	for pid := range t.cand {
		switch p := t.procs[pid]; {
		case p.Env.Seen:
			if p.Env.Marked && p.Env.Session != "" {
				t.id[pid] = p.Env.Session
			}
		case p.Out != "":
			t.id[pid] = p.Out
		}
	}
	for range claudeIDRounds {
		found := map[int32]string{}
		for pid := range t.cand {
			if _, has := t.id[pid]; !has && !t.procs[pid].Env.Seen {
				if id := t.childrenID(pid); id != "" {
					found[pid] = id
				}
			}
		}
		if len(found) == 0 {
			break
		}
		maps.Copy(t.id, found)
	}
	settled := map[int32]bool{}
	for pid := range t.cand {
		t.fromParent(pid, settled)
	}
}

// childrenID is the session id the children of pid carry, or "" when none
// carries one or they carry different ones.
func (t *claudeTable) childrenID(pid int32) string {
	id := ""
	for _, child := range t.children[pid] {
		if cid := t.id[child]; cid != "" {
			if id != "" && cid != id {
				return ""
			}
			id = cid
		}
	}
	return id
}

// fromParent gives the candidate pid, when it shows no environment and
// carries no session id, the one of its parent: a candidate carrying one,
// or a Claude Code process, whose session it joins. Parents on the way up
// that show no environment and carry no id get the same one; settled holds
// the processes already done.
func (t *claudeTable) fromParent(pid int32, settled map[int32]bool) {
	var path []int32
	id := ""
	for q := pid; ; {
		if qid, has := t.id[q]; has {
			id = qid
			break
		}
		p := t.procs[q]
		if !t.cand[q] || p.Env.Seen || settled[q] || slices.Contains(path, q) {
			break
		}
		path = append(path, q)
		if _, claude := t.claude[p.Ppid]; claude {
			id = t.records[p.Ppid].SessionID
			break
		}
		q = p.Ppid
	}
	for _, h := range path {
		settled[h] = true
		if id != "" {
			t.id[h] = id
		}
	}
}

// climb follows the parents of the candidate pid while they are candidates
// carrying its session id. It returns the Claude Code process it reaches, or
// 0 when it reaches launchd; ok is false when it meets anything else first,
// such as a process missing from the snapshot.
func (t *claudeTable) climb(pid int32) (claude int32, ok bool) {
	id, p := t.id[pid], t.procs[pid]
	for range len(t.procs) {
		parent := p.Ppid
		if parent == 1 {
			return 0, true
		}
		if _, cc := t.claude[parent]; cc {
			return parent, true
		}
		if !t.cand[parent] || t.id[parent] != id {
			return 0, false
		}
		p = t.procs[parent]
	}
	return 0, false
}

// openSession finds the open session of the detached candidate p carrying
// id: the one of the Claude Code process its CLAUDE_PID names, else the one
// of the oldest Claude Code process whose session id is id. It returns the
// Claude Code process heading that session; ok is false when there is none.
func (t *claudeTable) openSession(p claudeProc, id string) (head int32, ok bool) {
	if h, isClaude := t.claude[p.Env.Pid]; isClaude {
		return h, true
	}
	var oldest claudeProc
	for pid := range t.claude {
		if q := t.procs[pid]; t.records[pid].SessionID == id && (!ok || claudeByStart(q, oldest) < 0) {
			oldest, ok = q, true
		}
	}
	return t.claude[oldest.Pid], ok
}

// startedBefore reports whether pid is a live process that started before
// start; pid 0 is no process.
func (t *claudeTable) startedBefore(pid int32, start int64) bool {
	p, ok := t.procs[pid]
	return pid > 0 && ok && p.Start < start
}

// rows merges the processes listed in one place into rows: an app's helpers,
// inside the same outermost bundle, join the topmost of them they descend
// from; then the shell Claude Code runs a command in joins the oldest other
// row of its process group.
func (t *claudeTable) rows(procs []claudeProc) []claudeRow {
	procs = slices.SortedFunc(slices.Values(procs), claudeByStart)
	here := make(map[int32]bool, len(procs))
	for _, p := range procs {
		here[p.Pid] = true
	}
	into := map[int32]int32{} // a folded process to the process it joins
	for _, p := range procs {
		if bundle := outermostBundle(p.Path); bundle != "" {
			for q := range t.ancestors(p.Pid) {
				if here[q.Pid] && strings.EqualFold(outermostBundle(q.Path), bundle) {
					into[p.Pid] = q.Pid
				}
			}
		}
	}
	for _, w := range procs {
		if _, folded := into[w.Pid]; folded || w.Pid != w.Pgid {
			continue
		}
		if _, ok := claudeWrapperCommand(w.Args); !ok {
			continue
		}
		for _, r := range procs {
			if _, folded := into[r.Pid]; !folded && r.Pid != w.Pid && r.Pgid == w.Pgid {
				into[w.Pid] = r.Pid
				break
			}
		}
	}
	lead := func(pid int32) int32 {
		for range len(procs) {
			next, folded := into[pid]
			if !folded {
				break
			}
			pid = next
		}
		return pid
	}
	var rows []claudeRow
	at := map[int32]int{} // a lead to its row's index
	for _, p := range procs {
		if _, folded := into[p.Pid]; !folded {
			at[p.Pid] = len(rows)
			rows = append(rows, claudeRow{Lead: p})
		}
	}
	for _, p := range procs {
		if _, folded := into[p.Pid]; folded {
			i := at[lead(p.Pid)]
			rows[i].Folded = append(rows[i].Folded, p)
		}
	}
	return rows
}

// ancestors yields the processes above pid, nearest first, up to launchd,
// as far as the snapshot has them.
func (t *claudeTable) ancestors(pid int32) iter.Seq[claudeProc] {
	return func(yield func(claudeProc) bool) {
		p, ok := t.procs[pid]
		for range len(t.procs) {
			if !ok || p.Pid == 1 {
				return
			}
			if p, ok = t.procs[p.Ppid]; ok && !yield(p) {
				return
			}
		}
	}
}

// claudeByStart orders processes by start, then by pid.
func claudeByStart(a, b claudeProc) int {
	return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(a.Pid, b.Pid))
}

// claudeRecordValid reports whether r is the session record of p, a process
// of the account uid.
func claudeRecordValid(p claudeProc, r claudeRecord, uid uint32) bool {
	started := time.UnixMicro(p.Start).UTC().Format(claudeProcStartLayout)
	return p.UID == uid && squeezeSpaces(r.ProcStart) == squeezeSpaces(started)
}

// squeezeSpaces is s with each run of white space made one space and none at
// either end.
func squeezeSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// claudeWrapperCommand returns the command inside the shell Claude Code runs
// a command in; ok is false when args are not that shell.
func claudeWrapperCommand(args []string) (inner string, ok bool) {
	if len(args) == 0 {
		return "", false
	}
	switch strings.TrimPrefix(filepath.Base(args[0]), "-") {
	case "zsh", "bash", "sh":
	default:
		return "", false
	}
	const open = " eval '"
	for _, arg := range args[1:] {
		start := strings.Index(arg, open)
		if start < 0 {
			continue
		}
		rest := arg[start+len(open):]
		end := max(strings.LastIndex(rest, "' < /dev/null"), strings.LastIndex(rest, `' \< /dev/null`)) // slash-ok: the shell's input redirection, as Claude Code writes it
		if end >= 0 {
			return strings.ReplaceAll(rest[:end], `'\''`, "'"), true
		}
	}
	return "", false
}

// maskCommand is args as one line for display, with leading assignments and
// secret values removed.
func maskCommand(args []string) string {
	var words []string
	if inner, ok := claudeWrapperCommand(args); ok {
		words = shellWords(inner)
	} else if len(args) > 0 {
		words = slices.Clone(args)
		if words[0] != "" {
			words[0] = filepath.Base(words[0])
		}
	}
	for len(words) > 0 && isAssignment(words[0]) {
		words = words[1:]
	}
	for i := 0; i < len(words); i++ {
		flag, _, hasValue := strings.Cut(words[i], "=")
		if !strings.HasPrefix(flag, "-") || !secretFlag(flag) {
			continue
		}
		if hasValue {
			words[i] = flag + "=…"
		} else if i+1 < len(words) {
			words[i+1] = "…"
			i++
		}
	}
	return oneLine(strings.Join(words, " "), claudeCommandWidth)
}

// shellBlanks are the characters that end an unquoted shell word.
const shellBlanks = " \t\n\r\v\f"

// shellWords splits s into words where the shell would, at white space
// outside quotes, keeping each word as written.
func shellWords(s string) []string {
	var words []string
	for {
		s = strings.TrimLeft(s, shellBlanks)
		if s == "" {
			return words
		}
		n := shellWordLen(s)
		words = append(words, s[:n])
		s = s[n:]
	}
}

// shellWordLen is the length of the shell word s starts with: up to the
// first white space outside quotes and backslash escapes, or all of s when a
// quote is left open.
func shellWordLen(s string) int {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return len(s)
			}
			i += end + 1
		case '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
			if i >= len(s) {
				return len(s)
			}
		default:
			if strings.IndexByte(shellBlanks, s[i]) >= 0 {
				return i
			}
		}
	}
	return len(s)
}

// isAssignment reports whether word sets a shell variable: NAME=value.
func isAssignment(word string) bool {
	name, _, ok := strings.Cut(word, "=")
	if !ok || name == "" {
		return false
	}
	for i, c := range name {
		letter := c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// secretFlag reports whether the name of flag says its value is a secret.
func secretFlag(flag string) bool {
	flag = strings.ToLower(flag)
	return slices.ContainsFunc([]string{"key", "token", "secret", "password"}, func(word string) bool {
		return strings.Contains(flag, word)
	})
}

// oneLine is s as one line of at most width characters: each run of white
// space is one space, none at either end, other control characters are
// dropped, and a line cut short ends in "…".
func oneLine(s string, width int) string {
	var line []rune
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			space = len(line) > 0
		case unicode.IsControl(r):
		default:
			if space {
				line = append(line, ' ')
				space = false
			}
			line = append(line, r)
			if len(line) > width {
				return strings.TrimRight(string(line[:width-1]), " ") + "…"
			}
		}
	}
	return string(line)
}
