import { expect, test } from "bun:test";
import type { PhotoCluster, PhotoItem } from "../src/mygo";
import {
  effectiveRemoved, emptySelection, isGone, subtractRemoved, type ListSelection,
} from "../src/react/list/selection";
import {
  allChecked, defaultPickedPaths, liveSel, removeClusters, someChecked,
  visibleClusters, visibleItems, visibleSelectablePaths,
} from "../src/react/photos/helpers";

function item(path: string, size: number, keep = false): PhotoItem {
  return { path, size, width: 100, height: 100, thumb: "data:,", keep };
}

function cluster(id: number, kind: string, items: PhotoItem[]): PhotoCluster {
  const totalSize = items.reduce((s, it) => s + (it.size >= 0 ? it.size : 0), 0);
  const keepIt = items.find((it) => it.keep);
  const waste = keepIt ? Math.max(0, totalSize - keepIt.size) : 0;
  return { id, kind, items, totalSize, waste };
}

test("defaultPickedPaths selects only non-keep items inside exact clusters", () => {
  const clusters = [
    cluster(1, "exact", [item("/a/1.jpg", 100, true), item("/a/2.jpg", 100)]),
    cluster(2, "similar", [item("/b/1.jpg", 200, true), item("/b/2.jpg", 200)]),
  ];
  expect(defaultPickedPaths(clusters).sort()).toEqual(["/a/2.jpg"]);
});

test("visibleSelectablePaths excludes keep items and gone paths", () => {
  const clusters = [
    cluster(1, "exact", [item("/a/1.jpg", 100, true), item("/a/2.jpg", 100), item("/a/3.jpg", 100)]),
  ];
  let st: ListSelection = emptySelection();
  expect(visibleSelectablePaths(clusters, st).sort()).toEqual(["/a/2.jpg", "/a/3.jpg"].sort());
  st = subtractRemoved(st, ["/a/2.jpg"]);
  expect(visibleSelectablePaths(clusters, st)).toEqual(["/a/3.jpg"]);
  expect(isGone(st, "/a/2.jpg")).toBe(true);
});

test("visibleClusters drops clusters with fewer than 2 visible items", () => {
  const clusters = [
    cluster(1, "exact", [item("/a/1.jpg", 100, true), item("/a/2.jpg", 100)]),
    cluster(2, "similar", [item("/b/1.jpg", 200, true), item("/b/2.jpg", 200)]),
  ];
  let st: ListSelection = subtractRemoved(emptySelection(), ["/a/2.jpg"]);
  expect(visibleClusters(clusters, st).map((c) => c.id)).toEqual([2]);
  expect(visibleItems(clusters[0]!, st).map((i) => i.path)).toEqual(["/a/1.jpg"]);
});

test("allChecked / someChecked give the tri-state of the toolbar checkbox", () => {
  const visible = ["/a/2.jpg", "/a/3.jpg"];
  let sel = new Set<string>();
  expect(allChecked(visible, sel)).toBe(false);
  expect(someChecked(visible, sel)).toBe(false);
  sel = new Set(["/a/2.jpg"]);
  expect(allChecked(visible, sel)).toBe(false);
  expect(someChecked(visible, sel)).toBe(true);
  sel = new Set(["/a/2.jpg", "/a/3.jpg"]);
  expect(allChecked(visible, sel)).toBe(true);
  expect(someChecked(visible, sel)).toBe(true);
});

test("liveSel drops checked paths that were moved to Trash", () => {
  let st: ListSelection = { ...emptySelection(), sel: new Set(["/a/2.jpg", "/a/3.jpg"]) };
  st = subtractRemoved(st, ["/a/2.jpg"]);
  expect(liveSel(st)).toEqual(["/a/3.jpg"]);
});

test("removeClusters drops clusters under 2 items and rewrites keep/totalSize/waste", () => {
  const clusters = [
    cluster(1, "exact", [item("/a/1.jpg", 100, true), item("/a/2.jpg", 100), item("/a/3.jpg", 100)]),
    cluster(2, "exact", [item("/b/1.jpg", 50, true), item("/b/2.jpg", 50)]),
  ];
  const next = removeClusters(clusters, ["/a/2.jpg"]);
  // 簇 1 剩 keep + /a/3：>= 2 保留；keep 仍是 /a/1；waste = 200 - 100。簇 2 未受影响。
  expect(next.map((c) => c.id)).toEqual([1, 2]);
  const c1 = next[0]!;
  expect(c1.items.map((i) => i.path)).toEqual(["/a/1.jpg", "/a/3.jpg"]);
  expect(c1.items.map((i) => i.keep)).toEqual([true, false]);
  expect(c1.totalSize).toBe(200);
  expect(c1.waste).toBe(100);
  // 簇 2 只有 2 项，删掉一个后不足 2 项，整簇消失；簇 1 保留。
  const dropped = removeClusters(clusters, ["/b/2.jpg"]);
  expect(dropped.map((c) => c.id)).toEqual([1]);
});

test("removeClusters picks the largest remaining item as keep when the old keep is gone", () => {
  const clusters = [
    cluster(1, "exact", [item("/a/1.jpg", 100, true), item("/a/2.jpg", 300), item("/a/3.jpg", 200)]),
  ];
  const next = removeClusters(clusters, ["/a/1.jpg"]);
  const c1 = next[0]!;
  expect(c1.items.map((i) => i.path)).toEqual(["/a/2.jpg", "/a/3.jpg"]);
  expect(c1.items.find((i) => i.keep)?.path).toBe("/a/2.jpg");
  expect(c1.totalSize).toBe(500);
  expect(c1.waste).toBe(200);
});

test("effectiveRemoved collapses nested removals", () => {
  expect(effectiveRemoved(["/a/b", "/a/b/c", "/a/d"]).sort()).toEqual(["/a/b", "/a/d"].sort());
});
