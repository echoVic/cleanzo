package main

// menubar_darwin.go is the menu bar readings on macOS (design:
// docs/superpowers/specs/2026-10-29-menubar-design.md): the native reads they
// show.

import (
	"context"
	"log"
	"math"
	"slices"
	"sync"

	"github.com/egoist/mygo"
)

// realMenuBarSource wires the readings to the home page's reads
// (status_darwin.go, fs_darwin.go, machine_darwin.go) and the battery's charge.
func realMenuBarSource() menuBarSource {
	return menuBarSource{
		cpu:    cpuBusyTotal,
		memory: readMemoryStatus,
		disk:   diskStat,
		machine: func() menuBarMachine {
			r, _ := newMachineReader().(*machineReader)
			if r == nil { // a nil *machineReader would make a non-nil interface
				return nil
			}
			return r
		},
		battery: menuBarBatteryLevel,
	}
}

// menuBarBatteryLevel is the battery's charge in percent, read from
// AppleSmartBattery as the system's own battery item reads it, without
// system_profiler (spec §六); -1 without a battery or when it cannot be read.
// On Apple silicon CurrentCapacity is a percentage of MaxCapacity, 100; on
// Intel both are mAh: the ratio is the charge either way.
func menuBarBatteryLevel() int {
	if !iokitInit() {
		return -1
	}
	battery := ioFirst("AppleSmartBattery")
	if battery == 0 {
		return -1
	}
	defer ioObjectRelease(battery)
	cur, ok := ioPropInt(battery, "CurrentCapacity")
	full, fullOK := ioPropInt(battery, "MaxCapacity")
	if !ok || !fullOK || cur < 0 || full <= 0 {
		return -1
	}
	return int(math.Min(100, math.Round(float64(cur)*100/float64(full))))
}

// menuBarHasBattery tells whether this Mac has a battery, asked once.
var menuBarHasBattery = sync.OnceValue(func() bool {
	if !iokitInit() {
		return false
	}
	battery := ioFirst("AppleSmartBattery")
	if battery == 0 {
		return false
	}
	ioObjectRelease(battery)
	return true
})

// menuBarSupported: the readings are macOS only (spec §二).
func menuBarSupported() bool { return true }

// menuBarController is the readings' icons. Only the main thread uses it; the
// sampler's goroutine hands it each reading through mygo.RunOnMain.
type menuBarController struct {
	shown  bool                    // show has run
	items  []string                // the readings shown, in menuBarItems' order
	layout int                     // names the icons (menuBarItemName)
	trays  map[string]*mygo.Tray   // by reading
	drawn  map[string]menuBarText  // what each icon shows
	widths map[string]float64      // the room each icon keeps, in points, in the app's language
	last   menuBarReading          // the latest reading, to draw again in another language
	stop   context.CancelFunc      // ends the sampler; nil while none runs
	panels map[string]*mygo.Window // open panels by reading; at most one
}

// menuBar is the app's controller.
var menuBar = &menuBarController{}

// menuBarShow, menuBarLayout and menuBarRelabel are what growth_app.go asks of
// the readings, on the main thread.
func menuBarShow(items []string, layout int) { menuBar.show(items, layout) }
func menuBarLayout() int                     { return menuBar.layout }
func menuBarRelabel()                        { mygo.RunOnMain(menuBar.relabel) }

// show makes the icons of items, named for layout, unless they show already,
// and starts their sampler (spec §三). To put them in menuBarItems' order left
// of whatever the menu bar shows, it removes the Cleanzo icon too, which the
// caller makes again after them (showGrowthTray), and makes them from right to
// left: each new icon goes left of the one made before it.
func (m *menuBarController) show(items []string, layout int) {
	if m.shown && slices.Equal(items, m.items) && layout == m.layout {
		return
	}
	m.shown = true
	m.closePanels()
	if m.panels == nil {
		m.panels = map[string]*mygo.Window{}
	}
	if m.stop != nil {
		m.stop()
		m.stop = nil
	}
	for _, t := range m.trays {
		t.Destroy()
	}
	m.items, m.layout = slices.Clone(items), layout
	m.trays, m.drawn = map[string]*mygo.Tray{}, map[string]menuBarText{}
	m.last = unknownMenuBarReading()
	if len(items) == 0 {
		return
	}
	hideGrowthTray()
	m.measure()
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		text := menuBarTextFor(item, m.last, nativeText)
		t, err := mygo.NewTray(mygo.TrayOptions{
			ID:             menuBarItemName(layout, item),
			Icon:           drawReadout(text, m.width(item, text), readoutInk{template: true}),
			IconIsTemplate: true,
			ToolTip:        nativeText("menubar.tip."+item, nil),
		})
		if err != nil {
			log.Println("menubar:", err)
			continue
		}
		t.OnAppearanceChange(func() { m.repaint(item) })
		t.OnClick(func() { m.toggle(item) })
		m.trays[item] = t
		m.drawn[item] = text
	}
	ctx, stop := context.WithCancel(context.Background())
	m.stop = stop
	go sampleMenuBar(ctx, m.items, realMenuBarSource(), func(r menuBarReading) {
		mygo.RunOnMain(func() {
			if ctx.Err() == nil { // not for icons made again since
				m.draw(r)
			}
		})
	})
}

