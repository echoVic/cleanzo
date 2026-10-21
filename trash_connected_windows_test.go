package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConnectedFilesGuard refuses either half of a saved page, including mixed-case names.
func TestConnectedFilesGuard(t *testing.T) {
	for _, ext := range []string{".htm", ".html", ".shtml", ".shtm", ".xhtml", ".mht", ".mhtml", ".HTML"} {
		for _, suffix := range []string{"_files", ".files", "-files", " files", "_FiLeS"} {
			t.Run(ext+suffix, func(t *testing.T) {
				dir := t.TempDir()
				page := filepath.Join(dir, "Page"+ext)
				folder := filepath.Join(dir, "pAGE"+suffix)
				if err := os.Mkdir(folder, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(page, []byte("page"), 0644); err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{page, folder} {
					if err := checkConnectedFiles(path); err == nil || err.Error() != "connected-files" {
						t.Errorf("%s: got %v, want connected-files", path, err)
					}
					if _, err := os.Stat(path); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

// TestConnectedFilesUnpaired allows unrelated names, wrong types and unpaired saved pages.
func TestConnectedFilesUnpaired(t *testing.T) {
	for _, tc := range []struct {
		name, sibling   string
		dir, siblingDir bool
	}{
		{"page.htm", "page_files", false, false},
		{"page_files", "page.htm", true, true},
		{"page.txt", "page_files", false, true},
		{"page.htm", "other_files", false, true},
		{"page_files", "other.htm", true, false},
		{"page.htm", "", false, false},
		{"page_files", "", true, false},
		{"page.htm", "page_files_extra", false, true},
	} {
		t.Run(tc.name+tc.sibling, func(t *testing.T) {
			root := t.TempDir()
			create := func(name string, dir bool) {
				if name == "" {
					return
				}
				var err error
				if dir {
					err = os.Mkdir(filepath.Join(root, name), 0755)
				} else {
					err = os.WriteFile(filepath.Join(root, name), nil, 0644)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			create(tc.name, tc.dir)
			create(tc.sibling, tc.siblingDir)
			if err := checkConnectedFiles(filepath.Join(root, tc.name)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestConnectedFilesUnknown refuses a path whose metadata cannot be checked.
func TestConnectedFilesUnknown(t *testing.T) {
	if err := checkConnectedFiles(filepath.Join(t.TempDir(), "missing.htm")); err == nil {
		t.Fatal("unchecked path allowed")
	}
}

// TestConnectedFilesEmptyBase keeps even saved pages with an empty filename stem paired safely.
func TestConnectedFilesEmptyBase(t *testing.T) {
	root := t.TempDir()
	page, folder := filepath.Join(root, ".htm"), filepath.Join(root, "_files")
	if err := os.WriteFile(page, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(folder, 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{page, folder} {
		if err := checkConnectedFiles(path); err == nil || err.Error() != "connected-files" {
			t.Errorf("%s: %v", path, err)
		}
	}
}
