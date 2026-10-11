// Services and types are generated from the Go code by `mygo generate`.
import { Cleaner } from "./mygo";
import { Channel } from "mygo-runtime";
import type { AppInfo, AppCacheItem, BigItem, CategoryConfig, CleanEvent, CleanResult, CleanSummary, DirItem, DiskStat, FullScanResult, ScanEvent, LargeFile, LargeScanResult, OrphanItem, PhotoCluster } from "./mygo";
import { t, categoryName, categoryDescription, isBuiltinCategory, kindLabel, getPref, setPref, currentLang, type LangPref } from "./i18n";

const $ = <T extends HTMLElement>(s: string) => document.querySelector<T>(s)!;

const listEl = $("#list");
const totalEl = $("#total");
const totalPrefix = $("#total-prefix");
const heroSub = $("#hero-sub");
const heroProgress = $("#hero-progress");
const heroBar = $("#hero-bar");
const hero = $(".hero");
const scanBtn = $<HTMLButtonElement>("#scan");
const cleanAllBtn = $<HTMLButtonElement>("#clean-all");
const overlay = $("#overlay");
const modalTitle = $("#modal-title");
const modalBody = $("#modal-body");
const modalOk = $<HTMLButtonElement>("#modal-ok");
const modalCancel = $<HTMLButtonElement>("#modal-cancel");
const navItems = Array.from(document.querySelectorAll<HTMLButtonElement>(".nav-item"));
const appSort = $<HTMLSelectElement>("#app-sort");
const appSearch = $<HTMLInputElement>("#app-search");
const appListEl = $("#app-list");
const editorEl = $("#cat-editor");
const addCatBtn = $<HTMLButtonElement>("#add-cat");
const saveCatsBtn = $<HTMLButtonElement>("#save-cats");
const settingsStatus = $("#settings-status");
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
const acScanBtn = $<HTMLButtonElement>("#ac-scan");
const acProgress = $("#ac-progress");
const acCurrent = $("#ac-current");
const acList = $("#ac-list");
const acToolbar = $("#ac-toolbar");
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

let categories = [] as Awaited<ReturnType<typeof Cleaner.scan>>;
let apps: AppInfo[] = [];
const appBusy = new Set<string>();
let appsLoading = false;
let catConfig: CategoryConfig[] = [];
let fsResult: FullScanResult | null = null;
let fsDisk: DiskStat | null = null;
let fsDiskSeq = 0;
let fsDiskPending = 0;
let pending: (() => Promise<void>) | null = null;
let fsRunning = false;
let fsItems: BigItem[] = [];
let lgRunning = false;
let lgItems: LargeFile[] = [];
let lgResult: LargeScanResult | null = null;
let lgScannedThreshold: number | null = null;
let lgRestartQueued = false;
let lgStopped = false;
let orRunning = false;
let orItems: OrphanItem[] = [];
let orFoot: { n: string; size: string; t: string } | null = null;
let acRunning = false;
let acItems: AppCacheItem[] = [];
let phRunning = false;
let phClusters: PhotoCluster[] = [];
let phScanned = 0;
/** 相似照片扫描是否已经开始过：停掉之后再进这个视图不会自己重新开始 */
let phTried = false;

// ---- utils ----

const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

/** 将后端错误码翻成当前语言，普通错误保留原文。 */
function errText(e: unknown): string {
  const raw = msg(e);
  const plain: Record<string, string> = {
    "in-use": "fs.inUse",
    blocked: "fs.blocked",
    gone: "fs.gone",
    "not-app": "fs.notApp",
  };
  if (plain[raw]) return t(plain[raw] as never);
  try {
    const parsed = JSON.parse(raw) as { code?: string; args?: unknown[] };
    if (parsed && typeof parsed.code === "string") {
      const key: Record<string, string> = {
        "empty-name": "settings.emptyName",
        "dup-name": "settings.dupName",
        "no-paths": "settings.noPaths",
        "empty-path": "settings.emptyPath",
        "protected-path": "settings.protectedPath",
        "dup-path": "settings.dupPath",
      };
      const entry = key[parsed.code];
      if (entry) {
        const args = parsed.args ?? [];
        const vars: Record<string, string> = parsed.code === "dup-path"
          ? { path: String(args[0] ?? ""), owner: String(args[1] ?? ""), name: String(args[2] ?? "") }
          : { name: String(args[0] ?? ""), path: String(args[0] ?? "") };
        return t(entry as never, vars);
      }
    }
  } catch {
    /* 非 JSON 错误按原文显示 */
  }
  return raw;
}

