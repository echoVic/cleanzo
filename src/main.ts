// Services and types are generated from the Go code by `mygo generate`.
import { Cleaner } from "./mygo";
import { Channel } from "mygo-runtime";
import type { AppInfo, BigItem, CategoryConfig, FullScanResult, LargeFile, LargeScanResult, OrphanItem } from "./mygo";
import { t, categoryDescription, kindLabel, getPref, setPref, currentLang, type LangPref } from "./i18n";

const $ = <T extends HTMLElement>(s: string) => document.querySelector<T>(s)!;

const listEl = $("#list");
const totalEl = $("#total");
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
const lgThreshold = $("#lg-threshold");
const orScanBtn = $<HTMLButtonElement>("#or-scan");
const orSummary = $("#or-summary");
const orTotal = $("#or-total");
const orProgress = $("#or-progress");
const orCurrent = $("#or-current");
const orList = $("#or-list");

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

const VIEWS = ["clean", "fullscan", "large", "orphans", "apps", "settings"] as const;
type View = (typeof VIEWS)[number];

function switchView(v: View) {
  navItems.forEach((b) => b.classList.toggle("active", b.dataset.view === v));
  VIEWS.forEach((k) => $(`#view-${k}`).classList.toggle("hidden", k !== v));
  if (v === "apps" && apps.length === 0) loadApps();
  if (v === "settings" && catConfig.length === 0) loadCatConfig();
}
navItems.forEach((b) => b.addEventListener("click", () => switchView(b.dataset.view as View)));

// ---- 垃圾清理 ----

const IDLE_ICON = ` <svg class="eg-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
  <circle cx="12" cy="12" r="10.2" stroke="url(#eg-grad)" stroke-width="1.4" stroke-dasharray="3.4 2.8" opacity="0.7"/>
  <circle cx="12" cy="12" r="6.8" stroke="url(#eg-grad)" stroke-width="1.1" opacity="0.35"/>
  <path d="M12 2c.9 6 4.4 9.5 10.4 10.4-6 .9-9.5 4.4-10.4 10.4-.9-6-4.4-9.5-10.4-10.4 6-.9 9.5-4.4 10.4-10.4z" fill="url(#eg-grad)"/>
  <circle cx="17.2" cy="6.6" r="0.9" fill="#fff" opacity="0.85"/>
  <circle cx="6.6" cy="17.4" r="0.7" fill="#fff" opacity="0.6"/>
  <defs><linearGradient id="eg-grad" x1="0" y1="0" x2="24" y2="24"><stop offset="0" stop-color="#10b981"/><stop offset="1" stop-color="#0ea5e9"/></linearGradient></defs>
</svg>`;

function initIdleClean() {
  totalEl.textContent = "--";
  heroSub.textContent = t("clean.idleSub");
  hero.classList.add("compact");
  listEl.classList.add("idle-list");
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
  totalEl.textContent = "--";
  heroSub.textContent = t("scan.hero");
  listEl.innerHTML = skeletonRows(6);
  try {
    categories = await Cleaner.scan();
    render();
  } catch (e) {
    totalEl.textContent = t("scan.failed");
    heroSub.textContent = t("scan.retryHint");
    listEl.innerHTML = errorState(t("scan.error", { msg: msg(e) }), scan);
    toast(t("scan.error", { msg: msg(e) }), "err");
  } finally {
    setBusy(scanBtn, false);
    scanBtn.textContent = categories.length > 0 ? t("fs.rescan") : t("scan.start");
    cleanAllBtn.disabled = categories.length === 0;
  }
}

function render() {
  if (categories.length === 0) {
    initIdleClean();
    return;
  }
  hero.classList.remove("compact");
  listEl.classList.remove("idle-list");
  const usable = categories.filter((c) => c.size > 0);
  const total = categories.reduce((s, c) => s + c.size, 0);
  const files = categories.reduce((s, c) => s + c.files, 0);
  totalEl.textContent = human(total);
  heroSub.textContent = usable.length
    ? t("clean.usable", { n: usable.length, f: files.toLocaleString() })
    : t("clean.nothing");
  cleanAllBtn.disabled = usable.length === 0;

  listEl.innerHTML = categories.map(cardHtml).join("");
  listEl.querySelectorAll<HTMLButtonElement>(".clean-one").forEach((b) => {
    b.addEventListener("click", () => askCleanOne(b.dataset.name!));
  });
  scanBtn.textContent = t("fs.rescan");
}

