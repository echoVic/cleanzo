package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

// showItemInFolder is MyGo's public Explorer /select API; it requires a running app.
var showItemInFolder = mygo.Shell.ShowItemInFolder

// platformReveal expands either Windows home spelling before selecting the existing path.
func platformReveal(path string) error {
	abs, err := filepath.Abs(expandHome(path))
	if err != nil {
		return err
	}
	if _, err = os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return errGone
		}
		return err
	}
	showItemInFolder(abs)
	return nil
}

// windowsPhotoPath refuses OneDrive trees, including redirected environment roots.
func windowsPhotoPath(path string) bool {
	if !platformCleanPath(path) {
		return false
	}
	rel, err := filepath.Rel(homeDir(), path)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.HasPrefix(strings.ToLower(part), "onedrive") {
			return false
		}
	}
	for _, key := range []string{"OneDrive", "OneDriveConsumer", "OneDriveCommercial"} {
		if root := os.Getenv(key); root != "" && within(canonical(path), canonical(root)) {
			return false
		}
	}
	return true
}

// openPhotoFile rejects cloud attributes before a data open and rechecks the opened handle before reads.
func openPhotoFile(path string) (*os.File, error) {
	if !windowsPhotoPath(path) {
		return nil, errBlocked
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !windowsEntrySafe(info) {
		return nil, errBlocked
	}
	return photoFileOpen(path)
}

// windowsPhotoOpen prevents recall during an open and checks attributes again on the exact opened object.
func windowsPhotoOpen(path string) (*os.File, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(ptr, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_NO_RECALL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_OFFLINE|windows.FILE_ATTRIBUTE_RECALL_ON_OPEN|windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS) != 0 {
		windows.CloseHandle(h)
		return nil, errBlocked
	}
	return os.NewFile(uintptr(h), path), nil
}

// init selects the non-hydrating data open while keeping the hook observable by tests.
func init() { photoFileOpen = windowsPhotoOpen }

// platformCollectPhotoFiles walks only supported resident images and honors cancellation during discovery.
func platformCollectPhotoFiles(ctx context.Context, roots []string, cap int) []string {
	var out []string
	if cap <= 0 {
		return out
	}
	for _, root := range roots {
		if ctx.Err() != nil || len(out) >= cap {
			break
		}
		if !windowsPhotoPath(root) {
			continue
		}
		_ = walkWindows(ctx, root, func(path string, info fs.FileInfo) error {
			if !windowsPhotoPathEntry(path, info) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if info.Mode().IsRegular() && photoExts[strings.ToLower(filepath.Ext(path))] {
				ext := strings.ToLower(filepath.Ext(path))
				if ext == ".heic" || ext == ".heif" {
					return nil
				}
				out = append(out, path)
				if len(out) >= cap {
					return fs.SkipAll
				}
			}
			return nil
		})
	}
	return out
}

// windowsPhotoPathEntry uses the walk's metadata, avoiding path-resolution work for every photo.
func windowsPhotoPathEntry(path string, info fs.FileInfo) bool {
	name := info.Name()
	if strings.HasPrefix(strings.ToLower(name), "onedrive") {
		return false
	}
	if info.IsDir() && (strings.HasPrefix(name, ".") || isPackage(name)) {
		return false
	}
	for _, key := range []string{"OneDrive", "OneDriveConsumer", "OneDriveCommercial"} {
		if root := os.Getenv(key); root != "" && strings.EqualFold(filepath.Clean(root), path) {
			return false
		}
	}
	return true
}

// platformLoadPhotoImage uses only standard-library decoders and leaves oversized images unprocessed.
func platformLoadPhotoImage(ctx context.Context, path string) (image.Image, image.Config, func(), error) {
	if ctx.Err() != nil {
		return nil, image.Config{}, nil, ctx.Err()
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".heic" || ext == ".heif" {
		return nil, image.Config{}, nil, errUnsupported
	}
	cfg, err := decodeConfigFile(path)
	if err != nil {
		return nil, image.Config{}, nil, err
	}
	if !photoDecodeAllowed(cfg) {
		return nil, image.Config{}, nil, errUnsupported
	}
	return decodeImageFile(path)
}

// platformPhotoPreview renders a bounded JPEG preview without starting sips.
func platformPhotoPreview(path string) (string, error) {
	if !photoExts[strings.ToLower(filepath.Ext(path))] || !windowsPhotoPath(path) {
		return "", errBlocked
	}
	img, _, cleanup, err := platformLoadPhotoImage(context.Background(), path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errGone
		}
		return "", err
	}
	defer cleanup()
	var buf bytes.Buffer
	if err = jpeg.Encode(&buf, resizeMaxEdge(img, 1600), nil); err != nil {
		return "", err
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
