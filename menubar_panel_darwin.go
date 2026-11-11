//go:build darwin

package main

import (
	"context"
	"errors"
	"maps"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
)

// menuBarPanelDeps is what a panel does outside itself; tests replace it.
type menuBarPanelDeps struct {
	dark      func() bool
	text      panelText
	quit      func(path string) (bool, error)
	forceQuit func(path string) (bool, error)
	open      func(t MenuBarTarget)
	menu      func(x, y int) bool
	icon      func(path string) (*ui.Bitmap, bool)
	close     func()
	hold      func(on bool)
	after     func(d time.Duration, fn func())
}

// menuBarPanel keeps the readings and interactions of one open panel.
// Workers hand their results to update; only the main thread changes it.
type menuBarPanel struct {
	item               string
	deps               menuBarPanelDeps
	update             func(func())
	status             *StatusSample
	hist, gpuHist      []float64
	proc               *ProcessSample
	cpuHist            []float64
	info               *MachineInfo
	sys                *SystemInfo
	growth             *GrowthState
	failed, procFailed bool
	samples            int
	hover              bool
	book               panelQuitBook
	shown              []ProcessRow
	confirmGeneration  uint64
	growthGeneration   uint64
	more               ui.Handle
	scroll             ui.ScrollState
}

// newMenuBarPanel starts one panel with the app's appearance and language.
func newMenuBarPanel(item string, deps menuBarPanelDeps) *menuBarPanel {
	if deps.dark == nil {
		deps.dark = menuBarAppDark
	}
	if deps.text == nil {
		deps.text = nativeText
	}
	return &menuBarPanel{item: item, deps: deps}
}

// acceptStatus keeps the latest reading and bounded chart/diagnosis histories.
func (p *menuBarPanel) acceptStatus(s StatusSample) {
	p.status = &s
	p.samples++
	if v, ok := panelChartPoint(p.item, s); ok {
		p.hist = panelPush(p.hist, v, panelSparkPoints)
	}
	if p.item == "gpu" && s.Machine != nil && s.Machine.GPU >= 0 {
		p.gpuHist = panelPush(p.gpuHist, s.Machine.GPU, panelDiagSamples)
	}
}

// acceptProcesses refreshes process readings, CPU history and quit state.
func (p *menuBarPanel) acceptProcesses(s ProcessSample) {
	p.proc = &s
	p.cpuHist = panelPush(p.cpuHist, s.CPU, panelDiagSamples)
	p.changeBook(p.book.sync(s.Rows))
}

// menuBarAppDark tells whether the app shows dark, as its main window does:
// NSApp's appearance, which follows the system's unless the app sets one.
func menuBarAppDark() bool {
	if !appKitInit() {
		return false
	}
	dark := false
	withPool(func() {
		app := objc.ID(objc.GetClass("NSApplication")).Send(nsSel("sharedApplication"))
		names := objc.ID(objc.GetClass("NSMutableArray")).Send(nsSel("array"))
		names.Send(nsSel("addObject:"), nsString("NSAppearanceNameAqua"))
		names.Send(nsSel("addObject:"), nsString("NSAppearanceNameDarkAqua"))
		match := app.Send(nsSel("effectiveAppearance")).Send(nsSel("bestMatchFromAppearancesWithNames:"), names)
		dark = nsGoString(match) == "NSAppearanceNameDarkAqua"
	})
	return dark
}

// menuBarPanelTheme is base in Cleanzo's colors, those of the main window
// (src/style.css), light or dark as the app is.
func menuBarPanelTheme(base *ui.Theme, dark bool) *ui.Theme {
	t := *base
	t.Dark = dark
	if dark {
		t.Background, t.Surface, t.SurfaceHover = ui.Hex("#22252a"), ui.Hex("#ffffff").Alpha(0.045), ui.Hex("#ffffff").Alpha(0.055)
		t.Text, t.TextMuted, t.Border = ui.Hex("#e9ebef"), ui.Hex("#9AA3AF"), ui.Hex("#ffffff").Alpha(0.085)
		t.Accent, t.Warning, t.Danger = ui.Hex("#30D158"), ui.Hex("#f59e0b"), ui.Hex("#e5484d")
	} else {
		t.Background, t.Surface, t.SurfaceHover = ui.Hex("#ffffff"), ui.Hex("#78808c").Alpha(0.055), ui.Hex("#78808c").Alpha(0.08)
		t.Text, t.TextMuted, t.Border = ui.Hex("#1a1c20"), ui.Hex("#6B7280"), ui.Hex("#14181f").Alpha(0.09)
		t.Accent, t.Warning, t.Danger = ui.Hex("#1FA355"), ui.Hex("#b45309"), ui.Hex("#d92d20")
	}
	return &t
}

