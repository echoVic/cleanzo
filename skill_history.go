package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// SkillVersion describes a saved copy that can be restored from the Skills page.
type SkillVersion struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Name   string `json:"name"`
	Action string `json:"action"`
	At     int64  `json:"at"`
	Exists bool   `json:"exists"`
}

type skillVersionState struct {
	SkillVersion
	Hash   string            `json:"hash"`
	Origin SkillOrigin       `json:"origin"`
	Modes  map[string]string `json:"modes,omitempty"`
}

const maxSkillVersionMeta = 16 << 20

var skillVersionID = regexp.MustCompile(`^version-[0-9]+$`)

func skillHistoryDir() string {
	return filepath.Join(filepath.Dir(configPath()), "skill-versions")
}

// createSkillVersion takes a private copy before an explicit path mutation.
func createSkillVersion(ctx context.Context, folder, action string, origin SkillOrigin) (skillVersionState, string, error) {
	var state skillVersionState
	root := skillHistoryDir()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return state, "", err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return state, "", errSkillNotEntry
	}
	version, err := os.MkdirTemp(root, "version-")
	if err != nil {
		return state, "", err
	}
	contents := filepath.Join(version, "contents")
	if err := copySkillVersion(ctx, folder, contents); err != nil {
		_ = os.RemoveAll(version)
		return state, "", err
	}
	modes := loadSkillRecords()[folder].Modes
	digest, err := fingerprintSkillVersion(ctx, contents)
	if err != nil {
		_ = os.RemoveAll(version)
		return state, "", err
	}
	state = skillVersionState{SkillVersion: SkillVersion{ID: filepath.Base(version), Path: folder, Name: filepath.Base(folder), Action: action, At: time.Now().UnixMilli()}, Origin: origin, Modes: modes, Hash: digest}
	data, err := json.Marshal(state)
	if err == nil && len(data) > maxSkillVersionMeta {
		err = errSkillTooLarge
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(version, "meta.json"), data, 0o600)
	}
	if err != nil {
		_ = os.RemoveAll(version)
		return skillVersionState{}, "", err
	}
	return state, contents, nil
}

func discardSkillVersion(state skillVersionState) {
	if skillVersionID.MatchString(state.ID) {
		_ = os.RemoveAll(filepath.Join(skillHistoryDir(), state.ID))
	}
}

func readSkillVersion(id string) (skillVersionState, string, error) {
	var state skillVersionState
	if !skillVersionID.MatchString(id) {
		return state, "", errSkillNotEntry
	}
	root := skillHistoryDir()
	dir := filepath.Join(root, id)
	contents := filepath.Join(dir, "contents")
	for _, path := range []string{root, dir, contents} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			return state, "", errSkillNotEntry
		}
	}
	meta := filepath.Join(dir, "meta.json")
	info, err := os.Lstat(meta)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSkillVersionMeta {
		return state, "", errSkillNotEntry
	}
	data, err := os.ReadFile(meta)
	if err != nil {
		return state, "", err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return state, "", err
	}
	if state.ID != id || !filepath.IsAbs(state.Path) || state.Path != filepath.Clean(state.Path) || state.Name != filepath.Base(state.Path) {
		return state, "", errSkillNotEntry
	}
	if _, err := os.Lstat(state.Path); err == nil {
		state.Exists = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return state, "", err
	}
	return state, contents, nil
}

// ListSkillHistory lists saved versions, optionally for a single resolved folder.
func (Cleaner) ListSkillHistory(folder string) ([]SkillVersion, error) {
	out := []SkillVersion{}
	if folder != "" {
		folder = resolvePath(filepath.Clean(folder))
	}
	root := skillHistoryDir()
	if info, err := os.Lstat(root); errors.Is(err, fs.ErrNotExist) {
		return out, nil
	} else if err != nil {
		return nil, err
	} else if !info.IsDir() {
		return nil, errSkillNotEntry
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		state, _, err := readSkillVersion(entry.Name())
		if err == nil && (folder == "" || samePath(folder, state.Path)) {
			out = append(out, state.SkillVersion)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At == out[j].At {
			return out[i].ID > out[j].ID
		}
		return out[i].At > out[j].At
	})
	return out, nil
}

