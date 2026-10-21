package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestJunkTrashReceivesContext keeps a cancelled cleanup from entering a context-free recycle call.
func TestJunkTrashReceivesContext(t *testing.T) {
	env := testEnv(t)
	noOpenFiles(t)
	path := filepath.Join(env.home, "Downloads", "keep.bin")
	writeFile(t, path, 123)
	env.rules = []Rule{{ID: "fixture", Group: groupSystem, Targets: []Target{{Pattern: "Downloads", Mode: entriesOf}}, Trash: true}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	saved := trashWithContext
	defer func() { trashWithContext = saved }()
	calls := 0
	wantInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	trashWithContext = func(got context.Context, target string) error {
		calls++
		gotInfo, err := os.Stat(target)
		if got != ctx || err != nil || !os.SameFile(wantInfo, gotInfo) {
			t.Fatalf("cleanup context/path lost: ctx=%v path=%q", got, target)
		}
		cancel()
		return got.Err()
	}
	results := cleanJunk(ctx, env, []JunkSelection{{ID: "fixture"}}, time.Time{}, cleanHooks{})
	if calls != 1 || len(results) != 1 || results[0].Trashed != 0 || results[0].Files != 0 || !exists(path) {
		t.Fatalf("cancelled move: calls=%d results=%+v exists=%v", calls, results, exists(path))
	}
}

// TestAITrashReceivesContext passes a selected AI part's cleanup context into the platform move.
func TestAITrashReceivesContext(t *testing.T) {
	env := testEnv(t)
	path := filepath.Join(env.home, ".codex", "cache", "keep.bin")
	writeFile(t, path, 123)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	saved := trashWithContext
	defer func() { trashWithContext = saved }()
	calls := 0
	trashWithContext = func(got context.Context, target string) error {
		calls++
		if got != ctx || target != filepath.Dir(path) {
			t.Fatalf("AI cleanup context/path lost: ctx=%v path=%q", got, target)
		}
		cancel()
		return got.Err()
	}
	result := cleanAIPart(AISelection{Tool: "codex", Part: "codex.cache"}, map[string]bool{}, env.guard, ctx)
	if calls != 1 || result.Moved != 0 || result.Freed != 0 || len(result.Failed) != 1 || !exists(path) {
		t.Fatalf("cancelled AI move: calls=%d result=%+v exists=%v", calls, result, exists(path))
	}
}
