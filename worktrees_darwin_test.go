package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
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
		{"a folder in its .claude/worktrees", func(t *testing.T, git, repo, wt string) {
			mustMkdir(t, filepath.Join(wt, ".claude", "worktrees", "agent-2"))
		}, wtHolds, 0},
		{"a folder in its .claude/worktrees, and modules in the record", func(t *testing.T, git, repo, wt string) {
			mustMkdir(t, filepath.Join(wt, ".claude", "worktrees", "agent-2"))
			mustMkdir(t, filepath.Join(wtAdmin(t, wt), "modules"))
		}, wtHolds, 0},
		{"its .claude/worktrees cannot be read", func(t *testing.T, git, repo, wt string) {
			dir := filepath.Join(wt, ".claude", "worktrees")
			mustMkdir(t, dir)
			if err := os.Chmod(dir, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
			if _, err := os.ReadDir(dir); err == nil {
				t.Skip("this user reads any folder")
			}
		}, wtHolds, 0},
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

// wtNothingOpen is a guard that sees nothing in use.
var wtNothingOpen = &usageGuard{running: map[string]bool{}}

// wtTrashed reports whether the Trash holds rel inside the folder named name.
func wtTrashed(name, rel string) bool {
	return exists(filepath.Join(homeDir(), ".Trash", name, rel))
}

// TestRemoveWorktree: git forgets the worktree, its branch stays where it
// was and can be checked out, and the folder with what it holds is in the
// Trash.
func TestRemoveWorktree(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	admin := wtAdmin(t, wt)
	sha := wtGitOut(t, git, repo, "rev-parse", "refs/heads/agent-1")
	size, _, _ := measureAI(context.Background(), wt, nil)
	freed, code := removeWorktree(context.Background(), wtFound(t, "claude", wt), wtClean, wtNothingOpen)
	if code != "" || freed != size || size == 0 {
		t.Fatalf("freed %d (measured %d), code %q", freed, size, code)
	}
	if !wtTrashed("agent-1", "README.md") || exists(wt) || exists(wt+worktreeTemp) || exists(admin) {
		t.Fatalf("trashed %v, folder there %v, record there %v", wtTrashed("agent-1", "README.md"), exists(wt), exists(admin))
	}
	if got := wtGitOut(t, git, repo, "rev-parse", "refs/heads/agent-1"); got != sha {
		t.Fatalf("branch moved from %s to %s", sha, got)
	}
	gitIn(t, git, repo, "switch", "-q", "agent-1")
}

// TestRemoveWorktreeKeepsEveryFile: what git does not track and what it
// ignores go to the Trash with the folder.
func TestRemoveWorktreeKeepsEveryFile(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	writeText(t, filepath.Join(wt, ".gitignore"), ".env\nnode_modules/\n")
	writeText(t, filepath.Join(wt, "notes.txt"), "draft\n")
	writeText(t, filepath.Join(wt, ".env"), "API_KEY=example\n")
	writeText(t, filepath.Join(wt, "node_modules", "x", "index.js"), "module.exports = 1\n")
	w := wtFound(t, "claude", wt)
	if w.State != wtDirty || w.Changes != 2 {
		t.Fatalf("worktree = %+v", w)
	}
	if _, code := removeWorktree(context.Background(), w, wtDirty, wtNothingOpen); code != "" {
		t.Fatalf("code %q", code)
	}
	for _, rel := range []string{"notes.txt", ".env", filepath.Join("node_modules", "x", "index.js")} {
		if !wtTrashed("agent-1", rel) {
			t.Errorf("%s is not in the Trash", rel)
		}
	}
}

// TestRemoveWorktreeNeverLetsGitDelete: git is asked to forget the worktree
// only while its folder is away from the path git knows, so git never
// deletes a file of it.
func TestRemoveWorktreeNeverLetsGitDelete(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	saved := worktreeGit
	var asked, there bool
	worktreeGit = func(ctx context.Context, git string, args []string, limit time.Duration) (string, error) {
		if slices.Contains(args, "remove") {
			asked, there = true, exists(wt)
		}
		return saved(ctx, git, args, limit)
	}
	t.Cleanup(func() { worktreeGit = saved })
	if _, code := removeWorktree(context.Background(), wtFound(t, "claude", wt), wtClean, wtNothingOpen); code != "" || !asked || there {
		t.Fatalf("code %q, git asked %v, folder in place then %v", code, asked, there)
	}
}

// TestRemoveWorktreeWhileAScanRuns: a scan that starts while git forgets a
// worktree waits until the removal has ended, and never puts the folder
// back where git would delete it.
func TestRemoveWorktreeWhileAScanRuns(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	w := wtFound(t, "claude", wt)
	saved := worktreeGit
	started, scanned := make(chan struct{}), make(chan []agentWorktree, 1)
	var back, early bool
	worktreeGit = func(ctx context.Context, git string, args []string, limit time.Duration) (string, error) {
		if !slices.Contains(args, "remove") {
			return saved(ctx, git, args, limit)
		}
		go func() {
			close(started)
			scanned <- findWorktrees(context.Background(), "claude")
		}()
		<-started
		for until := time.Now().Add(time.Second); time.Now().Before(until) && !back; time.Sleep(10 * time.Millisecond) {
			back = exists(wt)
		}
		select {
		case <-scanned:
			early = true
		default:
		}
		if back {
			// git would delete the folder now: it is not asked.
			return "", errors.New("the folder is back in place")
		}
		return saved(ctx, git, args, limit)
	}
	t.Cleanup(func() { worktreeGit = saved })
	_, code := removeWorktree(context.Background(), w, wtClean, wtNothingOpen)
	var found []agentWorktree
	select {
	case found = <-scanned:
	case <-time.After(30 * time.Second):
		t.Fatal("the scan has not returned")
	}
	if back || early || code != "" || len(found) != 0 {
		t.Fatalf("back in place while git ran %v, scan returned first %v, code %q, the scan found %+v", back, early, code, found)
	}
	if !wtTrashed("agent-1", "README.md") || exists(wt) || exists(wt+worktreeTemp) {
		t.Fatalf("trashed %v, folder there %v", wtTrashed("agent-1", "README.md"), exists(wt))
	}
}

// TestRemoveWorktreeChangedSinceSeen: a worktree that is not in the state
// the user saw any more is left as it is.
func TestRemoveWorktreeChangedSinceSeen(t *testing.T) {
	for _, tc := range []struct {
		name         string
		before, then func(t *testing.T, git, wt string)
		seen         string
	}{
		{"clean, then a new file", nil, func(t *testing.T, git, wt string) {
			writeText(t, filepath.Join(wt, "notes.txt"), "draft\n")
		}, wtClean},
		{"dirty, then content only the index has", func(t *testing.T, git, wt string) {
			writeText(t, filepath.Join(wt, "notes.txt"), "draft\n")
		}, func(t *testing.T, git, wt string) {
			gitIn(t, git, wt, "add", "notes.txt")
			writeText(t, filepath.Join(wt, "notes.txt"), "draft, changed\n")
		}, wtDirty},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git, repo := wtRepo(t)
			stubWorktreeUse(t, nil, nil)
			wt := addWorktree(t, git, repo, "agent-1", "agent-1")
			if tc.before != nil {
				tc.before(t, git, wt)
			}
			w := wtFound(t, "claude", wt)
			if w.State != tc.seen {
				t.Fatalf("seen %s, want %s", w.State, tc.seen)
			}
			tc.then(t, git, wt)
			if _, code := removeWorktree(context.Background(), w, tc.seen, wtNothingOpen); code != wtFailChanged {
				t.Fatalf("code %q", code)
			}
			if !exists(filepath.Join(wt, "notes.txt")) || !exists(wtAdmin(t, wt)) || wtTrashed("agent-1", "") {
				t.Fatal("the worktree was touched")
			}
		})
	}
}

