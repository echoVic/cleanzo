// 系统状态页纯展示 / 选择逻辑：不依赖 DOM / React，可单测。
import type { AppUsage, DisplayInfo, StatusSample } from "../../mygo";
import type { Lang } from "../../i18n";

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
      if (!gone.has(k)) {
        gone.add(k);
        changed = true;
      }
    } else if (gone.has(k)) {
      done.delete(k);
      gone.delete(k);
      changed = true;
    }
  }
  if (!changed) return st;
  return { busy: st.busy, done, gone };
}

// ---- 机器信息（设计 2026-10-28 §三） ----

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** 括号里的一段，中文界面改写尺寸和月份："16-inch" → "16 英寸"，"Nov 2023" → "2023 年 11 月"。 */
function modelPart(part: string, lang: Lang): string {
  if (lang !== "zh") return part;
  const inch = /^(\d+(?:\.\d+)?)-inch$/.exec(part);
  if (inch) return `${inch[1]} 英寸`;
  const dated = /^([A-Z][a-z]{2})[a-z]* (\d{4})$/.exec(part);
  const month = dated ? MONTHS.indexOf(dated[1]!) : -1;
  if (dated && month >= 0) return `${dated[2]} 年 ${month + 1} 月`;
  return part;
}

/** 型号标识推出的系列名，设备树读不到型号全名时用；Apple 芯片的新标识（Mac14,10）看不出系列，只能写 "Mac"。 */
const FAMILIES: ReadonlyArray<readonly [RegExp, string]> = [
  [/^MacBookPro/, "MacBook Pro"], [/^MacBookAir/, "MacBook Air"], [/^MacBook\d/, "MacBook"],
  [/^Macmini/, "Mac mini"], [/^MacPro/, "Mac Pro"], [/^iMac/, "iMac"],
];

/**
 * 页头标题（设计 §三.1）：设备树的型号全名拆成名字和括号里的说明。
 * "MacBook Pro (16-inch, 2023)" → 中文 { name: "MacBook Pro", note: "16 英寸 · 2023" }，英文 note 是 "16-inch, 2023"。
 * 没有型号全名时用型号标识：{ name: 系列名, note: "Mac14,10" }；都没有时两项都空，页头退回「系统状态」。
 */
export function modelLabel(model: string, modelId: string, lang: Lang): { name: string; note: string } {
  const full = model.trim();
  const m = /^(.+?)\s*\((.+)\)$/.exec(full);
  if (m) {
    const parts = m[2]!.split(",").map((p) => p.trim()).filter(Boolean).map((p) => modelPart(p, lang));
    return { name: m[1]!, note: parts.join(lang === "zh" ? " · " : ", ") };
  }
  if (full) return { name: full, note: "" };
  const id = modelId.trim();
  if (!id) return { name: "", note: "" };
  const family = FAMILIES.find(([re]) => re.test(id));
  return { name: family ? family[1] : "Mac", note: id };
}

/** 每秒字节 → [数, 单位]，十进制（和活动监视器一致）：<1 MB/s 用 KB/s 取整，以上一位小数；按取整后的值选单位，不会出现 "1000 KB/s"；负数或 NaN = 读不到 → ["—", ""]。 */
export function fmtRate(bps: number): [string, string] {
  if (!(bps >= 0)) return ["—", ""];
  if (bps >= 999.95e6) return [(bps / 1e9).toFixed(1), "GB/s"];
  if (bps >= 999.5e3) return [(bps / 1e6).toFixed(1), "MB/s"];
  return [String(Math.round(bps / 1e3)), "KB/s"];
}

/** fmtRate 拼成一段文字，如 "10.3 MB/s"；读不到 → "—"。 */
export function fmtRateText(bps: number): string {
  const [v, unit] = fmtRate(bps);
  return unit ? `${v} ${unit}` : v;
}

/** 摄氏度取整，如 "52 °C"；0 = 读不到 → "—"。 */
export function fmtTemp(c: number): string {
  return c > 0 ? `${Math.round(c)} °C` : "—";
}

/** 十进制容量（SSD 容量、累计写入用厂商口径）：100 以上取整，以下一位小数（去掉 .0）；0 = 读不到 → "—"。 */
export function fmtDec(n: number): string {
  if (!(n > 0)) return "—";
  const [v, unit] = n >= 1e12 ? [n / 1e12, "TB"] : [n / 1e9, "GB"];
  const s = v >= 100 ? String(Math.round(v)) : v.toFixed(1).replace(/\.0$/, "");
  return `${s} ${unit}`;
}

/** NVMe 严重警告的各位（SMART 日志第 0 字节）：0 备用块不足、1 温度超限、2 可靠性下降、3 只读、4 断电保护失效。 */
const SSD_WARNINGS = [
  "status.ssd.warn.spare", "status.ssd.warn.temp", "status.ssd.warn.reliability",
  "status.ssd.warn.readonly", "status.ssd.warn.backup",
] as const;
export type SsdWarningKey = (typeof SSD_WARNINGS)[number] | "status.ssd.warn.other";

/** 警告位 → 文案 key；只有规范之外的位时给一条笼统的「报告了警告」。 */
export function ssdWarnings(bits: number): SsdWarningKey[] {
  const out: SsdWarningKey[] = SSD_WARNINGS.filter((_, i) => (bits >> i) & 1);
  if (out.length === 0 && bits !== 0) out.push("status.ssd.warn.other");
  return out;
}

/** 一台显示器："LS27D80xU 1920×1080 @60Hz"；没有名字时用 fallback（「内建显示器」「外接显示器」）。 */
export function displayText(d: DisplayInfo, fallback: string): string {
  const name = d.name.trim() || fallback;
  if (!(d.width > 0 && d.height > 0)) return name;
  return `${name} ${d.width}×${d.height}${d.hz > 0 ? ` @${d.hz}Hz` : ""}`;
}

/** 网络曲线纵轴的下限：空闲时几 KB/s 的波动不会被放大成满格。 */
export const NET_SPARK_FLOOR = 100_000;

/** 网络曲线的纵轴上限：最近一分钟的最大值，至少 NET_SPARK_FLOOR。 */
export function netSparkMax(hist: readonly number[]): number {
  return Math.max(NET_SPARK_FLOOR, ...hist);
}
