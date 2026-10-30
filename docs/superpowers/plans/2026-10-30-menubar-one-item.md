# Menu Bar Readings in One Item — Implementation Plan

> **For agentic workers:** In this project the tasks are executed by traecli and the Codex CLI. **Executors do not commit**: the reviewer checks and commits each task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** On macOS, every reading shown in the menu bar is drawn into one menu bar item, so the readings always sit together in a fixed order; a click on a reading opens that reading's panel. While a reading shows, the separate Cleanzo icon is gone: its menu opens from a right-click (or Control-click) on the readings and from a 「⋯」 button in each panel.

**Why (the maintainer, 2026-10-30):**
- On the real Mac, with each reading as its own menu bar item, macOS 26 moved them apart and swapped their order whenever it re-laid out the menu bar. The maintainer chose 「合成一项」: all readings in one item.
- Looking at the Cleanzo icon beside the readings, the maintainer said 「这个就不用再显示了」: no Cleanzo icon while readings show.

**Architecture:**
- **Task 1 (Go):**
  - `menubar_readout.go` gets the layout math of the combined image: `readoutGap`, `readoutsWidth` and `menuBarHit`, which maps a click's x to a reading.
  - `menubar_draw_darwin.go` draws several readings into one image (`drawReadouts`), which replaces `drawReadout`.
  - The controller in `menubar_darwin.go` keeps one `*mygo.Tray` for all readings. A click reads the current AppKit event to find which reading was under the pointer; a right-click or Control-click pops up Cleanzo's menu on the item.
  - `growth_app.go` shows the Cleanzo icon only while monitoring is on and no reading shows. The resident menu model stays current either way.
  - The layout number goes away: the icon and the readings item never show together, so they share one autosave name, `cleanzo`.
  - A new bound method `MenuBarMenu(x, y)` pops up the same menu inside the panel that calls it.
- **Task 2 (web and docs):** the panel footer's 「⋯」 button calls `MenuBarMenu`. Settings' note, README and CHANGELOG describe the one item and where the menu went.

