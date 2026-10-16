import * as React from "react";
import type { OrphanItem } from "../../mygo";
import { Button } from "../../ui/components/button";
import { IdleHero } from "../../ui/components/idle-hero";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogClose,
} from "../../ui/components/dialog";
import { useT } from "../../ui/i18n-react";
import { kindLabel } from "../../i18n";
import { errText, toast } from "../../shared";
import { human } from "../ai/format";
import {
  applyRemoval, emptySelection, isGone, pickedPaths, toggleChild, toggleRow, type ListSelection,
} from "../list/selection";
import { ListRows, ListToolbar, type ListRowData } from "../list/ListRows";
import { appsApi } from "./api";
import { barWidth, orphanFootAfterRemoval, parseDoneEv, sortOrphans } from "./helpers";

interface LeftoversTabProps {
  /** 该 tab 是否当前选中：首次激活时才懒启动扫描。 */
  active: boolean;
  /** 向页脚汇报残留数量（tab 角标实时更新）。 */
  onCountChange: (n: number) => void;
  /** 向页脚汇报繁忙态（导航 busy）。 */
  onBusyChange: (busy: boolean) => void;
}

/** 展开的子项里某个路径的大小（工具栏勾选合计用）。 */
function childSize(selection: ListSelection, path: string): number | undefined {
  for (const c of selection.children.values()) {
    if (!("items" in c)) continue;
    const it = c.items.find((i) => i.path === path);
    if (it) return it.size;
  }
  return undefined;
}

