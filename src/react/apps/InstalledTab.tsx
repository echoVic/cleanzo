import { isWindows } from "../../platform";
import * as React from "react";
import { Search } from "lucide-react";
import type { AppInfo, UninstallDetail } from "../../mygo";
import { useT } from "../../ui/i18n-react";
import { currentLang, t } from "../../i18n";
import { errText, fmtDate, human, toast } from "../../shared";
import { appsApi } from "./api";
import {
  bundleFailed, classifyUninstall, filterApps, readAppSort, saveAppSort, sortApps, type AppSortBy,
} from "./helpers";
import { UninstallDialog, type UninstallStage } from "./UninstallDialog";
import { useFlip } from "../../ui/motion/flip";
import { useLeaveAnimation, usePresence } from "../../ui/motion/presence";

interface InstalledTabProps {
  /** 该 tab 是否当前选中：首次激活时才懒加载应用列表。 */
  active: boolean;
  /** 向页脚汇报繁忙态（导航 busy）。 */
  onBusyChange: (busy: boolean) => void;
}

interface DialogState {
  stage: UninstallStage;
  detail: UninstallDetail | null;
  failMsg: string | null;
}

const DIALOG_IDLE: DialogState = { stage: "analyzing", detail: null, failMsg: null };

export function InstalledTab({ active, onBusyChange }: InstalledTabProps) {
  const t = useT();

  const [apps, setApps] = React.useState<AppInfo[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [loadError, setLoadError] = React.useState<string | null>(null);
  const [q, setQ] = React.useState("");
  const [sortBy, setSortBy] = React.useState<AppSortBy>(() => {
    const saved = readAppSort();
    return isWindows() && saved === "opened" ? "name" : saved;
  });
  const [busyPaths, setBusyPaths] = React.useState<string[]>([]);
  const [dialogOpen, setDialogOpen] = React.useState(false);
  const [dialog, setDialog] = React.useState<DialogState>(DIALOG_IDLE);

  // 异步分析 / 卸载里读最新值，避免闭包捕获旧 state。
  const seqRef = React.useRef(0);
  const loadedRef = React.useRef(false);

  const busy = loading || busyPaths.length > 0;
  React.useEffect(() => { onBusyChange(busy); }, [busy, onBusyChange]);

  const loadApps = React.useCallback(async () => {
    if (loading) return;
    setLoading(true);
    setLoadError(null);
    try {
      const list = await appsApi.listApps();
      setApps(list);
    } catch (e) {
      setApps([]);
      setLoadError(e instanceof Error ? e.message : String(e));
      toast(t("apps.loadFail", { msg: errText(e) }), "err");
    } finally {
      setLoading(false);
    }
  }, [loading, t]);

  // 首次进入「已安装」才加载；root 复用后再次进入不重拉。
  React.useEffect(() => {
    if (active && !loadedRef.current) {
      loadedRef.current = true;
      void loadApps();
    }
  }, [active, loadApps]);

  const onSortChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    const next = e.target.value as AppSortBy;
    setSortBy(next);
    saveAppSort(next);
  };

  const closeDialog = React.useCallback(() => {
    seqRef.current++; // 关掉后晚到的分析结果一律丢弃
    setDialogOpen(false);
  }, []);

  const openDetail = React.useCallback(async (path: string) => {
    const seq = ++seqRef.current;
    setDialog(DIALOG_IDLE);
    setDialogOpen(true);
    try {
      const d = await appsApi.uninstallDetail(path);
      if (seq !== seqRef.current) return;
      setDialog({ stage: "detail", detail: d, failMsg: null });
    } catch (e) {
      if (seq !== seqRef.current) return;
      setDialog({ stage: "fail", detail: null, failMsg: errText(e) });
    }
  }, []);

  const confirmUninstall = React.useCallback(async () => {
    const d = dialog.detail;
    if (!d) return;
    closeDialog();
    const path = d.app.id;
    const seq = seqRef.current;
    setBusyPaths((prev) => [...prev, path]);
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
      if (!bundleFailed(res)) setApps((prev) => prev.filter((a) => a.id !== res.path));
    } catch (e) {
      toast(t("apps.fail", { msg: errText(e) }), "err");
    } finally {
      setBusyPaths((prev) => prev.filter((p) => p !== path));
    }
  }, [dialog.detail, closeDialog, t]);

  // ---- 派生视图 ----
  const visible = sortApps(filterApps(apps, q), sortBy, currentLang());
  const listRef = React.useRef<HTMLDivElement>(null);
  const lastQ = React.useRef(q);
  // 这次更新是打字搜索：不收起、不换位，保证跟手（设计 6.6）
  const filtering = lastQ.current !== q;
  React.useEffect(() => { lastQ.current = q; });
  const rowsP = usePresence(visible, (a) => a.id, !filtering && !loading);
  const hiddenLeaves = React.useRef(new Set<string>());
  if (filtering) hiddenLeaves.current = new Set(rowsP.filter((row) => row.leaving).map((row) => row.key));
  // 搜索也立即丢掉此前仍在收起的行，之后的卸载仍正常离场。
  const shownRows = rowsP.filter((row) => {
    if (!row.leaving) hiddenLeaves.current.delete(row.key);
    return !row.leaving || !hiddenLeaves.current.has(row.key);
  });
  const keys = visible.map((a) => a.id);
  const previousKeys = React.useRef(keys);
  const removing = previousKeys.current.some((key) => !keys.includes(key));
  React.useLayoutEffect(() => { previousKeys.current = keys; });
  // 收起负责列表补位；宽窗口的网格在离场项移除后再滑到新格。
  useFlip(listRef, shownRows.map((row) => row.key), !filtering && !removing && !shownRows.some((row) => row.leaving));
  React.useLayoutEffect(() => {
    if (!filtering) return;
    listRef.current?.querySelectorAll<HTMLElement>(":scope > [data-key]").forEach((el) => {
      el.getAnimations().forEach((a) => { if (a.id === "cz-flip") a.cancel(); });
    });
  });

  return (
    <div className="flex flex-col gap-4">
      <div className="app-filters">
        <div className="search-wrap">
          <Search className="h-[14px] w-[14px]" aria-hidden="true" />
          <input
            className="search"
            placeholder={t("apps.search")}
            autoComplete="off"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
        </div>
        <select className="select" title={t("apps.sort")} value={sortBy} onChange={onSortChange}>
          <option value="name">{t("apps.sortName")}</option>
          <option value="size">{t("apps.sortSize")}</option>
          {!isWindows() && <option value="opened">{t("apps.sortOpened")}</option>}
        </select>
      </div>

      {loading && (
        <div className="app-list">
          {Array.from({ length: 8 }, (_, i) => <div className="skeleton card" key={i} />)}
        </div>
      )}

      {!loading && loadError && (
        <div className="empty err">
          <div className="err-icon">⚠</div>
          <div>{t("apps.loadFail", { msg: loadError })}</div>
          <button className="btn ghost retry-btn" type="button" onClick={() => void loadApps()}>
            {t("btn.retry")}
          </button>
        </div>
      )}

      {!loading && !loadError && apps.length === 0 && shownRows.length === 0 && (
        <div className="empty">{t("apps.empty")}</div>
      )}

      {!loading && !loadError && apps.length > 0 && visible.length === 0 && shownRows.length === 0 && (
        <div className="empty">{t("apps.noMatch", { q: q.trim() })}</div>
      )}

      {!loading && !loadError && shownRows.length > 0 && (
        <div className="app-list" ref={listRef}>
          {shownRows.map(({ item: a, key, leaving }) => (
            <AnimatedInstalledAppRow key={key} app={a} leaving={leaving} moving={busyPaths.includes(a.id)} onDetail={(id) => void openDetail(id)} />
          ))}
        </div>
      )}

      <UninstallDialog
        open={dialogOpen}
        stage={dialog.stage}
        detail={dialog.detail}
        failMsg={dialog.failMsg}
        onOpenChange={(v) => { if (!v) closeDialog(); }}
        onConfirm={() => void confirmUninstall()}
        onRefresh={() => { closeDialog(); void loadApps(); }}
      />
    </div>
  );
}

