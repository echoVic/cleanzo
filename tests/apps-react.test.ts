import { expect, spyOn, test } from "bun:test";
import type { AppInfo, AppUpdate, GrowthApp, GrowthSummary, OrphanItem, UninstallDetail, UninstallResult } from "../src/mygo";
import {
  barWidth, bundleFailed, classifyUninstall, filterApps, orphanFootAfterRemoval,
  parseDoneEv, readAppSort, sortApps, sortOrphans,
} from "../src/react/apps/helpers";
import { appsRequest, requestAppsFilter, requestAppsUninstall } from "../src/react/apps/requests";
import {
  filterByKind, growthFor, growthTagFor, isIdle, reasonKey, sourceKey, splitUsage, updateCount, versionTag,
} from "../src/react/apps/updates";
import type { Changes } from "./page-harness";

test("macOS app filters reference existing panels and label the active content", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppsPage } = await import("../src/react/apps/AppsPage");
  const { setPlatformForTest } = await import("../src/platform");
  try {
    setPlatformForTest("darwin");
    for (const filter of ["all", "updates", "idle", "leftovers"] as const) {
      const html = renderToStaticMarkup(createElement(AppsPage, { active: false, initialFilter: filter, nonce: 0 }));
      const tabs = [...html.matchAll(/<button\b[^>]*role="tab"[^>]*>/g)].map((m) => m[0]);
      expect(tabs).toHaveLength(4);
      for (const tab of tabs) {
        const panelId = /aria-controls="([^"]+)"/.exec(tab)![1]!;
        expect(html).toContain(`id="${panelId}"`);
      }
      const selected = tabs.find((tab) => tab.includes('aria-selected="true"'))!;
      const tabId = /\bid="([^"]+)"/.exec(selected)![1]!;
      const panelId = /aria-controls="([^"]+)"/.exec(selected)![1]!;
      const panels = [...html.matchAll(/<div\b[^>]*role="tabpanel"[^>]*>/g)].map((m) => m[0]);
      const panel = panels.find((p) => p.includes(`id="${panelId}"`))!;
      expect(panel).toContain(`aria-labelledby="${tabId}"`);
      expect(panel).not.toContain("hidden");
      // Switching the filter must retain one installed-list instance and its check state.
      expect(html.match(/name="app-search"/g)).toHaveLength(1);
    }
  } finally { setPlatformForTest(undefined); }
});

test("fresh app details update running controls in both directions", async () => {
  for (const running of [true, false]) {
    const a = app({ name: "Review App", path: "/Applications/Review App.app", running: !running });
    const stream = updateStream();
    const h = await appsHarness({
      listApps: async () => [a],
      startUpdateCheck: () => stream as any,
      uninstallDetail: async () => ({
        app: { ...a, running },
        items: [{ path: "/fixture/cache", kind: "cache", size: 1024 }],
        totalSize: 1024,
      }),
    });
    try {
      await h.flush({ active: true });
      h.rows()[0].props.onToggle(a.id);
      await h.flush();
      expect(h.expanded(a.id).usage.cache).toBe(1024);
      expect(h.expanded(a.id).app.running).toBe(running);
      expect(h.rows()[0].props.app.running).toBe(running);
    } finally { h.dispose(); }
  }
});

function app(p: Partial<AppInfo> & { name: string; path: string }): AppInfo {
  return { id: p.path, publisher: "", bundleId: "", version: "", size: 0, lastOpened: 0, running: false, ...p };
}

function orphan(p: Partial<OrphanItem> & { path: string }): OrphanItem {
  return { size: 0, kind: "preferences", trashable: true, isDir: false, ...p };
}

function uninstall(res: Partial<UninstallResult>): UninstallResult {
  return { launched: false, path: "/Applications/X.app", moved: 0, freed: 0, failed: [], ...res };
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

test("classifyUninstall: a usage check that could not finish (nothing moved) -> usageUnknown", () => {
  expect(classifyUninstall(uninstall({ moved: 0, failed: ["/Applications/X.app: usage-unknown"] }))).toBe("usageUnknown");
  expect(classifyUninstall(uninstall({ moved: 0, failed: ["/Applications/X.app: usage-unknown", "/x: denied"] }))).toBe("partial");
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


test("Windows Apps cannot mount Leftovers even when requested by an old navigation action", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppsPage } = await import("../src/react/apps/AppsPage");
  const { setPlatformForTest } = await import("../src/platform");
  try {
    setPlatformForTest("darwin");
    const mac = renderToStaticMarkup(createElement(AppsPage, { active: false, initialFilter: "leftovers", nonce: 0 }));
    expect(mac).toMatch(/role="tabpanel"[^>]*aria-labelledby="[^"]+-leftovers-tab"/);
    expect(mac).toContain("leftovers-head");
    setPlatformForTest("win32");
    const win = renderToStaticMarkup(createElement(AppsPage, { active: false, initialFilter: "leftovers", nonce: 0 }));
    expect(win).not.toContain('role="tabpanel"');
    expect(win).not.toContain("leftovers-head");
    expect(win).toContain('name="app-search"');
  } finally { setPlatformForTest(undefined); }
});

test("Windows launched result keeps the row until an explicit refresh", () => {
  const result = uninstall({ launched: true });
  expect(classifyUninstall(result)).toBe("launched");
  expect(bundleFailed(result)).toBe(true);
});

test("Windows app row displays publisher and version and sends only its registry id", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppRow } = await import("../src/react/apps/AppRow");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref } = await import("../src/i18n");
  const a = app({ id: "hklm32:chosen", name: "Acme", path: "", publisher: "Acme Inc", version: "2.4", bundleId: "mac.bundle" });
  try {
    setPlatformForTest("win32");
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      let picked = "";
      const row = AppRow({ app: a, update: undefined, growthTag: null, open: false, busy: null, brewBusy: false, onToggle: (id) => { picked = id; }, onUpgrade() {} });
      const html = renderToStaticMarkup(row);
      expect(html).toContain("Acme Inc");
      expect(html).toContain("2.4");
      expect(html).not.toContain("mac.bundle");
      const head = row.props.children[0];
      head.props.children[0].props.onClick();
      expect(picked).toBe("hklm32:chosen");
    }
  } finally { setPlatformForTest(undefined); setPref("system"); }
});

test("Windows uninstall dialog hides related files and offers refresh after launch in zh/en", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { Dialog } = await import("../src/ui/components/dialog");
  const { UninstallDialogBody } = await import("../src/react/apps/UninstallDialog");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref, t } = await import("../src/i18n");
  const detail = { app: app({ id: "hkcu:chosen", name: "Acme", path: "C:\\Acme" }), items: [{ path: "private-related-file", kind: "cache", size: 4 }], totalSize: 4 };
  try {
    setPlatformForTest("win32");
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      const props = { detail, failMsg: null, onConfirm() {}, onRefresh() {} };
      const render = (stage: "detail" | "launched") => renderToStaticMarkup(createElement(Dialog, { open: true }, createElement(UninstallDialogBody, { ...props, stage })));
      expect(render("detail")).toContain(t("apps.body", { n: 1, size: "4 B" }));
      expect(render("detail")).not.toContain("private-related-file");
      const confirm = lang === "zh" ? "打开卸载程序" : "Open Uninstaller";
      expect(render("detail")).toMatch(new RegExp(`<button[^>]*>${confirm}</button>`));
      for (const stage of ["detail", "launched"] as const) {
        expect(render(stage)).not.toMatch(/回收站|Recycle Bin/i);
      }
      expect(render("launched")).toContain(t("apps.launched"));
      expect(render("launched")).toContain(t("apps.refresh"));
      expect(render("launched")).not.toContain(t("apps.confirm"));
    }
  } finally { setPlatformForTest(undefined); setPref("system"); }
});

test("Windows Apps subtitle explains the uninstaller and Store link uses the OS settings URI", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppsPage } = await import("../src/react/apps/AppsPage");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref, t } = await import("../src/i18n");
  try {
    setPlatformForTest("win32");
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      const html = renderToStaticMarkup(createElement(AppsPage, { active: false, initialFilter: "all", nonce: 0 }));
      expect(html).toContain(t("apps.sub"));
      expect(t("apps.sub")).toContain(lang === "zh" ? "卸载器" : "uninstaller");
      expect(html).toContain('href="ms-settings:appsfeatures"');
      expect(html).toContain(t("apps.store"));
      expect(html).not.toContain(lang === "zh" ? "关联文件" : "related files");
    }
    setPlatformForTest("darwin");
    expect(renderToStaticMarkup(createElement(AppsPage, { active: false, initialFilter: "all", nonce: 0 }))).not.toContain("ms-settings:");
  } finally { setPlatformForTest(undefined); setPref("system"); }
});

test("Windows app rows omit unknown last-opened metadata without an empty line", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppRow } = await import("../src/react/apps/AppRow");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref } = await import("../src/i18n");
  const a = app({ name: "Acme", path: "", size: 1024, lastOpened: 0 });
  const render = () => renderToStaticMarkup(createElement(AppRow, { app: a, update: undefined, growthTag: null, open: false, busy: null, brewBusy: false, onToggle() {}, onUpgrade() {} }));
  try {
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      setPlatformForTest("win32");
      const win = render();
      expect(win).not.toContain("Never opened");
      expect(win).not.toContain("从未打开");
      expect(win).not.toContain("app-used");
      expect(win).toContain('<div class="app-meta"><div class="app-size">1.0 KB</div></div>');

      setPlatformForTest("darwin");
      const mac = render();
      expect(mac).toContain("app-used");
      expect(mac).toContain(lang === "zh" ? "从未打开" : "Never opened");
    }
  } finally { setPlatformForTest(undefined); setPref("system"); }
});