**Tech Stack:** Go 1.27.3, mygo 0.7.1 (`Tray.OnRightClick`, `Tray.SetMenu`/`PopUpMenu`, `Tray.Bounds`, `Menu.PopupAt`, `CallerWindow`), purego objc (`objc.Send[T]` returns structs of two doubles by value on both arm64 and amd64). TypeScript, React 19, bun test. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-29-menubar-design.md`, updated 2026-10-30: §二 goals, §三.1, §三.3, §三.4, §四, §七.2, §八.4, §十. The plan that built the readings is `docs/superpowers/plans/2026-10-29-menubar.md`. This plan changes its icons, order and Cleanzo-icon decisions and nothing else.

## Global Constraints

- **macOS only.** Windows shows no readings and no panels, and its behavior is unchanged. Windows compiles against the stubs in `menubar_other.go`. Keep `GOOS=windows go vet ./...` clean.
- **Repository rules:** exported Go names live only in untagged files. Untagged non-test `.go` files contain no string literal with a `/` path. Comments say what the code does and why, in the surrounding code's voice. User-facing text comes from `src/native-i18n.json` (Go) and `src/i18n.ts` (web), Chinese and English both.
- **Unchanged:**
  - which readings exist, their text and colors;
  - the sampler and its cost;
  - the panels' content and their popover;
  - holds during a quit;
  - the login-item rules;
  - the menu's lines.
- **No Intel breakage:** never send a message that returns `NSRect` through `objc.Send` (on amd64 that needs `objc_msgSend_stret`). Use `Tray.Bounds()` for the item's frame.

## Decisions this plan makes (reviewers, please check)

- **Gap:** 8 points between two readings (`readoutGap`), on top of the 1-point pad each reading already has on each side. The item reads as one group, unlike the ~20 points macOS leaves between separate items.
- **Which reading a click is on:** the x of the current event in the item's window (`locationInWindow`), less half the room macOS leaves around the centered image (`(Bounds().Width − image width) / 2`). The gap between two readings splits at its middle; left of the first or right of the last counts as that reading.
  - `[NSApp currentEvent]` is the click itself: mygo calls `OnClick` synchronously from the button's action (`mygoTrayClicked:`).
  - A press with no left-mouse event (VoiceOver's AXPress) opens the first reading's panel.
- **Panel anchor:** the popover still anchors to the whole item, so its arrow points at the item's middle. mygo has no sub-rectangle anchor for tray popovers.
- **Cleanzo's menu while readings show:**
  - **Right-click or Control-click on the item:** the item gets the menu only while it shows it: `SetMenu(menu)`, `PopUpMenu()`, `SetMenu(nil)`. Otherwise AppKit would open the menu on every left click too. Any open panel closes first.
  - **The panel's 「⋯」:** `MenuBarMenu(x, y)` pops up the same menu at the button with `Menu.PopupAt(panel, x, y)`. mygo runs the chosen line's `Click` before `PopupAt` returns, so a chosen line then closes the panel.
- **Names:** one autosave name, `cleanzo`, for both the readings item and the Cleanzo icon. They never show together: the controller removes the icon before it makes the item, and removes the item before `showGrowthTray` makes the icon.
  - Whichever appears comes where the other was, or where the user ⌘-dragged it.
  - The layout number (`menubar.json`'s `layout`) is dropped. A saved file that still has one loads fine; JSON ignores the extra field.
- **Tooltip:** each reading's existing tooltip, joined with ` · ` in the item's order.

## File Structure

| File | Task | Change |
|------|------|--------|
| `menubar_readout.go` | 1 | + `readoutGap`, `readoutsWidth`, `menuBarHit` |
| `menubar_readout_test.go` | 1 | + `TestReadoutsWidthAndHit` |
| `menubar_draw_darwin.go` | 1 | `drawReadout` → `readoutSlot` + `drawReadouts` |
| `menubar_draw_darwin_test.go` | 1 | three tests call `drawReadouts`; + two tests |
| `menubar_darwin.go` | 1 | one-item controller, `click`, `menu`, `menuBarClickAt`, `menuBarShowing` |
| `menubar_other.go` | 1 | stubs follow the new signatures |
| `menubar.go` | 1 | no layout; `menuBarItemID`; `SaveMenuBar`; + `MenuBarMenu` |
| `menubar_test.go` | 1 | layout checks go; the save test is rewritten |
| `growth_app.go` | 1 | the icon only without readings; `residentModelNow`; refresh for both |
| `growth_app_test.go` | 1 | + `TestResidentModelNow` |
| `src/mygo.ts` (generated) | 1 | `bun run generate` adds `Cleaner.menuBarMenu` |
| `src/panel/api.ts`, `src/panel/PanelApp.tsx`, `src/panel/panel.css` | 2 | the 「⋯」 button |
| `src/i18n.ts` | 2 | + `menubar.panel.more`; `settings.menubar.note` rewritten |
| `src/dev/mock-mygo.ts` | 2 | + `Cleaner.MenuBarMenu` |
| `tests/panel-react.test.ts` | 2 | footer markup test; the new string in the strings test |
| `README.md`, `CHANGELOG.md` | 2 | describe the one item and the menu |

---

### Task 1: Go — one item for all readings, carrying Cleanzo's menu

**Files:** as in the table above, Task 1 rows.

**Interfaces:**
- Consumes (exist today):
  - `menuBarText`, `menuBarTextFor`, `menuBarWidest`, `readoutWidth`, `readoutInk{template, dark, tone}.colors()`, `readoutFonts`, `textAttributes`, `bitmapPNG`, `nsString`, `nsSel`, `nsRect`, `nsSize`, `appKitInit`, `withPool`;
  - `sampleMenuBar`, `realMenuBarSource`, `unknownMenuBarReading`, `nativeText`;
  - `growthTrayMenu(model, act)`, `growthMenuAction`, `growthMenuCommands`, `menuBarOnlyMenu`, `residentMenu`, `growthMenuShown`, `growthMenuOn`, `growthTray`, `hideGrowthTray`, `showGrowthMenu`;
  - `menuBarHeld`, `menuBarRelease`.
- Produces:
  - `const readoutGap = 8.0`, `func readoutsWidth(widths []float64) float64`, `func menuBarHit(x float64, widths []float64) int` (untagged).
  - `type readoutSlot struct{ text menuBarText; width float64 }`, `func drawReadouts(slots []readoutSlot, template, dark bool) []byte` (darwin).
  - `func menuBarShow(items []string)`, `func menuBarShowing() bool`, `func menuBarRelabel()` (darwin and stubs).
  - `const menuBarItemID = "cleanzo"`, `func residentModelNow() []growthMenuItem`.
  - `func (Cleaner) MenuBarMenu(ctx context.Context, x, y int)`. Task 2 calls it as `Cleaner.menuBarMenu(x, y)`.

- [ ] **Step 1: The layout math, test first.** Append to `menubar_readout_test.go`:

```go
func TestReadoutsWidthAndHit(t *testing.T) {
	for _, tc := range []struct {
		widths []float64
		want   float64
	}{{nil, 0}, {[]float64{30}, 30}, {[]float64{30, 40}, 30 + readoutGap + 40}} {
		if got := readoutsWidth(tc.widths); got != tc.want {
			t.Errorf("readoutsWidth(%v) = %v, want %v", tc.widths, got, tc.want)
		}
	}
	two := []float64{30, 40}
	mid := 30 + readoutGap/2 // the gap splits at its middle
	for _, tc := range []struct {
		x    float64
		want int
	}{
		{-5, 0}, {0, 0}, {29, 0}, {mid - 0.1, 0},
		{mid, 1}, {50, 1}, {30 + readoutGap + 40, 1}, {500, 1},
	} {
		if got := menuBarHit(tc.x, two); got != tc.want {
			t.Errorf("menuBarHit(%v) = %d, want %d", tc.x, got, tc.want)
		}
	}
	if got := menuBarHit(12, []float64{30}); got != 0 {
		t.Errorf("one reading: %d", got)
	}
	if got := menuBarHit(12, nil); got != 0 {
		t.Errorf("no reading: %d", got)
	}
	if got := menuBarHit(30+readoutGap+40+readoutGap/2, []float64{30, 40, 20}); got != 2 {
		t.Errorf("third reading: %d", got)
	}
}
```

Run `go test -run TestReadoutsWidthAndHit .` and expect a compile failure: the names are undefined.

- [ ] **Step 2:** Append to `menubar_readout.go`:

```go
// readoutGap is the room between two readings in the readings' item, in
// points, on top of each reading's own pad: less than macOS leaves between two
// items, so that the readings read as one group (spec §三.1).
const readoutGap = 8.0

