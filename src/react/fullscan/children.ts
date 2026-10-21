import type { DirListing } from "../../mygo";
import { errText } from "../../shared";
import { isGone, toggleRow, type ListChildItem, type ListSelection } from "../list/selection";

type ChildrenApi = {
  dirChildren(path: string): Promise<DirListing>;
  dirSize(path: string): Promise<number>;
};
type UpdateSelection = (update: (prev: ListSelection) => ListSelection) => void;

/** Load the fast listing, then replace unknown folder sizes as measurements finish. */
export async function loadChildren(
  path: string, api: ChildrenApi, update: UpdateSelection, isCurrent: () => boolean,
): Promise<void> {
  const canApply = (st: ListSelection) => isCurrent() && st.expanded.has(path) && !isGone(st, path);
  const measured = (child: string, result: Pick<ListChildItem, "size" | "sizeError">) => {
    update((prev) => {
      if (!canApply(prev) || isGone(prev, child)) return prev;
      const listing = prev.children.get(path);
      if (!listing || !("items" in listing) || !listing.items.some((it) => it.path === child)) return prev;
      const items = listing.items.map((it) => it.path === child ? { ...it, ...result, measuring: false } : it);
      items.sort((a, b) => b.size - a.size);
      return { ...prev, children: new Map(prev.children).set(path, { ...listing, items }) };
    });
  };
  try {
    const listing = await api.dirChildren(path);
    if (!isCurrent()) return;
    const items = listing.items.map((it) => ({ ...it, measuring: it.isDir && it.size < 0 }));
    update((prev) => {
      if (!canApply(prev)) return prev;
      const next = { ...prev, children: new Map(prev.children).set(path, { ...listing, items }) };
      return prev.sel.has(path) ? toggleRow(next, path, true) : next;
    });
    for (const item of items) {
      if (!item.measuring) continue;
      void api.dirSize(item.path).then(
        (size) => measured(item.path, { size }),
        (error: unknown) => measured(item.path, { size: -1, sizeError: errText(error) }),
      );
    }
  } catch (error) {
    update((prev) => {
      if (!canApply(prev)) return prev;
      return { ...prev, children: new Map(prev.children).set(path, { error: errText(error) }) };
    });
  }
}