test("Windows Apps hide last-opened sorting and fall back from a saved opened sort", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppsList } = await import("../src/react/apps/AppsList");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref, t } = await import("../src/i18n");
  const savedSort = localStorage.getItem("cleanzo.appSort");
  const render = () => renderToStaticMarkup(createElement(AppsList, { active: false, filter: "all", onBusyChange() {}, onCounts() {} }));
  try {
    localStorage.setItem("cleanzo.appSort", "opened");
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      setPlatformForTest("win32");
      const win = render();
      expect(win).not.toContain('value="opened"');
      expect(win).not.toContain(t("apps.sortOpened"));
      expect(win).toContain('<option value="name" selected="">');

      setPlatformForTest("darwin");
      const mac = render();
      expect(mac).toContain('<option value="opened" selected="">');
      expect(mac).toContain(t("apps.sortOpened"));
    }
  } finally {
    localStorage.setItem("cleanzo.appSort", savedSort ?? "name");
    setPlatformForTest(undefined);
    setPref("system");
  }
});

test("增长页请求卸载：切到全部、记下要展开的 App，两个 nonce 都递增", () => {
  requestAppsFilter("leftovers");
  const before = appsRequest();
  requestAppsUninstall("/Applications/Lark.app");
  const after = appsRequest();
  expect(after.filter).toBe("all");
  expect(after.filterNonce).toBe(before.filterNonce + 1);
  expect(after.uninstallPath).toBe("/Applications/Lark.app");
  expect(after.uninstallNonce).toBe(before.uninstallNonce + 1);
});

test("installed app row shows the app's icon once known", async () => {
  await import("./setup-dom");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppRow } = await import("../src/react/apps/AppRow");
  const { loadIcon, resetIconsForTest } = await import("../src/react/icons/cache");
  const a = app({ id: "acme", name: "Acme", path: "/Applications/Acme.app" });
  resetIconsForTest(async () => "data:image/png;base64,QUJD");
  try {
    expect(renderToStaticMarkup(AppRow({ app: a, update: undefined, growthTag: null, open: false, busy: null, brewBusy: false, onToggle() {}, onUpgrade() {} }))).toContain('<div class="app-icon">A</div>');
    await loadIcon("/Applications/Acme.app");
    expect(renderToStaticMarkup(AppRow({ app: a, update: undefined, growthTag: null, open: false, busy: null, brewBusy: false, onToggle() {}, onUpgrade() {} }))).toContain('class="app-icon cz-app-img"');
  } finally { resetIconsForTest(async () => ""); }
});

function upd(p: Partial<AppUpdate> & { id: string }): AppUpdate {
  return { source: "", current: "", latest: "", state: "unknown", reason: "", notesUrl: "", action: "", ...p };
}

const NOW = 1_800_000_000;

test("「很久没用」：90 天以前或从未打开", () => {
  expect(isIdle(app({ name: "A", path: "/a", lastOpened: 0 }), NOW)).toBe(true);
  expect(isIdle(app({ name: "A", path: "/a", lastOpened: NOW - 91 * 86400 }), NOW)).toBe(true);
  expect(isIdle(app({ name: "A", path: "/a", lastOpened: NOW - 89 * 86400 }), NOW)).toBe(false);
});

test("筛选：有更新只看 state=update；残留不列已装 App", () => {
  const apps = [
    app({ name: "Arc", path: "/Applications/Arc.app", lastOpened: NOW }),
    app({ name: "Old", path: "/Applications/Old.app", lastOpened: 0 }),
    app({ name: "Cur", path: "/Applications/Cur.app", lastOpened: NOW }),
  ];
  const updates = new Map([
    ["/Applications/Arc.app", upd({ id: "/Applications/Arc.app", state: "update", current: "1.170.0", latest: "1.171.0" })],
    ["/Applications/Cur.app", upd({ id: "/Applications/Cur.app", state: "current" })],
    ["/Applications/Old.app", upd({ id: "/Applications/Old.app", state: "unknown", reason: "uncomparable" })],
  ]);
  expect(filterByKind(apps, "all", updates, NOW).map((a) => a.name)).toEqual(["Arc", "Old", "Cur"]);
  expect(filterByKind(apps, "updates", updates, NOW).map((a) => a.name)).toEqual(["Arc"]);
  expect(filterByKind(apps, "idle", updates, NOW).map((a) => a.name)).toEqual(["Old"]);
  expect(filterByKind(apps, "leftovers", updates, NOW)).toEqual([]);
  expect(updateCount(updates)).toBe(1);
});

test("版本标签和来源、原因文案键", () => {
  expect(versionTag(upd({ id: "x", state: "update", current: "1.170.0", latest: "1.171.0 (89666)" }))).toBe("1.170.0 → 1.171.0 (89666)");
  expect(versionTag(upd({ id: "x", state: "current", current: "1", latest: "1" }))).toBeNull();
  expect(versionTag(undefined)).toBeNull();
  expect(sourceKey("brew")).toBe("apps.src.brew");
  expect(sourceKey("")).toBeNull();
  expect(reasonKey("bad-url")).toBe("apps.reason.bad-url");
  expect(reasonKey("something-new")).toBe("apps.reason.network");
});

test("占用拆成本体、数据、缓存", () => {
  const d: UninstallDetail = {
    app: app({ name: "Arc", path: "/Applications/Arc.app" }),
    items: [
      { path: "/Applications/Arc.app", kind: "app", size: 800 },
      { path: "~/Library/Caches/a", kind: "cache", size: 300 },
      { path: "~/Library/Caches/Arc", kind: "cache", size: 100 },
      { path: "~/Library/Application Support/Arc", kind: "support", size: 2000 },
      { path: "~/Library/Preferences/a.plist", kind: "prefs", size: 5 },
    ],
    totalSize: 3205,
  };
  expect(splitUsage(d)).toEqual({ app: 800, data: 2005, cache: 400, cachePaths: ["~/Library/Caches/a", "~/Library/Caches/Arc"] });
});

test("增长按 appPath 对上；小标签按 bundle ID", () => {
  const a = app({ name: "Arc", path: "/Applications/Arc.app", bundleId: "company.thebrowser.Browser" });
  const g = { key: "app:company.thebrowser.Browser", appPath: "/Applications/Arc.app", week: { bytes: 3 * 2 ** 30, atLeast: false } } as GrowthApp;
  expect(growthFor(a, [g])).toBe(g);
  expect(growthFor(app({ name: "B", path: "/Applications/B.app" }), [g])).toBeNull();
  const summary = {
    supported: true, status: {} as never,
    config: { enabled: true, openAtLogin: false, dailyGB: 2, weeklyGB: 10, ignored: [], asked: true },
    alerts: [{ key: "app:company.thebrowser.Browser", name: "Arc", window: "week", days: 7, bytes: 11 * 2 ** 30, atLeast: false, folder: "", total: 0, at: 0 }],
  } as GrowthSummary;
  expect(growthTagFor(a, summary)?.bytes).toBe(11 * 2 ** 30);
  expect(growthTagFor(a, { ...summary, config: { ...summary.config, enabled: false } })).toBeNull();
});

test("按 7 天增长排序：有数据的在前，没数据的按大小", () => {
  const apps = [
    app({ name: "A", path: "/a", size: 10 }),
    app({ name: "B", path: "/b", size: 30 }),
    app({ name: "C", path: "/c", size: 20 }),
  ];
  const week = new Map([["/a", 5], ["/c", 9]]);
  expect(sortApps(apps, "growth", "en", week).map((a) => a.name)).toEqual(["C", "A", "B"]);
  localStorage.setItem("cleanzo.appSort", "growth");
  expect(readAppSort()).toBe("growth");
  localStorage.setItem("cleanzo.appSort", "name");
});

test("每个来源和原因都有中英文文案（不会把键名显示出来）", async () => {
  const { setPref, t } = await import("../src/i18n");
  const sources = ["mas", "brew", "sparkle", "electron"];
  const reasons = ["no-source", "bad-url", "network", "rate-limited", "bad-feed", "uncomparable", "not-found"];
  try {
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      for (const s of sources) expect(t(sourceKey(s)!)).not.toContain("apps.src.");
      for (const r of reasons) expect(t(reasonKey(r))).not.toContain("apps.reason.");
    }
  } finally { setPref("system"); }
});

test("收起的行：有新版本显示版本标签和独立的「更新」按钮；展开状态写在 aria-expanded", async () => {
  await import("./setup-dom");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { createElement } = await import("react");
  const { AppRow } = await import("../src/react/apps/AppRow");
  const { setPref } = await import("../src/i18n");
  setPref("zh");
  const a = app({ id: "/Applications/Arc.app", name: "Arc", path: "/Applications/Arc.app", version: "1.170.0", running: true });
  const u = upd({ id: a.id, state: "update", current: "1.170.0", latest: "1.171.0", source: "brew", action: "brew" });
  const base = { app: a, update: u, growthTag: { key: "app:x", window: "week" as const, bytes: 3 * 2 ** 30, atLeast: false }, open: false, busy: null, brewBusy: false, onToggle() {}, onUpgrade() {} };
  const html = renderToStaticMarkup(createElement(AppRow, base));
  expect(html).toContain("1.170.0 → 1.171.0");
  expect(html).toContain("运行中");
  expect(html).not.toContain("磁盘 7 天"); // 最多两个标签：版本、运行中优先
  expect(html).toContain('aria-expanded="false"');
  expect(html).toMatch(/<button[^>]*app-upgrade[^>]*>更新<\/button>/);
  expect(html).not.toMatch(/<button[^>]*app-row[^>]*>(?:(?!<\/button>).)*<button/); // 不能按钮套按钮
  const busy = renderToStaticMarkup(createElement(AppRow, { ...base, busy: "upgrade" }));
  expect(busy).toContain("正在更新…");
  const cur = renderToStaticMarkup(createElement(AppRow, { ...base, update: upd({ id: a.id, state: "current" }), app: { ...a, running: false } }));
  expect(cur).not.toContain("app-upgrade");
  expect(cur).toContain("磁盘 7 天 +3.0 GB");
  expect(cur).toContain("1.170.0"); // 灰色版本号
  setPref("system");
});

