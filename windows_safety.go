package main

import (
	"path"
	"strings"
	"time"
)

// procInfo is one process identity and its readable usage metadata.
type procInfo struct {
	Pid, PPid                   uint32
	Exe, Name, Product, CmdLine string
	Created                     time.Time
}

// windowsSystemPath identifies protected system executables after resolving both paths.
func windowsSystemPath(exe, root string, resolve func(string) string) bool {
	exe, root = resolve(exe), resolve(root)
	// Unknown identities cannot safely be offered a close request.
	if exe == "" || root == "" {
		return true
	}
	exe = path.Clean(strings.ReplaceAll(exe, `\`, "/"))
	root = path.Clean(strings.ReplaceAll(root, `\`, "/"))
	return strings.EqualFold(exe, root) || strings.HasPrefix(strings.ToLower(exe), strings.ToLower(strings.TrimRight(root, "/"))+"/") // slash-ok: compares normalized Windows paths on every host
}

// windowsLocalPath accepts only absolute DOS drive paths, with no stream or shell wildcard.
func windowsLocalPath(p string) bool {
	return len(p) >= 3 && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) && p[1] == ':' && (p[2] == '\\' || p[2] == '/') && !strings.ContainsAny(p[2:], ":\x00*?\"")
}

// windowsArgumentPaths extracts absolute path tokens and flag values after native command-line decoding.
func windowsArgumentPaths(args []string) []string {
	var paths []string
	for _, p := range args {
		if strings.HasPrefix(p, "-") {
			if i := strings.IndexByte(p, '='); i >= 0 {
				p = p[i+1:]
			}
		}
		// Usage roots accept absolute namespaces too; local forms are normalized by the guard.
		if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') || strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//") || strings.HasPrefix(p, `\??\`) {
			paths = append(paths, p)
		}
	}
	return paths
}

// windowsUsagePath normalizes local process namespaces; cleanup inputs retain stricter rules.
func windowsUsagePath(path string) string {
	for _, prefix := range []string{`\\?\`, `\\.\`, `\??\`} {
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}
	if !windowsLocalPath(path) {
		return ""
	}
	return path
}
