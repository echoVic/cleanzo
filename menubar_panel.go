package main

import (
	"cmp"
	"maps"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// panelText is nativeText, or the key-and-vars stand-in in tests.
type panelText = func(key string, vars map[string]string) string

// The panel limits follow the web's lists, confirmation and histories.
const (
	panelTop            = 5
	panelQuitConfirm    = 3 * time.Second
	panelDiskSparkFloor = 1_000_000
	panelSparkPoints    = 30
	panelDiagSamples    = 3
	panelBusy           = 80.0
	panelIdleBelow      = 50.0
	panelGiB            = 1 << 30
	panelMiB            = 1 << 20
)

// panelRound follows Math.round: a half goes toward positive infinity.
// Adding 0.5 first would round a value just below a half up too early.
func panelRound(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v == 0 {
		return v
	}
	f := math.Floor(v)
	if v-f >= 0.5 {
		return f + 1
	}
	return f
}

// panelNumber follows String(number), including its exponent boundaries.
func panelNumber(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Infinity"
	case math.IsInf(v, -1):
		return "-Infinity"
	case v == 0:
		return "0"
	case math.Abs(v) >= 1e-6 && math.Abs(v) < 1e21:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		s := strconv.FormatFloat(v, 'e', -1, 64)
		s = strings.Replace(s, "e-0", "e-", 1)
		return strings.Replace(s, "e+0", "e+", 1)
	}
}

// panelFixed follows toFixed(1). It rounds the original binary value, with
// exact halves up in magnitude; multiplying a float by ten can lose which
// side of the half it was on (1.15 becomes 1.1, while 1.25 becomes 1.3).
func panelFixed(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) >= 1e21 {
		return panelNumber(v)
	}
	r := new(big.Rat).SetFloat64(math.Abs(v))
	n := new(big.Int).Mul(r.Num(), big.NewInt(10))
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(n, r.Denom(), rem)
	if rem.Lsh(rem, 1).Cmp(r.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	s := q.String()
	if len(s) == 1 {
		s = "0" + s
	}
	s = s[:len(s)-1] + "." + s[len(s)-1:]
	if v < 0 {
		s = "-" + s
	}
	return s
}

// panelMem is fmtMem: zero is unknown, MB are whole, GB have one decimal
// with a trailing .0 removed. The units are binary, as on the status page.
func panelMem(n float64) string {
	if n == 0 || math.IsNaN(n) {
		return "—"
	}
	if n >= panelGiB {
		return strings.TrimSuffix(panelFixed(n/panelGiB), ".0") + " GB"
	}
	return panelNumber(panelRound(n/panelMiB)) + " MB"
}

