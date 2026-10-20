package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// AI coding tools: Claude Code and Codex
// ---------------------------------------------------------------------------
//
// Both tools keep their data in a dot folder in the home folder. Some of it
// regrows by itself (logs, caches, old releases); some of it is the user's
// own history (session transcripts). The page lists every part with its size
// and a safety level, and cleaning only ever moves things to the Trash.
//
// The safety levels, from most to least protected:
//
//   protected  settings, sign-in, skills, plugins, the version in use;
//              never offered
//   sensitive  conversation history; offered per project or month, never
//              preselected, and the last 24 hours are left alone
//   optional   regrows or loses little (prompt history, file snapshots,
//              generated images); offered, not preselected
//   safe       logs, caches, leftovers, old releases; preselected
//
// Parts a tool adds later than this table show up as "unknown": listed with
// their size so nothing is hidden, but never offered for cleaning.

// AI tool part kinds and safety levels.
const (
	aiSafe      = "safe"
	aiOptional  = "optional"
	aiSensitive = "sensitive"
	aiProtected = "protected"
	aiUnknown   = "unknown"
)

// aiRecent is how recent a session's last write must be for it to be left
// alone: the user may still be in it.
const aiRecent = 24 * time.Hour

// aiPart is one known part of a tool's folder.
type aiPart struct {
	ID    string // stable id, used in i18n and selections
	Path  string // relative to the tool's folder; a trailing / means a folder
	Glob  bool   // Path is a glob pattern (several files)
	Level string
	// Sessions splits a sensitive folder into groups the user picks from.
	Sessions string // "claude-projects" or "codex-months"; "" for none
	// Releases keeps the release a "current" link points to and offers the rest.
	Releases bool
	// RunSafe parts may be cleaned while the tool runs: a running tool does
	// not use old releases, sessions it last wrote over a day ago, or
	// leftovers of earlier writes. Open files are still left alone.
	RunSafe bool
}

// aiTool describes one AI coding tool.
type aiTool struct {
	ID      string
	Name    string
	Home    string   // folder in the home folder
	Process []string // command fragments that mean the tool is running
	Parts   []aiPart
}

// aiTools is the table of supported tools. Paths come from the tools' own
// documentation and source, and were checked against real installs.
var aiTools = []aiTool{
	{
		ID: "claude", Name: "Claude Code", Home: ".claude",
		// The CLI runs as "claude"; editors and other apps embed it as claude-code-*.
		Process: []string{"/claude ", "/claude\n", "claude-code"},
		Parts: []aiPart{
			{ID: "claude.sessions", Path: "projects/", Level: aiSensitive, Sessions: "claude-projects", RunSafe: true},
			{ID: "claude.fileHistory", Path: "file-history/", Level: aiOptional},
			{ID: "claude.promptHistory", Path: "history.jsonl", Level: aiOptional},
			{ID: "claude.debug", Path: "debug/", Level: aiSafe},
			{ID: "claude.telemetry", Path: "telemetry/", Level: aiSafe},
			{ID: "claude.pasteCache", Path: "paste-cache/", Level: aiSafe},
			{ID: "claude.shellSnapshots", Path: "shell-snapshots/", Level: aiSafe},
			{ID: "claude.cache", Path: "cache/", Level: aiSafe},
			{ID: "claude.statsig", Path: "statsig/", Level: aiSafe},
			{ID: "claude.plugins", Path: "plugins/", Level: aiProtected},
			{ID: "claude.skills", Path: "skills/", Level: aiProtected},
			{ID: "claude.backups", Path: "backups/", Level: aiProtected},
			{ID: "claude.todos", Path: "todos/", Level: aiProtected},
			{ID: "claude.settings", Path: "settings.json", Level: aiProtected},
			{ID: "claude.settingsLocal", Path: "settings.local.json", Level: aiProtected},
			{ID: "claude.memory", Path: "CLAUDE.md", Level: aiProtected},
			{ID: "claude.credentials", Path: ".credentials.json", Level: aiProtected},
		},
	},
	{
		ID: "codex", Name: "Codex", Home: ".codex",
		Process: []string{"/codex ", "/codex\n", "codex app-server", "app-server-daemon", "Codex.app/"},
		Parts: []aiPart{
			{ID: "codex.sessions", Path: "sessions/", Level: aiSensitive, Sessions: "codex-months", RunSafe: true},
			{ID: "codex.oldReleases", Path: "packages/app-server-daemon/releases/", Level: aiSafe, Releases: true, RunSafe: true},
			{ID: "codex.pluginCache", Path: "plugins/cache/", Level: aiSafe},
			{ID: "codex.logs", Path: "logs_*.sqlite*", Glob: true, Level: aiSafe},
			{ID: "codex.log", Path: "log/", Level: aiSafe},
			{ID: "codex.cache", Path: "cache/", Level: aiSafe},
			{ID: "codex.tmp", Path: "..codex-*.tmp-*", Glob: true, Level: aiSafe, RunSafe: true},
			{ID: "codex.tmpDir", Path: ".tmp/", Level: aiOptional},
			{ID: "codex.images", Path: "generated_images/", Level: aiOptional},
			{ID: "codex.computerUse", Path: "computer-use/", Level: aiOptional},
			{ID: "codex.threads", Path: "thread_history_*.sqlite*", Glob: true, Level: aiProtected},
			{ID: "codex.state", Path: "state_*.sqlite*", Glob: true, Level: aiProtected},
			{ID: "codex.archived", Path: "archived_sessions/", Level: aiProtected},
			{ID: "codex.plugins", Path: "plugins/", Level: aiProtected},
			{ID: "codex.packages", Path: "packages/", Level: aiProtected},
			{ID: "codex.skills", Path: "skills/", Level: aiProtected},
			{ID: "codex.config", Path: "config.toml", Level: aiProtected},
			{ID: "codex.auth", Path: "auth.json", Level: aiProtected},
			{ID: "codex.memory", Path: "AGENTS.md", Level: aiProtected},
		},
	},
}

