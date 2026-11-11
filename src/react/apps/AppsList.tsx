import { isWindows } from "../../platform";
import * as React from "react";
import { Search } from "lucide-react";
import type { AppInfo, AppUpdate, GrowthApp, GrowthSummary, UninstallDetail } from "../../mygo";
import { useT } from "../../ui/i18n-react";
import { currentLang } from "../../i18n";
import { errText, human, toast } from "../../shared";
import { Button } from "../../ui/components/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogClose } from "../../ui/components/dialog";
import { Collapse } from "../../ui/motion/Collapse";
import { useFlip } from "../../ui/motion/flip";
import { usePresence } from "../../ui/motion/presence";
import { quitErrCode } from "../status/format";
import { appsApi } from "./api";
import { bundleFailed, classifyUninstall, filterApps, readAppSort, saveAppSort, sortApps, type AppSortBy } from "./helpers";
import { AnimatedAppRow, type AppRowProps } from "./AppRow";
import { AppRowExpanded } from "./AppRowExpanded";
import { filterByKind, growthFor, growthTagFor, splitUsage, updateCount, type AppsFilter, type CheckProgress, type UsageSplit } from "./updates";
import { UninstallDialog, type UninstallStage } from "./UninstallDialog";

export interface AppsListProps {
  active: boolean;
  filter: AppsFilter;
  onBusyChange(b: boolean): void;
  onCounts(c: { updates: number | null; total: number }): void;
  uninstallPath?: string | null;
  uninstallNonce?: number;
  recheckNonce?: number;
  onChecking?(b: boolean): void;
}

interface DialogState {
  stage: UninstallStage;
  detail: UninstallDetail | null;
  failMsg: string | null;
}
const DIALOG_IDLE: DialogState = { stage: "analyzing", detail: null, failMsg: null };

/** 应用页开关时更新检查该做什么：start 开始（或从头再来）、stop 关掉通道、none 不动。 */
export function checkStep(
  prev: { active: boolean; loaded: boolean; running: boolean; finished: boolean },
  next: { active: boolean; loaded: boolean },
): "start" | "stop" | "none" {
  if (!next.active || !next.loaded) return prev.running ? "stop" : "none";
  if (prev.running || prev.finished) return "none";
  return "start";
}

