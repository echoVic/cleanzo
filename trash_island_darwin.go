//go:build darwin

package main

import (
	"time"

	"github.com/egoist/mygo"
)

type islandShape int

const (
	islandRest    islandShape = iota // the notch itself: nothing shows
	islandCompact                    // a strip to both sides of the notch
	islandOpen                       // the strip and a body under it
)

const (
	islandWing       = 62  // what the compact shape adds to each side of the notch
	islandOpenWidth  = 390 // the open shape, a point wider over a notch of odd width
	islandPanelWidth = 480 // the window: the widest shape and a spring going past it
	islandPanelBelow = 160 // what the window has under the notch: the tallest body, and the spring

	islandOpenDelay  = 80 * time.Millisecond  // at the notch's own size before it grows
	islandLinger     = 10 * time.Second       // open and untouched, until it folds
	islandLeaveDelay = 900 * time.Millisecond // the pointer gone, until it folds
	islandArmDelay   = time.Second            // out by itself, until it takes the click that empties
	islandDoneStay   = 4 * time.Second        // a result with nothing to add, until it goes
	islandRetract    = 500 * time.Millisecond // going back into the notch, before its window goes
)

// trashIsland shows the Trash reminder at the notch: it comes out of the
// notch open, folds to a strip beside the notch when left alone, and opens
// again under the pointer. It holds no timer of its own: step says when it
// next has something to do.
type trashIsland struct {
	p     *trashReminderPopup
	notch islandNotch
	now   func() time.Time
	// gone is called once, when the island is back in the notch for good.
	gone func()

	shape   islandShape
	hover   bool   // the pointer is on it
	leaving bool   // going back into the notch, whatever the pointer does
	ended   bool   // gone was called
	seen    string // the phase it last showed
	dirty   bool   // changed between steps
	// armedShown is whether the last step showed the button that empties as
	// one that takes a click.
	armedShown bool

	// When each change is due; zero when it is not.
	openAt, foldAt, leaveAt, goneAt time.Time
	armedAt                         time.Time
}

func newTrashIsland(p *trashReminderPopup, n islandNotch, now func() time.Time) *trashIsland {
	return &trashIsland{p: p, notch: n, now: now}
}

// appear starts the island at the notch's own size, from which it grows.
func (s *trashIsland) appear() {
	now := s.now()
	s.shape, s.hover, s.leaving, s.ended, s.dirty = islandRest, false, false, false, true
	s.seen, s.armedShown = s.p.phase, false
	s.openAt = now.Add(islandOpenDelay)
	s.foldAt = s.openAt.Add(islandLinger)
	s.armedAt = s.openAt.Add(islandArmDelay)
	s.leaveAt, s.goneAt = time.Time{}, time.Time{}
}

// leave sends the island back into the notch, for good.
func (s *trashIsland) leave() {
	if s.leaving || s.ended {
		return
	}
	s.shape, s.leaving, s.dirty = islandRest, true, true
	s.openAt, s.foldAt, s.leaveAt = time.Time{}, time.Time{}, time.Time{}
	s.goneAt = s.now().Add(islandRetract)
}

// armed reports whether the button that empties the Trash takes a click: an
// island that came out by itself may have landed under a click meant for
// what it covers.
func (s *trashIsland) armed() bool { return !s.now().Before(s.armedAt) }

// unwatched reports whether nobody reads the question now: folded, the
// pointer elsewhere. Only then may a new measurement replace its numbers.
func (s *trashIsland) unwatched() bool {
	return s.p.phase == "" && s.shape == islandCompact && !s.hover && !s.leaving && !s.ended
}

