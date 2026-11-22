package main

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
)

// PickCatalogSkillZIP selects a local ZIP archive from the native file dialog.
func (Cleaner) PickCatalogSkillZIP(ctx context.Context) (string, error) {
	files, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent: mygo.CallerWindow(ctx), DefaultPath: homeDir(),
		Filters: []mygo.FileFilter{{Name: "ZIP", Extensions: []string{"zip"}}},
	})
	if err != nil || len(files) == 0 {
		return "", err
	}
	return files[0], nil
}

// ImportCatalogSkillZIP installs the single folder containing SKILL.md in a local zip.
func (Cleaner) ImportCatalogSkillZIP(ctx context.Context, file string, tools []string) ([]SkillCatalogInstallResult, error) {
	roots, err := catalogInstallRoots(tools)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(file) || !strings.EqualFold(filepath.Ext(file), ".zip") {
		return nil, errors.New("invalid-zip")
	}
	selected, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !selected.Mode().IsRegular() {
		return nil, errors.New("invalid-zip")
	}
	archive, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	info, err := archive.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(selected, info) {
		return nil, errors.New("invalid-zip")
	}
	if info.Size() > skillArchiveLimit {
		return nil, errSkillTooLarge
	}
	reader, err := zip.NewReader(archive, info.Size())
	if err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp("", "cleanzo-skill-zip-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	folder, err := extractCatalogZIP(ctx, reader, stage)
	if err != nil {
		return nil, err
	}
	name := path.Base(folder)
	if folder == "." {
		name = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	}
	if !catalogNameOK(name) {
		return nil, errors.New("invalid-skill-path")
	}
	source := filepath.Join(stage, filepath.FromSlash(folder))
	return installCatalogCopies(ctx, source, name, SkillOrigin{}, nil, roots), nil
}

func extractCatalogZIP(ctx context.Context, archive *zip.Reader, stage string) (string, error) {
	if len(archive.File) > skillEntryLimit {
		return "", errSkillTooManyEntries
	}
	var declared uint64
	skill := ""
	seen := map[string]bool{}
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name := strings.TrimSuffix(entry.Name, "/")
		if name == "" || !validCatalogZIPPath(name) || !entry.Mode().IsRegular() && !entry.FileInfo().IsDir() {
			return "", errors.New("unsafe-zip")
		}
		// Refuse duplicate names, including case-only aliases on Windows and macOS.
		key := strings.ToLower(name)
		if seen[key] {
			return "", errors.New("unsafe-zip")
		}
		seen[key] = true
		if entry.UncompressedSize64 > uint64(skillUnpackLimit)-declared {
			return "", errSkillTooLarge
		}
		declared += entry.UncompressedSize64
		if isCatalogZIPArtifact(name) || !entry.Mode().IsRegular() || path.Base(name) != "SKILL.md" {
			continue
		}
		if skill != "" {
			return "", errors.New("zip-multiple-skills")
		}
		skill = path.Dir(name)
	}
	if skill == "" {
		return "", errors.New("zip-missing-skill")
	}
	remaining := skillUnpackLimit
	for _, entry := range archive.File {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name := strings.TrimSuffix(entry.Name, "/")
		if isCatalogZIPArtifact(name) || skill != "." && name != skill && !strings.HasPrefix(name, skill+"/") {
			continue
		}
		destination := filepath.Join(stage, filepath.FromSlash(name))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return "", err
			}
			continue
		}
		written, err := extractCatalogZIPFile(entry, destination, remaining)
		if err != nil {
			return "", err
		}
		remaining -= written
	}
	return skill, nil
}

func validCatalogZIPPath(name string) bool {
	for _, part := range strings.Split(name, "/") {
		// Hidden files such as .gitignore can ship with a skill. Git database
		// content and traversal components are rejected by component validation.
		if part == "." || part == ".." || strings.EqualFold(part, ".git") || !catalogNameOK(strings.TrimPrefix(part, ".")) {
			return false
		}
	}
	return true
}

func isCatalogZIPArtifact(name string) bool {
	first, _, _ := strings.Cut(name, "/")
	return first == "__MACOSX" || path.Base(name) == ".DS_Store"
}

func extractCatalogZIPFile(entry *zip.File, destination string, remaining int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return 0, err
	}
	source, err := entry.Open()
	if err != nil {
		return 0, err
	}
	defer source.Close()
	mode := os.FileMode(0o644)
	if entry.Mode()&0o111 != 0 {
		mode = 0o755
	}
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return 0, err
	}
	written, err := io.Copy(out, io.LimitReader(source, remaining+1))
	if err := errors.Join(err, out.Close()); err != nil {
		return 0, err
	}
	if written > remaining {
		return 0, errSkillTooLarge
	}
	return written, nil
}
