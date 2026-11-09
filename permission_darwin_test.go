package main

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/permissionflow"
)

// The settings page's Full Disk Access button runs the system's guide on a
// goroutine: it opens System Settings and waits for the user there, so the
// page never blocks. Tests replace the guide, so it never opens.
func TestOpenFullDiskAccessSettingsStartsGuide(t *testing.T) {
	original := guideFullDiskAccess
	t.Cleanup(func() { guideFullDiskAccess = original })
	options := make(chan permissionflow.GuideOptions, 1)
	guideFullDiskAccess = func(_ context.Context, o permissionflow.GuideOptions) error {
		options <- o
		return nil
	}
	if err := (Cleaner{}).OpenFullDiskAccessSettings(); err != nil {
		t.Fatalf("OpenFullDiskAccessSettings: %v", err)
	}
	select {
	case got := <-options:
		if got.Name != "Cleanzo" {
			t.Fatalf("the guide names the app %q, want Cleanzo", got.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the guide was not started")
	}
}
