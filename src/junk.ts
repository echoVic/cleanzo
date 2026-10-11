import { Cleaner } from "./mygo";
import { Channel } from "mygo-runtime";
import type { JunkItem, JunkScanEvent, JunkCleanEvent, JunkSelection, ScanSummary, CleanResult } from "./mygo";
import { t, ruleName, ruleDesc } from "./i18n";
import {
  $, errText, human, esc, displayPath, baseName, toast, emptyState, errorState,
  skeletonRows, setStoppable, stopScan, endScan, stoppedScans, markRescan, setNavBusy,
  modalTitle, modalBody, modalOk, showModal, setPending,
} from "./shared";

const listEl = $("#list");
const totalEl = $("#total");
const totalPrefix = $("#total-prefix");
const heroSub = $("#hero-sub");
const heroProgress = $("#hero-progress");
const heroBar = $("#hero-bar");
const hero = $("#view-clean .hero");
const scanBtn = $<HTMLButtonElement>("#scan");
const cleanBtn = $<HTMLButtonElement>("#clean-all");

type JunkPhase = "idle" | "scanning" | "scanned" | "cleaning" | "cleaned";
let phase: JunkPhase = "idle";
let items: JunkItem[] = [];
const pending = new Set<string>();
const selected = new Set<string>();
const subSel = new Map<string, Set<string>>();
const expanded = new Set<string>();
const collapsed = new Set<string>();
let summary: ScanSummary | null = null;
let stale = false;
let showAllApps = false;
let scanningPath = "";
let scannedAt = 0;
let scanFailed: unknown = null;
let scanStopped = false;
let menuID: string | null = null;
let ignoring = false;
let cleanChan: { close(): void } | null = null;
type RowState = { state: "waiting" | "cleaning" | "done" | "stopped" | "skipped"; result?: CleanResult };
const rowStates = new Map<string, RowState>();
const progress = { total: 0, bytes: 0, id: "", path: "", stopped: false, error: "" };
const groups = ["system", "apps", "browsers", "updates", "dev", "review", "custom"] as const;
export let openLeftovers: (() => void) | null = null;

/** 导航由主模块提供，下一步再换成应用页签。 */
export function setOpenLeftovers(fn: () => void) { openLeftovers = fn; }

const dynamic = (it: JunkItem) => it.id.startsWith("app:") || it.id.startsWith("custom:");
const title = (it: JunkItem) => dynamic(it) ? it.name : ruleName(it.id);
const visible = (it: JunkItem) => it.size === -1 || it.size > 0 || it.denied;
const selectable = (it: JunkItem) => it.size > 0 && !it.running && !it.denied;
const subsOf = (it: JunkItem) => it.subs ?? [];
const hasSubs = (it: JunkItem) => subsOf(it).length > 0;

/** 子项为空时不发整项选择；后端的空 paths 表示整项。 */
function chosenSize(it: JunkItem): number {
  if (!selectable(it) || !selected.has(it.id)) return 0;
  if (!hasSubs(it)) return it.size;
  return subsOf(it).filter((s) => subSel.get(it.id)?.has(s.path)).reduce((n, s) => n + s.size, 0);
}

function setSelected(it: JunkItem, on: boolean) {
  if (!selectable(it)) return;
  if (hasSubs(it)) {
    subSel.set(it.id, new Set(on ? subsOf(it).filter((s) => s.default && s.size > 0).map((s) => s.path) : []));
    on = (subSel.get(it.id)?.size ?? 0) > 0;
  }
  if (on) selected.add(it.id);
  else selected.delete(it.id);
}

function partial(it: JunkItem): boolean {
  const count = subSel.get(it.id)?.size ?? 0;
  return selected.has(it.id) && hasSubs(it) && count > 0 && count < subsOf(it).filter((s) => s.size > 0).length;
}