// menuBarPanelColors keeps the remaining main-window tokens together.
type menuBarPanelColors struct {
	warnText, warnSoft, dangerSoft, accentSoft ui.Color
	app, wired, compressed                     ui.Color
	skeleton, button                           ui.Color
}

// menuBarPanelPalettes follows the main window's light and dark tokens.
var menuBarPanelPalettes = map[bool]menuBarPanelColors{
	false: {
		ui.Hex("#9A6100"), ui.Hex("#d97706").Alpha(0.12),
		ui.Hex("#d92d20").Alpha(0.09), ui.Hex("#1FA355").Alpha(0.10),
		ui.Hex("#3b82f6"), ui.Hex("#8b5cf6"), ui.Hex("#D97706"),
		ui.Hex("#7f848c").Alpha(0.12), ui.Hex("#ffffff").Alpha(0.9),
	},
	true: {
		ui.Hex("#FFC35A"), ui.Hex("#f59e0b").Alpha(0.16),
		ui.Hex("#e5484d").Alpha(0.14), ui.Hex("#30D158").Alpha(0.14),
		ui.Hex("#60a5fa"), ui.Hex("#a78bfa"), ui.Hex("#fbbf24"),
		ui.Hex("#ffffff").Alpha(0.07), ui.Hex("#ffffff").Alpha(0.06),
	},
}

// menuBarPanelHeight sizes each 320-point card for its longest real content.
// ui.Render previews cover Chinese/English and light/dark; TestPanelViewLayout
// checks the full content fits. Heights round up with about 4 points to spare.
func menuBarPanelHeight(item string) float32 {
	switch item {
	case "cpu":
		return 507
	case "mem":
		return 597
	case "disk":
		return 650
	case "gpu":
		return 355
	case "temp":
		return 377
	case "battery":
		return 348
	default:
		return 263
	}
}

// view draws the fixed header and footer around a scrollable body, so longer
// readings stay reachable without moving the panel's navigation.
func (p *menuBarPanel) view(c *ui.Context) {
	c.SetTheme(menuBarPanelTheme(c.Theme(), p.deps.dark()))
	t := c.Theme()
	c.Root().Background(ui.Transparent)
	ui.Column(c).Fill().Background(t.Background).Radius(12).Clip().
		Padding(12, 0, 0).FontSize(12).FixedLineHeight(17.4).Children(func() {
		p.header(c)
		ui.Scroll(c.Key("body")).Grow(1).MinHeight(0).TrackScroll(&p.scroll).
			Children(func() {
				if sub := panelSubLine(p.item, p.status, p.info, p.sys, p.deps.text); sub != "" {
					ui.Text(c, sub).Margin(2, 14, 0).Shrink(0).FontSize(11).
						FixedLineHeight(16).MaxLines(2).TextColor(t.TextMuted).Tooltip(sub)
				}
				p.diagnosis(c)
				p.spark(c)
				if p.item == "mem" && p.status != nil {
					p.memoryStack(c)
				}
				p.details(c)
				if p.item == "cpu" || p.item == "mem" {
					p.processList(c)
				}
				if p.item == "disk" {
					p.growthList(c)
				}
			})
		p.footer(c)
	})
}

