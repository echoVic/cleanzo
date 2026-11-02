import { expect, test } from "bun:test";
import type { AppInfo, AppUpdate, GrowthApp, GrowthSummary, OrphanItem, UninstallDetail, UninstallResult } from "../src/mygo";
import {
  barWidth, bundleFailed, classifyUninstall, filterApps, orphanFootAfterRemoval,
  parseDoneEv, readAppSort, sortApps, sortOrphans,
} from "../src/react/apps/helpers";
import { appsRequest, requestAppsTab, requestAppsUninstall } from "../src/react/apps/requests";
import {
  filterByKind, growthFor, growthTagFor, isIdle, reasonKey, sourceKey, splitUsage, updateCount, versionTag,
} from "../src/react/apps/updates";

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
    const mac = renderToStaticMarkup(createElement(AppsPage, { initialTab: "leftovers", nonce: 0 }));
    expect(mac).toContain('content-leftovers');
    setPlatformForTest("win32");
    const win = renderToStaticMarkup(createElement(AppsPage, { initialTab: "leftovers", nonce: 0 }));
    expect(win).not.toContain('content-leftovers');
    expect(win).toContain('content-installed');
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
  const { InstalledAppRow } = await import("../src/react/apps/InstalledTab");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref } = await import("../src/i18n");
  const a = app({ id: "hklm32:chosen", name: "Acme", path: "", publisher: "Acme Inc", version: "2.4", bundleId: "mac.bundle" });
  try {
    setPlatformForTest("win32");
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      let picked = "";
      const row = InstalledAppRow({ app: a, moving: false, onDetail: (id) => { picked = id; } });
      const html = renderToStaticMarkup(row);
      expect(html).toContain("Acme Inc");
      expect(html).toContain("2.4");
      expect(html).not.toContain("mac.bundle");
      const children = row.props.children as any[];
      children[children.length - 1].props.onClick();
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
      const html = renderToStaticMarkup(createElement(AppsPage, { initialTab: "installed", nonce: 0 }));
      expect(html).toContain(t("apps.sub"));
      expect(t("apps.sub")).toContain(lang === "zh" ? "卸载器" : "uninstaller");
      expect(html).toContain('href="ms-settings:appsfeatures"');
      expect(html).toContain(t("apps.store"));
      expect(html).not.toContain(lang === "zh" ? "关联文件" : "related files");
    }
    setPlatformForTest("darwin");
    expect(renderToStaticMarkup(createElement(AppsPage, { initialTab: "installed", nonce: 0 }))).not.toContain("ms-settings:");
  } finally { setPlatformForTest(undefined); setPref("system"); }
});

test("Windows app rows omit unknown last-opened metadata without an empty line", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { InstalledAppRow } = await import("../src/react/apps/InstalledTab");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref } = await import("../src/i18n");
  const a = app({ name: "Acme", path: "", size: 1024, lastOpened: 0 });
  const render = () => renderToStaticMarkup(createElement(InstalledAppRow, { app: a, moving: false, onDetail() {} }));
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
  const { InstalledTab } = await import("../src/react/apps/InstalledTab");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref, t } = await import("../src/i18n");
  const savedSort = localStorage.getItem("cleanzo.appSort");
  const render = () => renderToStaticMarkup(createElement(InstalledTab, { active: false, onBusyChange() {} }));
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

test("增长页请求卸载：切到已安装、记下要打开的 App，两个 nonce 都递增", () => {
  requestAppsTab("leftovers");
  const before = appsRequest();
  requestAppsUninstall("/Applications/Lark.app");
  const after = appsRequest();
  expect(after.tab).toBe("installed");
  expect(after.tabNonce).toBe(before.tabNonce + 1);
  expect(after.uninstallPath).toBe("/Applications/Lark.app");
  expect(after.uninstallNonce).toBe(before.uninstallNonce + 1);
});

test("installed app row shows the app's icon once known", async () => {
  await import("./setup-dom");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { InstalledAppRow } = await import("../src/react/apps/InstalledTab");
  const { loadIcon, resetIconsForTest } = await import("../src/react/icons/cache");
  const a = app({ id: "acme", name: "Acme", path: "/Applications/Acme.app" });
  resetIconsForTest(async () => "data:image/png;base64,QUJD");
  try {
    expect(renderToStaticMarkup(InstalledAppRow({ app: a, moving: false, onDetail() {} }))).toContain('<div class="app-icon">A</div>');
    await loadIcon("/Applications/Acme.app");
    expect(renderToStaticMarkup(InstalledAppRow({ app: a, moving: false, onDetail() {} }))).toContain('class="app-icon cz-app-img"');
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
  const g = { key: "app:company.thebrowser.Browser", appPath: "/Applications/Arc.app", week: { bytes: 3 * 2 ** 30, atLeast: false, days: 7 } } as GrowthApp;
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
    usage: { app: 800, data: 2000, cache: 400, cachePaths: ["/c"] }, usageError: null, week: { bytes: 3 * 2 ** 30, atLeast: false, days: 7 }, month: null,
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
