// 应用卸载页纯逻辑：不依赖 DOM / React，可单测。等价旧 main.ts 的
// loadApps/renderApps/showUninstallDetail/startOrphanScan/renderOrphanRows 里
// 的过滤、排序、卸载结果分类与残留汇总。
import type { AppInfo, OrphanItem, UninstallResult } from "../../mygo";

export type AppSortBy = "name" | "size" | "opened";

const APP_SORT_KEY = "cleanzo.appSort";

export function readAppSort(fallback: AppSortBy = "name"): AppSortBy {
  try {
    const v = localStorage.getItem(APP_SORT_KEY);
    if (v === "name" || v === "size" || v === "opened") return v;
  } catch { /* 忽略 */ }
  return fallback;
}

export function saveAppSort(v: AppSortBy): void {
  try { localStorage.setItem(APP_SORT_KEY, v); } catch { /* 忽略 */ }
}

/** 搜索：名称或路径包含（不区分大小写）。q 为空时不过滤。 */
export function filterApps(apps: readonly AppInfo[], qRaw: string): AppInfo[] {
  const q = qRaw.trim().toLowerCase();
  if (!q) return [...apps];
  return apps.filter(
    (a) => a.name.toLowerCase().includes(q) || a.path.toLowerCase().includes(q),
  );
}

const byNameCompare = (a: AppInfo, b: AppInfo, lang: string) =>
  a.name.localeCompare(b.name, lang === "zh" ? "zh-CN" : "en");

/** 三种排序：name 名称（localeCompare）、size 大小降序、opened 上次打开降序；大小/打开相同按名称。 */
export function sortApps(apps: readonly AppInfo[], by: AppSortBy, lang: string): AppInfo[] {
  return [...apps].sort((a, b) => {
    if (by === "size") return b.size - a.size || byNameCompare(a, b, lang);
    if (by === "opened") return b.lastOpened - a.lastOpened || byNameCompare(a, b, lang);
    return byNameCompare(a, b, lang);
  });
}

/**
 * 卸载结果分类（决定 toast 文案）：
 * - inUse：moved===0 且所有失败都是 ": in-use" 后缀
 * - partial：有失败项（但不是上面那种纯 in-use）
 * - ok：全部成功移动
 */
export type UninstallOutcome = "inUse" | "partial" | "ok";

export function classifyUninstall(res: UninstallResult): UninstallOutcome {
  if (res.moved === 0 && res.failed.length > 0 && res.failed.every((f) => f.endsWith(": in-use"))) {
    return "inUse";
  }
  if (res.failed.length > 0) return "partial";
  return "ok";
}

/** bundle 本身删除失败：失败列表里就是 bundle 路径或以「路径:」开头。此时不从列表移除。 */
export function bundleFailed(res: UninstallResult): boolean {
  return res.failed.some((f) => f === res.path || f.startsWith(`${res.path}:`));
}

/** 残留项按大小降序。 */
export function sortOrphans(items: readonly OrphanItem[]): OrphanItem[] {
  return [...items].sort((a, b) => b.size - a.size);
}

/** 最大项条形图宽度：最小 2%。 */
export function barWidth(size: number, maxSize: number): number {
  return Math.max(2, Math.round((size / (maxSize || 1)) * 100));
}

/**
 * 解析 Channel 流末尾的 __done 项：
 * - size 字段携带本次扫描的总大小
 * - path 字段是字符串化的用时 ms
 */
export function parseDoneEv(o: OrphanItem): { totalSize: number; elapsedMs: number } {
  return { totalSize: o.size ?? 0, elapsedMs: Number(o.path ?? 0) || 0 };
}

/**
 * 删除后重算汇总：n = 剩余项数，size = 剩余项大小之和，t 沿用扫描用时秒。
 * 扫描刚结束（未删除）时 size 直接用后端 __done 报告的 totalSize。
 */
export function orphanFootAfterRemoval(
  items: readonly OrphanItem[],
  elapsedSec: number,
): { n: number; size: number; t: number } {
  const size = items.reduce((sum, it) => sum + (it.size >= 0 ? it.size : 0), 0);
  return { n: items.length, size, t: elapsedSec };
}