export function LeftoversTab({ active, onCountChange, onBusyChange }: LeftoversTabProps) {
  const t = useT();

  const [items, setItems] = React.useState<OrphanItem[]>([]);
  const [scanning, setScanning] = React.useState(false);
  const [stopped, setStopped] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  /** 是否开始过扫描：只有首次进入自动开始，之后再进不重扫。 */
  const [tried, setTried] = React.useState(false);
  const [doneMs, setDoneMs] = React.useState<number | null>(null);
  const [selection, setSelection] = React.useState<ListSelection>(emptySelection());
  const [busy, setBusy] = React.useState(false);
  const [trashTarget, setTrashTarget] = React.useState<{ path: string; size: number } | null>(null);
  const [trashBatchOpen, setTrashBatchOpen] = React.useState(false);

  // 异步循环里读最新值，避免闭包捕获旧 state。
  const runRef = React.useRef(0);
  const chanRef = React.useRef<{ close(): void } | null>(null);
  const stoppedRef = React.useRef(false);
  const scanningRef = React.useRef(false);
  const autoStartedRef = React.useRef(false);
  const itemsRef = React.useRef(items);
  itemsRef.current = items;
  scanningRef.current = scanning;

  // 离开页面（理论上 root 复用不会触发）：让旧流式循环停止消费并关闭 Channel。
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
    setTried(true);
    setItems([]);
    setDoneMs(null);
    setError(null);
    setStopped(false);
    setSelection(emptySelection());
    setScanning(true);
    const { ch, done } = appsApi.startOrphanScan();
    chanRef.current = ch;
    try {
      for await (const o of ch) {
        if (runRef.current !== myRun) continue; // 被新扫描取代：丢弃旧事件
        if (o.kind === "__done") {
          setDoneMs(parseDoneEv(o).elapsedMs);
          continue;
        }
        setItems((prev) => [...prev, o]);
      }
      await done;
    } catch (e) {
      if (runRef.current !== myRun) return;
      if (!stoppedRef.current) {
        setError(e instanceof Error ? e.message : String(e));
        setItems([]);
        setDoneMs(null);
        toast(t("orphans.fail", { msg: errText(e) }), "err");
      }
    } finally {
      if (runRef.current !== myRun) return;
      chanRef.current = null;
      setScanning(false);
      setStopped(stoppedRef.current);
      stoppedRef.current = false;
    }
  }, [t]);

  // 首次进入「残留」自动扫描一次（root 复用；之后再进不重扫）。
  React.useEffect(() => {
    if (active && !autoStartedRef.current) {
      autoStartedRef.current = true;
      void startScan();
    }
  }, [active, startScan]);

  const stopScanNow = React.useCallback(() => {
    if (!scanningRef.current) return;
    stoppedRef.current = true;
    chanRef.current?.close();
  }, []);

  // tab 角标：残留数量实时更新。
  React.useEffect(() => { onCountChange(items.length); }, [items.length, onCountChange]);
  // 导航 busy：扫描或删除中。
  React.useEffect(() => { onBusyChange(scanning || busy); }, [scanning, busy, onBusyChange]);

  // ---- 派生视图 ----
  const visible = sortOrphans(items.filter((o) => !isGone(selection, o.path)));
  const maxSize = visible[0]?.size || 1;
  const rows: ListRowData[] = visible.map((o, i) => ({
    path: o.path,
    name: (
      <>
        {o.path.replace(/^\/Users\/[^/]+/, "~/")}
        <span className="tag dim"> {kindLabel(o.kind)}</span>
      </>
    ),
    size: o.size,
    rank: `${i + 1}`,
    expandable: o.isDir,
    trashable: o.trashable,
    sub: (
      <div className="fs-track">
        <div className="fs-fill" style={{ width: `${barWidth(o.size, maxSize)}%` }} />
      </div>
    ),
  }));
  const selectableRows = rows.filter((r) => r.trashable).map((r) => r.path);
  const sizeOf = (path: string) =>
    items.find((it) => it.path === path)?.size ?? childSize(selection, path);

  // ---- 勾选 / 展开 / 动作 ----
  const onToggleRow = (path: string, on: boolean) => setSelection((prev) => toggleRow(prev, path, on));
  const onToggleChild = (parent: string, child: string, on: boolean) =>
    setSelection((prev) => toggleChild(prev, parent, child, on));

  const onExpand = async (path: string) => {
    if (busy || isGone(selection, path)) return;
    const collapsing = selection.expanded.has(path);
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
    try {
      const r = await appsApi.dirChildren(path);
      setSelection((prev) => {
        if (isGone(prev, path)) return prev;
        const children = new Map(prev.children);
        children.set(path, { items: r.items, total: r.total });
        // 整行已勾时：新展开的子项一并勾上。
        let next: ListSelection = { ...prev, children };
        if (prev.sel.has(path)) next = toggleRow(next, path, true);
        return next;
      });
    } catch (e) {
      setSelection((prev) => {
        if (isGone(prev, path)) return prev;
        const children = new Map(prev.children);
        children.set(path, { error: errText(e) });
        return { ...prev, children };
      });
    }
  };

  const onReveal = (path: string) => {
    appsApi.reveal(path).catch((e) => toast(t("fs.revealFail", { msg: errText(e) }), "err"));
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
      const res = await appsApi.trashPath(target.path);
      toast(t("fs.trash.ok", { path: target.path.replace(/^\/Users\/[^/]+/, "~/"), size: human(res.freed) }));
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
      const r = await appsApi.cleanPaths(paths);
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
    : (error || stopped || tried)
      ? t("fs.rescan")
      : t("orphans.scanBtn");

  const showIdle = !scanning && !error && !tried;
  const showEmptyScanned = !scanning && !error && !stopped && visible.length === 0 && tried;
  const showStoppedEmpty = !scanning && stopped && visible.length === 0;
  const summary = doneMs !== null
    ? orphanFootAfterRemoval(visible, doneMs / 1000)
    : null;

  return (
    <div>
      <div className="leftovers-head">
        <p className="muted">{t("orphans.sub")}</p>
        <Button onClick={() => (scanning ? stopScanNow() : void startScan())} disabled={busy && !scanning}>
          {scanning && <span className="spin" />}
          {scanButtonLabel}
        </Button>
      </div>

      {scanning && (
        <div className="fs-progress" style={{ display: "flex" }}>
          <div className="spinner" />
          <div className="fs-progress-text">
            <div className="muted small">
              {items.length > 0
                ? t("orphans.count", { n: items.length.toLocaleString() })
                : t("orphans.live")}
            </div>
          </div>
        </div>
      )}

      {summary && !scanning && (
        <div className="orphan-summary">
          <div className="orphan-total">
            {t("orphans.foot", { n: summary.n.toLocaleString(), size: human(summary.size), t: summary.t.toFixed(1) })}
          </div>
          <div className="muted small">{t("orphans.safeNote")}</div>
        </div>
      )}

      {showIdle && (
        <IdleHero
          label={t("hero.ready")}
          value="--"
          sub={t("clean.idleSub")}
          guide={t("orphans.idle.title")}
          note={t("orphans.idle.desc")}
          ctaText={t("orphans.idle.cta")}
          onCta={() => void startScan()}
        />
      )}

      {!scanning && error && (
        <div className="empty err">
          <div className="err-icon">⚠</div>
          <div>{t("orphans.fail", { msg: errText(error) })}</div>
          <button className="btn ghost retry-btn" type="button" onClick={() => void startScan()}>
            {t("btn.retry")}
          </button>
        </div>
      )}

      {showEmptyScanned && <div className="empty">{t("orphans.empty")}</div>}
      {showStoppedEmpty && <div className="empty">{t("scan.stopped")}</div>}

      {(visible.length > 0 || scanning) && (
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
            onExpand={(p) => void onExpand(p)}
            onReveal={onReveal}
            onTrashRow={onTrashRow}
          />
        </>
      )}

      <Dialog open={trashTarget !== null} onOpenChange={(v) => { if (!v) setTrashTarget(null); }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("fs.trash.title")}</DialogTitle>
            <DialogDescription>
              {t("fs.trash.body", {
                path: trashTarget ? trashTarget.path.replace(/^\/Users\/[^/]+/, "~/") : "",
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
