package main

import (
	"runtime"
	"unsafe"

	"github.com/ebitengine/purego/objc"
)

// nsRect is NSRect, passed by value.
type nsRect struct{ X, Y, W, H float64 }

// nativeAppIcon draws the icon NSWorkspace gives for path into a px × px
// bitmap and encodes it as PNG; nil when it can't. It runs on the calling
// goroutine's thread, not the main thread: NSWorkspace's icons and drawing
// into an offscreen bitmap are safe there, and a cold icon can take a few
// hundred milliseconds that the window must not wait for.
func nativeAppIcon(path string, px int) []byte {
	if !appKitInit() {
		return nil
	}
	// The autorelease pool and the current graphics context belong to the
	// thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var out []byte
	withPool(func() {
		ws := objc.ID(objc.GetClass("NSWorkspace")).Send(nsSel("sharedWorkspace"))
		img := ws.Send(nsSel("iconForFile:"), nsString(path))
		if img == 0 {
			return
		}
		rep := objc.ID(objc.GetClass("NSBitmapImageRep")).Send(nsSel("alloc")).Send(
			nsSel("initWithBitmapDataPlanes:pixelsWide:pixelsHigh:bitsPerSample:samplesPerPixel:hasAlpha:isPlanar:colorSpaceName:bytesPerRow:bitsPerPixel:"),
			uintptr(0), px, px, 8, 4, true, false, nsString("NSDeviceRGBColorSpace"), 0, 0)
		if rep == 0 {
			return
		}
		defer rep.Send(nsSel("release"))
		ctx := objc.ID(objc.GetClass("NSGraphicsContext")).Send(nsSel("graphicsContextWithBitmapImageRep:"), rep)
		if ctx == 0 {
			return
		}
		gc := objc.ID(objc.GetClass("NSGraphicsContext"))
		gc.Send(nsSel("saveGraphicsState"))
		gc.Send(nsSel("setCurrentContext:"), ctx)
		side := float64(px)
		img.Send(nsSel("drawInRect:fromRect:operation:fraction:"),
			nsRect{0, 0, side, side}, nsRect{}, uint64(2) /* NSCompositingOperationSourceOver */, 1.0)
		gc.Send(nsSel("restoreGraphicsState"))
		out = bitmapPNG(rep)
	})
	return out
}

// bitmapPNG encodes rep as PNG; nil when it cannot.
func bitmapPNG(rep objc.ID) []byte {
	dict := objc.ID(objc.GetClass("NSDictionary")).Send(nsSel("dictionary"))
	data := rep.Send(nsSel("representationUsingType:properties:"), uint64(4) /* NSBitmapImageFileTypePNG */, dict)
	if data == 0 {
		return nil
	}
	n := objc.Send[uint64](data, nsSel("length"))
	b := uintptr(data.Send(nsSel("bytes")))
	if n == 0 || b == 0 {
		return nil
	}
	// Taking the address and dereferencing keeps go vet from flagging the
	// uintptr→pointer conversion, as in nsGoString.
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&b))
	return append([]byte(nil), unsafe.Slice((*byte)(ptr), n)...)
}

// nsString makes an autoreleased NSString of s; purego passes s as a C
// string.
func nsString(s string) objc.ID {
	return objc.ID(objc.GetClass("NSString")).Send(nsSel("stringWithUTF8String:"), s)
}
