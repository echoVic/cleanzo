import * as React from "react";
import { Search } from "lucide-react";
import type { AppInfo, UninstallDetail } from "../../mygo";
import { useT } from "../../ui/i18n-react";
import { currentLang } from "../../i18n";
import { errText, fmtDate, human, toast } from "../../shared";
import { appsApi } from "./api";
import {
  bundleFailed, classifyUninstall, filterApps, readAppSort, saveAppSort, sortApps, type AppSortBy,
} from "./helpers";
import { UninstallDialog, type UninstallStage } from "./UninstallDialog";

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
  const [sortBy, setSortBy] = React.useState<AppSortBy>(() => readAppSort());
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
    const path = d.app.path;
    setBusyPaths((prev) => [...prev, path]);
    try {
      const res = await appsApi.uninstallApp(path);
      const name = d.app.name;
      const outcome = classifyUninstall(res);
      if (outcome === "inUse") {
        toast(t("apps.inUse", { name }), "err");
      } else if (outcome === "partial") {
        toast(t("apps.partial", { name, n: res.failed.length }), "err");
      } else {
        toast(t("apps.ok", { name, size: human(res.freed) }));
      }
      if (!bundleFailed(res)) setApps((prev) => prev.filter((a) => a.path !== res.path));
    } catch (e) {
      toast(t("apps.fail", { msg: errText(e) }), "err");
    } finally {
      setBusyPaths((prev) => prev.filter((p) => p !== path));
    }
  }, [dialog.detail, closeDialog, t]);

  // ---- 派生视图 ----
  const visible = sortApps(filterApps(apps, q), sortBy, currentLang());

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
          <option value="opened">{t("apps.sortOpened")}</option>
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

      {!loading && !loadError && apps.length === 0 && (
        <div className="empty">{t("apps.empty")}</div>
      )}

      {!loading && !loadError && apps.length > 0 && visible.length === 0 && (
        <div className="empty">{t("apps.noMatch", { q: q.trim() })}</div>
      )}

      {!loading && !loadError && visible.length > 0 && (
        <div className="app-list">
          {visible.map((a) => {
            const moving = busyPaths.includes(a.path);
            return (
              <div className="app-row card" key={a.path}>
                <div className="app-icon">{a.name.charAt(0).toUpperCase()}</div>
                <div className="app-main">
                  <div className="app-name">
                    {a.name}
                    {a.running && <span className="tag ok"> {t("apps.running")}</span>}
                    {a.version && <span className="ver muted"> {a.version}</span>}
                  </div>
                  <div className="app-path muted" title={a.path}>{a.path}</div>
                </div>
                <div className="app-meta">
                  <div className="app-size">{human(a.size)}</div>
                  <div className="app-used muted small">
                    {a.lastOpened ? fmtDate(a.lastOpened) : t("apps.never")}
                  </div>
                </div>
                <button
                  className="btn small uninstall-detail"
                  type="button"
                  disabled={moving}
                  onClick={() => void openDetail(a.path)}
                >
                  {moving && <span className="spin" />}
                  {moving ? t("apps.moving") : t("apps.detail")}
                </button>
              </div>
            );
          })}
        </div>
      )}

      <UninstallDialog
        open={dialogOpen}
        stage={dialog.stage}
        detail={dialog.detail}
        failMsg={dialog.failMsg}
        onOpenChange={(v) => { if (!v) closeDialog(); }}
        onConfirm={() => void confirmUninstall()}
      />
    </div>
  );
}
