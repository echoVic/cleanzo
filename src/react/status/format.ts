// 系统状态页纯展示 / 选择逻辑：不依赖 DOM / React，可单测。
import type { AppUsage, StatusSample } from "../../mygo";

export const GiB = 2 ** 30;
export const MiB = 2 ** 20;
/** CPU 曲线保留的点数：2s × 30 = 最近 1 分钟 */
export const SPARK_POINTS = 30;

export const STATUS_ALERT = {
  /** kern.memorystatus_vm_pressure_level：1 正常 / 2 紧张 / 4 严重，未知当正常 */
  pressure: { 1: "ok", 2: "warn", 4: "crit" } as Record<number, string>,
  /** 内存提示条里追加交换文件那行的下限 */
  swapMentionGB: 1,
  /** 压力正常时独立提示交换文件的下限 */
  swapStandaloneGB: 8,
  /** 磁盘可用空间告警下限 */
  diskFreeWarnGB: 20,
} as const;

// ---- 数字格式（设计稿 §6/§13：内存与磁盘整 GiB 不带 .0，如 22 GB / 32 GB / 91 GB） ----

/** <1 GiB → MB 取整；≥1 GiB → GB 一位小数（整 GiB 去掉 .0，如 22 GB）；0 = 读不到 → “—” */
export function fmtMem(n: number): string {
  if (!n) return "—";
  if (n >= GiB) {
    const g = (n / GiB).toFixed(1).replace(/\.0$/, "");
    return `${g} GB`;
  }
  return `${Math.round(n / MiB)} MB`;
}

export function fmtPct(v: number): string {
  return `${Math.max(0, Math.round(v))}%`;
}

/** fmtMem 的输出拆成 hero 大数字 / 小单位两段（如 "27.2 GB" → "27.2" + "GB"） */
export function splitMem(n: number): [string, string] {
  const s = fmtMem(n);
  const i = s.indexOf(" ");
  return i > 0 ? [s.slice(0, i), s.slice(i + 1)] : [s, ""];
}

/** 开机时长分解：<60s 返回 null（不显示）；文案由组件按语言拼。 */
export function uptimeParts(bootSec: number, nowSec: number): { days: number; hours: number; mins: number } | null {
  const secs = Math.max(0, Math.floor(nowSec - bootSec));
  if (secs < 60) return null;
  return {
    days: Math.floor(secs / 86400),
    hours: Math.floor(secs / 3600) % 24,
    mins: Math.floor(secs / 60) % 60,
  };
}

// ---- 应用列表排序与冻结（设计稿 §10） ----

export type SortBy = "mem" | "cpu";

/** 按内存 / CPU 降序（返回新数组，不改原采样）。 */
export function sortApps(apps: readonly AppUsage[], by: SortBy): AppUsage[] {
  return [...apps].sort(by === "mem" ? (a, b) => b.memory - a.memory : (a, b) => b.cpu - a.cpu);
}

/**
 * 冻结期行集合与顺序不变：按最新采样同 key 更新数字，
 * 本轮采样里已经消失的应用保留冻结行（行还在，数字停在上一轮）。
 */
export function refreshFrozenRows(frozen: readonly AppUsage[], sample: StatusSample): AppUsage[] {
  return frozen.map((r) => sample.apps.find((a) => a.key === r.key) ?? r);
}

// ---- 提示条选择器（设计稿 §9） ----

export type AlertKind = "memWarn" | "memCrit" | "swapOnly" | "diskLow";

export interface PickedAlert {
  kind: AlertKind;
  /** 交换文件占磁盘：memWarn/memCrit 的副行 / swapOnly 正文 */
  swapDisk?: number;
  /** 磁盘可用：diskLow 正文 */
  free?: number;
  /** apps = 页内切内存排序+滚动高亮；clean = 切垃圾页；无 = 无按钮 */
  action?: "apps" | "clean";
}

