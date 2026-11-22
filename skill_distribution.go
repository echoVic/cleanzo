package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SkillDeployment describes the global paths Cleanzo inspected for a tool.
// Availability is independent of the direct entry because some tools also
// discover skills in shared or other tools' user directories.
type SkillDeployment struct {
	Tool         string `json:"tool"`
	Label        string `json:"label"`
	Path         string `json:"path"`
	State        string `json:"state"`        // absent, directory, link, broken, conflict, unreadable
	Availability string `json:"availability"` // absent, direct, shared, unknown
	Shared       bool   `json:"shared"`
	SharedPath   string `json:"sharedPath"`
	LinkTarget   string `json:"linkTarget"`
	Source       string `json:"source"` // validated direct entry that can be deployed
	CanDeploy    bool   `json:"canDeploy"`
	CanRepair    bool   `json:"canRepair"`
}

// SkillDeployRequest applies an explicit link or copy, or repairs one observed broken link.
type SkillDeployRequest struct {
	Name           string `json:"name"`
	Source         string `json:"source"`
	Tool           string `json:"tool"`
	Mode           string `json:"mode"` // link or copy
	Repair         bool   `json:"repair"`
	PreviousTarget string `json:"previousTarget"`
}

// Additional documented global search roots under the default tool configuration.
// Leave availability unknown for unverified tools and custom search paths.
var skillDistributionSharedRoots = map[string][]string{
	"claude":   {},
	"codex":    {"agents"},
	"gemini":   {"agents"},
	"opencode": {"agents", "claude"},
	"grok":     {"agents"},
	"pi":       {"agents"},
}

// ListSkillDistribution inspects one exact folder name across the supported user directories.
func (Cleaner) ListSkillDistribution(ctx context.Context, name string) ([]SkillDeployment, error) {
	if !catalogNameOK(name) {
		return nil, errSkillNotEntry
	}
	rows := make([]SkillDeployment, 0, len(skillRoots))
	byTool := make(map[string]SkillDeployment, len(skillRoots))
	for _, tool := range skillRoots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		root := filepath.Join(homeDir(), tool.Rel)
		path := filepath.Join(root, name)
		state, target := inspectSkillDeployment(path)
		row := SkillDeployment{Tool: tool.Tool, Label: tool.Name, Path: path, State: state, LinkTarget: target, Availability: "unknown"}
		if state == "directory" || state == "link" {
			row.Availability = "direct"
			if _, err := skillDeploymentSource(name, path); err == nil {
				row.Source = path
			}
		}
		if _, err := skillDeploymentRoot(tool); err == nil {
			row.CanDeploy = state == "absent"
			row.CanRepair = state == "broken" && runtime.GOOS == "darwin"
		}
		rows = append(rows, row)
		byTool[row.Tool] = row
	}
	for i := range rows {
		row := &rows[i]
		sharedRoots, known := skillDistributionSharedRoots[row.Tool]
		unknown := row.State == "unreadable"
		for _, tool := range sharedRoots {
			source := byTool[tool]
			unknown = unknown || source.State == "unreadable"
			if source.Availability == "direct" {
				row.Shared, row.SharedPath = true, source.Path
				break
			}
		}
		if row.Availability != "direct" {
			if row.Shared {
				row.Availability = "shared"
			} else if !unknown && (known || row.Tool == "agents") {
				row.Availability = "absent"
			}
		}
	}
	return rows, nil
}

func inspectSkillDeployment(path string) (state, target string) {
	entry, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "absent", ""
	}
	if err != nil {
		return "unreadable", ""
	}
	if entry.Mode()&fs.ModeSymlink != 0 {
		target, err = os.Readlink(path)
		if err != nil {
			return "unreadable", ""
		}
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return "broken", target
		}
		if err != nil {
			return "unreadable", target
		}
		if !info.IsDir() {
			return "conflict", target
		}
	} else if !entry.IsDir() || entry.Mode()&linkModes != 0 {
		return "conflict", ""
	}

	skillFile, err := os.Stat(filepath.Join(path, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return "conflict", target
	}
	if err != nil {
		return "unreadable", target
	}
	if !skillFile.Mode().IsRegular() {
		return "conflict", target
	}
	if target != "" {
		return "link", target
	}
	return "directory", ""
}

