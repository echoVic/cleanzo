// 磁盘不足时「可以放心清」的那一份：纯逻辑，可单测。
import type { SafeSpaceRow } from "../../mygo";
import { ruleName } from "../../i18n";
import type { DictKey } from "../../ui/i18n-react";

/** 可放心清的不到这么多，就不单独给按钮：腾不出多少空间。 */
export const SAFE_MIN = 500 * 2 ** 20;

/** 量过一次以后这么久以内再进页面不重新量。 */
export const SAFE_FRESH_MS = 10 * 60_000;

/** 清完以后结果在提示条上留这么久；磁盘还是不足的话这一条本来就在。 */
export const SAFE_RESULT_MS = 8000;

type T = (key: DictKey, vars?: Record<string, string | number>) => string;

/** 行的名字：规则行按当前语言取名，应用缓存用后端给的名字。 */
export function safeRowName(row: SafeSpaceRow): string {
  return row.name || ruleName(row.id);
}

/** 清掉以后要联网才会回来的那一份有多大。 */
export function safeDownloadBytes(rows: readonly SafeSpaceRow[]): number {
  return rows.filter((row) => row.recovery === "download").reduce((n, row) => n + row.size, 0);
}

/** 提示里点名最大的几行；还有别的就补上一共几类。 */
export function safeNames(rows: readonly SafeSpaceRow[], t: T, max = 3): string {
  const names = rows.slice(0, max).map(safeRowName).join(t("list.sep"));
  return rows.length > max ? t("status.safe.more", { names, n: rows.length }) : names;
}
