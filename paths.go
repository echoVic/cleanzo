package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
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

// platformPaths selects and converts protection lists at initialization time.
func platformPaths(darwin, windows []string) []string {
	if runtime.GOOS == "windows" {
		darwin = windows
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
	}, []string{
		"/desktop", "/documents", "/downloads", "/pictures", "/music", "/videos", "/favorites", "/contacts", "/links", "/searches", "/saved games",
		"/appdata", "/appdata/local", "/appdata/roaming", "/appdata/locallow",
	})
	// CloudStorage holds the folders of cloud providers (OneDrive, Google
	// Drive, Dropbox), online-only placeholders among them: trashing one
	// frees nothing here and deletes it from the cloud.
	protectedHomeTrees = platformPaths([]string{
		"/library/keychains", "/library/mobile documents", "/library/cloudstorage", "/library/mail",
		"/library/messages", "/.ssh", "/.gnupg",
	}, []string{
		"/.ssh", "/.gnupg", "/appdata/roaming/microsoft", "/appdata/local/packages", "/appdata/local/programs",
		"/appdata/local/microsoft/windowsapps", "/appdata/local/microsoft/credentials", "/appdata/local/microsoft/vault", "/appdata/local/wsl", "/appdata/local/docker",
		"/appdata/roaming/cleanzo", "/appdata/local/cleanzo",
	})
)

// platformProtected adds Windows prefix and environment-target subtree protections.
func platformProtected(path, home string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	rel, err := filepath.Rel(home, path)
	if err != nil {
		return true
	}
	first := strings.ToLower(strings.Split(rel, string(filepath.Separator))[0])
	if strings.HasPrefix(first, "onedrive") || strings.HasPrefix(first, "ntuser") {
		return true
	}
	roots := environmentProtections()
	for _, root := range roots.dirs {
		if path == root {
			return true
		}
	}
	for _, root := range roots.trees {
		if within(path, root) {
			return true
		}
	}

	return false
}

// systemTemp reports whether p, resolved and lower-cased, is in one of the
// folders macOS keeps temporary files in: a user's own folder inside
// /var/folders, /tmp or /var/tmp. /var/folders itself holds every user's
// folders, so only a folder inside it counts.
func systemTemp(p string) bool {
	if p != "/private/var/folders" && within(p, "/private/var/folders") {
		return true
	}
	return within(p, "/private/tmp") || within(p, "/private/var/tmp")
}

// allowedTemp judges the temporary folder, whose old entries are removed
// for good. On Windows it must be inside the resolved profile. On macOS,
// where it is normally in /var/folders, it must be in one of the system's
// temporary folders, and not the home folder or inside a folder of the
// user's data: started from a shell with TMPDIR=~/Downloads or
// TMPDIR=~/Projects, the temporary rule would offer the files that were
// not opened this week.
func allowedTemp(home, tmp string) bool {
	if runtime.GOOS != "windows" {
		if !filepath.IsAbs(home) || !filepath.IsAbs(tmp) {
			return false
		}
		h, p := strings.ToLower(resolvePath(home)), strings.ToLower(resolvePath(tmp))
		if p == string(filepath.Separator) || p == h || !systemTemp(p) {
			return false
		}
		if !within(p, h) {
			return true
		}
		inside := func(dir string) bool { return within(strings.TrimPrefix(p, h), dir) }
		return !slices.ContainsFunc(protectedHomeDirs, inside) && !slices.ContainsFunc(protectedHomeTrees, inside)
	}
	if !windowsLocalPath(home) || !windowsLocalPath(tmp) {
		return false
	}
	h, p := resolvePath(home), resolvePath(tmp)
	return h != "" && p != "" && !strings.EqualFold(p, h) && within(p, h) && platformCleanPath(tmp) && safeResolved(strings.ToLower(p))
}

// statusProcessExcluded identifies Windows Cleanzo processes by PID and executable path.
func statusProcessExcluded(pid int32, path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if pid == int32(os.Getpid()) {
		return true
	}
	exe, err := os.Executable()
	if err != nil || path == "" {
		return false
	}
	own, target := resolvePath(exe), resolvePath(path)
	return own != "" && target != "" && strings.EqualFold(own, target)
}

// statusWindowsRoot is populated from GetWindowsDirectory on Windows, never from the environment.
var statusWindowsRoot string

// statusAppExcluded preserves Darwin identities and protects Windows system apps and Cleanzo.
func statusAppExcluded(a workspaceApp) bool {
	if statusPackageFamilyName != nil && windowsBuiltinPackage(a.Pid, a.Path, statusPackageFamilyName) {
		return true
	}
	if runtime.GOOS == "windows" {
		return statusProcessExcluded(a.Pid, a.Path) || strings.EqualFold(filepath.Base(a.Path), "explorer.exe") || windowsSystemPath(a.Path, statusWindowsRoot, resolvePath)
	}
	return a.BundleID == "com.apple.finder" || a.BundleID == "com.cleanzo.app"
}

// protectionPaths separates protected roots from protected subtrees.
type protectionPaths struct{ dirs, trees []string }

// protectionResolver resolves environment targets once when their values change.
var protectionResolver = resolvePath

// protectionCache stores immutable environment protection lists keyed by the current profile and environment.
var protectionCache struct {
	sync.Mutex
	values [6]string
	valid  bool
	roots  protectionPaths
}

// environmentProtections refreshes final environment targets only after an environment/profile change.
func environmentProtections() protectionPaths {
	values := [6]string{homeDir(), os.Getenv("OneDrive"), os.Getenv("OneDriveConsumer"), os.Getenv("OneDriveCommercial"), os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA")}
	protectionCache.Lock()
	defer protectionCache.Unlock()
	if protectionCache.valid && protectionCache.values == values {
		return protectionCache.roots
	}
	roots := protectionPaths{}
	for i, root := range values {
		if i == 0 || root == "" {
			continue
		}
		if resolved := protectionResolver(root); resolved != "" {
			root = resolved
		}
		root = strings.ToLower(filepath.Clean(root))
		if i <= 3 {
			roots.trees = append(roots.trees, root)
			continue
		}
		roots.dirs = append(roots.dirs, root)
		trees := []string{"Cleanzo"}
		if i == 4 {
			trees = append(trees, "Microsoft")
		} else {
			trees = append(trees, "Packages", "Programs", "Microsoft/WindowsApps", "Microsoft/Credentials", "Microsoft/Vault", "wsl", "Docker")
		}
		for _, tree := range trees {
			roots.trees = append(roots.trees, filepath.Join(root, strings.ToLower(filepath.FromSlash(tree))))
		}
	}
	protectionCache.values, protectionCache.roots, protectionCache.valid = values, roots, true
	return roots
}
