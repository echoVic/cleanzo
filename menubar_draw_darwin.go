package main

// menubar_draw_darwin.go draws a reading's two lines into the image its menu
// bar icon shows, with AppKit, as appicon_darwin.go draws app icons
// (menubar spec §三.1, §九).

import (
	"math"
	"runtime"

	"github.com/ebitengine/purego/objc"
)

// nsSize is NSSize, passed and returned by value.
type nsSize struct{ W, H float64 }

// mygo shows every menu bar image 18 points tall; the readings draw at twice
// that for Retina displays, with a point of room on each side.
const (
	readoutHeight = 18.0
	readoutScale  = 2.0
	readoutPad    = 1.0
)

// The lines' fonts, in points: a small label above a larger value, or two
// middle-sized values for the network. The weights are AppKit's
// NSFontWeightMedium and NSFontWeightSemibold.
const (
	readoutLabelSize = 7.5
	readoutValueSize = 11.5
	readoutDuoSize   = 9.0
	readoutMedium    = 0.23
	readoutSemibold  = 0.3
)

// The lines' baselines, in points above the image's bottom edge.
const (
	readoutLabelBase = 11.4
	readoutValueBase = 1.1
	readoutDuoTop    = 10.0
	readoutDuoBottom = 1.3
)

// readoutLabelAlpha is how much of the ink a label gets, to read lighter than
// its value.
const readoutLabelAlpha = 0.55

// readoutInk says how an icon is colored. A template is black with a lighter
// label, and macOS tints it for the menu bar and its highlight. An icon in
// color is drawn in the menu bar's ink, light on a dark bar and dark on a light
// one, with its value in its tone: the page's --warn and --danger on a light
// bar, --warn-text and --danger-strong of its dark theme on a dark one.
type readoutInk struct {
	template bool
	dark     bool
	tone     string
}

// rgba is a color in sRGB, each part 0–1.
type rgba struct{ r, g, b, a float64 }

func hexRGBA(c uint32) rgba {
	return rgba{float64(c>>16&0xff) / 255, float64(c>>8&0xff) / 255, float64(c&0xff) / 255, 1}
}

// colors returns the label's and the value's colors.
func (k readoutInk) colors() (label, value rgba) {
	if k.template {
		return rgba{0, 0, 0, readoutLabelAlpha}, rgba{0, 0, 0, 1}
	}
	ink := 0.0
	if k.dark {
		ink = 1
	}
	label, value = rgba{ink, ink, ink, readoutLabelAlpha}, rgba{ink, ink, ink, 1}
	switch {
	case k.tone == "warn" && k.dark:
		value = hexRGBA(0xFFC35A)
	case k.tone == "warn":
		value = hexRGBA(0xB45309)
	case k.tone == "crit" && k.dark:
		value = hexRGBA(0xF0666A)
	case k.tone == "crit":
		value = hexRGBA(0xD92D20)
	}
	return label, value
}

// readoutFonts returns the fonts of t's top and bottom lines; 0 when AppKit
// has none.
func readoutFonts(t menuBarText) (top, bottom objc.ID) {
	fonts := objc.ID(objc.GetClass("NSFont"))
	digits := func(size float64) objc.ID {
		return fonts.Send(nsSel("monospacedDigitSystemFontOfSize:weight:"), size, readoutSemibold)
	}
	if !t.Label {
		return digits(readoutDuoSize), digits(readoutDuoSize)
	}
	return fonts.Send(nsSel("systemFontOfSize:weight:"), readoutLabelSize, readoutMedium), digits(readoutValueSize)
}

// textAttributes is the attribute dictionary that draws in font and c, both
// set.
func textAttributes(font objc.ID, c rgba) objc.ID {
	color := objc.ID(objc.GetClass("NSColor")).Send(nsSel("colorWithSRGBRed:green:blue:alpha:"), c.r, c.g, c.b, c.a)
	keys := objc.ID(objc.GetClass("NSMutableArray")).Send(nsSel("array"))
	values := objc.ID(objc.GetClass("NSMutableArray")).Send(nsSel("array"))
	keys.Send(nsSel("addObject:"), nsString("NSFont")) // NSFontAttributeName
	values.Send(nsSel("addObject:"), font)
	keys.Send(nsSel("addObject:"), nsString("NSColor")) // NSForegroundColorAttributeName
	values.Send(nsSel("addObject:"), color)
	return objc.ID(objc.GetClass("NSDictionary")).Send(nsSel("dictionaryWithObjects:forKeys:"), values, keys)
}