// readoutsWidth is the width of the readings' image for readings that keep
// widths, in points: side by side, readoutGap between two.
func readoutsWidth(widths []float64) float64 {
	w := 0.0
	for i, s := range widths {
		if i > 0 {
			w += readoutGap
		}
		w += s
	}
	return w
}

// menuBarHit is the reading a click at x, in points from the left edge of the
// readings' image, is on (spec §四): the one under it, the gap between two
// split at its middle, the first left of it and the last right of it. It is 0
// without readings.
func menuBarHit(x float64, widths []float64) int {
	right := 0.0
	for i, w := range widths {
		right += w
		if i == len(widths)-1 || x < right+readoutGap/2 {
			return i
		}
		right += readoutGap
	}
	return 0
}
```

Run the test again; it passes.

- [ ] **Step 3: Drawing several readings, tests first.** In `menubar_draw_darwin_test.go`, the three tests that call `drawReadout` call `drawReadouts` with one slot instead:
  - in `TestDrawReadoutKeepsTheLinesApart`, `drawReadout(text, w, readoutInk{template: true})` becomes `drawReadouts([]readoutSlot{{text, w}}, true, false)`;
  - in `TestDrawReadoutTemplateIsBlack`, `drawReadout(text, w, readoutInk{template: true})` becomes `drawReadouts([]readoutSlot{{text, w}}, true, false)`;
  - in `TestDrawReadoutColorsTheValueOnADarkBar`, `drawReadout(text, w, readoutInk{dark: true, tone: "warn"})` becomes `drawReadouts([]readoutSlot{{text, w}}, false, true)`. The text already has `Tone: "warn"`.

Then append:

```go
// inkIn tells whether any pixel in columns [x0, x1) has more than
// antialiasing's faint edge.
func inkIn(img image.Image, x0, x1 int) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X + x0; x < b.Min.X+x1 && x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0x2000 {
				return true
			}
		}
	}
	return false
}

func TestDrawReadoutsPutsEachReadingInItsSlot(t *testing.T) {
	if !appKitInit() {
		t.Skip("AppKit is not available")
	}
	cpu := menuBarText{Top: "CPU", Bottom: "100%", Label: true}
	mem := menuBarText{Top: "内存", Bottom: "86%", Label: true}
	w1, w2 := readoutWidth(cpu), readoutWidth(mem)
	total := readoutsWidth([]float64{w1, w2})
	img := decodeReadout(t, drawReadouts([]readoutSlot{{cpu, w1}, {mem, w2}}, true, false), total)
	px := func(p float64) int { return int(math.Round(p * readoutScale)) }
	if !inkIn(img, 0, px(w1)) || !inkIn(img, px(w1+readoutGap), px(total)) {
		t.Error("a reading drew nothing in its slot")
	}
	if inkIn(img, px(w1), px(w1+readoutGap)) {
		t.Errorf("ink in the gap, pixel columns %d–%d", px(w1), px(w1+readoutGap))
	}
}

