package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/klauspost/compress/zstd"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Agent skills
// ---------------------------------------------------------------------------
//
// AI coding tools load "skills" (a folder with a SKILL.md) from their own
// skills folder; ~/.agents/skills is shared, and the other folders often
// link into it. The page lists every skill once, with the copies each tool
// has, problems (broken links, missing SKILL.md, copies that are the same or
// have drifted apart) and, where a tool keeps a usable record, when the skill
// was last called.
//
// Skills are written or installed by the user and do not regrow, so nothing
// is preselected and removal only moves things to the Trash. A folder other
// skills folders link to is not moved unless the links go with it.
//
// Usage comes from the tools' own transcripts, read only:
//
//   claude    ~/.claude/projects/**/*.jsonl, the Skill tool's calls and
//             slash commands named after a skill
//   codex     ~/.codex/sessions and archived_sessions, tool calls that read
//             a skills/<name>/SKILL.md (the skill list Codex puts in its
//             prompt is not a call and is left out)
//   opencode  ~/.local/share/opencode/opencode.db, the skill tool's calls and
//             reads of a SKILL.md, through the system sqlite3
//
// Codex's archived sessions are compressed (.jsonl.zst); they are decoded
// with the built-in zstd reader, so its records reach back for months.
//
// Other tools keep no record that can be read; their skills show no usage
// data rather than "never used". Transcripts are kept for a limited time
// (Claude Code: 30 days by default), so every source reports how far back it
// reaches.

// skillRoot is one tool's skills folder.
type skillRoot struct {
	Tool string // id; also the usage source that reads it
	Name string
	Rel  string // relative to the home folder
}

// skillRoots lists the skills folders Cleanzo knows. ~/.agents/skills is
// read by Codex as well as by tools that link to it.
var skillRoots = []skillRoot{
	{Tool: "agents", Name: "~/.agents", Rel: ".agents/skills"},
	{Tool: "claude", Name: "Claude Code", Rel: ".claude/skills"},
	{Tool: "codex", Name: "Codex", Rel: ".codex/skills"},
	{Tool: "gemini", Name: "Gemini CLI", Rel: ".gemini/skills"},
	{Tool: "opencode", Name: "OpenCode", Rel: ".config/opencode/skills"},
	{Tool: "grok", Name: "Grok", Rel: ".grok/skills"},
	{Tool: "hermes", Name: "Hermes", Rel: ".hermes/skills"},
	{Tool: "pi", Name: "Pi", Rel: ".pi/agent/skills"},
}

// skillReaders maps a skills folder to the usage sources that would see a
// skill in it being called.
var skillReaders = map[string][]string{
	"agents":   {"codex", "opencode"},
	"claude":   {"claude", "opencode"},
	"codex":    {"codex"},
	"opencode": {"opencode"},
}

// skillIdleWindow is the least time the records must cover before a skill
// that was not called in it counts as idle; a few days without a call says
// little.
const skillIdleWindow = 14 * 24 * time.Hour

// skillHashLimit bounds how much of a skill is read to compare copies;
// bigger skills are not compared (and not offered for linking).
const skillHashLimit = 64 << 20

