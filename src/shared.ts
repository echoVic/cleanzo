import { shortHome, baseName } from "./platform";
import { t, currentLang } from "./i18n";
import { play, dur } from "./ui/motion/motion";

const $ = <T extends HTMLElement>(s: string) => document.querySelector<T>(s)!;

const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

/** 将后端错误码翻成当前语言，普通错误保留原文。 */
function errText(e: unknown): string {
  const raw = msg(e);
  const plain: Record<string, string> = {
    "in-use": "fs.inUse",
    "usage-unknown": "fs.usageUnknown",
    blocked: "fs.blocked",
    "not-recycled": "fs.notRecycled",
    "connected-files": "fs.connectedFiles",
    gone: "fs.gone",
    unsupported: "fs.unsupported",
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
        "growth-login": "settings.growth.loginFail",
        "growth-threshold": "settings.growth.thresholdBad",
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

const fmtDate = (sec: number) =>
  new Date(sec * 1000).toLocaleDateString(currentLang() === "zh" ? "zh-CN" : "en-US", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  });

// 主目录缩写。
const displayPath = shortHome;

let toastTimer: number | undefined;
function toast(text: string, kind: "ok" | "err" = "ok") {
  let el = $<HTMLDivElement>("#toast");
  if (!el) {
    el = document.createElement("div");
    el.id = "toast";
    // 普通提示礼貌播报；错误用 alert 立即播报
    el.setAttribute("role", "status");
    el.setAttribute("aria-live", "polite");
    document.body.appendChild(el);
  }
  if (kind === "err") {
    el.setAttribute("role", "alert");
    el.setAttribute("aria-live", "assertive");
  } else {
    el.setAttribute("role", "status");
    el.setAttribute("aria-live", "polite");
  }
  const showing = typeof el.className === "string" && el.className.split(" ").includes("show");
  if (typeof el.replaceChildren === "function") {
    const span = document.createElement("span");
    span.textContent = text;
    el.replaceChildren(span);
    // 已在显示：只换文字并淡入，不重新弹出（设计第五节）。
    if (showing) void play(span, [{ opacity: 0 }, { opacity: 1 }], dur("fade"), "linear");
  } else {
    el.textContent = text;
  }
  el.className = `toast show ${kind}`;
  clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => el.classList.remove("show"), 3200);
}

const FOCUSABLE_AFTER_ROW =
  'button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])';

/**
 * 焦点所在行动态移除（如退出应用后整行消失）后，把焦点交给该行下方最近的可聚焦元素；
 * 下方没有就回到列表第一个，避免焦点掉到 body 或工具栏。
 */
function focusNextAfter(rowEl: Element | null) {
  if (!rowEl) return;
  const main = document.querySelector("main");
  if (!main) return;
  const list = Array.from(main.querySelectorAll<HTMLElement>(FOCUSABLE_AFTER_ROW)).filter(
    (el) => el.getClientRects().length > 0 && !el.closest("[inert]"),
  );
  if (list.length === 0) return;
  const top = rowEl.getBoundingClientRect().top;
  const next = list.find((el) => !rowEl.contains(el) && el.getBoundingClientRect().top >= top);
  (next ?? list[0])?.focus({ preventScroll: true });
}

const navItems = Array.from(document.querySelectorAll<HTMLButtonElement>(".nav-item"));

function setNavBusy(view: View, on: boolean) {
  navItems.find((b) => b.dataset.view === view)?.classList.toggle("busy", on);
}

const VIEWS = ["clean", "status", "process", "aitools", "aispace", "skills", "fullscan", "large", "photos", "apps", "settings"] as const;
type View = (typeof VIEWS)[number];

export { $, errText, human, fmtDate, displayPath, baseName, toast, setNavBusy, focusNextAfter, VIEWS };
export type { View };
