import { initJunk, refreshJunkI18n, setOpenLeftovers } from "./junk";
import { initReactAITools, showReactAITool } from "./react/ai/entry";
import { setSettingsLangHandler, showReactSettings } from "./react/settings/entry";
import { showReactLarge } from "./react/large/entry";
import { showReactPhotos } from "./react/photos/entry";
import { bumpLang } from "./ui/i18n-react";
import { initStatus, startStatus, stopStatus, refreshStatusI18n, setGoCleanView } from "./status";
import {
  $, errText, human, esc, fmtDate, displayPath, baseName, toast, emptyState, errorState,
  skeletonRows, setBusy, setStoppable, stopScan, endScan, stoppedScans, scanChans,
  markRescan, setNavBusy, VIEWS, overlay, modalTitle, modalBody, modalOk, showModal,
  hideModal, currentModalSeq, setPending, type View,
} from "./shared";
// Services and types are generated from the Go code by `mygo generate`.
import { Cleaner, LanguageSettings } from "./mygo";
import { Channel } from "mygo-runtime";
import type { AppInfo, BigItem, DirItem, DiskStat, FullScanResult, OrphanItem } from "./mygo";
import { t, kindLabel, currentLang, type LangPref } from "./i18n";
import { connectLanguage } from "./language";

const navItems = Array.from(document.querySelectorAll<HTMLButtonElement>(".nav-item"));
const appSort = $<HTMLSelectElement>("#app-sort");
const appSearch = $<HTMLInputElement>("#app-search");
const appListEl = $("#app-list");
const fsScanBtn = $<HTMLButtonElement>("#fs-scan");
const fsProgress = $("#fs-progress");
const fsCurrent = $("#fs-current");
const fsCount = $("#fs-count");
const fsList = $("#fs-list");
const fsToolbar = $("#fs-toolbar");
const fsUsed = $("#fs-used");
const fsFree = $("#fs-free");
const fsTotal = $("#fs-total");
const fsBar = $("#fs-bar") as HTMLDivElement;
const orScanBtn = $<HTMLButtonElement>("#or-scan");
const orSummary = $("#or-summary");
const orTotal = $("#or-total");
const orProgress = $("#or-progress");
const orCurrent = $("#or-current");
const orList = $("#or-list");
const orToolbar = $("#or-toolbar");

/** 小偏好：存储不可用时仍可操作。 */
function readChoice<T extends string>(key: string, choices: readonly T[], fallback: T): T {
  try {
    const value = localStorage.getItem(key);
    if (choices.includes(value as T)) return value as T;
  } catch { /* 忽略 */ }
  return fallback;
}
function saveChoice(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* 忽略 */ }
}
const appSorts = ["name", "size", "opened"] as const;
let appSortBy = readChoice("cleanzo.appSort", appSorts, "name");
appSort.value = appSortBy;

let apps: AppInfo[] = [];
const appBusy = new Set<string>();
let appsLoading = false;
let fsResult: FullScanResult | null = null;
let fsDisk: DiskStat | null = null;
let fsDiskSeq = 0;
let fsDiskPending = 0;
let fsRunning = false;
let fsItems: BigItem[] = [];
let orRunning = false;
let orTried = false;
let appsTab: "installed" | "leftovers" = "installed";
const appsTabs = Array.from(document.querySelectorAll<HTMLButtonElement>(".apps-tab"));
let orItems: OrphanItem[] = [];
let orFoot: { n: string; size: string; t: string } | null = null;

// ---- i18n ----

function applyStaticI18n() {
  document.documentElement.lang = currentLang() === "zh" ? "zh-CN" : "en";
  document.querySelectorAll<HTMLElement>("[data-i18n]").forEach((el) => {
    el.textContent = t(el.dataset.i18n as never);
  });
  document.querySelectorAll<HTMLElement>("[data-i18n-ph]").forEach((el) => {
    el.setAttribute("placeholder", t(el.dataset.i18nPh as never));
  });
  document.querySelectorAll<HTMLElement>("[data-i18n-title]").forEach((el) => {
    const text = t(el.dataset.i18nTitle as never);
    el.title = text;
    el.setAttribute("aria-label", text);
  });
  // 正在扫描的按钮是「停止扫描」，不是它的静态文案
  scanChans.forEach((_, btn) => (btn.innerHTML = `<span class="spin"></span>${esc(t("scan.stop"))}`));
}

/** Re-render the current view so all dynamic text follows the new language. */
function refreshI18n() {
  applyStaticI18n();
  // 每个已有内容的视图都按新语言重绘，不只是当前这个。垃圾清理页随时可以重绘；
  // 其他扫描中的视图等它结束后自己会重绘
  refreshJunkI18n();
  bumpLang();
  if (apps.length > 0) renderApps();
  if (fsDisk) renderDisk(fsDisk);
  if (fsResult && !fsRunning) renderFullScan(fsResult);
  renderIdleScanPages();
  if (!orRunning && orItems.length > 0) {
    if (orFoot) orTotal.textContent = t("orphans.foot", orFoot);
    renderOrphanRows(orItems);
  }
  renderAppsTabs();
  refreshStatusI18n();
}

const language = connectLanguage(window.mygo ? {
  locale: window.mygo.locale,
  setLanguage: preference => LanguageSettings.setLanguage(preference),
  onLocaleChange: listener => window.mygo!.on<{ locale: string }>("mygo:locale", data => listener(data.locale)),
} : undefined, refreshI18n);