// header shows the title, pressure badge when needed, and the main reading.
func (p *menuBarPanel) header(c *ui.Context) {
	t, colors := c.Theme(), menuBarPanelPalettes[c.Theme().Dark]
	ui.Row(c.Key("header")).Shrink(0).Padding(0, 14).Gap(8).
		AlignItems(ui.Baseline).Children(func() {
		ui.Text(c, p.deps.text(panelTitle(p.item), nil)).FontSize(13).
			FontWeight(600).FixedLineHeight(19).NoWrap()
		if p.item == "mem" && p.status != nil {
			pressure := p.status.Memory.Pressure
			if pressure == 2 || pressure == 4 {
				key, ink, fill := "status.mem.pressureWarn", colors.warnText, colors.warnSoft
				if pressure == 4 {
					key, ink, fill = "status.mem.pressureCrit", t.Danger, colors.dangerSoft
				}
				ui.Text(c, p.deps.text(key, nil)).FontSize(10).FixedLineHeight(14).
					NoWrap().Padding(1, 7).Radius(999).TextColor(ink).Background(fill)
			}
		}
		ui.Spacer(c)
		value, unit := panelHero(p.item, p.status)
		ui.Row(c).AlignItems(ui.Baseline).Gap(2).Children(func() {
			ui.Text(c, value).FontSize(26).FontWeight(650).
				FixedLineHeight(28.6).FontFeatures("tnum").NoWrap()
			if unit != "" {
				ui.Text(c, unit).FontSize(13).FontWeight(400).
					FixedLineHeight(19).TextColor(t.TextMuted).NoWrap()
			}
		})
	})
}

// diagnosis shows the complete reading, an error, or the loading placeholder.
func (p *menuBarPanel) diagnosis(c *ui.Context) {
	t, colors := c.Theme(), menuBarPanelPalettes[c.Theme().Dark]
	d := panelDiagnosis(p.item, panelDiagInput{
		S: p.status, Proc: p.proc, CPUHist: p.cpuHist,
		GPUHist: p.gpuHist, Growth: p.growth,
	}, p.deps.text)
	if p.failed || ((p.item == "cpu" || p.item == "mem") && p.procFailed) {
		d = &panelDiag{Tone: "crit", Text: p.deps.text("menubar.panel.error", nil)}
	}
	if d == nil {
		if p.samples < panelDiagSamples {
			ui.Box(c.Key("diagnosis")).Height(34).Shrink(0).Margin(8, 10, 4).
				Radius(8).Background(colors.skeleton)
		}
		return
	}
	ink, fill := t.Text, colors.accentSoft
	if d.Tone == "warn" {
		ink, fill = colors.warnText, colors.warnSoft
	} else if d.Tone == "crit" {
		ink, fill = t.Danger, colors.dangerSoft
	}
	ui.Text(c.Key("diagnosis"), d.Text).Shrink(0).Margin(8, 10, 4).
		Padding(8, 10).Radius(8).Background(fill).TextColor(ink).
		FixedLineHeight(17.4).Tooltip(d.Text)
}

// spark draws the thirty-slot area chart in the reading's tone.
func (p *menuBarPanel) spark(c *ui.Context) {
	t, colors := c.Theme(), menuBarPanelPalettes[c.Theme().Dark]
	ink, fill := t.Accent, colors.accentSoft
	switch panelChartTone(p.item, p.status) {
	case "gpu":
		ink, fill = colors.wired, colors.wired.Alpha(0.14)
	case "net":
		ink, fill = colors.app, colors.app.Alpha(0.14)
	case "warn":
		ink, fill = t.Warning, t.Warning.Alpha(0.16)
	case "crit":
		ink, fill = t.Danger, t.Danger.Alpha(0.14)
	}
	xs, ys := make([]float32, len(p.hist)), make([]float32, len(p.hist))
	top := panelChartMax(p.item, p.hist)
	for i, v := range p.hist {
		xs[i] = float32(i) / (panelSparkPoints - 1)
		// SparkSvg leaves 2 of 28 units above the peak: about 2.9 points.
		ys[i] = float32(math.Max(0, math.Min(top, v)) / top * 26 / 28)
	}
	ui.Box(c.Key("chart")).Height(40).Shrink(0).Margin(6, 14).
		Radius(6).Clip().Background(t.Surface).Draw(func(paint *ui.Painter, r ui.Rect) {
		if len(xs) < 2 {
			return
		}
		paint.FillPath(chart.Area(r, xs, nil, ys, chart.CurveLinear), fill)
		paint.StrokePath(chart.Line(r, xs, ys, chart.CurveLinear), 1.5, ink)
	})
}

