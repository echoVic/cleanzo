package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	entered := make(chan struct{})
	release := make(chan struct{})
	appIconRender = func(string, int) []byte {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return []byte{1, 2, 3}
	}
	urls := make([]string, 8)
	errs := make([]error, len(urls))
	var started, wg sync.WaitGroup
	started.Add(len(urls))
	for i := range urls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started.Done()
			urls[i], errs[i] = (Cleaner{}).AppIcon(app)
		}()
	}
	<-entered
	started.Wait()
	close(release)
	wg.Wait()
	for i, u := range urls {
		if u != "data:image/png;base64,AQID" || errs[i] != nil {
			t.Fatalf("AppIcon = %q, %v; want the PNG as a data URL", u, errs[i])
		}
	}
	if got, _ := (Cleaner{}).AppIcon(app + string(filepath.Separator)); got != urls[0] {
		t.Fatalf("with a trailing separator: %q", got)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("drew %d times, want once", n)
	}
}

func TestAppIconSerializesDrawingAcrossBundles(t *testing.T) {
	fakeAppIcons(t, nil)
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "A.app"), filepath.Join(dir, "B.app"), filepath.Join(dir, "C.app")}
	pngs := make(map[string][]byte)
	for i, path := range paths {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
		pngs[path] = []byte{byte(i + 1)}
	}
	var mu sync.Mutex
	drawing, maxDrawing := 0, 0
	calls := make(map[string]int)
	appIconRender = func(path string, _ int) []byte {
		mu.Lock()
		drawing++
		maxDrawing = max(maxDrawing, drawing)
		calls[path]++
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		drawing--
		mu.Unlock()
		return pngs[path]
	}
	var urls [3][3]string
	var errs [3][3]error
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, path := range paths {
		for j := range urls[i] {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				urls[i][j], errs[i][j] = (Cleaner{}).AppIcon(path)
			}()
		}
	}
	close(start)
	wg.Wait()
	if maxDrawing != 1 {
		t.Errorf("at most %d icons were drawn simultaneously, want 1", maxDrawing)
	}
	total := 0
	for i, path := range paths {
		total += calls[path]
		if calls[path] != 1 {
			t.Errorf("drew %q %d times, want once", path, calls[path])
		}
		want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngs[path])
		for j, url := range urls[i] {
			if url != want || errs[i][j] != nil {
				t.Errorf("AppIcon(%q), call %d = %q, %v; want %q", path, j, url, errs[i][j], want)
			}
		}
	}
	if total != 3 {
		t.Errorf("drew %d icons in total, want 3", total)
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