// TestRemoveWorktreeThatHoldsWorktrees: a worktree another worktree was
// made in stays until that one has gone, so no record is left for a folder
// in the Trash. The inner one goes on its own; then the outer one is judged
// by its status again.
func TestRemoveWorktreeThatHoldsWorktrees(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	outer := addWorktree(t, git, repo, "agent-1", "agent-1")
	seen := wtFound(t, "claude", outer)
	if seen.State != wtClean {
		t.Fatalf("seen %+v", seen)
	}
	inner := filepath.Join(outer, ".claude", "worktrees", "agent-2")
	gitIn(t, git, outer, "worktree", "add", "-q", "-b", "agent-2", inner, "main")
	outerAdmin, innerAdmin := wtAdmin(t, outer), wtAdmin(t, inner)
	// Clean as the user saw it, or dirty as git status now reads it.
	for _, state := range []string{wtClean, wtDirty} {
		if _, code := removeWorktree(context.Background(), seen, state, wtNothingOpen); code != wtFailChanged {
			t.Fatalf("seen %s: code %q", state, code)
		}
	}
	if !exists(filepath.Join(inner, "README.md")) || exists(outer+worktreeTemp) || !exists(outerAdmin) || !exists(innerAdmin) || wtTrashed("agent-1", "") {
		t.Fatal("a worktree was touched")
	}
	if w := wtFound(t, "claude", outer); w.State != wtHolds {
		t.Fatalf("outer = %+v", w)
	}
	if _, code := removeWorktree(context.Background(), wtFound(t, "claude", inner), wtClean, wtNothingOpen); code != "" {
		t.Fatalf("inner: code %q", code)
	}
	if exists(inner) || exists(innerAdmin) || !wtTrashed("agent-2", "README.md") {
		t.Fatalf("inner there %v, its record there %v", exists(inner), exists(innerAdmin))
	}
	if w := wtFound(t, "claude", outer); w.State != wtClean {
		t.Fatalf("outer, once the inner one has gone = %+v", w)
	}
}

