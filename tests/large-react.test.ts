import { expect, test } from "bun:test";
import type { DirItem, LargeFile } from "../src/mygo";
import {
  applyRemoval, effectiveRemoved, emptySelection, isGone, loadedKids,
  pickedPaths, rowIndeterminate, toggleChild, toggleRow, under,
  type ListSelection,
} from "../src/react/list/selection";
import {
  barWidth, displayPath, human, LARGE_THRESHOLDS, topBySize, visibleAfterThreshold,
} from "../src/react/large/format";

function file(path: string, size: number, trashable = true): LargeFile {
  return { path, size, trashable };
}

function kid(path: string, size: number, trashable = true): DirItem {
  return { name: path.split("/").pop() ?? path, path, size, isDir: false, trashable };
}

/** 模拟展开一个文件夹后加载出子项。 */
function withKids(st: ListSelection, parent: string, kids: DirItem[]): ListSelection {
  return { ...st, children: new Map(st.children).set(parent, { items: kids, total: kids.length }) };
}

test("under matches path itself and descendants only", () => {
  expect(under("/Users/a/b", "/Users/a/b")).toBe(true);
  expect(under("/Users/a/b/c", "/Users/a/b")).toBe(true);
  expect(under("/Users/a/bc", "/Users/a/b")).toBe(false);
  expect(under("/Users/a", "/Users/a/b")).toBe(false);
});

test("effectiveRemoved drops children of removed roots", () => {
  expect(effectiveRemoved(["/a/b", "/a/b/c", "/a/d"]).sort()).toEqual(["/a/b", "/a/d"].sort());
  expect(effectiveRemoved(["/a/b/c", "/a/b"]).sort()).toEqual(["/a/b"]);
});

test("toggleRow selects the row and loaded kids, deselects everything under it", () => {
  let st = emptySelection();
  st = withKids(st, "/docs", [kid("/docs/a", 10), kid("/docs/b", 20)]);
  st = toggleRow(st, "/docs", true);
  expect([...st.sel].sort()).toEqual(["/docs", "/docs/a", "/docs/b"].sort());
  st = toggleRow(st, "/docs", false);
  expect(st.sel.size).toBe(0);
});

test("toggleRow does nothing for gone paths", () => {
  let st = emptySelection();
  st = { ...st, gone: new Set(["/x"]) };
  st = toggleRow(st, "/x", true);
  expect(st.sel.has("/x")).toBe(false);
});

test("toggleChild off unwinds a whole-row pick into the remaining loaded kids", () => {
  let st = emptySelection();
  st = withKids(st, "/docs", [kid("/docs/a", 10), kid("/docs/b", 20)]);
  st = toggleRow(st, "/docs", true);
  // 取消其中一个子项：整行不再选中，其余已加载子项保留。
  st = toggleChild(st, "/docs", "/docs/a", false);
  expect(st.sel.has("/docs")).toBe(false);
  expect(st.sel.has("/docs/b")).toBe(true);
  expect(st.sel.has("/docs/a")).toBe(false);
});

test("rowIndeterminate is true when some but not all loaded kids are picked", () => {
  let st = emptySelection();
  st = withKids(st, "/docs", [kid("/docs/a", 10), kid("/docs/b", 20)]);
  expect(rowIndeterminate(st, "/docs")).toBe(false);
  st = toggleRow(st, "/docs", true);
  expect(rowIndeterminate(st, "/docs")).toBe(false); // 整行勾
  st = toggleChild(st, "/docs", "/docs/a", false);
  expect(rowIndeterminate(st, "/docs")).toBe(true);
});

test("pickedPaths collapses a picked folder and drops gone entries", () => {
  let st = emptySelection();
  st = withKids(st, "/docs", [kid("/docs/a", 10)]);
  st = toggleRow(st, "/docs", true);
  st = toggleRow(st, "/movies", true);
  expect(pickedPaths(st).sort()).toEqual(["/docs", "/movies"].sort());
  // 删除 /docs 后：gone 内的都不再被 picked。
  const after = applyRemoval([file("/docs", 100), file("/movies", 50)], st, ["/docs"]);
  expect(pickedPaths(after.selection).sort()).toEqual(["/movies"].sort());
  expect(isGone(after.selection, "/docs/a")).toBe(true);
});

test("applyRemoval removes rows under roots and shrinks kept rows by known kids", () => {
  const items = [file("/big", 1000), file("/keep", 500)];
  let st = emptySelection();
  st = withKids(st, "/keep", [kid("/keep/sub", 200)]);
  const after = applyRemoval(items, st, ["/big"]);
  expect(after.items.map((i) => i.path)).toEqual(["/keep"]);
  expect(isGone(after.selection, "/big")).toBe(true);
});

test("loadedKids filters gone children", () => {
  let st = emptySelection();
  st = withKids(st, "/d", [kid("/d/a", 1), kid("/d/b", 2)]);
  st = { ...st, gone: new Set(["/d/a"]) };
  expect(loadedKids(st, "/d")).toEqual(["/d/b"]);
});

test("visibleAfterThreshold keeps only files at or above the minimum", () => {
  const items = [file("/a", 2 * 1073741824), file("/b", 500 * 1048576), file("/c", 5 * 1048576)];
  expect(visibleAfterThreshold(items, 1073741824).map((i) => i.path)).toEqual(["/a"]);
  expect(visibleAfterThreshold(items, 524288000).map((i) => i.path).sort()).toEqual(["/a", "/b"].sort());
});

test("topBySize sorts desc and caps at n", () => {
  const items = [file("/a", 100), file("/b", 300), file("/c", 200)];
  expect(topBySize(items, 50).map((i) => i.path)).toEqual(["/b", "/c", "/a"]);
  expect(topBySize(items, 2).map((i) => i.path)).toEqual(["/b", "/c"]);
});

test("barWidth is at least 2% of the largest item", () => {
  expect(barWidth(1000, 1000)).toBe(100);
  expect(barWidth(10, 1000)).toBe(2); // 最小 2%
  expect(barWidth(500, 1000)).toBe(50);
});

test("threshold buckets are the four persisted tiers", () => {
  expect([...LARGE_THRESHOLDS]).toEqual([104857600, 524288000, 1073741824, 5368709120]);
});

test("human and displayPath match the rest of the UI", () => {
  expect(human(0)).toBe("0 B");
  expect(human(1024)).toBe("1 KB");
  expect(human(1500 * 1024 ** 3)).toBe("1.5 TB");
  expect(displayPath("/Users/alice/foo/bar")).toBe("~/foo/bar");
  expect(displayPath("/var/tmp/x")).toBe("/var/tmp/x");
});
