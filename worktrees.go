package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Worktree states. Only wtClean and wtDirty can be removed.
const (
	wtClean      = "clean"
	wtDirty      = "dirty"
	wtInUse      = "in-use"
	wtSubmodule  = "submodules"
	wtMidOp      = "mid-operation"
	wtUnbranched = "unbranched-commits"
	wtNoGit      = "git-unavailable"
	wtIndexOnly  = "index-only"
	wtUnknown    = "unrecognized"
	wtHolds      = "holds-worktrees" // other worktrees live inside it; they go first
)

// Outcomes of removing one worktree, besides success.
const (
	wtFailChanged = "wt-changed" // it is not what the user saw any more; nothing was changed
	wtFailGit     = "wt-git"     // git's record could not be cleared; nothing was changed
	wtFailTrash   = "wt-trash"   // git's record is cleared, the folder is still in place
)

// wtCancelled is removeWorktree's answer when the clean is cancelled before
// the folder moves: nothing was changed, and nothing failed.
const wtCancelled = "cancelled"

// worktreeTemp is appended to a worktree's folder name while git clears its record.
const worktreeTemp = ".cleanzo-removing"

// worktreeMoving is held while a worktree's folder may be away from its
// path: by a removal from its last look at the worktree until the folder is
// in the Trash or back, and by a listing while it reads a folder of
// worktrees and puts back what an interrupted removal left. A listing then
// never puts a folder back while git is asked to forget it, and never lists
// one a removal has moved aside.
var worktreeMoving sync.Mutex

// agentWorktree is one worktree an agent made.
type agentWorktree struct {
	Path, Name    string
	Repo          string // the repository's folder
	Admin, Common string // git's record of it, and the repository's git folder
	Branch        string
	State         string
	Changes       int
	Size, Last    int64
}

// worktreeUsage is what tells a worktree is in use. OK is false when it could
// not be read, and every worktree then counts as in use.
type worktreeUsage struct {
	Cwds     []string // working folders of this user's processes
	Sessions []string // working folders of the open Claude Code sessions
	OK       bool
}

var worktreeUse = claudeWorkingFolders // tests replace it

// worktreePidStart is when a process started; ok is false when it is not running. Tests replace it.
var worktreePidStart = func(pid int32) (time.Time, bool) {
	start, ok := realClaudeSource().started(pid)
	return time.UnixMicro(start), ok
}

// Time limits of the git commands the worktree rules run.
const (
	worktreeStatusLimit = 10 * time.Second
	worktreeGitLimit    = 5 * time.Second
)

// worktreeGit runs one git command for the worktree rules and returns its
// answer, what it printed on standard output. Tests replace it.
var worktreeGit = func(ctx context.Context, git string, args []string, limit time.Duration) (string, error) {
	return runToolWith(ctx, git, args, limit, func(cmd *exec.Cmd) ([]byte, error) {
		// An inherited GIT_DIR or GIT_WORK_TREE would point git at another
		// repository. Run in a worktree's folder, git never looks above it:
		// the folder is inside the repository it was made from.
		cmd.Env = append(slices.DeleteFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "GIT_") }),
			"GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
		if len(args) > 1 && args[0] == "-C" {
			cmd.Env = append(cmd.Env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(args[1]))
		}
		// A warning or a hint git prints on standard error would read as a
		// line of its answer, so standard error goes nowhere.
		return runToolStdout(cmd)
	})
}

// worktreeGitTool is the git the worktree rules ask, or "" when none can answer.
func worktreeGitTool() string {
	env := pathJunkEnv(homeDir())
	env.findTool = findTool
	return env.projectGit()
}

// worktreeLockPid finds the processes a lock reason names, as Claude Code
// writes them: "claude agent agent-a1b2 (pid 4242)".
var worktreeLockPid = regexp.MustCompile(`\(pid (\d+)\)`)

// worktreeObjectName matches a full SHA-1 or SHA-256 object name.
var worktreeObjectName = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// worktreeRefLine matches a ref as for-each-ref prints it by default.
var worktreeRefLine = regexp.MustCompile(`(?m)^(?:[0-9a-f]{40}|[0-9a-f]{64}) [a-z]+\trefs/(?:heads|tags|remotes)/`)

// worktreeOperations are what git keeps in a worktree's record while an
// operation is half done.
var worktreeOperations = []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"}