// AIGroup is a pickable slice of a sensitive part (a project's or a month's
// sessions) or of old releases (one release folder).
type AIGroup struct {
	Key    string `json:"key"`    // project folder or yyyy/mm; release name
	Label  string `json:"label"`  // project path or yyyy-mm; release version
	Size   int64  `json:"size"`   // bytes on disk, recent sessions included
	Count  int    `json:"count"`  // sessions, or 1 for a release
	Last   int64  `json:"last"`   // newest write, unix seconds
	Recent int    `json:"recent"` // sessions written in the last 24 hours, left alone
	Gone   bool   `json:"gone"`   // the project folder no longer exists
	Path   string `json:"path"`   // the group's folder, for Show in Finder
}

// AIEntry is one of the largest things inside a part, shown when it expands.
type AIEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"isDir"`
	Last  int64  `json:"last"`
}

// aiTopN is how many of a part's largest entries the page lists.
const aiTopN = 8

// AIPart is one part of a tool's folder as the page lists it.
type AIPart struct {
	ID      string    `json:"id"`   // known part id, or "unknown:<name>"
	Name    string    `json:"name"` // file or folder name, for unknown parts
	Path    string    `json:"path"` // absolute
	Level   string    `json:"level"`
	Size    int64     `json:"size"`
	Files   int64     `json:"files"`
	Last    int64     `json:"last"`
	Groups  []AIGroup `json:"groups"`  // pickable groups; empty when the part is offered whole
	Top     []AIEntry `json:"top"`     // largest entries inside, for parts without groups
	More    int       `json:"more"`    // entries beyond Top
	Keep    string    `json:"keep"`    // the release kept for old releases
	RunSafe bool      `json:"runSafe"` // can be cleaned while the tool runs
}

// AITool is one tool's folder as the page lists it.
type AITool struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Installed bool     `json:"installed"` // its folder exists
	Running   bool     `json:"running"`   // nothing is cleaned while it runs
	Size      int64    `json:"size"`
	Parts     []AIPart `json:"parts"`
}

// AIScanResult is what ScanAITools returns.
type AIScanResult struct {
	Tools     []AITool `json:"tools"`
	ScannedAt int64    `json:"scannedAt"`
}

// aiProcesses lists process commands; tests replace it.
var aiProcesses = func() (string, error) {
	out, err := psCommands()
	return string(out), err
}

// aiRunning reports which tools are running. When processes cannot be
// listed every tool counts as running, so nothing is cleaned blind.
func aiRunning() map[string]bool {
	out, err := aiProcesses()
	running := map[string]bool{}
	for _, tool := range aiTools {
		if err != nil {
			running[tool.ID] = true
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			line += "\n"
			if slices.ContainsFunc(tool.Process, func(p string) bool {
				return strings.Contains(line, p) || strings.HasPrefix(line, strings.TrimLeft(p, "/"))
			}) {
				running[tool.ID] = true
				break
			}
		}
	}
	return running
}

// aiNow is the clock; tests replace it.
var aiNow = time.Now

// ScanAITools measures the folders of the supported AI coding tools.
func (Cleaner) ScanAITools(ctx context.Context) (AIScanResult, error) {
	running := aiRunning()
	res := AIScanResult{ScannedAt: aiNow().Unix()}
	for _, tool := range aiTools {
		t, err := scanAITool(ctx, tool, running[tool.ID])
		if err != nil {
			return AIScanResult{}, err
		}
		res.Tools = append(res.Tools, t)
	}
	return res, nil
}

// AIToolInfo is one supported tool as the sidebar lists it.
type AIToolInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"` // its folder exists
}