export function AppsList({ active, filter, onBusyChange, onCounts, uninstallPath = null, uninstallNonce = 0, recheckNonce = 0, onChecking }: AppsListProps) {
  const t = useT();
  const win = isWindows();
  const [apps, setApps] = React.useState<AppInfo[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [loadError, setLoadError] = React.useState<string | null>(null);
  const [q, setQ] = React.useState("");
  const [sortBy, setSortBy] = React.useState<AppSortBy>(() => { const s = readAppSort(); return isWindows() && (s === "opened" || s === "growth") ? "name" : s; });
  const [updates, setUpdates] = React.useState<ReadonlyMap<string, AppUpdate>>(new Map());
  const [progress, setProgress] = React.useState<CheckProgress>({ done: 0, total: 0, finished: false });
  const [checking, setChecking] = React.useState(false);
  const [checkError, setCheckError] = React.useState<string | null>(null);
  const [openId, setOpenId] = React.useState<string | null>(null);
  const [usage, setUsage] = React.useState<Record<string, UsageSplit>>({});
  const [usageError, setUsageError] = React.useState<Record<string, string>>({});
  const [details, setDetails] = React.useState<Record<string, UninstallDetail>>({});
  const [busy, setBusy] = React.useState<Record<string, AppRowProps["busy"]>>({});
  const [brewBusyId, setBrewBusyId] = React.useState<string | null>(null);
  const [stuck, setStuck] = React.useState<ReadonlySet<string>>(new Set());
  const [upgradeFail, setUpgradeFail] = React.useState<Record<string, { detail: string; command: string }>>({});
  const [summary, setSummary] = React.useState<GrowthSummary | null>(null);
  const [growth, setGrowth] = React.useState<GrowthApp[]>([]);
  const [quitTarget, setQuitTarget] = React.useState<AppInfo | null>(null);
  const [forceTarget, setForceTarget] = React.useState<AppInfo | null>(null);
  const [dialogOpen, setDialogOpen] = React.useState(false);
  const [dialog, setDialog] = React.useState<DialogState>(DIALOG_IDLE);
  const [focusRequest, setFocusRequest] = React.useState<string | null>(null);
  const chRef = React.useRef<{ close(): void } | null>(null);
  const runRef = React.useRef(0);
  const seqRef = React.useRef(0);
  const triedRef = React.useRef(false);
  const loadingRef = React.useRef(false);
  const growthReadRef = React.useRef(false);
  const checkRef = React.useRef({ active: false, loaded: false, running: false, finished: false });
  const activeRef = React.useRef(active);
  const appsRef = React.useRef(apps);
  const detailsRef = React.useRef(details);
  const busyRef = React.useRef(new Set<string>());
  const brewRef = React.useRef<string | null>(null);
  const detailJobs = React.useRef(new Map<string, Promise<UninstallDetail>>());
  activeRef.current = active;
  appsRef.current = apps;
  detailsRef.current = details;

  const loadApps = React.useCallback(async () => {
    if (loadingRef.current) return;
    loadingRef.current = true;
    setLoading(true);
    setLoadError(null);
    try {
      const list = await appsApi.listApps();
      appsRef.current = list;
      setApps(list);
      setLoaded(true);
    } catch (e) {
      setApps([]);
      setLoadError(e instanceof Error ? e.message : String(e));
      // 错误面板负责失败反馈，不再重复弹提示。
    } finally {
      loadingRef.current = false;
      setLoading(false);
    }
  }, []);

  // 首次进入才加载；根复用后再次进入保留结果。
  React.useEffect(() => {
    if (active && !triedRef.current) {
      triedRef.current = true;
      void loadApps();
    }
  }, [active, loadApps]);

  React.useEffect(() => {
    if (!active || !loaded || win || growthReadRef.current) return;
    growthReadRef.current = true;
    void appsApi.growthSummary().then(async (s) => {
      setSummary(s);
      if (s.config.enabled) setGrowth((await appsApi.growthState()).apps);
    }).catch(() => { /* 增长不可读不妨碍应用列表。 */ });
  }, [active, loaded, win]);

  const stopCheck = React.useCallback(() => {
    runRef.current++;
    chRef.current?.close();
    chRef.current = null;
    checkRef.current.running = false;
    setChecking(false);
  }, []);

  const startCheck = React.useCallback(async () => {
    stopCheck();
    const run = ++runRef.current;
    const ids = appsRef.current.map((a) => a.id);
    const received = new Set<string>();
    checkRef.current.running = true;
    checkRef.current.finished = false;
    setChecking(true);
    setCheckError(null);
    setProgress({ done: 0, total: ids.length, finished: false });
    try {
      const { ch, done } = appsApi.startUpdateCheck(ids);
      chRef.current = ch;
      // 立即接住桥接拒绝；即使没收到哨兵也能结束迭代，不留下未处理的 Promise。
      const completion = done.then(() => { ch.close(); return null; }, (e: unknown) => { ch.close(); return e; });
      for await (const u of ch) {
        if (runRef.current !== run || !activeRef.current) continue;
        if (u.id === "__done") {
          checkRef.current.finished = true;
          setProgress({ done: ids.length, total: ids.length, finished: true });
        } else if (ids.includes(u.id)) {
          received.add(u.id);
          if (appsRef.current.some((a) => a.id === u.id)) {
            setUpdates((prev) => new Map(prev).set(u.id, u));
          }
          setProgress({ done: received.size, total: ids.length, finished: false });
        }
      }
      const error = await completion;
      if (error) throw error;
    } catch (e) {
      if (runRef.current === run && activeRef.current) setCheckError(errText(e));
    } finally {
      if (runRef.current === run) {
        chRef.current?.close();
        chRef.current = null;
        checkRef.current.running = false;
        setChecking(false);
      }
    }
  }, [stopCheck]);

  // 显隐只取消检查，不清空已经收到的结果；StrictMode 重挂也由同一个状态机恢复。
  React.useEffect(() => {
    const next = { active: active && !win, loaded };
    const step = checkStep(checkRef.current, next);
    Object.assign(checkRef.current, next);
    if (step === "stop") stopCheck();
    if (step === "start") void startCheck();
  }, [active, loaded, win, startCheck, stopCheck]);
  React.useEffect(() => () => { stopCheck(); seqRef.current++; }, [stopCheck]);

  const firstRecheck = React.useRef(0);
  React.useEffect(() => {
    if (!active || !loaded || win || recheckNonce === firstRecheck.current) return;
    firstRecheck.current = recheckNonce;
    setUpdates(new Map());
    setUpgradeFail({});
    setProgress({ done: 0, total: apps.length, finished: false });
    void startCheck();
  }, [recheckNonce, active, loaded, win, apps.length, startCheck]);

  const anyBusy = loading || checking || Object.values(busy).some(Boolean) || brewBusyId !== null;
  React.useEffect(() => { onBusyChange(anyBusy); }, [anyBusy, onBusyChange]);
  React.useEffect(() => { onChecking?.(checking); }, [checking, onChecking]);
  const count = progress.finished ? updateCount(updates) : null;
  React.useEffect(() => { onCounts({ updates: count, total: apps.length }); }, [count, apps.length, onCounts]);

  const ensureDetail = React.useCallback((id: string): Promise<UninstallDetail> => {
    const cached = detailsRef.current[id];
    if (cached) return Promise.resolve(cached);
    const pending = detailJobs.current.get(id);
    if (pending) return pending;
    const requestedApp = appsRef.current.find((a) => a.id === id);
    const job = appsApi.uninstallDetail(id).then((d) => {
      // 详情可能比退出或升级晚回来，只合并请求期间被动作改过的字段。
      const currentApp = appsRef.current.find((a) => a.id === id);
      if (requestedApp && currentApp) d = { ...d, app: {
        ...d.app,
        ...(currentApp.running !== requestedApp.running ? { running: currentApp.running } : {}),
        ...(currentApp.version !== requestedApp.version ? { version: currentApp.version } : {}),
      } };
      if (currentApp) {
        // 列表和展开操作共用这份状态，同时保留列表身份与请求期间完成的动作。
        const next = appsRef.current.map((a) => a.id === id
          ? { ...a, running: d.app.running, version: d.app.version }
          : a);
        appsRef.current = next;
        setApps(next);
      }
      detailsRef.current = { ...detailsRef.current, [id]: d };
      setDetails((prev) => ({ ...prev, [id]: d }));
      setUsage((prev) => ({ ...prev, [id]: splitUsage(d) }));
      setUsageError((prev) => { const next = { ...prev }; delete next[id]; return next; });
      return d;
    }).catch((e: unknown) => {
      setUsageError((prev) => ({ ...prev, [id]: errText(e) }));
      throw e;
    }).finally(() => { detailJobs.current.delete(id); });
    detailJobs.current.set(id, job);
    return job;
  }, []);

  React.useEffect(() => {
    if (openId && !details[openId] && !usageError[openId]) void ensureDetail(openId).catch(() => {});
  }, [openId, details, usageError, ensureDetail]);

  const firstUninstall = React.useRef(0);
  React.useEffect(() => {
    if (!active || !loaded || filter !== "all" || !uninstallPath || uninstallNonce === firstUninstall.current) return;
    if (!apps.some((a) => a.id === uninstallPath)) return;
    firstUninstall.current = uninstallNonce;
    setQ("");
    setOpenId(uninstallPath);
    setFocusRequest(uninstallPath);
  }, [active, loaded, filter, uninstallNonce, uninstallPath, apps]);
  React.useEffect(() => {
    if (!active || !focusRequest || openId !== focusRequest) return;
    // 刚展开的行先以收起态（visibility: hidden）挂上、下一帧才展开，那时 focus() 会静默失败：
    // 每帧试一次，直到焦点真的落到「卸载…」上（最多约 1 秒）。
    let raf = 0;
    let tries = 0;
    const attempt = () => {
      const button = document.querySelector<HTMLButtonElement>(`[data-key="${CSS.escape(focusRequest)}"] .app-uninstall`);
      button?.focus({ preventScroll: true });
      if (button && document.activeElement === button) {
        button.scrollIntoView({ block: "nearest" });
        setFocusRequest(null);
      } else if (++tries < 60) {
        raf = requestAnimationFrame(attempt);
      } else {
        setFocusRequest(null);
      }
    };
    raf = requestAnimationFrame(attempt);
    return () => cancelAnimationFrame(raf);
  }, [active, focusRequest, openId]);

  const beginAction = (id: string, action: NonNullable<AppRowProps["busy"]>) => {
    if (busyRef.current.has(id)) return false;
    busyRef.current.add(id);
    setBusy((prev) => ({ ...prev, [id]: action }));
    return true;
  };
  const endAction = (id: string) => {
    busyRef.current.delete(id);
    setBusy((prev) => ({ ...prev, [id]: null }));
  };
  const upgrade = async (id: string) => {
    const a = appsRef.current.find((a) => a.id === id), u = updates.get(id);
    if (!a || !u || u.state !== "update" || !u.action || win) return;
    if (u.action === "brew" && brewRef.current) { toast(t("apps.upgradeBusy"), "err"); return; }
    if (!beginAction(id, "upgrade")) return;
    if (u.action === "brew") { brewRef.current = id; setBrewBusyId(id); }
    setUpgradeFail((prev) => { const next = { ...prev }; delete next[id]; return next; });
    try {
      const res = await appsApi.upgradeApp(id);
      if (res.done) {
        if (u.action === "brew") {
          appsRef.current = appsRef.current.map((a) => a.id === id ? { ...a, version: res.version } : a);
          setApps((prev) => prev.map((a) => a.id === id ? { ...a, version: res.version } : a));
          setUpdates((prev) => new Map(prev).set(id, { ...u, state: "current", current: res.version, action: "" }));
          setDetails((prev) => prev[id] ? { ...prev, [id]: { ...prev[id], app: { ...prev[id].app, version: res.version } } } : prev);
          toast(t("apps.upgraded", { name: a.name, v: res.version }));
        } else if (u.action === "open-store") toast(t("apps.opened.store"));
        else toast(t("apps.opened.app", { name: a.name }));
      } else if (res.code === "in-use") toast(t("apps.inUse", { name: a.name }), "err");
      else if (res.code === "busy") toast(t("apps.upgradeBusy"), "err");
      else if (res.code === "not-checked") toast(t("apps.notChecked"), "err");
      else {
        if (res.code === "brew-failed") {
          setUpgradeFail((prev) => ({ ...prev, [id]: { detail: res.detail, command: res.command } }));
          setOpenId(id);
        }
        toast(t("apps.upgradeFail", { name: a.name }), "err");
      }
    } catch { toast(t("apps.upgradeFail", { name: a.name }), "err"); }
    finally {
      if (u.action === "brew") { brewRef.current = null; setBrewBusyId(null); }
      endAction(id);
    }
  };

  const cleanCache = async (id: string) => {
    const a = appsRef.current.find((a) => a.id === id);
    if (!a || win || !beginAction(id, "cache")) return;
    try {
      const res = await appsApi.cleanAppCache(id);
      if (res.failed.length === 0) { // 没有可移的也算清理完：缓存已经在别处清掉了
        toast(t("apps.cacheCleaned", { name: a.name, size: human(res.freed) }));
        setUsage((prev) => prev[id] ? { ...prev, [id]: { ...prev[id], cache: 0, cachePaths: [] } } : prev);
        // 确认框复用这份详情，清掉缓存路径，避免展示已经移走的文件。
        setDetails((prev) => {
          const d = prev[id];
          return d ? { ...prev, [id]: { ...d, items: d.items.filter((it) => it.kind !== "cache"), totalSize: Math.max(0, d.totalSize - splitUsage(d).cache) } } : prev;
        });
      } else if (res.failed.length > 0 && res.failed.every((f) => f.endsWith(": in-use"))) toast(t("apps.inUse", { name: a.name }), "err");
      else toast(t("apps.cacheFail", { name: a.name, n: res.failed.length }), "err");
    } catch { toast(t("apps.cacheFail", { name: a.name, n: usage[id]?.cachePaths.length ?? 0 }), "err"); }
    finally { endAction(id); }
  };

  const markStopped = (id: string) => {
    appsRef.current = appsRef.current.map((a) => a.id === id ? { ...a, running: false } : a);
    setApps((prev) => prev.map((a) => a.id === id ? { ...a, running: false } : a));
    setDetails((prev) => prev[id] ? { ...prev, [id]: { ...prev[id], app: { ...prev[id].app, running: false } } } : prev);
    setStuck((prev) => { const next = new Set(prev); next.delete(id); return next; });
  };
  const doQuit = async (a: AppInfo) => {
    setQuitTarget(null);
    if (!beginAction(a.id, "quit")) return;
    try {
      if (await appsApi.quitApp(a.path)) {
        toast(t("status.toast.quitDone", { name: a.name }));
        markStopped(a.id);
      } else {
        toast(t("status.toast.quitPending", { name: a.name }));
        setStuck((prev) => new Set(prev).add(a.id));
      }
    } catch (e) {
      const code = quitErrCode(e);
      if (code === "not-running") { toast(t("status.toast.notRunning", { name: a.name })); markStopped(a.id); }
      else if (code === "cannot-quit") {
        toast(t("status.toast.cannotQuit", { name: a.name }), "err");
        setStuck((prev) => new Set(prev).add(a.id));
      } else toast(`${t("status.error")}：${errText(e)}`, "err");
    } finally { endAction(a.id); }
  };
  const doForce = async (a: AppInfo) => {
    setForceTarget(null);
    if (!beginAction(a.id, "quit")) return;
    try {
      if (await appsApi.forceQuitApp(a.path)) { toast(t("process.toast.forceDone", { name: a.name })); markStopped(a.id); }
      else toast(t("process.toast.forceFail", { name: a.name }), "err");
    } catch (e) {
      if (quitErrCode(e) === "not-running") { toast(t("status.toast.notRunning", { name: a.name })); markStopped(a.id); }
      else toast(t("process.toast.forceFail", { name: a.name }), "err");
    } finally { endAction(a.id); }
  };

  const closeDialog = React.useCallback(() => {
    seqRef.current++;
    setDialogOpen(false);
  }, []);
  const openDetail = async (id: string) => {
    const seq = ++seqRef.current;
    const cached = detailsRef.current[id];
    setDialog(cached ? { stage: "detail", detail: cached, failMsg: null } : DIALOG_IDLE);
    setDialogOpen(true);
    if (cached) return;
    try {
      const d = await ensureDetail(id);
      if (seq !== seqRef.current) return;
      setDialog({ stage: "detail", detail: d, failMsg: null });
    } catch (e) {
      if (seq !== seqRef.current) return;
      setDialog({ stage: "fail", detail: null, failMsg: errText(e) });
    }
  };
  const confirmUninstall = async () => {
    const d = dialog.detail;
    if (!d || !beginAction(d.app.id, "uninstall")) return;
    closeDialog();
    const path = d.app.id;
    const seq = seqRef.current;
    try {
      const res = await appsApi.uninstallApp(path);
      const name = d.app.name;
      const outcome = classifyUninstall(res);
      if (outcome === "launched") {
        if (seq === seqRef.current) {
          setDialog({ stage: "launched", detail: d, failMsg: null });
          setDialogOpen(true);
        }
      } else if (outcome === "inUse") {
        toast(t("apps.inUse", { name }), "err");
      } else if (outcome === "partial") {
        if (isWindows()) {
          toast(t("apps.fail", { msg: errText(res.failed[0]?.slice(res.path.length + 2) ?? "blocked") }), "err");
        } else {
          toast(t("apps.partial", { name, n: res.failed.length }), "err");
        }
      } else {
        toast(t("apps.ok", { name, size: human(res.freed) }));
      }
      if (!bundleFailed(res)) {
        appsRef.current = appsRef.current.filter((a) => a.id !== res.path);
        setApps((prev) => prev.filter((a) => a.id !== res.path));
        setUpdates((prev) => { const next = new Map(prev); next.delete(res.path); return next; });
        setOpenId((open) => (open === res.path ? null : open)); // 卸载期间点开的另一行不收起
      }
    } catch (e) {
      toast(t("apps.fail", { msg: errText(e) }), "err");
    } finally { endAction(path); }
  };
  const copy = async (text: string) => {
    try { await navigator.clipboard.writeText(text); toast(t("apps.copied")); }
    catch { toast(t("apps.copyFail"), "err"); }
  };

  const growthEnabled = !win && !!summary?.config.enabled;
  const effectiveSort = (sortBy === "growth" && !growthEnabled) || (win && sortBy === "opened") ? "name" : sortBy;
  const weekGrowth = new Map<string, number>();
  if (growthEnabled) for (const g of growth) if (g.appPath && g.week) weekGrowth.set(g.appPath, g.week.bytes);
  const visible = sortApps(filterApps(filterByKind(apps, win ? "all" : filter, updates, Date.now() / 1000), q), effectiveSort, currentLang(), weekGrowth);
  const listRef = React.useRef<HTMLDivElement>(null);
  const lastQ = React.useRef(q);
  // 打字搜索不收起、不换位，保证跟手。
  const filtering = lastQ.current !== q;
  React.useEffect(() => { lastQ.current = q; });
  const rowsP = usePresence(visible, (a) => a.id, !filtering && !loading);
  const hiddenLeaves = React.useRef(new Set<string>());
  if (filtering) hiddenLeaves.current = new Set(rowsP.filter((row) => row.leaving).map((row) => row.key));
  const shownRows = rowsP.filter((row) => {
    if (!row.leaving) hiddenLeaves.current.delete(row.key);
    return !row.leaving || !hiddenLeaves.current.has(row.key);
  });
  const keys = visible.map((a) => a.id);
  const previousKeys = React.useRef(keys);
  const removing = previousKeys.current.some((key) => !keys.includes(key));
  React.useLayoutEffect(() => { previousKeys.current = keys; });
  useFlip(listRef, shownRows.map((row) => row.key), !filtering && !removing && !shownRows.some((row) => row.leaving));
  React.useLayoutEffect(() => {
    if (!filtering) return;
    listRef.current?.querySelectorAll<HTMLElement>(":scope > [data-key]").forEach((el) => {
      el.getAnimations().forEach((a) => { if (a.id === "cz-flip") a.cancel(); });
    });
  });

  const emptyKind = !win && (filter === "updates" || filter === "idle") && filterByKind(apps, filter, updates, Date.now() / 1000).length === 0;
  return (
    <div className="flex flex-col gap-4">
      <div className="app-filters">
        <div className="search-wrap">
          <Search className="h-[14px] w-[14px]" aria-hidden="true" />
          <input className="search" name="app-search" type="search" placeholder={t("apps.search")} aria-label={t("apps.search")} autoComplete="off" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <select className="select" name="app-sort" title={t("apps.sort")} aria-label={t("apps.sort")} value={effectiveSort} onChange={(e) => { const next = e.target.value as AppSortBy; setSortBy(next); saveAppSort(next); }}>
          <option value="name">{t("apps.sortName")}</option>
          <option value="size">{t("apps.sortSize")}</option>
          {!win && <option value="opened">{t("apps.sortOpened")}</option>}
          {growthEnabled && <option value="growth">{t("apps.sortGrowth")}</option>}
        </select>
      </div>
      {!win && filter === "updates" && !progress.finished && !checkError && !loading && !loadError && shownRows.length > 0 && <p className="muted small" role="status">{t("apps.checking", { done: progress.done, total: progress.total })}</p>}
      {checkError && <div className="empty err" role="alert">{t("apps.reason.network")}: {checkError}</div>}
      {loading && <div className="app-list">{Array.from({ length: 8 }, (_, i) => <div className="skeleton card" key={i} />)}</div>}
      {!loading && loadError && <div className="empty err" role="alert"><div className="err-icon">⚠</div><div>{t("apps.loadFail", { msg: loadError })}</div><button className="btn ghost retry-btn" type="button" onClick={() => void loadApps()}>{t("btn.retry")}</button></div>}
      {!loading && !loadError && shownRows.length === 0 && !(checkError && emptyKind && filter !== "idle") && (
        <div className="empty">{emptyKind ? (filter === "idle" ? t("apps.noIdle") : progress.finished ? t("apps.noUpdates") : t("apps.checking", { done: progress.done, total: progress.total })) : apps.length === 0 ? t("apps.empty") : t("apps.noMatch", { q: q.trim() })}</div>
      )}
      {!loading && !loadError && shownRows.length > 0 && <div className="app-list" ref={listRef}>
        {shownRows.map(({ item: a, key, leaving }) => {
          const g = growthEnabled ? growthFor(a, growth) : null;
          const brewBusy = brewBusyId !== null && brewBusyId !== a.id;
          return <AnimatedAppRow key={key} app={a} update={updates.get(a.id)} growthTag={growthEnabled ? growthTagFor(a, summary) : null} open={openId === a.id} busy={busy[a.id] ?? null} brewBusy={brewBusy} leaving={leaving} onToggle={(id) => setOpenId((prev) => prev === id ? null : id)} onUpgrade={(id) => void upgrade(id)}>
            <Collapse open={openId === a.id} animateResize>
              <AppRowExpanded app={a} update={updates.get(a.id)} checking={checking} usage={usage[a.id] ?? null} usageError={usageError[a.id] ?? null} week={g?.week ?? null} month={g?.month ?? null} busy={busy[a.id] ?? null} brewBusy={brewBusy} stuck={stuck.has(a.id)} upgradeFail={upgradeFail[a.id] ?? null}
                onUpgrade={(id) => void upgrade(id)} onCleanCache={(id) => void cleanCache(id)} onQuit={() => setQuitTarget(a)} onForceQuit={() => setForceTarget(a)} onUninstall={(id) => void openDetail(id)} onCopy={(text) => void copy(text)} />
            </Collapse>
          </AnimatedAppRow>;
        })}
      </div>}
      <UninstallDialog open={dialogOpen} stage={dialog.stage} detail={dialog.detail} failMsg={dialog.failMsg} onOpenChange={(v) => { if (!v) closeDialog(); }} onConfirm={() => void confirmUninstall()} onRefresh={() => { closeDialog(); void loadApps(); }} />
      <Dialog open={quitTarget !== null} onOpenChange={(v) => { if (!v) setQuitTarget(null); }}>
        <DialogContent>
          <DialogHeader><DialogTitle>{t("status.quit.title", { name: quitTarget?.name ?? "" })}</DialogTitle><DialogDescription>{t("status.quit.body", { name: quitTarget?.name ?? "" })}</DialogDescription></DialogHeader>
          <div className="mt-4 flex justify-end gap-2"><DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>{quitTarget && <Button onClick={() => void doQuit(quitTarget)}>{t("status.quit.ok")}</Button>}</div>
        </DialogContent>
      </Dialog>
      <Dialog open={forceTarget !== null} onOpenChange={(v) => { if (!v) setForceTarget(null); }}>
        <DialogContent>
          <DialogHeader><DialogTitle>{t("process.force.title", { name: forceTarget?.name ?? "" })}</DialogTitle><DialogDescription>{t("process.force.body")}</DialogDescription></DialogHeader>
          <div className="mt-4 flex justify-end gap-2"><DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>{forceTarget && <Button variant="destructive" onClick={() => void doForce(forceTarget)}>{t("process.force.ok")}</Button>}</div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
