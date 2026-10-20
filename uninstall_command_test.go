package main

import (
	"errors"
	"testing"
)

// TestParseUninstallString exercises Windows command parsing on every host.
func TestParseUninstallString(t *testing.T) {
	const guid = "{12345678-1234-1234-1234-123456789ABC}"
	exists := func(path string) bool {
		switch path {
		case `C:\Program Files\Acme\uninstall.exe`, `C:\Short.exe`, `C:\Short.exe Folder\remove.exe`, `C:\Tools\setup.dll`, `C:\Windows\System32\cmd.exe`:
			return true
		}
		return false
	}
	for _, tc := range []struct{ name, raw, exe, args string }{
		{"quoted path", `"C:\Program Files\Acme\uninstall.exe" /S "keep space"`, `C:\Program Files\Acme\uninstall.exe`, `/S "keep space"`},
		{"unquoted path with spaces", `C:\Program Files\Acme\uninstall.exe /uninstall`, `C:\Program Files\Acme\uninstall.exe`, `/uninstall`},
		{"longest existing prefix", `C:\Short.exe Folder\remove.exe /S`, `C:\Short.exe Folder\remove.exe`, `/S`},
		{"msi install", `MsiExec.exe /I` + guid, "MsiExec.exe", `/X` + guid},
		{"msi spaced install", `msiexec.exe /i ` + guid + ` /passive`, "msiexec.exe", `/X ` + guid + ` /passive`},
		{"msi uninstall", `MsiExec.exe /X` + guid, "MsiExec.exe", `/X` + guid},
		{"rundll quoted", `rundll32.exe "C:\Tools\setup.dll",Uninstall /silent`, "rundll32.exe", `"C:\Tools\setup.dll",Uninstall /silent`},
		{"rundll system", `rundll32.exe setupapi.dll,InstallHinfSection DefaultUninstall 132 C:\Tools\app.inf`, "rundll32.exe", `setupapi.dll,InstallHinfSection DefaultUninstall 132 C:\Tools\app.inf`},
		{"garbage", `not an uninstall command`, "", ""},
		{"empty", "", "", ""},
		{"missing executable", `"C:\Missing\uninstall.exe" /S`, "", ""},
		{"relative executable", `uninstall.exe /S`, "", ""},
		{"script shell", `"C:\Windows\System32\cmd.exe" /c echo bad`, "", ""},
		{"unclosed quote", `"C:\Short.exe`, "", ""},
		{"no argument boundary", `"C:\Short.exe"evil`, "", ""},
		{"msi garbage", `msiexec.exe /Igarbage`, "", ""},
		{"rundll script", `rundll32.exe javascript:evil`, "", ""},
		{"nul", "C:\\Short.exe\x00 /S", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseUninstallString(tc.raw, exists)
			if tc.exe == "" {
				if !errors.Is(err, errBlocked) {
					t.Fatalf("got %+v, %v; want blocked", got, err)
				}
			} else if err != nil || got.exe != tc.exe || got.args != tc.args {
				t.Fatalf("got %+v, %v; want %q %q", got, err, tc.exe, tc.args)
			}
		})
	}
}
