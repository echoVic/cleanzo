package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// openFiles lists the files the user's processes have open, lower-cased and
// sorted, from lsof.
func openFiles() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-w", "-Fn")
	// Apps started from Finder have no LANG, under which lsof escapes the
	// bytes of non-ASCII names as \xNN.
	cmd.Env = append(os.Environ(), "LC_ALL=en_US.UTF-8")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseLsofNames(out), nil
}

// parseLsofNames extracts the file paths from lsof -Fn output, whose name
// lines start with "n"; pipes have names that are not paths. Files are
// listed resolved, but sockets under the path they were bound with, which
// may go through the /var, /tmp and /etc links into /private.
func parseLsofNames(out []byte) []string {
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "n/") {
			continue
		}
		p := filepath.Clean(line[1:])
		for _, link := range []string{"/var", "/tmp", "/etc"} {
			if within(p, link) {
				p = "/private" + p
			}
		}
		paths = append(paths, strings.ToLower(p))
	}
	sort.Strings(paths)
	return paths
}

// runningApps returns the lower-cased bundle IDs of the running apps, and
// where their bundles are.
func runningApps() (ids map[string]bool, apps []string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/lsappinfo", "list").Output()
	if err != nil {
		return nil, nil, err
	}
	ids, apps = parseRunningApps(out)
	return ids, apps, nil
}

var (
	bundleIDRe   = regexp.MustCompile(`bundleID="([^"]+)"`)
	bundlePathRe = regexp.MustCompile(`bundle path="([^"]+\.app)"`)
)

// parseRunningApps extracts the bundle IDs and app bundle paths from
// lsappinfo list output.
func parseRunningApps(out []byte) (ids map[string]bool, apps []string) {
	ids = map[string]bool{}
	for _, m := range bundleIDRe.FindAllSubmatch(out, -1) {
		ids[strings.ToLower(string(m[1]))] = true
	}
	for _, m := range bundlePathRe.FindAllSubmatch(out, -1) {
		apps = append(apps, string(m[1]))
	}
	return ids, apps
}
