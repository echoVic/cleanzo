# Native Menu Bar Panels — Implementation Plan

> **For agentic workers:** In this project the tasks are executed by traecli and the Codex CLI. **Executors do not commit**: the reviewer checks and commits each task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** The seven menu bar panels are drawn with mygo's native UI (package `ui`) instead of a web page.
- A panel opens complete at its size, with no flash and no growing, and starts no web process.
- It shows what today's web panel shows, in Cleanzo's own colors (the main window's tokens), light or dark as the app is.
- The web panel and the bound methods only it used are removed.

**Why (the maintainer, 2026-10-30):**
- 「为什么打开面会闪一下，而且性能好像一般。… 原生组件可以用到吗」
- 「菜单栏下拉框的颜色应该跟着cleanzo 主应用的主题色吧」
- The maintainer chose to finish native panels before releasing 0.3.0.
- **The spike:** a native CPU panel in a throwaway build. It opened at full size every time; the web panel grew from 290 to 569 points high over 1.8 s.
  - The spike's code is `.superpowers/sdd/2026-10-30-menubar-native-panels/spike-native-cpu-panel.go.txt`.
  - Its offscreen renders are `spike-preview-sheet.png` in the same folder.

**Architecture:**
- **Task 1 (pure Go):** `menubar_panel.go` ports the panel's rules from `src/panel/format.ts`, and the formatting helpers they use from the web, with their tests. The panel's strings move into `src/native-i18n.json`, and a bun test keeps them equal to the main window's.
- **Task 2 (darwin):** `menubar_panel_darwin.go` draws each panel natively and feeds it straight from the Go readers. It also holds the panel during a quit, links to the main window, shows 「⋯」 and warms the renderer at launch. `toggle` opens it in the system popover with `Content` instead of `URL`.
- **Task 3 (cleanup):** remove `panel.html`, `src/panel/`, the panel's Vite entry, its web tests and strings, and the bound methods `WatchMenuBarPanel`, `MenuBarHold`, `MenuBarOpen` and `MenuBarMenu`.

**Tech Stack:** Go 1.27.3, mygo 0.7.1. The packages are `ui`, `ui/chart`, `ui.NewTester` and `ui.Render`. The flyout uses `FlyoutOptions.Content` with `Popover: true`. No new dependencies, but importing `ui` makes Go list two indirect requirements of mygo in `go.mod`: `github.com/go-text/typesetting v0.3.5` and `golang.org/x/image v0.46.0`. Both are already in `go.sum` and the module cache.

**Spec:** `docs/superpowers/specs/2026-10-29-menubar-design.md`: §四 (the panels and their table), §四.5 (quitting from a panel), §六 (cost), §十 (native panel details). Earlier plans:
- `2026-10-29-menubar.md` (Task 4 built the web panel);
- `2026-10-30-menubar-independent-items.md` (the items as they are now).

## Global Constraints

- **macOS only.** Windows has no panels. Every new darwin function has a stub in `menubar_other.go` where an untagged file calls it, and `GOOS=windows go vet ./...` stays clean.
- **Repository rules:**
  - exported Go names only in untagged files;
  - no `/` path string literals in untagged non-test `.go` files;
  - comments say what the code does, in the surrounding voice, wrapped at 80 columns.
  - User-facing text is in Chinese and English: the native panel's in `src/native-i18n.json`, the same words as the main window's.
- **Same content as today.** Each panel shows exactly what the web panel shows (spec §四's table; `src/panel/format.ts` and `PanelApp.tsx` are the behavior to match):
  - the name, with the pressure badge for memory, and the big number;
  - the sub-line;
  - the one-line diagnosis: a placeholder until 3 readings, an error line if reading failed;
  - the 30-point chart, colored by tone;
  - the memory panel's stacked bar;
  - the detail rows, with dot, bar and value;
  - for CPU and memory, the top 5 rows of the 进程 page's list;
  - for disk, the top 5 growing apps while monitoring is on;
  - the footer links, the update interval and 「⋯」.
- **The quit flow is unchanged** (spec §四.5):
  - 「退出」 shows on hover; one click turns it into 「确认退出」, which falls back after 3 s or when the pointer leaves the list.
  - 「还没退出」 offers 「强制退出」, also in two clicks.
  - The list keeps its rows and order while the pointer is in it, a confirmation is open, or a quit is pending.
  - The panel stays open while a quit is pending, for 15 s at most (`menuBarHoldMax`).
  - Clicking a name opens the 进程 page at that row.
- **Colors:** Cleanzo's tokens from `src/style.css`, light and dark. "Dark" is the app's appearance (`NSApp.effectiveAppearance`), as the main window's; it is not the menu bar's.
- **Cost** (spec §六): while a panel is open, ≤ 3% of one core for the app and its children, with no web process. Panels read only while open, and the native renderer adds nothing while no panel is open.
- **Never send a message returning `NSRect` through `objc.Send`** (amd64 needs `objc_msgSend_stret`).

## Decisions this plan makes (reviewers, please check)

- **Fixed heights.** mygo 0.7.1 can't size a flyout of native content to that content. Each item gets a height measured from its own layout, so its content fills it without a gap at the bottom:
  - `cpu` and `mem` include the 5-row list;
  - `disk` includes the growth list;
  - the others have no list.

  A missing row leaves room above the footer, which stays at the bottom. The panel never resizes after it opens.
- **The popover for now.** The panels stay in the system popover (`Popover: true`) until mygo places plain flyouts under the copy clicked (the PR from today). The native view draws its own opaque card with rounded corners; the popover's arrow keeps the popover's material.
- **Icons:** `appIconRender(path, 36)` runs on a background worker. The result is cached for the app's lifetime (shared by all panels), keyed by bundle path, including failures. A row shows its initial on `iconColor`'s hue until its icon arrives.
- **Warming:** the spike's first open after launch took 2.9 s (later ones 0.5 s). The app warms the native renderer once at launch, in the background, when a reading shows. Task 2 measures what the 2.9 s is and warms that.
- **Growth updates to the disk panel:** `growthChanged` (`growth_monitor.go`) also calls `menuBarGrowthChanged()`, which reloads the open disk panel's growth state. It is a stub off macOS.
- **The hold** reuses `menuBarHolds`: the panel holds itself through Go (`menuBarHoldPanel(w, on)`), not through a bound method.

## File Structure

| File | Task | Change |
|------|------|--------|
| `menubar_panel.go` | 1 | new: the panel's rules and formatting (untagged, pure) |
| `menubar_panel_test.go` | 1 | new: their tests, ported from `tests/panel-react.test.ts` plus format parity tables |
| `src/native-i18n.json` | 1 | the panel's strings, Chinese and English |
| `tests/native-i18n.test.ts` | 1 | new: native strings equal the main window's for the same keys |
| `menubar_panel_darwin.go` | 2 | new: native views, data, quitting, icons, theme, warming |
| `menubar_panel_darwin_test.go` | 2 | new: view tests with `ui.NewTester` |
| `menubar_darwin.go` | 2 | `toggle` opens native content; hold through Go |
| `menubar_other.go`, `growth_monitor.go`, `growth_app.go`, `go.mod` | 2 | stubs, the growth hook, warming at launch, the two indirect requirements |
| `menubar.go`, `menubar_test.go` | 3 | remove `WatchMenuBarPanel`, `MenuBarHold`, `MenuBarOpen`, `MenuBarMenu` and their tests |
| `panel.html`, `src/panel/`, `tests/panel-react.test.ts`, `vite.config.ts`, `src/dev/mock-mygo.ts`, `src/i18n.ts`, `src/mygo.ts` (generated) | 3 | the web panel goes |
| `CHANGELOG.md`, `README.md` | 3 | say the panels are native and open at once |

---

### Task 1: Pure Go — the panel's rules, formatting and strings

**Files:** `menubar_panel.go`, `menubar_panel_test.go`, `src/native-i18n.json`, `tests/native-i18n.test.ts`

**Interfaces:**
- **Consumes:** the Go types the web panel's types are generated from: `StatusSample`, `MemoryStatus`, `CPUStatus`, `DiskStat`, `BatteryStatus`, `MachineSample`, `MachineInfo`, `DisplayInfo`, the SSD and SMART types, `SystemInfo`, `ProcessSample`, `ProcessRow`, `GrowthState`, `GrowthApp`, `GrowthAlert`, `MenuBarTarget`. Also `growthSize` (`growth_monitor.go`), which matches the web's `human()`.
- **Produces** (untagged, unexported). In the names below:
  - `item` is one of `menuBarItems`;
  - `by` is `"cpu"` or `"mem"`;
  - `t` is `func(key string, vars map[string]string) string`, which is `nativeText`, or a stand-in in tests.

```go
type panelText = func(key string, vars map[string]string) string

const panelTop = 5                         // list rows (PANEL_TOP)
const panelQuitConfirm = 3 * time.Second   // QUIT_CONFIRM_MS
const panelDiskSparkFloor = 1_000_000      // DISK_SPARK_FLOOR
const panelSparkPoints = 30                // SPARK_POINTS
const panelDiagSamples, panelBusy, panelIdleBelow = 3, 80.0, 50.0 // PROCESS_DIAG, IDLE_BELOW

// formatting, each ported from the web function named in its comment
func panelMem(n float64) string                     // fmtMem      src/react/status/format.ts:24
func panelSplitMem(n float64) (value, unit string)  // splitMem    :38
func panelPct(v float64) string                     // fmtPct      :33
func panelRate(bps float64) (value, unit string)    // fmtRate     :223
func panelRateText(bps float64) string              // fmtRateText :231
func panelTemp(c float64) string                    // fmtTemp     :237
func panelDec(n float64) string                     // fmtDec      :242
func panelNetMax(hist []float64) float64            // netSparkMax :274
func panelDisplayText(d DisplayInfo, fallback string) string // displayText :264
func panelSSDWarnings(bits int) []string            // ssdWarnings :257, i18n keys
func panelIconHue(name string) float64              // the hue of iconColor :139
func panelSortRows(rows []ProcessRow, by string) []ProcessRow // sortRows src/react/process/format.ts:29
func panelEst(text string, estimated bool) string   // est :71
func panelRefreshFrozen(frozen []ProcessRow, s ProcessSample) []ProcessRow // refreshFrozen :39
func panelPush(hist []float64, v float64, keep int) []float64 // pushCpu (keep 3) and trimSpark (keep 30)
func panelGrowthHint(g *GrowthState) *GrowthAlert   // growthHint src/react/growth/hint.ts:5, no crash apps

// the panel, ported from src/panel/format.ts
type panelLink struct{ Key string; Target MenuBarTarget }
func panelTitle(item string) string                 // PANEL[item].title (an i18n key)
func panelLinks(item string) []panelLink            // PANEL[item].links
func panelHero(item string, s *StatusSample) (value, unit string)
func panelSubLine(item string, s *StatusSample, info *MachineInfo, sys *SystemInfo, t panelText) string
type panelDiag struct{ Tone, Text string }          // Tone: "ok", "warn" or "crit"
type panelDiagInput struct {
	S       *StatusSample
	Proc    *ProcessSample
	CPUHist []float64
	GPUHist []float64
	Growth  *GrowthState
}
func panelDiagnosis(item string, d panelDiagInput, t panelText) *panelDiag
func panelChartPoint(item string, s StatusSample) (float64, bool)
func panelChartMax(item string, hist []float64) float64
func panelChartTone(item string, s *StatusSample) string // "accent", "gpu", "net", "warn" or "crit"
type panelDetailRow struct {
	Key, Label, Value string
	Bar               float64 // 0–100, when HasBar
	HasBar            bool
	Dot               string  // "", "app", "wired" or "compressed"
}
func panelDetailRows(item string, s *StatusSample, info *MachineInfo, t panelText) []panelDetailRow
func panelMemShares(m MemoryStatus) (app, wired, compressed float64, ok bool)
func panelTopRows(s ProcessSample, by string, prev []ProcessRow, frozen bool) []ProcessRow
func panelGrowthTop(g *GrowthState) []GrowthApp

// quitting from a panel (spec §四.5)
type panelQuitStep string // "idle", "confirm", "quitting", "stuck", "forceConfirm", "forcing", "done", "failed"
type panelQuitBook struct{ steps map[string]panelQuitStep; gone map[string]bool }
func (b panelQuitBook) step(key string) panelQuitStep // "idle" when unknown
func (b panelQuitBook) with(key string, s panelQuitStep) panelQuitBook // a copy
func (b panelQuitBook) confirming() bool
func (b panelQuitBook) pending() bool
func (b panelQuitBook) frozen(hover bool) bool        // listFrozen
func (b panelQuitBook) revertConfirms() panelQuitBook
func (b panelQuitBook) sync(rows []ProcessRow) panelQuitBook // syncBook
```

- [ ] **Step 1: parity tables first.**
  1. Write a throwaway bun script, outside the repository, under `/tmp`. It calls the web functions above on a spread of inputs: zero, tiny, just under and over each unit boundary, large, and halves that round.
  2. Paste its outputs as literals into table tests in `menubar_panel_test.go`, one table per formatting function: `panelMem`, `panelSplitMem`, `panelPct`, `panelRate`, `panelRateText`, `panelTemp`, `panelDec`, `panelNetMax`, `panelIconHue`, `panelEst`.
  3. Port the logic tests of `tests/panel-react.test.ts`, everything but the rendering tests (`rows:` and `footer:`, which are web components), to Go with the same inputs and expectations:
     - `panelItem reads ?item=` has no Go counterpart: drop it.
     - `hero`, `subLine`, the four `diagnosis` tests, `chart`, `detailRows`, `memShares`, `topRows`, `growthTop` and `quit book`.
     - Their `t` is a stand-in that returns the key with its vars, so each test checks the key and the vars, not the words.
  4. Run `go test -run 'TestPanel' .` and watch it fail to compile.
- [ ] **Step 2: port.** Write `menubar_panel.go`, line for line from the web functions named above, with the same rules, thresholds and rounding.
  - Go's `math.Round` rounds half away from zero, and JavaScript's `toFixed` and `Math.round` don't always agree with it. Where the parity tables show a difference, match JavaScript.
  - Use `growthSize` for the web's `human()`.
- [ ] **Step 3: the strings.**
  1. Add to `src/native-i18n.json`, under both `zh` and `en`, every key the new functions and Task 2's views use. Copy the values from `src/i18n.ts`.
  2. The keys live under `menubar.panel.*`, `menubar.diag.*`, `status.*`, `process.*` and `growth.*`. Find them all by grepping `menubar_panel.go` and Task 2's view code for quoted keys.
  3. Write `tests/native-i18n.test.ts`. For every key in `native-i18n.json` that `src/i18n.ts` also has, the Chinese and English values must be equal (compare raw strings, placeholders included). It also checks that both languages have the same keys.
- [ ] **Step 4: verify.**
  - `gofmt -l .`
  - `go vet ./...` and `GOOS=windows GOARCH=amd64 go vet ./...`
  - `go test -count=1 -race -run 'TestPanel|TestMenuBar|TestNoSlashPathLiterals|TestSlashPathLiteralLint' .`
  - `bun run typecheck`
  - `bun test tests/native-i18n.test.ts tests/panel-react.test.ts`
- [ ] **Step 5: report.** Don't commit. Write the report to the path the dispatch gives.

### Task 2: Darwin — the native panels

**Files:** `menubar_panel_darwin.go`, `menubar_panel_darwin_test.go`, `menubar_darwin.go`, `menubar_other.go`, `growth_monitor.go`, `growth_app.go`, `go.mod`

**Interfaces:**
- **Consumes:**
  - Task 1's functions;
  - the readers: `watchStatusWith(ctx, send, menuBarPanelSource(realStatusSource(), item))`, `watchProcessesWith(ctx, send, realProcessSource())`, `Cleaner{}.MachineInfo()`, `Cleaner{}.Info()`, `Cleaner{}.GrowthState()`, `Cleaner{}.QuitApp(path)` and `Cleaner{}.ForceQuitApp(path)`, whose errors are `codeError` with codes `not-running` and `cannot-quit`;
  - the app: `menuBarGo`, `growthTrayMenu`, `residentModelNow`, `growthMenuAction`, `menuBarHolds`, `menuBarHoldMax`, `menuBarHeld`, `menuBarRelease`, `appIconRender`, `iconLooksBlank`, `nativeText`.
- **Produces:**
  - `func newMenuBarPanel(item string, deps menuBarPanelDeps) *menuBarPanel`, with `(p *menuBarPanel) view(c *ui.Context)`;
  - `func openMenuBarPanel(t *mygo.Tray, item string) *mygo.Window`;
  - `func menuBarGrowthChanged()`;
  - `func menuBarWarmPanels()`.

- [ ] **Step 1: the theme and the app's appearance.** Add to `menubar_panel_darwin.go`:

```go
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
```

  - The other colors the panel uses come from `src/style.css`'s light and dark blocks. Keep them in one small table next to the theme, keyed by `t.Dark`:
    - `--warn-text`, `--warn-soft`, `--danger-soft` and `--accent-soft`;
    - `--seg-app`, `--seg-wired` and `--seg-compressed`;
    - the chart tones `gpu` (`--seg-wired`) and `net` (`--seg-app`).
  - The view's first line is `c.SetTheme(menuBarPanelTheme(c.Theme(), p.dark()))`. `SetTheme` lasts one frame, so each frame starts from the system's theme. `p.dark` is `menuBarAppDark`, injectable for tests.

- [ ] **Step 2: the panel's state and data.**

```go
// menuBarPanelDeps is what a panel does outside itself; tests replace it.
type menuBarPanelDeps struct {
	dark      func() bool
	quit      func(path string) (bool, error)
	forceQuit func(path string) (bool, error)
	open      func(t MenuBarTarget)                // menuBarGo, after the panel closes
	menu      func(x, y int) bool                  // Cleanzo's menu at x, y; true when a line was chosen
	icon      func(path string) (*ui.Bitmap, bool) // the cached icon, if drawn; nil when the app has none
	close     func()                               // closes the panel
	hold      func(on bool)                        // keeps the panel open while a quit is pending
	after     func(d time.Duration, fn func())     // runs fn on the main thread after d
}
```

  - `menuBarPanel` keeps, as `PanelApp.tsx` does:
    - the latest `StatusSample`, the chart history (30) and the GPU history (3);
    - the `ProcessSample` and the CPU history (3);
    - `MachineInfo`, `SystemInfo` and `GrowthState`;
    - `failed`, `procFailed` and the number of samples;
    - the hover state, the quit book, and the rows shown last.
  - `openMenuBarPanel` makes the flyout and starts the readers, as `PanelApp.tsx`'s effects do:
    - the status stream for every item;
    - the process stream for `cpu` and `mem`;
    - `MachineInfo` once for `cpu`, `gpu` and `disk`;
    - `Info` once for `cpu`;
    - `GrowthState` for `disk`, read again on `menuBarGrowthChanged`.
  - Each result reaches the panel through `w.Update`. `OnClosed` cancels the readers.
  - `syncBook` runs on every process sample.

- [ ] **Step 3: the view.**
  - Lay it out as `src/panel/panel.css` does: the same paddings, sizes, weights and order. The spike shows the `ui` calls for each piece: header, sub-line, diagnosis callout, area chart, bar rows, list rows with icons, and footer with a link and 「⋯」.
  - Port the rest from `PanelApp.tsx`:
    - **Header:** title, then the memory pressure badge (`status.mem.pressureWarn` or `status.mem.pressureCrit`) for `mem`, then the hero with its small unit.
    - **Diagnosis:** the error line (`menubar.panel.error`) when a stream failed; otherwise the diagnosis in its tone; otherwise a placeholder bar while fewer than 3 samples arrived; otherwise nothing.
    - **Chart:** the chart in `panelChartTone`'s color, on 0 to `panelChartMax`.
    - **Memory:** the stacked bar.
    - **Detail rows:** dot, label, bar and value.
    - **List (`cpu`, `mem`):**
      - a section head (`menubar.panel.topCpu` and `menubar.panel.perCore`, or `menubar.panel.topMem`);
      - each row: the icon, or the initial on `panelIconHue`'s color; the name and the 「系统」 tag; the quit control in the 8 states of `QuitControl`; the value with 「≈」.
      - The quit control shows on the row's hover, or while the row's step is not `idle`.
      - The list's hover freezes it. Leaving it reverts confirmations (`revertConfirms`).
    - **Disk growth list:** `menubar.panel.topGrowth` and `menubar.panel.week`. Each row reads `+size`, or `≥ size` when at least.
    - **Footer:** the links, `menubar.panel.every` and 「⋯」 (`menubar.panel.more`).
  - **The quit flow,** as `PanelApp.tsx`'s `quit` and `force`:
    - The second click runs `deps.quit` or `deps.forceQuit` on a goroutine. The result comes back through `w.Update`, with codes mapped as `quitErrCode`'s callers map them.
    - A pending quit calls `deps.hold(true)`; the end of the last pending quit calls `deps.hold(false)`.
    - A confirmation arms `deps.after(panelQuitConfirm, revert)`.
  - **Links and names:**
    - A footer link closes the panel, then opens its target.
    - A name opens `MenuBarTarget{Page: "process", Tab: by, Key: row.Key}`; a growth row opens `{Page: "process", Tab: "disk", Key: app.Key}`.
    - 「⋯」 calls `deps.menu` at the button's bottom left. When a line was chosen, it closes the panel.
  - **Heights:** a `menuBarPanelHeight(item string) float32` table, measured with `ui.Render` previews at 320 points wide with full data. Write the previews to `/tmp`, not the repository, and say the numbers in the report.

- [ ] **Step 4: open it, and wire the rest.**
  - In `menubar_darwin.go`, `toggle` makes the flyout through `openMenuBarPanel(t, item)`. That keeps `Tray`, `PlacementBottom`, `Popover: true`, `Focusable` and `Width`, and passes `Content: ui.View(p.view)` and `Height: menuBarPanelHeight(item)`. It passes no `URL`, `FitContent`, `MinWidth`, `MaxWidth` or `MaxHeight`, and no `dark` parameter.
  - The `OnClose`/`OnClosed` hold logic stays. `deps.hold` writes `menuBarHolds` for the panel's window directly (`menuBarHoldPanel(w, on)`), with the same 15 s bound.
  - `deps.menu` shows `growthTrayMenu(residentModelNow(), …).PopupAt(w, x, y)` and reports whether a line was chosen, as `MenuBarMenu` does today.
  - **Icons:** a package-level cache and one worker goroutine (from the spike), which reuses `appIconDrawing`. `deps.icon` reads the cache and queues a draw on a miss.
  - **Growth hook:** in `growth_monitor.go`, `growthChanged` also calls `go menuBarGrowthChanged()`. `menubar_other.go` stubs it, and `menuBarWarmPanels`.
  - **Warming:**
    1. Measure the first open after launch: time from `toggle` to the first frame drawn, with `log` timestamps that you remove afterwards.
    2. Find where the time goes (fonts, glyph atlas, Metal pipeline) with a CPU profile or timestamps.
    3. Make `menuBarWarmPanels` do that work once in the background: for example, `ui.Render` of a CPU panel with sample data, so the fonts and atlases load. Call it from `show` when the first reading shows.
    4. Say in the report what the first open took before and after.
  - **`go.mod`:** add the two indirect requirements listed in the Tech Stack.

- [ ] **Step 5: view tests** in `menubar_panel_darwin_test.go`, with `ui.NewTester(p.view, 320, height)`, fake `deps` and sample data:
  - The CPU panel shows the hero, the diagnosis sentence, the detail rows and five rows.
  - Before any sample, it shows 「—」 and no list rows.
  - Two-click quit:
    1. Click 「退出」: the row shows 「确认退出」.
    2. Advance 3 s: it is back to 「退出」.
    3. Click twice: `deps.quit` is called once and `deps.hold(true)` runs.
    4. The quit returns false: 「还没退出」 and 「强制退出」 show.
  - Two clicks on 「强制退出」 call `deps.forceQuit`.
  - Clicking a name calls `deps.open` with the row's target and closes the panel.
  - The memory panel shows its badge under pressure 2.
  - The disk panel lists growth rows only while monitoring is on.
  - `SetDark(true)` with `deps.dark` true draws the dark card. Check `Background` through the theme, not by pixels.

- [ ] **Step 6: verify.**
  - `gofmt -l .`
  - `go vet ./...` and `GOOS=windows GOARCH=amd64 go vet ./...` and `GOARCH=amd64 go vet .`
  - `go test -count=1 -race -run 'TestPanel|TestMenuBar|TestGrowth|TestResident|TestNoSlashPathLiterals|TestSlashPathLiteralLint|TestBindingsArePlatformNeutral' .`
  - `bun run typecheck`

  The web panel still builds, until Task 3 removes it.

- [ ] **Step 7: report.** Don't commit. Write the report to the path the dispatch gives. Include the heights, the first-open times, and the preview images' paths.

### Task 3: Remove the web panel

**Files:** `menubar.go`, `menubar_test.go`, `menubar_sample_test.go` (its one reference), `panel.html`, `src/panel/`, `tests/panel-react.test.ts`, `vite.config.ts`, `src/dev/mock-mygo.ts`, `src/i18n.ts`, `src/mygo.ts` (generated), `CHANGELOG.md`, `README.md`

- [ ] **Step 1: Go.**
  - Delete `WatchMenuBarPanel`, `MenuBarHold`, `MenuBarOpen` and `MenuBarMenu` from `menubar.go`, and their tests from `menubar_test.go`.
  - Keep `menuBarPanelSource`, `menuBarHolds` and its helpers, `menuBarGo`, `MenuBarGo`, `MenuBarTarget` and `MenuBarUpdated`.
  - Fix the one reference in `menubar_sample_test.go`.
  - Run `bun run generate`.
- [ ] **Step 2: web.**
  - Delete `panel.html`, `src/panel/` and `tests/panel-react.test.ts`.
  - Remove the panel's input from `vite.config.ts`, and the four panel handlers from `src/dev/mock-mygo.ts`.
  - Remove from `src/i18n.ts` the keys only the panel used (`menubar.panel.*` and `menubar.diag.*`), after grepping `src/` for each.
  - `src/react/status/Spark.tsx` stays: the home page uses it.
- [ ] **Step 3: docs.**
  - `CHANGELOG.md`, the second bullet of 「未发布」: after `点开读数是它的详情面板：` insert `原生界面，一点开就是完整的面板、不闪；`.
  - `README.md`: after `点开任一读数是它的详情：` insert `原生界面，打开就是完整的面板；`.
- [ ] **Step 4: verify.**
  - `gofmt -l .`
  - `go vet ./...` and `GOOS=windows GOARCH=amd64 go vet ./...`
  - `go test -count=1 -race -timeout 40m ./...`, skipping the sandbox list given in the dispatch
  - `bun run typecheck`, `bun test` and `bun run build:web`
  - `git grep -n "panel.html\|src/panel"` finds nothing outside `docs/`.
- [ ] **Step 5: report.** Don't commit.

## Reviewer verification (on the maintainer's Mac)

1. Build, and launch with `open -g`.
2. Each of the seven panels:
   - opens complete at once, with no growing;
   - shows the same content as the web panel did;
   - is in Cleanzo's colors, light and dark;
   - updates every 2 s.
3. CPU and memory lists:
   - two-click quit and force quit work;
   - the panel holds during a quit;
   - a name opens the 进程 page at that row;
   - 「⋯」 shows the menu.
4. The first open after launch takes about as long as later ones.
5. Cost with a panel open is ≤ 3% (`/tmp/czt/panel-ab.py`, `/tmp/czt/idle.py`), with no extra WebContent process.
