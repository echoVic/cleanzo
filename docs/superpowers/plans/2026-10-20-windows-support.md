# Windows Support — Implementation Plan

> **For agentic workers:** In this project the tasks are executed by the Codex CLI, several tasks per run (see "Execution batches"). **Executors do not commit** — the reviewer checks and commits each task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Cleanzo installs, updates and works on Windows 10/11 x64 with the same safety guarantees as on macOS, and macOS behaviour does not change at all.

**Architecture:** OS calls move into `*_darwin.go` / `*_windows.go` pairs (darwin code moved verbatim). Pure logic and data tables, including the Windows rule table, stay untagged so the Mac can test them. Windows-only code is tested by a new GitHub Actions `windows-latest` job. Bound methods and their types stay in untagged files and are identical on every platform, because `mygo generate` builds the client on the host.

**Tech Stack:** Go 1.27.1 (package `main`), mygo 0.5.0 (WebView2 backend on Windows, NSIS installer, signed updates), `golang.org/x/sys` v0.49.0 (`windows`, `windows/registry`), TypeScript 7 + React 19 + Vite 8, bun test, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-20-windows-support-design.md` (Chinese). Read it first; section numbers below refer to it.

## Global Constraints

- **Dependencies:** none new. `golang.org/x/sys` is already in go.mod. Do not run commands that need the network. All modules are in the local cache, so `GOPROXY=off` is fine.
- **Bindings:** never edit `src/mygo.ts`; run `bun run generate` after changing bound methods or their types. Bound methods (`Cleaner`, `LanguageSettings`) live only in untagged files. Tagged files declare no exported identifiers (Task 5 enforces this).
- **macOS must not change:**
  - Darwin code is moved, not rewritten.
  - In darwin tests the only allowed edits are moving a test to a `*_darwin_test.go` file and the mechanical `t.Setenv("HOME", x)` → `setHome(t, x)` swap. No assertion may change.
  - `go test -list '.*' .` on darwin lists the same 272 tests plus any new ones.
- **Windows safety baseline:**
  - Windows code fails closed. If a probe (processes, usage, tools) cannot run, nothing is cleaned and the item reports why.
  - Only the resolved user profile and a temp folder inside it are ever modified (spec §五).
- **Strings and errors:** every user-visible string goes through `t()` with both `zh` and `en` entries in `src/i18n.ts`. Backend errors shown to users are codes (`in-use`, `blocked`, `gone`, `unsupported`, …) translated by the existing error mapping.
- **Style:**
  - Go: a short plain doc comment on every declaration; match the surrounding naming and idioms.
  - TypeScript: terse Chinese comments like the existing code.
  - Win32: prefer the typed wrappers in `golang.org/x/sys/windows` over hand-written struct layouts. Every hand-written layout gets a Windows CI assertion.
- **After each task:**
  - `gofmt -l .` prints nothing.
  - `go vet ./...` passes.
  - `go test -race -count=1 ./...` passes.
  - `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...` passes (this also compiles the tests).
  - `bun run typecheck && bun run test:web` pass, and `bun run build:web` passes when the frontend changed.
- **Codex sandbox:**
  - `TestLargePhotosAreDecodedSmall` and `TestUsageGuardSeesSockets` fail for environmental reasons (sips temp files, unix-socket bind). The reviewer runs the full suite outside the sandbox.
  - If the Go build cache is not writable, use `GOCACHE=$TMPDIR/cleanzo-gocache`.
  - Windows tests cannot run in the sandbox. Cross-compile them with `GOOS=windows go vet ./...`; they run in CI.
- **windows-latest differs from users' machines:**
  - The runner is an elevated admin with UAC off, so symlinks and Program Files writes succeed there.
  - TEMP is an 8.3 short path (`C:\Users\RUNNER~1\…`).
  - Defender briefly holds new files.

  Tests must tolerate these differences and must not depend on them.
- **Never touch** `交付/`, `build/`, `dist/` or any file outside the repository.

## Review Focus

1. **macOS is unchanged.** The darwin suite passes with the same test list, and no darwin assertion changed. *Tasks 1–3.*
2. **Paths are judged by their final target, and reparse points are never traversed or removed through** (junctions, symlinks, mount points, cloud placeholders). A junction from the profile to `D:\`, and the legacy `Application Data` junction, never lead to a removal. *Test in Task 7.*
3. **Nothing outside the profile, and nothing protected inside it, is modified.** This covers known-folder roots, `OneDrive*`, the AppData roots, `Roaming\Microsoft`, `Local\{Packages,Programs,Microsoft\WindowsApps,Microsoft\Credentials,Microsoft\Vault,wsl,Docker}`, `NTUSER*`, `.ssh`, `.gnupg`, and Cleanzo's own data. ADS, `\\?\`, `\\.\` and UNC paths are refused. A temp folder outside the profile is unusable. *Tests in Tasks 7, 10 and 12.*
4. **"Move to Recycle Bin" never silently becomes a permanent delete.** It runs only on fixed drives, and AppData paths cannot be trashed from user-picked rows. *Test in Task 8.*
5. **An item whose owner runs is left alone, and a failed process snapshot means nothing is cleaned.** This includes Chromium caches opened with share-delete. *Test in Task 9.*
6. **The Recycle Bin is emptied only when its row is selected, first in the pass, and only if it is unchanged since the scan.** *Test in Task 12.*
7. **An uninstall launches only the registry-declared uninstaller of the key the user picked; InstallLocation is never trashed.** *Test in Task 23.*
8. **Bound API is identical across platforms.** *Test in Task 5.*

---

## File Structure

| File | Responsibility |
|---|---|
| `fs_darwin.go` / `fs_windows.go` (new) | `changedAt`, `onDisk`, `removalRefused`, `mountPoint`, `diskStat`; Windows: final-path resolution, reparse and placeholder checks |
| `proc_darwin.go` / `proc_windows.go` (new) | Running tool commands with tree kill, tool dirs, `findTool`, `psCommands`; Windows: process snapshot |
| `usage_darwin.go` / `usage_windows.go` (new) | `openFiles`, `runningApps` (darwin: lsof/lsappinfo, moved); Windows: from the process snapshot |
| `trash_windows.go` (new) | Recycle Bin move with nuke warning, fixed-drive check; Recycle Bin query/empty for `sys.trash` |
| `status_darwin.go` / `status_windows.go` (new) | `realStatusSource`, memory, battery; darwin code moved from monitor.go |
| `native_windows.go` (new) | Win32 reads for the status page and `Info()` |
| `apps_darwin.go` / `apps_windows.go` (new) | Platform app listing and uninstall behind the existing bound methods |
| `paths.go` (new) | Separator- and case-aware `within`, `expandHome`, `shortHome`, platform protected lists |
| `windows_rules.go` (new, untagged) | `windowsJunkRules` table |
| `rules.go` | Keeps the model and the macOS table; `junkRules` selected per platform |
| `native_other.go` | Becomes `//go:build !darwin && !windows` |
| `testenv_test.go` (new) | `setHome`, the Windows TestMain sandbox, the slash-literal lint test, the binding AST test |
| `*_darwin_test.go` (new) | macOS-only tests moved out of the shared test files |
| `*_windows_test.go` (new) | Windows adversarial, e2e and smoke tests |
| `.github/workflows/ci.yml` (new) | macOS + Windows test jobs, the Windows probe job, later the smoke tour |
| `src/platform.ts` (new) | `isWindows()`, path helpers used by every page |
| `src/i18n.ts` | `@win` overrides, Windows glossary, rule names for new Windows rule IDs |
| `mygo.config.ts`, `package.json` | `windows` block, `build:windows` |
| `README.md`, `docs/RELEASING.md`, `CHANGELOG.md` | Windows install, release and changelog |