test("运行中的 brew 更新在按钮外提供退出提示；其他禁用原因不提示退出", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppRow } = await import("../src/react/apps/AppRow");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref, t } = await import("../src/i18n");
  const a = app({ name: "WeChat", path: "/Applications/WeChat.app", running: true });
  const u = upd({ id: a.id, source: "brew", action: "brew", state: "update", current: "4.1.15", latest: "4.2.0.15" });
  const base = { app: a, update: u, growthTag: null, open: false, busy: null, brewBusy: false, onToggle() {}, onUpgrade() {} };
  try {
    setPlatformForTest("darwin");
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      const html = renderToStaticMarkup(createElement(AppRow, base));
      expect(html).toMatch(new RegExp(`<span[^>]*title="${t("apps.hint.quitFirst")}"[^>]*><button[^>]*app-upgrade[^>]*disabled=""[^>]*>`));
      expect(html).not.toMatch(/<button[^>]*title=/);
      const available = renderToStaticMarkup(createElement(AppRow, { ...base, app: { ...a, running: false } }));
      expect(available).not.toContain('disabled=""');
      expect(available).not.toContain('title="' + t("apps.hint.quitFirst") + '"');
      const otherSource = renderToStaticMarkup(createElement(AppRow, { ...base, update: { ...u, source: "sparkle", action: "open-app" } }));
      expect(otherSource).not.toContain('disabled=""');
      expect(otherSource).not.toContain('title="' + t("apps.hint.quitFirst") + '"');
      for (const state of [{ busy: "upgrade" as const }, { busy: "cache" as const }, { brewBusy: true }]) {
        const blocked = renderToStaticMarkup(createElement(AppRow, { ...base, app: { ...a, running: false }, ...state }));
        expect(blocked).toContain('disabled=""');
        expect(blocked).not.toContain('title="' + t("apps.hint.quitFirst") + '"');
      }
    }
  } finally { setPlatformForTest(undefined); setPref("system"); }
});

test("展开的三栏：版本原因、缓存可清、运行中禁用清理、卸载按钮", async () => {
  await import("./setup-dom");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { createElement } = await import("react");
  const { AppRowExpanded } = await import("../src/react/apps/AppRowExpanded");
  const { setPref } = await import("../src/i18n");
  setPref("zh");
  const a = app({ id: "/Applications/ima.app", name: "ima", path: "/Applications/ima.app", running: false });
  const noop = () => {};
  const props = {
    app: a, update: upd({ id: a.id, source: "sparkle", state: "unknown", reason: "bad-url" }), checking: false,
    usage: { app: 800, data: 2000, cache: 400, cachePaths: ["/c"] }, usageError: null, week: { bytes: 3 * 2 ** 30, atLeast: false }, month: null,
    busy: null, brewBusy: false, stuck: false, upgradeFail: null,
    onUpgrade: noop, onCleanCache: noop, onQuit: noop, onForceQuit: noop, onUninstall: noop, onCopy: noop,
  };
  const html = renderToStaticMarkup(createElement(AppRowExpanded, props));
  expect(html).toContain("更新地址无效");
  expect(html).toContain("App 本体");
  expect(html).toMatch(/<button[^>]*>清理<\/button>/);
  expect(html).not.toMatch(/<button[^>]* disabled=""[^>]*>清理<\/button>/);
  expect(html).toContain("没有在运行");
  expect(html).toContain("卸载…");
  expect(html).toContain("+3.0 GB");

  const running = renderToStaticMarkup(createElement(AppRowExpanded, { ...props, app: { ...a, running: true }, stuck: true }));
  expect(running).toMatch(/<button[^>]* disabled=""[^>]*>清理<\/button>/);
  expect(running).toContain("先退出它");
  expect(running).toMatch(/<button[^>]*>退出<\/button>/);
  expect(running).toMatch(/<button[^>]*>强制退出<\/button>/);

  const failed = renderToStaticMarkup(createElement(AppRowExpanded, {
    ...props, update: upd({ id: a.id, source: "brew", state: "update", current: "1", latest: "2", action: "brew" }),
    upgradeFail: { detail: "sudo: a terminal is required", command: "brew upgrade --cask x" },
  }));
  expect(failed).toContain("sudo: a terminal is required");
  expect(failed).toContain("复制命令");
  const notes = renderToStaticMarkup(createElement(AppRowExpanded, { ...props, update: upd({ id: a.id, source: "sparkle", state: "update", current: "1", latest: "2", action: "open-app", notesUrl: "https://x.example.com/notes" }) }));
  expect(notes).toContain('href="https://x.example.com/notes"');
  expect(notes).toContain("请在它的菜单里选择「检查更新…」");
  setPref("system");
});

test("Windows 行和展开：没有更新、缓存、增长，只剩大小和卸载", async () => {
  await import("./setup-dom");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { createElement } = await import("react");
  const { AppRow } = await import("../src/react/apps/AppRow");
  const { AppRowExpanded } = await import("../src/react/apps/AppRowExpanded");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref } = await import("../src/i18n");
  const a = app({ id: "hkcu:x", name: "Acme", path: "", publisher: "Acme Inc", version: "2.4", size: 1024 });
  const noop = () => {};
  try {
    setPlatformForTest("win32");
    setPref("en");
    const row = renderToStaticMarkup(createElement(AppRow, { app: a, update: upd({ id: a.id, state: "update", current: "1", latest: "2" }), growthTag: null, open: true, busy: null, brewBusy: false, onToggle: noop, onUpgrade: noop }));
    expect(row).not.toContain("app-upgrade");
    expect(row).toContain("Acme Inc");
    const ex = renderToStaticMarkup(createElement(AppRowExpanded, {
      app: a, update: undefined, checking: false, usage: { app: 1024, data: 0, cache: 0, cachePaths: [] }, usageError: null, week: null, month: null,
      busy: null, brewBusy: false, stuck: false, upgradeFail: null,
      onUpgrade: noop, onCleanCache: noop, onQuit: noop, onForceQuit: noop, onUninstall: noop, onCopy: noop,
    }));
    expect(ex).not.toContain("Version");
    expect(ex).not.toContain("Cache");
    expect(ex).toContain("1.0 KB");
    expect(ex).toContain("Uninstall…");
  } finally { setPlatformForTest(undefined); setPref("system"); }
});

test("应用页筛选条：macOS 有四个筛选，Windows 没有；页脚写明会联网", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppsPage } = await import("../src/react/apps/AppsPage");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref, t } = await import("../src/i18n");
  try {
    setPref("zh");
    setPlatformForTest("darwin");
    const mac = renderToStaticMarkup(createElement(AppsPage, { active: false, initialFilter: "all", nonce: 0 }));
    for (const s of ["全部 0", "有更新", "很久没用", "残留", t("apps.checkNote"), t("apps.recheck")]) expect(mac).toContain(s);
    setPlatformForTest("win32");
    const win = renderToStaticMarkup(createElement(AppsPage, { active: false, initialFilter: "all", nonce: 0 }));
    for (const s of ["有更新", "很久没用", t("apps.checkNote"), t("apps.recheck")]) expect(win).not.toContain(s);
  } finally { setPlatformForTest(undefined); setPref("system"); }
});

test("外部请求：残留筛选只记请求不挂载；卸载请求回到「全部」", () => {
  requestAppsFilter("leftovers");
  expect(appsRequest().filter).toBe("leftovers");
  requestAppsUninstall("/Applications/Lark.app");
  expect(appsRequest().filter).toBe("all");
  expect(appsRequest().uninstallPath).toBe("/Applications/Lark.app");
});

test("main.ts 每次切视图都告诉应用页是否可见；垃圾页「查看」残留会切到应用页", async () => {
  const { readFileSync } = await import("node:fs");
  const main = readFileSync(new URL("../src/main.ts", import.meta.url), "utf8");
  expect(main).toContain('showReactApps(v === "apps")');
  expect(main).not.toMatch(/if \(v === "apps"\) showReactApps\(\)/);
  const clean = readFileSync(new URL("../src/react/clean/CleanPage.tsx", import.meta.url), "utf8");
  expect(clean).toContain('requestAppsFilter("leftovers")');
  expect(clean).toContain('.nav-item[data-view="apps"]');
});

test("更新检查只在页面开着时跑：离开就停，没查完回来从头再查，查完不重复", async () => {
  await import("./setup-dom");
  const { checkStep } = await import("../src/react/apps/AppsList");
  const idle = { running: false, finished: false };
  expect(checkStep({ active: true, loaded: false, ...idle }, { active: true, loaded: true })).toBe("start");
  expect(checkStep({ active: true, loaded: true, running: true, finished: false }, { active: false, loaded: true })).toBe("stop");
  expect(checkStep({ active: false, loaded: true, ...idle }, { active: true, loaded: true })).toBe("start");
  expect(checkStep({ active: false, loaded: true, running: false, finished: true }, { active: true, loaded: true })).toBe("none");
  expect(checkStep({ active: false, loaded: false, ...idle }, { active: false, loaded: true })).toBe("none");
  expect(checkStep({ active: true, loaded: true, running: true, finished: false }, { active: true, loaded: true })).toBe("none");
});

