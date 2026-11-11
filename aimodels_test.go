package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// Fixed modification times the model fixture uses, so Last is checkable and
// no test depends on the wall clock.
var (
	aiModelOld    = time.Date(2026, 8, 1, 10, 0, 0, 0, time.Local)
	aiModelMid    = time.Date(2026, 9, 1, 10, 0, 0, 0, time.Local)
	aiModelRecent = time.Date(2026, 10, 1, 10, 0, 0, 0, time.Local)
)

// aiModelsFixture builds fake Ollama, Hugging Face and LM Studio folders in a
// temporary home. It returns the home folder.
//
// Ollama's two models share one layer blob (sha256:aaaa1111) and have one
// exclusive blob each family; Hugging Face's model keeps the weights under
// blobs/ with a snapshot symlink and a refs file newer than both; LM Studio
// has two publisher/model folders and a non-model file at its root.
func aiModelsFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setHome(t, home)
	write := func(rel string, size int, mod time.Time) string {
		t.Helper()
		p := filepath.Join(home, rel)
		mustMkdir(t, filepath.Dir(p))
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		return p
	}
	writeText := func(rel, text string, mod time.Time) string {
		t.Helper()
		p := filepath.Join(home, rel)
		mustMkdir(t, filepath.Dir(p))
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// Ollama
	write(".ollama/models/blobs/sha256-aaaa1111", 4096, aiModelOld)
	write(".ollama/models/blobs/sha256-bbbb2222", 8192, aiModelMid)
	write(".ollama/models/blobs/sha256-cccc3333", 1024, aiModelOld)
	write(".ollama/models/blobs/sha256-eeee4444", 2048, aiModelRecent)
	writeText(".ollama/models/manifests/registry.ollama.ai/library/qwen3-coder/30b",
		`{"config":{"digest":"sha256:cccc3333"},"layers":[{"digest":"sha256:aaaa1111"},{"digest":"sha256:bbbb2222"}]}`,
		aiModelMid)
	writeText(".ollama/models/manifests/registry.ollama.ai/library/qwen3-coder/30b-small",
		`{"config":{"digest":"sha256:eeee4444"},"layers":[{"digest":"sha256:aaaa1111"}]}`,
		aiModelRecent)

	// Hugging Face: the snapshot entry is a symlink to blobs/bone and must
	// not count twice; the refs file is the newest thing in the folder.
	hf := ".cache/huggingface/hub/models--org--name"
	write(hf+"/blobs/bone", 4096, aiModelOld)
	write(hf+"/blobs/btwo", 8192, aiModelMid)
	mustMkdir(t, filepath.Join(home, hf, "snapshots", "abcdef"))
	if err := os.Symlink(filepath.Join(home, hf, "blobs", "bone"), filepath.Join(home, hf, "snapshots", "abcdef", "model.bin")); err != nil {
		t.Fatal(err)
	}
	writeText(hf+"/refs/main", "abcdef\n", aiModelRecent)
	write(".cache/huggingface/hub/datasets--other/data.bin", 4096, aiModelOld)

	// LM Studio
	write(".lmstudio/models/lmstudio-community/Qwen3-8B-GGUF/model.gguf", 16384, aiModelMid)
	write(".lmstudio/models/lmstudio-community/Qwen3-8B-GGUF/mmproj.gguf", 4096, aiModelOld)
	write(".lmstudio/models/second/embed-model/model.gguf", 2048, aiModelRecent)
	write(".lmstudio/models/README.txt", 100, aiModelOld)
	return home
}

// aiModelOnDisk is what a file takes on disk, the unit sizes are reported in.
func aiModelOnDisk(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return onDisk(info)
}

// aiModelSourceOf finds a source in a scan result.
func aiModelSourceOf(t *testing.T, res AIModelsResult, id string) AIModelSource {
	t.Helper()
	for _, src := range res.Sources {
		if src.ID == id {
			return src
		}
	}
	t.Fatalf("no source %s", id)
	return AIModelSource{}
}

