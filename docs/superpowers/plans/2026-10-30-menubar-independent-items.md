# Menu Bar Readings as Independent Items — Implementation Plan

> **For agentic workers:** In this project the tasks are executed by traecli and the Codex CLI. **Executors do not commit**: the reviewer checks and commits each task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** On macOS, each reading shown is its own menu bar item again, named after its reading, so the user can ⌘-drag each one on its own and macOS keeps each place. Clicking an item opens that reading's panel. Kept from the earlier change today:
- no Cleanzo icon while a reading shows;
- Cleanzo's menu opens from a right-click or Control-click on any reading, and from the panel's 「⋯」.

The right-click menu now shows at the pointer, so it appears on the display the user clicked.

**Why (the maintainer, 2026-10-30):**
- After trying the readings drawn in one item, the maintainer showed Pulse's menu bar and said: 「类似pulse 这种，独立就行，也能独立拖拽放位置」.
- The maintainer also said: 「我有外接屏幕，在外接屏幕点击的时候，下拉会显示在主屏幕上」.
  - Panels: they need a mygo change (a separate PR), because mygo places them from the item's own window, which is on the main display.
  - The right-click menu: this plan fixes it by showing the menu at the pointer.

**Architecture:** One task, in Go plus strings and docs.
- The controller in `menubar_darwin.go` goes back to a `*mygo.Tray` per reading, named `cleanzo.<reading>` (`menuBarReadingID`).
  - `show` changes incrementally: it removes the items of readings that went, keeps the rest, and makes items only for new readings (right to left).
  - The drawing and readout files go back to their single-reading form, as they were before the one-item change (`git checkout dd2484e`).
  - A click toggles that reading's panel, unless Control is held.
  - A right-click or Control-click pops up Cleanzo's menu at the pointer (`Menu.Popup(nil)`).
- The Cleanzo icon keeps the name `cleanzo` (`menuBarIconID`) and shows only while monitoring is on and no reading shows, as today.

**Tech Stack:** Go 1.27.3, mygo 0.7.1 (`NewTray`, `OnClick`, `OnRightClick`, `Menu.Popup`), purego objc. No new dependencies. TypeScript strings only on the web side.

**Spec:** `docs/superpowers/specs/2026-10-29-menubar-design.md` (updated 2026-10-30 for independent items: §二, §三.1, §三.3, §三.4, §四, §八.7, §十). Earlier plans: `docs/superpowers/plans/2026-10-29-menubar.md` (independent items with a layout number) and `docs/superpowers/plans/2026-10-30-menubar-one-item.md` (one item; its menu and 「⋯」 parts stay).

## Global Constraints

- **macOS only.** Windows shows no readings, and the `menubar_other.go` stubs keep their signatures (`menuBarShow([]string)`, `menuBarShowing() bool`, `menuBarRelabel()`). Keep `GOOS=windows go vet ./...` clean.
- **Repository rules:**
  - exported Go names only in untagged files;
  - no `/` path string literals in untagged non-test `.go` files;
  - comments say what the code does, in the surrounding voice, wrapped at 80 columns;
  - user-facing text in Chinese and English.
- **Unchanged:**
  - the readings' text, widths (high-water mark), colors and drawing;
  - the sampler;
  - the panels (popover, opaque card, holds during a quit);
  - `MenuBarMenu` and the 「⋯」;
  - the login rules;
  - the menu's lines;
  - `menubar.json` without a layout number.
- **Never send a message returning `NSRect` through `objc.Send`** (amd64 needs `objc_msgSend_stret`).

## Decisions this plan makes (reviewers, please check)

- **Names:** `cleanzo.cpu`, `cleanzo.mem`, `cleanzo.gpu`, … for the readings, and `cleanzo` for the icon. They never change, so macOS puts each item back where the user left it. The layout number stays gone: the readings are not remade in order when the set changes, and the user's places win.
- **First appearance:** new items are made from right to left, so readings that first show together appear in `menuBarItems`' order. A reading turned on later appears where macOS puts a new item, or where the user last left it.
- **Control-click:** mygo 0.7.1 reports a Control-click as a click, so the click handler checks `[NSEvent modifierFlags]` (the class method: the keys held now) for Control. mygo 0.7.3 routes it to `OnRightClick` itself; the check is harmless there.
- **The menu at the pointer:** `Menu.Popup(nil)` shows the menu at `[NSEvent mouseLocation]`, on the display the user clicked. The item's own menu would drop from the item's window, which macOS 26 can keep on another display.