// SkillRoot is a skills folder that exists, as the page lists it.
type SkillRoot struct {
	Tool  string `json:"tool"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Count int    `json:"count"` // entries, links included
}

// SkillCopy is one entry for a skill in one skills folder.
type SkillCopy struct {
	Tool      string   `json:"tool"` // the folder's tool
	Path      string   `json:"path"` // the entry, not resolved
	Kind      string   `json:"kind"` // dir, link, broken, invalid (a folder without SKILL.md)
	Target    string   `json:"target"`
	Real      string   `json:"real"` // resolved folder; "" for broken links
	Size      int64    `json:"size"` // on disk; 0 for links
	Hash      string   `json:"hash"` // contents of Real; "" when not compared
	Modified  int64    `json:"modified"`
	LinkedBy  []string `json:"linkedBy"`  // entries in skills folders that link to this folder
	Removable bool     `json:"removable"` // passes the safety checks
}

// SkillUse is a skill's recorded calls in one tool.
type SkillUse struct {
	Tool  string `json:"tool"`
	Count int    `json:"count"`
	Last  int64  `json:"last"` // unix seconds
}

// Skill is one skill name across the skills folders.
type Skill struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Copies      []SkillCopy `json:"copies"`
	Size        int64       `json:"size"`      // distinct folders, on disk
	Installed   int64       `json:"installed"` // oldest entry's modification time
	Uses        []SkillUse  `json:"uses"`
	Last        int64       `json:"last"`     // newest call in any tool; 0 for none
	Count       int         `json:"count"`    // calls in all tools
	Observed    bool        `json:"observed"` // a tool with usage records can see it
	Since       int64       `json:"since"`    // all those tools' records reach back to here
	Idle        bool        `json:"idle"`     // observed, installed before Since, not called since, and Since is skillIdleWindow ago or more
	Issues      []string    `json:"issues"`   // broken, invalid, duplicate, diverged
}

// SkillSource reports one usage source.
type SkillSource struct {
	Tool    string `json:"tool"`
	Name    string `json:"name"`
	Status  string `json:"status"`  // ok, none (no records), unsupported, error
	Since   int64  `json:"since"`   // records reach back at least to here
	Records int    `json:"records"` // calls found
}

// SkillScanResult is what ScanSkills returns.
type SkillScanResult struct {
	Roots     []SkillRoot   `json:"roots"`
	Skills    []Skill       `json:"skills"`
	Sources   []SkillSource `json:"sources"`
	ScannedAt int64         `json:"scannedAt"`
}

// skillNow is the clock; tests replace it.
var skillNow = time.Now

// ListSkillRoots reports the skills folders that exist, without measuring
// or reading transcripts, so the sidebar can show the page at start.
func (Cleaner) ListSkillRoots() []SkillRoot {
	out := []SkillRoot{}
	for _, r := range skillRoots {
		path := filepath.Join(homeDir(), r.Rel)
		entries, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		n := 0
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") {
				n++
			}
		}
		out = append(out, SkillRoot{Tool: r.Tool, Name: r.Name, Path: path, Count: n})
	}
	return out
}

// ScanSkills lists the skills, their copies and problems, and their usage.
func (c Cleaner) ScanSkills(ctx context.Context) (SkillScanResult, error) {
	res := SkillScanResult{Roots: c.ListSkillRoots(), Skills: []Skill{}, Sources: []SkillSource{}, ScannedAt: skillNow().Unix()}
	copies, err := skillCopies(ctx, res.Roots)
	if err != nil {
		return SkillScanResult{}, err
	}
	sources, uses := skillUsage(ctx, res.Roots)
	if ctx.Err() != nil {
		return SkillScanResult{}, ctx.Err()
	}
	res.Sources = sources
	ok := map[string]bool{}
	since := map[string]int64{}
	for _, s := range sources {
		ok[s.Tool] = s.Status == "ok"
		since[s.Tool] = s.Since
	}

	byName := map[string]*Skill{}
	var names []string
	for _, cp := range copies {
		name := filepath.Base(cp.Path)
		s := byName[name]
		if s == nil {
			s = &Skill{Name: name, Copies: []SkillCopy{}, Uses: []SkillUse{}, Issues: []string{}}
			byName[name] = s
			names = append(names, name)
		}
		s.Copies = append(s.Copies, cp)
	}
	sort.Strings(names)
	for _, name := range names {
		s := byName[name]
		reals := map[string]string{} // folder -> hash
		for _, cp := range s.Copies {
			if s.Installed == 0 || cp.Modified < s.Installed {
				s.Installed = cp.Modified
			}
			switch cp.Kind {
			case "broken":
				addIssue(s, "broken")
			case "invalid":
				addIssue(s, "invalid")
			}
			if cp.Real != "" {
				if _, seen := reals[cp.Real]; !seen {
					reals[cp.Real] = cp.Hash
					s.Size += cp.Size
				}
			}
			if s.Description == "" && cp.Real != "" {
				s.Description = skillDescription(filepath.Join(cp.Real, "SKILL.md"))
			}
			// Several tools may see a skill; "not called" holds only for the
			// time all their records cover, which starts at the latest start.
			for _, reader := range skillReaders[cp.Tool] {
				if ok[reader] {
					s.Observed = true
					s.Since = max(s.Since, since[reader])
				}
			}
		}
		if len(reals) > 1 {
			hashes := map[string]bool{}
			for _, h := range reals {
				hashes[h] = true
			}
			if len(hashes) == 1 && !hashes[""] {
				addIssue(s, "duplicate")
			} else {
				addIssue(s, "diverged")
			}
		}
		for _, tool := range []string{"claude", "codex", "opencode"} {
			u := uses[tool][name]
			if u.Count == 0 {
				continue
			}
			s.Uses = append(s.Uses, SkillUse{Tool: tool, Count: u.Count, Last: u.Last})
			s.Count += u.Count
			s.Last = max(s.Last, u.Last)
		}
		s.Idle = s.Observed && s.Count == 0 && s.Since > 0 && s.Installed > 0 && s.Installed < s.Since &&
			res.ScannedAt-s.Since >= int64(skillIdleWindow/time.Second)
		res.Skills = append(res.Skills, *s)
	}
	return res, nil
}

func addIssue(s *Skill, issue string) {
	if !slices.Contains(s.Issues, issue) {
		s.Issues = append(s.Issues, issue)
	}
}

// skillCopies reads the entries of every skills folder.
func skillCopies(ctx context.Context, roots []SkillRoot) ([]SkillCopy, error) {
	var copies []SkillCopy
	hashes := map[string]string{}
	sizes := map[string]int64{}
	for _, r := range roots {
		entries, err := os.ReadDir(r.Path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			path := filepath.Join(r.Path, e.Name())
			info, err := os.Lstat(path)
			if err != nil {
				continue
			}
			cp := SkillCopy{Tool: r.Tool, Path: path, Modified: info.ModTime().Unix(), LinkedBy: []string{}}
			if info.Mode()&fs.ModeSymlink != 0 {
				cp.Kind = "link"
				cp.Target, _ = os.Readlink(path)
				if st, err := os.Stat(path); err != nil || !st.IsDir() {
					cp.Kind = "broken"
				} else {
					cp.Real = resolvePath(path)
				}
			} else if info.IsDir() {
				cp.Kind = "dir"
				cp.Real = resolvePath(path)
			} else {
				continue // a stray file is not a skill
			}
			if cp.Real != "" {
				if _, err := os.Stat(filepath.Join(cp.Real, "SKILL.md")); err != nil {
					cp.Kind = map[string]string{"dir": "invalid", "link": "broken"}[cp.Kind]
				}
				if _, done := hashes[cp.Real]; !done {
					hashes[cp.Real], sizes[cp.Real] = skillHash(ctx, cp.Real)
				}
				cp.Hash = hashes[cp.Real]
				if cp.Kind == "dir" || cp.Kind == "invalid" {
					cp.Size = sizes[cp.Real]
				}
			}
			cp.Removable = skillEntryOK(path) == nil
			copies = append(copies, cp)
		}
	}
	// Who links to which folder.
	for i := range copies {
		if copies[i].Kind != "dir" && copies[i].Kind != "invalid" {
			continue
		}
		for _, other := range copies {
			if other.Kind == "link" && other.Real == copies[i].Real && other.Path != copies[i].Path {
				copies[i].LinkedBy = append(copies[i].LinkedBy, other.Path)
			}
		}
	}
	return copies, nil
}

// skillHash fingerprints a skill folder: its files' relative paths and
// contents and its links' targets, without following links. It returns ""
// when the folder is larger than skillHashLimit or cannot be read.
func skillHash(ctx context.Context, dir string) (string, int64) {
	h := sha256.New()
	var size, read int64
	failed := false
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			failed = true
			return nil
		}
		if d.Name() == ".DS_Store" || p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		info, err := d.Info()
		if err != nil {
			failed = true
			return nil
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			h.Write([]byte("L\x00" + rel + "\x00" + target + "\x00"))
		case d.IsDir():
			h.Write([]byte("D\x00" + rel + "\x00"))
		case d.Type().IsRegular():
			size += onDisk(info)
			read += info.Size()
			if read > skillHashLimit || failed {
				failed = true
				return nil
			}
			f, err := os.Open(p)
			if err != nil {
				failed = true
				return nil
			}
			h.Write([]byte("F\x00" + rel + "\x00"))
			_, err = io.Copy(h, f)
			f.Close()
			if err != nil {
				failed = true
			}
			h.Write([]byte{0})
		}
		return nil
	})
	if failed {
		return "", size
	}
	return hex.EncodeToString(h.Sum(nil)), size
}

// skillDescription reads the description from a SKILL.md's front matter.
func skillDescription(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 16*1024), 1024*1024)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return ""
	}
	var desc []string
	in := false
	for i := 0; i < 200 && sc.Scan(); i++ {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if in {
			if line != "" && (line[0] == ' ' || line[0] == '\t') {
				desc = append(desc, strings.TrimSpace(line))
				continue
			}
			break
		}
		if v, ok := strings.CutPrefix(line, "description:"); ok {
			v = strings.TrimSpace(v)
			if v == "" || v == ">" || v == "|" || v == ">-" || v == "|-" {
				in = true
				continue
			}
			desc = append(desc, strings.Trim(v, `"'`))
			break
		}
	}
	s := strings.Join(desc, " ")
	if r := []rune(s); len(r) > 240 {
		s = string(r[:240]) + "…"
	}
	return s
}