// 语言切换由 React 设置页的 <select> 触发，统一走这里：成功/失败 toast 与旧页一致。
async function changeLanguage(pref: LangPref): Promise<void> {
  try {
    await language.change(pref);
    toast(t("settings.languageChanged"));
  } catch {
    toast(t("settings.languageFailed"));
    throw new Error("language change failed");
  }
}
setSettingsLangHandler(changeLanguage);

// ---- 交互态组件 ----

const stoppedFoot = () => `<div class="fs-foot muted small">${esc(t("scan.stopped"))}</div>`;

// ---- 视图切换 ----

function switchView(v: View, tool?: string) {
  // AI 工具的几个侧栏项共用一个视图，按 data-tool 区分高亮。
  navItems.forEach((b) => b.classList.toggle("active", b.dataset.view === v && (v !== "aitools" || b.dataset.tool === tool)));
  VIEWS.forEach((k) => $(`#view-${k}`).classList.toggle("hidden", k !== v));
  if (v === "status") startStatus(); else stopStatus();
  if (v === "aitools" && tool) {
    const names: Record<string, string> = { claude: "Claude Code", codex: "Codex" };
    showReactAITool(tool, names[tool] ?? tool);
  }
  if (v === "apps") selectAppsTab(appsTab);
  if (v === "fullscan" && !fsDisk && fsDiskPending === 0) void refreshDisk();
  if (v === "settings") showReactSettings();
  if (v === "large") showReactLarge();
  if (v === "photos") showReactPhotos();
}
navItems.forEach((b) => b.addEventListener("click", () => switchView(b.dataset.view as View, b.dataset.tool)));

// ---- 垃圾清理 ----

function renderIdleScanPages() {
  const guides = [
    { list: fsList, running: fsRunning, key: "fs", start: startFullScan,
      path: '<circle cx="12" cy="12" r="6.8" opacity="0.25"/><path d="M12 5.2v6.8h6.8"/><path d="M12 12l-4.8 4.8"/>' },
    { list: orList, running: orRunning, key: "orphans", start: startOrphanScan,
      path: '<path d="M4.8 9V7h5l2 2h7.4v9H4.8z"/><path d="M9.5 13.5h5" stroke-dasharray="1 2"/>' },
  ] as const;
  for (const g of guides) {
    if (g.running || (g.list.childElementCount > 0 && !g.list.querySelector(".empty-guide"))) continue;
    const grad = `eg-${g.key}-grad`;
    g.list.innerHTML = `
      <div class="empty-guide">
        <svg class="eg-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <circle cx="12" cy="12" r="10.2" stroke="url(#${grad})" stroke-width="1.4" stroke-dasharray="3.4 2.8" opacity="0.7"/>
          <g stroke="url(#${grad})" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">${g.path}</g>
          <defs><linearGradient id="${grad}" x1="0" y1="0" x2="24" y2="24"><stop offset="0" stop-color="#0fb986"/><stop offset="1" stop-color="#0a9e8e"/></linearGradient></defs>
        </svg>
        <h3>${esc(t(`${g.key}.idle.title`))}</h3>
        <p>${esc(t(`${g.key}.idle.desc`))}</p>
        <button class="btn primary big" type="button">${esc(t(`${g.key}.idle.cta`))}</button>
      </div>`;
    g.list.querySelector("button")?.addEventListener("click", g.start);
  }
}

// ---- 全盘扫描 ----

function renderFsRows(items: BigItem[], live: boolean, foot = "") {
  items = items.filter((it) => !isGone(lists.fullscan, it.path));
  if (items.length === 0) {
    fsList.innerHTML = emptyState(live ? t("fs.live") : t("fs.empty")) + foot;
    fsToolbar.classList.add("hidden");
    fsToolbar.innerHTML = "";
    return;
  }
  const sorted = [...items].sort((a, b) => b.size - a.size).slice(0, 30);
  const maxSize = sorted[0]?.size || 1;
  const rows = sorted
    .map(
      (it, i) =>
        fsRowHtml(lists.fullscan, {
          path: it.path,
          name: esc(displayPath(it.path)),
          size: it.size,
          rank: `${i + 1}`,
          expandable: true,
          trashable: it.trashable,
          locked: !it.trashable,
          sub: `<div class="fs-track"><div class="fs-fill" style="width:${Math.max(2, Math.round((it.size / maxSize) * 100))}%"></div></div>`,
        }),
    )
    .join("");
  const footHtml = (live ? `<div class="fs-foot muted small">${esc(t("fs.live"))}</div>` : "") + foot;
  renderFsListInto({
    st: lists.fullscan,
    list: fsList,
    toolbar: fsToolbar,
    rerender: () => renderFsRows(fsItems, false),
    removeRows: (removed) => {
      updateRowsAfterRemoved(fsItems, removed, lists.fullscan);
      if (fsResult) fsResult.items = fsItems;
    },
    sizeOf: (path) => fsItems.find((it) => it.path === path)?.size ?? childSize(lists.fullscan, path),
  }, rows + footHtml);
}

