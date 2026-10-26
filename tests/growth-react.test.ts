import { expect, spyOn, test } from "bun:test";
import type { GrowthApp, GrowthState, UninstallDetail } from "../src/mygo";
import { t } from "../src/i18n";

const storage = new Map([["cleanzo.lang", "zh"]]);
Object.assign(globalThis, {
  document: { querySelectorAll: () => [] },
  localStorage: { getItem: (k: string) => storage.get(k), setItem: (k: string, v: string) => storage.set(k, v) },
});

const { fmtDelta, growthBanner, growthView, sortApps, sparkPath, uninstallGap } = await import("../src/react/growth/format");

const GB = 2 ** 30;

function app(p: Partial<GrowthApp>): GrowthApp {
  return { key: "app:x", name: "X", kind: "app", orphan: false, appPath: "", size: 0, partial: false, ignored: false,
    day: null, week: null, month: null, spark: [], folders: [], ...p };
}

function state(p: Partial<GrowthState>): GrowthState {
  return {
    supported: true, records: 0, apps: [], alerts: [],
    config: { enabled: false, openAtLogin: false, dailyGB: 2, weeklyGB: 10, ignored: [], asked: false },
    status: { running: false, lastAt: 0, nextAt: 0, fda: true, notifications: "unknown" }, ...p,
  };
}

test("页面状态：不支持 / 没有记录 / 只有一次 / 列表", () => {
  expect(growthView(null)).toBe("loading");
  expect(growthView(state({ supported: false }))).toBe("unsupported");
  expect(growthView(state({ records: 0 }))).toBe("empty");
  expect(growthView(state({ records: 1 }))).toBe("baseline");
  expect(growthView(state({ records: 3 }))).toBe("list");
});

test("横幅：没开过就询问，问过没开就提示，开着不显示", () => {
  expect(growthBanner(state({}))).toBe("optin");
  expect(growthBanner(state({ config: { ...state({}).config, asked: true } }))).toBe("off");
  expect(growthBanner(state({ config: { ...state({}).config, enabled: true } }))).toBeNull();
});

test("增长文案：正负、下限、没有可比记录", () => {
  const atLeast = (s: string) => t("growth.atLeast", { size: s });
  expect(fmtDelta({ bytes: 5.6 * GB, atLeast: false, days: 1 }, atLeast)).toBe("+5.6 GB");
  expect(fmtDelta({ bytes: -GB, atLeast: false, days: 1 }, atLeast)).toBe("−1.0 GB");
  expect(fmtDelta({ bytes: 3 * GB, atLeast: true, days: 1 }, atLeast)).toBe("至少 +3.0 GB");
  expect(fmtDelta(null, atLeast)).toBe("—");
});

test("排序：按 7 天增长时没有数据的排后面，再按占用", () => {
  const apps = [app({ key: "a", size: 1, week: { bytes: GB, atLeast: false, days: 7 } }), app({ key: "b", size: 9 }),
    app({ key: "c", size: 2, week: { bytes: 5 * GB, atLeast: false, days: 7 } }), app({ key: "d", size: 5 })];
  expect(sortApps(apps, "week").map((a) => a.key)).toEqual(["c", "a", "b", "d"]);
  expect(sortApps(apps, "size").map((a) => a.key)).toEqual(["b", "d", "c", "a"]);
  expect(apps[0].key).toBe("a"); // 不改原数组
});

test("迷你曲线：-1 处断开，少于两个点不画", () => {
  expect(sparkPath([-1, 5, -1], 100, 20)).toBe("");
  expect(sparkPath([0, 10, -1, 10], 30, 10)).toBe("M0.0 10.0L10.0 0.0M30.0 0.0");
});

test("卸载能移走多少、另外剩多少（不算 App 本身）", () => {
  const detail = { app: {} as UninstallDetail["app"], totalSize: 4 * GB,
    items: [{ path: "/A.app", kind: "app", size: GB }, { path: "/c", kind: "cache", size: 3 * GB }] } as UninstallDetail;
  expect(uninstallGap(30 * GB, detail)).toEqual({ freed: 4 * GB, rest: 27 * GB });
  expect(uninstallGap(GB, detail)).toEqual({ freed: 4 * GB, rest: 0 });
  expect(uninstallGap(GB, null)).toBeNull();
});