// ---------------------------------------------------------------------------
// Usage
// ---------------------------------------------------------------------------

type skillTally struct {
	Count int
	Last  int64
}

// skillUsage reads the usage sources for the tools whose skills folders
// exist, or whose records do.
func skillUsage(ctx context.Context, roots []SkillRoot) ([]SkillSource, map[string]map[string]skillTally) {
	present := map[string]bool{}
	for _, r := range roots {
		present[r.Tool] = true
	}
	home := homeDir()
	uses := map[string]map[string]skillTally{}
	var sources []SkillSource

	type reader struct {
		tool, name string
		read       func(context.Context) (map[string]skillTally, int64, int, error)
		has        bool
	}
	readers := []reader{
		{"claude", "Claude Code", claudeSkillUsage, fileExists(filepath.Join(home, ".claude/projects"))},
		{"codex", "Codex", codexSkillUsage, fileExists(filepath.Join(home, ".codex/sessions")) || fileExists(filepath.Join(home, ".codex/archived_sessions"))},
		{"opencode", "OpenCode", opencodeSkillUsage, fileExists(opencodeDB())},
	}
	for _, rd := range readers {
		if !rd.has {
			if present[rd.tool] {
				sources = append(sources, SkillSource{Tool: rd.tool, Name: rd.name, Status: "none"})
			}
			continue
		}
		tally, since, records, err := rd.read(ctx)
		s := SkillSource{Tool: rd.tool, Name: rd.name, Status: "ok", Since: since, Records: records}
		if err != nil {
			s.Status = "error"
		}
		uses[rd.tool] = tally
		sources = append(sources, s)
	}
	for _, r := range roots {
		if !slices.ContainsFunc(sources, func(s SkillSource) bool { return s.Tool == r.Tool }) && r.Tool != "agents" {
			sources = append(sources, SkillSource{Tool: r.Tool, Name: r.Name, Status: "unsupported"})
		}
	}
	return sources, uses
}

