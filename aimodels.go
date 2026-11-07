package main

import (
	"context"
	"fmt"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// Local AI models: Ollama, Hugging Face and LM Studio
// ---------------------------------------------------------------------------
//
// The AI Space page's Models tab lists the model weights the local runtimes
// keep on disk, one entry per model, with the bytes the model accounts for
// and when it last changed. Nothing is preselected and cleaning only ever
// moves things to the Trash; this page never deletes.
//
// Ollama keeps a tiny manifest per model under manifests/ and the weights as
// blobs shared between models: a manifest names its blobs by digest, and a
// digest belongs to at most one model, so the model sizes add up to the
// blobs' total size. Moving a manifest alone would leave its blobs behind,
// so cleaning a model also reclaims the blobs no remaining manifest names.
//
// Hugging Face's hub keeps one folder per repository: the weights live under
// blobs/ and snapshots/ points at them with symlinks, which is why only
// blobs/ is measured. LM Studio keeps one folder per model two levels down.
//
// Last is when a model last changed on disk, never when a runtime last used
// it: no runtime records that reliably, and the page must not guess.

// aiModelKindManifest marks a model whose cleaning must reclaim blobs.
const aiModelKindManifest = "manifest"

// AIModelEntry is one model as the page lists it.
type AIModelEntry struct {
	Name  string   `json:"name"`  // model name, such as qwen3-coder:30b or org/name
	Path  string   `json:"path"`  // absolute path: the unit cleaning moves
	Size  int64    `json:"size"`  // bytes the model alone accounts for, shared blobs once
	Last  int64    `json:"last"`  // newest modification, unix seconds; 0 when unknown
	Kind  string   `json:"kind"`  // aiModelKindManifest when blobs must go with the model
	Blobs []string `json:"blobs"` // for a manifest: the blobs this model accounts for
}

// AIModelSource is one runtime's model folder as the page lists it.
type AIModelSource struct {
	ID        string         `json:"id"`        // "ollama", "huggingface" or "lmstudio"
	Name      string         `json:"name"`      // display name
	Path      string         `json:"path"`      // absolute root folder
	Installed bool           `json:"installed"` // the root folder exists
	Size      int64          `json:"size"`      // bytes the models account for
	Files     int64          `json:"files"`     // files those bytes are in
	Last      int64          `json:"last"`      // newest model modification, unix seconds
	Models    []AIModelEntry `json:"models"`
}

// AIModelsResult is what ScanAIModels returns.
type AIModelsResult struct {
	Sources   []AIModelSource `json:"sources"`
	ScannedAt int64           `json:"scannedAt"`
}

// AIModelSourceInfo is one supported runtime as the sidebar lists it.
type AIModelSourceInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
}

// AIModelSelection picks the models of one runtime to clean.
type AIModelSelection struct {
	Source string   `json:"source"`
	Models []string `json:"models"` // AIModelEntry.Path values
}

// aiModelSource describes one local AI runtime and where it keeps its models.
type aiModelSource struct {
	ID   string
	Name string
	// Root is the folder the runtime keeps models in.
	Root func() string
}

// aiModelSources is the table of supported runtimes. The folders follow each
// runtime's own layout: Ollama's manifests and blobs, Hugging Face's hub,
// and LM Studio's publisher/model folders.
var aiModelSources = []aiModelSource{
	{ID: "ollama", Name: "Ollama", Root: func() string {
		return filepath.Join(homeDir(), ".ollama", "models")
	}},
	{ID: "huggingface", Name: "Hugging Face", Root: func() string {
		return filepath.Join(homeDir(), ".cache", "huggingface", "hub")
	}},
	{ID: "lmstudio", Name: "LM Studio", Root: func() string {
		return filepath.Join(homeDir(), ".lmstudio", "models")
	}},
}

// errAIModelBlocked refuses a path cleaning must not move.
var errAIModelBlocked = errors.New("blocked")

// aiModelSourceByID looks a runtime up in the table.
func aiModelSourceByID(id string) (aiModelSource, bool) {
	for _, src := range aiModelSources {
		if src.ID == id {
			return src, true
		}
	}
	return aiModelSource{}, false
}

