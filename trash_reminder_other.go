//go:build !darwin

package main

func showTrashReminder(_ trashReminderSnapshot, closed func()) { closed() }

func markTrashReminderQuitting() {}

func resetTrashReminderQuitting() {}