// memoryStack draws the app, wired and compressed shares of physical memory.
func (p *menuBarPanel) memoryStack(c *ui.Context) {
	app, wired, compressed, ok := panelMemShares(p.status.Memory)
	if !ok {
		return
	}
	t, colors := c.Theme(), menuBarPanelPalettes[c.Theme().Dark]
	ui.Box(c.Key("memory-stack")).Height(8).Shrink(0).Margin(2, 14, 4).
		Radius(4).Clip().Background(t.Surface).Children(func() {
		left := 0.0
		for _, seg := range []struct {
			share float64
			color ui.Color
		}{{app, colors.app}, {wired, colors.wired}, {compressed, colors.compressed}} {
			ui.Box(c).Absolute().Top(0).Height(8).LeftPercent(float32(left)).
				WidthPercent(float32(seg.share)).Background(seg.color)
			left += seg.share
		}
	})
}

// details shows the available readings with their dots, bars and values.
func (p *menuBarPanel) details(c *ui.Context) {
	rows := panelDetailRows(p.item, p.status, p.info, p.deps.text)
	if len(rows) == 0 {
		return
	}
	t, colors := c.Theme(), menuBarPanelPalettes[c.Theme().Dark]
	ui.Column(c.Key("details")).Shrink(0).Padding(0, 14, 4).Children(func() {
		for i, r := range rows {
			line := ui.Row(c.Key(r.Key)).Shrink(0).Padding(5, 0).Gap(8)
			if i > 0 {
				line.BorderWidth(1, 0, 0, 0).BorderColor(t.Border)
			}
			line.Children(func() {
				ui.Row(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
					if r.Dot != "" {
						color := colors.app
						if r.Dot == "wired" {
							color = colors.wired
						} else if r.Dot == "compressed" {
							color = colors.compressed
						}
						ui.Box(c).Size(7, 7).Shrink(0).Radius(4).Background(color)
					}
					ui.Text(c, r.Label).MinWidth(0).SingleLine().TextColor(t.TextMuted).Tooltip(r.Label)
				})
				if r.HasBar {
					ui.Box(c).Size(90, 6).Shrink(0).Radius(3).Clip().Background(t.Surface).
						Children(func() {
							ui.Box(c).Height(6).WidthPercent(float32(math.Max(0, math.Min(100, r.Bar)))).
								Radius(3).Background(t.Accent)
						})
				}
				ui.Text(c, r.Value).Shrink(0).MaxWidth(190).MaxLines(2).
					FontWeight(600).FontFeatures("tnum").TextAlign(ui.End).Tooltip(r.Value)
			})
		}
	})
}

// section labels a list and its optional note.
func (p *menuBarPanel) section(c *ui.Context, title, note string) {
	ui.Row(c).Shrink(0).Padding(8, 14, 2).FontSize(11).
		FixedLineHeight(16).TextColor(c.Theme().TextMuted).Children(func() {
		ui.Text(c, p.deps.text(title, nil)).NoWrap()
		ui.Spacer(c)
		if note != "" {
			ui.Text(c, p.deps.text(note, nil)).NoWrap()
		}
	})
}

// processList keeps five rows still while hovered or quitting, and reverts
// confirmations when the pointer leaves the list.
func (p *menuBarPanel) processList(c *ui.Context) {
	t, colors := c.Theme(), menuBarPanelPalettes[c.Theme().Dark]
	list := ui.Column(c.Key("process-list")).Shrink(0).Padding(2, 0, 4)
	hover := list.Hovered()
	if p.hover && !hover {
		p.changeBook(p.book.revertConfirms())
	}
	p.hover = hover
	if p.proc != nil {
		p.shown = panelTopRows(*p.proc, p.item, p.shown, p.book.frozen(p.hover))
	}
	list.Children(func() {
		title, note := "menubar.panel.topMem", ""
		if p.item == "cpu" {
			title, note = "menubar.panel.topCpu", "menubar.panel.perCore"
		}
		p.section(c, title, note)
		if p.proc == nil {
			if p.procFailed {
				ui.Text(c, p.deps.text("menubar.panel.error", nil)).FontSize(11).
					TextColor(t.TextMuted).Margin(2, 14, 0)
			} else {
				for range 3 {
					ui.Box(c).Height(22).Shrink(0).Margin(4, 14).Radius(5).
						Background(colors.skeleton)
				}
			}
			return
		}
		for _, row := range p.shown {
			p.processRow(c, row)
		}
	})
}

