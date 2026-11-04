//go:build !darwin

package main

import (
	"context"

	"github.com/egoist/mygo"
)

// App updates are macOS-only; other platforms end checks without doing work.
func init() {
	appUpdateCheck = func(_ context.Context, _ []string, ch *mygo.Channel[AppUpdate]) error {
		_ = ch.Send(AppUpdate{ID: "__done"})
		return nil
	}
	appUpdateUpgrade = func(_ context.Context, id string) AppUpgradeResult {
		return AppUpgradeResult{ID: id, Code: "unsupported"}
	}
	appUpdateClean = func(path string) UninstallResult {
		return UninstallResult{Path: path, Failed: []string{path + ": unsupported"}}
	}
}