const CAT_ICONS: Record<string, { icon: string; color: string }> = {
  caches: { icon: "▦", color: "#10b981" },
  logs: { icon: "≣", color: "#0ea5e9" },
  temp: { icon: "⏱", color: "#f59e0b" },
  trash: { icon: "🗑", color: "#64748b" },
  npm: { icon: "⬢", color: "#ef4444" },
  go: { icon: "🐹", color: "#22c55e" },
  pip: { icon: "🐍", color: "#eab308" },
  homebrew: { icon: "🍺", color: "#f97316" },
  xcode: { icon: "🔨", color: "#3b82f6" },
};
const fallbackIcon = { icon: "🧹", color: "#64748b" };

function cardHtml(c: Awaited<ReturnType<typeof Cleaner.scan>>[number]): string {
  const meta = CAT_ICONS[c.name] ?? fallbackIcon;
  const missing = c.missing.length > 0;
  const tags = [
    c.risk === "moderate" ? `<span class="tag warn">${esc(t("cat.risk"))}</span>` : "",
    missing ? `<span class="tag dim">${esc(t("cat.missing"))}</span>` : "",
  ].join("");
  const tops = (c.top ?? [])
    .map(
      (item) =>
        `<div class="top-item"><span class="top-name">${esc(item.name)}</span><span class="top-path muted">${esc(item.path)}</span><span class="top-size">${human(item.size)}</span></div>`,
    )
    .join("");
  return `
    <div class="cat-card card">
      <div class="cat-top">
        <div class="tile" style="--c:${meta.color}"><span>${meta.icon}</span></div>
        <div class="cat-info">
          <div class="cat-name">${esc(c.name)}${tags}</div>
          <div class="cat-desc muted">${esc(categoryDescription(c.name, c.description))}</div>
        </div>
        <div class="cat-size">${human(c.size)}</div>
      </div>
      <div class="cat-foot">
        <span class="muted small">${t("cat.files", { n: c.files.toLocaleString() })}</span>
        <button class="btn small clean-one" data-name="${esc(c.name)}" ${c.size === 0 ? "disabled" : ""}>${esc(t("cat.clean"))}</button>
      </div>
      ${tops ? `<details class="cat-details"><summary>${esc(t("cat.tops"))}</summary>${tops}</details>` : ""}
    </div>`;
}

function askCleanOne(name: string) {
  const c = categories.find((x) => x.name === name);
  if (!c || c.size === 0) return;
  pending = async () => {
    try {
      const res = await Cleaner.clean([name]);
      if (res.errors.length > 0) {
        toast(t("clean.one.partial", { name, err: res.errors.map((e) => e.error).join("; ") }), "err");
      } else {
        toast(t("clean.one.ok", { name, size: human(res.totalFreed) }));
      }
    } catch (e) {
      toast(t("clean.fail", { msg: msg(e) }), "err");
    }
    await scan();
  };
  modalTitle.textContent = t("clean.one.title", { name });
  modalBody.textContent = t("clean.one.body", {
    size: human(c.size),
    desc: categoryDescription(c.name, c.description),
    files: c.files.toLocaleString(),
  });
  modalOk.textContent = t("clean.confirm");
  showModal();
}

function askCleanAll() {
  const usable = categories.filter((c) => c.size > 0);
  if (usable.length === 0) return;
  const total = usable.reduce((s, c) => s + c.size, 0);
  pending = async () => {
    try {
      const res = await Cleaner.clean(usable.map((c) => c.name));
      if (res.errors.length > 0) {
        toast(t("clean.all.partial", { names: res.errors.map((e) => e.name).join("、") }), "err");
      } else {
        toast(t("clean.all.ok", { size: human(res.totalFreed) }));
      }
    } catch (e) {
      toast(t("clean.fail", { msg: msg(e) }), "err");
    }
    await scan();
  };
  modalTitle.textContent = t("clean.all.title");
  modalBody.textContent = t("clean.all.body", { n: usable.length, size: human(total) });
  modalOk.textContent = t("clean.confirm");
  showModal();
}

// ---- 全盘扫描 ----