// processRow shows an icon, name, quit control and the current reading.
func (p *menuBarPanel) processRow(c *ui.Context, row ProcessRow) {
	t := c.Theme()
	step := p.book.step(row.Key)
	line := ui.Row(c.Key(row.Key)).Height(30).Shrink(0).Padding(0, 14).Gap(8)
	hover := line.Hovered()
	if hover {
		line.Background(t.SurfaceHover)
	}
	if step == "done" {
		line.Opacity(0.45)
	}
	line.Children(func() {
		path := ""
		if row.Kind == "app" {
			path = row.Path
		}
		p.appIcon(c, row.Name, path, row.Kind != "app")
		p.name(c, row.Name, row.Key, row.Kind == "system", p.item)
		if row.Quit || step == "done" {
			p.quitControl(c, row, step, hover)
		}
		value := panelMem(float64(row.Memory))
		if p.item == "cpu" {
			value = panelPct(row.CPU)
		}
		ui.Text(c, panelEst(value, row.Estimated)).Shrink(0).MinWidth(52).
			NoWrap().TextAlign(ui.End).FontWeight(600).FontFeatures("tnum")
	})
}

// appIcon shows the cached app icon or the initial on its fallback color.
func (p *menuBarPanel) appIcon(c *ui.Context, name, path string, system bool) {
	if path != "" && p.deps.icon != nil {
		if icon, ok := p.deps.icon(path); ok && icon != nil {
			ui.Image(c, icon).Size(18, 18).Shrink(0).Fit(ui.Contain)
			return
		}
	}
	t := c.Theme()
	fill, ink := menuBarPanelIconColor(name), ui.Hex("#ffffff")
	if system {
		fill, ink = t.Surface, t.TextMuted
	}
	initial := strings.TrimLeft(name, ".")
	if initial == "" {
		initial = name
	}
	if chars := []rune(initial); len(chars) > 0 {
		initial = strings.ToUpper(string(chars[0]))
	}
	ui.Box(c).Size(18, 18).Shrink(0).Radius(5).Center().Background(fill).
		Children(func() {
			ui.Text(c, initial).FontSize(10).FixedLineHeight(14).
				FontWeight(600).TextColor(ink)
		})
}

// menuBarPanelIconPalette is iconColor's palette, with its hues.
var menuBarPanelIconPalette = []struct {
	hue   float64
	color ui.Color
}{
	{158, ui.Hex("#0c9d66")}, {217, ui.Hex("#3b82f6")},
	{258, ui.Hex("#8b5cf6")}, {38, ui.Hex("#f59e0b")},
	{0, ui.Hex("#ef4444")}, {173, ui.Hex("#14b8a6")},
	{239, ui.Hex("#6366f1")}, {330, ui.Hex("#ec4899")},
}

// menuBarPanelIconColor selects the palette color at panelIconHue. The
// rounded hues stay distinct, and preserve each color's own saturation.
func menuBarPanelIconColor(name string) ui.Color {
	h := panelIconHue(name)
	nearest, distance := 0, math.Inf(1)
	for i, color := range menuBarPanelIconPalette {
		if d := math.Abs(h - color.hue); d < distance {
			nearest, distance = i, d
		}
	}
	return menuBarPanelIconPalette[nearest].color
}

// name opens the process page at this row after closing the panel.
func (p *menuBarPanel) name(c *ui.Context, name, key string, system bool, tab string) {
	button := ui.ButtonBase(c.Key("name")).Grow(1).MinWidth(0).Padding(0).
		Gap(6).Justify(ui.Start).Tooltip(name).Label(name).Cursor(ui.CursorPointer)
	button.Children(func() {
		text := ui.Text(c, name).MinWidth(0).SingleLine()
		if button.Hovered() {
			text.Underline()
		}
		if system {
			ui.Text(c, p.deps.text("process.system", nil)).Shrink(0).
				NoWrap().FontSize(10).TextColor(c.Theme().TextMuted)
		}
	})
	button.OnClick(func() {
		p.open(MenuBarTarget{Page: "process", Tab: tab, Key: key})
	})
}