---

## Run A — Foundation

### Task 1: Compile for Windows with fail-closed stubs

**Files:** create `fs_darwin.go`, `fs_windows.go`, `proc_darwin.go`, `proc_windows.go`, `usage_darwin.go`, `usage_windows.go`, `status_darwin.go`, `status_windows.go`, `native_windows.go`. Modify `main.go`, `tools.go`, `monitor.go`, `native_other.go`.

- [ ] **Darwin moves (verbatim, no semantic change):**
  - From `main.go`: `changedAt` (174), `onDisk` (240), `protectedFlags`/`removalRefused` (468-488), `diskStat` (1104), `mountPoint` (1163) → `fs_darwin.go`.
  - From `main.go`: `openFiles`/`parseLsofNames` (388-421) and `runningApps`/`parseRunningApps` (428-457) → `usage_darwin.go`.
  - `tools.go`: move the process-group code of `runTool` (`Setpgid`, the `Cancel` kill, the final kill) behind `runToolCmd(cmd *exec.Cmd) ([]byte, error)` in `proc_darwin.go`. Also move `toolDirs`, `findTool`'s exec-bit check, `psCommands` and the PATH string to `proc_darwin.go`. `runTool` keeps its signature.
  - From `monitor.go`: `realStatusSource`, `readMemoryStatus`, `readSwapUsage`, `statusBattery` → `status_darwin.go`. Leave the pure helpers such as `memoryFromVM` and `parseBatteryJSON` untagged.
- [ ] **Windows versions (minimal):**
  - `changedAt`: the later of LastWriteTime and CreationTime, read from `info.Sys().(*syscall.Win32FileAttributeData)`.
  - `onDisk`: `info.Size()`.
  - `removalRefused`: true for any `FILE_ATTRIBUTE_REPARSE_POINT`, `FILE_ATTRIBUTE_OFFLINE`, `FILE_ATTRIBUTE_RECALL_ON_OPEN` or `FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS` entry, and for a folder that cannot be opened with access denied.
  - `mountPoint`: true for any reparse point, and when `GetVolumePathNameW` differs from the parent's.
  - `diskStat`: `GetDiskFreeSpaceExW` on the volume of the home folder.
- [ ] **Windows fail-closed stubs** until later tasks fill them in:
  - `openFiles`, `runningApps` and `psCommands` return an error, so the usage guard fails and `toolBusy` is true.
  - `findTool` returns `""`.
  - `runToolCmd` returns `errUnsupported`.
  - `realStatusSource` returns readers that return zero values.
  - `native_windows.go` has the same stubs as `native_other.go`.
- [ ] `native_other.go` → `//go:build !darwin && !windows`.
- [ ] Add `errUnsupported = errors.New("unsupported")`, and its translation in the frontend error mapping (zh/en).
- [ ] Verify: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...` and the same for `GOARCH=arm64` succeed; on darwin, `go test -race -count=1 ./...` is green.

### Task 2: Separator-aware paths

**Files:** create `paths.go`; modify `main.go`, `junk.go`, `aitools.go`, `crashpad.go`, `monitor.go`, `settings.go`, `skills.go`.

- [ ] Move `within` to `paths.go`, splitting on `filepath.Separator` (darwin behaviour identical).
- [ ] Add these helpers in `paths.go`:
  - `expandHome(p string) string`: turns `~`, `~/…` and, on Windows, `~\…` into a path under `homeDir()`.
  - `shortHome(p string) string`: the inverse, used for display.
- [ ] Replace hard-coded separators. Each site either uses `within`, `filepath.Join`/`Split`, or `strings.Split(…, string(filepath.Separator))`:
  - `main.go`: `safeResolved` (288), `inUse` (`p+"/"`, 366), `revealArgs` `~/` (1641).
  - `junk.go`: `displayable` (240), 290, 335.
  - `aitools.go`: 290, 388, 483-487, 802, `aiRemovable`/`aiInsideCleanable` (829-865), `displayHome` (870).
  - `crashpad.go`: 162-163.
  - `monitor.go`: 635, 699, 1016-1026.
  - `settings.go`: 157 `~/` expansion.
- [ ] **Protected lists:**
  - Keep the macOS values. Build them through a helper that applies `filepath.FromSlash` in the variable initialiser, not in `main()`.
  - Put the platform lists in `paths.go`: darwin lists unchanged; Windows lists added in Task 10 (empty for now).
- [ ] `configPath()` → `filepath.Join(os.UserConfigDir(), "Cleanzo", "config.json")`; on macOS this is the same path. `skill-updates.json` follows it.
- [ ] **Test** `TestNoSlashPathLiterals` (in `testenv_test.go`, untagged):
  - Scan every non-test `.go` file except `*_darwin.go` for `+"/"`, `"/"+`, `"~/"`, `Split(…, "/")`, and `HasPrefix(…, …+"/")`.
  - Fail unless the line ends with `// slash-ok: <reason>`. Use the marker for URLs, git paths, rule-pattern components and similar.
