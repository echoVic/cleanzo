// Services and types are generated from the Go code by `mygo generate`.
import { Cleaner } from "./mygo";
import { Channel } from "mygo-runtime";
import type { AppInfo, AppCacheItem, BigItem, CategoryConfig, CleanSummary, FullScanResult, LargeFile, LargeScanResult, OrphanItem, PhotoCluster } from "./mygo";
import { t, categoryName, categoryDescription, kindLabel, getPref, setPref, currentLang, type LangPref } from "./i18n";

const $ = <T extends HTMLElement>(s: string) => document.querySelector<T>(s)!;

const listEl = $("#list");
const totalEl = $("#total");
const totalPrefix = $("#total-prefix");
const heroSub = $("#hero-sub");
const hero = $(".hero");
const scanBtn = $<HTMLButtonElement>("#scan");
const cleanAllBtn = $<HTMLButtonElement>("#clean-all");
const overlay = $("#overlay");
const modalTitle = $("#modal-title");
const modalBody = $("#modal-body");
const modalOk = $<HTMLButtonElement>("#modal-ok");
const modalCancel = $<HTMLButtonElement>("#modal-cancel");
const navItems = Array.from(document.querySelectorAll<HTMLButtonElement>(".nav-item"));
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
const lgThreshold = $("#lg-threshold");
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
const phList = $("#ph-list");
const phToolbar = $("#ph-toolbar");

let categories = [] as Awaited<ReturnType<typeof Cleaner.scan>>;
let apps: AppInfo[] = [];
let catConfig: CategoryConfig[] = [];
let fsResult: FullScanResult | null = null;
let pending: (() => Promise<void>) | null = null;
let fsRunning = false;
let fsItems: BigItem[] = [];
let lgRunning = false;
let lgItems: LargeFile[] = [];
let orRunning = false;
let orItems: OrphanItem[] = [];
let acRunning = false;
let acItems: AppCacheItem[] = [];
let phRunning = false;
let phClusters: PhotoCluster[] = [];
let phScanned = 0;

// ---- utils ----

const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

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
  langSelect.value = getPref();
}

/** Re-render the current view so all dynamic text follows the new language. */
function refreshI18n() {
  applyStaticI18n();
  const active = navItems.find((b) => b.classList.contains("active"))?.dataset.view;
  if (active === "clean" || active === undefined) render();
  else if (active === "apps") renderApps();
  else if (active === "settings") renderCatEditor();
  else if (active === "fullscan") {
    if (fsResult) renderFullScan(fsResult);
  } else if (active === "large") {
    lgThreshold.textContent = t("large.threshold", { size: human(1 << 30) });
    if (lgItems.length > 0) renderLargeRows(lgItems, false);
  } else if (active === "orphans") {
    if (orItems.length > 0) renderOrphanRows(orItems);
  } else if (active === "appcaches") {
    if (acItems.length > 0) renderAppCacheRows();
  } else if (active === "photos") {
    if (phClusters.length > 0) renderPhotoClusters();
  }
}

