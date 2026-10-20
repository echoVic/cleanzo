package main

import "os/exec"

// quietCommand preserves Darwin command execution without process attributes.
func quietCommand(cmd *exec.Cmd) *exec.Cmd { return cmd }
