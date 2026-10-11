import { showSettings, refreshSettingsI18n } from "./settings";
import { initJunk, refreshJunkI18n, setOpenLeftovers } from "./junk";
import {
  $, errText, human, esc, fmtDate, displayPath, baseName, toast, emptyState, errorState,
  skeletonRows, setBusy, setStoppable, stopScan, endScan, stoppedScans, scanChans,
  markRescan, setNavBusy, VIEWS, overlay, modalTitle, modalBody, modalOk, showModal,
  hideModal, currentModalSeq, setPending, setBeforeModalShow, type View,
} from "./shared";
// Services and types are generated from the Go code by `mygo generate`.
import { Cleaner } from "./mygo";
import { Channel } from "mygo-runtime";
import type { AppInfo, BigItem, DirItem, DiskStat, FullScanResult, LargeFile, LargeScanResult, OrphanItem, PhotoCluster } from "./mygo";
import { t, kindLabel, getPref, setPref, currentLang, type LangPref } from "./i18n";

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
const langSelect = $<HTMLSelectElement>("#lang-select");
const lgScanBtn = $<HTMLButtonElement>("#lg-scan");
const lgProgress = $("#lg-progress");
const lgCurrent = $("#lg-current");
const lgCount = $("#lg-count");
const lgList = $("#lg-list");
const lgToolbar = $("#lg-toolbar");
const lgSize = $<HTMLSelectElement>("#lg-size");
const orScanBtn = $<HTMLButtonElement>("#or-scan");
const orSummary = $("#or-summary");
const orTotal = $("#or-total");
const orProgress = $("#or-progress");
const orCurrent = $("#or-current");
const orList = $("#or-list");
const orToolbar = $("#or-toolbar");
const phScanBtn = $<HTMLButtonElement>("#ph-scan");
const phProgress = $("#ph-progress");
const phCurrent = $("#ph-current");
const phBar = $("#ph-bar");
const phList = $("#ph-list");
const phToolbar = $("#ph-toolbar");
const photoViewer = $("#photo-viewer");
const pvName = $("#pv-name");
const pvMeta = $("#pv-meta");
const pvKeep = $("#pv-keep");
const pvImage = $<HTMLImageElement>("#pv-image");
const pvLoading = $("#pv-loading");
const pvError = $("#pv-error");
const pvCheck = $<HTMLInputElement>("#pv-check");
const pvClose = $<HTMLButtonElement>("#pv-close");
const pvPrev = $<HTMLButtonElement>("#pv-prev");
const pvNext = $<HTMLButtonElement>("#pv-next");

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
const largeSizes = ["104857600", "524288000", "1073741824", "5368709120"] as const;
let lgMinSize = Number(readChoice("cleanzo.largeSize", largeSizes, "1073741824"));
appSort.value = appSortBy;
lgSize.value = String(lgMinSize);

let apps: AppInfo[] = [];
const appBusy = new Set<string>();
let appsLoading = false;
let fsResult: FullScanResult | null = null;
let fsDisk: DiskStat | null = null;
let fsDiskSeq = 0;
let fsDiskPending = 0;
let fsRunning = false;
let fsItems: BigItem[] = [];
let lgRunning = false;
let lgItems: LargeFile[] = [];
let lgResult: LargeScanResult | null = null;
let lgScannedThreshold: number | null = null;
let lgRestartQueued = false;
let lgStopped = false;
let orRunning = false;
let orTried = false;
let appsTab: "installed" | "leftovers" = "installed";
const appsTabs = Array.from(document.querySelectorAll<HTMLButtonElement>(".apps-tab"));
let orItems: OrphanItem[] = [];
let orFoot: { n: string; size: string; t: string } | null = null;
let phRunning = false;
let phClusters: PhotoCluster[] = [];
let phScanned = 0;
/** 相似照片扫描是否已经开始过：停掉之后再进这个视图不会自己重新开始 */
let phTried = false;

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
  langSelect.value = getPref();
}