test("增长关闭时已保存的增长排序回退到名称；更新筛选在检查前显示进度", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppsList } = await import("../src/react/apps/AppsList");
  const { setPref } = await import("../src/i18n");
  const saved = localStorage.getItem("cleanzo.appSort");
  try {
    setPref("zh");
    localStorage.setItem("cleanzo.appSort", "growth");
    const html = renderToStaticMarkup(createElement(AppsList, { active: false, filter: "updates", onBusyChange() {}, onCounts() {} }));
    expect(html).toContain('<option value="name" selected="">');
    expect(html).not.toContain('value="growth"');
    expect(html).toContain("还在检查（0/0）");
    expect(html).not.toContain("都是最新的");
  } finally { localStorage.setItem("cleanzo.appSort", saved ?? "name"); setPref("system"); }
});

// 只运行页面自身的 hook 和返回的元素树，桥接在 appsApi 边界替换；不伪造子组件行为。
// 现有最小 DOM 不支持客户端渲染，这个夹具不验证布局、焦点环或原生窗口。
async function appsHarness(overrides: Partial<typeof import("../src/react/apps/api").appsApi> = {}) {
  await import("./setup-dom");
  const React = await import("react");
  const { AppsList } = await import("../src/react/apps/AppsList");
  const { appsApi } = await import("../src/react/apps/api");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref } = await import("../src/i18n");
  const savedApi = { ...appsApi };
  const savedWindow = globalThis.window;
  const savedQuery = document.querySelector;
  const savedRaf = globalThis.requestAnimationFrame, savedCancel = globalThis.cancelAnimationFrame;
  const savedCSS = globalThis.CSS;
  const frames = new Map<number, FrameRequestCallback>();
  const messages: string[] = [];
  const focus: string[] = [];
  const toastEl = { textContent: "", className: "", setAttribute() {} };
  // 和真的按钮一样：focus() 之后它就是 document.activeElement（实现会核对焦点真的落上去了）
  const button = { scrollIntoView() { focus.push("scroll"); }, focus() { focus.push("focus"); (document as any).activeElement = button; } };
  document.querySelector = ((selector: string) => selector === "#toast" ? toastEl : selector.endsWith(".app-uninstall") ? button : null) as any;
  globalThis.window = { setTimeout: () => 0, clearTimeout() {} } as any;
  globalThis.requestAnimationFrame = (fn) => { const id = frames.size + 1; frames.set(id, fn); return id; };
  globalThis.cancelAnimationFrame = (id) => { frames.delete(id); };
  globalThis.CSS = { escape: (s: string) => s.replaceAll('"', '\\"') } as any;
  setPlatformForTest("darwin");
  setPref("zh");
  Object.assign(appsApi, {
    listApps: async () => [],
    growthSummary: async () => ({ supported: true, config: { enabled: false }, status: {}, alerts: [] }),
    ...overrides,
  });
  const slots: any[] = [];
  let index = 0, dirty = true, tree: any;
  let pending: (() => void)[] = [];
  let counts: { updates: number | null; total: number } = { updates: null, total: 0 };
  let busy = false, checking = false;
  let props: import("../src/react/apps/AppsList").AppsListProps = {
    active: false, filter: "all", onCounts: (c) => { counts = c; }, onBusyChange: (b) => { busy = b; }, onChecking: (b) => { checking = b; },
  };
  const changed = (a: unknown[] | undefined, b: unknown[] | undefined) => !a || !b || a.length !== b.length || a.some((v, i) => !Object.is(v, b[i]));
  const effect = (fn: () => void | (() => void), deps?: unknown[]) => {
    const slot = index++;
    if (!slots[slot] || changed(slots[slot].deps, deps)) {
      pending.push(() => { slots[slot]?.cleanup?.(); slots[slot] = { deps, cleanup: fn() }; });
    }
  };
  const dispatcher = {
    useState(initial: any) {
      const slot = index++;
      if (!(slot in slots)) slots[slot] = typeof initial === "function" ? initial() : initial;
      return [slots[slot], (next: any) => {
        const value = typeof next === "function" ? next(slots[slot]) : next;
        if (!Object.is(value, slots[slot])) { slots[slot] = value; dirty = true; }
      }];
    },
    useRef(initial: any) { const slot = index++; return slots[slot] ??= { current: initial }; },
    useEffect: effect, useLayoutEffect: effect,
    useMemo(fn: () => unknown, deps: unknown[]) {
      const slot = index++;
      if (!slots[slot] || changed(slots[slot].deps, deps)) slots[slot] = { deps, value: fn() };
      return slots[slot].value;
    },
    useCallback(fn: any, deps: unknown[]) { return this.useMemo(() => fn, deps); },
    useSyncExternalStore(_subscribe: unknown, snapshot: () => unknown) { index++; return snapshot(); },
  };
  const internals = (React as any).__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE;
  function render() {
    let passes = 0;
    do {
      if (++passes > 40) throw new Error("页面渲染未收敛");
      dirty = false; index = 0; pending = [];
      const saved = internals.H;
      internals.H = dispatcher;
      try { tree = AppsList(props); } finally { internals.H = saved; }
      const effects = pending;
      pending = [];
      for (const run of effects) run();
      if (toastEl.textContent && messages.at(-1) !== toastEl.textContent) messages.push(toastEl.textContent);
    } while (dirty);
  }
  function nodes(node: any = tree): any[] {
    if (Array.isArray(node)) return node.flatMap((child) => nodes(child));
    return React.isValidElement(node) ? [node, ...nodes((node.props as any).children ?? null)] : [];
  }
  return {
    async flush(next: Partial<typeof props> = {}) {
      props = { ...props, ...next };
      for (let i = 0; i < 16; i++) { render(); await Promise.resolve(); }
      render();
    },
    nodes,
    rows: () => nodes().filter((n) => n.type.name === "AnimatedAppRow"),
    expanded: (id: string) => nodes().find((n) => n.type.name === "AppRowExpanded" && n.props.app.id === id).props,
    dialog: () => nodes().find((n) => n.type.name === "UninstallDialog").props,
    frames() { const batch = [...frames.values()]; frames.clear(); for (const fn of batch) fn(0); },
    replayEffects() { for (const slot of slots) if (slot && "cleanup" in Object(slot)) { slot.cleanup?.(); slot.cleanup = undefined; slot.deps = undefined; } },
    get counts() { return counts; }, get busy() { return busy; }, get checking() { return checking; }, messages, focus,
    dispose() {
      for (const slot of slots) slot?.cleanup?.();
      Object.assign(appsApi, savedApi);
      globalThis.window = savedWindow;
      document.querySelector = savedQuery; delete (document as any).activeElement;
      globalThis.requestAnimationFrame = savedRaf; globalThis.cancelAnimationFrame = savedCancel; globalThis.CSS = savedCSS;
      setPlatformForTest(undefined); setPref("system");
    },
  };
}

function updateStream() {
  const queue: AppUpdate[] = [];
  let wake: (() => void) | undefined;
  let closed = false, closeCount = 0;
  let resolve!: () => void, reject!: (e: unknown) => void;
  const done = new Promise<void>((yes, no) => { resolve = yes; reject = no; });
  const ch = {
    close() { closed = true; closeCount++; wake?.(); },
    async *[Symbol.asyncIterator]() {
      while (true) {
        if (queue.length) { yield queue.shift()!; continue; }
        if (closed) return;
        await new Promise<void>((yes) => { wake = yes; });
      }
    },
  };
  return { ch, done, emit(u: AppUpdate) { queue.push(u); wake?.(); }, finish() { queue.push(upd({ id: "__done" })); resolve(); wake?.(); }, fail(e: unknown) { reject(e); }, get closeCount() { return closeCount; } };
}

test("更新筛选空列表只显示一次进度；收到更新行后进度移到列表上方", async () => {
  const a = app({ name: "Arc", path: "/Applications/Arc.app" });
  const b = app({ name: "B", path: "/Applications/B.app" });
  const stream = updateStream();
  const h = await appsHarness({ listApps: async () => [a, b], startUpdateCheck: () => stream as any });
  const progressNodes = () => h.nodes().filter((n) => typeof n.props.children === "string" && n.props.children.startsWith("还在检查（"));
  try {
    await h.flush({ active: true, filter: "updates" });
    expect(h.rows()).toHaveLength(0);
    expect(progressNodes()).toHaveLength(1);
    expect(progressNodes()[0].props.className).toBe("empty");
    expect(progressNodes()[0].props.children).toBe("还在检查（0/2）");
    stream.emit(upd({ id: b.id, state: "current" }));
    await h.flush();
    expect(progressNodes()).toHaveLength(1);
    expect(progressNodes()[0].props.children).toBe("还在检查（1/2）");
    stream.emit(upd({ id: a.id, state: "update", source: "brew", action: "brew" }));
    await h.flush();
    expect(h.rows()).toHaveLength(1);
    expect(progressNodes()).toHaveLength(1);
    expect(progressNodes()[0].props.role).toBe("status");
    expect(progressNodes()[0].props.children).toBe("还在检查（2/2）");
    stream.finish();
    await h.flush();
    expect(progressNodes()).toHaveLength(0);
  } finally { h.dispose(); }
});