// TestRemoveWorktreeWhenGitFails: when git does not forget the worktree,
// the folder is back in place and still a worktree.
func TestRemoveWorktreeWhenGitFails(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	admin := wtAdmin(t, wt)
	saved := worktreeGit
	worktreeGit = func(ctx context.Context, git string, args []string, limit time.Duration) (string, error) {
		if slices.Contains(args, "remove") {
			return "fatal: made up\n", errors.New("exit status 128")
		}
		return saved(ctx, git, args, limit)
	}
	t.Cleanup(func() { worktreeGit = saved })
	if _, code := removeWorktree(context.Background(), wtFound(t, "claude", wt), wtClean, wtNothingOpen); code != wtFailGit {
		t.Fatalf("code %q", code)
	}
	if !exists(filepath.Join(wt, "README.md")) || exists(wt+worktreeTemp) || !exists(admin) {
		t.Fatalf("folder in place %v, record there %v", exists(wt), exists(admin))
	}
	gitIn(t, git, wt, "status")
}

// TestRemoveWorktreeWhenGitFailsAfterForgetting: git that ends in an error,
// as when its time limit stops it, after it has cleared its record of the
// worktree has forgotten it all the same: the folder goes to the Trash.
func TestRemoveWorktreeWhenGitFailsAfterForgetting(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	admin := wtAdmin(t, wt)
	w := wtFound(t, "claude", wt)
	saved := worktreeGit
	worktreeGit = func(ctx context.Context, git string, args []string, limit time.Duration) (string, error) {
		out, err := saved(ctx, git, args, limit)
		if slices.Contains(args, "remove") && err == nil {
			return out, errors.New("signal: killed")
		}
		return out, err
	}
	t.Cleanup(func() { worktreeGit = saved })
	if _, code := removeWorktree(context.Background(), w, wtClean, wtNothingOpen); code != "" {
		t.Fatalf("code %q", code)
	}
	if !wtTrashed("agent-1", "README.md") || exists(wt) || exists(wt+worktreeTemp) || exists(admin) {
		t.Fatalf("trashed %v, folder there %v, record there %v", wtTrashed("agent-1", "README.md"), exists(wt), exists(admin))
	}
	gitIn(t, git, repo, "switch", "-q", "agent-1")
}

// TestRemoveWorktreeWhenGitOnlySaysItForgot: git that answers success while
// its record of the worktree is still there has not forgotten it: the
// folder is back in place and still a worktree.
func TestRemoveWorktreeWhenGitOnlySaysItForgot(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	admin := wtAdmin(t, wt)
	w := wtFound(t, "claude", wt)
	saved := worktreeGit
	worktreeGit = func(ctx context.Context, git string, args []string, limit time.Duration) (string, error) {
		if slices.Contains(args, "remove") {
			return "", nil
		}
		return saved(ctx, git, args, limit)
	}
	t.Cleanup(func() { worktreeGit = saved })
	if _, code := removeWorktree(context.Background(), w, wtClean, wtNothingOpen); code != wtFailGit {
		t.Fatalf("code %q", code)
	}
	if !exists(filepath.Join(wt, "README.md")) || exists(wt+worktreeTemp) || !exists(admin) || wtTrashed("agent-1", "") {
		t.Fatalf("folder in place %v, record there %v, trashed %v", exists(wt), exists(admin), wtTrashed("agent-1", ""))
	}
	gitIn(t, git, wt, "status")
}

// TestRemoveWorktreeWhenTheTempNameIsTaken: nothing moves when the name the
// folder would wait under is taken, even by an empty folder a rename would
// replace.
func TestRemoveWorktreeWhenTheTempNameIsTaken(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	mustMkdir(t, wt+worktreeTemp)
	if _, code := removeWorktree(context.Background(), wtFound(t, "claude", wt), wtClean, wtNothingOpen); code != wtFailGit {
		t.Fatalf("code %q", code)
	}
	entries, err := os.ReadDir(wt + worktreeTemp)
	if !exists(filepath.Join(wt, "README.md")) || !exists(wtAdmin(t, wt)) || err != nil || len(entries) != 0 {
		t.Fatalf("folder in place %v, the other folder holds %v (%v)", exists(wt), entries, err)
	}
}

// TestRemoveWorktreeWhenTrashFails: git has forgotten the worktree, the
// folder stays in place with every file, and its branch can be checked out.
func TestRemoveWorktreeWhenTrashFails(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	writeText(t, filepath.Join(wt, "notes.txt"), "draft\n")
	admin := wtAdmin(t, wt)
	saved := trashWithContext
	trashWithContext = func(context.Context, string) error { return errors.New("the Trash refused") }
	t.Cleanup(func() { trashWithContext = saved })
	if _, code := removeWorktree(context.Background(), wtFound(t, "claude", wt), wtDirty, wtNothingOpen); code != wtFailTrash {
		t.Fatalf("code %q", code)
	}
	if !exists(filepath.Join(wt, "README.md")) || !exists(filepath.Join(wt, "notes.txt")) || exists(wt+worktreeTemp) || exists(admin) {
		t.Fatalf("files in place %v, record there %v", exists(filepath.Join(wt, "notes.txt")), exists(admin))
	}
	gitIn(t, git, repo, "switch", "-q", "agent-1")
}

