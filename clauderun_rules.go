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
	for pid := range t.claude {
		if t.records[pid].SessionID == "" {
			a.NoRecords = true
		}
	}
	places := map[int32]claudePlace{}
	carriers := map[string][]int32{} // the candidates carrying each session id, attached or detached
	detached := map[string][]int32{} // the detached ones
	for pid, id := range t.id {
		cc, ok := t.climb(pid)
		if !ok {
			continue
		}
		carriers[id] = append(carriers[id], pid)
		if cc != 0 {
			places[pid] = claudePlace{open: t.claude[cc]}
		} else {
			detached[id] = append(detached[id], pid)
		}
	}
	// A session id may be called closed only when every Claude Code process
	// shows its session id and s lists each pid once, as one read of the
	// process table does.
	closable := !a.NoRecords && len(t.procs) == len(s.Procs)
	for id, pids := range detached {
		a.Uncertain += t.placeDetached(id, pids, carriers[id], closable, places)
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

// claudePlace is where a listed candidate goes.
type claudePlace struct {
	open   int32  // the Claude Code process heading the open session it belongs to
	closed string // or the closed session that left it
}

// claudeTable is a snapshot indexed for the attribution rules.
type claudeTable struct {
	procs    map[int32]claudeProc
	children map[int32][]int32
	claude   map[int32]int32        // each Claude Code process to the one heading its session; 0 for none
	records  map[int32]claudeRecord // the valid records of Claude Code processes
	cand     map[int32]bool         // the processes that may be listed
	id       map[int32]string       // the session id each candidate carries; never ""
	dock     map[int32]bool         // the apps on the Dock
	bundles  []string               // their bundles, lower case
}

// newClaudeTable indexes s and finds its Claude Code processes: those with a
// valid record, and the account's whose argv[0] is claude. One with another
// above it is part of the topmost one's session, unless a Dock app stands
// between them. Of a pid s lists more than once, only the first copy counts.
func newClaudeTable(s claudeSnapshot) *claudeTable {
	t := &claudeTable{
		procs:    make(map[int32]claudeProc, len(s.Procs)),
		children: map[int32][]int32{},
		claude:   map[int32]int32{},
		records:  map[int32]claudeRecord{},
		cand:     map[int32]bool{},
		id:       map[int32]string{},
		dock:     map[int32]bool{},
	}
	for _, p := range s.Procs {
		if _, again := t.procs[p.Pid]; !again {
			t.procs[p.Pid] = p
			t.children[p.Ppid] = append(t.children[p.Ppid], p.Pid)
		}
	}
	for _, app := range s.Apps {
		if app.Regular && app.Path != "" {
			t.dock[app.Pid] = true
			t.bundles = append(t.bundles, strings.ToLower(app.Path))
		}
	}
	for pid, p := range t.procs {
		switch r := s.Records[pid]; {
		case pid <= 1:
		case claudeRecordValid(p, r, s.UID):
			t.records[pid] = r
			t.claude[pid] = pid
		case p.UID == s.UID && len(p.Args) > 0 && filepath.Base(p.Args[0]) == "claude":
			t.claude[pid] = pid
		}
	}
	for pid := range t.claude {
		t.claude[pid] = t.head(pid)
	}
	return t
}

// head is the Claude Code process heading the session of the Claude Code
// process pid: the topmost one above it that no process of a Dock app
// separates from it, or else pid itself. Only the processes between two
// Claude Code processes can separate them: a Claude Code process that runs
// from inside a Dock app's bundle still heads its sub-agents. It is 0, no
// session at all, when the parents of pid loop.
func (t *claudeTable) head(pid int32) int32 {
	if t.loops(pid) {
		return 0
	}
	head := pid
	for q := range t.ancestors(pid) {
		if _, claude := t.claude[q.Pid]; claude {
			head = q.Pid
		} else if t.inDock(q) {
			break
		}
	}
	return head
}

// loops reports whether the parents of pid come back around to a process
// already passed, pid included, before they reach launchd or a process the
// snapshot does not have.
func (t *claudeTable) loops(pid int32) bool {
	seen := map[int32]bool{}
	for p, ok := t.procs[pid]; ok && p.Pid != 1; p, ok = t.procs[p.Ppid] {
		if seen[p.Pid] {
			return true
		}
		seen[p.Pid] = true
	}
	return false
}

// inDock reports whether p is an app on the Dock or runs from inside the
// bundle of one.
func (t *claudeTable) inDock(p claudeProc) bool {
	path := strings.ToLower(p.Path)
	return t.dock[p.Pid] || path != "" && slices.ContainsFunc(t.bundles, func(b string) bool { return within(path, b) })
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
	for pid, p := range t.procs {
		if _, claude := t.claude[pid]; pid > 1 && p.UID == s.UID && !claude && !family[pid] && !t.inDock(p) {
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

// placeDetached decides once, for all the detached candidates pids that
// carry id, whether the session id is open, closed or uncertain, and puts
// each candidate it lists into places. carriers are the candidates carrying
// id that are attached to a session or detached, pids among them. id is open
// when a Claude Code process has it now or a CLAUDE_PID among carriers names
// a Claude Code process; it is uncertain when closable says no session id
// may be called closed, or when a CLAUDE_PID among pids is held by a process
// older than the candidate carrying it. It returns how many candidates it
// leaves uncertain.
func (t *claudeTable) placeDetached(id string, pids, carriers []int32, closable bool, places map[int32]claudePlace) (uncertain int) {
	named := map[int32]bool{} // the sessions the CLAUDE_PIDs among carriers point into
	for _, pid := range carriers {
		if head, isClaude := t.claude[t.procs[pid].Env.Pid]; isClaude {
			named[head] = true
		}
	}
	older := false
	for _, pid := range pids {
		p := t.procs[pid]
		if _, isClaude := t.claude[p.Env.Pid]; !isClaude && t.startedBefore(p.Env.Pid, p.Start) {
			older = true
		}
	}
	current, isCurrent := t.currentSession(id)
	heads := slices.Collect(maps.Keys(named))
	switch {
	case isCurrent || len(heads) > 0:
		for _, pid := range pids {
			if head, isClaude := t.claude[t.procs[pid].Env.Pid]; isClaude {
				places[pid] = claudePlace{open: head}
			} else if isCurrent {
				places[pid] = claudePlace{open: current}
			} else if len(heads) == 1 {
				places[pid] = claudePlace{open: heads[0]}
			} else {
				uncertain++
			}
		}
	case !closable || older:
		uncertain = len(pids)
	default:
		for _, pid := range pids {
			places[pid] = claudePlace{closed: id}
		}
	}
	return uncertain
}

// currentSession finds the session of the oldest Claude Code process, a
// session's own or a sub-agent, whose session id is id now. It returns the
// Claude Code process heading that session; ok is false when there is none.
func (t *claudeTable) currentSession(id string) (head int32, ok bool) {
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
// row of its process group. The parents of a listed process never loop, so
// every fold ends at the lead of a row.
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

// maskCommand is args as one line for display, with leading assignments
// dropped and every other assignment, secret value and URL credential
// hidden.
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
	maskWords(words, false)
	return oneLine(strings.Join(words, " "), claudeCommandWidth)
}

// maskWords hides, in place, what words must not show. Each word is one
// argument as written: a NAME=value becomes NAME=…; a flag whose name says
// its value is secret hides that value, after its "=" or as the whole next
// word, whatever that looks like; a flag whose value is a NAME=value hides
// the value of that; a NAME=value glued to a shell operator inside a word,
// or inside what a quoted word quotes, hides its value; an argument holding
// white space is a script, masked word by word; and every URL hides its
// credentials. Every test reads a word as written, with its quotes taken
// out, never what masking made of it; only a quoted script is read once the
// values glued to its operators are hidden. secret says the word before
// words is a secret flag waiting for its value; maskWords reports whether
// the last of words is one.
func maskWords(words []string, secret bool) bool {
	for i, word := range words {
		text := shellValue(word)
		name, value, inline := strings.Cut(text, "=")
		flag := strings.HasPrefix(name, "-")
		next := flag && !inline && secretFlag(name)
		switch {
		case secret:
			words[i] = "…"
		case isAssignment(text):
			words[i] = name + "=…"
		case flag && inline && secretFlag(name):
			words[i] = name + "=…"
		case flag && isAssignment(value):
			variable, _, _ := strings.Cut(value, "=")
			words[i] = name + "=" + variable + "=…"
		case strings.ContainsAny(text, shellBlanks):
			script, parts := word, shellWords(word)
			if len(parts) == 1 {
				// One quoted word: what it quotes is the script. A quoted
				// value glued to an operator outside the quotes is hidden
				// first, so its white space splits nothing.
				script = maskGlued(word)
				parts = shellWords(shellValue(script))
			}
			masked := slices.Clone(parts)
			next = maskWords(masked, false) || next
			if script != word || !slices.Equal(masked, parts) {
				words[i] = strings.Join(masked, " ")
			}
		default:
			words[i] = maskGlued(word)
			if quoted := shellValue(words[i]); quoted != words[i] {
				if masked := maskGlued(quoted); masked != quoted {
					words[i] = masked // what a quoted word quotes may be a script
				}
			}
		}
		words[i] = hideCredentials(words[i])
		secret = next
	}
	return secret
}

// shellBlanks are the characters that end an unquoted shell word.
const shellBlanks = " \t\n\r\v\f"

// shellOperators are the shell operators an assignment inside a word can
// directly follow, and shellValueEnds the characters that end its value.
const (
	shellOperators = ";&|({`"
	shellValueEnds = shellBlanks + ";&|)}`"
)

// shellWords splits s into words where the shell would, at white space
// outside quotes, keeping each word as written. A backslash ending a line
// joins the lines, so between words it is no word of its own.
func shellWords(s string) []string {
	var words []string
	for {
		s = strings.TrimLeft(s, shellBlanks)
		if rest, joined := strings.CutPrefix(s, "\\\n"); joined {
			s = rest
			continue
		}
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
	i := 0
	for i < len(s) && strings.IndexByte(shellBlanks, s[i]) < 0 {
		i += shellUnitLen(s[i:])
	}
	return i
}

// shellUnitLen is the length of what the non-empty s starts with as the
// shell reads it: a backslash with the byte it escapes, a quoted run with
// both its quotes or, when the quote is left open, all of s, or else one
// byte.
func shellUnitLen(s string) int {
	switch s[0] {
	case '\\':
		return min(2, len(s))
	case '\'':
		if end := strings.IndexByte(s[1:], '\''); end >= 0 {
			return end + 2
		}
		return len(s)
	case '"':
		for i := 1; i < len(s); i++ {
			switch s[i] {
			case '\\':
				i++
			case '"':
				return i + 1
			}
		}
		return len(s)
	}
	return 1
}

// maskGlued is word with the value of every NAME=value that directly follows
// ;, &, |, (, { or a backtick made "…", as in cd web&&API_KEY=…: the value
// runs to the next white space or ;, &, |, ), } or backtick. Only what stands
// outside quotes and backslash escapes counts as either.
func maskGlued(word string) string {
	var b strings.Builder
	for i := 0; i < len(word); {
		n := shellUnitLen(word[i:])
		b.WriteString(word[i : i+n])
		operator := n == 1 && strings.IndexByte(shellOperators, word[i]) >= 0
		i += n
		if !operator {
			continue
		}
		if name := assignedName(word[i:]); name != "" {
			b.WriteString(name + "=…")
			for i += len(name) + 1; i < len(word) && strings.IndexByte(shellValueEnds, word[i]) < 0; {
				i += shellUnitLen(word[i:])
			}
		}
	}
	return b.String()
}

// shellValue is the shell word word with its quotes and backslash escapes
// taken out, as the shell passes it on; a quote left open runs to the end.
func shellValue(word string) string {
	var b strings.Builder
	for i := 0; i < len(word); i++ {
		switch c := word[i]; c {
		case '\\':
			if i++; i < len(word) && word[i] != '\n' {
				b.WriteByte(word[i])
			}
		case '\'':
			end := strings.IndexByte(word[i+1:], '\'')
			if end < 0 {
				end = len(word) - i - 1
			}
			b.WriteString(word[i+1 : i+1+end])
			i += end + 1
		case '"':
			for i++; i < len(word) && word[i] != '"'; i++ {
				if word[i] == '\\' && i+1 < len(word) && strings.IndexByte("$`\"\\\n", word[i+1]) >= 0 {
					if i++; word[i] == '\n' {
						continue
					}
				}
				b.WriteByte(word[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// hideCredentials is word with the credentials of every URL in it hidden:
// what comes between "://" and the last "@" before the next "/" or white
// space becomes "…".
func hideCredentials(word string) string {
	var b strings.Builder
	for {
		start := strings.Index(word, "://") // slash-ok: URL syntax, not a path
		if start < 0 {
			b.WriteString(word)
			return b.String()
		}
		b.WriteString(word[:start+3])
		word = word[start+3:]
		end := strings.IndexAny(word, "/"+shellBlanks) // slash-ok: URL syntax, not a path
		if end < 0 {
			end = len(word)
		}
		if at := strings.LastIndexByte(word[:end], '@'); at >= 0 {
			b.WriteString("…")
			word = word[at:]
		}
	}
}

// isAssignment reports whether word sets a shell variable: NAME=value.
func isAssignment(word string) bool {
	return assignedName(word) != ""
}

// assignedName is the NAME of the NAME=value s starts with, letters, digits
// and underscores not starting with a digit, or "" when s starts with none.
func assignedName(s string) string {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '=':
			return s[:i]
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return ""
		}
	}
	return ""
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