// panelSplitMem is splitMem: the hero's number and its small unit.
func panelSplitMem(n float64) (value, unit string) {
	s := panelMem(n)
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// panelPct is fmtPct, without the menu bar readout's 100% ceiling.
func panelPct(v float64) string {
	return panelNumber(math.Max(0, panelRound(v))) + "%"
}

// panelRate is fmtRate: decimal units, promoted at the web's boundaries.
func panelRate(bps float64) (value, unit string) {
	if !(bps >= 0) {
		return "—", ""
	}
	if bps >= 999.95e6 {
		return panelFixed(bps / 1e9), "GB/s"
	}
	if bps >= 999.5e3 {
		return panelFixed(bps / 1e6), "MB/s"
	}
	return panelNumber(panelRound(bps / 1e3)), "KB/s"
}

// panelRateText is fmtRateText, joining a known value and unit.
func panelRateText(bps float64) string {
	v, u := panelRate(bps)
	if u != "" {
		return v + " " + u
	}
	return v
}

// panelTemp is fmtTemp: positive temperatures, rounded to whole degrees.
func panelTemp(c float64) string {
	if c > 0 {
		return panelNumber(panelRound(c)) + " °C"
	}
	return "—"
}

// panelDec is fmtDec, the manufacturer's decimal capacity units.
func panelDec(n float64) string {
	if !(n > 0) {
		return "—"
	}
	v, unit := n/1e9, "GB"
	if n >= 1e12 {
		v, unit = n/1e12, "TB"
	}
	s := strings.TrimSuffix(panelFixed(v), ".0")
	if v >= 100 {
		s = panelNumber(panelRound(v))
	}
	return s + " " + unit
}

// panelNetMax is netSparkMax, with the status page's 100 KB/s floor.
func panelNetMax(hist []float64) float64 {
	v := 100_000.0
	for _, n := range hist {
		v = math.Max(v, n)
	}
	return v
}

// panelDisplayText is displayText: name or fallback, then a known mode.
func panelDisplayText(d DisplayInfo, fallback string) string {
	name := strings.TrimSpace(d.Name)
	if name == "" {
		name = fallback
	}
	if d.Width > 0 && d.Height > 0 {
		name += " " + panelDisplayMode(d)
	}
	return name
}

// panelDisplayMode returns a known resolution and optional refresh rate.
func panelDisplayMode(d DisplayInfo) string {
	if !(d.Width > 0 && d.Height > 0) {
		return ""
	}
	s := strconv.Itoa(d.Width) + "×" + strconv.Itoa(d.Height)
	if d.Hz > 0 {
		s += " @" + strconv.Itoa(d.Hz) + "Hz"
	}
	return s
}

// panelSSDWarnings is ssdWarnings, in NVMe bit order.
func panelSSDWarnings(bits int) []string {
	keys := []string{
		"status.ssd.warn.spare", "status.ssd.warn.temp",
		"status.ssd.warn.reliability", "status.ssd.warn.readonly",
		"status.ssd.warn.backup",
	}
	out := []string{}
	for i, key := range keys {
		if (bits>>i)&1 != 0 {
			out = append(out, key)
		}
	}
	if len(out) == 0 && bits != 0 {
		out = append(out, "status.ssd.warn.other")
	}
	return out
}

// panelIconHue is iconColor's hue in degrees. The hash uses UTF-16 code
// units and wraps at 32 bits, as charCodeAt and >>> 0 do on the web.
func panelIconHue(name string) float64 {
	var h uint32
	for _, c := range utf16.Encode([]rune(name)) {
		h = h*31 + uint32(c)
	}
	palette := [...]uint32{
		0x0c9d66, 0x3b82f6, 0x8b5cf6, 0xf59e0b,
		0xef4444, 0x14b8a6, 0x6366f1, 0xec4899,
	}
	c := palette[h%uint32(len(palette))]
	r, g, b := float64(c>>16)/255, float64((c>>8)&255)/255, float64(c&255)/255
	hi, lo := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	delta := hi - lo
	if delta == 0 {
		return 0
	}
	switch hi {
	case r:
		v := (g - b) / delta
		if g < b {
			v += 6
		}
		return v * 60
	case g:
		return ((b-r)/delta + 2) * 60
	default:
		return ((r-g)/delta + 4) * 60
	}
}

// panelSortRows is sortRows: compare displayed numbers, then names ignoring
// case, then raw names. Ties may differ from the web's localeCompare order.
// It leaves the sample alone.
func panelSortRows(rows []ProcessRow, by string) []ProcessRow {
	out := append([]ProcessRow{}, rows...)
	shown := func(r ProcessRow) float64 {
		if by == "cpu" {
			return math.Max(0, panelRound(r.CPU))
		}
		n := float64(r.Memory)
		if n >= panelGiB {
			return (panelRound((n/panelGiB)*10) / 10) * panelGiB
		}
		return panelRound(n/panelMiB) * panelMiB
	}
	slices.SortStableFunc(out, func(a, b ProcessRow) int {
		d := shown(b) - shown(a)
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			cmp.Compare(a.Name, b.Name),
		)
	})
	return out
}

// panelEst is est: estimates carry ≈, except for an unknown reading.
func panelEst(text string, estimated bool) string {
	if estimated && text != "—" {
		return "≈ " + text
	}
	return text
}

// panelRefreshFrozen is refreshFrozen: keep rows and order, replace their
// readings by key, and keep a missing row's previous reading.
func panelRefreshFrozen(frozen []ProcessRow, s ProcessSample) []ProcessRow {
	byKey := make(map[string]ProcessRow, len(s.Rows))
	for _, r := range s.Rows {
		byKey[r.Key] = r
	}
	out := make([]ProcessRow, 0, len(frozen))
	for _, r := range frozen {
		if next, ok := byKey[r.Key]; ok {
			r = next
		}
		out = append(out, r)
	}
	return out
}

