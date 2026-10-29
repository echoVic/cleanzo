// 菜单栏读数的面板（设计 §四）：大数字、一句话诊断、一分钟曲线、明细、占用最多的 App 和底部链接。只在面板开着时读。
import * as React from "react";
import { Channel } from "mygo-runtime";
import type { GrowthApp, GrowthState, MachineInfo, MenuBarTarget, ProcessRow, ProcessSample, StatusSample, SystemInfo } from "../mygo";
import { useT } from "../ui/i18n-react";
import { human } from "../shared";
import { AppIcon } from "../react/icons/AppIcon";
import { SparkSvg } from "../react/status/Spark";
import { fmtMem, fmtPct, iconColor, quitErrCode, trimSpark } from "../react/status/format";
import { est, pushCpu } from "../react/process/format";
import { panelApi } from "./api";
import {
  PANEL, QUIT_CONFIRM_MS, chartMax, chartPoint, chartTone, confirming, detailRows, diagnosis, emptyBook, growthTop, hero,
  listFrozen, memShares, quitPending, revertConfirms, subLine, syncBook, topRows, withStep,
  type PanelItem, type QuitBook, type QuitStep,
} from "./format";

type T = ReturnType<typeof useT>;

/** 首字母圆标的字：点目录跳过开头的点。 */
function initial(name: string): string {
  return (name.replace(/^\.+/, "") || name).charAt(0).toUpperCase();
}

/** 条里的一段：从 left% 起、宽 width%，用 transform 定位（同首页内存条）。 */
const seg = (left: number, width: number) => `translateX(${left}%) scaleX(${width / 100})`;

/** 一行的退出按钮（设计 §四.5）：悬停出现「退出」，点一下变「确认退出」，再点才退；还没退出时「强制退出」也点两下。 */
function QuitControl({ step, t, onQuit, onForce }: { step: QuitStep; t: T; onQuit: () => void; onForce: () => void }) {
  switch (step) {
    case "confirm":
      return <button type="button" className="mb-quit on" onClick={onQuit}>{t("menubar.panel.confirmQuit")}</button>;
    case "quitting":
      return <button type="button" className="mb-quit on" disabled>{t("status.apps.quitting")}</button>;
    case "forcing":
      return <button type="button" className="mb-quit on danger" disabled>{t("status.apps.quitting")}</button>;
    case "stuck":
      return (
        <>
          <span className="mb-state">{t("process.stuck")}</span>
          <button type="button" className="mb-quit on danger" onClick={onForce}>{t("process.force")}</button>
        </>
      );
    case "forceConfirm":
      return (
        <>
          <span className="mb-state">{t("process.stuck")}</span>
          <button type="button" className="mb-quit on danger" onClick={onForce}>{t("menubar.panel.confirmForce")}</button>
        </>
      );
    case "failed":
      return (
        <>
          <span className="mb-state">{t("menubar.panel.quitFail")}</span>
          <button type="button" className="mb-quit" onClick={onQuit}>{t("status.apps.quit")}</button>
        </>
      );
    case "done":
      return <span className="mb-state">{t("menubar.panel.quitted")}</span>;
    default:
      return <button type="button" className="mb-quit" onClick={onQuit}>{t("status.apps.quit")}</button>;
  }
}

export interface PanelAppRowProps {
  row: ProcessRow;
  by: "cpu" | "mem";
  step: QuitStep;
  t: T;
  onName: () => void;
  onQuit: () => void;
  onForce: () => void;
}

/** 列表的一行：图标、名字（点开「进程」页并定位）、退出、数值；系统进程标「系统」，估算值带「≈」。 */
export function PanelAppRow({ row, by, step, t, onName, onQuit, onForce }: PanelAppRowProps) {
  return (
    <div className={`mb-app${step === "done" ? " done" : ""}`} data-key={row.key}>
      <AppIcon path={row.kind === "app" ? row.path : ""} box="mb-ico">
        <span className={`mb-ico${row.kind === "app" ? "" : " sys"}`} style={row.kind === "app" ? { background: iconColor(row.name) } : undefined} aria-hidden="true">
          {initial(row.name)}
        </span>
      </AppIcon>
      <button type="button" className="mb-name" title={row.name} onClick={onName}>
        <span>{row.name}</span>
        {row.kind === "system" && <em>{t("process.system")}</em>}
      </button>
      {(row.quit || step === "done") && <QuitControl step={step} t={t} onQuit={onQuit} onForce={onForce} />}
      <b className="mb-val">{est(by === "cpu" ? fmtPct(row.cpu) : fmtMem(row.memory), row.estimated)}</b>
    </div>
  );
}

