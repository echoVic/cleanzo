package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// stageNvm writes nvm's alias files (name to content, such as "lts/*" to
// "lts/jod") and a folder for each installed Node version.
func stageNvm(t *testing.T, home string, aliases map[string]string, versions ...string) {
	t.Helper()
	for _, v := range versions {
		writeFile(t, filepath.Join(home, ".nvm", "versions", "node", v, "bin", "node"), 100)
	}
	for name, content := range aliases {
		path := filepath.Join(home, ".nvm", "alias", filepath.FromSlash(name))
		mustMkdir(t, filepath.Dir(path))
		if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestNvmDefaultResolution follows nvm's default alias to one installed
// version, comparing versions as numbers, and gives up on anything else.
func TestNvmDefaultResolution(t *testing.T) {
	installed := []string{"v9.11.2", "v18.20.4", "v20.9.0", "v20.18.1", "v22.18.0", "v22.23.1", "v24.1.0"}
	for _, tc := range []struct {
		name    string
		aliases map[string]string
		keep    string // "" when the default is not resolved
	}{
		{"exact", map[string]string{"default": "v22.18.0"}, "v22.18.0"},
		{"exact without v", map[string]string{"default": "22.18.0"}, "v22.18.0"},
		{"major prefix", map[string]string{"default": "22"}, "v22.23.1"},
		{"minor prefix", map[string]string{"default": "v20.18"}, "v20.18.1"},
		{"single digit major", map[string]string{"default": "9"}, "v9.11.2"},
		{"node", map[string]string{"default": "node"}, "v24.1.0"},
		{"stable", map[string]string{"default": "stable"}, "v24.1.0"},
		{"lts star", map[string]string{"default": "lts/*", "lts/*": "lts/jod", "lts/jod": "v22.23.1"}, "v22.23.1"},
		{"lts name", map[string]string{"default": "lts/iron", "lts/iron": "v20.18.1"}, "v20.18.1"},
		{"chained alias ending in a prefix", map[string]string{"default": "lts/*", "lts/*": "lts/hydrogen", "lts/hydrogen": "18"}, "v18.20.4"},
		{"missing alias file", map[string]string{}, ""},
		{"missing lts file", map[string]string{"default": "lts/*"}, ""},
		{"alias loop", map[string]string{"default": "lts/a", "lts/a": "lts/b", "lts/b": "lts/a"}, ""},
		{"system", map[string]string{"default": "system"}, ""},
		{"iojs", map[string]string{"default": "iojs"}, ""},
		{"a user's own alias", map[string]string{"default": "work", "work": "v22.18.0"}, ""},
		{"x range", map[string]string{"default": "22.x"}, ""},
		{"lts escaping the alias folder", map[string]string{"default": "lts/../default"}, ""},
		{"two lines", map[string]string{"default": "v22.18.0\nv24.1.0"}, ""},
		{"empty", map[string]string{"default": ""}, ""},
		{"exact not installed", map[string]string{"default": "v21.0.0"}, ""},
		{"prefix not installed", map[string]string{"default": "16"}, ""},
		{"prefix of another major", map[string]string{"default": "2"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t)
			stageNvm(t, env.home, tc.aliases, installed...)
			names, ok := nvmKeep(env)
			if tc.keep == "" {
				if ok {
					t.Fatalf("resolved to %v, want unresolved", names)
				}
				return
			}
			if !ok || !slices.Equal(names, []string{tc.keep}) {
				t.Fatalf("keep = %v, %v; want [%s]", names, ok, tc.keep)
			}
		})
	}
}

// TestNodeVersionsOfferAllButTheDefault: the review row lists every other
// version on its own, unselected and bound for the Trash; one version can be
// cleaned alone; without a resolvable default there is no row and nothing
// else offers the versions.
func TestNodeVersionsOfferAllButTheDefault(t *testing.T) {
	env := testEnv(t)
	stageNvm(t, env.home, map[string]string{"default": "22"}, "v18.20.4", "v22.18.0", "v22.23.1", "v24.1.0")
	env.settings.Custom = []CustomFolder{{Path: filepath.Join(env.home, ".nvm"), Enabled: true}}
	nodes := filepath.Join(env.home, ".nvm", "versions", "node")
	specs, _ := expandItems(&env)
	if got, want := offeredIn(specs, nodes), []string{"review.node-versions v18.20.4", "review.node-versions v22.18.0", "review.node-versions v24.1.0"}; !slices.Equal(got, want) {
		t.Fatalf("offered = %q, want %q", got, want)
	}
	spec := catalogRow(t, &env, "review.node-versions")
	if spec.item.Group != string(groupReview) || spec.item.Default || !spec.item.Trash {
		t.Fatalf("item = %+v", spec.item)
	}
	plan := planItem(context.Background(), *spec, env, make(chan struct{}, 4), nil)
	var subs []string
	for _, s := range plan.item.Subs {
		subs = append(subs, s.Name)
	}
	slices.Sort(subs)
	if !slices.Equal(subs, []string{"v18.20.4", "v22.18.0", "v24.1.0"}) {
		t.Fatalf("subs = %v", subs)
	}
	res := cleanJunk(context.Background(), env, []JunkSelection{{ID: "review.node-versions", Paths: []string{filepath.Join(nodes, "v18.20.4")}}}, time.Time{}, cleanHooks{})
	if len(res) != 1 || res[0].Trashed != 100 || exists(filepath.Join(nodes, "v18.20.4")) || !exists(filepath.Join(nodes, "v24.1.0")) || !exists(filepath.Join(nodes, "v22.23.1")) {
		t.Fatalf("result = %+v", res)
	}
	if !exists(filepath.Join(env.home, ".Trash", "v18.20.4")) {
		t.Fatal("the version did not reach the Trash")
	}

	if err := os.Remove(filepath.Join(env.home, ".nvm", "alias", "default")); err != nil {
		t.Fatal(err)
	}
	specs, _ = expandItems(&env)
	if got := offeredIn(specs, nodes); len(got) > 0 || slices.Contains(itemIDs(specs), "review.node-versions") {
		t.Fatalf("without a default: offered = %q, items = %v", got, itemIDs(specs))
	}
}

// offeredIn lists "row-id relative-path" for every candidate inside dir.
func offeredIn(specs []itemSpec, dir string) []string {
	var offered []string
	for _, s := range specs {
		for _, c := range s.cands {
			if rel, err := filepath.Rel(resolvePath(dir), c.path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				offered = append(offered, s.item.ID+" "+filepath.ToSlash(rel))
			}
		}
	}
	slices.Sort(offered)
	return offered
}

// catalogRow expands the catalog and returns the row with the given ID.
func catalogRow(t *testing.T, env *junkEnv, id string) *itemSpec {
	t.Helper()
	specs, _ := expandItems(env)
	for i := range specs {
		if specs[i].item.ID == id {
			return &specs[i]
		}
	}
	t.Fatalf("no %s row in %v", id, itemIDs(specs))
	return nil
}

// stageRustup writes ~/.rustup/settings.toml (unless settings is "-") and a
// folder for each installed toolchain.
func stageRustup(t *testing.T, home, settings string, toolchains ...string) {
	t.Helper()
	for _, name := range toolchains {
		writeFile(t, filepath.Join(home, ".rustup", "toolchains", name, "bin", "rustc"), 100)
	}
	if settings == "-" {
		return
	}
	path := filepath.Join(home, ".rustup", "settings.toml")
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
}

// rustupSettings is what rustup writes after `rustup default stable` and two
// `rustup override set` calls, one in a folder whose name holds an equals sign.
const rustupSettings = `default_host_triple = "aarch64-apple-darwin"
default_toolchain = "stable-aarch64-apple-darwin"
profile = "default"
version = "12"

[overrides]
"/Users/me/old = project" = "1.79.0-aarch64-apple-darwin"
'/Users/me/edge' = "nightly-aarch64-apple-darwin" # pinned
`

// TestRustupKeepsDefaultAndOverrides reads the default toolchain and every
// override, and gives up when the settings cannot be read in full.
func TestRustupKeepsDefaultAndOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		keep           []string // nil when unresolved
	}{
		{"default and overrides", rustupSettings, []string{"stable-aarch64-apple-darwin", "1.79.0-aarch64-apple-darwin", "nightly-aarch64-apple-darwin"}},
		{"no overrides", "default_toolchain = \"stable-aarch64-apple-darwin\"\n", []string{"stable-aarch64-apple-darwin"}},
		{"short names for the host", "default_host_triple = \"aarch64-apple-darwin\"\ndefault_toolchain = 'stable'\n[overrides]\n\"/p\" = \"nightly\"\n", []string{"stable", "nightly", "stable-aarch64-apple-darwin", "nightly-aarch64-apple-darwin"}},
		{"escaped strings", "default_toolchain = \"stable-aarch64-apple-darwin\"\n[overrides]\n\"/a\\\"b\" = \"1.80.0-\\u0061arch64-apple-darwin\"\n", []string{"stable-aarch64-apple-darwin", "1.80.0-aarch64-apple-darwin"}},
		{"other sections", "default_toolchain = \"stable-aarch64-apple-darwin\"\n[other]\ndefault_toolchain = \"x\"\n", []string{"stable-aarch64-apple-darwin"}},
		{"missing settings", "-", nil},
		{"no default", "profile = \"default\"\n[overrides]\n\"/p\" = \"nightly-aarch64-apple-darwin\"\n", nil},
		{"default only in a section", "[overrides]\ndefault_toolchain = \"stable-aarch64-apple-darwin\"\n", nil},
		{"default not a string", "default_toolchain = 1\n", nil},
		{"override not a string", "default_toolchain = \"stable-aarch64-apple-darwin\"\n[overrides]\n\"/p\" = [\"nightly\"]\n", nil},
		{"unterminated override", "default_toolchain = \"stable-aarch64-apple-darwin\"\n[overrides]\n\"/p = \"nightly\"\n", nil},
		{"inline overrides", "default_toolchain = \"stable-aarch64-apple-darwin\"\noverrides = { \"/p\" = \"nightly\" }\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testEnv(t)
			stageRustup(t, env.home, tc.settings)
			names, ok := rustupKeep(env)
			if tc.keep == nil {
				if ok {
					t.Fatalf("resolved to %q, want unresolved", names)
				}
				return
			}
			if !ok || !slices.Equal(names, tc.keep) {
				t.Fatalf("keep = %q, %v; want %q", names, ok, tc.keep)
			}
		})
	}
}