test("页面检查实际接线：懒加载、流式计数、离开取消、返回保留结果、查完不重查、手动清空重查", async () => {
  const a = app({ name: "Arc", path: "/Applications/Arc.app" });
  const b = app({ name: "B", path: "/Applications/B.app" });
  let loads = 0;
  const streams: ReturnType<typeof updateStream>[] = [];
  const h = await appsHarness({ listApps: async () => { loads++; return [a, b]; }, startUpdateCheck(ids) { expect(ids).toEqual([a.id, b.id]); const stream = updateStream(); streams.push(stream); return stream as any; } });
  try {
    await h.flush(); expect(loads).toBe(0); expect(streams).toHaveLength(0);
    await h.flush({ active: true }); expect(loads).toBe(1); expect(streams).toHaveLength(1); expect(h.checking).toBe(true); expect(h.busy).toBe(true); expect(h.counts).toEqual({ total: 2, updates: null });
    streams[0]!.emit(upd({ id: a.id, state: "update", source: "brew", action: "brew", current: "1.170.0", latest: "1.171.0" }));
    await h.flush(); expect(h.rows()[0].props.update.latest).toBe("1.171.0"); expect(h.counts.updates).toBeNull();
    await h.flush({ active: false }); expect(streams[0]!.closeCount).toBeGreaterThan(0); expect(h.checking).toBe(false);
    await h.flush({ active: true }); expect(loads).toBe(1); expect(streams).toHaveLength(2); expect(h.rows()[0].props.update.latest).toBe("1.171.0");
    streams[0]!.emit(upd({ id: a.id, state: "current" }));
    streams[1]!.emit(upd({ id: a.id, state: "update", action: "brew", latest: "1.171.0" })); streams[1]!.finish();
    await h.flush(); expect(h.counts).toEqual({ total: 2, updates: 1 }); expect(h.checking).toBe(false); expect(h.busy).toBe(false);
    await h.flush({ active: false }); await h.flush({ active: true }); expect(streams).toHaveLength(2);
    await h.flush({ recheckNonce: 1 }); expect(streams).toHaveLength(3); expect(h.counts.updates).toBeNull(); expect(h.rows()[0].props.update).toBeUndefined();
    h.replayEffects(); await h.flush(); expect(streams).toHaveLength(4);
    streams[3]!.fail(new Error("test bridge failure")); await h.flush(); expect(h.checking).toBe(false); expect(h.nodes().some((n) => n.props.role === "alert")).toBe(true);
  } finally { h.dispose(); }
});

test("返回后新结果到达前，保留的更新按钮仍发送原应用 ID", async () => {
  const a = app({ name: "Arc", path: "/Applications/Arc.app" });
  const streams: ReturnType<typeof updateStream>[] = [];
  const upgraded: string[] = [];
  const h = await appsHarness({
    listApps: async () => [a],
    startUpdateCheck() { const s = updateStream(); streams.push(s); return s as any; },
    upgradeApp: async (id) => { upgraded.push(id); return { id, done: true, version: "2", code: "", detail: "", command: "" }; },
  });
  try {
    await h.flush({ active: true });
    streams[0]!.emit(upd({ id: a.id, state: "update", source: "brew", action: "brew", latest: "2" }));
    await h.flush();
    await h.flush({ active: false });
    await h.flush({ active: true });
    expect(streams).toHaveLength(2);
    expect(h.rows()[0].props.update.action).toBe("brew");
    h.rows()[0].props.onUpgrade(a.id);
    await h.flush();
    expect(upgraded).toEqual([a.id]);
    expect(h.rows()[0].props.update.state).toBe("current");
  } finally { h.dispose(); }
});

test("展开只开一行，重新展开复用详情、卸载确认重读；外部请求先展开和定位，不打开卸载确认", async () => {
  const a = app({ name: "A", path: "/Applications/A.app" }), b = app({ name: "B", path: "/Applications/B.app" });
  const calls: string[] = [];
  const stream = updateStream();
  const h = await appsHarness({ listApps: async () => [a, b], startUpdateCheck: () => stream as any, uninstallDetail: async (id) => { calls.push(id); return { app: id === a.id ? a : b, items: [{ path: id, kind: "app", size: 800 }, { path: "/cache", kind: "cache", size: 200 }], totalSize: 1000 }; } });
  try {
    await h.flush({ active: true, uninstallPath: b.id, uninstallNonce: 1 });
    expect(h.rows().filter((n) => n.props.open).map((n) => n.props.app.id)).toEqual([b.id]);
    expect(h.dialog().open).toBe(false); h.frames(); expect(h.focus).toEqual(["focus", "scroll"]);
    expect(h.expanded(b.id).usage).toEqual({ app: 800, data: 0, cache: 200, cachePaths: ["/cache"] });
    h.rows()[0].props.onToggle(a.id); await h.flush();
    expect(h.rows().filter((n) => n.props.open).map((n) => n.props.app.id)).toEqual([a.id]);
    h.rows()[1].props.onToggle(b.id); await h.flush(); expect(calls).toEqual([b.id, a.id]);
    h.expanded(b.id).onUninstall(b.id); await h.flush(); expect(h.dialog().open).toBe(true); expect(h.dialog().stage).toBe("detail"); expect(calls).toEqual([b.id, a.id, b.id]);
    h.dialog().onOpenChange(false); h.rows()[1].props.onToggle(b.id); await h.flush(); expect(h.rows().every((n) => !n.props.open)).toBe(true);
  } finally { h.dispose(); }
});

test("更新动作：brew 互斥、成功同步版本和计数，失败展开并提供命令，分类错误提示", async () => {
  const a = app({ name: "A", path: "/Applications/A.app", version: "1" }), b = app({ name: "B", path: "/Applications/B.app" });
  const stream = updateStream();
  let finishUpgrade!: (r: any) => void;
  let response: any = null;
  const ids: string[] = [];
  const h = await appsHarness({ listApps: async () => [a, b], startUpdateCheck: () => stream as any, uninstallDetail: async () => ({ app: b, items: [], totalSize: 0 }), upgradeApp: async (id) => { ids.push(id); return response ?? await new Promise((yes) => { finishUpgrade = yes; }); } });
  try {
    await h.flush({ active: true });
    for (const id of [a.id, b.id]) stream.emit(upd({ id, state: "update", source: "brew", action: "brew", current: "1", latest: "2" }));
    stream.finish(); await h.flush();
    h.rows()[0].props.onUpgrade(a.id); await h.flush(); expect(h.rows()[0].props.busy).toBe("upgrade"); expect(h.rows()[1].props.brewBusy).toBe(true);
    h.rows()[1].props.onUpgrade(b.id); await h.flush(); expect(ids).toEqual([a.id]); expect(h.messages.at(-1)).toContain("Homebrew 正在更新另一个 App");
    finishUpgrade({ id: a.id, done: true, version: "2", code: "", detail: "", command: "" }); await h.flush();
    expect(h.rows()[0].props.app.version).toBe("2"); expect(h.rows()[0].props.update.state).toBe("current"); expect(h.rows()[0].props.update.action).toBe(""); expect(h.counts.updates).toBe(1); expect(h.busy).toBe(false);
    response = { id: b.id, done: false, version: "", code: "brew-failed", detail: "needs terminal", command: "brew upgrade --cask b" };
    h.rows()[1].props.onUpgrade(b.id); await h.flush(); expect(h.rows()[1].props.open).toBe(true); expect(h.expanded(b.id).upgradeFail).toEqual({ detail: "needs terminal", command: "brew upgrade --cask b" });
    for (const [code, message] of [["in-use", "「B」正在运行，请先退出再更新"], ["busy", "Homebrew 正在更新"], ["not-checked", "请重新检查"]]) {
      response = { ...response, code }; h.rows()[1].props.onUpgrade(b.id); await h.flush(); expect(h.messages.at(-1)).toContain(message);
    }
  } finally { h.dispose(); }
});

test("清缓存后确认框移除缓存路径；卸载部分失败移除本体成功的行", async () => {
  const a = app({ name: "A", path: "/Applications/A.app" });
  const stream = updateStream();
  let removals = 0, cleaned = false;
  // 详情按磁盘上的现状返回：缓存清掉后就不再列出。
  const h = await appsHarness({ listApps: async () => [a], startUpdateCheck: () => stream as any, uninstallDetail: async () => cleaned
    ? { app: a, items: [{ kind: "app", path: a.id, size: 800 }], totalSize: 800 }
    : { app: a, items: [{ kind: "app", path: a.id, size: 800 }, { kind: "cache", path: "/cache", size: 200 }], totalSize: 1000 }, cleanAppCache: async (id) => { cleaned = true; return uninstall({ path: id, moved: 1, freed: 200 }); }, uninstallApp: async (id) => { removals++; return uninstall({ path: id, moved: 1, failed: ["/data: denied"] }); } });
  try {
    await h.flush({ active: true }); stream.emit(upd({ id: a.id, state: "update" })); stream.finish(); await h.flush();
    h.rows()[0].props.onToggle(a.id); await h.flush();
    h.expanded(a.id).onCleanCache(a.id); await h.flush(); expect(h.expanded(a.id).usage.cache).toBe(0); expect(h.expanded(a.id).usage.cachePaths).toEqual([]); expect(h.messages.at(-1)).toContain("200 B");
    h.expanded(a.id).onUninstall(a.id); await h.flush(); expect(removals).toBe(0); expect(h.dialog().detail.items).toEqual([{ kind: "app", path: a.id, size: 800 }]); expect(h.dialog().detail.totalSize).toBe(800);
    h.dialog().onConfirm(); await h.flush(); expect(removals).toBe(1); expect(h.rows()).toHaveLength(0); expect(h.counts).toEqual({ total: 0, updates: 0 }); expect(h.messages.at(-1)).toContain("1 项未能删除");
  } finally { h.dispose(); }
});