// panelPush is pushCpu and trimSpark, keeping the most recent readings.
func panelPush(hist []float64, v float64, keep int) []float64 {
	out := append(append([]float64{}, hist...), v)
	if len(out) > keep {
		out = out[len(out)-keep:]
	}
	return out
}

// panelGrowthHint is growthHint with no crash apps: the largest alert not
// ignored, only while supported and monitoring is on. Ties keep order.
func panelGrowthHint(g *GrowthState) *GrowthAlert {
	if g == nil || !g.Supported || !g.Config.Enabled {
		return nil
	}
	var top *GrowthAlert
	for i := range g.Alerts {
		a := &g.Alerts[i]
		if !slices.Contains(g.Config.Ignored, a.Key) &&
			(top == nil || float64(a.Bytes) > float64(top.Bytes)) {
			top = a
		}
	}
	return top
}

// panelLink is a footer's translation key and destination in the main window.
type panelLink struct {
	Key    string
	Target MenuBarTarget
}

// panelTitle returns PANEL[item].title, the panel's translation key.
func panelTitle(item string) string { return "menubar.panel." + item }

// panelLinks returns PANEL[item].links, the panel's footer destinations.
func panelLinks(item string) []panelLink {
	switch item {
	case "cpu", "mem":
		return []panelLink{{"menubar.panel.openProcess", MenuBarTarget{Page: "process", Tab: item}}}
	case "disk":
		return []panelLink{
			{"menubar.panel.clean", MenuBarTarget{Page: "clean"}},
			{"menubar.panel.view", MenuBarTarget{Page: "process", Tab: "disk"}},
		}
	default:
		return []panelLink{{"menubar.panel.openHome", MenuBarTarget{Page: "status"}}}
	}
}

// panelHero is hero: the big number and unit, or a dash when unknown.
func panelHero(item string, s *StatusSample) (value, unit string) {
	if s == nil {
		return "—", ""
	}
	m := s.Machine
	switch item {
	case "cpu":
		return panelNumber(math.Max(0, panelRound(s.CPU.Usage))), "%"
	case "mem":
		if s.Memory.Total > 0 {
			return panelNumber(panelRound((float64(s.Memory.Used) / float64(s.Memory.Total)) * 100)), "%"
		}
	case "gpu":
		if m != nil && m.GPU >= 0 {
			return panelNumber(panelRound(m.GPU)), "%"
		}
	case "net":
		if m != nil && m.NetDown >= 0 {
			return panelRate(m.NetDown)
		}
	case "disk":
		if s.Disk.Total > 0 {
			if s.Disk.Free <= 0 {
				return "0", "MB"
			}
			return panelSplitMem(float64(s.Disk.Free))
		}
	case "temp":
		if m != nil && m.ChipTemp > 0 {
			return panelNumber(panelRound(m.ChipTemp)), "°C"
		}
	case "battery":
		if s.Battery != nil {
			return strconv.Itoa(s.Battery.Percent), "%"
		}
	}
	return "—", ""
}

// panelSubLine is subLine: specifications and extra readings, when known.
func panelSubLine(item string, s *StatusSample, info *MachineInfo, sys *SystemInfo, t panelText) string {
	parts := []string{}
	var m *MachineSample
	if s != nil {
		m = s.Machine
	}
	switch item {
	case "cpu":
		if sys != nil && sys.Chip != "" {
			parts = append(parts, sys.Chip)
		}
		if info != nil && info.PerfCores > 0 && info.EffCores > 0 {
			parts = append(parts, t("status.spec.cpuSplit", map[string]string{
				"n": strconv.Itoa(info.PerfCores + info.EffCores),
				"p": strconv.Itoa(info.PerfCores), "e": strconv.Itoa(info.EffCores),
			}))
		} else if sys != nil && sys.Cores > 0 {
			parts = append(parts, t("status.spec.cpu", map[string]string{"n": strconv.FormatUint(sys.Cores, 10)}))
		}
		if s != nil {
			parts = append(parts, t("status.cpu.load", map[string]string{
				"l1": panelFixed(s.CPU.Load1), "l5": panelFixed(s.CPU.Load5), "l15": panelFixed(s.CPU.Load15),
			}))
		}
	case "mem":
		if s != nil && s.Memory.Total > 0 {
			parts = append(parts, t("menubar.panel.memSub", map[string]string{
				"used": panelMem(float64(s.Memory.Used)), "total": panelMem(float64(s.Memory.Total)),
			}))
		}
	case "gpu":
		if info != nil && info.GPUCores > 0 {
			parts = append(parts, t("status.spec.gpu", map[string]string{"n": strconv.Itoa(info.GPUCores)}))
		}
	case "net":
		if m != nil {
			if m.NetKind == "wifi" {
				parts = append(parts, t("status.net.wifi", nil))
			} else if m.NetKind == "ethernet" {
				parts = append(parts, t("status.net.ethernet", nil))
			}
			if m.NetUp >= 0 {
				parts = append(parts, "↑ "+panelRateText(m.NetUp))
			}
		}
	case "disk":
		if s != nil && s.Disk.Total > 0 {
			parts = append(parts, t("status.disk.avail", nil), t("status.disk.totalOnly", map[string]string{
				"total": panelMem(float64(s.Disk.Total)),
			}))
		}
	case "temp":
		if m != nil && m.ChipTemp > 0 {
			parts = append(parts, t("status.health.chipTemp", nil))
		}
	}
	return strings.Join(parts, " · ")
}