func TestDrawReadoutsColorsOnlyTheTonedValue(t *testing.T) {
	if !appKitInit() {
		t.Skip("AppKit is not available")
	}
	cpu := menuBarText{Top: "CPU", Bottom: "43%", Label: true}
	mem := menuBarText{Top: "MEM", Bottom: "86%", Label: true, Tone: "warn"}
	w1, w2 := readoutWidth(cpu), readoutWidth(mem)
	img := decodeReadout(t, drawReadouts([]readoutSlot{{cpu, w1}, {mem, w2}}, false, true), readoutsWidth([]float64{w1, w2}))
	split := int(math.Round((w1 + readoutGap/2) * readoutScale))
	find := func(x0, x1 int, match func(color.NRGBA) bool) bool {
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X + x0; x < b.Min.X+x1 && x < b.Max.X; x++ {
				if match(color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)) {
					return true
				}
			}
		}
		return false
	}
	orange := func(c color.NRGBA) bool { return c.A > 200 && c.R > 220 && c.G > 150 && c.G < 230 && c.B < 140 } // #FFC35A
	white := func(c color.NRGBA) bool { return c.A > 200 && c.R > 220 && c.G > 220 && c.B > 220 }
	if find(0, split, orange) || !find(0, split, white) {
		t.Error("CPU's value: want the dark bar's light ink, not orange")
	}
	if !find(split, img.Bounds().Dx(), orange) {
		t.Error("memory's value is not orange")
	}
}
```

Run `go test -run 'TestDrawReadout' .`; it fails to compile (`drawReadouts` and `readoutSlot` are undefined).

- [ ] **Step 4:** In `menubar_draw_darwin.go`, replace the whole `drawReadout` function, with its doc comment, by:

```go
// readoutSlot is one reading in the readings' image: its two lines and the
// room it keeps, in points.
type readoutSlot struct {
	text  menuBarText
	width float64
}