test("新文案中英文都有", () => {
  for (const key of ["nav.growth", "growth.sub", "growth.optin.body", "growth.uninstallGap", "growth.outside", "growth.badge.unknown"] as const) {
    storage.set("cleanzo.lang", "en");
    expect(t(key)).not.toBe(key);
    storage.set("cleanzo.lang", "zh");
    expect(t(key)).not.toBe(key);
  }
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

// 无浏览器的 effect 测试：保留 hook 槽位与依赖，执行页面实际的加载、定位和刷新回调。
async function pageHarness(focusKey: string | null = null) {
  const React = await import("react");
  const { GrowthPage } = await import("../src/react/growth/GrowthPage");
  const { growthApi } = await import("../src/react/growth/api");
  const slots: { value?: unknown; deps?: readonly unknown[]; cleanup?: () => void }[] = [];
  const pending: (() => void)[] = [];
  const frames = new Map<number, FrameRequestCallback>();
  const timers = new Map<number, () => void>();
  const requests: ReturnType<typeof deferred<GrowthState>>[] = [];
  const details: ReturnType<typeof deferred<UninstallDetail>>[] = [];
  let updated = () => {};
  let cursor = 0;
  let dirty = true;
  let tree: React.ReactNode;
  let scrolled = 0;
  let nextId = 0;
  const changed = (before: readonly unknown[] | undefined, after: readonly unknown[] | undefined) =>
    !before || !after || before.length !== after.length || before.some((v, i) => !Object.is(v, after[i]));
  const spies = [
    spyOn(React, "useState").mockImplementation((initial: unknown) => {
      const slot = slots[cursor++] ??= { value: typeof initial === "function" ? initial() : initial };
      return [slot.value, (value: unknown) => {
        const next = typeof value === "function" ? value(slot.value) : value;
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
    spyOn(React, "useSyncExternalStore").mockImplementation((_subscribe, snapshot) => snapshot()),
    spyOn(growthApi, "state").mockImplementation(() => {
      const request = deferred<GrowthState>(); requests.push(request); return request.promise;
    }),
    spyOn(growthApi, "uninstallDetail").mockImplementation(() => {
      const request = deferred<UninstallDetail>(); details.push(request); return request.promise;
    }),
    spyOn(growthApi, "onUpdated").mockImplementation((fn) => {
      updated = () => fn({ running: false, lastAt: 0, nextAt: 0, fda: true, notifications: "allowed" });
      return () => {};
    }),
  ];
  const previous = {
    document: globalThis.document, window: globalThis.window,
    raf: globalThis.requestAnimationFrame, cancelRaf: globalThis.cancelAnimationFrame, css: globalThis.CSS,
  };
  function nodes(node: React.ReactNode): React.ReactElement<Record<string, any>>[] {
    if (Array.isArray(node)) return node.flatMap(nodes);
    if (!React.isValidElement<Record<string, any>>(node)) return [];
    return [node, ...nodes(node.props.children)];
  }
  Object.assign(globalThis, {
    document: { querySelectorAll: () => [], querySelector: () =>
      nodes(tree).some((el) => el.props["data-growth-key"] === focusKey) ? { scrollIntoView: () => { scrolled++; } } : null },
    window: { setTimeout: (fn: () => void) => { timers.set(++nextId, fn); return nextId; }, clearTimeout: (id: number) => timers.delete(id) },
    requestAnimationFrame: (fn: FrameRequestCallback) => { frames.set(++nextId, fn); return nextId; },
    cancelAnimationFrame: (id: number) => frames.delete(id), CSS: { escape: (key: string) => key },
  });
  return {
    requests, details, timers,
    updated: () => updated(),
    get scrolled() { return scrolled; },
    async settle() {
      for (let i = 0; i < 10; i++) {
        if (dirty) {
          dirty = false; cursor = 0;
          tree = GrowthPage({ visit: 1, focusKey, focusNonce: 1, goLeftovers() {}, goUninstall() {} });
          for (const effect of pending.splice(0)) effect();
        }
        await Promise.resolve();
      }
    },
    frame() { for (const [id, fn] of [...frames]) { frames.delete(id); fn(0); } },
    openRow() { nodes(tree).find((el) => el.type === "button" && el.props.className === "cz-growth-row")!.props.onClick(); },
    text() {
      const text = (node: React.ReactNode): string => Array.isArray(node) ? node.map(text).join("") :
        React.isValidElement<{ children?: React.ReactNode }>(node) ? text(node.props.children) :
        typeof node === "string" || typeof node === "number" ? String(node) : "";
      return text(tree);
    },
    close() {
      for (const slot of slots) slot.cleanup?.();
      for (const spy of spies) spy.mockRestore();
      Object.assign(globalThis, { document: previous.document, window: previous.window,
        requestAnimationFrame: previous.raf, cancelAnimationFrame: previous.cancelRaf, CSS: previous.css });
    },
  };
}

test("首次通知定位等待目标行加载，再滚动并开始高亮", async () => {
  const page = await pageHarness("app:x");
  try {
    await page.settle();
    page.frame();
    expect(page.scrolled).toBe(0);
    expect(page.timers.size).toBe(0);
    page.requests[0]!.resolve(state({ records: 2, apps: [app({})] }));
    await page.settle();
    page.frame();
    expect(page.scrolled).toBe(1);
    expect(page.timers.size).toBe(1);
    page.updated();
    page.requests[1]!.resolve(state({ records: 3, apps: [app({ size: GB })] }));
    await page.settle();
    page.frame();
    expect(page.scrolled).toBe(1); // 同一次通知不因状态刷新重复定位。
    page.updated();
    page.requests[2]!.resolve(state({ records: 3, apps: [] }));
    await page.settle();
    page.updated();
    page.requests[3]!.resolve(state({ records: 3, apps: [app({})] }));
    await page.settle();
    page.frame();
    expect(page.scrolled).toBe(1); // 目标暂时消失后恢复，也不重放已处理的通知。
  } finally { page.close(); }
});

test("未知 App 的徽章不随 orphan 改变，orphan 只控制残留页签按钮", async () => {
  for (const orphan of [false, true]) {
    const page = await pageHarness();
    try {
      await page.settle();
      page.requests[0]!.resolve(state({ records: 2, apps: [app({ kind: "unknown", orphan })] }));
      await page.settle();
      page.openRow();
      await page.settle();
      expect(page.text()).toContain("未找到对应的 App");
      expect(page.text()).not.toContain("可能是残留");
      expect(page.text().includes(t("growth.goLeftovers"))).toBe(orphan);
    } finally { page.close(); }
  }
});

test("状态更新重新取卸载差额，过期请求不会盖掉新明细", async () => {
  const page = await pageHarness();
  const snapshot = (size: number, lastAt: number) => state({ records: 3, apps: [app({ size, appPath: "/A.app" })],
    status: { ...state({}).status, lastAt } });
  const detail = (cache: number): UninstallDetail => ({ app: {} as UninstallDetail["app"], totalSize: GB + cache,
    items: [{ path: "/A.app", kind: "app", size: GB }, { path: "/c", kind: "cache", size: cache }] });
  try {
    await page.settle();
    page.requests[0]!.resolve(snapshot(10 * GB, 1));
    await page.settle();
    page.openRow();
    await page.settle();
    page.details[0]!.resolve(detail(2 * GB));
    await page.settle();
    expect(page.text()).toContain("卸载能移走 3.0 GB；另有 8.0 GB");
    page.updated();
    page.requests[1]!.resolve(snapshot(20 * GB, 2));
    await page.settle();
    expect(page.details.length).toBe(2);
    page.updated();
    page.requests[2]!.resolve(snapshot(30 * GB, 3));
    await page.settle();
    expect(page.details.length).toBe(3);
    page.details[2]!.resolve(detail(4 * GB));
    await page.settle();
    expect(page.text()).toContain("卸载能移走 5.0 GB；另有 26.0 GB");
    page.details[1]!.resolve(detail(3 * GB));
    await page.settle();
    expect(page.text()).toContain("卸载能移走 5.0 GB；另有 26.0 GB");
  } finally { page.close(); }
});
