import { expect, spyOn, test } from "bun:test";
import type { AppInfo, OrphanItem, UninstallResult } from "../src/mygo";
import {
  barWidth, bundleFailed, classifyUninstall, filterApps, orphanFootAfterRemoval,
  parseDoneEv, sortApps, sortOrphans,
} from "../src/react/apps/helpers";
import { appsRequest, requestAppsTab, requestAppsUninstall } from "../src/react/apps/requests";

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
    spyOn(React, "useState").mockImplementation((initial: unknown) => {
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
    spyOn(React, "useSyncExternalStore").mockImplementation((_subscribe: unknown, snapshot: () => unknown) => snapshot()),
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

  function nodes(node: React.ReactNode): React.ReactElement<Record<string, unknown>>[] {
    if (Array.isArray(node)) return node.flatMap(nodes);
    if (!React.isValidElement<Record<string, unknown>>(node)) return [];
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
