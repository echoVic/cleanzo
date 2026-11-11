//go:build darwin

package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func panelViewText(key string, vars map[string]string) string {
	return nativeTextFor("zh", key, vars)
}

func panelViewSample(item string) *menuBarPanel {
	p := newMenuBarPanel(item, menuBarPanelDeps{
		dark: func() bool { return false }, text: panelViewText,
		icon: func(string) (*ui.Bitmap, bool) { return nil, false },
	})
	s := panelTestSample()
	s.Memory.SwapUsed, s.Memory.SwapTotal = 2<<30, 4<<30
	p.info, p.sys = panelTestInfo(), &SystemInfo{Chip: "Apple M2 Pro", Cores: 12}
	p.growth = panelTestGrowth(true)
	for i, name := range []string{"Trae", "Google Chrome", "WindowServer", "kernel_task", "Cleanzo"} {
		p.growth.Apps = append(p.growth.Apps, GrowthApp{
			Key: name, Name: name, AppPath: "/Applications/" + name + ".app",
			Week: &GrowthDelta{Bytes: int64(5-i) << 30, AtLeast: i == 1},
		})
	}
	proc := ProcessSample{CPU: 34, Rows: []ProcessRow{
		{Key: "trae", Name: "Trae", Kind: "app", Path: "/Applications/Trae.app",
			CPU: 148, Memory: 8 << 30, Quit: true},
		{Key: "chrome", Name: "Google Chrome", Kind: "app", Path: "/Applications/Google Chrome.app",
			CPU: 22, Memory: 6 << 30, Quit: true},
		{Key: "window", Name: "WindowServer", Kind: "system", CPU: 13,
			Memory: 3 << 30, Estimated: true},
		{Key: "kernel", Name: "kernel_task", Kind: "system", CPU: 8,
			Memory: 2 << 30, Estimated: true},
		{Key: "cleanzo", Name: "Cleanzo", Kind: "app", CPU: 3, Memory: 1 << 30},
		{Key: "sixth", Name: "Sixth", Kind: "cli", CPU: 1, Memory: 1},
	}}
	for i := range panelSparkPoints {
		s.CPU.Usage = 30 + float64(i%8)*3
		p.acceptStatus(*s)
		p.acceptProcesses(proc)
	}
	return p
}

func panelViewHas(t *testing.T, tt *ui.Tester, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if !tt.HasText(text) {
			t.Errorf("missing %q in %q", text, tt.Texts())
		}
	}
}

func panelViewClick(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	if err := tt.Click(text); err != nil {
		t.Fatal(err)
	}
}

func panelViewHover(t *testing.T, tt *ui.Tester, text string) {
	t.Helper()
	r, ok := tt.Find(text)
	if !ok {
		t.Fatalf("cannot hover %q", text)
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
}

func TestPanelViewCPU(t *testing.T) {
	p := panelViewSample("cpu")
	tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("cpu")))
	panelViewHas(t, tt, "CPU", "45", "%", "CPU 不忙，占得最多的是 Trae（148%）。",
		"性能核（8）", "能效核（4）", "芯片温度 · 风扇", "44%", "72%",
		"Trae", "Google Chrome", "WindowServer", "kernel_task", "Cleanzo", "≈ 13%")
	if tt.HasText("Sixth") {
		t.Error("sixth row shows in the top five")
	}
}

func TestPanelViewEmptyAndError(t *testing.T) {
	for _, item := range menuBarItems {
		p := newMenuBarPanel(item, menuBarPanelDeps{dark: func() bool { return false }, text: panelViewText})
		tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight(item)))
		panelViewHas(t, tt, "—")
		if tt.HasText("Trae") || tt.HasText("退出") {
			t.Errorf("%s shows process rows before a sample", item)
		}
		p.failed = true
		tt.Frame()
		panelViewHas(t, tt, panelViewText("menubar.panel.error", nil))
	}
}

