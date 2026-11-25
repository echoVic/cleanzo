package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// appInstaller identifies one attachment, including the image and provider PID
// so an attachment that reuses a volume name is not mistaken for the preview.
type appInstaller struct {
	mount, device, image string
	pid                  int
}

func (i appInstaller) id() string {
	if i.mount == "" {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%q %q %q %d", i.mount, i.device, i.image, i.pid))))
}

func mountedInstallers() []appInstaller {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/hdiutil", "info", "-plist").Output()
	if err != nil {
		return nil
	}
	convert := exec.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", "-")
	convert.Stdin = bytes.NewReader(out)
	out, err = convert.Output()
	if err != nil {
		return nil
	}
	return parseMountedInstallers(out)
}

func parseMountedInstallers(data []byte) []appInstaller {
	var info struct {
		Images []struct {
			Image     string `json:"image-path"`
			PID       int    `json:"hdid-pid"`
			Writeable *bool  `json:"writeable"`
			Entities  []struct {
				Mount  string `json:"mount-point"`
				Device string `json:"dev-entry"`
			} `json:"system-entities"`
		} `json:"images"`
	}
	if json.Unmarshal(data, &info) != nil {
		return nil
	}
	var installers []appInstaller
	for _, image := range info.Images {
		if !filepath.IsAbs(image.Image) || !strings.EqualFold(filepath.Ext(image.Image), ".dmg") || image.Writeable == nil || *image.Writeable {
			continue
		}
		var candidate appInstaller
		mountCount := 0
		for _, entry := range image.Entities {
			if entry.Mount == "" {
				continue
			}
			mountCount++
			if filepath.IsAbs(entry.Mount) && entry.Mount != "/" && strings.HasPrefix(entry.Device, "/dev/disk") {
				candidate = appInstaller{entry.Mount, entry.Device, image.Image, image.PID}
			}
		}
		if mountCount == 1 && candidate.mount != "" {
			installers = append(installers, candidate)
		}
	}
	return installers
}

// An installer volume must contain exactly one top-level app. A name match
// would also pick up unrelated disks, so compare the bundle ID instead.
func findAppInstaller(bundleID string, installers []appInstaller) appInstaller {
	if bundleID == "" {
		return appInstaller{}
	}
	var match appInstaller
	for _, candidate := range installers {
		entries, err := os.ReadDir(candidate.mount)
		if err != nil {
			continue
		}
		var bundle string
		for _, entry := range entries {
			if !strings.EqualFold(filepath.Ext(entry.Name()), ".app") {
				continue
			}
			if bundle != "" || !entry.IsDir() {
				bundle = ""
				break
			}
			bundle = entry.Name()
		}
		if bundle == "" {
			continue
		}
		plist := filepath.Join(candidate.mount, bundle, "Contents", "Info.plist")
		if resolveUncached(plist) != filepath.Join(resolveUncached(candidate.mount), bundle, "Contents", "Info.plist") {
			continue
		}
		if _, bid, _ := readBundleInfo(plist); !strings.EqualFold(bid, bundleID) {
			continue
		}
		if match.mount != "" {
			return appInstaller{}
		}
		match = candidate
	}
	return match
}

func ejectAppInstaller(bundleID, installerID string) bool {
	candidate := findAppInstaller(bundleID, mountedInstallers())
	if candidate.mount == "" || candidate.id() != installerID {
		return false
	}
	var fs unix.Statfs_t
	if unix.Statfs(candidate.mount, &fs) != nil || unix.ByteSliceToString(fs.Mntfromname[:]) != candidate.device || fs.Flags&unix.MNT_RDONLY == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "/usr/bin/hdiutil", "detach", "-quiet", candidate.device).Run() == nil
}
