import { expect, test } from "bun:test";
import "./setup-dom";
import { setLang } from "./setup-dom";
import { errText } from "../src/shared";
import { t, setPref } from "../src/i18n";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ListRows, ListToolbar } from "../src/react/list/ListRows";
import { pathHintKind } from "../src/react/list/type-hint";
import type { BigItem } from "../src/mygo";
import {
  applyRemoval, emptySelection, isGone, knownSizes, pickedPaths, toggleChild,
  type ListSelection,
} from "../src/react/list/selection";
import {
  barWidth, diskPercent, displayPath, human, FULLSCAN_TOP_N, topBySize,
} from "../src/react/fullscan/format";

test("usage probe failure does not tell users an app is running", () => {
  for (const lang of ["zh", "en"]) {
    setLang(lang);
    expect(errText("usage-unknown")).toBe(t("fs.usageUnknown"));
    expect(errText("usage-unknown")).not.toBe(errText("in-use"));
    expect(errText("blocked")).toBe(t("fs.blocked"));
  }
  setLang("zh");
});

test("type hints distinguish data from caches and do not change cleanup eligibility", () => {
  const cases = [
    ["/Applications/App.app", true, "app"], ["/Downloads/Setup.DMG", false, "installer"],
    ["/work/cache", true, "cache"], ["/work/.cache", true, "cache"], ["/work/tmp", true, "temp"],
    ["/work/backup.zip", false, "archive"], ["/work/config.json", false, "config"],
    ["/work/data.sqlite", false, "database"], ["/work/cache.sqlite", false, "database"],
    ["/work/Photos.photoslibrary", true, "library"], ["/work/Machine.pvm", true, "vm"],
    ["/work/report.pdf", false, "document"], ["/work/hello.go", false, "source"],
    ["/work/photo.heic", false, "media"], ["/work/server.log", false, "log"],
    ["/work/unrecognized.bin", false, "unknown"], ["/work/projects", true, "folder"],
  ] as const;
  for (const [path, isDir, expected] of cases) {
    expect(pathHintKind(path, isDir)).toBe(expected);
    for (const lang of ["zh", "en"]) {
      setLang(lang);
      const key = `fs.type.${expected}` as Parameters<typeof t>[0];
      expect(t(key)).not.toBe(key);
    }
  }
  setLang("zh");
  expect(t("fs.type.cache")).toContain("可能含离线内容");
  expect(t("fs.type.database")).toContain("先备份");
});

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

test("Windows Space Analysis subtitle and body measure the user profile in zh/en", async () => {
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref } = await import("../src/i18n");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { FullScanPage } = await import("../src/react/fullscan/FullScanPage");
  const { DiskCard } = await import("../src/react/fullscan/DiskCard");
  try {
    setPlatformForTest("win32");
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      for (const key of ["fs.sub", "fs.hint", "fs.idle.desc"] as const) {
        expect(t(key)).toContain(lang === "zh" ? "用户目录" : "user profile");
        expect(t(key)).not.toMatch(/whole disk|main directories of your disk|整个磁盘|磁盘上的主要目录/);
      }
      const page = renderToStaticMarkup(createElement(FullScanPage));
      expect(page).toContain(t("fs.sub"));
      expect(page).toContain(t("fs.idle.desc"));
      expect(renderToStaticMarkup(createElement(DiskCard, { disk: { total: 0, used: 0, free: 0 } }))).toContain(t("fs.hint"));
    }
  } finally { setPlatformForTest(undefined); setPref("system"); }
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function expansionFixture() {
  let state: ListSelection = { ...emptySelection(), expanded: new Set(["/docs"]) };
  const update = (fn: (prev: ListSelection) => ListSelection) => { state = fn(state); };
  const children = [
    { name: "small", path: "/docs/small", size: 1024, isDir: false, trashable: true },
    { name: "slow", path: "/docs/slow", size: -1, isDir: true, trashable: true },
  ];
  const callbacks = {
    onToggleRow() {}, onToggleChild() {}, onExpand() {}, onReveal() {}, onTrashRow() {},
  };
  const markup = () => renderToStaticMarkup(createElement(ListRows, {
    rows: [{ path: "/docs", name: "docs", size: 10000, expandable: true, trashable: true }],
    selection: state, busy: false, ...callbacks,
  }));
  const toolbar = () => renderToStaticMarkup(createElement(ListToolbar, {
    selectableRows: ["/docs"], selection: state, busy: false,
    sizeOf: (path: string) => knownSizes(state).get(path),
    onToggleAll() {}, onTrashChecked() {},
  }));
  return { get state() { return state; }, update, children, markup, toolbar };
}

