import * as React from "react";
import { FolderSearch } from "lucide-react";
import type { PhotoCluster } from "../../mygo";
import { Button } from "../../ui/components/button";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogClose,
} from "../../ui/components/dialog";
import { useT } from "../../ui/i18n-react";
import { baseName, errText, toast } from "../../shared";
import { human } from "../ai/format";
import {
  effectiveRemoved, emptySelection, isGone, subtractRemoved, toggleRow, type ListSelection,
} from "../list/selection";
import { photosApi, clearPreviewCache } from "./api";
import {
  allChecked, defaultPickedPaths, liveSel, removeClusters, someChecked,
  visibleClusters, visibleItems, visibleSelectablePaths,
} from "./helpers";
import { PhotoViewer } from "./PhotoViewer";

export function PhotosPage() {
  const t = useT();

  const [clusters, setClusters] = React.useState<PhotoCluster[]>([]);
  const [scanned, setScanned] = React.useState(0);
  /** 是否已开始过扫描：只有首次进入自动开始，之后再进不重扫。 */
  const [tried, setTried] = React.useState(false);
  const [scanning, setScanning] = React.useState(false);
  const [stopped, setStopped] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [progress, setProgress] = React.useState<{ processed: number; total: number } | null>(null);
  const [selection, setSelection] = React.useState<ListSelection>(emptySelection());
  const [trashOpen, setTrashOpen] = React.useState(false);
  const [viewer, setViewer] = React.useState<{ clusterId: number; path: string } | null>(null);
  const [viewerOpener, setViewerOpener] = React.useState<HTMLElement | null>(null);

  // 异步循环里读最新值，避免闭包捕获旧 state。
  const runRef = React.useRef(0);
  const chanRef = React.useRef<{ close(): void } | null>(null);
  const stoppedRef = React.useRef(false);
  const scanningRef = React.useRef(false);
  const clustersRef = React.useRef(clusters);
  clustersRef.current = clusters;
  scanningRef.current = scanning;

  // 离开页面：让旧流式循环停止消费并关闭 Channel，避免未处理 rejection。
  React.useEffect(() => {
    return () => {
      runRef.current++;
      chanRef.current?.close();
      chanRef.current = null;
    };
  }, []);

  const startScan = React.useCallback(async () => {
    const myRun = ++runRef.current;
    stoppedRef.current = false;
    setViewer(null);
    clearPreviewCache();
    setTried(true);
    setClusters([]);
    setScanned(0);
    setError(null);
    setStopped(false);
    setProgress(null);
    setSelection(emptySelection());
    setScanning(true);
    const { ch, done } = photosApi.startScan();
    chanRef.current = ch;
    try {
      for await (const ev of ch) {
        if (runRef.current !== myRun) continue; // 被新扫描取代：丢弃旧事件
        if (ev.kind === "progress") {
          setProgress({ processed: ev.processed ?? 0, total: ev.total ?? 0 });
        } else if (ev.kind === "done" && ev.result) {
          setClusters(ev.result.clusters);
          setScanned(ev.result.scanned);
          // 默认只勾「完全相同」簇里非建议保留的副本。
          setSelection({ ...emptySelection(), sel: new Set(defaultPickedPaths(ev.result.clusters)) });
        }
      }
      await done;
    } catch (e) {
      if (runRef.current !== myRun) return;
      if (!stoppedRef.current) {
        setError(e instanceof Error ? e.message : String(e));
        setClusters([]);
        toast(t("photos.fail", { msg: errText(e) }), "err");
      }
    } finally {
      if (runRef.current !== myRun) return;
      chanRef.current = null;
      setScanning(false);
      setProgress(null);
      setStopped(stoppedRef.current);
      stoppedRef.current = false;
    }
  }, [t]);

  // 首次进入视图自动开始一次扫描（root 复用；之后切语言重渲染不再重扫）。
  const autoStartedRef = React.useRef(false);
  React.useEffect(() => {
    if (autoStartedRef.current) return;
    autoStartedRef.current = true;
    void startScan();
  }, [startScan]);

  const stopScanNow = React.useCallback(() => {
    if (!scanningRef.current) return;
    stoppedRef.current = true;
    chanRef.current?.close();
  }, []);

  // ---- 派生视图 ----
  const visible = visibleClusters(clusters, selection);
  const selectable = visibleSelectablePaths(visible, selection);
  const picked = liveSel(selection);
  const selSize = React.useMemo(() => {
    const paths = new Set(picked);
    return clusters
      .flatMap((c) => c.items)
      .filter((it) => paths.has(it.path) && it.size >= 0)
      .reduce((sum, it) => sum + it.size, 0);
  }, [clusters, picked]);
  const all = allChecked(selectable, selection.sel);
  const some = someChecked(selectable, selection.sel);

  const viewerCluster = viewer ? visible.find((c) => c.id === viewer.clusterId) : undefined;
  const viewerItems = viewerCluster ? visibleItems(viewerCluster, selection) : [];
  // 查看项被删走（批量清理 / 收敛）：关闭查看器。
  React.useEffect(() => {
    if (viewer && !viewerItems.some((it) => it.path === viewer.path)) setViewer(null);
  }, [viewer, viewerItems]);

  // ---- 勾选 / 动作 ----
  const togglePath = (path: string, on: boolean) =>
    setSelection((prev) => toggleRow(prev, path, on));

  const toggleAll = (on: boolean) => {
    setSelection((prev) => {
      const sel = new Set(prev.sel);
      for (const p of selectable) (on ? sel.add(p) : sel.delete(p));
      return { ...prev, sel };
    });
  };

  const keepOnly = (clusterId: number) => {
    if (busy) return;
    setSelection((prev) => {
      const cl = clustersRef.current.find((c) => c.id === clusterId);
      if (!cl) return prev;
      const sel = new Set(prev.sel);
      for (const it of visibleItems(cl, prev)) {
        if (it.keep) sel.delete(it.path);
        else sel.add(it.path);
      }
      return { ...prev, sel };
    });
  };

  const reveal = (path: string) => {
    photosApi.reveal(path).catch((e) => toast(t("fs.revealFail", { msg: errText(e) }), "err"));
  };

  const openViewer = (path: string) => {
    const cl = clusters.find((c) => c.items.some((it) => it.path === path));
    if (!cl || isGone(selection, path)) return;
    setViewerOpener(document.activeElement as HTMLElement | null);
    setViewer({ clusterId: cl.id, path });
  };

  const onTrashChecked = () => {
    if (picked.length === 0 || busy) return;
    setTrashOpen(true);
  };

  const doTrashBatch = async () => {
    setTrashOpen(false);
    const paths = liveSel(selection);
    if (paths.length === 0) return;
    setBusy(true);
    try {
      const r = await photosApi.cleanPaths(paths);
      const parts: string[] = [];
      if (r.moved > 0) parts.push(t("photos.trashOk", { n: r.moved, size: human(r.freed) }));
      if (r.inUse > 0) parts.push(t("photos.inUse", { n: r.inUse }));
      if (r.failed.length > 0) parts.push(t("photos.trashFail", { n: r.failed.length }));
      toast(parts.join(t("phrase.sep")) || t("photos.none"), r.moved > 0 ? "ok" : "err");
      if (r.removed.length > 0) {
        setClusters((prev) => removeClusters(prev, r.removed));
        setSelection((prev) => subtractRemoved(prev, r.removed));
      }
    } catch (e) {
      toast(t("fs.actionFail", { msg: errText(e) }), "err");
    } finally {
      setBusy(false);
    }
  };

  const pct = progress && progress.total > 0
    ? Math.max(0, Math.min(100, (progress.processed / progress.total) * 100))
    : 0;
  const scanButtonLabel = scanning
    ? t("scan.stop")
    : (error || stopped || tried)
      ? t("fs.rescan")
      : t("photos.scanBtn");
  const showStoppedEmpty = !scanning && stopped && visible.length === 0;
  const showEmptyScanned = !scanning && !error && !stopped && visible.length === 0 && tried;

  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h2>{t("nav.photos")}</h2>
          <p className="muted">{t("photos.sub")}</p>
        </div>
        <div className="head-actions">
          <Button
            data-ph-scan-btn
            onClick={() => (scanning ? stopScanNow() : void startScan())}
            disabled={busy && !scanning}
          >
            {scanning && <span className="spin" />}
            {scanButtonLabel}
          </Button>
        </div>
      </header>

      {scanning && (
        <div className="fs-progress" style={{ display: "flex" }}>
          <div className="spinner" />
          <div className="fs-progress-text">
            <div className="muted small">
              {progress
                ? t("photos.progress", {
                    n: progress.processed.toLocaleString(),
                    total: progress.total.toLocaleString(),
                  })
                : t("photos.busy")}
            </div>
            <div className="hero-progress"><div className="hero-bar" style={{ width: `${pct}%` }} /></div>
          </div>
        </div>
      )}

      {!scanning && error && (
        <div className="empty err">
          <div className="err-icon">⚠</div>
          <div>{t("photos.fail", { msg: errText(error) })}</div>
          <button className="btn ghost retry-btn" type="button" onClick={() => void startScan()}>
            {t("btn.retry")}
          </button>
        </div>
      )}

      {showStoppedEmpty && <div className="empty">{t("scan.stopped")}</div>}
      {showEmptyScanned && <div className="empty">{t("photos.empty")}</div>}

      {selectable.length > 0 && (
        <div className="fs-toolbar">
          <label className="fs-check">
            <input
              type="checkbox"
              checked={all}
              ref={(el) => { if (el) el.indeterminate = !all && some; }}
              disabled={busy}
              onChange={(e) => toggleAll(e.target.checked)}
            />
          </label>
          <span className="fs-toolbar-count">
            {picked.length > 0
              ? t("photos.checkedSize", { n: picked.length, size: human(selSize) })
              : t("photos.nothing")}
          </span>
          <button
            className="btn danger small"
            type="button"
            disabled={picked.length === 0 || busy}
            onClick={onTrashChecked}
          >
            {busy && <span className="spin" />}
            {busy ? t("photos.moving") : t("photos.clean")}
          </button>
        </div>
      )}

      <section className="fs-list photo-clusters" aria-live="polite">
        {visible.map((cl) => {
          const items = visibleItems(cl, selection);
          return (
            <div className="photo-cluster card" key={cl.id} data-cluster={cl.id}>
              <div className="photo-cluster-head">
                <span className="photo-cluster-title">
                  {t("photos.cluster", { id: cl.id })}
                  <span className={`photo-badge ${cl.kind === "exact" ? "exact" : "similar"}`}>
                    {cl.kind === "exact" ? t("photos.kindExact") : t("photos.kindSimilar")}
                  </span>
                </span>
                <span className="photo-cluster-meta muted">
                  {t("photos.photoCount", { n: items.length })} · {t("photos.waste", { size: human(cl.waste) })}
                </span>
                <button
                  className="btn small ghost"
                  type="button"
                  disabled={busy}
                  onClick={() => keepOnly(cl.id)}
                >
                  {t("photos.keepOnly")}
                </button>
              </div>
              <div className="photo-grid">
                {items.map((it) => (
                  <figure className={`photo-cell${it.keep ? " keep" : ""}`} key={it.path}>
                    <div className="photo-thumb-wrap">
                      <label className="fs-check" title={it.path}>
                        <input
                          type="checkbox"
                          checked={selection.sel.has(it.path)}
                          disabled={busy}
                          onChange={(e) => togglePath(it.path, e.target.checked)}
                        />
                      </label>
                      {it.keep && <span className="photo-keep-tag">{t("photos.keep")}</span>}
                      <img
                        className="photo-thumb"
                        src={it.thumb}
                        alt=""
                        loading="lazy"
                        onClick={() => togglePath(it.path, !selection.sel.has(it.path))}
                      />
                    </div>
                    <figcaption className="photo-meta" title={it.path}>
                      <div className="photo-name">{baseName(it.path)}</div>
                      <div className="photo-sub muted small">
                        {t("photos.sizeDim", { w: it.width, h: it.height })} · {human(it.size)}
                      </div>
                    </figcaption>
                    <div className="photo-actions">
                      <button
                        className="btn small ghost icon"
                        type="button"
                        title={t("photos.viewLarger")}
                        aria-label={t("photos.viewLarger")}
                        data-preview={it.path}
                        onClick={() => openViewer(it.path)}
                      >
                        <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                          <circle cx="10" cy="10" r="6.5" />
                          <path d="m15 15 6 6M7 10h6M10 7v6" />
                        </svg>
                      </button>
                      <button
                        className="btn small ghost icon"
                        type="button"
                        title={t("fs.reveal")}
                        aria-label={t("fs.reveal")}
                        onClick={() => reveal(it.path)}
                      >
                        <FolderSearch className="h-[15px] w-[15px]" />
                      </button>
                    </div>
                  </figure>
                ))}
              </div>
            </div>
          );
        })}
        {!scanning && visible.length > 0 && (
          <div className="fs-foot muted small">
            {t("photos.count", { n: scanned.toLocaleString(), c: visible.length })}
          </div>
        )}
      </section>

      {viewer && viewerItems.length > 0 && (
        <PhotoViewer
          items={viewerItems}
          path={viewer.path}
          checked={selection.sel.has(viewer.path)}
          busy={busy}
          opener={viewerOpener}
          onNavigate={(p) => setViewer({ clusterId: viewer.clusterId, path: p })}
          onToggle={(on) => togglePath(viewer.path, on)}
          onClose={() => setViewer(null)}
        />
      )}

      <Dialog open={trashOpen} onOpenChange={(v) => { if (!v) setTrashOpen(false); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("photos.clean")}</DialogTitle>
            <DialogDescription>
              {t("photos.trashBody", { n: picked.length })}
            </DialogDescription>
          </DialogHeader>
          <div className="mt-4 flex justify-end gap-2">
            <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
            <Button variant="destructive" onClick={() => void doTrashBatch()}>{t("fs.trash.confirm")}</Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