// panelDiag is diagnosis's text with Tone "ok", "warn" or "crit".
type panelDiag struct{ Tone, Text string }

// panelDiagInput is DiagInput: readings and short histories for diagnosis.
type panelDiagInput struct {
	S       *StatusSample
	Proc    *ProcessSample
	CPUHist []float64
	GPUHist []float64
	Growth  *GrowthState
}

// panelHistAll reports whether the last three readings all match.
func panelHistAll(hist []float64, matches func(float64) bool) bool {
	if len(hist) < panelDiagSamples {
		return false
	}
	for _, v := range hist[len(hist)-panelDiagSamples:] {
		if !matches(v) {
			return false
		}
	}
	return true
}

// panelDiagnosis is diagnosis: each panel's one-line reading, or nil until
// its inputs arrive. CPU's short history still names its largest row.
func panelDiagnosis(item string, d panelDiagInput, t panelText) *panelDiag {
	s := d.S
	var m *MachineSample
	if s != nil {
		m = s.Machine
	}
	busy := func(h []float64) bool {
		return panelHistAll(h, func(v float64) bool { return v >= panelBusy })
	}
	idle := func(h []float64) bool {
		return panelHistAll(h, func(v float64) bool { return v < panelIdleBelow })
	}
	switch item {
	case "cpu", "mem":
		if d.Proc == nil || len(d.Proc.Rows) == 0 || (item == "mem" && s == nil) {
			return nil
		}
		top := panelSortRows(d.Proc.Rows, item)[0]
		if item == "cpu" {
			vars := map[string]string{"name": top.Name, "pct": panelEst(panelPct(top.CPU), top.Estimated)}
			if busy(d.CPUHist) {
				return &panelDiag{"warn", t("process.diag.cpu", vars)}
			}
			key := "menubar.diag.cpuTop"
			if idle(d.CPUHist) {
				key = "menubar.diag.cpuIdle"
			}
			return &panelDiag{"ok", t(key, vars)}
		}
		vars := map[string]string{"name": top.Name, "size": panelEst(panelMem(float64(top.Memory)), top.Estimated)}
		swap := ""
		if s.Memory.SwapTotal >= panelGiB {
			swap = " " + t("status.alert.swapLine", map[string]string{"size": panelMem(float64(s.Memory.SwapTotal))})
		}
		if p := s.Memory.Pressure; p == 2 || p == 4 {
			tone := "warn"
			if p == 4 {
				tone = "crit"
			}
			return &panelDiag{tone, t("process.diag.mem", vars) + swap}
		}
		return &panelDiag{"ok", t("menubar.diag.memOk", vars) + swap}
	case "gpu":
		if m == nil || m.GPU < 0 || len(d.GPUHist) < panelDiagSamples {
			return nil
		}
		if busy(d.GPUHist) {
			return &panelDiag{"warn", t("menubar.diag.gpuBusy", nil)}
		}
		key := "menubar.diag.gpuSome"
		if idle(d.GPUHist) {
			key = "menubar.diag.gpuIdle"
		}
		return &panelDiag{"ok", t(key, nil)}
	case "net":
		if m == nil {
			return nil
		}
		key := "menubar.diag.netNone"
		if m.NetKind == "wifi" {
			key = "menubar.diag.netWifi"
		} else if m.NetKind == "ethernet" {
			key = "menubar.diag.netEthernet"
		}
		return &panelDiag{"ok", t(key, nil)}
	case "disk":
		if s == nil || s.Disk.Total <= 0 {
			return nil
		}
		free := math.Max(0, float64(s.Disk.Free))
		low := free < 20*panelGiB
		key, size, tone := "menubar.diag.diskOk", "0 MB", "ok"
		if low {
			key, tone = "status.alert.diskLow", "warn"
		}
		if free > 0 {
			size = panelMem(free)
		}
		text := t(key, map[string]string{"size": size})
		if a := panelGrowthHint(d.Growth); a != nil {
			size := growthSize(a.Bytes)
			if a.AtLeast {
				size = t("growth.atLeast", map[string]string{"size": size})
			}
			key := "status.alert.growthWeek"
			if a.Window == "day" {
				key = "status.alert.growthDay"
			}
			alert := t(key, map[string]string{"app": a.Name, "days": strconv.Itoa(a.Days), "size": size})
			text += " " + t("menubar.diag.growth", map[string]string{"alert": alert})
			tone = "warn"
		}
		return &panelDiag{tone, text}
	case "temp":
		if m == nil || m.Thermal < 0 {
			return nil
		}
		n, tone := min(m.Thermal, 3), "crit"
		if n == 0 {
			tone = "ok"
		} else if n == 1 {
			tone = "warn"
		}
		return &panelDiag{tone, t("menubar.diag.thermal"+strconv.Itoa(n), nil)}
	case "battery":
		if s == nil || s.Battery == nil {
			return nil
		}
		b := s.Battery
		state := ""
		switch {
		case b.Full:
			state = t("status.battery.full", nil)
		case b.Charging:
			state = t("status.battery.charging", map[string]string{"pct": strconv.Itoa(b.Percent) + "%"})
		case b.Plugged:
			state = t("status.battery.plugged", nil)
		default:
			state = t("status.battery.onBattery", nil)
		}
		cond := strings.ToLower(strings.TrimSpace(b.Condition))
		if cond != "" && cond != "good" && cond != "normal" {
			return &panelDiag{"warn", state + " · " + t("status.battery.service", nil)}
		}
		return &panelDiag{"ok", state}
	}
	return nil
}

