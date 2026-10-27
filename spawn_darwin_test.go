package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSpawnOutputReadsStandardOutput(t *testing.T) {
	out, pid, err := spawnOutput(context.Background(), "/bin/echo", []string{"echo", "hello", "世界"}, nil)
	if err != nil || string(out) != "hello 世界\n" || pid <= 0 {
		t.Fatalf("got %q, pid %d, %v", out, pid, err)
	}
}

func TestSpawnOutputPassesTheEnvironmentAsGiven(t *testing.T) {
	env := withEnv([]string{"A=1", "LC_ALL=C", "B=two"}, "LC_ALL=en_US.UTF-8")
	out, _, err := spawnOutput(context.Background(), "/usr/bin/env", []string{"env"}, env)
	if err != nil || string(out) != "A=1\nB=two\nLC_ALL=en_US.UTF-8\n" {
		t.Fatalf("env printed %q, %v", out, err)
	}
}

func TestSpawnOutputFailures(t *testing.T) {
	if _, _, err := spawnOutput(context.Background(), "/usr/bin/false", []string{"false"}, nil); err == nil {
		t.Error("a non-zero exit was not an error")
	}
	if _, pid, err := spawnOutput(context.Background(), "/nonexistent/tool", []string{"tool"}, nil); err == nil || pid != 0 {
		t.Errorf("missing executable: pid %d, err %v", pid, err)
	}
	if _, _, err := spawnOutput(context.Background(), "/bin/echo", []string{"echo", "a\x00b"}, nil); err == nil {
		t.Error("an argument with a NUL byte was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, pid, err := spawnOutput(ctx, "/bin/echo", []string{"echo"}, nil); !errors.Is(err, context.Canceled) || pid != 0 {
		t.Errorf("canceled before the start: pid %d, err %v", pid, err)
	}
}

func TestSpawnOutputGivesTheChildNothingToRead(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, _, err := spawnOutput(context.Background(), "/bin/cat", []string{"cat"}, nil)
		if err != nil || len(out) != 0 {
			t.Errorf("cat got %q, %v", out, err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cat is still waiting for input")
	}
}

func TestSpawnOutputKillsTheChildWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, pid, err := spawnOutput(ctx, "/bin/sleep", []string{"sleep", "5"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
	if err := syscall.Kill(int(pid), 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("pid %d is still there (%v): not killed, or not reaped", pid, err)
	}
}

func TestSpawnOutputClosesOtherDescriptors(t *testing.T) {
	fd, err := syscall.Open(os.DevNull, syscall.O_RDONLY, 0) // no O_CLOEXEC on purpose
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	script := fmt.Sprintf("if [ -e /dev/fd/%d ]; then echo leaked; else echo closed; fi", fd)
	out, _, err := spawnOutput(context.Background(), "/bin/sh", []string{"sh", "-c", script}, nil)
	if err != nil || strings.TrimSpace(string(out)) != "closed" {
		t.Fatalf("descriptor %d in the child: %q, %v", fd, out, err)
	}
}

func TestSpawnOutputLeavesNoDescriptorsBehind(t *testing.T) {
	count := func() int { e, _ := os.ReadDir("/dev/fd"); return len(e) }
	before := count()
	for range 20 {
		if _, _, err := spawnOutput(context.Background(), "/usr/bin/true", []string{"true"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if after := count(); after != before {
		t.Fatalf("open descriptors %d -> %d", before, after)
	}
}

func TestSpawnOutputRunsSetuidPs(t *testing.T) {
	out, _, err := spawnOutput(context.Background(), "/bin/ps", []string{"/bin/ps", "-ax", "-o", "pid=,uid="}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "1" && f[1] == "0" {
			return
		}
	}
	t.Fatalf("ps did not list launchd (pid 1, uid 0); %d bytes of output", len(out))
}
