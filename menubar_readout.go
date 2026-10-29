package main

// menubar_readout.go turns what the menu bar readings read into the text their
// icons show (design: docs/superpowers/specs/2026-10-29-menubar-design.md
// §三.1): a small label above a value or, for the network, the download rate
// above the upload rate. The rules are pure, so they test on any platform;
// menubar_draw_darwin.go draws the text.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// menuBarReading is one sample of what the readings show. A reading that does
// not show is not read and stays unknown: CPU, GPU, NetDown, NetUp and
// Battery -1; MemTotal, Free and ChipTemp 0.
type menuBarReading struct {
	CPU      float64 // % busy, the whole CPU, as the home page's CPU card
	MemUsed  uint64  // bytes, the home page's memory used
	MemTotal uint64  // bytes
	Pressure uint64  // kern.memorystatus_vm_pressure_level: 1 normal, 2 warn, 4 critical
	GPU      float64 // % device utilization
	NetDown  float64 // bytes/s over Wi-Fi and Ethernet
	NetUp    float64 // bytes/s
	Free     int64   // bytes free on the startup disk
	ChipTemp float64 // °C, the hottest SoC die sensor
	Battery  int     // % charge
}

// unknownMenuBarReading is a reading before anything is read.
func unknownMenuBarReading() menuBarReading {
	return menuBarReading{CPU: -1, GPU: -1, NetDown: -1, NetUp: -1, Battery: -1}
}

// menuBarText is what one icon shows, Top above Bottom. With Label, Top is the
// reading's small name and Bottom its value; without, as for the network, both
// lines are values. Tone colors the value: "" as the menu bar's text, "warn"
// orange and "crit" red, for memory under pressure (spec §三.1).
type menuBarText struct {
	Top, Bottom string
	Label       bool
	Tone        string
}

// oneDecimal writes v with one decimal, halves rounded up as toFixed(1) rounds
// them on the page.
func oneDecimal(v float64) string {
	return strconv.FormatFloat(math.Round(v*10)/10, 'f', 1, 64)
}

// menuBarPct writes a percentage rounded to a whole number; "—" when unknown.
func menuBarPct(v float64) string {
	if !(v >= 0) { // NaN too
		return "—"
	}
	return fmt.Sprintf("%d%%", int(math.Round(math.Min(v, 100))))
}

// menuBarRate writes bytes per second the short way the menu bar has room for,
// in decimal units like the home page's rates: "156K", "1.2M", "12M", "1.3G";
// "—" when unknown. The unit is chosen after rounding, so it never writes
// "1000K".
func menuBarRate(bps float64) string {
	switch {
	case !(bps >= 0):
		return "—"
	case bps < 999.5e3:
		return fmt.Sprintf("%dK", int(math.Round(bps/1e3)))
	case bps < 9.95e6:
		return oneDecimal(bps/1e6) + "M"
	case bps < 999.5e6:
		return fmt.Sprintf("%dM", int(math.Round(bps/1e6)))
	case bps < 9.95e9:
		return oneDecimal(bps/1e9) + "G"
	default:
		return fmt.Sprintf("%dG", int(math.Round(bps/1e9)))
	}
}

// menuBarSize writes free space as the home page's disk card does (fmtMem in
// src/react/status/format.ts): 1024-based, from 1 GB on with one decimal and a
// trailing ".0" dropped, whole MB below; "—" when unknown.
func menuBarSize(n int64) string {
	switch {
	case n <= 0:
		return "—"
	case n >= 1<<30:
		return strings.TrimSuffix(oneDecimal(float64(n)/(1<<30)), ".0") + " GB"
	default:
		return fmt.Sprintf("%d MB", int(math.Round(float64(n)/(1<<20))))
	}
}

// menuBarTextFor is what item's icon shows for reading r (spec §三.1). text
// translates: nativeText, or a stand-in in tests.
func menuBarTextFor(item string, r menuBarReading, text func(string, map[string]string) string) menuBarText {
	label := func(value string) menuBarText {
		return menuBarText{Top: text("menubar.label."+item, nil), Bottom: value, Label: true}
	}
	switch item {
	case "cpu":
		return label(menuBarPct(r.CPU))
	case "mem":
		pct := -1.0
		if r.MemTotal > 0 {
			pct = float64(r.MemUsed) / float64(r.MemTotal) * 100
		}
		t := label(menuBarPct(pct))
		switch r.Pressure {
		case 2:
			t.Tone = "warn"
		case 4:
			t.Tone = "crit"
		}
		return t
	case "gpu":
		return label(menuBarPct(r.GPU))
	case "net":
		return menuBarText{Top: "↓" + menuBarRate(r.NetDown), Bottom: "↑" + menuBarRate(r.NetUp)}
	case "disk":
		if r.Free <= 0 {
			return label("—")
		}
		return label(text("menubar.value.free", map[string]string{"size": menuBarSize(r.Free)}))
	case "temp":
		if r.ChipTemp <= 0 {
			return label("—")
		}
		return label(fmt.Sprintf("%d°", int(math.Round(r.ChipTemp))))
	case "battery":
		if r.Battery < 0 {
			return label("—")
		}
		return label(fmt.Sprintf("%d%%", min(r.Battery, 100)))
	}
	return menuBarText{}
}

// menuBarWidest is the text item's icon keeps room for, so that it does not
// change width with its value (spec §三.1): its widest usual value. The digits
// all have one width, so 0 stands for any digit.
func menuBarWidest(item string, text func(string, map[string]string) string) menuBarText {
	if item == "net" {
		return menuBarText{Top: "↓000K", Bottom: "↑000K"}
	}
	w := menuBarTextFor(item, menuBarReading{}, text)
	w.Tone = ""
	switch item {
	case "disk":
		w.Bottom = text("menubar.value.free", map[string]string{"size": "000.0 GB"})
	case "temp":
		w.Bottom = "100°"
	default:
		w.Bottom = "100%"
	}
	return w
}