// aiModelRoot is a runtime's model folder, or "" without a home folder.
func aiModelRoot(src aiModelSource) string {
	root := src.Root()
	if root == "" || !filepath.IsAbs(root) {
		return ""
	}
	return root
}

// aiModelRootDir is a runtime's model folder when it exists.
func aiModelRootDir(src aiModelSource) string {
	root := aiModelRoot(src)
	if root == "" {
		return ""
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return ""
	}
	return root
}

// ListAIModelSources reports which runtimes keep models on disk, without
// measuring anything, so the sidebar can show one entry per installed
// runtime at start.
func (Cleaner) ListAIModelSources() []AIModelSourceInfo {
	out := []AIModelSourceInfo{}
	for _, src := range aiModelSources {
		out = append(out, AIModelSourceInfo{ID: src.ID, Name: src.Name, Installed: aiModelRootDir(src) != ""})
	}
	return out
}

// ScanAIModels measures the models of every supported runtime.
func (Cleaner) ScanAIModels(ctx context.Context) (AIModelsResult, error) {
	res := AIModelsResult{Sources: []AIModelSource{}}
	for _, src := range aiModelSources {
		if err := ctx.Err(); err != nil {
			return AIModelsResult{}, err
		}
		s, err := scanAIModelSource(ctx, src)
		if err != nil {
			return AIModelsResult{}, err
		}
		res.Sources = append(res.Sources, s)
	}
	res.ScannedAt = aiNow().Unix()
	return res, nil
}