func (t skillTally) add(ts int64) skillTally {
	t.Count++
	t.Last = max(t.Last, ts)
	return t
}

// parseTS reads an RFC 3339 timestamp; 0 when it is not one.
func parseTS(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// transcriptKind is how a transcript file is stored.
type transcriptKind int

const (
	transcriptPlain transcriptKind = iota
	transcriptZstd
)

// transcriptFile is one transcript file, plain or zstd-compressed.
type transcriptFile struct {
	path string
	kind transcriptKind
}

// jsonlFiles lists the .jsonl and .jsonl.zst transcript files under dirs and
// the oldest modification time among them, which the records reach back to
// at least. Compressed Codex archives are decoded with the built-in zstd
// reader; no command-line tool is needed.
func jsonlFiles(dirs ...string) (files []transcriptFile, since int64) {
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			kind := transcriptPlain
			switch {
			case strings.HasSuffix(p, ".jsonl"):
			case strings.HasSuffix(p, ".jsonl.zst"):
				kind = transcriptZstd
			default:
				return nil
			}
			files = append(files, transcriptFile{path: p, kind: kind})
			if info, err := d.Info(); err == nil {
				if m := info.ModTime().Unix(); since == 0 || m < since {
					since = m
				}
			}
			return nil
		})
	}
	return files, since
}

