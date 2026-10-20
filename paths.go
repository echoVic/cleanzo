package main

import (
	"path/filepath"
	"runtime"
	"strings"
)

// within reports whether path is dir or inside it at a separator boundary.
func within(path, dir string) bool {
	if runtime.GOOS == "windows" {
		path, dir = strings.ToLower(filepath.FromSlash(path)), strings.ToLower(filepath.FromSlash(dir))
	}
	sep := string(filepath.Separator)
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, sep)+sep)
}

// expandHome expands the current home marker using the native path separator.
func expandHome(p string) string {
	if p == "~" {
		return homeDir()
	}
	if strings.HasPrefix(p, "~/") || runtime.GOOS == "windows" && strings.HasPrefix(p, `~\`) { // slash-ok: accepts the user-facing home notation on every platform
		return filepath.Join(homeDir(), p[2:])
	}
	return p
}

// shortHome replaces the current home prefix with a display marker.
func shortHome(p string) string {
	home := homeDir()
	if within(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// platformPaths converts Darwin protection lists at initialization time.
// Windows protection lists are added with the Windows safety core.
func platformPaths(darwin []string) []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	paths := make([]string, len(darwin))
	for i, path := range darwin {
		paths[i] = filepath.FromSlash(path)
	}
	return paths
}

// Inside the home folder, cleaning never reaches these folders themselves
// (their contents are the user's data), nor anything in the subtrees below.
var (
	protectedHomeDirs = platformPaths([]string{
		"/library", "/library/application support", "/library/containers",
		"/library/group containers", "/library/preferences",
		"/desktop", "/documents", "/downloads", "/movies", "/music", "/pictures",
	})
	protectedHomeTrees = platformPaths([]string{
		"/library/keychains", "/library/mobile documents", "/library/mail",
		"/library/messages", "/.ssh", "/.gnupg",
	})
)
