package main

import "context"

// growthRoots is empty: the first version of the growth monitor is macOS only.
func growthRoots(home, system string) []growthRoot { return nil }

// growthSupported tells whether this platform has the growth monitor.
func growthSupported() bool { return false }

// growthBattery is never consulted on Windows.
func growthBattery(context.Context) (bool, int) { return false, 100 }
