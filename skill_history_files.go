package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

const maxSkillPreviewRows = 200

// SkillFileChange identifies a changed relative file path.
type SkillFileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

// SkillUpgradePreview describes the files changed by an archive upgrade.
type SkillUpgradePreview struct {
	Changes []SkillFileChange `json:"changes"`
	Total   int               `json:"total"`
}

func copySkillVersion(ctx context.Context, src, dst string) error {
	if err := os.Mkdir(dst, 0o700); err != nil {
		return err
	}
	out, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = readSkillVersionFiles(ctx, src, false, out)
	return err
}

func fingerprintSkillVersion(ctx context.Context, dir string) (string, error) {
	files, err := readSkillVersionFiles(ctx, dir, false, nil)
	if err != nil {
		return "", err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	digest := sha256.New()
	for _, p := range paths {
		_, _ = fmt.Fprintf(digest, "%q\x00%q\x00", p, files[p])
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// readSkillVersionFiles reads only within a directory handle. Copying within a
// private staging handle avoids following links introduced during a walk.
func readSkillVersionFiles(ctx context.Context, dir string, preview bool, out *os.Root) (map[string]string, error) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return nil, errSkillNotEntry
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	observed, err := root.Stat(".")
	if err != nil || !os.SameFile(info, observed) {
		return nil, errSkillChangedDuringUpgrade
	}
	files := make(map[string]string)
	var remaining = int64(2 << 30)
	count := 0
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		count++
		if count > 200000 {
			return errSkillTooManyEntries
		}
		if preview && (d.Name() == ".git" || d.Name() == ".DS_Store") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := filepath.FromSlash(p)
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			if out != nil {
				return out.Mkdir(name, 0o755)
			}
			if !preview {
				files[p] = "directory"
			}
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := root.Readlink(name)
			if err != nil {
				return err
			}
			if out != nil {
				return out.Symlink(link, name)
			}
			files[p] = "link:" + link
		case info.Mode().IsRegular():
			if info.Size() > remaining {
				return errSkillTooLarge
			}
			f, err := root.Open(name)
			if err != nil {
				return err
			}
			defer f.Close()
			observed, err := f.Stat()
			if err != nil || !observed.Mode().IsRegular() || !os.SameFile(observed, info) {
				return errSkillChangedDuringUpgrade
			}
			mode := os.FileMode(0o644)
			if info.Mode().Perm()&0o111 != 0 {
				mode = 0o755
			}
			digest := sha256.New()
			var target io.Writer = digest
			var output *os.File
			if out != nil {
				output, err = out.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
				if err != nil {
					return err
				}
				target = output
			}
			n, err := io.Copy(target, io.LimitReader(f, remaining+1))
			if output != nil {
				if closeErr := output.Close(); err == nil {
					err = closeErr
				}
			} else {
				files[p] = fmt.Sprintf("file:%o:%x", info.Mode().Perm()&0o111, digest.Sum(nil))
			}
			if err != nil {
				return err
			}
			remaining -= n
			if remaining < 0 {
				return errSkillTooLarge
			}
		default:
			return fmt.Errorf("unsupported skill entry %s", p)
		}
		return nil
	})
	return files, err
}

// PreviewSkillUpgrade validates and compares the current upstream archive with
// the local folder. Preview is advisory and the upgrade revalidates all input.
func (Cleaner) PreviewSkillUpgrade(ctx context.Context, folder string) (SkillUpgradePreview, error) {
	result := SkillUpgradePreview{Changes: []SkillFileChange{}}
	if !filepath.IsAbs(folder) {
		return result, errSkillNotEntry
	}
	folder = resolvePath(filepath.Clean(folder))
	if err := skillFolderOK(folder, ctx); err != nil {
		return result, err
	}
	origin := skillHistoryOrigin(ctx, folder)
	if origin.Repo == "" {
		return result, errSkillNoOrigin
	}
	if origin.Kind == originGit {
		return result, fmt.Errorf("git-skill-use-git")
	}
	cache := &treeCache{trees: map[string]*githubTree{}}
	update := checkOne(ctx, folder, filepath.Base(folder), origin, cache, true, skillNow().Unix())
	switch update.Status {
	case "error":
		return result, fmt.Errorf("%s", update.Error)
	case "missing":
		return result, errSkillNotFound
	}
	archives := map[string]skillArchive{}
	src, _, _, err := verifiedSkillArchive(ctx, origin, update.Remote, archives)
	defer func() {
		for _, archive := range archives {
			_ = os.RemoveAll(filepath.Dir(archive.root))
		}
	}()
	if err != nil {
		return result, err
	}
	local, err := readSkillVersionFiles(ctx, folder, true, nil)
	if err != nil {
		return result, err
	}
	remote, err := readSkillVersionFiles(ctx, src, true, nil)
	if err != nil {
		return result, err
	}
	for _, kind := range []string{"added", "changed", "removed"} {
		paths := []string{}
		if kind == "removed" {
			for p := range local {
				if _, ok := remote[p]; !ok {
					paths = append(paths, p)
				}
			}
		} else {
			for p, v := range remote {
				old, exists := local[p]
				if kind == "added" && !exists || kind == "changed" && exists && v != old {
					paths = append(paths, p)
				}
			}
		}
		slices.Sort(paths)
		result.Total += len(paths)
		for _, p := range paths {
			if len(result.Changes) >= maxSkillPreviewRows {
				break
			}
			result.Changes = append(result.Changes, SkillFileChange{Path: p, Kind: kind})
		}
	}
	return result, nil
}