/** 重绘后恢复三态，勾选状态不放在 DOM 里。 */
function syncChecks() {
  listEl.querySelectorAll<HTMLInputElement>('input[data-action="item"]').forEach((cb) => {
    const it = items.find((x) => x.id === cb.dataset.id);
    cb.indeterminate = !!it && partial(it);
  });
  listEl.querySelectorAll<HTMLInputElement>('input[data-action="group"]').forEach((cb) => {
    const members = items.filter((it) => it.group === cb.dataset.group && selectable(it));
    const picked = members.filter((it) => selected.has(it.id));
    cb.indeterminate = picked.length > 0 && (picked.length < members.length || picked.some(partial));
  });
}

const tag = (key: string, kind = "dim") => `<span class="tag ${kind}">${esc(t(key as never))}</span>`;
const caret = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m9 5 7 7-7 7"/></svg>';
const more = '<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true"><circle cx="5" cy="12" r="1.6"/><circle cx="12" cy="12" r="1.6"/><circle cx="19" cy="12" r="1.6"/></svg>';
const groupIcons: Record<string, string> = {
  system: '<path d="M12 3v3m0 12v3M3 12h3m12 0h3M6 6l2 2m8 8 2 2M6 18l2-2M16 8l2-2"/><circle cx="12" cy="12" r="5"/>',
  apps: '<rect x="4" y="4" width="6" height="6" rx="1"/><rect x="14" y="4" width="6" height="6" rx="1"/><rect x="4" y="14" width="6" height="6" rx="1"/><rect x="14" y="14" width="6" height="6" rx="1"/>',
  browsers: '<circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18"/>',
  updates: '<path d="M4 10a8 8 0 0 1 14-5l2 2M20 3v4h-4M20 14a8 8 0 0 1-14 5l-2-2M4 21v-4h4"/>',
  dev: '<path d="m8 6-6 6 6 6m8-12 6 6-6 6M14 4l-4 16"/>',
  review: '<circle cx="12" cy="12" r="9"/><path d="M12 7v6m0 3v1"/>',
  custom: '<path d="M3 7V5h7l2 3h9v12H3z"/>',
};
function icon(it: JunkItem): string {
  if (it.group === "apps") return `<span>${esc(Array.from(title(it))[0] ?? "")}</span>`;
  return `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${groupIcons[it.group] ?? groupIcons.custom}</svg>`;
}

/** 运行中的规则显示应用名，不把“缓存”放到退出提示里。 */
function description(it: JunkItem): string {
  if (it.running) {
    const app = dynamic(it) ? it.name : t(`rule.${it.id}.app` as never);
    return t("junk.quit", { app: app.startsWith("rule.") ? ruleName(it.id) : app });
  }
  if (dynamic(it)) return it.paths?.[0] ?? "";
  return ruleDesc(it.id);
}

function stateNote(row: RowState): string {
  if (row.state === "waiting") return esc(t("cat.waiting"));
  if (row.state === "cleaning") return `<span class="spin"></span> ${esc(t("cat.cleaning"))}`;
  if (row.state === "stopped") return tag("cat.stopped");
  if (row.state === "skipped") return tag("cat.notCleaned");
  const res = row.result!;
  const failed = res.failed?.length ? `<span class="tag warn">${esc(t("cat.failedN", { n: res.failed.length }))}</span>` : "";
  if (res.skipped) return tag(`junk.skip.${res.skipped}`, "warn") + failed;
  const parts = [res.freed > 0 ? t("cat.freed", { size: human(res.freed) }) : "",
    res.trashed > 0 ? t("cat.trashed", { size: human(res.trashed) }) : ""].filter(Boolean);
  return `<span class="cat-note-text ok">&#10003; ${esc(parts.join(t("phrase.sep")) || t("cat.cleanedNothing"))}</span>` + failed;
}