function human(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n;
  let i = -1;
  do {
    v /= 1024;
    i++;
  } while (v >= 1024 && i < units.length - 1);
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`;
}

const esc = (s: string) =>
  s.replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!,
  );

const fmtDate = (sec: number) =>
  new Date(sec * 1000).toLocaleDateString(currentLang() === "zh" ? "zh-CN" : "en-US", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  });

// ~/xxx for paths under the home directory
const displayPath = (p: string) => p.replace(/^\/Users\/[^/]+/, "~");

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
  render();
  if (apps.length > 0) renderApps();
  if (catConfig.length > 0) renderCatEditor();
  if (fsDisk) renderDisk(fsDisk);
  if (fsResult && !fsRunning) renderFullScan(fsResult);
  renderIdleScanPages();
  if (lgResult || lgItems.length > 0) renderLargeCurrent();
  if (!orRunning && orItems.length > 0) {
    if (orFoot) orTotal.textContent = t("orphans.foot", orFoot);
    renderOrphanRows(orItems);
  }
  if (acItems.length > 0 && !acRunning) {
    renderAppCacheRows();
  }
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

let toastTimer: number | undefined;
function toast(text: string, kind: "ok" | "err" = "ok") {
  let el = $<HTMLDivElement>("#toast");
  if (!el) {
    el = document.createElement("div");
    el.id = "toast";
    document.body.appendChild(el);
  }
  el.textContent = text;
  el.className = `toast show ${kind}`;
  clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => el.classList.remove("show"), 3200);
}

function setNavBusy(view: View, on: boolean) {
  navItems.find((b) => b.dataset.view === view)?.classList.toggle("busy", on);
}

const scanViews: Record<string, View> = {
  scan: "clean", "fs-scan": "fullscan", "lg-scan": "large", "or-scan": "orphans",
  "ac-scan": "appcaches", "ph-scan": "photos",
};

/** 恢复按钮时仍按当前语言显示「重新扫描」。 */
function markRescan(btn: HTMLButtonElement) {
  btn.dataset.i18n = "fs.rescan";
}

function setBusy(btn: HTMLButtonElement, busy: boolean, label?: string) {
  const view = scanViews[btn.id];
  if (view) setNavBusy(view, busy);
  if (busy) {
    btn.dataset.orig = btn.textContent ?? "";
    btn.disabled = true;
    btn.innerHTML = `<span class="spin"></span>${esc(label ?? t("scan.busy"))}`;
  } else {
    btn.disabled = false;
    // 文案取当前语言：忙碌期间可能切换过语言
    btn.textContent = btn.dataset.i18n ? t(btn.dataset.i18n as never) : (btn.dataset.orig ?? "");
  }
}

/** 正在进行的流式扫描各自的 Channel；扫描期间扫描按钮变成「停止扫描」 */
const scanChans = new Map<HTMLButtonElement, { close(): void }>();
const stoppedScans = new Set<HTMLButtonElement>();

/** 扫描开始：按钮保持可点，显示「停止扫描」 */
function setStoppable(btn: HTMLButtonElement, ch: { close(): void }) {
  btn.dataset.orig = btn.textContent ?? "";
  btn.dataset.started = String(Date.now());
  btn.disabled = false;
  btn.innerHTML = `<span class="spin"></span>${esc(t("scan.stop"))}`;
  scanChans.set(btn, ch);
  setNavBusy(scanViews[btn.id]!, true);
  stoppedScans.delete(btn);
}

/** 点了「停止扫描」：关闭 Channel，Go 那边的 ctx 随之取消，du、find、sips 都会停下 */
function stopScan(btn: HTMLButtonElement, immediate = false): boolean {
  const ch = scanChans.get(btn);
  if (!ch) return false;
  // 双击的第二下不算「停止」
  if (!immediate && Date.now() - Number(btn.dataset.started ?? 0) < 600) return true;
  scanChans.delete(btn);
  stoppedScans.add(btn);
  ch.close();
  return true;
}

/** 扫描结束（完成、出错或被停止）：恢复按钮，返回它是不是被用户停止的 */
const stoppedFoot = () => `<div class="fs-foot muted small">${esc(t("scan.stopped"))}</div>`;

function endScan(btn: HTMLButtonElement): boolean {
  scanChans.delete(btn);
  setBusy(btn, false);
  return stoppedScans.delete(btn);
}

function skeletonRows(n: number): string {
  return Array.from({ length: n }, () => `<div class="skeleton card"></div>`).join("");
}

function emptyState(text: string): string {
  return `<div class="empty">${esc(text)}</div>`;
}

/** 错误态的「重试」按钮：errorState 只产出 HTML，点击由文档级监听找回回调 */
const retryFns = new Map<string, () => void>();
let retrySeq = 0;
document.addEventListener("click", (e) => {
  const b = (e.target as HTMLElement).closest<HTMLButtonElement>(".retry-btn");
  const fn = b && retryFns.get(b.dataset.retry ?? "");
  if (fn) {
    retryFns.delete(b.dataset.retry!);
    fn();
  }
});

function errorState(text: string, retry?: () => void): string {
  let btn = "";
  if (retry) {
    const id = `r${++retrySeq}`;
    retryFns.set(id, retry);
    btn = `<button class="btn ghost retry-btn" data-retry="${id}" type="button">${esc(t("btn.retry"))}</button>`;
  }
  return `<div class="empty err"><div class="err-icon">⚠</div><div>${esc(text)}</div>${btn}</div>`;
}

// ---- 视图切换 ----

const VIEWS = ["clean", "fullscan", "large", "orphans", "appcaches", "photos", "apps", "settings"] as const;
type View = (typeof VIEWS)[number];

function switchView(v: View) {
  navItems.forEach((b) => b.classList.toggle("active", b.dataset.view === v));
  VIEWS.forEach((k) => $(`#view-${k}`).classList.toggle("hidden", k !== v));
  if (v === "apps" && apps.length === 0 && !appsLoading) loadApps();
  if (v === "fullscan" && !fsDisk && fsDiskPending === 0) void refreshDisk();
  if (v === "settings" && catConfig.length === 0) loadCatConfig();
  if (v === "appcaches" && acItems.length === 0 && !acRunning) startAppCacheScan();
  if (v === "photos" && !phTried) startPhotoScan();
}
navItems.forEach((b) => b.addEventListener("click", () => switchView(b.dataset.view as View)));

// ---- 垃圾清理 ----

