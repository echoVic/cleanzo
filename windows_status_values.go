package main

import (
	"strconv"
	"strings"
)

// memoryFromWindows maps physical memory and commit bytes into the existing status fields.
func memoryFromWindows(total, available, nonPaged, cache, commit, limit uint64) MemoryStatus {
	if total == 0 {
		return MemoryStatus{}
	}
	used := total - min(total, available)
	pressure := uint64(1)
	freeRatio := float64(available) / float64(total)
	commitRatio := 0.0
	if limit > 0 {
		commitRatio = float64(commit) / float64(limit)
	}
	if freeRatio < 0.05 || commitRatio > 0.95 {
		pressure = 4
	} else if freeRatio < 0.10 || commitRatio > 0.85 {
		pressure = 2
	}
	return MemoryStatus{Total: total, Used: used, Wired: nonPaged, App: used - min(used, nonPaged), Cached: cache, SwapTotal: limit - min(limit, total), SwapUsed: commit - min(commit, used), Pressure: pressure}
}

// windowsVersion brands client Windows by build rather than the registry's stale product name.
func windowsVersion(product, display, build string) string {
	number, _ := strconv.Atoi(build)
	if number >= 22000 {
		product = "Windows 11"
	} else if number >= 10240 {
		product = "Windows 10"
	}
	if !strings.HasPrefix(product, "Windows") {
		product = "Windows"
	}
	if display != "" {
		product += " " + display
	}
	if build != "" {
		product += " (" + build + ")"
	}
	return product
}
