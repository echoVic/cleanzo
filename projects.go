package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// Build folders of idle projects: the junk scan offers one row per project
// nothing changed in for projectIdle, holding the folders its next build or
// install recreates. A folder is offered only when its name, a marker file
// and, inside a repository, git all say it is build output.

// projectIDPrefix starts the ID of a project row; the rest is the project's path.
const projectIDPrefix = "project:"

// projectIdle is how long nothing in a project may have changed before its
// build folders are offered.
const projectIdle = 14 * 24 * time.Hour

// projectWalkLevels is how deep below the home folder build folders are
// looked for; the home folder's entries are level 1.
const projectWalkLevels = 6

// projectIdleLevels is how deep into a project its last change is looked for.
const projectIdleLevels = 3

// projectGitLevels is how far below a build folder a .git entry is looked
// for, such as that of an editable install pip cloned into .venv/src.
const projectGitLevels = 3

// buildFolderNames are the folders the walk records and never enters.
var buildFolderNames = map[string]bool{
	"node_modules": true, ".next": true, ".nuxt": true, ".svelte-kit": true, ".turbo": true,
	".venv": true, "venv": true, "target": true, "Pods": true, "build": true, "dist": true, "out": true,
}

// projectFiles mark a project beside a build, dist or out folder.
var projectFiles = []string{"package.json", "go.mod", "Cargo.toml", "pyproject.toml", "pubspec.yaml", "build.gradle", "build.gradle.kts", "pom.xml"}

// projectSkippedHome are the home folder's entries that hold other things than projects.
var projectSkippedHome = []string{"Library", "Applications", "Music", "Pictures", "Movies", "Public"}

// devProject is an idle project and its build folders, as walked from the
// home folder.
type devProject struct {
	root     string
	folders  []string
	idleDays int
}

// gitCheckIgnore runs git check-ignore in repo for paths relative to it and
// returns what git printed. Only check-ignore runs, without optional locks,
// prompts or a file system monitor, and for five seconds at most. Tests
// replace it.
var gitCheckIgnore = func(ctx context.Context, git, repo string, paths []string) (string, error) {
	input := strings.Join(paths, "\x00") + "\x00"
	return runToolWith(ctx, git, []string{"-C", repo, "-c", "core.fsmonitor=false", "check-ignore", "-z", "--stdin"}, 5*time.Second, func(cmd *exec.Cmd) ([]byte, error) {
		cmd.Stdin = strings.NewReader(input)
		// An inherited GIT_DIR or GIT_WORK_TREE would point git at another
		// repository. The ceiling keeps git from looking above the project
		// when its .git is not a repository, so it fails instead of
		// answering for an enclosing one.
		cmd.Env = append(slices.DeleteFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "GIT_") }),
			"GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_CEILING_DIRECTORIES="+filepath.Dir(repo))
		return runToolCmd(cmd)
	})
}

// developerToolsInstalled reports whether Apple's developer tools are
// installed: without them /usr/bin/git opens a dialog offering to install
// them instead of running. Tests replace it.
var developerToolsInstalled = func(ctx context.Context) bool {
	_, err := runTool(ctx, "/usr/bin/xcode-select", []string{"-p"}, 5*time.Second)
	return err == nil
}

// idleProjects finds the idle projects under the home folder and the build
// folders each offers, in path order. Only macOS has them.
func (env junkEnv) idleProjects(claims claimSet) []devProject {
	if runtime.GOOS != "darwin" {
		return nil
	}
	home := filepath.Clean(env.home)
	projects := map[string]*devProject{}
	repos := map[string]bool{}
	hasGit := map[string]bool{}
	for _, dir := range env.findBuildFolders(claims) {
		ok, needsGit := buildFolder(dir)
		if !ok {
			continue
		}
		root, repo := projectRoot(home, dir, hasGit)
		// The home folder is never a project, even with a repository in it.
		if root == home || needsGit && !repo {
			continue
		}
		p := projects[root]
		if p == nil {
			p = &devProject{root: root}
			projects[root] = p
		}
		p.folders = append(p.folders, dir)
		repos[root] = repo
	}
	var idle []*devProject
	for _, p := range projects {
		changed, ok := lastChange(p.root)
		age := now().Sub(changed)
		if !ok || age < projectIdle {
			continue
		}
		p.idleDays = int(age / (24 * time.Hour))
		idle = append(idle, p)
	}
	env.keepIgnored(idle, repos)
	var found []devProject
	for _, p := range idle {
		p.folders = slices.DeleteFunc(p.folders, func(dir string) bool { return holdsGit(dir, 0) })
		if len(p.folders) > 0 {
			found = append(found, *p)
		}
	}
	slices.SortFunc(found, func(a, b devProject) int { return strings.Compare(a.root, b.root) })
	return found
}

