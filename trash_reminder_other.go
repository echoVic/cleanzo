//go:build !darwin

package main

func showTrashReminder(_ trashReminderSnapshot, closed func()) { closed() }

func markTrashReminderQuitting() {}

func resetTrashReminderQuitting() {}

// The reminder shows at a notch on macOS alone.

func trashReminderWaiting() bool { return false }

func reviseTrashReminder(trashReminderSnapshot, bool) bool { return false }

func trashNotchPresent() bool { return false }
