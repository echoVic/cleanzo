package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// crashpadFixture stages Codex's two Crashpad databases and Chrome's in a
// temporary home, each with settings.dat and queued reports.
func crashpadFixture(t *testing.T, env junkEnv) (codexWeb, codex, chrome string) {
	t.Helper()
	support := filepath.Join(env.home, "Library/Application Support")
	codexWeb = filepath.Join(support, "com.openai.codex/web/Crashpad")
	codex = filepath.Join(support, "Codex/Crashpad")
	chrome = filepath.Join(support, "Google/Chrome/Crashpad")
	for _, db := range []string{codexWeb, codex, chrome} {
		writeFile(t, filepath.Join(db, "settings.dat"), 40)
		for _, q := range crashpadQueues {
			mustMkdir(t, filepath.Join(db, q))
		}
	}
	for i := 0; i < 5; i++ {
		writeFile(t, filepath.Join(codexWeb, "pending", "r"+string(rune('a'+i))+".dmp"), 10000)
		writeFile(t, filepath.Join(codexWeb, "pending", "r"+string(rune('a'+i))+"_sidecar.json"), 1000)
	}
	writeFile(t, filepath.Join(codexWeb, "attachments/abc/log.txt"), 2000)
	writeFile(t, filepath.Join(codex, "completed/old.dmp"), 5000)
	writeFile(t, filepath.Join(chrome, "pending/c.dmp"), 6000)
	return
}

func crashItem(t *testing.T, items []JunkItem) JunkItem {
	t.Helper()
	for _, it := range items {
		if it.ID == crashRuleID {
			return it
		}
	}
	t.Fatalf("no %s item in %d items", crashRuleID, len(items))
	return JunkItem{}
}

// TestCrashDumpsListEachAppAndLeaveSettings checks what the scan offers:
// one row per database, named after the app, sized without settings.dat.
func TestCrashDumpsListEachAppAndLeaveSettings(t *testing.T) {
	env := testEnv(t)
	codexWeb, codex, chrome := crashpadFixture(t, env)
	items, _ := scanJunk(context.Background(), env, nil, nil)
	it := crashItem(t, items)
	if it.Group != "system" || !it.Default {
		t.Fatalf("item = %+v", it)
	}
	if it.Size != 55000+2000+5000+6000 || it.Files != 13 {
		t.Fatalf("size %d files %d", it.Size, it.Files)
	}
	if len(it.Subs) != 3 {
		t.Fatalf("subs = %+v", it.Subs)
	}
	top := it.Subs[0]
	if top.Name != "Codex" || top.Size != 57000 || top.Files != 11 || resolvePath(top.Path) != resolvePath(codexWeb) {
		t.Fatalf("largest = %+v", top)
	}
	names := map[string]string{}
	for _, s := range it.Subs {
		names[resolvePath(s.Path)] = s.Name
	}
	if names[resolvePath(codex)] != "Codex" || names[resolvePath(chrome)] != "Chrome" {
		t.Fatalf("names = %v", names)
	}
	// The generic Application Support cache scan must not list them again.
	for _, other := range items {
		if other.ID != crashRuleID && strings.Contains(strings.Join(other.Paths, " "), "Crashpad") {
			t.Fatalf("%s also lists Crashpad: %v", other.ID, other.Paths)
		}
	}
}

// TestCrashDumpsEmptyQueuesKeepFolders checks cleaning: reports go, the
// queue folders and settings.dat stay, nothing goes to the Trash.
func TestCrashDumpsEmptyQueuesKeepFolders(t *testing.T) {
	env := testEnv(t)
	codexWeb, codex, chrome := crashpadFixture(t, env)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: crashRuleID, Paths: []string{codexWeb, codex}}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Freed != 62000 || res[0].Trashed != 0 || res[0].Files != 12 || len(res[0].Failed) != 0 {
		t.Fatalf("result = %+v", res)
	}
	for _, db := range []string{codexWeb, codex} {
		if !exists(filepath.Join(db, "settings.dat")) {
			t.Fatalf("settings.dat removed in %s", db)
		}
		for _, q := range crashpadQueues {
			entries, err := os.ReadDir(filepath.Join(db, q))
			if err != nil || len(entries) != 0 {
				t.Fatalf("%s/%s: %v %v", db, q, entries, err)
			}
		}
	}
	if !exists(filepath.Join(chrome, "pending/c.dmp")) {
		t.Fatal("unselected database was cleaned")
	}
	if entries, _ := os.ReadDir(filepath.Join(env.home, ".Trash")); len(entries) != 0 {
		t.Fatalf("crash dumps went to the Trash: %v", entries)
	}
}

