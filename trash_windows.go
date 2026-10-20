package main

// recycle refuses Trash moves until the Windows Recycle Bin safety checks exist.
func recycle(path string) error { return errUnsupported }

// init closes every Trash entry point until recycle is implemented.
func init() { trashItem = recycle }
