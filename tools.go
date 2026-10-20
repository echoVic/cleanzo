package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// toolCommand describes an official cleanup and its size estimate.
type toolCommand struct {
	Tool     string
	Args     []string
	Busy     []string
	Timeout  time.Duration
	Estimate func(env junkEnv, tool string) (size int64, upper bool, roots []string, err error)
}

// brewCleanup lets Homebrew remove old versions and downloads.
var brewCleanup = toolCommand{
	Tool: "brew", Args: []string{"cleanup", "--prune=30"}, Busy: []string{"Homebrew/brew.rb", "Homebrew/brew.sh"}, Timeout: 5 * time.Minute,
	Estimate: func(env junkEnv, tool string) (int64, bool, []string, error) {
		out, err := runTool(env.toolContext(), tool, []string{"cleanup", "--prune=30", "--dry-run"}, 5*time.Minute)
		return parseBrewFreed(out), false, nil, err
	},
}

// pnpmPrune lets pnpm remove unused packages from its store.
var pnpmPrune = toolCommand{
	Tool: "pnpm", Args: []string{"store", "prune"}, Busy: []string{"/pnpm", "pnpm.cjs", "pnpm.mjs"}, Timeout: 5 * time.Minute,
	Estimate: func(env junkEnv, tool string) (int64, bool, []string, error) {
		size, roots, err := estimateToolFolder(env, tool, []string{"store", "path"}, 5*time.Minute)
		return size, true, roots, err
	},
}

// goModCleanup lets Go remove the module cache, including read-only modules.
var goModCleanup = toolCommand{
	Tool: "go", Args: []string{"clean", "-modcache"}, Busy: []string{"bin/go ", "gopls"}, Timeout: 5 * time.Minute,
	Estimate: func(env junkEnv, tool string) (int64, bool, []string, error) {
		size, roots, err := estimateToolFolder(env, tool, []string{"env", "GOMODCACHE"}, 5*time.Minute)
		return size, false, roots, err
	},
}

// windowsPnpmPrune uses native Windows executable and script command names.
var windowsPnpmPrune = func() toolCommand {
	c := pnpmPrune
	c.Busy = []string{"/pnpm.exe", "/pnpm.cmd", "pnpm.cjs", "pnpm.mjs"}
	return c
}()

// windowsGoModCleanup keeps the Go estimator and recognizes Windows command lines.
var windowsGoModCleanup = func() toolCommand {
	c := goModCleanup
	c.Busy = []string{"/go.exe", "gopls"}
	return c
}()

// simctlCleanup lets Xcode remove devices whose runtimes are unavailable.
var simctlCleanup = toolCommand{
	Tool: "xcrun", Args: []string{"simctl", "delete", "unavailable"}, Timeout: 2 * time.Minute,
	Estimate: func(env junkEnv, tool string) (int64, bool, []string, error) {
		if _, ok := env.apps.lookup("com.apple.dt.Xcode"); !ok {
			return 0, false, nil, errBlocked
		}
		out, err := runTool(env.toolContext(), tool, []string{"simctl", "list", "devices", "unavailable", "-j"}, 2*time.Minute)
		if err != nil {
			return 0, false, nil, err
		}
		var devices struct {
			Devices map[string][]json.RawMessage `json:"devices"`
		}
		if json.Unmarshal([]byte(out), &devices) != nil {
			return 0, false, nil, errors.New("failed")
		}
		roots := parseSimctlUnavailable([]byte(out))
		base := canonical(filepath.Join(env.home, "Library/Developer/CoreSimulator/Devices"))
		for _, root := range roots {
			if canonical(filepath.Dir(root)) != base {
				return 0, false, nil, errBlocked
			}
		}
		size, err := env.toolRootsSize(roots)
		return size, false, roots, err
	},
}

