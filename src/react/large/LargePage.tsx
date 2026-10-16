import * as React from "react";
import { RefreshCw } from "lucide-react";
import type { LargeFile, LargeScanResult } from "../../mygo";
import { Button } from "../../ui/components/button";
import { EmptyGuide } from "../../ui/components/empty-guide";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogClose,
} from "../../ui/components/dialog";
import { useT } from "../../ui/i18n-react";
import { errText, toast } from "../../shared";
import {
  applyRemoval, emptySelection, isGone, pickedPaths, toggleChild, toggleRow,
  type ListSelection,
} from "../list/selection";
import { ListRows, ListToolbar, type ListRowData } from "../list/ListRows";
import { largeApi } from "./api";
import {
  barWidth, displayPath, human, LARGE_THRESHOLDS, readThreshold, saveThreshold,
  topBySize, visibleAfterThreshold,
} from "./format";

export function LargePage() {
  const t = useT();

  const [threshold, setThreshold] = React.useState<number>(() => readThreshold());
  const [items, setItems] = React.useState<LargeFile[]>([]);
  const [result, setResult] = React.useState<LargeScanResult | null>(null);
  const [scanning, setScanning] = React.useState(false);
  const [stopped, setStopped] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [tried, setTried] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [currentPath, setCurrentPath] = React.useState("");
  const [selection, setSelection] = React.useState<ListSelection>(emptySelection());
  const [trashTarget, setTrashTarget] = React.useState<{ path: string; size: number } | null>(null);
  const [trashBatchOpen, setTrashBatchOpen] = React.useState(false);

  // 异步循环里读最新值，避免闭包捕获旧 state。
  const runRef = React.useRef(0);
  const chanRef = React.useRef<{ close(): void } | null>(null);
  const stoppedRef = React.useRef(false);
  const restartQueuedRef = React.useRef(false);
  const thresholdRef = React.useRef(threshold);
  const scannedThresholdRef = React.useRef<number | null>(null);
  const itemsRef = React.useRef(items);
  const resultRef = React.useRef(result);
  const scanningRef = React.useRef(scanning);
  thresholdRef.current = threshold;
  itemsRef.current = items;
  resultRef.current = result;
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
    restartQueuedRef.current = false;
    const thr = thresholdRef.current;
    scannedThresholdRef.current = thr;
    setTried(true);
    setItems([]);
    setResult(null);
    setError(null);
    setStopped(false);
    setCurrentPath("");
    setSelection(emptySelection());
    setScanning(true);
    const { ch, done } = largeApi.startScan(thr);
    chanRef.current = ch;
    try {
      for await (const ev of ch) {
        if (runRef.current !== myRun) continue; // 被新扫描取代：丢弃旧事件
        if (ev.kind === "item" && ev.path) {
          setCurrentPath(ev.path);
          setItems((prev) => [...prev, {
            path: ev.path!, size: ev.size ?? 0, trashable: ev.trashable ?? false,
          }]);
        } else if (ev.kind === "done" && ev.result) {
          setItems(ev.result.items);
          setResult(ev.result);
          scannedThresholdRef.current = ev.result.threshold;
        }
      }
      await done;
    } catch (e) {
      if (runRef.current !== myRun) return;
      if (!stoppedRef.current) {
        setError(e instanceof Error ? e.message : String(e));
        setItems([]);
        setResult(null);
        toast(t("large.fail", { msg: errText(e) }), "err");
      }
    } finally {
      if (runRef.current !== myRun) return;
      chanRef.current = null;
      setScanning(false);
      if (restartQueuedRef.current) {
        restartQueuedRef.current = false;
        stoppedRef.current = false;
        void startScan();
      } else {
        setStopped(stoppedRef.current);
        stoppedRef.current = false;
      }
    }
  }, [t]);

  const stopScanNow = React.useCallback(() => {
    if (!scanningRef.current) return;
    stoppedRef.current = true;
    chanRef.current?.close();
  }, []);

  /** 阈值提高且已有更宽松阈值的结果：只前端过滤，剪掉不再可见的勾选。 */
  const pruneSelection = (thr: number) => {
    const visible = new Set(itemsRef.current.filter((it) => it.size >= thr).map((it) => it.path));
    setSelection((prev) => {
      const sel = new Set([...prev.sel].filter((p) => visible.has(p)));
      return { ...prev, sel };
    });
  };

  const onThresholdChange = (e: React.ChangeEvent<HTMLSelectElement>) => {
    const next = Number(e.target.value);
    if (next === thresholdRef.current) return;
    thresholdRef.current = next;
    setThreshold(next);
    saveThreshold(next);
    const scanned = scannedThresholdRef.current;
    const hasResults = resultRef.current !== null || itemsRef.current.length > 0;
    if (restartQueuedRef.current) {
      pruneSelection(next);
      return;
    }
    if (scanned !== null && next >= scanned && hasResults) {
      pruneSelection(next);
      return;
    }
    if (scanningRef.current) {
      // 降低下限且正在扫：停旧扫，结束后自动重扫。
      restartQueuedRef.current = true;
      stoppedRef.current = true;
      chanRef.current?.close();
    } else {
      void startScan();
    }
  };

  // ---- 派生视图 ----
  const visible = visibleAfterThreshold(items, threshold).filter((it) => !isGone(selection, it.path));
  const sorted = topBySize(visible, 50);
  const maxSize = sorted[0]?.size || 1;
  const rows: ListRowData[] = sorted.map((it, i) => ({
    path: it.path,
    name: displayPath(it.path),
    size: it.size,
    rank: `${i + 1}`,
    trashable: it.trashable,
    sub: (
      <div className="fs-track">
        <div className="fs-fill" style={{ width: `${barWidth(it.size, maxSize)}%` }} />
      </div>
    ),
  }));
  const selectableRows = rows.filter((r) => r.trashable && !isGone(selection, r.path)).map((r) => r.path);
  const sizeOf = (path: string) => items.find((it) => it.path === path)?.size;

  // ---- 勾选 / 展开 / 动作 ----
  const onToggleRow = (path: string, on: boolean) => setSelection((prev) => toggleRow(prev, path, on));
  const onToggleChild = (parent: string, child: string, on: boolean) =>
    setSelection((prev) => toggleChild(prev, parent, child, on));
  const onExpand = (path: string) => {
    // 大文件行是单个文件、不可展开；目录展开逻辑由 fullscan 页接 dirChildren 复用。
    void path;
  };
  const onReveal = (path: string) => {
    largeApi.reveal(path).catch((e) => toast(t("fs.revealFail", { msg: errText(e) }), "err"));
  };
  const onToggleAll = (on: boolean) => {
    setSelection((prev) => {
      let next = prev;
      for (const p of selectableRows) next = toggleRow(next, p, on);
      return next;
    });
  };

  const applyItemsRemoval = (removed: string[]) => {
    const upd = applyRemoval(itemsRef.current, selection, removed);
    setItems(upd.items);
    setSelection(upd.selection);
    if (resultRef.current) setResult((r) => (r ? { ...r, items: upd.items } : r));
  };

  const onTrashRow = (path: string, size: number) => {
    if (busy || isGone(selection, path)) return;
    setTrashTarget({ path, size });
  };

  const doTrashRow = async () => {
    const target = trashTarget;
    setTrashTarget(null);
    if (!target) return;
    setBusy(true);
    try {
      const res = await largeApi.trashPath(target.path);
      toast(t("fs.trash.ok", { path: displayPath(target.path), size: human(res.freed) }));
      applyItemsRemoval([target.path]);
    } catch (e) {
      toast(t("fs.actionFail", { msg: errText(e) }), "err");
    } finally {
      setBusy(false);
    }
  };

  const onTrashChecked = () => {
    const paths = pickedPaths(selection);
    if (paths.length === 0 || busy) return;
    setTrashBatchOpen(true);
  };

  const doTrashBatch = async () => {
    setTrashBatchOpen(false);
    const paths = pickedPaths(selection);
    if (paths.length === 0) return;
    setBusy(true);
    try {
      const r = await largeApi.cleanPaths(paths);
      const parts: string[] = [];
      if (r.moved > 0) parts.push(t("fs.trashChecked.ok", { n: r.moved, size: human(r.freed) }));
      if (r.inUse > 0) parts.push(t("fs.trashChecked.inUse", { n: r.inUse }));
      if (r.failed.length > 0) parts.push(t("fs.trashChecked.fail", { n: r.failed.length }));
      toast(
        parts.join(t("phrase.sep")) || t("fs.trashChecked.none"),
        r.moved > 0 && r.failed.length === 0 ? "ok" : "err",
      );
      applyItemsRemoval(r.removed);
    } catch (e) {
      toast(t("fs.actionFail", { msg: errText(e) }), "err");
    } finally {
      setBusy(false);
    }
  };

  const scanButtonLabel = scanning
    ? t("scan.stop")
    : (error || stopped || result || tried)
      ? t("fs.rescan")
      : t("large.scanBtn");

  const showIdle = !scanning && !error && !stopped && sorted.length === 0 && !tried;
  const showEmptyScanned = !scanning && !error && !stopped && sorted.length === 0 && tried;
  const showStoppedEmpty = !scanning && stopped && sorted.length === 0;

  return (
    <div className="cz-react px-1">
      <header className="view-head">
        <div>
          <h2>{t("nav.large")}</h2>
          <p className="muted">{t("large.sub")}</p>
        </div>
        <div className="head-actions">
          <Button onClick={() => (scanning ? stopScanNow() : void startScan())} disabled={busy && !scanning}>
            {scanning && <span className="spin" />}
            {scanButtonLabel}
          </Button>
        </div>
      </header>

      <label className="muted small fs-hint threshold-control">
        <span>{t("large.thresholdBefore")}</span>
        <select
          className="select"
          title={t("large.minSize")}
          value={threshold}
          onChange={(e) => onThresholdChange(e)}
        >
          <option value={LARGE_THRESHOLDS[0]}>{t("large.size100")}</option>
          <option value={LARGE_THRESHOLDS[1]}>{t("large.size500")}</option>
          <option value={LARGE_THRESHOLDS[2]}>{t("large.size1")}</option>
          <option value={LARGE_THRESHOLDS[3]}>{t("large.size5")}</option>
        </select>
        <span>{t("large.thresholdAfter")}</span>
      </label>

      {scanning && (
        <div className="fs-progress" style={{ display: "flex" }}>
          <div className="spinner" />
          <div className="fs-progress-text">
            <div>{currentPath ? t("fs.scanning", { path: displayPath(currentPath) }) : t("fs.prep")}</div>
            <div className="muted small">{t("large.count", { n: visible.length.toLocaleString() })}</div>
          </div>
        </div>
      )}

      {showIdle && (
        <EmptyGuide
          gradId="eg-large-grad"
          title={t("large.idle.title")}
          desc={t("large.idle.desc", { size: human(threshold) })}
          ctaText={t("large.idle.cta")}
          onCta={() => void startScan()}
        >
          <path d="M7 4.8h6.5l4 4v10.4H7z" />
          <path d="M13.5 4.8v4h4M10 12.5h4.5M10 15.5h4.5" />
        </EmptyGuide>
      )}

      {showEmptyScanned && (
        <div className="empty">{t("large.empty", { size: human(threshold) })}</div>
      )}

      {showStoppedEmpty && <div className="empty">{t("scan.stopped")}</div>}

      {!scanning && error && (
        <div className="empty err">
          <div className="err-icon">⚠</div>
          <div>{t("large.fail", { msg: errText(error) })}</div>
          <button className="btn ghost retry-btn" type="button" onClick={() => void startScan()}>
            {t("btn.retry")}
          </button>
        </div>
      )}

      {(sorted.length > 0 || scanning) && (
        <>
          <ListToolbar
            selectableRows={selectableRows}
            selection={selection}
            busy={busy}
            sizeOf={sizeOf}
            onToggleAll={onToggleAll}
            onTrashChecked={onTrashChecked}
          />
          <ListRows
            rows={rows}
            selection={selection}
            busy={busy}
            onToggleRow={onToggleRow}
            onToggleChild={onToggleChild}
            onExpand={onExpand}
            onReveal={onReveal}
            onTrashRow={onTrashRow}
          />
          {scanning && <div className="fs-foot muted small">{t("large.live")}</div>}
          {!scanning && stopped && sorted.length > 0 && (
            <div className="fs-foot muted small">{t("scan.stopped")}</div>
          )}
          {!scanning && !stopped && result && (
            <div className="fs-foot muted small">
              {t("large.foot", {
                n: result.scanned.toLocaleString(),
                t: (result.elapsedMs / 1000).toFixed(1),
                s: result.skipped.toLocaleString(),
              })}
            </div>
          )}
        </>
      )}

      <Dialog open={trashTarget !== null} onOpenChange={(v) => { if (!v) setTrashTarget(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("fs.trash.title")}</DialogTitle>
            <DialogDescription>
              {t("fs.trash.body", {
                path: trashTarget ? displayPath(trashTarget.path) : "",
                size: trashTarget ? human(trashTarget.size) : "",
              })}
            </DialogDescription>
          </DialogHeader>
          <div className="mt-4 flex justify-end gap-2">
            <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
            <Button variant="destructive" onClick={() => void doTrashRow()}>{t("fs.trash.confirm")}</Button>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={trashBatchOpen} onOpenChange={(v) => { if (!v) setTrashBatchOpen(false); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("fs.trashChecked.title")}</DialogTitle>
            <DialogDescription>
              {t("fs.trashChecked.body", { n: pickedPaths(selection).length })}
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
