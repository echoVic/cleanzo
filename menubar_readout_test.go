package main

import (
	"math"
	"testing"
)

// readoutText stands in for nativeText: the key itself, and the free-space
// value with its size.
func readoutText(key string, vars map[string]string) string {
	if key == "menubar.value.free" {
		return vars["size"] + " free"
	}
	return key
}

func TestMenuBarNumbers(t *testing.T) {
	for v, want := range map[float64]string{-1: "—", 0: "0%", 50.4: "50%", 50.5: "51%", 100: "100%", 130: "100%"} {
		if got := menuBarPct(v); got != want {
			t.Errorf("menuBarPct(%v) = %q, want %q", v, got, want)
		}
	}
	if got := menuBarPct(math.NaN()); got != "—" {
		t.Errorf("menuBarPct(NaN) = %q", got)
	}
	for v, want := range map[float64]string{
		-1: "—", 0: "0K", 156_400: "156K", 999_499: "999K", 999_500: "1.0M", 1_250_000: "1.3M",
		9_949_999: "9.9M", 9_950_000: "10M", 12_400_000: "12M", 999_500_000: "1.0G", 12e9: "12G",
	} {
		if got := menuBarRate(v); got != want {
			t.Errorf("menuBarRate(%v) = %q, want %q", v, got, want)
		}
	}
	for n, want := range map[int64]string{
		0: "—", -5: "—", 512 << 20: "512 MB", 1 << 30: "1 GB", 28_669_603_840: "26.7 GB", 91 << 30: "91 GB", 1900 << 30: "1900 GB",
	} {
		if got := menuBarSize(n); got != want {
			t.Errorf("menuBarSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestMenuBarTextFor(t *testing.T) {
	r := menuBarReading{CPU: 51.2, MemUsed: 27_500 << 20, MemTotal: 32 << 30, Pressure: 1, GPU: 14,
		NetDown: 156_000, NetUp: 55_400, Free: 28_669_603_840, ChipTemp: 43.4, Battery: 100}
	for item, want := range map[string]menuBarText{
		"cpu":     {Top: "menubar.label.cpu", Bottom: "51%", Label: true},
		"mem":     {Top: "menubar.label.mem", Bottom: "84%", Label: true},
		"gpu":     {Top: "menubar.label.gpu", Bottom: "14%", Label: true},
		"net":     {Top: "↓156K", Bottom: "↑55K"},
		"disk":    {Top: "menubar.label.disk", Bottom: "26.7 GB free", Label: true},
		"temp":    {Top: "menubar.label.temp", Bottom: "43°", Label: true},
		"battery": {Top: "menubar.label.battery", Bottom: "100%", Label: true},
	} {
		if got := menuBarTextFor(item, r, readoutText); got != want {
			t.Errorf("%s: %+v, want %+v", item, got, want)
		}
	}
	for pressure, tone := range map[uint64]string{0: "", 1: "", 2: "warn", 4: "crit"} {
		r.Pressure = pressure
		if got := menuBarTextFor("mem", r, readoutText).Tone; got != tone {
			t.Errorf("pressure %d: tone %q, want %q", pressure, got, tone)
		}
		if got := menuBarTextFor("cpu", r, readoutText).Tone; got != "" {
			t.Errorf("pressure %d colors the CPU: %q", pressure, got)
		}
	}
	unknown := unknownMenuBarReading()
	for item, want := range map[string]string{"cpu": "—", "mem": "—", "gpu": "—", "net": "↑—", "disk": "—", "temp": "—", "battery": "—"} {
		if got := menuBarTextFor(item, unknown, readoutText).Bottom; got != want {
			t.Errorf("%s unknown: %q, want %q", item, got, want)
		}
	}
}

func TestMenuBarWidest(t *testing.T) {
	for item, want := range map[string]menuBarText{
		"cpu":  {Top: "menubar.label.cpu", Bottom: "100%", Label: true},
		"mem":  {Top: "menubar.label.mem", Bottom: "100%", Label: true},
		"net":  {Top: "↓000M", Bottom: "↑000M"},
		"disk": {Top: "menubar.label.disk", Bottom: "000.0 GB free", Label: true},
		"temp": {Top: "menubar.label.temp", Bottom: "100°", Label: true},
	} {
		if got := menuBarWidest(item, readoutText); got != want {
			t.Errorf("%s: %+v, want %+v", item, got, want)
		}
	}
}

func TestMenuBarLabelsAreTranslated(t *testing.T) {
	for _, tc := range []struct{ locale, key, want string }{
		{"zh-Hans", "menubar.label.mem", "内存"},
		{"en", "menubar.label.mem", "MEM"},
		{"zh-Hans", "menubar.label.battery", "电池"},
		{"en", "menubar.label.battery", "BAT"},
	} {
		if got := nativeTextFor(tc.locale, tc.key, nil); got != tc.want {
			t.Errorf("%s %s = %q, want %q", tc.locale, tc.key, got, tc.want)
		}
	}
	if got := nativeTextFor("zh-Hans", "menubar.value.free", map[string]string{"size": "26.7 GB"}); got != "26.7 GB 可用" {
		t.Errorf("free = %q", got)
	}
	for _, item := range []string{"cpu", "mem", "gpu", "disk", "temp", "battery"} {
		for _, locale := range []string{"zh-Hans", "en"} {
			if nativeTextFor(locale, "menubar.label."+item, nil) == "" {
				t.Errorf("no %s label for %s", locale, item)
			}
		}
	}
}