func TestPanelViewMemoryPressure(t *testing.T) {
	p := panelViewSample("mem")
	for _, pressure := range []uint64{0, 1, 2, 4} {
		p.status.Memory.Pressure = pressure
		tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("mem")))
		if pressure == 0 || pressure == 1 {
			for _, key := range []string{"status.mem.pressureWarn", "status.mem.pressureCrit"} {
				if slices.Contains(tt.Texts(), panelViewText(key, nil)) {
					t.Errorf("pressure %d shows the %s badge", pressure, key)
				}
			}
		} else {
			key := "status.mem.pressureWarn"
			if pressure == 4 {
				key = "status.mem.pressureCrit"
			}
			panelViewHas(t, tt, panelViewText(key, nil))
		}
		panelViewHas(t, tt, "86", "8 GB")
	}
}

// panelViewDiagnosisHeight measures the whole text without a line limit.
func panelViewDiagnosisHeight(text string) float32 {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().FontSize(12).FixedLineHeight(17.4).Children(func() {
			ui.Text(c, text).Shrink(0).Margin(8, 10, 4).Padding(8, 10).
				FixedLineHeight(17.4)
		})
	}, 320, 1000)
	r, _ := tt.Find(text)
	return r.H
}

func TestPanelViewDiagnosisIsComplete(t *testing.T) {
	for _, lang := range []string{"zh", "en"} {
		for _, item := range []string{"mem", "disk"} {
			for _, pressure := range []uint64{2, 4} {
				p := panelViewSample(item)
				p.deps.text = func(key string, vars map[string]string) string {
					return nativeTextFor(lang, key, vars)
				}
				p.proc.Rows[0].Name = "Google Chrome"
				p.status.Memory.Pressure = pressure
				p.status.Disk.Free = 9 << 30
				d := panelDiagnosis(item, panelDiagInput{
					S: p.status, Proc: p.proc, Growth: p.growth,
				}, p.deps.text)
				tt := ui.NewTester(p.view, 320, 1000)
				r, ok := tt.Find(d.Text)
				want := panelViewDiagnosisHeight(d.Text)
				if !ok || r.H < want-0.1 {
					t.Errorf("%s %s pressure %d diagnosis height %.1f, needs %.1f: %s",
						item, lang, pressure, r.H, want, d.Text)
				}
			}
		}
	}
}

func TestPanelViewBodyScrollKeepsHeaderAndFooter(t *testing.T) {
	for _, item := range menuBarItems {
		p := panelViewSample(item)
		tt := ui.NewTester(p.view, 320, 220)
		title := p.deps.text(panelTitle(item), nil)
		header, _ := tt.Find(title)
		footerText := p.deps.text("menubar.panel.every", nil)
		footer, _ := tt.Find(footerText)
		if p.scroll.MaxY <= 0 {
			t.Fatalf("%s short panel should need scrolling", item)
		}
		subText := panelSubLine(item, p.status, p.info, p.sys, p.deps.text)
		sub, _ := tt.Find(subText)
		p.scroll.Y = p.scroll.MaxY
		tt.Frame()
		afterHeader, _ := tt.Find(title)
		afterFooter, _ := tt.Find(footerText)
		panelEqual(t, afterHeader, header)
		panelEqual(t, afterFooter, footer)
		if subText != "" {
			afterSub, _ := tt.Find(subText)
			if afterSub.Y >= sub.Y {
				t.Errorf("%s sub-line did not scroll with the body", item)
			}
		}
		last := "Cleanzo"
		if item != "cpu" && item != "mem" && item != "disk" {
			rows := panelDetailRows(item, p.status, p.info, p.deps.text)
			last = rows[len(rows)-1].Value
		}
		r, ok := tt.Find(last)
		if !ok || r.Y < header.Y+header.H || r.Y+r.H > footer.Y {
			t.Errorf("%s last row %q is clipped at scroll end: %v", item, last, r)
		}
	}
}

