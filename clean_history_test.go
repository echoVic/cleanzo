package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestCleanRecordKeepsWhatWasRemoved checks that a record holds the rows
// that gave something back, with the name of a row that is no catalog rule.
func TestCleanRecordKeepsWhatWasRemoved(t *testing.T) {
	at := time.Unix(1_800_000_000, 0)
	results := []CleanResult{
		{Name: "dev.npm", Freed: 3000},
		{Name: "review.installers", Trashed: 500},
		{Name: "sys.logs", Skipped: "running"},
		{Name: "app:com.example.notes", Freed: 200},
	}
	rec, ok := newCleanRecord("clean", results, map[string]string{"app:com.example.notes": "Notes"}, at)
	want := CleanRecord{At: at.Unix(), Source: "clean", Freed: 3200, Trashed: 500, Rows: []CleanRecordRow{
		{ID: "dev.npm", Freed: 3000}, {ID: "review.installers", Trashed: 500}, {ID: "app:com.example.notes", Name: "Notes", Freed: 200},
	}}
	if !ok || rec.At != want.At || rec.Source != want.Source || rec.Freed != want.Freed || rec.Trashed != want.Trashed || !slices.Equal(rec.Rows, want.Rows) {
		t.Fatalf("record = %+v, %v", rec, ok)
	}
	if _, ok := newCleanRecord("clean", []CleanResult{{Name: "sys.logs", Skipped: "running"}}, nil, at); ok {
		t.Fatal("a cleanup that removed nothing made a record")
	}
}

// TestCleanHistoryKeepsTheNewest checks the two bounds of the record: its
// age and its length.
func TestCleanHistoryKeepsTheNewest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Cleanzo", "cleanups.json")
	day := 24 * time.Hour
	start := time.Unix(1_800_000_000, 0)
	add := func(at time.Time) {
		t.Helper()
		if err := appendCleanRecord(path, CleanRecord{At: at.Unix(), Source: "clean", Freed: 1, Rows: []CleanRecordRow{{ID: "dev.npm", Freed: 1}}}, at); err != nil {
			t.Fatal(err)
		}
	}
	add(start)
	add(start.Add(10 * day))
	if got := loadCleanHistory(path); len(got) != 2 || got[0].At != start.Unix() {
		t.Fatalf("history = %+v", got)
	}
	// A record older than a year goes when the next one is added.
	add(start.Add(cleanHistoryKeep + day))
	if got := loadCleanHistory(path); len(got) != 2 || got[0].At != start.Add(10*day).Unix() {
		t.Fatalf("history after a year = %+v", got)
	}
	last := start.Add(cleanHistoryKeep + day)
	for i := 1; i <= cleanHistoryMax+5; i++ {
		add(last.Add(time.Duration(i) * time.Minute))
	}
	got := loadCleanHistory(path)
	if len(got) != cleanHistoryMax || got[len(got)-1].At != last.Add(time.Duration(cleanHistoryMax+5)*time.Minute).Unix() {
		t.Fatalf("history holds %d records, newest %d", len(got), got[len(got)-1].At)
	}
}

// TestCleanHistoryStartsOverAfterAFileItCannotRead checks that a file that
// is no record reads as an empty one and is replaced by the next cleanup.
func TestCleanHistoryStartsOverAfterAFileItCannotRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cleanups.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadCleanHistory(path); len(got) != 0 {
		t.Fatalf("history = %+v", got)
	}
	at := time.Unix(1_800_000_000, 0)
	if err := appendCleanRecord(path, CleanRecord{At: at.Unix(), Freed: 1}, at); err != nil {
		t.Fatal(err)
	}
	if got := loadCleanHistory(path); len(got) != 1 {
		t.Fatalf("history = %+v", got)
	}
}
