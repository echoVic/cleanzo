package main

// recycleFits decides whether known per-volume settings permit recycling an item.
func recycleFits(size int64, nuke, capacityMiB uint32, known bool) bool {
	if !known {
		return true // The shell's WANTNUKEWARNING remains the safety net.
	}
	return nuke != 1 && size >= 0 && size < int64(capacityMiB)*(1<<20)
}