func TestPanelViewGrowthAndLinks(t *testing.T) {
	p := panelViewSample("disk")
	var calls []string
	var target MenuBarTarget
	p.deps.close = func() { calls = append(calls, "close") }
	p.deps.open = func(v MenuBarTarget) { calls = append(calls, "open"); target = v }
	tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("disk")))
	panelViewHas(t, tt, panelViewText("menubar.panel.topGrowth", nil), "+5.0 GB", "≥ 4.0 GB")
	panelViewClick(t, tt, "Trae")
	panelEqual(t, calls, []string{"close", "open"})
	panelEqual(t, target, MenuBarTarget{Page: "process", Tab: "disk", Key: "Trae"})
	p.growth.Config.Enabled = false
	tt.Frame()
	if tt.HasText("Trae") || tt.HasText(panelViewText("menubar.panel.topGrowth", nil)) {
		t.Error("growth rows show while monitoring is off")
	}
}

func TestPanelViewNavigationAndMenu(t *testing.T) {
	p := panelViewSample("cpu")
	var calls []string
	var target MenuBarTarget
	p.deps.close = func() { calls = append(calls, "close") }
	p.deps.open = func(v MenuBarTarget) { calls = append(calls, "open"); target = v }
	tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("cpu")))
	panelViewClick(t, tt, "Trae")
	panelEqual(t, target, MenuBarTarget{Page: "process", Tab: "cpu", Key: "trae"})
	panelEqual(t, calls, []string{"close", "open"})
	calls = nil
	panelViewClick(t, tt, panelViewText("menubar.panel.openProcess", nil)+" ›")
	panelEqual(t, target, MenuBarTarget{Page: "process", Tab: "cpu"})
	panelEqual(t, calls, []string{"close", "open"})
	for _, chosen := range []bool{false, true} {
		calls = nil
		p.deps.menu = func(x, y int) bool {
			r, _ := tt.Find(panelViewText("menubar.panel.more", nil))
			if x != int(panelRound(float64(r.X))) || y != int(panelRound(float64(r.Y+r.H))) {
				t.Errorf("menu at %d,%d instead of bottom left of %v", x, y, r)
			}
			return chosen
		}
		panelViewClick(t, tt, panelViewText("menubar.panel.more", nil))
		if (len(calls) == 1) != chosen {
			t.Errorf("chosen=%v, close calls=%v", chosen, calls)
		}
	}
}

// panelViewJobs delivers worker results on the tester's thread.
func panelViewJobs(t *testing.T, p *menuBarPanel) func(*ui.Tester) {
	t.Helper()
	jobs := make(chan func(), 10)
	p.update = func(fn func()) { jobs <- fn }
	return func(tt *ui.Tester) {
		t.Helper()
		select {
		case fn := <-jobs:
			fn()
			tt.Frame()
		case <-time.After(5 * time.Second):
			t.Fatal("quit result did not reach the view")
		}
	}
}