const IDLE_ICON = ` <svg class="eg-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
  <circle cx="12" cy="12" r="10.2" stroke="url(#eg-grad)" stroke-width="1.4" stroke-dasharray="3.4 2.8" opacity="0.7"/>
  <circle cx="12" cy="12" r="6.8" stroke="url(#eg-grad)" stroke-width="1.1" opacity="0.35"/>
  <path d="M12 2c.9 6 4.4 9.5 10.4 10.4-6 .9-9.5 4.4-10.4 10.4-.9-6-4.4-9.5-10.4-10.4 6-.9 9.5-4.4 10.4-10.4z" fill="url(#eg-grad)"/>
  <circle cx="17.2" cy="6.6" r="0.9" fill="#fff" opacity="0.85"/>
  <circle cx="6.6" cy="17.4" r="0.7" fill="#fff" opacity="0.6"/>
  <defs><linearGradient id="eg-grad" x1="0" y1="0" x2="24" y2="24"><stop offset="0" stop-color="#0fb986"/><stop offset="1" stop-color="#0a9e8e"/></linearGradient></defs>
</svg>`;

/** 未扫描的列表沿用垃圾清理页的引导样式。 */
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

/**
 * 垃圾清理页的阶段：idle 尚未扫描；scanning 扫描中（分类逐个出结果）；scanned 可勾选清理；
 * cleaning 清理中（分类逐个推进）；cleaned 清理结束，结果留在页面上，等用户重新扫描。
 */
type JunkPhase = "idle" | "scanning" | "scanned" | "cleaning" | "cleaned";
let phase: JunkPhase = "idle";
/** 扫描中：还没出结果的分类、最近一次报告的路径 */
const pendingCats = new Set<string>();
let scanningPath = "";
/** 清理中及之后：每个选中分类的进展 */
type RowState = { state: "waiting" | "cleaning" | "done" | "stopped" | "skipped"; result?: CleanResult };
const rowStates = new Map<string, RowState>();
const progress = { total: 0, bytes: 0, name: "", path: "", stopped: false };
let cleanChan: { close(): void } | null = null;

function initIdleClean() {
  phase = "idle";
  renderHero();
  listEl.classList.remove("grouped");
  listEl.classList.add("cat-grid", "idle-list");
  listEl.innerHTML = `
    <div class="empty-guide">
      ${IDLE_ICON}
      <h3>${esc(t("clean.idle.title"))}</h3>
      <p>${esc(t("clean.idle.desc"))}</p>
      <button id="idle-scan" class="btn primary big" type="button">${esc(t("clean.idle.cta"))}</button>
    </div>`;
  const idleScan = document.getElementById("idle-scan");
  if (idleScan) idleScan.addEventListener("click", () => scan());
  scanBtn.textContent = t("scan.start");
  scanBtn.disabled = false;
}

/** 扫描中又要求扫描（设置里保存了分类）：这次停下后按新设置重扫 */
let rescanQueued = false;

/** 扫描：先列出全部分类，每个分类扫完立刻填上结果；扫描按钮在此期间是「停止扫描」 */
async function scan() {
  if (phase === "cleaning") return;
  if (phase === "scanning") {
    rescanQueued = true;
    stopScan(scanBtn);
    return;
  }
  phase = "scanning";
  categories = [];
  pendingCats.clear();
  rowStates.clear();
  scanningPath = "";
  scannedAt = Date.now();
  selectedCats.clear();
  const ch = new Channel<ScanEvent>();
  setStoppable(scanBtn, ch);
  listEl.classList.remove("grouped", "idle-list");
  listEl.classList.add("cat-grid");
  listEl.innerHTML = skeletonRows(window.innerWidth >= 1600 ? 9 : window.innerWidth >= 1200 ? 8 : 6);
  renderHero();
  let failed = false;
  try {
    const done = Cleaner.scanStream(ch);
    for await (const ev of ch) {
      if (ev.kind === "start") {
        categories = ev.categories ?? [];
        categories.forEach((c) => pendingCats.add(c.name));
        render();
      } else if (ev.kind === "scanning") {
        scanningPath = ev.path ?? "";
        renderHero();
      } else if (ev.kind === "category" && ev.category) {
        const c = ev.category;
        categories = categories.map((x) => (x.name === c.name ? c : x));
        pendingCats.delete(c.name);
        // 默认勾选「建议清理」类；「谨慎清理」类（moderate）默认不勾，由用户手动确认
        if (c.size > 0 && c.risk !== "moderate") selectedCats.add(c.name);
        render();
      }
    }
    await done;
  } catch (e) {
    if (!stoppedScans.has(scanBtn)) {
      failed = true;
      categories = [];
      totalEl.textContent = t("scan.failed");
      heroSub.textContent = t("scan.retryHint");
      cleanAllBtn.disabled = true;
      listEl.innerHTML = errorState(t("scan.error", { msg: errText(e) }), scan);
      toast(t("scan.error", { msg: errText(e) }), "err");
    }
  } finally {
    endScan(scanBtn);
    // 停止时只留下已经扫完的分类
    categories = categories.filter((c) => !pendingCats.has(c.name));
    pendingCats.clear();
    phase = categories.length > 0 ? "scanned" : "idle";
    if (!failed) render();
    if (rescanQueued) {
      rescanQueued = false;
      void scan();
    }
  }
}

type ScanCat = Awaited<ReturnType<typeof Cleaner.scan>>[number];

const CAT_GROUPS: { key: string; icon: string; color: string; names: string[] }[] = [
  { key: "group.system", icon: "⚙", color: "#0ea5e9", names: ["caches", "logs", "temp", "trash"] },
  { key: "group.dev", icon: "⌘", color: "#10b981", names: ["npm", "go", "pip", "homebrew", "xcode"] },
];

