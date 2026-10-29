//go:build !darwin

package main

// The menu bar readings are macOS only (menubar spec §二): elsewhere none
// shows, and the Cleanzo icon keeps its first name.

func menuBarSupported() bool    { return false }
func menuBarHasBattery() bool   { return false }
func menuBarShow([]string, int) {}
func menuBarLayout() int        { return 0 }
func menuBarRelabel()           {}

func menuBarBatteryTemp() *BatteryStatus { return nil }