interface InstalledAppRowProps {
  app: AppInfo; moving: boolean; onDetail: (id: string) => void; leaving?: boolean;
}

// 动效 hook 单独挂载，导出的纯行仍可直接调用（现有事件夹具）。
function AnimatedInstalledAppRow(props: InstalledAppRowProps) {
  const ref = React.useRef<HTMLDivElement>(null);
  useLeaveAnimation(ref, !!props.leaving);
  return React.cloneElement(InstalledAppRow(props), { ref });
}

/** 行动作只传稳定 ID；Windows 的安装位置不作为操作参数。 */
export function InstalledAppRow({ app: a, moving, onDetail, leaving = false }: InstalledAppRowProps) {
  return (
    <div className="app-row card" key={a.id} data-key={a.id} aria-hidden={leaving || undefined} inert={leaving || undefined}>
      <div className="app-icon">{a.name.charAt(0).toUpperCase()}</div>
      <div className="app-main">
        <div className="app-name">
          {a.name}
          {a.running && <span className="tag ok"> {t("apps.running")}</span>}
          {a.version && <span className="ver muted"> {a.version}</span>}
        </div>
        <div className="app-path muted" title={isWindows() ? a.publisher : a.path}>{isWindows() ? a.publisher : a.path}</div>
      </div>
      <div className="app-meta">
        <div className="app-size">{human(a.size)}</div>
        {(!isWindows() || a.lastOpened !== 0) && <div className="app-used muted small">
          {a.lastOpened ? fmtDate(a.lastOpened) : t("apps.never")}
        </div>}
      </div>
      <button
        className="btn small uninstall-detail"
        type="button"
        disabled={moving}
        onClick={() => void onDetail(a.id)}
      >
        {moving && <span className="spin" />}
        {moving ? t("apps.moving") : t("apps.detail")}
      </button>
    </div>
  );
}