/** 分类所属分组下标；未命中内置分组的自定义分类归入「其他」 */
function groupIndexOf(name: string): number {
  const gi = CAT_GROUPS.findIndex((g) => g.names.includes(name));
  return gi === -1 ? CAT_GROUPS.length : gi;
}

const selectedCats = new Set<string>();
/** 这次扫描开始的时间：清空废纸篓时只删那之前就在里面的东西 */
let scannedAt = 0;

function render() {
  if (phase === "idle") {
    initIdleClean();
    return;
  }
  // 扫描刚开始、分类还没列出来：骨架屏留着
  if (categories.length === 0) {
    renderHero();
    return;
  }
  listEl.classList.remove("idle-list", "cat-grid");
  listEl.classList.add("grouped");

  const groups: { key: string; icon: string; color: string; cats: ScanCat[] }[] = [
    ...CAT_GROUPS.map((g) => ({ ...g, cats: [] as ScanCat[] })),
    { key: "group.other", icon: "✦", color: "#64748b", names: [], cats: [] as ScanCat[] },
  ];
  categories.forEach((c) => groups[c.builtin ? groupIndexOf(c.name) : CAT_GROUPS.length]?.cats.push(c));
  listEl.innerHTML = groups
    .filter((g) => g.cats.length > 0)
    .map((g) => groupHtml(g.key, g.icon, g.color, g.cats))
    .join("");

  listEl.querySelectorAll<HTMLInputElement>("input[data-cat]").forEach((cb) => {
    cb.addEventListener("change", () => {
      const n = cb.dataset.cat!;
      if (cb.checked) selectedCats.add(n);
      else selectedCats.delete(n);
      updateSelection();
    });
  });
  listEl.querySelectorAll<HTMLButtonElement>(".cat-grant").forEach((b) => {
    b.addEventListener("click", () => {
      Cleaner.openFullDiskAccessSettings().catch((e) => toast(t("grant.fail", { msg: errText(e) }), "err"));
    });
  });
  renderHero();
  updateSelection();
  if (phase !== "scanning") scanBtn.textContent = t("fs.rescan");
  scanBtn.disabled = phase === "cleaning";
}

/** 横幅：按阶段显示已发现的空间、清理进度或清理结果，以及对应的主按钮 */
function renderHero() {
  hero.classList.toggle("compact", phase === "idle");
  heroProgress.classList.toggle("hidden", phase !== "cleaning" && phase !== "cleaned");
  if (phase !== "cleaning" && phase !== "cleaned") heroBar.style.width = "0";
  cleanAllBtn.classList.toggle("danger", phase !== "cleaning" && phase !== "cleaned");
  cleanAllBtn.classList.toggle("ghost", phase === "cleaning");
  cleanAllBtn.classList.toggle("primary", phase === "cleaned");
  const pct = progress.total > 0 ? Math.min(100, (progress.bytes / progress.total) * 100) : 100;
  if (phase === "idle") {
    totalPrefix.textContent = t("hero.ready");
    totalEl.textContent = "--";
    heroSub.textContent = t("clean.idleSub");
    cleanAllBtn.textContent = t("clean.now");
    cleanAllBtn.disabled = true;
  } else if (phase === "scanning") {
    const found = categories.filter((c) => !pendingCats.has(c.name)).reduce((s, c) => s + c.size, 0);
    totalPrefix.textContent = t("scan.found");
    totalEl.textContent = categories.length > pendingCats.size ? human(found) : "--";
    heroSub.textContent = scanningPath ? t("scan.at", { path: displayPath(scanningPath) }) : t("scan.hero");
    cleanAllBtn.textContent = t("clean.now");
    cleanAllBtn.disabled = true;
  } else if (phase === "scanned") {
    totalPrefix.textContent = t("clean.foundPrefix");
    totalEl.textContent = human(categories.reduce((s, c) => s + c.size, 0));
    cleanAllBtn.textContent = t("clean.now");
  } else if (phase === "cleaning") {
    const cur = categories.find((c) => c.name === progress.name);
    const where = cur ? catTitle(cur) + (progress.path ? ` · ${baseName(progress.path)}` : "") : "";
    totalPrefix.textContent = t("clean.progress.prefix");
    totalEl.textContent = human(progress.bytes);
    heroSub.textContent = where ? t("clean.progress.sub", { total: human(progress.total), where }) : t("clean.progress.preparing", { total: human(progress.total) });
    heroBar.style.width = `${pct}%`;
    cleanAllBtn.textContent = t("clean.stop");
    cleanAllBtn.disabled = progress.stopped;
  } else {
    const results = [...rowStates.values()].flatMap((r) => (r.result ? [r.result] : []));
    const failed = results.filter((r) => r.failed.length > 0).map((r) => {
      const c = categories.find((x) => x.name === r.name);
      return c ? catTitle(c) : r.name;
    });
    totalPrefix.textContent = progress.stopped ? t("clean.stoppedPrefix") : t("clean.donePrefix");
    totalEl.textContent = human(progress.bytes);
    heroSub.textContent = [
      // 停在半路时，已经做完的分类照常汇总；没有做完的就说明其余没有清理
      progress.stopped && results.length === 0
        ? t("clean.stoppedSub")
        : cleanDoneText({
            results,
            totalFreed: results.reduce((s, r) => s + r.freed, 0),
            totalTrashed: results.reduce((s, r) => s + r.trashed, 0),
            inUse: results.reduce((s, r) => s + r.inUse, 0),
            errors: [],
          }),
      progress.stopped && results.length > 0 ? t("clean.stoppedSub") : "",
      failed.length > 0 ? t("clean.all.partial", { names: failed.join(t("list.sep")) }) : "",
    ]
      .filter(Boolean)
      .join(t("phrase.sep"));
    heroBar.style.width = `${progress.stopped ? pct : 100}%`;
    cleanAllBtn.textContent = t("fs.rescan");
    cleanAllBtn.disabled = false;
  }
}

