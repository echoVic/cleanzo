import { shortHome } from "../../platform";
// 全盘扫描页纯展示 / 过滤逻辑：不依赖 DOM / React，可单测。
import type { BigItem, DiskStat } from "../../mygo";

/** 字节展示就是 shared.ts 那一份。 */
export { human } from "../../shared";

export function displayPath(p: string): string {
  return shortHome(p, true);
}

/** 旧页固定取占用最大的前 30 个目录。 */
export const FULLSCAN_TOP_N = 30;

/** 按大小降序取前 n（旧页 top 30）。 */
export function topBySize(items: readonly BigItem[], n: number = FULLSCAN_TOP_N): BigItem[] {
  return [...items].sort((a, b) => b.size - a.size).slice(0, n);
}

/** 最大项条形图宽度：最小 2%，相对当前最大项。 */
export function barWidth(size: number, maxSize: number): number {
  return Math.max(2, Math.round((size / (maxSize || 1)) * 100));
}

/** 磁盘已用百分比：0–100 取整；总量未知返回 0。 */
export function diskPercent(disk: DiskStat): number {
  if (!disk.total) return 0;
  return Math.min(100, Math.round((disk.used / disk.total) * 100));
}