function detailsHtml(it: JunkItem): string {
  if (!expanded.has(it.id)) return "";
  const locked = phase !== "scanned" || !selectable(it);
  if (hasSubs(it)) return `<div class="junk-details">${subsOf(it).map((s) => `
    <div class="junk-sub-row">
      <input type="checkbox" data-action="sub" data-id="${esc(it.id)}" data-path="${esc(s.path)}"
        aria-label="${esc(t("junk.select", { name: s.name }))}" ${locked || s.size <= 0 ? "disabled" : ""} ${selected.has(it.id) && subSel.get(it.id)?.has(s.path) ? "checked" : ""}>
      <div class="junk-sub-info"><span>${esc(s.name)}</span>${it.id === "review.device-support" && !s.default && !(selected.has(it.id) && subSel.get(it.id)?.has(s.path)) ? tag("junk.keepLatest") : ""}
        <div class="muted small">${esc(s.detail)}</div></div>
      <span class="junk-size">${human(s.size)}</span>
    </div>`).join("")}</div>`;
  return `<div class="junk-details">${(it.top ?? []).map((top) => `
    <div class="junk-top-row"><div><div>${esc(top.name)}</div><div class="muted small junk-path">${esc(displayPath(top.path))}</div></div>
      <span class="junk-size">${human(top.size)}</span></div>`).join("")}</div>`;
}

function rowHtml(it: JunkItem): string {
  const measuring = pending.has(it.id);
  const locked = phase !== "scanned" || !selectable(it);
  const row = rowStates.get(it.id);
  const used = row?.result?.inUse ?? it.inUse;
  const tags = [it.running ? tag("junk.running") : "", it.denied ? tag("junk.denied", "warn") : "",
    it.trash ? tag("cat.toTrash") : ""].join("");
  const note = row ? stateNote(row) : measuring ? `<span class="spin" aria-label="${esc(t("cat.scanning"))}"></span>` :
    it.denied ? "--" : `${it.estimate ? tag("junk.estimate") : ""} ${human(it.size)}`;
  const expandable = hasSubs(it) || (it.top?.length ?? 0) > 0;
  return `<div class="junk-item${(phase === "cleaning" || phase === "cleaned") && !row ? " is-dim" : ""}">
    <div class="cat-row junk-row">
      <label class="cat-check"><input type="checkbox" data-action="item" data-id="${esc(it.id)}"
        aria-label="${esc(t("junk.select", { name: title(it) }))}" ${locked ? "disabled" : ""} ${selected.has(it.id) ? "checked" : ""}></label>
      <div class="tile junk-icon junk-icon-${esc(it.group)}" aria-hidden="true">${icon(it)}</div>
      <div class="cat-info"><div class="cat-name">${esc(title(it))}${tags}</div><div class="cat-desc muted">${esc(description(it))}</div>
        ${used > 0 ? `<div class="cat-sub muted">${esc(t("junk.inUse", { n: used }))}</div>` : ""}</div>
      <div class="cat-note">${note}</div>
      <div class="junk-row-actions"><button class="junk-icon-btn" type="button" data-action="menu" data-id="${esc(it.id)}"
        aria-label="${esc(t("junk.menu", { name: title(it) }))}" title="${esc(t("junk.menu", { name: title(it) }))}" aria-expanded="${menuID === it.id}"
        ${phase !== "scanned" || ignoring ? "disabled" : ""}>${more}</button>
        ${expandable ? `<button class="junk-icon-btn junk-expand${expanded.has(it.id) ? " open" : ""}" type="button" data-action="expand" data-id="${esc(it.id)}"
          aria-expanded="${expanded.has(it.id)}" aria-label="${esc(t(expanded.has(it.id) ? "fs.collapse" : "fs.expand"))}"
          title="${esc(t(expanded.has(it.id) ? "fs.collapse" : "fs.expand"))}">${caret}</button>` : '<span class="junk-action-space"></span>'}</div>
      ${menuID === it.id ? `<div class="junk-menu" role="menu">
        <button type="button" role="menuitem" data-action="reveal" data-id="${esc(it.id)}" ${it.paths?.length ? "" : "disabled"}>${esc(t("junk.reveal"))}</button>
        <button type="button" role="menuitem" data-action="ignore" data-id="${esc(it.id)}" ${ignoring ? "disabled" : ""}>${esc(t("junk.ignore"))}</button></div>` : ""}
    </div>${detailsHtml(it)}</div>`;
}