async function startFullScan() {
  if (fsRunning) return;
  resetList("fullscan");
  fsResult = null;
  fsRunning = true;
  fsItems = [];
  fsProgress.classList.remove("hidden");
  fsList.innerHTML = "";
  fsCurrent.textContent = t("fs.prep");
  fsCount.textContent = "";
  const ch = new Channel<import("./mygo").FullScanEvent>();
  setStoppable(fsScanBtn, ch);
  try {
    const done = Cleaner.fullScan(ch);
    for await (const ev of ch) {
      if (ev.kind === "item" && ev.path) {
        fsCurrent.textContent = t("fs.scanning", { path: displayPath(ev.path) });
        fsItems.push({ path: ev.path, size: ev.size ?? 0, trashable: ev.trashable ?? false });
        fsCount.textContent = t("fs.count", { n: fsItems.length.toLocaleString() });
        renderFsRows(fsItems, true);
      } else if (ev.kind === "done" && ev.result) {
        fsDiskSeq++; // 不让先前的磁盘读取覆盖扫描结果
        fsResult = ev.result;
        fsItems = ev.result.items;
        renderFullScan(ev.result);
      }
    }
    await done;
    if (!stoppedScans.has(fsScanBtn) || fsItems.length > 0) markRescan(fsScanBtn);
  } catch (e) {
    if (!stoppedScans.has(fsScanBtn)) {
      fsResult = null;
      fsList.innerHTML = errorState(t("fs.fail", { msg: errText(e) }), startFullScan);
      toast(t("fs.fail", { msg: errText(e) }), "err");
    }
  } finally {
    fsRunning = false;
    fsProgress.classList.add("hidden");
    if (stoppedScans.has(fsScanBtn) && fsItems.length > 0) markRescan(fsScanBtn);
    if (endScan(fsScanBtn)) {
      // 停止时保留已经扫到的，并说明是停止了，而不是没找到
      if (fsItems.length > 0) renderFsRows(fsItems, false, stoppedFoot());
      else fsList.innerHTML = emptyState(t("scan.stopped"));
    }
  }
}

// ---- 勾选与展开（全盘扫描 / 大文件 / 残留 / 相似照片） ----

/** 展开后的子项：存数据而不是 HTML，每次重绘都按当前勾选状态生成 */
type ChildList = { items: DirItem[]; total: number } | { error: string };

/** 每个列表视图自己的勾选和展开状态，视图之间互不影响 */
interface ListState {
  sel: Set<string>;
  expanded: Set<string>;
  children: Map<string, ChildList>;
  gone: Set<string>;
  busy: boolean;
}

const newList = (): ListState => ({ sel: new Set(), expanded: new Set(), children: new Map(), gone: new Set(), busy: false });
const lists = { fullscan: newList(), orphans: newList() };
/** 扫描重新开始：换一份新状态，并收起还握着旧勾选的工具栏 */
function resetList(v: keyof typeof lists) {
  lists[v] = newList();
  const toolbar = { fullscan: fsToolbar, orphans: orToolbar }[v];
  toolbar.classList.add("hidden");
  toolbar.innerHTML = "";
}

/** 一个列表视图的上下文：状态、容器和重绘 */
interface ListCtx {
  st: ListState;
  list: HTMLElement;
  toolbar: HTMLElement;
  rerender: () => void;
  removeRows: (removed: string[]) => void;
  sizeOf: (path: string) => number | undefined;
}

function under(path: string, ancestor: string): boolean {
  return path === ancestor || path.startsWith(ancestor + "/");
}

function effectiveRemoved(paths: string[]): string[] {
  return [...new Set(paths)].filter((p, _, all) => !all.some((other) => other !== p && under(p, other)));
}

function isGone(st: ListState, path: string): boolean {
  for (const gone of st.gone) if (under(path, gone)) return true;
  return false;
}

function checkHtml(st: ListState, path: string, parent?: string, size = -1, disabled = false): string {
  const par = parent ? ` data-parent="${esc(parent)}"` : "";
  return `<label class="fs-check${disabled ? " disabled" : ""}" title="${esc(path)}"><input type="checkbox" data-check="${esc(path)}" data-size="${size}"${par} ${st.sel.has(path) ? "checked" : ""}${disabled || st.busy ? " disabled" : ""}></label>`;
}

/** 展开的文件夹里已经加载出来的子项 */
function loadedKids(st: ListState, path: string): string[] {
  const c = st.children.get(path);
  return c && "items" in c ? c.items.filter((i) => !isGone(st, i.path)).map((i) => i.path) : [];
}

/** 勾选一整行：文件夹连同已加载的子项一起勾上或取消 */
function toggleRow(st: ListState, path: string, on: boolean) {
  if (isGone(st, path)) return;
  if (on) {
    st.sel.add(path);
    for (const p of loadedKids(st, path)) st.sel.add(p);
    return;
  }
  for (const p of [...st.sel]) if (under(p, path)) st.sel.delete(p);
}

/** 勾选文件夹里的一项。取消其中一项时，文件夹不再整个移走，改为勾选其余已加载子项 */
function toggleChild(st: ListState, parent: string, child: string, on: boolean) {
  if (isGone(st, child)) return;
  if (on) {
    st.sel.add(child);
    return;
  }
  if (st.sel.delete(parent)) {
    for (const k of loadedKids(st, parent)) st.sel.add(k);
  }
  st.sel.delete(child);
}

