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

// toolPath builds the fixed PATH used for official cleanup tools.
func toolPath(tool string) string {
	return "PATH=" + filepath.Dir(tool) + ":/usr/bin:/bin:/usr/sbin:/sbin"
}
