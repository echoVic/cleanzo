package main

// The growth monitor (docs/superpowers/specs/2026-10-24-growth-monitor-design.md)
// records once a day how much every app's data folders take, keeps 90 days
// of records, and tells the user when an app grows fast.
//
// This file holds the record types and decides what gets measured. The other
// files cover the rest:
//   - growth_measure.go measures;
//   - growth_store.go keeps the files;
//   - growth_eval.go compares records;
//   - growth_monitor.go schedules;
//   - growth_app.go runs the menu bar side.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// growthRootKind tells where an entry lives.
type growthRootKind string

const (
	growthHome   growthRootKind = "home"   // a folder of ~/Library
	growthDot    growthRootKind = "dot"    // a dot folder of the home folder
	growthSystem growthRootKind = "system" // a folder of /Library: measured, never removed
)

// growthRoot is a folder whose first-level items are measured one by one.
type growthRoot struct {
	path     string
	kind     growthRootKind
	needsFDA bool // Containers and Group Containers: left out without Full Disk Access
}

// growthTarget is one entry to measure.
type growthTarget struct {
	path string
	kind growthRootKind
}

// growthEntry is one measured entry of a record.
type growthEntry struct {
	Path     string           `json:"path"`
	Kind     growthRootKind   `json:"kind"`
	Size     int64            `json:"size"`               // bytes on disk; a lower bound when !Complete
	Complete bool             `json:"complete"`           // measured to the end within its time limit
	Denied   bool             `json:"denied"`             // some folders inside could not be read
	Children map[string]int64 `json:"children,omitempty"` // first-level children, for big entries (growth_measure.go)
}

// growthSnapshot is one record: what one run measured.
type growthSnapshot struct {
	At      int64         `json:"at"`  // unix seconds when the run started
	FDA     bool          `json:"fda"` // Full Disk Access during the run
	Cut     bool          `json:"cut"` // the run ended at its time limit before every entry started
	Entries []growthEntry `json:"entries"`
}

// growthSkipDot are dot folders never measured: the Trash holds what the
// user deleted, and the others hold keys.
var growthSkipDot = map[string]bool{".trash": true, ".ssh": true, ".gnupg": true}

// growthVendors are folders named after a vendor rather than an app. Each of
// their children is an entry of its own, so Google/Chrome counts for Chrome.
var growthVendors = map[string]bool{"google": true, "microsoft": true, "jetbrains": true, "adobe": true, "bravesoftware": true}

// growthTargets lists what one run measures (spec §三):
//   - the first-level items of every root, with vendor folders one level deeper;
//   - dot folders only from the home folder.
//
// Links are never followed, and Cleanzo's own folder is left out. Roots that
// need Full Disk Access are skipped without it, so measuring never makes
// macOS ask about other apps' data.
func growthTargets(roots []growthRoot, fda bool, self string) []growthTarget {
	skip := func(e fs.DirEntry) bool {
		return e.Type()&fs.ModeSymlink != 0 || e.Name() == ".DS_Store" || e.Name() == ".localized"
	}
	var out []growthTarget
	for _, r := range roots {
		if r.needsFDA && !fda {
			continue
		}
		entries, err := os.ReadDir(r.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			p := filepath.Join(r.path, name)
			if skip(e) || canonical(p) == canonical(self) {
				continue
			}
			if r.kind == growthDot {
				if e.IsDir() && strings.HasPrefix(name, ".") && !growthSkipDot[strings.ToLower(name)] {
					out = append(out, growthTarget{path: p, kind: r.kind})
				}
				continue
			}
			if e.IsDir() && growthVendors[strings.ToLower(name)] {
				children, err := os.ReadDir(p)
				if err != nil {
					continue
				}
				for _, c := range children {
					if !skip(c) {
						out = append(out, growthTarget{path: filepath.Join(p, c.Name()), kind: r.kind})
					}
				}
				continue
			}
			out = append(out, growthTarget{path: p, kind: r.kind})
		}
	}
	return out
}
