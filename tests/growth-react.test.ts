import { expect, spyOn, test } from "bun:test";
import type { GrowthApp, GrowthConfig, GrowthConfigPatch, GrowthState, UninstallDetail } from "../src/mygo";
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
  const { GrowthPanel } = await import("../src/react/growth/GrowthPage");
  const { growthApi } = await import("../src/react/growth/api");
  const slots: { value?: unknown; deps?: readonly unknown[]; cleanup?: () => void }[] = [];
  const pending: (() => void)[] = [];
  const frames = new Map<number, FrameRequestCallback>();
  const timers = new Map<number, () => void>();
  const requests: ReturnType<typeof deferred<GrowthState>>[] = [];
  const details: ReturnType<typeof deferred<UninstallDetail>>[] = [];
  const saves: GrowthConfigPatch[] = [];
  let updated = () => {};
  let cursor = 0;
  let dirty = true;
  let tree: React.ReactNode;
  let scrolled = 0;
  let nextId = 0;
  const shown = { active: true, visit: 1 };
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
      return () => { updated = () => {}; };
    }),
    spyOn(growthApi, "saveConfig").mockImplementation(async (patch) => { saves.push(patch); }),
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
  /** 一个节点里所有的文字（含按钮）。 */
  const textOf = (node: React.ReactNode): string => Array.isArray(node) ? node.map(textOf).join("") :
    React.isValidElement<{ children?: React.ReactNode }>(node) ? textOf(node.props.children) :
    typeof node === "string" || typeof node === "number" ? String(node) : "";
  Object.assign(globalThis, {
    document: { querySelectorAll: () => [], querySelector: () =>
      nodes(tree).some((el) => el.props["data-growth-key"] === focusKey) ? { scrollIntoView: () => { scrolled++; } } : null },
    window: { setTimeout: (fn: () => void) => { timers.set(++nextId, fn); return nextId; }, clearTimeout: (id: number) => timers.delete(id) },
    requestAnimationFrame: (fn: FrameRequestCallback) => { frames.set(++nextId, fn); return nextId; },
    cancelAnimationFrame: (id: number) => frames.delete(id), CSS: { escape: (key: string) => key },
  });
  return {
    requests, details, timers, saves,
    updated: () => updated(),
    /** 各行字母头像上的字。 */
    icons() { return nodes(tree).filter((el) => el.props.className === "cz-growth-icon").map((el) => el.props.children); },
    /** 「现在量一次」的图标是否在转。 */
    spinning() { return nodes(tree).some((el) => el.props.className === "animate-spin"); },
    /** 空状态引导的按钮：文字和是否在转；没有空状态时为 null。 */
    guide() {
      const el = nodes(tree).find((node) => "ctaText" in node.props);
      return el ? { ctaText: el.props.ctaText, busy: !!el.props.busy } : null;
    },
    /** main.ts 切换视图：进入本视图时 visit 递增。 */
    show(active: boolean) { if (active) shown.visit++; shown.active = active; dirty = true; },
    /** 点文字为 label 的按钮。 */
    click(label: string) { nodes(tree).find((el) => el.props.children === label && el.props.onClick)!.props.onClick(); },
    get scrolled() { return scrolled; },
    async settle() {
      for (let i = 0; i < 10; i++) {
        if (dirty) {
          dirty = false; cursor = 0;
          tree = GrowthPanel({ ...shown, focusKey, focusNonce: 1, goLeftovers() {}, goUninstall() {} });
          for (const effect of pending.splice(0)) effect();
        }
        await Promise.resolve();
      }
    },
    frame() { for (const [id, fn] of [...frames]) { frames.delete(id); fn(0); } },
    openRow() { nodes(tree).find((el) => el.type === "button" && el.props.className === "cz-growth-row")!.props.onClick(); },
    text() { return textOf(tree); },
    /** 有没有 className 正好是 cls 的元素。 */
    has(cls: string) { return nodes(tree).some((el) => el.props.className === cls); },
    /** 状态那一行的文字（含「现在量一次」）。 */
    statusLine() {
      const el = nodes(tree).find((n) => n.props.className === "cz-growth-statusline");
      return el ? textOf(el) : "";
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

import { growthHint } from "../src/react/growth/hint";

test("状态页提示：监控开着时取最大的一条；崩溃转储提示已覆盖的同一 App 不重复", () => {
  const alert = (name: string, bytes: number, folder: string) =>
    ({ key: "app:" + name, name, window: "day", days: 1, bytes, atLeast: false, folder, total: bytes, at: 1 });
  const on = { ...state({}).config, enabled: true };
  const s = state({ config: on, alerts: [alert("ToDesk", 5 * GB, "/Library/Application Support/ToDesk/dumps"),
    alert("Codex", 40 * GB, "/Users/u/Library/Application Support/Codex/Crashpad")] });
  expect(growthHint(s, [])?.name).toBe("Codex");
  expect(growthHint(s, ["Codex"])?.name).toBe("ToDesk");
  expect(growthHint(state({ alerts: s.alerts }), [])).toBeNull(); // 监控关着
  expect(growthHint(null, [])).toBeNull();
});

test("状态页提示：最大的提醒已被忽略时返回下一条，全部忽略时不提示", () => {
  const alerts = [
    { key: "app:codex", name: "Codex", window: "day", days: 1, bytes: 40 * GB, atLeast: false, folder: "/Codex/cache", total: 40 * GB, at: 1 },
    { key: "app:todesk", name: "ToDesk", window: "week", days: 5, bytes: 5 * GB, atLeast: true, folder: "/ToDesk/dumps", total: 10 * GB, at: 1 },
  ];
  const config = { ...state({}).config, enabled: true, ignored: ["app:codex"] };
  const s = state({ config, alerts });
  expect(growthHint(s, [])).toBe(alerts[1]);
  expect(growthHint(state({ config: { ...config, ignored: ["app:codex", "app:todesk"] }, alerts }), [])).toBeNull();
  expect(s.alerts).toEqual(alerts); // 不改后端返回的提醒顺序。
});

test("状态页提示：不支持时不显示，Crashpad 去重忽略大小写并识别路径边界", () => {
  const alert = { key: "app:codex", name: "Codex", window: "day", days: 1, bytes: GB, atLeast: false,
    folder: "C:\\Codex\\crashpad\\reports", total: GB, at: 1 };
  const config = { ...state({}).config, enabled: true };
  expect(growthHint(state({ supported: false, config, alerts: [alert] }), [])).toBeNull();
  expect(growthHint(state({ config, alerts: [alert] }), ["CODEX"])).toBeNull();
  expect(growthHint(state({ config, alerts: [{ ...alert, folder: "/Codex/CrashpadBackup" }] }), ["Codex"])?.name).toBe("Codex");
  expect(growthHint(state({ config }), [])).toBeNull();
});

test("增长设置错误码：开机启动错误带名称，阈值错误有中英文文案", async () => {
  const { errText } = await import("../src/shared");
  try {
    for (const [lang, login, threshold] of [
      ["zh", "无法设置开机启动：系统拒绝", "提醒阈值无效"],
      ["en", "Couldn't set Open at Login: 系统拒绝", "Invalid alert threshold"],
    ] as const) {
      storage.set("cleanzo.lang", lang);
      expect(errText(new Error(JSON.stringify({ code: "growth-login", args: ["系统拒绝"] })))).toBe(login);
      expect(errText(JSON.stringify({ code: "growth-threshold" }))).toBe(threshold);
    }
  } finally { storage.set("cleanzo.lang", "zh"); }
});

// 执行设置页实际的 effect 和保存回调，验证后端回读、禁用状态及失败回退。
// 后端合并补丁的方式（与 Go 的 GrowthConfigPatch.apply 一致）。
function mergePatch(c: GrowthConfig, p: GrowthConfigPatch): GrowthConfig {
  const key = (k: string | undefined) => (k ?? "").trim().toLowerCase();
  let ignored = c.ignored;
  if (key(p.ignore) && !ignored.includes(key(p.ignore))) ignored = [...ignored, key(p.ignore)];
  ignored = ignored.filter((k) => k !== key(p.unignore));
  const enabled = p.enabled ?? c.enabled;
  return { enabled, openAtLogin: enabled && (p.openAtLogin ?? c.openAtLogin), asked: p.asked ?? c.asked,
    dailyGB: p.dailyGB ?? c.dailyGB, weeklyGB: p.weeklyGB ?? c.weeklyGB, ignored };
}

async function growthSettingsHarness(snapshot: GrowthState) {
  const React = await import("react");
  const { SettingsPage } = await import("../src/react/settings/SettingsPage");
  const { settingsApi } = await import("../src/react/settings/api");
  const { growthApi } = await import("../src/react/growth/api");
  const slots: { value?: unknown; deps?: readonly unknown[]; cleanup?: () => void }[] = [];
  const pending: (() => void)[] = [];
  const saved: GrowthConfigPatch[] = [];
  let cursor = 0;
  let dirty = true;
  let session = 1;
  let reads = 0;
  let tree: React.ReactNode;
  let saveError: Error | null = null;
  let saveApplied = false;
  let readError = false;
  let updated = () => {};
  let gate: ReturnType<typeof deferred<void>> | null = null;
  const toast = { textContent: "", className: "", classList: { remove() {} } };
  const previous = { document: globalThis.document, window: globalThis.window };
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
    spyOn(React, "useSyncExternalStore").mockImplementation((_subscribe, get) => get()),
    spyOn(settingsApi, "getSettings").mockResolvedValue({ disabledGroups: [], ignored: [], custom: [] }),
    spyOn(settingsApi, "info").mockResolvedValue({ version: "test" } as never),
    spyOn(settingsApi, "fullDiskAccess").mockResolvedValue(true),
    spyOn(settingsApi, "growthSummary").mockImplementation(async () => {
      reads++;
      if (readError) throw new Error("read failed");
      return snapshot;
    }),
    spyOn(settingsApi, "saveGrowthConfig").mockImplementation(async (patch) => {
      saved.push(patch);
      if (gate) await gate.promise;
      if (saveError && !saveApplied) throw saveError;
      snapshot = { ...snapshot, config: mergePatch(snapshot.config, patch) };
      if (saveError) throw saveError; // 开机启动出错：设置已保存并生效，错误最后返回
    }),
    spyOn(growthApi, "onUpdated").mockImplementation((fn) => {
      updated = () => fn(snapshot.status);
      return () => { updated = () => {}; };
    }),
  ];
  Object.assign(globalThis, {
    document: { querySelectorAll: () => [], querySelector: (selector: string) => selector === "#toast" ? toast : null },
    window: { addEventListener() {}, removeEventListener() {}, setTimeout: () => 0 },
  });
  function nodes(node: React.ReactNode): React.ReactElement<Record<string, any>>[] {
    if (Array.isArray(node)) return node.flatMap(nodes);
    if (!React.isValidElement<Record<string, any>>(node)) return [];
    return [node, ...nodes(node.props.children)];
  }
  return {
    saved, toast,
    get reads() { return reads; },
    checkbox(label: string) {
      const row = nodes(tree).find((el) => el.type === "label" && nodes(el).some((child) => child.type === "span" && child.props.children === label));
      return row ? nodes(row).find((el) => "onCheckedChange" in el.props)?.props : undefined;
    },
    select(id: string) { return nodes(tree).find((el) => el.type === "select" && el.props.id === id)?.props; },
    headings() { return nodes(tree).filter((el) => el.type === "h3").map((el) => el.props.children); },
    holdSave() { gate = deferred<void>(); return gate; },
    /** applied：像开机启动出错那样，设置已保存并生效后才返回错误。 */
    failSave(error: Error, applied = false) { saveError = error; saveApplied = applied; },
    failRead() { readError = true; },
    reenter(next: GrowthState) { snapshot = next; session++; dirty = true; },
    /** 别处（菜单栏、增长页）改了后端配置。 */
    backend(change: (s: GrowthState) => GrowthState) { snapshot = change(snapshot); },
    /** Go 广播 growthUpdated。 */
    updated: () => updated(),
    async settle() {
      for (let i = 0; i < 12; i++) {
        if (dirty) {
          dirty = false; cursor = 0;
          tree = SettingsPage({ callbacks: { changeLanguage: async () => {} }, session });
          for (const effect of pending.splice(0)) effect();
        }
        await Promise.resolve();
      }
    },
    close() {
      for (const slot of slots) slot.cleanup?.();
      for (const spy of spies) spy.mockRestore();
      Object.assign(globalThis, previous);
    },
  };
}

test("增长设置：关监控后回读后端的开机启动值，保存期间锁住控件", async () => {
  const config = { ...state({}).config, enabled: true, openAtLogin: true, ignored: ["app:codex"] };
  const page = await growthSettingsHarness(state({ config }));
  try {
    await page.settle();
    expect(page.headings().slice(0, 3)).toEqual([t("settings.general"), t("nav.clean"), t("settings.growth")]);
    expect(page.checkbox(t("settings.growth.login"))?.disabled).toBe(false);
    expect(page.select("growth-daily")?.children.map((option: any) => option.props.value)).toEqual([1, 2, 5, 10]);
    expect(page.select("growth-weekly")?.children.map((option: any) => option.props.value)).toEqual([5, 10, 20, 50]);
    const gate = page.holdSave();
    page.checkbox(t("settings.growth.enable"))!.onCheckedChange(false);
    await page.settle();
    expect(page.saved).toEqual([{ enabled: false, asked: true }]);
    expect(page.checkbox(t("settings.growth.enable"))?.disabled).toBe(true);
    expect(page.checkbox(t("settings.growth.login"))?.disabled).toBe(true);
    expect(page.select("growth-daily")?.disabled).toBe(true);
    gate.resolve();
    await page.settle();
    expect(page.reads).toBe(2);
    expect(page.checkbox(t("settings.growth.enable"))?.checked).toBe(false);
    expect(page.checkbox(t("settings.growth.enable"))?.disabled).toBe(false);
    expect(page.checkbox(t("settings.growth.login"))?.checked).toBe(false);
    expect(page.checkbox(t("settings.growth.login"))?.disabled).toBe(true);
  } finally { page.close(); }
});

test("增长设置：保存失败保留原值并通过 errText 显示错误 toast", async () => {
  for (const code of ["growth-login", "growth-threshold"]) {
    const page = await growthSettingsHarness(state({ config: { ...state({}).config, enabled: true } }));
    try {
      await page.settle();
      page.failSave(new Error(JSON.stringify({ code, args: ["拒绝"] })));
      if (code === "growth-login") page.checkbox(t("settings.growth.login"))!.onCheckedChange(true);
      else page.select("growth-daily")!.onChange({ target: { value: "5" } });
      await page.settle();
      expect(page.checkbox(t("settings.growth.login"))?.checked).toBe(false);
      expect(page.select("growth-daily")?.value).toBe(2);
      expect(page.select("growth-daily")?.disabled).toBe(false);
      expect(page.toast.textContent).toBe(code === "growth-login" ? "无法设置开机启动：拒绝" : "提醒阈值无效");
      expect(page.toast.className).toBe("toast show err");
      expect(page.reads).toBe(2); // 失败也回读一次
    } finally { page.close(); }
  }
});

test("增长设置：每次进入重新读取，不支持、Windows 或读取失败时隐藏", async () => {
  const { setPlatformForTest } = await import("../src/platform");
  const page = await growthSettingsHarness(state({}));
  try {
    await page.settle();
    expect(page.headings()).toContain(t("settings.growth"));
    page.reenter(state({ supported: false }));
    await page.settle();
    expect(page.headings()).not.toContain(t("settings.growth"));
    setPlatformForTest("win32");
    page.reenter(state({}));
    await page.settle();
    expect(page.headings()).not.toContain(t("settings.growth"));
    expect(page.reads).toBe(2);
    setPlatformForTest("darwin");
    page.failRead();
    page.reenter(state({}));
    await page.settle();
    expect(page.headings()).not.toContain(t("settings.growth"));
  } finally { page.close(); setPlatformForTest(undefined); }
});

test("增长设置：重新进入时等待配置刷新，改阈值不会覆盖刚忽略的 App", async () => {
  const config = { ...state({}).config, enabled: true };
  const fresh = { ...config, ignored: ["app:codex"] };
  const page = await growthSettingsHarness(state({ config }));
  try {
    await page.settle();
    const { settingsApi } = await import("../src/react/settings/api");
    const pending = deferred<GrowthState>();
    spyOn(settingsApi, "growthSummary").mockImplementationOnce(() => pending.promise);
    page.reenter(state({ config: fresh }));
    await page.settle();
    expect(page.checkbox(t("settings.growth.enable"))?.disabled).toBe(true);
    expect(page.checkbox(t("settings.growth.login"))?.disabled).toBe(true);
    expect(page.select("growth-daily")?.disabled).toBe(true);
    expect(page.select("growth-weekly")?.disabled).toBe(true);
    page.select("growth-daily")!.onChange({ target: { value: "5" } });
    await page.settle();
    expect(page.saved).toEqual([]);
    pending.resolve(state({ config: fresh }));
    await page.settle();
    expect(page.select("growth-daily")?.disabled).toBe(false);
    page.select("growth-daily")!.onChange({ target: { value: "5" } });
    await page.settle();
    expect(page.saved).toEqual([{ dailyGB: 5 }]);
    expect(page.select("growth-daily")?.value).toBe(5);
  } finally { page.close(); }
});

test("增长设置：每个控件只发自己改的字段，不带整份旧配置", async () => {
  const page = await growthSettingsHarness(state({ config: { ...state({}).config, enabled: true, ignored: ["app:codex"] } }));
  try {
    await page.settle();
    page.select("growth-daily")!.onChange({ target: { value: "5" } });
    await page.settle();
    page.select("growth-weekly")!.onChange({ target: { value: "20" } });
    await page.settle();
    page.checkbox(t("settings.growth.login"))!.onCheckedChange(true);
    await page.settle();
    page.checkbox(t("settings.growth.enable"))!.onCheckedChange(false);
    await page.settle();
    // 开关同时记下「问过了」，与增长页的打开按钮一致。
    expect(page.saved).toEqual([{ dailyGB: 5 }, { weeklyGB: 20 }, { openAtLogin: true }, { enabled: false, asked: true }]);
  } finally { page.close(); }
});

test("增长设置：后端广播时重读，保存期间到达的广播在保存后补读", async () => {
  const page = await growthSettingsHarness(state({ config: { ...state({}).config, enabled: true } }));
  const off = (s: GrowthState) => ({ ...s, config: { ...s.config, enabled: false, openAtLogin: false } });
  try {
    await page.settle();
    expect(page.reads).toBe(1);
    // 空闲时：菜单栏关掉监控，本页马上跟上。
    page.backend(off);
    page.updated();
    await page.settle();
    expect(page.reads).toBe(2);
    expect(page.checkbox(t("settings.growth.enable"))?.checked).toBe(false);
    // 保存进行中：广播先排队，保存和回读结束后再补读一次。
    page.backend((s) => ({ ...s, config: { ...s.config, enabled: true } }));
    page.updated();
    await page.settle();
    const gate = page.holdSave();
    page.select("growth-daily")!.onChange({ target: { value: "5" } });
    await page.settle();
    page.backend(off);
    page.updated();
    await page.settle();
    expect(page.reads).toBe(3);
    gate.resolve();
    await page.settle();
    expect(page.reads).toBe(5);
    expect(page.saved).toEqual([{ dailyGB: 5 }]);
    expect(page.checkbox(t("settings.growth.enable"))?.checked).toBe(false);
    expect(page.select("growth-daily")?.value).toBe(5);
    expect(page.select("growth-daily")?.disabled).toBe(false);
  } finally { page.close(); }
});

test("增长设置：开机启动出错时设置已保存并生效，回读后显示后端的值", async () => {
  const page = await growthSettingsHarness(state({}));
  try {
    await page.settle();
    page.failSave(new Error(JSON.stringify({ code: "growth-login", args: ["拒绝"] })), true);
    page.checkbox(t("settings.growth.enable"))!.onCheckedChange(true);
    await page.settle();
    expect(page.saved).toEqual([{ enabled: true, asked: true }]);
    expect(page.toast.textContent).toBe("无法设置开机启动：拒绝");
    expect(page.reads).toBe(2);
    expect(page.checkbox(t("settings.growth.enable"))?.checked).toBe(true);
    expect(page.checkbox(t("settings.growth.login"))?.disabled).toBe(false);
  } finally { page.close(); }
});

test("增长页只发改动的字段：打开、先不用、关着时打开、忽略与取消忽略", async () => {
  const page = await pageHarness();
  const latest = (s: GrowthState) => page.requests[page.requests.length - 1]!.resolve(s);
  const off = { ...state({}).config, asked: true };
  try {
    await page.settle();
    latest(state({ records: 2, apps: [app({})] }));
    await page.settle();
    page.click(t("growth.optin.enable"));
    await page.settle();
    page.click(t("growth.optin.later"));
    await page.settle();
    latest(state({ records: 2, apps: [app({})], config: off }));
    await page.settle();
    page.click(t("growth.off.enable"));
    await page.settle();
    page.openRow();
    await page.settle();
    page.click(t("growth.ignore"));
    await page.settle();
    latest(state({ records: 2, apps: [app({ ignored: true })], config: { ...off, ignored: ["app:x"] } }));
    await page.settle();
    page.click(t("growth.unignore"));
    await page.settle();
    expect(page.saves).toEqual([{ enabled: true, asked: true }, { asked: true }, { enabled: true }, { ignore: "app:x" }, { unignore: "app:x" }]);
  } finally { page.close(); }
});

test("刷新但没有新记录时保留卸载明细，有新记录才重新核对", async () => {
  const page = await pageHarness();
  const snapshot = (size: number, lastAt: number, records = 3) => state({ records, apps: [app({ size, appPath: "/A.app" })],
    status: { ...state({}).status, lastAt } });
  const detail: UninstallDetail = { app: {} as UninstallDetail["app"], totalSize: 3 * GB,
    items: [{ path: "/A.app", kind: "app", size: GB }, { path: "/c", kind: "cache", size: 2 * GB }] };
  try {
    await page.settle();
    page.requests[0]!.resolve(snapshot(10 * GB, 1));
    await page.settle();
    page.openRow();
    await page.settle();
    page.details[0]!.resolve(detail);
    await page.settle();
    page.updated(); // 比如开始量了：状态变了，记录没变
    page.requests[1]!.resolve({ ...snapshot(10 * GB, 1), status: { ...snapshot(10 * GB, 1).status, running: true } });
    await page.settle();
    expect(page.details.length).toBe(1);
    expect(page.text()).toContain("卸载能移走 3.0 GB；另有 8.0 GB");
    page.updated(); // 记录条数变了（旧记录过期删掉）也算新的一批
    page.requests[2]!.resolve(snapshot(10 * GB, 1, 2));
    await page.settle();
    expect(page.details.length).toBe(2);
  } finally { page.close(); }
});

test("隐藏时不跟着 growthUpdated 刷新，再显示时重新读", async () => {
  const page = await pageHarness();
  try {
    await page.settle();
    page.requests[0]!.resolve(state({ records: 2, apps: [app({})] }));
    await page.settle();
    page.show(false);
    await page.settle();
    page.updated();
    await page.settle();
    expect(page.requests.length).toBe(1);
    page.show(true);
    await page.settle();
    expect(page.requests.length).toBe(2);
    page.updated();
    await page.settle();
    expect(page.requests.length).toBe(3);
  } finally { page.close(); }
});

test("没有记录时「现在量一次」只在空状态里，量的时候转圈、不能再点；有了记录回到顶部", async () => {
  const page = await pageHarness();
  try {
    await page.settle();
    page.requests[0]!.resolve(state({ records: 0 }));
    await page.settle();
    expect(page.text()).not.toContain(t("growth.measureNow"));
    expect(page.guide()).toEqual({ ctaText: t("growth.measureNow"), busy: false });
    page.updated();
    page.requests[1]!.resolve(state({ records: 0, status: { ...state({}).status, running: true } }));
    await page.settle();
    expect(page.text()).not.toContain(t("growth.measuring"));
    expect(page.guide()).toEqual({ ctaText: t("growth.measuring"), busy: true });
    page.updated();
    page.requests[2]!.resolve(state({ records: 1, apps: [app({})] }));
    await page.settle();
    expect(page.guide()).toBeNull();
    expect(page.text()).toContain(t("growth.measureNow"));
  } finally { page.close(); }
});

test("面板没有页头；有记录时「现在量一次」在状态那一行里", async () => {
  const page = await pageHarness();
  try {
    await page.settle();
    page.requests[0]!.resolve(state({ records: 2, apps: [app({})] }));
    await page.settle();
    expect(page.has("view-head")).toBe(false);
    expect(page.statusLine()).toContain(t("growth.measureNow"));
  } finally { page.close(); }
});

test("行首有字母头像（点目录跳过开头的点），测量中图标用 animate-spin", async () => {
  const page = await pageHarness();
  try {
    await page.settle();
    page.requests[0]!.resolve(state({ records: 2, apps: [app({ name: "ToDesk", size: 2 * GB }), app({ key: "dir:/h/.npm", name: ".npm", kind: "dot", size: GB })],
      status: { ...state({}).status, running: true } }));
    await page.settle();
    expect(page.icons()).toEqual(["T", "N"]);
    expect(page.spinning()).toBe(true);
  } finally { page.close(); }
});