/** 要移到废纸篓的路径：勾选的文件夹已经包含它里面的子项 */
function pickedPaths(st: ListState): string[] {
  return [...st.sel].filter((p) => !isGone(st, p) && ![...st.sel].some((q) => q !== p && under(p, q)));
}

function applyIndeterminate(ctx: ListCtx) {
  ctx.list.querySelectorAll<HTMLDivElement>(".fs-row-wrap").forEach((wrap) => {
    const p = wrap.dataset.path!;
    const cb = wrap.querySelector<HTMLInputElement>("input[data-check]");
    if (!cb || cb.disabled || ctx.st.sel.has(p)) return;
    cb.indeterminate = loadedKids(ctx.st, p).some((k) => ctx.st.sel.has(k));
  });
}

function expandBtnHtml(st: ListState, path: string): string {
  const open = st.expanded.has(path);
  return `<button class="btn small ghost fs-expand" data-expand="${esc(path)}" type="button"${st.busy ? " disabled" : ""}>${open ? esc(t("fs.collapse")) : esc(t("fs.expand"))} <span class="caret">▸</span></button>`;
}

function revealBtnHtml(path: string): string {
  return `<button class="btn small ghost icon fs-reveal" data-reveal="${esc(path)}" type="button" title="${esc(t("fs.reveal"))}" aria-label="${esc(t("fs.reveal"))}"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/></svg></button>`;
}

function fsRowHtml(st: ListState, o: {
  path: string;
  name: string;
  size: number;
  sub?: string;
  rank?: string;
  expandable?: boolean;
  trashable: boolean;
  locked?: boolean;
}): string {
  if (isGone(st, o.path)) return "";
  const open = st.expanded.has(o.path);
  const name = !o.trashable && o.locked ? `${o.name} <span class="tag dim">${esc(t("fs.locked"))}</span>` : o.name;
  const trash = o.trashable
    ? `<button class="btn small danger fs-trash" data-path="${esc(o.path)}" data-size="${o.size}" type="button"${st.busy ? " disabled" : ""}>${st.busy ? `<span class="spin"></span>${esc(t("fs.moving"))}` : esc(t("fs.trash"))}</button>`
    : "";
  return `
      <div class="fs-row-wrap" data-path="${esc(o.path)}">
        <div class="fs-row card ${open ? "open" : ""}">
          ${checkHtml(st, o.path, undefined, o.size, !o.trashable)}
          ${o.rank ? `<div class="fs-rank">${o.rank}</div>` : ""}
          <div class="fs-main">
            <div class="fs-path" title="${esc(o.path)}">${name}</div>
            ${o.sub ? `<div class="fs-sub">${o.sub}</div>` : ""}
          </div>
          <div class="fs-size">${o.size >= 0 ? human(o.size) : "--"}</div>
          ${o.expandable ? expandBtnHtml(st, o.path) : ""}
          ${revealBtnHtml(o.path)}
          ${trash}
        </div>
        ${open ? `<div class="fs-children">${childrenHtml(st, o.path)}</div>` : ""}
      </div>`;
}

function childrenHtml(st: ListState, parent: string): string {
  const c = st.children.get(parent);
  if (!c) return `<span class="muted small">${esc(t("fs.children"))}</span>`;
  if ("error" in c) return `<div class="fs-child err-inline">${esc(t("fs.childrenFail", { msg: c.error }))}</div>`;
  const items = c.items.filter((it) => !isGone(st, it.path));
  if (items.length === 0) return `<div class="fs-child muted small">${esc(t("fs.childrenEmpty"))}</div>`;
  const rows = items
    .map(
      (it) => `
      <div class="fs-child">
        ${checkHtml(st, it.path, parent, it.size, !it.trashable)}
        <span class="fs-child-name" title="${esc(it.path)}">${esc(it.name)}${it.trashable ? "" : ` <span class="tag dim">${esc(t("fs.locked"))}</span>`}</span>
        <span class="fs-child-size">${it.size >= 0 ? human(it.size) : "--"}</span>
        ${revealBtnHtml(it.path)}
        ${it.trashable ? `<button class="btn small danger fs-child-trash" data-path="${esc(it.path)}" data-size="${it.size}" type="button"${st.busy ? " disabled" : ""}>${st.busy ? `<span class="spin"></span>${esc(t("fs.moving"))}` : esc(t("fs.trash"))}</button>` : ""}
      </div>`,
    )
    .join("");
  const more = c.total > items.length ? `<div class="fs-child muted small">${esc(t("fs.childrenMore", { n: items.length, total: c.total }))}</div>` : "";
  return rows + more;
}

function subtractRemovedFromState(st: ListState, removed: string[]) {
  const roots = effectiveRemoved(removed);
  const known = new Map<string, number>();
  for (const c of st.children.values()) {
    if (!("items" in c)) continue;
    for (const it of c.items) if (it.size >= 0) known.set(it.path, it.size);
  }
  for (const path of roots) st.gone.add(path);
  for (const p of [...st.sel]) if (isGone(st, p)) st.sel.delete(p);
  for (const p of [...st.expanded]) if (isGone(st, p)) st.expanded.delete(p);
  for (const p of [...st.children.keys()]) if (isGone(st, p)) st.children.delete(p);
  for (const [parent, c] of st.children) {
    if (!("items" in c)) continue;
    const kept = c.items.filter((it) => !roots.some((r) => under(it.path, r)));
    c.total = Math.max(kept.length, c.total - (c.items.length - kept.length));
    for (const it of kept) {
      if (it.size < 0) continue;
      const delta = roots
        .filter((r) => r !== it.path && under(r, it.path))
        .reduce((sum, r) => sum + (known.get(r) ?? 0), 0);
      if (delta > 0) it.size = Math.max(0, it.size - delta);
    }
    c.items = kept;
    st.children.set(parent, c);
  }
}