// TestCrashDumpsSkipOpenDumps leaves a dump a process has open, and still
// cleans the rest of the queue while the app runs.
func TestCrashDumpsSkipOpenDumps(t *testing.T) {
	env := testEnv(t)
	codexWeb, _, _ := crashpadFixture(t, env)
	open := resolvePath(filepath.Join(codexWeb, "pending/ra.dmp"))
	env.guard = &usageGuard{open: []string{strings.ToLower(open)}, running: map[string]bool{"com.openai.codex": true}}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: crashRuleID, Paths: []string{codexWeb}}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Skipped != "" || res[0].InUse != 1 || res[0].Freed != 47000 {
		t.Fatalf("result = %+v", res)
	}
	if !exists(open) || exists(filepath.Join(codexWeb, "pending/rb.dmp")) {
		t.Fatal("open dump removed or closed dump kept")
	}
}

// TestCrashDumpsRefuseFoldersOutsideTheRule keeps a selection inside the
// Crashpad databases the scan found.
func TestCrashDumpsRefuseFoldersOutsideTheRule(t *testing.T) {
	env := testEnv(t)
	crashpadFixture(t, env)
	other := filepath.Join(env.home, "Library/Application Support/Notes/data")
	writeFile(t, filepath.Join(other, "note.txt"), 100)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: crashRuleID, Paths: []string{other, filepath.Dir(other)}}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Freed != 0 || !exists(filepath.Join(other, "note.txt")) {
		t.Fatalf("result = %+v", res)
	}
}

// TestCrashDumpsHiddenWhenEmpty: databases with nothing queued add no row.
func TestCrashDumpsHiddenWhenEmpty(t *testing.T) {
	env := testEnv(t)
	db := filepath.Join(env.home, "Library/Application Support/Codex/Crashpad")
	writeFile(t, filepath.Join(db, "settings.dat"), 40)
	for _, q := range crashpadQueues {
		mustMkdir(t, filepath.Join(db, q))
	}
	items, _ := scanJunk(context.Background(), env, nil, nil)
	for _, it := range items {
		if it.ID == crashRuleID && (it.Size > 0 || len(it.Subs) > 0) {
			t.Fatalf("empty database listed: %+v", it)
		}
	}
}

// TestCrashDumpsSkipTinyDatabases: a few leftover sidecars are not a row.
func TestCrashDumpsSkipTinyDatabases(t *testing.T) {
	env := testEnv(t)
	db := filepath.Join(env.home, "Library/Application Support/Codex/Crashpad")
	writeFile(t, filepath.Join(db, "settings.dat"), 40)
	writeFile(t, filepath.Join(db, "pending/a_sidecar.json"), 24)
	writeFile(t, filepath.Join(env.home, "Library/Application Support/Slack/Crashpad/pending/x.dmp"), 50000)
	items, _ := scanJunk(context.Background(), env, nil, nil)
	it := crashItem(t, items)
	if len(it.Subs) != 1 || it.Subs[0].Name != "Slack" || it.Size != 50000 {
		t.Fatalf("item = %+v", it)
	}
	for _, d := range it.Details {
		if strings.Contains(d.Path, "Codex") {
			t.Fatalf("tiny database still in details: %+v", d)
		}
	}
}

// TestEmptyQueueManyFiles checks batched removal of a queue larger than one
// read batch.
func TestEmptyQueueManyFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pending")
	mustMkdir(t, dir)
	n := crashpadBatch + 37
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(dir, "d"+itoa(i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, files, inUse, err := emptyQueue(context.Background(), dir, &usageGuard{running: map[string]bool{}}, nil)
	if err != nil || inUse != 0 || files != int64(n) {
		t.Fatalf("err %v inUse %d files %d of %d", err, inUse, files, n)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("%d entries left", len(entries))
	}
	if !exists(dir) {
		t.Fatal("queue folder removed")
	}
}

// TestCrashDumpsSummary checks the quick du-based total the status page uses.
func TestCrashDumpsSummary(t *testing.T) {
	env := testEnv(t)
	codexWeb, _, _ := crashpadFixture(t, env)
	writeFile(t, filepath.Join(codexWeb, "pending/big.dmp"), 64*1024)
	sum, err := crashDumps(context.Background(), env.home, buildAppIndex(nil))
	if err != nil || sum.Threshold != crashThreshold || len(sum.Dirs) == 0 {
		t.Fatalf("summary %+v err %v", sum, err)
	}
	if sum.Dirs[0].App != "Codex" || sum.Dirs[0].Size < 64*1024 || sum.Total < sum.Dirs[0].Size {
		t.Fatalf("summary %+v", sum)
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{digits[i%10]}, b...)
	}
	return string(b)
}
