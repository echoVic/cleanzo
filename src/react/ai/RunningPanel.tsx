import * as React from "react";
import type { ClaudeLeft, ClaudeProc, ClaudeRunning, ClaudeSession } from "../../mygo";
import { currentLang } from "../../i18n";
import { baseName } from "../../platform";
import { errText, toast } from "../../shared";
import { Button } from "../../ui/components/button";
import {
  Dialog, DialogClose, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from "../../ui/components/dialog";
import { useT } from "../../ui/i18n-react";
import { Collapse } from "../../ui/motion/Collapse";
import { runningApi } from "./api";
import { human } from "./format";
import { RUNNING, badgeCount, durationText, endToast, freezeRunning, leftTargets, procTargets, sessionName } from "./running";

type T = ReturnType<typeof useT>;

/**
 * 要结束的东西：一行进程、一个已关闭会话留下的全部、一个开着的会话。
 * 点「结束」时照当时显示的记下，确认时照它发：之间列表变了也不跟着变，由后端核对。
 */
type Ask =
  | { kind: "proc"; key: string; session: string; name: string; proc: ClaudeProc }
  | { kind: "left"; key: string; group: ClaudeLeft; name: string }
  | { kind: "session"; key: string; session: ClaudeSession; name: string };

export interface RunningPanelProps {
  /** 页面在屏幕上、开在「运行中」标签：只有这时才读。 */
  active: boolean;
  /** 每次进入页面加一：显示着时马上读一次。 */
  visit: number;
  /** 页头的「刷新」每点一次加一：马上读一次。 */
  refresh: number;
  /** 每次读完，告诉页面标签上该显示的数。 */
  onCount: (n: number) => void;
}

/** 端口列表：中文用顿号，英文用逗号。 */
function portList(ports: readonly number[]): string {
  return ports.join(currentLang() === "zh" ? "、" : ", ");
}

/** 确认框的标题、说明的几句话、逐个列出的进程，以及确认按钮用不用危险样式。 */
function askText(a: Ask, t: T): { title: string; lines: string[]; items: string[]; danger: boolean } {
  if (a.kind === "proc") {
    const ports = a.proc.ports.length > 0 ? t("ai.run.confirm.ports", { ports: portList(a.proc.ports) }) : "";
    return {
      title: t("ai.run.confirm.proc.title"),
      lines: [t("ai.run.confirm.proc.body", { session: a.name }), ports].filter(Boolean),
      items: [a.proc.command],
      danger: false,
    };
  }
  if (a.kind === "left") {
    return {
      title: t("ai.run.confirm.left.title", { session: a.name, n: a.group.procs.length }),
      lines: [],
      items: a.group.procs.map((p) => (p.ports.length > 0 ? `${p.command} · ${t("ai.run.port", { ports: portList(p.ports) })}` : p.command)),
      danger: false,
    };
  }
  const s = a.session;
  const idle = s.status === "idle";
  return {
    title: t("ai.run.confirm.session.title", { session: a.name }),
    lines: [
      t(idle ? "ai.run.confirm.idle.body" : "ai.run.confirm.busy.body"),
      s.commands.length > 0 ? t("ai.run.confirm.keeps", { n: s.commands.length }) : "",
      s.entry && s.entry !== "cli" && s.host ? t("ai.run.confirm.host", { app: s.host }) : "",
    ].filter(Boolean),
    items: [],
    danger: !idle,
  };
}

/** Claude Code 页的「运行中」标签：开着的会话，和已关闭的会话留下的进程；每一样结束前都先问。 */
export function RunningPanel({ active, visit, refresh, onCount }: RunningPanelProps) {
  const t = useT();
  const [data, setData] = React.useState<ClaudeRunning | null>(null);
  const [failed, setFailed] = React.useState(false);
  const [hidden, setHidden] = React.useState(() => document.hidden);
  const [open, setOpen] = React.useState<ReadonlySet<number>>(() => new Set());
  const [ask, setAsk] = React.useState<Ask | null>(null);
  /** 正在结束的那一样（Ask 的 key）：一次只结束一样。 */
  const [ending, setEnding] = React.useState<string | null>(null);
  const countRef = React.useRef(onCount);
  countRef.current = onCount;
  /** 开始冻结时显示着的读数。 */
  const frozen = React.useRef<ClaudeRunning | null>(null);
  /** 确认框关上的动画里还照最后一次的内容画。 */
  const lastAsk = React.useRef<Ask | null>(null);

  // 读取一次只有一份在路上：路上时又要读，等它回来马上再读，回来的那一份作废（round 变了）。
  // 不显示或窗口隐藏时不读、不排下一次；离开以后才回来的读数也作废。
  const live = React.useRef(false);
  const reading = React.useRef(false);
  const again = React.useRef(false);
  const round = React.useRef(0);
  const timer = React.useRef<number | undefined>(undefined);
  const load = React.useCallback(() => {
    if (!live.current || document.hidden) return;
    if (reading.current) { again.current = true; return; }
    window.clearTimeout(timer.current);
    timer.current = undefined;
    reading.current = true;
    again.current = false;
    const mine = round.current;
    void runningApi.read().then((r) => {
      if (mine !== round.current) return;
      setData(r);
      setFailed(false);
      countRef.current(badgeCount(r, r.at));
    }, () => {
      if (mine === round.current) setFailed(true);
    }).finally(() => {
      reading.current = false;
      if (again.current) load();
      else if (live.current && !document.hidden) timer.current = window.setTimeout(load, RUNNING.refreshMs);
    });
  }, []);
  /** 马上重新读：路上的那一份作废。 */
  const reload = React.useCallback(() => { round.current++; load(); }, [load]);

  React.useEffect(() => {
    const onVis = () => setHidden(document.hidden);
    document.addEventListener("visibilitychange", onVis);
    return () => document.removeEventListener("visibilitychange", onVis);
  }, []);

  // 显示出来、再次进入页面、点了「刷新」：马上读，之后每 5 秒读一次。
  const shown = active && !hidden;
  React.useEffect(() => {
    if (!shown) return;
    live.current = true;
    load();
    return () => {
      live.current = false;
      round.current++;
      window.clearTimeout(timer.current);
      timer.current = undefined;
    };
  }, [shown, visit, refresh, load]);

  React.useEffect(() => {
    if (!active) setAsk(null);
  }, [active]);

  const confirm = async () => {
    const a = ask;
    if (!a || ending !== null) return;
    setAsk(null);
    setEnding(a.key);
    try {
      const res = a.kind === "session"
        ? await runningApi.endSession({ pid: a.session.pid, start: a.session.start }, a.session.id, a.session.status)
        : await runningApi.endProcs(a.kind === "proc" ? a.session : a.group.id, a.kind === "proc" ? procTargets(a.proc) : leftTargets(a.group));
      const done = endToast(res, t, human);
      toast(done.text, done.kind);
    } catch (e) {
      toast(errText(e), "err");
    } finally {
      setEnding(null);
      reload();
    }
  };

  // 确认框开着、正在结束时，行和顺序不变，只换数字（和进程页一样）。
  if (ask) lastAsk.current = ask;
  const freezing = ask !== null || ending !== null;
  let view = data;
  if (data && freezing && frozen.current) view = freezeRunning(frozen.current, data);
  else frozen.current = data;

  const at = view?.at ?? 0;
  const busy = ending !== null;
  const toggle = (pid: number) => setOpen((prev) => {
    const next = new Set(prev);
    if (next.has(pid)) next.delete(pid); else next.add(pid);
    return next;
  });
  /** 一行上的「结束」：说明在 nameId 那个元素上；正在结束时整页的「结束」都不响应。 */
  const endButton = (key: string, label: string, nameId: string, onAsk: () => void) => (
    <button type="button" className="st-quit" aria-describedby={nameId} aria-disabled={busy || undefined} onClick={() => { if (!busy) onAsk(); }}>
      {ending === key ? t("ai.run.ending") : label}
    </button>
  );

  const procRow = (p: ClaudeProc, session: string, name: string) => {
    const nameId = `cz-run-p${p.pid}`;
    const meta = [
      p.ports.length > 0 ? t("ai.run.port", { ports: portList(p.ports) }) : "",
      p.worktree || baseName(p.cwd),
      durationText(at - Math.floor(p.start / 1e6), t),
    ].filter(Boolean).join(" · ");
    return (
      <div key={p.pid} className="st-row cz-run-row cz-run-proc" data-pid={p.pid}>
        <div className="cz-run-main">
          <span id={nameId} className="st-name cz-run-cmd" title={p.command}>{p.command}</span>
          <span className="cz-run-meta">{meta}</span>
        </div>
        <span className="st-col num">{human(p.memory)}</span>
        <span className="st-act">
          {endButton(`p${p.pid}`, t("ai.run.end"), nameId, () => setAsk({ kind: "proc", key: `p${p.pid}`, session, name, proc: p }))}
        </span>
      </div>
    );
  };

  const sessionRow = (s: ClaudeSession) => {
    const name = sessionName(s, t);
    const nameId = `cz-run-s${s.pid}`;
    const expandable = s.commands.length > 0;
    const isOpen = open.has(s.pid);
    const state = s.status === "idle" ? t("ai.run.idle", { time: durationText(at - s.since, t) })
      : s.status === "busy" ? t("ai.run.busy") : "";
    const label = (
      <>
        <span className="cz-run-chev" aria-hidden="true">{expandable ? (isOpen ? "▾" : "›") : ""}</span>
        <span id={nameId} className="st-name" title={name}>{name}</span>
        {state && <span className={s.status === "busy" ? "cz-run-state busy" : "cz-run-state"}>{state}</span>}
        {s.host && <span className="cz-run-meta">{t("ai.run.in", { app: s.host })}</span>}
      </>
    );
    return (
      <div key={s.pid} className="cz-run-item">
        <div className="st-row cz-run-row" data-pid={s.pid}>
          {expandable ? (
            <button type="button" className="cz-run-main" aria-expanded={isOpen} onClick={() => toggle(s.pid)}>{label}</button>
          ) : (
            <div className="cz-run-main">{label}</div>
          )}
          <span className="st-col num">{human(s.memory)}</span>
          <span className="st-act">
            {!s.own && endButton(`s${s.pid}`, t("ai.run.end"), nameId, () => setAsk({ kind: "session", key: `s${s.pid}`, session: s, name }))}
          </span>
        </div>
        {expandable && (
          <Collapse open={isOpen}>
            <div className="cz-run-sub">{s.commands.map((c) => procRow(c, s.id, name))}</div>
          </Collapse>
        )}
      </div>
    );
  };

  const groupBlock = (g: ClaudeLeft) => {
    const name = sessionName(g, t);
    const nameId = `cz-run-g${g.id}`;
    return (
      <div key={g.id} className="cz-run-item">
        <div className="st-row cz-run-row cz-run-ghead" data-group={g.id}>
          <div className="cz-run-main"><span id={nameId} className="st-name" title={name}>{name}</span></div>
          <span className="st-act">
            {endButton(`g${g.id}`, t("ai.run.endAll"), nameId, () => setAsk({ kind: "left", key: `g${g.id}`, group: g, name }))}
          </span>
        </div>
        <div className="cz-run-sub">{g.procs.map((p) => procRow(p, g.id, name))}</div>
      </div>
    );
  };

  const sessions = view?.sessions ?? [];
  const groups = view?.left ?? [];
  const leftProcs = groups.flatMap((g) => g.procs);
  // 第一段的合计含会话下展开的命令：这一段列出的都算。
  const openSize = sessions.reduce((n, s) => n + s.memory + s.commands.reduce((m, c) => m + c.memory, 0), 0);
  const leftSize = leftProcs.reduce((n, p) => n + p.memory, 0);
  const nothing = view !== null && sessions.length === 0 && groups.length === 0 && !view.noRecords;
  const asked = ask ?? lastAsk.current;
  const words = asked ? askText(asked, t) : null;

  return (
    <div className="cz-run">
      {failed && (
        <div className="cz-run-error" role="alert">
          <span>{t("ai.run.error")}</span>
          <Button variant="ghost" size="sm" onClick={reload}>{t("btn.retry")}</Button>
        </div>
      )}
      {view === null ? (!failed && (
        <section className="st-listcard" aria-hidden="true">
          {[0, 1, 2].map((i) => (
            <div key={i} className="st-row st-skel-row"><div className="sk-line w40" /><div className="sk-line w60" /></div>
          ))}
        </section>
      )) : nothing ? (
        <section className="st-listcard"><p className="st-empty">{t("ai.run.none")}</p></section>
      ) : (
        <>
          <section className="st-listcard">
            <div className="st-list-head">
              <h2>{t("ai.run.open.title")}</h2>
              <span className="cz-run-sum">{t("ai.run.open.sum", { n: sessions.length, size: human(openSize) })}</span>
            </div>
            {sessions.map(sessionRow)}
          </section>
          <section className="st-listcard">
            <div className="st-list-head">
              <h2>{t("ai.run.left.title")}</h2>
              {/* 读不到会话记录时说不出留下了几个，不写个数 */}
              {!view.noRecords && <span className="cz-run-sum">{t("ai.run.left.sum", { n: leftProcs.length, size: human(leftSize) })}</span>}
            </div>
            {view.noRecords ? <p className="st-empty">{t("ai.run.left.noRecords")}</p>
              : groups.length === 0 ? <p className="st-empty">{t("ai.run.left.none")}</p>
              : groups.map(groupBlock)}
          </section>
        </>
      )}
      {view && (
        <p className="cz-run-foot">
          {[t("ai.run.foot"), view.uncertain > 0 ? t("ai.run.foot.uncertain", { n: view.uncertain }) : ""].filter(Boolean).join(currentLang() === "zh" ? "" : " ")}
          <br />
          {t("ai.run.foot.idle")}
        </p>
      )}

      <Dialog open={ask !== null} onOpenChange={(v) => { if (!v) setAsk(null); }}>
        <DialogContent>
          {words && (
            <>
              <DialogHeader>
                <DialogTitle>{words.title}</DialogTitle>
                <DialogDescription asChild>
                  <div className="cz-run-ask">
                    {words.lines.map((line, i) => <p key={i}>{line}</p>)}
                    {words.items.length > 0 && <ul>{words.items.map((item, i) => <li key={i}>{item}</li>)}</ul>}
                  </div>
                </DialogDescription>
              </DialogHeader>
              <div className="mt-4 flex justify-end gap-2">
                <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
                <Button variant={words.danger ? "destructive" : "default"} onClick={() => void confirm()}>{t("ai.run.confirm.ok")}</Button>
              </div>
            </>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
}
