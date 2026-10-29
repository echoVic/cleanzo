// 菜单栏面板的纯逻辑（设计 docs/superpowers/specs/2026-10-29-menubar-design.md §四）：大数字、一句话诊断、曲线、
// 明细、列表和退出的两下确认。不依赖 DOM / React，可单测。
import type {
  DisplayInfo, GrowthApp, GrowthState, MachineInfo, MachineSample, MemoryStatus, MenuBarTarget, ProcessRow, ProcessSample,
  StatusSample, SystemInfo,
} from "../mygo";
import type { DictKey } from "../ui/i18n-react";
import { human } from "../shared";
import { growthHint } from "../react/growth/hint";
import { PROCESS_DIAG, est, refreshFrozen, sortRows } from "../react/process/format";
import {
  GiB, STATUS_ALERT, displayText, fmtDec, fmtMem, fmtPct, fmtRate, fmtRateText, fmtTemp, netSparkMax, splitMem, ssdWarnings,
} from "../react/status/format";

/** 七个读数，和 Go 的 menuBarItems 同序。 */
export const PANEL_ITEMS = ["cpu", "mem", "gpu", "net", "disk", "temp", "battery"] as const;
export type PanelItem = (typeof PANEL_ITEMS)[number];

/** 翻译：useT() 给的，或测试里的 t。 */
export type T = (key: DictKey, vars?: Record<string, string | number>) => string;

/** 地址里的 ?item=cpu；不认识的读数为 null，面板什么也不画。 */
export function panelItem(search: string): PanelItem | null {
  const v = new URLSearchParams(search).get("item") ?? "";
  return (PANEL_ITEMS as readonly string[]).includes(v) ? (v as PanelItem) : null;
}

/** 底部链接：在主窗口打开哪一页。 */
export interface PanelLink {
  key: DictKey;
  target: MenuBarTarget;
}

const page = (p: string, tab = ""): MenuBarTarget => ({ page: p, tab, key: "" });
const HOME_LINK: PanelLink[] = [{ key: "menubar.panel.openHome", target: page("status") }];

/** 每个面板的名称和底部链接（设计 §四的表）。 */
export const PANEL: Record<PanelItem, { title: DictKey; links: PanelLink[] }> = {
  cpu: { title: "menubar.panel.cpu", links: [{ key: "menubar.panel.openProcess", target: page("process", "cpu") }] },
  mem: { title: "menubar.panel.mem", links: [{ key: "menubar.panel.openProcess", target: page("process", "mem") }] },
  gpu: { title: "menubar.panel.gpu", links: HOME_LINK },
  net: { title: "menubar.panel.net", links: HOME_LINK },
  disk: {
    title: "menubar.panel.disk",
    links: [
      { key: "menubar.panel.clean", target: page("clean") },
      { key: "menubar.panel.view", target: page("process", "disk") },
    ],
  },
  temp: { title: "menubar.panel.temp", links: HOME_LINK },
  battery: { title: "menubar.panel.battery", links: HOME_LINK },
};

/** 列表最多几行（设计 §四.5）。 */
export const PANEL_TOP = 5;

/** 散热状态四档（NSProcessInfo）：正常 / 偏热 / 很热 / 过热，和首页健康条同一套字。 */
const THERMAL: readonly DictKey[] = ["status.health.thermal0", "status.health.thermal1", "status.health.thermal2", "status.health.thermal3"];

/** 大数字和单位；读不到时写「—」（设计 §四）。 */
export function hero(item: PanelItem, s: StatusSample | null): { value: string; unit: string } {
  const none = { value: "—", unit: "" };
  if (!s) return none;
  const m = s.machine;
  switch (item) {
    case "cpu":
      return { value: String(Math.max(0, Math.round(s.cpu.usage))), unit: "%" };
    case "mem":
      return s.memory.total > 0 ? { value: String(Math.round((s.memory.used / s.memory.total) * 100)), unit: "%" } : none;
    case "gpu":
      return m && m.gpu >= 0 ? { value: String(Math.round(m.gpu)), unit: "%" } : none;
    case "net": {
      if (!m || !(m.netDown >= 0)) return none;
      const [value, unit] = fmtRate(m.netDown);
      return { value, unit };
    }
    case "disk": {
      if (!(s.disk.free > 0)) return none;
      const [value, unit] = splitMem(s.disk.free);
      return { value, unit };
    }
    case "temp":
      return m && m.chipTemp > 0 ? { value: String(Math.round(m.chipTemp)), unit: "°C" } : none;
    case "battery":
      return s.battery ? { value: String(s.battery.percent), unit: "%" } : none;
  }
}

