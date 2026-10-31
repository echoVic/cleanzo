//go:build !darwin

package main

// The menu bar readings are macOS only (menubar spec §二): elsewhere none
// shows.

func menuBarSupported() bool  { return false }
func menuBarHasBattery() bool { return false }
func menuBarShow([]string)    {}
func menuBarShowing() bool    { return false }
func menuBarRelabel()         {}

func menuBarBatteryTemp() *BatteryStatus { return nil }

func menuBarGrowthChanged() {}
