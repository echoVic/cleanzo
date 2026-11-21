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

// wtLock locks the worktree at wt with reason, as git worktree lock does,
// and returns when the lock was written.
func wtLock(t *testing.T, git, repo, wt, reason string) time.Time {
	t.Helper()
	gitIn(t, git, repo, "worktree", "lock", "--reason", reason, wt)
	info, err := os.Stat(filepath.Join(wtAdmin(t, wt), "locked"))
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

// TestWorktreeStates judges one worktree per case; the first rule that
// holds decides.
func TestWorktreeStates(t *testing.T) {
	const reason = "claude agent (pid 4242)"
	for _, tc := range []struct {
		name    string
		stage   func(t *testing.T, git, repo, wt string)
		state   string
		changes int
	}{
		{"untouched", nil, wtClean, 0},
		{"an untracked file and a changed one", func(t *testing.T, git, repo, wt string) {
			writeText(t, filepath.Join(wt, "notes.txt"), "draft\n")
			writeText(t, filepath.Join(wt, "README.md"), "changed\n")
		}, wtDirty, 2},
		{"changed again after git add (MM)", func(t *testing.T, git, repo, wt string) {
			writeText(t, filepath.Join(wt, "README.md"), "staged\n")
			gitIn(t, git, wt, "add", "README.md")
			writeText(t, filepath.Join(wt, "README.md"), "changed again\n")
		}, wtIndexOnly, 0},
		{"added, then deleted from disk (AD)", func(t *testing.T, git, repo, wt string) {
			writeText(t, filepath.Join(wt, "new.txt"), "new\n")
			gitIn(t, git, wt, "add", "new.txt")
			if err := os.Remove(filepath.Join(wt, "new.txt")); err != nil {
				t.Fatal(err)
			}
		}, wtIndexOnly, 0},
		{"rebase-merge in the record", func(t *testing.T, git, repo, wt string) {
			mustMkdir(t, filepath.Join(wtAdmin(t, wt), "rebase-merge"))
		}, wtMidOp, 0},
		{"MERGE_HEAD in the record", func(t *testing.T, git, repo, wt string) {
			writeText(t, filepath.Join(wtAdmin(t, wt), "MERGE_HEAD"), wtGitOut(t, git, wt, "rev-parse", "HEAD")+"\n")
		}, wtMidOp, 0},
		{"detached, with a commit on no branch", func(t *testing.T, git, repo, wt string) {
			gitIn(t, git, wt, "switch", "-q", "--detach")
			writeText(t, filepath.Join(wt, "work.txt"), "work\n")
			gitIn(t, git, wt, "add", "work.txt")
			gitCommit(t, git, wt)
		}, wtUnbranched, 0},
		{"detached on main's commit", func(t *testing.T, git, repo, wt string) {
			gitIn(t, git, wt, "switch", "-q", "--detach", "main")
		}, wtClean, 0},
		{"modules in the record", func(t *testing.T, git, repo, wt string) {
			mustMkdir(t, filepath.Join(wtAdmin(t, wt), "modules"))
		}, wtSubmodule, 0},
		{"a process works in a folder inside it", func(t *testing.T, git, repo, wt string) {
			mustMkdir(t, filepath.Join(wt, "src"))
			stubWorktreeUse(t, []string{filepath.Join(wt, "src")}, nil)
		}, wtInUse, 0},
		{"an open session works in it", func(t *testing.T, git, repo, wt string) {
			stubWorktreeUse(t, nil, []string{wt})
		}, wtInUse, 0},
		{"locked by a live process older than the lock", func(t *testing.T, git, repo, wt string) {
			locked := wtLock(t, git, repo, wt, reason)
			stubPidStart(t, map[int32]time.Time{4242: locked.Add(-time.Hour)})
		}, wtInUse, 0},
		{"locked by a process that is gone", func(t *testing.T, git, repo, wt string) {
			wtLock(t, git, repo, wt, reason)
			stubPidStart(t, map[int32]time.Time{})
		}, wtClean, 0},
		{"locked by a pid a newer process now has", func(t *testing.T, git, repo, wt string) {
			locked := wtLock(t, git, repo, wt, reason)
			stubPidStart(t, map[int32]time.Time{4242: locked.Add(time.Hour)})
		}, wtClean, 0},
		{"locked for a reason naming no process", func(t *testing.T, git, repo, wt string) {
			wtLock(t, git, repo, wt, "keep this")
		}, wtInUse, 0},
		{".git names a record that does not exist", func(t *testing.T, git, repo, wt string) {
			writeText(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(repo, ".git", "worktrees", "missing")+"\n")
		}, wtUnknown, 0},
		{"the record's gitdir names another folder", func(t *testing.T, git, repo, wt string) {
			writeText(t, filepath.Join(wtAdmin(t, wt), "gitdir"), filepath.Join(homeDir(), "elsewhere", ".git")+"\n")
		}, wtUnknown, 0},
		{"working folders cannot be read", func(t *testing.T, git, repo, wt string) {
			saved := worktreeUse
			worktreeUse = func() worktreeUsage { return worktreeUsage{} }
			t.Cleanup(func() { worktreeUse = saved })
		}, wtInUse, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git, repo := wtRepo(t)
			stubWorktreeUse(t, nil, nil)
			stubPidStart(t, nil)
			wt := addWorktree(t, git, repo, "agent-1", "agent-1")
			if tc.stage != nil {
				tc.stage(t, git, repo, wt)
			}
			w := wtFound(t, "claude", wt)
			if w.State != tc.state || w.Changes != tc.changes {
				t.Fatalf("state = %s with %d changes, want %s with %d", w.State, w.Changes, tc.state, tc.changes)
			}
			if tc.stage == nil && (w.Name != "agent-1" || w.Repo != repo || w.Branch != "agent-1" || w.Size == 0 || w.Last == 0) {
				t.Fatalf("worktree = %+v", w)
			}
		})
	}
}

// TestWorktreeReftableHead: a repository that keeps its refs in a reftable
// writes the placeholder branch .invalid in a worktree's HEAD file whatever
// is checked out, so a detached HEAD on a commit no branch holds cannot be
// told from a branch: such a worktree cannot be removed.
func TestWorktreeReftableHead(t *testing.T) {
	git, _ := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	repo := filepath.Join(homeDir(), "code", "tables")
	mustMkdir(t, repo)
	cmd := exec.Command(git, "-C", repo, "init", "-q", "-b", "main", "--ref-format=reftable")
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "GIT_") }), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("this git keeps no reftable: %v\n%s", err, out)
	}
	writeText(t, filepath.Join(repo, "README.md"), "tables\n")
	gitIn(t, git, repo, "add", "README.md")
	gitCommit(t, git, repo)
	wt := addWorktree(t, git, repo, "agent-1", "")
	writeText(t, filepath.Join(wt, "work.txt"), "work\n")
	gitIn(t, git, wt, "add", "work.txt")
	gitCommit(t, git, wt)
	if head := firstLineOf(t, filepath.Join(wtAdmin(t, wt), "HEAD")); head != "ref: refs/heads/.invalid" {
		t.Skipf("HEAD file = %q", head)
	}
	if w := wtFound(t, "claude", wt); w.State != wtNoGit || w.Branch != "" {
		t.Fatalf("worktree = %+v", w)
	}
}

// firstLineOf is the first line of the file at p.
func firstLineOf(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	line, _, _ := strings.Cut(string(data), "\n")
	return line
}