// findWorktrees lists the worktrees the agents of kind ("claude" or "codex",
// as in aiTool.Worktrees) made, each with its state.
func findWorktrees(ctx context.Context, kind string) []agentWorktree {
	if runtime.GOOS != "darwin" {
		return nil
	}
	use := worktreeUse()
	paths := worktreePaths(ctx, kind, use.Sessions)
	if len(paths) == 0 {
		return nil
	}
	git, users := worktreeGitTool(), worktreeUsersOf(use)
	found := make([]agentWorktree, len(paths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, scanConcurrency)
	for i, p := range paths {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			w := judgeWorktree(ctx, p, git, users)
			w.Size, _, _ = measureAI(ctx, p, nil)
			if changed, ok := lastChange(p); ok {
				w.Last = changed.Unix()
			}
			found[i] = w
		}()
	}
	wg.Wait()
	slices.SortFunc(found, func(a, b agentWorktree) int { return strings.Compare(a.Path, b.Path) })
	return found
}

// worktreePaths lists the folders of the worktrees the agents of kind made,
// spelled from the home folder, without judging or measuring them.
// findWorktrees judges these, and a clean takes a key it is sent only when
// it is one of them. sessions are the working folders of the open Claude
// Code sessions. Only macOS has them.
func worktreePaths(ctx context.Context, kind string, sessions []string) []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	var paths []string
	dirs, folders := map[string]bool{}, map[string]bool{}
	for _, dir := range worktreeDirs(ctx, kind, sessions) {
		key := strings.ToLower(resolvePath(dir))
		if dirs[key] || !worktreeListable(dir) {
			continue
		}
		dirs[key] = true
		for _, p := range worktreeFolders(dir) {
			if key := strings.ToLower(resolvePath(p)); !folders[key] && worktreeListable(p) {
				folders[key] = true
				paths = append(paths, homeSpelled(p))
			}
		}
	}
	return paths
}

// worktreeDirs lists the folders the agents of kind keep worktrees in.
// Claude Code keeps them in <repository>/.claude/worktrees: the
// repositories its transcripts and open sessions work in, and wherever the
// home walk passes one. Codex keeps them in ~/.codex/worktrees/<id>.
func worktreeDirs(ctx context.Context, kind string, sessions []string) []string {
	switch kind {
	case "codex":
		return subfolders(filepath.Join(homeDir(), ".codex", "worktrees"))
	case "claude":
		var dirs []string
		for _, cwd := range slices.Concat(claudeProjectFolders(ctx), sessions) {
			if repo := worktreeRepoOf(cwd); repo != "" {
				dirs = append(dirs, filepath.Join(repo, ".claude", "worktrees"))
			}
		}
		env := pathJunkEnv(homeDir())
		env.ctx = ctx
		_, walked := env.walkHome(claimSet{})
		return append(dirs, walked...)
	}
	return nil
}

// claudeProjectFolders lists the working folder of each of Claude Code's
// projects: the cwd its first transcript naming one names.
func claudeProjectFolders(ctx context.Context) []string {
	var cwds []string
	for _, project := range subfolders(filepath.Join(homeDir(), ".claude", "projects")) {
		if ctx.Err() != nil {
			break
		}
		entries, _ := os.ReadDir(project)
		for _, e := range entries {
			if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			if cwd := jsonlField(filepath.Join(project, e.Name()), "cwd"); cwd != "" {
				cwds = append(cwds, cwd)
				break
			}
		}
	}
	return cwds
}

// worktreeRepoOf is the repository a working folder is in: the nearest
// folder at or above it holding a .git, or, when that .git is a linked
// worktree's file, the repository git's record of the worktree names. It is
// "" when there is none.
func worktreeRepoOf(dir string) string {
	if !filepath.IsAbs(dir) {
		return ""
	}
	for p := filepath.Clean(dir); ; p = filepath.Dir(p) {
		if worktreeHas(p, ".git") {
			if _, common, ok := worktreeRecord(p, p); ok {
				return worktreeRepo(common)
			}
			return p
		}
		if filepath.Dir(p) == p {
			return ""
		}
	}
}

// worktreeFolders lists the folders in dir, where an agent keeps worktrees.
// A worktree an interrupted removal left under its temporary name is put
// back first, when its own name is free.
func worktreeFolders(dir string) []string {
	worktreeMoving.Lock()
	defer worktreeMoving.Unlock()
	folders := subfolders(dir)
	for i, p := range folders {
		name, waiting := strings.CutSuffix(filepath.Base(p), worktreeTemp)
		if !waiting || name == "" || !worktreeListable(p) {
			continue
		}
		original := filepath.Join(dir, name)
		if !worktreeHas(dir, name) && worktreeLeftAway(p, original) && os.Rename(p, original) == nil {
			folders[i] = original
		}
	}
	return folders
}