/** 大数字下面那行：规格或补充，读到什么写什么；什么都没有时为空串。 */
export function subLine(item: PanelItem, s: StatusSample | null, info: MachineInfo | null, sys: SystemInfo | null, t: T): string {
  const parts: string[] = [];
  const m = s?.machine ?? null;
  switch (item) {
    case "cpu":
      if (sys?.chip) parts.push(sys.chip);
      if (info && info.perfCores > 0 && info.effCores > 0) {
        parts.push(t("status.spec.cpuSplit", { n: info.perfCores + info.effCores, p: info.perfCores, e: info.effCores }));
      } else if (sys && sys.cores > 0) {
        parts.push(t("status.spec.cpu", { n: sys.cores }));
      }
      if (s) parts.push(t("status.cpu.load", { l1: s.cpu.load1.toFixed(1), l5: s.cpu.load5.toFixed(1), l15: s.cpu.load15.toFixed(1) }));
      break;
    case "mem":
      if (s && s.memory.total > 0) parts.push(t("menubar.panel.memSub", { used: fmtMem(s.memory.used), total: fmtMem(s.memory.total) }));
      break;
    case "gpu":
      if (info && info.gpuCores > 0) parts.push(t("status.spec.gpu", { n: info.gpuCores }));
      break;
    case "net":
      if (m?.netKind === "wifi") parts.push(t("status.net.wifi"));
      else if (m?.netKind === "ethernet") parts.push(t("status.net.ethernet"));
      if (m && m.netUp >= 0) parts.push(`↑ ${fmtRateText(m.netUp)}`);
      break;
    case "disk":
      if (s && s.disk.total > 0) parts.push(t("status.disk.avail"), t("status.disk.totalOnly", { total: fmtMem(s.disk.total) }));
      break;
    case "temp":
      if (m && m.chipTemp > 0) parts.push(t("status.health.chipTemp"));
      break;
    case "battery":
      break;
  }
  return parts.join(" · ");
}

/** 一句话诊断的语气：正常、要留意、严重。 */
export type DiagTone = "ok" | "warn" | "crit";

export interface PanelDiag {
  tone: DiagTone;
  text: string;
}

/** 诊断用到的读数。 */
export interface DiagInput {
  s: StatusSample | null;
  /** CPU、内存面板的进程读数（和「进程」页同一套） */
  proc: ProcessSample | null;
  /** 进程读数的整机 CPU，最近 3 次 */
  cpuHist: readonly number[];
  /** GPU 使用率，最近 3 次 */
  gpuHist: readonly number[];
  growth: GrowthState | null;
}

/** 最近 3 次都在 80% 以上：「进程」页说 CPU 很忙的门槛，GPU 也用它。 */
function busy(hist: readonly number[]): boolean {
  return hist.length >= PROCESS_DIAG.cpuSamples && hist.slice(-PROCESS_DIAG.cpuSamples).every((v) => v >= PROCESS_DIAG.cpuBusy);
}

