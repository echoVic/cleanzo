# Cleanup polish implementation plan

> Execute the three approved drafts in the current checkout. Approval: “同意，开干”.

**Goal:** Make selection, cleanup consequences and completed outcomes easy to verify without expanding cleanup behavior.

**Architecture:** Keep the existing React pages, typed MyGo bridge and backend protections. Use existing scan/clean results; add localized explanatory content and bounded review lists. Confirmation executes the same path snapshot the user reviewed.

**Tech stack:** React 19, TypeScript, existing CSS tokens, Radix Dialog, MyGo 0.8.3.

**Spec:** Approved draft images in the current conversation, `cleanzo-drafts/01-selection.png`, `02-explanations.png`, `03-review-results.png`.

## Constraints

- Preserve visual identity and cleanup eligibility/default selection.
- Keep scanning discovery separate from selected bytes. Completed primary bytes mean freed space, excluding Trash.
- Explain known rule contents and consequences; unknown custom folders get no invented safety claim.
- Keep paths available after the explanation, including rows with no top/sub entries.
- Batch review lists actual effective root paths, their known sizes, and unknown sizes honestly.
- Mirror zh/en and retain Windows terminology/behavior.
- Group uninstall related files as app, cache, settings and data; selection behavior stays unchanged.
- No release, publication or real user-file deletion during verification.

## Tasks

- [x] 1. Selection summary and explanatory rows: add meaningful page regression checks, observe failure, implement and verify. Add progressive explanations and retain existing item details.
- [x] 2. Confirmation: list selected items/paths and sizes, freeze reviewed batch paths, group uninstall contents and explain Trash recovery/capacity.
- [x] 3. Results: show freed bytes as primary, Trash separately, grouped success/skipped/incomplete rows with available reasons. Cover mixed and stopped results.
- [x] Validation: full web suite, TypeScript, web build, Go checks/MyGo vet, rendered zh/en light/dark and narrow layouts, final independent review.

## Progress

- Baseline: clean `master` at `e84723eaf595bfa25ec1c9522a26edd13ccd1012`; pinned bridge version verified.
- Scanned headline follows selected bytes. Expanded rows explain contents, consequences, method, then locations and existing details.
- Cleanup confirmation uses the selected plan; fullscan captures effective root paths and known sizes when its confirmation opens. Unknown measurements stay explicit.
- Uninstall review groups app, caches/logs, settings/state and data. WebKit and HTTP storage belong to data; associated data carries a backup reminder. Windows keeps its existing native uninstaller flow.
- Results separate freed and trashed bytes, group processed/skipped/incomplete rules, and retain concrete reasons. A stopped rule without a final result uses “Result unconfirmed”; incomplete totals are qualified and show `--` when no freed amount is confirmed.
- Browser QA found mixed cleanup notes left the footer too close to the minimum window edge. Only the two action-review dialogs now let their lists shrink while descriptions, totals and buttons retain their height.

## Validation ledger

- Final `bun run test:web`: **345 passed, 0 failed**. Includes selected headline, frozen confirmation paths/sizes, mixed outcomes, stopped-after-removal and WebKit/HTTP grouping regressions. Final log: `/tmp/cleanzo-polish-web-tests-final.log`.
- Final `bun run typecheck`: passed for application and tests. Fixed the new stopped-row fixture's widened Map inference with `Map<string, RowState>`.
- Final `bun run build:web`: passed. Vite reports the existing large-chunk advisory; bundle splitting was outside this polish. Log: `/tmp/cleanzo-polish-build-final.log`.
- `CGO_ENABLED=0 go test ./...`: first full run failed `TestSkillGitCallsTimeOut` because the expected child PID was empty. The isolated test and second full run passed (`ok cleanzo 187.147s`). No Go/backend source changed.
- Final pinned CLI check `CGO_ENABLED=0 go run github.com/egoist/mygo/cmd/mygo vet .`: passed. `go tool mygo` is unavailable in this repository's npm-pinned CLI setup.
- Final `git diff --check`: passed.
- Read-only independent review: 124 focused tests passed; no remaining findings. Follow-up review of scoped dialog CSS and fixture typing also found no issue.
- Actual browser render: selected headline and explanations; cleanup confirmation; grouped uninstall; fullscan review of Downloads 22.4 GB + .cache 9.6 GB = 32.0 GB; completed cleanup with 23.9 GB freed and 486 MB trashed. Checked Chinese/English and light/dark styling using the local mock page. Mock appearance preference alone does not alter browser media; dark rendering was verified with media emulation and computed CSS tokens.
- Minimum 560×420 window: English dark mixed cleanup confirmation fits (`scrollHeight == clientHeight == 386`, button bottom 383); uninstall controls remain visible; Chinese fullscan review fits (`scrollHeight == clientHeight == 379`) and returning to review retains selection.
- Retained screenshots stay in the session's local visualization folder and are not committed.
- Verification used `http://127.0.0.1:5173/?mock` and in-memory fixtures. Real native deletion, Trash restoration and Windows GUI acceptance were not exercised. Changes remain uncommitted; no release or publication performed.