// measure finds the room each icon keeps, in the app's language.
func (m *menuBarController) measure() {
	m.widths = map[string]float64{}
	for _, item := range m.items {
		m.widths[item] = readoutWidth(menuBarWidest(item, nativeText))
	}
}

// width is the room item's icon takes for t: its widest usual text's, or the
// widest text it has shown since, so that it widens at most once, as for a
// disk with a thousand gigabytes free, and never narrows back.
func (m *menuBarController) width(item string, t menuBarText) float64 {
	w := max(m.widths[item], readoutWidth(t))
	m.widths[item] = w
	return w
}

// draw shows reading r, drawing again only the icons whose text changed.
func (m *menuBarController) draw(r menuBarReading) {
	m.last = r
	for item, tray := range m.trays {
		text := menuBarTextFor(item, r, nativeText)
		if prev, ok := m.drawn[item]; ok && prev == text {
			continue
		}
		m.paint(item, tray, text)
	}
}

// paint draws t into item's icon: a template, or in the menu bar's ink when it
// has a tone.
func (m *menuBarController) paint(item string, tray *mygo.Tray, t menuBarText) {
	ink := readoutInk{template: t.Tone == "", tone: t.Tone}
	if !ink.template {
		ink.dark = tray.Dark()
	}
	png := drawReadout(t, m.width(item, t), ink)
	if png == nil {
		return
	}
	if err := tray.SetIcon(png, ink.template); err != nil {
		log.Println("menubar:", err)
		return
	}
	m.drawn[item] = t
}

// repaint draws item's icon again for the menu bar's new appearance. Only an
// icon in color needs it: macOS tints the templates itself.
func (m *menuBarController) repaint(item string) {
	if t, ok := m.drawn[item]; ok && t.Tone != "" {
		if tray := m.trays[item]; tray != nil {
			m.paint(item, tray, t)
		}
	}
}

// relabel draws every icon again in the app's new language, with its tooltip.
func (m *menuBarController) relabel() {
	if len(m.trays) == 0 {
		return
	}
	m.measure()
	for item, tray := range m.trays {
		tray.SetToolTip(nativeText("menubar.tip."+item, nil))
		m.paint(item, tray, menuBarTextFor(item, m.last, nativeText))
	}
}

// The panels' size in points (spec §九): as wide as the mockup's, as tall as
// their content up to most of a laptop's screen.
const (
	menuBarPanelWidth     = 320
	menuBarPanelMaxHeight = 640
)

// toggle opens item's panel below its icon and closes any other; a click on the
// icon while its panel is open closes it (spec §四). The panel is the app's
// page panel.html in the system's popover, which closes when the user clicks
// elsewhere. A quit under way keeps it open (MenuBarHold). It runs on the main
// thread.
func (m *menuBarController) toggle(item string) {
	if w := m.panels[item]; w != nil {
		w.Close()
		return
	}
	m.closePanels()
	t := m.trays[item]
	if t == nil {
		return
	}
	w := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:       t,
		Placement:  mygo.PlacementBottom,
		Popover:    true,
		Focusable:  true,
		Width:      menuBarPanelWidth,
		Height:     360,
		FitContent: true,
		MinWidth:   menuBarPanelWidth,
		MaxWidth:   menuBarPanelWidth,
		MaxHeight:  menuBarPanelMaxHeight,
		URL:        "/panel.html?item=" + item,
	})
	m.panels[item] = w
	w.OnClose(func(e *mygo.CloseEvent) {
		if menuBarHeld(w) {
			e.PreventDefault()
		}
	})
	w.OnClosed(func() {
		menuBarRelease(w)
		mygo.RunOnMain(func() {
			if m.panels[item] == w {
				delete(m.panels, item)
			}
		})
	})
}

// closePanels closes every open panel, held or not, as before its icon goes.
func (m *menuBarController) closePanels() {
	for item, w := range m.panels {
		menuBarRelease(w)
		w.Destroy()
		delete(m.panels, item)
	}
}

// menuBarBatteryTemp is the battery's temperature for the temperature panel,
// read from AppleSmartBattery without system_profiler; nil without a battery
// or when it cannot be read.
func menuBarBatteryTemp() *BatteryStatus {
	if !menuBarBattery() {
		return nil
	}
	if c, _ := smartBatteryExtras(false); c > 0 {
		return &BatteryStatus{Temperature: c}
	}
	return nil
}