// panelChartPoint is chartPoint: skip a reading whose source is unknown.
func panelChartPoint(item string, s StatusSample) (float64, bool) {
	m := s.Machine
	switch item {
	case "cpu":
		return s.CPU.Usage, true
	case "mem":
		if s.Memory.Total > 0 {
			return (float64(s.Memory.Used) / float64(s.Memory.Total)) * 100, true
		}
	case "gpu":
		if m != nil && m.GPU >= 0 {
			return m.GPU, true
		}
	case "net":
		if m != nil && m.NetDown >= 0 {
			return m.NetDown, true
		}
	case "disk":
		if m != nil && m.DiskRead >= 0 && m.DiskWrite >= 0 {
			return m.DiskRead + m.DiskWrite, true
		}
	case "temp":
		if m != nil && m.ChipTemp > 0 {
			return m.ChipTemp, true
		}
	case "battery":
		if s.Battery != nil {
			return float64(s.Battery.Percent), true
		}
	}
	return 0, false
}

// panelChartMax is chartMax: the recent rate maximum with a floor, or 100.
func panelChartMax(item string, hist []float64) float64 {
	if item == "net" {
		return panelNetMax(hist)
	}
	if item == "disk" {
		v := float64(panelDiskSparkFloor)
		for _, n := range hist {
			v = math.Max(v, n)
		}
		return v
	}
	return 100
}

// panelChartTone is chartTone: "accent", "gpu", "net", "warn" or "crit".
func panelChartTone(item string, s *StatusSample) string {
	if item == "mem" && s != nil {
		if s.Memory.Pressure == 4 {
			return "crit"
		}
		if s.Memory.Pressure == 2 {
			return "warn"
		}
	}
	if item == "gpu" {
		return "gpu"
	}
	if item == "net" || item == "disk" {
		return "net"
	}
	return "accent"
}

