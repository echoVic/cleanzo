# Audit fixes for the next release (2026-11-11)

**Spec:** `outputs/audit-2026-11-11/review-list.md` (untracked: it exists only in the maintainer's main checkout). Every item below points at a line of that list; read the matching entry for the scenario and the evidence. Line numbers in the list are for commit `b8bff34`; the branch has moved a few lines since, so find code by name.

**Goal:** fix what the audit left open so the branch can ship: safety items first, then wrong results, then the measured slowdowns, then verified dead code.

## Global Constraints

- **Workspace.** Work only inside your own git worktree. Never push, never switch or touch another branch, never edit the main checkout. Do not dispatch subagents.
- **Files.** Edit only the files your task names, plus their tests. If a fix truly needs a line elsewhere, make the smallest edit and name it in your report.
- **Test first.** Each behaviour fix gets a test that fails before the fix, for the reason the audit describes. Run it, see it fail, then fix. Deleting dead code needs no new test. If a fix cannot be tested (it needs live AppKit, a real Windows machine), say so in the report instead of writing a test that asserts nothing.
- **Contract.** `src/mygo.ts` is generated from the Go types and bound methods. Do not add, remove or rename bound methods or the JSON fields of types they use, unless your task says so. Go tasks check this before reporting: `bun install --frozen-lockfile && bun run generate && git diff --exit-code src/mygo.ts`.
- **Strings.** A new user-visible string goes into `src/i18n.ts` in both `zh` and `en` (same key set, same placeholders). Native strings read by Go live in `src/native-i18n.json`, both languages.
- **Skip rather than force.** If an item is wrong, unsafe, needs a product decision, or grows far beyond its description, leave the code alone and record the item and the reason in your report. A skipped item with a reason is a good outcome; a forced fix is not.
- **Comments.** Follow the file: Go comments in English, TS/TSX comments in Chinese where the file's are. A comment says what the code does or why, in the present tense. Never mention the audit, a reviewer, or what the code used to do.
- **Commits.** One commit per item or small group, in the style of `git log` (`fix(scope): …`, `perf(scope): …`, `refactor: …`, `test: …`, `docs: …`). No attribution trailers.
- **Before reporting.**
  - Go: `test -z "$(gofmt -l .)"`, `go vet ./...`, `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...`, `GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go vet ./...`, the tests you touched, then `go test -count=1 ./...` once (about four minutes; other agents load the machine, so re-run a timing failure alone before treating it as real).
  - Web: `bun install --frozen-lockfile`, `bun run typecheck`, `bun run test:web`, `bun run build:web`.
- **Tests never touch the real home.** Use the existing helpers (`setHome`, `t.TempDir`, `fakeTool`, `stubUsage`, the fake GitHub server).
- **Report.** Write the full report to the path given in your dispatch: per item, what you changed, the failing-then-passing test, or why you skipped it; then the verification output. Reply with only: status (DONE / DONE_WITH_CONCERNS / BLOCKED / NEEDS_CONTEXT), the commit range, one line of test results, and your concerns.

## Task 1: App updates and the growth monitor (Go)

**Files:** `appupdates.go`, `appupdates_darwin.go`, `appupdates_other.go`, every `growth*.go`, and their tests.

1. **Upgrade survives a closed window** (list §1 #1). `upgradeApp` runs brew and the re-read after it under `context.WithoutCancel(ctx)`; the ten-minute timeout stays. Test with a fake brew and a cancelled context.
2. **Four-part cask versions** (§2 "App updates"). When the cask version only adds trailing components to the bundle's short version (`4.1.15` vs `4.1.15.55,270135`), decide by `CFBundleVersion` (the cask's `,build` part or the full dotted version), as the Sparkle branch does; with nothing to compare, report "current". The success check after an upgrade uses the same function.
3. **Windows of seven and thirty days** (§2). Give the upper bounds twelve hours of slack (7 d + 12 h, 30 d + 12 h), as the day window has with 28 h. Update `TestWeekWindowBoundaries` to the new edge and add a case with daily runs 24 h + 15 min apart.
4. **Big folders that time out** (§2). `growthEntryChange` compares against the entry's latest complete measurement inside the window, not only the one base record. A manual run ("measure now") does not lower its own priority with `backgroundThread()`.
5. **Only GitHub's 403 is a rate limit** (§2). Other hosts' 403 map to the existing not-found / bad-feed result; 429 stays rate-limited everywhere.
6. **Cache clean after a failed usage probe** (§1 #11, the `cleanAppCache` part only). The failed entry carries `usage-unknown`, not `in-use`. Leave `UpgradeApp`'s answer as it is.
7. **Each Info.plist is read once** when the attributor rebuilds (§3, `growth_attrib.go`). Make the existing "once" test measure the real number of reads.
8. **Dead code** (§4): move `rememberAppAction`, `resetAppActions`, `appBrewLock.lock` into test files; remove the three unreachable nil branches; remove `fetchFeedWith` with its test; prune `growthNotified.Day` like `Week` and `Count`. Leave `GrowthDelta.Days` (contract).
9. **Comments** (§5): the `Code` list in `appupdates.go` gains "open-failed"; `growth_monitor.go` "growth page" and the `growth_app.go` comments say the icon also shows, and the app stays resident, when only Trash reminders are on.

## Task 2: Skills and AI tools (Go)

**Files:** `skills.go`, `skills_update.go`, `aitools.go`, `aimodels.go`, `tools.go`, and their tests.

1. **CC Switch origins** (§1 #2). A CC Switch row gives a folder its origin only when the folder is in CC Switch's own store or a link into it. If the code cannot tell where that store is, then an origin with no recorded tree whose local hash differs from the remote counts as modified, so an upgrade needs Force.
2. **No download redirects in release builds** (§1 #3). Remove the `CLEANZO_GITHUB_API` / `CLEANZO_CODELOAD` environment reads; tests keep assigning the variables.
3. **Unpacking is bounded** (§1 #4). Stop with an error past 512 MB written or 20,000 entries.
4. **`.DS_Store`** (§2 "Skills"). Skip it in the local tree hash only when the source listing has none at that path.
5. **Identical to upstream is current** (§2). When `remote == local`, the status is `current` and `Modified` is false, whatever the recorded tree says.
6. **Timeouts** (§2). The archive download is not cut at 60 s total (header timeout plus the caller's context); git calls get a 60 s timeout and the process-group handling `runToolCmd` has.
7. **Windows skill usage** (§2, `skills.go` `skillPathRef`). Backslash paths count; add a test that runs on every platform.
8. **`TrashSkills` leaves no broken link** (§1 #9). A folder is refused while any link to it remains.
9. **Unreadable Ollama manifest** (§1 #10). If any file under `manifests/` cannot be read or parsed, the clean stops for that model rather than treating shared blobs as unreferenced.
10. **Busy patterns** (§1 #7, `tools.go`). Match the command's first word (as `aiCommandRunning` does), so `go build` typed in a shell blocks `go clean -modcache`, and an unrelated process whose path contains `/pnpm` does not block pnpm.
11. **One scan per call** (§3). `ListSkillOrigins`, `CheckSkillUpdates` and `UpgradeSkills` build origins from the skill entry list without the transcript and hash work of a full `ScanSkills`; `skillGuard()` runs once per `UpgradeSkills` call.
12. **Parts add up** (§2 "AI tools"). The release in use is counted in a protected part, and entries under a tool's folder that no part covers are counted as unknown. Skip with a reason if this needs a new part id the page would show without a name.
13. **Dead code and comments** (§4, §5): `untarSafe` moves to the test file; `_ = fmt.Sprint` in `skills_update_test.go` goes; the `Skipped` and `Tree` doc comments list what the code returns. Leave `AIModelEntry.Kind` and `Blobs` (contract).

## Task 3: Junk rules, paths, Crashpad and the Trash (Go)

**Files:** `junk.go`, `rules.go`, `windows_rules.go`, `paths.go`, `crashpad.go`, `trash_reminder.go`, `trash_reminder_darwin.go`, `files_darwin.go`, `walk_darwin.go`, `recycle_capacity.go`, `windows_safety.go`, and their tests. Do not edit `main.go` (Task 4 owns it).

1. **Cloud storage is protected on macOS** (§1 #5): `~/Library/CloudStorage` joins the darwin protected trees.
2. **The temp root is checked on macOS** (§1 #6): refuse a resolved temp root that is `/`, the home folder, or inside a protected home folder; a normal `/var/folders/...` root passes.
3. **One rule for late arrivals in the Trash** (§1 #8): `cleanItem` uses `trashUnchanged` for entries in the Trash.
4. **The installer walk** (§3, measured 31–36 s): `cleanJunk` skips `recursiveFiles` targets unless the selection contains that rule or a `custom:` item; the walk skips `node_modules` folders and does not lstat each folder a second time. `ScanJunk` must still list every installer it listed before outside `node_modules`.
5. **Crashpad** (§3, §2): one measurement per database (not `measureChecked` then `measureCrashpad`), `measureCrashpad` takes the context, and `emptyQueue` reports entries it could not remove as blocked.
6. **Crash dumps on Windows** (§2 "Windows"): `crashDumps` uses the active catalog's rule and a filled environment; `crashpadOwner` skips a parent named "User Data".
7. **Names and Chromium caches** (§2 "Scans"): `collectDir` matches `Names` without regard to case; the two Chromium cache lists hold the same folders.
8. **Cancellation and goroutines** (§2, §3): `platformCollectPhotoFiles` on darwin stops when its context ends; `DirChildren` takes the semaphore before starting a goroutine.
9. **Trash reminder** (§3): skip the scan while `~/.Trash`'s own modification time is unchanged since the last result below the threshold. Remove the `p.resize()` calls that cannot change the height, with the two asserts on them, and correct the height comment.
10. **Guards resolve home once** per call in the clean phase (§3, `protectedKey`).
11. **Dead code and comments** (§4, §5): `scanJunk` and `expandMemoActive` leave production files; `recycleFits` loses its `known` parameter; `procInfo.Product` goes; the "// Task 6" comments go; `files_darwin.go` doc comments name the functions they sit on. Leave `JunkItem.Skipped` (contract).

## Task 4: App core, monitoring and the menu bar (Go)

**Files:** `main.go`, `apps_darwin.go`, `apps_windows.go`, `monitor.go`, `process.go`, `process_darwin.go`, `native.go`, `native_windows.go`, `status_darwin.go`, `status_windows.go`, `menubar*.go`, `shell_icon_windows.go`, and their tests. Do not edit `growth*.go`, `appupdates*.go`, `junk.go`.

1. **A failed usage probe is not "running"** (§1 #11): `uninstallApp` on macOS reports `usage-unknown`, and `scanOrphans` returns `errUsageUnknown` instead of an empty finished scan.
2. **Cancellation and timeouts** (§2 "Scans"): `measureChecked` honours its context on macOS too; `mdls` runs with a five-second timeout.
3. **Similar photos are deterministic** (§2): rows are sorted by path before clustering and ties break by path.
4. **Uninstall detail** (§3): `sharesBundleID` does not run `plutil` serially for every app on each call (bounded parallel, or the existing app index); the detail takes the app's size from its "app" item instead of walking the bundle twice.
5. **Windows uninstaller launch failure** (§2 "Windows"): `apps_windows.go` returns the code `launch-failed` when the uninstaller could not be started (including a declined UAC prompt), not `blocked`. Task 5 adds its text.
6. **Status sampler** (§2 "Status"): overall CPU uses the same counter-wrap guard as `process.go`; the per-minute battery refresh has a timeout and a timed-out refresh keeps the previous reading; `quitAppWith` answers at once when every terminate is refused.
7. **Dead code** (§4 "Go"): `procGroupKey` and its plumbing (the grouper becomes a required argument); the text sysctl fallbacks with their tests; `otherProc.UID`; `OrphanScanResult`; the pure aliases; the vacuous and duplicate tests the list names for these files. Keep the menu bar panels' error state. Leave `FullScanEvent.Scanned` and `SystemInfo` fields (contract), and leave `smoke.go` alone.
8. **`SHChangeNotify`** (§2): make the flag and its comment agree, after checking the constants against the Windows SDK values.
9. **Comments** (§5): `menubar.go` says the icon also shows for Trash reminders; `menubar_panel.go` and `menubar_panel_darwin.go` stop citing TypeScript names and a web panel that no longer exist.

## Task 5: Frontend shell: strings, styles, shared helpers, mock (web)

**Files:** `src/i18n.ts`, `src/style.css`, `src/ui/**`, `src/shared.ts`, `src/main.ts`, `src/platform.ts`, `src/dev/mock-mygo.ts`, the `format.ts` files under `src/react/` (only their `human` function), and the tests of those.

1. **Dead strings** (§4 "Frontend"): delete the listed keys in both languages and the code that only they feed (`isBuiltinCategory`, `categoryName`, `categoryDescription`, the seven settings codes in `shared.ts`). Grep each key in `src/`, `index.html`, `tests/` and `*.go` before deleting; keys built from a prefix are alive. Keep the 67 keys duplicated from `native-i18n.json`.
2. **Dead styles** (§4): delete the listed selectors from `src/style.css` after grepping each class; `.junk-icon-*` stays.
3. **`process.unsupported` on Windows** (§2): add an `@win` pair that does not say "macOS".
4. **`useT()` returns a stable function** between renders with the same language (§3).
5. **One `human()`** (§4, last bullet): `shared.ts` keeps the "1.5 KB" form; the copies in `ai/format.ts`, `large/format.ts`, `fullscan/format.ts` re-export it so page imports do not change; tests that pin "1 KB" / "2 KB" follow. Leave `displayPath` as each page has it.
6. **Mock runtime** (§2 "Frontend"): add handlers for `Cleaner.AppIcon`, `TrashReminder`, `SaveTrashReminder` and drop `Partial` from the `satisfies`.
7. **`launch-failed`**: `errText` translates it (zh "卸载程序没有启动，可能是取消了权限确认", en "The uninstaller did not start; the permission prompt may have been declined").
8. **Dead exports** (§4): `DialogTrigger`, the two unused attribute loops in `main.ts`.
9. **Small style defects** (fe list §5 of the audit): the cancelled `row-gap` near `.status` callouts, `.app-chev` in the reduced-motion reset, the undefined `--surface-2`. Leave the ghost button border as it is.

## Task 6: Cleanup pages (web)

**Files:** everything under `src/react/clean`, `list`, `large`, `fullscan`, `photos`, `apps` except the `human` function in their `format.ts`, and `tests/clean-react.test.ts`, `large-react`, `fullscan-react`, `photos-react`, `apps-react`, `crash-dumps`.

1. **Photos** (§2 "Frontend", §3): a selected photo whose cluster vanished is no longer selected; a partial failure toasts as an error like the other pages; `isGone` is not recomputed items × gone on every render; the preview cache is bounded and forgets trashed photos.
2. **Leftovers expansion** uses the same guarded loader as Full scan (request token, `expanded` check, folder sizes).
3. **Uninstall confirm** shows a freshly read detail, not the session's cached one (§1 #13).
4. **`pickedPaths`** is linear in the selection (§3).
5. **Stream rejections**: Large, Full scan, Photos and Leftovers attach the `done` handler before iterating, as Clean and Apps do.
6. **Dead code** (§4 "Frontend", the items in these folders) and the tests in `tests/clean-react.test.ts` that no longer exercise production code.

## Task 7: AI, skills, settings, process and status pages (web)

**Files:** everything under `src/react/ai`, `aispace`, `skills`, `settings`, `process`, `status`, `growth`, `icons` except the `human` function in `ai/format.ts`, and `tests/aitools-react.test.ts`, `aispace-react`, `skills-react`, `settings-react`, `process-react`, `status-react`, `growth-react`, `app-icon`.

1. **AI tool page** (§2 "AI tools"): the Codex crash-dump banner is re-read with each scan; a rescan that fails after a clean is shown as failed, not as the current list; a sessions part with no selectable group cannot be ticked into a checked "0 B".
2. **Skills** (§1 #12): the per-row overwrite asks with the same dialog as "update all"; the button and the dialog count the same thing.
3. **Settings** (§2): after a failed rename the folder-name input shows the saved name again.
4. **Process page** (§3): the row sort is memoised on the sample and the sort key, with one collator; child rows are built only for the open row; returning to the disk tab does not start from blank.
5. **Dead code** (§4 "Frontend", the items in these folders), and tests that assert on things that no longer exist (`st-viewall`, `STATUS_ALERT.pressure`).

## Task 8: Type-check the tests (web, after Tasks 5–7 are merged)

**Files:** `tsconfig.json`, `package.json`, `bun.lock`, `tests/**`.

Add `@types/bun` as a dev dependency, include `tests`, `vite.config.ts` and `mygo.config.ts` in the type check, and fix every error by correcting the test (a missing prop, a wrong argument type), never by `any` or `@ts-ignore`. `bun run typecheck`, `bun run test:web` and `bun run build:web` pass.
