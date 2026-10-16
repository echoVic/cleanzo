import * as React from "react";
import { ChevronRight, FolderSearch } from "lucide-react";
import type { DirItem } from "../../mygo";
import { Button } from "../../ui/components/button";
import { Checkbox } from "../../ui/components/checkbox";
import { cn } from "../../ui/cn";
import { useT, type DictKey } from "../../ui/i18n-react";
import { human, displayPath } from "../ai/format";
import {
  isGone, loadedKids, pickedPaths, rowIndeterminate, type ListSelection,
} from "./selection";

/** 一行顶层数据（文件或目录）。 */
export interface ListRowData {
  path: string;
  /** 已折叠 home 的展示名。 */
  name: string;
  size: number;
  /** 排名序号（1 起）；不显示则不传。 */
  rank?: string;
  /** 是否显示「展开」按钮（目录行）。 */
  expandable?: boolean;
  /** 可移入废纸篓；不可移动时显示「受保护」标签并禁用勾选 / 删除。 */
  trashable: boolean;
  /** 行内副内容（如占用条）。 */
  sub?: React.ReactNode;
}

interface RowCallbacks {
  onToggleRow: (path: string, on: boolean) => void;
  onToggleChild: (parent: string, child: string, on: boolean) => void;
  onExpand: (path: string) => void;
  onReveal: (path: string) => void;
  onTrashRow: (path: string, size: number) => void;
}

function RevealButton({ path, onReveal }: { path: string; onReveal: (path: string) => void }) {
  const t = useT();
  return (
    <button
      type="button"
      title={t("fs.reveal")}
      aria-label={t("fs.reveal")}
      className="flex h-7 w-7 items-center justify-center rounded-md text-muted transition-colors hover:bg-muted-background hover:text-foreground"
      onClick={() => onReveal(path)}
    >
      <FolderSearch className="h-[15px] w-[15px]" />
    </button>
  );
}

function ChildRow({ parent, item, selection, busy, callbacks }: {
  parent: string;
  item: DirItem;
  selection: ListSelection;
  busy: boolean;
  callbacks: RowCallbacks;
}) {
  const t = useT();
  if (isGone(selection, item.path)) return null;
  return (
    <div className="fs-child">
      <Checkbox
        checked={selection.sel.has(item.path)}
        disabled={busy || !item.trashable}
        onCheckedChange={(v) => callbacks.onToggleChild(parent, item.path, v === true)}
        className="h-[15px] w-[15px]"
      />
      <span className="fs-child-name" title={item.path}>
        {item.name}
        {!item.trashable && <span className="tag dim"> {t("fs.locked")}</span>}
      </span>
      <span className="fs-child-size">{item.size >= 0 ? human(item.size) : "--"}</span>
      <RevealButton path={item.path} onReveal={callbacks.onReveal} />
      {item.trashable && (
        <Button
          variant="ghost"
          size="sm"
          disabled={busy}
          className="h-7 px-2 text-xs text-destructive"
          onClick={() => callbacks.onTrashRow(item.path, item.size)}
        >
          {busy ? <span className="spin" /> : null}
          {busy ? t("fs.moving") : t("fs.trash")}
        </Button>
      )}
    </div>
  );
}

interface RowProps extends RowCallbacks {
  row: ListRowData;
  selection: ListSelection;
  busy: boolean;
}