// panelDetailRow is DetailRow: a known reading, with an optional bar or dot.
type panelDetailRow struct {
	Key, Label, Value string
	Bar               float64 // 0–100, when HasBar
	HasBar            bool
	Dot               string // "", "app", "wired" or "compressed"
}

// panelFansText returns fan speeds, their idle state, or an unknown blank.
func panelFansText(m MachineSample, t panelText) string {
	if m.FanCount == 0 {
		return t("status.health.noFans", nil)
	}
	if m.FanCount > 0 && len(m.Fans) > 0 {
		idle := true
		parts := make([]string, 0, len(m.Fans))
		for _, rpm := range m.Fans {
			idle = idle && rpm < 1
			parts = append(parts, strconv.Itoa(rpm))
		}
		if idle {
			return t("status.health.fansIdle", nil)
		}
		return strings.Join(parts, " · ") + " rpm"
	}
	return ""
}

// panelDetailRows is detailRows: known values in the web panel's order.
// Dot names select the native theme's memory segments instead of CSS vars.
func panelDetailRows(item string, s *StatusSample, info *MachineInfo, t panelText) []panelDetailRow {
	rows := []panelDetailRow{}
	if s == nil {
		return rows
	}
	m := s.Machine
	add := func(key, label, value string) {
		if value != "" && value != "—" {
			rows = append(rows, panelDetailRow{Key: key, Label: label, Value: value})
		}
	}
	switch item {
	case "cpu":
		if m != nil {
			for _, v := range []struct {
				key   string
				cpu   float64
				cores int
			}{
				{"perf", m.PerfCPU, 0}, {"eff", m.EffCPU, 0},
			} {
				if v.cpu < 0 || math.IsNaN(v.cpu) {
					continue
				}
				if info != nil {
					if v.key == "perf" {
						v.cores = info.PerfCores
					} else {
						v.cores = info.EffCores
					}
				}
				label := t("status.cpu."+v.key, nil)
				if v.cores > 0 {
					label = t("status.cpu."+v.key+"N", map[string]string{"n": strconv.Itoa(v.cores)})
				}
				rows = append(rows, panelDetailRow{Key: v.key, Label: label,
					Value: panelPct(v.cpu), Bar: v.cpu, HasBar: true})
			}
			parts := []string{}
			if m.ChipTemp > 0 {
				parts = append(parts, panelTemp(m.ChipTemp))
			}
			if fans := panelFansText(*m, t); fans != "" {
				parts = append(parts, fans)
			}
			add("chip", t("menubar.panel.chipFans", nil), strings.Join(parts, " · "))
		}
	case "mem":
		mm := s.Memory
		for _, v := range []struct {
			key, label, dot string
			n               uint64
		}{
			{"app", "status.mem.appMem", "app", mm.App},
			{"wired", "status.mem.wired", "wired", mm.Wired},
			{"compressed", "status.mem.compressed", "compressed", mm.Compressed},
			{"cached", "status.mem.cachedLabel", "", mm.Cached},
		} {
			value := panelMem(float64(v.n))
			if value != "—" {
				rows = append(rows, panelDetailRow{Key: v.key, Label: t(v.label, nil), Value: value, Dot: v.dot})
			}
		}
		if float64(mm.SwapUsed) > 0.1*panelGiB {
			add("swap", t("menubar.panel.swap", nil), t("menubar.panel.swapValue", map[string]string{
				"used": panelMem(float64(mm.SwapUsed)), "size": panelMem(float64(mm.SwapTotal)),
			}))
		}
	case "gpu":
		if info != nil && info.GPUCores > 0 {
			add("cores", t("menubar.panel.gpuCores", nil), t("status.cpu.cores", map[string]string{"n": strconv.Itoa(info.GPUCores)}))
		}
		if m != nil && m.GPUMemory > 0 {
			add("memory", t("status.gpu.memory", nil), panelMem(float64(m.GPUMemory)))
		}
		if info != nil {
			for _, d := range info.Displays {
				label, value := "status.display.external", ""
				if d.Builtin {
					label, value = "status.display.builtin", panelDisplayMode(d)
				} else {
					value = panelDisplayText(d, t("status.display.external", nil))
				}
				add("display-"+strconv.FormatUint(uint64(d.ID), 10), t(label, nil), value)
			}
		}
	case "net":
		if m != nil && m.NetDown >= 0 {
			add("down", t("status.net.down", nil), panelRateText(m.NetDown))
		}
		if m != nil && m.NetUp >= 0 {
			add("up", t("status.net.up", nil), panelRateText(m.NetUp))
		}
	case "disk":
		var ssd, smart *SSDInfo
		if info != nil {
			ssd = info.SSD
		}
		if ssd != nil && ssd.SMART {
			smart = ssd
		}
		if s.Disk.Total > 0 {
			free := "0 MB"
			if s.Disk.Free > 0 {
				free = panelMem(float64(s.Disk.Free))
			}
			add("space", t("menubar.panel.freeTotal", nil), free+" / "+panelMem(float64(s.Disk.Total)))
		}
		if ssd != nil && ssd.Model != "" {
			add("model", t("menubar.panel.ssdModel", nil), ssd.Model)
		}
		if smart != nil {
			key := "status.ssd.ok"
			if warns := panelSSDWarnings(smart.Warning); len(warns) > 0 {
				key = warns[0]
			}
			add("health", t("status.ssd.health", nil), t(key, nil))
			add("life", t("status.ssd.life", nil), strconv.Itoa(smart.Used)+"%")
			if smart.Written > 0 {
				add("written", t("status.ssd.written", nil), panelDec(float64(smart.Written)))
			}
		}
		// The live NAND sensor wins; SMART's temperature when the
		// panel opened is the fallback.
		temp := 0.0
		if m != nil && m.DiskTemp > 0 {
			temp = m.DiskTemp
		} else if smart != nil {
			temp = float64(smart.TempC)
		}
		if temp > 0 {
			add("temp", t("status.disk.temp", nil), panelTemp(temp))
		}
		if m != nil && m.DiskRead >= 0 {
			add("read", t("status.disk.read", nil), panelRateText(m.DiskRead))
		}
		if m != nil && m.DiskWrite >= 0 {
			add("write", t("status.disk.write", nil), panelRateText(m.DiskWrite))
		}
	case "temp":
		if m != nil {
			if m.Thermal >= 0 {
				add("thermal", t("status.health.thermal", nil), t("status.health.thermal"+strconv.Itoa(min(m.Thermal, 3)), nil))
			}
			if m.ChipTemp > 0 {
				add("chip", t("status.health.chipTemp", nil), panelTemp(m.ChipTemp))
			}
			add("fans", t("status.health.fans", nil), panelFansText(*m, t))
			if m.Power > 0 {
				add("power", t("status.health.power", nil), panelNumber(panelRound(m.Power))+" W")
			}
			if m.DiskTemp > 0 {
				add("ssd", t("menubar.panel.ssdTemp", nil), panelTemp(m.DiskTemp))
			}
		}
		if s.Battery != nil && s.Battery.Temperature > 0 {
			add("battery", t("menubar.panel.batteryTemp", nil), panelTemp(s.Battery.Temperature))
		}
	case "battery":
		b := s.Battery
		if b == nil {
			break
		}
		rows = append(rows, panelDetailRow{Key: "charge", Label: t("menubar.panel.charge", nil),
			Value: strconv.Itoa(b.Percent) + "%", Bar: float64(b.Percent), HasBar: true})
		if b.MaxCapacity > 0 {
			add("health", t("status.battery.maxCap", nil), strconv.Itoa(b.MaxCapacity)+"%")
		}
		if b.Cycles > 0 {
			add("cycles", t("menubar.panel.cycles", nil), strconv.Itoa(b.Cycles))
		}
		if b.Temperature > 0 {
			add("temp", t("status.battery.temp", nil), panelTemp(b.Temperature))
		}
		if b.Plugged && b.AdapterWatts > 0 {
			add("adapter", t("status.battery.adapter", nil), strconv.Itoa(b.AdapterWatts)+" W")
		}
	}
	return rows
}