/** 一句话诊断（设计 §四的表，Cleanzo 的特色）：读数还没到时为 null，面板画一条占位。 */
export function diagnosis(item: PanelItem, d: DiagInput, t: T): PanelDiag | null {
  const s = d.s;
  const m = s?.machine ?? null;
  switch (item) {
    case "cpu": {
      const top = d.proc ? sortRows(d.proc.rows, "cpu")[0] : undefined;
      if (!top) return null;
      const vars = { name: top.name, pct: est(fmtPct(top.cpu), top.estimated) };
      return busy(d.cpuHist) ? { tone: "warn", text: t("process.diag.cpu", vars) } : { tone: "ok", text: t("menubar.diag.cpuIdle", vars) };
    }
    case "mem": {
      const top = d.proc ? sortRows(d.proc.rows, "mem")[0] : undefined;
      if (!s || !top) return null;
      const vars = { name: top.name, size: est(fmtMem(top.memory), top.estimated) };
      const swap = s.memory.swapTotal >= STATUS_ALERT.swapMentionGB * GiB
        ? ` ${t("status.alert.swapLine", { size: fmtMem(s.memory.swapTotal) })}`
        : "";
      const p = s.memory.pressure;
      if (p === 2 || p === 4) return { tone: p === 4 ? "crit" : "warn", text: t("process.diag.mem", vars) + swap };
      return { tone: "ok", text: t("menubar.diag.memOk", vars) + swap };
    }
    case "gpu":
      if (!m || m.gpu < 0) return null;
      return busy(d.gpuHist) ? { tone: "warn", text: t("menubar.diag.gpuBusy") } : { tone: "ok", text: t("menubar.diag.gpuIdle") };
    case "net":
      if (!m) return null;
      return {
        tone: "ok",
        text: t(m.netKind === "wifi" ? "menubar.diag.netWifi" : m.netKind === "ethernet" ? "menubar.diag.netEthernet" : "menubar.diag.netNone"),
      };
    case "disk": {
      if (!s || !(s.disk.free > 0)) return null;
      const low = s.disk.free < STATUS_ALERT.diskFreeWarnGB * GiB;
      let text = t(low ? "status.alert.diskLow" : "menubar.diag.diskOk", { size: fmtMem(s.disk.free) });
      const a = growthHint(d.growth, []);
      if (a) {
        const size = a.atLeast ? t("growth.atLeast", { size: human(a.bytes) }) : human(a.bytes);
        const alert = t(a.window === "day" ? "status.alert.growthDay" : "status.alert.growthWeek", { app: a.name, days: a.days, size });
        text += ` ${t("menubar.diag.growth", { alert })}`;
      }
      return { tone: low || a ? "warn" : "ok", text };
    }
    case "temp": {
      if (!m || m.thermal < 0) return null;
      const n = Math.min(m.thermal, 3);
      return { tone: n === 0 ? "ok" : n === 1 ? "warn" : "crit", text: t(`menubar.diag.thermal${n}` as DictKey) };
    }
    case "battery": {
      const b = s?.battery;
      if (!b) return null;
      const state = b.full
        ? t("status.battery.full")
        : b.charging
          ? t("status.battery.charging", { pct: `${b.percent}%` })
          : b.plugged ? t("status.battery.plugged") : t("status.battery.onBattery");
      const cond = b.condition.trim().toLowerCase();
      const service = cond !== "" && cond !== "good" && cond !== "normal";
      return service ? { tone: "warn", text: `${state} · ${t("status.battery.service")}` } : { tone: "ok", text: state };
    }
  }
}

/** 曲线上的一点；读不到时为 null，不画这一点。 */
export function chartPoint(item: PanelItem, s: StatusSample): number | null {
  const m = s.machine;
  switch (item) {
    case "cpu":
      return s.cpu.usage;
    case "mem":
      return s.memory.total > 0 ? (s.memory.used / s.memory.total) * 100 : null;
    case "gpu":
      return m && m.gpu >= 0 ? m.gpu : null;
    case "net":
      return m && m.netDown >= 0 ? m.netDown : null;
    case "disk":
      return m && m.diskRead >= 0 && m.diskWrite >= 0 ? m.diskRead + m.diskWrite : null;
    case "temp":
      return m && m.chipTemp > 0 ? m.chipTemp : null;
    case "battery":
      return s.battery ? s.battery.percent : null;
  }
}

/** 磁盘读写曲线纵轴的下限：空闲时几百 KB/s 的读写不会撑满整格。 */
export const DISK_SPARK_FLOOR = 1_000_000;

/** 纵轴上限：网络、磁盘按最近一分钟的最大值（各有下限），其余 0–100（温度按 °C）。 */
export function chartMax(item: PanelItem, hist: readonly number[]): number {
  if (item === "net") return netSparkMax(hist);
  if (item === "disk") return Math.max(DISK_SPARK_FLOOR, ...hist);
  return 100;
}

