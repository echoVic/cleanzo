// 清理记录：哪一行什么时候清过、清过以后是不是又长回来了。纯计算，可单测。
import type { CleanRecord, JunkItem } from "../../mygo";
import { currentLang } from "../../i18n";
import { fmtDate } from "../../shared";
import { discoveredSize, type TFn } from "./helpers";

/**
 * 「又长回来了」：这么多天以内清过，现在又有上次清掉的这么大一份，而且不少于这个量。
 * 缓存本来就会慢慢长回来，所以只标很快就差不多长满的：清它腾不出多久的空间。
 */
export const BACK_DAYS = 14;
export const BACK_SHARE = 0.8;
export const BACK_MIN = 200 * 1024 ** 2;

/** 记录顶上的合计看最近这么多天。 */
export const TOTAL_DAYS = 30;

/** 弹窗里最多列这么多次清理。 */
export const HISTORY_SHOWN = 60;

/** 本地日历上隔了几天：昨天深夜清的，今天凌晨看是 1。记录比现在还晚（时钟调过）算 0。 */
export function daysSince(at: number, now: number): number {
  const day = (sec: number) => {
    const d = new Date(sec * 1000);
    return Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()) / 86400000;
  };
  return Math.max(0, Math.round(day(now) - day(at)));
}

/** 今天 / 昨天 / N 天前。 */
export function whenText(at: number, now: number, t: TFn): string {
  const days = daysSince(at, now);
  if (days === 0) return t("history.when.today");
  return days === 1 ? t("history.when.yesterday") : t("history.when.days", { n: days });
}

/** 记录里一次清理的日期和时刻。 */
export function fmtWhen(at: number): string {
  const time = new Date(at * 1000).toLocaleTimeString(currentLang() === "zh" ? "zh-CN" : "en-US", { hour: "2-digit", minute: "2-digit", hour12: false });
  return `${fmtDate(at)} ${time}`;
}

/** 这一行上一次清掉了多少（删除的和进废纸篓的一起算）、在什么时候；没清过为 null。history 从新到旧。 */
export function lastCleaned(history: readonly CleanRecord[], id: string): { at: number; bytes: number } | null {
  for (const rec of history) {
    const row = rec.rows.find((r) => r.id === id);
    if (row && row.freed + row.trashed > 0) return { at: rec.at, bytes: row.freed + row.trashed };
  }
  return null;
}

/** 清过以后又长回来了没有。废纸篓不算：那是用户自己放进去的。量到了、只是现在不能清的也算。 */
export function cameBack(last: { at: number; bytes: number } | null, it: JunkItem, now: number): boolean {
  if (!last || it.id === "sys.trash") return false;
  return daysSince(last.at, now) <= BACK_DAYS && discoveredSize(it) >= Math.max(BACK_MIN, last.bytes * BACK_SHARE);
}

/** 最近 TOTAL_DAYS 天清理了几次、删掉多少、多少进了废纸篓。 */
export function historyTotals(history: readonly CleanRecord[], now: number): { count: number; freed: number; trashed: number } {
  const recent = history.filter((rec) => daysSince(rec.at, now) <= TOTAL_DAYS);
  return {
    count: recent.length,
    freed: recent.reduce((n, rec) => n + rec.freed, 0),
    trashed: recent.reduce((n, rec) => n + rec.trashed, 0),
  };
}

export interface Regrown {
  item: JunkItem;
  /** 上次清理的时刻和清掉的量。 */
  at: number;
  cleaned: number;
  /** 现在又有多少。 */
  size: number;
}

/** 这次扫描里清过又长回来的行，现在占得多的在前。 */
export function regrown(history: readonly CleanRecord[], items: readonly JunkItem[], now: number): Regrown[] {
  return items.flatMap((item) => {
    const last = lastCleaned(history, item.id);
    return last && cameBack(last, item, now) ? [{ item, at: last.at, cleaned: last.bytes, size: discoveredSize(item) }] : [];
  }).sort((a, b) => b.size - a.size);
}