function groupHtml(group: string, members: JunkItem[]): string {
  const eligible = members.filter(selectable);
  const picked = eligible.filter((it) => selected.has(it.id));
  const total = eligible.reduce((n, it) => n + it.size, 0);
  const chosen = eligible.reduce((n, it) => n + chosenSize(it), 0);
  const folded = collapsed.has(group);
  const shown = group === "apps" && !showAllApps ? members.slice(0, 15) : members;
  const rest = members.slice(shown.length);
  const text = members.some((it) => pending.has(it.id)) ? t("cat.scanning") : t("junk.groupSummary", { total: human(total), sel: human(chosen) });
  return `<section class="junk-group"><div class="junk-group-head">
      <input type="checkbox" data-action="group" data-group="${group}" aria-label="${esc(t("junk.select", { name: t(`junk.group.${group}` as never) }))}"
        ${phase !== "scanned" || eligible.length === 0 ? "disabled" : ""} ${picked.length > 0 && picked.length === eligible.length ? "checked" : ""}>
      <button class="junk-group-toggle" type="button" data-action="fold" data-group="${group}" aria-expanded="${!folded}">
        <span class="group-name">${esc(t(`junk.group.${group}` as never))}</span><span class="group-sel">${esc(text)}</span>
        <span class="junk-fold${folded ? "" : " open"}">${caret}</span></button></div>
    ${folded ? "" : `<div class="group-rows">${shown.map(rowHtml).join("")}
      ${rest.length ? `<button class="junk-more" type="button" data-action="all-apps">${esc(t("junk.moreApps", { n: rest.length, size: human(rest.filter(selectable).reduce((n, it) => n + it.size, 0)) }))}${caret}</button>` : ""}
      ${group === "review" && summary && summary.leftovers > 0 ? `<div class="junk-leftovers"><span>${esc(t("junk.leftovers", { n: summary.leftovers, size: human(summary.leftoversSize) }))}</span>
        <button class="btn small ghost" type="button" data-action="leftovers">${esc(t("junk.view"))}</button></div>` : ""}</div>`}</section>`;
}

function render() {
  renderHero();
  listEl.classList.remove("cat-grid", "grouped", "idle-list");
  listEl.classList.add("junk-list");
  if (scanFailed !== null) {
    listEl.innerHTML = errorState(t("scan.error", { msg: errText(scanFailed) }), () => void scan());
    return;
  }
  if (phase === "idle") {
    listEl.innerHTML = `<div class="empty-guide"><div class="junk-idle-icon" aria-hidden="true">${icon({ group: "updates" } as JunkItem)}</div>
      <h3>${esc(t("clean.idle.title"))}</h3><p>${esc(t("clean.idle.desc"))}</p>
      <button class="btn primary big" type="button" data-action="scan">${esc(t("clean.idle.cta"))}</button></div>`;
    return;
  }
  let html = "";
  if (summary && !summary.fullDiskAccess) html += `<div class="junk-banner"><span>${esc(t("junk.access", { n: summary.containers }))}</span>
    <button class="btn small ghost" type="button" data-action="grant">${esc(t("cat.grant"))}</button></div>`;
  if (stale && phase === "scanned") html += `<div class="junk-stale"><span>${esc(t("junk.stale"))}</span>
    <button class="btn small ghost" type="button" data-action="scan">${esc(t("fs.rescan"))}</button></div>`;
  const shown = items.filter(visible);
  for (const group of groups) {
    let members = shown.filter((it) => it.group === group);
    if (group === "apps") members = [...members].sort((a, b) => b.size - a.size);
    if (members.length || (group === "review" && (summary?.leftovers ?? 0) > 0)) html += groupHtml(group, members);
  }
  if (shown.length === 0 && !(summary?.leftovers)) html += emptyState(t(phase === "scanning" ? "scan.hero" : "junk.empty"));
  if (summary) html += `<div class="junk-foot muted small">${esc(t("junk.checked", { n: summary.checked }))}
    ${summary.ignored > 0 ? `<span>${esc(t("junk.ignored", { n: summary.ignored }))}</span> <button class="junk-link" type="button" data-action="settings">${esc(t("junk.manage"))}</button>` : ""}</div>`;
  if (scanStopped) html += `<div class="junk-foot muted small">${esc(t("scan.stopped"))}</div>`;
  listEl.innerHTML = html;
  syncChecks();
}

