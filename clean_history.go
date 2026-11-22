package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// CleanRecord is one cleanup the user ran: when it ended, where it was
// started, and what each row gave back.
type CleanRecord struct {
	At      int64            `json:"at"`      // unix seconds
	Source  string           `json:"source"`  // "clean": the junk page; "low-space": the status page's low disk space notice; "trash-reminder": the Trash reminder
	Freed   int64            `json:"freed"`   // bytes deleted
	Trashed int64            `json:"trashed"` // bytes moved to the Trash
	Rows    []CleanRecordRow `json:"rows"`
}

// CleanRecordRow is a row of a cleanup that removed something.
type CleanRecordRow struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"` // the name shown then, for a row that is no catalog rule
	Freed   int64  `json:"freed"`
	Trashed int64  `json:"trashed"`
}

// The record of cleanups holds a year, and no more than cleanHistoryMax
// cleanups.
const (
	cleanHistoryKeep = 365 * 24 * time.Hour
	cleanHistoryMax  = 300
)

// cleanHistoryFile is the record as it is stored, oldest cleanup first.
type cleanHistoryFile struct {
	Version int           `json:"version"`
	Records []CleanRecord `json:"records"`
}

// cleanHistoryMu serializes reading the record, changing it and writing it.
var cleanHistoryMu sync.Mutex

// cleanHistoryPath is where the record is kept, next to config.json. It
// never leaves this computer.
func cleanHistoryPath() string { return filepath.Join(filepath.Dir(configPath()), "cleanups.json") }

// loadCleanHistory reads the record, oldest cleanup first. A file that is
// missing or is no record reads as an empty one.
func loadCleanHistory(path string) []CleanRecord {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f cleanHistoryFile
	if json.Unmarshal(data, &f) != nil || f.Version != 1 {
		return nil
	}
	return f.Records
}

// appendCleanRecord adds a cleanup to the record and drops what is older
// than cleanHistoryKeep at now, then the oldest beyond cleanHistoryMax.
func appendCleanRecord(path string, rec CleanRecord, now time.Time) error {
	cleanHistoryMu.Lock()
	defer cleanHistoryMu.Unlock()
	oldest := now.Add(-cleanHistoryKeep).Unix()
	records := slices.DeleteFunc(append(loadCleanHistory(path), rec), func(r CleanRecord) bool { return r.At < oldest })
	if len(records) > cleanHistoryMax {
		records = records[len(records)-cleanHistoryMax:]
	}
	data, err := json.Marshal(cleanHistoryFile{Version: 1, Records: records})
	if err != nil {
		return err
	}
	return writeGrowthFile(path, data)
}

// newCleanRecord is the record of a cleanup that ended at the given time:
// the rows that gave something back, each row that is no catalog rule with
// the name it showed. ok is false when nothing was removed.
func newCleanRecord(source string, results []CleanResult, names map[string]string, at time.Time) (rec CleanRecord, ok bool) {
	rec = CleanRecord{At: at.Unix(), Source: source}
	for _, res := range results {
		if res.Freed <= 0 && res.Trashed <= 0 {
			continue
		}
		rec.Rows = append(rec.Rows, CleanRecordRow{ID: res.Name, Name: names[res.Name], Freed: res.Freed, Trashed: res.Trashed})
		rec.Freed += res.Freed
		rec.Trashed += res.Trashed
	}
	return rec, len(rec.Rows) > 0
}

// cleanJunkRecorded cleans as cleanJunk does and adds what it removed to the
// record, also when the cleanup was stopped half way. A record that cannot
// be written does not fail the cleanup.
func cleanJunkRecorded(ctx context.Context, env junkEnv, selections []JunkSelection, scannedAt time.Time, hooks cleanHooks, source string) []CleanResult {
	names := map[string]string{}
	named := hooks.named
	hooks.named = func(id, name string) {
		names[id] = name
		if named != nil {
			named(id, name)
		}
	}
	results := cleanJunk(ctx, env, selections, scannedAt, hooks)
	recordCleanup(source, results, names)
	return results
}

// recordCleanup adds a cleanup that just ended to the record, when it
// removed anything.
func recordCleanup(source string, results []CleanResult, names map[string]string) {
	at := now()
	if rec, ok := newCleanRecord(source, results, names, at); ok {
		_ = appendCleanRecord(cleanHistoryPath(), rec, at)
	}
}

// CleanHistory returns the cleanups on record, newest first.
func (Cleaner) CleanHistory() []CleanRecord {
	cleanHistoryMu.Lock()
	defer cleanHistoryMu.Unlock()
	records := slices.Clone(loadCleanHistory(cleanHistoryPath()))
	slices.Reverse(records)
	if records == nil {
		records = []CleanRecord{}
	}
	return records
}

// ForgetCleanHistory empties the record.
func (Cleaner) ForgetCleanHistory() error {
	cleanHistoryMu.Lock()
	defer cleanHistoryMu.Unlock()
	if err := os.Remove(cleanHistoryPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