/** 曲线颜色：内存按压力变橙、变红；GPU 紫，网络、磁盘蓝，其余主题色。 */
export function chartTone(item: PanelItem, s: StatusSample | null): "accent" | "gpu" | "net" | "warn" | "crit" {
  if (item === "mem") return s?.memory.pressure === 4 ? "crit" : s?.memory.pressure === 2 ? "warn" : "accent";
  if (item === "gpu") return "gpu";
  if (item === "net" || item === "disk") return "net";
  return "accent";
}

/** 明细的一行；读不到的行不出现（设计 §四）。 */
export interface DetailRow {
  key: string;
  label: string;
  value: string;
  /** 0–100：值前面画一条小进度条 */
  bar?: number;
  /** 标签前的色点（内存的几段） */
  dot?: string;
}

/** 风扇：没有风扇、没在转，或各个风扇的转速；读不到为 ""。 */
function fansText(m: MachineSample, t: T): string {
  if (m.fanCount === 0) return t("status.health.noFans");
  if (m.fanCount > 0 && m.fans.length > 0) return m.fans.every((rpm) => rpm < 1) ? t("status.health.fansIdle") : `${m.fans.join(" · ")} rpm`;
  return "";
}

/** 一台显示器：外接的写名字和模式；内建的只写模式，名字由标签说（NSScreen 的名字在中文界面里也是英文）。 */
function displayValue(d: DisplayInfo, t: T): string {
  if (!d.builtin) return displayText(d, t("status.display.external"));
  return d.width > 0 && d.height > 0 ? `${d.width}×${d.height}${d.hz > 0 ? ` @${d.hz}Hz` : ""}` : "";
}

