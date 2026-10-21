package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RevealInFinder shows path in Finder: plain folders open in place; files
// and packages, which opening would launch or mount, are selected in their
// parent folder. The call returns an error when the path does not exist.
func platformReveal(path string) error {
	args, err := revealArgs(path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := quietCommand(exec.CommandContext(ctx, "/usr/bin/open", args...)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("open failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// PhotoPreview returns a JPEG data URL of a photo in the home folder, at most
// 1600 pixels on its longest side, for a closer look than the thumbnails give.
func platformPhotoPreview(path string) (string, error) {
	if !within(canonical(path), canonical(homeDir())) || !photoExts[strings.ToLower(filepath.Ext(path))] {
		return "", errBlocked
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errGone
		}
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", errBlocked
	}
	tmp, err := os.CreateTemp("", "cleanzo-preview-*.jpg")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if out, err := quietCommand(exec.CommandContext(ctx, "/usr/bin/sips", "-Z", "1600", "-s", "format", "jpeg", path, "--out", tmp.Name())).CombinedOutput(); err != nil {
		return "", fmt.Errorf("sips: %v: %s", err, strings.TrimSpace(string(out)))
	}
	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		return "", err
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data), nil
}

// loadPhotoImage decodes a photo for hashing and its thumbnail. cfg is the
// photo's real size, while img may be a smaller copy (see maxDecodePixels).
// The returned cleanup removes any temporary file and must be run by the
// caller.
func platformLoadPhotoImage(ctx context.Context, path string) (image.Image, image.Config, func(), error) {
	ext := strings.ToLower(filepath.Ext(path))
	var cfg image.Config
	if ext == ".heic" || ext == ".heif" {
		w, h, err := sipsSize(ctx, path)
		if err != nil {
			return nil, image.Config{}, nil, err
		}
		cfg = image.Config{Width: w, Height: h}
	} else {
		c, err := decodeConfigFile(path)
		if err != nil {
			return nil, image.Config{}, nil, err
		}
		if c.Width*c.Height <= maxDecodePixels {
			return decodeImageFile(path)
		}
		cfg = c
	}
	img, cleanup, err := decodeSmallCopy(ctx, path, cfg.Width*cfg.Height > maxDecodePixels)
	if err != nil {
		return nil, image.Config{}, nil, err
	}
	return img, cfg, cleanup, nil
}

// collectPhotoFiles walks the given roots recursively and collects known image
// files, stopping once cap files are found. Packages and hidden folders are
// skipped.
func platformCollectPhotoFiles(ctx context.Context, roots []string, cap int) []string {
	var out []string
	stop := errors.New("stop")
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d == nil {
				return nil
			}
			if d.IsDir() {
				if p != root && (strings.HasPrefix(d.Name(), ".") || isPackage(d.Name())) {
					return filepath.SkipDir
				}
				return nil
			}
			if photoExts[strings.ToLower(filepath.Ext(p))] {
				out = append(out, p)
				if len(out) >= cap {
					return stop
				}
			}
			return nil
		})
		if len(out) >= cap {
			break
		}
	}
	return out
}

// openPhotoFile retains the native Darwin file open behavior.
func openPhotoFile(path string) (*os.File, error) { return photoFileOpen(path) }

// platformDirChildren lists children with a short first pass for folder sizes.
func platformDirChildren(ctx context.Context, path string) (DirListing, error) {
	ents, err := os.ReadDir(path)
	if err != nil {
		return DirListing{}, err
	}
	type job struct {
		name  string
		full  string
		isDir bool
	}
	jobs := make([]job, 0, len(ents))
	for _, e := range ents {
		full := filepath.Join(path, e.Name())
		jobs = append(jobs, job{e.Name(), full, e.IsDir()})
	}
	var mu sync.Mutex
	items := make([]DirItem, 0, len(jobs))
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			size := int64(-1)
			if j.isDir {
				if sz, err := dirMeasure(ctx, j.full, 8*time.Second); err == nil {
					size = sz
				}
			} else if st, err := os.Stat(j.full); err == nil {
				size = st.Size()
			}
			mu.Lock()
			items = append(items, DirItem{Name: j.name, Path: j.full, Size: size, IsDir: j.isDir, Trashable: removable(j.full)})
			mu.Unlock()
		}(j)
	}
	wg.Wait()
	return childDirListing(items), nil
}