// transcriptReader opens a transcript, decompressing .jsonl.zst archives.
func transcriptReader(ctx context.Context, f transcriptFile) (io.ReadCloser, error) {
	file, err := os.Open(f.path)
	if err != nil {
		return nil, err
	}
	if f.kind == transcriptPlain {
		return file, nil
	}
	zr, err := zstd.NewReader(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	return &zstdFileReader{Decoder: zr, file: file}, nil
}

// zstdFileReader closes the decoder and the file.
type zstdFileReader struct {
	*zstd.Decoder
	file *os.File
}

func (z *zstdFileReader) Close() error {
	z.Decoder.Close()
	return z.file.Close()
}

// eachLine calls fn for each line of a transcript that contains one of the
// needles. Lines of any length are read.
func eachLine(ctx context.Context, f transcriptFile, needles [][]byte, fn func([]byte)) {
	if ctx.Err() != nil {
		return
	}
	r, err := transcriptReader(ctx, f)
	if err != nil {
		return
	}
	defer r.Close()
	br := bufio.NewReaderSize(r, 256*1024)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 && slices.ContainsFunc(needles, func(n []byte) bool { return bytes.Contains(line, n) }) {
			fn(line)
		}
		if err != nil {
			return
		}
	}
}

func pathsOf(files []transcriptFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.path
	}
	return out
}

func fileByName(files []transcriptFile, path string) transcriptFile {
	for _, f := range files {
		if f.path == path {
			return f
		}
	}
	return transcriptFile{path: path}
}

// parallelFiles runs fn over files on a few workers.
func parallelFiles(ctx context.Context, files []string, fn func(string)) {
	ch := make(chan string)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range ch {
				fn(p)
			}
		}()
	}
	for _, p := range files {
		if ctx.Err() != nil {
			break
		}
		ch <- p
	}
	close(ch)
	wg.Wait()
}

// skillCall is one recorded call, keyed so a call copied into another
// transcript (a resumed session) counts once.
type skillCall struct {
	Key, Name string
	TS        int64
}

func tallyCalls(calls []skillCall) (map[string]skillTally, int) {
	seen := map[string]bool{}
	out := map[string]skillTally{}
	n := 0
	for _, c := range calls {
		if c.Key != "" {
			if seen[c.Key] {
				continue
			}
			seen[c.Key] = true
		}
		out[c.Name] = out[c.Name].add(c.TS)
		n++
	}
	return out, n
}

var commandName = regexp.MustCompile(`<command-name>/?([A-Za-z0-9._:-]+)</command-name>`)

// claudeSkillUsage reads Claude Code's transcripts: the Skill tool's calls
// and the slash commands the user typed (a skill is also a command; built-in
// commands never match a skill name).
func claudeSkillUsage(ctx context.Context) (map[string]skillTally, int64, int, error) {
	files, since := jsonlFiles(filepath.Join(homeDir(), ".claude/projects"))
	var mu sync.Mutex
	var calls []skillCall
	needles := [][]byte{[]byte(`"name":"Skill"`), []byte(`<command-name>`)}
	parallelFiles(ctx, pathsOf(files), func(p string) {
		var local []skillCall
		f := fileByName(files, p)
		eachLine(ctx, f, needles, func(line []byte) {
			var rec struct {
				Type      string `json:"type"`
				UUID      string `json:"uuid"`
				Timestamp string `json:"timestamp"`
				Message   struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &rec) != nil {
				return
			}
			ts := parseTS(rec.Timestamp)
			switch rec.Type {
			case "assistant":
				var blocks []struct {
					Type  string `json:"type"`
					ID    string `json:"id"`
					Name  string `json:"name"`
					Input struct {
						Skill   string `json:"skill"`
						Command string `json:"command"`
					} `json:"input"`
				}
				if json.Unmarshal(rec.Message.Content, &blocks) != nil {
					return
				}
				for _, b := range blocks {
					if b.Type != "tool_use" || b.Name != "Skill" {
						continue
					}
					name := b.Input.Skill
					if name == "" {
						name = b.Input.Command
					}
					if name = strings.TrimPrefix(name, "/"); name != "" {
						local = append(local, skillCall{Key: b.ID, Name: name, TS: ts})
					}
				}
			case "user":
				var text string
				if json.Unmarshal(rec.Message.Content, &text) != nil {
					return
				}
				if m := commandName.FindStringSubmatch(text); m != nil {
					local = append(local, skillCall{Key: "cmd:" + rec.UUID, Name: m[1], TS: ts})
				}
			}
		})
		mu.Lock()
		calls = append(calls, local...)
		mu.Unlock()
	})
	if ctx.Err() != nil {
		return nil, 0, 0, ctx.Err()
	}
	tally, n := tallyCalls(calls)
	return tally, since, n, nil
}