// quitControl shows the row's quit step and a hover/focus-only retry.
func (p *menuBarPanel) quitControl(c *ui.Context, row ProcessRow, step panelQuitStep, hover bool) {
	t, colors := c.Theme(), menuBarPanelPalettes[c.Theme().Dark]
	state, key, danger, disabled := "", "status.apps.quit", false, false
	switch step {
	case "confirm":
		key = "menubar.panel.confirmQuit"
	case "quitting", "forcing":
		key, disabled, danger = "status.apps.quitting", true, step == "forcing"
	case "stuck", "forceConfirm":
		state, key, danger = "process.stuck", "process.force", true
		if step == "forceConfirm" {
			key = "menubar.panel.confirmForce"
		}
	case "failed":
		state = "menubar.panel.quitFail"
	case "done":
		ui.Text(c, p.deps.text("menubar.panel.quitted", nil)).Shrink(0).
			NoWrap().FontSize(10.5).TextColor(t.TextMuted)
		return
	}
	if state != "" {
		ui.Text(c, p.deps.text(state, nil)).Shrink(0).NoWrap().
			FontSize(10.5).TextColor(t.TextMuted)
	}
	// Idle and retry controls keep their room while transparent. The keyboard
	// can still reach them and shows the focused control.
	button := ui.Button(c.Key("quit"), p.deps.text(key, nil)).Shrink(0).
		Padding(1, 7).Radius(5).Border(1, t.Border).Background(colors.button).
		FontSize(10.5).FixedLineHeight(15).FontWeight(400).TextColor(t.Accent).
		Disabled(disabled).Cursor(ui.CursorPointer)
	if danger {
		button.TextColor(t.Danger)
	}
	if !hover && (step == "idle" || step == "failed") && !button.Focused() {
		button.Opacity(0)
	}
	button.OnClick(func() { p.quit(row, danger) })
}

// growthList shows the five largest weekly changes while monitoring is on.
func (p *menuBarPanel) growthList(c *ui.Context) {
	rows := panelGrowthTop(p.growth)
	if len(rows) == 0 {
		return
	}
	t := c.Theme()
	ui.Column(c.Key("growth-list")).Shrink(0).Padding(2, 0, 4).Children(func() {
		p.section(c, "menubar.panel.topGrowth", "menubar.panel.week")
		for _, app := range rows {
			line := ui.Row(c.Key(app.Key)).Height(30).Shrink(0).Padding(0, 14).Gap(8)
			if line.Hovered() {
				line.Background(t.SurfaceHover)
			}
			line.Children(func() {
				p.appIcon(c, app.Name, app.AppPath, false)
				p.name(c, app.Name, app.Key, false, "disk")
				value := "+" + growthSize(app.Week.Bytes)
				if app.Week.AtLeast {
					value = "≥ " + growthSize(app.Week.Bytes)
				}
				ui.Text(c, value).Shrink(0).MinWidth(52).NoWrap().TextAlign(ui.End).
					FontWeight(600).FontFeatures("tnum")
			})
		}
	})
}

// footer keeps navigation, the update interval and the menu at the bottom.
func (p *menuBarPanel) footer(c *ui.Context) {
	t := c.Theme()
	ui.Row(c.Key("footer")).Shrink(0).Margin(4, 0, 0).Padding(8, 14, 10).
		BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Gap(12).Children(func() {
			for _, link := range panelLinks(p.item) {
				ui.Link(c.Key(link.Key), p.deps.text(link.Key, nil)+" ›", "").
					FontWeight(600).NoWrap().TextColor(t.Accent).
					OnClick(func() { p.open(link.Target) })
			}
		})
		ui.Spacer(c)
		ui.Text(c, p.deps.text("menubar.panel.every", nil)).Shrink(0).
			FontSize(11).NoWrap().TextColor(t.TextMuted)
		label := p.deps.text("menubar.panel.more", nil)
		more := ui.ButtonBase(c.Key("more")).Bind(&p.more).Padding(0, 5).
			Margin(0, 0, 0, 6).Radius(5).FontSize(15).FixedLineHeight(18).
			TextColor(t.TextMuted).Label(label).Tooltip(label).
			Children(func() { ui.Text(c, "⋯") })
		if more.Hovered() {
			more.Background(t.SurfaceHover).TextColor(t.Text)
		}
		more.OnClick(func() {
			r := p.more.Bounds(c)
			if p.deps.menu != nil && p.deps.menu(int(panelRound(float64(r.X))),
				int(panelRound(float64(r.Y+r.H)))) {
				p.close()
			}
		})
	})
}