func TestPanelViewQuitAndForce(t *testing.T) {
	p := panelViewSample("cpu")
	p.proc.Rows, p.shown = p.proc.Rows[:1], nil
	finish := panelViewJobs(t, p)
	var holds []bool
	p.deps.hold = func(on bool) { holds = append(holds, on) }
	var revert func()
	var delay time.Duration
	p.deps.after = func(d time.Duration, fn func()) { delay, revert = d, fn }
	quitCalls, forceCalls := make(chan string, 2), make(chan string, 2)
	quitResult, forceResult := make(chan bool, 2), make(chan bool, 2)
	p.deps.quit = func(path string) (bool, error) { quitCalls <- path; return <-quitResult, nil }
	p.deps.forceQuit = func(path string) (bool, error) { forceCalls <- path; return <-forceResult, nil }
	tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("cpu")))
	panelViewHover(t, tt, "Trae")
	panelViewClick(t, tt, "退出")
	if _, ok := tt.Find("确认退出"); !ok {
		t.Fatal("the quit button's text is not exactly 确认退出")
	}
	panelEqual(t, delay, 3*time.Second)
	// The injected scheduler is independent of the Tester's clock.
	revert()
	tt.Frame()
	panelEqual(t, p.book.step("trae"), panelQuitStep("idle"))
	if _, ok := tt.Find("退出"); !ok {
		t.Error("the quit button's text is not exactly 退出")
	}
	if slices.Contains(tt.Texts(), "确认退出") {
		t.Error("确认退出 remains after the three-second callback")
	}
	panelViewClick(t, tt, "退出")
	panelViewClick(t, tt, "确认退出")
	panelEqual(t, holds, []bool{true})
	panelViewHas(t, tt, panelViewText("status.apps.quitting", nil))
	quitResult <- false
	finish(tt)
	panelEqual(t, <-quitCalls, "/Applications/Trae.app")
	if len(quitCalls) != 0 {
		t.Fatal("quit called more than once")
	}
	panelEqual(t, holds, []bool{true, false})
	panelViewHas(t, tt, "还没退出", "强制退出")
	panelViewClick(t, tt, "强制退出")
	panelViewHas(t, tt, "确认强制退出")
	panelViewClick(t, tt, "确认强制退出")
	panelEqual(t, holds, []bool{true, false, true})
	forceResult <- true
	finish(tt)
	panelEqual(t, <-forceCalls, "/Applications/Trae.app")
	if len(forceCalls) != 0 {
		t.Fatal("force quit called more than once")
	}
	panelViewHas(t, tt, "已退出")
	panelEqual(t, holds, []bool{true, false, true, false})
}

func TestPanelViewQuitIgnoresOldTimer(t *testing.T) {
	p := panelViewSample("cpu")
	var timers []func()
	p.deps.after = func(delay time.Duration, fn func()) {
		panelEqual(t, delay, 3*time.Second)
		timers = append(timers, fn)
	}
	tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("cpu")))
	panelViewHover(t, tt, "Trae")
	panelViewClick(t, tt, "退出")
	panelEqual(t, p.book.step("trae"), panelQuitStep("confirm"))
	panelViewHover(t, tt, "Google Chrome")
	// Trae now says 确认退出, so the exact 退出 target is Chrome's.
	panelViewClick(t, tt, "退出")
	panelEqual(t, p.book.step("chrome"), panelQuitStep("confirm"))
	if len(timers) != 2 {
		t.Fatalf("got %d confirmation timers, want two", len(timers))
	}
	timers[0]()
	tt.Frame()
	panelEqual(t, p.book.step("chrome"), panelQuitStep("confirm"))
	if !slices.Contains(tt.Texts(), "确认退出") {
		t.Error("the old timer reverted the newer confirmation")
	}
	timers[1]()
	tt.Frame()
	panelEqual(t, p.book.step("trae"), panelQuitStep("idle"))
	panelEqual(t, p.book.step("chrome"), panelQuitStep("idle"))
	if slices.Contains(tt.Texts(), "确认退出") {
		t.Error("the current timer did not revert the confirmations")
	}
	if _, ok := tt.Find("退出"); !ok {
		t.Error("no button has the exact text 退出 after the current timer")
	}
}

func TestPanelViewQuitErrors(t *testing.T) {
	for _, force := range []bool{false, true} {
		for _, code := range []string{"not-running", "cannot-quit", "other"} {
			p := panelViewSample("cpu")
			finish := panelViewJobs(t, p)
			quit := func(string) (bool, error) {
				if code == "other" {
					return false, errors.New("quit failed")
				}
				return false, codeError{Code: code}
			}
			p.deps.quit, p.deps.forceQuit = quit, quit
			p.deps.hold = func(bool) {}
			p.deps.after = func(time.Duration, func()) {}
			tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("cpu")))
			panelViewHover(t, tt, "Trae")
			if force {
				p.book = p.book.with("trae", "stuck")
				tt.Frame()
				panelViewClick(t, tt, "强制退出")
				panelViewClick(t, tt, "确认强制退出")
			} else {
				panelViewClick(t, tt, "退出")
				panelViewClick(t, tt, "确认退出")
			}
			finish(tt)
			want := "stuck"
			if code == "not-running" {
				want = "done"
			} else if code == "other" && !force {
				want = "failed"
			}
			panelEqual(t, p.book.step("trae"), panelQuitStep(want))
		}
	}
}

