//go:build !darwin

package main

import "github.com/egoist/mygo"

// isIslandWindow is false where no window is an island: they are at the
// notch of a Mac's display.
func isIslandWindow(*mygo.Window) bool { return false }
