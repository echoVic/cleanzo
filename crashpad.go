package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Crash dumps: Crashpad report queues of Chromium and Electron apps
// ---------------------------------------------------------------------------
//
// A Crashpad database is a folder an app's crash handler owns:
//
//	Crashpad/settings.dat        client id and upload consent; never touched
//	Crashpad/pending/            reports waiting to be uploaded
//	Crashpad/completed/          reports already handled
//	Crashpad/new/                reports being written
//	Crashpad/attachments/<id>/   files that go with a report
//
// The handler keeps running while the app does and writes into the queue
// folders, so cleaning empties them and leaves the folders in place. A dump
// a process has open right now is left alone.

// crashThreshold is how large an app's crash dumps grow before Cleanzo
// points them out: Codex's desktop app has been seen to add 5 GB a day.
const crashThreshold = 5 << 30

// crashThresholdNow is crashThreshold, or CLEANZO_CRASH_THRESHOLD (bytes)
// when set, so the notice can be checked without gigabytes of dumps.
func crashThresholdNow() int64 {
	if v, err := strconv.ParseInt(os.Getenv("CLEANZO_CRASH_THRESHOLD"), 10, 64); err == nil && v > 0 {
		return v
	}
	return crashThreshold
}

// measureCrashpad sums the report queues of a Crashpad database, leaving
// settings.dat out, so a database with nothing queued measures zero.
func measureCrashpad(db string) (size, files int64, newest time.Time, readErrors int) {
	for _, q := range crashpadQueues {
		dir := filepath.Join(db, q)
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		s, f, n, e := measureChecked(dir)
		size, files, readErrors = size+s, files+f, readErrors+e
		if n.After(newest) {
			newest = n
		}
	}
	return
}

// crashpadMinSize is the least a database must hold to be listed: a few
// leftover sidecar files are not worth a row.
const crashpadMinSize = 4096

// crashpadBatch is how many entries emptyCrashpad reads at a time: a queue
// may hold millions of files, more than is wise to list at once.
const crashpadBatch = 4096

// emptyCrashpad removes what the report queues of db hold, keeping the
// queue folders and anything a process has open. progress is called after
// each batch with the bytes freed so far.
func emptyCrashpad(ctx context.Context, db string, guard *usageGuard, progress func(freed int64)) (freed, files, inUse int64, err error) {
	for _, q := range crashpadQueues {
		dir := filepath.Join(db, q)
		info, lerr := os.Lstat(dir)
		if lerr != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		f, n, u, qerr := emptyQueue(ctx, dir, guard, func(b int64) {
			if progress != nil {
				progress(freed + b)
			}
		})
		freed, files, inUse = freed+f, files+n, inUse+u
		if qerr != nil {
			return freed, files, inUse, qerr
		}
	}
	return freed, files, inUse, ctx.Err()
}

// emptyQueue removes a queue folder's entries in batches. Entries a removal
// skips (in use) are counted once; a pass that removes nothing ends it.
func emptyQueue(ctx context.Context, dir string, guard *usageGuard, progress func(int64)) (freed, files, inUse int64, err error) {
	real := resolvePath(dir)
	kept := map[string]bool{}
	for {
		removed := false
		d, oerr := os.Open(dir)
		if oerr != nil {
			return freed, files, inUse, oerr
		}
		for {
			if ctx.Err() != nil {
				d.Close()
				return freed, files, inUse, ctx.Err()
			}
			entries, rerr := d.ReadDir(crashpadBatch)
			for _, e := range entries {
				name := e.Name()
				if kept[name] {
					continue
				}
				p := filepath.Join(dir, name)
				if guard.inUse(filepath.Join(real, name)) {
					kept[name] = true
					inUse++
					continue
				}
				var size, n int64
				if e.Type()&fs.ModeSymlink != 0 || !e.IsDir() {
					if info, ierr := e.Info(); ierr == nil && info.Mode().IsRegular() {
						size, n = onDisk(info), 1
					}
					if os.Remove(p) != nil {
						kept[name] = true
						continue
					}
				} else {
					size, n, _, _ = measureChecked(p)
					if removeAll(p) != nil {
						kept[name] = true
						continue
					}
				}
				freed, files, removed = freed+size, files+n, true
			}
			if progress != nil && len(entries) > 0 {
				progress(freed)
			}
			if errors.Is(rerr, io.EOF) || len(entries) == 0 {
				break
			}
			if rerr != nil {
				d.Close()
				return freed, files, inUse, rerr
			}
		}
		d.Close()
		// Removing while reading may leave entries the listing skipped.
		if !removed {
			return freed, files, inUse, nil
		}
	}
}