/** 磁盘面板「涨得最快」的一行：点名字打开「进程」页的磁盘占用标签并定位。 */
export function PanelGrowthRow({ app, onName }: { app: GrowthApp; onName: () => void }) {
  const week = app.week!;
  return (
    <div className="mb-app" data-key={app.key}>
      <AppIcon path={app.appPath} box="mb-ico">
        <span className="mb-ico" style={{ background: iconColor(app.name) }} aria-hidden="true">{initial(app.name)}</span>
      </AppIcon>
      <button type="button" className="mb-name" title={app.name} onClick={onName}><span>{app.name}</span></button>
      <b className="mb-val">{`${week.atLeast ? "≥ " : "+"}${human(week.bytes)}`}</b>
    </div>
  );
}

export function PanelApp({ item }: { item: PanelItem }) {
  const t = useT();
  const listed = item === "cpu" || item === "mem";
  const by = item === "mem" ? "mem" : "cpu";
  const [s, setS] = React.useState<StatusSample | null>(null);
  const [hist, setHist] = React.useState<number[]>([]);
  const [gpuHist, setGpuHist] = React.useState<number[]>([]);
  const [proc, setProc] = React.useState<ProcessSample | null>(null);
  const [cpuHist, setCpuHist] = React.useState<number[]>([]);
  const [info, setInfo] = React.useState<MachineInfo | null>(null);
  const [sys, setSys] = React.useState<SystemInfo | null>(null);
  const [growth, setGrowth] = React.useState<GrowthState | null>(null);
  const [failed, setFailed] = React.useState(false);
  const [hover, setHover] = React.useState(false);
  const [book, setBook] = React.useState<QuitBook>(emptyBook);
  const shownRef = React.useRef<ProcessRow[]>([]);

  // 读数：第一条约半秒后到，之后每 2 秒一条；面板关上时页面销毁、通道关闭，Go 不再读。
  React.useEffect(() => {
    let live = true;
    const ch = new Channel<StatusSample>();
    setFailed(false);
    void (async () => {
      try {
        const done = panelApi.watch(item, ch);
        for await (const v of ch) {
          if (!live) continue;
          setS(v);
          const p = chartPoint(item, v);
          if (p !== null) setHist((h) => trimSpark(h, p));
          const gpu = v.machine?.gpu ?? -1;
          if (item === "gpu" && gpu >= 0) setGpuHist((h) => pushCpu(h, gpu));
        }
        await done;
      } catch {
        if (live) setFailed(true);
      }
    })();
    return () => { live = false; ch.close(); };
  }, [item]);

  // CPU、内存面板：和「进程」页同一套进程读数，给诊断和列表用。
  React.useEffect(() => {
    if (!listed) return;
    let live = true;
    const ch = new Channel<ProcessSample>();
    void (async () => {
      try {
        const done = panelApi.watchProcesses(ch);
        for await (const v of ch) {
          if (!live) continue;
          setProc(v);
          setCpuHist((h) => pushCpu(h, v.cpu));
          setBook((b) => syncBook(b, v.rows));
        }
        await done;
      } catch { /* 读不到进程：诊断和列表留着占位 */ }
    })();
    return () => { live = false; ch.close(); };
  }, [listed]);

  // 规格：打开时读一次。
  React.useEffect(() => {
    if (item === "cpu" || item === "gpu" || item === "disk") panelApi.machineInfo().then(setInfo, () => {});
    if (item === "cpu") panelApi.info().then(setSys, () => {});
  }, [item]);

  // 磁盘面板：增长记录，打开时读一次，增长有变化再读。
  React.useEffect(() => {
    if (item !== "disk") return;
    const load = () => { panelApi.growthState().then(setGrowth, () => setGrowth(null)); };
    load();
    return panelApi.onGrowthUpdated(load);
  }, [item]);

  // 退出还在等待：让 Go 别在点到别处时关面板（设计 §四.5）。
  const pending = quitPending(book);
  React.useEffect(() => {
    if (!pending) return;
    void panelApi.hold(true).catch(() => {});
    return () => { void panelApi.hold(false).catch(() => {}); };
  }, [pending]);

  // 「确认退出」「确认强制退出」3 秒没点第二下就退回。
  React.useEffect(() => {
    if (!confirming(book)) return;
    const id = window.setTimeout(() => setBook(revertConfirms), QUIT_CONFIRM_MS);
    return () => window.clearTimeout(id);
  }, [book]);

  const stepOf = (key: string): QuitStep => book.steps.get(key) ?? "idle";
  const setStep = (key: string, step: QuitStep) => setBook((b) => withStep(b, key, step));
  const quit = async (row: ProcessRow) => {
    const step = stepOf(row.key);
    if (step === "idle" || step === "failed") { setStep(row.key, "confirm"); return; }
    if (step !== "confirm") return;
    setStep(row.key, "quitting");
    try {
      setStep(row.key, (await panelApi.quitApp(row.path)) ? "done" : "stuck");
    } catch (e) {
      const code = quitErrCode(e);
      setStep(row.key, code === "not-running" ? "done" : code === "cannot-quit" ? "stuck" : "failed");
    }
  };
  const force = async (row: ProcessRow) => {
    const step = stepOf(row.key);
    if (step === "stuck") { setStep(row.key, "forceConfirm"); return; }
    if (step !== "forceConfirm") return;
    setStep(row.key, "forcing");
    try {
      setStep(row.key, (await panelApi.forceQuitApp(row.path)) ? "done" : "stuck");
    } catch (e) {
      setStep(row.key, quitErrCode(e) === "not-running" ? "done" : "stuck");
    }
  };
  const open = (target: MenuBarTarget) => { void panelApi.open(target).catch(() => {}); };

  // 列表在指针停留、确认和退出期间冻结顺序（设计 §四.5）。
  const rows = proc && listed ? topRows(proc, by, shownRef.current, listFrozen(hover, book)) : [];
  if (proc && listed) shownRef.current = rows;
  const h = hero(item, s);
  const diag = diagnosis(item, { s, proc, cpuHist, gpuHist, growth }, t);
  const sub = subLine(item, s, info, sys, t);
  const details = detailRows(item, s, info, t);
  const shares = item === "mem" && s ? memShares(s.memory) : null;
  const grown = item === "disk" ? growthTop(growth) : [];
  const pressure = item === "mem" ? s?.memory.pressure ?? 0 : 0;

  return (
    <main className="mb-panel">
      <header className="mb-head">
        <h1>{t(PANEL[item].title)}</h1>
        {(pressure === 2 || pressure === 4) && (
          <em className={`mb-badge${pressure === 4 ? " crit" : ""}`}>{t(pressure === 4 ? "status.mem.pressureCrit" : "status.mem.pressureWarn")}</em>
        )}
        <span className="mb-hero"><b>{h.value}</b>{h.unit && <small>{h.unit}</small>}</span>
      </header>
      {sub && <p className="mb-sub">{sub}</p>}
      {failed
        ? <p className="mb-diag crit" role="alert">{t("menubar.panel.error")}</p>
        : diag
          ? <p className={`mb-diag ${diag.tone}`}>{diag.text}</p>
          : <div className="mb-diag mb-skel" aria-hidden="true" />}
      <div className="mb-chart"><SparkSvg hist={hist} max={chartMax(item, hist)} tone={chartTone(item, s)} /></div>
      {shares && (
        <div className="mb-stack" aria-hidden="true">
          <i style={{ transform: seg(0, shares.app), background: "var(--seg-app)" }} />
          <i style={{ transform: seg(shares.app, shares.wired), background: "var(--seg-wired)" }} />
          <i style={{ transform: seg(shares.app + shares.wired, shares.compressed), background: "var(--seg-compressed)" }} />
        </div>
      )}
      {details.length > 0 && (
        <ul className="mb-rows">
          {details.map((r) => (
            <li key={r.key}>
              <span className="k">{r.dot && <i className="dot" style={{ background: r.dot }} />}{r.label}</span>
              {r.bar !== undefined && <span className="mb-bar"><i style={{ transform: `scaleX(${Math.min(100, Math.max(0, r.bar)) / 100})` }} /></span>}
              <b>{r.value}</b>
            </li>
          ))}
        </ul>
      )}
      {listed && (
        <section className="mb-list" onPointerEnter={() => setHover(true)} onPointerLeave={() => { setHover(false); setBook(revertConfirms); }}>
          <div className="mb-sec">
            <span>{t(item === "cpu" ? "menubar.panel.topCpu" : "menubar.panel.topMem")}</span>
            <span>{item === "cpu" ? t("menubar.panel.perCore") : ""}</span>
          </div>
          {proc
            ? rows.map((r) => (
              <PanelAppRow
                key={r.key}
                row={r}
                by={by}
                step={stepOf(r.key)}
                t={t}
                onName={() => open({ page: "process", tab: item, key: r.key })}
                onQuit={() => void quit(r)}
                onForce={() => void force(r)}
              />
            ))
            : Array.from({ length: 3 }, (_, i) => <div key={i} className="mb-skel-row" aria-hidden="true" />)}
        </section>
      )}
      {grown.length > 0 && (
        <section className="mb-list">
          <div className="mb-sec"><span>{t("menubar.panel.topGrowth")}</span><span>{t("menubar.panel.week")}</span></div>
          {grown.map((a) => <PanelGrowthRow key={a.key} app={a} onName={() => open({ page: "process", tab: "disk", key: a.key })} />)}
        </section>
      )}
      <footer className="mb-foot">
        <span className="mb-links">
          {PANEL[item].links.map((l) => (
            <button key={l.key} type="button" className="mb-link" onClick={() => open(l.target)}>{t(l.key)} ›</button>
          ))}
        </span>
        <span className="mb-every">{t("menubar.panel.every")}</span>
      </footer>
    </main>
  );
}