// skillPathRef matches an absolute or ~ path to a SKILL.md; JSON escapes
// a path's slashes at most as \/.
var skillPathRef = regexp.MustCompile(`((?:~|/)[^"'\s\\]*)/([A-Za-z0-9._-]+)/SKILL\.md`)

// knownSkillRef returns the skill a path to a SKILL.md names when it sits in
// one of the known skills folders; plugin and memory skills that happen to
// share a name are not the user's.
func knownSkillRef(s string, roots map[string]bool) []string {
	var out []string
	home := homeDir()
	for _, m := range skillPathRef.FindAllStringSubmatch(strings.ReplaceAll(s, `\/`, `/`), -1) {
		dir := m[1]
		if strings.HasPrefix(dir, "~") {
			dir = home + strings.TrimPrefix(dir, "~")
		}
		if roots[filepath.Clean(dir)] && !slices.Contains(out, m[2]) {
			out = append(out, m[2])
		}
	}
	return out
}

// skillRootDirs returns the known skills folders, as written and resolved.
func skillRootDirs() map[string]bool {
	out := map[string]bool{}
	for _, r := range skillRoots {
		p := filepath.Join(homeDir(), r.Rel)
		out[p] = true
		out[resolvePath(p)] = true
	}
	return out
}

// codexSkillUsage reads Codex's rollouts: tool calls that read a SKILL.md.
// Only the model's own tool calls count; the skill list in the prompt and
// tool output do not.
func codexSkillUsage(ctx context.Context) (map[string]skillTally, int64, int, error) {
	home := homeDir()
	files, since := jsonlFiles(filepath.Join(home, ".codex/sessions"), filepath.Join(home, ".codex/archived_sessions"))
	var mu sync.Mutex
	var calls []skillCall
	needles := [][]byte{[]byte("SKILL.md")}
	roots := skillRootDirs()
	parallelFiles(ctx, pathsOf(files), func(p string) {
		var local []skillCall
		f := fileByName(files, p)
		eachLine(ctx, f, needles, func(line []byte) {
			var rec struct {
				Timestamp string `json:"timestamp"`
				Type      string `json:"type"`
				Payload   struct {
					Type      string          `json:"type"`
					CallID    string          `json:"call_id"`
					Arguments json.RawMessage `json:"arguments"`
					Input     json.RawMessage `json:"input"`
					Action    json.RawMessage `json:"action"`
				} `json:"payload"`
			}
			if json.Unmarshal(line, &rec) != nil || rec.Type != "response_item" {
				return
			}
			switch rec.Payload.Type {
			case "function_call", "custom_tool_call", "local_shell_call":
			default:
				return
			}
			args := string(rec.Payload.Arguments) + string(rec.Payload.Input) + string(rec.Payload.Action)
			for _, name := range knownSkillRef(args, roots) {
				key := ""
				if rec.Payload.CallID != "" {
					key = rec.Payload.CallID + "/" + name // slash-ok: logical transcript call IDs, not filesystem paths
				}
				local = append(local, skillCall{Key: key, Name: name, TS: parseTS(rec.Timestamp)})
			}
		})
		mu.Lock()
		calls = append(calls, local...)
		mu.Unlock()
	})
	if ctx.Err() != nil {
		return nil, 0, 0, ctx.Err()
	}
	tally, n := tallyCalls(calls)
	return tally, since, n, nil
}

func opencodeDB() string {
	return filepath.Join(homeDir(), ".local/share/opencode/opencode.db")
}

// opencodeQuery runs a read-only query with the system sqlite3; tests
// replace it.
var opencodeQuery = func(ctx context.Context, db, query string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/bin/sqlite3", "-readonly", "-json", db, query).Output()
}

