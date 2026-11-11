package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
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

// appUninstallDetail preserves the macOS AppUninstallDetail behavior. The
// app's size is that of its "app" item, so the bundle is walked once. What it
// lists is kept for the uninstall its confirm leads to (uninstallApp).
func appUninstallDetail(path string) UninstallDetail {
	apps := []AppInfo{appMetadata(path)}
	if ids, paths, err := runningApps(); err == nil {
		markRunning(apps, ids, paths)
	}
	app := apps[0]
	items := uninstallPaths(app)
	if len(items) > 0 && items[0].Kind == "app" {
		app.Size = items[0].Size
	} else {
		app.Size, _ = scanPath(path)
	}
	var total int64
	for _, it := range items {
		total += it.Size
	}
	rememberUninstallListed(path, items)
	return UninstallDetail{App: app, Items: items, TotalSize: total}
}

// uninstallListedMax bounds how many apps' last uninstall details are kept.
const uninstallListedMax = 32

// uninstallListed holds the paths the last uninstall detail of each app
// listed, for the apps whose details were read last, oldest first. The
// confirm shows a detail, so the uninstall after it moves no more than that.
var uninstallListed struct {
	sync.Mutex
	apps []listedUninstall
}

// listedUninstall is what one uninstall detail listed for app.
type listedUninstall struct {
	app   string
	paths map[string]bool
}

// rememberUninstallListed records what a detail of app listed, in place of
// what an earlier one did. A detail without the app's bundle offers nothing
// to uninstall and leaves no record.
func rememberUninstallListed(app string, items []UninstallItem) {
	uninstallListed.Lock()
	defer uninstallListed.Unlock()
	uninstallListed.apps = slices.DeleteFunc(uninstallListed.apps, func(l listedUninstall) bool { return l.app == app })
	if len(items) == 0 || items[0].Kind != "app" {
		return
	}
	paths := make(map[string]bool, len(items))
	for _, it := range items {
		paths[it.Path] = true
	}
	uninstallListed.apps = append(uninstallListed.apps, listedUninstall{app: app, paths: paths})
	if over := len(uninstallListed.apps) - uninstallListedMax; over > 0 {
		uninstallListed.apps = slices.Delete(uninstallListed.apps, 0, over)
	}
}

// takeUninstallListed returns what the last detail of app listed and drops
// it; false when none is on record.
func takeUninstallListed(app string) (map[string]bool, bool) {
	uninstallListed.Lock()
	defer uninstallListed.Unlock()
	i := slices.IndexFunc(uninstallListed.apps, func(l listedUninstall) bool { return l.app == app })
	if i < 0 {
		return nil, false
	}
	paths := uninstallListed.apps[i].paths
	uninstallListed.apps = slices.Delete(uninstallListed.apps, i, i+1)
	return paths, true
}

// uninstallApp moves the app's bundle and its related files to the Trash.
// They are found again, and when a detail of the app is on record, only those
// it listed are moved: one that appeared since its confirm stays. An
// uninstall refused before moving anything leaves the detail on record for
// the next try; one that goes ahead drops it.
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
	// moved bundle running. A failed probe says nothing about whether it runs.
	g := probeUsage()
	if g.failed {
		res.Failed = append(res.Failed, path+": "+errUsageUnknown.Error())
		return res
	}
	if g.inUse(resolvePath(path)) || g.running[strings.ToLower(bid)] {
		res.Failed = append(res.Failed, path+": "+errInUse.Error())
		return res
	}
	if listed, ok := takeUninstallListed(path); ok {
		items = slices.DeleteFunc(items, func(it UninstallItem) bool { return !listed[it.Path] })
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

// scanOrphans streams the macOS leftovers. A failed usage probe would hide
// every candidate as in use, so the scan reports usage-unknown instead of
// finishing with nothing found.
func scanOrphans(ctx context.Context, ch *mygo.Channel[OrphanItem]) error {
	start := time.Now()
	g := probeUsage()
	if g.failed {
		return errUsageUnknown
	}
	_, total := collectOrphansWith(ctx, g, func(o OrphanItem) {
		_ = ch.Send(o)
	})
	// send a terminal sentinel so the frontend knows the scan finished
	_ = ch.Send(OrphanItem{Kind: "__done", Size: total, Path: fmt.Sprintf("%d", time.Since(start).Milliseconds())})
	return nil
}
