package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	maxCatalogEntries     = 300
	catalogArchiveTimeout = 10 * time.Minute
)

// SkillCatalogEntry is one installable skill folder found in a GitHub tree.
type SkillCatalogEntry struct {
	Name      string      `json:"name"`
	Origin    SkillOrigin `json:"origin"`
	Installed []string    `json:"installed"` // roots where the same source is recorded
	SameName  []string    `json:"sameName"`  // roots where another source or an unknown copy has this name
}

// SkillDiscoveryEntry is a skills.sh search result. Its slug is a hint, not an install path.
type SkillDiscoveryEntry struct {
	Name      string   `json:"name"`
	Slug      string   `json:"slug"`
	Repo      string   `json:"repo"`
	Installs  int      `json:"installs"`
	Installed []string `json:"installed"`
	SameName  []string `json:"sameName"`
}

// SkillCatalogInstallResult reports each destination independently.
type SkillCatalogInstallResult struct {
	Tool  string `json:"tool"`
	Path  string `json:"path"`
	Error string `json:"error"`
}

var catalogOwner = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
var catalogRepositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

func catalogRepo(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
			return "", errors.New("invalid-repository")
		}
		value = strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/")
	}
	owner, repo, ok := strings.Cut(strings.TrimSuffix(value, ".git"), "/")
	if !ok || !catalogOwner.MatchString(owner) || !catalogRepositoryName.MatchString(repo) || repo == "." || repo == ".." {
		return "", errors.New("invalid-repository")
	}
	return owner + "/" + repo, nil
}

func catalogRef(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "HEAD" {
		return "", nil
	}
	if len(value) > 200 || strings.HasPrefix(value, "-") || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.ContainsAny(value, "~^:?*[\\") || strings.ContainsFunc(value, unicode.IsControl) || strings.ContainsFunc(value, unicode.IsSpace) || strings.HasSuffix(value, ".") {
		return "", errors.New("invalid-ref")
	}
	if value != "" {
		for _, part := range strings.Split(value, "/") {
			if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
				return "", errors.New("invalid-ref")
			}
		}
	}
	return value, nil
}

func catalogRelativePath(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 2000 || strings.ContainsAny(value, `\:`) || strings.ContainsFunc(value, unicode.IsControl) {
		return errors.New("invalid-skill-path")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || strings.HasPrefix(part, ".") {
			return errors.New("invalid-skill-path")
		}
	}
	return nil
}

func catalogCoordinates(repo, ref, dir string) (SkillOrigin, error) {
	repo, err := catalogRepo(repo)
	if err != nil {
		return SkillOrigin{}, err
	}
	ref, err = catalogRef(ref)
	if err != nil {
		return SkillOrigin{}, err
	}
	dir = strings.TrimSpace(dir)
	if err := catalogRelativePath(dir); err != nil {
		return SkillOrigin{}, err
	}
	return SkillOrigin{Kind: originCleanzo, Repo: repo, Ref: ref, Path: dir}, nil
}

func catalogSkillName(origin SkillOrigin) string {
	if origin.Path != "" {
		return path.Base(origin.Path)
	}
	return path.Base(origin.Repo)
}

