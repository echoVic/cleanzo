import { isWindows } from "../../platform";
import * as React from "react";
import type { JunkItem, ScanSummary } from "../../mygo";
import { ruleDesc, ruleName } from "../../i18n";
import { Button } from "../../ui/components/button";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogClose,
} from "../../ui/components/dialog";
import { IdleHero } from "../../ui/components/idle-hero";
import { Collapse } from "../../ui/motion/Collapse";
import { NumberTicker } from "../../ui/motion/NumberTicker";
import { usePresence, Leave } from "../../ui/motion/presence";
import { useFlip } from "../../ui/motion/flip";
import { flyText, type Box } from "../../ui/motion/fly";
import { dur, play } from "../../ui/motion/motion";
import { useT, type DictKey } from "../../ui/i18n-react";
import { baseName, displayPath, errText, human, setNavBusy, toast } from "../../shared";
import { showReactApps } from "../apps/entry";
import { setCleanStaleHandler, takeStalePending } from "./entry";
import { cleanApi } from "./api";
import { CRASH_RULE_ID, forgetCrashDumps } from "../list/crash-dumps";
import { playCleanFocal, shouldPlayFocal } from "./focal";
import {
  GROUPS, createCleanRowTracker, buildCleanPlan, canStartScan, chosenSize, cleanBarWidth, cleanTotals,
  finalizeScan, groupCheckState, groupToggleOn, hasSubs, heroTotals, discoveredSize,
  isPartial, isSelectable, isVisible, isDynamicId, itemTitle, keepLatestVisible,
  removeFromSel, rowStateNote, scanItem, scanStart, scanProgress, scanPhase, splitApps, subsOf, toggleGroup, toggleRow,
  toggleSub, withIgnored, withIgnoredSummary,
  type CleanPlan, type RowState, type ScanState, emptySel,
} from "./helpers";

type Phase = "idle" | "scanning" | "scanned" | "cleaning" | "cleaned";

const groupIcons: Record<string, React.ReactNode> = {
  system: <><path d="M12 3v3m0 12v3M3 12h3m12 0h3M6 6l2 2m8 8 2 2M6 18l2-2M16 8l2-2" /><circle cx="12" cy="12" r="5" /></>,
  apps: <><rect x="4" y="4" width="6" height="6" rx="1" /><rect x="14" y="4" width="6" height="6" rx="1" /><rect x="4" y="14" width="6" height="6" rx="1" /><rect x="14" y="14" width="6" height="6" rx="1" /></>,
  browsers: <><circle cx="12" cy="12" r="9" /><ellipse cx="12" cy="12" rx="4" ry="9" /><path d="M3 12h18" /></>,
  updates: <path d="M4 10a8 8 0 0 1 14-5l2 2M20 3v4h-4M20 14a8 8 0 0 1-14 5l-2-2M4 21v-4h4" />,
  dev: <path d="m8 6-6 6 6 6m8-12 6 6-6 6M14 4l-4 16" />,
  review: <><circle cx="12" cy="12" r="9" /><path d="M12 7v6m0 3v1" /></>,
  custom: <path d="M3 7V5h7l2 3h9v12H3z" />,
};

const caret = (
  <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth={1.8} aria-hidden="true">
    <path d="m9 5 7 7-7 7" />
  </svg>
);

/** 原生 15px accent-color 复选框 + 半选（indeterminate 只能经 ref 设置）。 */
function TriCheck({ checked, indeterminate, disabled, onChange, ariaLabel }: {
  checked: boolean; indeterminate?: boolean; disabled?: boolean;
  onChange: (on: boolean) => void; ariaLabel: string;
}) {
  const ref = React.useRef<HTMLInputElement>(null);
  // Clicking a native checkbox clears indeterminate even when its React prop
  // remains true (e.g. selecting a group with blocked rows). Reapply both DOM
  // properties after every commit, before paint, not only when the prop changes.
  React.useLayoutEffect(() => {
    if (ref.current) {
      ref.current.checked = checked;
      ref.current.indeterminate = !!indeterminate;
    }
  });
  return (
    <input
      ref={ref}
      type="checkbox"
      aria-label={ariaLabel}
      checked={checked}
      disabled={disabled}
      onChange={(e) => onChange(e.target.checked)}
    />
  );
}

function Tag({ text, kind = "dim" }: { text: string; kind?: string }) {
  return <span className={`tag ${kind}`}>{text}</span>;
}

