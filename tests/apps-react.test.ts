import { expect, test } from "bun:test";
import type { AppInfo, OrphanItem, UninstallResult } from "../src/mygo";
import {
  barWidth, bundleFailed, classifyUninstall, filterApps, orphanFootAfterRemoval,
  parseDoneEv, sortApps, sortOrphans,
} from "../src/react/apps/helpers";

function app(p: Partial<AppInfo> & { name: string; path: string }): AppInfo {
  return { bundleId: "", version: "", size: 0, lastOpened: 0, running: false, ...p };
}

function orphan(p: Partial<OrphanItem> & { path: string }): OrphanItem {
  return { size: 0, kind: "preferences", trashable: true, isDir: false, ...p };
}

function uninstall(res: Partial<UninstallResult>): UninstallResult {
  return { path: "/Applications/X.app", moved: 0, freed: 0, failed: [], ...res };
}

// ---- 已安装：搜索过滤 ----

test("filterApps matches name or path case-insensitively", () => {
  const apps = [
    app({ name: "Google Chrome", path: "/Applications/Google Chrome.app" }),
    app({ name: "Visual Studio Code", path: "/Applications/Visual Studio Code.app" }),
  ];
  expect(filterApps(apps, "chrome").map((a) => a.name)).toEqual(["Google Chrome"]);
  expect(filterApps(apps, "/applications/visual").map((a) => a.name)).toEqual(["Visual Studio Code"]);
});

test("filterApps with empty/blank query keeps everything", () => {
  const apps = [app({ name: "A", path: "/a" }), app({ name: "B", path: "/b" })];
  expect(filterApps(apps, "")).toHaveLength(2);
  expect(filterApps(apps, "   ")).toHaveLength(2);
  // 不修改原数组
  expect(filterApps(apps, "x")).toEqual([]);
});

// ---- 已安装：三种排序 ----

test("sortApps by name uses localeCompare, size/opened fall back to name", () => {
  const apps = [
    app({ name: "Zed", path: "/z", size: 100, lastOpened: 5 }),
    app({ name: "Alpha", path: "/a", size: 300, lastOpened: 1 }),
    app({ name: "Mike", path: "/m", size: 300, lastOpened: 9 }),
  ];
  expect(sortApps(apps, "name", "en").map((a) => a.name)).toEqual(["Alpha", "Mike", "Zed"]);
  // 大小降序；相同大小按名称
  expect(sortApps(apps, "size", "en").map((a) => a.name)).toEqual(["Alpha", "Mike", "Zed"]);
  // 上次打开降序（越大越近）；相同按名称
  expect(sortApps(apps, "opened", "en").map((a) => a.name)).toEqual(["Mike", "Zed", "Alpha"]);
});

// ---- 卸载结果分类与是否从列表移除 ----

test("classifyUninstall: pure in-use failures (moved 0) -> inUse", () => {
  expect(classifyUninstall(uninstall({ moved: 0, failed: ["/Applications/X.app: in-use"] }))).toBe("inUse");
});

test("classifyUninstall: any other failures -> partial", () => {
  expect(classifyUninstall(uninstall({ moved: 1, failed: ["/private/x"] }))).toBe("partial");
  expect(classifyUninstall(uninstall({ moved: 0, failed: ["/x: denied"] }))).toBe("partial");
});

test("classifyUninstall: no failures -> ok", () => {
  expect(classifyUninstall(uninstall({ moved: 4, failed: [] }))).toBe("ok");
});

test("bundleFailed: bundle path itself or 'path:' prefix fails -> true, otherwise row removed", () => {
  const p = "/Applications/X.app";
  expect(bundleFailed(uninstall({ path: p, failed: [p] }))).toBe(true);
  expect(bundleFailed(uninstall({ path: p, failed: [`${p}: cache`] }))).toBe(true);
  expect(bundleFailed(uninstall({ path: p, failed: ["/private/other"] }))).toBe(false);
  expect(bundleFailed(uninstall({ path: p, failed: [] }))).toBe(false);
});

// ---- 残留：__done 汇总与排序 ----

test("parseDoneEv reads total size from size field and elapsed ms from path field", () => {
  const done = orphan({ path: "1250", size: 4096 });
  expect(parseDoneEv(done)).toEqual({ totalSize: 4096, elapsedMs: 1250 });
});

test("orphanFootAfterRemoval recomputes n and size from remaining items, keeps elapsed seconds", () => {
  const foot = orphanFootAfterRemoval(
    [orphan({ path: "/a", size: 100 }), orphan({ path: "/b", size: 250 }), orphan({ path: "/c", size: -1 })],
    1.5,
  );
  expect(foot.n).toBe(3);
  expect(foot.size).toBe(350); // size<0 的未知项不计
  expect(foot.t).toBe(1.5);
});

test("sortOrphans sorts by size desc", () => {
  const items = [orphan({ path: "/a", size: 10 }), orphan({ path: "/b", size: 90 }), orphan({ path: "/c", size: 50 })];
  expect(sortOrphans(items).map((o) => o.path)).toEqual(["/b", "/c", "/a"]);
});

test("barWidth is at least 2% of the largest item", () => {
  expect(barWidth(1000, 1000)).toBe(100);
  expect(barWidth(5, 1000)).toBe(2);
  expect(barWidth(500, 1000)).toBe(50);
});
