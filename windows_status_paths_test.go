package main

import "testing"

// TestWindowsSystemPath runs on Darwin too; only the native root lookup is Windows-only.
func TestWindowsSystemPath(t *testing.T) {
	for _, tt := range []struct {
		name, exe, root string
		want            bool
	}{
		{"system32", `C:\Windows\System32\RuntimeBroker.exe`, `C:\Windows`, true},
		{"system-apps", `C:\Windows\SystemApps\Terminal\host.exe`, `C:\Windows`, true},
		{"case-and-slashes", `c:/WINDOWS/System32/host.exe`, `C:\Windows\`, true},
		{"program-files", `C:\Program Files\Acme\app.exe`, `C:\Windows`, false},
		{"prefix-sibling", `C:\WindowsApps\app.exe`, `C:\Windows`, false},
		{"other-drive", `D:\Windows\app.exe`, `C:\Windows`, false},
		{"unknown-root", `C:\Program Files\Acme\app.exe`, "", true},
		{"unknown-executable", "", `C:\Windows`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := windowsSystemPath(tt.exe, tt.root, func(path string) string { return path }); got != tt.want {
				t.Fatalf("system path %q under %q = %v, want %v", tt.exe, tt.root, got, tt.want)
			}
		})
	}
}

func TestWindowsSystemPathResolvesBothPaths(t *testing.T) {
	resolve := func(path string) string {
		switch path {
		case `C:\WinAlias`:
			return `D:\Windows`
		case `C:\Apps\linked.exe`:
			return `D:\Windows\System32\host.exe`
		case `C:\Unreadable.exe`:
			return ""
		default:
			return path
		}
	}
	if !windowsSystemPath(`C:\Apps\linked.exe`, `C:\WinAlias`, resolve) {
		t.Fatal("resolved system executable allowed to quit")
	}
	if !windowsSystemPath(`C:\Unreadable.exe`, `C:\WinAlias`, resolve) {
		t.Fatal("unresolved executable allowed to quit")
	}
}
