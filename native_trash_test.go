package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestRecycleBinRuleSemantics checks selection, change detection, snapshot failure and repeat safety.
func TestRecycleBinRuleSemantics(t *testing.T) {
	env := testEnv(t)
	env.rules = []Rule{{ID: "sys.trash", Group: groupSystem, RecycleBin: true}}
	savedQuery, savedEmpty := queryNativeBin, emptyNativeBin
	t.Cleanup(func() { queryNativeBin, emptyNativeBin = savedQuery, savedEmpty })
	state := binStats{size: 123, items: 1}
	emptied := 0
	queryNativeBin = func(string) (binStats, error) { return state, nil }
	emptyNativeBin = func(string) error { emptied++; state = binStats{}; return nil }
	scan := func() {
		rows, _ := scanJunk(context.Background(), env, nil, nil)
		if len(rows) != 1 || rows[0].Size != state.size || rows[0].Default {
			t.Fatalf("bin row: %+v", rows)
		}
	}
	clean := func() CleanResult {
		return cleanJunk(context.Background(), env, []JunkSelection{{ID: "sys.trash"}}, time.Time{}, cleanHooks{})[0]
	}
	scan()
	cleanJunk(context.Background(), env, nil, time.Time{}, cleanHooks{})
	if emptied != 0 {
		t.Fatal("unselected bin emptied")
	}
	state.items++
	if r := clean(); r.Skipped != "changed" || emptied != 0 {
		t.Fatalf("changed bin: %+v", r)
	}
	scan()
	env.guard.failed = true
	if r := clean(); r.Freed != 0 || emptied != 0 {
		t.Fatalf("snapshot failure emptied bin: %+v", r)
	}
	env.guard.failed = false
	if r := clean(); r.Freed != 123 || r.Files != 2 || emptied != 1 {
		t.Fatalf("matching bin: %+v", r)
	}
	if r := clean(); r.Skipped != "changed" || emptied != 1 {
		t.Fatalf("repeated clean: %+v", r)
	}
	queryNativeBin = func(string) (binStats, error) { return binStats{}, errors.New("failed") }
	scanJunk(context.Background(), env, nil, nil)
	queryNativeBin = func(string) (binStats, error) { return state, nil }
	if r := clean(); r.Skipped != "changed" || emptied != 1 {
		t.Fatalf("failed scan reused snapshot: %+v", r)
	}
}
