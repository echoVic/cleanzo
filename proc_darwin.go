package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"time"
)

// toolDirs lists fixed executable directories for apps started from Finder.
var toolDirs = func() []string {
	home := homeDir()
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin"}
	for _, dir := range []string{".local/bin", ".cargo/bin", ".bun/bin", ".volta/bin", "Library/pnpm"} {
		dirs = append(dirs, filepath.Join(home, dir))
	}
	dirs = append(dirs, "/usr/local/go/bin")
	nodes, _ := filepath.Glob(filepath.Join(home, ".nvm/versions/node/*/bin"))
	slices.SortFunc(nodes, func(a, b string) int {
		return compareVersions(nodeVersion(a), nodeVersion(b))
	})
	if len(nodes) > 0 {
		dirs = append(dirs, nodes[len(nodes)-1])
	}
	return dirs
}

// psCommands lists process commands with a bounded wait.
var psCommands = func() ([]byte, error) {
	out, err := runTool(context.Background(), "/bin/ps", []string{"-axo", "command="}, 5*time.Second)
	return []byte(out), err
}

// findTool finds an executable without consulting a shell or inherited PATH.
func findTool(name string) string {
	if name == "" || filepath.Base(name) != name {
		return ""
	}
	for _, dir := range toolDirs() {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return path
		}
	}
	return ""
}

// runToolCmd runs a command and kills its process group on completion.
func runToolCmd(cmd *exec.Cmd) ([]byte, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.CombinedOutput()
	// A leader can exit while a background child still owns the output pipes.
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return out, err
}

// detachedOutputTail is how much of a detached command's output is read
// back: its end, where the result is.
const detachedOutputTail = 64 << 10

// runToolDetached runs a command that must finish even when Cleanzo quits
// under it, as a brew upgrade must. Its output goes to a file, unlinked once
// open, instead of a pipe: a write to a pipe whose reader has gone kills the
// writer. It runs in its own process group, which cancellation kills while
// Cleanzo runs, and which is killed once the command ends, as runToolCmd
// does. The end of the output is read back from the file then.
func runToolDetached(cmd *exec.Cmd) ([]byte, error) {
	f, err := os.CreateTemp("", "cleanzo-tool-*.log")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	_ = os.Remove(f.Name()) // the command and f keep it open; nothing is left if Cleanzo quits
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 100 * time.Millisecond
	err = cmd.Run()
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	info, statErr := f.Stat()
	if statErr != nil {
		return nil, err
	}
	off := max(0, info.Size()-detachedOutputTail)
	out := make([]byte, info.Size()-off)
	n, _ := f.ReadAt(out, off)
	return out[:n], err
}

// toolPath builds the fixed PATH used for official cleanup tools.
func toolPath(tool string) string {
	return "PATH=" + filepath.Dir(tool) + ":/usr/bin:/bin:/usr/sbin:/sbin"
}
