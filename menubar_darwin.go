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

	"github.com/ebitengine/purego/objc"
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

// menuBarController is the readings' items: one menu bar item per reading
// shown (spec §三.1), named after its reading, so that each stays where the
// user dragged it with ⌘. While a reading shows they are Cleanzo's place in
// the menu bar: a right-click on any shows Cleanzo's menu, and no Cleanzo icon
// shows (spec §三.4). Only the main thread uses it; the sampler's goroutine
// hands it each reading through mygo.RunOnMain.
type menuBarController struct {
	shown  bool                    // show has run
	items  []string                // the readings shown, in menuBarItems' order
	trays  map[string]*mygo.Tray   // by reading
	drawn  map[string]menuBarText  // what each item shows
	widths map[string]float64      // the room each item keeps, in points, in the app's language
	last   menuBarReading          // the latest reading, to draw again in another language
	stop   context.CancelFunc      // ends the sampler; nil while none runs
	panels map[string]*mygo.Window // open panels by reading; at most one
}

// menuBar is the app's controller.
var menuBar = &menuBarController{
	trays:  map[string]*mygo.Tray{},
	drawn:  map[string]menuBarText{},
	widths: map[string]float64{},
	panels: map[string]*mygo.Window{},
}

// menuBarShow, menuBarShowing and menuBarRelabel are what growth_app.go asks
// of the readings, on the main thread.
func menuBarShow(items []string) { menuBar.show(items) }
func menuBarShowing() bool       { return len(menuBar.trays) > 0 }
func menuBarRelabel()            { mygo.RunOnMain(menuBar.relabel) }

// show shows an item for each reading in items and starts their sampler
// (spec §三). A reading that stays keeps its item, where the user dragged it;
// one that goes loses its item; a new one gets an item named after it
// (menuBarReadingID), which macOS puts back where the user last left it. The
// first reading removes the Cleanzo icon, which does not show with readings
// (spec §三.4). New items are made from right to left: each goes left of those
// made before it, so readings that first show together come in menuBarItems'
// order.
func (m *menuBarController) show(items []string) {
	if m.shown && slices.Equal(items, m.items) {
		return
	}
	m.shown = true
	m.closePanels()
	if m.stop != nil {
		m.stop()
		m.stop = nil
	}
	for item, t := range m.trays {
		if !slices.Contains(items, item) {
			t.Destroy()
			delete(m.trays, item)
			delete(m.drawn, item)
		}
	}
	m.items = slices.Clone(items)
	if len(m.trays) == 0 {
		m.last = unknownMenuBarReading()
		if len(items) > 0 {
			hideGrowthTray()
		}
	}
	if len(items) == 0 {
		return
	}
	m.measure()
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		if m.trays[item] != nil {
			continue
		}
		t, err := mygo.NewTray(mygo.TrayOptions{ID: menuBarReadingID(item), ToolTip: nativeText("menubar.tip."+item, nil)})
		if err != nil {
			log.Println("menubar:", err)
			continue
		}
		t.OnAppearanceChange(func() { m.repaint(item) })
		t.OnClick(func() { m.click(item) })
		t.OnRightClick(m.menu)
		m.trays[item] = t
		m.paint(item, t, menuBarTextFor(item, m.last, nativeText))
	}
	ctx, stop := context.WithCancel(context.Background())
	m.stop = stop
	go sampleMenuBar(ctx, m.items, realMenuBarSource(), func(r menuBarReading) {
		mygo.RunOnMain(func() {
			if ctx.Err() == nil { // not for readings changed since
				m.draw(r)
			}
		})
	})
}

// measure finds the room each reading keeps, in the app's language, for the
// readings that have none yet.
func (m *menuBarController) measure() {
	for _, item := range m.items {
		if _, ok := m.widths[item]; !ok {
			m.widths[item] = readoutWidth(menuBarWidest(item, nativeText))
		}
	}
}

// width is the room item's item takes for t: its widest usual text's, or the
// widest text it has shown since, so that it widens at most once, as for a
// disk with a thousand gigabytes free, and never narrows back.
func (m *menuBarController) width(item string, t menuBarText) float64 {
	w := max(m.widths[item], readoutWidth(t))
	m.widths[item] = w
	return w
}

// draw shows reading r, drawing again only the items whose text changed.
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

// paint draws t into item's item: a template, or in the menu bar's ink when it
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

// repaint draws item's item again for the menu bar's new appearance. Only an
// item in color needs it: macOS tints the templates itself.
func (m *menuBarController) repaint(item string) {
	if t, ok := m.drawn[item]; ok && t.Tone != "" {
		if tray := m.trays[item]; tray != nil {
			m.paint(item, tray, t)
		}
	}
}

// relabel draws every item again in the app's new language, with its tooltip.
func (m *menuBarController) relabel() {
	if len(m.trays) == 0 {
		return
	}
	clear(m.widths)
	m.measure()
	for item, tray := range m.trays {
		tray.SetToolTip(nativeText("menubar.tip."+item, nil))
		m.paint(item, tray, menuBarTextFor(item, m.last, nativeText))
	}
}

// click opens item's panel (spec §四), or Cleanzo's menu when Control is held,
// as for a right-click (spec §三.4).
func (m *menuBarController) click(item string) {
	if menuBarControlHeld() {
		m.menu()
		return
	}
	m.toggle(item)
}

// menu shows Cleanzo's menu at the pointer (spec §三.4): the lines of the
// Cleanzo icon, which does not show while a reading does. A right-click or a
// Control-click on a reading asks for it. At the pointer, it shows on the
// display the user clicked: macOS 26 shows each menu bar item on every
// display, and an item's own menu drops from its window, which can be on
// another. It closes an open panel first: the user asked for something else.
// While a quit under way holds a panel open, no menu shows, as no other
// reading's panel opens then (spec §四.5).
func (m *menuBarController) menu() {
	for _, open := range m.panels {
		if menuBarHeld(open) {
			return
		}
	}
	m.closePanels()
	growthTrayMenu(residentModelNow(), growthMenuAction).Popup(nil)
}

// menuBarControlHeld tells whether Control is held now. A click with it is a
// right-click on macOS, which mygo 0.7.1 still reports as a click.
func menuBarControlHeld() bool {
	if !appKitInit() {
		return false
	}
	held := false
	withPool(func() {
		flags := objc.Send[uint64](objc.ID(objc.GetClass("NSEvent")), nsSel("modifierFlags"))
		held = flags&(1<<18) != 0 // NSEventModifierFlagControl
	})
	return held
}

// menuBarPanelWidth is the mockup's width in points (spec §九).
const menuBarPanelWidth = 320

// toggle opens item's native panel below its item and closes any other;
// clicking the item again closes it (spec §四). The system popover anchors
// to the item's own window; on macOS 26 that can be on another display than
// the copy clicked, until mygo places flyouts from that copy. A quit under
// way keeps the panel open (spec §四.5). It runs on the main thread.
func (m *menuBarController) toggle(item string) {
	if w := m.panels[item]; w != nil {
		w.Close()
		return
	}
	// A quit under way keeps its panel open (spec §四.5): another reading
	// does not open until that quit is done.
	for _, open := range m.panels {
		if menuBarHeld(open) {
			return
		}
	}
	m.closePanels()
	t := m.trays[item]
	if t == nil {
		return
	}
	w := openMenuBarPanel(t, item)
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

// closePanels closes every open panel, held or not, as before an item goes or
// Cleanzo's menu shows.
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
