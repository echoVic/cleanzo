package main

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeAppIcons swaps the renderer for one that counts its calls and returns
// png, and starts from an empty cache; both are restored afterwards.
func fakeAppIcons(t *testing.T, png []byte) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	old := appIconRender
	appIconRender = func(string, int) []byte { calls.Add(1); return png }
	reset := func() { appIcons.Lock(); clear(appIcons.urls); appIcons.Unlock() }
	reset()
	t.Cleanup(func() { appIconRender = old; reset() })
	return &calls
}

func TestAppIconRefusesWhatIsNotAnAppBundle(t *testing.T) {
	calls := fakeAppIcons(t, []byte{1})
	dir := t.TempDir()
	file := filepath.Join(dir, "File.app")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"", "Relative.app", dir, filepath.Join(dir, "Missing.app"), file} {
		if got, err := (Cleaner{}).AppIcon(p); got != "" || err != nil {
			t.Errorf("AppIcon(%q) = %q, %v; want no icon", p, got, err)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("drew %d icons for paths that are not app bundles", n)
	}
}

func TestAppIconDrawsEachBundleOnce(t *testing.T) {
	calls := fakeAppIcons(t, []byte{1, 2, 3})
	app := filepath.Join(t.TempDir(), "Acme.app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	urls := make([]string, 8)
	var wg sync.WaitGroup
	for i := range urls {
		wg.Add(1)
		go func() { defer wg.Done(); urls[i], _ = (Cleaner{}).AppIcon(app) }()
	}
	wg.Wait()
	for _, u := range urls {
		if u != "data:image/png;base64,AQID" {
			t.Fatalf("AppIcon = %q, want the PNG as a data URL", u)
		}
	}
	if got, _ := (Cleaner{}).AppIcon(app + string(filepath.Separator)); got != urls[0] {
		t.Fatalf("with a trailing separator: %q", got)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("drew %d times, want once", n)
	}
}

func TestAppIconRemembersBundlesWithoutAnIcon(t *testing.T) {
	calls := fakeAppIcons(t, nil)
	app := filepath.Join(t.TempDir(), "Blank.app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if got, err := (Cleaner{}).AppIcon(app); got != "" || err != nil {
			t.Fatalf("AppIcon = %q, %v; want no icon", got, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("drew %d times, want once", n)
	}
}