// opencodeSkillUsage reads OpenCode's database: the skill tool's calls and
// reads of a SKILL.md.
func opencodeSkillUsage(ctx context.Context) (map[string]skillTally, int64, int, error) {
	db := opencodeDB()
	var since int64
	var first []struct {
		Min *int64 `json:"m"`
	}
	out, err := opencodeQuery(ctx, db, "select min(time_created) as m from part")
	if err != nil {
		return map[string]skillTally{}, 0, 0, err
	}
	if json.Unmarshal(out, &first) == nil && len(first) > 0 && first[0].Min != nil {
		since = *first[0].Min / 1000
	}
	out, err = opencodeQuery(ctx, db, `select id, time_created as t, data from part where data like '%"tool":"skill"%' or data like '%SKILL.md%'`)
	if err != nil {
		return map[string]skillTally{}, since, 0, err
	}
	var rows []struct {
		ID   string `json:"id"`
		T    int64  `json:"t"`
		Data string `json:"data"`
	}
	if len(bytes.TrimSpace(out)) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			return map[string]skillTally{}, since, 0, err
		}
	}
	var calls []skillCall
	roots := skillRootDirs()
	for _, row := range rows {
		var part struct {
			Type  string `json:"type"`
			Tool  string `json:"tool"`
			State struct {
				Input map[string]any `json:"input"`
			} `json:"state"`
		}
		if json.Unmarshal([]byte(row.Data), &part) != nil || part.Type != "tool" {
			continue
		}
		ts := row.T / 1000
		if part.Tool == "skill" {
			for _, k := range []string{"name", "skill"} {
				if v, ok := part.State.Input[k].(string); ok && v != "" {
					calls = append(calls, skillCall{Key: row.ID, Name: v, TS: ts})
					break
				}
			}
			continue
		}
		in, _ := json.Marshal(part.State.Input)
		for _, name := range knownSkillRef(string(in), roots) {
			calls = append(calls, skillCall{Key: row.ID + "/" + name, Name: name, TS: ts}) // slash-ok: logical transcript call IDs, not filesystem paths
		}
	}
	tally, n := tallyCalls(calls)
	return tally, since, n, nil
}

// ---------------------------------------------------------------------------
// Removing and linking
// ---------------------------------------------------------------------------

// skillGuard snapshots what is in use before a skill moves; tests replace
// openFilesHook to skip lsof.
var openFilesHook func() ([]string, error)

func skillGuard() *usageGuard {
	if openFilesHook != nil {
		open, err := openFilesHook()
		if err != nil {
			return &usageGuard{failed: true}
		}
		return &usageGuard{open: open, running: map[string]bool{}}
	}
	return probeUsage()
}

var (
	errSkillNotEntry = errors.New("not-a-skill")
	errSkillLinked   = errors.New("linked")
	errSkillDiverged = errors.New("diverged")
	errSkillInUse    = errors.New("in-use")
)

// skillRootOf returns the known skills folder path is a direct entry of.
func skillRootOf(path string) (skillRoot, string, bool) {
	clean := filepath.Clean(path)
	parent := resolvePath(filepath.Dir(clean))
	for _, r := range skillRoots {
		root := filepath.Join(homeDir(), r.Rel)
		if fileExists(root) && resolvePath(root) == parent {
			return r, root, true
		}
	}
	return skillRoot{}, "", false
}

// skillEntryOK checks that path is an entry of a skills folder that may be
// moved: a direct, visible entry, inside the cleanable part of the home
// folder.
func skillEntryOK(path string) error {
	if !filepath.IsAbs(path) {
		return errSkillNotEntry
	}
	clean := filepath.Clean(path)
	base := filepath.Base(clean)
	if base == "" || strings.HasPrefix(base, ".") {
		return errSkillNotEntry
	}
	if _, _, ok := skillRootOf(clean); !ok {
		return errSkillNotEntry
	}
	if _, err := os.Lstat(clean); err != nil {
		return errSkillNotEntry
	}
	// The entry itself is judged where it sits; a link's target is not moved.
	if !safeToClean(filepath.Dir(clean)) || !safeResolved(strings.ToLower(filepath.Join(resolvePath(filepath.Dir(clean)), base))) {
		return errSkillNotEntry
	}
	return nil
}