// aiModelNamed finds a model in a source.
func aiModelNamed(t *testing.T, src AIModelSource, name string) AIModelEntry {
	t.Helper()
	for _, m := range src.Models {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no model %s in %s: %+v", name, src.ID, src.Models)
	return AIModelEntry{}
}

// aiModelsRecordTrash redirects the Trash to a temporary folder and returns
// the paths it received, so tests can check what would have left the disk.
func aiModelsRecordTrash(t *testing.T) *[]string {
	t.Helper()
	dir := t.TempDir()
	moved := &[]string{}
	saved := trashItem
	trashItem = func(p string) error {
		*moved = append(*moved, p)
		return os.Rename(p, filepath.Join(dir, fmt.Sprintf("%03d-%s", len(*moved), filepath.Base(p))))
	}
	t.Cleanup(func() { trashItem = saved })
	return moved
}

func TestScanAIModels(t *testing.T) {
	home := aiModelsFixture(t)
	res, err := (Cleaner{}).ScanAIModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.ScannedAt == 0 || len(res.Sources) != 3 {
		t.Fatalf("scan = %+v", res)
	}
	ids := []string{}
	for _, src := range res.Sources {
		ids = append(ids, src.ID)
	}
	if !slices.Equal(ids, []string{"ollama", "huggingface", "lmstudio"}) {
		t.Fatalf("source order = %v", ids)
	}

	// Ollama: one entry per manifest, shared blobs counted once.
	ollama := aiModelSourceOf(t, res, "ollama")
	blobs := filepath.Join(home, ".ollama", "models", "blobs")
	blob := func(name string) string { return filepath.Join(blobs, name) }
	aaa := aiModelOnDisk(t, blob("sha256-aaaa1111"))
	bbb := aiModelOnDisk(t, blob("sha256-bbbb2222"))
	ccc := aiModelOnDisk(t, blob("sha256-cccc3333"))
	eee := aiModelOnDisk(t, blob("sha256-eeee4444"))
	if !ollama.Installed || ollama.Name != "Ollama" || !filepath.IsAbs(ollama.Path) || len(ollama.Models) != 2 {
		t.Fatalf("ollama = %+v", ollama)
	}
	big := aiModelNamed(t, ollama, "qwen3-coder:30b")
	small := aiModelNamed(t, ollama, "qwen3-coder:30b-small")
	if big.Kind != aiModelKindManifest || len(big.Blobs) != 3 || !filepath.IsAbs(big.Path) {
		t.Fatalf("big = %+v", big)
	}
	if big.Size != aaa+bbb+ccc {
		t.Fatalf("big size = %d, want %d", big.Size, aaa+bbb+ccc)
	}
	// bbbb is the newest of big's blobs.
	if big.Last != aiModelMid.Unix() {
		t.Fatalf("big last = %d, want %d", big.Last, aiModelMid.Unix())
	}
	// The shared blob is assigned to the first manifest only, so the model
	// sizes add up to the blobs' total instead of counting it twice.
	if !slices.Contains(big.Blobs, blob("sha256-aaaa1111")) || slices.Contains(small.Blobs, blob("sha256-aaaa1111")) || len(small.Blobs) != 1 {
		t.Fatalf("shared blob assignment: big = %+v, small = %+v", big, small)
	}
	if small.Size != eee || small.Blobs[0] != blob("sha256-eeee4444") {
		t.Fatalf("small = %+v", small)
	}
	if small.Last != aiModelRecent.Unix() {
		t.Fatalf("small last = %d, want %d", small.Last, aiModelRecent.Unix())
	}
	if big.Size+small.Size != aaa+bbb+ccc+eee {
		t.Fatalf("model sizes %d+%d do not add up to the blobs' %d", big.Size, small.Size, aaa+bbb+ccc+eee)
	}
	if ollama.Size != big.Size+small.Size || ollama.Last != aiModelRecent.Unix() || ollama.Files != 4 {
		t.Fatalf("ollama totals = %+v", ollama)
	}

	// Hugging Face: only blobs/ counts, snapshots/ symlinks do not.
	hf := aiModelSourceOf(t, res, "huggingface")
	if !hf.Installed || hf.Name != "Hugging Face" || len(hf.Models) != 1 {
		t.Fatalf("huggingface = %+v", hf)
	}
	hfModel := aiModelNamed(t, hf, "org/name")
	bone := aiModelOnDisk(t, filepath.Join(hfModel.Path, "blobs", "bone"))
	btwo := aiModelOnDisk(t, filepath.Join(hfModel.Path, "blobs", "btwo"))
	if hfModel.Size != bone+btwo || hfModel.Kind != "" || len(hfModel.Blobs) != 0 {
		t.Fatalf("huggingface model = %+v", hfModel)
	}
	if hfModel.Last != aiModelRecent.Unix() || hfModel.Path != filepath.Join(home, ".cache", "huggingface", "hub", "models--org--name") {
		t.Fatalf("huggingface model = %+v", hfModel)
	}
	if hf.Size != hfModel.Size || hf.Files != 2 || hf.Last != aiModelRecent.Unix() {
		t.Fatalf("huggingface totals = %+v", hf)
	}

	// LM Studio: one entry per publisher/model folder; stray files ignored.
	lm := aiModelSourceOf(t, res, "lmstudio")
	if !lm.Installed || lm.Name != "LM Studio" || len(lm.Models) != 2 {
		t.Fatalf("lmstudio = %+v", lm)
	}
	lmModel := aiModelNamed(t, lm, "lmstudio-community/Qwen3-8B-GGUF")
	gguf := aiModelOnDisk(t, filepath.Join(lm.Path, "lmstudio-community", "Qwen3-8B-GGUF", "model.gguf"))
	mmproj := aiModelOnDisk(t, filepath.Join(lm.Path, "lmstudio-community", "Qwen3-8B-GGUF", "mmproj.gguf"))
	if lmModel.Size != gguf+mmproj || lmModel.Last != aiModelMid.Unix() || lmModel.Kind != "" {
		t.Fatalf("lmstudio model = %+v", lmModel)
	}
	// A source's total is its models' total, so nothing is counted twice.
	if lm.Size != lmModel.Size+aiModelNamed(t, lm, "second/embed-model").Size || lm.Files != 3 {
		t.Fatalf("lmstudio totals = %+v", lm)
	}
}

func TestListAIModelSources(t *testing.T) {
	home := aiModelsFixture(t)
	list := (Cleaner{}).ListAIModelSources()
	if len(list) != 3 || list[0].ID != "ollama" || list[1].Name != "Hugging Face" || list[2].ID != "lmstudio" || list[2].Name != "LM Studio" {
		t.Fatalf("list = %+v", list)
	}
	for _, info := range list {
		if !info.Installed {
			t.Fatalf("listed as missing: %+v", info)
		}
	}
	if err := os.RemoveAll(filepath.Join(home, ".lmstudio", "models")); err != nil {
		t.Fatal(err)
	}
	if list := (Cleaner{}).ListAIModelSources(); list[2].Installed || !list[0].Installed {
		t.Fatalf("list after removal = %+v", list)
	}
}

func TestScanAIModelsMissing(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	res, err := (Cleaner{}).ScanAIModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range res.Sources {
		if src.Installed || len(src.Models) != 0 || src.Size != 0 || src.Files != 0 || !filepath.IsAbs(src.Path) {
			t.Fatalf("missing source = %+v", src)
		}
		if src.Models == nil {
			t.Fatalf("models must serialize as []: %+v", src)
		}
	}
	// An empty folder is installed but has no models.
	mustMkdir(t, filepath.Join(home, ".lmstudio", "models"))
	res, err = (Cleaner{}).ScanAIModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lm := aiModelSourceOf(t, res, "lmstudio")
	if !lm.Installed || len(lm.Models) != 0 || lm.Size != 0 {
		t.Fatalf("empty lmstudio = %+v", lm)
	}
}

func TestScanAIModelsCancel(t *testing.T) {
	aiModelsFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Cleaner{}).ScanAIModels(ctx); err == nil {
		t.Fatal("cancelled scan returned no error")
	}
}

