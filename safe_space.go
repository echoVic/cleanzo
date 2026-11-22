package main

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/egoist/mygo"
)

// SafeSpace is what the rows a scan selects by itself would give back at
// once: the status page offers it when the disk runs low.
type SafeSpace struct {
	Bytes int64          `json:"bytes"`
	Rows  []SafeSpaceRow `json:"rows"` // largest first
	At    int64          `json:"at"`   // unix milliseconds: when the rows were measured
}

// SafeSpaceRow is a row of the measure.
type SafeSpaceRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"` // the name to show, for a row that is no catalog rule
	Size     int64  `json:"size"`
	Recovery string `json:"recovery"` // what brings the contents back, as JunkItem.Recovery
}

// measureSafeSpace measures the rows a scan selects by itself and deletes
// outright, and only those: it looks into no folder of the other rules, so
// it reads neither Downloads, Desktop and Documents nor the Trash. A row
// whose app is running is left out, as cleaning would skip it.
func measureSafeSpace(ctx context.Context, env junkEnv) SafeSpace {
	space := SafeSpace{Rows: []SafeSpaceRow{}, At: time.Now().UnixMilli()}
	env.ctx, env.observe, env.safeOnly = ctx, true, true
	specs, _ := expandItems(&env)
	for _, it := range measureItems(ctx, specs, env, nil, nil) {
		if it.Size <= 0 || it.Running || it.Denied || it.Problem != "" {
			continue
		}
		space.Rows = append(space.Rows, SafeSpaceRow{ID: it.ID, Name: it.Name, Size: it.Size, Recovery: it.Recovery})
		space.Bytes += it.Size
	}
	slices.SortStableFunc(space.Rows, func(a, b SafeSpaceRow) int { return cmp.Compare(b.Size, a.Size) })
	return space
}

// cleanSafeSpace deletes rows of the measure. Whatever else it is handed is
// no row to it: the rows are looked for as the measure looked for them.
func cleanSafeSpace(ctx context.Context, env junkEnv, ids []string, measuredAt time.Time, hooks cleanHooks) []CleanResult {
	env.safeOnly = true
	selections := make([]JunkSelection, len(ids))
	for i, id := range ids {
		selections[i] = JunkSelection{ID: id}
	}
	return cleanJunkRecorded(ctx, env, selections, measuredAt, hooks, "low-space")
}

// SafeSpace measures what cleaning the rows a scan selects by itself would
// free now. Nothing is deleted.
func (Cleaner) SafeSpace(ctx context.Context) (SafeSpace, error) {
	space := measureSafeSpace(ctx, currentJunkEnv())
	return space, ctx.Err()
}

// CleanSafeSpace deletes rows SafeSpace measured, streaming as CleanJunk
// does.
func (Cleaner) CleanSafeSpace(ctx context.Context, ids []string, measuredAt int64, ch *mygo.Channel[JunkCleanEvent]) error {
	defer userScan()()
	env := currentJunkEnv()
	env.refreshGuard = probeUsage
	var last time.Time
	var cleaned int64
	every := func(event JunkCleanEvent) {
		if time.Since(last) >= progressInterval {
			last = time.Now()
			_ = junkCleanSend(ch, event)
		}
	}
	cleanSafeSpace(ctx, env, ids, time.UnixMilli(measuredAt), cleanHooks{
		started:  func(id string) { _ = junkCleanSend(ch, JunkCleanEvent{Kind: "start", ID: id}) },
		checking: func(id, path string) { every(JunkCleanEvent{Kind: "checking", ID: id, Path: path}) },
		removed: func(id, path string, bytes int64) {
			cleaned = bytes
			every(JunkCleanEvent{Kind: "removed", ID: id, Path: path, Bytes: bytes})
		},
		finished: func(res CleanResult) {
			_ = junkCleanSend(ch, JunkCleanEvent{Kind: "done", ID: res.Name, Bytes: cleaned, Result: &res})
		},
	})
	return ctx.Err()
}