test("退出和强退只在确认后调用；待退出、不能退出和已退出按进程页规则处理", async () => {
  const a = app({ name: "Running", path: "/Applications/Running.app", running: true });
  for (const mode of ["pending", "cannot-quit", "not-running", "done"] as const) {
    const calls: string[] = [];
    const stream = updateStream();
    let stopped = false;
    const h = await appsHarness({
      listApps: async () => [a], startUpdateCheck: () => stream as any,
      uninstallDetail: async () => ({ app: { ...a, running: !stopped }, items: [], totalSize: 0 }),
      quitApp: async (path) => {
        calls.push(`quit:${path}`);
        if (mode === "not-running") stopped = true;
        if (mode === "cannot-quit" || mode === "not-running") throw new Error(JSON.stringify({ code: mode }));
        stopped = mode === "done";
        return stopped;
      },
      forceQuitApp: async (path) => { calls.push(`force:${path}`); stopped = true; return true; },
    });
    try {
      await h.flush({ active: true }); h.rows()[0].props.onToggle(a.id); await h.flush();
      h.expanded(a.id).onQuit(a.id); await h.flush(); expect(calls).toEqual([]);
      const confirm = h.nodes().find((n) => n.props.onClick && n.props.children === "退出");
      expect(confirm).toBeDefined(); confirm.props.onClick(); await h.flush(); expect(calls).toEqual([`quit:${a.path}`]);
      if (mode === "pending" || mode === "cannot-quit") {
        expect(h.expanded(a.id).stuck).toBe(true); expect(h.rows()[0].props.app.running).toBe(true);
        h.expanded(a.id).onForceQuit(a.id); await h.flush(); expect(calls).toHaveLength(1);
        h.nodes().find((n) => n.props.onClick && n.props.children === "强制退出").props.onClick(); await h.flush(); expect(calls[1]).toBe(`force:${a.path}`);
      }
      expect(h.rows()[0].props.app.running).toBe(false); expect(h.expanded(a.id).stuck).toBe(false); expect(h.busy).toBe(true); // 检查流仍在跑。
      h.expanded(a.id).onUninstall(a.id); await h.flush(); expect(h.dialog().detail.app.running).toBe(false);
    } finally { h.dispose(); }
  }
});

test("Windows 接线：不检查更新和增长，外部展开后打开卸载器、保留行、显式刷新", async () => {
  const a = app({ id: "hklm32:chosen", name: "Acme", path: "C:\\Acme", version: "2.4" });
  let loads = 0, forbidden = 0;
  const calls: string[] = [];
  const h = await appsHarness({
    listApps: async () => { loads++; return [a]; },
    startUpdateCheck: () => { forbidden++; throw new Error("Windows must not check"); },
    growthSummary: async () => { forbidden++; throw new Error("Windows must not read growth"); },
    uninstallDetail: async (id) => { calls.push(`detail:${id}`); return { app: a, items: [], totalSize: a.size }; },
    uninstallApp: async (id) => { calls.push(`uninstall:${id}`); return uninstall({ path: id, launched: true }); },
  });
  const { setPlatformForTest } = await import("../src/platform");
  try {
    setPlatformForTest("win32");
    await h.flush({ active: true, uninstallPath: a.id, uninstallNonce: 1 }); expect(forbidden).toBe(0); expect(h.busy).toBe(false); expect(h.rows()[0].props.open).toBe(true); expect(h.dialog().open).toBe(false);
    h.expanded(a.id).onUninstall(a.id); await h.flush(); expect(calls).toEqual([`detail:${a.id}`, `detail:${a.id}`]); expect(h.dialog().stage).toBe("detail");
    h.dialog().onConfirm(); await h.flush(); expect(calls[2]).toBe(`uninstall:${a.id}`); expect(h.dialog().stage).toBe("launched"); expect(h.rows()).toHaveLength(1);
    h.dialog().onRefresh(); await h.flush(); expect(loads).toBe(2); expect(h.dialog().open).toBe(false); expect(forbidden).toBe(0);
  } finally { h.dispose(); }
});

test("增长开启才读明细、展示排序和增长；搜索排在筛选后，关闭时不读明细", async () => {
  const a = app({ name: "A", path: "/Applications/A.app", lastOpened: NOW });
  const b = app({ name: "B", path: "/Applications/B.app", lastOpened: 0 });
  const g = (appPath: string, bytes: number): GrowthApp => ({ key: appPath, name: appPath, kind: "app", orphan: false, appPath, size: 1, partial: false, ignored: false, day: null, week: { bytes, atLeast: false }, month: null, spark: [], folders: [] });
  for (const enabled of [false, true]) {
    let reads = 0;
    const stream = updateStream();
    const summary: GrowthSummary = { supported: true, config: { enabled, openAtLogin: false, dailyGB: 2, weeklyGB: 10, ignored: [], asked: true }, status: { running: false, lastAt: 0, nextAt: 0, fda: false, notifications: "unknown" }, alerts: [] };
    const h = await appsHarness({ listApps: async () => [a, b], startUpdateCheck: () => stream as any, growthSummary: async () => summary, growthState: async () => { reads++; return { ...summary, records: 1, apps: [g(a.path, 5), g(b.path, 20)] }; } });
    try {
      await h.flush({ active: true }); expect(reads).toBe(enabled ? 1 : 0);
      const sort = h.nodes().find((n) => n.props.name === "app-sort");
      expect(h.nodes(sort).some((n) => n.props.value === "growth")).toBe(enabled);
      if (enabled) { sort.props.onChange({ target: { value: "growth" } }); await h.flush(); expect(h.rows().map((n) => n.props.app.name)).toEqual(["B", "A"]); expect(h.expanded(b.id).week.bytes).toBe(20); }
      await h.flush({ filter: "idle" }); expect(h.rows().map((n) => n.props.app.name)).toEqual(["B"]);
      h.nodes().find((n) => n.props.name === "app-search").props.onChange({ target: { value: "A.app" } }); await h.flush(); expect(h.rows()).toHaveLength(0);
    } finally { h.dispose(); localStorage.setItem("cleanzo.appSort", "name"); }
  }
});

test("占用读取失败保留错误；卸载可重试分析，关闭后丢弃晚到的对话框结果", async () => {
  const a = app({ name: "A", path: "/Applications/A.app" });
  const stream = updateStream();
  let calls = 0, resolve!: (d: UninstallDetail) => void;
  const h = await appsHarness({ listApps: async () => [a], startUpdateCheck: () => stream as any, uninstallDetail: async () => { if (++calls === 1) throw new Error("detail denied"); return await new Promise((yes) => { resolve = yes; }); } });
  try {
    await h.flush({ active: true }); h.rows()[0].props.onToggle(a.id); await h.flush(); expect(h.expanded(a.id).usageError).toBe("detail denied");
    h.rows()[0].props.onToggle(a.id); await h.flush(); h.rows()[0].props.onToggle(a.id); await h.flush(); expect(calls).toBe(1);
    h.expanded(a.id).onUninstall(a.id); await h.flush(); expect(h.dialog().stage).toBe("analyzing"); expect(calls).toBe(2);
    h.dialog().onOpenChange(false); resolve({ app: a, items: [], totalSize: 0 }); await h.flush(); expect(h.dialog().open).toBe(false); expect(h.expanded(a.id).usageError).toBeNull();
  } finally { h.dispose(); }
});

test("卸载确认每次重读详情：读到前显示分析中，读到后展示当下的文件并更新占用", async () => {
  const a = app({ name: "A", path: "/Applications/A.app" });
  const stream = updateStream();
  let reads = 0, resolve!: (d: UninstallDetail) => void;
  const later: UninstallDetail = { app: a, items: [{ kind: "app", path: a.id, size: 800 }, { kind: "data", path: "/data", size: 300 }], totalSize: 1100 };
  const h = await appsHarness({ listApps: async () => [a], startUpdateCheck: () => stream as any, uninstallDetail: async () => {
    if (++reads === 1) return { app: a, items: [{ kind: "app", path: a.id, size: 800 }, { kind: "cache", path: "/cache", size: 200 }], totalSize: 1000 };
    return await new Promise((yes) => { resolve = yes; });
  } });
  try {
    await h.flush({ active: true }); stream.finish(); await h.flush();
    h.rows()[0].props.onToggle(a.id); await h.flush(); expect(reads).toBe(1); expect(h.expanded(a.id).usage.cache).toBe(200);
    h.expanded(a.id).onUninstall(a.id); await h.flush();
    expect(reads).toBe(2); expect(h.dialog().open).toBe(true); expect(h.dialog().stage).toBe("analyzing"); expect(h.dialog().detail).toBeNull();
    resolve(later); await h.flush();
    expect(h.dialog().stage).toBe("detail"); expect(h.dialog().detail.items).toEqual(later.items); expect(h.dialog().detail.totalSize).toBe(1100);
    expect(h.expanded(a.id).usage).toEqual({ app: 800, data: 300, cache: 0, cachePaths: [] });
  } finally { h.dispose(); }
});

