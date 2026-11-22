package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// A numbered read-only descriptor stays readable across an atomic directory
// exchange. Writable handles, cwd, mapped executables and unknown access
// still block an upgrade. Cleanup and Git upgrades use the stricter probe.
func parseSkillArchiveOpenFiles(out []byte) []string {
	var blocking strings.Builder
	var numbered, readOnly bool
	for line := range strings.SplitSeq(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			numbered, readOnly = false, false
		case 'f':
			numbered = len(line) > 1 && line[1] >= '0' && line[1] <= '9'
			readOnly = false
		case 'a':
			readOnly = line == "ar"
		case 'n':
			if !numbered || !readOnly {
				blocking.WriteString(line)
				blocking.WriteByte('\n')
			}
		}
	}
	return parseLsofNames([]byte(blocking.String()))
}

func skillArchiveGuard() *usageGuard {
	if openFilesHook != nil {
		return skillGuard()
	}
	out, err := lsofFields("fan")
	if err != nil {
		return &usageGuard{failed: true}
	}
	return &usageGuard{open: parseSkillArchiveOpenFiles(out)}
}

func swapSkillFolder(ctx context.Context, folder, src string, validate func() error) error {
	original, err := os.Lstat(folder)
	if err != nil || !original.IsDir() {
		return errSkillChangedDuringUpgrade
	}
	staging, err := os.MkdirTemp(filepath.Dir(folder), ".cleanzo-skill-update-")
	if err != nil {
		return err
	}
	// Keep the skill's basename so the old copy is recognizable in the Trash.
	backup := filepath.Join(staging, filepath.Base(folder))
	removeStaging := true
	defer func() {
		if removeStaging {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := copyTree(src, backup); err != nil {
		return err
	}
	replacement, err := os.Lstat(backup)
	if err != nil {
		return err
	}
	if validate != nil {
		if err := validate(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !sameSkillDirectory(folder, original) {
		return errSkillChangedDuringUpgrade
	}
	if err := unix.RenamexNp(folder, backup, unix.RENAME_SWAP); err != nil {
		return fmt.Errorf("atomic skill exchange: %w", err)
	}
	if err := trashItem(backup); err != nil {
		// A process may have written to the newly published tree. Keep both
		// copies when rolling back, and never exchange an unrelated directory.
		removeStaging = false
		if !sameSkillDirectory(folder, replacement) || !sameSkillDirectory(backup, original) {
			return errors.Join(err, fmt.Errorf("skill paths changed before rollback (staging location: %s)", staging))
		}
		if rollbackErr := unix.RenamexNp(backup, folder, unix.RENAME_SWAP); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("restoring the previous skill from %s: %w", backup, rollbackErr))
		}
		return errors.Join(err, fmt.Errorf("previous skill restored; displaced version kept at %s", backup))
	}
	return nil
}

func sameSkillDirectory(path string, expected os.FileInfo) bool {
	actual, err := os.Lstat(path)
	return err == nil && actual.IsDir() && os.SameFile(actual, expected)
}

func publishSkillRestore(staged, folder string) error {
	return unix.RenamexNp(staged, folder, unix.RENAME_EXCL)
}
