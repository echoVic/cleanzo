package main

import (
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// recycleSettings reads per-volume policy; tests can isolate shell checks from the user's settings.
var recycleSettings = readRecycleSettings

// readRecycleSettings treats a missing capacity as unknown, while honouring an independent nuke setting.
func readRecycleSettings(root *uint16) (nuke, capacityMiB uint32, known bool) {
	var volume [128]uint16
	if windows.GetVolumeNameForVolumeMountPoint(root, &volume[0], uint32(len(volume))) != nil {
		return 0, 0, false
	}
	name := windows.UTF16ToString(volume[:])
	if !strings.HasPrefix(name, `\\?\Volume{`) || !strings.HasSuffix(name, `}\`) {
		return 0, 0, false
	}
	guid := strings.TrimSuffix(strings.TrimPrefix(name, `\\?\Volume`), `\`)
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\`+guid, registry.QUERY_VALUE)
	if err != nil {
		return 0, 0, false
	}
	defer key.Close()
	if value, kind, err := key.GetIntegerValue("NukeOnDelete"); err == nil && kind == registry.DWORD && value == 1 {
		return 1, 0, true
	}
	if value, kind, err := key.GetIntegerValue("MaxCapacity"); err == nil && kind == registry.DWORD {
		return 0, uint32(value), true
	}
	return 0, 0, false
}