// close releases a pending hold before closing the panel.
func (p *menuBarPanel) close() {
	if p.deps.close != nil {
		p.deps.close()
	}
}

// open closes this panel before navigating the main window to target.
func (p *menuBarPanel) open(target MenuBarTarget) {
	p.close()
	if p.deps.open != nil {
		p.deps.open(target)
	}
}

// changeBook keeps one hold for all pending quits and replaces old timers.
func (p *menuBarPanel) changeBook(next panelQuitBook) {
	if maps.Equal(p.book.steps, next.steps) && maps.Equal(p.book.gone, next.gone) {
		return
	}
	wasPending := p.book.pending()
	p.book = next
	if pending := next.pending(); wasPending != pending && p.deps.hold != nil {
		p.deps.hold(pending)
	}
	p.confirmGeneration++
	if next.confirming() && p.deps.after != nil {
		generation := p.confirmGeneration
		p.deps.after(panelQuitConfirm, func() {
			if p.confirmGeneration == generation {
				p.changeBook(p.book.revertConfirms())
			}
		})
	}
}

// quit confirms an action before running it off the main thread; its result
// returns through update as the row's next step: done, stuck or failed.
func (p *menuBarPanel) quit(row ProcessRow, force bool) {
	step := p.book.step(row.Key)
	action, pending := p.deps.quit, panelQuitStep("quitting")
	if force {
		if step == "stuck" {
			p.changeBook(p.book.with(row.Key, "forceConfirm"))
			return
		}
		if step != "forceConfirm" {
			return
		}
		action, pending = p.deps.forceQuit, "forcing"
	} else {
		if step == "idle" || step == "failed" {
			p.changeBook(p.book.with(row.Key, "confirm"))
			return
		}
		if step != "confirm" {
			return
		}
	}
	if action == nil || p.update == nil {
		return
	}
	p.changeBook(p.book.with(row.Key, pending))
	update := p.update
	go func() {
		done, err := action(row.Path)
		result := panelQuitStep("stuck")
		var ce codeError
		if done && err == nil || errors.As(err, &ce) && ce.Code == "not-running" {
			result = "done"
		} else if err != nil && !force && ce.Code != "cannot-quit" {
			result = "failed"
		}
		update(func() { p.changeBook(p.book.with(row.Key, result)) })
	}()
}

// openMenuBarPanel reads only while its native flyout is open. Results and
// timers reach the view through Update and are dropped after it closes.
func openMenuBarPanel(tray *mygo.Tray, item string) *mygo.Window {
	p := newMenuBarPanel(item, menuBarPanelDeps{})
	w := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray: tray, Placement: mygo.PlacementBottom, Popover: true,
		Focusable: true, Width: menuBarPanelWidth,
		Height: int(menuBarPanelHeight(item)), Content: ui.View(p.view),
	})
	ctx, cancel := context.WithCancel(context.Background())
	p.update = func(fn func()) {
		w.Update(func() {
			if ctx.Err() == nil {
				fn()
			}
		})
	}
	p.deps.quit, p.deps.forceQuit = Cleaner{}.QuitApp, Cleaner{}.ForceQuitApp
	p.deps.open = menuBarGo
	p.deps.close = func() { menuBarRelease(w); w.Destroy() }
	p.deps.hold = func(on bool) { menuBarHoldPanel(w, on) }
	p.deps.after = func(d time.Duration, fn func()) {
		time.AfterFunc(d, func() { p.update(fn) })
	}
	p.deps.menu = func(x, y int) bool {
		chosen := false
		growthTrayMenu(residentModelNow(), func(it growthMenuItem) {
			chosen = true
			growthMenuAction(it)
		}).PopupAt(w, x, y)
		return chosen
	}
	p.deps.icon = menuBarPanelIcon
	menuBarNativePanels[w] = p
	w.OnClosed(func() {
		cancel()
		delete(menuBarNativePanels, w)
	})
	go func() {
		err := watchStatusWith(ctx, func(s StatusSample) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			p.update(func() { p.acceptStatus(s) })
			return nil
		}, menuBarPanelSource(realStatusSource(), item))
		if err != nil && ctx.Err() == nil {
			p.update(func() { p.failed = true })
		}
	}()
	if item == "cpu" || item == "mem" {
		go func() {
			err := watchProcessesWith(ctx, func(s ProcessSample) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				p.update(func() { p.acceptProcesses(s) })
				return nil
			}, realProcessSource())
			if err != nil && ctx.Err() == nil {
				p.update(func() { p.procFailed = true })
			}
		}()
	}
	if item == "cpu" || item == "gpu" || item == "disk" {
		go func() {
			info := Cleaner{}.MachineInfo()
			p.update(func() { p.info = &info })
		}()
	}
	if item == "cpu" {
		go func() {
			sys := Cleaner{}.Info()
			p.update(func() { p.sys = &sys })
		}()
	}
	if item == "disk" {
		p.loadGrowth()
	}
	return w
}

