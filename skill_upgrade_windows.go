package main

import (
	"context"

	"golang.org/x/sys/windows"
)

func skillArchiveGuard() *usageGuard { return skillGuard() }

func swapSkillFolder(ctx context.Context, folder, src string, validate func() error) error {
	return swapWindowsSkillFolderValidated(ctx, folder, src, validate)
}

func publishSkillRestore(staged, folder string) error {
	src, err := windows.UTF16PtrFromString(staged)
	if err != nil {
		return err
	}
	dest, err := windows.UTF16PtrFromString(folder)
	if err != nil {
		return err
	}
	return windows.MoveFile(src, dest)
}