/** Re-render the current view so all dynamic text follows the new language. */
function refreshI18n() {
  applyStaticI18n();
  // 每个已有内容的视图都按新语言重绘，不只是当前这个。垃圾清理页随时可以重绘；
  // 其他扫描中的视图等它结束后自己会重绘
  refreshJunkI18n();
  if (apps.length > 0) renderApps();
  refreshSettingsI18n();
  if (fsDisk) renderDisk(fsDisk);
  if (fsResult && !fsRunning) renderFullScan(fsResult);
  renderIdleScanPages();
  if (lgResult || lgItems.length > 0) renderLargeCurrent();
  if (!orRunning && orItems.length > 0) {
    if (orFoot) orTotal.textContent = t("orphans.foot", orFoot);
    renderOrphanRows(orItems);
  }
  renderAppsTabs();
  if (phClusters.length > 0 && !phRunning) {
    renderPhotoClusters();
  }
  syncPhotoViewer();
}

langSelect.addEventListener("change", () => {
  setPref(langSelect.value as LangPref);
  refreshI18n();
  toast(t("settings.languageChanged"));
});

// ---- 交互态组件 ----

const stoppedFoot = () => `<div class="fs-foot muted small">${esc(t("scan.stopped"))}</div>`;

// ---- 视图切换 ----

function switchView(v: View) {
  navItems.forEach((b) => b.classList.toggle("active", b.dataset.view === v));
  VIEWS.forEach((k) => $(`#view-${k}`).classList.toggle("hidden", k !== v));
  if (v === "apps") selectAppsTab(appsTab);
  if (v === "fullscan" && !fsDisk && fsDiskPending === 0) void refreshDisk();
  if (v === "settings") showSettings();
  if (v === "photos" && !phTried) startPhotoScan();
}
navItems.forEach((b) => b.addEventListener("click", () => switchView(b.dataset.view as View)));

// ---- 垃圾清理 ----

