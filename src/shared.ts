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
    document.body.appendChild(el);
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

const navItems = Array.from(document.querySelectorAll<HTMLButtonElement>(".nav-item"));

function setNavBusy(view: View, on: boolean) {
  navItems.find((b) => b.dataset.view === view)?.classList.toggle("busy", on);
}

const VIEWS = ["clean", "status", "aitools", "skills", "fullscan", "large", "photos", "apps", "settings"] as const;
type View = (typeof VIEWS)[number];

export { $, errText, human, fmtDate, displayPath, baseName, toast, setNavBusy, VIEWS };
export type { View };
