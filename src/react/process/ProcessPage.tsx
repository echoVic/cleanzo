import * as React from "react";
import { Pause, Play } from "lucide-react";
import { Root as TabsRoot, List as TabsList, Trigger as TabsTrigger } from "@radix-ui/react-tabs";
import { Channel } from "mygo-runtime";
import type { GrowthSummary, ProcessChild, ProcessRow, ProcessSample } from "../../mygo";
import { Button } from "../../ui/components/button";
import {
  Dialog, DialogClose, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "../../ui/components/dialog";
import { segTrigger } from "../../ui/components/seg";
import { useT } from "../../ui/i18n-react";
import { Collapse } from "../../ui/motion/Collapse";
import { useIndicator } from "../../ui/motion/indicator";
import { isWindows } from "../../platform";
import { errText, focusNextAfter, human, toast } from "../../shared";
import { emptyQuit, fmtMem, fmtPct, iconColor, iconFg, quitErrCode, syncQuitRows, type QuitState } from "../status/format";
import { growthApi } from "../growth/api";
import { GrowthPanel, type GrowthHandlers } from "../growth/GrowthPage";
import { processApi } from "./api";
import {
  diagnose, est, growthTags, pruneStuck, pushCpu, refreshFrozen, rowTag, shouldWatch, sortChildren, sortRows,
  type GrowthTag, type ProcessRequest, type ProcessTab,
} from "./format";
import { AppIcon } from "../icons/AppIcon";

type T = ReturnType<typeof useT>;

/** 首字母圆标的字：点目录跳过开头的点。 */
function initial(name: string): string {
  return (name.replace(/^\.+/, "") || name).charAt(0).toUpperCase();
}

/** 增长小标签的文字：「磁盘 7 天 +11.2 GB」；量到一半的写「至少」。 */
function tagText(tag: GrowthTag, t: T): string {
  const size = `+${human(tag.bytes)}`;
  return t(tag.window === "day" ? "process.tag.day" : "process.tag.week", { size: tag.atLeast ? t("growth.atLeast", { size }) : size });
}

export interface ProcessRowViewProps {
  row: ProcessRow;
  /** 展开后的进程跟着当前标签排。 */
  sortBy: "cpu" | "mem";
  open: boolean;
  tag: GrowthTag | null;
  /** 正在退出或强制退出。 */
  busy: boolean;
  /** 正常退出后还在跑：显示「还没退出」和「强制退出」。 */
  stuck: boolean;
  /** 已经退出：置灰、去掉按钮。 */
  done: boolean;
  /** 菜单栏面板点名字进来：高亮这一行 900ms。 */
  hl?: boolean;
  t: T;
  onToggle: () => void;
  onQuit: () => void;
  onForce: () => void;
  onTag: (key: string) => void;
}

/** 数字列：估算值另加一个类。 */
const numClass = (estimated: boolean) => `st-col num${estimated ? " pr-est" : ""}`;

/** 展开后的进程列表。单独成组件：Collapse 挂上内容时才排序、生成，收着的行不做这些。 */
function ProcessChildren({ items, sortBy }: { items: readonly ProcessChild[]; sortBy: "cpu" | "mem" }) {
  return (
    <ul className="pr-children">
      {sortChildren(items, sortBy).map((c) => (
        <li key={c.name} className="pr-child">
          <span className="pr-child-name" title={c.name}>{c.count > 1 ? `${c.name} × ${c.count}` : c.name}</span>
          <span className={numClass(c.estimated)}>{est(fmtPct(c.cpu), c.estimated)}</span>
          <span className={numClass(c.estimated)}>{est(fmtMem(c.memory), c.estimated)}</span>
          <span className="st-act pr-act" />
        </li>
      ))}
    </ul>
  );
}

/** 一行：App、命令行程序或系统程序；有不止一个进程时能展开，同名的合成一条。 */
export function ProcessRowView({ row, sortBy, open, tag, busy, stuck, done, hl, t, onToggle, onQuit, onForce, onTag }: ProcessRowViewProps) {
  const expandable = row.children.length > 1 || (row.children[0]?.count ?? 0) > 1;
  const label = (
    <>
      <span className="pr-chev" aria-hidden="true">{expandable ? (open ? "▾" : "›") : ""}</span>
      <AppIcon path={row.kind === "app" ? row.path : ""} box="st-ico">
        <span className={`st-ico${row.kind === "app" ? "" : " pr-ico-sys"}`} style={row.kind === "app" ? { background: iconColor(row.name), color: iconFg(iconColor(row.name)) } : undefined} aria-hidden="true">
          {initial(row.name)}
        </span>
      </AppIcon>
      <span className="st-name" title={row.name}>{row.name}</span>
      {row.procs > 1 && <span className="st-procs">{t("status.apps.procs", { n: row.procs })}</span>}
      {row.kind === "system" && <span className="pr-sys">{t("process.system")}</span>}
    </>
  );
  return (
    <div className={`pr-item${done ? " quitted" : ""}${hl ? " pr-hl" : ""}`} data-key={row.key}>
      <div className="st-row pr-row">
        <div className="pr-app">
          {expandable ? (
            <button type="button" className="pr-toggle" aria-expanded={open} onClick={onToggle}>{label}</button>
          ) : (
            <div className="pr-toggle">{label}</div>
          )}
          {tag && <button type="button" className="pr-tag" onClick={() => onTag(tag.key)}>{tagText(tag, t)}</button>}
        </div>
        <span className={numClass(row.estimated)}>{est(fmtPct(row.cpu), row.estimated)}</span>
        <span className={numClass(row.estimated)}>{est(fmtMem(row.memory), row.estimated)}</span>
        <span className="st-act pr-act">
          {row.quit && !done && (stuck ? (
            <>
              <span className="pr-stuck">{t("process.stuck")}</span>
              <button type="button" className="pr-force" aria-disabled={busy || undefined} onClick={() => { if (!busy) onForce(); }} aria-label={t("process.forceName", { name: row.name })}>{t("process.force")}</button>
            </>
          ) : (
            <button type="button" className="st-quit" aria-disabled={busy || undefined} onClick={() => { if (!busy) onQuit(); }} aria-label={t("status.apps.quitName", { name: row.name })}>
              {busy ? t("status.apps.quitting") : t("status.apps.quit")}
            </button>
          ))}
        </span>
      </div>
      {expandable && (
        <Collapse open={open}>
          <ProcessChildren items={row.children} sortBy={sortBy} />
        </Collapse>
      )}
    </div>
  );
}

/** 进程页的显示状态、访问次数、打开请求和磁盘占用标签的导航回调。 */
export interface ProcessPageProps extends GrowthHandlers {
  /** 是否正显示本视图：离开就关采样通道。 */
  active: boolean;
  /** 每次进入视图递增：标签回到 CPU（或请求的标签），开关回到「在走」。 */
  visit: number;
  /** main.ts 的打开请求；nonce 变了才执行。 */
  request: ProcessRequest;
}

/** 进程页：CPU、内存两个实时标签，加上磁盘占用（占用增长的列表）。spec 2026-10-26。 */
export function ProcessPage({ active, visit, request, goLeftovers, goUninstall }: ProcessPageProps) {
  const t = useT();
  const [tab, setTab] = React.useState<ProcessTab>("cpu");
  const [running, setRunning] = React.useState(true);
  const [hidden, setHidden] = React.useState(() => typeof document !== "undefined" && document.hidden);
  const [sample, setSample] = React.useState<ProcessSample | null>(null);
  const [error, setError] = React.useState(false);
  const [retry, setRetry] = React.useState(0);
  const [cpuHist, setCpuHist] = React.useState<number[]>([]);
  const [open, setOpen] = React.useState<string | null>(null);
  const [quit, setQuit] = React.useState<QuitState>(emptyQuit());
  const [stuck, setStuck] = React.useState<ReadonlySet<string>>(new Set());
  const [quitTarget, setQuitTarget] = React.useState<ProcessRow | null>(null);
  const [forceTarget, setForceTarget] = React.useState<ProcessRow | null>(null);
  const [growth, setGrowth] = React.useState<GrowthSummary | null>(null);
  const [focus, setFocus] = React.useState<{ key: string | null; nonce: number }>({ key: null, nonce: 0 });
  /** CPU、内存标签里要定位的行（菜单栏面板点名字进来）；hl 是正在高亮的行。 */
  const [rowFocus, setRowFocus] = React.useState<string | null>(null);
  const [hl, setHl] = React.useState<string | null>(null);
  const [seen, setSeen] = React.useState({ visit: -1, nonce: 0 });
  const frozenRef = React.useRef<ProcessRow[]>([]);
  const segRef = React.useRef<HTMLDivElement>(null);
  const segStyle = useIndicator(segRef, '[data-state="active"]');

  // 每次进入：开关回到「在走」，清掉上次的读数和退出状态；有新的打开请求就按它切标签、定位行，否则回到 CPU。
  // 在渲染时调整而不是在 effect 里，采样通道第一次就按正确的标签决定开不开。
  if (active && seen.visit !== visit) {
    const fresh = request.nonce !== seen.nonce;
    setSeen({ visit, nonce: request.nonce });
    setTab(fresh && request.tab ? request.tab : "cpu");
    setRunning(true);
    setSample(null);
    setOpen(null);
    setQuit(emptyQuit());
    setStuck(new Set());
    setQuitTarget(null);
    setForceTarget(null);
    frozenRef.current = [];
    setRowFocus(null);
    setHl(null);
    if (fresh && request.focusKey) {
      const key = request.focusKey;
      // 磁盘占用标签交给 GrowthPanel 定位；CPU、内存标签等这一行出现在表里再定位。
      if (request.tab === "disk") setFocus((f) => ({ key, nonce: f.nonce + 1 }));
      else setRowFocus(key);
    }
  }

  React.useEffect(() => {
    if (!active) {
      setQuitTarget(null);
      setForceTarget(null);
    }
  }, [active]);

  // 窗口隐藏时停采样，回来接着按开关走。
  React.useEffect(() => {
    const onVis = () => setHidden(document.hidden);
    document.addEventListener("visibilitychange", onVis);
    return () => document.removeEventListener("visibilitychange", onVis);
  }, []);

  // 只在页面显示、在 CPU 或内存标签、没暂停、窗口没隐藏时开通道；关通道 Go 侧就不再读进程、不再调用 ps。
  const watching = !isWindows() && shouldWatch({ active, tab, running, hidden });
  React.useEffect(() => {
    if (!watching) return;
    let live = true;
    let ch: Channel<ProcessSample> | null = null;
    setError(false);
    setCpuHist([]);
    void (async () => {
      try {
        ch = new Channel<ProcessSample>();
        const done = processApi.watch(ch);
        for await (const s of ch) {
          if (!live) continue; // 已关通道：丢弃迟到的采样
          setSample(s);
          setCpuHist((h) => pushCpu(h, s.cpu));
        }
        await done;
      } catch {
        if (live) setError(true);
      }
    })();
    return () => { live = false; ch?.close(); };
  }, [watching, retry]);

  // 磁盘占用标签上的数字和行上的小标签：每次进入读一次，增长有变化时再读。
  const loadGrowth = React.useCallback(() => {
    growthApi.summary().then(setGrowth, () => setGrowth(null));
  }, []);
  React.useEffect(() => { if (active) loadGrowth(); }, [active, visit, loadGrowth]);
  React.useEffect(() => (active ? growthApi.onUpdated(() => loadGrowth()) : undefined), [active, loadGrowth]);

  // 离开磁盘占用标签就放下定位请求，回来时不再高亮旧的那一行。
  React.useEffect(() => {
    if (tab !== "disk") setFocus((f) => (f.key ? { key: null, nonce: f.nonce } : f));
  }, [tab]);

  // 每轮采样后对账：退出过的行消失记 gone、同 key 再现恢复；「强制退出」只留给还在跑的。
  React.useEffect(() => {
    if (!sample) return;
    setQuit((q) => syncQuitRows(q, new Set(sample.rows.map((r) => r.key))));
    setStuck((s) => pruneStuck(s, sample.rows));
  }, [sample]);

  const markDone = (row: ProcessRow) => {
    const rowEl = document.querySelector(`[data-key="${CSS.escape(row.key)}"]`);
    const focusInRow = !!rowEl && rowEl.contains(document.activeElement);
    setQuit((q) => ({ ...q, done: new Set(q.done).add(row.key) }));
    setStuck((s) => { const next = new Set(s); next.delete(row.path); return next; });
    // done 会让该行的退出按钮从 DOM 移除；若焦点在该行内，重渲染后回退到下一个可聚焦元素。
    if (focusInRow) requestAnimationFrame(() => focusNextAfter(rowEl));
  };
  const setBusy = (path: string, on: boolean) => setQuit((q) => {
    const busy = new Set(q.busy);
    if (on) busy.add(path); else busy.delete(path);
    return { ...q, busy };
  });

  // 「还没退出」会把退出按钮换成强退按钮：焦点还在这一行时，延续到强退按钮，不丢给 body。
  // 新按钮可能晚一帧才提交，逐帧重试几次。
  const focusRowForce = (row: ProcessRow) => {
    const keySel = `[data-key="${CSS.escape(row.key)}"]`;
    const rowEl = document.querySelector(keySel);
    if (!rowEl?.contains(document.activeElement)) return;
    let tries = 0;
    const tryFocus = () => {
      const btn = document.querySelector<HTMLElement>(`${keySel} .pr-force`);
      if (btn) { btn.focus({ preventScroll: true }); return; }
      if (tries++ < 6) requestAnimationFrame(tryFocus);
    };
    requestAnimationFrame(tryFocus);
  };

  // 正常退出：和系统状态一样；5 秒后还在跑（或被拒绝），这一行改成「还没退出」+「强制退出」。
  const doQuit = async (row: ProcessRow) => {
    setQuitTarget(null);
    setBusy(row.path, true);
    try {
      if (await processApi.quitApp(row.path)) {
        toast(t("status.toast.quitDone", { name: row.name }));
        markDone(row);
      } else {
        toast(t("status.toast.quitPending", { name: row.name }));
        setStuck((s) => new Set(s).add(row.path));
        focusRowForce(row);
      }
    } catch (e) {
      const code = quitErrCode(e);
      if (code === "not-running") {
        toast(t("status.toast.notRunning", { name: row.name }));
        markDone(row);
      } else if (code === "cannot-quit") {
        toast(t("status.toast.cannotQuit", { name: row.name }), "err");
        setStuck((s) => new Set(s).add(row.path));
        focusRowForce(row);
      } else {
        toast(`${t("status.error")}：${errText(e)}`, "err");
      }
    } finally {
      setBusy(row.path, false);
    }
  };

  // 强制退出：只在确认框里点了才调用。
  const doForce = async (row: ProcessRow) => {
    setForceTarget(null);
    setBusy(row.path, true);
    try {
      if (await processApi.forceQuitApp(row.path)) {
        toast(t("process.toast.forceDone", { name: row.name }));
        markDone(row);
      } else {
        toast(t("process.toast.forceFail", { name: row.name }), "err");
      }
    } catch (e) {
      if (quitErrCode(e) === "not-running") {
        toast(t("status.toast.notRunning", { name: row.name }));
        markDone(row);
      } else {
        toast(t("process.toast.forceFail", { name: row.name }), "err");
      }
    } finally {
      setBusy(row.path, false);
    }
  };

  const sortBy: "cpu" | "mem" = tab === "mem" ? "mem" : "cpu";
  // 只在确认框开着、正在退出时冻结行和顺序；鼠标停在列表上不冻结，想停下来看用「暂停」（spec §三）。
  const freezing = quit.busy.size > 0 || quitTarget !== null || forceTarget !== null;
  // 每条采样、每次换排序只排一次；展开一行、高亮这类重渲染沿用上次的顺序。
  const sorted = React.useMemo(() => (sample ? sortRows(sample.rows, sortBy) : []), [sample, sortBy]);
  let rows: ProcessRow[] = [];
  if (sample) {
    rows = freezing && frozenRef.current.length > 0 ? refreshFrozen(frozenRef.current, sample) : sorted;
    if (!freezing || frozenRef.current.length === 0) frozenRef.current = rows;
  }
  const tags = growthTags(growth);
  const diag = React.useMemo(() => (tab === "disk" ? null : diagnose(sample, cpuHist)), [tab, sample, cpuHist]);
  // 读屏里程碑：播报监控状态 + 当前标签；退出/诊断等细节由 toast 和页面自身结构承担。
  const liveMsg = sample
    ? (running ? "" : t("process.paused")) + (tab === "cpu" ? t("process.tab.cpu") : tab === "mem" ? t("process.tab.mem") : t("process.tab.disk"))
    : "";
  const goDisk = (key: string) => {
    setFocus((f) => ({ key, nonce: f.nonce + 1 }));
    setTab("disk");
  };
  // 磁盘占用面板一直挂着，在 CPU、内存标签时只是藏起来、不刷新：回来时还是上次的内容，不从空白开始。
  // 元素按它自己的 props 记住，采样带来的重渲染不会把藏着的面板也重画一遍。
  const onDisk = tab === "disk";
  const diskPanel = React.useMemo(() => (
    <GrowthPanel active={active && onDisk} visit={visit} focusKey={focus.key} focusNonce={focus.nonce} goLeftovers={goLeftovers} goUninstall={goUninstall} />
  ), [active, onDisk, visit, focus.key, focus.nonce, goLeftovers, goUninstall]);

  // 菜单栏面板点名字进来：等这一行画在 CPU 或内存表里，滚到中间、高亮 900ms，只定位一次；
  // 先切到了磁盘占用标签就等着，回到 CPU、内存标签再定位。
  const rowReady = rowFocus !== null && tab !== "disk" && rows.some((r) => r.key === rowFocus);
  React.useEffect(() => {
    if (!rowReady || rowFocus === null) return;
    const key = rowFocus;
    const raf = requestAnimationFrame(() => {
      const el = document.querySelector(`.pr-item[data-key="${CSS.escape(key)}"]`);
      if (!el) return; // 这一行还没画出来：下一次渲染再试
      el.scrollIntoView({ block: "center" });
      setHl(key);
      setRowFocus(null);
    });
    return () => cancelAnimationFrame(raf);
  }, [rowFocus, rowReady, rows]);
  React.useEffect(() => {
    if (!hl) return;
    const id = window.setTimeout(() => setHl(null), 900);
    return () => window.clearTimeout(id);
  }, [hl]);

  if (isWindows()) {
    return (
      <div className="cz-react">
        <header className="view-head">
          <div>
            <h1>{t("nav.process")}</h1>
            <p className="muted">{t("process.unsupported")}</p>
          </div>
        </header>
      </div>
    );
  }

  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h1>{t("nav.process")}</h1>
          <p className="muted">{t("process.sub")}</p>
        </div>
        <div className="head-actions pr-head-actions">
          {tab !== "disk" && (
            <Button variant="ghost" size="sm" aria-pressed={!running} onClick={() => setRunning((r) => !r)}>
              {running ? <Pause size={14} /> : <Play size={14} />}
              {running ? t("process.pause") : t("process.resume")}
            </Button>
          )}
          <TabsRoot value={tab} onValueChange={(v) => setTab(v as ProcessTab)}>
            <TabsList
              ref={segRef}
              aria-label={t("process.tabs")}
              className="relative flex flex-none gap-[2px] rounded-[8px] bg-[var(--bg-subtle)] p-[2px]"
            >
              <span aria-hidden="true" className="cz-seg-thumb" style={segStyle} />
              <TabsTrigger value="cpu" className={segTrigger}>{t("process.tab.cpu")}</TabsTrigger>
              <TabsTrigger value="mem" className={segTrigger}>{t("process.tab.mem")}</TabsTrigger>
              <TabsTrigger value="disk" className={segTrigger}>
                {t("process.tab.disk")}
                {tags.size > 0 && <i className="pr-badge">{tags.size}</i>}
              </TabsTrigger>
            </TabsList>
          </TabsRoot>
        </div>
      </header>

      <span className="sr-only" role="status">{liveMsg}</span>

      <div hidden={!onDisk}>{diskPanel}</div>
      {!onDisk && (
        <>
          {diag && (
            <div className="pr-diag">
              <span>
                {diag.kind === "mem"
                  ? t("process.diag.mem", { name: diag.top?.name ?? "—", size: est(fmtMem(diag.top?.memory ?? 0), !!diag.top?.estimated) })
                  : t("process.diag.cpu", { name: diag.top?.name ?? "—", pct: est(fmtPct(diag.top?.cpu ?? 0), !!diag.top?.estimated) })}
              </span>
              {diag.kind !== tab && (
                <Button variant="ghost" size="sm" onClick={() => setTab(diag.kind)}>
                  {t(diag.kind === "mem" ? "process.diag.goMem" : "process.diag.goCpu")}
                </Button>
              )}
            </div>
          )}
          {!running && <p className="pr-paused muted">{t("process.paused")}</p>}
          <section className="st-listcard pr-card">
            <div className="st-cols num pr-cols">
              <span className="c-app">{t("status.apps.appCol")}</span>
              <button type="button" className="pr-sort" aria-pressed={sortBy === "cpu"} onClick={() => setTab("cpu")}>
                {t("process.col.cpu")}{sortBy === "cpu" ? " ▾" : ""}
              </button>
              <button type="button" className="pr-sort" aria-pressed={sortBy === "mem"} onClick={() => setTab("mem")}>
                {t("process.col.mem")}{sortBy === "mem" ? " ▾" : ""}
              </button>
              <span className="c-act" />
            </div>
            <div className="st-list pr-list">
              {error ? (
                <div className="st-empty" role="alert">
                  {t("status.error")}{" "}
                  <Button variant="ghost" size="sm" onClick={() => { setRunning(true); setRetry((n) => n + 1); }}>{t("btn.retry")}</Button>
                </div>
              ) : !sample ? (
                Array.from({ length: 6 }, (_, i) => (
                  <div key={i} className="st-row st-skel-row"><div className="sk-line w40" /><div className="sk-line w60" /></div>
                ))
              ) : rows.map((r) => (
                <ProcessRowView
                  key={r.key}
                  row={r}
                  sortBy={sortBy}
                  open={open === r.key}
                  tag={rowTag(r, tags)}
                  busy={quit.busy.has(r.path)}
                  stuck={stuck.has(r.path)}
                  done={quit.done.has(r.key)}
                  hl={hl === r.key}
                  t={t}
                  onToggle={() => setOpen((o) => (o === r.key ? null : r.key))}
                  onQuit={() => setQuitTarget(r)}
                  onForce={() => setForceTarget(r)}
                  onTag={goDisk}
                />
              ))}
            </div>
            <p className="st-footnote muted small">
              {sample ? t("process.footnote", { own: sample.own.toLocaleString(), others: sample.others.toLocaleString() }) : ""}
            </p>
          </section>
        </>
      )}

      <Dialog open={quitTarget !== null} onOpenChange={(v) => { if (!v) setQuitTarget(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("status.quit.title", { name: quitTarget?.name ?? "" })}</DialogTitle>
            <DialogDescription>{t("status.quit.body", { name: quitTarget?.name ?? "" })}</DialogDescription>
          </DialogHeader>
          <div className="mt-4 flex justify-end gap-2">
            <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
            {quitTarget && <Button onClick={() => void doQuit(quitTarget)}>{t("status.quit.ok")}</Button>}
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={forceTarget !== null} onOpenChange={(v) => { if (!v) setForceTarget(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("process.force.title", { name: forceTarget?.name ?? "" })}</DialogTitle>
            <DialogDescription>{t("process.force.body")}</DialogDescription>
          </DialogHeader>
          <div className="mt-4 flex justify-end gap-2">
            <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
            {forceTarget && <Button variant="destructive" onClick={() => void doForce(forceTarget)}>{t("process.force.ok")}</Button>}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
