# Audit leftovers (2026-11-11)

**Spec:** `outputs/audit-2026-11-11/after-fixes.md` (untracked: it exists only in the maintainer's main checkout), section "没有改、留给你决定的". The maintainer asked for each item to be decided and then fixed. The decisions are the tasks below; an item not listed here was decided as "leave it".

**Goal:** close the items the first round left open, on the same branch (`fix/audit-2026-11-11`).

## Global Constraints

- **Workspace.** Work only inside your own git worktree. Never push, never switch or touch another branch, never edit the main checkout. Do not dispatch subagents.
- **Files.** Edit the files your task names, plus their tests. If a fix truly needs a line elsewhere, make the smallest edit and name it in your report.
- **Test first.** Each behaviour fix gets a test that fails before the fix, for the reason the item describes. Run it, see it fail, then fix. Deleting dead code needs no new test. If a fix cannot be tested (it needs live AppKit, a real Windows machine), say so in the report instead of writing a test that asserts nothing.
- **Contract.** `src/mygo.ts` is generated from the Go types and bound methods. Change it only where your task says to remove a field. When a Go change flows into it, run `bun install --frozen-lockfile && bun run generate` and commit the regenerated file in a commit of its own, last, so it can be redone at merge.
- **Strings.** A new user-visible string goes into `src/i18n.ts` in both `zh` and `en` (same key set, same placeholders). Native strings read by Go live in `src/native-i18n.json`, both languages.
- **Skip rather than force.** If an item is wrong, unsafe, or grows far beyond its description, leave the code alone and record the item and the reason in your report.
- **Comments.** Follow the file: Go comments in English, TS/TSX comments in Chinese where the file's are. A comment says what the code does or why, in the present tense. Never mention an audit, a reviewer, or what the code used to do.
- **Commits.** One commit per item or small group, in the style of `git log` (`fix(scope): …`, `perf(scope): …`, `refactor: …`, `test: …`). No attribution trailers.
- **Before reporting.**
  - Go: `test -z "$(gofmt -l .)"`, `go vet ./...`, `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...`, `GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go vet ./...`, the tests you touched, then `go test -count=1 ./...` once (about four minutes; other agents load the machine, so re-run a timing failure alone before treating it as real).
  - Web: `bun install --frozen-lockfile`, `bun run typecheck`, `bun run test:web`, `bun run build:web`.
- **Tests never touch the real home.** Use the existing helpers (`setHome`, `t.TempDir`, `fakeTool`, `stubUsage`, the fake GitHub server).
- **Report.** Write the full report to `.superpowers/sdd/2026-11-11-audit-leftovers/task-N-report.md` inside your own worktree (that folder is git-ignored): per item, what you changed, the failing-then-passing test, or why you skipped it; then the verification output. Reply with only: status (DONE / DONE_WITH_CONCERNS / BLOCKED / NEEDS_CONTEXT), the worktree path and branch, the commit range, one line of test results, and your concerns.

## Task 1: Upgrades, uninstall and the status reads (Go)

**Files:** `appupdates.go`, `appupdates_darwin.go`, `apps_darwin.go`, `growth_eval.go`, `growth*.go` where an item needs it, `main.go` (only the types named in item 5), `status_darwin.go`, `tools.go` / `proc_darwin.go` only if item 1 needs a variant of the command runner, `src/dev/mock-mygo.ts` for the removed fields, and their tests.

1. **Quitting Cleanzo during a Homebrew upgrade does not break brew.** Today brew writes to pipes Cleanzo owns; when Cleanzo exits (Cmd+Q, Quit in the menu) brew is not killed but dies on its next write. Run the upgrade's brew with its output going to a file Cleanzo reads when brew ends, in its own process group, so it finishes whether or not Cleanzo is still there. While Cleanzo lives, the ten-minute timeout still stops the whole group, and a failed upgrade still returns brew's last output line and the full command. Correct the comment in `upgradeApp` that says quitting can break brew.
2. **A good upgrade is not reported as failed.** For an app whose build cannot be read (the shapes where `caskState` falls back and can answer "update" even when current), the success check after brew exits zero also accepts a bundle whose short version or build changed from what it was before the upgrade. A bundle that did not change is still `brew-failed`.
3. **Uninstall removes what its confirm listed.** Go keeps the paths of the last uninstall detail it returned for an app (bounded, dropped after the uninstall). `uninstallApp` on macOS still recomputes the paths, and removes only those the detail listed; a path that appeared since is left in place and not counted. With no detail on record it behaves as today.
4. **The battery read cannot outlive its timeout.** `statusBattery` sets a wait delay on its command, so a child that keeps the output open does not hold a refresh goroutine.
5. **Fields nothing reads leave the contract:** `GrowthDelta.Days`, `FullScanEvent.Scanned`, and `SystemInfo.OS`, `Arch`, `GoVersion`. Remove them from Go and from `src/dev/mock-mygo.ts`, regenerate `src/mygo.ts`, and confirm with grep that no file under `src/` or `tests/` reads them.

## Task 2: Paths, tools, skills and the smoke tour (Go)

**Files:** `paths.go`, `tools.go`, `junk.go`, `aimodels.go`, `skills.go`, `skills_update.go`, `smoke.go`, `tests/smoke/preload.js`, `tests/smoke/hook.test.ts`, `src/dev/mock-mygo.ts` for the removed fields, and their tests.

1. **The temp root on macOS is one of the system's.** Accept a resolved temp root only under `/private/var/folders`, `/private/tmp` or `/private/var/tmp`, keeping the refusals already there. Any other root (`/Users`, `/Applications`, `~/Projects`, `~/tmp`) hides the temp rule and nothing else.
2. **A tool command checks the installer folders too.** In `cleanTool`, a command whose estimated roots overlap the folders a clean left unwalked (`env.unwalked`) is refused as blocked, as it would be when the walk had run and found an installer there.
3. **The smoke tour visits the AI space page.** Add `aispace` to `smokeViews` and make `visit` in `tests/smoke/preload.js` handle it the way it handles the other scan pages (scan only, nothing is moved); update the count `smoke_test.go` pins and the cases in `tests/smoke/hook.test.ts`. Verify on macOS with a real run: `node_modules/.bin/mygo build -o /tmp/<your-own-folder> -sign=- -skip-notarize -skip-dmg`, then `HOME=<temp home with a few seeded caches> CLEANZO_SMOKE_DIR=<temp out> perl -e 'alarm 330; exec @ARGV' /tmp/<your-own-folder>/darwin-arm64/Cleanzo.app/Contents/MacOS/Cleanzo`; expect exit 0, `errors.json` equal to `[]`, and 22 PNGs. Never build into the repository's `build/` folder. (The screenshots may be unpainted if the screen is locked; the exit code and `errors.json` are what count.)
4. **Skills, three small ones.** After a folder is found identical to its source, record that tree so the next source change does not read as "modified"; `skillPathRef` matches Windows home folders with a space in the name; the skills-cli lock check compares the folder the way `within` does on Windows.
5. **Fields nothing reads leave the contract:** `JunkItem.Skipped` and `AIModelEntry.Kind` and `Blobs` (with `aiModelKindManifest` if nothing else uses it). Remove them from Go and from `src/dev/mock-mygo.ts`, regenerate `src/mygo.ts`, and confirm with grep that no file under `src/` or `tests/` reads them.

## Task 3: Pages and test helpers (web)

**Files:** `src/react/**`, `src/shared.ts`, `src/i18n.ts`, `src/ui/components/button.tsx`, `tests/**` except `tests/smoke`, `tests/tsconfig.json`. Do not edit `src/dev/mock-mygo.ts` or any Go file.

1. **The AI space toast speaks the user's language.** The first failure it shows is `path: code`; show the path as it is and the code through `errText`, with a text for `unreadable` in both languages (zh: "读不出来", en: "could not be read"). An unknown code or an OS error message is shown as it is.
2. **One border for ghost buttons.** The React ghost variant in `src/ui/components/button.tsx` uses the same border token as `.btn.ghost` in `src/style.css` (`--border-strong`, the one the stylesheet says controls need for contrast), and its comment says so.
3. **One page-test helper.** `tests/page-harness.ts` and `tests/list-page-harness.ts` become one helper with one API; port the tests of one to the other. Each test's setup runs inside its own try/finally, so a failing first step restores the globals, API overrides and language it changed. Every test that passed before passes after, and none is deleted.
4. **Small page fixes.** The Codex crash-dump banner is read again each time the AI tool view is shown, not only on mount and scan; a failed folder rename in Settings puts the saved name back even when the input lost focus because the window did; the Leftovers expansion's request token gets a test of its own (a first listing that arrives after a re-expand is dropped).
5. **The tests' type check covers `.tsx`.** `tests/tsconfig.json` matches `tests/**/*.tsx` too.