- [ ] **Test:** table tests for `within`, `expandHome` and `shortHome` built with `filepath.Join`, so they are platform-neutral.

### Task 3: Test isolation and macOS-only tests

**Files:** create `testenv_test.go` (helpers) and `*_darwin_test.go` files; modify all `*_test.go`.

- [ ] `setHome(t, dir)`:
  - Sets `HOME`.
  - On Windows also sets `USERPROFILE`, `APPDATA=dir\AppData\Roaming` and `LOCALAPPDATA=dir\AppData\Local` (creating both).
  - Clears the memoised path resolution if there is one.
- [ ] Replace all 57 `t.Setenv("HOME", x)` with `setHome(t, x)` (mechanical).
- [ ] **TestMain** (`main_test.go:1162`) keeps its Trash stand-in. On Windows it first does this:
  - Create a sandbox with `os.MkdirTemp`, resolving 8.3 names.
  - Point `USERPROFILE`, `HOME`, `APPDATA`, `LOCALAPPDATA`, `TEMP` and `TMP` into it.
  - Check that `homeDir()` and `os.TempDir()` are now inside it, otherwise print why and `os.Exit(2)`. Tests must never run against the real profile.
- [ ] **Move to `*_darwin_test.go`** (unchanged) every test that needs a macOS binary or syscall:
  - binaries: plutil, lsof, lsappinfo, du, find, sips, hdiutil, system_profiler;
  - syscalls: `syscall.Mkfifo`, `syscall.Chflags`;
  - `#!/bin/sh` fake tools;
  - macOS paths (`/System`, `/Volumes`, `/private`, `/etc`) in expected values;
  - `owners_test.go` entirely, and the tests built on `makeApp`.

  Shared helpers stay untagged.
- [ ] Verify:
  - The darwin test list is unchanged (272 tests listed by `go test -list '.*' .`).
  - `GOOS=windows go vet ./...` compiles the remaining tests.

### Task 4: Rule-ID frontend test reads every rule table

**Files:** modify `tests/clean-react.test.ts` (296).

- [ ] Read rule IDs from every repo-root `*.go` file whose name ends in `rules.go` (now `rules.go`; `windows_rules.go` from Task 11).
- [ ] Assert at least 55 IDs, so an empty table cannot pass.

### Task 5: Binding consistency test

**Files:** `testenv_test.go`.

- [ ] `TestBindingsArePlatformNeutral` parses the package's non-test files with `go/parser`. For every file with a GOOS suffix or `//go:build` GOOS constraint, it asserts:
  - no method has a receiver of type `Cleaner` or `LanguageSettings`;
  - no exported identifier is declared.

### Task 6: CI and the Windows probe

**Files:** create `.github/workflows/ci.yml`; append a "Probe results" section to this plan.

- [ ] Triggers: `push` on `master` and `feat/**`, plus `pull_request`. Actions: `actions/checkout@v7`, `actions/setup-go@v7` (`go-version-file: go.mod`), `oven-sh/setup-bun@v2`.
- [ ] **Job `macos`** (`macos-latest`):
  1. `bun install --frozen-lockfile`
  2. `test -z "$(gofmt -l .)"`
  3. `go vet ./...`
  4. `go test -race -count=1 ./...`
  5. `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...`
  6. `bun run typecheck`, `bun run test:web`, `bun run build:web`
- [ ] **Job `windows`** (`windows-latest`, `CGO_ENABLED=0`): `go vet ./...`, then `go test -count=1 ./...`. No gofmt there: checkouts on Windows may use CRLF line endings.
- [ ] **Job `windows-probe`** (`windows-latest`, after `windows`), which records facts for later runs:
  1. Run `bun install --frozen-lockfile`.
  2. Build a debug installer with `bun run build -- -platform windows/amd64 -debug`. Without the updater key it produces no update files; mygo downloads NSIS itself.
  3. Install it silently with `& "build\windows-amd64\Cleanzo Setup *.exe" /S`.
  4. Report whether the WebView2 Runtime is present (registry `EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`).
  5. Launch `%LOCALAPPDATA%\Programs\Cleanzo\Cleanzo.exe` with `WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--remote-debugging-port=9222`.
  6. Wait up to 30 s, then `curl http://127.0.0.1:9222/json/version`.
  7. Take a full-screen screenshot with PowerShell (`System.Windows.Forms` + `CopyFromScreen`).
  8. Write the WebView2 and CDP results to `$GITHUB_STEP_SUMMARY`, and upload the screenshot as an artifact.

  The job must not fail when CDP is unreachable; it reports instead.
- [ ] After the first CI run, the reviewer fills in "Probe results" (below).

---

## Run B — Windows safety core

### Task 7: Final-path resolution and reparse points

**Files:** `fs_windows.go`, `fs_darwin.go`, `junk.go`, `main.go`.