// nodeVersion reads the numerical parts of an nvm version directory.
func nodeVersion(bin string) []int {
	var parts []int
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Base(filepath.Dir(bin)), "v"), ".") {
		n, _ := strconv.Atoi(part)
		parts = append(parts, n)
	}
	return parts
}

// toolBusy fails closed when process commands cannot be inspected.
func toolBusy(patterns []string) bool {
	out, err := psCommands()
	if err != nil {
		return true
	}
	return commandBusy(string(out), patterns, runtime.GOOS == "windows")
}

// commandBusy normalizes Windows separators and case while preserving Darwin matches.
func commandBusy(out string, patterns []string, windows bool) bool {
	if windows {
		out = strings.ToLower(strings.ReplaceAll(out, `\`, "/"))
	}
	return slices.ContainsFunc(patterns, func(pattern string) bool { return strings.Contains(out, pattern) })
}

// runTool bounds an official command and kills its process group on cancellation.
func runTool(ctx context.Context, tool string, args []string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = append(os.Environ(), toolPath(tool))
	if filepath.Base(tool) == "brew" {
		cmd.Env = append(cmd.Env, "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ANALYTICS=1", "HOMEBREW_NO_ENV_HINTS=1")
	}
	out, err := runToolCmd(cmd)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return string(out), err
}

// brewFreedPattern matches Homebrew's estimate and actual release messages.
var brewFreedPattern = regexp.MustCompile(`(?:would free|has freed) approximately ([\d.]+)\s?(B|KB|MB|GB|TB)`)

// parseBrewFreed reads Homebrew's binary-unit disk space report.
func parseBrewFreed(out string) int64 {
	match := brewFreedPattern.FindStringSubmatch(out)
	if len(match) != 3 {
		return 0
	}
	n, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0
	}
	power := slices.Index([]string{"B", "KB", "MB", "GB", "TB"}, match[2])
	return int64(n * float64(int64(1)<<(10*power)))
}

// parseSimctlUnavailable returns unique device folders in deterministic order.
func parseSimctlUnavailable(out []byte) []string {
	var list struct {
		Devices map[string][]struct {
			DataPath  string `json:"dataPath"`
			Available bool   `json:"isAvailable"`
		} `json:"devices"`
	}
	if json.Unmarshal(out, &list) != nil {
		return nil
	}
	var roots []string
	for _, devices := range list.Devices {
		for _, device := range devices {
			if !device.Available && filepath.IsAbs(device.DataPath) && filepath.Base(device.DataPath) == "data" {
				roots = append(roots, filepath.Dir(device.DataPath))
			}
		}
	}
	slices.Sort(roots)
	return slices.Compact(roots)
}

// toolContext uses the current pass's cancellation for command estimates.
func (env junkEnv) toolContext() context.Context {
	if env.ctx != nil {
		return env.ctx
	}
	return context.Background()
}

// estimateToolFolder queries and measures a tool-owned directory under home.
func estimateToolFolder(env junkEnv, tool string, args []string, timeout time.Duration) (int64, []string, error) {
	out, err := runTool(env.toolContext(), tool, args, timeout)
	if err != nil {
		return 0, nil, err
	}
	root := strings.TrimSpace(out)
	if !filepath.IsAbs(root) {
		return 0, nil, errBlocked
	}
	roots := []string{filepath.Clean(root)}
	size, err := env.toolRootsSize(roots)
	return size, roots, err
}

// validToolRoot checks a home target before resolving or measuring its contents.
func (env junkEnv) validToolRoot(root string) bool {
	return filepath.IsAbs(root) && !env.traversalBlocked(root) && within(canonical(root), canonical(env.home)) && env.removable(resolvePath(root), false)
}

// toolRootsSize validates roots and measures without following links or ignoring cancellation.
func (env junkEnv) toolRootsSize(roots []string) (int64, error) {
	var size int64
	for _, root := range roots {
		if !env.validToolRoot(root) {
			return 0, errBlocked
		}
		info, err := os.Lstat(root)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return 0, err
		}
		if !info.Mode().IsRegular() && !info.IsDir() || removalRefused(root, info) {
			return 0, errBlocked
		}
		for _, pattern := range protectedPatterns {
			for _, protected := range env.globTargets(pattern) {
				if within(canonical(protected), canonical(root)) {
					return 0, errBlocked
				}
			}
		}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if env.toolContext().Err() != nil {
				return env.toolContext().Err()
			}
			if errors.Is(err, fs.ErrNotExist) && path == root {
				return nil
			}
			if err != nil {
				return err
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				size += onDisk(info)
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return size, nil
}

// commandOwners includes the Simulator app for simulator cleanup.
func commandOwners(rule *Rule) []string {
	if rule.ID == "review.simulators" {
		return []string{"com.apple.iphonesimulator"}
	}
	return rule.Owners
}

// estimateCommand applies the command's timeout to its read-only estimate.
func estimateCommand(env junkEnv, command *toolCommand, tool string) (int64, bool, []string, error) {
	ctx, cancel := context.WithTimeout(env.toolContext(), command.Timeout)
	defer cancel()
	env.ctx = ctx
	return command.Estimate(env, tool)
}

// expandCommand builds a pending command row when its tool exists.
func expandCommand(env junkEnv, rule *Rule) itemSpec {
	spec := itemSpec{rule: rule, item: JunkItem{ID: rule.ID, Group: string(rule.Group), Default: rule.Default, Trash: rule.Trash, Running: env.running(commandOwners(rule))}}
	if env.findTool(rule.Command.Tool) != "" {
		spec.item.Size = -1
	}
	return spec
}

// cleanTool runs one cleanup, reporting its failure or actual release.
func cleanTool(ctx context.Context, rule *Rule, env junkEnv, hooks cleanHooks, cleaned *int64) CleanResult {
	res := CleanResult{Name: rule.ID}
	command := rule.Command
	tool := env.findTool(command.Tool)
	if tool == "" {
		res.Skipped = "no-tool"
		return res
	}
	if env.guard.failed || toolBusy(command.Busy) {
		res.Skipped = "busy"
		return res
	}
	before, _, roots, err := estimateCommand(env, command, tool)
	if err == nil && env.commandClaims[rule.ID].overlaps(roots) {
		err = errBlocked
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		if hooks.checking != nil {
			hooks.checking(rule.ID, tool)
		}
		// Estimation may take minutes; check usage again before the destructive command.
		if env.refreshGuard != nil {
			env.guard = env.refreshGuard()
		}
		if env.running(commandOwners(rule)) {
			res.Skipped = "running"
			return res
		}
		if env.guard.failed || toolBusy(command.Busy) {
			res.Skipped = "busy"
			return res
		}
		for _, root := range roots {
			if env.guard.inUse(resolvePath(root)) {
				res.Skipped = "busy"
				return res
			}
		}
	}
	var out string
	if err == nil {
		out, err = runTool(ctx, tool, command.Args, command.Timeout)
	}
	if err != nil {
		res.Skipped = "failed"
		if errors.Is(err, context.DeadlineExceeded) {
			res.Skipped = "timeout"
		} else if !errors.Is(err, context.Canceled) {
			line := strings.TrimSpace(out)
			if i := strings.LastIndexByte(line, '\n'); i >= 0 {
				line = line[i+1:]
			}
			if line == "" {
				line = err.Error()
			}
			res.Failed = []string{line}
		}
		return res
	}
	if command.Tool == "brew" {
		res.Freed = parseBrewFreed(out)
	} else {
		after, err := env.toolRootsSize(roots)
		if err != nil {
			res.Skipped, res.Failed = "failed", []string{err.Error()}
			return res
		}
		res.Freed = max(0, before-after)
	}
	*cleaned += res.Freed
	if hooks.removed != nil {
		hooks.removed(rule.ID, tool, *cleaned)
	}
	return res
}
