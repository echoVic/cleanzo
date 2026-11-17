package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// projectClock moves the clock past the idle threshold, so that what a test
// writes now is fifteen days old, and returns the moved time.
func projectClock(t *testing.T) time.Time {
	t.Helper()
	saved := now
	future := time.Now().Add(15*24*time.Hour + time.Hour)
	now = func() time.Time { return future }
	t.Cleanup(func() { now = saved })
	return future
}

// projectEnv is testEnv with no catalog rules, so a scan returns project rows only.
func projectEnv(t *testing.T) junkEnv {
	env := testEnv(t)
	env.rules = []Rule{}
	return env
}

// installedGit finds git for staging repositories and skips the test without
// one. The developer's global git configuration and ignores under
// XDG_CONFIG_HOME stay out of the test, as those under HOME do.
func installedGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return git
}

// useGit lets the scan run the installed git, as if developer tools were
// installed, through a wrapper that keeps the system git configuration out.
func useGit(t *testing.T, env *junkEnv) string {
	t.Helper()
	git := installedGit(t)
	saved := developerToolsInstalled
	developerToolsInstalled = func(context.Context) bool { return true }
	t.Cleanup(func() { developerToolsInstalled = saved })
	bin := t.TempDir()
	fakeTool(t, bin, "git", "GIT_CONFIG_NOSYSTEM=1 exec '"+git+"' \"$@\"\n")
	env.findTool = func(name string) string {
		if name == "git" {
			return filepath.Join(bin, "git")
		}
		return ""
	}
	return git
}

// gitIn runs git in dir with none of the caller's GIT_ variables and without
// the system git configuration.
func gitIn(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "GIT_") }), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitCommit records what is staged in dir as a commit.
func gitCommit(t *testing.T, git, dir string) {
	t.Helper()
	gitIn(t, git, dir, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "staged")
}

// gitRepo makes dir a repository with the given ignore file, tracking paths.
func gitRepo(t *testing.T, git, dir, ignore string, track ...string) {
	t.Helper()
	mustMkdir(t, dir)
	gitIn(t, git, dir, "init", "-q")
	if ignore != "" {
		writeText(t, filepath.Join(dir, ".gitignore"), ignore)
	}
	if len(track) > 0 {
		gitIn(t, git, dir, append([]string{"add", "-f", "--"}, track...)...)
	}
}

// touch marks p as changed at the moved clock's time.
func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.Chtimes(p, now(), now()); err != nil {
		t.Fatal(err)
	}
}

// projectRows scans and returns the measured project rows.
func projectRows(t *testing.T, env junkEnv) []JunkItem {
	t.Helper()
	items, _ := scanJunk(context.Background(), env, nil, nil)
	var rows []JunkItem
	for _, it := range items {
		if it.Group == string(groupProjects) {
			rows = append(rows, it)
		}
	}
	return rows
}

// offered lists the folders the rows offer, as displayed, sorted.
func offered(rows []JunkItem) []string {
	var paths []string
	for _, it := range rows {
		paths = append(paths, it.Paths...)
	}
	slices.Sort(paths)
	return paths
}

// rowIDs lists the rows' IDs in order.
func rowIDs(rows []JunkItem) []string {
	var ids []string
	for _, it := range rows {
		ids = append(ids, it.ID)
	}
	return ids
}

// countGitCalls wraps the check-ignore runner and records each call's paths.
func countGitCalls(t *testing.T) *[][]string {
	t.Helper()
	var mu sync.Mutex
	calls := &[][]string{}
	saved := gitCheckIgnore
	gitCheckIgnore = func(ctx context.Context, git, repo string, paths []string) (string, error) {
		mu.Lock()
		*calls = append(*calls, slices.Clone(paths))
		mu.Unlock()
		return saved(ctx, git, repo, paths)
	}
	t.Cleanup(func() { gitCheckIgnore = saved })
	return calls
}