function renderIdleScanPages() {
  const guides = [
    { list: fsList, running: fsRunning, key: "fs", start: startFullScan,
      path: '<circle cx="12" cy="12" r="6.8" opacity="0.25"/><path d="M12 5.2v6.8h6.8"/><path d="M12 12l-4.8 4.8"/>' },
    { list: lgList, running: lgRunning, key: "large", start: startLargeScan,
      path: '<path d="M7 4.8h6.5l4 4v10.4H7z"/><path d="M13.5 4.8v4h4M10 12.5h4.5M10 15.5h4.5"/>' },
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
        <p>${esc(t(`${g.key}.idle.desc`, { size: human(lgMinSize) }))}</p>
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
const lists = { fullscan: newList(), large: newList(), orphans: newList(), photos: newList() };
/** 扫描重新开始：换一份新状态，并收起还握着旧勾选的工具栏 */
function resetList(v: keyof typeof lists) {
  lists[v] = newList();
  const toolbar = { fullscan: fsToolbar, large: lgToolbar, orphans: orToolbar, photos: phToolbar }[v];
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

// ---- 大文件专清 ----

async function startLargeScan() {
  if (lgRunning) return;
  resetList("large");
  lgResult = null;
  lgScannedThreshold = lgMinSize;
  lgStopped = false;
  lgRunning = true;
  lgItems = [];
  lgProgress.classList.remove("hidden");
  lgList.innerHTML = "";
  lgCurrent.textContent = t("fs.prep");
  lgCount.textContent = "";
  const ch = new Channel<import("./mygo").LargeScanEvent>();
  setStoppable(lgScanBtn, ch);
  try {
    const done = Cleaner.largeScan(lgScannedThreshold, ch);
    for await (const ev of ch) {
      if (lgRestartQueued) continue; // 停下后重扫，旧结果不再显示
      if (ev.kind === "item" && ev.path) {
        lgCurrent.textContent = t("fs.scanning", { path: displayPath(ev.path) });
        lgItems.push({ path: ev.path, size: ev.size ?? 0, trashable: ev.trashable ?? false });
        renderLargeRows(lgItems, true);
      } else if (ev.kind === "done" && ev.result) {
        lgItems = ev.result.items;
        lgResult = ev.result;
        lgScannedThreshold = ev.result.threshold;
        renderLargeDone(ev.result);
      }
    }
    await done;
    if (!stoppedScans.has(lgScanBtn) || lgItems.length > 0) markRescan(lgScanBtn);
  } catch (e) {
    if (!stoppedScans.has(lgScanBtn)) {
      lgItems = [];
      lgList.innerHTML = errorState(t("large.fail", { msg: errText(e) }), startLargeScan);
      toast(t("large.fail", { msg: errText(e) }), "err");
    }
  } finally {
    lgRunning = false;
    lgProgress.classList.add("hidden");
    if (stoppedScans.has(lgScanBtn) && lgItems.length > 0) markRescan(lgScanBtn);
    lgStopped = endScan(lgScanBtn);
    if (lgStopped) {
      if (lgItems.length > 0) renderLargeCurrent();
      else lgList.innerHTML = emptyState(t("scan.stopped"));
    }
    if (lgRestartQueued) {
      lgRestartQueued = false;
      void startLargeScan();
    }
  }
}

/** 提高下限复用结果；降低下限等旧扫描退出后再扫。 */
function changeLargeSize() {
  lgMinSize = Number(lgSize.value);
  saveChoice("cleanzo.largeSize", lgSize.value);
  if (lgRestartQueued) {
    if (lgResult || lgItems.length > 0) renderLargeCurrent();
    return;
  }
  if (lgScannedThreshold !== null && lgMinSize >= lgScannedThreshold && (lgResult || lgItems.length > 0)) {
    renderLargeCurrent();
  } else if (lgRunning) {
    lgRestartQueued = true;
    stopScan(lgScanBtn, true);
  } else {
    void startLargeScan();
  }
}

function renderLargeCurrent() {
  if (lgResult) renderLargeDone(lgResult);
  else renderLargeRows(lgItems, lgRunning, lgStopped ? stoppedFoot() : "");
}

function renderLargeRows(items: LargeFile[], live: boolean, foot = "") {
  items = items.filter((it) => it.size >= lgMinSize && !isGone(lists.large, it.path));
  const visible = new Set(items.map((it) => it.path));
  for (const path of lists.large.sel) if (!visible.has(path)) lists.large.sel.delete(path);
  lgCount.textContent = t("large.count", { n: items.length.toLocaleString() });
  if (items.length === 0) {
    lgList.innerHTML = emptyState(live ? t("large.live") : t("large.empty", { size: human(lgMinSize) })) + foot;
    lgToolbar.classList.add("hidden");
    lgToolbar.innerHTML = "";
    return;
  }
  const sorted = [...items].sort((a, b) => b.size - a.size).slice(0, 50);
  const maxSize = sorted[0]?.size || 1;
  const rows = sorted
    .map(
      (it, i) =>
        fsRowHtml(lists.large, {
          path: it.path,
          name: esc(displayPath(it.path)),
          size: it.size,
          rank: `${i + 1}`,
          trashable: it.trashable,
          locked: !it.trashable,
          sub: `<div class="fs-track"><div class="fs-fill" style="width:${Math.max(2, Math.round((it.size / maxSize) * 100))}%"></div></div>`,
        }),
    )
    .join("");
  const footHtml = (live ? `<div class="fs-foot muted small">${esc(t("large.live"))}</div>` : "") + foot;
  renderFsListInto({
    st: lists.large,
    list: lgList,
    toolbar: lgToolbar,
    rerender: renderLargeCurrent,
    removeRows: (removed) => {
      updateRowsAfterRemoved(lgItems, removed, lists.large);
      if (lgResult) lgResult.items = lgItems;
    },
    sizeOf: (path) => lgItems.find((it) => it.path === path)?.size ?? childSize(lists.large, path),
  }, rows + footHtml);
}

function renderLargeDone(r: LargeScanResult) {
  const foot = `<div class="fs-foot muted small">${esc(t("large.foot", { n: r.scanned.toLocaleString(), t: (r.elapsedMs / 1000).toFixed(1), s: r.skipped.toLocaleString() }))}</div>`;
  renderLargeRows(r.items, false, foot);
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

// ---- 相似照片 ----

let viewerPhoto: { cluster: number; path: string } | null = null;
let viewerOpener: HTMLElement | null = null;
let viewerSeq = 0;
let viewerError: unknown = null;
const photoPreviews = new Map<string, string>();
const photoPreviewLoads = new Map<string, Promise<string>>();

function viewerItems() {
  return phClusters.find((cl) => cl.id === viewerPhoto?.cluster)?.items.filter((it) => !isGone(lists.photos, it.path)) ?? [];
}

/** 查看器与网格共用勾选；重绘不改变当前预览。 */
function syncPhotoViewer() {
  if (!viewerPhoto) return;
  const items = viewerItems();
  const index = items.findIndex((it) => it.path === viewerPhoto!.path);
  const it = items[index];
  if (!it) { closePhotoViewer(); return; }
  pvName.textContent = baseName(it.path);
  pvName.title = it.path;
  pvImage.alt = baseName(it.path);
  pvMeta.textContent = t("photos.detail", { w: it.width, h: it.height, size: human(it.size) });
  pvKeep.classList.toggle("hidden", !it.keep);
  pvCheck.checked = lists.photos.sel.has(it.path);
  pvCheck.disabled = lists.photos.busy;
  pvPrev.disabled = index === 0;
  pvNext.disabled = index === items.length - 1;
  if (viewerError !== null) pvError.textContent = errText(viewerError);
}

async function showPhotoViewer(path: string) {
  const cluster = phClusters.find((cl) => cl.items.some((it) => it.path === path));
  const it = cluster?.items.find((it) => it.path === path);
  if (!cluster || !it || isGone(lists.photos, path)) return;
  const opening = viewerPhoto === null;
  if (opening) viewerOpener = document.activeElement as HTMLElement | null;
  viewerPhoto = { cluster: cluster.id, path };
  const seq = ++viewerSeq;
  viewerError = null;
  const cached = photoPreviews.get(path);
  pvImage.src = cached ?? it.thumb;
  pvImage.classList.remove("hidden");
  pvLoading.classList.toggle("hidden", !!cached);
  pvError.classList.add("hidden");
  photoViewer.classList.remove("hidden");
  $(".app").inert = true;
  syncPhotoViewer();
  if (opening) pvClose.focus();
  if (cached) return;
  try {
    let load = photoPreviewLoads.get(path);
    if (!load) {
      const request = Cleaner.photoPreview(path).then((data) => {
        // 重扫已清空缓存时，旧请求不能把它填回来
        if (photoPreviewLoads.get(path) === request) photoPreviews.set(path, data);
        return data;
      }).finally(() => {
        if (photoPreviewLoads.get(path) === request) photoPreviewLoads.delete(path);
      });
      photoPreviewLoads.set(path, request);
      load = request;
    }
    const data = await load;
    if (seq !== viewerSeq) return;
    pvImage.src = data;
  } catch (e) {
    if (seq !== viewerSeq) return;
    viewerError = e;
    pvImage.classList.add("hidden");
    pvError.textContent = errText(e);
    pvError.classList.remove("hidden");
  } finally {
    if (seq === viewerSeq) pvLoading.classList.add("hidden");
  }
}

function movePhotoViewer(step: number) {
  if (!viewerPhoto) return;
  const items = viewerItems();
  const index = items.findIndex((it) => it.path === viewerPhoto!.path);
  const next = items[index + step];
  if (next) void showPhotoViewer(next.path);
}

function closePhotoViewer() {
  if (!viewerPhoto) return;
  const path = viewerPhoto.path;
  viewerPhoto = null;
  viewerSeq++;
  viewerError = null;
  photoViewer.classList.add("hidden");
  $(".app").inert = false;
  pvImage.removeAttribute("src");
  const target = viewerOpener?.isConnected ? viewerOpener : phList.querySelector<HTMLElement>(`[data-preview="${CSS.escape(path)}"]`);
  (target ?? phScanBtn).focus();
  viewerOpener = null;
}

pvClose.addEventListener("click", closePhotoViewer);
pvPrev.addEventListener("click", () => movePhotoViewer(-1));
pvNext.addEventListener("click", () => movePhotoViewer(1));
pvCheck.addEventListener("change", () => {
  if (!viewerPhoto || pvCheck.disabled) return;
  const path = viewerPhoto.path;
  if (pvCheck.checked) lists.photos.sel.add(path);
  else lists.photos.sel.delete(path);
  renderPhotoClusters();
});
photoViewer.addEventListener("click", (e) => {
  if (e.target === photoViewer) closePhotoViewer();
});

async function startPhotoScan() {
  if (phRunning) return;
  closePhotoViewer();
  photoPreviews.clear();
  photoPreviewLoads.clear();
  resetList("photos");
  phClusters = [];
  phTried = true;
  phRunning = true;
  phProgress.classList.remove("hidden");
  phList.innerHTML = "";
  phCurrent.textContent = t("photos.busy");
  phBar.style.width = "0%";
  const ch = new Channel<import("./mygo").SimilarPhotoEvent>();
  setStoppable(phScanBtn, ch);
  try {
    const done = Cleaner.scanSimilarPhotos(ch);
    for await (const ev of ch) {
      if (ev.kind === "progress") {
        const total = ev.total ?? 0;
        const pct = total > 0 ? Math.max(0, Math.min(100, ((ev.processed ?? 0) / total) * 100)) : 0;
        phBar.style.width = `${pct}%`;
        phCurrent.textContent = t("photos.progress", {
          n: (ev.processed ?? 0).toLocaleString(),
          total: (ev.total ?? 0).toLocaleString(),
        });
      } else if (ev.kind === "done" && ev.result) {
        phClusters = ev.result.clusters;
        phScanned = ev.result.scanned;
        // 默认只勾选「完全相同」的副本（推荐保留的那份不勾）；「相似」的分组由用户自己挑
        const sel = lists.photos.sel;
        sel.clear();
        for (const cl of phClusters) {
          if (cl.kind !== "exact") continue;
          for (const it of cl.items) if (!it.keep) sel.add(it.path);
        }
        renderPhotoClusters();
      }
    }
    await done;
    if (!stoppedScans.has(phScanBtn) || phClusters.length > 0) markRescan(phScanBtn);
  } catch (e) {
    if (!stoppedScans.has(phScanBtn)) {
      phClusters = [];
      phList.innerHTML = errorState(t("photos.fail", { msg: errText(e) }), startPhotoScan);
      toast(t("photos.fail", { msg: errText(e) }), "err");
    }
  } finally {
    phRunning = false;
    phProgress.classList.add("hidden");
    if (stoppedScans.has(phScanBtn) && phClusters.length > 0) markRescan(phScanBtn);
    if (endScan(phScanBtn) && phClusters.length === 0) phList.innerHTML = emptyState(t("scan.stopped"));
  }
}

function renderPhotoClusters() {
  phClusters = phClusters.filter((cl) => cl.items.filter((it) => !isGone(lists.photos, it.path)).length >= 2);
  syncPhotoViewer();
  if (phClusters.length === 0) {
    phToolbar.classList.add("hidden");
    phToolbar.innerHTML = "";
    phList.innerHTML = emptyState(t("photos.empty"));
    return;
  }
  const cards = phClusters
    .map((cl) => {
      const items = cl.items.filter((it) => !isGone(lists.photos, it.path));
      const badge =
        cl.kind === "exact"
          ? `<span class="photo-badge exact">${esc(t("photos.kindExact"))}</span>`
          : `<span class="photo-badge similar">${esc(t("photos.kindSimilar"))}</span>`;
      const cells = items
        .map((it) => {
          const keepCls = it.keep ? " keep" : "";
          const keepTag = it.keep
            ? `<span class="photo-keep-tag">${esc(t("photos.keep"))}</span>`
            : "";
          return `
        <figure class="photo-cell${keepCls}">
          <div class="photo-thumb-wrap">
            ${checkHtml(lists.photos, it.path, undefined, it.size)}
            ${keepTag}
            <img class="photo-thumb" data-photo="${esc(it.path)}" src="${it.thumb}" alt="" loading="lazy">
          </div>
          <figcaption class="photo-meta" title="${esc(it.path)}">
            <div class="photo-name">${esc(baseName(it.path))}</div>
            <div class="photo-sub muted small">${esc(t("photos.sizeDim", { w: it.width, h: it.height }))} · ${human(it.size)}</div>
          </figcaption>
          <div class="photo-actions">
            <button class="btn small ghost icon ph-preview" data-preview="${esc(it.path)}" type="button" title="${esc(t("photos.viewLarger"))}" aria-label="${esc(t("photos.viewLarger"))}"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="10" cy="10" r="6.5"/><path d="m15 15 6 6M7 10h6M10 7v6"/></svg></button>
            ${revealBtnHtml(it.path)}
          </div>
        </figure>`;
        })
        .join("");
      return `
      <div class="photo-cluster card" data-cluster="${cl.id}">
        <div class="photo-cluster-head">
          <span class="photo-cluster-title">${esc(t("photos.cluster", { id: cl.id }))} ${badge}</span>
          <span class="photo-cluster-meta muted">${esc(t("photos.photoCount", { n: items.length }))} · ${esc(t("photos.waste", { size: human(cl.waste) }))}</span>
          <button class="btn small ghost ph-keep-only" type="button" data-cluster="${cl.id}"${lists.photos.busy ? " disabled" : ""}>${esc(t("photos.keepOnly"))}</button>
        </div>
        <div class="photo-grid">${cells}</div>
      </div>`;
    })
    .join("");
  const foot = `<div class="fs-foot muted small">${esc(t("photos.count", { n: phScanned, c: phClusters.length }))}</div>`;
  phList.innerHTML = cards + foot;
  bindPhotoList();
  updatePhotoToolbar();
}

function bindPhotoList() {
  phList.querySelectorAll<HTMLButtonElement>(".ph-preview").forEach((b) => {
    b.addEventListener("click", () => void showPhotoViewer(b.dataset.preview!));
  });
  phList.querySelectorAll<HTMLInputElement>("input[data-check]").forEach((cb) => {
    cb.addEventListener("change", () => {
      if (cb.disabled) return;
      const p = cb.dataset.check!;
      if (cb.checked) lists.photos.sel.add(p);
      else lists.photos.sel.delete(p);
      renderPhotoClusters();
    });
  });
  phList.querySelectorAll<HTMLImageElement>("img[data-photo]").forEach((img) => {
    img.addEventListener("click", () => {
      const p = img.dataset.photo!;
      const cb = phList.querySelector<HTMLInputElement>(`input[data-check="${CSS.escape(p)}"]`);
      if (!cb || cb.disabled) return;
      if (cb.checked) lists.photos.sel.delete(p);
      else lists.photos.sel.add(p);
      renderPhotoClusters();
    });
  });
  phList.querySelectorAll<HTMLButtonElement>(".fs-reveal").forEach((b) => {
    b.addEventListener("click", () => {
      Cleaner.revealInFinder(b.dataset.reveal!).catch((e) =>
        toast(t("fs.revealFail", { msg: errText(e) }), "err"),
      );
    });
  });
  phList.querySelectorAll<HTMLButtonElement>(".ph-keep-only").forEach((b) => {
    b.addEventListener("click", () => {
      if (lists.photos.busy) return;
      const cl = phClusters.find((c) => String(c.id) === b.dataset.cluster);
      if (!cl) return;
      for (const it of cl.items) {
        if (it.keep) lists.photos.sel.delete(it.path);
        else lists.photos.sel.add(it.path);
      }
      renderPhotoClusters();
    });
  });
}

/** 全选只选可以移走的：每组推荐保留的那份不在其中 */
function visiblePhotoPaths(): string[] {
  return phClusters.flatMap((c) => c.items.filter((i) => !i.keep && !isGone(lists.photos, i.path)).map((i) => i.path));
}

function updatePhotoToolbar() {
  const visible = visiblePhotoPaths();
  const sel = lists.photos.sel;
  for (const p of [...sel]) if (isGone(lists.photos, p)) sel.delete(p);
  const n = sel.size;
  const size = phClusters
    .flatMap((c) => c.items)
    .filter((it) => sel.has(it.path) && it.size >= 0)
    .reduce((sum, it) => sum + it.size, 0);
  if (visible.length === 0) {
    phToolbar.classList.add("hidden");
    phToolbar.innerHTML = "";
    return;
  }
  const all = visible.every((p) => sel.has(p));
  const some = visible.some((p) => sel.has(p));
  phToolbar.classList.remove("hidden");
  phToolbar.innerHTML = `
    <label class="fs-check"><input type="checkbox" class="ph-checkall" ${all ? "checked" : ""}${lists.photos.busy ? " disabled" : ""}></label>
    <span class="fs-toolbar-count">${esc(n > 0 ? t("photos.checkedSize", { n, size: human(size) }) : t("photos.nothing"))}</span>
    <button class="btn danger small ph-trash" type="button"${n === 0 || lists.photos.busy ? " disabled" : ""}>${lists.photos.busy ? `<span class="spin"></span>${esc(t("photos.moving"))}` : esc(t("photos.clean"))}</button>`;
  const allBox = phToolbar.querySelector<HTMLInputElement>(".ph-checkall");
  if (allBox) allBox.indeterminate = !all && some;
  allBox?.addEventListener("change", () => {
    if (allBox.disabled) return;
    visible.forEach((p) => (allBox.checked ? sel.add(p) : sel.delete(p)));
    renderPhotoClusters();
  });
  phToolbar.querySelector<HTMLButtonElement>(".ph-trash")?.addEventListener("click", askTrashPhotos);
}

function removePhotos(removed: string[]) {
  const roots = effectiveRemoved(removed);
  for (const path of roots) lists.photos.gone.add(path);
  for (const p of [...lists.photos.sel]) if (isGone(lists.photos, p)) lists.photos.sel.delete(p);
  phClusters = phClusters.flatMap((cl) => {
    const items = cl.items.filter((it) => !roots.some((r) => under(it.path, r)));
    if (items.length < 2) return [];
    const keep = items.find((it) => it.keep) ?? items.reduce((best, it) => (it.size > best.size ? it : best), items[0]!);
    for (const it of items) it.keep = it.path === keep.path;
    cl.items = items;
    cl.totalSize = items.reduce((sum, it) => sum + (it.size >= 0 ? it.size : 0), 0);
    cl.waste = Math.max(0, cl.totalSize - (keep.size >= 0 ? keep.size : 0));
    return [cl];
  });
}

function askTrashPhotos() {
  const paths = [...lists.photos.sel].filter((p) => !isGone(lists.photos, p));
  if (paths.length === 0 || lists.photos.busy) return;
  setPending(async () => {
    lists.photos.busy = true;
    renderPhotoClusters();
    try {
      const r = await Cleaner.cleanPaths(paths);
      const parts: string[] = [];
      if (r.moved > 0) parts.push(t("photos.trashOk", { n: r.moved, size: human(r.freed) }));
      if (r.inUse > 0) parts.push(t("photos.inUse", { n: r.inUse }));
      if (r.failed.length > 0) parts.push(t("photos.trashFail", { n: r.failed.length }));
      toast(parts.join(t("phrase.sep")) || t("photos.none"), r.moved > 0 ? "ok" : "err");
      removePhotos(r.removed);
      renderPhotoClusters();
    } catch (e) {
      toast(t("fs.actionFail", { msg: errText(e) }), "err");
    } finally {
      lists.photos.busy = false;
      renderPhotoClusters();
    }
  });
  modalTitle.textContent = t("photos.clean");
  modalBody.textContent = t("photos.trashBody", { n: paths.length });
  modalOk.textContent = t("fs.trash.confirm");
  showModal();
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
lgScanBtn.addEventListener("click", () => stopScan(lgScanBtn) || startLargeScan());
orScanBtn.addEventListener("click", () => stopScan(orScanBtn) || startOrphanScan());
phScanBtn.addEventListener("click", () => stopScan(phScanBtn) || startPhotoScan());
appSearch.addEventListener("input", renderApps);
appSort.addEventListener("change", () => {
  appSortBy = appSort.value as typeof appSortBy;
  saveChoice("cleanzo.appSort", appSortBy);
  if (!appsLoading) renderApps();
});
lgSize.addEventListener("change", changeLargeSize);
document.addEventListener("keydown", (e) => {
  if (viewerPhoto) {
    if (e.key === "Escape") closePhotoViewer();
    else if (e.key === "ArrowLeft") movePhotoViewer(-1);
    else if (e.key === "ArrowRight") movePhotoViewer(1);
    else if (e.key === "Tab") {
      const controls = Array.from(photoViewer.querySelectorAll<HTMLElement>("button:not(:disabled), input:not(:disabled)"));
      const first = controls[0];
      const last = controls[controls.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last?.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first?.focus(); }
      return;
    } else return;
    e.preventDefault();
    return;
  }
  if (e.key === "Escape" && !overlay.classList.contains("hidden")) hideModal();
});

setBeforeModalShow(closePhotoViewer);
applyStaticI18n();
initJunk();
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