function renderHero() {
  hero.classList.toggle("compact", phase === "idle");
  heroProgress.classList.toggle("hidden", phase !== "cleaning" && phase !== "cleaned");
  cleanBtn.classList.toggle("danger", phase !== "cleaning" && phase !== "cleaned");
  cleanBtn.classList.toggle("ghost", phase === "cleaning");
  cleanBtn.classList.toggle("primary", phase === "cleaned");
  if (phase !== "scanning") {
    scanBtn.textContent = t(phase === "idle" ? "scan.start" : "fs.rescan");
    scanBtn.disabled = phase === "cleaning" || ignoring;
  }
  if (scanFailed !== null) {
    totalPrefix.textContent = t("hero.ready"); totalEl.textContent = t("scan.failed");
    heroSub.textContent = t("scan.retryHint"); cleanBtn.disabled = true;
    return;
  }
  const eligible = items.filter(selectable);
  const found = eligible.filter((it) => it.group !== "review").reduce((n, it) => n + it.size, 0);
  const review = eligible.filter((it) => it.group === "review").reduce((n, it) => n + it.size, 0);
  const reviewText = review > 0 ? t("junk.reviewTotal", { size: human(review) }) : "";
  if (phase === "idle") {
    totalPrefix.textContent = t("hero.ready"); totalEl.textContent = "--";
    heroSub.textContent = t("clean.idleSub"); cleanBtn.textContent = t("clean.now"); cleanBtn.disabled = true;
  } else if (phase === "scanning" || phase === "scanned") {
    totalPrefix.textContent = t(phase === "scanning" ? "scan.found" : "clean.foundPrefix");
    totalEl.textContent = phase === "scanning" && items.every((it) => pending.has(it.id)) ? "--" : human(found);
    const text = phase === "scanning" ? (scanningPath ? t("scan.at", { path: displayPath(scanningPath) }) : t("scan.hero")) :
      t("clean.selected", { size: human(eligible.filter((it) => it.group !== "review").reduce((n, it) => n + chosenSize(it), 0)) });
    heroSub.textContent = [text, reviewText].filter(Boolean).join(t("phrase.sep"));
    cleanBtn.textContent = t("clean.now");
    cleanBtn.disabled = phase !== "scanned" || ignoring || eligible.every((it) => chosenSize(it) === 0);
    heroBar.style.width = "0%";
  } else if (phase === "cleaning") {
    const cur = items.find((it) => it.id === progress.id);
    const where = cur ? title(cur) + (progress.path ? ` · ${baseName(progress.path)}` : "") : "";
    totalPrefix.textContent = t("clean.progress.prefix"); totalEl.textContent = human(progress.bytes);
    heroSub.textContent = where ? t("clean.progress.sub", { total: human(progress.total), where }) : t("clean.progress.preparing", { total: human(progress.total) });
    heroBar.style.width = `${progress.total > 0 ? Math.min(100, progress.bytes / progress.total * 100) : 0}%`;
    cleanBtn.textContent = t("clean.stop"); cleanBtn.disabled = progress.stopped;
  } else {
    const results = [...rowStates.values()].flatMap((r) => r.result ? [r.result] : []);
    const freed = results.reduce((n, r) => n + r.freed, 0);
    const trashed = results.reduce((n, r) => n + r.trashed, 0);
    const inUse = results.reduce((n, r) => n + r.inUse, 0);
    const failures = results.filter((r) => r.failed?.length || r.skipped).map((r) => title(items.find((it) => it.id === r.name) ?? { id: r.name, name: "" } as JunkItem));
    const text = [freed > 0 ? t("clean.done.freed", { size: human(freed) }) : "",
      trashed > 0 ? t("clean.done.trashed", { size: human(trashed) }) : "", inUse > 0 ? t("clean.done.inUse", { n: inUse }) : ""].filter(Boolean);
    totalPrefix.textContent = t(progress.stopped ? "clean.stoppedPrefix" : "clean.donePrefix");
    totalEl.textContent = human(progress.bytes);
    heroSub.textContent = [text.join(t("phrase.sep")) || t("clean.nothing"),
      progress.stopped ? t("clean.stoppedSub") : "", failures.length ? t("junk.partial", { names: failures.join(t("list.sep")) }) : "", progress.error].filter(Boolean).join(t("phrase.sep"));
    heroBar.style.width = `${progress.stopped || failures.length || progress.error ? (progress.total > 0 ? Math.min(100, progress.bytes / progress.total * 100) : 0) : 100}%`;
    cleanBtn.textContent = t("fs.rescan"); cleanBtn.disabled = false;
  }
}

