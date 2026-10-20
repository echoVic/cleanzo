package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// listInstalledApps preserves the macOS ListApps behavior.
func listInstalledApps() []AppInfo {
	var apps []AppInfo
	for _, dir := range []string{"/Applications", filepath.Join(homeDir(), "Applications")} {
		apps = append(apps, listAppsIn(dir)...)
	}
	if ids, paths, err := runningApps(); err == nil {
		markRunning(apps, ids, paths)
	}
	sort.Slice(apps, func(i, j int) bool { return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name) })
	return apps
}

// appUninstallDetail preserves the macOS AppUninstallDetail behavior.
func appUninstallDetail(path string) UninstallDetail {
	apps := []AppInfo{appInfoFromPath(path)}
	if ids, paths, err := runningApps(); err == nil {
		markRunning(apps, ids, paths)
	}
	app := apps[0]
	items := uninstallPaths(app)
	var total int64
	for _, it := range items {
		total += it.Size
	}
	return UninstallDetail{App: app, Items: items, TotalSize: total}
}

// uninstallApp preserves the macOS UninstallApp behavior.
func uninstallApp(path string) UninstallResult {
	name, bid, _ := readBundleInfo(filepath.Join(path, "Contents", "Info.plist"))
	app := AppInfo{ID: path, Publisher: "", Name: name, Path: path, BundleID: bid}
	res := UninstallResult{Path: path}
	items := uninstallPaths(app)
	if len(items) == 0 || items[0].Kind != "app" {
		res.Failed = append(res.Failed, path+": not-app")
		return res
	}
	// A running app would recreate what is removed under it, or keep its
	// moved bundle running.
	g := probeUsage()
	if g.inUse(resolvePath(path)) || g.running[strings.ToLower(bid)] {
		res.Failed = append(res.Failed, path+": "+errInUse.Error())
		return res
	}
	for _, item := range items {
		if item.Kind != "app" && g.inUse(resolvePath(item.Path)) {
			res.Failed = append(res.Failed, item.Path+": "+errInUse.Error())
			continue
		}
		if err := trashItem(item.Path); err != nil {
			res.Failed = append(res.Failed, item.Path+": "+err.Error())
			if item.Kind == "app" {
				return res
			}
			continue
		}
		res.Moved++
		res.Freed += item.Size
	}
	return res
}

// scanOrphans preserves the macOS ScanOrphans behavior.
func scanOrphans(ctx context.Context, ch *mygo.Channel[OrphanItem]) error {
	start := time.Now()
	_, total := collectOrphans(ctx, func(o OrphanItem) {
		_ = ch.Send(o)
	})
	// send a terminal sentinel so the frontend knows the scan finished
	_ = ch.Send(OrphanItem{Kind: "__done", Size: total, Path: fmt.Sprintf("%d", time.Since(start).Milliseconds())})
	return nil
}