/** 勾选变化时联动：横幅「已选择」、各分组小计、立即清理按钮可用态 */
function updateSelection() {
  const sel = categories.filter((c) => selectedCats.has(c.name) && c.size > 0);
  const total = sel.reduce((s, c) => s + c.size, 0);
  if (phase === "scanned") {
    heroSub.textContent = t("clean.selected", { size: human(total) });
    cleanAllBtn.disabled = sel.length === 0;
  }
  listEl.querySelectorAll<HTMLElement>(".group-sel").forEach((el) => {
    const names = (el.dataset.names ?? "").split(",").filter(Boolean);
    el.textContent = groupSummary(categories.filter((c) => names.includes(c.name)));
  });
}

/** 分组小计：组里还有分类在扫描时只说「扫描中」 */
function groupSummary(cats: ScanCat[]): string {
  if (cats.some((c) => pendingCats.has(c.name))) return t("cat.scanning");
  const total = cats.reduce((s, c) => s + c.size, 0);
  const sel = cats.filter((c) => selectedCats.has(c.name)).reduce((s, c) => s + c.size, 0);
  return t("group.summary", { total: human(total), sel: human(sel) });
}

const CAT_ICONS: Record<string, { icon: string; color: string }> = {
  caches: { icon: "▦", color: "#10b981" },
  logs: { icon: "≣", color: "#0ea5e9" },
  temp: { icon: "⏱", color: "#f59e0b" },
  trash: { icon: "🗑", color: "#64748b" },
  npm: { icon: "⬢", color: "#8b5cf6" },
  go: { icon: "🐹", color: "#22c55e" },
  pip: { icon: "🐍", color: "#eab308" },
  homebrew: { icon: "🍺", color: "#f97316" },
  xcode: { icon: "🔨", color: "#3b82f6" },
};
const fallbackIcon = { icon: "🧹", color: "#64748b" };

function groupHtml(key: string, icon: string, color: string, cats: ScanCat[]): string {
  const names = cats.map((c) => c.name).join(",");
  return `
    <details class="cat-group" open>
      <summary>
        <div class="tile group-tile" style="--c:${color}"><span>${icon}</span></div>
        <span class="group-name">${esc(t(key as never))}</span>
        <span class="group-sel" data-names="${esc(names)}">${esc(groupSummary(cats))}</span>
        <span class="group-caret" aria-hidden="true">▾</span>
      </summary>
      <div class="group-rows">${cats.map(rowHtml).join("")}</div>
    </details>`;
}

/** 分类标题：未改动的内置分类用本地化名称，其余用用户起的名字 */
const catTitle = (c: ScanCat) => (c.builtin ? categoryName(c.name) : c.name);

const grantBtn = () => `<button class="btn small ghost cat-grant" type="button">${esc(t("cat.grant"))}</button>`;

/** 分类右侧的状态：没有权限、受保护、未找到、很干净，或大小与清理建议 */
function catNote(c: ScanCat): string {
  if (c.size > 0) {
    return c.risk === "moderate"
      ? `<span class="cat-note-text caution">${esc(t("cat.sizeCaution", { size: human(c.size) }))}</span>`
      : `<span class="cat-note-text">${esc(t("cat.sizeSafe", { size: human(c.size) }))}</span>`;
  }
  if (c.denied.length > 0) return `<span class="tag warn">${esc(t("cat.denied"))}</span>${grantBtn()}`;
  if (c.blocked.length > 0) return `<span class="tag dim">${esc(t("cat.blocked"))}</span>`;
  if (c.missing.length > 0) return `<span class="tag dim">${esc(t("cat.missing"))}</span>`;
  return `<span class="tag ok">${esc(t("cat.cleanState"))}</span>`;
}

/** 清理中及之后，一个分类在这次清理里的进展 */
function cleanStateNote(r: RowState): string {
  const spin = (text: string) => `<span class="cat-note-text muted"><span class="spin"></span> ${esc(text)}</span>`;
  if (r.state === "waiting") return `<span class="cat-note-text muted">${esc(t("cat.waiting"))}</span>`;
  if (r.state === "cleaning") return spin(t("cat.cleaning"));
  if (r.state === "stopped") return `<span class="tag dim">${esc(t("cat.stopped"))}</span>`;
  if (r.state === "skipped") return `<span class="tag dim">${esc(t("cat.notCleaned"))}</span>`;
  const res = r.result!;
  const parts: string[] = [];
  if (res.freed > 0) parts.push(t("cat.freed", { size: human(res.freed) }));
  if (res.trashed > 0) parts.push(t("cat.trashed", { size: human(res.trashed) }));
  const done = `<span class="cat-note-text ok">✓ ${esc(parts.join(t("phrase.sep")) || t("cat.cleanedNothing"))}</span>`;
  return res.failed.length > 0 ? `${done}<span class="tag warn">${esc(t("cat.failedN", { n: res.failed.length }))}</span>` : done;
}

