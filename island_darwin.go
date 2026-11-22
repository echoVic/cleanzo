//go:build darwin

package main

import (
	"math"

	"github.com/ebitengine/purego/objc"
)

// islandNotch is the notch of a display, in DIPs of the screen coordinates
// mygo's windows use: the origin is the top-left corner of the primary
// display.
type islandNotch struct {
	X, Y          int // its top-left corner
	Width, Height int
}

type nsEdgeInsets struct{ Top, Left, Bottom, Right float64 }

// islandNotchOf is the notch of a screen AppKit describes: its frame, how
// far its safe area is from its top, and the widths of the strips free to
// the left and right of the notch. primaryHeight is the height of the
// primary screen, from whose bottom AppKit counts.
func islandNotchOf(primaryHeight float64, frame nsRect, top, left, right float64) (islandNotch, bool) {
	width := frame.W - left - right
	if top <= 0 || left <= 0 || right <= 0 || width <= 0 {
		return islandNotch{}, false
	}
	return islandNotch{
		X:      int(math.Round(frame.X + left)),
		Y:      int(math.Round(primaryHeight - frame.Y - frame.H)),
		Width:  int(math.Round(width)),
		Height: int(math.Round(top)),
	}, true
}

// readIslandNotch finds the display with a notch, on the main thread: the
// screen whose safe area leaves room at its top, with a strip free to each
// side of what takes that room.
func readIslandNotch() (n islandNotch, ok bool) {
	runOnMain(func() {
		if !appKitInit() {
			return
		}
		withPool(func() {
			screens := objc.ID(objc.GetClass("NSScreen")).Send(nsSel("screens"))
			count := int(objc.Send[uint64](screens, nsSel("count")))
			if count == 0 {
				return
			}
			// The first screen is the primary one, whatever shows the menu bar.
			primary := objc.Send[nsRect](screens.Send(nsSel("objectAtIndex:"), 0), nsSel("frame"))
			for i := 0; i < count && !ok; i++ {
				s := screens.Send(nsSel("objectAtIndex:"), i)
				n, ok = islandNotchOf(primary.H, objc.Send[nsRect](s, nsSel("frame")),
					objc.Send[nsEdgeInsets](s, nsSel("safeAreaInsets")).Top,
					objc.Send[nsRect](s, nsSel("auxiliaryTopLeftArea")).W,
					objc.Send[nsRect](s, nsSel("auxiliaryTopRightArea")).W)
			}
		})
	})
	return n, ok
}
