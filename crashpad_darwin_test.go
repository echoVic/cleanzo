package main

import (
	"context"
	"path/filepath"
	"testing"
)

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