// keepIgnored keeps, in each repository's project, the folders git ignores
// and tracks nothing in. Git is asked once per repository; when it is
// missing or fails, the repository keeps nothing.
func (env junkEnv) keepIgnored(projects []*devProject, repos map[string]bool) {
	var git string
	if slices.ContainsFunc(projects, func(p *devProject) bool { return repos[p.root] }) {
		git = env.projectGit()
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, scanConcurrency)
	for _, p := range projects {
		if !repos[p.root] {
			continue
		}
		if git == "" {
			p.folders = nil
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			p.folders = env.gitIgnored(git, p.root, p.folders)
		}()
	}
	wg.Wait()
}

// projectGit is the git to ask, or "" when none can answer.
func (env junkEnv) projectGit() string {
	if env.findTool == nil {
		return ""
	}
	git := env.findTool("git")
	if git == "/usr/bin/git" && !developerToolsInstalled(env.toolContext()) {
		return ""
	}
	return git
}

// gitIgnored returns the folders of the repository at root that git
// ignores. A folder holding a tracked file is not ignored, and git says so.
// It returns nothing when git fails.
func (env junkEnv) gitIgnored(git, root string, folders []string) []string {
	rels := make([]string, len(folders))
	for i, dir := range folders {
		rels[i], _ = filepath.Rel(root, dir)
	}
	out, err := gitCheckIgnore(env.toolContext(), git, root, rels)
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) { // 1: none of them is ignored
		return nil
	}
	// The output holds the ignored paths; anything git also printed (warnings
	// share the stream) matches no path asked about.
	said := map[string]bool{}
	for _, line := range strings.FieldsFunc(out, func(r rune) bool { return r == 0 || r == '\n' }) {
		said[line] = true
	}
	var kept []string
	for i, dir := range folders {
		if said[rels[i]] {
			kept = append(kept, dir)
		}
	}
	return kept
}

// findBuildFolders walks the home folder for folders named as build folders,
// without entering them. It skips hidden entries, the home folder's Library
// and media folders, packages, links, and folders the scan protects or another
// row has taken. It enters the agents' worktrees: ~/.codex/worktrees/*/* and
// <project>/.claude/worktrees/*, the latter walked as deep as the project
// they copy.
func (env junkEnv) findBuildFolders(claims claimSet) []string {
	home := filepath.Clean(env.home)
	if env.traversalBlocked(home) {
		return nil
	}
	resolvedHome := resolvePath(home)
	homeKey := strings.ToLower(resolvedHome)
	// The walk follows no links, so a folder resolves to the resolved home
	// folder and the rest of its path.
	enterable := func(dir string) bool {
		if dir == home {
			return true
		}
		key := strings.ToLower(filepath.Join(resolvedHome, strings.TrimPrefix(dir, home)))
		if env.protectedKey(key) || claims.claimedKey(key) {
			return false
		}
		return safeResolved(key) || slices.Contains(protectedHomeDirs, strings.TrimPrefix(key, homeKey))
	}
	var found []string
	var walk func(dir string, level int)
	walk = func(dir string, level int) {
		if env.toolContext().Err() != nil || !enterable(dir) {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() { // files, and links, which are never followed
				continue
			}
			name, path := e.Name(), filepath.Join(dir, e.Name())
			switch {
			case buildFolderNames[name]:
				found = append(found, path)
			case name == ".claude" && level > 1:
				for _, worktree := range subfolders(filepath.Join(path, "worktrees")) {
					walk(worktree, level)
				}
			case name == ".codex" && level == 1:
				// ~/.codex/worktrees/<id>/<name>: the worktree is level 4.
				for _, group := range subfolders(filepath.Join(path, "worktrees")) {
					for _, worktree := range subfolders(group) {
						walk(worktree, 5)
					}
				}
			case strings.HasPrefix(name, "."), isPackage(name), level == 1 && slices.Contains(projectSkippedHome, name):
			case level < projectWalkLevels:
				walk(path, level+1)
			}
		}
	}
	walk(home, 1)
	return found
}

// subfolders lists the folders in dir, which must be a folder, not a link.
func subfolders(dir string) []string {
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return nil
	}
	entries, _ := os.ReadDir(dir)
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(dir, e.Name()))
		}
	}
	return dirs
}

