package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	// Inject the privilege error rather than assuming the elevated runner lacks privileges.
	saved := skillSymlink
	skillSymlink = func(string, string) error { return &os.LinkError{Op: "symlink", Err: windows.ERROR_PRIVILEGE_NOT_HELD} }
	t.Cleanup(func() { skillSymlink = saved })
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
	noOpenFiles(t)
	original, err := gitTreeHash(old)
	if err != nil {
		t.Fatal(err)
	}
	savedHTTP := skillUpdateHTTP
	skillUpdateHTTP = &http.Client{Transport: skillModesTransport(func(r *http.Request) (*http.Response, error) {
		body := archive.Bytes()
		if strings.Contains(r.URL.Path, "/git/trees/") {
			body = []byte(`{"sha":"root","tree":[{"path":"skills/fixture","type":"tree","sha":"replacement"},{"path":"skills/fixture/SKILL.md","type":"blob","mode":"100644"}]}`)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	t.Cleanup(func() { skillUpdateHTTP = savedHTTP })
	origins := map[string]SkillOrigin{resolvePath(old): {Kind: originSkillsCLI, Repo: "acme/skills", Path: "skills/fixture", Tree: original}}
	var result SkillUpgradeResult
	err = upgradeOne(context.Background(), SkillUpgradeRequest{Path: old}, origins, &treeCache{trees: map[string]*githubTree{}}, map[string]skillArchive{}, &result)
	if !errors.Is(err, errUnsupported) {
		t.Fatalf("real upgrade privilege failure: %v", err)
	}
	if exists(filepath.Join(filepath.Dir(old), ".fixture.cleanzo-new")) {
		t.Fatal("failed upgrade leaked a replacement")
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