// drawReadouts draws slots into one image, left to right with readoutGap
// between two, 18 points tall at twice that in pixels, and encodes it as PNG;
// nil when it cannot. A template draws every reading black, for macOS to tint;
// otherwise each reading is in the menu bar's ink, light on a dark bar, with
// its value in its tone. Each line sits on a fixed baseline, so a label in
// another script, or a value without descenders, sits where every other does.
// It locks the calling goroutine to its thread for the autorelease pool and
// the current graphics context.
func drawReadouts(slots []readoutSlot, template, dark bool) []byte {
	widths := make([]float64, len(slots))
	for i, s := range slots {
		if s.width <= 0 {
			return nil
		}
		widths[i] = s.width
	}
	w := readoutsWidth(widths)
	if w <= 0 || !appKitInit() {
		return nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var out []byte
	withPool(func() {
		fonts := make([][2]objc.ID, len(slots))
		for i, s := range slots {
			top, bottom := readoutFonts(s.text)
			if top == 0 || bottom == 0 {
				return
			}
			fonts[i] = [2]objc.ID{top, bottom}
		}
		rep := objc.ID(objc.GetClass("NSBitmapImageRep")).Send(nsSel("alloc")).Send(
			nsSel("initWithBitmapDataPlanes:pixelsWide:pixelsHigh:bitsPerSample:samplesPerPixel:hasAlpha:isPlanar:colorSpaceName:bytesPerRow:bitsPerPixel:"),
			uintptr(0), int(math.Round(w*readoutScale)), int(readoutHeight*readoutScale), 8, 4, true, false, nsString("NSDeviceRGBColorSpace"), 0, 0)
		if rep == 0 {
			return
		}
		defer rep.Send(nsSel("release"))
		rep.Send(nsSel("setSize:"), nsSize{w, readoutHeight}) // draw in points
		ctx := objc.ID(objc.GetClass("NSGraphicsContext")).Send(nsSel("graphicsContextWithBitmapImageRep:"), rep)
		if ctx == 0 {
			return
		}
		gc := objc.ID(objc.GetClass("NSGraphicsContext"))
		gc.Send(nsSel("saveGraphicsState"))
		gc.Send(nsSel("setCurrentContext:"), ctx)
		x := 0.0
		for i, s := range slots {
			t := s.text
			label, value := readoutInk{template: template, dark: dark, tone: t.Tone}.colors()
			topColor, topBase, bottomBase := value, readoutDuoTop, readoutDuoBottom
			if t.Label {
				topColor, topBase, bottomBase = label, readoutLabelBase, readoutValueBase
			}
			line := func(str string, font objc.ID, c rgba, base float64) {
				if str == "" {
					return
				}
				// Without NSStringDrawingUsesLineFragmentOrigin, the rectangle's
				// origin is the line's baseline; no drawing context is needed.
				nsString(str).Send(nsSel("drawWithRect:options:attributes:context:"),
					nsRect{x + readoutPad, base, s.width, readoutHeight}, uint64(0), textAttributes(font, c), objc.ID(0))
			}
			line(t.Top, fonts[i][0], topColor, topBase)
			line(t.Bottom, fonts[i][1], value, bottomBase)
			x += s.width + readoutGap
		}
		gc.Send(nsSel("restoreGraphicsState"))
		out = bitmapPNG(rep)
	})
	return out
}
```

Reword three comments in this file to talk about readings instead of icons:
- the file's header: `draws the readings' two lines into the one image their menu bar item shows`;
- `readoutInk`: `says how a reading is colored`;
- `readoutWidth`: `the width in points a reading needs for t`.

Run `go test -run 'TestDrawReadout|TestReadoutWidth' .`; all pass.

- [ ] **Step 5: Settings without a layout, tests first.** In `menubar_test.go`:
  - In `TestMenuBarConfigDefaultsAndNormalizes`, drop `|| c.Layout != 0` and `|| c.Layout != 3` from the two conditions, and change `menuBarConfig{Items: []string{}, Layout: 3}` to `menuBarConfig{Items: []string{}}`. Adjust their messages if they mention the layout.
  - Delete `TestMenuBarNames`.
  - Replace `TestSaveMenuBarRenamesTheIconsOnlyWhenTheReadingsChange` with:

```go
func TestSaveMenuBarKeepsAndAppliesTheReadings(t *testing.T) {
	if !menuBarSupported() {
		t.Skip("the menu bar readings are macOS only")
	}
	now, size := t0, 10*gb
	var sent []GrowthAlert
	stubGrowth(t, &now, &size, &sent)
	withBattery(t, true)
	applied := stubResident(t)
	save := func(items ...string) menuBarConfig {
		t.Helper()
		if err := (Cleaner{}).SaveMenuBar(items); err != nil {
			t.Fatal(err)
		}
		return loadMenuBarConfig(menuBarPath(), true)
	}
	for _, tc := range []struct {
		save []string
		want []string
	}{
		{[]string{"mem", "cpu", "cpu", "bogus"}, []string{"cpu", "mem"}},
		{[]string{"gpu", "cpu"}, []string{"cpu", "gpu"}},
		{nil, []string{}},
		{[]string{"temp"}, []string{"temp"}},
	} {
		if c := save(tc.save...); !slices.Equal(c.Items, tc.want) {
			t.Errorf("save %v: kept %v, want %v", tc.save, c.Items, tc.want)
		}
	}
	if len(*applied) != 4 || !slices.Equal((*applied)[3].Items, []string{"temp"}) {
		t.Errorf("applied %+v", *applied)
	}
	if got := (Cleaner{}).MenuBar(); !got.Supported || !slices.Equal(got.Items, []string{"temp"}) || !slices.Contains(got.Available, "battery") {
		t.Errorf("MenuBar() = %+v", got)
	}
}
```

Append to `growth_app_test.go`:

```go
// The readings' item shows the menu last built for the icon or for itself;
// before one is built, the lines that act for the monitoring state applied.
func TestResidentModelNow(t *testing.T) {
	shown, on := growthMenuShown, growthMenuOn
	t.Cleanup(func() { growthMenuShown, growthMenuOn = shown, on })
	growthMenuShown, growthMenuOn = nil, false
	if got, want := residentModelNow(), menuBarOnlyMenu(nativeText); !slices.Equal(got, want) {
		t.Errorf("readings only: %+v, want %+v", got, want)
	}
	growthMenuOn = true
	if got, want := residentModelNow(), growthMenuCommands(false, nativeText); !slices.Equal(got, want) {
		t.Errorf("monitoring on, nothing built: %+v, want %+v", got, want)
	}
	built := growthMenuModel(nil, true, labelsOnly)
	growthMenuShown = built
	if got := residentModelNow(); !slices.Equal(got, built) {
		t.Errorf("built: %+v, want %+v", got, built)
	}
}
```

- [ ] **Step 6: `menubar.go`.**
  - The package comment's last sentence becomes: `menubar_readout.go turns readings into text; menubar_darwin.go makes the readings' item and feeds it; elsewhere the readings are not supported.`
  - `menuBarConfig` loses `Layout`:

```go
// menuBarConfig is what menubar.json keeps: the readings shown.
type menuBarConfig struct {
	Items []string `json:"items"`
}
```

  - Replace `menuBarIconName` and `menuBarItemName`, with their comments, by:

```go
// menuBarItemID names Cleanzo's place in the menu bar: the readings' item, or
// the Cleanzo icon while monitoring is on and no reading shows (spec §三.4).
// The two never show together, so they share it: each comes where the other
// was, or where the user dragged it with ⌘.
const menuBarItemID = "cleanzo"
```

  - In `SaveMenuBar`, delete `cur := loadMenuBarConfig(menuBarPath(), battery)` and the `if len(next.Items) > 0 && …` bump. `next` becomes `next := menuBarConfig{Items: normalizeMenuBarItems(items, battery)}`.
  - Add after `MenuBarOpen`:

```go
// MenuBarMenu shows Cleanzo's menu at x, y in the panel that calls it, in
// points from the top left of its page: the lines of the Cleanzo icon, which
// does not show while a reading does (spec §三.4). mygo runs the line chosen
// before the menu returns; a line chosen then closes the panel, held or not.
func (Cleaner) MenuBarMenu(ctx context.Context, x, y int) {
	if !menuBarSupported() {
		return
	}
	w := mygo.CallerWindow(ctx)
	if w == nil || !w.IsFlyout() {
		return
	}
	var menu *mygo.Menu
	chosen := false
	mygo.RunOnMain(func() { // the model is the main thread's
		menu = growthTrayMenu(residentModelNow(), func(it growthMenuItem) {
			chosen = true
			growthMenuAction(it)
		})
	})
	menu.PopupAt(w, x, y)
	if chosen {
		menuBarRelease(w)
		w.Destroy()
	}
}
```

- [ ] **Step 7: `growth_app.go`.**
  - `growthTray`'s comment:

```go
// growthTray is the Cleanzo icon in the menu bar, shown while monitoring is on
// and no reading shows: with readings, their item carries Cleanzo's menu
// instead (menubar spec §三.4). The main thread makes and removes it.
```

  - `growthMenuShown`'s comment:

```go
// growthMenuShown is the model of Cleanzo's menu as last built: the icon's
// menu, or what the readings' item shows on a right-click and in its panels.
// Only the main thread reads or writes it.
```

  - In `startResident`, the call becomes `menuBarShow(mb.Items) // first: with a reading, its item carries the menu and no icon shows`. The at-login test becomes `if atLogin && growthTray.Load() == nil && !menuBarShowing() {`.
  - In its doc comment, "the readings chosen, then the Cleanzo icon while monitoring is on or a reading shows" becomes "the readings chosen, in one item, or else the Cleanzo icon while monitoring is on". "the icon is there" becomes "Cleanzo shows in the menu bar". "Without the icon nothing would show" becomes "Without either, nothing would show".
  - In `applyResident`, the call becomes `menuBarShow(mb.Items)`. Its first two bullets become:

```go
//   - the readings in mb, in one item, which carries Cleanzo's menu;
//   - the Cleanzo icon while monitoring is on and no reading shows;
```

  - Replace `showGrowthTray` with:

```go
// showGrowthTray shows Cleanzo's menu for model (menubar spec §三.4). While a
// reading shows, the readings' item carries it, on a right-click and in its
// panels, and the Cleanzo icon goes. Otherwise it goes on the icon, made if
// missing, with the tooltip tip. It runs on the main thread.
func showGrowthTray(model []growthMenuItem, tip string) {
	if menuBarShowing() {
		hideGrowthTray()
		growthMenuShown = slices.Clone(model)
		return
	}
	if t := growthTray.Load(); t != nil {
		t.SetToolTip(tip)
		showGrowthMenu(t, model)
		return
	}
	t, err := mygo.NewTray(mygo.TrayOptions{ID: menuBarItemID, Icon: growthIconPNG(), IconIsTemplate: true, ToolTip: tip, Menu: growthTrayMenu(model, growthMenuAction)})
	if err != nil {
		log.Println("tray:", err)
		return
	}
	growthTray.Store(t)
	growthMenuShown = slices.Clone(model)
}
```

  - Replace `refreshGrowthTray` with:

```go
// refreshGrowthTray rebuilds Cleanzo's menu while Cleanzo is in the menu bar,
// and the icon's tooltip; both follow the language. The icon's menu is only
// replaced when it changed. A menu built for the other monitoring state than
// the one last applied is dropped.
func refreshGrowthTray() {
	on := loadGrowthConfig(growthDir()).Enabled
	if !on && !menuBarOn() {
		return
	}
	model, tip := residentMenu(on)
	mygo.RunOnMain(func() {
		if on != growthMenuOn {
			return
		}
		if t := growthTray.Load(); t != nil {
			t.SetToolTip(tip)
			showGrowthMenu(t, model)
		} else if menuBarShowing() {
			growthMenuShown = slices.Clone(model)
		}
	})
}

// residentModelNow is Cleanzo's menu as last built, for the readings' item to
// show. Before one is built, it is the lines that act, as startResident first
// shows them. It runs on the main thread.
func residentModelNow() []growthMenuItem {
	if growthMenuShown != nil {
		return growthMenuShown
	}
	if growthMenuOn {
		return growthMenuCommands(false, nativeText)
	}
	return menuBarOnlyMenu(nativeText)
}
```

  - Doc comments that say "the Cleanzo icon's menu" now say "Cleanzo's menu":
    - `residentMenu`: "is Cleanzo's menu and the icon's tooltip";
    - `menuBarOnlyMenu`: "is Cleanzo's menu while monitoring is off and a reading shows";
    - `menuBarMenuTail`: "ends every menu of Cleanzo".

- [ ] **Step 8: `menubar_other.go`.** Its stubs become:

```go
func menuBarSupported() bool  { return false }
func menuBarHasBattery() bool { return false }
func menuBarShow([]string)    {}
func menuBarShowing() bool    { return false }
func menuBarRelabel()         {}
```

Its comment's "and the Cleanzo icon keeps its first name" is dropped.

- [ ] **Step 9: the controller, `menubar_darwin.go`.**
  - Imports gain `"strings"` and `"github.com/ebitengine/purego/objc"`.
  - Replace everything from `// menuBarController is the readings' icons.` down to the end of `relabel` with the code below. The `menuBarPanelWidth`/`menuBarPanelMaxHeight` constants stay where they are, after it.

```go
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
// panel first, held or not: the user asked for something else. The item has
// no menu of its own, so that a click reaches OnClick: it is given one only
// while it shows it.
func (m *menuBarController) menu() {
	t := m.tray
	if t == nil {
		return
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
// the item's window less the room macOS leaves on each side of the image,
// which it centers. control tells whether Control was held. itemWidth is the
// item's width, imageWidth the image's. ok is false when the event being
// handled is no click, as for VoiceOver's press. It runs on the main thread,
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
		p := objc.Send[nsPoint](ev, nsSel("locationInWindow"))
		x = p.X - (itemWidth-imageWidth)/2
		control = objc.Send[uint64](ev, nsSel("modifierFlags"))&(1<<18) != 0 // NSEventModifierFlagControl
		ok = true
	})
	return x, control, ok
}
```

  - In `toggle`, the doc comment's first sentence becomes `toggle opens item's panel below the readings' item and closes any other; a click on that reading while its panel is open closes it (spec §四).` The line `t := m.trays[item]` becomes `t := m.tray`.
  - `closePanels`' comment becomes `closePanels closes every open panel, held or not, as before the item goes or its menu shows.`

- [ ] **Step 10: bindings.** Run `bun run generate`. The generated bindings gain `menuBarMenu(x: number, y: number): Promise<void>` on `Cleaner`. Keep the generated changes.

- [ ] **Step 11: verify.** All of these must pass:
  - `gofmt -l .` prints nothing;
  - `go vet ./...` and `GOOS=windows GOARCH=amd64 go vet ./...`;
  - `go test -count=1 -race -run 'TestMenuBar|TestReadout|TestDrawReadout|TestSaveMenuBar|TestResident|TestGrowth|TestShowGrowthMenu|TestApplyGrowthLifecycle|TestWatchMenuBarPanel' .`;
  - `bun run typecheck`.

  In a sandbox, the AppKit drawing tests may skip. Say so in the report if they do.

- [ ] **Step 12: report.** Don't commit. Write the report to the path the dispatch gives. List each file you changed, every command you ran with its result, and any deviation with its reason.

### Task 2: Web and docs — the panel's 「⋯」 and the copy

**Files:** as in the table above, Task 2 rows.

**Interfaces:**
- Consumes: `Cleaner.menuBarMenu(x: number, y: number): Promise<void>`, from Task 1's generated bindings.
- Produces: `export function PanelFoot(props: PanelFootProps)` in `src/panel/PanelApp.tsx`, where

```ts
export interface PanelFootProps { links: PanelLink[]; t: T; onOpen: (target: MenuBarTarget) => void; onMenu: (x: number, y: number) => void }
```

- [ ] **Step 1: test first.** In `tests/panel-react.test.ts`:
  - Add `"menubar.panel.more"` to the `keys` of `panel strings exist in Chinese and English`.
  - Append:

```ts
test("footer: the panel's links, the update interval and the 「⋯」 that opens Cleanzo's menu", async () => {
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { PanelFoot } = await import("../src/panel/PanelApp");
  const { PANEL } = await import("../src/panel/format");
  const html = renderToStaticMarkup(createElement(PanelFoot, { links: PANEL.cpu.links, t, onOpen() {}, onMenu() {} }));
  expect(html).toContain(t("menubar.panel.openProcess"));
  expect(html).toContain(t("menubar.panel.every"));
  expect(html).toContain('class="mb-more"');
  expect(html).toContain(`aria-label="${t("menubar.panel.more")}"`);
});
```

Run `bun test tests/panel-react.test.ts`; the new test fails (`PanelFoot` is not exported) and so does the strings test.

- [ ] **Step 2: strings.** In `src/i18n.ts`, after `"menubar.panel.every"` in each language:
  - Chinese: `"menubar.panel.more": "Cleanzo 菜单",`
  - English: `"menubar.panel.more": "Cleanzo menu",`

  Rewrite `settings.menubar.note`:
  - Chinese: `"勾选的读数一起显示在菜单栏的同一项里，点一个读数看它的详情和占用最多的 App；右键点读数，或者点面板里的「⋯」，打开 Cleanzo 的菜单。有读数时，关掉窗口 Cleanzo 会留在菜单栏。"`
  - English: `"Checked readings show together in one menu bar item: click a reading for its details and the apps using the most; right-click the readings, or click ⋯ in a panel, for Cleanzo's menu. While any shows, closing the window keeps Cleanzo in the menu bar."`

- [ ] **Step 3: the API and the footer.**
  - `src/panel/api.ts` gains, after `open`:

```ts
  /** 在面板里 x、y 处（页面左上角起的点）弹出 Cleanzo 的菜单；选了一行，面板随后关上。 */
  menu: (x: number, y: number): Promise<void> => Cleaner.menuBarMenu(x, y),
```

  - In `src/panel/PanelApp.tsx`, add the component before `PanelApp`. `PanelLink` is imported from `./format` and `MenuBarTarget` from `../mygo`, if they aren't already.

```tsx
export interface PanelFootProps { links: PanelLink[]; t: T; onOpen: (target: MenuBarTarget) => void; onMenu: (x: number, y: number) => void }

/** 面板底部：去主窗口的链接，更新间隔，和打开 Cleanzo 菜单的「⋯」（有读数时菜单栏里没有 Cleanzo 图标，设计 §三.4）。 */
export function PanelFoot({ links, t, onOpen, onMenu }: PanelFootProps) {
  return (
    <footer className="mb-foot">
      <span className="mb-links">
        {links.map((l) => (
          <button key={l.key} type="button" className="mb-link" onClick={() => onOpen(l.target)}>{t(l.key)} ›</button>
        ))}
      </span>
      <span className="mb-end">
        <span className="mb-every">{t("menubar.panel.every")}</span>
        <button
          type="button"
          className="mb-more"
          aria-label={t("menubar.panel.more")}
          title={t("menubar.panel.more")}
          onClick={(e) => {
            const r = e.currentTarget.getBoundingClientRect();
            onMenu(Math.round(r.left), Math.round(r.bottom));
          }}
        >
          ⋯
        </button>
      </span>
    </footer>
  );
}
```

  - In `PanelApp`, replace the whole `<footer className="mb-foot">…</footer>` with:

```tsx
      <PanelFoot links={PANEL[item].links} t={t} onOpen={open} onMenu={(x, y) => { void panelApi.menu(x, y).catch(() => {}); }} />
```

  - `src/panel/panel.css`, after `.mb-every`:

```css
.mb-end { display: flex; align-items: center; gap: 6px; }
.mb-more { padding: 0 5px; border: 0; border-radius: 5px; background: none; font: inherit; font-size: 15px; line-height: 18px; color: var(--muted); cursor: pointer; }
.mb-more:hover, .mb-more:focus-visible { background: var(--hover); color: var(--fg); }
```

  - `src/dev/mock-mygo.ts`: after `"Cleaner.MenuBarOpen": async () => {},` add `"Cleaner.MenuBarMenu": async () => {},`.

- [ ] **Step 4: README and CHANGELOG.**
  - In `README.md`, replace `芯片温度和电池电量。点开任一读数是它的详情` with `芯片温度和电池电量。读数都画在菜单栏的同一项里，顺序固定；有读数时不再单独显示 Cleanzo 图标，右键点读数或点面板里的「⋯」是 Cleanzo 的菜单（打开窗口、菜单栏设置、退出）。点开任一读数是它的详情`.
  - In `CHANGELOG.md`, in the first bullet of 「未发布」, replace `菜单栏默认显示 CPU、内存两个读数，` with `菜单栏默认显示 CPU、内存两个读数，所有读数画在同一项里、顺序固定，`.
  - In the third bullet, replace `有读数时关掉窗口 Cleanzo 留在菜单栏；Cleanzo 图标的菜单新增「菜单栏设置…」。` with `有读数时关掉窗口 Cleanzo 留在菜单栏，菜单栏里不再另外显示 Cleanzo 图标：右键点读数或点面板里的「⋯」打开 Cleanzo 的菜单，菜单新增「打开 Cleanzo」「菜单栏设置…」「退出」。`

- [ ] **Step 5: verify.** All of these must pass:
  - `bun run typecheck`;
  - `bun test tests/panel-react.test.ts`;
  - `bun test` (the whole web suite);
  - `bun run build:web`, if the repository has it; otherwise `bunx vite build`. It must succeed.

- [ ] **Step 6: report.** Don't commit. Write the report to the path the dispatch gives.

## Reviewer verification (on the maintainer's Mac)

1. Build: `bun run build -- -skip-notarize -skip-dmg`. Launch the test build with `open -g`.
2. One item:
   - The menu bar shows one item with CPU and 内存 side by side, and no Cleanzo icon.
   - Turning GPU on in Settings widens the item in place.
   - Turning all readings off with monitoring on brings the Cleanzo icon back where the item was.
3. Clicks. A real click needs the maintainer, because AXPress has no position:
   - CPU's half opens the CPU panel and 内存's half the memory panel.
   - The other reading's half switches panels; the same half again closes the panel.
   - Right-click and Control-click show Cleanzo's menu below the item.
4. 「⋯」:
   - AXPress on the panel's 「⋯」 shows the menu inside the panel.
   - Choosing 「打开 Cleanzo」 opens the window and closes the panel.
5. Two displays: the item, its panels and its menu appear on the display that was clicked.

## Execution

| Task | Executor | Notes |
|------|----------|-------|
| 1 | traecli | Go; the reviewer re-runs the AppKit drawing tests outside the sandbox. |
| 2 | traecli | Web; needs Task 1's generated bindings in the tree. |
