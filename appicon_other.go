//go:build !darwin

package main

// nativeAppIcon: app bundles and their icons are macOS's; elsewhere the
// pages keep the letter.
func nativeAppIcon(string, int) []byte { return nil }
