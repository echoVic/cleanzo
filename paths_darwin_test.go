package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestCloudStorageIsProtected: cloud providers keep their files in
// ~/Library/CloudStorage, online-only placeholders among them. Moving one to
// the Trash frees nothing on this Mac and deletes it from the cloud.
func TestCloudStorageIsProtected(t *testing.T) {
	env := testEnv(t)
	drive := filepath.Join(env.home, "Library", "CloudStorage", "OneDrive-Personal")
	file := filepath.Join(drive, "Videos", "big.mov")
	writeFile(t, file, 100)
	for _, p := range []string{drive, file, filepath.Join(env.home, "library", "cloudstorage")} {
		if safeToClean(p) || removable(p) {
			t.Errorf("%s is offered for cleaning", p)
		}
	}
	env.settings.Custom = []CustomFolder{{Name: "drive", Path: drive, Enabled: true}}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "custom:" + drive}}, time.Time{}, cleanHooks{})
	if !exists(file) || len(res) != 1 || res[0].Trashed != 0 {
		t.Fatalf("a cloud folder was cleaned: %+v", res)
	}
}
