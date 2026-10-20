package main

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

// TestWindowsRevealHomePaths checks both home spellings through the public reveal entry point.
func TestWindowsRevealHomePaths(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	path := filepath.Join(home, "Pictures", "hello world.jpg")
	writeFile(t, path, 1)
	saved := showItemInFolder
	var seen []string
	showItemInFolder = func(p string) { seen = append(seen, p) }
	t.Cleanup(func() { showItemInFolder = saved })
	for _, p := range []string{`~\Pictures\hello world.jpg`, "~/Pictures/hello world.jpg"} {
		if err := (Cleaner{}).RevealInFinder(p); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || seen[0] != path || seen[1] != path {
		t.Fatalf("revealed=%v", seen)
	}
	if err := (Cleaner{}).RevealInFinder(`~\missing`); !errors.Is(err, errGone) {
		t.Fatalf("missing err=%v", err)
	}
}

// TestWindowsPhotosSkipOfflineAndFindDuplicates records every data open, including config, decode and SHA1.
func TestWindowsPhotosSkipOfflineAndFindDuplicates(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	pictures := filepath.Join(home, "Pictures")
	mustMkdir(t, pictures)
	a := filepath.Join(pictures, "a.jpg")
	writeGradientJPEG(t, a, 128, 96, false)
	b := filepath.Join(pictures, "b.jpg")
	copyFile(t, a, b)
	offline := filepath.Join(pictures, "offline.jpg")
	copyFile(t, a, offline)
	ptr, err := windows.UTF16PtrFromString(offline)
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := windows.GetFileAttributes(ptr)
	if err != nil {
		t.Fatal(err)
	}
	if err = windows.SetFileAttributes(ptr, attrs|windows.FILE_ATTRIBUTE_OFFLINE); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.SetFileAttributes(ptr, attrs) })
	for _, dir := range []string{"OneDrive", "OneDrive - Work", "oNeDrIvE Personal"} {
		mustMkdir(t, filepath.Join(pictures, dir))
		copyFile(t, a, filepath.Join(pictures, dir, "cloud.jpg"))
	}
	copyFile(t, a, filepath.Join(pictures, "unsupported.heic"))
	copyFile(t, a, filepath.Join(pictures, "unsupported.heif"))
	saved := photoFileOpen
	var mu sync.Mutex
	var opened []string
	photoFileOpen = func(path string) (*os.File, error) {
		mu.Lock()
		opened = append(opened, path)
		mu.Unlock()
		return saved(path)
	}
	t.Cleanup(func() { photoFileOpen = saved })
	result := buildSimilarPhotos(context.Background(), []string{pictures}, nil)
	if len(result.Clusters) != 1 || result.Clusters[0].Kind != "exact" || len(result.Clusters[0].Items) != 2 {
		t.Fatalf("photos=%+v", result)
	}
	for _, path := range opened {
		if path != a && path != b {
			t.Errorf("unsafe/unsupported data opened: %s", path)
		}
	}
	if len(opened) == 0 {
		t.Fatal("open hook did not see normal JPEGs")
	}
	if after, err := windows.GetFileAttributes(ptr); err != nil || after&windows.FILE_ATTRIBUTE_OFFLINE == 0 {
		t.Fatalf("offline attrs=%x %v", after, err)
	}
	for _, path := range []string{offline, filepath.Join(pictures, "OneDrive", "cloud.jpg")} {
		if _, err := (Cleaner{}).PhotoPreview(path); !errors.Is(err, errBlocked) {
			t.Fatalf("preview unsafe=%v", err)
		}
		if _, err := fileSHA1(path); !errors.Is(err, errBlocked) {
			t.Fatalf("hash unsafe=%v", err)
		}
	}
	if preview, err := (Cleaner{}).PhotoPreview(a); err != nil || !strings.HasPrefix(preview, "data:image/jpeg;base64,") {
		t.Fatalf("preview=%s %v", preview, err)
	}
}

// TestWindowsPhotosDecodeLimitAndCancellation checks oversized images are skipped before full decoding.
func TestWindowsPhotosDecodeLimitAndCancellation(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	path := filepath.Join(home, "large.jpg")
	writeGradientJPEG(t, path, 128, 96, false)
	saved := maxDecodePixels
	maxDecodePixels = 100
	t.Cleanup(func() { maxDecodePixels = saved })
	if _, _, _, err := loadPhotoImage(context.Background(), path); err == nil {
		t.Fatal("oversized JPEG decoded")
	}
	if _, err := (Cleaner{}).PhotoPreview(path); err == nil {
		t.Fatal("oversized preview decoded")
	}
	if _, _, _, err := decodeImageFile(path); err == nil {
		t.Fatal("direct decode exceeded pixel limit")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := buildSimilarPhotos(ctx, []string{home}, nil); result.Scanned != 0 || len(result.Clusters) != 0 {
		t.Fatal("canceled photos scanned")
	}
	if photoDecodeAllowed(image.Config{Width: int(^uint(0) >> 1), Height: 2}) {
		t.Fatal("pixel overflow accepted")
	}
}