// ListAITools reports which supported tools have a folder, without measuring
// anything, so the sidebar can show one entry per installed tool at start.
func (Cleaner) ListAITools() []AIToolInfo {
	out := []AIToolInfo{}
	for _, tool := range aiTools {
		info, err := os.Lstat(filepath.Join(homeDir(), tool.Home))
		out = append(out, AIToolInfo{ID: tool.ID, Name: tool.Name, Installed: err == nil && info.IsDir()})
	}
	return out
}

// errAIUnknownTool is a tool id the table does not have.
var errAIUnknownTool = errors.New("unknown-tool")

// ScanAITool measures one tool's folder; each tool has its own page.
func (Cleaner) ScanAITool(ctx context.Context, id string) (AIScanResult, error) {
	for _, tool := range aiTools {
		if tool.ID != id {
			continue
		}
		t, err := scanAITool(ctx, tool, aiRunning()[tool.ID])
		if err != nil {
			return AIScanResult{}, err
		}
		return AIScanResult{Tools: []AITool{t}, ScannedAt: aiNow().Unix()}, nil
	}
	return AIScanResult{}, errAIUnknownTool
}

// scanAITool measures one tool's folder part by part.
func scanAITool(ctx context.Context, tool aiTool, running bool) (AITool, error) {
	root := filepath.Join(homeDir(), tool.Home)
	out := AITool{ID: tool.ID, Name: tool.Name, Path: root, Running: running, Parts: []AIPart{}}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return out, nil
	}
	out.Installed = true
	claimed := map[string]bool{} // top-level entries a known part accounts for
	for _, part := range tool.Parts {
		if ctx.Err() != nil {
			return AITool{}, ctx.Err()
		}
		paths := aiPartPaths(root, part)
		if len(paths) == 0 {
			continue
		}
		for _, p := range paths {
			claimed[strings.SplitN(strings.TrimPrefix(p, root+string(filepath.Separator)), string(filepath.Separator), 2)[0]] = true
		}
		// A nested known part (plugins/cache) is measured on its own; its
		// parent (plugins) reports the rest.
		p := AIPart{ID: part.ID, Path: paths[0], Level: part.Level, Groups: []AIGroup{}, RunSafe: part.RunSafe}
		if part.Glob {
			p.Path = filepath.Join(root, part.Path)
		}
		for _, path := range paths {
			size, files, last := measureAI(ctx, path, aiNested(tool, part, root))
			p.Size += size
			p.Files += files
			p.Last = max(p.Last, last)
		}
		switch {
		case part.Sessions == "claude-projects":
			p.Groups = claudeProjects(ctx, paths[0])
		case part.Sessions == "codex-months":
			p.Groups = codexMonths(ctx, paths[0])
		case part.Releases:
			p.Keep, p.Groups = oldReleases(ctx, paths[0])
			// Only the old ones are on offer.
			p.Size, p.Files = 0, int64(len(p.Groups))
			for _, g := range p.Groups {
				p.Size += g.Size
			}
		}
		if p.Size == 0 && p.Files == 0 && len(p.Groups) == 0 {
			continue
		}
		if len(p.Groups) == 0 {
			p.Top, p.More = aiTopEntries(ctx, paths, aiNested(tool, part, root))
		}
		out.Parts = append(out.Parts, p)
	}
	// Anything else in the folder is listed as unknown, never offered.
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if claimed[e.Name()] || e.Name() == ".DS_Store" {
			continue
		}
		path := filepath.Join(root, e.Name())
		size, files, last := measureAI(ctx, path, nil)
		if size == 0 {
			continue
		}
		top, more := aiTopEntries(ctx, []string{path}, nil)
		out.Parts = append(out.Parts, AIPart{ID: "unknown:" + e.Name(), Name: e.Name(), Path: path, Level: aiUnknown, Size: size, Files: files, Last: last, Groups: []AIGroup{}, Top: top, More: more})
	}
	for _, p := range out.Parts {
		out.Size += p.Size
	}
	if du, err := duSize(ctx, root, 60*time.Second); err == nil {
		out.Size = du
	}
	sort.SliceStable(out.Parts, func(i, j int) bool {
		return aiLevelRank(out.Parts[i].Level) < aiLevelRank(out.Parts[j].Level) ||
			aiLevelRank(out.Parts[i].Level) == aiLevelRank(out.Parts[j].Level) && out.Parts[i].Size > out.Parts[j].Size
	})
	return out, nil
}