// readoutWidth is the width in points an icon needs for t: its wider line and
// the room on each side, rounded up to a whole pixel; 0 when AppKit cannot
// say.
func readoutWidth(t menuBarText) float64 {
	if !appKitInit() {
		return 0
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var w float64
	withPool(func() {
		top, bottom := readoutFonts(t)
		if top == 0 || bottom == 0 {
			return
		}
		for _, line := range []struct {
			s    string
			font objc.ID
		}{{t.Top, top}, {t.Bottom, bottom}} {
			if line.s == "" {
				continue
			}
			size := objc.Send[nsSize](nsString(line.s), nsSel("sizeWithAttributes:"), textAttributes(line.font, rgba{0, 0, 0, 1}))
			w = max(w, size.W)
		}
	})
	if w == 0 {
		return 0
	}
	return math.Ceil((w+2*readoutPad)*readoutScale) / readoutScale
}

// drawReadout draws t into an image w points wide and 18 tall, at twice that
// in pixels, and encodes it as PNG; nil when it cannot. Each line sits on a
// fixed baseline, so a label in another script, or a value without
// descenders, sits where every other does. It locks the calling goroutine to
// its thread for the autorelease pool and the current graphics context.
func drawReadout(t menuBarText, w float64, ink readoutInk) []byte {
	if w <= 0 || !appKitInit() {
		return nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var out []byte
	withPool(func() {
		topFont, bottomFont := readoutFonts(t)
		if topFont == 0 || bottomFont == 0 {
			return
		}
		rep := objc.ID(objc.GetClass("NSBitmapImageRep")).Send(nsSel("alloc")).Send(
			nsSel("initWithBitmapDataPlanes:pixelsWide:pixelsHigh:bitsPerSample:samplesPerPixel:hasAlpha:isPlanar:colorSpaceName:bytesPerRow:bitsPerPixel:"),
			uintptr(0), int(math.Round(w*readoutScale)), int(readoutHeight*readoutScale), 8, 4, true, false, nsString("NSDeviceRGBColorSpace"), 0, 0)
		if rep == 0 {
			return
		}
		defer rep.Send(nsSel("release"))
		rep.Send(nsSel("setSize:"), nsSize{w, readoutHeight}) // draw in points
		ctx := objc.ID(objc.GetClass("NSGraphicsContext")).Send(nsSel("graphicsContextWithBitmapImageRep:"), rep)
		if ctx == 0 {
			return
		}
		gc := objc.ID(objc.GetClass("NSGraphicsContext"))
		gc.Send(nsSel("saveGraphicsState"))
		gc.Send(nsSel("setCurrentContext:"), ctx)
		label, value := ink.colors()
		topColor, topBase, bottomBase := value, readoutDuoTop, readoutDuoBottom
		if t.Label {
			topColor, topBase, bottomBase = label, readoutLabelBase, readoutValueBase
		}
		line := func(s string, font objc.ID, c rgba, base float64) {
			if s == "" {
				return
			}
			// Without NSStringDrawingUsesLineFragmentOrigin, the rectangle's
			// origin is the line's baseline; no drawing context is needed.
			nsString(s).Send(nsSel("drawWithRect:options:attributes:context:"),
				nsRect{readoutPad, base, w, readoutHeight}, uint64(0), textAttributes(font, c), objc.ID(0))
		}
		line(t.Top, topFont, topColor, topBase)
		line(t.Bottom, bottomFont, value, bottomBase)
		gc.Send(nsSel("restoreGraphicsState"))
		out = bitmapPNG(rep)
	})
	return out
}