func samePath(a, b string) bool { return within(a, b) && within(b, a) }

// RestoreSkillVersion restores a saved version to its original path. An explicit
// replace confirmation is required for an occupied path, and current contents
// are saved in history before being replaced.
func (Cleaner) RestoreSkillVersion(ctx context.Context, id string, replace bool) error {
	skillMutationMu.Lock()
	defer skillMutationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	state, contents, err := readSkillVersion(id)
	if err != nil {
		return err
	}
	if err := validSkillRestorePath(state.Path); err != nil {
		return err
	}
	digest, err := fingerprintSkillVersion(ctx, contents)
	if err != nil {
		return err
	}
	if digest != state.Hash {
		return errSkillMismatch
	}
	if state.Exists {
		if !replace {
			return errors.New("restore-occupied")
		}
		if err := skillFolderOK(state.Path, ctx); err != nil {
			return err
		}
		// Git history and its working tree are owned by Git.
		if _, gitErr := os.Lstat(filepath.Join(state.Path, ".git")); !errors.Is(gitErr, fs.ErrNotExist) {
			return errors.New("git-skill-use-git")
		}
		current := skillHistoryOrigin(ctx, state.Path)
		validate := func() error { _, err := saveBeforeSkillReplace(ctx, state.Path, "restore", current); return err }
		if err := swapSkillFolder(ctx, state.Path, contents, validate); err != nil {
			return err
		}
	} else {
		parent, err := os.MkdirTemp(filepath.Dir(state.Path), ".cleanzo-skill-restore-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(parent)
		staged := filepath.Join(parent, "contents")
		if err := copySkillVersion(ctx, contents, staged); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validSkillRestorePath(state.Path); err != nil {
			return err
		}
		if err := publishSkillRestore(staged, state.Path); err != nil {
			return err
		}
	}
	return saveSkillRecord(state.Path, skillRecord{Origin: state.Origin, UpdatedAt: skillNow().Unix(), Modes: state.Modes})
}

func validSkillRestorePath(folder string) error {
	// Observe the parent; resolve the entry itself only through the normal skill
	// guard after confirming that a real directory is in place.
	resolved := filepath.Join(resolveUncached(filepath.Dir(folder)), filepath.Base(folder))
	if !samePath(resolved, folder) || !filepath.IsAbs(folder) || strings.HasPrefix(filepath.Base(folder), ".") || !safeToClean(filepath.Dir(folder)) || !safeResolved(strings.ToLower(folder)) {
		return errSkillNotEntry
	}
	if _, _, ok := skillRootOf(folder); ok {
		return nil
	}
	if ccSwitchStoreHolds(folder) {
		return nil
	}
	if info, err := os.Lstat(folder); err == nil && info.IsDir() && skillFolderOK(folder) == nil {
		return nil
	}
	return errSkillNotEntry
}

func saveBeforeSkillReplace(ctx context.Context, folder, action string, origin SkillOrigin) (skillVersionState, error) {
	guard := skillArchiveGuard()
	if runtime.GOOS == "windows" {
		guard = skillGuard()
	}
	if guard.failed {
		return skillVersionState{}, errSkillUsageUnknown
	}
	if guard.inUse(folder) {
		return skillVersionState{}, errSkillInUse
	}
	state, _, err := createSkillVersion(ctx, folder, action, origin)
	if err != nil {
		return state, fmt.Errorf("save skill version: %w", err)
	}
	return state, nil
}

func skillHistoryOrigin(ctx context.Context, folder string) SkillOrigin {
	// Resolve origin using the same registered entries as the Skills page. An
	// unlisted or unreadable source does not make a local backup unrestorable.
	entries, err := skillEntries(ctx)
	if err != nil {
		return SkillOrigin{}
	}
	return skillOrigins(ctx, entries)[folder]
}