function updateRowsAfterRemoved<T extends { path: string; size: number }>(items: T[], removed: string[], st: ListState): void {
  const roots = effectiveRemoved(removed);
  const known = new Map<string, number>();
  for (const it of items) if (it.size >= 0) known.set(it.path, it.size);
  for (const c of st.children.values()) {
    if (!("items" in c)) continue;
    for (const it of c.items) if (it.size >= 0) known.set(it.path, it.size);
  }
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i]!;
    if (roots.some((r) => under(it.path, r))) {
      items.splice(i, 1);
      continue;
    }
    if (it.size >= 0) {
      const delta = roots
        .filter((r) => r !== it.path && under(r, it.path))
        .reduce((sum, r) => sum + (known.get(r) ?? 0), 0);
      if (delta > 0) it.size = Math.max(0, it.size - delta);
    }
  }
}

function applyRemoved(ctx: ListCtx, removed: string[]) {
  if (removed.length === 0) return;
  // 先改各行的数据：它要从还没删掉的子项列表里查出移走的子项有多大
  ctx.removeRows(removed);
  subtractRemovedFromState(ctx.st, removed);
  ctx.rerender();
  if (ctx.st === lists.fullscan) void refreshDisk();
}

async function toggleExpand(ctx: ListCtx, path: string) {
  const { st } = ctx;
  if (st.busy || isGone(st, path)) return;
  if (st.expanded.delete(path)) {
    st.children.delete(path);
    ctx.rerender();
    return;
  }
  st.expanded.add(path);
  ctx.rerender();
  try {
    const r = await Cleaner.dirChildren(path);
    if (!isGone(st, path)) {
      st.children.set(path, { items: r.items, total: r.total });
      if (st.sel.has(path)) toggleRow(st, path, true);
    }
  } catch (e) {
    if (!isGone(st, path)) st.children.set(path, { error: errText(e) });
  }
  if (st.expanded.has(path)) ctx.rerender();
}

function childSize(st: ListState, path: string): number | undefined {
  for (const c of st.children.values()) {
    if (!("items" in c)) continue;
    const it = c.items.find((item) => item.path === path);
    if (it) return it.size;
  }
  return undefined;
}

function updateToolbar(ctx: ListCtx) {
  const { st, toolbar } = ctx;
  const allInputs = Array.from(ctx.list.querySelectorAll<HTMLInputElement>("input[data-check]:not([data-parent])"));
  const rows = allInputs.filter((i) => !i.disabled).map((i) => i.dataset.check!);
  const hasRows = ctx.list.querySelectorAll(".fs-row-wrap").length > 0;
  const picked = pickedPaths(st);
  const size = picked.reduce((sum, p) => {
    const n = ctx.sizeOf(p);
    return sum + (n !== undefined && n >= 0 ? n : 0);
  }, 0);
  if (!hasRows) {
    toolbar.classList.add("hidden");
    toolbar.innerHTML = "";
    return;
  }
  const some = rows.some((p) => st.sel.has(p));
  const all = rows.length > 0 && rows.every((p) => st.sel.has(p));
  toolbar.classList.remove("hidden");
  toolbar.innerHTML = `
    <label class="fs-check"><input type="checkbox" class="fs-checkall" ${all ? "checked" : ""}${rows.length === 0 || st.busy ? " disabled" : ""}></label>
    <span class="fs-toolbar-count">${esc(picked.length > 0 ? t("fs.checkedSize", { n: picked.length, size: human(size) }) : t("fs.nothing"))}</span>
    <button class="btn danger small fs-toolbar-trash" type="button"${picked.length === 0 || st.busy ? " disabled" : ""}>${st.busy ? `<span class="spin"></span>${esc(t("fs.moving"))}` : esc(t("fs.trashChecked"))}</button>`;
  const allBox = toolbar.querySelector<HTMLInputElement>(".fs-checkall");
  if (allBox) allBox.indeterminate = !all && some;
  allBox?.addEventListener("change", () => {
    rows.forEach((p) => toggleRow(st, p, allBox.checked));
    ctx.rerender();
  });
  toolbar.querySelector<HTMLButtonElement>(".fs-toolbar-trash")?.addEventListener("click", () => trashChecked(ctx));
}

