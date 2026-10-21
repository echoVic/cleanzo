package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWindowsCurrentProcessHasNoPackage exercises the real appmodel API on an unpackaged executable.
func TestWindowsCurrentProcessHasNoPackage(t *testing.T) {
	pid := int32(os.Getpid())
	family, err := processPackageFamilyName(pid)
	if err != nil || family != "" {
		t.Fatalf("unpackaged test process: family=%q err=%v", family, err)
	}
	if windowsBuiltinPackage(pid, `C:\Apps\test.exe`, processPackageFamilyName) {
		t.Fatal("unpackaged test process excluded on package identity")
	}
}

// TestWindowsTerminateRefusesBuiltinPackage uses a real quittable process so other guards cannot mask a regression.
func TestWindowsTerminateRefusesBuiltinPackage(t *testing.T) {
	cmd, _ := startQuitWindowFixture(t)
	pid := int32(cmd.Process.Pid)
	if !workspaceTerminate(pid) {
		t.Fatal("fixture must accept an ordinary close request before package protection")
	}
	saved := statusPackageFamilyName
	t.Cleanup(func() { statusPackageFamilyName = saved })
	for _, family := range []string{"Microsoft.WindowsTerminal_8wekyb3d8bbwe", "MicrosoftWindows.Client.WebExperience_cw5n1h2txyewy"} {
		statusPackageFamilyName = func(target int32) (string, error) {
			if target != pid {
				t.Fatalf("queried PID=%d, want %d", target, pid)
			}
			return family, nil
		}
		if workspaceTerminate(pid) {
			t.Fatalf("quit Microsoft package %s accepted", family)
		}
	}
}

// TestWindowsPackagedAppsStayListedWithoutQuit covers both API protection and third-party eligibility.
func TestWindowsPackagedAppsStayListedWithoutQuit(t *testing.T) {
	saved := statusPackageFamilyName
	t.Cleanup(func() { statusPackageFamilyName = saved })
	for _, tt := range []struct {
		family   string
		excluded bool
	}{
		{"Microsoft.WindowsTerminal_8wekyb3d8bbwe", true},
		{"MicrosoftWindows.Client.WebExperience_cw5n1h2txyewy", true},
		{"SpotifyAB.SpotifyMusic_zpdnekdrzrea0", false},
	} {
		t.Run(tt.family, func(t *testing.T) {
			statusPackageFamilyName = func(int32) (string, error) { return tt.family, nil }
			a := workspaceApp{Pid: 900001, Path: filepath.Join(t.TempDir(), "app.exe"), Name: "Example", Regular: true}
			if statusAppExcluded(a) != tt.excluded || canQuit(a.Path, []workspaceApp{a}) == tt.excluded || (len(quitMatches(a.Path, []workspaceApp{a})) == 0) != tt.excluded {
				t.Fatal("incorrect package quit eligibility")
			}
			rows := mergeApps(nil, []procReading{{Pid: a.Pid, Path: a.Path, Footprint: 20, Start: 1}}, []workspaceApp{a}, nativeResponsiblePid, time.Second,
				func(procReading, map[int32]procReading, func(int32) int32) (string, string) {
					return "app:" + a.Path, a.Path
				})
			if len(rows) != 1 || rows[0].Quit == tt.excluded {
				t.Fatalf("package must remain listed with correct Quit value: %+v", rows)
			}
		})
	}
}

// TestWindowsRunningBuiltinPackageProtection probes identities without closing any real app.
func TestWindowsRunningBuiltinPackageProtection(t *testing.T) {
	procs, err := processSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range procs {
		family, err := processPackageFamilyName(int32(p.Pid))
		if err != nil || family == "" {
			continue
		}
		publisher := windowsPackageFamilyPublisher(family)
		if publisher != "8wekyb3d8bbwe" && publisher != "cw5n1h2txyewy" {
			continue
		}
		t.Logf("built-in process PID=%d family=%s exe=%s", p.Pid, family, p.Exe)
		if !statusAppExcluded(workspaceApp{Pid: int32(p.Pid), Path: p.Exe}) {
			t.Fatal("running Microsoft package allowed to quit")
		}
		return
	}
	t.Skip("no accessible Microsoft-published packaged process is running in the runner's session")
}