func catalogNameOK(name string) bool {
	if name == "" || len(name) > 200 || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") || strings.TrimSpace(name) != name || strings.ContainsAny(name, `/\:<>|?*"`) || strings.ContainsFunc(name, unicode.IsControl) {
		return false
	}
	// These device names are reserved on Windows, even with an extension.
	stem, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "COM0", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT0", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9", "COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³":
		return false
	}
	return true
}

func catalogTree(ctx context.Context, origin SkillOrigin) (map[string]string, map[string]string, error) {
	trees, modes, err := fetchGitHubTree(ctx, origin.Repo, origin.Ref)
	if err != nil {
		return nil, nil, err
	}
	if trees["\x00truncated"] != "" {
		return nil, nil, errors.New("truncated")
	}
	return trees, modes, nil
}

type catalogLocal struct {
	name   string
	tool   string
	origin SkillOrigin
}

func catalogLocalCopies(ctx context.Context) ([]catalogLocal, error) {
	skills, err := skillEntries(ctx)
	if err != nil {
		return nil, err
	}
	origins := skillOrigins(ctx, skills)
	local := make([]catalogLocal, 0, len(skills))
	for _, skill := range skills {
		for _, cp := range skill.Copies {
			local = append(local, catalogLocal{skill.Name, cp.Tool, origins[cp.Real]})
		}
	}
	return local, ctx.Err()
}

func catalogMatches(local []catalogLocal, name string, origin SkillOrigin, exact bool) (installed, sameName []string) {
	known, collisions := map[string]bool{}, map[string]bool{}
	for _, item := range local {
		sameRepo := strings.EqualFold(item.origin.Repo, origin.Repo)
		sameSource := sameRepo && item.origin.Path == origin.Path && item.origin.Ref == origin.Ref
		if !exact {
			sameSource = sameRepo && (strings.EqualFold(catalogSkillName(item.origin), name) || strings.EqualFold(item.name, name) && item.origin.Path == "")
		}
		switch {
		case sameSource:
			known[item.tool] = true
		case strings.EqualFold(item.name, name):
			collisions[item.tool] = true
		}
	}
	installed, sameName = []string{}, []string{}
	for _, root := range skillRoots {
		if known[root.Tool] {
			installed = append(installed, root.Tool)
		}
		if collisions[root.Tool] {
			sameName = append(sameName, root.Tool)
		}
	}
	return installed, sameName
}

// ListSkillInstallTargets lists supported roots, including roots not yet created.
func (Cleaner) ListSkillInstallTargets() []SkillRoot {
	out := make([]SkillRoot, 0, len(skillRoots))
	for _, root := range skillRoots {
		out = append(out, SkillRoot{Tool: root.Tool, Name: root.Name, Path: filepath.Join(homeDir(), root.Rel)})
	}
	return out
}

// BrowseSkillRepository lists regular SKILL.md entries without downloading or executing them.
func (Cleaner) BrowseSkillRepository(ctx context.Context, repo, ref, subdir string) ([]SkillCatalogEntry, error) {
	origin, err := catalogCoordinates(repo, ref, subdir)
	if err != nil {
		return nil, err
	}
	trees, modes, err := catalogTree(ctx, origin)
	if err != nil {
		return nil, err
	}
	local, err := catalogLocalCopies(ctx)
	if err != nil {
		return nil, err
	}
	out := []SkillCatalogEntry{}
	for file := range modes {
		if path.Base(file) != "SKILL.md" {
			continue
		}
		entry := origin
		entry.Path = path.Dir(file)
		if entry.Path == "." {
			entry.Path = ""
		}
		if sub := origin.Path; sub != "" && entry.Path != sub && !strings.HasPrefix(entry.Path, sub+"/") {
			continue
		}
		entry.Tree = trees[entry.Path]
		name := catalogSkillName(entry)
		if entry.Tree == "" || catalogRelativePath(entry.Path) != nil || !catalogNameOK(name) {
			continue
		}
		installed, sameName := catalogMatches(local, name, entry, true)
		out = append(out, SkillCatalogEntry{Name: name, Origin: entry, Installed: installed, SameName: sameName})
		if len(out) > maxCatalogEntries {
			return nil, errors.New("too-many-skills")
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Origin.Path < out[j].Origin.Path })
	return out, nil
}

func catalogExpected(ctx context.Context, chosen SkillOrigin) (SkillOrigin, map[string]string, error) {
	origin, err := catalogCoordinates(chosen.Repo, chosen.Ref, chosen.Path)
	if err != nil {
		return SkillOrigin{}, nil, err
	}
	trees, modes, err := catalogTree(ctx, origin)
	if err != nil {
		return SkillOrigin{}, nil, err
	}
	origin.Tree = trees[origin.Path]
	if origin.Tree == "" || modes[path.Join(origin.Path, "SKILL.md")] == "" || !catalogNameOK(catalogSkillName(origin)) {
		return SkillOrigin{}, nil, errSkillNotFound
	}
	if chosen.Tree == "" || chosen.Tree != origin.Tree {
		return SkillOrigin{}, nil, errSkillMismatch
	}
	relativeModes := map[string]string{}
	prefix := origin.Path
	if prefix != "" {
		prefix += "/"
	}
	for file, mode := range modes {
		if strings.HasPrefix(file, prefix) {
			relativeModes[strings.TrimPrefix(file, prefix)] = mode
		}
	}
	return origin, relativeModes, nil
}

// BindCatalogSkillSource records a verified upstream tree and leaves local content in place.
func (Cleaner) BindCatalogSkillSource(ctx context.Context, folder string, chosen SkillOrigin) (SkillUpdate, error) {
	if !filepath.IsAbs(folder) {
		return SkillUpdate{}, errSkillNotEntry
	}
	folder = resolvePath(folder)
	if err := skillFolderOK(folder, ctx); err != nil {
		return SkillUpdate{}, err
	}
	origin, modes, err := catalogExpected(ctx, chosen)
	if err != nil {
		return SkillUpdate{}, err
	}
	skillMutationMu.Lock()
	defer skillMutationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return SkillUpdate{}, err
	}
	if err := skillFolderOK(folder, ctx); err != nil {
		return SkillUpdate{}, err
	}
	if _, err := os.Lstat(filepath.Join(folder, ".git")); !errors.Is(err, os.ErrNotExist) {
		return SkillUpdate{}, errors.New("git-owned")
	}
	if info, err := os.Lstat(filepath.Join(folder, "SKILL.md")); err != nil || !info.Mode().IsRegular() {
		return SkillUpdate{}, errSkillNotEntry
	}
	local, err := gitTreeHash(folder, modes)
	if err != nil {
		return SkillUpdate{}, err
	}
	if runtime.GOOS != "windows" {
		modes = nil
	}
	if err := saveSkillRecord(folder, skillRecord{Origin: origin, Modes: modes, UpdatedAt: skillNow().Unix()}); err != nil {
		return SkillUpdate{}, err
	}
	update := SkillUpdate{Path: folder, Name: filepath.Base(folder), Origin: origin, Local: local, Remote: origin.Tree, Status: "current", CheckedAt: skillNow().Unix()}
	if local != origin.Tree {
		update.Status, update.Modified = "update", true
	}
	return update, nil
}

// SearchSkillCatalog searches skills.sh and annotates results with already visible local copies.
func (Cleaner) SearchSkillCatalog(ctx context.Context, query string) ([]SkillDiscoveryEntry, error) {
	return searchSkillCatalog(ctx, "https://skills.sh/api/search", query)
}

func searchSkillCatalog(ctx context.Context, endpoint, query string) ([]SkillDiscoveryEntry, error) {
	query = strings.TrimSpace(query)
	if len(query) < 2 || len(query) > 200 {
		return nil, errors.New("invalid-query")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	params := url.Values{"q": {query}, "limit": {"30"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Cleanzo")
	response, err := skillUpdateHTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skills.sh HTTP %d", response.StatusCode)
	}
	var payload struct {
		Skills []struct {
			Name     string `json:"name"`
			Slug     string `json:"skillId"`
			Source   string `json:"source"`
			Installs int    `json:"installs"`
		} `json:"skills"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	local, err := catalogLocalCopies(ctx)
	if err != nil {
		return nil, err
	}
	out := []SkillDiscoveryEntry{}
	seen := map[string]bool{}
	for _, entry := range payload.Skills {
		repo, err := catalogRepo(entry.Source)
		key := strings.ToLower(repo + "/" + entry.Slug)
		if err != nil || !catalogNameOK(entry.Slug) || seen[key] {
			continue
		}
		seen[key] = true
		name := strings.TrimSpace(entry.Name)
		if name == "" || len(name) > 200 || strings.ContainsFunc(name, unicode.IsControl) {
			name = entry.Slug
		}
		installed, sameName := catalogMatches(local, entry.Slug, SkillOrigin{Repo: repo}, false)
		out = append(out, SkillDiscoveryEntry{Name: name, Repo: repo, Slug: entry.Slug, Installs: entry.Installs, Installed: installed, SameName: sameName})
		if len(out) == 30 {
			break
		}
	}
	return out, nil
}