// step moves the island on at now, with the pointer on it or not. It returns
// when it next has something to do, zero for never, and whether what it
// shows changed.
func (s *trashIsland) step(now time.Time, inside bool) (next time.Time, changed bool) {
	if s.ended {
		return time.Time{}, false
	}
	changed, s.dirty = s.dirty, false
	before := s.shape
	due := func(at time.Time) bool { return !at.IsZero() && !now.Before(at) }
	if s.p.phase != s.seen {
		s.seen, changed = s.p.phase, true
		switch {
		case s.leaving:
		case s.p.phase == "done":
			s.shape, s.foldAt, s.leaveAt = islandOpen, time.Time{}, now.Add(islandDoneStay)
		case s.p.phase == "partial" || s.p.phase == "cancelled":
			s.shape, s.foldAt = islandOpen, now.Add(islandLinger)
		default:
			s.foldAt = time.Time{} // from here the pointer leaving folds it
		}
	}
	if due(s.openAt) {
		s.openAt = time.Time{}
		if s.shape == islandRest && !s.leaving {
			s.shape = islandOpen
		}
	}
	s.hover = inside
	switch {
	case s.leaving:
	case inside:
		// The pointer opens the strip and keeps the open shape.
		if s.shape == islandCompact {
			s.shape = islandOpen
		}
		s.foldAt = time.Time{}
	case s.shape == islandOpen && s.p.phase != "done":
		if s.foldAt.IsZero() {
			s.foldAt = now.Add(islandLeaveDelay)
		} else if due(s.foldAt) {
			s.shape, s.foldAt = islandCompact, time.Time{}
		}
	}
	if due(s.leaveAt) {
		if inside && !s.leaving {
			s.leaveAt = now.Add(time.Second) // a result stays while the pointer is on it
		} else {
			s.leave()
		}
	}
	if due(s.goneAt) {
		s.goneAt, s.ended = time.Time{}, true
		if s.gone != nil {
			s.gone()
		}
		return time.Time{}, true
	}
	armed := !now.Before(s.armedAt)
	changed = changed || s.dirty || s.shape != before || armed != s.armedShown
	s.dirty, s.armedShown = false, armed
	for _, at := range []time.Time{s.openAt, s.foldAt, s.leaveAt, s.goneAt} {
		if !at.IsZero() && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	// The button that empties is drawn again as it starts taking clicks.
	if !armed && s.p.phase == "" && !s.leaving && (next.IsZero() || s.armedAt.Before(next)) {
		next = s.armedAt
	}
	return next, changed
}

// size is the size the island goes to in its shape and phase.
func (s *trashIsland) size() (w, h int) {
	odd := s.notch.Width % 2
	switch s.shape {
	case islandCompact:
		return s.notch.Width + 2*islandWing, s.notch.Height
	case islandOpen:
		return islandOpenWidth + odd, s.notch.Height + trashIslandBody(s.p.phase)
	}
	return s.notch.Width, s.notch.Height
}

// trashIslandBody is the height of what the open shape shows under the
// notch in a phase.
func trashIslandBody(phase string) int {
	switch phase {
	case "cleaning", "stopping":
		return 88
	case "done":
		return 60
	case "partial", "cancelled":
		return 124
	}
	return 104
}

// frame is the island's window: as wide as the widest shape and a spring
// going past it, centred on the notch to the point, which takes a width of
// the notch's parity.
func (s *trashIsland) frame() mygo.Rectangle {
	w := islandPanelWidth + s.notch.Width%2
	return mygo.Rectangle{X: s.notch.X + (s.notch.Width-w)/2, Y: s.notch.Y, Width: w, Height: s.notch.Height + islandPanelBelow}
}

// region is where the island's window takes the mouse, from its top-left
// corner: the shape the island goes to. At the notch's own size it takes
// none.
func (s *trashIsland) region() (mygo.Rectangle, bool) {
	if s.shape == islandRest || s.ended {
		return mygo.Rectangle{}, false
	}
	w, h := s.size()
	return mygo.Rectangle{X: (s.frame().Width - w) / 2, Width: w, Height: h}, true
}

// contains reports whether a point of the screen is on the island.
func (s *trashIsland) contains(x, y int) bool {
	r, ok := s.region()
	if !ok {
		return false
	}
	f := s.frame()
	x, y = x-f.X-r.X, y-f.Y-r.Y
	return x >= 0 && x < r.Width && y >= 0 && y < r.Height
}
