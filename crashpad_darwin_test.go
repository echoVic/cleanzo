package main

import (
	"context"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
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

// TestCrashDumpsReportWhatCouldNotBeRemoved: a dump macOS refuses to remove
// (here, a locked file) is reported, and the database does not finish as
// cleaned.
func TestCrashDumpsReportWhatCouldNotBeRemoved(t *testing.T) {
	env := testEnv(t)
	codexWeb, _, _ := crashpadFixture(t, env)
	locked := filepath.Join(codexWeb, "pending", "ra.dmp")
	const immutable = 0x2 // UF_IMMUTABLE, which the owner may set
	if err := syscall.Chflags(locked, immutable); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Chflags(locked, 0) })
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: crashRuleID, Paths: []string{codexWeb}}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Freed != 47000 || res[0].Files != 10 || !slices.Contains(res[0].Failed, "blocked") || !exists(locked) {
		t.Fatalf("result = %+v", res)
	}
}
