//go:build darwin

package main

import (
	"context"
	"path/filepath"
	"testing"
)

// TestScannedRowsCarryTheirRecovery checks the rows of each source: a rule,
// an app's caches, a custom folder and a project.
func TestScannedRowsCarryTheirRecovery(t *testing.T) {
	env := testEnv(t)
	projectClock(t)
	writeFile(t, filepath.Join(env.home, "Library/Caches/go-build/ab/obj"), 1000)
	writeFile(t, filepath.Join(env.home, ".npm/_cacache/x"), 1000)
	writeFile(t, filepath.Join(env.home, "Library/Logs/app.log"), 1000)
	writeFile(t, filepath.Join(env.home, ".Trash/old"), 1000)
	writeFile(t, filepath.Join(env.home, "Library/Caches/com.example.notes/blob"), 1000)
	custom := filepath.Join(env.home, "scratch")
	writeFile(t, filepath.Join(custom, "a"), 1000)
	env.settings.Custom = []CustomFolder{{Name: "scratch", Path: custom, Enabled: true}}
	app := filepath.Join(env.home, "code", "app")
	writeText(t, filepath.Join(app, "package.json"), "{}")
	writeFile(t, filepath.Join(app, "node_modules", "dep", "index.js"), 100)
	items, _ := scanJunk(context.Background(), env, nil, nil)
	got := map[string]string{}
	for _, it := range items {
		got[it.ID] = it.Recovery
	}
	want := map[string]recoveryKind{
		"dev.go-build": recoverRebuild, "dev.npm": recoverDownload, "sys.logs": recoverLeftover, "sys.trash": recoverGone,
		"app:dir:com.example.notes": recoverDownload, "custom:" + custom: recoverGone, "project:" + app: recoverDownload,
	}
	for id, kind := range want {
		if got[id] != string(kind) {
			t.Errorf("%s recovery = %q, want %q (rows: %v)", id, got[id], kind, got)
		}
	}
}