// scanAIModelSource measures one runtime's models.
func scanAIModelSource(ctx context.Context, src aiModelSource) (AIModelSource, error) {
	out := AIModelSource{ID: src.ID, Name: src.Name, Path: aiModelRoot(src), Models: []AIModelEntry{}}
	root := aiModelRootDir(src)
	if root == "" {
		return out, nil
	}
	out.Path, out.Installed = root, true
	var err error
	switch src.ID {
	case "ollama":
		err = scanOllamaModels(ctx, root, &out)
	case "huggingface":
		err = scanHuggingFaceModels(ctx, root, &out)
	case "lmstudio":
		err = scanLMStudioModels(ctx, root, &out)
	}
	if err != nil {
		return AIModelSource{}, err
	}
	// The sources' sizes are the models' sizes, so a blob shared by several
	// models is counted once and the numbers match what cleaning can free.
	for _, m := range out.Models {
		out.Size += m.Size
		out.Last = max(out.Last, m.Last)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Ollama: a manifest per model, the weights as shared blobs
// ---------------------------------------------------------------------------

// aiOllamaManifest is one model's manifest file.
type aiOllamaManifest struct {
	path  string   // absolute path of the manifest file
	name  string   // model name, such as qwen3-coder:30b
	blobs []string // absolute blob paths the manifest names, each once
}

// ollamaManifestJSON is the part of a manifest that names its blobs.
type ollamaManifestJSON struct {
	Config struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		Digest string `json:"digest"`
	} `json:"layers"`
}

// ollamaManifests reads every model manifest under root/manifests, sorted by
// path so the blob ownership below is the same on every scan.
func ollamaManifests(ctx context.Context, root string) ([]aiOllamaManifest, error) {
	dir := filepath.Join(root, "manifests")
	out := []aiOllamaManifest{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		// Manifest files sit below the registry and namespace folders.
		if rel, err := filepath.Rel(dir, p); err != nil || !strings.Contains(rel, string(filepath.Separator)) {
			return nil
		}
		m, ok := readOllamaManifest(root, p)
		if ok {
			out = append(out, m)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// readOllamaManifest reads one manifest: its name and the blobs it names,
// config first as Ollama stores it. Files that are not manifests are skipped.
func readOllamaManifest(root, path string) (aiOllamaManifest, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return aiOllamaManifest{}, false
	}
	var raw ollamaManifestJSON
	if json.Unmarshal(data, &raw) != nil {
		return aiOllamaManifest{}, false
	}
	m := aiOllamaManifest{path: path, name: ollamaModelName(root, path), blobs: []string{}}
	seen := map[string]bool{}
	add := func(digest string) {
		name, ok := ollamaBlobName(digest)
		if !ok {
			return
		}
		blob := filepath.Join(root, "blobs", name)
		if seen[blob] {
			return
		}
		seen[blob] = true
		m.blobs = append(m.blobs, blob)
	}
	add(raw.Config.Digest)
	for _, layer := range raw.Layers {
		add(layer.Digest)
	}
	// Without a config or layer digest the file is JSON in the manifests
	// folder, not a model: listing it would offer its file for cleaning.
	return m, len(m.blobs) > 0
}

// ollamaBlobName turns a digest (sha256:...) into a blob file name
// (sha256-...). Anything that is not an algorithm and a hex hash is refused:
// a digest must never name a path outside the blobs folder.
func ollamaBlobName(digest string) (string, bool) {
	algo, hash, ok := strings.Cut(digest, ":")
	if !ok || algo == "" || hash == "" {
		return "", false
	}
	for _, r := range algo {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return "", false
		}
	}
	for _, r := range hash {
		if (r < 'a' || r > 'f') && (r < '0' || r > '9') {
			return "", false
		}
	}
	return algo + "-" + hash, true
}

// ollamaModelName names a manifest by its path below manifests/:
// registry.ollama.ai/library/qwen3-coder/30b becomes qwen3-coder:30b.
func ollamaModelName(root, path string) string {
	rel, err := filepath.Rel(filepath.Join(root, "manifests"), path)
	if err != nil {
		return filepath.Base(path)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) >= 2 {
		return parts[len(parts)-2] + ":" + parts[len(parts)-1]
	}
	return filepath.Base(path)
}

// ollamaOwnedBlobs assigns every blob to the first manifest in path order
// that names it. A blob shared between models is therefore counted once, and
// the model sizes add up to the blobs' total size.
func ollamaOwnedBlobs(manifests []aiOllamaManifest) map[string][]string {
	owned := make(map[string][]string, len(manifests))
	claimed := map[string]bool{}
	for _, m := range manifests {
		for _, blob := range m.blobs {
			if claimed[blob] {
				continue
			}
			claimed[blob] = true
			owned[m.path] = append(owned[m.path], blob)
		}
	}
	return owned
}

// scanOllamaModels lists one entry per manifest, sized by the blobs it owns.
func scanOllamaModels(ctx context.Context, root string, out *AIModelSource) error {
	manifests, err := ollamaManifests(ctx, root)
	if err != nil {
		return err
	}
	owned := ollamaOwnedBlobs(manifests)
	for _, m := range manifests {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := AIModelEntry{Name: m.name, Path: m.path, Kind: aiModelKindManifest, Blobs: []string{}}
		for _, blob := range owned[m.path] {
			info, err := os.Lstat(blob)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			entry.Blobs = append(entry.Blobs, blob)
			entry.Size += onDisk(info)
			entry.Last = max(entry.Last, info.ModTime().Unix())
			out.Files++
		}
		out.Models = append(out.Models, entry)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Hugging Face and LM Studio: one folder per model
// ---------------------------------------------------------------------------

// scanHuggingFaceModels lists one entry per models--* repository folder.
func scanHuggingFaceModels(ctx context.Context, root string, out *AIModelSource) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, "models--") {
			continue
		}
		dir := filepath.Join(root, name)
		size, files, last := measureAIModelDir(ctx, dir, filepath.Join(dir, "blobs"))
		display := strings.ReplaceAll(strings.TrimPrefix(name, "models--"), "--", "/")
		out.Models = append(out.Models, AIModelEntry{Name: display, Path: dir, Size: size, Last: last, Blobs: []string{}})
		out.Files += files
	}
	return nil
}

// scanLMStudioModels lists one entry per publisher/model folder.
func scanLMStudioModels(ctx context.Context, root string, out *AIModelSource) error {
	publishers, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, pub := range publishers {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !pub.IsDir() || strings.HasPrefix(pub.Name(), ".") {
			continue
		}
		pubDir := filepath.Join(root, pub.Name())
		models, err := os.ReadDir(pubDir)
		if err != nil {
			continue
		}
		for _, m := range models {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !m.IsDir() || strings.HasPrefix(m.Name(), ".") {
				continue
			}
			dir := filepath.Join(pubDir, m.Name())
			size, files, last := measureAIModelDir(ctx, dir, dir)
			out.Models = append(out.Models, AIModelEntry{
				Name: strings.Join([]string{pub.Name(), m.Name()}, "/"), Path: dir, Size: size, Last: last, Blobs: []string{},
			})
			out.Files += files
		}
	}
	return nil
}

// measureAIModelDir sums a model folder's regular files without following
// links. When sizeRoot is an existing folder only files inside it count
// toward size and files, as Hugging Face keeps the weights under blobs/ and
// points at them from snapshots/ with symlinks.
func measureAIModelDir(ctx context.Context, dir, sizeRoot string) (size, files, last int64) {
	inside := dir
	if info, err := os.Lstat(sizeRoot); err == nil && info.IsDir() {
		inside = sizeRoot
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		last = max(last, info.ModTime().Unix())
		if within(p, inside) {
			size += onDisk(info)
			files++
		}
		return nil
	})
	return size, files, last
}