/** 一次扫描只接受这一条流；停止后只留下已测量的行。 */
async function scan() {
  if (phase === "cleaning" || phase === "scanning" || ignoring) return;
  phase = "scanning"; items = []; summary = null; stale = false; showAllApps = false;
  scanFailed = null; scanStopped = false; scanningPath = ""; menuID = null; scannedAt = Date.now();
  pending.clear(); selected.clear(); subSel.clear(); expanded.clear(); rowStates.clear();
  const ch = new Channel<JunkScanEvent>();
  setStoppable(scanBtn, ch);
  renderHero();
  listEl.classList.remove("cat-grid", "grouped", "idle-list"); listEl.classList.add("junk-list");
  listEl.innerHTML = skeletonRows(5);
  try {
    const done = Cleaner.scanJunk(ch);
    // 先挂拒绝处理，流消费期间不会留下未处理的 Promise rejection。
    const completion = done.then(() => null, (e: unknown) => e);
    for await (const ev of ch) {
      if (stoppedScans.has(scanBtn)) continue;
      if (ev.kind === "start") {
        items = ev.items ?? []; items.forEach((it) => pending.add(it.id)); render();
      } else if (ev.kind === "scanning") {
        scanningPath = ev.path ?? ""; renderHero();
      } else if (ev.kind === "item" && ev.item) {
        const it = ev.item;
        items = items.map((old) => old.id === it.id ? it : old);
        pending.delete(it.id);
        subSel.set(it.id, new Set(subsOf(it).filter((s) => s.default && s.size > 0).map((s) => s.path)));
        if (it.default && selectable(it) && (!hasSubs(it) || subSel.get(it.id)!.size > 0)) selected.add(it.id);
        render();
      } else if (ev.kind === "done") { summary = ev.summary ?? null; render(); }
    }
    const error = await completion;
    if (error !== null) throw error;
  } catch (e) {
    if (!stoppedScans.has(scanBtn)) {
      scanFailed = e; items = []; selected.clear(); subSel.clear();
      toast(t("scan.error", { msg: errText(e) }), "err");
    }
  } finally {
    scanStopped = endScan(scanBtn);
    items = items.filter((it) => !pending.has(it.id)); pending.clear();
    phase = "scanned"; markRescan(scanBtn); render();
  }
}