// worktreeLeftAway reports whether the folder at p is a worktree a removal
// left away from its path, original: git's record of it still names
// original, or git has forgotten it, and nothing is where its .git file
// names the record.
func worktreeLeftAway(p, original string) bool {
	if _, _, ok := worktreeRecord(p, original); ok {
		return true
	}
	admin, ok := worktreeGitdir(p)
	if !ok {
		return false
	}
	_, err := os.Lstat(admin)
	return errors.Is(err, fs.ErrNotExist)
}

// worktreeListable reports whether a folder may be listed: it is inside the
// home folder once links are resolved, and cleaning may reach it. Cleaning
// may also reach the temporary folder, which holds no agent's worktrees.
func worktreeListable(p string) bool {
	return within(strings.ToLower(resolvePath(p)), strings.ToLower(resolvePath(homeDir()))) && safeToClean(p)
}

// homeSpelled spells p from the home folder when it is inside it once links
// are resolved, so a worktree reads the same whichever way it was found.
func homeSpelled(p string) string {
	home := homeDir()
	real, realHome := resolvePath(p), resolvePath(home)
	if rel, err := filepath.Rel(realHome, real); err == nil && within(real, realHome) {
		return filepath.Join(home, rel)
	}
	return p
}

// worktreeUsers are the working folders that keep worktrees in use, read
// once for a pass.
type worktreeUsers struct {
	folders []string // resolved and lower-cased
	known   bool     // false when they could not be read: every worktree is in use then
}

// worktreeUsersOf resolves what worktreeUse read.
func worktreeUsersOf(use worktreeUsage) worktreeUsers {
	users := worktreeUsers{known: use.OK}
	for _, dir := range slices.Concat(use.Cwds, use.Sessions) {
		if filepath.IsAbs(dir) {
			users.folders = append(users.folders, strings.ToLower(resolvePath(dir)))
		}
	}
	return users
}

// holds reports whether one of the working folders is path or inside it.
func (u worktreeUsers) holds(path string) bool {
	if !u.known {
		return true
	}
	key := strings.ToLower(resolvePath(path))
	return slices.ContainsFunc(u.folders, func(dir string) bool { return within(dir, key) })
}

// judgeWorktree reads the state of the folder at path, one of an agent's
// worktrees, asking git only when it must. Size and Last are left to the
// caller.
func judgeWorktree(ctx context.Context, path, git string, users worktreeUsers) agentWorktree {
	w := agentWorktree{Path: path, Name: filepath.Base(path), State: wtUnknown}
	admin, common, ok := worktreeRecord(path, path)
	if !ok {
		return w
	}
	w.Admin, w.Common, w.Repo = admin, common, homeSpelled(worktreeRepo(common))
	head := worktreeLine(filepath.Join(admin, "HEAD"))
	branch, attached := strings.CutPrefix(head, "ref: refs/heads/")
	// A repository that keeps its refs in a reftable writes the placeholder
	// .invalid there, whatever is checked out: HEAD cannot be read then.
	readable := attached && branch != "" && branch != ".invalid" || !attached && worktreeObjectName.MatchString(head)
	if attached && readable {
		w.Branch = branch
	}
	switch {
	case users.holds(path) || worktreeLocked(admin):
		w.State = wtInUse
	case worktreeHoldsOthers(path):
		w.State = wtHolds
	case worktreeHas(admin, "modules"):
		w.State = wtSubmodule
	case slices.ContainsFunc(worktreeOperations, func(name string) bool { return worktreeHas(admin, name) }):
		w.State = wtMidOp
	case !readable || git == "":
		w.State = wtNoGit
	default:
		w.State, w.Changes = worktreeAskGit(ctx, git, path, common, head, attached)
	}
	return w
}

// worktreeHoldsOthers reports whether other worktrees may live inside the
// worktree at path: the .claude/worktrees folder in it, where Claude Code
// keeps them, holds a folder or cannot be read. A file there holds nothing,
// and a link moves without what it points to.
func worktreeHoldsOthers(path string) bool {
	dir := path
	for _, name := range []string{".claude", "worktrees"} {
		dir = filepath.Join(dir, name)
		info, err := os.Lstat(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return false
		case err != nil:
			return true
		case !info.IsDir():
			return false
		}
	}
	entries, err := os.ReadDir(dir)
	return err != nil || slices.ContainsFunc(entries, fs.DirEntry.IsDir)
}

