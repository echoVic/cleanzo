import { isWindows } from "../../platform";
import * as React from "react";
import { Root as TabsRoot, List as TabsList, Trigger as TabsTrigger } from "@radix-ui/react-tabs";
import { Channel } from "mygo-runtime";
import type { AppUsage, BatteryStatus, GrowthSummary, MachineInfo, MachineSample, SSDInfo, StatusSample, SystemInfo } from "../../mygo";
import { currentLang } from "../../i18n";
import { Button } from "../../ui/components/button";
import {
  Dialog, DialogClose, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "../../ui/components/dialog";
import { segTrigger } from "../../ui/components/seg";
import { useT } from "../../ui/i18n-react";
import { Collapse } from "../../ui/motion/Collapse";
import { NumberTicker } from "../../ui/motion/NumberTicker";
import { useFlip } from "../../ui/motion/flip";
import { useIndicator } from "../../ui/motion/indicator";
import { dur, EASE, play, scrollBehavior } from "../../ui/motion/motion";
import { Leave, usePresence } from "../../ui/motion/presence";
import { errText, human, toast } from "../../shared";
import { statusApi } from "./api";
import { crashDumps, oversized } from "../list/crash-dumps";
import { growthApi } from "../growth/api";
import { growthHint } from "../growth/hint";
import type { ProcessTab } from "../process/format";
import type { CrashDumpSummary } from "../../mygo";
import {
  GiB, MiB, SPARK_POINTS, displayText, emptyQuit, fmtDec, fmtMem, fmtPct, fmtRate, fmtRateText, fmtTemp, iconColor,
  isSparkShift, modelLabel, netSparkMax, pickAlerts, quitErrCode, refreshFrozenRows, sortApps, splitMem, ssdWarnings,
  syncQuitRows, trimSpark, uptimeParts,
  type QuitState, type SortBy,
} from "./format";
import { AppIcon } from "../icons/AppIcon";

type T = ReturnType<typeof useT>;

// ---- 指标卡图标（与旧内联 SVG 一致） ----

const MemoryIcon = () => (
  <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <rect x="3" y="7" width="18" height="10" rx="2" /><path d="M7 10v4M11 10v4M15 10v4" />
  </svg>
);
const CpuIcon = () => (
  <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <rect x="6" y="6" width="12" height="12" rx="2" /><path d="M9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3" />
  </svg>
);
const DiskIcon = () => (
  <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <rect x="3" y="5" width="18" height="14" rx="2" /><circle cx="12" cy="12" r="3.2" /><path d="M17.5 7.5h.01" />
  </svg>
);
const BatteryIcon = () => (
  <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <rect x="2" y="8" width="17" height="9" rx="2" /><path d="M21.5 11v3" /><path d="m10 10-2 3h2.2L10 16" />
  </svg>
);
const GpuIcon = () => (
  <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <rect x="2" y="6" width="20" height="12" rx="2" /><rect x="6" y="9.5" width="7" height="5" rx="1" /><path d="M16 10.5h2M16 13.5h2M6 18v2.5M10 18v2.5" />
  </svg>
);
const NetIcon = () => (
  <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M8 4v16M8 20l-3.5-3.5M8 20l3.5-3.5M16 20V4M16 4l-3.5 3.5M16 4l3.5 3.5" />
  </svg>
);
const CalloutIcon = () => (
  <svg className="st-callout-ico" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M12 9v4M12 17h.01" /><path d="M10.3 3.9 1.9 18a2 2 0 0 0 1.7 3h16.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z" />
  </svg>
);

// ---- 指标卡 ----

export function MemoryCard({ m, t }: { m: StatusSample["memory"]; t: T }) {
  const badge = m.pressure === 2
    ? <em className="mbadge">{t("status.mem.pressureWarn")}</em>
    : m.pressure === 4
      ? <em className="mbadge crit">{t("status.mem.pressureCrit")}</em>
      : null;
  const usedUnit = splitMem(m.used)[1];
  const pct = (n: number) => (m.used > 0 ? Math.max(0, Math.round((n / m.used) * 100)) : 0);
  return (
    <article className="mcard cz-fade-in">
      <div className="mhead"><MemoryIcon /><span>{t("status.card.memory")}</span>{badge}</div>
      {m.total
        ? (
          <div className="mhero num">
            <NumberTicker as="b" value={m.used} fps={15} format={(v) => splitMem(v)[0]} /><i>{usedUnit}</i><span className="mctx">/ {fmtMem(m.total)}</span>
          </div>
        )
        : <div className="mhero num"><b className="ph">—</b></div>}
      <div className="mhcapt">{t("status.mem.caption")}</div>
      {m.used > 0 && (
        <>
          <div className="mviz">
            <div
              className="mstack"
              role="img"
              aria-label={t("status.mem.barLabel", { used: fmtMem(m.used), app: fmtMem(m.app), wired: fmtMem(m.wired), comp: fmtMem(m.compressed) })}
            >
              <i style={{ width: `${pct(m.app)}%`, background: "var(--seg-app)" }} />
              <i style={{ width: `${pct(m.wired)}%`, background: "var(--seg-wired)" }} />
              {(!isWindows() || m.compressed > 0) && <i style={{ width: `${pct(m.compressed)}%`, background: "var(--seg-compressed)" }} />}
            </div>
          </div>
          <ul className={isWindows() ? "mlegend num mlegend-win" : "mlegend num"}>
            <li><i className="mdot" style={{ background: "var(--seg-app)" }} /><span>{t("status.mem.appMem")}</span><b>{fmtMem(m.app)}</b></li>
            <li><i className="mdot" style={{ background: "var(--seg-wired)" }} /><span>{t("status.mem.wired")}</span><b>{fmtMem(m.wired)}</b></li>
            {(!isWindows() || m.compressed > 0) && <li><i className="mdot" style={{ background: "var(--seg-compressed)" }} /><span>{t("status.mem.compressed")}</span><b>{fmtMem(m.compressed)}</b></li>}
            <li><span>{t("status.mem.cachedLabel")}</span><b>{fmtMem(m.cached)}</b></li>
          </ul>
        </>
      )}
      <div className={m.swapUsed > 0.1 * GiB ? "mfoot num" : "mfoot"}>
        {m.swapUsed > 0.1 * GiB ? t("status.mem.swap", { used: fmtMem(m.swapUsed), size: fmtMem(m.swapTotal) }) : ""}
      </div>
    </article>
  );
}

const SPARK_STEP = 120 / (SPARK_POINTS - 1);
/** 平移时 svg 多画一格：宽 120 + 一格，平移一格占 svg 自身宽度的百分比。 */
const SPARK_WIDE = 120 + SPARK_STEP;
const SPARK_SHIFT_PCT = (SPARK_STEP / SPARK_WIDE) * 100;
/** 曲线配色：CPU 用主题色，GPU 紫、网络蓝（与内存条的联动、App 两色一致）。 */
const SPARK_TONE = {
  accent: { stroke: "var(--accent)", fill: "var(--accent-soft)", fillOpacity: undefined },
  gpu: { stroke: "var(--seg-wired)", fill: "var(--seg-wired)", fillOpacity: 0.14 },
  net: { stroke: "var(--seg-app)", fill: "var(--seg-app)", fillOpacity: 0.14 },
} as const;

/** 30 点面积曲线，纵轴 0–max（默认 0–100%）；重开采样时缓冲清空，不足 2 点不画。 */
function SparkSvg({ hist, max = 100, tone = "accent" }: { hist: number[]; max?: number; tone?: keyof typeof SPARK_TONE }) {
  const svg = React.useRef<SVGSVGElement>(null);
  const prev = React.useRef<number[]>(hist);
  const ms = dur("spark");
  // 同一次采样保留同一组点，悬停等重渲染不打断平移。
  const { shift, pts } = React.useMemo(() => {
    const shift = ms > 0 && isSparkShift(prev.current, hist);
    // 平移：多画最左边那个旧点，整组左移一格后正好接上（设计 6.3）
    return { shift, pts: shift ? [prev.current[0]!, ...hist] : hist };
  }, [hist, ms]);
  React.useLayoutEffect(() => {
    prev.current = hist;
    const el = svg.current;
    if (!el) return;
    el.getAnimations().forEach((a) => a.cancel());
    // 移动整张 svg 而不是里面的 <g>：WebKit 把它交给合成器，平移时网页不用每帧重排重画
    if (shift) void play(el, [{ transform: "translateX(0)" }, { transform: `translateX(-${SPARK_SHIFT_PCT}%)` }], dur("spark"), EASE.out, { fill: "forwards" });
  }, [hist, shift]);
  if (pts.length < 2) return null;
  const top = max > 0 ? max : 100;
  const y = (v: number) => (28 - (Math.max(0, Math.min(top, v)) / top) * 26).toFixed(1);
  const c = SPARK_TONE[tone];
  const line = `M${pts.map((v, i) => `${(i * SPARK_STEP).toFixed(1)},${y(v)}`).join(" L")}`;
  const area = `${line} L${((pts.length - 1) * SPARK_STEP).toFixed(1)},28 L0,28 Z`;
  return (
    <div className="st-spark" aria-hidden="true">
      <svg ref={svg} viewBox={`0 0 ${SPARK_WIDE.toFixed(2)} 28`} preserveAspectRatio="none" style={{ width: `${((SPARK_WIDE / 120) * 100).toFixed(3)}%` }}>
        <path fill={c.fill} fillOpacity={c.fillOpacity} d={area} />
        <path fill="none" stroke={c.stroke} strokeWidth={1.5} strokeLinejoin="round" d={line} />
      </svg>
    </div>
  );
}

export function CpuCard({ cpu, cores, hist, t, m, split }: {
  cpu: StatusSample["cpu"]; cores: number; hist: number[]; t: T;
  /** macOS：性能核、能效核各自的使用率（读不到为 -1） */
  m?: MachineSample | null;
  /** 性能核、能效核的个数（MachineInfo；0 = 还不知道） */
  split?: { perf: number; eff: number };
}) {
  const loads: Array<[string, string]> = [
    [t("status.cpu.load1"), cpu.load1.toFixed(1)],
    [t("status.cpu.load5"), cpu.load5.toFixed(1)],
    [t("status.cpu.load15"), cpu.load15.toFixed(1)],
  ];
  return (
    <article className="mcard cz-fade-in">
      <div className="mhead"><CpuIcon /><span>{t("status.card.cpu")}</span></div>
      <div className="mhero num"><NumberTicker as="b" value={cpu.usage} fps={15} format={(v) => String(Math.round(v))} /><i>%</i></div>
      <div className="mhcapt">{t("status.cpu.caption")}</div>
      <div className="mviz"><SparkSvg hist={hist} /></div>
      {!isWindows() && <div className="mloads num">
        {loads.map(([label, v], i) => <div key={i}><span>{label}</span><b>{v}</b></div>)}
      </div>}
      {m && (m.perfCpu >= 0 || m.effCpu >= 0) && (
        <ul className="mlegend num mlegend-rows">
          {m.perfCpu >= 0 && <li><span>{split?.perf ? t("status.cpu.perfN", { n: split.perf }) : t("status.cpu.perf")}</span><b>{fmtPct(m.perfCpu)}</b></li>}
          {m.effCpu >= 0 && <li><span>{split?.eff ? t("status.cpu.effN", { n: split.eff }) : t("status.cpu.eff")}</span><b>{fmtPct(m.effCpu)}</b></li>}
        </ul>
      )}
      <div className="mfoot num">{cores > 0 ? t("status.cpu.cores", { n: cores }) : "—"}</div>
    </article>
  );
}

export function DiskCard({ d, t, onClean, ssd, live }: {
  d: StatusSample["disk"]; t: T; onClean: () => void;
  /** macOS：内置 SSD（MachineInfo）；没有时不写型号、健康、寿命和写入量 */
  ssd?: SSDInfo | null;
  /** macOS：读写速度和 NAND 温度（MachineSample） */
  live?: MachineSample | null;
}) {
  const usedPct = d.total ? Math.min(100, Math.round((d.used / d.total) * 100)) : 0;
  const freeUnit = d.free > 0 ? splitMem(d.free)[1] : "";
  const smart = ssd?.smart ? ssd : null;
  const warns = smart ? ssdWarnings(smart.warning) : [];
  // NAND 传感器优先；没有时用进入首页时 SMART 日志里的温度
  const temp = live && live.diskTemp > 0 ? live.diskTemp : smart ? smart.tempC : 0;
  return (
    <article className="mcard cz-fade-in">
      <div className="mhead">
        <DiskIcon /><span>{t("status.card.disk")}</span>
        {warns.length > 0 && <em className="mbadge crit" title={warns.map((k) => t(k)).join(currentLang() === "zh" ? "、" : ", ")}>{t("status.ssd.attention")}</em>}
      </div>
      <div className="mhero num"><NumberTicker as="b" value={d.free} fps={15} format={(v) => (v > 0 ? splitMem(v)[0] : "—")} /><i>{freeUnit}</i><span className="mctx">{t("status.disk.avail")}</span></div>
      <div className="mhcapt">{t("status.disk.totalOnly", { total: fmtMem(d.total) })}{ssd?.model ? ` · ${ssd.model}` : ""}</div>
      <div className="mviz"><div className="mbar"><i style={{ width: `${usedPct}%` }} /></div></div>
      <ul className={isWindows() ? "mlegend num mlegend-win" : "mlegend num"}>
        <li><span>{t("status.disk.used")}</span><b>{usedPct}%</b></li>
        <li><span>{t("status.disk.avail")}</span><b>{fmtMem(d.free)}</b></li>
        {live && live.diskRead >= 0 && <li><span>{t("status.disk.read")}</span><b>{fmtRateText(live.diskRead)}</b></li>}
        {live && live.diskWrite >= 0 && <li><span>{t("status.disk.write")}</span><b>{fmtRateText(live.diskWrite)}</b></li>}
        {smart && <li><span>{t("status.ssd.health")}</span><b>{warns.length > 0 ? t(warns[0]!) : t("status.ssd.ok")}</b></li>}
        {smart && <li><span>{t("status.ssd.life")}</span><b>{`${smart.used}%`}</b></li>}
        {smart && smart.written > 0 && <li><span>{t("status.ssd.written")}</span><b>{fmtDec(smart.written)}</b></li>}
        {temp > 0 && <li><span>{t("status.disk.temp")}</span><b>{fmtTemp(temp)}</b></li>}
      </ul>
      <div className="mfoot">
        <span>{t("status.disk.footHint")}</span>
        <button className="mlink" type="button" onClick={onClean}>{t("status.disk.clean")} ›</button>
      </div>
    </article>
  );
}

export function BatteryCard({ b, t }: { b: BatteryStatus; t: T }) {
  const cond = b.condition.trim().toLowerCase();
  const badge = cond && cond !== "good" && cond !== "normal"
    ? <em className="mbadge">{t("status.battery.service")}</em>
    : null;
  const state = b.full
    ? t("status.battery.full")
    : b.charging
      ? t("status.battery.charging", { pct: `${b.percent}%` })
      : b.plugged
        ? t("status.battery.plugged")
        : t("status.battery.onBattery");
  const healthPct = b.maxCapacity > 0 ? b.maxCapacity : 0;
  // 状况以后端字符串为准；Good/Normal 显示「正常」，空值降级「—」
  const condVal = !b.condition.trim()
    ? "—"
    : cond === "good" || cond === "normal"
      ? t("status.battery.conditionOk")
      : b.condition.trim();
  return (
    <article className="mcard cz-fade-in">
      <div className="mhead"><BatteryIcon /><span>{t("status.card.battery")}</span>{badge}</div>
      <div className="mhero num"><NumberTicker as="b" value={b.percent} fps={15} format={(v) => String(Math.round(v))} /><i>%</i></div>
      <div className="mhcapt">{state}</div>
      <div className="mviz">
        <div className={`mbar${healthPct > 0 && healthPct < 80 ? " low" : ""}`}><i style={{ width: `${healthPct}%` }} /></div>
      </div>
      <ul className="mlegend num">
        <li><span>{t("status.battery.maxCap")}</span><b>{healthPct > 0 ? `${healthPct}%` : "—"}</b></li>
        <li><span>{t("status.battery.condition")}</span><b>{condVal}</b></li>
        {b.temperature > 0 && <li><span>{t("status.battery.temp")}</span><b>{fmtTemp(b.temperature)}</b></li>}
        {b.plugged && b.adapterWatts > 0 && <li><span>{t("status.battery.adapter")}</span><b>{`${b.adapterWatts} W`}</b></li>}
      </ul>
      <div className="mfoot num">{b.cycles > 0 ? t("status.battery.cycles", { n: b.cycles }) : "—"}</div>
    </article>
  );
}

export function GpuCard({ m, cores, displays, hist, t }: { m: MachineSample; cores: number; displays: number; hist: number[]; t: T }) {
  return (
    <article className="mcard cz-fade-in">
      <div className="mhead"><GpuIcon /><span>{t("status.card.gpu")}</span></div>
      {m.gpu >= 0
        ? <div className="mhero num"><NumberTicker as="b" value={m.gpu} fps={15} format={(v) => String(Math.round(v))} /><i>%</i></div>
        : <div className="mhero num"><b className="ph">—</b></div>}
      <div className="mhcapt">{t("status.gpu.caption")}</div>
      <div className="mviz"><SparkSvg hist={hist} tone="gpu" /></div>
      {(m.gpuMemory > 0 || displays > 0) && (
        <ul className="mlegend num mlegend-rows">
          {m.gpuMemory > 0 && <li><span>{t("status.gpu.memory")}</span><b>{fmtMem(m.gpuMemory)}</b></li>}
          {displays > 0 && <li><span>{t("status.gpu.displays")}</span><b>{t("status.gpu.displayCount", { n: displays })}</b></li>}
        </ul>
      )}
      <div className="mfoot num">{cores > 0 ? t("status.cpu.cores", { n: cores }) : "—"}</div>
    </article>
  );
}

export function NetCard({ m, hist, t }: { m: MachineSample; hist: number[]; t: T }) {
  const [down, downUnit] = fmtRate(m.netDown);
  const [up, upUnit] = fmtRate(m.netUp);
  const kind = m.netKind === "wifi" ? t("status.net.wifi") : m.netKind === "ethernet" ? t("status.net.ethernet") : "";
  return (
    <article className="mcard cz-fade-in">
      <div className="mhead"><NetIcon /><span>{t("status.card.network")}</span>{kind && <span className="mhead-note">{kind}</span>}</div>
      <div className="mhero mhero-duo num">
        <span className="mdir"><i aria-hidden="true">↓</i><span className="sr-only">{t("status.net.down")}</span><b>{down}</b><i>{downUnit}</i></span>
        <span className="mdir"><i aria-hidden="true">↑</i><span className="sr-only">{t("status.net.up")}</span><b>{up}</b><i>{upUnit}</i></span>
      </div>
      <div className="mhcapt">{t("status.net.caption")}</div>
      <div className="mviz"><SparkSvg hist={hist} max={netSparkMax(hist)} tone="net" /></div>
      <div className="mfoot">{t("status.net.foot")}</div>
    </article>
  );
}

/** thermal state 四档（NSProcessInfo）：正常 / 偏热 / 很热 / 过热。 */
const THERMAL = [
  { key: "status.health.thermal0", tone: "ok" },
  { key: "status.health.thermal1", tone: "warn" },
  { key: "status.health.thermal2", tone: "crit" },
  { key: "status.health.thermal3", tone: "crit" },
] as const;

/** 健康条（设计 §三.2）：读不到的格子整格省掉，四格都读不到时整条不出现。 */
export function HealthStrip({ m, t }: { m: MachineSample; t: T }) {
  const tiles: Array<{ key: string; label: string; value: React.ReactNode; tone?: string; title?: string }> = [];
  const thermal = m.thermal >= 0 ? THERMAL[Math.min(m.thermal, THERMAL.length - 1)] : undefined;
  if (thermal) tiles.push({ key: "thermal", label: t("status.health.thermal"), value: t(thermal.key), tone: thermal.tone });
  if (m.chipTemp > 0) tiles.push({ key: "chip", label: t("status.health.chipTemp"), value: fmtTemp(m.chipTemp) });
  if (m.fanCount === 0) {
    tiles.push({ key: "fans", label: t("status.health.fans"), value: t("status.health.noFans") });
  } else if (m.fanCount > 0 && m.fans.length > 0) {
    const idle = m.fans.every((rpm) => rpm < 1);
    const value = idle ? t("status.health.fansIdle") : <>{m.fans.join(" · ")} <small>rpm</small></>;
    // 三个以上风扇时格子放不下，悬停看全
    tiles.push({ key: "fans", label: t("status.health.fans"), value, title: idle ? undefined : `${m.fans.join(" · ")} rpm` });
  }
  if (m.power > 0) tiles.push({ key: "power", label: t("status.health.power"), value: `${Math.round(m.power)} W` });
  if (tiles.length === 0) return null;
  return (
    <section className="st-health cz-fade-in" aria-label={t("status.health.label")}>
      {tiles.map((x) => (
        <div key={x.key} className="st-htile">
          <span className="k">{x.label}</span>
          <b className={x.tone ? `v num ${x.tone}` : "v num"} title={x.title}>{x.value}</b>
        </div>
      ))}
    </section>
  );
}

// ---- 骨架态（首份采样未到：禁 0 / 假值） ----

const SkCard = () => (
  <article className="mcard st-skel">
    <div className="sk-line w40" /><div className="sk-line w70" /><div className="sk-line w90" />
  </article>
);
const SkRow = () => (
  <div className="st-row st-skel-row">
    <div className="sk-line w40" /><div className="sk-line w60" /><div className="sk-line w70" /><div className="sk-line w50" />
  </div>
);

/** 健康条的骨架：首份采样到达前先占住高度，卡片不会在采样到达时往下跳。 */
const SkHealth = () => (
  <section className="st-health" aria-hidden="true">
    {Array.from({ length: 4 }, (_, i) => (
      <div key={i} className="st-htile st-skel"><div className="sk-line w40" /><div className="sk-line w60" /></div>
    ))}
  </section>
);

export interface StatusPageProps {
  /** 当前是否停在系统状态视图：进入开 2s 采样流，离开关 Channel。 */
  active: boolean;
  /** 告警 / 磁盘卡的「去清理」：与点侧栏「垃圾清理」同一条路径（main.ts 注入）。 */
  goClean: () => void;
  /** 增长提示的「查看」、内存提示的「看谁在占内存」（macOS）：打开进程页的某个标签，可带磁盘占用标签里要定位的行（main.ts 注入）。 */
  goProcess: (tab: ProcessTab, focusKey?: string) => void;
}

export function StatusPage({ active, goClean, goProcess }: StatusPageProps) {
  const t = useT();
  const [sample, setSample] = React.useState<StatusSample | null>(null);
  const [sysInfo, setSysInfo] = React.useState<SystemInfo | null>(null);
  const [error, setError] = React.useState(false);
  const [cpuHist, setCpuHist] = React.useState<number[]>([]);
  /** 这台 Mac 是什么（macOS）：每次进入读一次，显示器变了再读；读到之前页头还是「系统状态」。 */
  const [machine, setMachine] = React.useState<MachineInfo | null>(null);
  const [gpuHist, setGpuHist] = React.useState<number[]>([]);
  const [netHist, setNetHist] = React.useState<number[]>([]);
  const [sortBy, setSortBy] = React.useState<SortBy>("mem");
  const [hover, setHover] = React.useState(false);
  const [focus, setFocus] = React.useState(false);
  const [hl, setHl] = React.useState(false);
  const [quit, setQuit] = React.useState<QuitState>(emptyQuit());
  const [quitTarget, setQuitTarget] = React.useState<AppUsage | null>(null);
  // 崩溃转储只在打开 Cleanzo 后量一次（du），超过阈值才提示。
  const [crashSum, setCrashSum] = React.useState<CrashDumpSummary | null>(null);
  React.useEffect(() => {
    if (!active) return;
    let live = true;
    void crashDumps().then((s) => { if (live) setCrashSum(s); });
    return () => { live = false; };
  }, [active]);

  // 每次进入读最近的增长提醒；读取失败不显示。
  const [growthState, setGrowthState] = React.useState<GrowthSummary | null>(null);
  React.useEffect(() => {
    if (!active) return;
    let live = true;
    void growthApi.summary().then((s) => { if (live) setGrowthState(s); }).catch(() => {
      if (live) setGrowthState(null);
    });
    return () => { live = false; };
  }, [active]);

  // 异步循环 / 事件里读最新值，避免闭包捕获旧 state。
  const runRef = React.useRef(0);
  const chanRef = React.useRef<{ close(): void } | null>(null);
  const stoppedRef = React.useRef(false);
  const activeRef = React.useRef(active);
  const infoSeqRef = React.useRef(0);
  const sysInfoRef = React.useRef(sysInfo);
  sysInfoRef.current = sysInfo;
  const machineRef = React.useRef(machine);
  machineRef.current = machine;
  const machineSeqRef = React.useRef(0);
  const goCleanRef = React.useRef(goClean);
  goCleanRef.current = goClean;
  /** 冻结期间的行集合与顺序（只更新数字）；重开采样时清空。 */
  const frozenRowsRef = React.useRef<AppUsage[]>([]);
  const listHeadRef = React.useRef<HTMLDivElement | null>(null);
  const listRef = React.useRef<HTMLElement>(null);
  const segRef = React.useRef<HTMLDivElement>(null);
  const segStyle = useIndicator(segRef, '[data-state="active"]');
  const hlTimerRef = React.useRef<number | null>(null);

  // ---- 生命周期：进入视图开采样，离开 / 窗口隐藏关 Channel，回来重开 ----

  const loadInfo = React.useCallback(async () => {
    if (sysInfoRef.current) return; // 已成功过就不再请求（同旧模块缓存）
    const seq = ++infoSeqRef.current;
    try {
      const i = await statusApi.info();
      if (seq === infoSeqRef.current) setSysInfo(i);
    } catch {
      if (seq === infoSeqRef.current) setSysInfo(null); // 失败留空，重进视图重试
    }
  }, []);

  /** 型号、核数、GPU、显示器、SSD（macOS）：读不到时保留上一次的；从没读到时页头保持原样。 */
  const loadMachine = React.useCallback(async () => {
    if (isWindows()) return;
    const seq = ++machineSeqRef.current;
    try {
      const m = await statusApi.machine();
      if (seq === machineSeqRef.current) setMachine(m);
    } catch { /* 下次进入或显示器变化时再读 */ }
  }, []);

  const startWatch = React.useCallback(async () => {
    const myRun = ++runRef.current;
    stoppedRef.current = false;
    setError(false);
    setSample(null);
    setCpuHist([]);
    setGpuHist([]);
    setNetHist([]);
    setQuit(emptyQuit());
    frozenRowsRef.current = [];
    void loadInfo();
    void loadMachine();
    const ch = new Channel<StatusSample>();
    chanRef.current = ch;
    try {
      const done = statusApi.watch(ch);
      for await (const s of ch) {
        if (runRef.current !== myRun) continue; // 已切走 / 重开：丢弃旧采样
        setSample(s);
        setCpuHist((prev) => trimSpark(prev, s.cpu.usage));
        const m = s.machine;
        if (m) {
          if (m.gpu >= 0) setGpuHist((prev) => trimSpark(prev, m.gpu));
          if (m.netDown >= 0) setNetHist((prev) => trimSpark(prev, m.netDown));
          // 接上或拔掉显示器：采样里的签名和页头读到的不同，重读一次
          const known = machineRef.current;
          if (known?.displaySig && m.displaySig && m.displaySig !== known.displaySig) void loadMachine();
        }
      }
      await done;
    } catch (e) {
      if (runRef.current === myRun && !stoppedRef.current) {
        // 本轮采样已经结束；重试按钮等价于 stopWatch + startWatch 重建通道
        setError(true);
        setSample(null);
        setCpuHist([]);
        setGpuHist([]);
        setNetHist([]);
      }
    } finally {
      if (chanRef.current === ch) chanRef.current = null;
    }
  }, [loadInfo, loadMachine]);

  const stopWatch = React.useCallback(() => {
    stoppedRef.current = true;
    runRef.current++; // Channel 循环停止消费
    chanRef.current?.close();
    chanRef.current = null;
  }, []);

  React.useEffect(() => {
    activeRef.current = active;
    if (active) void startWatch();
    else stopWatch();
  }, [active, startWatch, stopWatch]);

  // 卸载兜底（root 复用时通常不触发）：停止消费并关闭 Channel，避免未处理 rejection。
  React.useEffect(() => () => {
    runRef.current++;
    chanRef.current?.close();
    chanRef.current = null;
  }, []);

  // 窗口隐藏关通道，回来且还在本视图时重开（Go ctx 取消）。
  React.useEffect(() => {
    const onVis = () => {
      if (document.hidden) stopWatch();
      else if (activeRef.current) void startWatch();
    };
    document.addEventListener("visibilitychange", onVis);
    return () => document.removeEventListener("visibilitychange", onVis);
  }, [startWatch, stopWatch]);

  // 每轮采样后对账退出状态机：消失过的已退出行，同 key 再现恢复行态（QA P2-1）。
  React.useEffect(() => {
    if (!sample) return;
    const present = new Set(sample.apps.map((a) => a.key));
    setQuit((prev) => syncQuitRows(prev, present));
  }, [sample]);

  // ---- 「查看占内存的应用」联动：切按内存 + 滚动到列表 + 高亮 900ms ----

  const viewApps = React.useCallback(() => {
    setSortBy("mem");
    listHeadRef.current?.scrollIntoView({ behavior: scrollBehavior(), block: "start" });
    setHl(true);
    if (hlTimerRef.current) window.clearTimeout(hlTimerRef.current);
    hlTimerRef.current = window.setTimeout(() => setHl(false), 900);
  }, []);

  // ---- 退出应用（设计稿 §11：四结果 toast 映射） ----

  const doQuit = React.useCallback(async (app: AppUsage) => {
    setQuitTarget(null);
    setQuit((prev) => ({ ...prev, busy: new Set(prev.busy).add(app.path) }));
    try {
      const ok = await statusApi.quitApp(app.path);
      if (ok) {
        toast(t("status.toast.quitDone", { name: app.name }));
        setQuit((prev) => ({ ...prev, done: new Set(prev.done).add(app.key) }));
      } else {
        // 5s 超时仍在跑：多半在等未保存内容的确认
        toast(t("status.toast.quitPending", { name: app.name }));
      }
    } catch (e) {
      const code = quitErrCode(e);
      if (code === "not-running") {
        toast(t("status.toast.notRunning", { name: app.name }));
        setQuit((prev) => ({ ...prev, done: new Set(prev.done).add(app.key) }));
      } else if (code === "cannot-quit") {
        toast(t("status.toast.cannotQuit", { name: app.name }), "err");
      } else {
        toast(`${t("status.error")}：${errText(e)}`, "err");
      }
    } finally {
      setQuit((prev) => {
        if (!prev.busy.has(app.path)) return prev;
        const busy = new Set(prev.busy);
        busy.delete(app.path);
        return { ...prev, busy };
      });
    }
  }, [t]);

  // ---- 派生视图 ----

  let memPart = "", osPart = "", upPart = "";
  if (sysInfo) {
    if (sysInfo.memory >= GiB) memPart = t("status.machine.mem", { size: `${Math.round(sysInfo.memory / GiB)} GB` });
    else if (sysInfo.memory) memPart = t("status.machine.mem", { size: `${Math.round(sysInfo.memory / MiB)} MB` });
    if (sysInfo.osVersion) osPart = t("status.osVersion", { version: sysInfo.osVersion });
    const up = sysInfo.bootTime > 0 ? uptimeParts(sysInfo.bootTime, Date.now() / 1000) : null;
    if (up) {
      upPart = up.days >= 1 ? t("status.uptime.days", { d: up.days, h: up.hours })
        : up.hours >= 1 ? t("status.uptime.hours", { h: up.hours, m: up.mins })
          : t("status.uptime.mins", { m: up.mins });
    }
  }
  const machineParts = [sysInfo?.chip ?? "", memPart, osPart, upPart].filter(Boolean);
  // macOS 页头讲这台机器（设计 §三.1）：型号作标题；规格一行；系统、运行时长和显示器一行。
  const label = machine && !isWindows() ? modelLabel(machine.model, machine.modelId, currentLang()) : null;
  const specParts: string[] = [];
  const hwParts: string[] = [];
  if (machine && label?.name) {
    if (sysInfo?.chip) specParts.push(sysInfo.chip);
    const { perfCores: p, effCores: e } = machine;
    if (p > 0 && e > 0) specParts.push(t("status.spec.cpuSplit", { n: p + e, p, e }));
    else if (sysInfo?.cores) specParts.push(t("status.spec.cpu", { n: sysInfo.cores }));
    if (machine.gpuCores > 0) specParts.push(t("status.spec.gpu", { n: machine.gpuCores }));
    if (memPart) specParts.push(memPart);
    if (machine.ssd && machine.ssd.capacity > 0) specParts.push(t("status.spec.disk", { size: fmtDec(machine.ssd.capacity) }));
    if (osPart) hwParts.push(osPart);
    if (upPart) hwParts.push(upPart);
    // 内建屏的名字是 macOS 按自己的语言给的（中文界面里会是英文），用我们自己的说法
    const screens = machine.displays.map((d) => (d.builtin
      ? displayText({ ...d, name: "" }, t("status.display.builtin"))
      : displayText(d, t("status.display.external"))));
    if (screens.length > 0) hwParts.push(screens.join(" + "));
  }

  // hover / 焦点 / 确认框 / 等待期：冻结行集合与顺序，数字继续更新
  const freezing = hover || focus || quit.busy.size > 0 || quitTarget !== null;
  let rows: AppUsage[] = [];
  if (sample) {
    rows = freezing && frozenRowsRef.current.length > 0
      ? refreshFrozenRows(frozenRowsRef.current, sample)
      : sortApps(sample.apps, sortBy);
    if (!freezing || frozenRowsRef.current.length === 0) frozenRowsRef.current = rows;
  }

  const alerts = error || !sample ? [] : pickAlerts(sample);
  const bigDumps = oversized(crashSum);
  const growthTop = growthHint(growthState, bigDumps.map((d) => d.app));
  const alertItems = [...alerts.map((a) => ({ key: a.kind, a })),
    ...(bigDumps[0] ? [{ key: "crashDumps", a: null }] : []), ...(growthTop ? [{ key: "growth", a: null }] : [])];
  const alertsP = usePresence(alertItems, (x) => x.key);
  const rowsP = usePresence(rows, (r) => r.key, !!sample && !error);
  const rowKeys = rows.map((r) => r.key);
  const prevRowKeys = React.useRef(rowKeys);
  const removing = prevRowKeys.current.some((key) => !rowKeys.includes(key));
  React.useLayoutEffect(() => { prevRowKeys.current = rowKeys; });
  // 有行离场的这次更新不换位，补位交给收起。
  useFlip(listRef, rowKeys, !removing);

  return (
    <div className="cz-react">
      <header className="view-head">
        {label?.name ? (
          <div>
            <h2 className="st-model">{label.name}{label.note && <span className="st-model-note">{label.note}</span>}</h2>
            {specParts.length > 0 && <p className="muted num">{specParts.join(" · ")}</p>}
            {hwParts.length > 0 && <p className="st-machine num">{hwParts.join(" · ")}</p>}
          </div>
        ) : (
          <div>
            <h2>{t("nav.status")}</h2>
            <p className="muted">{t("status.sub")}</p>
            {machineParts.length > 0 && <p className="st-machine num">{machineParts.join(" · ")}</p>}
          </div>
        )}
      </header>

      {!isWindows() && !error && (sample ? sample.machine && <HealthStrip m={sample.machine} t={t} /> : <SkHealth />)}

      {/* 与原生 #st-alerts 一致：告警始终占一个区块位（为空时也保留节奏），多条告警紧贴排列。 */}
      <div className="st-alerts">
        {alertsP.map(({ item: { a }, key, leaving }) => (
          <Collapse key={key} open={!leaving} appear>
            {a ? <div className={`st-callout ${a.kind === "memCrit" ? "crit" : "warn"}`} role="status">
              <CalloutIcon />
              <div className="st-callout-body">
                {a.kind === "memWarn" && t("status.alert.memWarn")}
                {a.kind === "memCrit" && t("status.alert.memCrit")}
                {a.kind === "swapOnly" && t("status.alert.swapOnly", { size: fmtMem(a.swapDisk ?? 0) })}
                {a.kind === "diskLow" && t("status.alert.diskLow", { size: fmtMem(a.free ?? 0) })}
                {a.swapDisk != null && (a.kind === "memWarn" || a.kind === "memCrit") && (
                  <span className="st-extra num">{t("status.alert.swapLine", { size: fmtMem(a.swapDisk) })}</span>
                )}
              </div>
              {a.action && (
                <button
                  className="btn ghost small"
                  type="button"
                  onClick={() => (a.action !== "apps" ? goCleanRef.current() : isWindows() ? viewApps() : goProcess("mem"))}
                >
                  {a.action !== "apps" ? t("status.disk.clean") : isWindows() ? t("status.alert.viewApps") : t("status.alert.viewInProcess")}
                </button>
              )}
            </div> : key === "crashDumps" && bigDumps[0] ? <div className="st-callout warn" role="status">
              <CalloutIcon />
              <div className="st-callout-body">
                {t("status.alert.crashDumps", { app: bigDumps[0].app, size: fmtMem(bigDumps[0].size) })}
              </div>
              <button className="btn ghost small" type="button" onClick={() => goCleanRef.current()}>
                {t("status.disk.clean")}
              </button>
            </div> : key === "growth" && growthTop && <div className="st-callout warn" role="status">
              <CalloutIcon />
              <div className="st-callout-body">
                {t(growthTop.window === "day" ? "status.alert.growthDay" : "status.alert.growthWeek", {
                  app: growthTop.name, days: growthTop.days,
                  size: growthTop.atLeast ? t("growth.atLeast", { size: human(growthTop.bytes) }) : human(growthTop.bytes),
                })}
              </div>
              <button className="btn ghost small" type="button" onClick={() => goProcess("disk", growthTop.key)}>
                {t("status.alert.viewGrowth")}
              </button>
            </div>}
          </Collapse>
        ))}
      </div>

      {error ? (
        <div className="empty err">
          <div className="err-icon">⚠</div>
          <div>{t("status.error")}</div>
          <button
            className="btn ghost retry-btn"
            type="button"
            onClick={() => { stopWatch(); void startWatch(); }}
          >
            {t("btn.retry")}
          </button>
        </div>
      ) : (
        <section className={isWindows() ? "st-grid" : "st-grid st-grid-machine"} aria-label="指标卡">
          {sample ? (
            isWindows() ? (
              <>
                <MemoryCard m={sample.memory} t={t} />
                <CpuCard cpu={sample.cpu} cores={sysInfo?.cores ?? 0} hist={cpuHist} t={t} />
                <DiskCard d={sample.disk} t={t} onClean={() => goCleanRef.current()} />
                {sample.battery && <BatteryCard b={sample.battery} t={t} />}
              </>
            ) : (
              <>
                <CpuCard
                  cpu={sample.cpu} cores={sysInfo?.cores ?? 0} hist={cpuHist} t={t}
                  m={sample.machine} split={machine ? { perf: machine.perfCores, eff: machine.effCores } : undefined}
                />
                {sample.machine && <GpuCard m={sample.machine} cores={machine?.gpuCores ?? 0} displays={machine?.displays.length ?? 0} hist={gpuHist} t={t} />}
                <MemoryCard m={sample.memory} t={t} />
                <DiskCard d={sample.disk} t={t} onClean={() => goCleanRef.current()} ssd={machine?.ssd} live={sample.machine} />
                {sample.machine && <NetCard m={sample.machine} hist={netHist} t={t} />}
                {sample.battery && <BatteryCard b={sample.battery} t={t} />}
              </>
            )
          ) : (
            Array.from({ length: isWindows() ? 4 : 6 }, (_, i) => <SkCard key={i} />)
          )}
        </section>
      )}

      {/* 「占用最多的应用」只在 Windows 首页：macOS 有「进程」页（设计 2026-10-28 §三.5）。 */}
      {isWindows() && <>
        <section className="st-listcard">
          <div ref={listHeadRef} className={`st-list-head${hl ? " hl" : ""}`}>
            <h3>{t("status.apps.title")}</h3>
            <TabsRoot value={sortBy} onValueChange={(v) => setSortBy(v as SortBy)}>
              <TabsList
                ref={segRef}
                aria-label="排序方式"
                className="relative ml-auto flex flex-none gap-[2px] rounded-[8px] bg-[var(--bg-subtle)] p-[2px]"
              >
                <span aria-hidden="true" className="cz-seg-thumb" style={segStyle} />
                <TabsTrigger value="mem" className={segTrigger}>{t("status.apps.byMemory")}</TabsTrigger>
                <TabsTrigger value="cpu" className={segTrigger}>{t("status.apps.byCpu")}</TabsTrigger>
              </TabsList>
            </TabsRoot>
          </div>
          <div className="st-cols num">
            <span className="c-app">{t("status.apps.appCol")}</span>
            <span className="c-mem">{t("status.apps.memCol")}</span>
            <span className="c-cpu">{t("status.apps.cpuCol")}</span>
            <span className="c-act" />
          </div>
          <section
            ref={listRef}
            className="st-list"
            aria-live="polite"
            onMouseEnter={() => setHover(true)}
            onMouseLeave={() => setHover(false)}
            onFocusCapture={() => setFocus(true)}
            onBlurCapture={() => setFocus(false)}
          >
            {error ? null : !sample ? (
              Array.from({ length: 3 }, (_, i) => <SkRow key={i} />)
            ) : rowsP.length === 0 ? (
              <div className="st-empty">{t("status.apps.empty")}</div>
            ) : (
              rowsP.map(({ item: r, key, leaving }) => {
                const quitted = leaving || (quit.done.has(r.key) && !quit.gone.has(r.key));
                const busy = quit.busy.has(r.path);
                return (
                  <Leave key={key} leaving={leaving}>
                    <div className={`st-row cz-fade-in${quitted ? " quitted" : ""}`} data-key={key}>
                      <div className="st-app">
                        <AppIcon path={r.path} box="st-ico">
                          <div className="st-ico" style={{ background: iconColor(r.name) }}>{r.name.charAt(0).toUpperCase()}</div>
                        </AppIcon>
                        <span className="st-name" title={`${r.name} · ${t("status.apps.procs", { n: r.procs })}`}>{r.name}</span>
                        <span className="st-procs">{t("status.apps.procs", { n: r.procs })}</span>
                      </div>
                      <span className="st-col st-mem num">{fmtMem(r.memory)}</span>
                      <span className="st-col st-cpu num">{fmtPct(r.cpu)}</span>
                      <span className="st-act">
                        {r.quit && !leaving && !quit.done.has(r.key) && (
                          <button key={busy ? "busy" : "idle"} className="st-quit cz-fade-in" disabled={busy} type="button" onClick={() => setQuitTarget(r)}>
                            {busy ? t("status.apps.quitting") : t("status.apps.quit")}
                          </button>
                        )}
                      </span>
                    </div>
                  </Leave>
                );
              })
            )}
          </section>
          <p className="st-footnote muted small">
            {sample ? t("status.footnote", { n: sample.procs.toLocaleString() }) : ""}
          </p>
        </section>

        <Dialog open={quitTarget !== null} onOpenChange={(v) => { if (!v) setQuitTarget(null); }}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{t("status.quit.title", { name: quitTarget?.name ?? "" })}</DialogTitle>
              <DialogDescription>
                {t("status.quit.body", { name: quitTarget?.name ?? "" })}
              </DialogDescription>
            </DialogHeader>
            <div className="mt-4 flex justify-end gap-2">
              <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
              {quitTarget && <Button onClick={() => void doQuit(quitTarget)}>{t("status.quit.ok")}</Button>}
            </div>
          </DialogContent>
        </Dialog>
      </>}
    </div>
  );
}