- [ ] Add `linkModes = fs.ModeSymlink | fs.ModeIrregular` and use it at `junk.go:1205` (`traversalBlocked`), `:1300` (`memoResolved`) and `:637` (`planItem`). On darwin `ModeIrregular` never marks a link, so the behaviour is the same.
- [ ] **Windows `resolvePath`:** resolve the longest existing prefix with `GetFinalPathNameByHandleW`. Open with `FILE_FLAG_BACKUP_SEMANTICS` and `FILE_READ_ATTRIBUTES`, and strip `\\?\`. Darwin keeps `EvalSymlinks`. Put the platform part in `resolveFinal(p string) string`.
- [ ] **Windows `safeToClean`:**
  - Refuse the path if any component from the home folder down is a reparse point (Lstat attributes).
  - Refuse paths that are not on a local drive letter: UNC, `\\?\`, `\\.\`, or a `:` after index 1 (ADS).
  - Every caller gets this, including CleanPaths, TrashPath, AI tools and skills.
- [ ] **Windows tests** (`fs_windows_test.go`, CI). Each fixture is created inside the sandbox profile:
  - A junction (`mklink /J` via `cmd /c`, or `windows.DeviceIoControl`) inside a cache folder, pointing outside the profile. Neither the junction nor its target is listed, measured or removed by `ScanJunk`/`CleanJunk`.
  - A junction named `Application Data` → `AppData\Roaming`. Nothing under it is offered, and `safeToClean` refuses it.
  - An ADS path and a UNC path are refused by `safeToClean`.
  - A file with `attrib +O` (offline) is refused by `removalRefused` and survives a clean.

### Task 8: Recycle Bin moves that never nuke silently

**Files:** create `trash_windows.go`; modify `main.go`.

- [ ] **`recycle(path string) error` on Windows:**
  - Call `SHFileOperationW` with `FO_DELETE` and `FOF_ALLOWUNDO|FOF_NOCONFIRMATION|FOF_SILENT|FOF_NOERRORUI|FOF_WANTNUKEWARNING`, using a double-NUL-terminated path.
  - Return an error when the result is non-zero or `fAnyOperationsAborted` is set.
  - Refuse paths whose drive is not `DRIVE_FIXED` (`GetDriveTypeW`).
  - `func init() { trashItem = recycle }`. TestMain's stand-in still replaces it in tests.
- [ ] **Split `removable`:**
  - `cleanable(path) = trashable(path) && safeToClean(path)`, used by `junkEnv.removable`.
  - `removable(path) = cleanable(path) && userPickable(path)`, used by CleanPaths, TrashPath and the rows of Large Files and Full Scan.
  - `userPickable` is false on Windows for anything inside `AppData` and true on darwin.
- [ ] **Windows tests:**
  - `recycle` moves a file from the sandbox to the bin, and the file is gone from disk.
  - `recycle` refuses a path on a non-fixed drive (mock `GetDriveTypeW` through a variable).
  - `userPickable` refuses `AppData\Local\wsl\x\ext4.vhdx`.
  - Never trigger the nuke dialog in CI.

### Task 9: Windows usage guard

**Files:** `usage_windows.go`, `proc_windows.go`, `main.go` (the `usageGuard` struct).

- [ ] **`processSnapshot() ([]procInfo, error)`** with `procInfo{Pid, PPid uint32; Exe, Name, Product, CmdLine string; Created time.Time}`:
  - Enumerate with Toolhelp32.
  - Exe path from `QueryFullProcessImageNameW` (`PROCESS_QUERY_LIMITED_INFORMATION`).
  - Command line from `NtQueryInformationProcess(ProcessCommandLineInformation)`.
  - Product name from the exe's version info (FileDescription, then ProductName).
  - Keep only processes in the current session.
  - Return an error, so the guard fails closed, when the snapshot itself fails. Unreadable single processes are kept with the fields that could be read.
- [ ] **`usageGuard` gains `roots []string`:** lower-cased folders whose contents are in use, matched both ways (`within(p, root) || within(root, p)`). darwin leaves it empty, so its behaviour is unchanged.
- [ ] **Windows guard sources:**
  - `openFiles` returns no files.
  - `runningApps` returns the exe base names without `.exe` plus the product names (lower-cased) as `ids`, and the exe paths as `apps`.
  - `probeUsage` fills `roots` with every exe folder and every absolute path found in command lines. Parse both quoted tokens and `--flag=` values.
  - `psCommands` returns the command lines, one per line.
- [ ] **Windows tests:**
  - Start a child process (the test binary re-run with a helper flag) whose command line includes `--user-data-dir=<sandbox>\AppData\Local\Fake\User Data`.
  - With it running, a cache under that folder is `inUse`, and the parent folder is too.
  - After it exits, neither is.
  - Snapshot failure (injected through a variable) makes `inUse` true for everything.

### Task 10: Windows protected areas, temp root and small platform switches

**Files:** `paths.go`, `main.go`, `settings.go`, `monitor.go`, `junk.go`.

- [ ] **Windows protected lists** (the spec §五.4 list). For `protectedHomeDirs` the folder itself is protected; for `protectedHomeTrees` the whole subtree is.
  - **`OneDrive*`:** any first component under home starting with `onedrive`, plus the targets of `%OneDrive%`, `%OneDriveConsumer%` and `%OneDriveCommercial%`.
  - **AppData:** the roots `AppData`, `AppData\Local`, `AppData\Roaming` and `AppData\LocalLow`; the subtrees `Roaming\Microsoft`, `Local\Packages`, `Local\Programs`, `Local\Microsoft\WindowsApps`, `Local\Microsoft\Credentials`, `Local\Microsoft\Vault`, `Local\wsl` and `Local\Docker`.
  - **Home:** `NTUSER*`, `.ssh`, `.gnupg`.
  - **Cleanzo's own data:** `$APPDATA\Cleanzo` and `$LOCALAPPDATA\Cleanzo`.
  - **Known folders:** the roots Desktop, Documents, Downloads, Pictures, Music, Videos, Favorites, Contacts, Links, Searches and Saved Games.
- [ ] **Windows temp root:** accepted only when the resolved `os.TempDir()` is inside the resolved profile. Otherwise `$TMPDIR` patterns expand to nothing and `safeResolved` gives the temp branch nothing.
- [ ] **`fullDiskAccess()`:** true on Windows. **`OpenFullDiskAccessSettings`:** returns `errUnsupported` on Windows.
- [ ] **Self-exclusion:** the status page's app list and quit matching skip the current PID and `os.Executable()` on Windows. The bundle-ID checks at `monitor.go:695/1022` stay for darwin.
- [ ] **Windows tests:** each protected entry is refused by `safeToClean`. A TEMP set to `C:\CleanzoTempOutside` makes `sys.temp` empty. A `%OneDrive%` pointing to `D:\X` is refused.

---

## Run C — Junk cleaning on Windows

### Task 11: Windows rule table and roots

**Files:** create `windows_rules.go` (untagged); modify `rules.go`, `junk.go`, `src/i18n.ts`.

- [ ] **Table selection:** `junkRules` is chosen per platform (`rules_select_darwin.go` / `rules_select_windows.go`, or a `runtime.GOOS` switch in an untagged initialiser). The macOS table keeps its order and content.
- [ ] **New roots:**
  - `junkEnv` gains `localAppData` and `appData`, resolved from `LOCALAPPDATA`/`APPDATA` and accepted only inside the home folder.
  - `globTargets` accepts the roots `$LOCALAPPDATA` and `$APPDATA` the way it accepts `$TMPDIR`.
  - Tests set the fields directly.
- [ ] **`windowsJunkRules`** (spec §六). Reuse existing IDs where the concept is the same. Patterns are written with `/`.
  - **System:**
    - `sys.temp` (`$TMPDIR`, `tempMinAge`).
    - `sys.crashdumps`, `Contents: true`, over `$LOCALAPPDATA/*/Crashpad`, `$LOCALAPPDATA/*/*/Crashpad`, `$LOCALAPPDATA/*/User Data/Crashpad`, `$LOCALAPPDATA/*/*/User Data/Crashpad`, `$APPDATA/*/Crashpad`.
    - New `sys.wer` over `$LOCALAPPDATA/Microsoft/Windows/WER/ReportArchive`, `…/ReportQueue`, and `$LOCALAPPDATA/CrashDumps` (`*.dmp`).
    - New `sys.shadercache` over `$LOCALAPPDATA/D3DSCache`, `$LOCALAPPDATA/NVIDIA/DXCache`, `$LOCALAPPDATA/NVIDIA/GLCache`, `$LOCALAPPDATA/AMD/DxCache`.
    - `sys.trash` (Task 12).
  - **Browsers** (Owners are exe base names). The Chromium profile caches use `chromiumProfileCaches` plus `Cache`.
    - `browser.chrome` → `$LOCALAPPDATA/Google/Chrome/User Data/*`, owner `chrome`.
    - `browser.edge` → `$LOCALAPPDATA/Microsoft/Edge/User Data/*`, owner `msedge`.
    - `browser.brave` → `$LOCALAPPDATA/BraveSoftware/Brave-Browser/User Data/*`, owner `brave`.
    - `browser.vivaldi` → `$LOCALAPPDATA/Vivaldi/User Data/*`, owner `vivaldi`.
    - `browser.opera` → `$LOCALAPPDATA/Opera Software/Opera Stable/Cache` and `$APPDATA/Opera Software/Opera Stable/{Code Cache,GPUCache}`, owner `opera`.
    - `browser.firefox` → `$LOCALAPPDATA/Mozilla/Firefox/Profiles/*/cache2`, owner `firefox`.
    - `browser.qq` → `$LOCALAPPDATA/Tencent/QQBrowser/User Data/*`, owner `qqbrowser`.
  - **Updates:** `update.updaters` → `$LOCALAPPDATA/*-updater/pending`.
  - **Dev:**

    | Rule | Targets |
    |---|---|
    | `dev.npm` | `$LOCALAPPDATA/npm-cache/_cacache`, `…/_logs` |
    | `dev.npx` | `$LOCALAPPDATA/npm-cache/_npx` |
    | `dev.pnpm` | `$LOCALAPPDATA/pnpm-cache` |
    | `dev.pnpm-store` | Task 13 command |
    | `dev.yarn` | `$LOCALAPPDATA/Yarn/Cache`, `.yarn/berry/cache` |
    | `dev.bun` | `.bun/install/cache` |
    | `dev.node-gyp` | `$LOCALAPPDATA/node-gyp/Cache` |
    | `dev.electron` | `$LOCALAPPDATA/electron/Cache`, `$LOCALAPPDATA/electron-builder/Cache` |
    | `dev.typescript` | `$LOCALAPPDATA/Microsoft/TypeScript` |
    | `dev.playwright` | `$LOCALAPPDATA/ms-playwright` |
    | `dev.pip` | `$LOCALAPPDATA/pip/Cache` |
    | `dev.uv` | `$LOCALAPPDATA/uv/cache` |
    | `dev.poetry` | `$LOCALAPPDATA/pypoetry/Cache/cache`, `…/artifacts` |
    | `dev.cargo` | as on macOS |
    | `dev.go-build` | `$LOCALAPPDATA/go-build` |
    | `dev.go-mod` | Task 13 command |
    | `dev.android` | `.android/cache`, `.android/build-cache` |
    | `dev.gradle` | `.gradle/caches` |
    | `dev.maven` | `.m2/repository` |
    | `dev.nuget` (new) | `$LOCALAPPDATA/NuGet/v3-cache` |

    Default flags follow the macOS rule with the same ID.
  - **Review:**
    - `review.installers` → Downloads, Desktop and Documents, `recursiveFiles`, names `*.msi`, `*.msix`, `*.msixbundle`, `*.appx`, `*.appxbundle`, `*setup*.exe`, `*install*.exe`.
    - `review.partial-downloads` → Downloads, names `*.crdownload`, `*.part`, `*.partial`, `MinAge` 24 h.
- [ ] **Generic app caches on Windows:** a source over `$APPDATA/*/{chromiumCacheDirs}` replaces the three macOS sources inside `expandItems`. The macOS sources stay as they are. Items are named after the folder. `Running` comes from the usage snapshot (folder name vs exe/product names).
- [ ] **i18n:** zh/en names and descriptions for the new IDs (`sys.wer`, `sys.shadercache`, `dev.nuget`). Descriptions of reused IDs that mention macOS paths get `@win` overrides (Task 19 adds the override lookup; until then plain keys are fine).
- [ ] **Tests (untagged, run on the Mac):**
  - The Windows table is well-formed: unique IDs, valid groups, every pattern root is `~`, `$TMPDIR`, `$LOCALAPPDATA` or `$APPDATA`, and every ID has zh/en names.
  - Expansion against a fake Windows layout built under `t.TempDir()`, with `junkEnv.localAppData`/`appData` set, finds the expected candidates and nothing in `User Data/Default/{Cookies,History,Login Data}`.

### Task 12: Native `sys.trash` (Recycle Bin)

**Files:** `trash_windows.go`, `junk.go`, `windows_rules.go`.

- [ ] **Scan:** `SHQueryRecycleBinW(<home volume root>)` → size and count.
- [ ] **Clean:**
  - Run a fresh query. If size or count differ from the scan, skip with the reason `changed`.
  - Otherwise call `SHEmptyRecycleBinW(root, SHERB_NOCONFIRMATION|SHERB_NOPROGRESSUI|SHERB_NOSOUND)` and report the freed size.
- [ ] **Plumbing:**
  - Keep the ID `sys.trash`, so the existing "Trash first" ordering (`junk.go:912`) applies.
  - Add the smallest hook the engine needs, for example an optional in-process runner on `toolCommand` or a `native` field on `Rule`. Never go through `runTool`. Not selected by default.
- [ ] **On Windows nothing is folder-based Trash:**
  - The `inTrash` logic (`junk.go:1018/1066`) must be false.
  - `userTrashDir()` must never be canonicalised as `""`, because `canonical("")` is the working directory.
- [ ] **Windows tests:**
  - Seed the bin with a file (`recycle`), then scan: the item reports at least that size.
  - Seed another file between scan and clean: clean reports `changed` and the bin keeps both files.
  - A matching clean empties it.
  - Ordering: `sys.trash` runs before an item with `Trash: true` in the same pass.

### Task 13: Tool commands on Windows

**Files:** `proc_windows.go`, `tools.go`, `windows_rules.go`.

- [ ] **`findTool`:** look in Windows tool folders for `name` plus each extension in `PATHEXT`; no exec-bit check. The folders are `%LOCALAPPDATA%\pnpm`, `%APPDATA%\npm`, `%ProgramFiles%\nodejs`, `%ProgramFiles%\Go\bin`, `%USERPROFILE%\scoop\shims`, `%LOCALAPPDATA%\Microsoft\WinGet\Links`, `%USERPROFILE%\.bun\bin` and `%USERPROFILE%\.cargo\bin`.
- [ ] **`runToolCmd`:**
  - `CREATE_NO_WINDOW`.
  - `PATH` = the tool's folder `;` node's folder when found `;` `%SystemRoot%\System32` `;` `%SystemRoot%`. Other variables are inherited.
  - `Start`, then assign the process to a Job Object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`.
  - `Cancel` terminates the job, and the job is closed after `Wait`. This keeps the macOS semantics: the tree is killed on timeout or cancel and after exit.