// ---------------------------------------------------------------------------
// Cleaning
// ---------------------------------------------------------------------------

// CleanAIModels moves the selected models to the Trash. A model's blobs only
// go when no manifest left behind still names them, so a digest shared with
// a model that stays does not break it. Paths outside a runtime's own model
// folder are refused, and nothing is ever deleted.
func (Cleaner) CleanAIModels(ctx context.Context, sels []AIModelSelection) (AICleanResult, error) {
	res := AICleanResult{Parts: []AIPartResult{}}
	// Each folder is measured once per run, just before its first model
	// moves; every target is statted again right before it moves.
	snapshots := map[string]AIModelSource{}
	for _, sel := range sels {
		src, ok := aiModelSourceByID(sel.Source)
		if !ok {
			res.Parts = append(res.Parts, AIPartResult{Tool: sel.Source, Skipped: "unknown", Failed: []string{}})
			continue
		}
		for _, model := range sel.Models {
			if ctx.Err() != nil {
				res.Parts = append(res.Parts, AIPartResult{Tool: src.ID, Part: model, Skipped: "cancelled", Failed: []string{}})
				continue
			}
			r := cleanAIModel(ctx, src, snapshots, model)
			res.Freed += r.Freed
			res.Parts = append(res.Parts, r)
		}
	}
	return res, nil
}

// cleanAIModel cleans one selected model path.
func cleanAIModel(ctx context.Context, src aiModelSource, snapshots map[string]AIModelSource, modelPath string) AIPartResult {
	r := AIPartResult{Tool: src.ID, Part: modelPath, Failed: []string{}}
	root := aiModelRootDir(src)
	if root == "" {
		r.Skipped = "missing"
		return r
	}
	// The selection comes from the page; never trust a path that is not a
	// clean absolute path inside this runtime's folder.
	if !filepath.IsAbs(modelPath) || filepath.Clean(modelPath) != modelPath || !aiModelInsideRoot(root, modelPath) {
		r.Failed = append(r.Failed, displayHome(modelPath)+": "+errAIModelBlocked.Error())
		return r
	}
	if ctx.Err() != nil {
		r.Skipped = "cancelled"
		return r
	}
	if src.ID == "ollama" {
		return cleanOllamaModel(ctx, root, modelPath, r)
	}
	snapshot, err := aiModelSnapshot(ctx, src, snapshots)
	if err != nil {
		r.Skipped = "cancelled"
		return r
	}
	entry, ok := aiModelEntryAt(snapshot.Models, modelPath)
	if !ok {
		// The folder changed since the page scanned it: the model is gone or
		// no longer a model here.
		r.Skipped = "changed"
		return r
	}
	return cleanAIModelDir(ctx, src, root, entry, r)
}

// aiModelSnapshot remembers one runtime's scan for the current clean run.
func aiModelSnapshot(ctx context.Context, src aiModelSource, snapshots map[string]AIModelSource) (AIModelSource, error) {
	if s, ok := snapshots[src.ID]; ok {
		return s, nil
	}
	s, err := scanAIModelSource(ctx, src)
	if err != nil {
		return AIModelSource{}, err
	}
	snapshots[src.ID] = s
	return s, nil
}

