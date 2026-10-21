package main

import (
	"path"
	"strconv"
	"strings"
)

// statusPackageFamilyName is enabled by Windows; nil leaves Darwin's identity rules unchanged.
var statusPackageFamilyName func(int32) (string, error)

// windowsPackageFamilyPublisher extracts the publisher from a valid package family name.
func windowsPackageFamilyPublisher(family string) string {
	name, publisher, ok := strings.Cut(family, "_")
	if !ok || len(name) < 3 || len(name) > 50 || !windowsPackageToken(name) || len(publisher) != 13 {
		return ""
	}
	publisher = strings.ToLower(publisher)
	if strings.Trim(publisher, "abcdefghijklmnopqrstuvwxyz0123456789") != "" {
		return ""
	}
	return publisher
}

// windowsPackagePathPublisher extracts a package publisher from a WindowsApps directory.
func windowsPackagePathPublisher(exe string) string {
	exe = windowsUsagePath(exe)
	if exe == "" {
		return ""
	}
	parts := strings.Split(path.Clean(strings.ReplaceAll(exe, `\`, "/")), "/") // slash-ok: parses Windows paths on every host
	for i := 0; i+2 < len(parts); i++ {
		if !strings.EqualFold(parts[i], "WindowsApps") {
			continue
		}
		full := strings.Split(parts[i+1], "_")
		if len(full) != 5 {
			continue
		}
		publisher := windowsPackageFamilyPublisher(full[0] + "_" + full[4])
		if publisher == "" || len(full[3]) > 30 || full[3] != "" && !windowsPackageToken(full[3]) {
			continue
		}
		switch strings.ToLower(full[2]) {
		case "x86", "x64", "arm", "arm64", "neutral", "x86a64":
		default:
			continue
		}
		version := strings.Split(full[1], ".")
		if len(version) != 4 {
			continue
		}
		valid := true
		for _, number := range version {
			if _, err := strconv.ParseUint(number, 10, 16); err != nil || number[0] < '0' || number[0] > '9' {
				valid = false
				break
			}
		}
		if valid {
			return publisher
		}
	}
	return ""
}

// windowsPackageToken validates the ASCII characters allowed in package names and resource IDs.
func windowsPackageToken(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
			return false
		}
	}
	return true
}

// windowsBuiltinPackage prefers the native package identity and falls back when the query fails.
func windowsBuiltinPackage(pid int32, exe string, lookup func(int32) (string, error)) bool {
	family, err := lookup(pid)
	publisher := windowsPackageFamilyPublisher(family)
	if err != nil {
		publisher = windowsPackagePathPublisher(exe)
	}
	return publisher == "8wekyb3d8bbwe" || publisher == "cw5n1h2txyewy"
}