// panelViewHasInk checks actual paint, since transparent buttons keep text
// labels for keyboard input and cannot be tested with HasText alone.
func panelViewHasInk(tt *ui.Tester, r ui.Rect, background color.RGBA) bool {
	for y := int(r.Y) + 1; y < int(r.Y+r.H)-1; y++ {
		for x := int(r.X) + 1; x < int(r.X+r.W)-1; x++ {
			if tt.Image().RGBAAt(x, y) != background {
				return true
			}
		}
	}
	return false
}

func TestPanelViewFailedRetryShowsOnHover(t *testing.T) {
	for _, dark := range []bool{false, true} {
		p := panelViewSample("cpu")
		p.deps.dark = func() bool { return dark }
		p.book = p.book.with("trae", "failed")
		tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("cpu")))
		for _, hover := range []bool{false, true, false} {
			if hover {
				panelViewHover(t, tt, "Trae")
			} else {
				tt.Move(-10, -10)
			}
			button, ok := tt.Find("退出")
			if !ok {
				t.Fatal("the retry button is missing from keyboard navigation")
			}
			background := tt.Image().RGBAAt(2, int(button.Y+button.H/2))
			if shown := panelViewHasInk(tt, button, background); shown != hover {
				t.Errorf("dark=%v hover=%v retry button painted=%v", dark, hover, shown)
			}
			state, ok := tt.Find("退出失败")
			if !ok || !panelViewHasInk(tt, state, background) {
				t.Errorf("dark=%v hover=%v failure state is not visible", dark, hover)
			}
		}
	}
}

func TestPanelViewFrozenAndReopened(t *testing.T) {
	p := panelViewSample("cpu")
	tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("cpu")))
	panelViewHover(t, tt, "Trae")
	before := slices.Clone(p.shown)
	next := *p.proc
	next.Rows = slices.Clone(next.Rows)
	next.Rows[0].CPU, next.Rows[1].CPU = 0, 300
	p.acceptProcesses(next)
	tt.Frame()
	panelEqual(t, p.shown[0].Key, before[0].Key)
	panelViewHas(t, tt, "0%")
	panelViewClick(t, tt, "退出")
	tt.Move(-10, -10)
	panelEqual(t, p.book.step("trae"), panelQuitStep("idle"))
	panelEqual(t, p.shown[0].Key, "chrome")
	p.book = p.book.with("chrome", "done")
	next.Rows = slices.DeleteFunc(next.Rows, func(r ProcessRow) bool { return r.Key == "chrome" })
	p.acceptProcesses(next)
	tt.Frame()
	next.Rows = append(next.Rows, before[1])
	p.acceptProcesses(next)
	tt.Frame()
	panelEqual(t, p.book.step("chrome"), panelQuitStep("idle"))
}

func TestPanelViewDarkTheme(t *testing.T) {
	p := panelViewSample("cpu")
	p.deps.dark = func() bool { return true }
	var background ui.Color
	tt := ui.NewTester(func(c *ui.Context) { p.view(c); background = c.Theme().Background }, 320, int(menuBarPanelHeight("cpu")))
	tt.SetDark(true)
	panelEqual(t, background, ui.Hex("#22252a"))
	p.deps.dark = func() bool { return false }
	tt.Frame()
	panelEqual(t, background, ui.Hex("#ffffff"))
}