async function trashChecked(ctx: ListCtx) {
  const paths = pickedPaths(ctx.st);
  if (paths.length === 0 || ctx.st.busy) return;
  setPending(async () => {
    ctx.st.busy = true;
    ctx.rerender();
    try {
      const r = await Cleaner.cleanPaths(paths);
      const parts: string[] = [];
      if (r.moved > 0) parts.push(t("fs.trashChecked.ok", { n: r.moved, size: human(r.freed) }));
      if (r.inUse > 0) parts.push(t("fs.trashChecked.inUse", { n: r.inUse }));
      if (r.failed.length > 0) parts.push(t("fs.trashChecked.fail", { n: r.failed.length }));
      toast(parts.join(t("phrase.sep")) || t("fs.trashChecked.none"), r.moved > 0 && r.failed.length === 0 ? "ok" : "err");
      applyRemoved(ctx, r.removed);
    } catch (e) {
      toast(t("fs.actionFail", { msg: errText(e) }), "err");
    } finally {
      ctx.st.busy = false;
      ctx.rerender();
    }
  });
  modalTitle.textContent = t("fs.trashChecked.title");
  modalBody.textContent = t("fs.trashChecked.body", { n: paths.length });
  modalOk.textContent = t("fs.trash.confirm");
  showModal();
}

function bindFsList(ctx: ListCtx) {
  const { st, list } = ctx;
  list.querySelectorAll<HTMLInputElement>("input[data-check]").forEach((cb) => {
    cb.addEventListener("change", () => {
      if (cb.disabled) return;
      const p = cb.dataset.check!;
      const parent = cb.dataset.parent;
      if (parent) toggleChild(st, parent, p, cb.checked);
      else toggleRow(st, p, cb.checked);
      ctx.rerender();
    });
  });
  list.querySelectorAll<HTMLButtonElement>(".fs-expand").forEach((b) => {
    b.addEventListener("click", () => toggleExpand(ctx, b.dataset.expand!));
  });
  list.querySelectorAll<HTMLButtonElement>(".fs-reveal").forEach((b) => {
    b.addEventListener("click", () => {
      const path = b.dataset.reveal!;
      Cleaner.revealInFinder(path).catch((e) => toast(t("fs.revealFail", { msg: errText(e) }), "err"));
    });
  });
  list.querySelectorAll<HTMLButtonElement>(".fs-trash, .fs-child-trash").forEach((b) => {
    b.addEventListener("click", () => askTrash(b.dataset.path!, Number(b.dataset.size ?? 0), ctx));
  });
}

function renderFsListInto(ctx: ListCtx, html: string) {
  ctx.list.innerHTML = html;
  bindFsList(ctx);
  applyIndeterminate(ctx);
  updateToolbar(ctx);
}

/** 磁盘卡不依赖目录扫描。 */
function renderDisk(disk: DiskStat) {
  fsDisk = disk;
  const usedPct = disk.total ? Math.min(100, Math.round((disk.used / disk.total) * 100)) : 0;
  fsUsed.textContent = human(disk.used);
  fsFree.textContent = human(disk.free);
  fsTotal.textContent = human(disk.total);
  fsBar.style.width = `${usedPct}%`;
}

async function refreshDisk() {
  const seq = ++fsDiskSeq;
  fsDiskPending++;
  try {
    const disk = await Cleaner.disk();
    if (seq !== fsDiskSeq) return;
    if (fsResult) fsResult.disk = disk;
    renderDisk(disk);
  } catch {
    // 读取失败时保留上次容量。
  } finally {
    fsDiskPending--;
  }
}

function renderFullScan(r: FullScanResult) {
  renderDisk(r.disk);
  fsCount.textContent = "";
  renderFsRows(r.items, false);
}

/** 将一次性移动错误翻成当前语言。 */
const failText = (e: unknown) => t("fs.actionFail", { msg: errText(e) });

function askTrash(path: string, size: number, ctx: ListCtx) {
  if (ctx.st.busy || isGone(ctx.st, path)) return;
  setPending(async () => {
    ctx.st.busy = true;
    ctx.rerender();
    try {
      const res = await Cleaner.trashPath(path);
      toast(t("fs.trash.ok", { path: displayPath(path), size: human(res.freed) }));
      applyRemoved(ctx, [path]);
    } catch (e) {
      toast(failText(e), "err");
    } finally {
      ctx.st.busy = false;
      ctx.rerender();
    }
  });
  modalTitle.textContent = t("fs.trash.title");
  modalBody.textContent = t("fs.trash.body", { path: displayPath(path), size: human(size) });
  modalOk.textContent = t("fs.trash.confirm");
  showModal();
}

// ---- 残留清理 ----

async function startOrphanScan() {
  if (orRunning) return;
  resetList("orphans");
  orFoot = null;
  orRunning = true;
  orTried = true;
  orItems = [];
  renderAppsTabs();
  orSummary.classList.add("hidden");
  orProgress.classList.remove("hidden");
  orCurrent.textContent = t("orphans.live");
  orList.innerHTML = "";
  const ch = new Channel<OrphanItem>();
  setStoppable(orScanBtn, ch);
  try {
    const done = Cleaner.scanOrphans(ch);
    for await (const o of ch) {
      if (o.kind === "__done") {
        const total = o.size ?? 0;
        const elapsed = Number(o.path ?? 0) / 1000;
        orProgress.classList.add("hidden");
        orSummary.classList.remove("hidden");
        orFoot = { n: orItems.length.toLocaleString(), size: human(total), t: elapsed.toFixed(1) };
        orTotal.textContent = t("orphans.foot", orFoot);
        if (orItems.length === 0) {
          orList.innerHTML = emptyState(t("orphans.empty"));
        }
        continue;
      }
      orItems.push(o);
      renderAppsTabs();
      orCurrent.textContent = t("orphans.count", { n: orItems.length.toLocaleString() });
      renderOrphanRows(orItems);
    }
    await done;
    if (!stoppedScans.has(orScanBtn) || orItems.length > 0) markRescan(orScanBtn);
  } catch (e) {
    if (!stoppedScans.has(orScanBtn)) {
      orItems = [];
      orList.innerHTML = errorState(t("orphans.fail", { msg: errText(e) }), startOrphanScan);
      toast(t("orphans.fail", { msg: errText(e) }), "err");
    }
  } finally {
    orRunning = false;
    orProgress.classList.add("hidden");
    if (stoppedScans.has(orScanBtn) && orItems.length > 0) markRescan(orScanBtn);
    if (endScan(orScanBtn) && orItems.length === 0) orList.innerHTML = emptyState(t("scan.stopped"));
    renderAppsTabs();
    syncAppsBusy();
  }
}

