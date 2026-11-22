//go:build darwin

package main

import (
	"context"
	"sync/atomic"
	"time"
)

// trashSurface is what shows a reminder: its window, or its island.
type trashSurface interface {
	// redraw shows the reminder's state again, after it changed.
	redraw()
	// discard takes it off the screen as another surface takes its place.
	discard()
}

// trashIslandSurface is an island showing a reminder.
type trashIslandSurface interface {
	trashSurface
	// moveTo follows the notch to where the displays have it now.
	moveTo(islandNotch)
	// unwatched reports whether nobody reads the question now.
	unwatched() bool
	// leave sends the island back into the notch, which ends the reminder.
	leave()
}

var (
	// What tells where a reminder can show, and what puts it there. Tests
	// replace them.
	islandNotchNow  = readIslandNotch
	voiceOverOn     = readVoiceOver
	openTrashIsland = (*trashReminderRun).openIsland
	openTrashWindow = (*trashReminderRun).openWindow

	// islandUnplaceable is set once an island's window could not be kept at
	// the notch: reminders show in windows from then on.
	islandUnplaceable atomic.Bool
	// trashReminderCurrent is the reminder that shows, which the monitor
	// follows.
	trashReminderCurrent atomic.Pointer[trashReminderRun]
)

// trashReminderRun is one reminder, from the moment it shows until it is
// dismissed: what it says, the cleanup a click starts, and the window or the
// island that shows it. Apart from update, it belongs to the main thread.
type trashReminderRun struct {
	p *trashReminderPopup
	// snap is what a click deletes: the measurement p shows.
	snap   trashReminderSnapshot
	ctx    context.Context
	cancel context.CancelFunc
	alive  atomic.Bool
	closed func()
	// clean deletes what a snapshot holds, telling how much it freed so far.
	clean func(ctx context.Context, s trashReminderSnapshot, progress func(int64)) CleanResult

	surface trashSurface
	island  trashIslandSurface // surface, while it is an island
}

func newTrashReminderRun(s trashReminderSnapshot, closed func()) *trashReminderRun {
	r := &trashReminderRun{snap: s, closed: closed}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.alive.Store(true)
	r.clean = func(ctx context.Context, s trashReminderSnapshot, progress func(int64)) CleanResult {
		return cleanTrashReminder(ctx, trashReminderEnv(true), s, progress)
	}
	r.p = &trashReminderPopup{size: s.Size, text: nativeText, dark: menuBarAppDark, start: r.start, cancel: r.cancel}
	return r
}

// show puts the reminder on screen: at the notch where it can be, in a
// window otherwise.
func (r *trashReminderRun) show() {
	trashReminderCurrent.Store(r)
	if n, ok := trashReminderNotch(); ok {
		if island, ok := openTrashIsland(r, n); ok {
			r.surface, r.island = island, island
			return
		}
		islandUnplaceable.Store(true)
	}
	r.surface = openTrashWindow(r)
}

// trashReminderNotch is the notch a reminder about to show does so at:
// that of a display that has one, unless the user turned the island off,
// VoiceOver is on, which the island cannot be used with, or an island could
// not be placed before.
func trashReminderNotch() (islandNotch, bool) {
	if islandUnplaceable.Load() || !loadTrashReminder(trashReminderDir()).Island || voiceOverOn() {
		return islandNotch{}, false
	}
	return islandNotchNow()
}

// displaysChanged follows a new arrangement of the displays: an island goes
// where the notch is now, and the reminder into a window where there is no
// notch any more, as when the lid closes. It goes on from where it was.
func (r *trashReminderRun) displaysChanged() {
	if r.island == nil || !r.alive.Load() {
		return
	}
	if n, ok := islandNotchNow(); ok {
		r.island.moveTo(n)
		return
	}
	old := r.island
	r.island = nil
	r.surface = openTrashWindow(r)
	old.discard()
}

// start runs the cleanup the user confirmed, of the measurement the
// reminder shows now.
func (r *trashReminderRun) start() {
	snap := r.snap
	go func() {
		if !trashCleanupGate.TryLock() {
			r.update(func() { r.p.finish(CleanResult{Skipped: "busy"}, false) })
			return
		}
		defer trashCleanupGate.Unlock()
		defer userScan()()
		last := time.Time{}
		res := r.clean(r.ctx, snap, func(b int64) {
			if time.Since(last) < 50*time.Millisecond {
				return
			}
			last = time.Now()
			r.update(func() { r.p.freed = b })
		})
		r.update(func() { r.p.finish(res, r.ctx.Err() != nil) })
	}()
}

// update changes the reminder's state from the cleanup's goroutine: fn runs
// on the main thread, and whatever shows the reminder then draws it again.
// Nothing reaches a reminder that ended.
func (r *trashReminderRun) update(fn func()) {
	runOnMain(func() {
		if !r.alive.Load() {
			return
		}
		fn()
		if r.surface != nil {
			r.surface.redraw()
		}
	})
}

// end is the reminder off the screen for good: its cleanup stops, and the
// monitor may remind again.
func (r *trashReminderRun) end() {
	if !r.alive.CompareAndSwap(true, false) {
		return
	}
	r.cancel()
	trashReminderCurrent.CompareAndSwap(r, nil)
	r.closed()
}

// revise takes a newer measurement of the Trash into a reminder that still
// asks its question at the notch, and reports whether it did. A Trash no
// longer over the threshold leaves nothing to ask about. One still over it
// replaces what the reminder shows only while nobody reads it, so that the
// size a user reads and what their click deletes are of one measurement.
func (r *trashReminderRun) revise(s trashReminderSnapshot, over bool) bool {
	if !r.alive.Load() || r.island == nil || r.p.phase != "" {
		return false
	}
	if !over {
		r.island.leave()
		return true
	}
	if !r.island.unwatched() {
		return false
	}
	r.snap, r.p.size = s, s.Size
	r.island.redraw()
	return true
}

// trashReminderWaiting reports whether the reminder that shows still asks
// its question at the notch, where it follows the Trash.
func trashReminderWaiting() bool {
	r := trashReminderCurrent.Load()
	if r == nil {
		return false
	}
	waiting := false
	runOnMain(func() { waiting = r.alive.Load() && r.island != nil && r.p.phase == "" })
	return waiting
}

// reviseTrashReminder offers that reminder a newer measurement.
func reviseTrashReminder(s trashReminderSnapshot, over bool) (took bool) {
	r := trashReminderCurrent.Load()
	if r == nil {
		return false
	}
	runOnMain(func() { took = r.revise(s, over) })
	return took
}

// trashNotchPresent tells the settings page whether a display has a notch
// now.
func trashNotchPresent() bool {
	_, ok := islandNotchNow()
	return ok
}
