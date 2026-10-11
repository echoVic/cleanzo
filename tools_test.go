package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeTool writes a tool script in a temporary executable directory.
func fakeTool(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// stageTools isolates tool lookup and process listings for a test.
func stageTools(t *testing.T, env *junkEnv, bin string) {
	t.Helper()
	saved, savedPs := toolDirs, psCommands
	toolDirs = func() []string { return []string{bin} }
	psCommands = func() ([]byte, error) { return nil, nil }
	t.Cleanup(func() { toolDirs, psCommands = saved, savedPs })
	env.findTool = findTool
}

// shellPath quotes a test path for the fake tools' shell scripts.
func shellPath(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

// TestToolExpansionDefersEstimate keeps slow commands off the initial screen.
func TestToolExpansionDefersEstimate(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	fakeTool(t, bin, "brew", `echo "$@" >> `+shellPath(log)+`
sleep 2
echo "would free approximately 1.5GB"`)
	stageTools(t, &env, bin)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/pkg"), 100)
	start := time.Now()
	specs, _ := expandItems(&env)
	for _, rule := range junkRules {
		if rule.Command != nil {
			if _, ok := env.commandClaims[rule.ID]; !ok {
				t.Errorf("missing claims snapshot for %s", rule.ID)
			}
		}
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Errorf("expansion waited for dry run: %s", elapsed)
	}
	if data, err := os.ReadFile(log); err != nil && !os.IsNotExist(err) || len(data) != 0 {
		t.Errorf("expansion ran brew: %q, %v", data, err)
	}
	var pending bool
	for _, spec := range specs {
		if spec.item.ID == "dev.homebrew-old" {
			pending = spec.item.Size == -1
		}
	}
	if !pending {
		t.Error("brew item was not pending")
	}
	start = time.Now()
	items := measureItems(context.Background(), specs, env, nil, func(item JunkItem) {
		if item.ID == "dev.npx" && time.Since(start) >= time.Second {
			t.Error("ordinary measurement waited for brew")
		}
	})
	var measured bool
	for _, item := range items {
		if item.ID == "dev.homebrew-old" {
			measured = item.Size == 1610612736 && !item.Estimate
		}
	}
	if !measured {
		t.Fatalf("brew estimate missing: %+v", items)
	}
}

// TestCleanOrdinaryItemDoesNotEstimateTools avoids unselected command work.
func TestCleanOrdinaryItemDoesNotEstimateTools(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	fakeTool(t, bin, "brew", `echo "$@" >> `+shellPath(log)+`
echo "would free approximately 1GB"`)
	stageTools(t, &env, bin)
	file := filepath.Join(env.home, ".npm/_npx/pkg")
	writeFile(t, file, 100)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.npx"}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Freed != 100 || exists(file) {
		t.Fatalf("ordinary clean = %+v", res)
	}
	if data, err := os.ReadFile(log); err != nil && !os.IsNotExist(err) || len(data) != 0 {
		t.Fatalf("unselected brew invoked: %q, %v", data, err)
	}
}

// TestToolEstimateSkipsLinksAndSpecialFiles measures only regular store files.
func TestToolEstimateSkipsLinksAndSpecialFiles(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	root := filepath.Join(env.home, "Library/pnpm/store/v10")
	writeFile(t, filepath.Join(root, "regular"), 2000)
	other := filepath.Join(env.home, ".ssh/keep")
	writeFile(t, other, 10000)
	if err := os.Symlink(other, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeTool(t, bin, "pnpm", `case "$*" in "store path") echo `+shellPath(root)+`;; *) exit 1;; esac`)
	stageTools(t, &env, bin)
	specs, _ := expandItems(&env)
	items := measureItems(context.Background(), specs, env, nil, nil)
	if len(items) != 1 || items[0].Size != 2000 || !items[0].Estimate || !slices.Equal(items[0].Paths, []string{"~/Library/pnpm/store/v10"}) {
		t.Fatalf("store measurement = %+v", items)
	}
	if !exists(other) {
		t.Fatal("estimate modified the link target")
	}
}

// TestToolMeasurementHonorsPoolCancellation prevents estimates after cancellation.
func TestToolMeasurementHonorsPoolCancellation(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	fakeTool(t, bin, "brew", `echo "$@" >> `+shellPath(log)+`
echo "would free approximately 1GB"`)
	stageTools(t, &env, bin)
	specs, _ := expandItems(&env)
	if len(specs) != 1 {
		t.Fatalf("pending commands = %v", itemIDs(specs))
	}
	// Fill the shared pool: no estimate may start while waiting for a slot.
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	plan := planItem(ctx, specs[0], env, sem, nil)
	if plan.item.Size != 0 || ctx.Err() == nil {
		t.Errorf("pool wait ignored cancellation: %+v, %v", plan.item, ctx.Err())
	}
	if data, err := os.ReadFile(log); err != nil && !os.IsNotExist(err) || len(data) != 0 {
		t.Fatalf("estimate invoked while pool full: %q, %v", data, err)
	}
}

// TestToolMeasurementCancelsEstimate stops a running dry run with the scan context.
func TestToolMeasurementCancelsEstimate(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	marker := filepath.Join(bin, "started")
	fakeTool(t, bin, "brew", `touch `+shellPath(marker)+`
sleep 5
echo "would free approximately 1GB"`)
	stageTools(t, &env, bin)
	specs, _ := expandItems(&env)
	if len(specs) != 1 {
		t.Fatalf("pending commands = %v", itemIDs(specs))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan itemPlan, 1)
	go func() {
		done <- planItem(ctx, specs[0], env, make(chan struct{}, 1), nil)
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !exists(marker) {
		select {
		case <-tick.C:
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("estimate tool never started")
		}
	}
	start := time.Now()
	cancel()
	select {
	case plan := <-done:
		if plan.item.Size != 0 || time.Since(start) > 2*time.Second {
			t.Fatalf("estimate ignored scan cancellation: %+v after %s", plan.item, time.Since(start))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("estimate did not stop after scan cancellation")
	}
}

// TestFailedToolMeasurementHidesRow keeps estimate failures local to their row.
func TestFailedToolMeasurementHidesRow(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	fakeTool(t, bin, "brew", `echo "would free approximately 1GB"; exit 1`)
	stageTools(t, &env, bin)
	specs, _ := expandItems(&env)
	if len(specs) != 1 {
		t.Fatalf("failed tool must still start pending: %v", itemIDs(specs))
	}
	items := measureItems(context.Background(), specs, env, nil, nil)
	if len(items) != 1 || items[0].Size != 0 {
		t.Fatalf("failed estimate = %+v", items)
	}
}

// TestBrewCleanupEstimateAndRun checks the dry run, arguments and reported release.
func TestBrewCleanupEstimateAndRun(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	fakeTool(t, bin, "brew", `echo "$@" >> `+shellPath(log)+`
case "$*" in *--dry-run*) echo "==> This operation would free approximately 1.5GB of disk space." ;;
*) echo "==> This operation has freed approximately 1.5GB of disk space." ;; esac`)
	stageTools(t, &env, bin)
	specs, _ := expandItems(&env)
	var found bool
	for _, s := range specs {
		if s.item.ID == "dev.homebrew-old" {
			p := planItem(context.Background(), s, env, make(chan struct{}, 1), nil)
			found = p.item.Size == 1610612736 && len(p.entries) == 0
		}
	}
	if !found {
		t.Fatal("brew estimate missing")
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.homebrew-old"}}, time.Time{}, cleanHooks{})
	data, _ := os.ReadFile(log)
	if len(res) != 1 || res[0].Freed != 1610612736 || !strings.Contains(string(data), "cleanup --prune=30\n") {
		t.Fatalf("result %+v, calls %q", res, data)
	}
}

// TestToolCleanupSkipsWhenBusyMissingOrHanging checks isolation from ordinary cleanup.
func TestToolCleanupSkipsWhenBusyMissingOrHanging(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	fakeTool(t, bin, "go", `case "$*" in "env GOMODCACHE") echo `+shellPath(filepath.Join(env.home, "go/pkg/mod"))+`;; *) sleep 5;; esac`)
	writeFile(t, filepath.Join(env.home, "go/pkg/mod/cache/x"), 100)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/x"), 100)
	stageTools(t, &env, bin)
	psCommands = func() ([]byte, error) { return []byte("/usr/libexec/gopls serve\n"), nil }
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.go-mod"}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Skipped != "busy" {
		t.Fatalf("gopls at work: %+v", res)
	}
	psCommands = func() ([]byte, error) { return nil, nil }
	saved := goModCleanup.Timeout
	goModCleanup.Timeout = 200 * time.Millisecond
	t.Cleanup(func() { goModCleanup.Timeout = saved })
	start := time.Now()
	res = cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.go-mod"}, {ID: "dev.npx"}}, time.Time{}, cleanHooks{})
	if len(res) != 2 || res[0].Skipped != "timeout" || res[1].Freed != 100 || time.Since(start) > 2*time.Second {
		t.Fatalf("a hanging tool must time out and allow npx: %+v (%s)", res, time.Since(start))
	}
	toolDirs = func() []string { return nil }
	res = cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.go-mod"}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Skipped != "no-tool" {
		t.Fatalf("no go: %+v", res)
	}
}

// TestParseSimctlUnavailable checks device roots and excludes available devices.
func TestParseSimctlUnavailable(t *testing.T) {
	out := []byte(`{"devices":{"runtime":[{"udid":"A","dataPath":"/Users/x/Library/Developer/CoreSimulator/Devices/A/data","isAvailable":false},{"dataPath":"/Users/x/Library/Developer/CoreSimulator/Devices/B/data","isAvailable":true}]}}`)
	if got := parseSimctlUnavailable(out); !slices.Equal(got, []string{"/Users/x/Library/Developer/CoreSimulator/Devices/A"}) {
		t.Fatalf("got %v", got)
	}
	if got := parseSimctlUnavailable([]byte(`{broken`)); len(got) != 0 {
		t.Fatalf("invalid JSON: %v", got)
	}
}

// TestParseBrewFreed checks binary units and both estimate and cleanup text.
func TestParseBrewFreed(t *testing.T) {
	for _, c := range []struct {
		out  string
		want int64
	}{
		{"would free approximately 1.5GB", 1610612736},
		{"has freed approximately 2 MB of disk space.", 2097152},
		{"would free approximately 1 TB", 1099511627776},
		{"has freed approximately 42B", 42},
		{"unrelated 25 GB", 0},
	} {
		if got := parseBrewFreed(c.out); got != c.want {
			t.Errorf("parseBrewFreed(%q) = %d, want %d", c.out, got, c.want)
		}
	}
}

// TestFindToolUsesFixedDirectories checks executability and avoids inherited PATH.
func TestFindToolUsesFixedDirectories(t *testing.T) {
	env := testEnv(t)
	bin, other := t.TempDir(), t.TempDir()
	fakeTool(t, other, "cleanzo-test-tool", "exit 0")
	t.Setenv("PATH", other)
	stageTools(t, &env, bin)
	if got := findTool("cleanzo-test-tool"); got != "" {
		t.Fatalf("must not use inherited PATH: %s", got)
	}
	fakeTool(t, bin, "cleanzo-test-tool", "exit 0")
	if got := findTool("cleanzo-test-tool"); got != filepath.Join(bin, "cleanzo-test-tool") {
		t.Fatalf("fixed directory tool = %s", got)
	}
	if err := os.Chmod(filepath.Join(bin, "cleanzo-test-tool"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := findTool("cleanzo-test-tool"); got != "" {
		t.Fatalf("not executable: %s", got)
	}
}

// TestToolDirsUsesNewestNode checks numerical version ordering for nvm.
func TestToolDirsUsesNewestNode(t *testing.T) {
	env := testEnv(t)
	for _, v := range []string{"v9.11.0", "v22.9.0", "v22.10.0"} {
		mustMkdir(t, filepath.Join(env.home, ".nvm/versions/node", v, "bin"))
	}
	dirs := toolDirs()
	if !slices.Contains(dirs, filepath.Join(env.home, ".nvm/versions/node/v22.10.0/bin")) || slices.Contains(dirs, filepath.Join(env.home, ".nvm/versions/node/v9.11.0/bin")) {
		t.Fatalf("tool directories = %v", dirs)
	}
}

// TestRunToolControlsEnvironmentAndCancellation checks Finder execution and child termination.
func TestRunToolControlsEnvironmentAndCancellation(t *testing.T) {
	bin := t.TempDir()
	fakeTool(t, bin, "brew", `printf '%s\n' "$PATH" "$HOMEBREW_NO_AUTO_UPDATE" "$HOMEBREW_NO_ANALYTICS" "$HOMEBREW_NO_ENV_HINTS"`)
	out, err := runTool(context.Background(), filepath.Join(bin, "brew"), nil, time.Second)
	if err != nil || out != bin+":/usr/bin:/bin:/usr/sbin:/sbin\n1\n1\n1\n" {
		t.Fatalf("environment = %q, %v", out, err)
	}
	fakeTool(t, bin, "hang", "sleep 5")
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(100*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	start := time.Now()
	_, err = runTool(ctx, filepath.Join(bin, "hang"), nil, time.Minute)
	if !errors.Is(err, context.Canceled) || time.Since(start) > 2*time.Second {
		t.Fatalf("cancellation = %v after %s", err, time.Since(start))
	}
}

// TestToolRootsAreMeasured checks safe official cleanup and actual release.
func TestToolRootsAreMeasured(t *testing.T) {
	for _, c := range []struct {
		id, tool, query, cleanup string
		upper                    bool
	}{
		{"dev.pnpm-store", "pnpm", "store path", "store prune", true},
		{"dev.go-mod", "go", "env GOMODCACHE", "clean -modcache", false},
	} {
		t.Run(c.tool, func(t *testing.T) {
			env := testEnv(t)
			bin := t.TempDir()
			root := filepath.Join(env.home, "tool-store")
			file := filepath.Join(root, "data")
			writeFile(t, file, 2000)
			fakeTool(t, bin, c.tool, `case "$*" in `+shellPath(c.query)+`) echo `+shellPath(root)+`;; `+shellPath(c.cleanup)+`) rm -f `+shellPath(file)+`;; *) exit 1;; esac`)
			stageTools(t, &env, bin)
			items, _ := scanJunk(context.Background(), env, nil, nil)
			if len(items) != 1 || items[0].ID != c.id || items[0].Size != 2000 || items[0].Estimate != c.upper {
				t.Fatalf("items = %+v", items)
			}
			res := cleanJunk(context.Background(), env, []JunkSelection{{ID: c.id}}, time.Time{}, cleanHooks{})
			if len(res) != 1 || res[0].Freed != 2000 || exists(file) {
				t.Fatalf("actual release = %+v", res)
			}
		})
	}
}

// TestToolRootsRetainRootGuards blocks protected flags and special roots.
func TestToolRootsRetainRootGuards(t *testing.T) {
	for _, kind := range []string{"fifo", "locked"} {
		t.Run(kind, func(t *testing.T) {
			env := testEnv(t)
			root := filepath.Join(env.home, "store")
			if kind == "fifo" {
				if err := syscall.Mkfifo(root, 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, filepath.Join(root, "cache"), 100)
				if err := syscall.Chflags(root, 0x2); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { syscall.Chflags(root, 0) })
			}
			if _, err := env.toolRootsSize([]string{root}); !errors.Is(err, errBlocked) {
				t.Fatalf("unguarded %s root: %v", kind, err)
			}
		})
	}
}

// TestToolCleanupRejectsUnsafeRoots checks home, protected and linked stores.
func TestToolCleanupRejectsUnsafeRoots(t *testing.T) {
	for _, kind := range []string{"outside", "home", "protected", "link"} {
		t.Run(kind, func(t *testing.T) {
			env := testEnv(t)
			bin := t.TempDir()
			root := t.TempDir()
			switch kind {
			case "home":
				root = env.home
			case "protected":
				root = filepath.Join(env.home, ".cache/huggingface")
			case "link":
				link := filepath.Join(env.home, "store")
				if err := os.Symlink(root, link); err != nil {
					t.Fatal(err)
				}
				root = link
			}
			writeFile(t, filepath.Join(root, "data"), 100)
			log := filepath.Join(bin, "called")
			fakeTool(t, bin, "pnpm", `case "$*" in "store path") echo `+shellPath(root)+`;; *) touch `+shellPath(log)+`;; esac`)
			stageTools(t, &env, bin)
			res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.pnpm-store"}}, time.Time{}, cleanHooks{})
			if exists(log) || len(res) != 1 || res[0].Skipped != "failed" {
				t.Fatalf("unsafe store %s: %+v", root, res)
			}
		})
	}
}

// TestToolFailureContinuesAndUsageFailsClosed checks failure output and safety probes.
func TestToolFailureContinuesAndUsageFailsClosed(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	fakeTool(t, bin, "brew", `case "$*" in *--dry-run*) echo "would free approximately 1GB";; *) printf 'first\nlast failure\n'; exit 2;; esac`)
	stageTools(t, &env, bin)
	writeFile(t, filepath.Join(env.home, ".npm/_npx/x"), 100)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.homebrew-old"}, {ID: "dev.npx"}}, time.Time{}, cleanHooks{})
	if len(res) != 2 || res[0].Skipped != "failed" || !slices.Equal(res[0].Failed, []string{"last failure"}) || res[1].Freed != 100 {
		t.Fatalf("failure isolation = %+v", res)
	}
	psCommands = func() ([]byte, error) { return nil, errors.New("cannot list processes") }
	res = cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.homebrew-old"}}, time.Time{}, cleanHooks{})
	if res[0].Skipped != "busy" {
		t.Fatalf("failed ps: %+v", res)
	}
	psCommands = func() ([]byte, error) { return nil, nil }
	env.guard.failed = true
	res = cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.homebrew-old"}}, time.Time{}, cleanHooks{})
	if res[0].Skipped != "busy" {
		t.Fatalf("failed usage guard: %+v", res)
	}
}

// TestSimulatorCleanupUsesDeviceRoots checks unavailable devices and running Simulator.
func TestSimulatorCleanupUsesDeviceRoots(t *testing.T) {
	env := testEnv(t)
	env.apps.keys["com.apple.dt.xcode"] = appRef{BundleID: "com.apple.dt.Xcode", Name: "Xcode"}
	bin := t.TempDir()
	root := filepath.Join(env.home, "Library/Developer/CoreSimulator/Devices/A")
	writeFile(t, filepath.Join(root, "data/x"), 500)
	json := `{"devices":{"runtime":[{"dataPath":` + strconv.Quote(filepath.Join(root, "data")) + `,"isAvailable":false}]}}`
	fakeTool(t, bin, "xcrun", `case "$*" in "simctl list devices unavailable -j") echo `+shellPath(json)+`;; "simctl delete unavailable") rm -rf `+shellPath(root)+`;; *) exit 1;; esac`)
	stageTools(t, &env, bin)
	specs, _ := expandItems(&env)
	if !slices.Contains(itemIDs(specs), "review.simulators") {
		t.Fatalf("missing simulators: %v", itemIDs(specs))
	}
	env.guard.running["com.apple.iphonesimulator"] = true
	specs, _ = expandItems(&env)
	for _, spec := range specs {
		if spec.item.ID == "review.simulators" && (spec.item.Size != -1 || !spec.item.Running) {
			t.Errorf("running simulator must start pending: %+v", spec.item)
		}
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.simulators"}}, time.Time{}, cleanHooks{})
	if res[0].Skipped != "running" || !exists(root) {
		t.Fatalf("running simulator: %+v", res)
	}
	delete(env.guard.running, "com.apple.iphonesimulator")
	res = cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.simulators"}}, time.Time{}, cleanHooks{})
	if res[0].Freed != 500 || exists(root) {
		t.Fatalf("simulator release: %+v", res)
	}
}

// TestToolCleanupSkipsLinkedEntries leaves link handling to the official tool.
func TestToolCleanupSkipsLinkedEntries(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	root := filepath.Join(env.home, "store")
	writeFile(t, filepath.Join(root, "cache"), 100)
	other := filepath.Join(env.home, "Documents/keep")
	writeFile(t, other, 100)
	if err := os.Symlink(other, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(bin, "called")
	fakeTool(t, bin, "pnpm", `case "$*" in "store path") echo `+shellPath(root)+`;; *) touch `+shellPath(log)+`;; esac`)
	stageTools(t, &env, bin)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.pnpm-store"}}, time.Time{}, cleanHooks{})
	if !exists(log) || len(res) != 1 || res[0].Skipped != "" || res[0].Freed != 0 || !exists(other) {
		t.Fatalf("tool cleanup with linked entries: %+v", res)
	}
}

// TestToolCleanupRespectsPriorClaims prevents an ignored rule from leaking into a command.
func TestToolCleanupRespectsPriorClaims(t *testing.T) {
	for _, kind := range []string{"same", "ancestor", "descendant"} {
		t.Run(kind, func(t *testing.T) {
			env := testEnv(t)
			bin := t.TempDir()
			root := filepath.Join(env.home, "Library/Caches/pnpm")
			writeFile(t, filepath.Join(root, "data"), 100)
			if kind == "ancestor" {
				root = filepath.Dir(root)
			} else if kind == "descendant" {
				root = filepath.Join(root, "nested")
				writeFile(t, filepath.Join(root, "data"), 100)
			}
			log := filepath.Join(bin, "called")
			fakeTool(t, bin, "pnpm", `case "$*" in "store path") echo `+shellPath(root)+`;; *) touch `+shellPath(log)+`;; esac`)
			stageTools(t, &env, bin)
			env.settings.Ignored = []string{"dev.pnpm"}
			specs, _ := expandItems(&env)
			var measured bool
			for _, item := range measureItems(context.Background(), specs, env, nil, nil) {
				if item.ID == "dev.pnpm-store" {
					measured = true
					if item.Size != 0 {
						t.Errorf("overlapping estimate must be hidden: %+v", item)
					}
				}
			}
			if !measured {
				t.Error("overlapping command was not measured")
			}
			res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.pnpm-store"}}, time.Time{}, cleanHooks{})
			if exists(log) || len(res) != 1 || res[0].Skipped != "failed" {
				t.Fatalf("overlapping command executed: %+v", res)
			}
		})
	}
}

// TestIgnoredToolDoesNotEstimate preserves catalog claims without querying hidden tools.
func TestIgnoredToolDoesNotEstimate(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	root := filepath.Join(env.home, "Library/Caches/pnpm")
	writeFile(t, filepath.Join(root, "cache"), 100)
	log := filepath.Join(bin, "calls")
	fakeTool(t, bin, "pnpm", `echo "$@" >> `+shellPath(log)+`
echo `+shellPath(root))
	stageTools(t, &env, bin)
	env.settings.Ignored = []string{"dev.pnpm", "dev.pnpm-store"}
	specs, _ := expandItems(&env)
	if len(specs) != 0 {
		t.Fatalf("ignored rules leaked store: %v", itemIDs(specs))
	}
	env.settings.Ignored = nil
	env.settings.DisabledGroups = []string{"dev"}
	specs, _ = expandItems(&env)
	if len(specs) != 0 {
		t.Fatalf("disabled rules leaked store: %v", itemIDs(specs))
	}
	if data, err := os.ReadFile(log); err != nil && !os.IsNotExist(err) || len(data) != 0 {
		t.Fatalf("hidden tool estimated: %q, %v", data, err)
	}
}

// TestToolCleanupRechecksUsageAfterEstimate protects apps and tools started during estimation.
func TestToolCleanupRechecksUsageAfterEstimate(t *testing.T) {
	for _, kind := range []string{"owner", "process", "open"} {
		t.Run(kind, func(t *testing.T) {
			env := testEnv(t)
			bin := t.TempDir()
			root := filepath.Join(env.home, "store")
			writeFile(t, filepath.Join(root, "cache"), 100)
			log := filepath.Join(bin, "called")
			fakeTool(t, bin, "pnpm", `case "$*" in "store path") echo `+shellPath(root)+`;; *) touch `+shellPath(log)+`;; esac`)
			stageTools(t, &env, bin)
			var changed bool
			psCommands = func() ([]byte, error) {
				if changed && kind == "process" {
					return []byte("/opt/homebrew/bin/pnpm install\n"), nil
				}
				return nil, nil
			}
			env.refreshGuard = func() *usageGuard {
				g := &usageGuard{running: map[string]bool{}}
				if changed && kind == "owner" {
					g.running["com.test.pnpm-owner"] = true
				}
				if changed && kind == "open" {
					g.open = []string{canonical(filepath.Join(root, "cache"))}
				}
				return g
			}
			// Give this command an owner for the fresh-guard boundary test.
			for i := range junkRules {
				if junkRules[i].ID == "dev.pnpm-store" {
					saved := junkRules[i].Owners
					junkRules[i].Owners = []string{"com.test.pnpm-owner"}
					t.Cleanup(func() { junkRules[i].Owners = saved })
				}
			}
			res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.pnpm-store"}}, time.Time{}, cleanHooks{checking: func(string, string) { changed = true }})
			want := "busy"
			if kind == "owner" {
				want = "running"
			}
			if exists(log) || len(res) != 1 || res[0].Skipped != want {
				t.Fatalf("started during estimate (%s): %+v", kind, res)
			}
		})
	}
}

// TestRunToolEndsChildrenAfterLeaderExit prevents writes after a failed command returns.
func TestRunToolEndsChildrenAfterLeaderExit(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "late-write")
	fakeTool(t, bin, "background", `(sleep 1; touch `+shellPath(marker)+`) &
exit 0`)
	_, err := runTool(context.Background(), filepath.Join(bin, "background"), nil, 5*time.Second)
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("background pipe owner: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	if exists(marker) {
		t.Fatal("child wrote after the command had returned")
	}
}

// TestToolCleanupSkipsSpecialEntries leaves FIFO handling to the official tool.
func TestToolCleanupSkipsSpecialEntries(t *testing.T) {
	env := testEnv(t)
	bin := t.TempDir()
	root := filepath.Join(env.home, "store")
	writeFile(t, filepath.Join(root, "cache"), 100)
	if err := syscall.Mkfifo(filepath.Join(root, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(bin, "called")
	fakeTool(t, bin, "pnpm", `case "$*" in "store path") echo `+shellPath(root)+`;; *) touch `+shellPath(log)+`;; esac`)
	stageTools(t, &env, bin)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "dev.pnpm-store"}}, time.Time{}, cleanHooks{})
	if !exists(log) || len(res) != 1 || res[0].Skipped != "" || res[0].Freed != 0 || !exists(filepath.Join(root, "fifo")) {
		t.Fatalf("tool cleanup with special entries: %+v", res)
	}
}