test("详情晚于退出返回时，卸载确认仍使用退出后的运行状态", async () => {
  const a = app({ name: "A", path: "/Applications/A.app", running: true });
  const stream = updateStream();
  let reads = 0, resolve!: (d: UninstallDetail) => void;
  const h = await appsHarness({ listApps: async () => [a], startUpdateCheck: () => stream as any, uninstallDetail: async () => { reads++; return await new Promise((yes) => { resolve = yes; }); }, quitApp: async () => true });
  try {
    await h.flush({ active: true }); h.rows()[0].props.onToggle(a.id); await h.flush();
    h.expanded(a.id).onQuit(a.id); await h.flush(); h.nodes().find((n) => n.props.onClick && n.props.children === "退出").props.onClick(); await h.flush();
    expect(h.rows()[0].props.app.running).toBe(false);
    // 确认框打开时第一次读取还没回来：复用它，停在「分析中」，不另发一次。
    h.expanded(a.id).onUninstall(a.id); await h.flush(); expect(h.dialog().stage).toBe("analyzing"); expect(reads).toBe(1);
    resolve({ app: a, items: [], totalSize: 0 }); await h.flush();
    expect(h.dialog().detail.app.running).toBe(false); expect(h.rows()[0].props.app.running).toBe(false);
  } finally { h.dispose(); }
});

test("详情晚于升级返回时，卸载确认仍使用升级后的版本", async () => {
  const a = app({ name: "A", path: "/Applications/A.app", version: "1.170.0" });
  const stream = updateStream();
  let resolve!: (d: UninstallDetail) => void;
  const h = await appsHarness({ listApps: async () => [a], startUpdateCheck: () => stream as any, uninstallDetail: async () => await new Promise((yes) => { resolve = yes; }), upgradeApp: async (id) => ({ id, done: true, version: "1.171.0", code: "", detail: "", command: "" }) });
  try {
    await h.flush({ active: true }); stream.emit(upd({ id: a.id, state: "update", action: "brew" })); stream.finish(); await h.flush(); h.rows()[0].props.onToggle(a.id); await h.flush();
    h.rows()[0].props.onUpgrade(a.id); await h.flush(); expect(h.rows()[0].props.app.version).toBe("1.171.0");
    h.expanded(a.id).onUninstall(a.id); await h.flush(); expect(h.dialog().stage).toBe("analyzing");
    resolve({ app: a, items: [], totalSize: 0 }); await h.flush();
    expect(h.dialog().detail.app.version).toBe("1.171.0"); expect(h.rows()[0].props.app.version).toBe("1.171.0");
  } finally { h.dispose(); }
});

test("其他更新来源只打开目标；复制命令成功或失败都有反馈", async () => {
  const a = app({ name: "A", path: "/Applications/A.app" }), b = app({ name: "B", path: "/Applications/B.app" });
  const stream = updateStream();
  const ids: string[] = [], copied: string[] = [];
  const savedNavigator = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  let denied = false;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { clipboard: { async writeText(text: string) { if (denied) throw new Error("denied"); copied.push(text); } } } });
  const h = await appsHarness({ listApps: async () => [a, b], startUpdateCheck: () => stream as any, upgradeApp: async (id) => { ids.push(id); return { id, done: true, version: "", code: "", detail: "", command: "" }; } });
  try {
    await h.flush({ active: true });
    stream.emit(upd({ id: a.id, state: "update", action: "open-store" })); stream.emit(upd({ id: b.id, state: "update", action: "open-app" })); stream.finish(); await h.flush();
    h.rows()[0].props.onUpgrade(a.id); await h.flush(); expect(h.messages.at(-1)).toBe("已打开 App Store"); expect(h.counts.updates).toBe(2);
    h.rows()[1].props.onUpgrade(b.id); await h.flush(); expect(h.messages.at(-1)).toContain("已打开「B」，请在它的菜单里选择"); expect(ids).toEqual([a.id, b.id]); expect(h.rows().every((n) => !n.props.brewBusy)).toBe(true);
    const command = "brew upgrade --cask visual-studio-code";
    h.expanded(a.id).onCopy(command); await h.flush(); expect(copied).toEqual([command]); expect(h.messages.at(-1)).toBe("已复制");
    denied = true; h.expanded(a.id).onCopy(command); await h.flush(); expect(h.messages.at(-1)).toBe("复制失败"); expect(copied).toHaveLength(1);
  } finally { h.dispose(); if (savedNavigator) Object.defineProperty(globalThis, "navigator", savedNavigator); else delete (globalThis as any).navigator; }
});

test("缓存清理失败不会清掉占用；全是 in-use、其他失败和异常分别反馈", async () => {
  const a = app({ name: "A", path: "/Applications/A.app" });
  const stream = updateStream();
  let response = uninstall({ failed: ["/cache: in-use"] }), rejects = false;
  const h = await appsHarness({ listApps: async () => [a], startUpdateCheck: () => stream as any, uninstallDetail: async () => ({ app: a, items: [{ kind: "cache", path: "/cache", size: 1024 }], totalSize: 1024 }), cleanAppCache: async () => { if (rejects) throw new Error("denied"); return response; } });
  try {
    await h.flush({ active: true }); stream.finish(); await h.flush(); h.rows()[0].props.onToggle(a.id); await h.flush();
    for (const mode of ["in-use", "failed", "exception"]) {
      response = uninstall({ failed: [mode === "in-use" ? "/cache: in-use" : "/cache: denied"] }); rejects = mode === "exception";
      h.expanded(a.id).onCleanCache(a.id); await h.flush(); expect(h.messages.at(-1)).toContain(mode === "in-use" ? "「A」正在运行，请先退出再清理缓存" : "有 1 项缓存未能清理"); expect(h.expanded(a.id).usage.cache).toBe(1024); expect(h.expanded(a.id).usage.cachePaths).toEqual(["/cache"]); expect(h.busy).toBe(false);
    }
  } finally { h.dispose(); }
});

test("占用检查没能完成：卸载和清缓存都说没有移动任何东西，不报「有 1 项未能删除」", async () => {
  const a = app({ name: "A", path: "/Applications/A.app" });
  const stream = updateStream();
  const { t } = await import("../src/i18n");
  const h = await appsHarness({
    listApps: async () => [a], startUpdateCheck: () => stream as any,
    uninstallDetail: async () => ({ app: a, items: [{ kind: "app", path: a.id, size: 800 }, { kind: "cache", path: "/cache", size: 200 }], totalSize: 1000 }),
    uninstallApp: async (id) => uninstall({ path: id, failed: [`${id}: usage-unknown`] }),
    cleanAppCache: async (id) => uninstall({ path: id, failed: [`${id}: usage-unknown`] }),
  });
  try {
    await h.flush({ active: true }); stream.finish(); await h.flush(); h.rows()[0].props.onToggle(a.id); await h.flush();
    h.expanded(a.id).onUninstall(a.id); await h.flush(); h.dialog().onConfirm(); await h.flush();
    expect(h.messages.at(-1)).toBe(t("apps.fail", { msg: t("fs.usageUnknown") }));
    expect(h.rows()).toHaveLength(1);
    h.expanded(a.id).onCleanCache(a.id); await h.flush();
    expect(h.messages.at(-1)).toBe(t("fs.usageUnknown"));
    expect(h.expanded(a.id).usage.cache).toBe(200);
  } finally { h.dispose(); }
});

test("加载失败可重试；空更新在完成前显示进度，完成后显示最新，闲置为空显示对应文案", async () => {
  const a = app({ name: "A", path: "/Applications/A.app", lastOpened: Date.now() / 1000 });
  const stream = updateStream();
  let loads = 0;
  const h = await appsHarness({ listApps: async () => { if (++loads === 1) throw new Error("list denied"); return [a]; }, startUpdateCheck: () => stream as any });
  try {
    await h.flush({ active: true, filter: "updates" }); expect(h.nodes().some((n) => n.props.role === "alert")).toBe(true);
    h.nodes().find((n) => n.props.className?.includes("retry-btn")).props.onClick(); await h.flush(); expect(loads).toBe(2); expect(h.checking).toBe(true); expect(h.nodes().some((n) => n.props.children === "还在检查（0/1）")).toBe(true);
    stream.finish(); await h.flush(); expect(h.nodes().some((n) => n.props.children === "都是最新的")).toBe(true); expect(h.counts).toEqual({ total: 1, updates: 0 });
    await h.flush({ filter: "idle" }); expect(h.nodes().some((n) => n.props.children === "没有很久没用的 App")).toBe(true);
  } finally { h.dispose(); }
});