// TestCleanAIModelsOllamaOrphans covers the manifest/blob split: a model's
// exclusive blobs go with it, a blob another manifest still names stays, and
// deleting the last model that names it then reclaims it.
func TestCleanAIModelsOllamaOrphans(t *testing.T) {
	home := aiModelsFixture(t)
	trashed := aiModelsRecordTrash(t)
	scan, err := (Cleaner{}).ScanAIModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ollama := aiModelSourceOf(t, scan, "ollama")
	big := aiModelNamed(t, ollama, "qwen3-coder:30b")
	small := aiModelNamed(t, ollama, "qwen3-coder:30b-small")
	blobs := filepath.Join(home, ".ollama", "models", "blobs")
	blob := func(name string) string { return filepath.Join(blobs, name) }
	aaa, bbb, ccc := aiModelOnDisk(t, blob("sha256-aaaa1111")), aiModelOnDisk(t, blob("sha256-bbbb2222")), aiModelOnDisk(t, blob("sha256-cccc3333"))
	eee := aiModelOnDisk(t, blob("sha256-eeee4444"))
	bigManifest, smallManifest := aiModelOnDisk(t, big.Path), aiModelOnDisk(t, small.Path)

	res, err := (Cleaner{}).CleanAIModels(context.Background(), []AIModelSelection{{Source: "ollama", Models: []string{big.Path}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Parts) != 1 {
		t.Fatalf("result = %+v", res)
	}
	r := res.Parts[0]
	if r.Tool != "ollama" || r.Part != big.Path || r.Skipped != "" || len(r.Failed) != 0 {
		t.Fatalf("part = %+v", r)
	}
	// The manifest and the two blobs only it names moved; the shared blob
	// was left alone.
	if want := bigManifest + bbb + ccc; r.Freed != want || res.Freed != want {
		t.Fatalf("freed = %d, want %d", r.Freed, want)
	}
	if r.Moved != 3 || r.Kept != 1 {
		t.Fatalf("moved/kept = %d/%d", r.Moved, r.Kept)
	}
	if exists(big.Path) || exists(blob("sha256-bbbb2222")) || exists(blob("sha256-cccc3333")) {
		t.Fatal("the model and its exclusive blobs should have moved")
	}
	if !exists(blob("sha256-aaaa1111")) {
		t.Fatal("a blob another manifest names must stay")
	}
	if !exists(small.Path) {
		t.Fatal("the other model must stay")
	}
	if len(*trashed) != 3 {
		t.Fatalf("trashed = %v", *trashed)
	}

	// Now that no other manifest names the shared blob, cleaning the last
	// model reclaims it too.
	res, err = (Cleaner{}).CleanAIModels(context.Background(), []AIModelSelection{{Source: "ollama", Models: []string{small.Path}}})
	if err != nil {
		t.Fatal(err)
	}
	r = res.Parts[0]
	if want := smallManifest + aaa + eee; r.Freed != want || r.Moved != 3 || r.Kept != 0 || len(r.Failed) != 0 || r.Skipped != "" {
		t.Fatalf("second part = %+v (want freed %d)", r, want)
	}
	if exists(blob("sha256-aaaa1111")) || exists(blob("sha256-eeee4444")) || exists(small.Path) {
		t.Fatal("the last model and its orphaned blobs should have moved")
	}
	if entries, _ := os.ReadDir(blobs); len(entries) != 0 {
		t.Fatalf("blobs left behind: %v", entries)
	}
	// A fresh scan sees an empty folder that is still installed.
	scan, err = (Cleaner{}).ScanAIModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ollama = aiModelSourceOf(t, scan, "ollama"); !ollama.Installed || len(ollama.Models) != 0 || ollama.Size != 0 {
		t.Fatalf("after cleaning = %+v", ollama)
	}
}

// TestCleanAIModelsOllamaUnreadableManifest: when another model's manifest
// cannot be parsed or read, the blobs it names are unknown, so the clean
// stops for the model rather than treating a shared blob as an orphan, and
// names the file that stopped it.
func TestCleanAIModelsOllamaUnreadableManifest(t *testing.T) {
	for _, broken := range []string{"truncated", "unreadable", "folder"} {
		t.Run(broken, func(t *testing.T) {
			if broken != "truncated" && (runtime.GOOS == "windows" || os.Geteuid() == 0) {
				t.Skip("file modes do not deny reading here")
			}
			home := aiModelsFixture(t)
			trashed := aiModelsRecordTrash(t)
			scan, err := (Cleaner{}).ScanAIModels(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ollama := aiModelSourceOf(t, scan, "ollama")
			big, small := aiModelNamed(t, ollama, "qwen3-coder:30b"), aiModelNamed(t, ollama, "qwen3-coder:30b-small")
			bad := small.Path
			switch broken {
			case "truncated":
				if err := os.WriteFile(small.Path, []byte(`{"config":{"digest":"sha256:eeee4444"},"layers":[{"dig`), 0o644); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if err := os.Chmod(small.Path, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(small.Path, 0o644) })
			case "folder":
				// Another model's folder that cannot be listed may hold manifests.
				bad = filepath.Join(home, ".ollama/models/manifests/registry.ollama.ai/library/other")
				mustMkdir(t, bad)
				if err := os.Chmod(bad, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(bad, 0o755) })
			}
			res, err := (Cleaner{}).CleanAIModels(context.Background(), []AIModelSelection{{Source: "ollama", Models: []string{big.Path}}})
			if err != nil || len(res.Parts) != 1 {
				t.Fatalf("result = %+v, %v", res, err)
			}
			if r := res.Parts[0]; r.Skipped != "blocked" || r.Moved != 0 || r.Freed != 0 || len(*trashed) != 0 {
				t.Fatalf("part = %+v, trashed %v", r, *trashed)
			}
			if r := res.Parts[0]; !slices.Equal(r.Failed, []string{displayHome(bad) + ": unreadable"}) {
				t.Fatalf("failed = %q, want the file that stopped the clean", r.Failed)
			}
			if !exists(filepath.Join(home, ".ollama/models/blobs/sha256-aaaa1111")) || !exists(big.Path) {
				t.Fatal("the shared blob or the model left")
			}
		})
	}
}

// TestCleanAIModelsDirs cleans whole model folders of the other two sources.
func TestCleanAIModelsDirs(t *testing.T) {
	home := aiModelsFixture(t)
	trashed := aiModelsRecordTrash(t)
	scan, err := (Cleaner{}).ScanAIModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hfModel := aiModelNamed(t, aiModelSourceOf(t, scan, "huggingface"), "org/name")
	lm := aiModelSourceOf(t, scan, "lmstudio")
	lmOne := aiModelNamed(t, lm, "lmstudio-community/Qwen3-8B-GGUF")
	lmTwo := aiModelNamed(t, lm, "second/embed-model")

	res, err := (Cleaner{}).CleanAIModels(context.Background(), []AIModelSelection{
		{Source: "huggingface", Models: []string{hfModel.Path}},
		{Source: "lmstudio", Models: []string{lmOne.Path, lmTwo.Path}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Parts) != 3 {
		t.Fatalf("parts = %+v", res.Parts)
	}
	want := []struct {
		tool, path string
		freed      int64
	}{
		{"huggingface", hfModel.Path, hfModel.Size},
		{"lmstudio", lmOne.Path, lmOne.Size},
		{"lmstudio", lmTwo.Path, lmTwo.Size},
	}
	var total int64
	for i, w := range want {
		r := res.Parts[i]
		if r.Tool != w.tool || r.Part != w.path || r.Moved != 1 || r.Freed != w.freed || r.Skipped != "" || len(r.Failed) != 0 {
			t.Fatalf("part %d = %+v, want %+v", i, r, w)
		}
		total += w.freed
		if exists(w.path) {
			t.Fatalf("%s should have moved", w.path)
		}
	}
	if res.Freed != total {
		t.Fatalf("res.Freed = %d, want %d", res.Freed, total)
	}
	// A folder cleaned whole takes its snapshot symlinks with it.
	if exists(filepath.Join(home, ".cache", "huggingface", "hub", "models--org--name")) {
		t.Fatal("huggingface model folder should be gone")
	}
	if len(*trashed) != 3 {
		t.Fatalf("trashed = %v", *trashed)
	}
}

// TestCleanAIModelsRejectsOutside keeps cleaning inside the runtimes' own
// folders: a path outside them, a link leaving them and a traversal path are
// all refused and nothing moves.
func TestCleanAIModelsRejectsOutside(t *testing.T) {
	home := aiModelsFixture(t)
	trashed := aiModelsRecordTrash(t)
	scan, err := (Cleaner{}).ScanAIModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lmRoot := aiModelSourceOf(t, scan, "lmstudio").Path
	outside := filepath.Join(home, "notes", "keep.txt")
	mustMkdir(t, filepath.Dir(outside))
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A link inside the model folder that points out of it.
	link := filepath.Join(lmRoot, "linked-model")
	if err := os.Symlink(filepath.Dir(outside), link); err != nil {
		t.Fatal(err)
	}
	traversal := lmRoot + string(filepath.Separator) + ".." + string(filepath.Separator) + ".." + string(filepath.Separator) + "notes"

	res, err := (Cleaner{}).CleanAIModels(context.Background(), []AIModelSelection{
		{Source: "ollama", Models: []string{outside}},
		{Source: "lmstudio", Models: []string{link, traversal, lmRoot}},
		{Source: "no-such-runtime", Models: []string{outside}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Parts) != 5 {
		t.Fatalf("parts = %+v", res.Parts)
	}
	for i := 0; i < 4; i++ {
		r := res.Parts[i]
		if r.Moved != 0 || r.Skipped != "" || len(r.Failed) != 1 || !strings.Contains(r.Failed[0], "blocked") {
			t.Fatalf("part %d = %+v", i, r)
		}
	}
	if r := res.Parts[4]; r.Skipped != "unknown" || r.Moved != 0 {
		t.Fatalf("unknown source part = %+v", r)
	}
	if len(*trashed) != 0 {
		t.Fatalf("trashed = %v", *trashed)
	}
	if !exists(outside) || !exists(link) {
		t.Fatal("nothing outside a model folder may move")
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link itself must be untouched: %v", err)
	}
}

// TestCleanAIModelsChanged skips models that changed since the page scanned.
func TestCleanAIModelsChanged(t *testing.T) {
	home := aiModelsFixture(t)
	trashed := aiModelsRecordTrash(t)
	scan, err := (Cleaner{}).ScanAIModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lm := aiModelSourceOf(t, scan, "lmstudio")
	gone := aiModelNamed(t, lm, "second/embed-model")
	if err := os.RemoveAll(gone.Path); err != nil {
		t.Fatal(err)
	}
	ollama := aiModelSourceOf(t, scan, "ollama")
	manifest := aiModelNamed(t, ollama, "qwen3-coder:30b-small").Path
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	res, err := (Cleaner{}).CleanAIModels(context.Background(), []AIModelSelection{
		{Source: "lmstudio", Models: []string{gone.Path}},
		{Source: "ollama", Models: []string{manifest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Parts) != 2 {
		t.Fatalf("parts = %+v", res.Parts)
	}
	for i, r := range res.Parts {
		if r.Skipped != "changed" || r.Moved != 0 || len(r.Failed) != 0 {
			t.Fatalf("part %d = %+v", i, r)
		}
	}
	if len(*trashed) != 0 {
		t.Fatalf("trashed = %v", *trashed)
	}
	if !fileExists(filepath.Join(home, ".lmstudio", "models", "lmstudio-community", "Qwen3-8B-GGUF")) {
		t.Fatal("an untouched model must stay")
	}
}

// TestCleanAIModelsCancel skips whatever is left once the context is
// cancelled, before and during a clean.
func TestCleanAIModelsCancel(t *testing.T) {
	aiModelsFixture(t)
	trashed := aiModelsRecordTrash(t)
	scan, err := (Cleaner{}).ScanAIModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lm := aiModelSourceOf(t, scan, "lmstudio")
	paths := []string{lm.Models[0].Path, lm.Models[1].Path}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := (Cleaner{}).CleanAIModels(ctx, []AIModelSelection{{Source: "lmstudio", Models: paths}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Parts) != 2 || len(*trashed) != 0 {
		t.Fatalf("result = %+v, trashed = %v", res.Parts, *trashed)
	}
	for i, r := range res.Parts {
		if r.Skipped != "cancelled" || r.Moved != 0 {
			t.Fatalf("part %d = %+v", i, r)
		}
	}
	for _, path := range paths {
		if !exists(path) {
			t.Fatalf("%s should not have moved", path)
		}
	}

	// Cancel from inside the first move: that model goes, the rest is skipped.
	trashDir := t.TempDir()
	saved := trashItem
	var moved []string
	trashItem = func(p string) error {
		moved = append(moved, p)
		cancel()
		return os.Rename(p, filepath.Join(trashDir, filepath.Base(p)))
	}
	t.Cleanup(func() { trashItem = saved })
	ctx, cancel = context.WithCancel(context.Background())
	res, err = (Cleaner{}).CleanAIModels(ctx, []AIModelSelection{{Source: "lmstudio", Models: paths}})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Parts) != 2 || len(moved) != 1 {
		t.Fatalf("parts = %+v, moved = %v", res.Parts, moved)
	}
	if res.Parts[0].Moved != 1 || res.Parts[0].Skipped != "" {
		t.Fatalf("first part = %+v", res.Parts[0])
	}
	if res.Parts[1].Skipped != "cancelled" || res.Parts[1].Moved != 0 {
		t.Fatalf("second part = %+v", res.Parts[1])
	}
	if !exists(paths[1]) {
		t.Fatalf("%s should have been skipped", paths[1])
	}
}

// TestOllamaBlobName keeps manifest digests from naming paths outside blobs/.
func TestOllamaBlobName(t *testing.T) {
	for _, tc := range []struct {
		digest, want string
		ok           bool
	}{
		{"sha256:abc123", "sha256-abc123", true},
		{"sha512:0123abcd", "sha512-0123abcd", true},
		{"sha256:../../etc/passwd", "", false},
		{"sha256:abcd/ef", "", false},
		{"sha256:", "", false},
		{":abc", "", false},
		{"no-colon", "", false},
		{"SHA256:abcd", "", false},
		{"sha256:ABCD", "", false},
	} {
		got, ok := ollamaBlobName(tc.digest)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ollamaBlobName(%q) = %q, %v; want %q, %v", tc.digest, got, ok, tc.want, tc.ok)
		}
	}
}