// aiModelEntryAt finds a scanned model by path.
func aiModelEntryAt(entries []AIModelEntry, path string) (AIModelEntry, bool) {
	want := canonical(path)
	for _, e := range entries {
		if canonical(e.Path) == want {
			return e, true
		}
	}
	return AIModelEntry{}, false
}

// cleanAIModelDir moves a whole model folder, measured again just before it
// goes.
func cleanAIModelDir(ctx context.Context, src aiModelSource, root string, entry AIModelEntry, r AIPartResult) AIPartResult {
	target := entry.Path
	info, err := os.Lstat(target)
	if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		r.Skipped = "changed"
		return r
	}
	if ctx.Err() != nil {
		r.Skipped = "cancelled"
		return r
	}
	sizeRoot := target
	if src.ID == "huggingface" {
		sizeRoot = filepath.Join(target, "blobs")
	}
	size, _, _ := measureAIModelDir(ctx, target, sizeRoot)
	if ctx.Err() != nil {
		r.Skipped = "cancelled"
		return r
	}
	if _, err := aiModelTrash(ctx, root, target); err != nil {
		r.Failed = append(r.Failed, displayHome(target)+": "+err.Error())
		return r
	}
	r.Moved, r.Freed = 1, size
	return r
}

// cleanOllamaModel moves a manifest and the blobs no remaining manifest
// names. The manifest goes first, so the runtime stops offering the model
// even when a blob cannot be moved; blobs still named by another manifest
// stay put.
func cleanOllamaModel(ctx context.Context, root, modelPath string, r AIPartResult) AIPartResult {
	manifests, err := ollamaManifests(ctx, root)
	if err != nil {
		r.Skipped = "cancelled"
		return r
	}
	want := canonical(modelPath)
	target := ""
	remaining := map[string]bool{}
	for _, m := range manifests {
		if canonical(m.path) == want {
			target = m.path
			continue
		}
		for _, blob := range m.blobs {
			remaining[blob] = true
		}
	}
	if target == "" {
		r.Skipped = "changed"
		return r
	}
	if _, err := os.Lstat(target); err != nil {
		r.Skipped = "changed"
		return r
	}
	owned := ollamaOwnedBlobs(manifests)[target]
	if ctx.Err() != nil {
		r.Skipped = "cancelled"
		return r
	}
	manifestSize, err := aiModelTrash(ctx, root, target)
	if err != nil {
		r.Failed = append(r.Failed, displayHome(target)+": "+err.Error())
		return r
	}
	r.Moved++
	r.Freed += manifestSize
	for _, blob := range owned {
		if ctx.Err() != nil {
			break
		}
		if remaining[blob] {
			// Another model still names this digest: it is not an orphan.
			r.Kept++
			continue
		}
		size, err := aiModelTrash(ctx, root, blob)
		if err != nil {
			r.Failed = append(r.Failed, displayHome(blob)+": "+err.Error())
			continue
		}
		r.Moved++
		r.Freed += size
	}
	if r.Moved == 0 && len(r.Failed) == 0 && ctx.Err() != nil {
		r.Skipped = "cancelled"
	}
	return r
}

// aiModelInsideRoot reports whether target resolves to a path inside root,
// root itself excluded; links that lead out of the folder are refused.
func aiModelInsideRoot(root, target string) bool {
	realRoot := canonical(root)
	real := canonical(target)
	return real != realRoot && within(real, realRoot)
}

// aiModelTrash moves target to the Trash when it is a real path inside the
// runtime's root, and returns the bytes the entry took.
func aiModelTrash(ctx context.Context, root, target string) (int64, error) {
	if !aiModelInsideRoot(root, target) || !safeToClean(target) || !platformRemovalSafe(target) {
		// TEMP diag
		return 0, fmt.Errorf("%w inside=%v safe=%v platform=%v home=%q root=%q", errAIModelBlocked, aiModelInsideRoot(root, target), safeToClean(target), platformRemovalSafe(target), homeDir(), root)
	}
	info, err := os.Lstat(target)
	if err != nil {
		return 0, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return 0, errAIModelBlocked
	}
	size := onDisk(info)
	if err := trashWithContext(ctx, target); err != nil {
		return 0, err
	}
	return size, nil
}