export function CleanPage() {
  const t = useT();

  const [st, setSt] = React.useState<ScanState>({ items: [], pending: new Set(), sel: emptySel() });
  const [phase, setPhase] = React.useState<Phase>("idle");
  const [summary, setSummary] = React.useState<ScanSummary | null>(null);
  const [stale, setStale] = React.useState(false);
  const [showAllApps, setShowAllApps] = React.useState(false);
  const [scanningPath, setScanningPath] = React.useState("");
  const [scanFailed, setScanFailed] = React.useState<unknown>(null);
  const [scanStopped, setScanStopped] = React.useState(false);
  const [menuID, setMenuID] = React.useState<string | null>(null);
  const [ignoring, setIgnoring] = React.useState(false);
  const [expanded, setExpanded] = React.useState<Set<string>>(new Set());
  const [collapsed, setCollapsed] = React.useState<Set<string>>(new Set());
  const [rowStates, setRowStates] = React.useState<Map<string, RowState>>(new Map());
  const [progress, setProgress] = React.useState({ total: 0, bytes: 0, id: "", path: "", stopped: false, error: "" });
  const [confirmOpen, setConfirmOpen] = React.useState(false);
  const [plan, setPlan] = React.useState<CleanPlan | null>(null);
  const [barHold, setBarHold] = React.useState(false);
  const heroRef = React.useRef<HTMLDivElement>(null);
  const heroNumRef = React.useRef<HTMLSpanElement>(null);
  const appsRowsRef = React.useRef<HTMLDivElement>(null);
  const morphFrom = React.useRef<{ rect: Box; fontSize: number; color: string } | null>(null);
  const barHoldTimer = React.useRef<number | undefined>(undefined);

  // 异步循环里读最新值，避免闭包捕获旧 state。
  const runRef = React.useRef(0);
  const chanRef = React.useRef<{ close(): void } | null>(null);
  const stoppedScanRef = React.useRef(false);
  const stoppedCleanRef = React.useRef(false);
  const scannedAtRef = React.useRef(0);
  const phaseRef = React.useRef(phase);
  phaseRef.current = phase;
  const ignoringRef = React.useRef(ignoring);
  ignoringRef.current = ignoring;
  const stRef = React.useRef(st);
  stRef.current = st;
  const progressRef = React.useRef(progress);
  progressRef.current = progress;

  // 离开页面（root 复用下通常不触发）：让旧流式循环停止消费并关闭 Channel。
  React.useEffect(() => {
    return () => {
      runRef.current++;
      chanRef.current?.close();
      chanRef.current = null;
      window.clearTimeout(barHoldTimer.current);
    };
  }, []);

  // 设置页保存配置后通知本页显过期条；clean 页未挂载过则由 takeStalePending 兜底。
  React.useEffect(() => {
    setCleanStaleHandler(() => {
      if (phaseRef.current !== "idle") setStale(true);
    });
    if (takeStalePending() && phaseRef.current !== "idle") setStale(true);
    return () => setCleanStaleHandler(() => {});
  }, []);

  // 扫描 / 清理期间侧栏对应项的 busy 提示（与旧 setNavBusy 一致）。
  React.useEffect(() => {
    setNavBusy("clean", phase === "scanning" || phase === "cleaning");
  }, [phase]);

  // 开始扫描：中央「--」移到横幅数字的位置（设计 6.2）
  React.useLayoutEffect(() => {
    const from = morphFrom.current;
    if (phase !== "scanning" || !from || !heroNumRef.current) return;
    morphFrom.current = null;
    void flyText(from, heroNumRef.current);
  }, [phase]);
  // 清理完成的招牌时刻：只在进入「完成」时判断一次
  React.useEffect(() => {
    if (phase !== "cleaned") return;
    const totals = cleanTotals(rowStates);
    if (shouldPlayFocal({ stopped: progress.stopped, failed: totals.failedNames.length, error: progress.error !== "", bytes: progress.bytes })) {
      playCleanFocal(heroRef.current);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [phase]);

  // 行菜单：开关、失焦、Escape、方向键导航（行为对齐旧 document 级监听）。
  React.useEffect(() => {
    if (menuID === null) return;
    const menu = document.querySelector<HTMLElement>(".junk-menu:not([data-leaving])");
    menu?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") { setMenuID(null); return; }
      if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
      const buttons = [...document.querySelectorAll<HTMLButtonElement>(".junk-menu:not([data-leaving]) button:not(:disabled)")];
      const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
      buttons[(index + (e.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length]?.focus();
      e.preventDefault();
    };
    const onDocClick = (e: MouseEvent) => {
      if (!(e.target as HTMLElement).closest(".junk-menu, [data-action='menu']")) setMenuID(null);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("click", onDocClick);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("click", onDocClick);
    };
  }, [menuID]);

  // ---- 扫描流 ----

  const startScan = React.useCallback(async () => {
    if (!canStartScan(phaseRef.current, ignoringRef.current)) return;
    if (phaseRef.current === "idle") {
      const b = document.querySelector<HTMLElement>("#clean-react-root .ha-avail b");
      if (b && typeof b.getBoundingClientRect === "function") {
        const cs = getComputedStyle(b);
        morphFrom.current = { rect: b.getBoundingClientRect(), fontSize: parseFloat(cs.fontSize), color: cs.color };
      }
    } else if (dur("fade") > 0) {
      // 旧列表留一份只读替身淡出，新扫描的状态和流式消费立即开始。
      const list = document.querySelector<HTMLElement>("#clean-react-root .junk-list");
      if (list && typeof list.cloneNode === "function") {
        const rect = list.getBoundingClientRect();
        const ghost = list.cloneNode(true) as HTMLElement;
        ghost.inert = true;
        ghost.setAttribute("aria-hidden", "true");
        ghost.querySelectorAll<HTMLElement>("*").forEach((el) => { el.style.animation = "none"; el.style.transition = "none"; });
        Object.assign(ghost.style, { position: "fixed", left: `${rect.left}px`, top: `${rect.top}px`, width: `${rect.width}px`, height: `${rect.height}px`, margin: "0", pointerEvents: "none", zIndex: "2" });
        list.parentElement?.appendChild(ghost);
        void play(ghost, [{ opacity: 1 }, { opacity: 0 }], dur("fade"), "linear").then(() => ghost.remove());
      }
    }
    window.clearTimeout(barHoldTimer.current);
    setBarHold(false);
    const myRun = ++runRef.current;
    stoppedScanRef.current = false;
    setPhase("scanning");
    setSt(scanStart([]));
    setSummary(null);
    setStale(false);
    setShowAllApps(false);
    setScanFailed(null);
    setScanStopped(false);
    setScanningPath("");
    setMenuID(null);
    setExpanded(new Set());
    setRowStates(new Map());
    setProgress({ total: 0, bytes: 0, id: "", path: "", stopped: false, error: "" });
    scannedAtRef.current = Date.now();
    const { ch, done } = cleanApi.startScan();
    chanRef.current = ch;
    let failed = false;
    try {
      // 先挂拒绝处理，流消费期间不会留下未处理的 Promise rejection。
      const completion = done.then(() => null, (e: unknown) => e);
      for await (const ev of ch) {
        if (runRef.current !== myRun) continue;
        if (ev.kind === "start" && ev.items) {
          setSt(scanStart(ev.items));
        } else if (ev.kind === "scanning") {
          setScanningPath(ev.path ?? "");
        } else if (ev.kind === "item" && ev.item) {
          const item = ev.item;
          setSt((prev) => scanItem(prev, item));
        } else if (ev.kind === "done") {
          setSummary(ev.summary ?? null);
        }
      }
      const error = await completion;
      if (error !== null) throw error;
    } catch (e) {
      if (!stoppedScanRef.current) {
        failed = true;
        setScanFailed(e);
        setSt((prev) => ({ items: [], pending: new Set(), sel: emptySel() }));
        toast(t("scan.error", { msg: errText(e) }), "err");
      }
    } finally {
      if (runRef.current !== myRun) return;
      chanRef.current = null;
      const stopped = stoppedScanRef.current;
      setScanStopped(stopped);
      stoppedScanRef.current = false;
      // 停止后只留下已测量的行；buffered/unmeasured 行被丢掉。
      setSt((prev) => finalizeScan(prev));
      setPhase("scanned");
      if (!stopped && !failed) {
        setBarHold(true);
        barHoldTimer.current = window.setTimeout(() => setBarHold(false), 450);
      }
    }
  }, [t]);

  const stopScan = React.useCallback(() => {
    stoppedScanRef.current = true;
    chanRef.current?.close();
  }, []);

  // ---- 确认与清理流 ----

  const askClean = () => {
    if (phase !== "scanned" || ignoring) return;
    const plan = buildCleanPlan(st.items, st.sel);
    if (!plan) return;
    setPlan(plan);
    setConfirmOpen(true);
  };

  const doClean = async () => {
    const p = plan;
    setConfirmOpen(false);
    setPlan(null);
    if (phaseRef.current !== "scanned" || !p) return;
    setPhase("cleaning");
    setMenuID(null);
    const tracker = createCleanRowTracker(p.selections);
    setRowStates(tracker.rows);
    setProgress({ total: p.bytes, bytes: 0, id: "", path: "", stopped: false, error: "" });
    const myRun = ++runRef.current;
    stoppedCleanRef.current = false;
    const { ch, done } = cleanApi.startClean(p.selections, scannedAtRef.current);
    chanRef.current = ch;
    try {
      const completion = done.then(() => null, (e: unknown) => e);
      for await (const ev of ch) {
        if (runRef.current !== myRun) continue;
        if (ev.kind === "start" && ev.id) {
          setRowStates(tracker.apply(ev));
          setProgress((pr) => ({ ...pr, id: ev.id!, path: "" }));
        } else if (ev.kind === "checking" || ev.kind === "removed") {
          setProgress((pr) => ({
            ...pr, id: ev.id, path: ev.path ?? "",
            bytes: ev.kind === "removed" ? (ev.bytes ?? pr.bytes) : pr.bytes,
          }));
        } else if (ev.kind === "done" && ev.result) {
          setRowStates(tracker.apply(ev));
          setProgress((pr) => ({ ...pr, bytes: ev.bytes ?? pr.bytes }));
        }
      }
      const error = await completion;
      if (error !== null) throw error;
    } catch (e) {
      if (!stoppedCleanRef.current) {
        const msg = t("clean.fail", { msg: errText(e) });
        setProgress((pr) => ({ ...pr, error: msg }));
        toast(msg, "err");
      }
    } finally {
      if (runRef.current !== myRun) return;
      chanRef.current = null;
      const { rows: next, stopped } = tracker.finish();
      setRowStates(next);
      if (stopped) setProgress((pr) => ({ ...pr, stopped: true }));
      // 清过崩溃转储后，状态页 / Codex 页的提示下次进入时重新量。
      if (p.selections.some((x) => x.id === CRASH_RULE_ID)) forgetCrashDumps();
      setPhase("cleaned");
    }
  };

  const stopClean = () => {
    stoppedCleanRef.current = true;
    setProgress((pr) => ({ ...pr, stopped: true }));
    chanRef.current?.close();
  };

  const onCleanButton = () => {
    if (phase === "cleaning") stopClean();
    else if (phase === "cleaned") void startScan();
    else askClean();
  };

  // ---- 行动作 ----

  const reveal = (it: JunkItem) => {
    setMenuID(null);
    if (!it.paths?.[0]) return;
    cleanApi.reveal(it.paths[0]).catch((e) => toast(t("fs.revealFail", { msg: errText(e) }), "err"));
  };

  const ignore = async (it: JunkItem) => {
    if (phaseRef.current !== "scanned" || ignoringRef.current) return;
    setIgnoring(true);
    setMenuID(null);
    try {
      const settings = await cleanApi.getSettings();
      await cleanApi.saveSettings(withIgnored(settings, it.id));
      // 保存成功才移走行；失败保留结果与选择。
      setSt((prev) => ({
        ...prev,
        items: prev.items.filter((x) => x.id !== it.id),
        sel: removeFromSel(prev.sel, it.id),
      }));
      setSummary((prev) => withIgnoredSummary(prev));
      toast(t("junk.ignoredItem", { name: itemTitle(it, ruleName) }));
    } catch (e) {
      toast(t("settings.saveFail", { msg: errText(e) }), "err");
    } finally {
      setIgnoring(false);
    }
  };

  const grant = () => {
    cleanApi.grantAccess().catch((e) => toast(t("grant.fail", { msg: errText(e) }), "err"));
  };

  const openSettings = () => {
    document.querySelector<HTMLButtonElement>('.nav-item[data-view="settings"]')?.click();
  };

  // ---- hero 派生 ----

  const eligible = st.items.filter(isSelectable);
  const totals = heroTotals(st.items, st.sel);
  const { foundBytes, reviewBytes, selectedBytes } = totals;
  const prog = scanProgress(st);
  const phaseGroup = scanPhase(st);

  let heroPrefix = t("hero.ready");
  let heroValue: number | null = null;
  let heroText = "--";
  let heroSub = t("clean.idleSub");
  let heroBarWidth = 0;
  let cleanBtnLabel = t("clean.now");
  let cleanBtnDisabled = true;
  let cleanBtnVariant: "destructive" | "ghost" | "default" = "default";

  if (scanFailed !== null) {
    heroPrefix = t("hero.ready");
    heroText = t("scan.failed");
    heroSub = t("scan.retryHint");
    cleanBtnDisabled = true;
  } else if (phase === "idle") {
    heroPrefix = t("hero.ready");
    heroText = "--";
    heroSub = t("clean.idleSub");
  } else if (phase === "scanning" || phase === "scanned") {
    heroPrefix = t(phase === "scanning" ? "scan.found" : "clean.foundPrefix");
    heroValue = phase === "scanning" && st.items.every((it) => st.pending.has(it.id)) ? null : foundBytes;
    const text = phase === "scanning"
      ? (phaseGroup ? t(`scan.phase.${phaseGroup}` as DictKey) : t("scan.hero"))
      : t("clean.selected", { size: human(selectedBytes) });
    heroSub = [
      text,
      t("junk.availableTotal", { size: human(totals.availableBytes) }),
      totals.runningBytes > 0 ? t("junk.runningTotal", { size: human(totals.runningBytes) }) : "",
      reviewBytes > 0 ? t("junk.reviewTotal", { size: human(reviewBytes) }) : "",
      totals.incompleteItems > 0 ? t("junk.incompleteTotal", { n: totals.incompleteItems }) : "",
    ].filter(Boolean).join(t("phrase.sep"));
    cleanBtnLabel = t("clean.now");
    cleanBtnDisabled = phase !== "scanned" || ignoring || eligible.every((it) => chosenSize(it, st.sel) === 0);
  } else if (phase === "cleaning") {
    const cur = st.items.find((it) => it.id === progress.id);
    const where = cur ? itemTitle(cur, ruleName) + (progress.path ? ` · ${baseName(progress.path)}` : "") : "";
    heroPrefix = t("clean.progress.prefix");
    heroValue = progress.bytes;
    heroSub = where
      ? t("clean.progress.sub", { total: human(progress.total), where })
      : t("clean.progress.preparing", { total: human(progress.total) });
    cleanBtnLabel = t("clean.stop");
    cleanBtnDisabled = progress.stopped;
    cleanBtnVariant = "ghost";
  } else {
    const totals = cleanTotals(rowStates);
    const parts = [
      totals.freed > 0 ? t("clean.done.freed", { size: human(totals.freed) }) : "",
      totals.trashed > 0 ? t("clean.done.trashed", { size: human(totals.trashed) }) : "",
      totals.inUse > 0 ? t("clean.done.inUse", { n: totals.inUse }) : "",
    ].filter(Boolean);
    const failedNames = totals.failedNames.map((id) => {
      const it = st.items.find((x) => x.id === id);
      return it ? itemTitle(it, ruleName) : id;
    });
    heroPrefix = t(progress.stopped ? "clean.stoppedPrefix" : "clean.donePrefix");
    heroValue = progress.bytes;
    heroSub = [
      parts.join(t("phrase.sep")) || t("clean.nothing"),
      progress.stopped ? t("clean.stoppedSub") : "",
      failedNames.length ? t("junk.partial", { names: failedNames.join(t("list.sep")) }) : "",
      progress.error,
    ].filter(Boolean).join(t("phrase.sep"));
    heroBarWidth = cleanBarWidth(progress.bytes, progress.total, progress.stopped, failedNames.length > 0, progress.error !== "");
    cleanBtnLabel = t("fs.rescan");
    cleanBtnDisabled = false;
    cleanBtnVariant = "default";
  }

  const barShown = (phase === "scanning" && prog.total > 0) || (phase === "scanned" && barHold) || phase === "cleaning" || phase === "cleaned";
  const barWidth = phase === "scanning" || (phase === "scanned" && barHold)
    ? (prog.total > 0 ? (prog.done / prog.total) * 100 : 0)
    : phase === "cleaning"
      ? (progress.total > 0 ? Math.min(100, (progress.bytes / progress.total) * 100) : 0)
      : heroBarWidth;
  const barCount = phase === "scanning" || (phase === "scanned" && barHold) ? t("scan.progressCount", { done: prog.done, total: prog.total }) : "";

  // ---- 列表派生 ----

  const rowNote = (it: JunkItem, row?: RowState): React.ReactNode => {
    if (row) {
      const note = rowStateNote(row, t, human);
      if (note.kind === "waiting") return note.text;
      if (note.kind === "cleaning") return <><span className="spin" aria-label={t("cat.scanning")} /> {note.text}</>;
      if (note.kind === "stopped") return <Tag text={note.text} />;
      if (note.kind === "skipped") return <Tag text={note.text} kind="warn" />;
      const skipTag = note.skipWarn ? <Tag text={note.skipWarn} kind="warn" /> : null;
      const failedTag = note.failedCount ? <Tag text={t("cat.failedN", { n: note.failedCount })} kind="warn" /> : null;
      return (
        <span className="cat-note-text ok"><svg className="cz-check" viewBox="0 0 12 12" aria-hidden="true"><path d="M2.2 6.4 4.8 8.9 9.8 3.4" /></svg>{note.text} {skipTag} {failedTag}</span>
      );
    }
    if (st.pending.has(it.id)) return <span className="spin" aria-label={t("cat.scanning")} />;
    if (it.denied && discoveredSize(it) === 0) return "--";
    return (
      <>
        {it.estimate ? <Tag text={t("junk.estimate")} /> : null} {human(discoveredSize(it))}
      </>
    );
  };

  const rowIcon = (it: JunkItem) => {
    if (it.group === "apps") return <span>{Array.from(itemTitle(it, ruleName))[0] ?? ""}</span>;
    return (
      <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        {groupIcons[it.group] ?? groupIcons.custom}
      </svg>
    );
  };

  const rowDesc = (it: JunkItem): string => {
    if (it.running) {
      const app = isDynamicId(it.id) ? it.name : t(`rule.${it.id}.app` as never);
      return t("junk.quit", { app: app.startsWith("rule.") ? ruleName(it.id) : app });
    }
    if (isDynamicId(it.id)) return it.paths?.[0] ?? "";
    return ruleDesc(it.id);
  };

  const renderRow = (it: JunkItem) => {
    const locked = phase !== "scanned" || !isSelectable(it);
    const row = rowStates.get(it.id);
    const used = row?.result?.inUse ?? it.inUse;
    const dimmed = (phase === "cleaning" || phase === "cleaned") && !row;
    const expandable = hasSubs(it) || (it.details?.length ?? 0) > 0 || (it.top?.length ?? 0) > 0;
    const open = expanded.has(it.id);
    const noteKey = row ? `row:${row.state}` : st.pending.has(it.id) ? "pending" : "size";
    return (
      <div className={`junk-item${dimmed ? " is-dim" : ""}`} key={it.id} data-key={it.id}>
        <div className="cat-row junk-row">
          <label className="cat-check">
            <TriCheck
              checked={st.sel.selected.has(it.id)}
              indeterminate={isPartial(it, st.sel)}
              disabled={locked}
              ariaLabel={t("junk.select", { name: itemTitle(it, ruleName) })}
              onChange={(on) => setSt((prev) => ({ ...prev, sel: toggleRow(it, on, prev.sel) }))}
            />
          </label>
          <div className={`tile junk-icon junk-icon-${it.group}`} aria-hidden="true">{rowIcon(it)}</div>
          <div className="cat-info">
            <div className="cat-name">
              {itemTitle(it, ruleName)}
              {it.running ? <Tag text={t("junk.running")} /> : null}
              {it.denied ? <Tag text={t("junk.denied")} kind="warn" /> : null}
              {it.trash ? <Tag text={t("cat.toTrash")} /> : null}
            </div>
            <div className="cat-desc muted">{rowDesc(it)}</div>
            {used > 0 ? <div className="cat-sub muted">{t("junk.inUse", { n: used })}</div> : null}
            {!it.running && discoveredSize(it) > it.size ? <div className="cat-sub muted">{t("junk.availableTotal", { size: human(it.size) })}</div> : null}
            {(it.readErrors ?? 0) > 0 ? <div className="cat-sub muted">{t("junk.readErrors", { n: it.readErrors! })}</div> : null}
          </div>
          <div className="cat-note"><span key={noteKey} className="cz-fade-in">{rowNote(it, row)}</span></div>
          <div className="junk-row-actions">
            <button
              className="junk-icon-btn"
              type="button"
              data-action="menu"
              aria-label={t("junk.menu", { name: itemTitle(it, ruleName) })}
              title={t("junk.menu", { name: itemTitle(it, ruleName) })}
              aria-expanded={menuID === it.id}
              disabled={phase !== "scanned" || ignoring}
              onClick={() => setMenuID(menuID === it.id ? null : it.id)}
            >
              <svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true">
                <circle cx="5" cy="12" r="1.6" /><circle cx="12" cy="12" r="1.6" /><circle cx="19" cy="12" r="1.6" />
              </svg>
            </button>
            {expandable ? (
              <button
                className={`junk-icon-btn junk-expand${open ? " open" : ""}`}
                type="button"
                aria-expanded={open}
                aria-label={t(open ? "fs.collapse" : "fs.expand")}
                title={t(open ? "fs.collapse" : "fs.expand")}
                onClick={() => setExpanded((prev) => {
                  const next = new Set(prev);
                  if (next.has(it.id)) next.delete(it.id); else next.add(it.id);
                  return next;
                })}
              >
                {caret}
              </button>
            ) : <span className="junk-action-space" />}
          </div>
          {menuP.filter((m) => m.item === it.id).map((m) => (
            <div key="menu" className="junk-menu" role="menu" data-leaving={m.leaving ? "" : undefined}>
              <button type="button" role="menuitem" disabled={!it.paths?.length} onClick={() => reveal(it)}>
                {t("junk.reveal")}
              </button>
              <button type="button" role="menuitem" disabled={ignoring} onClick={() => void ignore(it)}>
                {t("junk.ignore")}
              </button>
            </div>
          ))}
        </div>
        <Collapse open={open}>
          <div className="junk-details">
            {hasSubs(it) ? subsOf(it).map((s) => (
              <div className="junk-sub-row" key={s.path}>
                <TriCheck
                  checked={st.sel.selected.has(it.id) && (st.sel.subSel.get(it.id)?.has(s.path) ?? false)}
                  disabled={locked || s.size <= 0}
                  ariaLabel={t("junk.select", { name: s.name })}
                  onChange={(on) => setSt((prev) => ({ ...prev, sel: toggleSub(it, s.path, on, prev.sel) }))}
                />
                <div className="junk-sub-info">
                  <span>{s.name}</span>
                  {keepLatestVisible(it, s, st.sel) ? <Tag text={t("junk.keepLatest")} /> : null}
                  <div className="muted small">{it.id === CRASH_RULE_ID && s.files ? `${t("junk.crashFiles", { n: s.files.toLocaleString() })} · ` : ""}{s.detail}{it.id === "review.installers" && s.ageDays != null ? ` · ${s.ageDays === 0 ? t("junk.installerAgeToday") : t("junk.installerAge", { n: s.ageDays })}` : ""}</div>
                </div>
                <span className="junk-size">{human(s.size)}</span>
              </div>
            )) : (it.details?.length ? [] : it.top ?? []).map((top) => (
              <div className="junk-top-row" key={top.path}>
                <div>
                  <div>{top.name}</div>
                  <div className="muted small junk-path">{displayPath(top.path)}</div>
                </div>
                <span className="junk-size">{human(top.size)}</span>
              </div>
            ))}
            {(it.details ?? []).map((detail, i) => (
              <div className="junk-top-row" key={`${detail.path}:${i}`}>
                <div>
                  <div>{t(`junk.reason.${detail.reason}` as DictKey)}</div>
                  <div className="muted small junk-path">{displayPath(detail.path)}</div>
                </div>
                <span className="junk-size">{detail.reason === "boundary" || (detail.reason === "too-new" && detail.size === 0) ? "--" : `${detail.reason === "read-error" ? "≥ " : ""}${human(detail.size)}`}</span>
              </div>
            ))}
            {(it.detailsDropped ?? 0) > 0 ? <div className="muted small">{t("junk.detailsDropped", { n: it.detailsDropped! })}</div> : null}
          </div>
        </Collapse>
      </div>
    );
  };

  const shown = st.items.filter(isVisible);
  // 列表整体重置（重新扫描、失败）时直接替换，不逐行收起
  const resetting = st.items.length === 0;
  const shownP = usePresence(st.items.filter(isVisible), (it) => it.id, !resetting);
  const groupList = GROUPS.map((g) => {
    let members = shownP.filter((p) => p.item.group === g);
    if (g === "apps") members = [...members].sort((a, b) => b.item.size - a.item.size);
    return { g, members };
  }).filter(({ g, members }) => members.length > 0 || (g === "review" && (summary?.leftovers ?? 0) > 0));
  const groupsP = usePresence(groupList, (x) => x.g, !resetting);
  const appsKeys = (groupList.find((x) => x.g === "apps")?.members ?? []).filter((p) => !p.leaving).map((p) => p.key);
  useFlip(appsRowsRef, appsKeys);
  const menuP = usePresence(menuID ? [menuID] : [], (id) => id);

  // ---- 确认弹层文案 ----

  const names = (list: JunkItem[]) => list.length > 6
    ? t("junk.andMore", { names: list.slice(0, 6).map((it) => itemTitle(it, ruleName)).join(t("list.sep")), n: list.length })
    : list.map((it) => itemTitle(it, ruleName)).join(t("list.sep"));
  const sumSel = (list: JunkItem[]) => human(list.reduce((n, it) => n + chosenSize(it, st.sel), 0));
  const confirmLines = plan ? [
    plan.deleteItems.length ? t("clean.body.delete", { size: sumSel(plan.deleteItems), names: names(plan.deleteItems) }) : "",
    plan.trashItems.length ? t("clean.body.trash", { size: sumSel(plan.trashItems), names: names(plan.trashItems) }) : "",
    t("clean.body.inUse"),
  ].filter(Boolean) : [];

  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h2>{t("nav.clean")}</h2>
          <p className="muted">{t("clean.sub")}</p>
        </div>
        {phase !== "idle" && (
          <div className="head-actions">
            <Button
              variant="ghost"
              data-action="scan"
              onClick={() => (phase === "scanning" ? stopScan() : void startScan())}
              disabled={phase === "cleaning" || ignoring}
            >
              {phase === "scanning" && <span className="spin" />}
              {phase === "scanning" ? t("scan.stop") : t("fs.rescan")}
            </Button>
          </div>
        )}
      </header>

      {phase !== "idle" && (
        <div className="hero cz-enter" ref={heroRef}>
          <div className="hero-sweep" aria-hidden="true"><i /></div>
          <div className="hero-left">
            <div className="banner-title">
              <span>{heroPrefix}</span>
              <span className="hero-num" ref={heroNumRef}>
                {heroValue === null ? heroText : (
                  <NumberTicker key={phase === "cleaning" || phase === "cleaned" ? "clean" : "scan"} value={heroValue} from={0} format={human} />
                )}
              </span>
            </div>
            <div className="hero-sub" title={phase === "scanning" && scanningPath ? displayPath(scanningPath) : undefined}>{heroSub}</div>
            <Collapse open={barShown}>
              <div className="hero-prog-row">
                <div className="hero-progress"><div className="hero-bar" style={{ width: `${barWidth}%` }} /></div>
                {barCount && <span className="hero-prog-count">{barCount}</span>}
              </div>
            </Collapse>
          </div>
          <div className="banner-actions">
            <Button variant={cleanBtnVariant} size="lg" disabled={cleanBtnDisabled} onClick={onCleanButton}>
              {cleanBtnLabel}
            </Button>
          </div>
          <div className="hero-spark" aria-hidden="true">
            <svg viewBox="0 0 24 24" width="60" height="60" fill="currentColor">
              <path d="M12 2c.9 6 4.4 9.5 10.4 10.4-6 .9-9.5 4.4-10.4 10.4-.9-6-4.4-9.5-10.4-10.4 6-.9 9.5-4.4 10.4-10.4z" />
              <circle cx="17.6" cy="6" r="1.7" />
              <circle cx="6.4" cy="17.6" r="1.2" />
            </svg>
          </div>
        </div>
      )}

      {phase === "idle" ? (
        <IdleHero
          label={t("hero.ready")}
          value="--"
          sub={t("clean.idleSub")}
          guide={t("clean.idle.title")}
          ctaText={t("clean.idle.cta")}
          safetyText={t("clean.idle.desc")}
          chips={[t("clean.chip.caches"), t("clean.chip.logs"), t("clean.chip.temp")]}
          onCta={() => void startScan()}
        />
      ) : scanFailed !== null ? (
        <div className="empty err">
          <div className="err-icon">⚠</div>
          <div>{t("scan.error", { msg: errText(scanFailed) })}</div>
          <button className="btn ghost" type="button" onClick={() => void startScan()}>{t("btn.retry")}</button>
        </div>
      ) : phase === "scanning" && st.items.length === 0 ? (
        <div className="junk-list">{Array.from({ length: 5 }, (_, i) => <div className="skeleton card cz-enter" key={i} style={{ "--cz-i": i, "--cz-step": "30ms" } as React.CSSProperties} />)}</div>
      ) : (
        <div className="junk-list">
          <FullDiskAccessNotice summary={summary} grant={grant} />
          <Collapse open={stale && phase === "scanned"}>
            <div className="junk-stale">
              <span>{t("junk.stale")}</span>
              <button className="btn small ghost" type="button" onClick={() => void startScan()}>{t("fs.rescan")}</button>
            </div>
          </Collapse>
          {groupsP.map(({ key, leaving, item: { g, members } }, index) => {
            const items = members.map((p) => p.item);
            const eligibleM = items.filter(isSelectable);
            const gState = groupCheckState(g, st.items, st.sel);
            const folded = collapsed.has(g);
            const scanningGroup = items.some((it) => st.pending.has(it.id));
            // 一项都还没测完时只写「扫描中…」，不显示「发现 0 B」；只有残留的空分组照常显示小计
            const measuredSome = !scanningGroup || items.some((it) => !st.pending.has(it.id));
            const gTotals = heroTotals(items, st.sel);
            const gTotal = gTotals.foundBytes;
            const gChosen = gTotals.selectedBytes;
            const split = g === "apps" ? splitApps(members.map((p) => p.item), showAllApps) : null;
            const shownMembers = split ? members.slice(0, split.shown.length) : members;
            const rest = split?.rest ?? [];
            return (
              <Leave key={key} leaving={leaving}>
              <section className="junk-group cz-enter" style={{ "--cz-i": Math.min(index, 6) } as React.CSSProperties}>
                <div className="junk-group-head">
                  <TriCheck
                    checked={gState === "checked"}
                    indeterminate={gState === "partial"}
                    disabled={phase !== "scanned" || eligibleM.length === 0}
                    ariaLabel={t("junk.select", { name: t(`junk.group.${g}` as DictKey) })}
                    onChange={() => setSt((prev) => ({ ...prev, sel: toggleGroup(g, groupToggleOn(g, prev.items, prev.sel), prev.items, prev.sel) }))}
                  />
                  <button
                    className="junk-group-toggle"
                    type="button"
                    aria-expanded={!folded}
                    onClick={() => setCollapsed((prev) => {
                      const next = new Set(prev);
                      if (next.has(g)) next.delete(g); else next.add(g);
                      return next;
                    })}
                  >
                    <span className="group-name">{t(`junk.group.${g}` as DictKey)}</span>
                    <span className="group-sel">
                      {scanningGroup ? (measuredSome ? `${t("cat.scanning")} · ` : t("cat.scanning")) : ""}
                      {measuredSome && t("junk.groupSummary").split(/(\{total\}|\{sel\})/).map((part, i) => (
                        part === "{total}" ? <NumberTicker key={i} value={gTotal} format={human} />
                          : part === "{sel}" ? <NumberTicker key={i} value={gChosen} format={human} /> : part
                      ))}
                      {!scanningGroup && gTotals.runningBytes > 0 ? ` · ${t("junk.runningTotal", { size: human(gTotals.runningBytes) })}` : ""}
                    </span>
                    <span className={`junk-fold${folded ? "" : " open"}`}>{caret}</span>
                  </button>
                </div>
                <Collapse open={!folded}>
                  <div className="group-rows" ref={g === "apps" ? appsRowsRef : undefined}>
                    {shownMembers.map((p) => <Leave key={p.key} leaving={p.leaving}>{renderRow(p.item)}</Leave>)}
                    {rest.length > 0 && (
                      <button className="junk-more" type="button" onClick={() => setShowAllApps(true)}>
                        {t("junk.moreApps", {
                          n: rest.length,
                          size: human(rest.reduce((n, it) => n + discoveredSize(it), 0)),
                        })}
                        {caret}
                      </button>
                    )}
                    {!isWindows() && g === "review" && summary && summary.leftovers > 0 && (
                      <div className="junk-leftovers">
                        <span>{t("junk.leftovers", { n: summary.leftovers, size: human(summary.leftoversSize) })}</span>
                        <button className="btn small ghost" type="button" onClick={() => showReactApps("leftovers")}>
                          {t("junk.view")}
                        </button>
                      </div>
                    )}
                  </div>
                </Collapse>
              </section>
              </Leave>
            );
          })}
          {shown.length === 0 && !(summary?.leftovers) && (
            <div className="empty">{t(phase === "scanning" ? "scan.hero" : "junk.empty")}</div>
          )}
          {summary && (
            <div className="junk-foot muted small">
              {t("junk.checked", { n: summary.checked })}
              {summary.ignored > 0 && (
                <> <span>{t("junk.ignored", { n: summary.ignored })}</span>{" "}
                  <button className="junk-link" type="button" onClick={openSettings}>{t("junk.manage")}</button>
                </>
              )}
            </div>
          )}
          {scanStopped && <div className="junk-foot muted small">{t("scan.stopped")}</div>}
        </div>
      )}

      <Dialog open={confirmOpen} onOpenChange={(v) => setConfirmOpen(v)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("clean.sel.title")}</DialogTitle>
          </DialogHeader>
          {confirmLines.map((line, i) => <p key={i}>{line}</p>)}
          <div className="mt-4 flex justify-end gap-2">
            <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
            <Button variant="destructive" onClick={() => void doClean()}>{t("clean.confirm")}</Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

/** macOS 的扫描授权提示。 */
export function FullDiskAccessNotice({ summary, grant }: { summary: ScanSummary | null; grant: () => void }) {
  const t = useT();
  if (isWindows() || !summary || summary.fullDiskAccess) return null;
  return (
    <div className="junk-banner">
      <span>{t("junk.access", { n: summary.containers })}</span>
      <button className="btn small ghost" type="button" onClick={grant}>{t("cat.grant")}</button>
    </div>
  );
}