// TestProjectTableRows recognises each build folder by its marker and
// rejects it without one. build, dist and out also need git's word, so
// they are staged in repositories that ignore them.
func TestProjectTableRows(t *testing.T) {
	env := projectEnv(t)
	git := useGit(t, &env)
	projectClock(t)
	code := filepath.Join(env.home, "code")
	stage := func(project, folder string, files ...string) {
		writeFile(t, filepath.Join(code, project, folder, "payload"), 100)
		for _, f := range files {
			writeText(t, filepath.Join(code, project, f), "x")
		}
	}
	var want []string
	for _, name := range []string{"node_modules", ".next", ".nuxt", ".svelte-kit", ".turbo"} {
		stage("with"+name, name, "package.json")
		stage("without"+name, name, "index.js")
		want = append(want, "~/code/with"+name+"/"+name)
	}
	for _, name := range []string{".venv", "venv"} {
		stage("with"+name, name, filepath.Join(name, "pyvenv.cfg"))
		stage("without"+name, name, "main.py")
		want = append(want, "~/code/with"+name+"/"+name)
	}
	stage("with-target", "target", "Cargo.toml", filepath.Join("target", "CACHEDIR.TAG"))
	stage("target-without-cachedir", "target", "Cargo.toml")
	stage("target-without-cargo", "target", filepath.Join("target", "CACHEDIR.TAG"))
	want = append(want, "~/code/with-target/target")
	stage("with-pods", "Pods", "Podfile")
	stage("without-pods", "Pods", "Package.swift")
	want = append(want, "~/code/with-pods/Pods")
	for _, name := range []string{"build", "dist", "out"} {
		for _, marker := range projectFiles {
			project := name + "-" + strings.ReplaceAll(marker, ".", "-")
			gitRepo(t, git, filepath.Join(code, project), name+"/\n")
			stage(project, name, marker)
			want = append(want, "~/code/"+project+"/"+name)
		}
		project := name + "-without-marker"
		gitRepo(t, git, filepath.Join(code, project), name+"/\n")
		stage(project, name, "README.md")
	}
	slices.Sort(want)
	rows := projectRows(t, env)
	if got := offered(rows); !slices.Equal(got, want) {
		t.Fatalf("offered:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, it := range rows {
		if it.Default || !it.Trash || it.Name != filepath.Base(strings.TrimPrefix(it.ID, "project:")) || it.Size < 100 || it.IdleDays != 15 {
			t.Fatalf("row = %+v", it)
		}
	}
}

// TestProjectRepositoryNeedsGitsWord: inside a repository a build folder
// is offered only when git ignores it and tracks nothing in it.
func TestProjectRepositoryNeedsGitsWord(t *testing.T) {
	env := projectEnv(t)
	git := useGit(t, &env)
	projectClock(t)
	repo := filepath.Join(env.home, "code", "repo")
	writeText(t, filepath.Join(repo, "package.json"), "{}")
	writeText(t, filepath.Join(repo, "build", "keep.txt"), "tracked")
	writeText(t, filepath.Join(repo, "web", "package.json"), "{}")
	writeFile(t, filepath.Join(repo, "web", "build", "app.js"), 100)
	writeFile(t, filepath.Join(repo, "out", "big.bin"), 100)
	writeText(t, filepath.Join(repo, "api", "package.json"), "{}")
	writeText(t, filepath.Join(repo, "api", "dist", "forced.js"), "tracked although ignored")
	writeFile(t, filepath.Join(repo, "node_modules", "dep", "index.js"), 100)
	gitRepo(t, git, repo, "web/build/\napi/dist/\n", "build/keep.txt", "api/dist/forced.js")
	rows := projectRows(t, env)
	if got := offered(rows); !slices.Equal(got, []string{"~/code/repo/web/build"}) || len(rows) != 1 || rows[0].ID != "project:"+repo {
		t.Fatalf("rows = %v, offered %v", rowIDs(rows), got)
	}
}

// TestProjectOutsideRepository: without git only the marker rows apply.
func TestProjectOutsideRepository(t *testing.T) {
	env := projectEnv(t)
	projectClock(t)
	app := filepath.Join(env.home, "code", "app")
	writeText(t, filepath.Join(app, "package.json"), "{}")
	writeFile(t, filepath.Join(app, "node_modules", "dep", "index.js"), 100)
	writeFile(t, filepath.Join(app, "dist", "app.js"), 100)
	rows := projectRows(t, env)
	if got := offered(rows); !slices.Equal(got, []string{"~/code/app/node_modules"}) || rowIDs(rows)[0] != "project:"+app {
		t.Fatalf("rows = %v, offered %v", rowIDs(rows), got)
	}
}

// TestProjectIdleness: an idle project has a row saying how long it has been
// idle; a change to a source file or to git's index takes the row away,
// while a change inside a build folder or a hidden folder does not count.
func TestProjectIdleness(t *testing.T) {
	stageRepo := func(t *testing.T) (junkEnv, string) {
		env := projectEnv(t)
		git := useGit(t, &env)
		projectClock(t)
		repo := filepath.Join(env.home, "code", "repo")
		writeText(t, filepath.Join(repo, "package.json"), "{}")
		writeText(t, filepath.Join(repo, "src", "lib", "index.js"), "x")
		writeFile(t, filepath.Join(repo, "node_modules", "dep", "index.js"), 100)
		writeText(t, filepath.Join(repo, ".idea", "workspace.xml"), "x")
		gitRepo(t, git, repo, "node_modules/\n", "package.json")
		return env, repo
	}
	t.Run("idle", func(t *testing.T) {
		env, repo := stageRepo(t)
		touch(t, filepath.Join(repo, "node_modules", "dep", "index.js"))
		touch(t, filepath.Join(repo, ".idea", "workspace.xml"))
		rows := projectRows(t, env)
		if len(rows) != 1 || rows[0].IdleDays != 15 {
			t.Fatalf("rows = %+v", rows)
		}
	})
	for _, changed := range []string{filepath.Join("src", "lib", "index.js"), filepath.Join(".git", "index")} {
		t.Run(changed, func(t *testing.T) {
			env, repo := stageRepo(t)
			touch(t, filepath.Join(repo, changed))
			if rows := projectRows(t, env); len(rows) != 0 {
				t.Fatalf("a project changed now has rows %+v", rows)
			}
		})
	}
	t.Run("almost idle", func(t *testing.T) {
		env, repo := stageRepo(t)
		recent := now().Add(-13 * 24 * time.Hour)
		if err := os.Chtimes(filepath.Join(repo, "package.json"), recent, recent); err != nil {
			t.Fatal(err)
		}
		if rows := projectRows(t, env); len(rows) != 0 {
			t.Fatalf("a project changed 13 days ago has rows %+v", rows)
		}
	})
}

// TestProjectNestedPackage: a package inside a repository belongs to the
// repository's row.
func TestProjectNestedPackage(t *testing.T) {
	env := projectEnv(t)
	git := useGit(t, &env)
	projectClock(t)
	repo := filepath.Join(env.home, "code", "repo")
	writeText(t, filepath.Join(repo, "web", "package.json"), "{}")
	writeFile(t, filepath.Join(repo, "web", "node_modules", "dep", "index.js"), 100)
	writeText(t, filepath.Join(repo, "py", "tool", ".venv", "pyvenv.cfg"), "home = /usr")
	gitRepo(t, git, repo, "node_modules/\n.venv/\n")
	rows := projectRows(t, env)
	if len(rows) != 1 || rows[0].ID != "project:"+repo || rows[0].Name != "repo" || !slices.Equal(offered(rows), []string{"~/code/repo/py/tool/.venv", "~/code/repo/web/node_modules"}) {
		t.Fatalf("rows = %v, offered %v", rowIDs(rows), offered(rows))
	}
}

// TestProjectWorktrees: agent worktrees are entered on purpose and each is a
// project of its own; nothing else hidden is entered.
func TestProjectWorktrees(t *testing.T) {
	env := projectEnv(t)
	git := useGit(t, &env)
	projectClock(t)
	stage := func(dir string) {
		writeText(t, filepath.Join(dir, "package.json"), "{}")
		writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), 100)
		gitRepo(t, git, dir, "node_modules/\n")
	}
	codex := filepath.Join(env.home, ".codex", "worktrees", "a1b2", "site")
	stage(codex)
	repo := filepath.Join(env.home, "Documents", "GitHub", "repo")
	writeText(t, filepath.Join(repo, "README.md"), "x")
	gitRepo(t, git, repo, ".claude/\n")
	claude := filepath.Join(repo, ".claude", "worktrees", "agent-1")
	stage(claude)
	writeText(t, filepath.Join(env.home, ".cache", "tool", "package.json"), "{}")
	writeFile(t, filepath.Join(env.home, ".cache", "tool", "node_modules", "x"), 100)
	writeText(t, filepath.Join(repo, ".hidden", "pkg", "package.json"), "{}")
	writeFile(t, filepath.Join(repo, ".hidden", "pkg", "node_modules", "x"), 100)
	rows := projectRows(t, env)
	want := []string{"project:" + codex, "project:" + claude}
	slices.Sort(want)
	if got := rowIDs(rows); !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

