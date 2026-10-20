package main

import (
	"reflect"
	"testing"
)

// TestWindowsLocalPaths rejects device namespaces, streams and nonlocal paths before normalization.
func TestWindowsLocalPaths(t *testing.T) {
	for _, p := range []string{`\\server\share\x`, `\\?\C:\home\x`, `\\.\C:\home\x`, `C:\home\x:stream`, `C:relative`, `\relative`, `//server/share`, `C:\home\x\..\y:stream`, "C:\\x\x00bad", `C:\x*`, `C:\x?`} {
		if windowsLocalPath(p) {
			t.Errorf("accepted %q", p)
		}
	}
	for _, p := range []string{`C:\Users\A\cache`, `d:/Profile/cache`} {
		if !windowsLocalPath(p) {
			t.Errorf("refused %q", p)
		}
	}
}

// TestWindowsArgumentPaths extracts decoded absolute tokens and flag values, including unsupported usage namespaces.
func TestWindowsArgumentPaths(t *testing.T) {
	args := []string{`C:\Program Files\Fake\fake.exe`, `--user-data-dir=C:\Users\A\User Data`, `--cache`, `C:\Users\A\cache`, `--x=relative`, `https://example.com:80`, `--extended=\\?\C:\Users\A\Data`, `\\server\share`}
	want := []string{`C:\Program Files\Fake\fake.exe`, `C:\Users\A\User Data`, `C:\Users\A\cache`, `\\?\C:\Users\A\Data`, `\\server\share`}
	if got := windowsArgumentPaths(args); !reflect.DeepEqual(got, want) {
		t.Errorf("paths=%q want %q", got, want)
	}
}

// TestWindowsUsagePathNormalization accepts local namespaces without changing deletion-input restrictions.
func TestWindowsUsagePathNormalization(t *testing.T) {
	for _, prefix := range []string{`\\?\`, `\\.\`, `\??\`, ``} {
		want := `C:\Users\A\cache`
		if got := windowsUsagePath(prefix + want); got != want {
			t.Errorf("usage path=%q want %q", got, want)
		}
	}
	for _, path := range []string{`\\server\share\data`, `\\.\pipe\docker_engine`, `relative`} {
		if got := windowsUsagePath(path); got != "" {
			t.Errorf("non-local usage path=%q", got)
		}
	}
}
