//go:build windows

package main

import (
	"log"
	"path/filepath"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

var shellChangeNotify = windows.NewLazySystemDLL("shell32.dll").NewProc("SHChangeNotify")

// NSIS replaces the executable at the same path. Explorer can keep its old
// icon until the Shell cache is invalidated; do this before the first window
// appears, once per installed version, including archive/delta updates.
func refreshApplicationIcon() {
	err := refreshShellIconOnce(filepath.Join(filepath.Dir(configPath()), "shell-icon-version"), mygo.App.Version(), func() error {
		if err := shellChangeNotify.Find(); err != nil {
			return err
		}
		// SHCNE_ASSOCCHANGED | SHCNF_IDLIST | SHCNF_FLUSH. No files, pins,
		// associations or OS settings are changed. The API returns void.
		shellChangeNotify.Call(0x08000000, 0x2000, 0, 0)
		return nil
	})
	if err != nil {
		log.Printf("refresh application icon: %v", err)
	}
}
