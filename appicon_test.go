package main

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testPNG(t *testing.T) []byte {
	t.Helper()
	return testPNGWithColor(t, color.NRGBA{R: 32, G: 64, B: 128, A: 255})
}

func testPNGWithColor(t *testing.T, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 80, 80))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIconLooksBlank(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		{"opaque", testPNG(t), false},
		{"almost transparent", testPNGWithColor(t, color.NRGBA{A: 6}), true},
		{"not a PNG", []byte("not a PNG"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := iconLooksBlank(tc.data); got != tc.want {
				t.Fatalf("iconLooksBlank = %v, want %v", got, tc.want)
			}
		})
	}
}

// fakeAppIcons swaps the renderer for one that counts its calls and returns
// icon, and starts from an empty cache; both are restored afterwards.
func fakeAppIcons(t *testing.T, icon []byte) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	old := appIconRender
	appIconRender = func(string, int) []byte { calls.Add(1); return icon }
	reset := func() { appIcons.Lock(); clear(appIcons.urls); appIcons.Unlock() }
	reset()
	t.Cleanup(func() { appIconRender = old; reset() })
	return &calls
}

func TestAppIconRefusesWhatIsNotAnAppBundle(t *testing.T) {
	calls := fakeAppIcons(t, testPNG(t))
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
	data := testPNG(t)
	calls := fakeAppIcons(t, data)
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
		return data
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
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the renderer was never called")
	}
	started.Wait()
	close(release)
	wg.Wait()
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	for i, u := range urls {
		if u != want || errs[i] != nil {
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
		pngs[path] = testPNGWithColor(t, color.NRGBA{R: uint8(i + 1), A: 255})
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

func TestAppIconRejectsBlankPNG(t *testing.T) {
	calls := fakeAppIcons(t, testPNGWithColor(t, color.NRGBA{A: 6}))
	app := filepath.Join(t.TempDir(), "Blank.app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if got, err := (Cleaner{}).AppIcon(app); got != "" || err != nil {
			t.Fatalf("AppIcon = %q, %v; want no icon for a blank PNG", got, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("drew %d times, want once", n)
	}
}