// skillDeploymentRoot permits only the known, physical root under the resolved home.
func skillDeploymentRoot(tool skillRoot) (string, error) {
	base := resolveUncached(homeDir())
	if !filepath.IsAbs(base) {
		return "", errSkillNotEntry
	}
	root := base
	for _, part := range strings.Split(filepath.Clean(filepath.FromSlash(tool.Rel)), string(filepath.Separator)) {
		root = filepath.Join(root, part)
		info, err := os.Lstat(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&linkModes != 0 {
			return "", errSkillNotEntry
		}
	}
	if !safeToClean(root) || !sameSkillDeploymentPath(root, resolveUncached(root)) {
		return "", errSkillNotEntry
	}
	return root, nil
}

// A source must be a current, same-name direct entry. A link may point to a
// same-name physical entry or to the configured CC Switch skill store.
func skillDeploymentSource(name, source string) (string, error) {
	if !filepath.IsAbs(source) || !sameSkillDeploymentPath(filepath.Base(source), name) {
		return "", errSkillNotEntry
	}
	found := false
	for _, tool := range skillRoots {
		root, err := skillDeploymentRoot(tool)
		if err != nil {
			continue
		}
		rawPath := filepath.Join(homeDir(), tool.Rel, name)
		if sameSkillDeploymentPath(source, filepath.Join(root, name)) || sameSkillDeploymentPath(source, rawPath) {
			found = true
			break
		}
	}
	if !found {
		return "", errSkillNotEntry
	}
	real, err := filepath.EvalSymlinks(source)
	if err != nil || !safeToClean(real) || !sameSkillDeploymentPath(filepath.Base(real), name) {
		return "", errSkillNotEntry
	}
	real = resolveUncached(real)
	if !within(strings.ToLower(real), strings.ToLower(resolveUncached(homeDir()))) {
		return "", errSkillNotEntry
	}
	allowed := false
	for _, tool := range skillRoots {
		root, err := skillDeploymentRoot(tool)
		if err == nil && directlyIn(root, real) {
			allowed = true
			break
		}
	}
	if !allowed && ccSwitchStoreHolds(real) {
		store := skillRoot{Rel: filepath.Join(".cc-switch", "skills")}
		root, err := skillDeploymentRoot(store)
		allowed = err == nil && directlyIn(root, real)
	}
	if !allowed {
		return "", errSkillNotEntry
	}
	file, err := os.Lstat(filepath.Join(real, "SKILL.md"))
	if err != nil || !file.Mode().IsRegular() {
		return "", errSkillNotEntry
	}
	return real, nil
}

func sameSkillDeploymentPath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// DeploySkill stages one complete entry and publishes to the chosen tool. No
// existing file, directory, or live link can be replaced by a normal deploy.
func (Cleaner) DeploySkill(ctx context.Context, req SkillDeployRequest) error {
	if !catalogNameOK(req.Name) || req.Mode != "link" && req.Mode != "copy" || req.Repair && (req.Mode != "link" || runtime.GOOS != "darwin") {
		return errSkillNotEntry
	}
	var tool skillRoot
	for _, current := range skillRoots {
		if current.Tool == req.Tool {
			tool = current
			break
		}
	}
	if tool.Tool == "" {
		return errSkillNotEntry
	}
	skillMutationMu.Lock()
	defer skillMutationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	source, err := skillDeploymentSource(req.Name, req.Source)
	if err != nil {
		return err
	}
	root, err := skillDeploymentRoot(tool)
	if err != nil {
		return err
	}
	destination := filepath.Join(root, req.Name)
	if sameSkillDeploymentPath(source, destination) {
		return errSkillNotEntry
	}
	if err := validateSkillDeploymentDestination(destination, req); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	parent, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if _, err := skillDeploymentRoot(tool); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(root, ".cleanzo-deploy-")
	if err != nil {
		return err
	}
	cleanupStage := true
	defer func() {
		if cleanupStage {
			_ = os.RemoveAll(stage)
		}
	}()
	replacement := filepath.Join(stage, req.Name)
	if req.Mode == "link" {
		err = makeSkillSymlink(source, replacement)
	} else {
		err = stageSkillDeploymentCopy(ctx, source, replacement)
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := skillDeploymentRoot(tool); err != nil {
		return err
	}
	if !sameSkillDirectoryIdentity(root, parent) {
		return errSkillDiverged
	}
	if current, err := skillDeploymentSource(req.Name, req.Source); err != nil || !sameSkillDeploymentPath(current, source) {
		return errSkillDiverged
	}
	if req.Repair {
		var keepStage bool
		keepStage, err = publishSkillLinkRepair(replacement, destination, req.PreviousTarget)
		cleanupStage = !keepStage
		return err
	}
	return publishSkillDeployment(replacement, destination)
}

func sameSkillDirectoryIdentity(path string, expected fs.FileInfo) bool {
	current, err := os.Lstat(path)
	return err == nil && current.IsDir() && os.SameFile(expected, current)
}

func validateSkillDeploymentDestination(destination string, req SkillDeployRequest) error {
	state, target := inspectSkillDeployment(destination)
	if req.Repair {
		if state != "broken" || req.PreviousTarget == "" || target != req.PreviousTarget {
			return errSkillDiverged
		}
		return nil
	}
	if state != "absent" {
		return fs.ErrExist
	}
	return nil
}

// Copy only portable files, directories, and internal relative links. Reject
// special files, absolute links and links through another user directory.
func stageSkillDeploymentCopy(ctx context.Context, source, destination string) error {
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		switch {
		case strings.EqualFold(entry.Name(), ".git"):
			return fmt.Errorf("skill Git metadata cannot be deployed as a copy")
		case entry.IsDir(), entry.Type().IsRegular():
			return nil
		case entry.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(target) && filepath.VolumeName(target) == "" && within(filepath.Join(filepath.Dir(path), target), source) {
				if resolved, err := filepath.EvalSymlinks(path); err == nil && within(resolved, source) {
					return nil
				}
			}
		}
		return fmt.Errorf("unsafe skill copy entry %q", filepath.Base(path))
	})
	if err != nil {
		return err
	}
	return copyTree(source, destination)
}
