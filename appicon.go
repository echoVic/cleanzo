package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// appIconPx is the side of the icons the pages show. 80 px covers the 38 px
// avatar of the uninstall list on a Retina screen; the 24 and 28 px avatars
// elsewhere scale it down.
const appIconPx = 80

// appIconRender draws the icon of an app bundle as a PNG, nil when it
// can't. Tests replace it.
var appIconRender = nativeAppIcon

// appIcons holds every icon drawn so far, by bundle path, for the life of
// the app; "" records a bundle without one.
var appIcons = struct {
	sync.Mutex
	urls map[string]string
}{urls: map[string]string{}}

// appIconDrawing keeps to one drawing at a time: a cold icon takes up to a
// few hundred milliseconds of Icon Services work, and a page asking for
// twenty at once should not take twenty threads.
var appIconDrawing sync.Mutex

// AppIcon returns the icon Finder shows for the app bundle at path, as a
// PNG data URL, or "" when there is none: not an absolute path to an .app
// folder, gone, or off macOS. Pages show the first letter of the name until
// it arrives, and instead of it when there is none. Each bundle is drawn
// once.
func (Cleaner) AppIcon(path string) (string, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), ".app") {
		return "", nil
	}
	if url, ok := cachedAppIcon(path); ok {
		return url, nil
	}
	appIconDrawing.Lock()
	defer appIconDrawing.Unlock()
	if url, ok := cachedAppIcon(path); ok { // drawn while this call waited
		return url, nil
	}
	url := ""
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		if png := appIconRender(path, appIconPx); len(png) > 0 {
			url = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
		}
	}
	appIcons.Lock()
	appIcons.urls[path] = url
	appIcons.Unlock()
	return url, nil
}

func cachedAppIcon(path string) (string, bool) {
	appIcons.Lock()
	defer appIcons.Unlock()
	url, ok := appIcons.urls[path]
	return url, ok
}
