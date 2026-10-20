package main

import (
	"path/filepath"
	"testing"
)

// TestRecordedGitModesHash hashes identical Windows contents with their recorded executable mode.
func TestRecordedGitModesHash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "run.sh"), 10)
	regular, _, err := gitTreeWithModes(dir, dir, map[string]string{"run.sh": "100644"}, true)
	if err != nil {
		t.Fatal(err)
	}
	executable, _, err := gitTreeWithModes(dir, dir, map[string]string{"run.sh": "100755"}, true)
	if err != nil || executable == regular {
		t.Fatalf("recorded mode lost: %s %s %v", regular, executable, err)
	}
	defaultMode, _, err := gitTreeWithModes(dir, dir, nil, true)
	if err != nil || defaultMode != regular {
		t.Fatal("Windows unrecorded file used native permission bits")
	}
}
