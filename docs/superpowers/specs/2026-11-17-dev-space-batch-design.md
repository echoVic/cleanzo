# Dev space batch: design

Date: 2026-11-17. Status: awaiting the maintainer's review. Nothing here is implemented.

The maintainer asked for five additions after a read-only survey of his Mac (macOS 27.2.1). Open questions were decided by the author of this document and are recorded as **Rulings** with their reason, so each can be overturned on its own.

## What the survey found

All sizes are per-file sums unless marked "reclaimable" (clones counted once, as the junk scan does since 2026-11-17).

| Where | Size | What is in it |
|---|---|---|
| Build folders in 34 projects under the home folder | 23.7 GB | `node_modules` 15.4, `build` 4.3, `.next` 2.0, `target` 0.9, `.venv` 0.5, `dist` 0.5, `out` 0.2 |
| … of which in projects untouched for 14 days or more | 6.9 GB in 12 projects (5.74 GB reclaimable) | 20 folders, 279,159 files |
| … untouched for 30 days or more | 2.7 GB in 9 projects | |
| `~/Library/Application Support/LarkShell` | 22.5 GB | logs 2.5, npm cache 1.9, Service Worker cache 2.8, message databases and media 6.3, a staged update 2.4 |
| `~/Library/Application Support/DoubaoWork` | 13.3 GB | npm cache 5.7, logs 1.5, Service Worker cache 1.0, agent sessions and databases 3.3 |
| `~/Library/Application Support/Aime` | 5.0 GB | Service Worker cache 3.4, `Partitions/*/Cache` and `Code Cache` 1.5 |
| `~/Library/Application Support/Doubao` | 3.3 GB | logs 0.7, sandbox cache 0.2, profiles 1.6 |
| `~/.nvm/versions/node` | 7.6 GB | six Node versions; global packages are 0.7 to 2.4 GB of each |
| `~/.rustup/toolchains` | 3.6 GB | `stable` (default) 2.1, `1.92.0` 1.5 |
| `~/.orca` | 2.45 GB | sessions 2.2 (`sessions/YYYY/MM/DD/*.jsonl`), task sessions 0.1 |
| `~/.pnpm-store` | 1.97 GB | a second pnpm store: `v3` last written 2025-07, `v10` last written 2026-06 |
| `~/.codex/worktrees` | 1.6 GB | five worktrees; 1.45 GB of it is `node_modules` and `build` |
| `~/.git-ai` | 1.42 GB | a metrics database; no binary on PATH, last written 2026-08 |
| `~/.dsh` | 1.16 GB | DeepSeek Harness: program runtime 0.8, sessions and attachments 0.27 |

Facts the design leans on:

- Finding build folders is cheap: walking the home folder without entering them takes 0.3 to 0.6 s, and 112 `git check-ignore` calls take 2.3 s. Sizing is the cost: the 20 stale folders take 12 to 17 s single-threaded.
- "Ignored by the project's git" separates build output from source. Of 112 candidates it rejected four tracked `build` folders, one tracked `dist`, and an 8.3 GB untracked `out` folder that is not ignored.
- `pnpm store path` fails on this Mac (corepack is too old for pnpm 12), so the pnpm row is hidden and a 5.9 GB store is invisible in the app.
- Lark, DoubaoWork and Doubao share one layout: `sdk_storage/log`, `sandbox_runtime/.cache/{node,python}`, `aha/users/*/profile_explorer/Service Worker/CacheStorage`.
- Bundle ids: Lark `com.electron.lark`, DoubaoWork `com.work.pc.doubao`, Doubao `com.bot.pc.doubao`, Aime `com.bytedance.aime.electron`.
- All five Codex worktrees are still listed by their repositories, and one holds 35 uncommitted changes.

## Scope

macOS only, except Part D's row for a tool that cannot run, which applies wherever command rules exist. Everything lives in the existing junk scan (垃圾清理) and AI tools pages; there is no new page.

**Ruling: one batch, one release.** The parts share the junk rule table and the i18n dictionary, so they are planned together and split into tasks by file ownership.

## Part A: build folders of idle projects

A new junk group `projects` (开发项目 / Dev Projects), shown after 开发工具. One row per project, ID `project:<absolute path>`, named after the project folder, with the path and "N 天没有改动" beneath. Each build folder is an entry of the row, listed in the expanded details. Rows are never preselected and always go to the Trash.

**Finding candidates.** Walk the home folder to six levels, skipping hidden entries, `Library`, `Applications`, `Music`, `Pictures`, `Movies`, `Public`, packages, symlinks, and everything the junk scan already protects. A folder with a candidate name is recorded and not entered. Two hidden places are entered on purpose: `~/.codex/worktrees/*/*` and `<project>/.claude/worktrees/*`, so agent worktrees are treated as projects.