/**
 * 压力 warn/crit 置顶（swap≥1GB 附 swapLine）；压力正常且 swap≥8GB 独立一条；
 * 磁盘可用<20GB 一条；前三条互斥取其一，内存条固定在上；同屏最多 2 条。
 */
export function pickAlerts(s: StatusSample): PickedAlert[] {
  const items: PickedAlert[] = [];
  const p = s.memory.pressure;
  // 占磁盘用 swapTotal：macOS 交换文件占用 / Windows 页面文件分配量。
  const swapDisk = s.memory.swapTotal;
  const swapBig = swapDisk >= STATUS_ALERT.swapMentionGB * GiB;
  if (p === 2 || p === 4) {
    items.push({
      kind: p === 4 ? "memCrit" : "memWarn",
      swapDisk: swapBig ? swapDisk : undefined,
      action: "apps",
    });
  } else if (swapDisk >= STATUS_ALERT.swapStandaloneGB * GiB) {
    items.push({ kind: "swapOnly", swapDisk });
  }
  if (s.disk.free > 0 && s.disk.free < STATUS_ALERT.diskFreeWarnGB * GiB) {
    items.push({ kind: "diskLow", free: s.disk.free, action: "clean" });
  }
  return items.slice(0, 2);
}

// ---- CPU 曲线缓冲 ----

/** 追加一点，超过 30 点丢最旧（重开采样时调用方清空为 []）。 */
export function trimSpark(hist: readonly number[], v: number): number[] {
  const next = [...hist, v];
  if (next.length > SPARK_POINTS) next.shift();
  return next;
}

/** 新采样是否只是把曲线左移一格（两段都满 30 点，新的前 29 点等于旧的后 29 点）。 */
export function isSparkShift(prev: readonly number[], next: readonly number[]): boolean {
  return prev.length === SPARK_POINTS && next.length === SPARK_POINTS && prev !== next && prev.slice(1).every((v, i) => v === next[i]);
}

// ---- 退出应用（设计稿 §11） ----

/** QuitApp 的结构化错误：{"code":"not-running"} / {"code":"cannot-quit"} */
export function quitErrCode(e: unknown): string | null {
  const raw = e instanceof Error ? e.message : String(e);
  try {
    const parsed = JSON.parse(raw) as { code?: string };
    if (parsed && typeof parsed.code === "string") return parsed.code;
  } catch { /* 非 JSON 按原文报 */ }
  return null;
}

/** 首字母圆标的底色：按应用名哈希取自品牌色板（M8：不只靠颜色传义） */
const PALETTE = ["#0c9d66", "#3b82f6", "#8b5cf6", "#f59e0b", "#ef4444", "#14b8a6", "#6366f1", "#ec4899"];
export function iconColor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return PALETTE[h % PALETTE.length]!;
}

/** 退出状态机：busy 正在退出中的 .app 路径；done 已退出行 key（置灰、去按钮）；gone 已从采样消失过的 key。 */
export interface QuitState {
  busy: ReadonlySet<string>;
  done: ReadonlySet<string>;
  gone: ReadonlySet<string>;
}

export function emptyQuit(): QuitState {
  return { busy: new Set(), done: new Set(), gone: new Set() };
}

/**
 * 每轮采样后对账（QA P2-1）：已退出行在采样里消失 → 记 gone；
 * 同 key 再现（用户重启了该应用，新实例）→ 从 done/gone 移除，恢复行态与退出按钮。
 */
export function syncQuitRows(st: QuitState, presentKeys: ReadonlySet<string>): QuitState {
  if (st.done.size === 0) return st;
  let changed = false;
  const done = new Set(st.done);
  const gone = new Set(st.gone);
  for (const k of done) {
    if (!presentKeys.has(k)) {
      gone.add(k);
      changed = true;
    } else if (gone.has(k)) {
      done.delete(k);
      gone.delete(k);
      changed = true;
    }
  }
  if (!changed) return st;
  return { busy: st.busy, done, gone };
}
