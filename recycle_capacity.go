package main

// recycleFits decides whether a volume's Recycle Bin settings permit
// recycling an item of size bytes.
func recycleFits(size int64, nuke, capacityMiB uint32) bool {
	return nuke != 1 && size >= 0 && size < int64(capacityMiB)*(1<<20)
}
