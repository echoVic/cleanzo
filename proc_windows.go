package main

import "os/exec"

// toolDirs offers no tools until Windows command execution is implemented.
var toolDirs = func() []string { return nil }

// psCommands fails closed until Windows process snapshots are implemented.
var psCommands = func() ([]byte, error) { return nil, errUnsupported }

// findTool disables cleanup commands until Windows tool lookup is implemented.
func findTool(name string) string { return "" }

// toolPath supplies no executable search path to unsupported commands.
func toolPath(tool string) string { return "PATH=" }

// runToolCmd refuses to start a process until tree cancellation is implemented.
func runToolCmd(cmd *exec.Cmd) ([]byte, error) { return nil, errUnsupported }
