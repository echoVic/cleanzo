package main

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Constants from <sys/attr.h>.
const (
	attrBitMapCount       = 5          // ATTR_BIT_MAP_COUNT
	attrCmnReturnedAttrs  = 0x80000000 // ATTR_CMN_RETURNED_ATTRS
	attrCmnextCloneID     = 0x00000100 // ATTR_CMNEXT_CLONEID
	attrCmnextCloneRefcnt = 0x00001000 // ATTR_CMNEXT_CLONE_REFCNT
	fsoptNoFollow         = 0x00000001 // FSOPT_NOFOLLOW
	fsoptPackInvalAttrs   = 0x00000008 // FSOPT_PACK_INVAL_ATTRS
	fsoptAttrCmnExtended  = 0x00000020 // FSOPT_ATTR_CMN_EXTENDED
)

// attrList mirrors struct attrlist. With FSOPT_ATTR_CMN_EXTENDED its last
// field carries the ATTR_CMNEXT_* bits.
type attrList struct {
	bitmapCount, reserved                   uint16
	common, volume, directory, file, cmnExt uint32
}

// cloneReply is what getattrlist writes for the attributes cloneRefs asks
// for, in the order macOS packs them: the length, the attributes it
// returned, the clone id and the clone count.
type cloneReply struct {
	length   uint32
	returned [5]uint32 // attribute_set_t; the last word holds the ATTR_CMNEXT_* bits
	cloneID  uint64
	refs     uint32
}

var (
	getattrlistOnce sync.Once
	getattrlistFn   func(path string, list *attrList, buf unsafe.Pointer, size uintptr, options uint32) int32
)

// cloneRefs reads how many files share a file's blocks as APFS clones (1
// for a file that shares none) and the id those files have in common. ok is
// false when macOS or the volume does not say, as on a volume that is not
// APFS.
func cloneRefs(path string) (id uint64, refs uint32, ok bool) {
	getattrlistOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		if _, err := purego.Dlsym(lib, "getattrlist"); err == nil {
			purego.RegisterLibFunc(&getattrlistFn, lib, "getattrlist")
		}
	})
	if getattrlistFn == nil {
		return 0, 0, false
	}
	const wanted = attrCmnextCloneID | attrCmnextCloneRefcnt
	list := attrList{bitmapCount: attrBitMapCount, common: attrCmnReturnedAttrs, cmnExt: wanted}
	var reply cloneReply
	if getattrlistFn(path, &list, unsafe.Pointer(&reply), unsafe.Sizeof(reply), fsoptNoFollow|fsoptPackInvalAttrs|fsoptAttrCmnExtended) != 0 {
		return 0, 0, false
	}
	if reply.returned[4]&wanted != wanted {
		return 0, 0, false
	}
	return reply.cloneID, reply.refs, true
}