// crashpadOwner names the app a Crashpad database belongs to, from the
// folder it sits in: an installed app's name when it is known.
func crashpadOwner(home, db string, apps *appIndex) string {
	rel := strings.TrimPrefix(resolvePath(db), resolvePath(filepath.Join(home, "Library/Application Support"))+"/")
	folder := strings.SplitN(rel, "/", 2)[0]
	if crashpadCodex[strings.ToLower(folder)] {
		return "Codex"
	}
	if rel == db || folder == "" {
		return filepath.Base(filepath.Dir(db))
	}
	if apps != nil {
		if app, ok := apps.lookup(folder); ok && app.Name != "" {
			return app.Name
		}
	}
	// Chrome keeps it in Google/Chrome/Crashpad: the folder next to it says more.
	if parent := filepath.Base(filepath.Dir(db)); parent != folder {
		return parent
	}
	return folder
}

// crashpadCodex are the Application Support folders of Codex's desktop app,
// which ships inside ChatGPT.app under the bundle id com.openai.codex.
var crashpadCodex = map[string]bool{"codex": true, "com.openai.codex": true}

// crashpadSubs lists each Crashpad database with queued reports, named
// after its app, largest first.
func crashpadSubs(env junkEnv, entries []cleanEntry) []SubItem {
	subs := []SubItem{}
	for _, e := range entries {
		if e.size <= 0 {
			continue
		}
		subs = append(subs, SubItem{Path: e.path, Name: crashpadOwner(env.home, e.path, env.apps), Detail: env.displayable(e.path), Size: e.size, Files: e.files, Default: true})
	}
	sort.SliceStable(subs, func(i, j int) bool { return subs[i].Size > subs[j].Size })
	return subs
}

// CrashDumpDir is one app's Crashpad database with queued reports.
type CrashDumpDir struct {
	App  string `json:"app"`
	Path string `json:"path"` // the database folder, as the junk page selects it
	Size int64  `json:"size"`
}

// CrashDumpSummary is how much the crash dumps of all apps take.
type CrashDumpSummary struct {
	Total     int64          `json:"total"`
	Threshold int64          `json:"threshold"` // a database at least this large is pointed out
	Dirs      []CrashDumpDir `json:"dirs"`      // largest first
}

// CrashDumps measures every app's crash dump queues quickly with du, for the
// status page to point out runaway ones when Cleanzo opens. Nothing is
// listed file by file and nothing is changed.
func (Cleaner) CrashDumps(ctx context.Context) (CrashDumpSummary, error) {
	return crashDumps(ctx, homeDir(), buildAppIndex(appDirs()))
}

// crashDumps is CrashDumps for a given home and app index.
func crashDumps(ctx context.Context, home string, apps *appIndex) (CrashDumpSummary, error) {
	env := junkEnv{home: home, tmp: filepath.Clean(os.TempDir())}
	out := CrashDumpSummary{Threshold: crashThresholdNow(), Dirs: []CrashDumpDir{}}
	for _, db := range crashpadDatabases(env) {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		var size int64
		for _, q := range crashpadQueues {
			dir := filepath.Join(db, q)
			if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
				continue
			}
			if n, err := duSize(ctx, dir, 90*time.Second); err == nil {
				// An empty folder takes one block; that is not a dump.
				if n > 4096 {
					size += n
				}
			}
		}
		if size == 0 {
			continue
		}
		out.Total += size
		out.Dirs = append(out.Dirs, CrashDumpDir{App: crashpadOwner(home, db, apps), Path: resolvePath(db), Size: size})
	}
	sort.SliceStable(out.Dirs, func(i, j int) bool { return out.Dirs[i].Size > out.Dirs[j].Size })
	return out, nil
}

// crashpadDatabases lists the Crashpad folders the crash dump rule covers.
func crashpadDatabases(env junkEnv) []string {
	var dbs []string
	for _, t := range crashpadTargets() {
		for _, p := range env.resolve(t.Pattern) {
			if info, err := os.Lstat(p); err == nil && info.IsDir() && safeToClean(p) {
				dbs = append(dbs, p)
			}
		}
	}
	return dbs
}
