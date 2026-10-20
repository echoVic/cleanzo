package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestWindowsPsCommandsIncludesExecutable protects launches whose command line uses only the typed name.
func TestWindowsPsCommandsIncludesExecutable(t *testing.T) {
	saved := snapshotProcesses
	snapshotProcesses = func() ([]procInfo, error) {
		return []procInfo{{Exe: `C:\tools\pnpm.exe`, CmdLine: "pnpm install"}, {CmdLine: "unreadable.exe"}}, nil
	}
	t.Cleanup(func() { snapshotProcesses = saved })
	out, err := psCommands()
	if err != nil || !strings.Contains(string(out), `C:\tools\pnpm.exe pnpm install`) || !toolBusy(windowsPnpmPrune.Busy) {
		t.Fatalf("commands=%q error=%v", out, err)
	}
}

// TestWindowsQuietCommandAndNativeCmd ignores a spoofed SystemRoot and retains hidden process flags.
func TestWindowsQuietCommandAndNativeCmd(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "ready.cmd")
	if err := os.WriteFile(shim, []byte("@echo ready\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SystemRoot", filepath.Join(dir, "spoof"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	var out []byte
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		cmd = quietCommand(exec.CommandContext(ctx, shim))
		out, err = runToolCmd(cmd)
		if err == nil || ctx.Err() != nil {
			break
		}
		if attempt == 0 {
			time.Sleep(150 * time.Millisecond)
		}
	}
	systemDir, systemErr := windows.GetSystemDirectory()
	if systemErr != nil || err != nil || strings.TrimSpace(string(out)) != "ready" || !strings.EqualFold(cmd.Path, filepath.Join(systemDir, "cmd.exe")) {
		t.Fatalf("cmd=%q output=%q err=%v system=%v", cmd.Path, out, err, systemErr)
	}
	if !cmd.SysProcAttr.HideWindow || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("command can show a console window")
	}
}

// TestWindowsFindToolPATHEXT checks batch discovery and the exact isolated tool PATH.
func TestWindowsFindToolPATHEXT(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	writeFile(t, filepath.Join(dir, "x.cmd"), 10)
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(node, "node.exe"), 10)
	saved := toolDirs
	toolDirs = func() []string { return []string{dir, node} }
	t.Cleanup(func() { toolDirs = saved })
	t.Setenv("PATHEXT", ".EXE;.CMD")
	tool := findTool("x")
	if tool != filepath.Join(dir, "x.CMD") || findTool(`..\x`) != "" {
		t.Fatalf("PATHEXT lookup: %q", tool)
	}
	root := os.Getenv("SystemRoot")
	if got, want := toolPath(tool), "PATH="+strings.Join([]string{dir, node, filepath.Join(root, "System32"), root}, ";"); !strings.EqualFold(got, want) {
		t.Fatalf("PATH=%q want %q", got, want)
	}
}

// TestWindowsToolTreeKill observes the specific ping descendant, then checks timeout kills the whole tree.
func TestWindowsToolTreeKill(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "slowtool.cmd")
	if err := os.WriteFile(tool, []byte("@echo off\r\nping -n 30 127.0.0.1 >nul\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	timeout := 8 * time.Second
	done := make(chan error, 1)
	stopped := make(chan struct{})
	defer func() { cancel(); <-stopped }()
	go func() {
		defer close(stopped)
		_, err := runTool(ctx, tool, nil, timeout)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			time.Sleep(150 * time.Millisecond)
			_, err = runTool(ctx, tool, nil, timeout)
		}
		done <- err
	}()
	var child uint32
	deadline := time.Now().Add(5 * time.Second)
	for child == 0 && time.Now().Before(deadline) {
		procs, err := processSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		parents := map[uint32]bool{}
		for _, p := range procs {
			if p.PPid == uint32(os.Getpid()) && strings.Contains(strings.ToLower(p.CmdLine), "slowtool.cmd") {
				parents[p.Pid] = true
			}
		}
		for _, p := range procs {
			if parents[p.PPid] && strings.EqualFold(p.Name, "ping.exe") {
				child = p.Pid
			}
		}
		if child == 0 {
			time.Sleep(25 * time.Millisecond)
		}
	}
	if child == 0 {
		cancel()
		t.Fatal("tool never started its ping child")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout result: %v", err)
		}
	case <-time.After(12 * time.Second):
		cancel()
		t.Fatal("tool timeout did not finish")
	}
	for deadline := time.Now().Add(2 * time.Second); ; {
		alive := false
		procs, err := processSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range procs {
			if p.Pid == child {
				alive = true
			}
		}
		if !alive {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ping descendant survived job close")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