function rowHtml(c: ScanCat): string {
  const meta = c.builtin && Object.hasOwn(CAT_ICONS, c.name) ? CAT_ICONS[c.name]! : fallbackIcon;
  const scanning = pendingCats.has(c.name);
  const cleanState = rowStates.get(c.name);
  const empty = c.size === 0;
  const caution = !empty && c.risk === "moderate";
  const checked = selectedCats.has(c.name);
  // 只有扫描完、尚未清理时能勾选；清理中及之后，没参与这次清理的分类变淡
  const locked = phase !== "scanned" || empty;
  const dim = (phase === "cleaning" || phase === "cleaned") && !cleanState;
  const note = scanning
    ? `<span class="cat-note-text muted"><span class="spin"></span> ${esc(t("cat.scanning"))}</span>`
    : cleanState
      ? cleanStateNote(cleanState)
      : catNote(c);
  const inUse = cleanState?.result ? cleanState.result.inUse : c.inUse;
  // 明细是扫描时的样子；清理过的分类，那些条目已经删掉或移走了
  const showTops = !scanning && (!cleanState || cleanState.state === "skipped");
  const trashTag = c.permanent ? "" : `<span class="tag dim">${esc(t("cat.toTrash"))}</span>`;
  const subs = [
    !scanning && inUse > 0 ? esc(t("cat.inUse", { n: inUse })) : "",
    !scanning && !empty && c.denied.length > 0 ? `${esc(t("cat.deniedPart"))}${grantBtn()}` : "",
  ]
    .filter(Boolean)
    .map((s) => `<div class="cat-sub muted">${s}</div>`)
    .join("");
  const tops = (c.top ?? [])
    .map(
      (item) =>
        `<div class="top-item"><span class="top-name">${esc(item.name)}</span><span class="top-path muted">${esc(item.path)}</span><span class="top-size">${human(item.size)}</span></div>`,
    )
    .join("");
  return `
    <div class="cat-row${empty && !scanning ? " is-empty" : ""}${caution ? " caution" : ""}${dim ? " is-dim" : ""}">
      <label class="cat-check"><input type="checkbox" data-cat="${esc(c.name)}" ${locked ? "disabled" : ""} ${checked && !scanning ? "checked" : ""}></label>
      <div class="tile" style="--c:${meta.color}"><span>${meta.icon}</span></div>
      <div class="cat-info">
        <div class="cat-name">${esc(catTitle(c))}${trashTag}</div>
        <div class="cat-desc muted">${esc(c.builtin ? categoryDescription(c.name, c.description) : c.description)}</div>
        ${subs}
      </div>
      <div class="cat-note">${note}</div>
      ${tops && showTops ? `<details class="cat-details"><summary>${esc(t("cat.tops"))}</summary>${tops}</details>` : ""}
    </div>`;
}

/**
 * 清理：横幅显示进度，选中的分类依次「等待中 → 清理中 → 结果」；结束后结果留在页面上，
 * 横幅的按钮变成「重新扫描」。清理中按钮是「停止清理」：处理完手头这一项就停。
 */
async function clean(sel: ScanCat[]) {
  phase = "cleaning";
  setNavBusy("clean", true);
  rowStates.clear();
  sel.forEach((c) => rowStates.set(c.name, { state: "waiting" }));
  Object.assign(progress, { total: sel.reduce((s, c) => s + c.size, 0), bytes: 0, name: "", path: "", stopped: false });
  const ch = new Channel<CleanEvent>();
  cleanChan = ch;
  render();
  try {
    const done = Cleaner.cleanStream(sel.map((c) => c.name), scannedAt, ch);
    for await (const ev of ch) {
      if (ev.kind === "start") {
        rowStates.set(ev.name, { state: "cleaning" });
        Object.assign(progress, { name: ev.name, path: "" });
        render();
      } else if (ev.kind === "checking" || ev.kind === "removed") {
        Object.assign(progress, { name: ev.name, path: ev.path ?? "" });
        if (ev.kind === "removed") progress.bytes = ev.bytes ?? 0;
        renderHero();
      } else if (ev.kind === "done" && ev.result) {
        rowStates.set(ev.name, { state: "done", result: ev.result });
        progress.bytes = ev.bytes ?? 0;
        render();
      }
    }
    await done;
  } catch (e) {
    if (!progress.stopped) toast(t("clean.fail", { msg: errText(e) }), "err");
  } finally {
    cleanChan = null;
    // 停在半路的分类标「已停止」，还没轮到的标「未清理」；一个都没打断就算清理完成
    let interrupted = false;
    rowStates.forEach((r) => {
      if (r.state === "cleaning") r.state = "stopped";
      else if (r.state === "waiting") r.state = "skipped";
      else return;
      interrupted = true;
    });
    progress.stopped = interrupted;
    phase = "cleaned";
    setNavBusy("clean", false);
    render();
  }
}

function stopClean() {
  if (!cleanChan) return;
  progress.stopped = true;
  cleanChan.close();
  renderHero();
}

/** 清理结果：释放了多少、移到废纸篓多少、跳过了几项 */
function cleanDoneText(res: CleanSummary): string {
  const parts: string[] = [];
  if (res.totalFreed > 0) parts.push(t("clean.done.freed", { size: human(res.totalFreed) }));
  if (res.totalTrashed > 0) parts.push(t("clean.done.trashed", { size: human(res.totalTrashed) }));
  if (res.inUse > 0) parts.push(t("clean.done.inUse", { n: res.inUse }));
  return parts.length > 0 ? parts.join(t("phrase.sep")) : t("clean.nothing");
}

