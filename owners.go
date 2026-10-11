package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// appRef names an installed app.
type appRef struct{ BundleID, Name string }

// appIndex finds installed apps by their bundle IDs and names.
type appIndex struct{ keys map[string]appRef }

// buildAppIndex reads app IDs, display names, bundle names, executables and folders.
func buildAppIndex(dirs []string) *appIndex {
	var paths []string
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() && strings.HasSuffix(e.Name(), ".app") {
				paths = append(paths, filepath.Join(dir, e.Name()))
			}
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	metadata := make([]map[string]string, len(paths))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out, err := exec.Command("/usr/bin/plutil", "-convert", "xml1", "-o", "-", filepath.Join(p, "Contents", "Info.plist")).Output()
			if err == nil {
				metadata[i] = plistStrings(out)
			}
		}()
	}
	wg.Wait()
	x := &appIndex{keys: map[string]appRef{}}
	// Merge in path order so completion timing cannot decide an alias's owner.
	for i, m := range metadata {
		bid := m["CFBundleIdentifier"]
		if bid == "" {
			continue
		}
		folder := strings.TrimSuffix(filepath.Base(paths[i]), ".app")
		name := m["CFBundleDisplayName"]
		if name == "" {
			name = m["CFBundleName"]
		}
		if name == "" {
			name = folder
		}
		app := appRef{BundleID: bid, Name: name}
		exe := m["CFBundleExecutable"]
		if strings.EqualFold(exe, "Electron") {
			exe = "" // shared by many Electron apps (VS Code's is "Electron"), so it names none of them
		}
		for _, key := range []string{bid, name, m["CFBundleName"], exe, folder} {
			key = strings.ToLower(key)
			if key != "" {
				if _, ok := x.keys[key]; !ok {
					x.keys[key] = app
				}
			}
		}
	}
	return x
}

// lookup finds an exact name or the longest bundle ID followed by a dot.
func (x *appIndex) lookup(name string) (appRef, bool) {
	name = strings.ToLower(name)
	if app, ok := x.keys[name]; ok {
		return app, true
	}
	var found appRef
	longest := 0
	for key, app := range x.keys {
		if len(key) > longest && key == strings.ToLower(app.BundleID) && strings.HasPrefix(name, key+".") {
			found, longest = app, len(key)
		}
	}
	return found, longest > 0
}