// TestRemoveWorktreeWithAStaleLock: a lock whose process is gone is lifted,
// and the worktree goes.
func TestRemoveWorktreeWithAStaleLock(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	stubPidStart(t, map[int32]time.Time{})
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	admin := wtAdmin(t, wt)
	wtLock(t, git, repo, wt, "claude agent (pid 4242)")
	if _, code := removeWorktree(context.Background(), wtFound(t, "claude", wt), wtClean, wtNothingOpen); code != "" {
		t.Fatalf("code %q", code)
	}
	if exists(wt) || exists(admin) || !wtTrashed("agent-1", "README.md") {
		t.Fatalf("folder there %v, record there %v", exists(wt), exists(admin))
	}
}

// TestRemoveWorktreeWhenTheUnlockFails: a stale lock git does not lift
// stops the removal before the folder moves; at most the lock is gone.
func TestRemoveWorktreeWhenTheUnlockFails(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	stubPidStart(t, map[int32]time.Time{})
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	admin := wtAdmin(t, wt)
	wtLock(t, git, repo, wt, "claude agent (pid 4242)")
	w := wtFound(t, "claude", wt)
	saved := worktreeGit
	var forgetting bool
	worktreeGit = func(ctx context.Context, git string, args []string, limit time.Duration) (string, error) {
		switch {
		case slices.Contains(args, "unlock"):
			return "", errors.New("exit status 128")
		case slices.Contains(args, "remove"):
			forgetting = true
		}
		return saved(ctx, git, args, limit)
	}
	t.Cleanup(func() { worktreeGit = saved })
	if _, code := removeWorktree(context.Background(), w, wtClean, wtNothingOpen); code != wtFailGit || forgetting {
		t.Fatalf("code %q, git asked to forget it %v", code, forgetting)
	}
	if !exists(filepath.Join(wt, "README.md")) || exists(wt+worktreeTemp) || !exists(admin) || wtTrashed("agent-1", "") {
		t.Fatalf("folder in place %v, record there %v, trashed %v", exists(wt), exists(admin), wtTrashed("agent-1", ""))
	}
	gitIn(t, git, wt, "status")
}

// TestRemoveWorktreeWithAnOpenFile: a worktree holding a file a process has
// open is kept, untouched.
func TestRemoveWorktreeWithAnOpenFile(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	guard := &usageGuard{open: []string{strings.ToLower(resolvePath(filepath.Join(wt, "README.md")))}}
	if _, code := removeWorktree(context.Background(), wtFound(t, "claude", wt), wtClean, guard); code != "in-use" {
		t.Fatalf("code %q", code)
	}
	if !exists(filepath.Join(wt, "README.md")) || !exists(wtAdmin(t, wt)) || exists(wt+worktreeTemp) {
		t.Fatal("the worktree was touched")
	}
}

// wtCountdown is a context that is cancelled once the code under test has
// asked it n times whether it is: it stops that code while at work.
type wtCountdown struct {
	context.Context
	cancel context.CancelFunc
	asked  atomic.Int64
	n      int64
}

// wtCancelledAfter is a wtCountdown that lets n questions pass.
func wtCancelledAfter(t *testing.T, n int64) *wtCountdown {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &wtCountdown{Context: ctx, cancel: cancel, n: n}
}

// Err cancels the context once it has been asked more than n times.
func (c *wtCountdown) Err() error {
	if c.asked.Add(1) > c.n {
		c.cancel()
	}
	return c.Context.Err()
}

// TestRemoveWorktreeCancelledWhileMeasuring: a clean cancelled while the
// worktree is measured stops before anything changes.
func TestRemoveWorktreeCancelledWhileMeasuring(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	for i := range 20 {
		writeText(t, filepath.Join(wt, "notes", fmt.Sprintf("%02d.txt", i)), "draft\n")
	}
	gitIn(t, git, wt, "add", "notes")
	gitCommit(t, git, wt)
	admin := wtAdmin(t, wt)
	w := wtFound(t, "claude", wt)
	// The walk asks before each of the worktree's 20 and more entries.
	ctx := wtCancelledAfter(t, 5)
	if _, code := removeWorktree(ctx, w, wtClean, wtNothingOpen); code != wtCancelled {
		t.Fatalf("code %q", code)
	}
	if !exists(filepath.Join(wt, "README.md")) || exists(wt+worktreeTemp) || !exists(admin) || wtTrashed("agent-1", "") {
		t.Fatalf("folder in place %v, record there %v, trashed %v", exists(wt), exists(admin), wtTrashed("agent-1", ""))
	}
	gitIn(t, git, wt, "status")
}

