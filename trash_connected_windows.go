package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// errConnectedFiles refuses a shell move that could also remove an unselected sibling.
var errConnectedFiles = errors.New("connected-files")

// connectedFileSuffixes is deliberately minimal: English connector forms only, case-insensitive.
// No speculative translations are treated as Windows connector names.
var connectedFileSuffixes = []string{"_files", ".files", "-files", " files"}

// htmlLike reports extensions used by Windows saved-page connected files.
func htmlLike(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".htm", ".html", ".shtml", ".shtm", ".xhtml", ".mht", ".mhtml":
		return true
	}
	return false
}

// checkConnectedFiles refuses either half of a saved page and fails closed on unreadable metadata.
func checkConnectedFiles(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	name := info.Name()
	base := ""
	pairedName := false
	if info.IsDir() {
		for _, suffix := range connectedFileSuffixes {
			if strings.HasSuffix(strings.ToLower(name), suffix) {
				base = name[:len(name)-len(suffix)]
				pairedName = true
				break
			}
		}
	} else if info.Mode().IsRegular() && htmlLike(name) {
		base = strings.TrimSuffix(name, filepath.Ext(name))
		pairedName = true
	}
	if !pairedName {
		return nil
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		match := false
		if info.IsDir() {
			match = htmlLike(entry.Name()) && strings.EqualFold(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), base)
		} else {
			for _, suffix := range connectedFileSuffixes {
				if strings.EqualFold(entry.Name(), base+suffix) {
					match = true
					break
				}
			}
		}
		if !match {
			continue
		}
		// Stat resolves a sibling link too: uncertainty must never permit a coupled shell move.
		sibling, err := os.Stat(filepath.Join(filepath.Dir(path), entry.Name()))
		if err != nil {
			return err
		}
		if (info.IsDir() && sibling.Mode().IsRegular()) || (!info.IsDir() && sibling.IsDir()) {
			return errConnectedFiles
		}
	}
	return nil
}
