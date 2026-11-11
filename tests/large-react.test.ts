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

/** 记下被整体遍历了几次的集合：用来确认逐项判断时没有反复扫整个集合。 */
class CountingSet<T> extends Set<T> {
  passes = 0;
  override [Symbol.iterator]() { this.passes++; return super[Symbol.iterator](); }
}

test("pickedPaths walks the selection a fixed number of times, not once per pick", () => {
  const sel = new CountingSet<string>();
  for (let i = 0; i < 3000; i++) sel.add(`/h/d${i % 30}/f${i}`);
  for (let d = 0; d < 10; d++) sel.add(`/h/d${d}`);
  const picked = pickedPaths({ ...emptySelection(), sel });
  expect(sel.passes).toBeLessThanOrEqual(2);
  // 勾了的文件夹带走它里面的文件；其余文件各自发送
  expect(picked).toHaveLength(10 + 3000 - 1000);
  expect(picked).toContain("/h/d0");
  expect(picked).not.toContain("/h/d0/f0");
  expect(picked).toContain("/h/d10/f10");
});

test("pickedPaths tells a folder from a sibling that shares its prefix", () => {
  const st = { ...emptySelection(), sel: new Set(["/a/b/c", "/a-b", "/a", "/ab", "/a/b", "/x/y"]), gone: new Set(["/x"]) };
  expect(pickedPaths(st).sort()).toEqual(["/a", "/a-b", "/ab"]);
});

test("pickedPaths on Windows ignores letter case and sends one path per location", async () => {
  const { setPlatformForTest } = await import("../src/platform");
  try {
    setPlatformForTest("win32");
    const st = { ...emptySelection(), sel: new Set(["C:\\Users\\a\\Docs", "c:\\users\\a\\docs\\x.txt", "C:\\Users\\a\\DOCS", "C:\\Users\\a\\Docs2"]) };
    expect(pickedPaths(st)).toEqual(["C:\\Users\\a\\Docs", "C:\\Users\\a\\Docs2"]);
    expect(pickedPaths({ ...st, gone: new Set(["c:\\USERS\\a\\docs\\"]) })).toEqual(["C:\\Users\\a\\Docs2"]);
    expect(isGone({ ...st, gone: new Set(["C:\\"]) }, "c:\\users")).toBe(true);
  } finally { setPlatformForTest(undefined); }
});

test("isGone checks a path against every removal without walking them all", () => {
  const gone = new CountingSet<string>();
  for (let i = 0; i < 1000; i++) gone.add(`/h/p/IMG_${i}.jpg`);
  gone.add("/h/old");
  const st = { ...emptySelection(), gone };
  const paths = Array.from({ length: 500 }, (_, i) => `/h/p/IMG_${i * 3}.jpg`);
  const left = paths.filter((p) => !isGone(st, p));
  expect(left).toHaveLength(500 - 334);
  expect(isGone(st, "/h/old/a/b")).toBe(true);
  expect(isGone(st, "/h/older")).toBe(false);
  expect(gone.passes).toBeLessThanOrEqual(1);
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

test("a large-file scan that fails while its stream is open shows the error and leaves no unhandled rejection", async () => {
  const { changes, fakeStream, textOf } = await import("./page-harness");
  const env = changes();
  try {
    const { LargePage } = await import("../src/react/large/LargePage");
    const { largeApi } = await import("../src/react/large/api");
    const stream = fakeStream<never>();
    env.assign(largeApi, { startScan: () => stream });
    env.lang("zh");
    const h = env.page(LargePage, {});
    await h.settle();
    h.find((n) => n.type?.name === "EmptyGuide")!.props.onCta();
    await h.settle();
    stream.fail(new Error("disk denied"));
    await h.settle();
    stream.end();
    await h.settle();
    const alert = h.find((n) => n.props.role === "alert");
    expect(alert && textOf(alert)).toContain("disk denied");
  } finally { env.restore(); }
});

test("human and displayPath match the rest of the UI", () => {
  expect(human(0)).toBe("0 B");
  expect(human(1024)).toBe("1.0 KB");
  expect(human(1536)).toBe("1.5 KB");
  expect(human(1500 * 1024 ** 3)).toBe("1.5 TB");
  expect(displayPath("/Users/alice/foo/bar")).toBe("~/foo/bar");
  expect(displayPath("/var/tmp/x")).toBe("/var/tmp/x");
});