func init() {
	panelTestViewKeys = func(text panelText) {
		for _, item := range menuBarItems {
			p := panelViewSample(item)
			p.deps.text = text
			tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight(item)))
			if item == "cpu" || item == "mem" {
				panelViewHoverKey(tt, "Trae")
				for _, step := range []panelQuitStep{"idle", "confirm", "quitting", "stuck", "forceConfirm", "forcing", "done", "failed"} {
					p.book = p.book.with("trae", step)
					tt.Frame()
				}
			}
			if item == "mem" {
				for _, pressure := range []uint64{2, 4} {
					p.status.Memory.Pressure = pressure
					tt.Frame()
				}
			}
			p.failed = true
			tt.Frame()
		}
	}
}

func panelViewHoverKey(tt *ui.Tester, text string) {
	if r, ok := tt.Find(text); ok {
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
	}
}

// TestPanelViewPreviews writes full-data cards only when a path is supplied.
func TestPanelViewPreviews(t *testing.T) {
	dir := os.Getenv("CLEANZO_PANEL_PREVIEWS")
	if dir == "" {
		t.Skip("set CLEANZO_PANEL_PREVIEWS to render the height previews")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, item := range menuBarItems {
		for _, lang := range []string{"zh", "en"} {
			for _, dark := range []bool{false, true} {
				var p *menuBarPanel
				var height float32
				var longest string
				for _, sample := range panelViewLongCases(item, lang, dark) {
					_, needed := panelViewContentHeight(sample.p)
					d := panelDiagnosis(item, panelDiagInput{
						S: sample.p.status, Proc: sample.p.proc, Growth: sample.p.growth,
						CPUHist: sample.p.cpuHist, GPUHist: sample.p.gpuHist,
					}, sample.p.deps.text)
					if needed > height+0.1 || needed >= height-0.1 && len(d.Text) > len(longest) {
						p, height, longest = sample.p, needed, d.Text
					}
				}
				mode := "light"
				if dark {
					mode = "dark"
				}
				path := filepath.Join(dir, item+"-"+lang+"-"+mode+".png")
				f, err := os.Create(path)
				if err != nil {
					t.Fatal(err)
				}
				img := ui.Render(p.view, 320, int(menuBarPanelHeight(item)), 2)
				err = png.Encode(f, img)
				closeErr := f.Close()
				if err != nil {
					t.Fatal(err)
				}
				if closeErr != nil {
					t.Fatal(closeErr)
				}
				t.Logf("%s %s %s: content %.1f, 320 x %.0f points: %s",
					item, lang, mode, height, menuBarPanelHeight(item), path)
			}
		}
	}
}

// panelViewLayoutCase names a real state whose full content is measured.
type panelViewLayoutCase struct {
	name string
	p    *menuBarPanel
}

// panelViewLongCases covers the diagnosis branches with all details and
// five list rows. Long app names and swap/growth warnings exercise wrapping.
func panelViewLongCases(item, lang string, dark bool) []panelViewLayoutCase {
	var cases []panelViewLayoutCase
	add := func(name string, edit func(*menuBarPanel)) {
		p := panelViewSample(item)
		p.deps.dark = func() bool { return dark }
		p.deps.text = func(key string, vars map[string]string) string {
			return nativeTextFor(lang, key, vars)
		}
		p.proc.Rows[0].Name, p.proc.Rows[1].Name = "Google Chrome", "Trae"
		p.growth.Apps[0].Name, p.growth.Apps[1].Name = "Google Chrome", "Trae"
		p.shown = nil
		edit(p)
		cases = append(cases, panelViewLayoutCase{name, p})
	}
	switch item {
	case "cpu":
		for _, name := range []string{"Google Chrome", "Microsoft Teams"} {
			for _, usage := range []float64{30, 60, 90} {
				add(fmt.Sprintf("%s-%.0f", name, usage), func(p *menuBarPanel) {
					p.proc.Rows[0].Name = name
					p.proc.CPU, p.status.CPU.Usage = usage, usage
					p.cpuHist = []float64{usage, usage, usage}
				})
			}
		}
	case "mem":
		for _, pressure := range []uint64{0, 1, 2, 4} {
			add(fmt.Sprintf("pressure-%d-swap", pressure), func(p *menuBarPanel) {
				p.status.Memory.Pressure = pressure
			})
		}
	case "gpu":
		for _, usage := range []float64{14, 60, 90} {
			add(fmt.Sprintf("gpu-%.0f", usage), func(p *menuBarPanel) {
				p.status.Machine.GPU = usage
				p.gpuHist = []float64{usage, usage, usage}
			})
		}
	case "net":
		for _, kind := range []string{"wifi", "ethernet", ""} {
			add("net-"+kind, func(p *menuBarPanel) { p.status.Machine.NetKind = kind })
		}
	case "disk":
		for _, low := range []bool{false, true} {
			for _, week := range []bool{false, true} {
				add(fmt.Sprintf("low-%v-week-%v", low, week), func(p *menuBarPanel) {
					if low {
						p.status.Disk.Free = 9 << 30
						p.status.Disk.Used = p.status.Disk.Total - p.status.Disk.Free
					}
					if week {
						a := &p.growth.Alerts[0]
						a.Name, a.Window, a.Days, a.AtLeast = "Google Chrome", "week", 7, true
					}
				})
			}
		}
	case "temp":
		for _, thermal := range []int{0, 1, 2, 3} {
			add(fmt.Sprintf("thermal-%d", thermal), func(p *menuBarPanel) {
				p.status.Machine.Thermal = thermal
			})
		}
	case "battery":
		for _, state := range []string{"charging", "full", "plugged", "battery"} {
			for _, condition := range []string{"Normal", "Service Recommended"} {
				add(state+"-"+condition, func(p *menuBarPanel) {
					b := p.status.Battery
					b.Full, b.Charging = state == "full", state == "charging"
					b.Plugged, b.Condition = state != "battery", condition
				})
			}
		}
	}
	return cases
}

// panelViewContentHeight measures the scroll overflow in a short card;
// adding it to that card's height includes the fixed header and footer.
func panelViewContentHeight(p *menuBarPanel) (*ui.Tester, float32) {
	const height = 220
	tt := ui.NewTester(p.view, 320, height)
	return tt, height + p.scroll.MaxY
}

func TestPanelViewLayout(t *testing.T) {
	for _, item := range menuBarItems {
		for _, lang := range []string{"zh", "en"} {
			for _, dark := range []bool{false, true} {
				var largest float32
				var longest string
				for _, sample := range panelViewLongCases(item, lang, dark) {
					p := sample.p
					tt, height := panelViewContentHeight(p)
					if height > largest {
						largest, longest = height, sample.name
					}
					if height > menuBarPanelHeight(item) {
						t.Errorf("%s %s dark=%v %s needs %.1f points, table %.0f",
							item, lang, dark, sample.name, height, menuBarPanelHeight(item))
					}
					tt.SetSize(320, int(menuBarPanelHeight(item)))
					if p.scroll.MaxY > 0.1 {
						t.Errorf("%s %s dark=%v %s still scrolls %.1f points",
							item, lang, dark, sample.name, p.scroll.MaxY)
					}
				}
				t.Logf("%s %s dark=%v maximum %.1f points (%s), table %.0f",
					item, lang, dark, largest, longest, menuBarPanelHeight(item))
			}
		}
	}
}

func TestPanelViewIconColors(t *testing.T) {
	for i, hex := range []string{"#3b82f6", "#8b5cf6", "#f59e0b", "#ef4444", "#14b8a6", "#6366f1", "#ec4899", "#0c9d66"} {
		panelEqual(t, menuBarPanelIconColor(string(rune('a'+i))), ui.Hex(hex))
	}
}

func TestPanelViewPartialChart(t *testing.T) {
	p := newMenuBarPanel("cpu", menuBarPanelDeps{
		dark: func() bool { return false }, text: panelViewText,
	})
	p.samples, p.hist = 3, []float64{50, 50}
	tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("cpu")))
	// Two readings start on the left of the 30-slot plot, as SparkSvg.
	r, g, b, _ := tt.Image().At(18, 75).RGBA()
	if g <= r || g <= b {
		t.Errorf("partial history has no green area on the left: %d,%d,%d", r, g, b)
	}
}