- [ ] **Windows command variants:** `goModCleanup` and `pnpmPrune` with Windows `Busy` patterns, matched against command lines with `\` normalised to `/` and lower-cased, e.g. `/go.exe`, `gopls`, `pnpm.cjs`, `/pnpm.exe`. Referenced by `dev.go-mod` and `dev.pnpm-store` in `windowsJunkRules`. `brew` and `xcrun` don't exist on Windows.
- [ ] **Windows tests:**
  - A fake `slowtool.cmd` (`ping -n 30 127.0.0.1`) is killed with its children at the timeout, and no `ping.exe` survives.
  - `findTool` finds `x.cmd` through PATHEXT.

### Task 14: AI tools and skills on Windows

**Files:** `aitools.go`, `skills.go`, `skills_update.go`.

- [ ] **AI tool running checks** (`aitools.go:190`) use `psCommands`. Match Windows command lines too (`node.exe …\@anthropic-ai\claude-code\cli.js`, `codex.exe`). Untagged table tests use Windows-style sample lines.
- [ ] **`LinkSkillCopy`** returns `errUnsupported` on Windows. **Skill upgrades** never leave a half-made link: when `os.Symlink` fails with `ERROR_PRIVILEGE_NOT_HELD`, report `unsupported` and keep the old copy.
- [ ] **sqlite3:** on Windows, use `exec.LookPath("sqlite3")`; when absent, the OpenCode and CC Switch sources report unavailable as they do today on failure.
- [ ] **Exec bits** in `skills_update.go`: harmless on Windows, but tree hashing must not depend on `Mode().Perm()` there. Use the recorded git mode.
- [ ] **Run-level acceptance (CI, `junk_windows_test.go`):**
  - **Seeded e2e clean:**
    - Build a fake profile in the sandbox with caches for Chrome/npm/pip/Electron.
    - Add the protected fixtures from Tasks 7–10: junction out, `Application Data`, offline file, `OneDrive` folder, `NTUSER.DAT`, `AppData\Roaming\Microsoft\Credentials\x`, `AppData\Local\Cleanzo\WebView2\x`, `AppData\Local\Packages\x`.
    - Hold a file open with share-delete.
    - Run a fake process with `--user-data-dir` pointing at the Chrome folder.
    - Run `ScanJunk` and then `CleanJunk` with every item selected.
    - Assert: every protected fixture is intact; the in-use Chrome cache is untouched; the other caches are gone.
  - **Real scan:** `TestJunkScanReport` gains assertions. Every candidate is inside the profile or the accepted temp folder, and none is protected. CI runs it with `CLEANZO_SCAN_REPORT=$RUNNER_TEMP\report.json` and uploads the report as an artifact.

---

## Run D — Status page and file pages

### Task 15: Windows status page and `Info()`

**Files:** `native_windows.go`, `status_windows.go`, `monitor.go` (pure helpers only).

- [ ] **`realStatusSource` on Windows:**
  - **`procs`** (`procReading`): from `processSnapshot`, adding `GetProcessMemoryInfo` (`PROCESS_MEMORY_COUNTERS_EX2.PrivateWorkingSetSize`, falling back to `WorkingSetSize`) and `GetProcessTimes`. CPU times are in ns.
  - **`apps`** (`workspaceApp`):
    - `Regular` processes have a visible, unowned, non-tool top-level window (`EnumWindows`).
    - `Path` is the exe path, `BundleID` is empty, and `Name` is the product name.
    - Windowless processes are attributed to the nearest windowed ancestor through `PPid`. The ancestor's creation time must precede the child's, which guards against PID reuse.
  - **`memory`:** from `GlobalMemoryStatusEx` + `GetPerformanceInfo`, through the untagged pure function `memoryFromWindows(...)`. No binding type changes.

    | Field | Value |
    |---|---|
    | `Total` | `ullTotalPhys` |
    | `Used` | `Total − ullAvailPhys` |
    | `Wired` | kernel non-paged pool |
    | `App` | `Used − Wired` |
    | `Compressed` | 0 |
    | `Cached` | `SystemCache` |
    | `SwapTotal` / `SwapUsed` | page file size and in-use (commit limit − physical, and commit total − physical used, floored at 0) |
    | `Pressure` | 4 when available memory < 5 % or commit > 95 % of the limit; 2 when < 10 % or > 85 %; else 1 |
  - **`cpu`:** `GetSystemTimes`. `busy` = kernel − idle + user; `total` = kernel + user.
  - **`battery`:** `GetSystemPowerStatus`. nil when `BatteryFlag == 128`; `Cycles`, `MaxCapacity` and `Condition` stay unknown.
  - **`disk`:** `diskStat`.
  - **`terminate`:** post `WM_CLOSE` to the process's top-level windows. Refuse `explorer.exe` and Cleanzo itself. The existing quit timeout logic is reused.
- [ ] **`nativeMachineInfo` on Windows:**
  - CPU name from `HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0\ProcessorNameString`.
  - Total memory.
  - Logical cores.
  - OS version string `Windows 11 <DisplayVersion> (<CurrentBuild>)` from `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`. Build ≥ 22000 means Windows 11, whatever `ProductName` says.
  - Boot time = now − `GetTickCount64`.
- [ ] **Tests:**
  - Untagged: `memoryFromWindows` table tests, and the pressure thresholds.
  - Windows (CI):
    - `Total` matches `GlobalMemoryStatusEx`.
    - The process list contains the test process with memory > 0.
    - CPU ticks increase.
    - `Info()` reports a non-empty CPU name and an OS version starting with "Windows".
    - `terminate` refuses `explorer.exe`.

### Task 16: Disk, Full Scan, Large Files and folder sizes

**Files:** `fs_windows.go`, `main.go` (platform hooks only), new `walk_windows.go` if useful.

- [ ] **`duSize` on Windows:** a Go walk with a context deadline (reuse `measureChecked` semantics). It skips `linkModes` entries and other volumes.
- [ ] **Large Files on Windows:** a `filepath.WalkDir` replacing `find -x … -size +Nk`, with the same skips. Rows inside AppData are listed but `removable: false` (Task 8).
- [ ] **`fullScanTargets` on Windows:** the home entries, with `AppData` expanded one level the way `Library` is. No system roots.
- [ ] **Windows tests:** a junction inside a scanned folder is not followed by `duSize`, Large Files or Full Scan; sizes of a seeded tree are exact.

### Task 17: Reveal and photos

**Files:** `main.go`, `fs_windows.go`.

- [ ] **`RevealInFinder` on Windows:**
  - Use mygo's reveal (`explorer.exe /select,` — `internal/windows/system.go:282`) through its public Shell API.
  - `~\` and `~/` inputs are expanded with `expandHome`.
  - The bound name is unchanged; the UI label changes in Task 19.
- [ ] **Photos on Windows:**
  - No sips: HEIC/HEIF files are not candidates.
  - Images over the pure-Go decode limit are skipped with their existing "too large" handling.
  - Never open files with offline/recall attributes, since opening hydrates cloud files.
  - Skip `OneDrive*` trees.
  - Roots stay `home\Pictures` and `home\Desktop`.
- [ ] **Windows tests:**
  - An offline-attribute JPEG in Pictures is not read. Check through an injected open hook, or check that the file's attributes are unchanged.
  - Duplicates among normal JPEGs are found.

---

## Run E — Frontend and packaging

### Task 18: `src/platform.ts`

**Files:** create `src/platform.ts`; modify `src/shared.ts`, `src/react/large/format.ts`, `src/react/fullscan/format.ts`, `src/react/ai/format.ts`, `src/react/apps/LeftoversTab.tsx`, `src/react/settings/settings-helpers.ts`, `src/react/list/type-hint.ts`, `src/react/list/selection.ts`, `src/react/ai/PartRow.tsx`.

- [ ] **Platform detection:** `isWindows()` reads `window.mygo?.runtime().platform === "win32"`. Tests can override it with `setPlatformForTest`.
- [ ] **Path helpers** replacing the copies:
  - `shortHome(path)`: `^/Users/[^/]+` on macOS, `^[A-Za-z]:\\Users\\[^\\]+` on Windows.
  - `pathSep()`, `splitPath`, `baseName`, `dirName`.
  - `isWithin(child, ancestor)`: case-insensitive on Windows.
- [ ] **Type hints:** add `.exe`, `.msi`, `.msix`, `.lnk`, `.zip`, `.7z`, `.iso`, `.vhdx`.
- [ ] **Tests:** `tests/platform.test.ts` covers both platforms. Existing macOS assertions are unchanged.

### Task 19: i18n for Windows

**Files:** `src/i18n.ts`, `tests/language.test.ts` (or a new `tests/i18n-windows.test.ts`).

- [ ] **Lookup order in `t()`, `ruleName()`, `ruleDesc()` on Windows:**
  1. `key@win` overrides.
  2. A glossary, applied longest first. zh: 这台 Mac→这台电脑, 废纸篓→回收站, 访达→文件资源管理器, macOS→Windows, 系统设置→Windows 设置. en: "this Mac"→"this PC", Trash→Recycle Bin, Finder→File Explorer, macOS→Windows, System Settings→Windows Settings.
- [ ] **Explicit `@win` overrides** for Full Disk Access, Dock, Activity Monitor, `/Applications`/`.app`/Library item kinds, the memory terms (wired/compressed/pressure/swap → kernel/—/load/page file), and descriptions of reused rule IDs that cite macOS paths.
- [ ] **Test:** in Windows mode, every value of both languages contains none of 废纸篓, 访达, Finder, Trash, macOS, `\bMac\b`, 系统设置, System Settings, Dock, 活动监视器, Activity Monitor, `~/Library`, `.app`, 完全磁盘访问, Full Disk Access. Allowlist the few keys that are never shown on Windows, and list them in the test.

### Task 20: Windows UI switches

**Files:** `src/react/clean/CleanPage.tsx`, `src/react/settings/SettingsPage.tsx`, `src/react/status/StatusPage.tsx`, `src/react/status/format.ts`, `src/react/skills/*`, `src/style.css`.

- [ ] **Hide on Windows:** the Full Disk Access banner (`CleanPage.tsx:654-659`) and Settings section (351-376); the skills "link copy" action.
- [ ] **Status page:**
  - Header `Windows ${osVersion}` (`StatusPage.tsx:388`).
  - The memory card shows Windows labels.
  - Compressed is hidden when 0 on Windows.
  - The swap note refers to the page file (`format.ts:92`).
- [ ] **Fonts:** `"Segoe UI Variable", "Microsoft YaHei UI"` are added to the stack after the Apple fonts.
- [ ] **Tests:** in `status-react`/`clean-react`/`settings-react`, Windows-mode cases sit alongside the unchanged macOS ones.

### Task 21: Packaging and the WebView2 check

**Files:** `mygo.config.ts`, `package.json`, `main.go`.

- [ ] **`mygo.config.ts`:** add `windows: {}` with a comment naming `certificate` (+ `MYGO_WINDOWS_CERTIFICATE_PASSWORD`) and `signCommand` for later signing. No other change.
- [ ] **`package.json`:** `"build:windows": "mygo build -platform windows/amd64"`.
- [ ] **WebView2 check:** on Windows, when `mygo.WebViewAvailable()` is false at startup, show a native message dialog (mygo's task dialog). It offers to open `https://developer.microsoft.com/microsoft-edge/webview2/` and then exits. macOS is untouched.
- [ ] **Verify on the Mac:** `bun run build -- -platform windows/amd64 -skip-build-command` (after `bun run build:web`) produces `build/windows-amd64/Cleanzo Setup <v> amd64.exe`.

### Task 22: CI smoke tour

**Files:** `.github/workflows/ci.yml`, `tests/smoke/` (new; not part of `test:web`).

- [ ] **Job `windows-smoke`:** debug build → `/S` install → launch. Then visit every sidebar view (status, clean incl. a scan without cleaning, aitools, skills, fullscan, large, photos, apps, settings), wait for each view to settle, and save one PNG per view. Collect console errors, then upload the PNGs and a JSON of the errors. The job fails on any console error.
  - **If the Run A probe reached CDP:** Playwright `chromium.connectOverCDP("http://127.0.0.1:9222")`.
  - **Otherwise:** an env-gated smoke mode (`CLEANZO_SMOKE_DIR`). After load, the Go side switches views through the page, using mygo's JS evaluation on the window, then saves `Window.CapturePage()` images. With the variable unset, behaviour is unchanged.
- [ ] **Light and dark:** run the tour once in light and once in dark mode, using a forced theme or the system setting on the runner.

---

## Run F — Apps page and docs

### Task 23: Installed apps and uninstall on Windows

**Files:** create `apps_windows.go` and `apps_darwin.go`; modify `main.go`.

- [ ] **Platform split:** the bound `ListApps`, `AppUninstallDetail`, `UninstallApp` and `ScanOrphans` call platform helpers. `apps_darwin.go` calls the existing macOS code (move nothing else).
- [ ] **List** (`listInstalledApps()`):
  - Read `HKCU` and `HKLM` `Software\Microsoft\Windows\CurrentVersion\Uninstall`, plus the `WOW6432Node` view, using `windows/registry`.
  - Skip entries that have `SystemComponent=1`, have `ParentKeyName`, or lack `DisplayName` or `UninstallString`.
  - Map to `AppInfo`: name, version, publisher, `EstimatedSize` KiB → bytes, and `InstallLocation` for display.
  - The app's ID is an opaque key ID (`hkcu|hklm[:wow64]\<subkey>`).
  - Add optional fields to `AppInfo` only if needed, and regenerate bindings.
  - Running = some snapshot exe is inside `InstallLocation`.
- [ ] **Uninstall** (`UninstallApp(id)`):
  1. Look the key up again by ID; it must still exist.
  2. Refuse if the app is running.
  3. Parse `UninstallString`, preferring `QuietUninstallString` only when the user asked for it (not in v1): a quoted exe plus args, or `MsiExec.exe /I{GUID}` → `/X{GUID}`.
  4. Launch it with `ShellExecuteExW`; UAC appears as needed.
  5. Return "launched".
  6. Never call `trashItem` or `removeAll` on `InstallLocation` or anything derived from it.
- [ ] **`ScanOrphans`:** returns an empty list on Windows (the tab is hidden).
- [ ] **Tests:**
  - Untagged: `UninstallString` parser table, covering quoted paths with spaces, unquoted paths, MsiExec `/I` → `/X`, `rundll32` forms, and garbage.
  - Windows (CI):
    - Listing returns at least one entry on the runner.
    - With a test-only registry root (an injectable base key under `HKCU\Software\CleanzoTest`), `UninstallApp` launches the fake uninstaller (`cmd /c echo > marker`).
    - A recording `trashItem` sees no call.

### Task 24: Apps page flow on Windows

**Files:** `src/react/apps/*`, `src/i18n.ts`.

- [ ] **Hide on Windows:** the Leftovers tab, and the related-files list in the uninstall dialog.
- [ ] **Uninstall dialog:** it explains that the app's own uninstaller will open. After the "launched" result it shows "Uninstaller opened — refresh when it finishes", with a refresh button.
- [ ] **Store apps:** a footer link opens `ms-settings:appsfeatures`.
- [ ] **Tests:** Windows-mode cases in `apps-react.test.ts`; the macOS cases are unchanged.

### Task 25: Docs

**Files:** `README.md`, `docs/RELEASING.md`, `CHANGELOG.md`.

- [ ] **README:**
  - Windows install: Windows 10/11 x64, WebView2 Runtime (included in Windows 11).
  - SmartScreen on unsigned builds: "More info → Run anyway".
  - Per-user install, and what is not supported on Windows.
  - Fix the stale dmg names and the `src/junk.ts`/`src/settings.ts` references.
- [ ] **RELEASING:**
  - Windows section: on the Mac, `MYGO_UPDATER_PRIVATE_KEY=… bun run build:windows`.
  - Upload `Cleanzo Setup <v> amd64.exe`, `cleanzo-<v>-windows-amd64.tar.gz`, the `.delta` files and `update-windows-amd64.json` to the same GitHub release as the macOS files.
  - The key never goes to CI.
- [ ] **CHANGELOG:** an "Unreleased" section describing Windows support.

---

## Execution batches (Codex runs)

| Run | Tasks | Done when |
|---|---|---|
| A | 1–6 | Mac suite green with the unchanged test list; Windows CI `windows` job green; probe results recorded below |
| B | 7–10 | Windows adversarial tests green in CI |
| C | 11–14 | Mac table/expansion tests green; Windows seeded e2e clean leaves every protected fixture intact; real-scan assertions pass |
| D | 15–17 | Windows readings and walker tests green |
| E | 18–22 | bun Windows-mode tests green; one screenshot per view, light and dark, zero console errors; installer builds on the Mac |
| F | 23–25 | Parser tests green; Windows uninstall test proves InstallLocation is never trashed; final smoke screenshots reviewed |

## Probe results

_To be filled in by the reviewer after Run A's first CI run: WebView2 Runtime present on windows-latest (yes/no), CDP on :9222 reachable (yes/no), screenshot notes._