/** 明细（设计 §四的表）；还没有读数时为空。 */
export function detailRows(item: PanelItem, s: StatusSample | null, info: MachineInfo | null, t: T): DetailRow[] {
  if (!s) return [];
  const m = s.machine;
  const rows: DetailRow[] = [];
  const add = (row: DetailRow) => {
    if (row.value && row.value !== "—") rows.push(row);
  };
  switch (item) {
    case "cpu":
      if (m && m.perfCpu >= 0) {
        add({ key: "perf", label: info && info.perfCores > 0 ? t("status.cpu.perfN", { n: info.perfCores }) : t("status.cpu.perf"), value: fmtPct(m.perfCpu), bar: m.perfCpu });
      }
      if (m && m.effCpu >= 0) {
        add({ key: "eff", label: info && info.effCores > 0 ? t("status.cpu.effN", { n: info.effCores }) : t("status.cpu.eff"), value: fmtPct(m.effCpu), bar: m.effCpu });
      }
      if (m) add({ key: "chip", label: t("menubar.panel.chipFans"), value: [m.chipTemp > 0 ? fmtTemp(m.chipTemp) : "", fansText(m, t)].filter(Boolean).join(" · ") });
      break;
    case "mem": {
      const mm = s.memory;
      add({ key: "app", label: t("status.mem.appMem"), value: fmtMem(mm.app), dot: "var(--seg-app)" });
      add({ key: "wired", label: t("status.mem.wired"), value: fmtMem(mm.wired), dot: "var(--seg-wired)" });
      add({ key: "compressed", label: t("status.mem.compressed"), value: fmtMem(mm.compressed), dot: "var(--seg-compressed)" });
      add({ key: "cached", label: t("status.mem.cachedLabel"), value: fmtMem(mm.cached) });
      if (mm.swapUsed > 0.1 * GiB) {
        add({ key: "swap", label: t("menubar.panel.swap"), value: t("menubar.panel.swapValue", { used: fmtMem(mm.swapUsed), size: fmtMem(mm.swapTotal) }) });
      }
      break;
    }
    case "gpu":
      if (info && info.gpuCores > 0) add({ key: "cores", label: t("menubar.panel.gpuCores"), value: t("status.cpu.cores", { n: info.gpuCores }) });
      if (m && m.gpuMemory > 0) add({ key: "memory", label: t("status.gpu.memory"), value: fmtMem(m.gpuMemory) });
      for (const d of info?.displays ?? []) {
        add({ key: `display-${d.id}`, label: t(d.builtin ? "status.display.builtin" : "status.display.external"), value: displayValue(d, t) });
      }
      break;
    case "net":
      if (m && m.netDown >= 0) add({ key: "down", label: t("status.net.down"), value: fmtRateText(m.netDown) });
      if (m && m.netUp >= 0) add({ key: "up", label: t("status.net.up"), value: fmtRateText(m.netUp) });
      break;
    case "disk": {
      const ssd = info?.ssd ?? null;
      const smart = ssd?.smart ? ssd : null;
      if (s.disk.total > 0) add({ key: "space", label: t("menubar.panel.freeTotal"), value: `${fmtMem(s.disk.free)} / ${fmtMem(s.disk.total)}` });
      if (ssd?.model) add({ key: "model", label: t("menubar.panel.ssdModel"), value: ssd.model });
      if (smart) {
        const warns = ssdWarnings(smart.warning);
        add({ key: "health", label: t("status.ssd.health"), value: warns.length > 0 ? t(warns[0]!) : t("status.ssd.ok") });
        add({ key: "life", label: t("status.ssd.life"), value: `${smart.used}%` });
        if (smart.written > 0) add({ key: "written", label: t("status.ssd.written"), value: fmtDec(smart.written) });
      }
      // NAND 传感器优先；没有时用打开面板时 SMART 日志里的温度，和首页一样
      const temp = m && m.diskTemp > 0 ? m.diskTemp : smart ? smart.tempC : 0;
      if (temp > 0) add({ key: "temp", label: t("status.disk.temp"), value: fmtTemp(temp) });
      if (m && m.diskRead >= 0) add({ key: "read", label: t("status.disk.read"), value: fmtRateText(m.diskRead) });
      if (m && m.diskWrite >= 0) add({ key: "write", label: t("status.disk.write"), value: fmtRateText(m.diskWrite) });
      break;
    }
    case "temp":
      if (m) {
        if (m.thermal >= 0) add({ key: "thermal", label: t("status.health.thermal"), value: t(THERMAL[Math.min(m.thermal, 3)]!) });
        if (m.chipTemp > 0) add({ key: "chip", label: t("status.health.chipTemp"), value: fmtTemp(m.chipTemp) });
        add({ key: "fans", label: t("status.health.fans"), value: fansText(m, t) });
        if (m.power > 0) add({ key: "power", label: t("status.health.power"), value: `${Math.round(m.power)} W` });
        if (m.diskTemp > 0) add({ key: "ssd", label: t("menubar.panel.ssdTemp"), value: fmtTemp(m.diskTemp) });
      }
      if (s.battery && s.battery.temperature > 0) add({ key: "battery", label: t("menubar.panel.batteryTemp"), value: fmtTemp(s.battery.temperature) });
      break;
    case "battery": {
      const b = s.battery;
      if (!b) break;
      add({ key: "charge", label: t("menubar.panel.charge"), value: `${b.percent}%`, bar: b.percent });
      if (b.maxCapacity > 0) add({ key: "health", label: t("status.battery.maxCap"), value: `${b.maxCapacity}%` });
      if (b.cycles > 0) add({ key: "cycles", label: t("menubar.panel.cycles"), value: String(b.cycles) });
      if (b.temperature > 0) add({ key: "temp", label: t("status.battery.temp"), value: fmtTemp(b.temperature) });
      if (b.plugged && b.adapterWatts > 0) add({ key: "adapter", label: t("status.battery.adapter"), value: `${b.adapterWatts} W` });
      break;
    }
  }
  return rows;
}

/** 内存条三段（App、联动、被压缩）各占总量的百分比；总量读不到时为 null。 */
export function memShares(m: MemoryStatus): { app: number; wired: number; compressed: number } | null {
  if (!(m.total > 0)) return null;
  const pct = (n: number) => Math.max(0, Math.min(100, (n / m.total) * 100));
  return { app: pct(m.app), wired: pct(m.wired), compressed: pct(m.compressed) };
}