func TestPanelViewChartPeakHasHeadroom(t *testing.T) {
	for _, item := range []string{"cpu", "disk"} {
		for _, dark := range []bool{false, true} {
			p := newMenuBarPanel(item, menuBarPanelDeps{dark: func() bool { return dark }})
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(menuBarPanelTheme(c.Theme(), dark))
				ui.Column(c).Fill().Background(c.Theme().Background).Children(func() {
					p.spark(c)
				})
			}, 320, 52)
			// spark has a 6-point top margin and a 40-point chart. Compare
			// with its empty surface before painting a full-height plateau.
			background := tt.Image().RGBAAt(160, 7)
			peak := 100.0
			if item == "disk" {
				peak = 2e6
			}
			for range panelSparkPoints {
				p.hist = append(p.hist, peak)
			}
			tt.Frame()
			for y := 6; y < 8; y++ {
				if got := tt.Image().RGBAAt(160, y); got != background {
					t.Errorf("%s dark=%v peak paints in the top %.0f points: %v",
						item, dark, float32(y-6), got)
				}
			}
			if tt.Image().RGBAAt(160, 9) == background {
				t.Errorf("%s dark=%v peak is missing about 3 points below the top", item, dark)
			}
		}
	}
}

func TestPanelViewCLIFallback(t *testing.T) {
	p := panelViewSample("cpu")
	p.proc.Rows = []ProcessRow{{Key: "node", Name: "node", Kind: "cli", CPU: 30}}
	tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("cpu")))
	r, ok := tt.Find("node")
	if !ok {
		t.Fatal("CLI row not shown")
	}
	red, green, blue, _ := tt.Image().At(16, int(r.Y+r.H/2)).RGBA()
	if max(red, green, blue)-min(red, green, blue) > 5*257 {
		t.Errorf("CLI fallback should have the subtle system background: %d,%d,%d", red, green, blue)
	}
	if tt.HasText(panelViewText("process.system", nil)) {
		t.Error("CLI row should not carry the system tag")
	}
}