function askCleanAll() {
  const sel = categories.filter((c) => selectedCats.has(c.name) && c.size > 0);
  if (sel.length === 0) return;
  // 内置垃圾直接删除；用户自定义的分类移到废纸篓
  const sum = (cs: ScanCat[]) => human(cs.reduce((s, c) => s + c.size, 0));
  const names = (cs: ScanCat[]) => cs.map(catTitle).join(t("list.sep"));
  const del = sel.filter((c) => c.permanent);
  const keep = sel.filter((c) => !c.permanent);
  const lines = [
    del.length > 0 ? t("clean.body.delete", { size: sum(del), names: names(del) }) : "",
    keep.length > 0 ? t("clean.body.trash", { size: sum(keep), names: names(keep) }) : "",
    t("clean.body.inUse"),
  ].filter(Boolean);
  pending = () => clean(sel);
  modalTitle.textContent = t("clean.sel.title");
  modalBody.innerHTML = lines.map((l) => `<p>${esc(l)}</p>`).join("");
  modalOk.textContent = t("clean.confirm");
  showModal();
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

// ---- 勾选与展开（全盘扫描 / 大文件 / 残留 / 应用缓存 / 相似照片） ----

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
const lists = { fullscan: newList(), large: newList(), orphans: newList(), appcaches: newList(), photos: newList() };
/** 扫描重新开始：换一份新状态，并收起还握着旧勾选的工具栏 */
function resetList(v: keyof typeof lists) {
  lists[v] = newList();
  const toolbar = { fullscan: fsToolbar, large: lgToolbar, orphans: orToolbar, appcaches: acToolbar, photos: phToolbar }[v];
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
  pending = async () => {
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
  };
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
  pending = async () => {
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
  };
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
  orItems = [];
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
      if (orFoot) {
        orFoot.n = orItems.length.toLocaleString();
        orFoot.size = human(orItems.reduce((sum, it) => sum + (it.size >= 0 ? it.size : 0), 0));
        orTotal.textContent = t("orphans.foot", orFoot);
      }
    },
    sizeOf: (path) => orItems.find((it) => it.path === path)?.size ?? childSize(lists.orphans, path),
  }, rows);
}

// ---- 应用缓存（按应用拆分） ----

const baseName = (p: string) => p.split("/").filter(Boolean).pop() ?? p;

async function startAppCacheScan() {
  if (acRunning) return;
  resetList("appcaches");
  acRunning = true;
  setBusy(acScanBtn, true, t("appcaches.busy"));
  acProgress.classList.remove("hidden");
  acList.innerHTML = "";
  acCurrent.textContent = t("appcaches.busy");
  try {
    acItems = await Cleaner.scanAppCaches();
    markRescan(acScanBtn);
    renderAppCacheRows();
  } catch (e) {
    acItems = [];
    acList.innerHTML = errorState(t("fs.fail", { msg: errText(e) }), startAppCacheScan);
    toast(t("fs.fail", { msg: errText(e) }), "err");
  } finally {
    acRunning = false;
    acProgress.classList.add("hidden");
    setBusy(acScanBtn, false);
  }
}

function renderAppCacheRows() {
  acItems = acItems.filter((it) => !isGone(lists.appcaches, it.path));
  if (acItems.length === 0) {
    acToolbar.classList.add("hidden");
    acToolbar.innerHTML = "";
    acList.innerHTML = emptyState(t("appcaches.empty"));
    return;
  }
  const sorted = [...acItems].sort((a, b) => b.size - a.size);
  const max = sorted[0]?.size || 1;
  const rows = sorted
    .map(
      (it, i) =>
        fsRowHtml(lists.appcaches, {
          path: it.path,
          name: esc(it.app || it.name),
          size: it.size,
          rank: `${i + 1}`,
          trashable: !it.inUse,
          sub: `<div class="fs-sub muted small">${it.inUse ? `<span class="tag dim">${esc(t("appcaches.inUse"))}</span> ` : ""}${esc(displayPath(it.path))}</div>`,
        }),
    )
    .join("");
  const foot = `<div class="fs-foot muted small">${esc(t("appcaches.count", { n: acItems.length }))}</div>`;
  renderFsListInto({
    st: lists.appcaches,
    list: acList,
    toolbar: acToolbar,
    rerender: renderAppCacheRows,
    removeRows: (removed) => updateRowsAfterRemoved(acItems, removed, lists.appcaches),
    sizeOf: (path) => acItems.find((it) => it.path === path)?.size ?? childSize(lists.appcaches, path),
  }, rows + foot);
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
  pending = async () => {
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
  };
  modalTitle.textContent = t("photos.clean");
  modalBody.textContent = t("photos.trashBody", { n: paths.length });
  modalOk.textContent = t("fs.trash.confirm");
  showModal();
}

// ---- 应用卸载 ----

async function loadApps() {
  if (appsLoading) return;
  appsLoading = true;
  setNavBusy("apps", true);
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
    setNavBusy("apps", appBusy.size > 0);
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
  const seq = modalSeq;
  try {
    const d = await Cleaner.appUninstallDetail(path);
    if (seq !== modalSeq) return; // 这个框已经关掉或换成了别的
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
      pending = null;
      modalOk.textContent = t("btn.close");
      return;
    }
    pending = async () => {
      if (appBusy.has(d.app.path)) return;
      appBusy.add(d.app.path);
      setNavBusy("apps", true);
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
        setNavBusy("apps", appsLoading || appBusy.size > 0);
        renderApps();
      }
    };
    modalOk.textContent = t("apps.confirm");
  } catch (e) {
    if (seq !== modalSeq) return;
    modalBody.innerHTML = `<div class="err-inline">${esc(t("apps.analyzeFail", { msg: errText(e) }))}</div>`;
    pending = null;
    modalOk.textContent = t("btn.close");
  } finally {
    if (seq === modalSeq) modalOk.disabled = false;
  }
}

