//go:build !darwin

package main

// islandCheck is a check of macOS alone.
func islandCheck() bool { return false }