func TestPanelViewGrowthDropsOldResult(t *testing.T) {
	p := panelViewSample("disk")
	p.growthGeneration = 2
	p.acceptGrowth(2, *panelTestGrowth(false))
	tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("disk")))
	p.acceptGrowth(1, *panelViewSample("disk").growth)
	tt.Frame()
	if tt.HasText("Trae") || p.growth.Config.Enabled {
		t.Error("an older read replaced the latest disabled monitoring state")
	}
}

func TestPanelViewDisplayValueWraps(t *testing.T) {
	p := panelViewSample("gpu")
	tt := ui.NewTester(p.view, 320, int(menuBarPanelHeight("gpu")))
	value := panelDetailRows("gpu", p.status, p.info, p.deps.text)[3].Value
	r, ok := tt.Find(value)
	if !ok || r.H < 30 {
		t.Errorf("external display name and mode should wrap in full: %q at %v", value, r)
	}
}

func TestPanelViewFunctionComments(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "menubar_panel_darwin.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Doc == nil || !strings.HasPrefix(fn.Doc.Text(), fn.Name.Name+" ") {
			t.Errorf("%s: %s needs a doc comment", fset.Position(fn.Pos()), fn.Name.Name)
		}
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			for _, line := range strings.Split(comment.Text, "\n") {
				if len([]rune(line)) > 80 {
					t.Errorf("%s: comment exceeds 80 columns", fset.Position(comment.Pos()))
				}
			}
		}
	}
}