/** 列表：按 CPU 或内存排的前 5 行（排法同「进程」页）；frozen 时沿用上一轮的行和顺序，只换数字。 */
export function topRows(sample: ProcessSample, by: "cpu" | "mem", prev: readonly ProcessRow[], frozen: boolean): ProcessRow[] {
  return frozen && prev.length > 0 ? refreshFrozen(prev, sample) : sortRows(sample.rows, by).slice(0, PANEL_TOP);
}

/** 磁盘面板的「涨得最快」：增长监控开着时，近 7 天涨了、没被忽略的 App，最多 5 个，涨得多的在前。 */
export function growthTop(g: GrowthState | null): GrowthApp[] {
  if (!g || !g.supported || !g.config.enabled) return [];
  return g.apps
    .filter((a) => !a.ignored && (a.week?.bytes ?? 0) > 0)
    .sort((a, b) => b.week!.bytes - a.week!.bytes || a.key.localeCompare(b.key))
    .slice(0, PANEL_TOP);
}

/** 一行的退出流程（设计 §四.5）：退出、确认、正在退出，没退掉时还没退出、确认强制、正在强制，最后已退出或失败。 */
export type QuitStep = "idle" | "confirm" | "quitting" | "stuck" | "forceConfirm" | "forcing" | "done" | "failed";

/** 列表各行的退出状态；gone 是「已退出」后离开过列表的行，同一个 App 再出现时恢复成可退出。 */
export interface QuitBook {
  steps: ReadonlyMap<string, QuitStep>;
  gone: ReadonlySet<string>;
}

export const emptyBook = (): QuitBook => ({ steps: new Map(), gone: new Set() });

/** 「确认退出」「确认强制退出」没点第二下，多久退回。 */
export const QUIT_CONFIRM_MS = 3000;

export function withStep(b: QuitBook, key: string, step: QuitStep): QuitBook {
  return { ...b, steps: new Map(b.steps).set(key, step) };
}

/** 有一行在等第二下。 */
export function confirming(b: QuitBook): boolean {
  return [...b.steps.values()].some((s) => s === "confirm" || s === "forceConfirm");
}

/** 正在退出、正在强制退出：面板这时不自动关上（设计 §四.5）。 */
export function quitPending(b: QuitBook): boolean {
  return [...b.steps.values()].some((s) => s === "quitting" || s === "forcing");
}

/** 列表不重排：指针在列表里，或有一行在确认、在退出（设计 §四.5）。 */
export function listFrozen(hover: boolean, b: QuitBook): boolean {
  return hover || confirming(b) || quitPending(b);
}

/** 确认没点第二下：「确认退出」退回原样，「确认强制退出」退回「还没退出」；没有在确认的行时原样返回。 */
export function revertConfirms(b: QuitBook): QuitBook {
  if (!confirming(b)) return b;
  const steps = new Map<string, QuitStep>();
  for (const [k, v] of b.steps) {
    if (v === "forceConfirm") steps.set(k, "stuck");
    else if (v !== "confirm") steps.set(k, v);
  }
  return { ...b, steps };
}

/**
 * 每次进程读数后对账（同「进程」页的 syncQuitRows）：「已退出」的行离开列表后记下，同一个 App 再出现时恢复成可退出；
 * 「还没退出」「退出失败」的 App 不在了就忘掉。没变时原样返回。
 */
export function syncBook(b: QuitBook, rows: readonly ProcessRow[]): QuitBook {
  if (b.steps.size === 0 && b.gone.size === 0) return b;
  const present = new Set(rows.map((r) => r.key));
  const steps = new Map(b.steps);
  const gone = new Set(b.gone);
  for (const [k, v] of b.steps) {
    if (v === "done") {
      if (!present.has(k)) gone.add(k);
      else if (b.gone.has(k)) {
        steps.delete(k);
        gone.delete(k);
      }
    } else if ((v === "stuck" || v === "failed") && !present.has(k)) {
      steps.delete(k);
    }
  }
  const same = steps.size === b.steps.size && gone.size === b.gone.size && [...gone].every((k) => b.gone.has(k));
  return same ? b : { steps, gone };
}