function askClean() {
  if (phase !== "scanned" || ignoring) return;
  const chosen = items.filter((it) => chosenSize(it) > 0);
  if (!chosen.length) return;
  const selections: JunkSelection[] = chosen.map((it) => hasSubs(it) ? { id: it.id, paths: [...(subSel.get(it.id) ?? [])] } : { id: it.id });
  const bytes = chosen.reduce((n, it) => n + chosenSize(it), 0);
  const sum = (list: JunkItem[]) => human(list.reduce((n, it) => n + chosenSize(it), 0));
  // 选了几十项时只列前几个名字，免得确认框被名字撑满
  const names = (list: JunkItem[]) => list.length > 6
    ? t("junk.andMore", { names: list.slice(0, 6).map(title).join(t("list.sep")), n: list.length })
    : list.map(title).join(t("list.sep"));
  const del = chosen.filter((it) => !it.trash), trash = chosen.filter((it) => it.trash);
  const lines = [del.length ? t("clean.body.delete", { size: sum(del), names: names(del) }) : "",
    trash.length ? t("clean.body.trash", { size: sum(trash), names: names(trash) }) : "", t("clean.body.inUse")].filter(Boolean);
  setPending(() => clean(selections, bytes));
  modalTitle.textContent = t("clean.sel.title"); modalBody.innerHTML = lines.map((line) => `<p>${esc(line)}</p>`).join("");
  modalOk.textContent = t("clean.confirm"); showModal();
}

async function clean(selections: JunkSelection[], bytes: number) {
  if (phase !== "scanned") return;
  phase = "cleaning"; menuID = null; rowStates.clear();
  selections.forEach((s) => rowStates.set(s.id, { state: "waiting" }));
  Object.assign(progress, { total: bytes, bytes: 0, id: "", path: "", stopped: false, error: "" });
  const ch = new Channel<JunkCleanEvent>(); cleanChan = ch; setNavBusy("clean", true); render();
  try {
    const completion = Cleaner.cleanJunk(selections, scannedAt, ch).then(() => null, (e: unknown) => e);
    for await (const ev of ch) {
      if (ev.kind === "start") {
        rowStates.set(ev.id, { state: "cleaning" }); Object.assign(progress, { id: ev.id, path: "" }); render();
      } else if (ev.kind === "checking" || ev.kind === "removed") {
        Object.assign(progress, { id: ev.id, path: ev.path ?? "" });
        if (ev.kind === "removed") progress.bytes = ev.bytes ?? progress.bytes;
        renderHero();
      } else if (ev.kind === "done" && ev.result) {
        rowStates.set(ev.id, { state: "done", result: ev.result });
        progress.bytes = ev.bytes ?? progress.bytes; render();
      }
    }
    const error = await completion;
    if (error !== null) throw error;
  } catch (e) {
    if (!progress.stopped) {
      progress.error = t("clean.fail", { msg: errText(e) }); toast(progress.error, "err");
    }
  } finally {
    cleanChan = null;
    rowStates.forEach((row) => {
      if (row.state === "cleaning") { row.state = "stopped"; progress.stopped = true; }
      else if (row.state === "waiting") { row.state = "skipped"; progress.stopped = true; }
    });
    phase = "cleaned"; setNavBusy("clean", false); render();
  }
}

/** 保存成功才移走行；失败保留结果与选择。 */
async function ignore(it: JunkItem) {
  if (phase !== "scanned" || ignoring) return;
  ignoring = true; menuID = null; render();
  try {
    const settings = await Cleaner.getSettings();
    await Cleaner.saveSettings({ ...settings, ignored: [...new Set([...(settings.ignored ?? []), it.id])] });
    items = items.filter((x) => x.id !== it.id); selected.delete(it.id); subSel.delete(it.id); expanded.delete(it.id);
    if (summary) summary = { ...summary, ignored: summary.ignored + 1 };
    toast(t("junk.ignoredItem", { name: title(it) }));
  } catch (e) { toast(t("settings.saveFail", { msg: errText(e) }), "err"); }
  finally { ignoring = false; render(); }
}