test("an unknown directory with a measurement in flight renders the measuring label in zh/en", () => {
  const fixture = expansionFixture();
  fixture.update((st) => ({ ...st, children: new Map([["/docs", {
    items: fixture.children.map((it) => ({ ...it, measuring: it.isDir })), total: 2,
  }]]) }));
  try {
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      expect(fixture.markup()).toContain(lang === "zh" ? "计算中…" : "Measuring…");
    }
  } finally { setPref("system"); }
});

test("expansion measures unknown children, re-sorts, and includes the late size in selection totals", async () => {
  setPref("zh");
  try {
    const { loadChildren } = await import("../src/react/fullscan/children");
    const fixture = expansionFixture();
    const measured = deferred<number>();
    const requested: string[] = [];
    await loadChildren("/docs", {
      dirChildren: async () => ({ items: fixture.children, total: 2 }),
      dirSize: (path: string) => { requested.push(path); return measured.promise; },
    }, fixture.update, () => true);
    expect(requested).toEqual(["/docs/slow"]);
    expect(fixture.markup()).toContain("计算中…");
    fixture.update((st) => toggleChild(st, "/docs", "/docs/slow", true));
    expect(fixture.toolbar()).toContain("已选 1 项 · 0 B");
    measured.resolve(8192);
    await measured.promise;
    const html = fixture.markup();
    expect(html).not.toContain("计算中…");
    expect(html).toContain("8 KB");
    expect(html.indexOf('title="/docs/slow"')).toBeLessThan(html.indexOf('title="/docs/small"'));
    expect(fixture.toolbar()).toContain("已选 1 项 · 8 KB");
  } finally { setPref("system"); }
});

test("failed measurements render -- with a localized tooltip and translated backend error", async () => {
  const { loadChildren } = await import("../src/react/fullscan/children");
  for (const lang of ["zh", "en"] as const) {
    setPref(lang);
    try {
      const fixture = expansionFixture();
      const measured = deferred<number>();
      await loadChildren("/docs", {
        dirChildren: async () => ({ items: fixture.children, total: 2 }),
        dirSize: () => measured.promise,
      }, fixture.update, () => true);
      measured.reject(new Error("blocked"));
      await measured.promise.catch(() => {});
      const html = fixture.markup();
      expect(html).not.toContain(lang === "zh" ? "计算中…" : "Measuring…");
      expect(html).toContain(lang === "zh" ? "无法统计大小" : "Size unavailable");
      expect(html).toContain(errText("blocked"));
      expect(html).toContain('>--</span>');
    } finally { setPref("system"); }
  }
});

test("collapsed, removed, and replaced expansions ignore late measurement results", async () => {
  const { loadChildren } = await import("../src/react/fullscan/children");
  for (const mode of ["collapsed", "removed", "child removed", "replaced"] as const) {
    for (const fails of [false, true]) {
      const fixture = expansionFixture();
      const measured = deferred<number>();
      let current = true;
      await loadChildren("/docs", {
        dirChildren: async () => ({ items: fixture.children, total: 2 }),
        dirSize: () => measured.promise,
      }, fixture.update, () => current);
      if (mode === "collapsed") fixture.update((st) => ({ ...st, expanded: new Set(), children: new Map() }));
      if (mode === "removed") fixture.update((st) => applyRemoval([], st, ["/docs"]).selection);
      if (mode === "child removed") fixture.update((st) => applyRemoval([], st, ["/docs/slow"]).selection);
      if (mode === "replaced") current = false;
      const before = fixture.state;
      if (fails) measured.reject(new Error("blocked")); else measured.resolve(8192);
      await measured.promise.catch(() => {});
      expect(fixture.state).toBe(before);
    }
  }
});

test("a collapsed expansion ignores a late initial child listing", async () => {
  const { loadChildren } = await import("../src/react/fullscan/children");
  const fixture = expansionFixture();
  const listing = deferred<{ items: typeof fixture.children; total: number }>();
  let measures = 0;
  const loaded = loadChildren("/docs", {
    dirChildren: () => listing.promise,
    dirSize: async () => { measures++; return 8192; },
  }, fixture.update, () => false);
  fixture.update((st) => ({ ...st, expanded: new Set(), children: new Map() }));
  const before = fixture.state;
  listing.resolve({ items: fixture.children, total: 2 });
  await loaded;
  expect(fixture.state).toBe(before);
  expect(measures).toBe(0);
});
