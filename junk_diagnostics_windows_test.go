package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWindowsJunkScanReport is the only test allowed to observe the unsandboxed profile.
func TestWindowsJunkScanReport(t *testing.T) {
	if os.Getenv("CLEANZO_REAL_PROFILE_SCAN") != "1" {
		t.Skip("set CLEANZO_REAL_PROFILE_SCAN=1 with exactly -run '^TestWindowsJunkScanReport$'")
	}
	output := os.Getenv("CLEANZO_SCAN_REPORT")
	if !filepath.IsAbs(output) {
		t.Fatal("CLEANZO_SCAN_REPORT must be absolute")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	env := currentJunkEnv()
	env.ctx, env.observe = ctx, true
	// This diagnostic never invokes tools, including their read-only estimate commands.
	env.findTool = func(string) string { return "" }
	specs, summary := expandItems(&env)
	type row struct {
		Item       JunkItem `json:"item"`
		Candidates []string `json:"candidates"`
	}
	report := struct {
		ReadOnly bool        `json:"readOnly"`
		Home     string      `json:"home"`
		Summary  ScanSummary `json:"summary"`
		Rows     []row       `json:"rows"`
	}{ReadOnly: true, Home: resolvePath(env.home), Summary: summary}
	home := canonical(env.home)
	temp := ""
	if allowedTemp(env.home, env.tmp) {
		temp = canonical(env.tmp)
	}
	goBuildCandidates := 0
	for _, spec := range specs {
		if spec.item.ID == "dev.go-build" {
			goBuildCandidates += len(spec.cands)
		}
		r := row{Item: spec.item}
		for _, c := range spec.cands {
			p := canonical(c.path)
			// Independent lexical checks must not share the production protection helpers.
			lexical := strings.ToLower(strings.ReplaceAll(filepath.Clean(c.path), "/", `\`))
			for _, forbidden := range []string{`\appdata\roaming\microsoft\`, `\onedrive`, `\ntuser`, `\appdata\local\packages\`, `\appdata\local\programs\`, `\appdata\local\cleanzo\`} {
				if strings.Contains(lexical, forbidden) {
					t.Errorf("deny-listed candidate: %q", c.path)
				}
			}
			if p == "" || !(within(p, home) || temp != "" && within(p, temp)) {
				t.Errorf("candidate escaped profile: %q", c.path)
			}
			if !safeToClean(c.path) || platformProtected(strings.ToLower(resolvePath(c.path)), home) {
				t.Errorf("protected candidate: %q", c.path)
			}
			r.Candidates = append(r.Candidates, c.path)
		}
		report.Rows = append(report.Rows, r)
	}
	if goBuildCandidates == 0 {
		t.Error("dev.go-build must have candidates after compiling the tests")
	}
	items := measureItems(ctx, specs, env, nil, nil)
	for i := range report.Rows {
		report.Rows[i].Item = items[i]
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("write=%v close=%v", writeErr, closeErr)
	}
	t.Logf("read-only scan: %d rows, report %s", len(items), output)
}