langSelect.addEventListener("change", () => {
  setPref(langSelect.value as LangPref);
  refreshI18n();
  toast(t("settings.saved"));
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

function setBusy(btn: HTMLButtonElement, busy: boolean, label?: string) {
  if (busy) {
    btn.dataset.orig = btn.dataset.origText ?? btn.textContent ?? "";
    btn.dataset.origText = btn.dataset.orig;
    btn.disabled = true;
    btn.innerHTML = `<span class="spin"></span>${esc(label ?? t("scan.busy"))}`;
  } else {
    btn.disabled = false;
    const orig = btn.dataset.origText ?? btn.textContent ?? "";
    btn.innerHTML = orig;
  }
}

function skeletonRows(n: number): string {
  return Array.from({ length: n }, () => `<div class="skeleton card"></div>`).join("");
}

function emptyState(text: string): string {
  return `<div class="empty">${esc(text)}</div>`;
}

function errorState(text: string, retry?: () => void): string {
  const btn = retry ? `<button class="btn ghost retry-btn" type="button">${esc(t("btn.retry"))}</button>` : "";
  return `<div class="empty err"><div class="err-icon">⚠</div><div>${esc(text)}</div>${btn}</div>`;
}

// ---- 视图切换 ----

const VIEWS = ["clean", "fullscan", "large", "orphans", "appcaches", "photos", "apps", "settings"] as const;
type View = (typeof VIEWS)[number];

function switchView(v: View) {
  navItems.forEach((b) => b.classList.toggle("active", b.dataset.view === v));
  VIEWS.forEach((k) => $(`#view-${k}`).classList.toggle("hidden", k !== v));
  if (v === "apps" && apps.length === 0) loadApps();
  if (v === "settings" && catConfig.length === 0) loadCatConfig();
  if (v === "appcaches" && acItems.length === 0 && !acRunning) startAppCacheScan();
  if (v === "photos" && phClusters.length === 0 && !phRunning) startPhotoScan();
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

function initIdleClean() {
  totalPrefix.textContent = t("hero.ready");
  totalEl.textContent = "--";
  heroSub.textContent = t("clean.idleSub");
  hero.classList.add("compact");
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
  cleanAllBtn.disabled = true;
  scanBtn.textContent = t("scan.start");
  scanBtn.disabled = false;
}

async function scan() {
  setBusy(scanBtn, true);
  cleanAllBtn.disabled = true;
  totalPrefix.textContent = t("hero.ready");
  totalEl.textContent = "--";
  heroSub.textContent = t("scan.hero");
  listEl.classList.remove("grouped", "idle-list");
  listEl.classList.add("cat-grid");
  listEl.innerHTML = skeletonRows(window.innerWidth >= 1600 ? 9 : window.innerWidth >= 1200 ? 8 : 6);
  try {
    categories = await Cleaner.scan();
    render();
  } catch (e) {
    totalEl.textContent = t("scan.failed");
    heroSub.textContent = t("scan.retryHint");
    cleanAllBtn.disabled = true;
    listEl.innerHTML = errorState(t("scan.error", { msg: msg(e) }), scan);
    toast(t("scan.error", { msg: msg(e) }), "err");
  } finally {
    setBusy(scanBtn, false);
    scanBtn.textContent = categories.length > 0 ? t("fs.rescan") : t("scan.start");
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

function render() {
  if (categories.length === 0) {
    initIdleClean();
    return;
  }
  hero.classList.remove("compact");
  listEl.classList.remove("idle-list", "cat-grid");
  listEl.classList.add("grouped");
  const total = categories.reduce((s, c) => s + c.size, 0);
  totalPrefix.textContent = t("clean.foundPrefix");
  totalEl.textContent = human(total);

  // 默认勾选「建议清理」类；「谨慎清理」类（moderate）默认不勾，由用户手动确认
  selectedCats.clear();
  categories.forEach((c) => {
    if (c.size > 0 && c.risk !== "moderate") selectedCats.add(c.name);
  });

  const groups: { key: string; icon: string; color: string; cats: ScanCat[] }[] = [
    ...CAT_GROUPS.map((g) => ({ ...g, cats: [] as ScanCat[] })),
    { key: "group.other", icon: "✦", color: "#64748b", names: [], cats: [] as ScanCat[] },
  ];
  categories.forEach((c) => groups[groupIndexOf(c.name)]?.cats.push(c));
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
      Cleaner.openFullDiskAccessSettings().catch((e) => toast(t("grant.fail", { msg: msg(e) }), "err"));
    });
  });
  updateSelection();
  scanBtn.textContent = t("fs.rescan");
}

/** 勾选变化时联动：横幅「已选择」、各分组小计、立即清理按钮可用态 */
function updateSelection() {
  const sel = categories.filter((c) => selectedCats.has(c.name) && c.size > 0);
  const total = sel.reduce((s, c) => s + c.size, 0);
  heroSub.textContent = t("clean.selected", { size: human(total) });
  cleanAllBtn.disabled = sel.length === 0;
  listEl.querySelectorAll<HTMLElement>(".group-sel").forEach((el) => {
    const names = (el.dataset.names ?? "").split(",").filter(Boolean);
    const inGroup = (c: ScanCat) => names.includes(c.name);
    const gtotal = categories.filter(inGroup).reduce((s, c) => s + c.size, 0);
    const gsel = categories.filter((c) => inGroup(c) && selectedCats.has(c.name)).reduce((s, c) => s + c.size, 0);
    el.textContent = t("group.summary", { total: human(gtotal), sel: human(gsel) });
  });
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
  const total = cats.reduce((s, c) => s + c.size, 0);
  const sel = cats.filter((c) => selectedCats.has(c.name)).reduce((s, c) => s + c.size, 0);
  const names = cats.map((c) => c.name).join(",");
  return `
    <details class="cat-group" open>
      <summary>
        <div class="tile group-tile" style="--c:${color}"><span>${icon}</span></div>
        <span class="group-name">${esc(t(key as never))}</span>
        <span class="group-sel" data-names="${esc(names)}">${esc(t("group.summary", { total: human(total), sel: human(sel) }))}</span>
        <span class="group-caret" aria-hidden="true">▾</span>
      </summary>
      <div class="group-rows">${cats.map(rowHtml).join("")}</div>
    </details>`;
}

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

function rowHtml(c: ScanCat): string {
  const meta = CAT_ICONS[c.name] ?? fallbackIcon;
  const empty = c.size === 0;
  const caution = !empty && c.risk === "moderate";
  const checked = selectedCats.has(c.name);
  const trashTag = c.permanent ? "" : `<span class="tag dim">${esc(t("cat.toTrash"))}</span>`;
  const subs = [
    c.inUse > 0 ? esc(t("cat.inUse", { n: c.inUse })) : "",
    !empty && c.denied.length > 0 ? `${esc(t("cat.deniedPart"))}${grantBtn()}` : "",
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
    <div class="cat-row${empty ? " is-empty" : ""}${caution ? " caution" : ""}">
      <label class="cat-check"><input type="checkbox" data-cat="${esc(c.name)}" ${empty ? "disabled" : ""} ${checked ? "checked" : ""}></label>
      <div class="tile" style="--c:${meta.color}"><span>${meta.icon}</span></div>
      <div class="cat-info">
        <div class="cat-name">${esc(categoryName(c.name))}${trashTag}</div>
        <div class="cat-desc muted">${esc(categoryDescription(c.name, c.description))}</div>
        ${subs}
      </div>
      <div class="cat-note">${catNote(c)}</div>
      ${tops ? `<details class="cat-details"><summary>${esc(t("cat.tops"))}</summary>${tops}</details>` : ""}
    </div>`;
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
  const names = (cs: ScanCat[]) => cs.map((c) => categoryName(c.name)).join(t("list.sep"));
  const del = sel.filter((c) => c.permanent);
  const keep = sel.filter((c) => !c.permanent);
  const lines = [
    del.length > 0 ? t("clean.body.delete", { size: sum(del), names: names(del) }) : "",
    keep.length > 0 ? t("clean.body.trash", { size: sum(keep), names: names(keep) }) : "",
    t("clean.body.inUse"),
  ].filter(Boolean);
  pending = async () => {
    try {
      const res = await Cleaner.clean(sel.map((c) => c.name));
      if (res.errors.length > 0) {
        toast(t("clean.all.partial", { names: res.errors.map((e) => categoryName(e.name)).join(t("list.sep")) }), "err");
      } else {
        toast(cleanDoneText(res));
      }
    } catch (e) {
      toast(t("clean.fail", { msg: msg(e) }), "err");
    }
    await scan();
  };
  modalTitle.textContent = t("clean.sel.title");
  modalBody.innerHTML = lines.map((l) => `<p>${esc(l)}</p>`).join("");
  modalOk.textContent = t("clean.confirm");
  showModal();
}

// ---- 全盘扫描 ----

function renderFsRows(items: BigItem[], live: boolean, foot = "") {
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
        fsRowHtml({
          path: it.path,
          name: esc(displayPath(it.path)),
          size: it.size,
          rank: `${i + 1}`,
          expandable: true,
          trashable: it.trashable,
          sub: `<div class="fs-track"><div class="fs-fill" style="width:${Math.max(2, Math.round((it.size / maxSize) * 100))}%"></div></div>`,
        }),
    )
    .join("");
  const footHtml = (live ? `<div class="fs-foot muted small">${esc(t("fs.live"))}</div>` : "") + foot;
  renderFsListInto(fsList, fsToolbar, rows + footHtml, () => renderFsRows(fsItems, false), startFullScan);
}

async function startFullScan() {
  checkedPaths.clear();
  if (fsRunning) return;
  fsRunning = true;
  fsItems = [];
  setBusy(fsScanBtn, true, t("fs.busy"));
  fsProgress.classList.remove("hidden");
  fsList.innerHTML = "";
  fsCurrent.textContent = t("fs.prep");
  fsCount.textContent = "";
  const ch = new Channel<import("./mygo").FullScanEvent>();
  try {
    const done = Cleaner.fullScan(ch);
    for await (const ev of ch) {
      if (ev.kind === "item" && ev.path) {
        fsCurrent.textContent = t("fs.scanning", { path: displayPath(ev.path) });
        fsItems.push({ path: ev.path, size: ev.size ?? 0, trashable: ev.trashable ?? false });
        fsCount.textContent = t("fs.count", { n: fsItems.length.toLocaleString() });
        renderFsRows(fsItems, true);
      } else if (ev.kind === "done" && ev.result) {
        fsResult = ev.result;
        fsItems = ev.result.items;
        renderFullScan(ev.result);
      }
    }
    await done;
  } catch (e) {
    fsResult = null;
    fsList.innerHTML = errorState(t("fs.fail", { msg: msg(e) }), startFullScan);
    toast(t("fs.fail", { msg: msg(e) }), "err");
  } finally {
    fsRunning = false;
    fsProgress.classList.add("hidden");
    setBusy(fsScanBtn, false);
  }
}

// ---- 勾选与展开（全盘 / 大文件 / 残留 通用） ----

const checkedPaths = new Set<string>();
const expandedPaths = new Set<string>();
const expandedChildren = new Map<string, string>();
const expandedChildPaths = new Map<string, string[]>();

function checkHtml(path: string, parent?: string): string {
  const par = parent ? ` data-parent="${esc(parent)}"` : "";
  return `<label class="fs-check" title="${esc(path)}"><input type="checkbox" data-check="${esc(path)}"${par} ${checkedPaths.has(path) ? "checked" : ""}></label>`;
}

function setParentChecked(path: string, checked: boolean) {
  if (checked) {
    checkedPaths.add(path);
    const kids = expandedChildPaths.get(path);
    if (kids) kids.forEach((k) => checkedPaths.add(k));
  } else {
    checkedPaths.delete(path);
    const kids = expandedChildPaths.get(path);
    if (kids) kids.forEach((k) => checkedPaths.delete(k));
  }
}

function syncParent(listEl: HTMLElement, parentPath: string) {
  const kids = expandedChildPaths.get(parentPath);
  if (!kids || kids.length === 0) return;
  const n = kids.filter((k) => checkedPaths.has(k)).length;
  if (n === kids.length) checkedPaths.add(parentPath);
  else if (n === 0) checkedPaths.delete(parentPath);
}

function applyIndeterminate(listEl: HTMLElement) {
  listEl.querySelectorAll<HTMLDivElement>(".fs-row-wrap").forEach((wrap) => {
    const p = wrap.dataset.path!;
    const kids = expandedChildPaths.get(p);
    if (!kids || kids.length === 0) return;
    const cb = wrap.querySelector<HTMLInputElement>("input[data-check]");
    if (!cb) return;
    const n = kids.filter((k) => checkedPaths.has(k)).length;
    cb.indeterminate = n > 0 && n < kids.length;
  });
}

function expandBtnHtml(path: string): string {
  const open = expandedPaths.has(path);
  return `<button class="btn small ghost fs-expand" data-expand="${esc(path)}" type="button">${open ? esc(t("fs.collapse")) : esc(t("fs.expand"))} <span class="caret">▸</span></button>`;
}

function revealBtnHtml(path: string): string {
  return `<button class="btn small ghost icon fs-reveal" data-reveal="${esc(path)}" type="button" title="${esc(t("fs.reveal"))}" aria-label="${esc(t("fs.reveal"))}"><svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"/></svg></button>`;
}

function fsRowHtml(o: {
  path: string;
  name: string;
  size: number;
  sub?: string;
  rank?: string;
  expandable?: boolean;
  trashable: boolean;
}): string {
  const open = expandedPaths.has(o.path);
  return `
      <div class="fs-row-wrap" data-path="${esc(o.path)}">
        <div class="fs-row card ${open ? "open" : ""}">
          ${checkHtml(o.path)}
          ${o.rank ? `<div class="fs-rank">${o.rank}</div>` : ""}
          <div class="fs-main">
            <div class="fs-path" title="${esc(o.path)}">${o.name}</div>
            ${o.sub ? `<div class="fs-sub">${o.sub}</div>` : ""}
          </div>
          <div class="fs-size">${o.size >= 0 ? human(o.size) : "--"}</div>
          ${o.expandable ? expandBtnHtml(o.path) : ""}
          ${revealBtnHtml(o.path)}
          ${o.trashable ? `<button class="btn small danger fs-trash" data-path="${esc(o.path)}" data-size="${o.size}" type="button">${esc(t("fs.trash"))}</button>` : ""}
        </div>
        ${expandedPaths.has(o.path) ? `<div class="fs-children">${expandedChildren.get(o.path) ?? `<span class="muted small">${esc(t("fs.children"))}</span>`}</div>` : ""}
      </div>`;
}

function childRowsHtml(items: { name: string; path: string; size: number; trashable: boolean }[], parentPath: string): string {
  if (items.length === 0) return `<div class="fs-child muted small">${esc(t("fs.childrenEmpty"))}</div>`;
  return items
    .map(
      (c) => `
      <div class="fs-child">
        ${checkHtml(c.path, parentPath)}
        <span class="fs-child-name" title="${esc(c.path)}">${esc(c.name)}${c.trashable ? "" : ` <span class="tag dim">${esc(t("fs.locked"))}</span>`}</span>
        <span class="fs-child-size">${c.size >= 0 ? human(c.size) : "--"}</span>
        ${revealBtnHtml(c.path)}
        ${c.trashable ? `<button class="btn small danger fs-child-trash" data-path="${esc(c.path)}" data-size="${c.size}" type="button">${esc(t("fs.trash"))}</button>` : ""}
      </div>`,
    )
    .join("");
}

async function toggleExpand(path: string, rerender: () => void) {
  if (expandedPaths.has(path)) {
    expandedPaths.delete(path);
    expandedChildren.delete(path);
    expandedChildPaths.delete(path);
    rerender();
    return;
  }
  expandedPaths.add(path);
  expandedChildren.delete(path);
  rerender();
  try {
    const items = await Cleaner.dirChildren(path);
    expandedChildPaths.set(path, items.map((i) => i.path));
    if (checkedPaths.has(path)) items.forEach((i) => checkedPaths.add(i.path));
    expandedChildren.set(path, childRowsHtml(items, path));
  } catch (e) {
    expandedChildren.set(path, `<div class="fs-child err-inline">${esc(t("fs.childrenFail", { msg: msg(e) }))}</div>`);
  }
  rerender();
}

function updateToolbar(toolbar: HTMLElement, listEl: HTMLElement, rerender: () => void, afterTrash: () => Promise<void>) {
  const visible = Array.from(listEl.querySelectorAll<HTMLInputElement>("input[data-check]")).map((i) => i.dataset.check!);
  const n = checkedPaths.size;
  if (n === 0) {
    toolbar.classList.add("hidden");
    toolbar.innerHTML = "";
    return;
  }
  const all = visible.length > 0 && visible.every((p) => checkedPaths.has(p));
  toolbar.classList.remove("hidden");
  toolbar.innerHTML = `
    <label class="fs-check"><input type="checkbox" class="fs-checkall" ${all ? "checked" : ""}></label>
    <span class="fs-toolbar-count">${esc(t("fs.checked", { n }))}</span>
    <button class="btn danger small fs-toolbar-trash" type="button">${esc(t("fs.trashChecked"))}</button>`;
  const allBox = toolbar.querySelector<HTMLInputElement>(".fs-checkall");
  if (allBox) {
    allBox.addEventListener("change", () => {
      visible.forEach((p) => (allBox.checked ? checkedPaths.add(p) : checkedPaths.delete(p)));
      rerender();
      updateToolbar(toolbar, listEl, rerender, afterTrash);
    });
  }
  toolbar.querySelector<HTMLButtonElement>(".fs-toolbar-trash")?.addEventListener("click", () => trashChecked(afterTrash, rerender));
}

async function trashChecked(after: () => Promise<void>, rerender: () => void) {
  const paths = [...checkedPaths].filter((p) => ![...checkedPaths].some((q) => p !== q && p.startsWith(q + "/")));
  if (paths.length === 0) return;
  pending = async () => {
    let ok = 0;
    let fail = 0;
    let freed = 0;
    for (const p of paths) {
      try {
        const r = await Cleaner.trashPath(p);
        ok++;
        freed += r.freed;
      } catch {
        fail++;
      }
    }
    checkedPaths.clear();
    if (ok > 0) toast(t("fs.trashChecked.ok", { n: ok, size: human(freed) }));
    if (fail > 0) toast(t("fs.trashChecked.fail", { n: fail }), "err");
    if (after) await after();
    rerender();
  };
  modalTitle.textContent = t("fs.trashChecked.title");
  modalBody.textContent = t("fs.trashChecked.body", { n: paths.length });
  modalOk.textContent = t("fs.trash.confirm");
  showModal();
}

function bindFsList(listEl: HTMLElement, rerender: () => void, afterTrash: () => Promise<void>) {
  listEl.querySelectorAll<HTMLInputElement>("input[data-check]").forEach((cb) => {
    cb.addEventListener("change", () => {
      const p = cb.dataset.check!;
      const parent = cb.dataset.parent;
      if (parent) {
        if (cb.checked) checkedPaths.add(p);
        else checkedPaths.delete(p);
        syncParent(listEl, parent);
      } else if (cb.checked) {
        setParentChecked(p, true);
      } else {
        setParentChecked(p, false);
      }
      rerender();
    });
  });
  listEl.querySelectorAll<HTMLButtonElement>(".fs-expand").forEach((b) => {
    b.addEventListener("click", () => toggleExpand(b.dataset.expand!, rerender));
  });
  listEl.querySelectorAll<HTMLButtonElement>(".fs-reveal").forEach((b) => {
    b.addEventListener("click", () => {
      const path = b.dataset.reveal!;
      Cleaner.revealInFinder(path).catch((e) => toast(t("fs.revealFail", { msg: msg(e) }), "err"));
    });
  });
  listEl.querySelectorAll<HTMLButtonElement>(".fs-trash").forEach((b) => {
    b.addEventListener("click", () => askTrash(b.dataset.path!, Number(b.dataset.size ?? 0), afterTrash));
  });
}

function renderFsListInto(listEl: HTMLElement, toolbar: HTMLElement, html: string, rerender: () => void, afterTrash: () => Promise<void>) {
  listEl.innerHTML = html;
  bindFsList(listEl, rerender, afterTrash);
  applyIndeterminate(listEl);
  updateToolbar(toolbar, listEl, rerender, afterTrash);
}

function renderFullScan(r: FullScanResult) {
  const usedPct = r.disk.total ? Math.min(100, Math.round((r.disk.used / r.disk.total) * 100)) : 0;
  fsUsed.textContent = human(r.disk.used);
  fsFree.textContent = human(r.disk.free);
  fsTotal.textContent = human(r.disk.total);
  fsBar.style.width = `${usedPct}%`;
  fsCount.textContent = "";
  renderFsRows(r.items, false);
}

function askTrash(path: string, size: number, after?: () => Promise<void>) {
  pending = async () => {
    try {
      const res = await Cleaner.trashPath(path);
      toast(t("fs.trash.ok", { path: displayPath(path), size: human(res.freed) }));
      if (after) await after();
    } catch (e) {
      toast(t("fs.actionFail", { msg: msg(e) }), "err");
    }
  };
  modalTitle.textContent = t("fs.trash.title");
  modalBody.textContent = t("fs.trash.body", { path: displayPath(path), size: human(size) });
  modalOk.textContent = t("fs.trash.confirm");
  showModal();
}

// ---- 大文件专清 ----

async function startLargeScan() {
  checkedPaths.clear();
  if (lgRunning) return;
  lgRunning = true;
  lgItems = [];
  setBusy(lgScanBtn, true, t("large.busy"));
  lgProgress.classList.remove("hidden");
  lgList.innerHTML = "";
  lgCurrent.textContent = t("fs.prep");
  lgCount.textContent = "";
  const ch = new Channel<import("./mygo").LargeScanEvent>();
  try {
    const done = Cleaner.largeScan(ch);
    for await (const ev of ch) {
      if (ev.kind === "item" && ev.path) {
        lgCurrent.textContent = t("fs.scanning", { path: displayPath(ev.path) });
        lgItems.push({ path: ev.path, size: ev.size ?? 0, trashable: ev.trashable ?? false });
        lgCount.textContent = t("large.count", { n: lgItems.length.toLocaleString() });
        renderLargeRows(lgItems, true);
      } else if (ev.kind === "done" && ev.result) {
        lgItems = ev.result.items;
        renderLargeDone(ev.result);
      }
    }
    await done;
  } catch (e) {
    lgItems = [];
    lgList.innerHTML = errorState(t("large.fail", { msg: msg(e) }), startLargeScan);
    toast(t("large.fail", { msg: msg(e) }), "err");
  } finally {
    lgRunning = false;
    lgProgress.classList.add("hidden");
    setBusy(lgScanBtn, false);
  }
}

function renderLargeRows(items: LargeFile[], live: boolean, foot = "") {
  if (items.length === 0) {
    lgList.innerHTML = emptyState(live ? t("large.live") : t("large.empty", { size: human(1 << 30) })) + foot;
    lgToolbar.classList.add("hidden");
    lgToolbar.innerHTML = "";
    return;
  }
  const sorted = [...items].sort((a, b) => b.size - a.size).slice(0, 50);
  const maxSize = sorted[0]?.size || 1;
  const rows = sorted
    .map(
      (it, i) =>
        fsRowHtml({
          path: it.path,
          name: esc(displayPath(it.path)),
          size: it.size,
          rank: `${i + 1}`,
          trashable: it.trashable,
          sub: `<div class="fs-track"><div class="fs-fill" style="width:${Math.max(2, Math.round((it.size / maxSize) * 100))}%"></div></div>`,
        }),
    )
    .join("");
  const footHtml = (live ? `<div class="fs-foot muted small">${esc(t("large.live"))}</div>` : "") + foot;
  renderFsListInto(lgList, lgToolbar, rows + footHtml, () => renderLargeRows(lgItems, false), startLargeScan);
}

function renderLargeDone(r: LargeScanResult) {
  const foot = `<div class="fs-foot muted small">${esc(t("large.foot", { n: r.scanned.toLocaleString(), t: (r.elapsedMs / 1000).toFixed(1), s: r.skipped.toLocaleString() }))}</div>`;
  renderLargeRows(r.items, false, foot);
}

// ---- 残留清理 ----

async function startOrphanScan() {
  checkedPaths.clear();
  if (orRunning) return;
  orRunning = true;
  orItems = [];
  setBusy(orScanBtn, true, t("orphans.busy"));
  orSummary.classList.add("hidden");
  orProgress.classList.remove("hidden");
  orCurrent.textContent = t("orphans.live");
  orList.innerHTML = "";
  const ch = new Channel<OrphanItem>();
  try {
    const done = Cleaner.scanOrphans(ch);
    for await (const o of ch) {
      if (o.kind === "__done") {
        const total = o.size ?? 0;
        const elapsed = Number(o.path ?? 0) / 1000;
        orProgress.classList.add("hidden");
        orSummary.classList.remove("hidden");
        orTotal.textContent = t("orphans.foot", { n: orItems.length.toLocaleString(), size: human(total), t: elapsed.toFixed(1) });
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
  } catch (e) {
    orItems = [];
    orList.innerHTML = errorState(t("orphans.fail", { msg: msg(e) }), startOrphanScan);
    toast(t("orphans.fail", { msg: msg(e) }), "err");
  } finally {
    orRunning = false;
    orProgress.classList.add("hidden");
    setBusy(orScanBtn, false);
  }
}

function renderOrphanRows(items: OrphanItem[]) {
  if (items.length === 0) return;
  const sorted = [...items].sort((a, b) => b.size - a.size);
  const maxSize = sorted[0]?.size || 1;
  const rows = sorted
    .map(
      (o, i) =>
        fsRowHtml({
          path: o.path,
          name: `${esc(displayPath(o.path))} <span class="tag dim">${esc(kindLabel(o.kind))}</span>`,
          size: o.size,
          rank: `${i + 1}`,
          expandable: true,
          trashable: o.trashable,
          sub: `<div class="fs-track"><div class="fs-fill" style="width:${Math.max(2, Math.round((o.size / maxSize) * 100))}%"></div></div>`,
        }),
    )
    .join("");
  renderFsListInto(orList, orToolbar, rows, () => renderOrphanRows(orItems), startOrphanScan);
}

// ---- 应用缓存（按应用拆分） ----

const baseName = (p: string) => p.split("/").filter(Boolean).pop() ?? p;

async function startAppCacheScan() {
  checkedPaths.clear();
  if (acRunning) return;
  acRunning = true;
  setBusy(acScanBtn, true, t("appcaches.busy"));
  acProgress.classList.remove("hidden");
  acList.innerHTML = "";
  acCurrent.textContent = t("appcaches.busy");
  try {
    acItems = await Cleaner.scanAppCaches();
    renderAppCacheRows();
  } catch (e) {
    acItems = [];
    acList.innerHTML = errorState(t("fs.fail", { msg: msg(e) }), startAppCacheScan);
    toast(t("fs.fail", { msg: msg(e) }), "err");
  } finally {
    acRunning = false;
    acProgress.classList.add("hidden");
    setBusy(acScanBtn, false);
    acScanBtn.textContent = t("appcaches.rescan");
  }
}

function renderAppCacheRows() {
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
        fsRowHtml({
          path: it.path,
          name: esc(it.name),
          size: it.size,
          rank: `${i + 1}`,
          trashable: !it.inUse,
          sub: `<div class="fs-sub muted small">${it.inUse ? `<span class="tag dim">${esc(t("appcaches.inUse"))}</span> ` : ""}${esc(displayPath(it.path))}</div>`,
        }),
    )
    .join("");
  const foot = `<div class="fs-foot muted small">${esc(t("appcaches.count", { n: acItems.length }))}</div>`;
  renderFsListInto(acList, acToolbar, rows + foot, renderAppCacheRows, startAppCacheScan);
}

// ---- 相似照片 ----

async function startPhotoScan() {
  checkedPaths.clear();
  if (phRunning) return;
  phRunning = true;
  setBusy(phScanBtn, true, t("photos.busy"));
  phProgress.classList.remove("hidden");
  phList.innerHTML = "";
  phCurrent.textContent = t("photos.busy");
  const ch = new Channel<import("./mygo").SimilarPhotoEvent>();
  try {
    const done = Cleaner.scanSimilarPhotos(ch);
    for await (const ev of ch) {
      if (ev.kind === "progress") {
        phCurrent.textContent = t("photos.progress", {
          n: (ev.processed ?? 0).toLocaleString(),
          total: (ev.total ?? 0).toLocaleString(),
        });
      } else if (ev.kind === "done" && ev.result) {
        phClusters = ev.result.clusters;
        phScanned = ev.result.scanned;
        // default: keep the recommended copy unchecked, trash everything else
        checkedPaths.clear();
        for (const cl of phClusters) {
          for (const it of cl.items) if (!it.keep) checkedPaths.add(it.path);
        }
        renderPhotoClusters();
      }
    }
    await done;
  } catch (e) {
    phClusters = [];
    phList.innerHTML = errorState(t("photos.fail", { msg: msg(e) }), startPhotoScan);
    toast(t("photos.fail", { msg: msg(e) }), "err");
  } finally {
    phRunning = false;
    phProgress.classList.add("hidden");
    setBusy(phScanBtn, false);
    phScanBtn.textContent = t("photos.rescan");
  }
}

function renderPhotoClusters() {
  if (phClusters.length === 0) {
    phToolbar.classList.add("hidden");
    phToolbar.innerHTML = "";
    phList.innerHTML = emptyState(t("photos.empty"));
    return;
  }
  const cards = phClusters
    .map((cl) => {
      const badge =
        cl.kind === "exact"
          ? `<span class="ph-badge exact">${esc(t("photos.kindExact"))}</span>`
          : `<span class="ph-badge similar">${esc(t("photos.kindSimilar"))}</span>`;
      const cells = cl.items
        .map((it) => {
          const keepCls = it.keep ? " keep" : "";
          const keepTag = it.keep
            ? `<span class="ph-keep-tag">${esc(t("photos.keep"))}</span>`
            : "";
          return `
        <figure class="photo-cell${keepCls}">
          <div class="photo-thumb-wrap">
            ${checkHtml(it.path)}
            ${keepTag}
            <img class="photo-thumb" src="${it.thumb}" alt="" loading="lazy">
          </div>
          <figcaption class="photo-meta" title="${esc(it.path)}">
            <div class="photo-name">${esc(baseName(it.path))}</div>
            <div class="photo-sub muted small">${esc(t("photos.sizeDim", { w: it.width, h: it.height }))} · ${human(it.size)}</div>
          </figcaption>
          ${revealBtnHtml(it.path)}
        </figure>`;
        })
        .join("");
      return `
      <div class="photo-cluster card" data-cluster="${cl.id}">
        <div class="photo-cluster-head">
          <span class="photo-cluster-title">${esc(t("photos.cluster", { id: cl.id }))} ${badge}</span>
          <span class="photo-cluster-meta muted">${esc(t("photos.photoCount", { n: cl.items.length }))} · ${esc(t("photos.waste", { size: human(cl.waste) }))}</span>
          <button class="btn small ghost ph-keep-only" type="button" data-cluster="${cl.id}">${esc(t("photos.keepOnly"))}</button>
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
  phList.querySelectorAll<HTMLInputElement>("input[data-check]").forEach((cb) => {
    cb.addEventListener("change", () => {
      const p = cb.dataset.check!;
      if (cb.checked) checkedPaths.add(p);
      else checkedPaths.delete(p);
      renderPhotoClusters();
    });
  });
  phList.querySelectorAll<HTMLButtonElement>(".fs-reveal").forEach((b) => {
    b.addEventListener("click", () => {
      Cleaner.revealInFinder(b.dataset.reveal!).catch((e) =>
        toast(t("fs.revealFail", { msg: msg(e) }), "err"),
      );
    });
  });
  phList.querySelectorAll<HTMLButtonElement>(".ph-keep-only").forEach((b) => {
    b.addEventListener("click", () => {
      const cl = phClusters.find((c) => String(c.id) === b.dataset.cluster);
      if (!cl) return;
      for (const it of cl.items) {
        if (it.keep) checkedPaths.delete(it.path);
        else checkedPaths.add(it.path);
      }
      renderPhotoClusters();
    });
  });
}

function visiblePhotoPaths(): string[] {
  return phClusters.flatMap((c) => c.items.map((i) => i.path));
}

function updatePhotoToolbar() {
  const visible = visiblePhotoPaths();
  const n = checkedPaths.size;
  if (n === 0) {
    phToolbar.classList.add("hidden");
    phToolbar.innerHTML = "";
    return;
  }
  const all = visible.length > 0 && visible.every((p) => checkedPaths.has(p));
  phToolbar.classList.remove("hidden");
  phToolbar.innerHTML = `
    <label class="fs-check"><input type="checkbox" class="ph-checkall" ${all ? "checked" : ""}></label>
    <span class="fs-toolbar-count">${esc(t("photos.checked", { n }))}</span>
    <button class="btn danger small ph-trash" type="button">${esc(t("photos.clean"))}</button>`;
  phToolbar.querySelector<HTMLInputElement>(".ph-checkall")?.addEventListener("change", (e) => {
    const on = (e.target as HTMLInputElement).checked;
    visible.forEach((p) => (on ? checkedPaths.add(p) : checkedPaths.delete(p)));
    renderPhotoClusters();
  });
  phToolbar.querySelector<HTMLButtonElement>(".ph-trash")?.addEventListener("click", askTrashPhotos);
}

function askTrashPhotos() {
  const paths = [...checkedPaths];
  if (paths.length === 0) return;
  pending = async () => {
    try {
      const r = await Cleaner.cleanPaths(paths);
      if (r.failed.length > 0) toast(t("photos.trashFail", { n: r.failed.length }), "err");
      else {
        const parts: string[] = [];
        if (r.moved > 0) parts.push(t("photos.trashOk", { n: r.moved, size: human(r.freed) }));
        if (r.inUse > 0) parts.push(t("photos.inUse", { n: r.inUse }));
        toast(parts.join(t("phrase.sep")), r.moved === 0 ? "err" : "ok");
      }
    } catch (e) {
      toast(t("fs.actionFail", { msg: msg(e) }), "err");
    }
    checkedPaths.clear();
    await startPhotoScan();
  };
  modalTitle.textContent = t("photos.clean");
  modalBody.textContent = t("photos.trashBody", { n: paths.length });
  modalOk.textContent = t("fs.trash.confirm");
  showModal();
}

// ---- 应用卸载 ----

async function loadApps() {
  appListEl.innerHTML = skeletonRows(8);
  try {
    apps = await Cleaner.listApps();
    renderApps();
  } catch (e) {
    apps = [];
    appListEl.innerHTML = errorState(t("apps.loadFail", { msg: msg(e) }), loadApps);
    toast(t("apps.loadFail", { msg: msg(e) }), "err");
  }
}

function renderApps() {
  const q = appSearch.value.trim().toLowerCase();
  const list = apps.filter(
    (a) => !q || a.name.toLowerCase().includes(q) || a.path.toLowerCase().includes(q),
  );
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
          <div class="app-name">${esc(a.name)}${a.version ? ` <span class="ver muted">${esc(a.version)}</span>` : ""}</div>
          <div class="app-path muted">${esc(a.path)}</div>
        </div>
        <div class="app-meta">
          <div class="app-size">${human(a.size)}</div>
          <div class="app-used muted small">${a.lastOpened ? fmtDate(a.lastOpened) : esc(t("apps.never"))}</div>
        </div>
        <button class="btn small uninstall-detail" data-path="${esc(a.path)}">${esc(t("apps.detail"))}</button>
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
  try {
    const d = await Cleaner.appUninstallDetail(path);
    modalTitle.textContent = t("apps.title", { name: d.app.name });
    const rows = d.items
      .map(
        (it) =>
          `<div class="detail-item"><span class="kind">${esc(kindLabel(it.kind))}</span><span class="path">${esc(it.path)}</span><span class="size">${human(it.size)}</span></div>`,
      )
      .join("");
    modalBody.innerHTML = `<div class="detail-list">${rows}</div>
      <p class="warn-text">${esc(t("apps.body", { n: d.items.length, size: human(d.totalSize) }))}</p>`;
    pending = async () => {
      try {
        const res = await Cleaner.uninstallApp(d.app.path);
        if (res.failed.length > 0) {
          toast(t("apps.partial", { name: d.app.name, n: res.failed.length }), "err");
        } else {
          toast(t("apps.ok", { name: d.app.name, size: human(res.freed) }));
        }
        await loadApps();
      } catch (e) {
        toast(t("apps.fail", { msg: msg(e) }), "err");
      }
    };
    modalOk.textContent = t("apps.confirm");
  } catch (e) {
    modalBody.innerHTML = `<div class="err-inline">${esc(t("apps.analyzeFail", { msg: msg(e) }))}</div>`;
    pending = null;
    modalOk.textContent = t("btn.close");
  } finally {
    modalOk.disabled = false;
  }
}

// ---- 设置 ----

async function loadCatConfig() {
  try {
    catConfig = await Cleaner.getCategories();
  } catch (e) {
    settingsStatus.textContent = t("settings.readFail", { msg: msg(e) });
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
          <input data-i="${i}" data-f="name" value="${esc(c.name)}" placeholder="${esc(t("settings.name"))}" />
          <input data-i="${i}" data-f="description" value="${esc(c.description)}" placeholder="${esc(t("settings.desc"))}" />
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
    settingsStatus.textContent = t("settings.saveFail", { msg: msg(e) });
    settingsStatus.classList.add("err");
    toast(t("settings.saveFail", { msg: msg(e) }), "err");
  } finally {
    setBusy(saveCatsBtn, false);
  }
});

// ---- 弹窗 ----

function showModal() {
  overlay.classList.remove("hidden");
  modalOk.focus();
}
function hideModal() {
  overlay.classList.add("hidden");
  pending = null;
}

// ---- 事件 ----

scanBtn.addEventListener("click", scan);
cleanAllBtn.addEventListener("click", askCleanAll);
fsScanBtn.addEventListener("click", startFullScan);
lgScanBtn.addEventListener("click", startLargeScan);
orScanBtn.addEventListener("click", startOrphanScan);
acScanBtn.addEventListener("click", startAppCacheScan);
phScanBtn.addEventListener("click", startPhotoScan);
appSearch.addEventListener("input", renderApps);
modalOk.addEventListener("click", async () => {
  const fn = pending;
  hideModal();
  if (fn) await fn();
});
modalCancel.addEventListener("click", hideModal);
overlay.addEventListener("click", (e) => {
  if (e.target === overlay) hideModal();
});

applyStaticI18n();
lgThreshold.textContent = t("large.threshold", { size: human(1 << 30) });
initIdleClean();
