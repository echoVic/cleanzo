package main

import (
	"os"
	"path/filepath"
	"slices"
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

// rustupKeep names the toolchains ~/.rustup/settings.toml chooses, so
// review.rust-toolchains never offers them: default_toolchain and every
// folder's toolchain under [overrides]. Settings that cannot be read in full,
// or that choose no default, leave them unresolved.
func rustupKeep(env junkEnv) ([]string, bool) {
	data, ok := readSettingsFile(env, filepath.Join(env.home, ".rustup", "settings.toml"), 1<<20)
	if !ok {
		return nil, false
	}
	var names []string
	host, section, found := "", "", false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			header, _, _ := strings.Cut(line, "#")
			section = strings.TrimSpace(strings.Trim(strings.TrimSpace(header), "[]"))
			continue
		}
		key, value, isString, parsed := tomlStringPair(line)
		switch {
		case section == "overrides":
			if !parsed || !isString {
				return nil, false
			}
			names = append(names, value)
		case section != "" || !parsed:
			// Other tables, and lines of values spanning several lines, name no toolchain.
		case key == "default_toolchain":
			if !isString || found || value == "" {
				return nil, false
			}
			names, found = append([]string{value}, names...), true
		case key == "default_host_triple" && isString:
			host = value
		case key == "overrides":
			return nil, false // an inline table
		}
	}
	if !found {
		return nil, false
	}
	// rustup writes full names, such as stable-aarch64-apple-darwin; a short
	// one names the toolchain for the host.
	if host != "" {
		for _, name := range slices.Clone(names) {
			if !strings.HasSuffix(name, "-"+host) {
				names = append(names, name+"-"+host)
			}
		}
	}
	return names, true
}

// tomlStringPair reads a TOML line `key = value`, the key bare or quoted.
// isString reports a one-line string value, which comes back unquoted, with
// only a comment after it.
func tomlStringPair(line string) (key, value string, isString, parsed bool) {
	key, rest, ok := tomlString(line)
	if !ok {
		end := strings.IndexFunc(line, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
		})
		if end <= 0 {
			return "", "", false, false
		}
		key, rest = line[:end], line[end:]
	}
	rest, ok = strings.CutPrefix(strings.TrimLeft(rest, " \t"), "=")
	if !ok {
		return "", "", false, false
	}
	value, rest, ok = tomlString(strings.TrimLeft(rest, " \t"))
	rest = strings.TrimSpace(rest)
	if !ok || rest != "" && rest[0] != '#' {
		return key, "", false, true
	}
	return key, value, true, true
}

// tomlString reads the one-line TOML string s starts with, basic ("…", with
// escapes) or literal ('…'), and returns what follows it.
func tomlString(s string) (value, rest string, ok bool) {
	if strings.HasPrefix(s, `"""`) || strings.HasPrefix(s, "'''") {
		return "", s, false
	}
	if strings.HasPrefix(s, "'") {
		if end := strings.IndexByte(s[1:], '\''); end >= 0 {
			return s[1 : 1+end], s[2+end:], true
		}
		return "", s, false
	}
	if !strings.HasPrefix(s, `"`) {
		return "", s, false
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			value, err := strconv.Unquote(s[:i+1])
			if err != nil {
				return "", s, false
			}
			return value, s[i+1:], true
		}
	}
	return "", s, false
}