// aiLevelRank orders parts on the page: what can be cleaned first.
func aiLevelRank(level string) int {
	return slices.Index([]string{aiSafe, aiOptional, aiSensitive, aiUnknown, aiProtected}, level)
}

// aiPartPaths returns the existing paths a part matches, in order.
func aiPartPaths(root string, part aiPart) []string {
	if part.Glob {
		matches, _ := filepath.Glob(filepath.Join(root, part.Path))
		sort.Strings(matches)
		return matches
	}
	path := filepath.Join(root, strings.TrimSuffix(part.Path, "/"))
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 {
		return nil
	}
	if strings.HasSuffix(part.Path, "/") != info.IsDir() {
		return nil
	}
	return []string{path}
}

// aiNested returns the folders of other known parts inside part's folder,
// which part's size leaves out.
func aiNested(tool aiTool, part aiPart, root string) []string {
	if part.Glob {
		return nil
	}
	base := filepath.Join(root, part.Path)
	var nested []string
	for _, other := range tool.Parts {
		if other.ID == part.ID || other.Glob {
			continue
		}
		p := filepath.Join(root, other.Path)
		if p != filepath.Clean(base) && within(p, filepath.Clean(base)) {
			nested = append(nested, filepath.Clean(p))
		}
	}
	return nested
}

// measureAI sums a path's size on disk without following links, skipping
// the folders in skip.
func measureAI(ctx context.Context, path string, skip []string) (size, files, last int64) {
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil
		}
		if d.IsDir() && slices.Contains(skip, filepath.Clean(p)) {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		size += onDisk(info)
		files++
		last = max(last, info.ModTime().Unix())
		return nil
	})
	return size, files, last
}

// claudeProjects groups Claude Code's transcripts by project folder.
func claudeProjects(ctx context.Context, dir string) []AIGroup {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []AIGroup{}
	}
	cutoff := aiNow().Add(-aiRecent).Unix()
	groups := []AIGroup{}
	for _, e := range entries {
		if !e.IsDir() || ctx.Err() != nil {
			continue
		}
		path := filepath.Join(dir, e.Name())
		g := AIGroup{Key: e.Name(), Label: e.Name(), Path: path}
		var cwd string
		_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			g.Size += onDisk(info)
			g.Last = max(g.Last, info.ModTime().Unix())
			// Top-level transcripts are sessions; subagent logs belong to one.
			if filepath.Dir(p) == path && strings.HasSuffix(p, ".jsonl") {
				g.Count++
				if info.ModTime().Unix() > cutoff {
					g.Recent++
				}
				if cwd == "" {
					cwd = jsonlField(p, "cwd")
				}
			}
			return nil
		})
		if cwd != "" {
			g.Label = cwd
			g.Gone = !fileExists(cwd)
		}
		if g.Size > 0 {
			groups = append(groups, g)
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Size > groups[j].Size })
	return groups
}

