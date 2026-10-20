package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRuleCatalogMeasuresEachTarget checks every non-command rule with staged paths.
func TestRuleCatalogMeasuresEachTarget(t *testing.T) {
	for _, rule := range junkRules {
		if rule.Command != nil {
			continue
		}
		t.Run(rule.ID, func(t *testing.T) {
			env := testEnv(t)
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
// what the scan showed; what arrives later stays recoverable.
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
	if exists(filepath.Join(trash, "old.bin")) || !exists(filepath.Join(trash, "new.bin")) || res.Freed != 1500 {
		t.Fatalf("result = %+v", res)
	}
}
