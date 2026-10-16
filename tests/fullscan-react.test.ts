import { expect, test } from "bun:test";
import type { BigItem } from "../src/mygo";
import {
  applyRemoval, emptySelection, isGone, pickedPaths,
  type ListSelection,
} from "../src/react/list/selection";
import {
  barWidth, diskPercent, displayPath, human, FULLSCAN_TOP_N, topBySize,
} from "../src/react/fullscan/format";

function dir(path: string, size: number, trashable = true): BigItem {
  return { path, size, trashable };
}

test("topBySize sorts desc and caps at 30", () => {
  const items = Array.from({ length: 50 }, (_, i) => dir(`/d${i}`, i));
  const top = topBySize(items);
  expect(top).toHaveLength(30);
  // 降序：最大的在前。
  expect(top[0]!.size).toBe(49);
  expect(top[1]!.size).toBe(48);
  expect(top[top.length - 1]!.size).toBe(20);
  expect(FULLSCAN_TOP_N).toBe(30);
});

test("topBySize honors a custom n and short arrays", () => {
  expect(topBySize([dir("/a", 100), dir("/b", 300)], 5).map((i) => i.path)).toEqual(["/b", "/a"]);
  expect(topBySize([dir("/a", 1)], 30)).toHaveLength(1);
});

test("barWidth is at least 2% of the largest item", () => {
  expect(barWidth(1000, 1000)).toBe(100);
  expect(barWidth(10, 1000)).toBe(2); // 最小 2%
  expect(barWidth(500, 1000)).toBe(50);
  // 最大项为 0 时不除零，落到最小宽。
  expect(barWidth(0, 0)).toBe(2);
});

test("diskPercent rounds and clamps to 0..100", () => {
  expect(diskPercent({ total: 1000, used: 500, free: 500 })).toBe(50);
  expect(diskPercent({ total: 1000, used: 999, free: 1 })).toBe(100);
  // 总量未知返回 0（保留 --）。
  expect(diskPercent({ total: 0, used: 0, free: 0 })).toBe(0);
});

test("gone filter hides rows removed to Trash and collapses selection", () => {
  const items = [dir("/big", 1000), dir("/keep", 500)];
  let st: ListSelection = emptySelection();
  // 勾选两行后删掉 /big：它从列表消失，/keep 保留。
  st = { ...st, sel: new Set(["/big", "/keep"]) };
  const upd = applyRemoval(items, st, ["/big"]);
  expect(upd.items.map((i) => i.path)).toEqual(["/keep"]);
  expect(isGone(upd.selection, "/big")).toBe(true);
  expect(pickedPaths(upd.selection)).toEqual(["/keep"]);
});

test("human and displayPath match the rest of the UI", () => {
  expect(human(0)).toBe("0 B");
  expect(human(1024)).toBe("1 KB");
  expect(human(1500 * 1024 ** 3)).toBe("1.5 TB");
  expect(displayPath("/Users/alice/foo/bar")).toBe("~/foo/bar");
  expect(displayPath("/var/tmp/x")).toBe("/var/tmp/x");
});