function renderOrphanRows(items: OrphanItem[]) {
  items = items.filter((o) => !isGone(lists.orphans, o.path));
  if (items.length === 0) {
    orList.innerHTML = emptyState(t("orphans.empty"));
    orToolbar.classList.add("hidden");
    orToolbar.innerHTML = "";
    return;
  }
  const sorted = [...items].sort((a, b) => b.size - a.size);
  const maxSize = sorted[0]?.size || 1;
  const rows = sorted
    .map(
      (o, i) =>
        fsRowHtml(lists.orphans, {
          path: o.path,
          name: `${esc(displayPath(o.path))} <span class="tag dim">${esc(kindLabel(o.kind))}</span>`,
          size: o.size,
          rank: `${i + 1}`,
          expandable: o.isDir,
          trashable: o.trashable,
          locked: !o.trashable,
          sub: `<div class="fs-track"><div class="fs-fill" style="width:${Math.max(2, Math.round((o.size / maxSize) * 100))}%"></div></div>`,
        }),
    )
    .join("");
  renderFsListInto({
    st: lists.orphans,
    list: orList,
    toolbar: orToolbar,
    rerender: () => renderOrphanRows(orItems),
    removeRows: (removed) => {
      updateRowsAfterRemoved(orItems, removed, lists.orphans);
      renderAppsTabs();
      if (orFoot) {
        orFoot.n = orItems.length.toLocaleString();
        orFoot.size = human(orItems.reduce((sum, it) => sum + (it.size >= 0 ? it.size : 0), 0));
        orTotal.textContent = t("orphans.foot", orFoot);
      }
    },
    sizeOf: (path) => orItems.find((it) => it.path === path)?.size ?? childSize(lists.orphans, path),
  }, rows);
}

// ---- 应用卸载 ----

/** 应用与残留的后台任务共用导航提示。 */
function syncAppsBusy() {
  setNavBusy("apps", appsLoading || appBusy.size > 0 || orRunning);
}

function renderAppsTabs() {
  appsTabs.forEach((b) => {
    const active = b.dataset.tab === appsTab;
    b.classList.toggle("active", active);
    b.setAttribute("aria-selected", String(active));
    b.tabIndex = active ? 0 : -1;
  });
  $("#apps-tab-leftovers").textContent = t("apps.tab.leftoversCount", { n: orItems.length });
}

function selectAppsTab(tab: typeof appsTab) {
  appsTab = tab;
  $("#apps-installed").classList.toggle("hidden", tab !== "installed");
  $("#apps-leftovers").classList.toggle("hidden", tab !== "leftovers");
  renderAppsTabs();
  if (tab === "installed" && apps.length === 0 && !appsLoading) void loadApps();
  if (tab === "leftovers" && !orTried) void startOrphanScan();
}

appsTabs.forEach((b, i) => {
  b.addEventListener("click", () => selectAppsTab(b.dataset.tab as typeof appsTab));
  b.addEventListener("keydown", (e) => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)) return;
    e.preventDefault();
    const next = e.key === "Home" ? 0 : e.key === "End" ? appsTabs.length - 1 : (i + 1) % appsTabs.length;
    appsTabs[next]?.click();
    appsTabs[next]?.focus();
  });
});

async function loadApps() {
  if (appsLoading) return;
  appsLoading = true;
  syncAppsBusy();
  appListEl.innerHTML = skeletonRows(8);
  try {
    apps = await Cleaner.listApps();
    renderApps();
  } catch (e) {
    apps = [];
    appListEl.innerHTML = errorState(t("apps.loadFail", { msg: errText(e) }), loadApps);
    toast(t("apps.loadFail", { msg: errText(e) }), "err");
  } finally {
    appsLoading = false;
    syncAppsBusy();
  }
}

