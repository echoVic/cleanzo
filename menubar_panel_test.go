package main

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"
)

// panelTestText keeps the key and variables visible in logic tests.
func panelTestText(key string, vars map[string]string) string {
	if len(vars) == 0 {
		return key
	}
	b, _ := json.Marshal(vars)
	return key + string(b)
}

func panelEqual[T any](t *testing.T, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func panelTestSample() *StatusSample {
	return &StatusSample{
		Memory: MemoryStatus{
			Total: 32 << 30, Used: 29527900160, App: 6871947673,
			Wired: 7086696038, Compressed: 15569256448,
			Cached: 4080218931, Pressure: 1,
		},
		CPU:  CPUStatus{Usage: 51.2, Load1: 3.3, Load5: 2.5, Load15: 2.4},
		Disk: DiskStat{Total: 494 << 30, Used: 501759554355, Free: 28668906700},
		Battery: &BatteryStatus{
			Percent: 86, Charging: true, Plugged: true, Cycles: 120,
			MaxCapacity: 92, Condition: "Normal", Temperature: 31.2,
			AdapterWatts: 96,
		},
		Machine: &MachineSample{
			Thermal: 0, ChipTemp: 52.4, FanCount: 2, Fans: []int{1350, 1400},
			Power: 12.6, PerfCPU: 44, EffCPU: 72, GPU: 14,
			GPUMemory: 1610612736, DiskRead: 2e6, DiskWrite: 1e6,
			DiskTemp: 38, NetDown: 156000, NetUp: 55000, NetKind: "wifi",
			DisplaySig: "1,2",
		},
	}
}

func panelTestInfo() *MachineInfo {
	return &MachineInfo{
		PerfCores: 8, EffCores: 4, GPUCores: 19, DisplaySig: "1,2",
		Displays: []DisplayInfo{
			{ID: 1, Name: "Built-in Retina Display", Builtin: true,
				Width: 1728, Height: 1117, Hz: 120},
			{ID: 2, Name: "LS27D80xU", Width: 1920, Height: 1080, Hz: 60},
		},
		SSD: &SSDInfo{Model: "APPLE SSD AP0512Z", Capacity: 5e11,
			SMART: true, TempC: 33, Spare: 100, Used: 26, Written: 624500000000000},
	}
}

func panelTestProc() *ProcessSample {
	return &ProcessSample{Pressure: 1, Rows: []ProcessRow{
		{Key: "cli:node", Name: "node", Kind: "cli", CPU: 12.6,
			Memory: 3113851289, Procs: 1},
		{Key: "app:/applications/google chrome.app", Name: "Google Chrome",
			Kind: "app", Path: "/Applications/Google Chrome.app", CPU: 53.8,
			Memory: 33822867456, Procs: 1, Quit: true},
		{Key: "sys:WindowServer", Name: "WindowServer", Kind: "system",
			CPU: 40.2, Memory: 139586437, Procs: 1, Estimated: true},
	}}
}

func panelTestInput() panelDiagInput { return panelDiagInput{S: panelTestSample()} }

func panelTestGrowth(enabled bool) *GrowthState {
	return &GrowthState{
		Supported: true, Config: GrowthConfig{Enabled: enabled},
		Alerts: []GrowthAlert{{Key: "app:com.youqu.todesk.mac", Name: "ToDesk",
			Window: "day", Days: 1, Bytes: 6012954214,
			Folder: "/Library/Application Support/ToDesk/dumps", Total: 152 << 30}},
	}
}

func TestPanelConstantsAndLinks(t *testing.T) {
	panelEqual(t, panelTop, 5)
	panelEqual(t, panelQuitConfirm, 3*time.Second)
	panelEqual(t, panelDiskSparkFloor, 1000000)
	panelEqual(t, panelSparkPoints, 30)
	panelEqual(t, panelDiagSamples, 3)
	panelEqual(t, float64(panelBusy), 80.0)
	panelEqual(t, float64(panelIdleBelow), 50.0)
	for _, item := range menuBarItems {
		panelEqual(t, panelTitle(item), "menubar.panel."+item)
		want := []panelLink{{"menubar.panel.openHome", MenuBarTarget{Page: "status"}}}
		if item == "cpu" || item == "mem" {
			want = []panelLink{{"menubar.panel.openProcess",
				MenuBarTarget{Page: "process", Tab: item}}}
		} else if item == "disk" {
			want = []panelLink{
				{"menubar.panel.clean", MenuBarTarget{Page: "clean"}},
				{"menubar.panel.view", MenuBarTarget{Page: "process", Tab: "disk"}},
			}
		}
		panelEqual(t, panelLinks(item), want)
	}
}

func TestPanelNativeTextKeys(t *testing.T) {
	keys := map[string]bool{}
	record := func(key string, vars map[string]string) string {
		keys[key] = true
		return panelTestText(key, vars)
	}
	sys := &SystemInfo{Chip: "Apple M2 Pro", Cores: 12}
	visit := func(s *StatusSample, info *MachineInfo, sys *SystemInfo, d panelDiagInput) {
		d.S = s
		for _, item := range menuBarItems {
			record(panelTitle(item), nil)
			for _, link := range panelLinks(item) {
				record(link.Key, nil)
			}
			panelHero(item, s)
			panelSubLine(item, s, info, sys, record)
			panelDiagnosis(item, d, record)
			panelDetailRows(item, s, info, record)
			panelChartTone(item, s)
			panelChartMax(item, d.CPUHist)
			if s != nil {
				panelChartPoint(item, *s)
			}
		}
		if s != nil && s.Machine != nil {
			panelFansText(*s.Machine, record)
		}
	}
	visit(nil, nil, nil, panelDiagInput{})
	visit(nil, panelTestInfo(), sys, panelDiagInput{Proc: panelTestProc()})
	visit(&StatusSample{}, &MachineInfo{}, &SystemInfo{}, panelDiagInput{Proc: &ProcessSample{}})
	s := panelTestSample()
	s.Machine, s.Battery = nil, nil
	visit(s, nil, sys, panelDiagInput{})
	s = &StatusSample{Machine: newMachineSample(), Battery: &BatteryStatus{}}
	visit(s, &MachineInfo{}, nil, panelDiagInput{Proc: panelTestProc()})
	visit(panelTestSample(), panelTestInfo(), sys, panelDiagInput{Proc: panelTestProc()})
	visit(panelTestSample(), panelTestInfo(), sys, panelDiagInput{
		Proc: &ProcessSample{Rows: []ProcessRow{panelTestProc().Rows[2]}},
	})
	visit(panelTestSample(), nil, nil, panelDiagInput{})
	for _, hist := range [][]float64{nil, {95}, {80, 80, 80}, {30, 40, 20}, {49, 50, 49}} {
		visit(panelTestSample(), panelTestInfo(), sys, panelDiagInput{
			Proc: panelTestProc(), CPUHist: hist, GPUHist: hist,
		})
	}
	for _, pressure := range []uint64{0, 1, 2, 4} {
		for _, swap := range []uint64{0, panelGiB - 1, panelGiB} {
			s = panelTestSample()
			s.Memory.Pressure = pressure
			s.Memory.SwapTotal, s.Memory.SwapUsed = swap, swap
			visit(s, panelTestInfo(), sys, panelDiagInput{Proc: panelTestProc()})
		}
	}
	for _, kind := range []string{"wifi", "ethernet", ""} {
		s = panelTestSample()
		s.Machine.NetKind = kind
		visit(s, panelTestInfo(), sys, panelDiagInput{})
	}
	for _, thermal := range []int{-1, 0, 1, 2, 3, 4} {
		s = panelTestSample()
		s.Machine.Thermal = thermal
		visit(s, panelTestInfo(), sys, panelDiagInput{})
	}
	for _, fans := range []struct {
		count int
		rpm   []int
	}{{-1, nil}, {0, nil}, {2, nil}, {2, []int{0, 0}}, {2, []int{0, 1350}}} {
		s = panelTestSample()
		s.Machine.FanCount, s.Machine.Fans = fans.count, fans.rpm
		visit(s, panelTestInfo(), sys, panelDiagInput{})
	}
	for _, state := range []BatteryStatus{
		{Full: true, Charging: true, Plugged: true},
		{Charging: true, Plugged: true}, {Plugged: true}, {},
	} {
		for _, condition := range []string{"", "Good", " normal ", "Service Recommended"} {
			s = panelTestSample()
			b := state
			b.Condition = condition
			s.Battery = &b
			visit(s, panelTestInfo(), sys, panelDiagInput{})
		}
	}
	for _, warning := range []int{0, 1, 2, 4, 8, 16, 32, 33} {
		info := panelTestInfo()
		info.SSD.Warning = warning
		s = panelTestSample()
		s.Machine.DiskTemp = 0 // Also visit SMART's temperature fallback.
		visit(s, info, sys, panelDiagInput{})
	}
	info := panelTestInfo()
	info.SSD.SMART = false
	visit(panelTestSample(), info, sys, panelDiagInput{})
	info = panelTestInfo()
	info.SSD.TempC, info.SSD.Written = 0, 0
	info.Displays = []DisplayInfo{{Builtin: true}, {Width: 1920, Height: 1080}, {Name: "External"}}
	s = panelTestSample()
	s.Machine = &MachineSample{}
	visit(s, info, sys, panelDiagInput{})
	for _, free := range []int64{-1, 0, 12 << 30, 20 << 30} {
		for _, window := range []string{"day", "week"} {
			for _, atLeast := range []bool{false, true} {
				s = panelTestSample()
				s.Disk.Free = free
				g := panelTestGrowth(true)
				g.Alerts[0].Window, g.Alerts[0].AtLeast = window, atLeast
				visit(s, panelTestInfo(), sys, panelDiagInput{Growth: g})
			}
		}
	}
	for _, g := range []*GrowthState{
		nil, panelTestGrowth(false), {Config: GrowthConfig{Enabled: true}},
		{Supported: true, Config: GrowthConfig{Enabled: true}},
	} {
		visit(panelTestSample(), panelTestInfo(), sys, panelDiagInput{Growth: g})
	}
	g := panelTestGrowth(true)
	g.Config.Ignored = []string{g.Alerts[0].Key}
	visit(panelTestSample(), panelTestInfo(), sys, panelDiagInput{Growth: g})
	panelDiagnosis("unknown", panelDiagInput{}, record)
	if len(keys) == 0 {
		t.Fatal("panel functions recorded no translation keys")
	}
	for _, lang := range []string{"zh", "en"} {
		for key := range keys {
			value, ok := nativeTexts()[lang][key]
			if !ok || value == "" {
				t.Errorf("native %s is missing a nonempty panel key %q", lang, key)
			}
		}
	}
	t.Logf("checked %d panel translation keys in zh and en", len(keys))
}

func TestPanelHero(t *testing.T) {
	for _, tc := range []struct{ item, value, unit string }{
		{"cpu", "51", "%"}, {"mem", "86", "%"}, {"gpu", "14", "%"},
		{"net", "156", "KB/s"}, {"disk", "26.7", "GB"},
		{"temp", "52", "°C"}, {"battery", "86", "%"},
	} {
		v, u := panelHero(tc.item, panelTestSample())
		panelEqual(t, []string{v, u}, []string{tc.value, tc.unit})
		v, u = panelHero(tc.item, nil)
		panelEqual(t, []string{v, u}, []string{"—", ""})
	}
	for _, item := range []string{"gpu", "temp", "battery", "mem", "net"} {
		s := panelTestSample()
		s.Machine.GPU = -1
		s.Machine.NetDown = -1
		if item == "temp" {
			s.Machine = nil
		}
		s.Battery = nil
		s.Memory.Total = 0
		v, u := panelHero(item, s)
		panelEqual(t, []string{v, u}, []string{"—", ""})
	}
}

func TestPanelSubLine(t *testing.T) {
	s, info := panelTestSample(), panelTestInfo()
	sys := &SystemInfo{Chip: "Apple M2 Pro", Cores: 12}
	for item, want := range map[string]string{
		"cpu":  `Apple M2 Pro · status.spec.cpuSplit{"e":"4","n":"12","p":"8"} · status.cpu.load{"l1":"3.3","l15":"2.4","l5":"2.5"}`,
		"mem":  `menubar.panel.memSub{"total":"32 GB","used":"27.5 GB"}`,
		"gpu":  `status.spec.gpu{"n":"19"}`,
		"net":  "status.net.wifi · ↑ 55 KB/s",
		"disk": `status.disk.avail · status.disk.totalOnly{"total":"494 GB"}`,
		"temp": "status.health.chipTemp", "battery": "",
	} {
		panelEqual(t, panelSubLine(item, s, info, sys, panelTestText), want)
		panelEqual(t, panelSubLine(item, nil, nil, nil, panelTestText), "")
	}
	panelEqual(t, panelSubLine("cpu", nil, nil, sys, panelTestText),
		`Apple M2 Pro · status.spec.cpu{"n":"12"}`)
}

func TestPanelDiagnosisCPU(t *testing.T) {
	for _, tc := range []struct {
		hist      []float64
		tone, key string
	}{
		{[]float64{85, 90, 81}, "warn", "process.diag.cpu"},
		{[]float64{85, 70, 90}, "ok", "menubar.diag.cpuTop"},
		{[]float64{30, 40, 20}, "ok", "menubar.diag.cpuIdle"},
		{[]float64{95}, "ok", "menubar.diag.cpuTop"},
		{[]float64{80, 80, 80}, "warn", "process.diag.cpu"},
		{[]float64{49, 50, 49}, "ok", "menubar.diag.cpuTop"},
		{[]float64{1, 80, 81, 82}, "warn", "process.diag.cpu"},
	} {
		d := panelTestInput()
		d.Proc = panelTestProc()
		d.CPUHist = tc.hist
		want := &panelDiag{tc.tone, tc.key + `{"name":"Google Chrome","pct":"54%"}`}
		panelEqual(t, panelDiagnosis("cpu", d, panelTestText), want)
	}
	panelEqual(t, panelDiagnosis("cpu", panelTestInput(), panelTestText), (*panelDiag)(nil))
	// Literal output of the throwaway Bun script, with an estimated top row.
	d := panelTestInput()
	d.Proc = &ProcessSample{Rows: []ProcessRow{panelTestProc().Rows[2]}}
	panelEqual(t, panelDiagnosis("cpu", d, panelTestText),
		&panelDiag{"ok", `menubar.diag.cpuTop{"name":"WindowServer","pct":"≈ 40%"}`})
}

func TestPanelDiagnosisMemory(t *testing.T) {
	for _, tc := range []struct {
		pressure  uint64
		tone, key string
	}{
		{1, "ok", "menubar.diag.memOk"}, {2, "warn", "process.diag.mem"},
		{4, "crit", "process.diag.mem"},
	} {
		d := panelTestInput()
		d.Proc = panelTestProc()
		d.S.Memory.Pressure = tc.pressure
		want := &panelDiag{tc.tone, tc.key + `{"name":"Google Chrome","size":"31.5 GB"}`}
		panelEqual(t, panelDiagnosis("mem", d, panelTestText), want)
		for _, swap := range []uint64{(1 << 30) - 1, 1 << 30, 37 << 30} {
			d.S.Memory.SwapTotal = swap
			text := want.Text
			if swap >= 1<<30 {
				size := "1 GB"
				if swap == 37<<30 {
					size = "37 GB"
				}
				text += " " + panelTestText("status.alert.swapLine", map[string]string{"size": size})
			}
			panelEqual(t, panelDiagnosis("mem", d, panelTestText), &panelDiag{tc.tone, text})
		}
	}
	// Literal output of the throwaway Bun script, with an estimated top row.
	d := panelTestInput()
	d.Proc = &ProcessSample{Rows: []ProcessRow{panelTestProc().Rows[2]}}
	panelEqual(t, panelDiagnosis("mem", d, panelTestText),
		&panelDiag{"ok", `menubar.diag.memOk{"name":"WindowServer","size":"≈ 133 MB"}`})
}

func TestPanelDiagnosisMachine(t *testing.T) {
	for _, tc := range []struct {
		hist      []float64
		tone, key string
	}{
		{[]float64{90, 95, 88}, "warn", "menubar.diag.gpuBusy"},
		{[]float64{10}, "", ""},
		{[]float64{10, 20, 30}, "ok", "menubar.diag.gpuIdle"},
		{[]float64{60, 70, 90}, "ok", "menubar.diag.gpuSome"},
	} {
		d := panelTestInput()
		d.GPUHist = tc.hist
		var want *panelDiag
		if tc.key != "" {
			want = &panelDiag{tc.tone, tc.key}
		}
		panelEqual(t, panelDiagnosis("gpu", d, panelTestText), want)
	}
	d := panelTestInput()
	d.S.Machine.GPU = -1
	panelEqual(t, panelDiagnosis("gpu", d, panelTestText), (*panelDiag)(nil))
	for kind, key := range map[string]string{"wifi": "netWifi", "ethernet": "netEthernet", "": "netNone"} {
		d.S.Machine.NetKind = kind
		panelEqual(t, panelDiagnosis("net", d, panelTestText), &panelDiag{"ok", "menubar.diag." + key})
	}
	for thermal, tone := range map[int]string{0: "ok", 1: "warn", 2: "crit", 3: "crit", 4: "crit", -1: ""} {
		d.S.Machine.Thermal = thermal
		var want *panelDiag
		if thermal >= 0 {
			want = &panelDiag{tone, fmt.Sprintf("menubar.diag.thermal%d", min(thermal, 3))}
		}
		panelEqual(t, panelDiagnosis("temp", d, panelTestText), want)
	}
	d = panelTestInput()
	panelEqual(t, panelDiagnosis("battery", d, panelTestText),
		&panelDiag{"ok", `status.battery.charging{"pct":"86%"}`})
	d.S.Battery.Condition = "Service Recommended"
	d.S.Battery.Charging, d.S.Battery.Plugged = false, false
	panelEqual(t, panelDiagnosis("battery", d, panelTestText),
		&panelDiag{"warn", "status.battery.onBattery · status.battery.service"})
	for _, condition := range []string{"", "Good", " normal "} {
		d.S.Battery.Condition = condition
		panelEqual(t, panelDiagnosis("battery", d, panelTestText), &panelDiag{"ok", "status.battery.onBattery"})
	}
	d.S.Battery.Plugged = true
	panelEqual(t, panelDiagnosis("battery", d, panelTestText), &panelDiag{"ok", "status.battery.plugged"})
	d.S.Battery.Full = true
	panelEqual(t, panelDiagnosis("battery", d, panelTestText), &panelDiag{"ok", "status.battery.full"})
}

func TestPanelDiagnosisDisk(t *testing.T) {
	d := panelTestInput()
	panelEqual(t, panelDiagnosis("disk", d, panelTestText),
		&panelDiag{"ok", `menubar.diag.diskOk{"size":"26.7 GB"}`})
	d.S.Disk = DiskStat{Total: 494 << 30, Used: 482 << 30, Free: 12 << 30}
	panelEqual(t, panelDiagnosis("disk", d, panelTestText).Tone, "warn")
	d = panelTestInput()
	d.Growth = panelTestGrowth(true)
	alert := panelTestText("status.alert.growthDay", map[string]string{
		"app": "ToDesk", "days": "1", "size": "5.6 GB"})
	panelEqual(t, panelDiagnosis("disk", d, panelTestText), &panelDiag{"warn",
		`menubar.diag.diskOk{"size":"26.7 GB"} ` + panelTestText("menubar.diag.growth", map[string]string{"alert": alert})})
	// Week and at-least variables come from the throwaway Bun script.
	d.Growth.Alerts = []GrowthAlert{{Name: "Xcode", Window: "week", Days: 5, Bytes: 12 << 30, AtLeast: true}}
	alert = panelTestText("status.alert.growthWeek", map[string]string{
		"app": "Xcode", "days": "5", "size": `growth.atLeast{"size":"12.0 GB"}`})
	panelEqual(t, panelDiagnosis("disk", d, panelTestText), &panelDiag{"warn",
		`menubar.diag.diskOk{"size":"26.7 GB"} ` + panelTestText("menubar.diag.growth", map[string]string{"alert": alert})})
	d.Growth.Config.Enabled = false
	panelEqual(t, panelDiagnosis("disk", d, panelTestText), &panelDiag{"ok", `menubar.diag.diskOk{"size":"26.7 GB"}`})
	for _, free := range []int64{0, -1, 20 << 30} {
		d.S.Disk.Free = free
		v, u := panelHero("disk", d.S)
		wantV, wantU, tone, key, size := "0", "MB", "warn", "status.alert.diskLow", "0 MB"
		if free > 0 {
			wantV, wantU, tone, key, size = "20", "GB", "ok", "menubar.diag.diskOk", "20 GB"
		}
		panelEqual(t, []string{v, u}, []string{wantV, wantU})
		panelEqual(t, panelDiagnosis("disk", d, panelTestText), &panelDiag{tone, panelTestText(key, map[string]string{"size": size})})
	}
	d.S.Disk = DiskStat{}
	v, u := panelHero("disk", d.S)
	panelEqual(t, []string{v, u}, []string{"—", ""})
	panelEqual(t, panelDiagnosis("disk", d, panelTestText), (*panelDiag)(nil))
}

func TestPanelChart(t *testing.T) {
	s := panelTestSample()
	for item, want := range map[string]float64{"cpu": 51.2, "mem": 85.9375, "gpu": 14, "net": 156000, "disk": 3e6, "temp": 52.4, "battery": 86} {
		v, ok := panelChartPoint(item, *s)
		panelEqual(t, ok, true)
		panelEqual(t, v, want)
	}
	s.Machine.GPU = -1
	s.Battery = nil
	for _, item := range []string{"gpu", "battery"} {
		_, ok := panelChartPoint(item, *s)
		panelEqual(t, ok, false)
	}
	panelEqual(t, panelChartMax("cpu", []float64{12}), 100.0)
	panelEqual(t, panelChartMax("net", []float64{5000}), 100000.0)
	panelEqual(t, panelChartMax("disk", []float64{3e6, 2e6}), 3e6)
	panelEqual(t, panelChartMax("disk", nil), float64(panelDiskSparkFloor))
	for pressure, tone := range map[uint64]string{1: "accent", 2: "warn", 4: "crit"} {
		s.Memory.Pressure = pressure
		panelEqual(t, panelChartTone("mem", s), tone)
	}
	panelEqual(t, panelChartTone("gpu", s), "gpu")
	panelEqual(t, panelChartTone("disk", s), "net")
	panelEqual(t, panelChartTone("mem", nil), "accent")
}

func TestPanelDetailRows(t *testing.T) {
	s, info := panelTestSample(), panelTestInfo()
	keys := func(item string) []string {
		out := []string{}
		for _, r := range panelDetailRows(item, s, info, panelTestText) {
			out = append(out, r.Key)
		}
		return out
	}
	for item, want := range map[string][]string{
		"cpu": {"perf", "eff", "chip"}, "mem": {"app", "wired", "compressed", "cached"},
		"net": {"down", "up"}, "disk": {"space", "model", "health", "life", "written", "temp", "read", "write"},
		"temp":    {"thermal", "chip", "fans", "power", "ssd", "battery"},
		"battery": {"charge", "health", "cycles", "temp", "adapter"},
	} {
		panelEqual(t, keys(item), want)
	}
	cpu := panelDetailRows("cpu", s, info, panelTestText)
	panelEqual(t, cpu[0], panelDetailRow{Key: "perf", Label: `status.cpu.perfN{"n":"8"}`, Value: "44%", Bar: 44, HasBar: true})
	panelEqual(t, cpu[2].Value, "52 °C · 1350 · 1400 rpm")
	values := []string{}
	for _, r := range panelDetailRows("gpu", s, info, panelTestText) {
		values = append(values, r.Value)
	}
	panelEqual(t, values, []string{`status.cpu.cores{"n":"19"}`, "1.5 GB", "1728×1117 @120Hz", "LS27D80xU 1920×1080 @60Hz"})
	mem := panelDetailRows("mem", s, info, panelTestText)
	for i, dot := range []string{"app", "wired", "compressed"} {
		panelEqual(t, mem[i].Dot, dot)
	}
	s.Memory.SwapUsed, s.Memory.SwapTotal = 2<<30, 3<<30
	mem = panelDetailRows("mem", s, info, panelTestText)
	panelEqual(t, mem[len(mem)-1].Value, `menubar.panel.swapValue{"size":"3 GB","used":"2 GB"}`)
	s.Machine.PerfCPU, s.Machine.EffCPU, s.Machine.ChipTemp = -1, -1, 0
	s.Machine.FanCount, s.Machine.Fans = -1, nil
	panelEqual(t, keys("cpu"), []string{})
	s.Battery.Plugged, s.Battery.MaxCapacity, s.Battery.Cycles, s.Battery.Temperature = false, 0, 0, 0
	panelEqual(t, keys("battery"), []string{"charge"})
	panelEqual(t, panelDetailRows("cpu", nil, info, panelTestText), []panelDetailRow{})
	s = panelTestSample()
	s.Machine.DiskTemp = 0
	disk := panelDetailRows("disk", s, info, panelTestText)
	panelEqual(t, disk[5].Value, "33 °C")
	info.SSD.Warning = 3
	panelEqual(t, panelDetailRows("disk", s, info, panelTestText)[2].Value, "status.ssd.warn.spare")
}

func TestPanelMemShares(t *testing.T) {
	a, w, c, ok := panelMemShares(panelTestSample().Memory)
	panelEqual(t, ok, true)
	for i, pair := range [][2]float64{{a, 20}, {w, 20.625}, {c, 45.3125}} {
		if math.Abs(pair[0]-pair[1]) > 1e-6 {
			t.Errorf("share %d = %v, want %v", i, pair[0], pair[1])
		}
	}
	_, _, _, ok = panelMemShares(MemoryStatus{})
	panelEqual(t, ok, false)
	a, w, c, ok = panelMemShares(MemoryStatus{Total: 100, App: 200, Wired: 0, Compressed: 50})
	panelEqual(t, []float64{a, w, c}, []float64{100, 0, 50})
}

func TestPanelTopRows(t *testing.T) {
	rows := []ProcessRow{}
	for i := 0; i < 7; i++ {
		rows = append(rows, ProcessRow{Key: fmt.Sprintf("cli:p%d", i), Name: fmt.Sprintf("p%d", i), Kind: "cli", CPU: float64(i * 10), Memory: uint64(7-i) << 30})
	}
	names := func(rows []ProcessRow) []string {
		out := []string{}
		for _, r := range rows {
			out = append(out, r.Name)
		}
		return out
	}
	first := ProcessSample{Rows: rows}
	shown := panelTopRows(first, "cpu", nil, false)
	panelEqual(t, names(shown), []string{"p6", "p5", "p4", "p3", "p2"})
	panelEqual(t, names(panelTopRows(first, "mem", nil, false)), []string{"p0", "p1", "p2", "p3", "p4"})
	rows[0].CPU, rows[6].CPU = 99, 1
	next := ProcessSample{Rows: rows}
	panelEqual(t, names(panelTopRows(next, "cpu", shown, true)), names(shown))
	panelEqual(t, panelTopRows(next, "cpu", shown, true)[0].CPU, 1.0)
	panelEqual(t, panelTopRows(next, "cpu", shown, false)[0].Name, "p0")
	panelEqual(t, panelTopRows(next, "cpu", nil, true)[0].Name, "p0")
	panelEqual(t, panelRefreshFrozen(shown, ProcessSample{}), shown)
}

func TestPanelSortRows(t *testing.T) {
	rows := []ProcessRow{{Name: "B", CPU: 10.4, Memory: 1127428915}, {Name: "a", CPU: 10.1, Memory: 1168231104}}
	for _, by := range []string{"cpu", "mem"} {
		panelEqual(t, panelSortRows(rows, by)[0].Name, "a")
		panelEqual(t, rows[0].Name, "B")
	}
	// Equal displayed values use case-insensitive names, then raw strings.
	names := []string{"A", "a", "Google Chrome", "WindowServer", "node", "Cleanzo", "飞书", "😀", "𝄞Music", "é", "é"}
	rows = []ProcessRow{}
	for _, name := range names {
		rows = append(rows, ProcessRow{Name: name})
	}
	got := []string{}
	for _, r := range panelSortRows(rows, "cpu") {
		got = append(got, r.Name)
	}
	panelEqual(t, got, []string{"A", "a", "Cleanzo", "é", "Google Chrome", "node", "WindowServer", "é", "飞书", "𝄞Music", "😀"})
}

func TestPanelPush(t *testing.T) {
	hist := []float64{1, 2, 3}
	panelEqual(t, panelPush(hist, 4, 3), []float64{2, 3, 4})
	panelEqual(t, hist, []float64{1, 2, 3})
	panelEqual(t, panelPush(nil, 4, 30), []float64{4})
	for i := 0; i < 30; i++ {
		hist = panelPush(hist, float64(i), 30)
	}
	panelEqual(t, len(hist), 30)
	panelEqual(t, hist[0], 0.0)
	panelEqual(t, hist[29], 29.0)
}

func TestPanelGrowthTopAndHint(t *testing.T) {
	g := panelTestGrowth(true)
	for _, tc := range []struct {
		key     string
		week    *GrowthDelta
		ignored bool
	}{
		{"a", &GrowthDelta{Bytes: 3 << 30}, false}, {"b", &GrowthDelta{Bytes: 9 << 30}, false},
		{"c", &GrowthDelta{Bytes: 5 << 30}, true}, {"d", &GrowthDelta{Bytes: -(1 << 30)}, false},
		{"e", nil, false}, {"f", &GrowthDelta{Bytes: 1 << 30}, false}, {"g", &GrowthDelta{Bytes: 2 << 30}, false},
		{"h", &GrowthDelta{Bytes: 4 << 30}, false}, {"i", &GrowthDelta{Bytes: 6 << 30}, false},
	} {
		g.Apps = append(g.Apps, GrowthApp{Key: tc.key, Name: tc.key, Week: tc.week, Ignored: tc.ignored})
	}
	keys := []string{}
	for _, a := range panelGrowthTop(g) {
		keys = append(keys, a.Key)
	}
	panelEqual(t, keys, []string{"b", "i", "h", "a", "g"})
	panelEqual(t, panelGrowthHint(g), &g.Alerts[0])
	g.Alerts = append(g.Alerts, GrowthAlert{Key: "ignore", Bytes: 10 << 30}, GrowthAlert{Key: "big", Bytes: 8 << 30, Folder: "/Crashpad"})
	g.Config.Ignored = []string{"ignore"}
	panelEqual(t, panelGrowthHint(g).Key, "big")
	panelEqual(t, g.Alerts[0].Name, "ToDesk")
	g.Config.Enabled = false
	panelEqual(t, panelGrowthTop(g), []GrowthApp{})
	panelEqual(t, panelGrowthHint(g), (*GrowthAlert)(nil))
	g.Config.Enabled = true
	g.Supported = false
	panelEqual(t, panelGrowthTop(g), []GrowthApp{})
	panelEqual(t, panelGrowthHint(g), (*GrowthAlert)(nil))
	panelEqual(t, panelGrowthTop(nil), []GrowthApp{})
	panelEqual(t, panelGrowthHint(nil), (*GrowthAlert)(nil))
	// Equal growth uses case-insensitive keys, then raw strings.
	g = panelTestGrowth(true)
	for _, key := range []string{"B", "a", "A", "b"} {
		g.Apps = append(g.Apps, GrowthApp{Key: key, Week: &GrowthDelta{Bytes: 1 << 30}})
	}
	keys = nil
	for _, app := range panelGrowthTop(g) {
		keys = append(keys, app.Key)
	}
	panelEqual(t, keys, []string{"A", "a", "B", "b"})
}

func TestPanelGrowthHintNumberTie(t *testing.T) {
	g := panelTestGrowth(true)
	g.Alerts = []GrowthAlert{
		{Key: "first", Bytes: 9007199254740992},
		{Key: "second", Bytes: 9007199254740993},
	}
	// Both byte counts reach the web as the same Number; its stable sort
	// chooses "first", as the throwaway Bun script confirms.
	panelEqual(t, panelGrowthHint(g).Key, "first")
}

func TestPanelQuitBook(t *testing.T) {
	empty := panelQuitBook{}
	panelEqual(t, empty.step("unknown"), panelQuitStep("idle"))
	b := empty.with("a", "confirm").with("b", "forceConfirm")
	panelEqual(t, empty.step("a"), panelQuitStep("idle"))
	panelEqual(t, b.frozen(false), true)
	panelEqual(t, b.pending(), false)
	r := b.revertConfirms()
	panelEqual(t, r.steps, map[string]panelQuitStep{"b": "stuck"})
	panelEqual(t, b.step("a"), panelQuitStep("confirm"))
	panelEqual(t, r.revertConfirms(), r)
	panelEqual(t, r.with("a", "quitting").pending(), true)
	panelEqual(t, r.with("a", "forcing").pending(), true)
	panelEqual(t, empty.frozen(false), false)
	panelEqual(t, empty.frozen(true), true)
	app := ProcessRow{Key: "app:a", Name: "A", Quit: true}
	d := empty.with("app:a", "done").sync([]ProcessRow{app})
	panelEqual(t, d.step("app:a"), panelQuitStep("done"))
	before := d
	d = d.sync(nil)
	panelEqual(t, d.gone["app:a"], true)
	panelEqual(t, before.gone["app:a"], false)
	panelEqual(t, d.sync(nil), d)
	d = d.sync([]ProcessRow{app})
	panelEqual(t, d.step("app:a"), panelQuitStep("idle"))
	panelEqual(t, len(d.gone), 0)
	for _, step := range []panelQuitStep{"stuck", "failed"} {
		panelEqual(t, len(empty.with("app:a", step).sync(nil).steps), 0)
	}
	for _, step := range []panelQuitStep{"confirm", "forceConfirm", "quitting", "forcing"} {
		panelEqual(t, empty.with("app:a", step).sync(nil).step("app:a"), step)
	}
}

func TestPanelDisplayText(t *testing.T) {
	for _, tc := range []struct {
		d    DisplayInfo
		want string
	}{
		{DisplayInfo{Name: " LS27D80xU ", Width: 1920, Height: 1080, Hz: 60}, "LS27D80xU 1920×1080 @60Hz"},
		{DisplayInfo{Name: " ", Width: 1920, Height: 1080}, "external 1920×1080"},
		{DisplayInfo{Name: " Name ", Width: 0, Height: 1080}, "Name"},
	} {
		panelEqual(t, panelDisplayText(tc.d, "external"), tc.want)
	}
}

func TestPanelSSDWarnings(t *testing.T) {
	keys := []string{"status.ssd.warn.spare", "status.ssd.warn.temp", "status.ssd.warn.reliability", "status.ssd.warn.readonly", "status.ssd.warn.backup"}
	panelEqual(t, panelSSDWarnings(0), []string{})
	panelEqual(t, panelSSDWarnings(31), keys)
	panelEqual(t, panelSSDWarnings(32), []string{"status.ssd.warn.other"})
	panelEqual(t, panelSSDWarnings(33), keys[:1])
	for i, key := range keys {
		panelEqual(t, panelSSDWarnings(1<<i), []string{key})
	}
}

// panelMem cases are literal outputs of the throwaway Bun parity script.
func TestPanelMemParity(t *testing.T) {
	for _, tc := range []struct {
		n    float64
		want string
	}{
		{0, "—"},
		{1, "0 MB"},
		{-524288, "0 MB"},
		{-1572864, "-1 MB"},
		{524288, "1 MB"},
		{1572864, "2 MB"},
		{1073741823, "1024 MB"},
		{1073741824, "1 GB"},
		{1073741825, "1 GB"},
		{1127428915.2, "1.1 GB"},
		{1234803097.6, "1.1 GB"},
		{1342177280, "1.3 GB"},
		{2738041651.2, "2.5 GB"},
		{2845415833.6, "2.6 GB"},
		{107320495308.8, "100 GB"},
		{107374182400, "100 GB"},
		{1099511627776, "1024 GB"},
		{math.NaN(), "—"},
	} {
		panelEqual(t, panelMem(tc.n), tc.want)
	}
}

// panelSplitMem cases are literal outputs of the throwaway Bun parity script.
func TestPanelSplitMemParity(t *testing.T) {
	for _, tc := range []struct {
		n           float64
		value, unit string
	}{
		{0, "—", ""},
		{1, "0", "MB"},
		{-524288, "0", "MB"},
		{-1572864, "-1", "MB"},
		{524288, "1", "MB"},
		{1572864, "2", "MB"},
		{1073741823, "1024", "MB"},
		{1073741824, "1", "GB"},
		{1073741825, "1", "GB"},
		{1127428915.2, "1.1", "GB"},
		{1234803097.6, "1.1", "GB"},
		{1342177280, "1.3", "GB"},
		{2738041651.2, "2.5", "GB"},
		{2845415833.6, "2.6", "GB"},
		{107320495308.8, "100", "GB"},
		{107374182400, "100", "GB"},
		{1099511627776, "1024", "GB"},
		{math.NaN(), "—", ""},
	} {
		v, u := panelSplitMem(tc.n)
		panelEqual(t, []string{v, u}, []string{tc.value, tc.unit})
	}
}

// panelPct cases are literal outputs of the throwaway Bun parity script.
func TestPanelPctParity(t *testing.T) {
	for _, tc := range []struct {
		n    float64
		want string
	}{
		{-1.5, "0%"},
		{-0.5, "0%"},
		{0, "0%"},
		{0.49999999999999994, "0%"},
		{0.5, "1%"},
		{1.5, "2%"},
		{50.4, "50%"},
		{50.5, "51%"},
		{99.5, "100%"},
		{100, "100%"},
		{153.5, "154%"},
		{math.NaN(), "NaN%"},
		{math.Inf(1), "Infinity%"},
	} {
		panelEqual(t, panelPct(tc.n), tc.want)
	}
}

// panelRate cases are literal outputs of the throwaway Bun parity script.
func TestPanelRateParity(t *testing.T) {
	for _, tc := range []struct {
		n           float64
		value, unit string
	}{
		{-1, "—", ""},
		{0, "0", "KB/s"},
		{1, "0", "KB/s"},
		{499, "0", "KB/s"},
		{500, "1", "KB/s"},
		{1500, "2", "KB/s"},
		{999499, "999", "KB/s"},
		{999500, "1.0", "MB/s"},
		{999501, "1.0", "MB/s"},
		{1050000, "1.1", "MB/s"},
		{1150000, "1.1", "MB/s"},
		{1250000, "1.3", "MB/s"},
		{2550000, "2.5", "MB/s"},
		{2650000, "2.6", "MB/s"},
		{999949999, "999.9", "MB/s"},
		{999950000, "1.0", "GB/s"},
		{999950001, "1.0", "GB/s"},
		{1150000000, "1.1", "GB/s"},
		{1250000000, "1.3", "GB/s"},
		{1000000000000, "1000.0", "GB/s"},
		{math.NaN(), "—", ""},
		{math.Inf(1), "Infinity", "GB/s"},
	} {
		v, u := panelRate(tc.n)
		panelEqual(t, []string{v, u}, []string{tc.value, tc.unit})
	}
}

// panelRateText cases are literal outputs of the throwaway Bun parity script.
func TestPanelRateTextParity(t *testing.T) {
	for _, tc := range []struct {
		n    float64
		want string
	}{
		{-1, "—"},
		{0, "0 KB/s"},
		{1, "0 KB/s"},
		{499, "0 KB/s"},
		{500, "1 KB/s"},
		{1500, "2 KB/s"},
		{999499, "999 KB/s"},
		{999500, "1.0 MB/s"},
		{999501, "1.0 MB/s"},
		{1050000, "1.1 MB/s"},
		{1150000, "1.1 MB/s"},
		{1250000, "1.3 MB/s"},
		{2550000, "2.5 MB/s"},
		{2650000, "2.6 MB/s"},
		{999949999, "999.9 MB/s"},
		{999950000, "1.0 GB/s"},
		{999950001, "1.0 GB/s"},
		{1150000000, "1.1 GB/s"},
		{1250000000, "1.3 GB/s"},
		{1000000000000, "1000.0 GB/s"},
		{math.NaN(), "—"},
		{math.Inf(1), "Infinity GB/s"},
	} {
		panelEqual(t, panelRateText(tc.n), tc.want)
	}
}

// panelTemp cases are literal outputs of the throwaway Bun parity script.
func TestPanelTempParity(t *testing.T) {
	for _, tc := range []struct {
		n    float64
		want string
	}{
		{-1, "—"},
		{0, "—"},
		{0.1, "0 °C"},
		{0.49999999999999994, "0 °C"},
		{0.5, "1 °C"},
		{1.5, "2 °C"},
		{52.4, "52 °C"},
		{52.5, "53 °C"},
		{99.5, "100 °C"},
		{math.NaN(), "—"},
	} {
		panelEqual(t, panelTemp(tc.n), tc.want)
	}
}

// panelDec cases are literal outputs of the throwaway Bun parity script.
func TestPanelDecParity(t *testing.T) {
	for _, tc := range []struct {
		n    float64
		want string
	}{
		{-1, "—"},
		{0, "—"},
		{1, "0 GB"},
		{999999999, "1 GB"},
		{1000000000, "1 GB"},
		{1000000001, "1 GB"},
		{1050000000, "1.1 GB"},
		{1150000000, "1.1 GB"},
		{1250000000, "1.3 GB"},
		{2550000000, "2.5 GB"},
		{2650000000, "2.6 GB"},
		{99950000000, "100 GB"},
		{100000000000, "100 GB"},
		{100500000000, "101 GB"},
		{999999999999, "1000 GB"},
		{1000000000000, "1 TB"},
		{1000000000001, "1 TB"},
		{1150000000000, "1.1 TB"},
		{1250000000000, "1.3 TB"},
		{624500000000000, "625 TB"},
		{1000000000000000, "1000 TB"},
		{math.NaN(), "—"},
	} {
		panelEqual(t, panelDec(tc.n), tc.want)
	}
}

// panelNetMax cases are literal outputs of the throwaway Bun parity script.
func TestPanelNetMaxParity(t *testing.T) {
	for _, tc := range []struct {
		hist []float64
		want float64
	}{
		{[]float64{}, 100000},
		{[]float64{0}, 100000},
		{[]float64{99999}, 100000},
		{[]float64{100000}, 100000},
		{[]float64{100001}, 100001},
		{[]float64{1000000, 2000000}, 2000000},
		{[]float64{-1}, 100000},
		{[]float64{math.NaN()}, math.NaN()},
	} {
		got := panelNetMax(tc.hist)
		if !(math.IsNaN(got) && math.IsNaN(tc.want)) {
			panelEqual(t, got, tc.want)
		}
	}
}

// panelIconHue cases are literal outputs of the throwaway Bun parity script.
func TestPanelIconHueParity(t *testing.T) {
	for _, tc := range []struct {
		name string
		want float64
	}{
		{"", 157.24137931034483},              // #0c9d66
		{"A", 217.21925133689842},             // #3b82f6
		{"a", 217.21925133689842},             // #3b82f6
		{"Google Chrome", 217.21925133689842}, // #3b82f6
		{"WindowServer", 37.69230769230769},   // #f59e0b
		{"node", 258.31168831168833},          // #8b5cf6
		{"Cleanzo", 238.73239436619718},       // #6366f1
		{"Mail", 330.3658536585366},           // #ec4899
		{"Xcode", 173.41463414634146},         // #14b8a6
		{"飞书", 157.24137931034483},            // #0c9d66
		{"😀", 37.69230769230769},              // #f59e0b
		{"𝄞Music", 37.69230769230769},         // #f59e0b
		{"é", 217.21925133689842},             // #3b82f6
		{"é", 0},                             // #ef4444
	} {
		if got := panelIconHue(tc.name); math.Abs(got-tc.want) > 1e-10 {
			t.Errorf("%q: hue %v, want %v", tc.name, got, tc.want)
		}
	}
}

// panelEst cases are literal outputs of the throwaway Bun parity script.
func TestPanelEstParity(t *testing.T) {
	for _, tc := range []struct {
		text      string
		estimated bool
		want      string
	}{
		{"—", false, "—"},
		{"—", true, "—"},
		{"", false, ""},
		{"", true, "≈ "},
		{"0%", false, "0%"},
		{"0%", true, "≈ 0%"},
		{"31.5 GB", false, "31.5 GB"},
		{"31.5 GB", true, "≈ 31.5 GB"},
		{"≈ 1 MB", false, "≈ 1 MB"},
		{"≈ 1 MB", true, "≈ ≈ 1 MB"},
	} {
		panelEqual(t, panelEst(tc.text, tc.estimated), tc.want)
	}
}