function renderFsRows(items: BigItem[], live: boolean, foot = "") {
  if (items.length === 0) {
    fsList.innerHTML = emptyState(live ? t("fs.live") : t("fs.empty")) + foot;
    return;
  }
  const sorted = [...items].sort((a, b) => b.size - a.size).slice(0, 30);
  const maxSize = sorted[0]?.size || 1;
  fsList.innerHTML =
    sorted
      .map(
        (it, i) => `
      <div class="fs-row card">
        <div class="fs-rank">${i + 1}</div>
        <div class="fs-main">
          <div class="fs-path" title="${esc(it.path)}">${esc(displayPath(it.path))}</div>
          <div class="fs-sub">
            <div class="fs-track"><div class="fs-fill" style="width:${Math.max(2, Math.round((it.size / maxSize) * 100))}%"></div></div>
          </div>
        </div>
        <div class="fs-size">${human(it.size)}</div>
        ${it.trashable ? `<button class="btn small danger fs-trash" data-path="${esc(it.path)}" data-size="${it.size}">${esc(t("fs.trash"))}</button>` : ""}
      </div>`,
      )
      .join("") +
    (live ? `<div class="fs-foot muted small">${esc(t("fs.live"))}</div>` : "") +
    foot;
  fsList.querySelectorAll<HTMLButtonElement>(".fs-trash").forEach((b) => {
    b.addEventListener("click", () => askTrash(b.dataset.path!, Number(b.dataset.size ?? 0), startFullScan));
  });
}

async function startFullScan() {
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

function renderFullScan(r: FullScanResult) {
  const usedPct = r.disk.total ? Math.min(100, Math.round((r.disk.used / r.disk.total) * 100)) : 0;
  fsUsed.textContent = human(r.disk.used);
  fsFree.textContent = human(r.disk.free);
  fsTotal.textContent = human(r.disk.total);
  fsBar.style.width = `${usedPct}%`;
  fsCount.textContent = "";
  const foot = `<div class="fs-foot muted small">${esc(t("fs.foot", { n: r.scanned.toLocaleString(), t: (r.elapsedMs / 1000).toFixed(1), s: r.skipped.toLocaleString() }))}</div>`;
  renderFsRows(r.items, false, foot);
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
    return;
  }
  const sorted = [...items].sort((a, b) => b.size - a.size).slice(0, 50);
  const maxSize = sorted[0]?.size || 1;
  lgList.innerHTML =
    sorted
      .map(
        (it, i) => `
      <div class="fs-row card">
        <div class="fs-rank">${i + 1}</div>
        <div class="fs-main">
          <div class="fs-path" title="${esc(it.path)}">${esc(displayPath(it.path))}</div>
          <div class="fs-sub">
            <div class="fs-track"><div class="fs-fill" style="width:${Math.max(2, Math.round((it.size / maxSize) * 100))}%"></div></div>
          </div>
        </div>
        <div class="fs-size">${human(it.size)}</div>
        ${it.trashable ? `<button class="btn small danger fs-trash" data-path="${esc(it.path)}" data-size="${it.size}">${esc(t("fs.trash"))}</button>` : ""}
      </div>`,
      )
      .join("") +
    (live ? `<div class="fs-foot muted small">${esc(t("large.live"))}</div>` : "") +
    foot;
  lgList.querySelectorAll<HTMLButtonElement>(".fs-trash").forEach((b) => {
    b.addEventListener("click", () => askTrash(b.dataset.path!, Number(b.dataset.size ?? 0), startLargeScan));
  });
}

function renderLargeDone(r: LargeScanResult) {
  const foot = `<div class="fs-foot muted small">${esc(t("large.foot", { n: r.scanned.toLocaleString(), t: (r.elapsedMs / 1000).toFixed(1), s: r.skipped.toLocaleString() }))}</div>`;
  renderLargeRows(r.items, false, foot);
}

// ---- 残留清理 ----

async function startOrphanScan() {
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
  orList.innerHTML = sorted
    .map(
      (o, i) => `
      <div class="fs-row card">
        <div class="fs-rank">${i + 1}</div>
        <div class="fs-main">
          <div class="fs-path" title="${esc(o.path)}">${esc(displayPath(o.path))} <span class="tag dim">${esc(kindLabel(o.kind))}</span></div>
          <div class="fs-sub">
            <div class="fs-track"><div class="fs-fill" style="width:${Math.max(2, Math.round((o.size / maxSize) * 100))}%"></div></div>
          </div>
        </div>
        <div class="fs-size">${human(o.size)}</div>
        <button class="btn small danger fs-trash" data-path="${esc(o.path)}" data-size="${o.size}">${esc(t("fs.trash"))}</button>
      </div>`,
    )
    .join("");
  orList.querySelectorAll<HTMLButtonElement>(".fs-trash").forEach((b) => {
    b.addEventListener("click", () => askTrash(b.dataset.path!, Number(b.dataset.size ?? 0), startOrphanScan));
  });
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
