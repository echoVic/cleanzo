import * as React from "react";
import type { BigItem, DiskStat, FullScanResult } from "../../mygo";
import { Button } from "../../ui/components/button";
import { EmptyGuide } from "../../ui/components/empty-guide";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogClose,
} from "../../ui/components/dialog";
import { useT } from "../../ui/i18n-react";
import { errText, toast } from "../../shared";
import {
  applyRemoval, emptySelection, isGone, pickedPaths, toggleChild, toggleRow, type ListSelection,
} from "../list/selection";
import { ListRows, ListToolbar, PathTypeHint, type ListRowData } from "../list/ListRows";
import { DiskCard } from "./DiskCard";
import { fullscanApi } from "./api";
import { loadChildren } from "./children";
import { barWidth, displayPath, human, topBySize } from "./format";

/** 展开的子项里某个路径的大小（工具栏勾选合计用）。 */
function childSize(selection: ListSelection, path: string): number | undefined {
  for (const c of selection.children.values()) {
    if (!("items" in c)) continue;
    const it = c.items.find((i) => i.path === path);
    if (it) return it.size;
  }
  return undefined;
}

export function FullScanPage() {
  const t = useT();

  const [items, setItems] = React.useState<BigItem[]>([]);
  const [result, setResult] = React.useState<FullScanResult | null>(null);
  const [disk, setDisk] = React.useState<DiskStat | null>(null);
  const [scanning, setScanning] = React.useState(false);
  const [stopped, setStopped] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [tried, setTried] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [currentPath, setCurrentPath] = React.useState("");
  const [selection, setSelection] = React.useState<ListSelection>(emptySelection());
  const [trashTarget, setTrashTarget] = React.useState<{ path: string; size: number; isDir: boolean } | null>(null);
  const [trashBatchOpen, setTrashBatchOpen] = React.useState(false);
  const [actionErrors, setActionErrors] = React.useState<string[]>([]);

  // 异步循环里读最新值，避免闭包捕获旧 state。
  const runRef = React.useRef(0);
  const chanRef = React.useRef<{ close(): void } | null>(null);
  const stoppedRef = React.useRef(false);
  const scanningRef = React.useRef(false);
  const diskSeqRef = React.useRef(0);
  const expansionsRef = React.useRef(new Map<string, symbol>());
  const itemsRef = React.useRef(items);
  itemsRef.current = items;
  scanningRef.current = scanning;

  // 离开页面（root 复用下通常不触发）：让旧流式循环停止消费并关闭 Channel，避免未处理 rejection。
  React.useEffect(() => {
    return () => {
      runRef.current++;
      expansionsRef.current.clear();
      chanRef.current?.close();
      chanRef.current = null;
    };
  }, []);

  /** 磁盘卡懒加载：失败静默保留 --；序号防止旧读取覆盖更新的磁盘数据。 */
  const refreshDisk = React.useCallback(async () => {
    const seq = ++diskSeqRef.current;
    try {
      const d = await fullscanApi.disk();
      if (seq !== diskSeqRef.current) return; // 有更新的读取 / 扫描结果，丢弃
      setDisk(d);
    } catch {
      // 读取失败时保留上次容量。
    }
  }, []);

  // 首次进入即懒加载磁盘容量（root 复用后不再重挂）。
  React.useEffect(() => { void refreshDisk(); }, [refreshDisk]);

  const startScan = React.useCallback(async () => {
    const myRun = ++runRef.current;
    expansionsRef.current.clear();
    stoppedRef.current = false;
    setTried(true);
    setItems([]);
    setResult(null);
    setError(null);
    setActionErrors([]);
    setStopped(false);
    setCurrentPath("");
    setSelection(emptySelection());
    setScanning(true);
    const { ch, done } = fullscanApi.startScan();
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
          diskSeqRef.current++; // 不让先前的磁盘读取覆盖扫描结果
          setResult(ev.result);
          setItems(ev.result.items);
          setDisk(ev.result.disk);
        }
      }
      await done;
    } catch (e) {
      if (runRef.current !== myRun) return;
      if (!stoppedRef.current) {
        setError(e instanceof Error ? e.message : String(e));
        setItems([]);
        setResult(null);
        toast(t("fs.fail", { msg: errText(e) }), "err");
      }
    } finally {
      if (runRef.current !== myRun) return;
      chanRef.current = null;
      setScanning(false);
      setStopped(stoppedRef.current);
      stoppedRef.current = false;
    }
  }, [t]);

  const stopScanNow = React.useCallback(() => {
    if (!scanningRef.current) return;
    stoppedRef.current = true;
    chanRef.current?.close();
  }, []);

  // ---- 派生视图 ----
  const visible = items.filter((it) => !isGone(selection, it.path));
  const sorted = topBySize(visible);
  const maxSize = sorted[0]?.size || 1;
  const rows: ListRowData[] = sorted.map((it, i) => ({
    path: it.path,
    name: displayPath(it.path),
    size: it.size,
    rank: `${i + 1}`,
    expandable: true,
    trashable: it.trashable,
    sub: (
      <div className="fs-track">
        <div className="fs-fill" style={{ width: `${barWidth(it.size, maxSize)}%` }} />
      </div>
    ),
  }));
  const selectableRows = rows.filter((r) => r.trashable && !isGone(selection, r.path)).map((r) => r.path);
  const sizeOf = (path: string) =>
    items.find((it) => it.path === path)?.size ?? childSize(selection, path);

  // ---- 勾选 / 展开 / 动作 ----
  const onToggleRow = (path: string, on: boolean) => setSelection((prev) => toggleRow(prev, path, on));
  const onToggleChild = (parent: string, child: string, on: boolean) =>
    setSelection((prev) => toggleChild(prev, parent, child, on));

  const onExpand = async (path: string) => {
    if (busy || isGone(selection, path)) return;
    const collapsing = selection.expanded.has(path);
    const request = Symbol(path);
    if (collapsing) expansionsRef.current.delete(path);
    else expansionsRef.current.set(path, request);
    setSelection((prev) => {
      const expanded = new Set(prev.expanded);
      const children = new Map(prev.children);
      if (collapsing) {
        expanded.delete(path);
        children.delete(path);
      } else {
        expanded.add(path);
      }
      return { ...prev, expanded, children };
    });
    if (collapsing) return;
    await loadChildren(path, fullscanApi, setSelection, () => expansionsRef.current.get(path) === request);
  };

  const onReveal = (path: string) => {
    fullscanApi.reveal(path).catch((e) => toast(t("fs.revealFail", { msg: errText(e) }), "err"));
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
    if (result) setResult((r) => (r ? { ...r, items: upd.items } : r));
    // 删除后磁盘容量变化，刷新磁盘卡（本页独有）。
    void refreshDisk();
  };

  const onTrashRow = (path: string, size: number, isDir = false) => {
    if (busy || isGone(selection, path)) return;
    setTrashTarget({ path, size, isDir });
  };

  const doTrashRow = async () => {
    const target = trashTarget;
    setTrashTarget(null);
    if (!target) return;
    setBusy(true);
    setActionErrors([]);
    try {
      const res = await fullscanApi.trashPath(target.path);
      toast(t("fs.trash.ok", { path: displayPath(target.path), size: human(res.freed) }));
      applyItemsRemoval([target.path]);
    } catch (e) {
      setActionErrors([`${displayPath(target.path)} — ${errText(e)}`]);
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
    setActionErrors([]);
    try {
      const r = await fullscanApi.cleanPaths(paths);
      setActionErrors(r.failed.map((failure) => {
        const split = failure.lastIndexOf(": ");
        return split < 0 ? errText(failure) : `${displayPath(failure.slice(0, split))} — ${errText(failure.slice(split + 2))}`;
      }));
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
      setActionErrors([errText(e)]);
      toast(t("fs.actionFail", { msg: errText(e) }), "err");
    } finally {
      setBusy(false);
    }
  };

  const scanButtonLabel = scanning
    ? t("scan.stop")
    : (error || stopped || result || tried)
      ? t("fs.rescan")
      : t("fs.scanBtn");

  const showIdle = !scanning && !error && !stopped && sorted.length === 0 && !tried;
  const showEmptyScanned = !scanning && !error && !stopped && sorted.length === 0 && tried;
  const showStoppedEmpty = !scanning && stopped && sorted.length === 0;

  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h2>{t("nav.fullscan")}</h2>
          <p className="muted">{t("fs.sub")}</p>
        </div>
        <div className="head-actions">
          <Button onClick={() => (scanning ? stopScanNow() : void startScan())} disabled={busy && !scanning}>
            {scanning && <span className="spin" />}
            {scanButtonLabel}
          </Button>
        </div>
      </header>

      <DiskCard disk={disk} />
      {actionErrors.length > 0 && <div role="alert" className="err-inline" style={{ padding: 12, overflowWrap: "anywhere" }}>
        <strong>{t("fs.actionErrors")}</strong>
        <ul>{actionErrors.map((message, i) => <li key={i}>{message}</li>)}</ul>
      </div>}

      {scanning && (
        <div className="fs-progress" style={{ display: "flex" }}>
          <div className="spinner" />
          <div className="fs-progress-text">
            <div className="fs-progress-current">{currentPath ? t("fs.scanning", { path: displayPath(currentPath) }) : t("fs.prep")}</div>
            <div className="muted small">{t("fs.count", { n: items.length.toLocaleString() })}</div>
          </div>
        </div>
      )}

      {showIdle && (
        <EmptyGuide
          gradId="eg-fs-grad"
          title={t("fs.idle.title")}
          desc={t("fs.idle.desc")}
          ctaText={t("fs.idle.cta")}
          onCta={() => void startScan()}
        >
          <circle cx="12" cy="12" r="6.8" opacity="0.25" />
          <path d="M12 5.2v6.8h6.8" />
          <path d="M12 12l-4.8 4.8" />
        </EmptyGuide>
      )}

      {showEmptyScanned && <div className="empty">{t("fs.empty")}</div>}
      {scanning && sorted.length === 0 && <div className="empty">{t("fs.live")}</div>}
      {showStoppedEmpty && <div className="empty">{t("scan.stopped")}</div>}

      {!scanning && error && (
        <div className="empty err">
          <div className="err-icon">⚠</div>
          <div>{t("fs.fail", { msg: errText(error) })}</div>
          <button className="btn ghost retry-btn" type="button" onClick={() => void startScan()}>
            {t("btn.retry")}
          </button>
        </div>
      )}

      {sorted.length > 0 && (
        <>
          <ListToolbar
            selectableRows={selectableRows}
            selection={selection}
            busy={busy}
            sizeOf={sizeOf}
            onToggleAll={onToggleAll}
            onTrashChecked={onTrashChecked}
          />
          <p className="muted small">{t("fs.typeHint")}</p>
          <ListRows
            typeHints
            rows={rows}
            selection={selection}
            busy={busy}
            onToggleRow={onToggleRow}
            onToggleChild={onToggleChild}
            onExpand={(p) => void onExpand(p)}
            onReveal={onReveal}
            onTrashRow={onTrashRow}
          />
          {scanning && <div className="fs-foot muted small">{t("fs.live")}</div>}
          {!scanning && stopped && sorted.length > 0 && (
            <div className="fs-foot muted small">{t("scan.stopped")}</div>
          )}
        </>
      )}

      <Dialog open={trashTarget !== null} onOpenChange={(v) => { if (!v) setTrashTarget(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("fs.trash.title")}</DialogTitle>
            <DialogDescription style={{ overflowWrap: "anywhere" }}>
              {t("fs.trash.body", {
                path: trashTarget ? displayPath(trashTarget.path) : "",
                size: trashTarget ? human(trashTarget.size) : "",
              })}
            </DialogDescription>
          </DialogHeader>
          {trashTarget && <PathTypeHint path={trashTarget.path} isDir={trashTarget.isDir} />}
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
