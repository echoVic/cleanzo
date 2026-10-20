import { isWindows } from "../../platform";
import * as React from "react";
import { Root as TabsRoot, List as TabsList, Trigger as TabsTrigger } from "@radix-ui/react-tabs";
import { Channel } from "mygo-runtime";
import type { AppUsage, BatteryStatus, StatusSample, SystemInfo } from "../../mygo";
import { Button } from "../../ui/components/button";
import {
  Dialog, DialogClose, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "../../ui/components/dialog";
import { useT } from "../../ui/i18n-react";
import { errText, toast } from "../../shared";
import { statusApi } from "./api";
import { crashDumps, oversized } from "../list/crash-dumps";
import type { CrashDumpSummary } from "../../mygo";
import {
  GiB, MiB, SPARK_POINTS, emptyQuit, fmtMem, fmtPct, iconColor, pickAlerts, quitErrCode,
  refreshFrozenRows, sortApps, splitMem, syncQuitRows, trimSpark, uptimeParts,
  type PickedAlert, type QuitState, type SortBy,
} from "./format";

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
  const [usedNum, usedUnit] = splitMem(m.used);
  const pct = (n: number) => (m.used > 0 ? Math.max(0, Math.round((n / m.used) * 100)) : 0);
  return (
    <article className="mcard">
      <div className="mhead"><MemoryIcon /><span>{t("status.card.memory")}</span>{badge}</div>
      {m.total
        ? (
          <div className="mhero num">
            <b>{usedNum}</b><i>{usedUnit}</i><span className="mctx">/ {fmtMem(m.total)}</span>
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
          <ul className="mlegend num">
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

/** 30 点 0–100% 面积曲线；重开采样时缓冲清空，不足 2 点不画。 */
function SparkSvg({ hist }: { hist: number[] }) {
  if (hist.length < 2) return null;
  const step = 120 / (SPARK_POINTS - 1);
  const y = (v: number) => (28 - (Math.max(0, Math.min(100, v)) / 100) * 26).toFixed(1);
  const pts = hist.map((v, i) => `${(i * step).toFixed(1)},${y(v)}`);
  const line = `M${pts.join(" L")}`;
  const area = `${line} L${((hist.length - 1) * step).toFixed(1)},28 L0,28 Z`;
  return (
    <div className="st-spark" aria-hidden="true">
      <svg viewBox="0 0 120 28" preserveAspectRatio="none">
        <path fill="var(--accent-soft)" d={area} />
        <path fill="none" stroke="var(--accent)" strokeWidth={1.5} strokeLinejoin="round" d={line} />
      </svg>
    </div>
  );
}

function CpuCard({ cpu, cores, hist, t }: { cpu: StatusSample["cpu"]; cores: number; hist: number[]; t: T }) {
  const loads: Array<[string, string]> = [
    [t("status.cpu.load1"), cpu.load1.toFixed(1)],
    [t("status.cpu.load5"), cpu.load5.toFixed(1)],
    [t("status.cpu.load15"), cpu.load15.toFixed(1)],
  ];
  return (
    <article className="mcard">
      <div className="mhead"><CpuIcon /><span>{t("status.card.cpu")}</span></div>
      <div className="mhero num"><b>{Math.round(cpu.usage)}</b><i>%</i></div>
      <div className="mhcapt">{t("status.cpu.caption")}</div>
      <div className="mviz"><SparkSvg hist={hist} /></div>
      <div className="mloads num">
        {loads.map(([label, v], i) => <div key={i}><span>{label}</span><b>{v}</b></div>)}
      </div>
      <div className="mfoot num">{cores > 0 ? t("status.cpu.cores", { n: cores }) : "—"}</div>
    </article>
  );
}

function DiskCard({ d, t, onClean }: { d: StatusSample["disk"]; t: T; onClean: () => void }) {
  const usedPct = d.total ? Math.min(100, Math.round((d.used / d.total) * 100)) : 0;
  const [freeNum, freeUnit] = d.free > 0 ? splitMem(d.free) : ["—", ""];
  return (
    <article className="mcard">
      <div className="mhead"><DiskIcon /><span>{t("status.card.disk")}</span></div>
      <div className="mhero num"><b>{freeNum}</b><i>{freeUnit}</i><span className="mctx">{t("status.disk.avail")}</span></div>
      <div className="mhcapt">{t("status.disk.totalOnly", { total: fmtMem(d.total) })}</div>
      <div className="mviz"><div className="mbar"><i style={{ width: `${usedPct}%` }} /></div></div>
      <ul className="mlegend num">
        <li><span>{t("status.disk.used")}</span><b>{usedPct}%</b></li>
        <li><span>{t("status.disk.avail")}</span><b>{fmtMem(d.free)}</b></li>
      </ul>
      <div className="mfoot">
        <span>{t("status.disk.footHint")}</span>
        <button className="mlink" type="button" onClick={onClean}>{t("status.disk.clean")} ›</button>
      </div>
    </article>
  );
}

function BatteryCard({ b, t }: { b: BatteryStatus; t: T }) {
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
    <article className="mcard">
      <div className="mhead"><BatteryIcon /><span>{t("status.card.battery")}</span>{badge}</div>
      <div className="mhero num"><b>{b.percent}</b><i>%</i></div>
      <div className="mhcapt">{state}</div>
      <div className="mviz">
        <div className={`mbar${healthPct > 0 && healthPct < 80 ? " low" : ""}`}><i style={{ width: `${healthPct}%` }} /></div>
      </div>
      <ul className="mlegend num">
        <li><span>{t("status.battery.maxCap")}</span><b>{healthPct > 0 ? `${healthPct}%` : "—"}</b></li>
        <li><span>{t("status.battery.condition")}</span><b>{condVal}</b></li>
      </ul>
      <div className="mfoot num">{b.cycles > 0 ? t("status.battery.cycles", { n: b.cycles }) : "—"}</div>
    </article>
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

const segTrigger =
  "cursor-pointer rounded-[6px] px-3 py-1 text-xs leading-[18px] text-[var(--muted)] transition-colors " +
  "data-[state=active]:bg-[var(--card)] data-[state=active]:font-semibold data-[state=active]:text-[var(--fg)] " +
  "data-[state=active]:shadow-[0_1px_2px_rgba(0,0,0,0.1)] " +
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]";

export interface StatusPageProps {
  /** 当前是否停在系统状态视图：进入开 2s 采样流，离开关 Channel。 */
  active: boolean;
  /** 告警 / 磁盘卡的「去清理」：与点侧栏「垃圾清理」同一条路径（main.ts 注入）。 */
  goClean: () => void;
}

export function StatusPage({ active, goClean }: StatusPageProps) {
  const t = useT();
  const [sample, setSample] = React.useState<StatusSample | null>(null);
  const [sysInfo, setSysInfo] = React.useState<SystemInfo | null>(null);
  const [error, setError] = React.useState(false);
  const [cpuHist, setCpuHist] = React.useState<number[]>([]);
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

  // 异步循环 / 事件里读最新值，避免闭包捕获旧 state。
  const runRef = React.useRef(0);
  const chanRef = React.useRef<{ close(): void } | null>(null);
  const stoppedRef = React.useRef(false);
  const activeRef = React.useRef(active);
  const infoSeqRef = React.useRef(0);
  const sysInfoRef = React.useRef(sysInfo);
  sysInfoRef.current = sysInfo;
  const goCleanRef = React.useRef(goClean);
  goCleanRef.current = goClean;
  /** 冻结期间的行集合与顺序（只更新数字）；重开采样时清空。 */
  const frozenRowsRef = React.useRef<AppUsage[]>([]);
  const listHeadRef = React.useRef<HTMLDivElement | null>(null);
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

  const startWatch = React.useCallback(async () => {
    const myRun = ++runRef.current;
    stoppedRef.current = false;
    setError(false);
    setSample(null);
    setCpuHist([]);
    setQuit(emptyQuit());
    frozenRowsRef.current = [];
    void loadInfo();
    const ch = new Channel<StatusSample>();
    chanRef.current = ch;
    try {
      const done = statusApi.watch(ch);
      for await (const s of ch) {
        if (runRef.current !== myRun) continue; // 已切走 / 重开：丢弃旧采样
        setSample(s);
        setCpuHist((prev) => trimSpark(prev, s.cpu.usage));
      }
      await done;
    } catch (e) {
      if (runRef.current === myRun && !stoppedRef.current) {
        // 本轮采样已经结束；重试按钮等价于 stopWatch + startWatch 重建通道
        setError(true);
        setSample(null);
        setCpuHist([]);
      }
    } finally {
      if (chanRef.current === ch) chanRef.current = null;
    }
  }, [loadInfo]);

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

  // ---- 「查看占内存的应用」联动：切按内存 + 滚动到列表 + 高亮 1s ----

  const viewApps = React.useCallback(() => {
    setSortBy("mem");
    listHeadRef.current?.scrollIntoView({ behavior: "auto", block: "start" });
    setHl(true);
    if (hlTimerRef.current) window.clearTimeout(hlTimerRef.current);
    hlTimerRef.current = window.setTimeout(() => setHl(false), 1000);
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

  const machineParts: string[] = [];
  if (sysInfo) {
    if (sysInfo.chip) machineParts.push(sysInfo.chip);
    if (sysInfo.memory >= GiB) machineParts.push(t("status.machine.mem", { size: `${Math.round(sysInfo.memory / GiB)} GB` }));
    else if (sysInfo.memory) machineParts.push(t("status.machine.mem", { size: `${Math.round(sysInfo.memory / MiB)} MB` }));
    if (sysInfo.osVersion) machineParts.push(t("status.osVersion", { version: sysInfo.osVersion }));
    const up = sysInfo.bootTime > 0 ? uptimeParts(sysInfo.bootTime, Date.now() / 1000) : null;
    if (up) {
      machineParts.push(
        up.days >= 1 ? t("status.uptime.days", { d: up.days, h: up.hours })
          : up.hours >= 1 ? t("status.uptime.hours", { h: up.hours, m: up.mins })
            : t("status.uptime.mins", { m: up.mins }),
      );
    }
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

  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h2>{t("nav.status")}</h2>
          <p className="muted">{t("status.sub")}</p>
          {machineParts.length > 0 && <p className="st-machine num">{machineParts.join(" · ")}</p>}
        </div>
      </header>

      {/* 与原生 #st-alerts 一致：告警始终占一个区块位（为空时也保留节奏），多条告警紧贴排列。 */}
      <div className="st-alerts">
        {alerts.map((a: PickedAlert) => (
          <div key={a.kind} className={`st-callout ${a.kind === "memCrit" ? "crit" : "warn"}`} role="status">
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
                onClick={() => (a.action === "apps" ? viewApps() : goCleanRef.current())}
              >
                {a.action === "apps" ? t("status.alert.viewApps") : t("status.disk.clean")}
              </button>
            )}
          </div>
        ))}
        {bigDumps[0] && (
          <div key="crashDumps" className="st-callout warn" role="status">
            <CalloutIcon />
            <div className="st-callout-body">
              {t("status.alert.crashDumps", { app: bigDumps[0].app, size: fmtMem(bigDumps[0].size) })}
            </div>
            <button className="btn ghost small" type="button" onClick={() => goCleanRef.current()}>
              {t("status.disk.clean")}
            </button>
          </div>
        )}
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
        <section className="st-grid" aria-label="指标卡">
          {sample ? (
            <>
              <MemoryCard m={sample.memory} t={t} />
              <CpuCard cpu={sample.cpu} cores={sysInfo?.cores ?? 0} hist={cpuHist} t={t} />
              <DiskCard d={sample.disk} t={t} onClean={() => goCleanRef.current()} />
              {sample.battery && <BatteryCard b={sample.battery} t={t} />}
            </>
          ) : (
            Array.from({ length: 4 }, (_, i) => <SkCard key={i} />)
          )}
        </section>
      )}

      <section className="st-listcard">
        <div ref={listHeadRef} className={`st-list-head${hl ? " hl" : ""}`}>
          <h3>{t("status.apps.title")}</h3>
          <TabsRoot value={sortBy} onValueChange={(v) => setSortBy(v as SortBy)}>
            <TabsList
              aria-label="排序方式"
              className="ml-auto flex flex-none gap-[2px] rounded-[8px] bg-[var(--bg-subtle)] p-[2px]"
            >
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
          className="st-list"
          aria-live="polite"
          onMouseEnter={() => setHover(true)}
          onMouseLeave={() => setHover(false)}
          onFocusCapture={() => setFocus(true)}
          onBlurCapture={() => setFocus(false)}
        >
          {error ? null : !sample ? (
            Array.from({ length: 3 }, (_, i) => <SkRow key={i} />)
          ) : sample.apps.length === 0 ? (
            <div className="st-empty">{t("status.apps.empty")}</div>
          ) : (
            rows.map((r) => {
              const quitted = quit.done.has(r.key) && !quit.gone.has(r.key);
              return (
                <div key={r.key} className={`st-row${quitted ? " quitted" : ""}`}>
                  <div className="st-app">
                    <div className="st-ico" style={{ background: iconColor(r.name) }}>{r.name.charAt(0).toUpperCase()}</div>
                    <span className="st-name" title={`${r.name} · ${t("status.apps.procs", { n: r.procs })}`}>{r.name}</span>
                    <span className="st-procs">{t("status.apps.procs", { n: r.procs })}</span>
                  </div>
                  <span className="st-col st-mem num">{fmtMem(r.memory)}</span>
                  <span className="st-col st-cpu num">{fmtPct(r.cpu)}</span>
                  <span className="st-act">
                    {r.quit && !quit.done.has(r.key) && (
                      quit.busy.has(r.path)
                        ? <button className="st-quit" disabled type="button">{t("status.apps.quitting")}</button>
                        : <button className="st-quit" type="button" onClick={() => setQuitTarget(r)}>{t("status.apps.quit")}</button>
                    )}
                  </span>
                </div>
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
    </div>
  );
}
