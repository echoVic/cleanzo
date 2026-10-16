// 相似照片页纯逻辑：不依赖 DOM / React，可单测。
import type { PhotoCluster, PhotoItem } from "../../mygo";
import { effectiveRemoved, isGone, under, type ListSelection } from "../list/selection";

/**
 * 扫描完成后的默认勾选：仅「完全相同」簇里非建议保留的副本；
 * 「近似」簇由用户自己挑。等价旧 startPhotoScan 里的 sel 初始化。
 */
export function defaultPickedPaths(clusters: readonly PhotoCluster[]): string[] {
  const out: string[] = [];
  for (const cl of clusters) {
    if (cl.kind !== "exact") continue;
    for (const it of cl.items) if (!it.keep) out.push(it.path);
  }
  return out;
}

/** 某簇里还没被删除的项。 */
export function visibleItems(cl: PhotoCluster, st: ListSelection): PhotoItem[] {
  return cl.items.filter((it) => !isGone(st, it.path));
}

/** 簇收敛后仍可见（未删除项 >= 2）的簇。 */
export function visibleClusters(clusters: readonly PhotoCluster[], st: ListSelection): PhotoCluster[] {
  return clusters.filter((cl) => visibleItems(cl, st).length >= 2);
}

/** 全选可用路径：每簇 !keep 且未删除。等价旧 visiblePhotoPaths。 */
export function visibleSelectablePaths(
  clusters: readonly PhotoCluster[],
  st: ListSelection,
): string[] {
  const out: string[] = [];
  for (const cl of clusters) {
    for (const it of visibleItems(cl, st)) if (!it.keep) out.push(it.path);
  }
  return out;
}

/** 全选判定：visible 全部勾上。 */
export function allChecked(visible: readonly string[], sel: ReadonlySet<string>): boolean {
  return visible.length > 0 && visible.every((p) => sel.has(p));
}

/** 半选判定：visible 里有任意勾上。 */
export function someChecked(visible: readonly string[], sel: ReadonlySet<string>): boolean {
  return visible.some((p) => sel.has(p));
}

/** 勾选里仍有效（未删除）的路径。 */
export function liveSel(st: ListSelection): string[] {
  return [...st.sel].filter((p) => !isGone(st, p));
}

/**
 * 删除后簇收敛：被移走的项从簇里删掉；剩余不足 2 项的簇消失；
 * 找不到原 keep 项则取最大 size 的为 keep；totalSize / waste 重算。
 * 等价旧 removePhotos（改为不可变）。
 */
export function removeClusters(
  clusters: readonly PhotoCluster[],
  removed: string[],
): PhotoCluster[] {
  const roots = effectiveRemoved(removed);
  if (roots.length === 0) return [...clusters];
  const out: PhotoCluster[] = [];
  for (const cl of clusters) {
    const items = cl.items.filter((it) => !roots.some((r) => under(it.path, r)));
    if (items.length < 2) continue;
    const keep =
      items.find((it) => it.keep) ??
      items.reduce((best, it) => (it.size > best.size ? it : best), items[0]!);
    const kept = items.map((it) => ({ ...it, keep: it.path === keep.path }));
    const totalSize = kept.reduce((sum, it) => sum + (it.size >= 0 ? it.size : 0), 0);
    const waste = Math.max(0, totalSize - (keep.size >= 0 ? keep.size : 0));
    out.push({ ...cl, items: kept, totalSize, waste });
  }
  return out;
}