// TestRemoveWorktreeCancelledWhileGitRuns: once the folder is away from its
// path, a cancelled clean still runs to the end: git forgets the worktree
// and the folder goes to the Trash, though the Trash would turn down a
// cancelled move.
func TestRemoveWorktreeCancelledWhileGitRuns(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	admin := wtAdmin(t, wt)
	w := wtFound(t, "claude", wt)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	saved, savedTrash := worktreeGit, trashWithContext
	var stopped error // what git's context said once the clean was cancelled
	worktreeGit = func(gitCtx context.Context, git string, args []string, limit time.Duration) (string, error) {
		if slices.Contains(args, "remove") {
			cancel()
			stopped = gitCtx.Err()
		}
		return saved(gitCtx, git, args, limit)
	}
	trashWithContext = func(trashCtx context.Context, p string) error {
		if err := trashCtx.Err(); err != nil {
			return err
		}
		return savedTrash(trashCtx, p)
	}
	t.Cleanup(func() { worktreeGit, trashWithContext = saved, savedTrash })
	if _, code := removeWorktree(ctx, w, wtClean, wtNothingOpen); code != "" || stopped != nil || ctx.Err() == nil {
		t.Fatalf("code %q, git's context %v, the clean's %v", code, stopped, ctx.Err())
	}
	if !wtTrashed("agent-1", "README.md") || exists(wt) || exists(wt+worktreeTemp) || exists(admin) {
		t.Fatalf("trashed %v, folder there %v, record there %v", wtTrashed("agent-1", "README.md"), exists(wt), exists(admin))
	}
	gitIn(t, git, repo, "switch", "-q", "agent-1")
}

// TestWorktreeGitUnavailable: a worktree cannot be judged without a git that
// answers: none is found, /usr/bin/git is the placeholder that stands in
// for missing developer tools, or git runs out of time.
func TestWorktreeGitUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage func(t *testing.T)
	}{
		{"no git", func(t *testing.T) {
			empty := t.TempDir()
			toolDirs = func() []string { return []string{empty} }
		}},
		{"/usr/bin/git without developer tools", func(t *testing.T) {
			toolDirs = func() []string { return []string{"/usr/bin"} }
			if findTool("git") != "/usr/bin/git" {
				t.Skip("no /usr/bin/git")
			}
			developerToolsInstalled = func(context.Context) bool { return false }
		}},
		{"git runs out of time", func(t *testing.T) {
			saved := worktreeGit
			worktreeGit = func(context.Context, string, []string, time.Duration) (string, error) {
				return "", context.DeadlineExceeded
			}
			t.Cleanup(func() { worktreeGit = saved })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git, repo := wtRepo(t)
			stubWorktreeUse(t, nil, nil)
			wt := addWorktree(t, git, repo, "agent-1", "agent-1")
			tc.stage(t)
			if w := wtFound(t, "claude", wt); w.State != wtNoGit {
				t.Fatalf("state = %s", w.State)
			}
		})
	}
}

// TestWorktreeStatusIgnoresUserConfig: a repository set to hide untracked
// files from git status does not hide them from the worktree rules.
func TestWorktreeStatusIgnoresUserConfig(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	gitIn(t, git, repo, "config", "status.showUntrackedFiles", "no")
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	writeText(t, filepath.Join(wt, "notes.txt"), "draft\n")
	if w := wtFound(t, "claude", wt); w.State != wtDirty || w.Changes != 1 {
		t.Fatalf("state = %s with %d changes", w.State, w.Changes)
	}
}

// TestWorktreeGitAnswersOnStandardOutput: a warning or a hint git prints on
// standard error is not part of its answer.
func TestWorktreeGitAnswersOnStandardOutput(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	tools := t.TempDir()
	fakeTool(t, tools, "git", `for arg in "$@"; do
	if [ "$arg" = status ]; then
		echo 'warning: made up' >&2
		exit 0
	fi
done
GIT_CONFIG_NOSYSTEM=1 exec `+shellPath(git)+` "$@"
`)
	toolDirs = func() []string { return []string{tools} }
	if w := wtFound(t, "claude", wt); w.State != wtClean || w.Changes != 0 {
		t.Fatalf("state = %s with %d changes", w.State, w.Changes)
	}
}