// skillLinksTo lists the links in the skills folders that resolve to real.
func skillLinksTo(real string) []string {
	var out []string
	for _, r := range skillRoots {
		root := filepath.Join(homeDir(), r.Rel)
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.Type()&fs.ModeSymlink == 0 {
				continue
			}
			p := filepath.Join(root, e.Name())
			if st, err := os.Stat(p); err == nil && st.IsDir() && resolvePath(p) == real {
				out = append(out, p)
			}
		}
	}
	return out
}

// SkillTrashItem reports one entry's outcome.
type SkillTrashItem struct {
	Path  string `json:"path"`
	Error string `json:"error"` // "" when moved; not-a-skill, linked, in-use or the Trash's error
	Freed int64  `json:"freed"`
}

// SkillTrashResult is what TrashSkills returns.
type SkillTrashResult struct {
	Items []SkillTrashItem `json:"items"`
	Moved int              `json:"moved"`
	Freed int64            `json:"freed"`
}

// TrashSkills moves skill entries to the Trash. A link moves as a link; its
// target stays. A folder that a skills folder links to is moved only when
// all those links are moved with it, so no tool is left with a broken link.
// Folders with files a process has open are left alone.
func (Cleaner) TrashSkills(paths []string) SkillTrashResult {
	res := SkillTrashResult{Items: []SkillTrashItem{}}
	moving := map[string]bool{}
	for _, p := range paths {
		moving[filepath.Clean(p)] = true
	}
	// Links first, so a folder's links are gone before it moves.
	ordered := slices.Clone(paths)
	sort.SliceStable(ordered, func(i, j int) bool { return isLink(ordered[i]) && !isLink(ordered[j]) })
	var guard *usageGuard
	for _, p := range ordered {
		p = filepath.Clean(p)
		item := SkillTrashItem{Path: p}
		if err := skillEntryOK(p); err != nil {
			item.Error = err.Error()
			res.Items = append(res.Items, item)
			continue
		}
		if !isLink(p) {
			real := resolvePath(p)
			for _, l := range skillLinksTo(real) {
				if !moving[l] {
					item.Error = errSkillLinked.Error()
				}
			}
			if item.Error == "" {
				if guard == nil {
					guard = skillGuard()
				}
				if guard.inUse(real) {
					item.Error = errSkillInUse.Error()
				}
			}
			if item.Error != "" {
				res.Items = append(res.Items, item)
				continue
			}
			item.Freed, _, _ = measureAI(context.Background(), p, nil)
		}
		if err := trashItem(p); err != nil {
			item.Error = err.Error()
			item.Freed = 0
		} else {
			res.Moved++
			res.Freed += item.Freed
		}
		res.Items = append(res.Items, item)
	}
	return res
}

func isLink(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode()&fs.ModeSymlink != 0
}

// LinkSkillCopy replaces the folder at path with a link to target, another
// skills folder's copy of the same skill with the same contents. The folder
// goes to the Trash; copies that have drifted apart are refused, so nothing
// is lost.
func (Cleaner) LinkSkillCopy(path, target string) error {
	path, target = filepath.Clean(path), filepath.Clean(target)
	if err := skillEntryOK(path); err != nil {
		return err
	}
	if err := skillEntryOK(target); err != nil {
		return err
	}
	if isLink(path) || isLink(target) || filepath.Base(path) != filepath.Base(target) {
		return errSkillNotEntry
	}
	pr, _, _ := skillRootOf(path)
	tr, _, _ := skillRootOf(target)
	realPath, realTarget := resolvePath(path), resolvePath(target)
	if pr.Tool == tr.Tool || realPath == realTarget {
		return errSkillNotEntry
	}
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		return errSkillNotEntry
	}
	for _, l := range skillLinksTo(realPath) {
		if l != path {
			return errSkillLinked
		}
	}
	a, _ := skillHash(context.Background(), path)
	b, _ := skillHash(context.Background(), target)
	if a == "" || a != b {
		return errSkillDiverged
	}
	if skillGuard().inUse(realPath) {
		return errSkillInUse
	}
	// Make the link beside the folder first, so the folder only moves once
	// its replacement exists, then swap the link in.
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".cleanzo-link")
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := trashItem(path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
