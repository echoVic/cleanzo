package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var errCatalogDestinationExists = errors.New("destination-exists")

func catalogInstallRoots(tools []string) ([]skillRoot, error) {
	if len(tools) == 0 || len(tools) > len(skillRoots) {
		return nil, errors.New("invalid-install-target")
	}
	out := make([]skillRoot, 0, len(tools))
	seen := map[string]bool{}
	for _, tool := range tools {
		if seen[tool] {
			return nil, errors.New("invalid-install-target")
		}
		seen[tool] = true
		found := false
		for _, root := range skillRoots {
			if root.Tool == tool {
				out = append(out, root)
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("invalid-install-target")
		}
	}
	return out, nil
}

func publishCatalogSkill(staged, destination string) error {
	err := publishSkillDeployment(staged, destination)
	if errors.Is(err, fs.ErrExist) {
		return errCatalogDestinationExists
	}
	return err
}

// checkCatalogCopy excludes Git metadata and checks links against the selected
// skill folder. The surrounding archive is deliberately not part of the install.
func checkCatalogCopy(ctx context.Context, source string, allowLinks bool) error {
	info, err := os.Lstat(source)
	if err != nil || !info.IsDir() {
		return errSkillNotEntry
	}
	if info, err = os.Lstat(filepath.Join(source, "SKILL.md")); err != nil || !info.Mode().IsRegular() {
		return errSkillNotEntry
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == source {
			return nil
		}
		if strings.EqualFold(entry.Name(), ".git") || entry.Type() != 0 && !entry.IsDir() && entry.Type() != fs.ModeSymlink {
			return errBlocked
		}
		if entry.Type() != fs.ModeSymlink {
			return nil
		}
		if !allowLinks {
			return errBlocked
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		if filepath.IsAbs(target) || filepath.VolumeName(target) != "" {
			return errBlocked
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !within(resolved, source) || !within(filepath.Join(filepath.Dir(path), target), source) {
			return errBlocked
		}
		return nil
	})
}

// InstallCatalogSkill installs the selected, verified tree to each chosen root.
func (Cleaner) InstallCatalogSkill(ctx context.Context, chosen SkillOrigin, tools []string) ([]SkillCatalogInstallResult, error) {
	roots, err := catalogInstallRoots(tools)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, catalogArchiveTimeout)
	defer cancel()
	origin, _, err := catalogExpected(ctx, chosen)
	if err != nil {
		return nil, err
	}
	archives := map[string]skillArchive{}
	archive, err := extractedRepo(ctx, origin.Repo, origin.Ref, archives)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(filepath.Dir(archive.root))
	archiveRoot, err := filepath.EvalSymlinks(archive.root)
	if err != nil {
		return nil, err
	}
	source := filepath.Join(archiveRoot, filepath.FromSlash(origin.Path))
	realSource, err := filepath.EvalSymlinks(source)
	if err != nil || !sameSkillDeploymentPath(realSource, source) {
		return nil, errBlocked
	}
	if err := checkCatalogCopy(ctx, source, true); err != nil {
		return nil, err
	}
	// The verifier reuses this archive. Check containment first to avoid
	// hashing a directory reached through an archive-provided link.
	_, modes, _, err := verifiedSkillArchive(ctx, origin, origin.Tree, archives)
	if err != nil {
		return nil, err
	}
	return installCatalogCopies(ctx, source, catalogSkillName(origin), origin, modes, roots), nil
}

func installCatalogCopies(ctx context.Context, source, name string, origin SkillOrigin, modes map[string]string, roots []skillRoot) []SkillCatalogInstallResult {
	skillMutationMu.Lock()
	defer skillMutationMu.Unlock()
	out := make([]SkillCatalogInstallResult, 0, len(roots))
	for _, root := range roots {
		result := SkillCatalogInstallResult{Tool: root.Tool, Path: filepath.Join(homeDir(), root.Rel, name)}
		if err := installCatalogCopy(ctx, source, name, origin, modes, root, &result); err != nil {
			result.Error = err.Error()
		}
		out = append(out, result)
	}
	return out
}

func installCatalogCopy(ctx context.Context, source, name string, origin SkillOrigin, modes map[string]string, root skillRoot, result *SkillCatalogInstallResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := skillDeploymentRoot(root)
	if err != nil {
		return err
	}
	result.Path = filepath.Join(dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	identity, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if _, err := skillDeploymentRoot(root); err != nil {
		return err
	}
	if _, err := os.Lstat(result.Path); err == nil {
		return errCatalogDestinationExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staging, err := os.MkdirTemp(dir, ".cleanzo-skill-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	prepared := filepath.Join(staging, name)
	if origin.Repo == "" {
		input, err := os.OpenRoot(source)
		if err != nil {
			return err
		}
		defer input.Close()
		if err := os.CopyFS(prepared, input.FS()); err != nil {
			return err
		}
		if err := checkCatalogCopy(ctx, prepared, false); err != nil {
			return err
		}
	} else if err := copyTree(source, prepared); err != nil {
		return err
	}
	if origin.Tree != "" {
		tree, err := gitTreeHash(prepared, modes)
		if err != nil {
			return err
		}
		if tree != origin.Tree {
			return errSkillMismatch
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := skillDeploymentRoot(root); err != nil {
		return err
	}
	if !sameSkillDirectoryIdentity(dir, identity) {
		return errSkillDiverged
	}
	if err := publishCatalogSkill(prepared, result.Path); err != nil {
		return err
	}
	if origin.Repo == "" {
		return nil
	}
	if runtime.GOOS != "windows" {
		modes = nil
	}
	if err := saveSkillRecord(result.Path, skillRecord{Origin: origin, Modes: modes, UpdatedAt: skillNow().Unix()}); err != nil {
		return errors.Join(errors.New("installed-source-not-saved"), err)
	}
	return nil
}

// ImportCatalogSkillFolder copies a local skill into selected roots while preserving the original.
func (Cleaner) ImportCatalogSkillFolder(ctx context.Context, folder string, tools []string) ([]SkillCatalogInstallResult, error) {
	roots, err := catalogInstallRoots(tools)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(folder)
	if !filepath.IsAbs(folder) || !catalogNameOK(name) || !platformCleanPath(folder) {
		return nil, errSkillNotEntry
	}
	folder = resolvePath(folder)
	if err := checkCatalogCopy(ctx, folder, false); err != nil {
		return nil, err
	}
	return installCatalogCopies(ctx, folder, name, SkillOrigin{}, nil, roots), nil
}