export function markJunkStale() {
  if (phase === "idle") return;
  stale = true;
  if (phase === "scanned") render();
}

export function refreshJunkI18n() { render(); }

/** 只绑定一次，列表重绘不用重新注册监听。 */
export function initJunk() {
  scanBtn.addEventListener("click", () => { if (!stopScan(scanBtn)) void scan(); });
  cleanBtn.addEventListener("click", () => {
    if (phase === "cleaning" && cleanChan) { progress.stopped = true; cleanChan.close(); renderHero(); }
    else if (phase === "cleaned") void scan();
    else askClean();
  });
  listEl.addEventListener("change", (e) => {
    if (phase !== "scanned" || ignoring) return;
    const cb = e.target as HTMLInputElement;
    const it = items.find((x) => x.id === cb.dataset.id);
    if (cb.dataset.action === "group") items.filter((x) => x.group === cb.dataset.group).forEach((x) => setSelected(x, cb.checked));
    else if (it && cb.dataset.action === "item") setSelected(it, cb.checked);
    else if (it && selectable(it) && cb.dataset.action === "sub") {
      const sub = subsOf(it).find((s) => s.path === cb.dataset.path);
      if (!sub || sub.size <= 0) return;
      // 未选整项时，点单个子项不带入其他默认子项。
      const paths = selected.has(it.id) ? subSel.get(it.id) ?? new Set<string>() : new Set<string>();
      if (cb.checked) paths.add(sub.path); else paths.delete(sub.path);
      subSel.set(it.id, paths);
      if (paths.size) selected.add(it.id); else selected.delete(it.id);
    }
    render();
  });
  listEl.addEventListener("click", (e) => {
    const btn = (e.target as HTMLElement).closest<HTMLElement>("[data-action]");
    if (!btn || (btn instanceof HTMLButtonElement && btn.disabled)) return;
    const action = btn.dataset.action;
    const it = items.find((x) => x.id === btn.dataset.id);
    if (action === "scan") void scan();
    else if (action === "fold") {
      const group = btn.dataset.group!;
      if (collapsed.has(group)) collapsed.delete(group); else collapsed.add(group);
      render();
    } else if (action === "expand" && it) {
      if (expanded.has(it.id)) expanded.delete(it.id); else expanded.add(it.id);
      render();
    } else if (action === "all-apps") { showAllApps = true; render(); }
    else if (action === "grant") Cleaner.openFullDiskAccessSettings().catch((err) => toast(t("grant.fail", { msg: errText(err) }), "err"));
    else if (action === "leftovers") openLeftovers?.();
    else if (action === "settings") document.querySelector<HTMLButtonElement>('.nav-item[data-view="settings"]')?.click();
    else if (action === "menu" && it && phase === "scanned") {
      menuID = menuID === it.id ? null : it.id; render();
      listEl.querySelector<HTMLButtonElement>('.junk-menu button:not(:disabled)')?.focus();
    } else if (action === "reveal" && it?.paths?.[0]) {
      menuID = null; render();
      Cleaner.revealInFinder(it.paths[0]).catch((err) => toast(t("fs.revealFail", { msg: errText(err) }), "err"));
    } else if (action === "ignore" && it) void ignore(it);
  });
  document.addEventListener("click", (e) => {
    if (menuID && !(e.target as HTMLElement).closest('.junk-menu, [data-action="menu"]')) { menuID = null; render(); }
  });
  document.addEventListener("keydown", (e) => {
    if (!menuID) return;
    if (e.key === "Escape") { menuID = null; render(); }
    else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      const buttons = [...listEl.querySelectorAll<HTMLButtonElement>('.junk-menu button:not(:disabled)')];
      const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
      buttons[(index + (e.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length]?.focus();
      e.preventDefault();
    }
  });
  render();
}