// TestProjectDiscoveryBounds: the walk goes six levels below the home
// folder and stays out of the folders the home folder keeps for other
// things, packages and links.
func TestProjectDiscoveryBounds(t *testing.T) {
	env := projectEnv(t)
	projectClock(t)
	stage := func(dir string) {
		writeText(t, filepath.Join(dir, "package.json"), "{}")
		writeFile(t, filepath.Join(dir, "node_modules", "x"), 100)
	}
	deep := filepath.Join(env.home, "a", "b", "c", "d", "e")
	stage(deep)
	stage(filepath.Join(deep, "f"))
	for _, skipped := range []string{"Library", "Applications", "Music", "Pictures", "Movies", "Public"} {
		stage(filepath.Join(env.home, skipped, "app"))
	}
	stage(filepath.Join(env.home, "code", "Tool.app", "Contents"))
	outside := t.TempDir()
	stage(filepath.Join(outside, "linked"))
	if err := os.Symlink(outside, filepath.Join(env.home, "link")); err != nil {
		t.Fatal(err)
	}
	rows := projectRows(t, env)
	if got := rowIDs(rows); !slices.Equal(got, []string{"project:" + deep}) {
		t.Fatalf("rows = %v", got)
	}
}

// TestProjectRowsInPathOrder emits one row per project, sorted by path.
func TestProjectRowsInPathOrder(t *testing.T) {
	env := projectEnv(t)
	projectClock(t)
	for _, name := range []string{"zeta", "alpha", "mid"} {
		writeText(t, filepath.Join(env.home, "code", name, "package.json"), "{}")
		writeFile(t, filepath.Join(env.home, "code", name, "node_modules", "x"), 100)
	}
	specs, _ := expandItems(&env)
	var ids []string
	for _, s := range specs {
		ids = append(ids, s.item.ID)
	}
	code := filepath.Join(env.home, "code")
	want := []string{"project:" + filepath.Join(code, "alpha"), "project:" + filepath.Join(code, "mid"), "project:" + filepath.Join(code, "zeta")}
	if !slices.Equal(ids, want) {
		t.Fatalf("ids = %v", ids)
	}
}