// TestWorktreeGitEndsItsChildren: git runs in a process group of its own,
// killed whole when its time limit runs out and once git has ended, so a
// child it starts that keeps standard output open does not outlive the call.
func TestWorktreeGitEndsItsChildren(t *testing.T) {
	const limit = 2 * time.Second
	for _, tc := range []struct {
		name, then string
		want       error
		inTime     func(took time.Duration) bool
	}{
		{"git hangs", "wait", context.DeadlineExceeded, func(took time.Duration) bool { return took >= limit && took < limit+2*time.Second }},
		{"git ends at once", "exit 0", exec.ErrWaitDelay, func(took time.Duration) bool { return took < limit }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := t.TempDir()
			child := filepath.Join(tools, "child.pid")
			fakeTool(t, tools, "git", "/bin/sleep 6 &\necho $! > "+shellPath(child)+"\n"+tc.then+"\n")
			start := time.Now()
			out, err := worktreeGit(context.Background(), filepath.Join(tools, "git"), []string{"status"}, limit)
			if took := time.Since(start); !errors.Is(err, tc.want) || !tc.inTime(took) {
				t.Fatalf("answer %q, %v after %s", out, err, took)
			}
			data, _ := os.ReadFile(child)
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || pid <= 1 {
				t.Fatalf("child pid %q: %v", data, err)
			}
			// A killed child is gone once launchd has reaped it.
			for until := time.Now().Add(2 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
				if time.Now().After(until) {
					t.Fatalf("git's child %d outlived the call", pid)
				}
			}
		})
	}
}

// TestWorktreeGitDropsStandardError: git's answer is what it prints on
// standard output, without what it prints on standard error in between.
func TestWorktreeGitDropsStandardError(t *testing.T) {
	tools := t.TempDir()
	fakeTool(t, tools, "git", `echo 'on standard output'
echo 'warning: on standard error' >&2
echo 'on standard output, again'
`)
	out, err := worktreeGit(context.Background(), filepath.Join(tools, "git"), []string{"status"}, worktreeStatusLimit)
	if err != nil || out != "on standard output\non standard output, again\n" {
		t.Fatalf("answer %q, %v", out, err)
	}
}

// TestWorktreeGitHardening: every git command of a scan and a removal runs
// without the caller's GIT_ variables, optional locks, prompts or a file
// system monitor, and git run in a worktree never looks above it. Only
// status, for-each-ref and worktree unlock and remove run, never forced,
// and nothing prunes.
func TestWorktreeGitHardening(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	stubPidStart(t, map[int32]time.Time{})
	locked := addWorktree(t, git, repo, "agent-1", "agent-1")
	wtLock(t, git, repo, locked, "claude agent (pid 4242)")
	addWorktree(t, git, repo, "agent-2", "")
	tools := t.TempDir()
	logged := filepath.Join(tools, "git.log")
	fakeTool(t, tools, "git", `printf '%s\n' "git_dir=${GIT_DIR-unset} optional_locks=${GIT_OPTIONAL_LOCKS-unset} terminal_prompt=${GIT_TERMINAL_PROMPT-unset} ceiling=${GIT_CEILING_DIRECTORIES-unset} args=$*" >> `+shellPath(logged)+`
GIT_CONFIG_NOSYSTEM=1 exec `+shellPath(git)+` "$@"
`)
	toolDirs = func() []string { return []string{tools} }
	other := filepath.Join(t.TempDir(), "other")
	wtCommitted(t, git, other)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_OPTIONAL_LOCKS", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	if _, code := removeWorktree(context.Background(), wtFound(t, "claude", locked), wtClean, wtNothingOpen); code != "" {
		t.Fatalf("code %q", code)
	}
	log, err := os.ReadFile(logged)
	if err != nil {
		t.Fatal(err)
	}
	ran := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		env, args, _ := strings.Cut(line, " args=")
		fields := strings.Fields(args)
		ceiling := "unset"
		if len(fields) > 1 && fields[0] == "-C" {
			ceiling = filepath.Dir(fields[1])
		}
		command := ""
		for i := 0; i < len(fields) && command == ""; i++ {
			switch {
			case fields[i] == "-C" || fields[i] == "-c":
				i++
			case strings.HasPrefix(fields[i], "--git-dir="):
			case fields[i] == "worktree" && i+1 < len(fields):
				command = "worktree " + fields[i+1]
			default:
				command = fields[i]
			}
		}
		ran[command] = true
		forced := slices.ContainsFunc(fields, func(f string) bool { return f == "--force" || f == "-f" || f == "prune" })
		if env != "git_dir=unset optional_locks=0 terminal_prompt=0 ceiling="+ceiling || !strings.Contains(args, "-c core.fsmonitor=false") || forced {
			t.Errorf("git ran as: %s", line)
		}
	}
	if want := []string{"for-each-ref", "status", "worktree remove", "worktree unlock"}; !slices.Equal(slices.Sorted(maps.Keys(ran)), want) {
		t.Fatalf("git ran %v, want %v", slices.Sorted(maps.Keys(ran)), want)
	}
}

