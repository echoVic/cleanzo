import { expect, test } from "bun:test";
import type { AppInfo, OrphanItem, UninstallResult } from "../src/mygo";
import {
  barWidth, bundleFailed, classifyUninstall, filterApps, orphanFootAfterRemoval,
  parseDoneEv, sortApps, sortOrphans,
} from "../src/react/apps/helpers";

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