**Recognising a build folder.**

| Folder | Must be true |
|---|---|
| `node_modules`, `.next`, `.nuxt`, `.svelte-kit`, `.turbo` | `package.json` beside it |
| `.venv`, `venv` | `pyvenv.cfg` inside it |
| `target` | `Cargo.toml` beside it and `CACHEDIR.TAG` inside it |
| `Pods` | `Podfile` beside it |
| `build`, `dist`, `out` | a project file beside it (`package.json`, `go.mod`, `Cargo.toml`, `pyproject.toml`, `pubspec.yaml`, `build.gradle`, `build.gradle.kts`, `pom.xml`) **and** the folder is ignored by the project's git |

Inside a git repository every candidate must also be ignored by git and not tracked. Outside a repository only the first four table rows apply.

**The project** is the nearest ancestor holding `.git`; without one, the folder holding the marker file.

**Idle** means nothing changed for 14 days: the newest change time among the project's files to three levels deep (hidden folders and candidate folders skipped), and `.git/index`, `.git/HEAD`, `.git/logs/HEAD`. Projects changed more recently are not listed.

**Git** is asked once per repository (`git check-ignore --stdin`), with `GIT_OPTIONAL_LOCKS=0`, `GIT_TERMINAL_PROMPT=0`, `-c core.fsmonitor=false` and a five-second limit. When git is missing, fails, or would be Apple's `/usr/bin/git` stub without developer tools installed (it opens an install dialog), repositories are skipped whole.

**Existing protections apply unchanged:** the in-use probe skips a folder with open files (a running dev server), and the clean re-discovers and re-checks before moving anything.

Rulings:

- **14 days, fixed, no setting.** 30 days finds 2.7 GB here and 14 days finds 6.9 GB; rows are not preselected and are recoverable from the Trash, so the lower bar costs little. A setting can follow if asked for.
- **A row per project, not per folder kind.** The decision a person makes is "do I still work on this project".
- **In the junk scan, not a new page.** It reuses confirmation, results, in-use checks and clone-aware sizes. The cost is scan time: budget is at most 10 s more on this Mac (the scan takes about 14 s today), checked before release. If it exceeds that, the group moves behind its own "扫描项目" action.
- **Worktrees are never removed, only their build folders.** Removing a worktree can lose uncommitted work and leaves git bookkeeping behind. On this Mac none of the five is idle for 14 days yet, so this yields nothing today.
- **Ambiguous names need git's word.** Without it `build`, `dist` and `out` are never offered.

## Part B: old toolchain versions

Two rules in 需要确认 (review), not preselected, to the Trash, each version selectable on its own:

- `review.node-versions`: entries of `~/.nvm/versions/node`, except the version nvm's `default` alias resolves to.
- `review.rust-toolchains`: entries of `~/.rustup/toolchains`, except `default_toolchain` and any toolchain named under `[overrides]` in `~/.rustup/settings.toml`.

A rule gains a `Keep` hook that returns entry names never offered. If the default cannot be resolved (alias file missing, an alias form the resolver does not know, settings unreadable), the hook keeps every entry and the row does not appear. A version whose `node` or `rustc` is running is skipped by the in-use probe as today.

The explanation says what is lost: the version's global packages, and that a project pinning the version will need it installed again.

Rulings:

- **Trash the folder, do not run `nvm uninstall` or `rustup toolchain uninstall`.** `nvm` is a shell function, and both tools remove permanently. The Trash keeps the version recoverable.
- **Project pins (`.nvmrc`, `rust-toolchain.toml`) are not read.** On this Mac two of the five non-default Node versions are pinned by a project (`.node-version` 22.18.0 and `.nvmrc` 24). The row is not preselected and says so; reading pins means another walk for little gain.
- On this Mac this offers 6.0 GB of Node versions and 1.5 GB of Rust.

## Part C: caches and logs of four apps

Four catalog rules in 应用缓存 (apps), each owned by the app's bundle id so a running app's row is shown but cannot be cleaned. Not preselected. Deleted, like other caches.

| Rule | Folder under `~/Library/Application Support` | Offered |
|---|---|---|
| `app.lark` | `LarkShell` | entries of `sdk_storage/log`; entries of `sandbox_runtime/.cache`; `aha/users/*/profile_explorer/Service Worker/CacheStorage` |
| `app.doubao-work` | `DoubaoWork` | the same three |
| `app.doubao` | `Doubao` | the same three |
| `app.aime` | `Aime` | `Partitions/*/Service Worker/CacheStorage` |

