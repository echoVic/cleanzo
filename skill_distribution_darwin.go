package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func publishSkillDeployment(staged, destination string) error {
	return unix.RenamexNp(staged, destination, unix.RENAME_EXCL)
}

func publishSkillLinkRepair(staged, destination, previousTarget string) (keepStage bool, err error) {
	if err := validateSkillDeploymentDestination(destination, SkillDeployRequest{Repair: true, PreviousTarget: previousTarget}); err != nil {
		return false, err
	}
	oldLink, err := os.Lstat(destination)
	if err != nil {
		return false, err
	}
	newLink, err := os.Lstat(staged)
	if err != nil {
		return false, err
	}
	if err := unix.RenamexNp(staged, destination, unix.RENAME_SWAP); err != nil {
		return false, err
	}
	old, oldErr := os.Lstat(staged)
	target, targetErr := os.Readlink(staged)
	if oldErr == nil && targetErr == nil && os.SameFile(old, oldLink) && target == previousTarget {
		return false, nil
	}
	current, err := os.Lstat(destination)
	if err == nil && os.SameFile(current, newLink) {
		if rollback := unix.RenamexNp(staged, destination, unix.RENAME_SWAP); rollback == nil {
			return false, errSkillDiverged
		} else {
			return true, errors.Join(errSkillDiverged, fmt.Errorf("could not restore displaced entry kept at %s: %w", filepath.Dir(staged), rollback))
		}
	}
	return true, fmt.Errorf("destination changed during repair; displaced entry is at %s", filepath.Dir(staged))
}