// TestRustToolchainsOfferAllButTheChosen: the review row lists the other
// toolchains on their own, unselected and bound for the Trash; without
// readable settings there is no row.
func TestRustToolchainsOfferAllButTheChosen(t *testing.T) {
	env := testEnv(t)
	stageRustup(t, env.home, rustupSettings, "stable-aarch64-apple-darwin", "1.79.0-aarch64-apple-darwin", "nightly-aarch64-apple-darwin", "1.92.0-aarch64-apple-darwin", "beta-aarch64-apple-darwin")
	toolchains := filepath.Join(env.home, ".rustup", "toolchains")
	specs, _ := expandItems(&env)
	if got, want := offeredIn(specs, toolchains), []string{"review.rust-toolchains 1.92.0-aarch64-apple-darwin", "review.rust-toolchains beta-aarch64-apple-darwin"}; !slices.Equal(got, want) {
		t.Fatalf("offered = %q, want %q", got, want)
	}
	spec := catalogRow(t, &env, "review.rust-toolchains")
	if spec.item.Group != string(groupReview) || spec.item.Default || !spec.item.Trash {
		t.Fatalf("item = %+v", spec.item)
	}
	if plan := planItem(context.Background(), *spec, env, make(chan struct{}, 4), nil); len(plan.item.Subs) != 2 {
		t.Fatalf("subs = %+v", plan.item.Subs)
	}

	if err := os.Remove(filepath.Join(env.home, ".rustup", "settings.toml")); err != nil {
		t.Fatal(err)
	}
	specs, _ = expandItems(&env)
	if got := offeredIn(specs, toolchains); len(got) > 0 || slices.Contains(itemIDs(specs), "review.rust-toolchains") {
		t.Fatalf("without settings: offered = %q, items = %v", got, itemIDs(specs))
	}
}