// worktreeAskGit judges a worktree by what git says: whether a detached
// HEAD is on a branch, a tag or a remote's branch, and what is not
// committed. A command that fails or runs out of time leaves it unjudged.
func worktreeAskGit(ctx context.Context, git, path, common, head string, attached bool) (state string, changes int) {
	if !attached {
		out, err := worktreeGit(ctx, git, []string{"--git-dir=" + common, "-c", "core.fsmonitor=false", "for-each-ref", "--contains", head, "--count=1", "refs/heads", "refs/tags", "refs/remotes"}, worktreeGitLimit)
		switch {
		case err != nil:
			return wtNoGit, 0
		case !worktreeRefLine.MatchString(out):
			return wtUnbranched, 0
		}
	}
	out, err := worktreeGit(ctx, git, []string{"-C", path, "-c", "core.fsmonitor=false", "status", "--porcelain=v1", "--untracked-files=normal"}, worktreeStatusLimit)
	if err != nil {
		return wtNoGit, 0
	}
	return worktreeStatus(out)
}

// worktreeStatus judges what git status --porcelain printed. A line whose
// first column is a change in the index and whose second is a change on
// disk holds content only the index has; any other line is an uncommitted
// change.
func worktreeStatus(out string) (state string, changes int) {
	indexOnly := false
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		changes++
		if len(line) > 1 && !strings.ContainsRune(" ?!", rune(line[0])) && line[1] != ' ' {
			indexOnly = true
		}
	}
	switch {
	case indexOnly:
		return wtIndexOnly, 0
	case changes > 0:
		return wtDirty, changes
	}
	return wtClean, 0
}

// worktreeLocked reports whether git's record of a worktree is locked by
// someone still there: a process the lock names that started before the
// lock was written, or anyone at all when the lock names no process, as
// such a lock was taken on purpose. A lock naming only processes that are
// gone, or newer than it, is stale.
func worktreeLocked(admin string) bool {
	lock := filepath.Join(admin, "locked")
	info, err := os.Lstat(lock)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	reason, ok := worktreeFile(lock)
	if err != nil || !ok {
		return true
	}
	pids := worktreeLockPid.FindAllStringSubmatch(reason, -1)
	if len(pids) == 0 {
		return true
	}
	for _, m := range pids {
		pid, err := strconv.ParseInt(m[1], 10, 32)
		if err != nil {
			return true
		}
		if start, running := worktreePidStart(int32(pid)); running && start.Before(info.ModTime()) {
			return true
		}
	}
	return false
}

// worktreeRecord finds git's record of the worktree whose folder is dir:
// dir/.git is a file whose gitdir line names a folder in <common>/worktrees,
// and that folder's gitdir file names <points>/.git, once both are
// resolved. Relative paths count from the file's folder. It returns the
// record's folder and the repository's git folder its commondir names.
func worktreeRecord(dir, points string) (admin, common string, ok bool) {
	admin, ok = worktreeGitdir(dir)
	if !ok {
		return "", "", false
	}
	if info, err := os.Lstat(admin); err != nil || !info.IsDir() {
		return "", "", false
	}
	back := worktreePath(admin, worktreeLine(filepath.Join(admin, "gitdir")))
	if back == "" || resolvePath(back) != resolvePath(filepath.Join(points, ".git")) {
		return "", "", false
	}
	common = worktreePath(admin, worktreeLine(filepath.Join(admin, "commondir")))
	if info, err := os.Stat(common); common == "" || err != nil || !info.IsDir() {
		return "", "", false
	}
	return admin, common, true
}

// worktreeGitdir is where dir/.git names git's record of the worktree in
// dir: the file's gitdir line, a path in a folder called worktrees. A
// relative path counts from dir.
func worktreeGitdir(dir string) (string, bool) {
	line, isGitDir := strings.CutPrefix(worktreeLine(filepath.Join(dir, ".git")), "gitdir:")
	admin := worktreePath(dir, strings.TrimSpace(line))
	if !isGitDir || admin == "" || filepath.Base(filepath.Dir(admin)) != "worktrees" {
		return "", false
	}
	return admin, true
}

// worktreeRepo is the repository's folder for its git folder: the folder
// holding it, or the git folder itself when it is not named .git (a bare
// repository).
func worktreeRepo(common string) string {
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common
}

