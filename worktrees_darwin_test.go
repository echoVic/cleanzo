package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// wtRepo makes a temporary home with a repository at code/app holding one
// commit on main, and lets the worktree rules run the installed git. It
// returns git and the repository's folder.
func wtRepo(t *testing.T) (git, repo string) {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	return wtRepoIn(t, home)
}

// wtRepoIn is wtRepo in a home folder already set up. The rules find git,
// as if developer tools were installed, through a wrapper that keeps the
// system git configuration out.
func wtRepoIn(t *testing.T, home string) (git, repo string) {
	t.Helper()
	git = installedGit(t)
	savedTools, savedDirs := developerToolsInstalled, toolDirs
	developerToolsInstalled = func(context.Context) bool { return true }
	bin := t.TempDir()
	fakeTool(t, bin, "git", "GIT_CONFIG_NOSYSTEM=1 exec "+shellPath(git)+" \"$@\"\n")
	toolDirs = func() []string { return []string{bin} }
	t.Cleanup(func() { developerToolsInstalled, toolDirs = savedTools, savedDirs })
	repo = filepath.Join(home, "code", "app")
	wtCommitted(t, git, repo)
	return git, repo
}

// wtCommitted makes dir a repository with one commit on main.
func wtCommitted(t *testing.T, git, dir string) {
	t.Helper()
	writeText(t, filepath.Join(dir, "README.md"), "app\n")
	gitIn(t, git, dir, "init", "-q", "-b", "main")
	gitIn(t, git, dir, "add", "README.md")
	gitCommit(t, git, dir)
}

// addWorktree adds a worktree of repo at <repo>/.claude/worktrees/<name> on
// a new branch, or detached at main's commit when branch is "".
func addWorktree(t *testing.T, git, repo, name, branch string) string {
	t.Helper()
	path := filepath.Join(repo, ".claude", "worktrees", name)
	if branch == "" {
		gitIn(t, git, repo, "worktree", "add", "-q", "--detach", path, "main")
	} else {
		gitIn(t, git, repo, "worktree", "add", "-q", "-b", branch, path, "main")
	}
	return path
}

// stubWorktreeUse makes cwds the working folders of this user's processes
// and sessions those of the open Claude Code sessions.
func stubWorktreeUse(t *testing.T, cwds, sessions []string) {
	t.Helper()
	saved := worktreeUse
	worktreeUse = func() worktreeUsage { return worktreeUsage{Cwds: cwds, Sessions: sessions, OK: true} }
	t.Cleanup(func() { worktreeUse = saved })
}

// stubPidStart makes starts the running processes, each with when it started.
func stubPidStart(t *testing.T, starts map[int32]time.Time) {
	t.Helper()
	saved := worktreePidStart
	worktreePidStart = func(pid int32) (time.Time, bool) {
		start, ok := starts[pid]
		return start, ok
	}
	t.Cleanup(func() { worktreePidStart = saved })
}

// wtAdmin is git's record of the worktree at wt, as its .git file names it.
func wtAdmin(t *testing.T, wt string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	admin, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		t.Fatalf("%s/.git = %q", wt, data)
	}
	return admin
}

// wtGitOut runs git in dir like gitIn and returns what it printed, trimmed.
func wtGitOut(t *testing.T, git, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "GIT_") }), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// wtFound returns the worktree findWorktrees lists at path, failing the
// test when it lists none there or more than one.
func wtFound(t *testing.T, kind, path string) agentWorktree {
	t.Helper()
	var hits []agentWorktree
	found := findWorktrees(context.Background(), kind)
	for _, w := range found {
		if w.Path == path {
			hits = append(hits, w)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("%s listed %d times in %+v", path, len(hits), found)
	}
	return hits[0]
}