// buildFolder reports whether dir, named as a build folder, has the marker
// its name needs. needsGit is true for build, dist and out, which are often
// source: only a repository that ignores them makes them build output.
func buildFolder(dir string) (ok, needsGit bool) {
	parent := filepath.Dir(dir)
	beside := func(name string) bool { return regularFile(filepath.Join(parent, name)) }
	inside := func(name string) bool { return regularFile(filepath.Join(dir, name)) }
	switch filepath.Base(dir) {
	case "node_modules", ".next", ".nuxt", ".svelte-kit", ".turbo":
		return beside("package.json"), false
	case ".venv", "venv":
		return inside("pyvenv.cfg"), false
	case "target":
		return beside("Cargo.toml") && inside("CACHEDIR.TAG"), false
	case "Pods":
		return beside("Podfile"), false
	case "build", "dist", "out":
		return slices.ContainsFunc(projectFiles, beside), true
	}
	return false, false
}

// holdsGit reports whether dir, or a folder down to projectGitLevels below
// it, holds a .git entry of any kind: a worktree, a repository or a checkout,
// with work that may be nowhere else, so dir is never offered, whatever its
// name and whatever git says. Links are not followed. A folder that cannot
// be read may hold one, and counts as holding one.
func holdsGit(dir string, level int) bool {
	if _, err := os.Lstat(filepath.Join(dir, ".git")); !errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if level == projectGitLevels {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	return slices.ContainsFunc(entries, func(e fs.DirEntry) bool {
		return e.IsDir() && holdsGit(filepath.Join(dir, e.Name()), level+1)
	})
}

// regularFile reports whether p is a regular file, not a link.
func regularFile(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode().IsRegular()
}

// projectRoot is the project a build folder belongs to: the nearest folder
// above it holding .git, up to the home folder, or else the folder it is in.
// repo reports a .git. hasGit remembers the folders already looked at.
func projectRoot(home, dir string, hasGit map[string]bool) (root string, repo bool) {
	for p := filepath.Dir(dir); within(p, home); p = filepath.Dir(p) {
		has, seen := hasGit[p]
		if !seen {
			_, err := os.Lstat(filepath.Join(p, ".git"))
			has = err == nil
			hasGit[p] = has
		}
		if has {
			return p, true
		}
		if p == home {
			break
		}
	}
	return filepath.Dir(dir), false
}

// lastChange is when anything in the project at root last changed: its
// entries to projectIdleLevels deep, hidden and build folders left out, and
// git's index, HEAD and HEAD log. ok is false when a folder could not be
// read, or git's folder could not be found, as a change may hide there.
func lastChange(root string) (newest time.Time, ok bool) {
	saw := func(info fs.FileInfo) {
		if t := changedAt(info); t.After(newest) {
			newest = t
		}
	}
	info, err := os.Lstat(root)
	if err != nil {
		return newest, false
	}
	saw(info)
	ok = true
	var walk func(dir string, level int)
	walk = func(dir string, level int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			ok = false
			return
		}
		for _, e := range entries {
			if e.IsDir() && (strings.HasPrefix(e.Name(), ".") || buildFolderNames[e.Name()]) {
				continue
			}
			if info, err := e.Info(); err == nil {
				saw(info)
			}
			if e.IsDir() && level < projectIdleLevels {
				walk(filepath.Join(dir, e.Name()), level+1)
			}
		}
	}
	walk(root, 1)
	gitDir, found := gitDirOf(root)
	if !found {
		return newest, false
	}
	if gitDir != "" {
		for _, name := range []string{"index", "HEAD", filepath.Join("logs", "HEAD")} {
			if info, err := os.Lstat(filepath.Join(gitDir, name)); err == nil {
				saw(info)
			}
		}
	}
	return newest, ok
}

// gitDirOf is the folder holding the git state of the project at root: its
// .git folder, or for a linked worktree or a submodule, where its .git file
// points, a relative path counting from root. It is "" without a .git, and
// found is false when the .git file cannot be read or leads nowhere.
func gitDirOf(root string) (dir string, found bool) {
	dotGit := filepath.Join(root, ".git")
	info, err := os.Lstat(dotGit)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", true
	case err != nil:
		return "", false
	case !info.Mode().IsRegular():
		return dotGit, true
	}
	f, err := os.Open(dotGit)
	if err != nil {
		return "", false
	}
	defer f.Close()
	head, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "", false
	}
	line, _, _ := strings.Cut(string(head), "\n")
	path, isGitDir := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	path = strings.TrimSpace(path)
	if !isGitDir || path == "" {
		return "", false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return "", false
	}
	return path, true
}
