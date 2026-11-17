package main

import "io/fs"

// reclaimTally sums what removing the files added to it gives back. Files
// that are APFS clones of each other share their blocks, and those blocks
// are freed only when the last of them goes: they count once, and only when
// every file sharing them was added.
type reclaimTally struct {
	size   int64
	shared map[uint64]*cloneFamily
}

// cloneFamily is the files of one clone id added so far.
type cloneFamily struct {
	seen uint32
	size int64
}

// add counts one regular file.
func (t *reclaimTally) add(path string, info fs.FileInfo) {
	size := onDisk(info)
	if size == 0 {
		return
	}
	id, refs, ok := cloneRefs(path)
	if !ok || refs <= 1 {
		t.size += size
		return
	}
	if t.shared == nil {
		t.shared = map[uint64]*cloneFamily{}
	}
	family := t.shared[id]
	if family == nil {
		family = &cloneFamily{}
		t.shared[id] = family
	}
	family.seen++
	family.size = max(family.size, size)
	if family.seen == refs {
		t.size += family.size
	}
}
