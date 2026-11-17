package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// keepFixtures stage what a rule's Keep reads, and the entry it keeps beside
// the staged one; that entry must not count.
var keepFixtures = map[string]func(t *testing.T, home string){
	"review.node-versions": func(t *testing.T, home string) {
		stageNvm(t, home, map[string]string{"default": "v1.2.3"}, "v1.2.3")
	},
	"review.rust-toolchains": func(t *testing.T, home string) {
		stageRustup(t, home, rustupSettings, "stable-aarch64-apple-darwin")
	},
}

// TestRuleCatalogMeasuresEachTarget checks every non-command rule with staged paths.
func TestRuleCatalogMeasuresEachTarget(t *testing.T) {
	for _, rule := range junkRules {
		if rule.Command != nil {
			continue
		}
		t.Run(rule.ID, func(t *testing.T) {
			env := testEnv(t)
			if rule.Keep != nil {
				stage := keepFixtures[rule.ID]
				if stage == nil {
					t.Fatalf("%s has a Keep hook and no fixture for what it reads", rule.ID)
				}
				stage(t, env.home)
			}
			unit := int64(100)
			if rule.Contents {
				unit = crashpadMinSize // smaller databases are not listed
			}
			for _, target := range rule.Targets {
				root := filepath.Join(env.home, strings.ReplaceAll(target.Pattern, "*", "Fixture"))
				if target.Pattern == "$TMPDIR" {
					root = env.tmp
				}
				path := root
				if target.Mode == entriesOf || target.Mode == recursiveFiles {
					name := "entry.bin"
					if len(target.Names) > 0 {
						name = strings.ReplaceAll(target.Names[0], "*", "Fixture")
					}
					path = filepath.Join(root, name)
				} else {
					path = filepath.Join(root, "entry.bin")
				}
				if rule.Contents {
					// Crashpad rules count only what the report queues hold.
					path = filepath.Join(root, "pending", "entry.bin")
				}
				writeFile(t, path, int(unit))
			}
			saved := now
			now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
			defer func() { now = saved }()
			specs, _ := expandItems(&env)
			for _, spec := range specs {
				if spec.item.ID == rule.ID {
					plan := planItem(context.Background(), spec, env, make(chan struct{}, 4), nil)
					if plan.item.Size != int64(len(rule.Targets))*unit {
						t.Fatalf("size = %d, want %d", plan.item.Size, int64(len(rule.Targets))*unit)
					}
					return
				}
			}
			t.Fatalf("rule %s missing, got %v", rule.ID, itemIDs(specs))
		})
	}
}

// TestAppRulesOfferOnlyLogsAndCaches: each of the four apps' rules offers
// its logs, its sandbox's package caches and its web caches, never the
// messages, resources or staged update beside them; the generic row keeps the
// app's other cache folders; a running app's row is listed and cleans nothing.
func TestAppRulesOfferOnlyLogsAndCaches(t *testing.T) {
	shared := []string{"sdk_storage/log/main.log", "sandbox_runtime/.cache/node/pkg", "sandbox_runtime/.cache/python/pkg", "aha/users/u1/profile_explorer/Service Worker/CacheStorage/blob"}
	sharedOffered := []string{"sdk_storage/log/main.log", "sandbox_runtime/.cache/node", "sandbox_runtime/.cache/python", "aha/users/u1/profile_explorer/Service Worker/CacheStorage"}
	for _, app := range []struct {
		id, folder, owner string
		staged, offered   []string // relative to the app's folder
	}{
		{"app.lark", "LarkShell", "com.electron.lark", shared, sharedOffered},
		{"app.doubao-work", "DoubaoWork", "com.work.pc.doubao", shared, sharedOffered},
		{"app.doubao", "Doubao", "com.bot.pc.doubao", shared, sharedOffered},
		{"app.aime", "Aime", "com.bytedance.aime.electron", []string{"Partitions/p1/Service Worker/CacheStorage/blob"}, []string{"Partitions/p1/Service Worker/CacheStorage"}},
	} {
		t.Run(app.id, func(t *testing.T) {
			env := testEnv(t)
			base := filepath.Join(env.home, "Library/Application Support", app.folder)
			for _, rel := range app.staged {
				writeFile(t, filepath.Join(base, rel), 100)
			}
			for _, rel := range []string{"messages.db", "resources/sticker/a.png", "update/7.40/app.bin", "sdk_storage/messages.db", "sandbox_runtime/bases/x",
				"aha/users/u1/profile_explorer/Service Worker/ScriptCache/x", "Partitions/p1/Service Worker/ScriptCache/x", "Partitions/p1/IndexedDB/x"} {
				writeFile(t, filepath.Join(base, rel), 1000)
			}
			writeFile(t, filepath.Join(base, "Code Cache/js/x"), 100)
			specs, _ := expandItems(&env)
			want := []string{"app:dir:" + strings.ToLower(app.folder) + " Code Cache/js"}
			for _, rel := range app.offered {
				want = append(want, app.id+" "+rel)
			}
			slices.Sort(want)
			if got := offeredIn(specs, base); !slices.Equal(got, want) {
				t.Fatalf("offered = %q\nwant %q", got, want)
			}
			spec := catalogRow(t, &env, app.id)
			if spec.item.Group != string(groupApps) || spec.item.Default || spec.item.Trash || spec.item.Running {
				t.Fatalf("item = %+v", spec.item)
			}
			if plan := planItem(context.Background(), *spec, env, make(chan struct{}, 4), nil); plan.item.Size != int64(100*len(app.staged)) {
				t.Fatalf("size = %d", plan.item.Size)
			}

			env.guard.running[app.owner] = true
			spec = catalogRow(t, &env, app.id)
			if plan := planItem(context.Background(), *spec, env, make(chan struct{}, 4), nil); !plan.item.Running || len(plan.entries) > 0 {
				t.Fatalf("running app: item = %+v, entries = %v", plan.item, plan.entries)
			}
			res := cleanJunk(context.Background(), env, []JunkSelection{{ID: app.id}}, time.Time{}, cleanHooks{})
			if len(res) != 1 || res[0].Skipped != "running" || !exists(filepath.Join(base, app.staged[0])) {
				t.Fatalf("running app cleaned: %+v", res)
			}
		})
	}
}

