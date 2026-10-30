package main

// menubar_darwin.go is the menu bar readings on macOS (design:
// docs/superpowers/specs/2026-10-29-menubar-design.md): the native reads they
// show.

import (
	"context"
	"log"
	"math"
	"slices"
	"strings"
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

// menuBarController is the readings' item: one menu bar item that draws every
// reading shown, side by side (spec §三.1), so that macOS cannot move them
// apart or change their order. While a reading shows it is Cleanzo's place in
// the menu bar: a right-click on it shows Cleanzo's menu, and no Cleanzo icon
// shows (spec §三.4). Only the main thread uses it; the sampler's goroutine
// hands it each reading through mygo.RunOnMain.
type menuBarController struct {
	shown  bool                    // show has run
	items  []string                // the readings shown, in menuBarItems' order
	tray   *mygo.Tray              // the readings' item; nil while none shows
	drawn  []menuBarText           // what the item shows: a text per reading, in items' order
	slots  []float64               // each reading's room in the drawn image, in points
	widths map[string]float64      // the room each reading keeps, in points, in the app's language
	last   menuBarReading          // the latest reading, to draw again in another language
	stop   context.CancelFunc      // ends the sampler; nil while none runs
	panels map[string]*mygo.Window // open panels by reading; at most one
}

// menuBar is the app's controller.
var menuBar = &menuBarController{}

// menuBarShow, menuBarShowing and menuBarRelabel are what growth_app.go asks
// of the readings, on the main thread.
func menuBarShow(items []string) { menuBar.show(items) }
func menuBarShowing() bool       { return menuBar.tray != nil }
func menuBarRelabel()            { mygo.RunOnMain(menuBar.relabel) }

// show shows the readings in items in one item and starts their sampler
// (spec §三). The first reading makes the item, under menuBarItemID. It
// removes the Cleanzo icon first, which shares that name and must not show
// with it (spec §三.4), so the item comes where the icon was. A change of
// readings keeps the item, and the place the user dragged it to with ⌘: it
// draws the new readings, wider or narrower, and its sampler starts again for
// them. No reading removes the item.
func (m *menuBarController) show(items []string) {
	if m.shown && slices.Equal(items, m.items) {
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
	m.items = slices.Clone(items)
	m.drawn, m.slots = nil, nil
	if len(items) == 0 {
		if m.tray != nil {
			m.tray.Destroy()
			m.tray = nil
		}
		m.last = unknownMenuBarReading()
		return
	}
	m.measure()
	if m.tray == nil {
		m.last = unknownMenuBarReading()
		hideGrowthTray()
		t, err := mygo.NewTray(mygo.TrayOptions{ID: menuBarItemID, ToolTip: m.tip()})
		if err != nil {
			log.Println("menubar:", err)
			return
		}
		t.OnAppearanceChange(m.repaint)
		t.OnClick(m.click)
		t.OnRightClick(m.menu)
		m.tray = t
	} else {
		m.tray.SetToolTip(m.tip())
	}
	m.paint(m.texts(m.last))
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

// tip is the item's tooltip: what each reading is, in items' order.
func (m *menuBarController) tip() string {
	tips := make([]string, len(m.items))
	for i, item := range m.items {
		tips[i] = nativeText("menubar.tip."+item, nil)
	}
	return strings.Join(tips, " · ")
}

// measure finds the room each reading keeps, in the app's language.
func (m *menuBarController) measure() {
	m.widths = map[string]float64{}
	for _, item := range m.items {
		m.widths[item] = readoutWidth(menuBarWidest(item, nativeText))
	}
}

// width is the room item takes for t: its widest usual text's, or the widest
// text it has shown since, so that it widens at most once, as for a disk with
// a thousand gigabytes free, and never narrows back.
func (m *menuBarController) width(item string, t menuBarText) float64 {
	w := max(m.widths[item], readoutWidth(t))
	m.widths[item] = w
	return w
}

// texts is what each reading shows for r, in items' order.
func (m *menuBarController) texts(r menuBarReading) []menuBarText {
	out := make([]menuBarText, len(m.items))
	for i, item := range m.items {
		out[i] = menuBarTextFor(item, r, nativeText)
	}
	return out
}

// draw shows reading r, drawing the item again only when a reading's text
// changed.
func (m *menuBarController) draw(r menuBarReading) {
	m.last = r
	if texts := m.texts(r); !slices.Equal(texts, m.drawn) {
		m.paint(texts)
	}
}

// paint draws texts into the item, a reading per slot: a template, or in the
// menu bar's ink when a reading has a tone.
func (m *menuBarController) paint(texts []menuBarText) {
	if m.tray == nil || len(texts) != len(m.items) {
		return
	}
	template := !slices.ContainsFunc(texts, func(t menuBarText) bool { return t.Tone != "" })
	dark := !template && m.tray.Dark()
	slots := make([]readoutSlot, len(texts))
	widths := make([]float64, len(texts))
	for i, t := range texts {
		widths[i] = m.width(m.items[i], t)
		slots[i] = readoutSlot{text: t, width: widths[i]}
	}
	png := drawReadouts(slots, template, dark)
	if png == nil {
		return
	}
	if err := m.tray.SetIcon(png, template); err != nil {
		log.Println("menubar:", err)
		return
	}
	m.drawn, m.slots = texts, widths
}

// repaint draws the item again for the menu bar's new appearance. Only an
// item in color needs it: macOS tints a template itself.
func (m *menuBarController) repaint() {
	if slices.ContainsFunc(m.drawn, func(t menuBarText) bool { return t.Tone != "" }) {
		m.paint(m.drawn)
	}
}

// relabel draws the item again in the app's new language, with its tooltip.
func (m *menuBarController) relabel() {
	if m.tray == nil {
		return
	}
	m.measure()
	m.tray.SetToolTip(m.tip())
	m.paint(m.texts(m.last))
}

// click opens the panel of the reading clicked (spec §四): menuBarHit finds it
// from where in the item the click was. A click with Control held shows
// Cleanzo's menu, as a right-click does. A press that is no click, as
// VoiceOver's, opens the first reading's panel.
func (m *menuBarController) click() {
	if m.tray == nil || len(m.items) == 0 {
		return
	}
	i := 0
	if x, control, ok := menuBarClickAt(float64(m.tray.Bounds().Width), readoutsWidth(m.slots)); ok {
		if control {
			m.menu()
			return
		}
		i = menuBarHit(x, m.slots)
	}
	if i < len(m.items) {
		m.toggle(m.items[i])
	}
}

// menu shows Cleanzo's menu below the item (spec §三.4): the lines of the
// Cleanzo icon, which does not show while a reading does. It closes an open
// panel first: the user asked for something else. While a quit under way holds
// a panel open, no menu shows, as no other reading's panel opens then (spec
// §四.5). The item has no menu of its own, so that a click reaches OnClick: it
// is given one only while it shows it.
func (m *menuBarController) menu() {
	t := m.tray
	if t == nil {
		return
	}
	for _, open := range m.panels {
		if menuBarHeld(open) {
			return
		}
	}
	m.closePanels()
	t.SetMenu(growthTrayMenu(residentModelNow(), growthMenuAction))
	t.PopUpMenu()
	t.SetMenu(nil)
}

// nsPoint is NSPoint, returned by value.
type nsPoint struct{ X, Y float64 }

// menuBarClickAt reads the click being handled in the readings' item. x is
// where it is in the item's image, in points from its left edge: its place in
// the item's window less the room macOS leaves on each side of the image, which
// it centers. control tells whether Control was held. itemWidth is the item's
// width, imageWidth the image's. ok is false when the event being handled is no
// click, or an old one, as for VoiceOver's press. It runs on the main thread,
// in the click's action: mygo calls OnClick from it, so the current event is
// the click.
func menuBarClickAt(itemWidth, imageWidth float64) (x float64, control, ok bool) {
	if !appKitInit() {
		return 0, false, false
	}
	withPool(func() {
		app := objc.ID(objc.GetClass("NSApplication")).Send(nsSel("sharedApplication"))
		ev := app.Send(nsSel("currentEvent"))
		if ev == 0 {
			return
		}
		switch objc.Send[uint64](ev, nsSel("type")) {
		case 1, 2: // NSEventTypeLeftMouseDown, NSEventTypeLeftMouseUp
		default:
			return
		}
		if ev.Send(nsSel("window")) == 0 {
			return
		}
		// A press that no click sent, as VoiceOver's, leaves the app's last
		// event current, which may be an old click in another window: the
		// click this action is for happened a moment ago.
		info := objc.ID(objc.GetClass("NSProcessInfo")).Send(nsSel("processInfo"))
		if objc.Send[float64](info, nsSel("systemUptime"))-objc.Send[float64](ev, nsSel("timestamp")) > 1 {
			return
		}
		p := objc.Send[nsPoint](ev, nsSel("locationInWindow"))
		x = p.X - (itemWidth-imageWidth)/2
		control = objc.Send[uint64](ev, nsSel("modifierFlags"))&(1<<18) != 0 // NSEventModifierFlagControl
		ok = true
	})
	return x, control, ok
}

// The panels' size in points (spec §九): as wide as the mockup's, as tall as
// their content up to most of a laptop's screen.
const (
	menuBarPanelWidth     = 320
	menuBarPanelMaxHeight = 640
)

// toggle opens item's panel below the readings' item and closes any other; a
// click on that reading while its panel is open closes it (spec §四). The panel
// is the app's page panel.html in the system's popover, which AppKit anchors to
// the readings' item on whichever display the user clicked: a floating panel
// placed from the item's frame lands on the wrong display of a Mac with two,
// where macOS 26 keeps a hidden copy of every menu bar item. The popover takes
// the menu bar's appearance, which can be dark while the app is light, so the
// page is told which it is and draws an opaque card in that theme. A quit under
// way keeps the panel open (MenuBarHold). It runs on the main thread.
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
	t := m.tray
	if t == nil {
		return
	}
	dark := "0"
	if t.Dark() {
		dark = "1"
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
		URL:        "/panel.html?item=" + item + "&dark=" + dark,
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

// closePanels closes every open panel, held or not, as before the item goes or
// its menu shows.
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
