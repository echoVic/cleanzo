import { shortHome } from "../../platform";
// 大文件页纯展示 / 过滤逻辑：不依赖 DOM / React，可单测。
import type { LargeFile } from "../../mygo";

/** 字节展示就是 shared.ts 那一份。 */
export { human } from "../../shared";

export function displayPath(p: string): string {
  return shortHome(p, true);
}

/** 阈值四档：100MB / 500MB / 1GB / 5GB。 */
export const LARGE_THRESHOLDS = [104857600, 524288000, 1073741824, 5368709120] as const;

const STORAGE_KEY = "cleanzo.largeSize";

export function readThreshold(fallback = 1073741824): number {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    const n = v === null ? NaN : Number(v);
    if ((LARGE_THRESHOLDS as readonly number[]).includes(n)) return n;
  } catch { /* 忽略 */ }
  return fallback;
}

export function saveThreshold(n: number): void {
  try { localStorage.setItem(STORAGE_KEY, String(n)); } catch { /* 忽略 */ }
}

/** 阈值过滤：只保留 >= 下限、未被删除的项。 */
export function visibleAfterThreshold(items: readonly LargeFile[], threshold: number): LargeFile[] {
  return items.filter((it) => it.size >= threshold);
}

/** 按大小降序取前 n（旧页 top 50）。 */
export function topBySize(items: readonly LargeFile[], n = 50): LargeFile[] {
  return [...items].sort((a, b) => b.size - a.size).slice(0, n);
}

/** 最大项条形图宽度：最小 2%。 */
export function barWidth(size: number, maxSize: number): number {
  return Math.max(2, Math.round((size / (maxSize || 1)) * 100));
}