// codexMonths groups Codex's rollouts by month: sessions/yyyy/mm/dd/*.jsonl.
func codexMonths(ctx context.Context, dir string) []AIGroup {
	cutoff := aiNow().Add(-aiRecent).Unix()
	byMonth := map[string]*AIGroup{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		parts := strings.Split(strings.TrimPrefix(p, dir+string(filepath.Separator)), string(filepath.Separator))
		if len(parts) < 2 || len(parts[0]) != 4 || len(parts[1]) != 2 {
			return nil
		}
		key := parts[0] + "/" + parts[1] // slash-ok: Codex month keys use yyyy/mm independent of filesystem separators
		g := byMonth[key]
		if g == nil {
			g = &AIGroup{Key: key, Label: parts[0] + "-" + parts[1], Path: filepath.Join(dir, parts[0], parts[1])}
			byMonth[key] = g
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		g.Size += onDisk(info)
		g.Last = max(g.Last, info.ModTime().Unix())
		if strings.HasSuffix(p, ".jsonl") {
			g.Count++
			if info.ModTime().Unix() > cutoff {
				g.Recent++
			}
		}
		return nil
	})
	groups := []AIGroup{}
	for _, g := range byMonth {
		groups = append(groups, *g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Key > groups[j].Key })
	return groups
}

// oldReleases lists the release folders a "current" link next to dir does
// not point to. Without a readable link nothing is offered: the version in
// use cannot be told apart.
func oldReleases(ctx context.Context, dir string) (keep string, groups []AIGroup) {
	groups = []AIGroup{}
	target, err := os.Readlink(filepath.Join(filepath.Dir(dir), "current"))
	if err != nil {
		return "", groups
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(dir), target)
	}
	keep = filepath.Base(filepath.Clean(target))
	if filepath.Dir(filepath.Clean(target)) != filepath.Clean(dir) || !fileExists(target) {
		return "", groups
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() || e.Name() == keep || ctx.Err() != nil {
			continue
		}
		size, _, last := measureAI(ctx, filepath.Join(dir, e.Name()), nil)
		groups = append(groups, AIGroup{Key: e.Name(), Label: strings.SplitN(e.Name(), "-", 2)[0], Size: size, Count: 1, Last: last, Path: filepath.Join(dir, e.Name())})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Key < groups[j].Key })
	return keep, groups
}

// aiTopEntries lists the largest direct entries of a part: a folder's
// children, or the files a glob matched. Folders in skip (nested known parts)
// are left out. It returns the top aiTopN and how many more there are.
func aiTopEntries(ctx context.Context, paths []string, skip []string) ([]AIEntry, int) {
	var all []AIEntry
	add := func(path string, d fs.DirEntry) {
		if slices.Contains(skip, filepath.Clean(path)) || d.Type()&fs.ModeSymlink != 0 {
			return
		}
		size, _, last := measureAI(ctx, path, skip)
		if size == 0 {
			return
		}
		all = append(all, AIEntry{Name: d.Name(), Path: path, Size: size, IsDir: d.IsDir(), Last: last})
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			add(path, fs.FileInfoToDirEntry(info))
			continue
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if ctx.Err() != nil {
				break
			}
			add(filepath.Join(path, e.Name()), e)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Size > all[j].Size })
	if len(all) <= aiTopN {
		if all == nil {
			all = []AIEntry{}
		}
		return all, 0
	}
	return all[:aiTopN], len(all) - aiTopN
}