// menuBarNativePanels belongs to the main thread, as menuBar.panels does.
var menuBarNativePanels = map[*mygo.Window]*menuBarPanel{}

// loadGrowth reads growth off the main thread with a new request generation.
func (p *menuBarPanel) loadGrowth() {
	p.growthGeneration++
	generation := p.growthGeneration
	go func() {
		g := Cleaner{}.GrowthState()
		p.update(func() { p.acceptGrowth(generation, g) })
	}()
}

// menuBarGrowthChanged reloads the growth record of an open disk panel.
func menuBarGrowthChanged() {
	if !mygo.App.IsReady() {
		return
	}
	mygo.RunOnMain(func() {
		for _, p := range menuBarNativePanels {
			if p.item == "disk" {
				p.loadGrowth()
			}
		}
	})
}

// menuBarHoldPanel keeps the native window open for the existing bounded
// hold, without a page calling a bound method.
func menuBarHoldPanel(w *mygo.Window, on bool) {
	menuBarHolds.Lock()
	defer menuBarHolds.Unlock()
	if on {
		menuBarHolds.held[w] = time.Now()
	} else {
		delete(menuBarHolds.held, w)
	}
}

// menuBarPanelIcons caches icons, including failures, for the app's lifetime.
// One worker serializes Icon Services with the pages' icons through
// appIconDrawing.
var menuBarPanelIcons = struct {
	sync.Mutex
	cache map[string]*ui.Bitmap
	queue []string
	wake  chan struct{}
	once  sync.Once
}{cache: map[string]*ui.Bitmap{}, wake: make(chan struct{}, 1)}

// menuBarPanelIcon returns a cached icon or queues its first draw once.
func menuBarPanelIcon(path string) (*ui.Bitmap, bool) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), ".app") {
		return nil, false
	}
	icons := &menuBarPanelIcons
	icons.Lock()
	icon, found := icons.cache[path]
	if !found {
		icons.cache[path] = nil
		icons.queue = append(icons.queue, path)
	}
	icons.Unlock()
	if !found {
		icons.once.Do(func() { go menuBarPanelIconWorker() })
		select {
		case icons.wake <- struct{}{}:
		default:
		}
	}
	return icon, icon != nil
}

// menuBarPanelIconWorker draws queued icons one at a time and asks open
// panels to redraw on the main thread after publishing each result.
func menuBarPanelIconWorker() {
	icons := &menuBarPanelIcons
	for range icons.wake {
		for {
			icons.Lock()
			if len(icons.queue) == 0 {
				icons.Unlock()
				break
			}
			path := icons.queue[0]
			icons.queue[0] = ""
			icons.queue = icons.queue[1:]
			icons.Unlock()
			appIconDrawing.Lock()
			data := appIconRender(path, 36)
			appIconDrawing.Unlock()
			var icon *ui.Bitmap
			if len(data) > 0 && !iconLooksBlank(data) {
				icon, _ = ui.DecodeBitmap(data)
			}
			icons.Lock()
			icons.cache[path] = icon
			icons.Unlock()
			mygo.RunOnMain(func() {
				for w := range menuBarNativePanels {
					w.Invalidate()
				}
			})
		}
	}
}

// acceptGrowth keeps a result only if it belongs to the latest request.
func (p *menuBarPanel) acceptGrowth(generation uint64, g GrowthState) {
	if generation == p.growthGeneration {
		p.growth = &g
	}
}
