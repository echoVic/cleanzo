import { t, currentLang } from "./i18n";

const $ = <T extends HTMLElement>(s: string) => document.querySelector<T>(s)!;

const overlay = $("#overlay");
const modalTitle = $("#modal-title");
const modalBody = $("#modal-body");
const modalOk = $<HTMLButtonElement>("#modal-ok");
const modalCancel = $<HTMLButtonElement>("#modal-cancel");
const navItems = Array.from(document.querySelectorAll<HTMLButtonElement>(".nav-item"));
let pending: (() => Promise<void>) | null = null;

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
        "folder-empty": "settings.folderEmpty",
        "folder-protected": "settings.folderProtected",
        "folder-dup": "settings.folderDup",
        "folder-missing": "settings.folderMissing",
        "group-unknown": "settings.groupUnknown",
      };
      const entry = key[parsed.code];
      if (entry) {
        const args = parsed.args ?? [];
        const vars: Record<string, string> = parsed.code === "dup-path"
          ? { path: String(args[0] ?? ""), owner: String(args[1] ?? ""), name: String(args[2] ?? "") }
          : { name: String(args[0] ?? ""), path: String(args[0] ?? ""), group: String(args[0] ?? "") };
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

// 主目录缩写。
const displayPath = (p: string) => p.replace(/^\/Users\/[^/]+/, "~");

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
  scan: "clean", "fs-scan": "fullscan", "lg-scan": "large", "or-scan": "apps", "ph-scan": "photos",
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

const VIEWS = ["clean", "fullscan", "large", "photos", "apps", "settings"] as const;
type View = (typeof VIEWS)[number];

const baseName = (p: string) => p.split("/").filter(Boolean).pop() ?? p;

/** 每次打开或关闭确认框都换一个编号；晚到的异步结果凭它判断这个框是否还是自己的 */
let modalSeq = 0;
function showModal() {
  beforeModalShow?.();
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

/** 确认框打开前，关闭其他预览。 */
let beforeModalShow: (() => void) | null = null;
export function setBeforeModalShow(fn: () => void) { beforeModalShow = fn; }

export function currentModalSeq() { return modalSeq; }
export function setPending(fn: (() => Promise<void>) | null) { pending = fn; }

modalOk.addEventListener("click", async () => {
  const fn = pending;
  hideModal();
  if (fn) await fn();
});
modalCancel.addEventListener("click", hideModal);
overlay.addEventListener("click", (e) => {
  if (e.target === overlay) hideModal();
});

export { $, msg, errText, human, esc, fmtDate, displayPath, baseName, toast, emptyState, errorState, skeletonRows, setBusy, setStoppable, stopScan, endScan, stoppedScans, scanChans, markRescan, setNavBusy, VIEWS, overlay, modalTitle, modalBody, modalOk, modalCancel, showModal, hideModal };
export type { View };

