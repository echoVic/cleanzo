package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// TestWindowsSkillsUnsupportedLinks keeps the old copy even when the runner itself can make links.
func TestWindowsSkillsUnsupportedLinks(t *testing.T) {
	env := testEnv(t)
	old := filepath.Join(env.home, ".agents", "skills", "fixture")
	writeFile(t, filepath.Join(old, "SKILL.md"), 123)
	if err := (Cleaner{}).LinkSkillCopy(old, "anything"); !errors.Is(err, errUnsupported) {
		t.Fatalf("LinkSkillCopy: %v", err)
	}
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "SKILL.md"), 456)
	// Inject the privilege error rather than assuming the elevated runner lacks privileges.
	saved := skillSymlink
	skillSymlink = func(string, string) error { return &os.LinkError{Op: "symlink", Err: windows.ERROR_PRIVILEGE_NOT_HELD} }
	t.Cleanup(func() { skillSymlink = saved })
	if err := makeSkillSymlink("target", filepath.Join(src, "link")); !errors.Is(err, errUnsupported) {
		t.Fatalf("privilege mapping: %v", err)
	}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "repo/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "repo/link", Typeflag: tar.TypeSymlink, Linkname: "SKILL.md"}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := untarSafe(bytes.NewReader(archive.Bytes()), filepath.Join(src, "extract")); !errors.Is(err, errUnsupported) {
		t.Fatalf("archive must fail before swapping old folder: %v", err)
	}
	if info, err := os.Stat(filepath.Join(old, "SKILL.md")); err != nil || info.Size() != 123 {
		t.Fatal("old copy changed")
	}
}

// TestWindowsSkillRecordedModes survives permission loss during extraction and record reload.
func TestWindowsSkillRecordedModes(t *testing.T) {
	env := testEnv(t)
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "repo/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "repo/run.sh", Typeflag: tar.TypeReg, Mode: 0o755, Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("run\n")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	modes := map[string]string{}
	root, err := untarSafeModes(bytes.NewReader(archive.Bytes()), filepath.Join(env.home, "extract"), modes)
	if err != nil {
		t.Fatal(err)
	}
	if modes["run.sh"] != "100755" {
		t.Fatalf("tar mode lost: %v", modes)
	}
	want, err := gitTreeHash(root, modes)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveSkillRecord(resolvePath(root), skillRecord{Modes: modes}); err != nil {
		t.Fatal(err)
	}
	got, err := gitTreeHash(root)
	if err != nil || got != want {
		t.Fatalf("mode record lost: got %s want %s err %v", got, want, err)
	}
}