// TestCleanJunkTrashFirstAndScannedAt checks recoverability and scan cutoffs.
func TestCleanJunkTrashFirstAndScannedAt(t *testing.T) {
	env := testEnv(t)
	old := filepath.Join(env.home, ".Trash/old.bin")
	fresh := filepath.Join(env.home, ".Trash/fresh.bin")
	writeFile(t, old, 100)
	writeFile(t, fresh, 200)
	scanTime := time.Now().Add(time.Hour)
	if err := os.Chtimes(fresh, scanTime.Add(time.Hour), scanTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(env.home, "stuff/recover.bin"), 300)
	env.settings.Custom = []CustomFolder{{Name: "stuff", Path: filepath.Join(env.home, "stuff"), Enabled: true}}
	var started []string
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + filepath.Join(env.home, "stuff")}, {ID: "sys.trash"}}, scanTime, cleanHooks{started: func(id string) { started = append(started, id) }})
	if len(res) != 2 || res[0].Name != "sys.trash" || res[0].Freed != 100 || res[1].Trashed != 300 || started[0] != "sys.trash" {
		t.Fatalf("results = %+v, started = %v", res, started)
	}
	if exists(old) || !exists(fresh) || !exists(filepath.Join(env.home, ".Trash/recover.bin")) {
		t.Fatal("trash ordering or cutoff lost")
	}
}

// TestCleanKeepsWhatReachedTheTrashAfterTheScan: emptying the Trash deletes
// what the scan showed; what arrives later stays recoverable, and the result
// says the Trash was not emptied.
func TestCleanKeepsWhatReachedTheTrashAfterTheScan(t *testing.T) {
	env := testEnv(t)
	home := env.home
	trash := filepath.Join(home, ".Trash")
	writeFile(t, filepath.Join(trash, "old.bin"), 1500)
	time.Sleep(20 * time.Millisecond)
	scannedAt := time.Now()
	time.Sleep(20 * time.Millisecond)
	writeFile(t, filepath.Join(trash, "new.bin"), 700)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "sys.trash"}}, time.UnixMilli(scannedAt.UnixMilli()), cleanHooks{})[0]
	if exists(filepath.Join(trash, "old.bin")) || !exists(filepath.Join(trash, "new.bin")) || res.Freed != 1500 || !slices.Equal(res.Failed, []string{"changed"}) {
		t.Fatalf("result = %+v", res)
	}
}

// TestCleanKeepsAFolderThatChangedInTheTrash: a file that reaches a folder
// already in the Trash after the scan keeps that whole folder recoverable,
// even when only a folder deep inside it changed.
func TestCleanKeepsAFolderThatChangedInTheTrash(t *testing.T) {
	env := testEnv(t)
	trash := filepath.Join(env.home, ".Trash")
	writeFile(t, filepath.Join(trash, "old.bin"), 100)
	kept := filepath.Join(trash, "folder", "nested", "old.bin")
	writeFile(t, kept, 200)
	time.Sleep(20 * time.Millisecond)
	scannedAt := time.Now()
	time.Sleep(20 * time.Millisecond)
	fresh := filepath.Join(trash, "folder", "nested", "new.bin")
	writeFile(t, fresh, 300)
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "sys.trash"}}, time.UnixMilli(scannedAt.UnixMilli()), cleanHooks{})[0]
	if exists(filepath.Join(trash, "old.bin")) || !exists(kept) || !exists(fresh) || res.Freed != 100 || !slices.Equal(res.Failed, []string{"changed"}) {
		t.Fatalf("result = %+v", res)
	}
}