function ListRow({ row, selection, busy, ...cb }: RowProps) {
  const t = useT();
  if (isGone(selection, row.path)) return null;
  const open = selection.expanded.has(row.path);
  const checked = selection.sel.has(row.path);
  const indeterminate = rowIndeterminate(selection, row.path);
  const kids = loadedKids(selection, row.path);
  const childList = selection.children.get(row.path);

  return (
    <div className="fs-row-wrap">
      <div className={cn("fs-row", open && "open")}>
        <Checkbox
          checked={checked ? true : indeterminate ? "indeterminate" : false}
          disabled={busy || !row.trashable}
          onCheckedChange={(v) => cb.onToggleRow(row.path, v === true)}
        />
        {row.rank && <div className="fs-rank">{row.rank}</div>}
        <div className="fs-main">
          <div className="fs-path" title={row.path}>
            {row.name}
            {!row.trashable && <span className="tag dim"> {t("fs.locked")}</span>}
          </div>
          {row.sub && <div className="fs-sub">{row.sub}</div>}
        </div>
        <div className="fs-size">{row.size >= 0 ? human(row.size) : "--"}</div>
        {row.expandable && (
          <Button
            variant="ghost"
            size="sm"
            disabled={busy}
            className="fs-expand h-7 px-2 text-xs"
            onClick={() => cb.onExpand(row.path)}
          >
            {open ? t("fs.collapse") : t("fs.expand")}
            <ChevronRight className={cn("h-3 w-3 transition-transform", open && "rotate-90")} />
          </Button>
        )}
        <RevealButton path={row.path} onReveal={cb.onReveal} />
        {row.trashable && (
          <Button
            variant="ghost"
            size="sm"
            disabled={busy}
            className="h-7 px-2 text-xs text-destructive"
            onClick={() => cb.onTrashRow(row.path, row.size)}
          >
            {busy ? <span className="spin" /> : null}
            {busy ? t("fs.moving") : t("fs.trash")}
          </Button>
        )}
      </div>
      {open && (
        <div className="fs-children">
          {!childList && <span className="muted small">{t("fs.children")}</span>}
          {childList && "error" in childList && (
            <div className="fs-child err-inline">{t("fs.childrenFail" as DictKey, { msg: childList.error })}</div>
          )}
          {childList && "items" in childList && kids.length === 0 && (
            <div className="fs-child muted small">{t("fs.childrenEmpty")}</div>
          )}
          {childList && "items" in childList && kids.map((p) => {
            const it = childList.items.find((i) => i.path === p)!;
            return (
              <ChildRow key={p} parent={row.path} item={it} selection={selection} busy={busy} callbacks={cb} />
            );
          })}
          {childList && "items" in childList && childList.total > kids.length && (
            <div className="fs-child muted small">
              {t("fs.childrenMore", { n: kids.length, total: childList.total })}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/** 行列表容器：直接复用 fs-list 的视觉。 */
export function ListRows(props: Omit<RowProps, "row"> & { rows: ListRowData[] }) {
  const { rows, ...rest } = props;
  return (
    <div className="fs-list">
      {rows.map((row) => <ListRow key={row.path} row={row} {...rest} />)}
    </div>
  );
}

/** 三态全选工具栏。 */
export interface ListToolbarProps {
  /** 当前可见、可勾选的顶层行路径。 */
  selectableRows: string[];
  selection: ListSelection;
  busy: boolean;
  /** 返回某路径当前大小（未知子项返回 undefined）。 */
  sizeOf: (path: string) => number | undefined;
  onToggleAll: (on: boolean) => void;
  onTrashChecked: () => void;
}

export function ListToolbar({ selectableRows, selection, busy, sizeOf, onToggleAll, onTrashChecked }: ListToolbarProps) {
  const t = useT();
  if (selectableRows.length === 0) return null;
  const picked = pickedPaths(selection);
  const size = picked.reduce((sum, p) => {
    const n = sizeOf(p);
    return sum + (n !== undefined && n >= 0 ? n : 0);
  }, 0);
  const some = selectableRows.some((p) => selection.sel.has(p));
  const all = selectableRows.length > 0 && selectableRows.every((p) => selection.sel.has(p));
  return (
    <div className="fs-toolbar">
      <Checkbox
        checked={all ? true : some ? "indeterminate" : false}
        disabled={busy || selectableRows.length === 0}
        onCheckedChange={(v) => onToggleAll(v === true)}
      />
      <span className="fs-toolbar-count">
        {picked.length > 0
          ? t("fs.checkedSize", { n: picked.length, size: human(size) })
          : t("fs.nothing")}
      </span>
      <Button
        variant="ghost"
        size="sm"
        disabled={busy || picked.length === 0}
        className="h-8 px-3 text-xs text-destructive"
        onClick={onTrashChecked}
      >
        {busy ? <span className="spin" /> : null}
        {busy ? t("fs.moving") : t("fs.trashChecked")}
      </Button>
    </div>
  );
}

export { displayPath };