The generic per-app cache rows also learn Electron's partition layout: the cache folders they already know (`Cache`, `Code Cache`, `GPUCache`, …) are taken under `Application Support/<app>/Partitions/*/` as well.

Rulings:

- **Not offered:** message and contact databases, `resources` (stickers, avatars, images), `PC_Gadget`, `deployments`, `connector_runtime`, `sandbox_runtime/bases`, agent sessions, `~/DoubaoWork` (source trees and git repositories), Doubao's `hot_fix` and `gecko_cache`. They are user data or could not be shown to be regenerable.
- **Lark's `update` folder (2.4 GB) is not offered.** It holds a build newer than the installed one, so it is a pending update, not a leftover.
- **Service Worker caches are offered only for these four apps, never generically and never for browsers.** They were inspected here; elsewhere an offline-first app may need its cache to start without a network.
- **Not preselected in this release**, although ordinary app caches are. A wrong call on a chat app costs more than a missed default; the default can be raised after a release without complaints.
- On this Mac, with the apps quit: Lark about 7.3 GB, DoubaoWork about 8.3 GB, Aime about 4.9 GB, Doubao about 0.9 GB.

## Part D: a tool that cannot run, and the second pnpm store

- When a command rule's tool is found but its estimate command fails, the row stays visible, cannot be selected, shows no size, and reads "pnpm 无法运行，暂时不能清理" with the tool's last output line in the expanded box. `JunkItem` gains `Problem` (a code, `tool-failed`) and `ProblemDetail`. Cancelled estimates and roots refused by the safety checks stay hidden as today.
- `dev.pnpm-store-home`: entries of `~/.pnpm-store`, in 开发工具, not preselected, to the Trash, offered only when nothing in an entry changed for 30 days.

Ruling: **the store's size is not shown for a failed tool.** The path comes from the tool, and guessing the default location would show a number for a store the tool may not use.

## Part E: Orca as an AI tool

A fourth tool beside Claude Code, Codex and TRAE, present when `~/.orca` exists.

| Part | Path | Treatment |
|---|---|---|
| Sessions | `sessions/YYYY/MM/DD/*.jsonl` | by month, like Codex; the last 24 hours kept |
| Task sessions | entries of `task-sessions` | optional |
| Logs | `logs` | safe |
| Cache | `summary_cache`, `pricing-cache.json` | safe |
| Credentials | `auth.json` | shown, never offered |
| Settings and memory | `config.toml`, `folder_trust.toml`, `onboarding.toml`, `memory`, `history.jsonl`, `goals.sqlite3`, `sessions-index.sqlite3` | shown, never offered |

Everything else (`rewind`, `operations`, lock files) shows as unknown and is never offered, which is what the tool table already does with files it does not know.

Ruling: **`~/.git-ai` and `~/.dsh` get no entry.** The first has no identifiable vendor and its contents could not be classified; the second is mostly program runtime, with 38 MB of cache.

## Not in this batch

- Removing whole agent worktrees, and Purge-style orphaned-worktree detection.
- A setting for the idle threshold; reading project version pins.
- Windows versions of Parts A, B, C and E.
- Lark's message databases, media and staged update.
- The row note "shared with projects, will not be freed".

## Risks

| Risk | Handling |
|---|---|
| A build folder that is really source | Marker file plus git-ignore; ambiguous names need git; Trash only; never preselected |
| A project in active use | 14-day idle rule, in-use probe, re-check at clean time |
| Running git in a repository with hostile config | `core.fsmonitor=false`, no optional locks, no prompts, time limit; only `check-ignore` is run |
| Removing a cache while its app runs | Rules carry the bundle id; a running app's row cannot be cleaned |
| A removed Rust toolchain confuses rustup | Trash is recoverable; the implementer checks `rustup toolchain list` against a staged `RUSTUP_HOME` before release |
| Scan time | Budget above, measured before release |

## Testing

- Go tests stage every case in a temporary home with the existing helpers: recognised and rejected folders, tracked and ignored folders in a real temporary git repository, idle and active projects, worktree roots, default-version resolution and its failure modes, each app rule's targets, the partition layout, the failed-tool row, the Orca parts.
- `TestRuleCatalogMeasuresEachTarget` and the Windows rule and i18n tests stay green.
- Web tests cover the new group, the project row's subtitle, the unselectable problem row and the fourth tool.
- Before release, on the maintainer's Mac and read-only: one real scan built from `master` and one from the branch, comparing rows and time. No real clean is run.