// panelMemShares is memShares: each segment's 0–100 share, or false if unknown.
func panelMemShares(m MemoryStatus) (app, wired, compressed float64, ok bool) {
	if m.Total == 0 {
		return 0, 0, 0, false
	}
	pct := func(n uint64) float64 {
		return math.Max(0, math.Min(100, (float64(n)/float64(m.Total))*100))
	}
	return pct(m.App), pct(m.Wired), pct(m.Compressed), true
}

// panelTopRows is topRows: refresh a frozen list, or return the top five rows.
func panelTopRows(s ProcessSample, by string, prev []ProcessRow, frozen bool) []ProcessRow {
	if frozen && len(prev) > 0 {
		return panelRefreshFrozen(prev, s)
	}
	rows := panelSortRows(s.Rows, by)
	return rows[:min(len(rows), panelTop)]
}

// panelGrowthTop is growthTop: the five largest positive, unignored increases.
// Ties compare keys ignoring case, then raw keys; web tie order may differ.
func panelGrowthTop(g *GrowthState) []GrowthApp {
	out := []GrowthApp{}
	if g == nil || !g.Supported || !g.Config.Enabled {
		return out
	}
	for _, a := range g.Apps {
		if !a.Ignored && a.Week != nil && a.Week.Bytes > 0 {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(a, b GrowthApp) int {
		av, bv := float64(a.Week.Bytes), float64(b.Week.Bytes)
		if av > bv {
			return -1
		}
		if av < bv {
			return 1
		}
		return cmp.Or(
			cmp.Compare(strings.ToLower(a.Key), strings.ToLower(b.Key)),
			cmp.Compare(a.Key, b.Key),
		)
	})
	return out[:min(len(out), panelTop)]
}

// panelQuitStep is one of "idle", "confirm", "quitting", "stuck",
// "forceConfirm", "forcing", "done" or "failed" in the two-click quit flow.
type panelQuitStep string

// panelQuitBook remembers exited rows that left the list, so that an app
// opened again becomes quittable. Each update leaves its input untouched.
type panelQuitBook struct {
	steps map[string]panelQuitStep
	gone  map[string]bool
}

// step returns a row's quit step, or "idle" for an unknown key.
func (b panelQuitBook) step(key string) panelQuitStep {
	if s, ok := b.steps[key]; ok {
		return s
	}
	return "idle"
}

// with returns a copy of the book with the row's quit step replaced.
func (b panelQuitBook) with(key string, s panelQuitStep) panelQuitBook {
	next := maps.Clone(b.steps)
	if next == nil {
		next = map[string]panelQuitStep{}
	}
	next[key] = s
	return panelQuitBook{steps: next, gone: b.gone}
}

// confirming reports whether a row is waiting for either confirmation click.
func (b panelQuitBook) confirming() bool {
	for _, s := range b.steps {
		if s == "confirm" || s == "forceConfirm" {
			return true
		}
	}
	return false
}

// pending reports whether a normal or forced quit is in progress.
func (b panelQuitBook) pending() bool {
	for _, s := range b.steps {
		if s == "quitting" || s == "forcing" {
			return true
		}
	}
	return false
}

// frozen is listFrozen: hover, confirmation or a pending quit holds the list.
func (b panelQuitBook) frozen(hover bool) bool {
	return hover || b.confirming() || b.pending()
}

// revertConfirms returns a book with confirms idle and force confirms stuck.
func (b panelQuitBook) revertConfirms() panelQuitBook {
	if !b.confirming() {
		return b
	}
	steps := map[string]panelQuitStep{}
	for key, s := range b.steps {
		if s == "forceConfirm" {
			steps[key] = "stuck"
		} else if s != "confirm" {
			steps[key] = s
		}
	}
	return panelQuitBook{steps: steps, gone: b.gone}
}

// sync is syncBook: return the book with vanished and reopened rows reconciled.
func (b panelQuitBook) sync(rows []ProcessRow) panelQuitBook {
	if len(b.steps) == 0 && len(b.gone) == 0 {
		return b
	}
	present := make(map[string]bool, len(rows))
	for _, r := range rows {
		present[r.Key] = true
	}
	steps, gone := maps.Clone(b.steps), maps.Clone(b.gone)
	if gone == nil {
		gone = map[string]bool{}
	}
	for key, s := range b.steps {
		if s == "done" {
			if !present[key] {
				gone[key] = true
			} else if b.gone[key] {
				delete(steps, key)
				delete(gone, key)
			}
		} else if (s == "stuck" || s == "failed") && !present[key] {
			delete(steps, key)
		}
	}
	if len(steps) == len(b.steps) && maps.Equal(gone, b.gone) {
		return b
	}
	return panelQuitBook{steps: steps, gone: gone}
}
