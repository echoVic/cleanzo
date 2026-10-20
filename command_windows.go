package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// quietCommand suppresses consoles for commands launched by the GUI application.
func quietCommand(cmd *exec.Cmd) *exec.Cmd {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
	return cmd
}
