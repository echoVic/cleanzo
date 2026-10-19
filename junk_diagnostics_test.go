package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestJunkScanReport is an opt-in, read-only scan of this Mac. Normal test runs
// skip it. The report contains paths and sizes, never file contents. It never
// invokes cleanJunk or any destructive tool command.
// CLEANZO_SCAN_REPORT=/absolute/report.json go test -run '^TestJunkScanReport$' -count=1
func TestJunkScanReport(t *testing.T) {
	output := os.Getenv("CLEANZO_SCAN_REPORT")
	if output == "" {
		t.Skip("set CLEANZO_SCAN_REPORT to an absolute output path to scan this Mac")
	}
	if !filepath.IsAbs(output) {
		t.Fatal("CLEANZO_SCAN_REPORT must be an absolute path")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("report output must not already exist")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env := currentJunkEnv()
	env.ctx = ctx
	env.observe = true
	specs, summary := expandItems(&env)
	items := measureItems(ctx, specs, env, nil, nil)
	if err := ctx.Err(); err != nil {
		t.Fatal(err)
	}
	type candidate struct {
		Path          string `json:"path"`
		MinAgeSeconds int64  `json:"minAgeSeconds"`
	}
	type row struct {
		Item       JunkItem    `json:"item"`
		Candidates []candidate `json:"candidates"`
	}
	report := struct {
		GeneratedAt time.Time   `json:"generatedAt"`
		ReadOnly    bool        `json:"readOnly"`
		Summary     ScanSummary `json:"summary"`
		Rows        []row       `json:"rows"`
	}{GeneratedAt: time.Now(), ReadOnly: true, Summary: summary}
	for i, spec := range specs {
		r := row{Item: items[i]}
		for _, c := range spec.cands {
			r.Candidates = append(r.Candidates, candidate{Path: env.displayable(c.path), MinAgeSeconds: int64(c.minAge.Seconds())})
		}
		report.Rows = append(report.Rows, r)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	t.Logf("read-only report: %d items, full disk access: %t", len(items), summary.FullDiskAccess)
}