// wtTranscript writes a Claude Code transcript of project that works in cwd.
func wtTranscript(t *testing.T, project, cwd string) {
	t.Helper()
	line, err := json.Marshal(map[string]string{"type": "user", "cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(homeDir(), ".claude", "projects", project, "a1b2.jsonl"), string(line)+"\n")
}

// TestWorktreeDiscoveryClaude: Claude Code's worktrees are found in the
// repositories its transcripts and open sessions work in, and wherever the
// home walk passes a .claude/worktrees folder. Each is listed once, and the
// walk finds the same build folders as before.
func TestWorktreeDiscoveryClaude(t *testing.T) {
	git, near := wtRepo(t)
	home := homeDir()
	// Eight levels down: the walk does not get there.
	deep := filepath.Join(home, "l1", "l2", "l3", "l4", "l5", "l6", "l7", "transcribed")
	wtCommitted(t, git, deep)
	open := filepath.Join(home, "m1", "m2", "m3", "m4", "m5", "m6", "m7", "opened")
	wtCommitted(t, git, open)
	walked := addWorktree(t, git, near, "walked", "walked")
	transcribed := addWorktree(t, git, deep, "transcribed", "transcribed")
	opened := addWorktree(t, git, open, "opened", "opened")
	// One transcript works inside the repository, another in the worktree
	// itself, spelled as resolved: both lead to the same folder.
	wtTranscript(t, "-l1-transcribed", filepath.Join(deep, "src"))
	wtTranscript(t, "-l1-transcribed-worktree", resolvePath(transcribed))
	stubWorktreeUse(t, nil, []string{open})
	for _, dir := range []string{filepath.Join(near, "web"), walked} {
		writeText(t, filepath.Join(dir, "package.json"), "{}")
		writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), 100)
	}
	var paths []string
	for _, w := range findWorktrees(context.Background(), "claude") {
		paths = append(paths, w.Path)
	}
	if want := []string{walked, transcribed, opened}; !slices.Equal(paths, slices.Sorted(slices.Values(want))) {
		t.Fatalf("found %v, want %v", paths, want)
	}
	builds := pathJunkEnv(home).findBuildFolders(claimSet{})
	if want := []string{filepath.Join(walked, "node_modules"), filepath.Join(near, "web", "node_modules")}; !slices.Equal(slices.Sorted(slices.Values(builds)), slices.Sorted(slices.Values(want))) {
		t.Fatalf("build folders %v, want %v", builds, want)
	}
}

// TestWorktreeDiscoveryCodex: Codex keeps its worktrees in
// ~/.codex/worktrees/<id>/<name>, and only Codex lists them.
func TestWorktreeDiscoveryCodex(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := filepath.Join(homeDir(), ".codex", "worktrees", "ab12", "app")
	gitIn(t, git, repo, "worktree", "add", "-q", "-b", "codex-1", wt, "main")
	if w := wtFound(t, "codex", wt); w.State != wtClean || w.Repo != repo || w.Branch != "codex-1" {
		t.Fatalf("worktree = %+v", w)
	}
	if found := findWorktrees(context.Background(), "claude"); len(found) != 0 {
		t.Fatalf("Claude Code lists %+v", found)
	}
}

// TestWorktreeOutsideHomeIsNotListed: a repository outside the home folder
// is neither listed nor touched, even with a transcript and a session
// working in it.
func TestWorktreeOutsideHomeIsNotListed(t *testing.T) {
	git, _ := wtRepo(t)
	outside := filepath.Join(t.TempDir(), "app")
	wtCommitted(t, git, outside)
	wt := addWorktree(t, git, outside, "agent-1", "agent-1")
	waiting := addWorktree(t, git, outside, "agent-2", "agent-2")
	if err := os.Rename(waiting, waiting+worktreeTemp); err != nil {
		t.Fatal(err)
	}
	wtTranscript(t, "-outside-app", outside)
	stubWorktreeUse(t, nil, []string{wt})
	if found := findWorktrees(context.Background(), "claude"); len(found) != 0 {
		t.Fatalf("found %+v", found)
	}
	if !exists(wt) || !exists(waiting+worktreeTemp) || exists(waiting) {
		t.Fatal("a folder outside the home folder was touched")
	}
}

// TestWorktreeFolderThatIsNotOne: a folder among the worktrees that is not
// one is listed, as unrecognized.
func TestWorktreeFolderThatIsNotOne(t *testing.T) {
	_, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	plain := filepath.Join(repo, ".claude", "worktrees", "plain")
	writeText(t, filepath.Join(plain, "notes.txt"), "draft\n")
	if w := wtFound(t, "claude", plain); w.State != wtUnknown || w.Size == 0 {
		t.Fatalf("worktree = %+v", w)
	}
}