// 无浏览器的 effect 测试：手动驱动 LeftoversTab 的 hooks，注入受控的流式扫描结果，
// 校验 tab 角标 onCountChange 在「扫描失败 / 扫描成功」两种终态下的上报值。
async function leftoversHarness() {
  await import("./setup-dom");
  const React = await import("react");
  const { LeftoversTab } = await import("../src/react/apps/LeftoversTab");
  const { appsApi } = await import("../src/react/apps/api");
  const { setPref } = await import("../src/i18n");
  setPref("zh");

  const previous = {
    document: globalThis.document, window: globalThis.window, storage: globalThis.localStorage,
    raf: globalThis.requestAnimationFrame, cancelRaf: globalThis.cancelAnimationFrame, css: globalThis.CSS,
  };
  const store = new Map<string, string>([["cleanzo.lang", "zh"]]);
  Object.assign(globalThis, {
    window: {
      addEventListener() {}, removeEventListener() {}, dispatchEvent() {},
      matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
      setTimeout, clearTimeout,
    },
    localStorage: { getItem: (k: string) => (store.has(k) ? store.get(k)! : null), setItem: (k: string, v: string) => { store.set(k, v); } },
    requestAnimationFrame: (fn: FrameRequestCallback) => setTimeout(() => fn(0), 0),
    cancelAnimationFrame: (id: number) => clearTimeout(id),
    CSS: { escape: (k: string) => k },
  });

  const slots: { value?: unknown; deps?: readonly unknown[]; cleanup?: () => void }[] = [];
  const pending: (() => void)[] = [];
  let cursor = 0;
  let dirty = true;
  let tree: React.ReactNode;
  const counts: (number | null)[] = [];
  let scan: { evs: unknown[]; fail: Error | null } = { evs: [], fail: null };
  const changed = (b: readonly unknown[] | undefined, a: readonly unknown[] | undefined) =>
    !b || !a || b.length !== a.length || b.some((v, i) => !Object.is(v, a[i]));

  const spies = [
    spyOn(React, "useState").mockImplementation((initial?: unknown) => {
      const slot = slots[cursor++] ??= { value: typeof initial === "function" ? (initial as () => unknown)() : initial };
      return [slot.value, (value: unknown) => {
        const next = typeof value === "function" ? (value as (p: unknown) => unknown)(slot.value) : value;
        if (!Object.is(next, slot.value)) { slot.value = next; dirty = true; }
      }] as never;
    }),
    spyOn(React, "useRef").mockImplementation((initial: unknown) => {
      const slot = slots[cursor++] ??= { value: { current: initial } };
      return slot.value as never;
    }),
    spyOn(React, "useCallback").mockImplementation((fn: unknown, deps: readonly unknown[]) => {
      const slot = slots[cursor++] ??= {};
      if (changed(slot.deps, deps)) { slot.value = fn; slot.deps = deps; }
      return slot.value as never;
    }),
    spyOn(React, "useEffect").mockImplementation((effect, deps) => {
      const slot = slots[cursor++] ??= {};
      if (changed(slot.deps, deps)) {
        slot.deps = deps;
        pending.push(() => { slot.cleanup?.(); slot.cleanup = effect() || undefined; });
      }
    }),
    spyOn(React, "useSyncExternalStore").mockImplementation(<T>(_subscribe: unknown, snapshot: () => T) => snapshot()),
    spyOn(appsApi, "startOrphanScan").mockImplementation(() => {
      let i = 0;
      const evs = scan.evs;
      const ch = {
        [Symbol.asyncIterator]() {
          return { next: () => {
            if (i < evs.length) return Promise.resolve({ value: evs[i++], done: false });
            return Promise.resolve({ value: undefined, done: true });
          } };
        },
      };
      const done = scan.fail ? Promise.reject(scan.fail) : Promise.resolve();
      if (scan.fail) done.catch(() => {}); // 组件内部会 await 并接住；这里仅防无处理器告警
      return { ch: ch as never, done };
    }),
  ];

  type Props = { children?: React.ReactNode; [prop: string]: unknown };
  function nodes(node: React.ReactNode): React.ReactElement<Props>[] {
    if (Array.isArray(node)) return node.flatMap(nodes);
    if (!React.isValidElement<Props>(node)) return [];
    return [node, ...nodes(node.props.children)];
  }
  const textOf = (node: React.ReactNode): string => Array.isArray(node) ? node.map(textOf).join("") :
    React.isValidElement<{ children?: React.ReactNode }>(node) ? textOf(node.props.children) :
    typeof node === "string" || typeof node === "number" ? String(node) : "";

  return {
    counts,
    configure(setup: { evs?: unknown[]; fail?: Error | null }) { scan = { evs: setup.evs ?? [], fail: setup.fail ?? null }; },
    async settle() {
      for (let i = 0; i < 40; i++) {
        if (dirty) {
          dirty = false;
          cursor = 0;
          tree = LeftoversTab({ active: true, onCountChange: (n: number | null) => counts.push(n), onBusyChange() {} });
          for (const effect of pending.splice(0)) effect();
        }
        await Promise.resolve();
      }
    },
    text() { return textOf(tree); },
    hasAlert() { return nodes(tree).some((el) => el.props.role === "alert"); },
    close() {
      for (const slot of slots) slot.cleanup?.();
      for (const spy of spies) spy.mockRestore();
      Object.assign(globalThis, {
        document: previous.document, window: previous.window, localStorage: previous.storage,
        requestAnimationFrame: previous.raf, cancelAnimationFrame: previous.cancelRaf, CSS: previous.css,
      });
    },
  };
}

test("残留扫描失败：tab 角标回退为未知 null（不用 0 误导），内容区给 role=alert", async () => {
  const h = await leftoversHarness();
  try {
    h.configure({ fail: new Error("disk denied") });
    await h.settle();
    expect(h.counts.at(-1)).toBeNull();
    expect(h.hasAlert()).toBe(true);
  } finally { h.close(); }
});

test("残留扫描因占用检查没能完成而失败：说这次没有扫描，不提应用、不说移动", async () => {
  const { t } = await import("../src/i18n");
  const h = await leftoversHarness();
  try {
    h.configure({ fail: new Error("usage-unknown") });
    await h.settle();
    expect(h.hasAlert()).toBe(true);
    expect(h.text()).toContain(t("orphans.usageUnknown"));
    expect(h.text()).not.toContain(t("fs.usageUnknown"));
  } finally { h.close(); }
});

test("残留扫描成功：tab 角标实时为残留数量", async () => {
  const h = await leftoversHarness();
  try {
    h.configure({ evs: [
      orphan({ path: "/a", size: 10 }),
      orphan({ path: "/b", size: 20 }),
      { kind: "__done", path: "900", size: 30, trashable: false, isDir: false },
    ] });
    await h.settle();
    expect(h.counts.at(-1)).toBe(2);
    expect(h.hasAlert()).toBe(false);
  } finally { h.close(); }
});

// ---- 残留页接线：展开文件夹与流式扫描（页面 hooks 由 page-harness 驱动） ----

function deferredOf<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

/** 搭好残留页（中文）：改动都记在 env 里，由测试的 finally 还原。 */
async function leftoversPage(env: Changes, overrides: Record<string, unknown> = {}) {
  const { fakeStream } = await import("./page-harness");
  const { LeftoversTab } = await import("../src/react/apps/LeftoversTab");
  const { appsApi } = await import("../src/react/apps/api");
  env.lang("zh");
  const stream = fakeStream<OrphanItem>();
  env.assign(appsApi, { startOrphanScan: () => stream, ...overrides });
  const h = env.page(LeftoversTab, { active: true, onCountChange() {}, onBusyChange() {} });
  await h.settle();
  return {
    h, stream,
    rows: () => h.find((n) => n.type?.name === "ListRows")!.props,
  };
}

test("残留：收起后才到的子项列表不再填回这一行，也不把子项勾上", async () => {
  const listing = deferredOf<unknown>();
  const env = (await import("./page-harness")).changes();
  try {
    const p = await leftoversPage(env, { dirChildren: () => listing.promise });
    p.stream.emit(orphan({ path: "/u/Library/Old", size: 4096, isDir: true }));
    p.stream.finish();
    await p.h.settle();
    p.rows().onToggleRow("/u/Library/Old", true);
    p.rows().onExpand("/u/Library/Old");
    await p.h.settle();
    p.rows().onExpand("/u/Library/Old");
    await p.h.settle();
    listing.resolve({ items: [{ name: "a", path: "/u/Library/Old/a", size: 10, isDir: false, trashable: true }], total: 1 });
    await p.h.settle();
    const st = p.rows().selection;
    expect(st.expanded.has("/u/Library/Old")).toBe(false);
    expect(st.children.has("/u/Library/Old")).toBe(false);
    expect([...st.sel]).toEqual(["/u/Library/Old"]);
  } finally { env.restore(); }
});

test("残留：展开后量出大小未知的子文件夹，并按大小重排", async () => {
  const measured = deferredOf<number>();
  const asked: string[] = [];
  const env = (await import("./page-harness")).changes();
  try {
    const p = await leftoversPage(env, {
      dirChildren: async () => ({ items: [
        { name: "small", path: "/u/Library/Old/small", size: 1024, isDir: false, trashable: true },
        { name: "slow", path: "/u/Library/Old/slow", size: -1, isDir: true, trashable: true },
      ], total: 2 }),
      dirSize: (path: string) => { asked.push(path); return measured.promise; },
    });
    p.stream.emit(orphan({ path: "/u/Library/Old", size: 8192, isDir: true }));
    p.stream.finish();
    await p.h.settle();
    p.rows().onExpand("/u/Library/Old");
    await p.h.settle();
    expect(asked).toEqual(["/u/Library/Old/slow"]);
    const listed = () => p.rows().selection.children.get("/u/Library/Old").items as { path: string; size: number; measuring?: boolean }[];
    expect(listed().find((it) => it.path.endsWith("slow"))?.measuring).toBe(true);
    measured.resolve(4096);
    await p.h.settle();
    expect(listed().map((it) => [it.path, it.size, !!it.measuring])).toEqual([
      ["/u/Library/Old/slow", 4096, false], ["/u/Library/Old/small", 1024, false],
    ]);
  } finally { env.restore(); }
});

test("残留扫描在通道还开着时失败：显示错误，不留下未处理的 rejection", async () => {
  const { changes, textOf } = await import("./page-harness");
  const env = changes();
  try {
    const p = await leftoversPage(env);
    p.stream.fail(new Error("leftovers denied"));
    await p.h.settle();
    p.stream.end();
    await p.h.settle();
    const alert = p.h.find((n) => n.props.role === "alert");
    expect(alert && textOf(alert)).toContain("leftovers denied");
  } finally { env.restore(); }
});