// jsonlField reads a string field from the first lines of a JSON Lines file.
func jsonlField(path, field string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for i := 0; i < 20 && sc.Scan(); i++ {
		var line map[string]any
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if v, ok := line[field].(string); ok && v != "" {
			return v
		}
		if payload, ok := line["payload"].(map[string]any); ok {
			if v, ok := payload[field].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Cleaning
// ---------------------------------------------------------------------------

// AISelection picks a part, or some of its groups.
type AISelection struct {
	Tool   string   `json:"tool"`
	Part   string   `json:"part"`
	Groups []string `json:"groups"` // group keys; required for parts with groups
}

// AIPartResult reports one selection's outcome.
type AIPartResult struct {
	Tool    string   `json:"tool"`
	Part    string   `json:"part"`
	Moved   int64    `json:"moved"`   // paths moved to the Trash
	Freed   int64    `json:"freed"`   // bytes
	Kept    int64    `json:"kept"`    // recent sessions left alone
	Skipped string   `json:"skipped"` // running, blocked, unknown, in-use; "" when cleaned
	Failed  []string `json:"failed"`
}

// AICleanResult is what CleanAITools returns.
type AICleanResult struct {
	Parts []AIPartResult `json:"parts"`
	Freed int64          `json:"freed"`
}

// CleanAITools moves the selected parts to the Trash. While a tool runs only
// its run-safe parts (old releases, sessions, leftovers) are cleaned; files
// it has open are still left alone. Protected and unknown parts are refused;
// sessions written in the last 24 hours are kept.
func (Cleaner) CleanAITools(ctx context.Context, selections []AISelection) (AICleanResult, error) {
	running := aiRunning()
	guard := probeUsage()
	res := AICleanResult{Parts: []AIPartResult{}}
	for _, sel := range selections {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		r := cleanAIPart(sel, running, guard)
		res.Freed += r.Freed
		res.Parts = append(res.Parts, r)
	}
	return res, nil
}

// errAIRefused is a selection the safety rules turn down.
var errAIRefused = errors.New("blocked")

// cleanAIPart cleans one selection.
func cleanAIPart(sel AISelection, running map[string]bool, guard *usageGuard) AIPartResult {
	r := AIPartResult{Tool: sel.Tool, Part: sel.Part, Failed: []string{}}
	tool, part, ok := findAIPart(sel.Tool, sel.Part)
	switch {
	case !ok:
		r.Skipped = "unknown"
		return r
	case running[tool.ID] && !part.RunSafe:
		r.Skipped = "running"
		return r
	case part.Level == aiProtected || part.Level == aiUnknown:
		r.Skipped = "blocked"
		return r
	}
	root := filepath.Join(homeDir(), tool.Home)
	targets, kept, err := aiTargets(root, tool, part, sel.Groups)
	r.Kept = kept
	if err != nil {
		r.Skipped = "blocked"
		return r
	}
	for _, target := range targets {
		if !aiRemovable(root, tool, target) {
			r.Failed = append(r.Failed, displayHome(target)+": blocked")
			continue
		}
		if guard.inUse(resolvePath(target)) {
			r.Kept++
			continue
		}
		size, _, _ := measureAI(context.Background(), target, nil)
		if err := trashItem(target); err != nil {
			r.Failed = append(r.Failed, displayHome(target)+": "+err.Error())
			continue
		}
		r.Moved++
		r.Freed += size
	}
	if r.Moved == 0 && len(r.Failed) == 0 && r.Kept > 0 {
		r.Skipped = "in-use"
	}
	return r
}

// findAIPart looks a selection up in the table.
func findAIPart(toolID, partID string) (aiTool, aiPart, bool) {
	for _, tool := range aiTools {
		if tool.ID != toolID {
			continue
		}
		for _, part := range tool.Parts {
			if part.ID == partID {
				return tool, part, true
			}
		}
	}
	return aiTool{}, aiPart{}, false
}

// aiTargets turns a selection into the paths to move to the Trash, and
// counts the recent sessions it leaves alone.
func aiTargets(root string, tool aiTool, part aiPart, groups []string) (targets []string, kept int64, err error) {
	paths := aiPartPaths(root, part)
	if len(paths) == 0 {
		return nil, 0, nil
	}
	cutoff := aiNow().Add(-aiRecent)
	switch {
	case part.Sessions != "" || part.Releases:
		if len(groups) == 0 {
			return nil, 0, errAIRefused
		}
		dir := paths[0]
		keep := ""
		if part.Releases {
			keep, _ = oldReleases(context.Background(), dir)
			if keep == "" {
				return nil, 0, errAIRefused
			}
		}
		for _, key := range groups {
			// A key names a folder inside dir: no escaping it.
			clean := filepath.Clean(key)
			if clean == "." || clean == keep || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") || strings.Contains(clean, "/../") {
				return nil, 0, errAIRefused
			}
			if part.Sessions == "codex-months" && (len(clean) != 7 || clean[4] != '/') {
				return nil, 0, errAIRefused
			}
			if part.Sessions != "codex-months" && strings.Contains(clean, "/") {
				return nil, 0, errAIRefused
			}
			group := filepath.Join(dir, clean)
			if part.Releases {
				targets = append(targets, group)
				continue
			}
			// Sessions: move the files one by one, keeping recent ones.
			_ = filepath.WalkDir(group, func(p string, d fs.DirEntry, err error) error {
				if err != nil || !d.Type().IsRegular() {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return nil
				}
				if info.ModTime().After(cutoff) {
					if strings.HasSuffix(p, ".jsonl") && (part.Sessions == "codex-months" || filepath.Dir(p) == group) {
						kept++
					}
					return nil
				}
				targets = append(targets, p)
				return nil
			})
			// A Claude session's folder (subagents, tool results) goes with it.
			if part.Sessions == "claude-projects" {
				targets = claudeSessionFolders(group, targets, cutoff)
			}
		}
		return targets, kept, nil
	default:
		return paths, 0, nil
	}
}

// claudeSessionFolders replaces the files of a Claude session's folder
// (<id>/subagents/...) with the folder itself when its transcript <id>.jsonl
// is being moved too and nothing in it is recent.
func claudeSessionFolders(group string, targets []string, cutoff time.Time) []string {
	moving := map[string]bool{}
	for _, t := range targets {
		moving[t] = true
	}
	var out []string
	folders := map[string]bool{}
	for _, t := range targets {
		rel := strings.TrimPrefix(t, group+string(filepath.Separator))
		first, rest, nested := strings.Cut(rel, string(filepath.Separator))
		if !nested || rest == "" {
			out = append(out, t)
			continue
		}
		folder := filepath.Join(group, first)
		if !moving[folder+".jsonl"] {
			// An orphaned folder: leave it to its transcript.
			continue
		}
		if !folders[folder] {
			folders[folder] = true
			_, _, last := measureAI(context.Background(), folder, nil)
			if time.Unix(last, 0).Before(cutoff) {
				out = append(out, folder)
			}
		}
	}
	return out
}

// aiRemovable checks a target against the tool's folder and protected parts
// right before it moves.
func aiRemovable(root string, tool aiTool, target string) bool {
	real := resolvePath(target)
	realRoot := resolvePath(root)
	if (real == realRoot || !within(real, realRoot)) || !safeToClean(target) {
		return false
	}
	info, err := os.Lstat(target)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 {
		return false
	}
	for _, part := range tool.Parts {
		if part.Level != aiProtected {
			continue
		}
		for _, p := range aiPartPaths(root, part) {
			rp := resolvePath(p)
			// The target is a protected part, or holds one. Being inside a
			// protected folder is fine when a known cleanable part lives there
			// (plugins/cache inside plugins).
			if within(rp, real) {
				return false
			}
			if real != rp && within(real, rp) && !aiInsideCleanable(root, tool, real) {
				return false
			}
		}
	}
	return true
}

// aiInsideCleanable reports whether path is inside a cleanable part's folder.
func aiInsideCleanable(root string, tool aiTool, path string) bool {
	for _, part := range tool.Parts {
		if part.Level == aiProtected || part.Level == aiUnknown || part.Glob {
			continue
		}
		p := resolvePath(filepath.Join(root, part.Path))
		if within(path, p) {
			return true
		}
	}
	return false
}

// displayHome shortens the home folder to ~ in messages.
func displayHome(p string) string {
	if p == homeDir() {
		return p
	}
	return shortHome(p)
}
