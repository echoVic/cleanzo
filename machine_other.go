//go:build !darwin

package main

// readMachineInfo: the machine reads are macOS's; the Windows home page
// keeps its own layout.
func readMachineInfo() MachineInfo {
	return MachineInfo{Displays: []DisplayInfo{}}
}