// TestProjectGitUnavailable: without a git that can answer, repositories are
// skipped whole; projects outside a repository are still offered.
func TestProjectGitUnavailable(t *testing.T) {
	stage := func(t *testing.T) (junkEnv, string) {
		env := projectEnv(t)
		git := installedGit(t)
		projectClock(t)
		repo := filepath.Join(env.home, "code", "repo")
		writeText(t, filepath.Join(repo, "package.json"), "{}")
		writeFile(t, filepath.Join(repo, "node_modules", "x"), 100)
		gitRepo(t, git, repo, "node_modules/\n")
		plain := filepath.Join(env.home, "code", "plain")
		writeText(t, filepath.Join(plain, "package.json"), "{}")
		writeFile(t, filepath.Join(plain, "node_modules", "x"), 100)
		return env, plain
	}
	only := func(t *testing.T, env junkEnv, plain string) {
		t.Helper()
		if got := rowIDs(projectRows(t, env)); !slices.Equal(got, []string{"project:" + plain}) {
			t.Fatalf("rows = %v", got)
		}
	}
	t.Run("missing", func(t *testing.T) {
		env, plain := stage(t)
		env.findTool = func(string) string { return "" }
		only(t, env, plain)
	})
	t.Run("failing", func(t *testing.T) {
		env, plain := stage(t)
		bin := t.TempDir()
		fakeTool(t, bin, "git", "echo 'fatal: bad config' >&2\nexit 128\n")
		env.findTool = func(name string) string { return filepath.Join(bin, name) }
		only(t, env, plain)
	})
	t.Run("stub without developer tools", func(t *testing.T) {
		env, plain := stage(t)
		env.findTool = func(name string) string { return "/usr/bin/" + name }
		saved, savedTools := gitCheckIgnore, developerToolsInstalled
		var used []string
		gitCheckIgnore = func(_ context.Context, git, _ string, paths []string) (string, error) {
			used = append(used, git)
			return strings.Join(paths, "\x00") + "\x00", nil
		}
		t.Cleanup(func() { gitCheckIgnore, developerToolsInstalled = saved, savedTools })
		developerToolsInstalled = func(context.Context) bool { return false }
		only(t, env, plain)
		if len(used) != 0 {
			t.Fatalf("ran %v without developer tools", used)
		}
		developerToolsInstalled = func(context.Context) bool { return true }
		if got := projectRows(t, env); len(got) != 2 || !slices.Equal(used, []string{"/usr/bin/git"}) {
			t.Fatalf("with developer tools: rows %v, ran %v", rowIDs(got), used)
		}
	})
}

