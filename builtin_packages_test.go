package main

import (
	"errors"
	"testing"
)

// TestStatusTerminalPackageRegression reproduces W16 with the actual VM executable path.
func TestStatusTerminalPackageRegression(t *testing.T) {
	saved := statusPackageFamilyName
	statusPackageFamilyName = func(int32) (string, error) { return "", errors.New("package API unavailable") }
	t.Cleanup(func() { statusPackageFamilyName = saved })
	a := workspaceApp{Pid: 900001, Path: `C:\Program Files\WindowsApps\Microsoft.WindowsTerminal_1.24.11321.0_x64__8wekyb3d8bbwe\WindowsTerminal.exe`, Regular: true}
	if !statusAppExcluded(a) {
		t.Fatal("W16: Microsoft-packaged Windows Terminal Host is still eligible for Quit")
	}
}

// TestWindowsPackageFamilyPublisher separates trusted publishers from package display names.
func TestWindowsPackageFamilyPublisher(t *testing.T) {
	for _, tt := range []struct {
		family, publisher string
		builtin           bool
	}{
		{"Microsoft.WindowsTerminal_8wekyb3d8bbwe", "8wekyb3d8bbwe", true},
		{"MicrosoftWindows.Client.WebExperience_cw5n1h2txyewy", "cw5n1h2txyewy", true},
		{"SpotifyAB.SpotifyMusic_zpdnekdrzrea0", "zpdnekdrzrea0", false},
		{"Microsoft.Spoof_zpdnekdrzrea0", "zpdnekdrzrea0", false},
		{"", "", false},
		{"Microsoft.WindowsTerminal", "", false},
		{"_8wekyb3d8bbwe", "", false},
		{"Microsoft.WindowsTerminal_extra_8wekyb3d8bbwe", "", false},
		{"Microsoft.WindowsTerminal_8wekyb3d8bbw!", "", false},
	} {
		t.Run(tt.family, func(t *testing.T) {
			if got := windowsPackageFamilyPublisher(tt.family); got != tt.publisher {
				t.Fatalf("publisher=%q, want %q", got, tt.publisher)
			}
			lookup := func(int32) (string, error) { return tt.family, nil }
			if got := windowsBuiltinPackage(123, `C:\Apps\app.exe`, lookup); got != tt.builtin {
				t.Fatalf("built-in=%v, want %v", got, tt.builtin)
			}
		})
	}
}

// TestWindowsPackagePathPublisher validates a full package directory directly under WindowsApps.
func TestWindowsPackagePathPublisher(t *testing.T) {
	for _, tt := range []struct{ name, exe, publisher string }{
		{"terminal", `C:\Program Files\WindowsApps\Microsoft.WindowsTerminal_1.24.11321.0_x64__8wekyb3d8bbwe\WindowsTerminal.exe`, "8wekyb3d8bbwe"},
		{"other-drive", `D:\WindowsApps\Microsoft.WindowsCalculator_11.0.0.0_arm64__8wekyb3d8bbwe\CalculatorApp.exe`, "8wekyb3d8bbwe"},
		{"case-slashes-resource", `d:/WINDOWSAPPS/MicrosoftWindows.Client.WebExperience_1.2.3.4_neutral_split.scale-100_cw5n1h2txyewy/bin/app.exe`, "cw5n1h2txyewy"},
		{"third-party", `C:\Program Files\WindowsApps\SpotifyAB.SpotifyMusic_1.2.3.4_x86__zpdnekdrzrea0\Spotify.exe`, "zpdnekdrzrea0"},
		{"prefix", `C:\MyWindowsApps\Microsoft.WindowsTerminal_1.24.11321.0_x64__8wekyb3d8bbwe\app.exe`, ""},
		{"suffix", `C:\WindowsApps-backup\Microsoft.WindowsTerminal_1.24.11321.0_x64__8wekyb3d8bbwe\app.exe`, ""},
		{"not-direct-child", `C:\WindowsApps\Other\Microsoft.WindowsTerminal_1.24.11321.0_x64__8wekyb3d8bbwe\app.exe`, ""},
		{"family-only", `C:\WindowsApps\Microsoft.WindowsTerminal_8wekyb3d8bbwe\app.exe`, ""},
		{"missing-resource", `C:\WindowsApps\Microsoft.WindowsTerminal_1.2.3.4_x64_8wekyb3d8bbwe\app.exe`, ""},
		{"bad-version", `C:\WindowsApps\Microsoft.WindowsTerminal_1.2.3_x64__8wekyb3d8bbwe\app.exe`, ""},
		{"overflow-version", `C:\WindowsApps\Microsoft.WindowsTerminal_1.2.3.65536_x64__8wekyb3d8bbwe\app.exe`, ""},
		{"bad-architecture", `C:\WindowsApps\Microsoft.WindowsTerminal_1.2.3.4_cpu__8wekyb3d8bbwe\app.exe`, ""},
		{"bad-name", `C:\WindowsApps\Microsoft Terminal_1.2.3.4_x64__8wekyb3d8bbwe\app.exe`, ""},
		{"bad-publisher", `C:\WindowsApps\Microsoft.WindowsTerminal_1.2.3.4_x64__8wekyb3d8bbwe-extra\app.exe`, ""},
		{"traversal", `C:\WindowsApps\Microsoft.WindowsTerminal_1.2.3.4_x64__8wekyb3d8bbwe\..\Other\app.exe`, ""},
		{"relative", `WindowsApps\Microsoft.WindowsTerminal_1.2.3.4_x64__8wekyb3d8bbwe\app.exe`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := windowsPackagePathPublisher(tt.exe); got != tt.publisher {
				t.Fatalf("publisher=%q, want %q", got, tt.publisher)
			}
		})
	}
}

// TestWindowsBuiltinPackageLookup protects API identities and falls back only on query errors.
func TestWindowsBuiltinPackageLookup(t *testing.T) {
	for _, tt := range []struct {
		name, family, exe string
		err               error
		want              bool
	}{
		{"api-microsoft", "Microsoft.WindowsTerminal_8wekyb3d8bbwe", `C:\Apps\terminal.exe`, nil, true},
		{"api-windows", "MicrosoftWindows.Client.WebExperience_cw5n1h2txyewy", `C:\Apps\widget.exe`, nil, true},
		{"api-third-party", "SpotifyAB.SpotifyMusic_zpdnekdrzrea0", `C:\WindowsApps\Microsoft.WindowsTerminal_1.2.3.4_x64__8wekyb3d8bbwe\app.exe`, nil, false},
		{"api-unpackaged", "", `C:\WindowsApps\Microsoft.WindowsTerminal_1.2.3.4_x64__8wekyb3d8bbwe\app.exe`, nil, false},
		{"failed-open", "", `D:\WindowsApps\Microsoft.WindowsTerminal_1.2.3.4_x64__8wekyb3d8bbwe\app.exe`, errors.New("access denied"), true},
		{"api-error-third-party", "", `D:\WindowsApps\SpotifyAB.SpotifyMusic_1.2.3.4_x64__zpdnekdrzrea0\app.exe`, errors.New("query failed"), false},
		{"api-error-ordinary", "", `C:\Apps\app.exe`, errors.New("query failed"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lookup := func(pid int32) (string, error) {
				if pid != 123 {
					t.Fatalf("queried PID=%d, want 123", pid)
				}
				return tt.family, tt.err
			}
			if got := windowsBuiltinPackage(123, tt.exe, lookup); got != tt.want {
				t.Fatalf("built-in=%v, want %v", got, tt.want)
			}
		})
	}
}
