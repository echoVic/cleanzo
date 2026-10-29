// 进程页的纯逻辑：排序、冻结、诊断、开不开采样、增长小标签、「强制退出」的对账。不依赖 DOM / React，可单测。
import type { GrowthSummary, ProcessChild, ProcessRow, ProcessSample } from "../../mygo";
import { GiB, MiB } from "../status/format";

/** 三个标签：CPU、内存是实时的进程表，磁盘占用是占用增长的列表。 */
export type ProcessTab = "cpu" | "mem" | "disk";

/** main.ts 的打开请求：tab 为 null 时按默认（CPU）；focusKey 是要定位的行（磁盘占用标签里的 App，或 CPU、内存标签里的一行）；nonce 变了才执行。 */
export interface ProcessRequest {
  tab: ProcessTab | null;
  focusKey: string | null;
  nonce: number;
}

/** 诊断条的 CPU 门槛：整机连续 3 次统计都在 80% 以上（spec §三）。 */
export const PROCESS_DIAG = { cpuBusy: 80, cpuSamples: 3 } as const;

/** CPU 按显示的整数百分比比（fmtPct）：显示一样的行按名字排，不会每次采样都换位置。 */
function shownCpu(v: number): number {
  return Math.max(0, Math.round(v));
}

/** 内存按显示的精度比（fmtMem）：1 GB 以上到 0.1 GB，以下到 1 MB。 */
function shownMem(n: number): number {
  return n >= GiB ? (Math.round((n / GiB) * 10) / 10) * GiB : Math.round(n / MiB) * MiB;
}

/** 按 CPU 或内存从高到低，比的是页面上显示的数，同值按名字；返回新数组，不改采样。 */
export function sortRows(rows: readonly ProcessRow[], by: "cpu" | "mem"): ProcessRow[] {
  return [...rows].sort((a, b) => (by === "cpu" ? shownCpu(b.cpu) - shownCpu(a.cpu) : shownMem(b.memory) - shownMem(a.memory)) || a.name.localeCompare(b.name));
}

/** 展开后的进程：和表一样按当前标签、按显示的数排。 */
export function sortChildren(children: readonly ProcessChild[], by: "cpu" | "mem"): ProcessChild[] {
  return [...children].sort((a, b) => (by === "cpu" ? shownCpu(b.cpu) - shownCpu(a.cpu) : shownMem(b.memory) - shownMem(a.memory)) || a.name.localeCompare(b.name));
}

/** 冻结期间（确认框开着、正在退出）行和顺序不变，按 key 换上新数字；这一轮没有的行停在上一轮。 */
export function refreshFrozen(frozen: readonly ProcessRow[], sample: ProcessSample): ProcessRow[] {
  const byKey = new Map(sample.rows.map((r) => [r.key, r]));
  return frozen.map((r) => byKey.get(r.key) ?? r);
}

/** 追加一次整机 CPU，只留最近 3 次。 */
export function pushCpu(hist: readonly number[], v: number): number[] {
  return [...hist, v].slice(-PROCESS_DIAG.cpuSamples);
}

/** 顶上的诊断条：卡在哪，点名占得最多的那一行（没有行时为 null）。 */
export interface Diagnosis {
  kind: "mem" | "cpu";
  top: ProcessRow | null;
}

/** 内存压力到警告（2）或严重（4）时说内存；否则整机 CPU 连续 3 次都在 80% 以上时说 CPU；两个都有只说内存。 */
export function diagnose(sample: ProcessSample | null, cpuHist: readonly number[]): Diagnosis | null {
  if (!sample) return null;
  if (sample.pressure === 2 || sample.pressure === 4) return { kind: "mem", top: sortRows(sample.rows, "mem")[0] ?? null };
  if (cpuHist.length >= PROCESS_DIAG.cpuSamples && cpuHist.slice(-PROCESS_DIAG.cpuSamples).every((v) => v >= PROCESS_DIAG.cpuBusy)) {
    return { kind: "cpu", top: sortRows(sample.rows, "cpu")[0] ?? null };
  }
  return null;
}

/** 开采样通道的条件（spec §三）：页面显示、在 CPU 或内存标签、没暂停、窗口没隐藏。 */
export function shouldWatch(o: { active: boolean; tab: ProcessTab; running: boolean; hidden: boolean }): boolean {
  return o.active && o.tab !== "disk" && o.running && !o.hidden;
}

/** 估算值前面带「≈ 」；读不到的「—」不带。 */
export function est(text: string, estimated: boolean): string {
  return estimated && text !== "—" ? `≈ ${text}` : text;
}

/** 一个 owner 的增长提醒，取最大的一条。 */
export interface GrowthTag {
  key: string;
  window: "day" | "week";
  bytes: number;
  atLeast: boolean;
}

/**
 * 近 7 天涨过提醒线的 owner：没被忽略的提醒按 key 取最大的一条；不支持或监控关着时没有。
 * 磁盘占用标签上的数字就是它的个数。口径和状态页的增长提示一样，只是那里另外去掉了崩溃转储提示已经提过的 Crashpad。
 */
export function growthTags(s: GrowthSummary | null): Map<string, GrowthTag> {
  const out = new Map<string, GrowthTag>();
  if (!s || !s.supported || !s.config.enabled) return out;
  const ignored = new Set(s.config.ignored);
  for (const a of s.alerts) {
    if (ignored.has(a.key)) continue;
    const prev = out.get(a.key);
    if (!prev || a.bytes > prev.bytes) {
      out.set(a.key, { key: a.key, window: a.window === "day" ? "day" : "week", bytes: a.bytes, atLeast: a.atLeast });
    }
  }
  return out;
}

/** 进程行上的增长小标签：只给 App 行，按 bundle ID 优先对上 app:<bundle id>，再查 macos:<bundle id>。 */
export function rowTag(row: ProcessRow, tags: ReadonlyMap<string, GrowthTag>): GrowthTag | null {
  return row.kind === "app" && row.bundleId ? tags.get(`app:${row.bundleId}`) ?? tags.get(`macos:${row.bundleId}`) ?? null : null;
}

/** 「强制退出」只留给还在跑的 App：这一轮已经没有这个 .app 的行就去掉；没变时返回原集合。 */
export function pruneStuck(stuck: ReadonlySet<string>, rows: readonly ProcessRow[]): ReadonlySet<string> {
  if (stuck.size === 0) return stuck;
  const present = new Set(rows.map((r) => r.path));
  const next = new Set([...stuck].filter((p) => present.has(p)));
  return next.size === stuck.size ? stuck : next;
}