// TestProjectGitAskedOncePerRepository sends all of a repository's
// candidates in one call, and never a folder inside a recognised one.
func TestProjectGitAskedOncePerRepository(t *testing.T) {
	env := projectEnv(t)
	git := useGit(t, &env)
	projectClock(t)
	calls := countGitCalls(t)
	repo := filepath.Join(env.home, "code", "repo")
	writeText(t, filepath.Join(repo, "package.json"), "{}")
	writeText(t, filepath.Join(repo, "node_modules", "pkg", "package.json"), "{}")
	writeFile(t, filepath.Join(repo, "node_modules", "pkg", "dist", "index.js"), 100)
	writeText(t, filepath.Join(repo, "node_modules", "pkg", "node_modules", "inner", "package.json"), "{}")
	writeText(t, filepath.Join(repo, "web", "package.json"), "{}")
	writeFile(t, filepath.Join(repo, "web", ".next", "cache"), 100)
	gitRepo(t, git, repo, "node_modules/\n.next/\n")
	rows := projectRows(t, env)
	if len(*calls) != 1 || !slices.Equal((*calls)[0], []string{"node_modules", filepath.Join("web", ".next")}) {
		t.Fatalf("git calls = %v", *calls)
	}
	if got := offered(rows); !slices.Equal(got, []string{"~/code/repo/node_modules", "~/code/repo/web/.next"}) {
		t.Fatalf("offered %v", got)
	}
}