---

### Task 1: Go, strings and docs — an item per reading

**Files:**
- Restore from `dd2484e`: `menubar_draw_darwin.go`, `menubar_draw_darwin_test.go`, `menubar_readout.go`, `menubar_readout_test.go`
- Modify: `menubar.go`, `menubar_test.go`, `menubar_darwin.go`, `growth_app.go`, `src/i18n.ts`, `README.md`, `CHANGELOG.md`

**Interfaces:**
- Consumes:
  - `drawReadout(t menuBarText, w float64, ink readoutInk) []byte`, `readoutWidth`, `menuBarTextFor`, `menuBarWidest`, `unknownMenuBarReading` (restored files);
  - `growthTrayMenu`, `growthMenuAction`, `residentModelNow`, `hideGrowthTray`, `menuBarHeld`, `menuBarRelease` (unchanged).
- Produces:
  - `const menuBarIconID = "cleanzo"`;
  - `func menuBarReadingID(item string) string`;
  - the controller's `show(items)`, `click(item)`, `menu()`, and `menuBarControlHeld() bool`.

- [ ] **Step 1: restore the single-reading drawing.** Run:

```sh
git checkout dd2484e -- menubar_draw_darwin.go menubar_draw_darwin_test.go menubar_readout.go menubar_readout_test.go
```

This brings back `drawReadout` and its tests, and drops `readoutGap`, `readoutsWidth`, `menuBarHit`, `readoutSlot` and `drawReadouts`, which only the one-item drawing used.

- [ ] **Step 2: names, test first.** In `menubar_test.go`, append:

```go
// The items' names never change: macOS keeps under each where the user
// dragged it with ⌘, and a new name would lose that place.
func TestMenuBarNamesStay(t *testing.T) {
	if menuBarIconID != "cleanzo" {
		t.Errorf("icon %q", menuBarIconID)
	}
	for item, want := range map[string]string{"cpu": "cleanzo.cpu", "mem": "cleanzo.mem", "battery": "cleanzo.battery"} {
		if got := menuBarReadingID(item); got != want {
			t.Errorf("%s: %q, want %q", item, got, want)
		}
	}
}
```

In `menubar.go`, replace `menuBarItemID` and its comment with:

```go
// menuBarIconID names the Cleanzo icon, which shows while monitoring is on and
// no reading shows (spec §三.4).
const menuBarIconID = "cleanzo"

// menuBarReadingID names item's menu bar item. macOS keeps under the name
// where the user dragged the item with ⌘, and puts it back there whenever it
// shows again (spec §三.3), so the names never change.
func menuBarReadingID(item string) string { return "cleanzo." + item }
```

Make these comment changes in `menubar.go`:
- `menuBarItems`' comment becomes `menuBarItems are the readings in the order they first show in the menu bar, left to right (spec §三.3).`
- The package comment's `makes the readings' item and feeds it` becomes `makes the readings' items and feeds them`.

- [ ] **Step 3: the controller.** In `menubar_darwin.go`:
  - Imports: drop `"strings"`. Keep `objc`.
  - Replace everything from `// menuBarController is the readings' item` down to the end of `menuBarClickAt` (just before `// The panels' size in points`) with:

```go
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
```

  - In `toggle`:
    - Its doc comment becomes (wrapped at 80 columns): `toggle opens item's panel below its item and closes any other; a click on the item while its panel is open closes it (spec §四). The panel is the app's page panel.html in the system's popover, anchored to the item's own window. macOS 26 shows each menu bar item on every display and keeps that window on one of them, so on a Mac with two displays the panel can open on the other display than the one clicked; mygo's plain flyout has the same problem until mygo places it from the copy clicked. The popover takes the menu bar's appearance, which can be dark while the app is light, so the page is told which it is and draws an opaque card in that theme. A quit under way keeps the panel open (MenuBarHold). It runs on the main thread.`
    - `t := m.tray` becomes `t := m.trays[item]`.
  - The `closePanels` comment becomes: `closePanels closes every open panel, held or not, as before an item goes or Cleanzo's menu shows.`