// ---- 设置 ----

async function loadCatConfig() {
  try {
    catConfig = await Cleaner.getCategories();
  } catch (e) {
    settingsStatus.textContent = t("settings.readFail", { msg: errText(e) });
    settingsStatus.classList.add("err");
    return;
  }
  renderCatEditor();
}

function renderCatEditor() {
  editorEl.innerHTML = catConfig
    .map(
      (c, i) => `
      <div class="cat-edit card">
        <div class="edit-grid">
          ${isBuiltinCategory(c.name) ? `
          <div class="cat-edit-name">${esc(categoryName(c.name))} <span class="tag dim">${esc(t("settings.builtin"))}</span></div>
          <div class="cat-edit-desc muted">${esc(categoryDescription(c.name, c.description))}</div>` : `
          <input data-i="${i}" data-f="name" value="${esc(c.name)}" placeholder="${esc(t("settings.name"))}" />
          <input data-i="${i}" data-f="description" value="${esc(c.description)}" placeholder="${esc(t("settings.desc"))}" />`}
        </div>
        <textarea data-i="${i}" data-f="paths" rows="2" placeholder="${esc(t("settings.paths"))}">${esc(c.paths.join("\n"))}</textarea>
        <div class="edit-actions">
          <label class="check"><input type="checkbox" data-i="${i}" data-f="enabled" ${c.enabled ? "checked" : ""} /> ${esc(t("settings.enabled"))}</label>
          <button class="btn ghost small remove-cat" data-i="${i}" type="button">${esc(t("settings.remove"))}</button>
        </div>
      </div>`,
    )
    .join("");

  editorEl
    .querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("input[data-f], textarea[data-f]")
    .forEach((el) => {
      el.addEventListener(
        el instanceof HTMLInputElement && el.type === "checkbox" ? "change" : "input",
        () => {
          const cfg = catConfig[Number(el.dataset.i)];
          if (!cfg) return;
          const f = el.dataset.f!;
          if (f === "enabled") cfg.enabled = (el as HTMLInputElement).checked;
          else if (f === "paths") cfg.paths = el.value.split("\n");
          else if (f === "name") cfg.name = el.value;
          else cfg.description = el.value;
        },
      );
    });
  editorEl.querySelectorAll<HTMLButtonElement>(".remove-cat").forEach((b) => {
    b.addEventListener("click", () => {
      catConfig.splice(Number(b.dataset.i), 1);
      renderCatEditor();
    });
  });
}

addCatBtn.addEventListener("click", () => {
  catConfig.push({ name: "", description: "", risk: "safe", paths: [""], enabled: true });
  renderCatEditor();
});

saveCatsBtn.addEventListener("click", async () => {
  setBusy(saveCatsBtn, true);
  settingsStatus.textContent = "";
  settingsStatus.classList.remove("err");
  try {
    await Cleaner.saveCategories(catConfig);
    settingsStatus.textContent = t("settings.saved");
    toast(t("settings.saved"));
    await scan();
  } catch (e) {
    settingsStatus.textContent = t("settings.saveFail", { msg: errText(e) });
    settingsStatus.classList.add("err");
    toast(t("settings.saveFail", { msg: errText(e) }), "err");
  } finally {
    setBusy(saveCatsBtn, false);
  }
});

// ---- 弹窗 ----

/** 每次打开或关闭确认框都换一个编号；晚到的异步结果凭它判断这个框是否还是自己的 */
let modalSeq = 0;
function showModal() {
  closePhotoViewer();
  modalSeq++;
  overlay.classList.remove("hidden");
  modalOk.focus();
}
function hideModal() {
  modalSeq++;
  overlay.classList.add("hidden");
  modalOk.disabled = false; // 取消一次还在分析的卸载，不能让之后的确认框都点不动
  pending = null;
}

// ---- 事件 ----

scanBtn.addEventListener("click", () => stopScan(scanBtn) || scan());
cleanAllBtn.addEventListener("click", () => {
  if (phase === "cleaning") stopClean();
  else if (phase === "cleaned") scan();
  else askCleanAll();
});
fsScanBtn.addEventListener("click", () => stopScan(fsScanBtn) || startFullScan());
lgScanBtn.addEventListener("click", () => stopScan(lgScanBtn) || startLargeScan());
orScanBtn.addEventListener("click", () => stopScan(orScanBtn) || startOrphanScan());
acScanBtn.addEventListener("click", startAppCacheScan);
phScanBtn.addEventListener("click", () => stopScan(phScanBtn) || startPhotoScan());
appSearch.addEventListener("input", renderApps);
appSort.addEventListener("change", () => {
  appSortBy = appSort.value as typeof appSortBy;
  saveChoice("cleanzo.appSort", appSortBy);
  if (!appsLoading) renderApps();
});
lgSize.addEventListener("change", changeLargeSize);
modalOk.addEventListener("click", async () => {
  const fn = pending;
  hideModal();
  if (fn) await fn();
});
modalCancel.addEventListener("click", hideModal);
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
overlay.addEventListener("click", (e) => {
  if (e.target === overlay) hideModal();
});

applyStaticI18n();
initIdleClean();
renderIdleScanPages();
// 版本号以应用自己为准，不写死在页面里
Cleaner.info()
  .then((i) => {
    if (i.version) $("#app-version").textContent = `Cleanzo v${i.version}`;
  })
  .catch(() => {});