// TestProjectRowsFollowSettings: ignored rows are counted, a turned-off group
// shows nothing, and a folder another row has taken leaves no project row.
func TestProjectRowsFollowSettings(t *testing.T) {
	stage := func(t *testing.T) (junkEnv, string) {
		env := projectEnv(t)
		projectClock(t)
		app := filepath.Join(env.home, "code", "app")
		writeText(t, filepath.Join(app, "package.json"), "{}")
		writeFile(t, filepath.Join(app, "node_modules", "x"), 100)
		return env, app
	}
	t.Run("ignored", func(t *testing.T) {
		env, app := stage(t)
		env.settings.Ignored = []string{"project:" + app}
		specs, sum := expandItems(&env)
		if len(specs) != 0 || sum.Ignored != 1 {
			t.Fatalf("specs = %v, ignored %d", itemIDs(specs), sum.Ignored)
		}
	})
	t.Run("group off", func(t *testing.T) {
		env, app := stage(t)
		git := useGit(t, &env)
		gitRepo(t, git, app, "node_modules/\n", "package.json")
		calls := countGitCalls(t)
		env.settings.DisabledGroups = []string{string(groupProjects)}
		if specs, _ := expandItems(&env); len(specs) != 0 || len(*calls) != 0 {
			t.Fatalf("specs = %v, git calls %v", itemIDs(specs), *calls)
		}
	})
	t.Run("claimed", func(t *testing.T) {
		env, app := stage(t)
		env.settings.Custom = []CustomFolder{{Name: "deps", Path: filepath.Join(app, "node_modules"), Enabled: true}}
		if specs, _ := expandItems(&env); !slices.Equal(itemIDs(specs), []string{"custom:" + filepath.Join(app, "node_modules")}) {
			t.Fatalf("specs = %v", itemIDs(specs))
		}
	})
}

// TestProjectSizesCountClonesOnce: a file cloned from outside the build
// folder frees nothing when the folder goes.
func TestProjectSizesCountClonesOnce(t *testing.T) {
	env := projectEnv(t)
	projectClock(t)
	app := filepath.Join(env.home, "code", "app")
	writeText(t, filepath.Join(app, "package.json"), "{}")
	store := filepath.Join(env.home, ".pnpm-store", "lib.so")
	writeFile(t, store, 3000)
	cloneFile(t, store, filepath.Join(app, "node_modules", "dep", "lib.so"))
	writeFile(t, filepath.Join(app, "node_modules", "dep", "own.js"), 700)
	rows := projectRows(t, env)
	if len(rows) != 1 || rows[0].Size != 700 {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestProjectClean moves a selected project's build folders to the Trash and
// leaves its sources; a project changed since the scan is left alone.
func TestProjectClean(t *testing.T) {
	stage := func(t *testing.T) (junkEnv, string) {
		env := testEnv(t)
		git := useGit(t, &env)
		projectClock(t)
		repo := filepath.Join(env.home, "code", "repo")
		writeText(t, filepath.Join(repo, "package.json"), "{}")
		writeText(t, filepath.Join(repo, "src", "index.js"), "x")
		writeFile(t, filepath.Join(repo, "node_modules", "dep", "index.js"), 300)
		writeFile(t, filepath.Join(repo, ".next", "cache", "page"), 200)
		gitRepo(t, git, repo, "node_modules/\n.next/\n", "package.json", "src/index.js")
		return env, repo
	}
	t.Run("idle", func(t *testing.T) {
		env, repo := stage(t)
		if rows := projectRows(t, env); len(rows) != 1 {
			t.Fatalf("rows = %v", rowIDs(rows))
		}
		res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "project:" + repo}}, time.Time{}, cleanHooks{})
		if len(res) != 1 || res[0].Trashed != 500 || res[0].Freed != 0 || len(res[0].Failed) != 0 {
			t.Fatalf("result = %+v", res)
		}
		for _, gone := range []string{"node_modules", ".next"} {
			if exists(filepath.Join(repo, gone)) || !exists(filepath.Join(env.home, ".Trash", gone)) {
				t.Fatalf("%s was not moved to the Trash", gone)
			}
		}
		for _, kept := range []string{"package.json", filepath.Join("src", "index.js"), ".git", ".gitignore"} {
			if !exists(filepath.Join(repo, kept)) {
				t.Fatalf("%s is gone", kept)
			}
		}
	})
	t.Run("changed", func(t *testing.T) {
		env, repo := stage(t)
		if rows := projectRows(t, env); len(rows) != 1 {
			t.Fatalf("rows = %v", rowIDs(rows))
		}
		touch(t, filepath.Join(repo, "src", "index.js"))
		res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "project:" + repo}}, time.Time{}, cleanHooks{})
		if len(res) != 1 || res[0].Trashed != 0 || !slices.Equal(res[0].Failed, []string{"changed"}) || !exists(filepath.Join(repo, "node_modules")) {
			t.Fatalf("result = %+v", res)
		}
	})
	t.Run("no longer recognised", func(t *testing.T) {
		env, repo := stage(t)
		if rows := projectRows(t, env); len(rows) != 1 {
			t.Fatalf("rows = %v", rowIDs(rows))
		}
		// Both happen at the real time, fifteen days before the moved clock:
		// the project is still idle, but git tracks .next now and nothing
		// marks node_modules.
		gitIn(t, installedGit(t), repo, "add", "-f", "--", ".next/cache/page")
		if err := os.Remove(filepath.Join(repo, "package.json")); err != nil {
			t.Fatal(err)
		}
		res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "project:" + repo}}, time.Time{}, cleanHooks{})
		if len(res) != 1 || res[0].Trashed != 0 || !slices.Equal(res[0].Failed, []string{"changed"}) || !exists(filepath.Join(repo, ".next")) || !exists(filepath.Join(repo, "node_modules")) {
			t.Fatalf("result = %+v", res)
		}
		// The project was idle all along: with its marker back, node_modules goes.
		writeText(t, filepath.Join(repo, "package.json"), "{}")
		res = cleanJunk(context.Background(), env, []JunkSelection{{ID: "project:" + repo}}, time.Time{}, cleanHooks{})
		if len(res) != 1 || res[0].Trashed != 300 || exists(filepath.Join(repo, "node_modules")) || !exists(filepath.Join(repo, ".next")) {
			t.Fatalf("result = %+v", res)
		}
	})
}