// TestWorktreePathsWithSpacesAndUnicode: spaces, Chinese and # in the names
// of the repository and the worktree change nothing, though git names its
// record differently from the folder.
func TestWorktreePathsWithSpacesAndUnicode(t *testing.T) {
	git, _ := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	repo := filepath.Join(homeDir(), "code", "my repo 仓库")
	wtCommitted(t, git, repo)
	wt := addWorktree(t, git, repo, "修复 bug #1", "fix/修复-bug-1")
	admin := wtAdmin(t, wt)
	w := wtFound(t, "claude", wt)
	if w.State != wtClean || w.Branch != "fix/修复-bug-1" || w.Repo != repo || filepath.Base(admin) == w.Name {
		t.Fatalf("worktree = %+v, record %s", w, admin)
	}
	stubWorktreeUse(t, []string{strings.ToUpper(resolvePath(wt))}, nil)
	if w := wtFound(t, "claude", wt); w.State != wtInUse {
		t.Fatalf("state with a process working in it = %s", w.State)
	}
	stubWorktreeUse(t, nil, nil)
	if _, code := removeWorktree(context.Background(), w, wtClean, wtNothingOpen); code != "" {
		t.Fatalf("code %q", code)
	}
	if !wtTrashed("修复 bug #1", "README.md") || exists(wt) || exists(admin) {
		t.Fatalf("trashed %v, folder there %v, record there %v", wtTrashed("修复 bug #1", "README.md"), exists(wt), exists(admin))
	}
	gitIn(t, git, repo, "switch", "-q", "fix/修复-bug-1")
}

// TestWorktreeInterruptedRemovalIsRestored: a worktree an interrupted
// removal left under its temporary name, git's record untouched, is put
// back before it is judged. A folder that only shares the suffix stays.
func TestWorktreeInterruptedRemovalIsRestored(t *testing.T) {
	for _, kind := range []string{"claude", "codex"} {
		t.Run(kind, func(t *testing.T) {
			git, repo := wtRepo(t)
			stubWorktreeUse(t, nil, nil)
			wt := addWorktree(t, git, repo, "agent-1", "agent-1")
			if kind == "codex" {
				wt = filepath.Join(homeDir(), ".codex", "worktrees", "ab12", "app")
				gitIn(t, git, repo, "worktree", "add", "-q", "-b", "codex-1", wt, "main")
			}
			other := filepath.Join(filepath.Dir(wt), "notes"+worktreeTemp)
			writeText(t, filepath.Join(other, "draft.txt"), "draft\n")
			if err := os.Rename(wt, wt+worktreeTemp); err != nil {
				t.Fatal(err)
			}
			if w := wtFound(t, kind, wt); w.State != wtClean {
				t.Fatalf("state = %s", w.State)
			}
			if !exists(filepath.Join(wt, "README.md")) || exists(wt+worktreeTemp) || !exists(filepath.Join(other, "draft.txt")) {
				t.Fatalf("back in place %v, the other folder in place %v", exists(wt), exists(other))
			}
		})
	}
}

// TestWorktreeForgottenWhileAwayIsRestored: a removal interrupted after git
// forgot the worktree leaves its folder under the temporary name, with no
// record. The folder is put back under its name and listed as unrecognized.
// A folder whose .git names no record in a worktrees folder stays.
func TestWorktreeForgottenWhileAwayIsRestored(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	admin := wtAdmin(t, wt)
	if err := os.Rename(wt, wt+worktreeTemp); err != nil {
		t.Fatal(err)
	}
	// The folder is away from its path, so git only clears its record.
	gitIn(t, git, repo, "worktree", "remove", wt)
	if exists(admin) || !exists(filepath.Join(wt+worktreeTemp, "README.md")) {
		t.Fatalf("record there %v, folder away %v", exists(admin), exists(wt+worktreeTemp))
	}
	other := filepath.Join(filepath.Dir(wt), "notes"+worktreeTemp)
	writeText(t, filepath.Join(other, ".git"), "gitdir: "+filepath.Join(homeDir(), "elsewhere", "notes")+"\n")
	if w := wtFound(t, "claude", wt); w.State != wtUnknown {
		t.Fatalf("state = %s", w.State)
	}
	if !exists(filepath.Join(wt, "README.md")) || exists(wt+worktreeTemp) || !exists(filepath.Join(other, ".git")) {
		t.Fatalf("back in place %v, the other folder in place %v", exists(wt), exists(other))
	}
}

// TestWorktreeInterruptedRemovalWithItsNameTaken: a worktree an interrupted
// removal left under its temporary name stays there while its own name is
// taken, even by an empty folder. Both folders stay as they are.
func TestWorktreeInterruptedRemovalWithItsNameTaken(t *testing.T) {
	git, repo := wtRepo(t)
	stubWorktreeUse(t, nil, nil)
	wt := addWorktree(t, git, repo, "agent-1", "agent-1")
	if err := os.Rename(wt, wt+worktreeTemp); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, wt)
	if w := wtFound(t, "claude", wt+worktreeTemp); w.State != wtUnknown {
		t.Fatalf("under the temporary name: state = %s", w.State)
	}
	entries, err := os.ReadDir(wt)
	if !exists(filepath.Join(wt+worktreeTemp, "README.md")) || err != nil || len(entries) != 0 {
		t.Fatalf("folder away %v, the folder with its name holds %v (%v)", exists(wt+worktreeTemp), entries, err)
	}
}
