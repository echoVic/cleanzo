package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestUninstallEjectsOnlySelectedInstaller(t *testing.T) {
	for _, name := range []string{"unchecked", "checked", "stale-preview"} {
		t.Run(name, func(t *testing.T) {
			eject := name == "checked"
			stale := name == "stale-preview"
			home := t.TempDir()
			setHome(t, home)
			stubUsage(t, &usageGuard{running: map[string]bool{}})
			resetUninstallListed()
			t.Cleanup(resetUninstallListed)
			app := makeFakeApp(t, filepath.Join(home, "Applications"), "TestApp")
			source := t.TempDir()
			makeFakeApp(t, source, "TestApp")
			image := filepath.Join(t.TempDir(), "installer.dmg")
			mount := filepath.Join(t.TempDir(), "TestApp Installer")
			if out, err := exec.Command("/usr/bin/hdiutil", "create", "-quiet", "-fs", "HFS+", "-srcfolder", source, "-format", "UDZO", image).CombinedOutput(); err != nil {
				t.Skipf("cannot create test image: %v %s", err, out)
			}
			if out, err := exec.Command("/usr/bin/hdiutil", "attach", "-quiet", "-nobrowse", "-mountpoint", mount, image).CombinedOutput(); err != nil {
				t.Skipf("cannot mount test image: %v %s", err, out)
			}
			t.Cleanup(func() { _ = exec.Command("/usr/bin/hdiutil", "detach", "-quiet", mount).Run() })
			detail := Cleaner{}.AppUninstallDetail(app)
			if canonical(detail.InstallerMount) != canonical(mount) {
				t.Fatalf("installer mount = %q, want %q", detail.InstallerMount, mount)
			}
			installerID := ""
			if eject {
				installerID = detail.InstallerID
			} else if stale {
				installerID = "stale-preview"
			}
			res := Cleaner{}.UninstallApp(app, false, installerID)
			if res.Moved != 1 || len(res.Failed) != 0 || res.InstallerEjected != eject || res.InstallerEjectFailed != stale {
				t.Fatalf("uninstall = %+v", res)
			}
			if bundlePresent := exists(filepath.Join(mount, "TestApp.app")); bundlePresent == eject || !exists(image) {
				t.Fatalf("installer bundle still mounted = %v; source image exists = %v", bundlePresent, exists(image))
			}
		})
	}
}
