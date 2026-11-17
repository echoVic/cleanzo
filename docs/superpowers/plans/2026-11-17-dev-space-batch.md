# Dev space batch implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Each task is one implementer in its own git worktree; the controller reviews, cherry-picks and verifies.

**Goal:** The junk scan and the AI tools pages show five kinds of space they miss today: build folders of idle projects, old toolchain versions, the caches and logs of four apps, a tool that cannot run, and the Orca agent.

**Architecture:** Everything extends flows that exist. Project rows are generated in `expandItems` beside the custom-folder rows; toolchain and app rows are catalog rules; the tool problem is two fields on `JunkItem`; Orca is a fourth entry in the `aiTools` table. Task 0 lands the shared contract first, then Tasks 1 to 4 run in parallel from that commit.

**Tech Stack:** Go 1.27 (no new dependency), React 19 + TypeScript, Bun, MyGo 0.9.0.

**Spec:** `docs/superpowers/specs/2026-11-17-dev-space-batch-design.md`. Read it before your task: it holds the evidence, the recognition table, the app rule table and every ruling. Where this plan and the spec differ, the spec wins; report the difference.

## Global Constraints

- **Workspace.** Work only inside your own git worktree. First run `git reset --hard <BASE>` with the commit your brief names. Never push, never switch or touch another branch, never edit the main checkout. Do not dispatch subagents.
- **Files.** Edit the files your task names, plus their tests. If the work truly needs a line elsewhere, make the smallest edit and name it in your report. Tasks 1 to 4 run at the same time: stay out of the other tasks' functions.
- **Contract.** `JunkItem.IdleDays`, `JunkItem.Problem`, `JunkItem.ProblemDetail`, `Rule.Keep` and `groupProjects` exist after Task 0. Do not rename them or add fields to `JunkItem`, `CleanResult` or `Rule`. `src/mygo.ts` is generated; only Task 0 regenerates it.
- **Platforms.** macOS behaviour only, except Task 3. Windows must keep building and passing: nothing new is offered there. In untagged `.go` files a `"/"` join or a `"~/"` literal needs a `// slash-ok:` comment (see `testenv_test.go`), and exported declarations stay in untagged files.
- **Safety.** Nothing is offered that the spec does not list. Every new path goes through the existing checks (`env.collect` / `env.collectDir`, claims, `safeToClean`, the in-use guard); do not bypass or loosen one. If a check refuses something the spec says to offer, stop and report it instead of working around it.
- **Tests never touch the real home and never clean real files.** Use `testEnv`, `setHome`, `t.TempDir`, `writeFile`, `mustMkdir`, `makeApp`, `fakeTool`, `stageTools`, `aiFixture`. Git repositories in tests are created in the temporary home with a real `git init`; skip the test when no `git` is installed.
- **Test first.** Each behaviour gets a test that fails before the code, for the reason the item describes. Run it, see it fail, then write the code.
- **Strings.** Every user-visible string goes into `src/i18n.ts` in both `zh` and `en` (same keys, same placeholders). Rule names and descriptions use `rule.<id>.name` / `rule.<id>.desc`; AI parts use `ai.part.<id>.{name,desc,lose}`. Strings must pass `tests/i18n-windows.test.ts` (no `~/Library`, `.app`, `Mac` wording in keys shared with Windows; a macOS-only rule's keys are fine). Add your keys as one block right after the anchor your task names, so parallel tasks do not collide.
- **Comments.** Go comments in English, TS/TSX comments in Chinese where the file's are. Present tense, saying what the code does or why. Never mention a plan, a spec, a review or what the code used to do.
- **Commits.** One commit per item or small group, in the style of `git log` (`feat(junk): …`, `fix(junk): …`, `test: …`). No attribution trailers.
- **Before reporting, run all of:**
  - `test -z "$(gofmt -l .)"`, `go vet ./...`, `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...`, `GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go vet ./...`
  - the tests you touched, then `go test -race -count=1 -skip '^TestNativeSysctlRawProbes$' ./...` once (about three and a half minutes; other agents load the machine, so re-run a timing failure alone before treating it as real)
  - `bun install --frozen-lockfile`, `bun run typecheck`, `bun run test:web`, `bun run build:web`
- **Report.** Write the full report to `.superpowers/sdd/2026-11-17-dev-space-batch/task-N-report.md` inside your own worktree (git-ignored): per item, what you changed, the failing-then-passing test, anything you skipped and why, then the verification output. Reply with only: status (DONE / DONE_WITH_CONCERNS / BLOCKED / NEEDS_CONTEXT), the worktree path and branch, the commit range, one line of test results, and your concerns.

## Execution

| Wave | Task | Runs | Owns |
|---|---|---|---|
| 1 | 0 Contract | alone | `JunkItem`, `Rule`, group constant, `src/mygo.ts` |
| 2 | 1 Project rows | parallel | `projects.go`, the project block in `expandItems`, the group and row in the clean page |
| 2 | 2 Catalog rules | parallel | `rules.go` table entries, `toolchains.go`, `Keep` handling, partition caches in `addApp` |
| 2 | 3 Tool problem row | parallel | the command branch of `planItem`, `tools.go`, the problem row in the clean page |
| 2 | 4 Orca | parallel | `aitools.go`, the AI tools sidebar and pages |
| 3 | Review, merge, verify | controller | cherry-picks, `bun run generate` if needed, real-scan comparison |

---

## Task 0: Contract

**Files:** `junk.go` (`JunkItem`), `rules.go` (`Rule`, group constants), `src/mygo.ts` (generated), `src/dev/mock-mygo.ts` only if the type check needs it.

1. Add to `JunkItem`, after `DetailsDropped`:
   ```go
   IdleDays      int    `json:"idleDays,omitempty"`      // project rows: whole days since anything in the project changed
   Problem       string `json:"problem,omitempty"`       // why the row cannot be cleaned now: "tool-failed"
   ProblemDetail string `json:"problemDetail,omitempty"` // the tool's last output line, for Problem "tool-failed"
   ```
2. Add `groupProjects junkGroup = "projects"` after `groupDev`.
3. Add to `Rule`, after `Reserve`:
   ```go
   // Keep names the entries of the rule's folders that are never offered. ok
   // false means they cannot be told apart from the rest, and the rule then
   // offers nothing.
   Keep func(env junkEnv) (names []string, ok bool)
   ```
4. Run `bun install --frozen-lockfile && bun run generate`; commit the Go change and the regenerated `src/mygo.ts` as two commits.

Nothing reads the new fields yet. All checks in Global Constraints must pass.

---

## Task 1: Project rows

**Files:** create `projects.go` and `projects_darwin_test.go`; `junk.go` (one block in `expandItems`, after the custom folders and before the app caches; the clean path's re-expansion); `src/react/clean/` (group order, the project row, its explanation), `src/i18n.ts` (anchor: after `"junk.group.custom"`), web tests, `src/dev/mock-mygo.ts` (one sample project row).

**Consumes:** `JunkItem.IdleDays`, `groupProjects` (Task 0); `env.collect`, `claimSet`, `itemSpec`, `measureReclaimable` through the normal candidate path.

Behaviour, all of it specified in the spec's Part A:

1. **Discovery.** From the home folder, six levels deep, skipping what Part A lists. A folder with a candidate name is recorded and never entered. The two agent-worktree places are entered: `~/.codex/worktrees/*/*` and `<project>/.claude/worktrees/*`.
2. **Recognition.** Implement the table exactly. Inside a repository each candidate must be ignored by git and not tracked; outside one, only the first four table rows apply.
3. **Project and idleness.** The project is the nearest ancestor holding `.git` (a folder or a file), else the folder holding the marker. Idle is 14 days, a constant, measured as the spec says with `changedAt`. A project that is not idle yields no row.
4. **Git.** One `git check-ignore --stdin` per repository, with the environment, option and five-second limit the spec gives. Resolve git through `env.findTool`. If the only git is `/usr/bin/git` and `xcode-select -p` fails, treat git as missing. Git missing or failing skips the repository whole. Make the runner replaceable for tests, as `psCommands` and `aiProcesses` are.
5. **Rows.** One `itemSpec` per project: `ID: "project:" + path`, `Group: groupProjects`, `Name` the folder name, `Size: -1`, `Default: false`, `Trash: true`, `IdleDays` set; each build folder added with `spec.addRoot` and `env.collect`, then claimed. Rows are emitted in path order. A project whose candidates were all refused by the checks yields no row.
6. **Scan and clean.** Rows respect `DisabledGroups` and `Ignored` through `emit`. During a clean, discovery runs only when a selected row's ID starts with `project:`; a selected project row whose project is no longer idle, or whose folder is no longer recognised, is not cleaned and reports the existing `changed` reason.
7. **Other platforms.** On anything but macOS the block produces no rows.
8. **Page.** The group appears after 开发工具 with the label 开发项目 / Dev Projects. A project row shows its name, the path, and "N 天没有改动" / "No changes for N days". The expanded box explains: what it holds (rebuildable build output and installed dependencies), what happens (the next build or install recreates it), how (moved to the Trash). The group can be turned off wherever the other groups can.

Tests to write first (Go, in a temporary home):

- Each table row: recognised with its marker; rejected without it.
- A repository with a tracked `build`, an ignored `build` and an untracked, unignored `out`: only the ignored one is offered.
- Outside a repository: `node_modules` with `package.json` is offered; `dist` with `package.json` is not.
- An idle project yields a row with `IdleDays`; touching a source file, or `.git/index`, removes the row.
- A nested package (`repo/web/node_modules`) lands in the repository's row.
- A worktree under `.codex/worktrees/x/y` is its own project; a folder under another hidden folder is not found.
- Git missing, git failing, and the `/usr/bin/git` stub case each skip the repository.
- A candidate name inside a recognised candidate is never visited (stage `node_modules/pkg/dist`).
- Clean: a selected project row moves its folders to the Trash and leaves the sources; a project touched between scan and clean is left alone and reports `changed`; a clean with no project row selected does not walk (assert through the replaceable runner or a counter).
- Sizes are clone-aware (reuse `cloneFile` from `clones_darwin_test.go`).

Web tests: the group renders in order, the row's subtitle, the explanation, the row is not preselected.

---

## Task 2: Catalog rules

**Files:** `rules.go` (new table entries only), create `toolchains.go` and `toolchains_darwin_test.go`, `junk.go` (`Keep` in the rule loop of `expandItems`; the partition layout in `addApp`), `junk_rules_darwin_test.go` and `junk_darwin_test.go` as needed, `src/i18n.ts` (anchor: after the last existing `rule.review.*` key for the review rules, after the last `rule.dev.*` key for the dev rule, and a new block after the last `rule.browser.*` key for the app rules), `src/react/clean/explanations.ts` only if a new rule needs an entry there.

**Consumes:** `Rule.Keep` (Task 0).

1. **`Keep`.** In the rule loop, when `r.Keep != nil`: call it once; `ok == false` emits nothing for the rule; otherwise candidates whose base name is in `names` are dropped before claiming (the kept entries are still claimed, so no later rule or app row offers them).
2. **`review.node-versions`** (spec Part B): entries of `.nvm/versions/node`, `Group: groupReview`, `Trash: true`, not default, each version selectable on its own through the existing detail mechanism (look at how `review.device-support` lists folders; add a detail kind only if none fits). `Keep` resolves nvm's `default` alias from `~/.nvm/alias`: an exact version, a prefix such as `22` (highest installed match), `node` or `stable` (highest installed), `lts/*` and `lts/<name>` by following the alias files. Anything else, a missing file, or no installed match gives `ok == false`.
3. **`review.rust-toolchains`** (spec Part B): entries of `.rustup/toolchains`, same flags. `Keep` reads `~/.rustup/settings.toml`: `default_toolchain`, plus every value under `[overrides]`. Unreadable settings or a missing `default_toolchain` give `ok == false`. Parse the two things needed by hand; do not add a TOML dependency.
4. **App rules** (spec Part C table): `app.lark`, `app.doubao-work`, `app.doubao`, `app.aime`, `Group: groupApps`, not default, deleted (no `Trash`), `Owners` the bundle ids in the spec, placed before the generic app rows take effect (catalog rules claim first). Use `entriesOf` for `sdk_storage/log` and `sandbox_runtime/.cache`, `matched` for the `CacheStorage` patterns. `Reserve` nothing: the generic row keeps the rest of each app's folder.
5. **Partition caches.** The generic per-app rows also take the folders in `chromiumCacheDirs` under `Application Support/<app>/Partitions/*/`. Service Worker folders are not added there.
6. **`dev.pnpm-store-home`** (spec Part D): entries of `.pnpm-store`, `Group: groupDev`, not default, `Trash: true`, `MinAge: 30 * 24 * time.Hour`.
7. **Strings.** Names and descriptions for the seven rules, zh and en. The Node and Rust descriptions say what is lost: the version's global packages, and that a project pinning the version needs it installed again. The app descriptions say what is and is not removed (logs, the sandbox's package caches and web caches; never messages or files).

Tests to write first:

- `Keep`: kept names are not offered and not offered by a later rule; `ok == false` hides the rule.
- nvm resolution, table-driven: exact, prefix, `node`, `stable`, `lts/*`, `lts/<name>`, a chained alias, a missing alias file, an unknown form, a default that is not installed.
- rustup: default kept, overrides kept, missing settings hides the rule.
- Each app rule stages its three targets plus a `messages.db`, a `resources` folder and an `update` folder beside them, and offers only the targets. A running owner (`env.guard.running`) marks the row `Running`.
- Partition layout: `Application Support/X/Partitions/a/Cache` joins app X's row; `Partitions/a/Service Worker` does not.
- `dev.pnpm-store-home`: an entry changed within 30 days is `too-new`; an older one is offered and goes to the Trash.
- `TestRuleCatalogMeasuresEachTarget` passes with the new rules (it stages each target with `*` replaced by "Fixture"; make the `Keep` rules pass it honestly, by staging what `Keep` needs, not by exempting them).

---

## Task 3: Tool problem row

**Files:** `junk.go` (the `Command` branch of `planItem` only), `tools.go`, `tools_darwin_test.go`, `src/react/clean/helpers.ts`, `src/react/clean/CleanPage.tsx` (the row), web tests, `src/i18n.ts` (anchor: after `"junk.skip.blocked"`), `src/dev/mock-mygo.ts` (one sample).

**Consumes:** `JunkItem.Problem`, `JunkItem.ProblemDetail` (Task 0).

1. When a command rule's tool is found and its estimate fails because the tool's own command failed or timed out, the item keeps `Size: 0` and gets `Problem: "tool-failed"` and `ProblemDetail`: the last non-empty line of the tool's output, or the error text when there is none, cut to 300 characters. Make the estimate's error carry that line.
2. Unchanged: a cancelled scan, roots refused by the safety checks (`errBlocked`), and roots that overlap an earlier claim leave the row hidden; a missing tool yields no row.
3. Page: a row with `problem` is visible, cannot be selected (alone or through its group's select-all), shows no size, and reads "{tool} 无法运行，暂时不能清理" / "{tool} cannot run, so this cannot be cleaned for now" where the size would be. Its expanded box shows `problemDetail` under the rule's usual explanation. The tool's display name comes from the rule name.
4. Cleaning: a row with `problem` is never sent in a selection; `cleanJunk` behaviour is unchanged.
5. Windows shares this code: its command rules get the same row.

Tests to write first: replace what `TestFailedToolMeasurementHidesRow` asserts for a failing tool (the item now carries `Problem` and the last output line) and keep its hidden cases (cancelled, blocked, overlapping); a tool that prints nothing; a tool that times out. Web: visible, unselectable, excluded from select-all and from the selected total, text and detail rendered.

---

## Task 4: Orca

**Files:** `aitools.go`, `aitools_test.go`, `aitools_darwin_test.go`, `windows_tools_test.go` if its command-line test needs the new patterns, `index.html` and `src/main.ts` (the fourth tool button and name), the AI tools and AI space pages where tools are listed, `src/i18n.ts` (anchor: after the last `ai.part.trae.*` key), `src/dev/mock-mygo.ts`, web tests.

1. A fourth `aiTool`, `orca`, present when `~/.orca` is a folder, declared like the Codex entry. Parts and levels as the spec's Part E table: sessions by month with the 24-hour protection (the layout is Codex's `sessions/YYYY/MM/DD/*.jsonl`, so reuse `codex-months`), task sessions as entries, logs and cache as safe, `auth.json` protected, settings and memory shown and never offered. Files the table does not name stay `unknown`.
2. Running detection: a process pattern that matches the Orca CLI (`@blade-ai/orca`, binary `orca`) and nothing else; `TestWindowsAICommandLines` must still pass. On Windows the tool is absent (`Home` resolves to nothing there, as TRAE's helpers do).
3. Everything is moved to the Trash by the existing code path; add no removal code.
4. Page: the tool appears in the sidebar and in AI space only when installed, with the same row layout as the others; names and descriptions in zh and en.

Tests to write first: `TestListAITools` expects four tools in order; an Orca fixture with sessions in two months (one file written within 24 hours), task sessions, logs, cache, `auth.json`, `config.toml` and an unknown folder asserts each part's level, size and what a clean moves and keeps; a running Orca refuses parts that are not run-safe. Web: the button shows only when the tool is listed.

---

## Controller verification (after the cherry-picks, outside any sandbox)

- [ ] All checks in Global Constraints on the merged branch; `bun run generate` leaves `src/mygo.ts` unchanged.
- [ ] One real, read-only junk scan built from `master` and one from the branch (the `currentJunkEnv()` test used on 2026-11-17): the new rows match the spec's numbers within what changed on disk since, every other row is unchanged, and the scan takes at most 10 s longer. If it takes longer, stop and report: the spec's fallback is a separate scan action for projects.
- [ ] `ListAITools` on the real home lists Orca with sessions near 2.2 GB.
- [ ] `rustup toolchain list` against a staged `RUSTUP_HOME` with one toolchain folder removed still lists the rest without error.
- [ ] The dev build opened once: the 开发项目 group, a toolchain row, an app row marked running, the pnpm problem row and the Orca page each look right in zh and en, light and dark.
- [ ] No real clean is run.

## Validation ledger (2026-11-17)

Branch `feat/dev-space-batch`, head `7f339ab`. Five Opus implementers in their own worktrees, a task review each, one fix round for Tasks 1, 2 and 3, a whole-branch review, one fix wave and its re-review. Everything below ran on the maintainer's Mac (macOS 27.2.1), read-only; no real clean was run.

- `test -z "$(gofmt -l .)"`, `go vet ./...` and both Windows `go vet` runs: clean. `bun run generate` leaves `src/mygo.ts` unchanged.
- `go test -race -count=1 ./...` with the CI skip: `ok  	cleanzo	263.070s`.
- `bun run typecheck`: clean. `bun run test:web`: 354 pass, 0 fail. `bun run build:web`: built (Vite's existing chunk-size warning).
- One real junk scan through `currentJunkEnv()`, built from `master` and from the branch, two runs each: `master` 22.6 and 20.7 s, branch 27.0 and 26.8 s (4 to 6 s more, inside the 10 s budget). Expansion before the first screen: 9.4 and 8.4 s against 9.2 and 8.9 s. Rows 122 to 142.
- New rows on this Mac, none preselected: 12 project rows (5.76 GB, idle 15 to 177 days), `review.node-versions` 5.65 GB, `review.rust-toolchains` 1.47 GB, `app.lark` 7.70 GB, `app.doubao-work` 8.97 GB and `app.aime` 3.60 GB (all three running, so shown and not cleanable), `app.doubao` 0.91 GB, `dev.pnpm-store-home` 1.97 GB.
- The preselected total grew from 25.0 to 30.2 GB: the generic app rows now take Chromium `Cache` and `Code Cache` under `Partitions/*/` (flux-desktop-app 2.22, Aime 1.53, pilion-browser 1.09, TRAE 0.27, LarkShell 0.05 GB). Checked on disk that only those cache folders are counted.
- Orca on the real home: 2.52 GB; sessions 2.32 GB, task sessions 89 MB, eight protected parts, five unknown entries.
- rustup 1.29.0 against a staged `RUSTUP_HOME` with 1.92.0's folder gone and its update-hash left: `toolchain list` works, and `toolchain install 1.92.0` removes the stray hash and installs.
- Look check in the browser mock, Chinese and English, light: the group, the project row, the Node versions row, the Lark row marked running, the pnpm problem row, Orca in the sidebar. No raw i18n keys.
- Not checked: the dark theme; the tool problem row with real data (pnpm works again on this Mac since 16:37); Orca's running detection against a live process; the Windows tests, which run only in CI.

## Known gap

- A `:` in a project's parent path cancels `GIT_CEILING_DIRECTORIES` (git reads it as a colon-separated list; Finder stores a `/` in a folder name as `:`). With an empty or corrupt `.git` in the project and an enclosing repository that ignores the folder, a source `build`, `dist` or `out` could then be offered, unselected and to the Trash. Follow-up: skip git when the parent path holds a colon.

## Rulings made during execution

Each is `what was decided — why — what it costs if wrong`.

- Tasks 1–4 run in parallel, each in its own git worktree, against the skill's one-implementer-at-a-time rule — the maintainer's standing workflow (2026-11-11 audit fixes) and worktrees remove the shared working tree — if wrong: conflicts at cherry-pick, which the controller resolves.
- implementers and reviewers all run on Opus — the maintainer asked for Opus — if wrong: more tokens than a cheaper tier would need for Task 0.
- the detail is the most informative line of the tool's output, not literally the last — real corepack/Node failures end with a stack frame, `}` or `Node.js v22.23.1`, so "last line" (spec Part D, brief item 1) shows nothing useful for the very failure that motivated the row — if wrong: an odd line is shown for some tool; no safety effect. `JunkItem.ProblemDetail`'s comment and `src/mygo.ts` are updated by Task 3 in a last commit of their own.
- a command that hits its own timeout gets `Problem: "tool-timeout"` with its own row text, and no raw Go error text is ever shown as detail — "context deadline exceeded" is not a reason a user can read — if wrong: one more i18n key pair; no safety effect.
- `dev.pnpm-store-home` goes AFTER `dev.pnpm-store` — losing a default-on prune row is a regression for anyone whose pnpm store is ~/.pnpm-store, and the 30-day MinAge already keeps an active store out of the new row — if wrong: an idle ~/.pnpm-store that is also pnpm's configured store shows in two rows. Carried into Task 2's fix round.
- the Rust description naming "components and targets" instead of "global packages" stands — a toolchain holds no global packages, so the design's wording was wrong for Rust — if wrong: a string change.
- the four app rows keep the generic "deleted" consequence line for this release — the row name and description already say what is removed — if wrong: a string change after the dev-build look.
- `nvmKeep` must count only folders as installed versions — a stray file or link named like a version could stand in for the real default, and the real default would then be offered — if wrong: nothing, it only keeps more. Enters fix round 1 with the pnpm order ruling and the Keep test that cannot fail.
- the three deviations stand — `.claude/worktrees/*` walked at the level of the folder holding `.claude`; outside a repository the project is the build folder's parent (also for `.venv`); home is never a project — each keeps more or matches intent — if wrong: a project without its own `.git` under a `~/.git` dotfiles repository gets no row.
- minors 1-3 (no test pins git's hardening; the home rule has no test; test git reads XDG and system config) enter fix round 1 — they guard rows of the spec's Risks table, and a test that depends on the developer's global gitignore will fail on someone's machine — if wrong: a few more tests.
- the check for a nested checkout is general, not venv-only — any candidate holding a `.git` entry within three levels below it is never offered — a checkout under `node_modules/x` is as unrebuildable as one under `.venv/src/x` — if wrong: a few thousand extra directory reads per idle project's folders.
- parked — a ":" in the project's parent path defeats GIT_CEILING_DIRECTORIES (projects.go:74), so git again answers for an enclosing repository — real and deferred; it needs three things at once (an empty or corrupt `.git` in the project, an enclosing repository that ignores the folder, and a colon in the parent path, which Finder writes for a "/" in a folder name), and the row is still unselected and goes to the Trash — if wrong: in that case a source `dist`, `build` or `out` could be offered. One-line follow-up: skip git when the parent path holds a colon. Surfaced to the maintainer.