- [ ] **Step 4: `growth_app.go`.**
  - `menuBarItemID` becomes `menuBarIconID` in `showGrowthTray`'s `NewTray` call.
  - Comment rewording, wrapped at 80 columns. Leave the code alone.
    - In `growthTray`'s comment, `with readings, their item carries Cleanzo's menu instead` becomes `with readings, their items carry Cleanzo's menu instead`.
    - In `growthMenuShown`'s comment, `what the readings' item shows on a right-click and in its panels` becomes `what the readings show on a right-click and in their panels`.
    - In `startResident`'s comment, `the readings chosen, in one item, or else the Cleanzo icon` becomes `the readings chosen, an item each, or else the Cleanzo icon`.
    - The comment on the `menuBarShow(mb.Items)` call becomes `// first: with a reading, the readings carry the menu and no icon shows`.
    - In `applyResident`'s bullet, `the readings in mb, in one item, which carries Cleanzo's menu;` becomes `the readings in mb, an item each, which carry Cleanzo's menu;`.
    - In `showGrowthTray`'s comment, `the readings' item carries it, on a right-click and in its panels` becomes `the readings carry it, on a right-click and in their panels`.
    - In `residentModelNow`'s comment, `for the readings' item to show` becomes `for the readings to show`.

- [ ] **Step 5: strings and docs.**
  - `src/i18n.ts`, `settings.menubar.note`:
    - Chinese: `"勾选的读数各自显示在菜单栏里，按住 ⌘ 可以把每一项拖到想放的位置；点一个读数看它的详情和占用最多的 App，右键点读数，或者点面板里的「⋯」，打开 Cleanzo 的菜单。有读数时，关掉窗口 Cleanzo 会留在菜单栏。"`
    - English: `"Each checked reading shows in the menu bar on its own; hold ⌘ to drag each where you want it. Click a reading for its details and the apps using the most; right-click it, or click ⋯ in a panel, for Cleanzo's menu. While any shows, closing the window keeps Cleanzo in the menu bar."`
  - `README.md`: replace `读数都画在菜单栏的同一项里，顺序固定；有读数时不再单独显示 Cleanzo 图标` with `每个读数是菜单栏里单独的一项，按住 ⌘ 可以各自拖到想放的位置；有读数时不再单独显示 Cleanzo 图标`.
  - `CHANGELOG.md`, first bullet of 「未发布」: replace `所有读数画在同一项里、顺序固定，` with `每个读数单独一项、可以按住 ⌘ 各自拖动，`.

- [ ] **Step 6: verify.** All must pass:
  - `gofmt -l .` prints nothing;
  - `go vet ./...`, `GOOS=windows GOARCH=amd64 go vet ./...` and `GOARCH=amd64 go vet .`;
  - `go test -count=1 -race -run 'TestMenuBar|TestReadout|TestDrawReadout|TestSaveMenuBar|TestResident|TestGrowth|TestShowGrowthMenu|TestApplyGrowthLifecycle|TestWatchMenuBarPanel|TestNoSlashPathLiterals|TestSlashPathLiteralLint|TestBindingsArePlatformNeutral' .`;
  - `bun run typecheck`;
  - `bun test`.

  `bun run generate` must leave `src/mygo.ts` unchanged: no Go signature changes.

- [ ] **Step 7: report.** Don't commit. Write the report to the path the dispatch gives.

## Reviewer verification (on the maintainer's Mac)

1. The defaults show two items, CPU then 内存, and no Cleanzo icon.
2. ⌘-drag 内存 somewhere else, then turn GPU on in Settings. 内存 and CPU stay put, and GPU appears.
3. Click CPU, then 内存: each opens its own panel. A right-click or Control-click on either shows Cleanzo's menu at the pointer, on the display clicked.
4. On the external display, the menu appears there. The panel still opens on the main display until the mygo change lands.
