package main

import "golang.org/x/sys/windows"

func publishSkillDeployment(staged, destination string) error {
	from, err := windows.UTF16PtrFromString(staged)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFile(from, to)
}

func publishSkillLinkRepair(_, _, _ string) (bool, error) { return false, errUnsupported }
