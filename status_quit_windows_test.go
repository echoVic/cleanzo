package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestWindowsQuitReachesOnlyMainWindow models an app waiting for a save decision.
func TestWindowsQuitReachesOnlyMainWindow(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "cleanzo-quit-helper" {
		runQuitWindowHelper(t)
		return
	}
	cmd, records := startQuitWindowFixture(t)
	if !workspaceTerminate(int32(cmd.Process.Pid)) {
		t.Fatal("main-window close request refused")
	}
	// Queue a marker after the close requests. All fixture windows share one
	// thread, so it proves the negative assertions aren't racing pending messages.
	markerSent := false
	enumTopWindows(func(hwnd windows.HWND, pid uint32) {
		if pid == uint32(cmd.Process.Pid) && regularWindow(hwnd) {
			ok, _, _ := postMessage.Call(uintptr(hwnd), 0x8001, 0, 0) // WM_APP + 1
			markerSent = ok != 0
		}
	})
	if !markerSent {
		t.Fatal("main window disappeared before save decision")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(records)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		events := string(data)
		if strings.Contains(events, "helper-destroyed") || strings.Contains(events, "tool-close") {
			t.Fatalf("internal window received close: %s", events)
		}
		if strings.Contains(events, "close-requested\n") && strings.Contains(events, "queue-drained\n") {
			if !strings.Contains(events, "system-close\n") {
				t.Fatalf("close did not use WM_SYSCOMMAND / SC_CLOSE: %s", events)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("main window did not receive close request within 5s: %s", events)
		}
		time.Sleep(10 * time.Millisecond)
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if state, err := windows.WaitForSingleObject(h, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("app exited while waiting for save: state=%d err=%v", state, err)
	}
}

// TestWindowsTerminateRefusesSystemProcesses uses a live PID and its real birth time,
// so OpenProcess/PID-reuse refusal cannot mask a missing system-path guard.
func TestWindowsTerminateRefusesSystemProcesses(t *testing.T) {
	cmd, _ := startQuitWindowFixture(t)
	procs, err := processSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	var target procInfo
	for _, p := range procs {
		if p.Pid == uint32(cmd.Process.Pid) {
			target = p
			break
		}
	}
	if target.Created.IsZero() {
		t.Fatal("fixture birth time unavailable")
	}
	saved := statusSnapshotProcesses
	t.Cleanup(func() { statusSnapshotProcesses = saved })
	for _, tt := range []struct {
		name, exe string
		want      bool
	}{
		{"system32", filepath.Join(statusWindowsRoot, "System32", "RuntimeBroker.exe"), false},
		{"system-apps", filepath.Join(statusWindowsRoot, "SystemApps", "Terminal", "host.exe"), false},
		{"program-files", filepath.Join(filepath.VolumeName(statusWindowsRoot)+`\`, "Program Files", "Acme", "app.exe"), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := target
			p.Exe, p.Name = tt.exe, filepath.Base(tt.exe)
			statusSnapshotProcesses = func() ([]procInfo, error) { return []procInfo{p}, nil }
			if got := workspaceTerminate(int32(p.Pid)); got != tt.want {
				t.Fatalf("quit %s = %v, want %v", tt.exe, got, tt.want)
			}
		})
	}
}

func startQuitWindowFixture(t *testing.T) (*exec.Cmd, string) {
	t.Helper()
	// A distinct executable path keeps the real "another Cleanzo instance" guard active.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	childExe := filepath.Join(t.TempDir(), "save-prompt-fixture.exe")
	if err := os.WriteFile(childExe, data, 0700); err != nil {
		t.Fatal(err)
	}
	records := filepath.Join(t.TempDir(), "events.txt")
	cmd := exec.Command(childExe, "-test.run=^TestWindowsQuitReachesOnlyMainWindow$", "--", "cleanzo-quit-helper")
	cmd.Env = append(os.Environ(), "CLEANZO_QUIT_RECORDS="+records)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	// Keep helper errors visible on runners where the desktop is unavailable.
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill() // Fixture cleanup only; never part of the quit implementation.
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Error("fixture did not exit after cleanup")
		}
	})
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		var output []string
		for scanner.Scan() {
			output = append(output, scanner.Text())
			if scanner.Text() == "ready" {
				ready <- "ready"
				return
			}
		}
		ready <- "fixture stdout closed before ready: " + strings.Join(output, "\n")
	}()
	select {
	case result := <-ready:
		if result != "ready" {
			t.Fatal(result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("fixture did not become ready")
	}
	return cmd, records
}

// runQuitWindowHelper owns a real Win32 queue; SC_CLOSE must pass through DefWindowProc.
func runQuitWindowHelper(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	file, err := os.OpenFile(os.Getenv("CLEANZO_QUIT_RECORDS"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	record := func(event string) {
		if _, err := fmt.Fprintln(file, event); err != nil {
			panic(err)
		}
	}
	var mainWindow, helperWindow, toolWindow uintptr
	defWindowProc := statusUser.NewProc("DefWindowProcW")
	callback := windows.NewCallback(func(hwnd uintptr, message uint32, wparam, lparam uintptr) uintptr {
		switch {
		case hwnd == mainWindow && message == 0x0112 && wparam&0xfff0 == 0xf060:
			record("system-close") // Still forward SC_CLOSE to the default proc.
		case hwnd == mainWindow && message == 0x0010:
			record("close-requested")
			return 0 // Simulate an unsaved-work prompt: keep the app and windows alive.
		case hwnd == mainWindow && message == 0x8001:
			record("queue-drained")
			return 0
		case hwnd == helperWindow && message == 0x0002:
			record("helper-destroyed")
		case hwnd == toolWindow && message == 0x0010:
			record("tool-close")
			return 0
		}
		result, _, _ := defWindowProc.Call(hwnd, uintptr(message), wparam, lparam)
		return result
	})
	// WNDCLASSEXW and MSG from winuser.h, pointer-sized on both supported architectures.
	type windowClass struct {
		Size, Style            uint32
		Proc                   uintptr
		ClassExtra, WinExtra   int32
		Instance, Icon, Cursor windows.Handle
		Background             windows.Handle
		Menu, Name             *uint16
		SmallIcon              windows.Handle
	}
	name := windows.StringToUTF16Ptr("CleanzoQuitSaveFixture")
	instance, _, err := statusKernel.NewProc("GetModuleHandleW").Call(0)
	if instance == 0 {
		t.Fatal(err)
	}
	class := windowClass{Proc: callback, Name: name, Instance: windows.Handle(instance)}
	class.Size = uint32(unsafe.Sizeof(class))
	if ok, _, err := statusUser.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&class))); ok == 0 {
		t.Fatal(err)
	}
	create := func(style, extended uintptr) uintptr {
		hwnd, _, err := statusUser.NewProc("CreateWindowExW").Call(extended, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)), style, 0, 0, 100, 100, 0, 0, instance, 0)
		if hwnd == 0 {
			t.Fatal(err)
		}
		return hwnd
	}
	mainWindow = create(0x10cf0000, 0) // WS_OVERLAPPEDWINDOW | WS_VISIBLE
	helperWindow = create(0x80000000, 0)
	toolWindow = create(0x90000000, 0x80) // WS_POPUP | WS_VISIBLE, WS_EX_TOOLWINDOW
	fmt.Println("ready")
	type message struct {
		Window         windows.HWND
		ID             uint32
		WParam, LParam uintptr
		Time           uint32
		X, Y           int32
		Private        uint32
	}
	var msg message
	getMessage := statusUser.NewProc("GetMessageW")
	for {
		result, _, err := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) == -1 {
			t.Fatal(err)
		}
		if result == 0 {
			return
		}
		statusUser.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		statusUser.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
}