function renderApps() {
  const q = appSearch.value.trim().toLowerCase();
  const list = apps.filter(
    (a) => !q || a.name.toLowerCase().includes(q) || a.path.toLowerCase().includes(q),
  ).sort((a, b) => {
    const byName = () => a.name.localeCompare(b.name, currentLang() === "zh" ? "zh-CN" : "en");
    if (appSortBy === "size") return b.size - a.size || byName();
    if (appSortBy === "opened") return b.lastOpened - a.lastOpened || byName();
    return byName();
  });
  if (apps.length === 0) {
    appListEl.innerHTML = emptyState(t("apps.empty"));
    return;
  }
  if (list.length === 0) {
    appListEl.innerHTML = emptyState(t("apps.noMatch", { q }));
    return;
  }
  appListEl.innerHTML = list
    .map(
      (a) => `
      <div class="app-row card">
        <div class="app-icon">${esc(a.name.charAt(0).toUpperCase())}</div>
        <div class="app-main">
          <div class="app-name">${esc(a.name)}${a.running ? ` <span class="tag ok">${esc(t("apps.running"))}</span>` : ""}${a.version ? ` <span class="ver muted">${esc(a.version)}</span>` : ""}</div>
          <div class="app-path muted">${esc(a.path)}</div>
        </div>
        <div class="app-meta">
          <div class="app-size">${human(a.size)}</div>
          <div class="app-used muted small">${a.lastOpened ? fmtDate(a.lastOpened) : esc(t("apps.never"))}</div>
        </div>
        <button class="btn small uninstall-detail" data-path="${esc(a.path)}"${appBusy.has(a.path) ? " disabled" : ""}>${appBusy.has(a.path) ? `<span class="spin"></span>${esc(t("apps.moving"))}` : esc(t("apps.detail"))}</button>
      </div>`,
    )
    .join("");
  appListEl.querySelectorAll<HTMLButtonElement>(".uninstall-detail").forEach((b) => {
    b.addEventListener("click", () => showUninstallDetail(b.dataset.path!));
  });
}

async function showUninstallDetail(path: string) {
  modalTitle.textContent = t("apps.analyzing");
  modalBody.textContent = t("apps.analyzingBody");
  modalOk.disabled = true;
  modalOk.textContent = t("apps.wait");
  showModal();
  const seq = currentModalSeq();
  try {
    const d = await Cleaner.appUninstallDetail(path);
    if (seq !== currentModalSeq()) return; // 这个框已经关掉或换成了别的
    modalTitle.textContent = t("apps.title", { name: d.app.name });
    const rows = d.items
      .map(
        (it) =>
          `<div class="detail-item"><span class="kind">${esc(kindLabel(it.kind))}</span><span class="path">${esc(it.path)}</span><span class="size">${human(it.size)}</span></div>`,
      )
      .join("");
    // 正在运行的应用卸不了：照样列出文件，但不问「确定卸载吗」，只请用户先退出
    const note = d.app.running ? t("apps.inUse", { name: d.app.name }) : t("apps.body", { n: d.items.length, size: human(d.totalSize) });
    modalBody.innerHTML = `<div class="detail-list">${rows}</div>
      <p class="warn-text">${esc(note)}</p>`;
    if (d.app.running) {
      setPending(null);
      modalOk.textContent = t("btn.close");
      return;
    }
    setPending(async () => {
      if (appBusy.has(d.app.path)) return;
      appBusy.add(d.app.path);
      syncAppsBusy();
      renderApps();
      try {
        const res = await Cleaner.uninstallApp(d.app.path);
        const bundleFailed = res.failed.some((f) => f === res.path || f.startsWith(`${res.path}:`));
        if (res.moved === 0 && res.failed.some((f) => f.endsWith(": in-use"))) {
          toast(t("apps.inUse", { name: d.app.name }), "err");
        } else if (res.failed.length > 0) {
          toast(t("apps.partial", { name: d.app.name, n: res.failed.length }), "err");
        } else {
          toast(t("apps.ok", { name: d.app.name, size: human(res.freed) }));
        }
        if (!bundleFailed) apps = apps.filter((a) => a.path !== res.path);
        renderApps();
      } catch (e) {
        toast(t("apps.fail", { msg: errText(e) }), "err");
      } finally {
        appBusy.delete(d.app.path);
        syncAppsBusy();
        renderApps();
      }
    });
    modalOk.textContent = t("apps.confirm");
  } catch (e) {
    if (seq !== currentModalSeq()) return;
    modalBody.innerHTML = `<div class="err-inline">${esc(t("apps.analyzeFail", { msg: errText(e) }))}</div>`;
    setPending(null);
    modalOk.textContent = t("btn.close");
  } finally {
    if (seq === currentModalSeq()) modalOk.disabled = false;
  }
}

// ---- 事件 ----

fsScanBtn.addEventListener("click", () => stopScan(fsScanBtn) || startFullScan());
orScanBtn.addEventListener("click", () => stopScan(orScanBtn) || startOrphanScan());
appSearch.addEventListener("input", renderApps);
appSort.addEventListener("change", () => {
  appSortBy = appSort.value as typeof appSortBy;
  saveChoice("cleanzo.appSort", appSortBy);
  if (!appsLoading) renderApps();
});
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && !overlay.classList.contains("hidden")) hideModal();
});

applyStaticI18n();
initJunk();
// AI 工具页是 React 挂载的，无需原生初始化；侧栏显隐由后端 listAITools 决定，启动时已安装即可见。
initStatus();
// 默认落在「系统状态」（HTML 初始高亮已同步），启动即开始采样
void initReactAITools();
switchView("status");
setGoCleanView(() => switchView("clean"));
setOpenLeftovers(() => {
  appsTab = "leftovers";
  switchView("apps");
});
renderIdleScanPages();
renderAppsTabs();
// 版本号以应用自己为准，不写死在页面里
Cleaner.info()
  .then((i) => {
    if (i.version) $("#app-version").textContent = `Cleanzo v${i.version}`;
  })
  .catch(() => {});

void language.initialize()
  .catch(() => toast(t("settings.languageFailed")));