// worktreePath is p, read from a file in dir, as an absolute path: a
// relative one counts from dir as it really is. It is "" for an empty p.
func worktreePath(dir, p string) string {
	switch {
	case p == "":
		return ""
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	}
	return filepath.Join(resolvePath(dir), p)
}

// worktreeFile is the start of the regular file at p, at most 4 KB, and
// whether it could be read.
func worktreeFile(p string) (string, bool) {
	if !regularFile(p) {
		return "", false
	}
	f, err := os.Open(p)
	if err != nil {
		return "", false
	}
	defer f.Close()
	head, err := io.ReadAll(io.LimitReader(f, 4096))
	return string(head), err == nil
}

// worktreeLine is the first line of the regular file at p, trimmed; "" when
// it cannot be read.
func worktreeLine(p string) string {
	head, _ := worktreeFile(p)
	line, _, _ := strings.Cut(head, "\n")
	return strings.TrimSpace(line)
}

// worktreeHas reports whether dir holds an entry named name. One that
// cannot be looked at counts as there.
func worktreeHas(dir, name string) bool {
	_, err := os.Lstat(filepath.Join(dir, name))
	return !errors.Is(err, fs.ErrNotExist)
}

// removeWorktree clears git's record of w and moves its folder to the Trash,
// when its state is still seen. code is "" when it is gone, otherwise
// "in-use", wtCancelled or one of the wtFail outcomes; freed is measured
// before it moves.
//
// Everything is read again from w.Path. git forgets the worktree while its
// folder waits under a temporary name beside it, so git finds no folder to
// delete; the folder then comes back and goes to the Trash. When git does
// not forget it, the folder comes back and nothing has changed. A cancel
// stops the removal only until the folder moves.
func removeWorktree(ctx context.Context, w agentWorktree, seen string, guard *usageGuard) (freed int64, code string) {
	path := w.Path
	if info, err := os.Lstat(path); err != nil || !info.IsDir() || !worktreeListable(path) {
		return 0, wtFailChanged
	}
	// Measured first: a walk can take seconds, and what the folder is
	// judged to be must be the last look before it moves.
	size, _, _ := measureAI(ctx, path, nil)
	if ctx.Err() != nil {
		return 0, wtCancelled
	}
	worktreeMoving.Lock()
	defer worktreeMoving.Unlock()
	git := worktreeGitTool()
	now := judgeWorktree(ctx, path, git, worktreeUsersOf(worktreeUse()))
	switch {
	case ctx.Err() != nil:
		return 0, wtCancelled
	case now.State != seen || now.State != wtClean && now.State != wtDirty:
		return 0, wtFailChanged
	case guard.inUse(resolvePath(path)):
		return 0, "in-use"
	}
	temp := path + worktreeTemp
	if worktreeHas(filepath.Dir(temp), filepath.Base(temp)) {
		return 0, wtFailGit
	}
	repo := "--git-dir=" + now.Common
	unlocked := true
	// The state is clean or dirty, so a lock still there is stale.
	if worktreeHas(now.Admin, "locked") {
		_, err := worktreeGit(ctx, git, []string{repo, "-c", "core.fsmonitor=false", "worktree", "unlock", path}, worktreeGitLimit)
		unlocked = err == nil
	}
	switch {
	case ctx.Err() != nil:
		return 0, wtCancelled
	case !unlocked:
		return 0, wtFailGit
	}
	if err := os.Rename(path, temp); err != nil {
		return 0, wtFailGit
	}
	// The folder is away from its path now: whatever the caller says, the
	// rest runs to its end, so the folder never stays away.
	steady := context.WithoutCancel(ctx)
	cleared := false
	if !worktreeHas(filepath.Dir(path), filepath.Base(path)) {
		// The record says whether git forgot the worktree, not git's answer:
		// a git its time limit stops may have cleared the record already,
		// and one that answers success may have left it.
		_, _ = worktreeGit(steady, git, []string{repo, "-c", "core.fsmonitor=false", "worktree", "remove", path}, worktreeGitLimit)
		_, err := os.Lstat(now.Admin)
		cleared = errors.Is(err, fs.ErrNotExist)
	}
	if !cleared {
		_ = os.Rename(temp, path)
		return 0, wtFailGit
	}
	target := path
	if os.Rename(temp, path) != nil {
		target = temp
	}
	if err := trashWithContext(steady, target); err != nil {
		return 0, wtFailTrash
	}
	return size, ""
}