// TestProjectCleanLooksOnlyWhenSelected: finding projects walks the home
// folder, so a clean does it only for a selected project row.
func TestProjectCleanLooksOnlyWhenSelected(t *testing.T) {
	env := testEnv(t)
	git := useGit(t, &env)
	projectClock(t)
	calls := countGitCalls(t)
	repo := filepath.Join(env.home, "code", "repo")
	writeText(t, filepath.Join(repo, "package.json"), "{}")
	writeFile(t, filepath.Join(repo, "node_modules", "x"), 100)
	gitRepo(t, git, repo, "node_modules/\n")
	writeFile(t, filepath.Join(env.home, ".npm", "_npx", "a", "x"), 100)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.npx"}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Freed != 100 || len(*calls) != 0 {
		t.Fatalf("result = %+v, git calls %v", res, *calls)
	}
	res = cleanJunk(context.Background(), env, []JunkSelection{{ID: "project:" + repo}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Trashed != 100 || len(*calls) != 1 {
		t.Fatalf("result = %+v, git calls %v", res, *calls)
	}
}

// TestProjectGroupCanBeTurnedOff: settings accept the projects group among
// the groups to turn off.
func TestProjectGroupCanBeTurnedOff(t *testing.T) {
	got, err := validateSettings(Settings{DisabledGroups: []string{string(groupProjects)}})
	if err != nil || !slices.Equal(got.DisabledGroups, []string{string(groupProjects)}) {
		t.Fatalf("validateSettings = %+v, %v", got, err)
	}
}

// TestProjectBuildFolderHoldingGitIsKept: a build folder that is a worktree
// or a repository of its own may hold work found nowhere else, so it is
// never offered, though git ignores it.
func TestProjectBuildFolderHoldingGitIsKept(t *testing.T) {
	env := projectEnv(t)
	git := useGit(t, &env)
	projectClock(t)
	site := filepath.Join(env.home, "code", "site")
	writeText(t, filepath.Join(site, "package.json"), "{}")
	writeFile(t, filepath.Join(site, "node_modules", "dep", "index.js"), 100)
	gitRepo(t, git, site, "node_modules/\ndist/\nout/\n", "package.json", ".gitignore")
	gitCommit(t, git, site)
	gitIn(t, git, site, "worktree", "add", "-q", "-b", "gh-pages", "dist")
	writeText(t, filepath.Join(site, "dist", "index.html"), "not committed anywhere")
	gitRepo(t, git, filepath.Join(site, "out"), "")
	writeText(t, filepath.Join(site, "out", "notes.md"), "only here")
	rows := projectRows(t, env)
	if got := offered(rows); !slices.Equal(got, []string{"~/code/site/node_modules"}) || len(rows) != 1 {
		t.Fatalf("rows = %v, offered %v", rowIDs(rows), got)
	}
}

// TestProjectLinkedWorktreeIdleness: a linked worktree's index, HEAD and
// HEAD log live where its .git file points, and a change there means the
// worktree is in use. A .git file that leads nowhere leaves its idleness
// unknown, so it has no row.
func TestProjectLinkedWorktreeIdleness(t *testing.T) {
	stage := func(t *testing.T) (env junkEnv, worktree, gitdir string) {
		env = projectEnv(t)
		git := useGit(t, &env)
		projectClock(t)
		main := filepath.Join(env.home, "code", "main")
		writeText(t, filepath.Join(main, "package.json"), "{}")
		gitRepo(t, git, main, "node_modules/\n", "package.json", ".gitignore")
		gitCommit(t, git, main)
		worktree = filepath.Join(env.home, ".codex", "worktrees", "x", "y")
		gitIn(t, git, main, "worktree", "add", "-q", "-b", "agent", worktree)
		writeFile(t, filepath.Join(worktree, "node_modules", "dep", "index.js"), 100)
		gitdir = filepath.Join(main, ".git", "worktrees", "y")
		for _, name := range []string{"index", "HEAD", filepath.Join("logs", "HEAD")} {
			if _, err := os.Stat(filepath.Join(gitdir, name)); err != nil {
				t.Fatal(err)
			}
		}
		return env, worktree, gitdir
	}
	relative := func(t *testing.T, worktree, gitdir string) {
		rel, err := filepath.Rel(worktree, gitdir)
		if err != nil {
			t.Fatal(err)
		}
		writeText(t, filepath.Join(worktree, ".git"), "gitdir: "+rel+"\n")
	}
	rowsAre := func(t *testing.T, env junkEnv, want ...string) {
		t.Helper()
		if got := rowIDs(projectRows(t, env)); !slices.Equal(got, want) {
			t.Fatalf("rows = %v, want %v", got, want)
		}
	}
	t.Run("idle", func(t *testing.T) {
		env, worktree, _ := stage(t)
		rowsAre(t, env, "project:"+worktree)
	})
	t.Run("idle, relative gitdir", func(t *testing.T) {
		env, worktree, gitdir := stage(t)
		relative(t, worktree, gitdir)
		rowsAre(t, env, "project:"+worktree)
	})
	for _, name := range []string{"index", "HEAD", filepath.Join("logs", "HEAD")} {
		t.Run("changed "+name, func(t *testing.T) {
			env, _, gitdir := stage(t)
			touch(t, filepath.Join(gitdir, name))
			rowsAre(t, env)
		})
	}
	t.Run("changed index, relative gitdir", func(t *testing.T) {
		env, worktree, gitdir := stage(t)
		relative(t, worktree, gitdir)
		touch(t, filepath.Join(gitdir, "index"))
		rowsAre(t, env)
	})
	// git is stubbed to ignore everything here, so only the idle check can
	// keep these rows out.
	for name, dotGit := range map[string]string{"gitdir missing": "gitdir: /nonexistent/worktrees/y\n", "no gitdir line": "nonsense\n"} {
		t.Run(name, func(t *testing.T) {
			env, worktree, _ := stage(t)
			writeText(t, filepath.Join(worktree, ".git"), dotGit)
			saved := gitCheckIgnore
			gitCheckIgnore = func(_ context.Context, _, _ string, paths []string) (string, error) {
				return strings.Join(paths, "\x00") + "\x00", nil
			}
			t.Cleanup(func() { gitCheckIgnore = saved })
			rowsAre(t, env)
		})
	}
}
