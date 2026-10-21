import { isWithin } from "../../platform";
// 纯选择逻辑：不依赖 DOM / React，等价于旧 main.ts 里 fullscan/large/orphans/photos
// 共用的 ListState/toggleRow/toggleChild/pickedPaths/subtractRemovedFromState。
// 改为不可变：每次切换返回新状态对象，便于 React useState 直接使用。
import type { DirItem } from "../../mygo";

export type ListChildItem = DirItem & { measuring?: boolean; sizeError?: string };

/** 展开后加载出来的子项：成功列出或失败。 */
export type ChildList = { items: ListChildItem[]; total: number } | { error: string };

/** 一个列表视图的勾选 / 展开 / 已删除状态。视图之间互不影响。 */
export interface ListSelection {
  sel: ReadonlySet<string>;
  expanded: ReadonlySet<string>;
  gone: ReadonlySet<string>;
  children: ReadonlyMap<string, ChildList>;
}

export function emptySelection(): ListSelection {
  return { sel: new Set(), expanded: new Set(), gone: new Set(), children: new Map() };
}

/** path 是否等于 ancestor 或在其内部。 */
export function under(path: string, ancestor: string): boolean {
  return isWithin(path, ancestor);
}

/** 删掉「父子都被选中」时重复的子路径，只留根。 */
export function effectiveRemoved(paths: string[]): string[] {
  return [...new Set(paths)].filter((p, _, all) => !all.some((other) => other !== p && under(p, other)));
}

/** path 是否已经在某次删除里被移走。 */
export function isGone(st: ListSelection, path: string): boolean {
  for (const g of st.gone) if (under(path, g)) return true;
  return false;
}

/** 展开文件夹里已经加载、且仍存在的子项路径。 */
export function loadedKids(st: ListSelection, path: string): string[] {
  const c = st.children.get(path);
  return c && "items" in c ? c.items.filter((i) => !isGone(st, i.path)).map((i) => i.path) : [];
}

/**
 * 勾选一整行：文件夹连同已加载的子项一起勾上或取消。
 * 取消时把它内部所有勾选项一并移除。
 */
export function toggleRow(st: ListSelection, path: string, on: boolean): ListSelection {
  if (isGone(st, path)) return st;
  const sel = new Set(st.sel);
  if (on) {
    sel.add(path);
    for (const p of loadedKids(st, path)) sel.add(p);
  } else {
    for (const p of [...sel]) if (under(p, path)) sel.delete(p);
  }
  return { ...st, sel };
}

/**
 * 勾选文件夹里的一项。取消其中一项时，文件夹不再整个移走，
 * 改为勾选其余已加载子项。
 */
export function toggleChild(st: ListSelection, parent: string, child: string, on: boolean): ListSelection {
  if (isGone(st, child)) return st;
  const sel = new Set(st.sel);
  if (on) {
    sel.add(child);
  } else {
    if (sel.delete(parent)) {
      for (const k of loadedKids(st, parent)) sel.add(k);
    }
    sel.delete(child);
  }
  return { ...st, sel };
}

/** 要移到废纸篓的路径：勾选的文件夹已经包含它里面的子项。 */
export function pickedPaths(st: ListSelection): string[] {
  return [...st.sel].filter(
    (p) => !isGone(st, p) && ![...st.sel].some((q) => q !== p && under(p, q)),
  );
}

/** 某行是否处于半选（未整行勾，但有已加载子项被勾）。等价旧 applyIndeterminate。 */
export function rowIndeterminate(st: ListSelection, path: string): boolean {
  if (st.sel.has(path)) return false;
  return loadedKids(st, path).some((k) => st.sel.has(k));
}

/** 已知子项里每个路径的大小（size<0 的未知项不计）。 */
export function knownSizes(
  st: ListSelection,
  extras: readonly { path: string; size: number }[] = [],
): Map<string, number> {
  const m = new Map<string, number>();
  for (const it of extras) if (it.size >= 0) m.set(it.path, it.size);
  for (const c of st.children.values()) {
    if ("items" in c) for (const it of c.items) if (it.size >= 0) m.set(it.path, it.size);
  }
  return m;
}

function pruneUnderGone(st: ListSelection): Pick<ListSelection, "sel" | "expanded"> {
  const sel = new Set([...st.sel].filter((p) => !isGone(st, p)));
  const expanded = new Set([...st.expanded].filter((p) => !isGone(st, p)));
  return { sel, expanded };
}

/**
 * 删除后收敛勾选状态：把移走的路径标记 gone，清掉在其内部的勾选 / 展开 /
 * 子项列表；父文件夹已知子项里被扣掉的大小按已知子项大小扣减。
 * 等价旧 subtractRemovedFromState。
 */
export function subtractRemoved(st: ListSelection, removed: string[]): ListSelection {
  const roots = effectiveRemoved(removed);
  if (roots.length === 0) return st;
  const known = knownSizes(st);
  const gone = new Set(st.gone);
  for (const r of roots) gone.add(r);
  const next: ListSelection = { ...st, gone };
  const pruned = pruneUnderGone(next);
  next.sel = pruned.sel;
  next.expanded = pruned.expanded;
  const children = new Map<string, ChildList>();
  for (const [parent, c] of next.children) {
    if (isGone(next, parent)) continue;
    if (!("items" in c)) { children.set(parent, c); continue; }
    const kept = c.items.filter((it) => !roots.some((r) => under(it.path, r)));
    const total = Math.max(kept.length, c.total - (c.items.length - kept.length));
    const adjusted = kept.map((it) => {
      if (it.size < 0) return it;
      const delta = roots
        .filter((r) => r !== it.path && under(r, it.path))
        .reduce((sum, r) => sum + (known.get(r) ?? 0), 0);
      return delta > 0 ? { ...it, size: Math.max(0, it.size - delta) } : it;
    });
    children.set(parent, { items: adjusted, total });
  }
  next.children = children;
  return next;
}

/**
 * 删除后更新顶层数据列表：移除落在 roots 内的行，保留的行按已知子项大小扣减。
 * 等价旧 updateRowsAfterRemoved。返回新数组（不可变）。
 */
export function applyRemovalToItems<T extends { path: string; size: number }>(
  items: readonly T[],
  st: ListSelection,
  removed: string[],
): T[] {
  const roots = effectiveRemoved(removed);
  if (roots.length === 0) return [...items];
  const known = knownSizes(st, items);
  const out: T[] = [];
  for (const it of items) {
    if (roots.some((r) => under(it.path, r))) continue;
    if (it.size >= 0) {
      const delta = roots
        .filter((r) => r !== it.path && under(r, it.path))
        .reduce((sum, r) => sum + (known.get(r) ?? 0), 0);
      out.push(delta > 0 ? { ...it, size: Math.max(0, it.size - delta) } : it);
    } else {
      out.push(it);
    }
  }
  return out;
}

/** 删除后同时收敛数据列表与勾选状态，返回新的 { items, selection }。 */
export function applyRemoval<T extends { path: string; size: number }>(
  items: readonly T[],
  st: ListSelection,
  removed: string[],
): { items: T[]; selection: ListSelection } {
  return { items: applyRemovalToItems(items, st, removed), selection: subtractRemoved(st, removed) };
}
