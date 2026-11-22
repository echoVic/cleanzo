//go:build darwin

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestCleanJunkIsRecorded checks that a cleanup of the junk page ends up in
// the record, newest first, and that forgetting empties it.
func TestCleanJunkIsRecorded(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.home, ".npm/_cacache/x"), 3000)
	writeFile(t, filepath.Join(env.home, "Library/Caches/com.example.notes/blob"), 2000)
	results := cleanJunkRecorded(context.Background(), env, []JunkSelection{{ID: "dev.npm"}, {ID: "app:dir:com.example.notes"}, {ID: "dev.pip"}}, time.Time{}, cleanHooks{}, "clean")
	if len(results) != 3 {
		t.Fatalf("results = %+v", results)
	}
	history := (Cleaner{}).CleanHistory()
	if len(history) != 1 || history[0].Source != "clean" || len(history[0].Rows) != 2 {
		t.Fatalf("history = %+v", history)
	}
	rec := history[0]
	if rec.Freed != results[0].Freed+results[1].Freed || rec.Freed == 0 || rec.Trashed != 0 {
		t.Fatalf("record = %+v, results = %+v", rec, results)
	}
	if rec.Rows[0] != (CleanRecordRow{ID: "dev.npm", Freed: results[0].Freed}) || rec.Rows[1].ID != "app:dir:com.example.notes" || rec.Rows[1].Name != "com.example.notes" {
		t.Fatalf("rows = %+v", rec.Rows)
	}
	// A second cleanup comes first.
	writeFile(t, filepath.Join(env.home, ".npm/_cacache/y"), 1000)
	cleanJunkRecorded(context.Background(), env, []JunkSelection{{ID: "dev.npm"}}, time.Time{}, cleanHooks{}, "low-space")
	if history = (Cleaner{}).CleanHistory(); len(history) != 2 || history[0].Source != "low-space" || history[1].Source != "clean" {
		t.Fatalf("history = %+v", history)
	}
	// A cleanup that removed nothing adds no record.
	cleanJunkRecorded(context.Background(), env, []JunkSelection{{ID: "dev.npm"}}, time.Time{}, cleanHooks{}, "clean")
	if history = (Cleaner{}).CleanHistory(); len(history) != 2 {
		t.Fatalf("history after an empty cleanup = %+v", history)
	}
	if err := (Cleaner{}).ForgetCleanHistory(); err != nil {
		t.Fatal(err)
	}
	if history = (Cleaner{}).CleanHistory(); history == nil || len(history) != 0 {
		t.Fatalf("history after forgetting = %#v", history)
	}
	if err := (Cleaner{}).ForgetCleanHistory(); err != nil {
		t.Fatalf("forgetting an empty record: %v", err)
	}
}
