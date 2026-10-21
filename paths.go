package main

import (
	"os"
	"path/filepath"
	"runtime"
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
	protectedHomeTrees = platformPaths([]string{
		"/library/keychains", "/library/mobile documents", "/library/mail",
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

// allowedTemp keeps Windows temp patterns inside the resolved profile; Darwin retains all existing roots.
func allowedTemp(home, tmp string) bool {
	if runtime.GOOS != "windows" {
		return true
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
