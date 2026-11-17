package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// aliasHops bounds the nvm alias files one lookup reads; a longer chain, or
// a loop, leaves the default unresolved.
const aliasHops = 5

// nvmKeep names the Node version nvm's default alias resolves to, so
// review.node-versions never offers it. The alias is a version, a prefix of
// one (the highest installed match), node or stable (the highest installed),
// or lts/* or lts/<name>, which lead through files in ~/.nvm/alias/lts. A
// missing file, any other form (system, iojs, an alias of the user's own) or
// no installed match leaves the default unresolved.
func nvmKeep(env junkEnv) ([]string, bool) {
	nvm := filepath.Join(env.home, ".nvm")
	entries, err := env.readDir(filepath.Join(nvm, "versions", "node"))
	if err != nil {
		return nil, false
	}
	var installed []string
	for _, e := range entries {
		if v := nvmVersion(e.Name()); len(v) == 3 && strings.HasPrefix(e.Name(), "v") {
			installed = append(installed, e.Name())
		}
	}
	alias, ok := readAlias(env, filepath.Join(nvm, "alias", "default"))
	for hops := 1; ok; hops++ {
		if alias == "node" || alias == "stable" {
			return highestNode(installed, nil)
		}
		if name, lts := strings.CutPrefix(alias, "lts/"); lts {
			if hops == aliasHops || name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
				return nil, false
			}
			alias, ok = readAlias(env, filepath.Join(nvm, "alias", "lts", name))
			continue
		}
		if prefix := nvmVersion(alias); prefix != nil {
			return highestNode(installed, prefix)
		}
		return nil, false
	}
	return nil, false
}

// nvmVersion parses a version or a prefix of one, such as v22.23.1, 22.18
// or 22, into its numbers; nil when s is neither.
func nvmVersion(s string) []int {
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) > 3 {
		return nil
	}
	version := make([]int, len(parts))
	for i, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return nil
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		version[i] = n
	}
	return version
}

// highestNode picks the highest installed version that starts with prefix,
// compared as numbers.
func highestNode(installed []string, prefix []int) ([]string, bool) {
	best := ""
	for _, name := range installed {
		v := nvmVersion(name)
		if compareVersions(v[:len(prefix)], prefix) != 0 {
			continue
		}
		if best == "" || compareVersions(v, nvmVersion(best)) > 0 {
			best = name
		}
	}
	if best == "" {
		return nil, false
	}
	return []string{best}, true
}

// readAlias reads one line of an nvm alias file: a regular file inside the
// home folder, reached without links.
func readAlias(env junkEnv, path string) (string, bool) {
	data, ok := readSettingsFile(env, path, 4<<10)
	alias := strings.TrimSpace(string(data))
	if !ok || alias == "" || strings.ContainsAny(alias, " \t\r\n") {
		return "", false
	}
	return alias, true
}

// readSettingsFile reads a small regular file inside the home folder, reached
// without links; anything else (a folder, a pipe, a file larger than limit)
// is not read.
func readSettingsFile(env junkEnv, path string, limit int64) ([]byte, bool) {
	if env.traversalBlocked(path) {
		return nil, false
	}
	info, err := env.stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, false
	}
	data, err := os.ReadFile(path)
	return data, err == nil
}

// rustupKeep names the toolchains rustup's settings choose.
func rustupKeep(env junkEnv) ([]string, bool) { return nil, false }
