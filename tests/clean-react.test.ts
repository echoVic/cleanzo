import { beforeEach, expect, test } from "bun:test";
import { readFileSync, readdirSync } from "node:fs";
import "./setup-dom";
import { setLang } from "./setup-dom";
import type { JunkItem, Settings, ScanSummary } from "../src/mygo";
import { ruleDesc, ruleName, t } from "../src/i18n";
import { errText, human } from "../src/shared";
import { changes, fakeStream, nodes, textOf } from "./page-harness";
import { PathText } from "../src/ui/components/path-text";
import { CleanPage } from "../src/react/clean/CleanPage";
import { cleanApi } from "../src/react/clean/api";
import { NumberTicker } from "../src/ui/motion/NumberTicker";
import { CleanResults } from "../src/react/clean/CleanResults";
import { cleanupExplanation } from "../src/react/clean/explanations";
import {
  applyCleanEvent, buildCleanPlan, canStartScan, chosenSize, cleanBarWidth, cleanTotals, emptySel,
  finalizeCleanRows, finalizeScan, createCleanRowTracker, groupCheckState, groupToggleOn, isSelectable, isVisible, itemTitle,
  keepLatestVisible, removeFromSel,
  rowStateNote, scanItem, scanStart, scanPhase, scanProgress, splitApps, toggleGroup, toggleRow, toggleSub,
  withIgnored, withIgnoredSummary, heroTotals,
  type RowState,
} from "../src/react/clean/helpers";

beforeEach(() => {
  setLang("zh");
});

test("the scanned headline follows selection and completion excludes recoverable Trash bytes", async () => {
  const env = changes();
  try {
    const stream = fakeStream<import("../src/mygo").JunkScanEvent>();
    const cleanup = fakeStream<import("../src/mygo").JunkCleanEvent>();
    let sent: unknown;
    env.assign(cleanApi, { startScan: () => stream, startClean: (sels: unknown) => { sent = sels; return cleanup; } });
    const h = env.page(CleanPage, {});
    await h.settle();
    h.find((n) => n.type?.name === "IdleHero")!.props.onCta();
    const cache = item("dev.npm", "dev", 1024);
    const review = item("review.installers", "review", 2048, { default: false, trash: true });
    stream.emit({ kind: "start", items: [cache, review] });
    stream.emit({ kind: "item", item: cache });
    stream.emit({ kind: "item", item: review });
    stream.finish();
    await h.settle();
    const headline = () => h.ofType(NumberTicker)[0]!.props.value;
    expect(headline()).toBe(1024);
    h.find((n) => n.props.ariaLabel === t("junk.select", { name: ruleName(review.id) }))!.props.onChange(true);
    await h.settle();
    expect(headline()).toBe(3072);
    h.click("清理所选");
    await h.settle();
    h.click(t("clean.confirm"));
    await h.settle();
    expect(sent).toEqual([{ id: "dev.npm" }, { id: "review.installers" }]);
    cleanup.emit({ kind: "done", id: "dev.npm", bytes: 1024, result: { name: "dev.npm", freed: 1024, trashed: 0, files: 1, inUse: 0, failed: [] } });
    cleanup.emit({ kind: "done", id: "review.installers", bytes: 3072, result: { name: "review.installers", freed: 0, trashed: 2048, files: 1, inUse: 0, failed: [] } });
    cleanup.finish();
    await h.settle();
    expect(headline()).toBe(1024);
  } finally { env.restore(); }
});

test("mixed cleanup results retain freed and Trash bytes and explain skipped, changed and stopped items", async () => {
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const rows = new Map<string, RowState>([
    ["dev.npm", { state: "done", result: { name: "dev.npm", freed: 1024, trashed: 0, files: 1, inUse: 0, failed: [] } }],
    ["review.installers", { state: "done", result: { name: "review.installers", freed: 0, trashed: 2048, files: 1, inUse: 0, failed: [] } }],
    ["app:running", { state: "done", result: { name: "app:running", freed: 0, trashed: 0, files: 0, inUse: 0, failed: [], skipped: "running" } }],
    ["sys.logs", { state: "done", result: { name: "sys.logs", freed: 4096, trashed: 0, files: 1, inUse: 2, failed: ["blocked", "blocked", "changed"] } }],
    ["dev.pip", { state: "stopped" }],
    ["dev.npx", { state: "skipped" }],
  ]);
  const env = changes();
  try {
    for (const lang of ["zh", "en"] as const) {
      env.lang(lang);
      const html = renderToStaticMarkup(createElement(CleanResults, { rows, items: [] }));
      expect(html).toContain(t("clean.result.count", { n: 2 }));
      expect(html).toContain(t("clean.result.count", { n: 1 }));
      expect(html).toContain(t("clean.result.count", { n: 3 }));
      expect(html).toContain(t("junk.skip.running"));
      expect(html).toContain(t("clean.done.inUse", { n: 2 }));
      expect(html).toContain(t("cat.changedN", { n: 1 }));
      expect(html).toContain(t("clean.result.reason", { n: 2, reason: errText("blocked") }).replaceAll("'", "&#x27;"));
      expect(html).toContain(t("clean.result.stopped"));
      expect(html).toContain(t("clean.result.notStarted"));
      expect(html).toContain(t("clean.done.freed", { size: human(4096) }));
      expect(html).toContain(t("cat.trashed", { size: human(2048) }));
    }
  } finally { env.restore(); }
});

test("stopping after a removal keeps unconfirmed totals honest", async () => {
  const env = changes();
  try {
    const stream = fakeStream<import("../src/mygo").JunkScanEvent>();
    const cleanup = fakeStream<import("../src/mygo").JunkCleanEvent>();
    env.assign(cleanApi, { startScan: () => stream, startClean: () => cleanup });
    const h = env.page(CleanPage, {});
    await h.settle();
    h.find((n) => n.type?.name === "IdleHero")!.props.onCta();
    const cache = item("dev.npm", "dev", 2048);
    stream.emit({ kind: "start", items: [cache] });
    stream.emit({ kind: "item", item: cache });
    stream.finish();
    await h.settle();
    h.click(t("clean.now"));
    await h.settle();
    h.click(t("clean.confirm"));
    await h.settle();
    cleanup.emit({ kind: "start", id: cache.id });
    cleanup.emit({ kind: "removed", id: cache.id, path: "/cache/npm/a", bytes: 1024 });
    await h.settle();
    h.click(t("clean.stop"));
    cleanup.finish();
    await h.settle();
    expect(h.find((n) => n.props.className === "hero-num")!.props.children).toBe("--");
    const text = h.text();
    expect(text).toContain(t("clean.result.unconfirmedTotal"));
    const { createElement } = await import("react");
    const { renderToStaticMarkup } = await import("react-dom/server");
    const result = renderToStaticMarkup(createElement(CleanResults, { rows: new Map<string, RowState>([[cache.id, { state: "stopped" }]]), items: [cache] }));
    expect(result).toContain(t("clean.result.unconfirmed"));
    expect(result).not.toContain(t("cat.notCleaned"));
  } finally { env.restore(); }
});

test("cache explanations preserve known browser scope and avoid inferring custom-folder safety", () => {
  const env = changes();
  try {
    for (const lang of ["zh", "en"] as const) {
      env.lang(lang);
      const browser = cleanupExplanation(item("browser.chrome", "browsers", 1), t);
      expect(browser.consequence).toContain(lang === "zh" ? "保留浏览历史" : "preserving history");
      const appCache = cleanupExplanation(item("app:example", "apps", 1), t);
      expect(appCache.consequence).toContain(lang === "zh" ? "离线内容" : "offline content");
      const custom = cleanupExplanation(item("custom:/work", "custom", 1), t);
      expect(custom.content).toContain(lang === "zh" ? "用途待确认" : "Purpose needs review");
      expect(custom.consequence).toContain(lang === "zh" ? "个人文件" : "personal files");
      expect(cleanupExplanation(item("dev.go-mod", "dev", 1), t).method).toBe(t("clean.explain.tool"));
    }
  } finally { env.restore(); }
});

const item = (id: string, group: string, size: number, extra: Partial<JunkItem> = {}): JunkItem => ({
  id, group, size, name: id, files: 1, default: true, trash: false, running: false,
  denied: false, estimate: false, inUse: 0, paths: [`~/cache/${id}`], top: [], subs: [], recovery: "download", ...extra,
});

/** 模拟一轮完整扫描：start 全量骨架 → 逐项 measured → done。 */
function scan(items: JunkItem[]): ReturnType<typeof finalizeScan> {
  let st = scanStart(items.map((it) => ({ ...it, size: -1 })));
  for (const it of items) st = scanItem(st, it);
  return finalizeScan(st);
}

test("pending rows spin; measured zero rows disappear; running and denied rows remain unselectable", () => {
  const items = [
    item("dev.npx", "dev", 1024), item("dev.npm", "dev", 0),
    item("browser.chrome", "browsers", 8192, { running: true }),
    item("sys.trash", "system", 0, { denied: true }), item("review.ipsw", "review", 2048),
  ];
  // start 之后：全部行 pending（骨架旋转），尚未测量。
  const pendingState = scanStart(items);
  expect([...pendingState.pending].sort()).toEqual(items.map((i) => i.id).sort());

  const done = scan(items);
  // 零大小行不可见（不渲染）；denied 行仍可见（显示 --）。
  expect(done.items.filter(isVisible).map((i) => i.id)).not.toContain("dev.npm");
  expect(done.items.filter(isVisible).map((i) => i.id)).toContain("sys.trash");

  // hero 总量只算可清非 review 行：运行中 / 被拒不计入，review 单列。
  const sel = done.sel;
  const eligible = done.items.filter(isSelectable);
  expect(eligible.map((i) => i.id).sort()).toEqual(["dev.npx", "review.ipsw"]);
  expect(human(eligible.filter((i) => i.group !== "review").reduce((n, i) => n + i.size, 0))).toBe("1.0 KB");
  // review 组另有 2.0 KB 需要确认（副行文案的数据源）。
  expect(human(eligible.filter((i) => i.group === "review").reduce((n, i) => n + i.size, 0))).toBe("2.0 KB");
  // 运行中行的描述是退出提示，不写「缓存」。
  const running = done.items.find((i) => i.id === "browser.chrome")!;
  expect(t("junk.quit", { app: "Chrome" })).toBe("退出「Chrome」后可清理");
  // 未默认的行不进默认勾选。
  expect(sel.selected.has("browser.chrome")).toBe(false);
});

test("discovered bytes include running and review candidates without making them selectable", () => {
  const st = scan([
    item("dev.npx", "dev", 1024),
    item("browser.chrome", "browsers", 8192, { running: true }),
    item("review.ipsw", "review", 2048, { default: false }),
    item("app:partial", "apps", 4096, { denied: true }),
    item("app:pending", "apps", -1),
  ]);
  const totals = heroTotals(st.items, st.sel);
  expect(totals.foundBytes).toBe(15360);
  expect(totals.selectedBytes).toBe(1024);
  expect(totals.reviewBytes).toBe(2048);
  expect(buildCleanPlan(st.items, st.sel)!.selections).toEqual([{ id: "dev.npx" }]);
});

test("selected review bytes agree with the confirmation plan", () => {
  const st = scan([item("dev.npx", "dev", 1024), item("review.ipsw", "review", 2048, { default: false })]);
  const sel = toggleRow(st.items[1]!, true, st.sel);
  expect(heroTotals(st.items, sel).selectedBytes).toBe(buildCleanPlan(st.items, sel)!.bytes);
});

test("group totals and hidden apps use discovered capacity, while busy entries remain unselectable", () => {
  const st = scan([
    item("app:partial", "apps", 1024, { foundSize: 4096, inUse: 1 }),
    item("app:busy", "apps", 2048, { running: true }),
    item("app:error", "apps", 0, { readErrors: 1 }),
  ]);
  const totals = heroTotals(st.items, st.sel);
  expect(totals.foundBytes).toBe(6144);
  expect(totals.availableBytes).toBe(1024);
  expect(totals.runningBytes).toBe(2048);
  expect(totals.incompleteItems).toBe(1);
  expect(isVisible(st.items[2]!)).toBe(true);
  expect(buildCleanPlan(st.items, st.sel)!.bytes).toBe(1024);
});

test("review and running are overlapping labels, not additive buckets", () => {
  const st = scan([item("review.ipsw", "review", 2048, { running: true, default: false })]);
  const totals = heroTotals(st.items, st.sel);
  expect(totals.foundBytes).toBe(2048);
  expect(totals.runningBytes).toBe(2048);
  expect(totals.reviewBytes).toBe(2048);
  expect(totals.availableBytes).toBe(0);
  expect(buildCleanPlan(st.items, st.sel)).toBeNull();
});

test("detail selection uses only default children and confirmation sends their paths", () => {
  const dev = item("review.device-support", "review", 3072, { default: false, subs: [
    { path: "/old", name: "17.0", detail: "iOS", size: 1024, default: true },
    { path: "/new", name: "18.0", detail: "iOS", size: 2048, default: false },
  ] });
  const st = scan([dev]);
  // 未默认整项时整行不勾；子项默认集 = 只有 /old。
  expect(st.sel.selected.has(dev.id)).toBe(false);
  expect([...(st.sel.subSel.get(dev.id) ?? [])]).toEqual(["/old"]);

  const on = toggleRow(dev, true, st.sel);
  const plan = buildCleanPlan(st.items, on)!;
  expect(plan.selections).toEqual([{ id: "review.device-support", paths: ["/old"] }]);
  expect(human(plan.bytes)).toBe("1.0 KB");
  // 展开时非默认子项上显示「保留最新」。
  const fresh = dev.subs![1]!;
  expect(keepLatestVisible(dev, fresh, on)).toBe(true);
});

test("unchecking the last child never falls back to cleaning the entire item", () => {
  const ipsw = item("review.ipsw", "review", 1024, { subs: [
    { path: "/old", name: "old", detail: "", size: 1024, default: true },
  ] });
  const st = scan([ipsw]);
  expect(st.sel.selected.has(ipsw.id)).toBe(true);
  const off = toggleSub(ipsw, "/old", false, st.sel);
  expect(off.selected.has(ipsw.id)).toBe(false);
  expect(chosenSize(ipsw, off)).toBe(0);
  // 没有勾中任何字节时确认弹层不出现（立即清理保持禁用）。
  expect(buildCleanPlan(st.items, off)).toBeNull();
});

test("apps are truncated at 15 and show their remaining size; usage and estimate labels render", () => {
  const apps = Array.from({ length: 17 }, (_, i) =>
    item(`app:${i}`, "apps", (17 - i) * 1024, { estimate: true, inUse: 2 }));
  const st = scan(apps);
  const { shown, rest } = splitApps(st.items, false);
  expect(shown.length).toBe(15);
  expect(rest.length).toBe(2);
  // 「其余 N 个应用（共 size）」的文案数据源：剩余 2 个、共 3.0 KB。
  expect(rest.map((i) => i.id)).toEqual(["app:15", "app:16"]);
  expect(human(rest.filter(isSelectable).reduce((n, i) => n + i.size, 0))).toBe("3.0 KB");
  expect(t("junk.moreApps", { n: rest.length, size: "3.0 KB" })).toBe("其余 2 个应用（共 3.0 KB）");
  // 标签文案：最多（estimate）、N 项正在使用（inUse）。
  expect(t("junk.estimate")).toBe("最多");
  expect(t("junk.inUse", { n: 2 })).toBe("2 项正在使用，已跳过");
  // 「查看全部」后 17 个全显示。
  expect(splitApps(st.items, true).shown.length).toBe(17);
});

test("ignoring a row adds it to the ignored list once, drops its selection and counts it in the summary", () => {
  const st = scan([item("dev.npx", "dev", 1024, { subs: [{ path: "/a", name: "a", detail: "", size: 1024, default: true }] })]);
  expect(st.sel.selected.has("dev.npx")).toBe(true);
  expect([...st.sel.subSel.keys()]).toContain("dev.npx");
  const settings: Settings = { disabledGroups: [], ignored: [], custom: [] };
  const next = withIgnored(settings, "dev.npx");
  expect(next.ignored).toEqual(["dev.npx"]);
  expect(withIgnored(next, "dev.npx").ignored).toEqual(["dev.npx"]);
  // 传入的设置不变：保存失败时调用方手里还是原来的那份。
  expect(settings.ignored).toEqual([]);
  const after = removeFromSel(st.sel, "dev.npx");
  expect(after.selected.has("dev.npx")).toBe(false);
  expect([...after.subSel.keys()]).not.toContain("dev.npx");
  expect(withIgnoredSummary({ fullDiskAccess: false, containers: 3, checked: 54, ignored: 2, leftovers: 0, leftoversSize: 0 })!.ignored).toBe(3);
});

test("a scan can start after a scan or a clean, but not while scanning, cleaning or ignoring", () => {
  for (const phase of ["idle", "scanned", "cleaned"]) expect(canStartScan(phase, false)).toBe(true);
  for (const phase of ["scanning", "cleaning"]) expect(canStartScan(phase, false)).toBe(false);
  expect(canStartScan("scanned", true)).toBe(false);
});

test("stopping a scan retains measured rows and drops buffered and unmeasured rows", () => {
  let st = scanStart([item("dev.npx", "dev", 1024), item("dev.npm", "dev", 2048)]);
  st = scanItem(st, item("dev.npx", "dev", 1024));
  // 用户点了停止：npm 还在 pending（没收到 item），finalize 时被丢掉。
  const done = finalizeScan(st);
  expect(done.items.map((i) => i.id)).toEqual(["dev.npx"]);
  expect(done.pending.size).toBe(0);
});

test("group checkbox is half-checked, never fully checked, while running or denied rows are unselected", () => {
  const st = scan([
    item("app:ready", "apps", 1024, { default: true }), item("app:busy", "apps", 8192, { running: true }),
    item("app:denied", "apps", 4096, { denied: true }),
  ]);
  // 只有 ready 默勾；busy/denied 不可选没勾 → 分组框半选，绝不是全选。
  expect(groupCheckState("apps", st.items, st.sel)).toBe("partial");
  expect(st.sel.selected.has("app:ready")).toBe(true);
  expect(st.sel.selected.has("app:busy")).toBe(false);
});

test("group is fully checked only when every visible row is selected", () => {
  const st = scan([item("app:one", "apps", 1024), item("app:two", "apps", 2048)]);
  expect(groupCheckState("apps", st.items, st.sel)).toBe("checked");
  // 组框取消 → 全部取消。
  const off = toggleGroup("apps", false, st.items, st.sel);
  expect(off.selected.size).toBe(0);
});

test("group state follows nested child selection instead of counting a partial row as fully checked", () => {
  const installers = item("review.installers", "review", 3072, { default: false, trash: true, subs: [
    { path: "/one.dmg", name: "one.dmg", detail: "", size: 1024, default: true },
    { path: "/two.pkg", name: "two.pkg", detail: "", size: 2048, default: true },
  ] });
  const st = scan([installers]);
  expect(groupCheckState("review", st.items, st.sel)).toBe("unchecked");
  const one = toggleSub(installers, "/one.dmg", true, st.sel);
  expect(groupCheckState("review", st.items, one)).toBe("partial");
  expect(buildCleanPlan(st.items, one)!.selections).toEqual([{ id: installers.id, paths: ["/one.dmg"] }]);
  const all = toggleGroup("review", true, st.items, one);
  expect(groupCheckState("review", st.items, all)).toBe("checked");
  const partial = toggleSub(installers, "/two.pkg", false, all);
  expect(groupCheckState("review", st.items, partial)).toBe("partial");
  const none = toggleGroup("review", false, st.items, partial);
  expect(groupCheckState("review", st.items, none)).toBe("unchecked");
  expect(buildCleanPlan(st.items, none)).toBeNull();
});

test("group select and deselect preserve partial state while blocked rows never enter the plan", () => {
  const ready = item("review.installers", "review", 1024, { default: false, subs: [
    { path: "/ready.dmg", name: "ready.dmg", detail: "", size: 1024, default: true },
  ] });
  const st = scan([ready,
    item("review.ios-backups", "review", 4096, { denied: true }),
    item("review.mail-downloads", "review", 2048, { running: true }),
  ]);
  for (let i = 0; i < 3; i++) {
    expect(groupToggleOn("review", st.items, st.sel)).toBe(true);
    st.sel = toggleGroup("review", groupToggleOn("review", st.items, st.sel), st.items, st.sel);
    expect(groupCheckState("review", st.items, st.sel)).toBe("partial");
    expect(buildCleanPlan(st.items, st.sel)!.selections).toEqual([{ id: ready.id, paths: ["/ready.dmg"] }]);
    expect(groupToggleOn("review", st.items, st.sel)).toBe(false);
    st.sel = toggleGroup("review", groupToggleOn("review", st.items, st.sel), st.items, st.sel);
    expect(groupCheckState("review", st.items, st.sel)).toBe("unchecked");
    expect(buildCleanPlan(st.items, st.sel)).toBeNull();
  }
});

test("group selection never includes a running app or a denied item", () => {
  const st = scan([
    item("app:ready", "apps", 1024), item("app:busy", "apps", 8192, { running: true }),
    item("app:denied", "apps", 4096, { denied: true }),
  ]);
  const on = toggleGroup("apps", true, st.items, st.sel);
  const plan = buildCleanPlan(st.items, on)!;
  expect(plan.selections).toEqual([{ id: "app:ready" }]);
  // 后端把运行中的行标记 skipped=running：行注记显示「运行中，已跳过」。
  const note = rowStateNote(
    { state: "done", result: { name: "app:ready", freed: 0, trashed: 0, inUse: 0, files: 0, failed: [], skipped: "running" } },
    t, human);
  expect(note.skipWarn).toBe("运行中，已跳过");
});

test("a tool that cannot run stays visible but is never selected, alone, by its group or in the total", () => {
  const broken = item("dev.pnpm-store", "dev", 0, { paths: [], problem: "tool-failed", problemDetail: "Internal Error: Cannot find matching keyid" });
  expect(scan([item("dev.npx", "dev", 1024), broken]).items.filter(isVisible).map((i) => i.id)).toEqual(["dev.npx", "dev.pnpm-store"]);
  // 带着大小也一样：问题行不默勾、不能单选、组选跳过它，已选总量和清理计划都不算它。
  const sized = { ...broken, size: 2048 };
  const st = scan([item("dev.npx", "dev", 1024), sized]);
  expect(isSelectable(sized)).toBe(false);
  expect([...st.sel.selected]).toEqual(["dev.npx"]);
  expect(toggleRow(sized, true, st.sel).selected.has(sized.id)).toBe(false);
  const all = toggleGroup("dev", groupToggleOn("dev", st.items, emptySel()), st.items, emptySel());
  expect([...all.selected]).toEqual(["dev.npx"]);
  const forced = { ...all, selected: new Set([...all.selected, sized.id]) };
  expect(heroTotals(st.items, forced).selectedBytes).toBe(1024);
  expect(buildCleanPlan(st.items, forced)!.selections).toEqual([{ id: "dev.npx" }]);
});

test("a tool that cannot run reads why where its size would be and shows the tool's line in its details", async () => {
  const env = changes();
  try {
    const stream = fakeStream<import("../src/mygo").JunkScanEvent>();
    env.assign(cleanApi, { startScan: () => stream });
    const h = env.page(CleanPage, {});
    await h.settle();
    h.find((n) => n.type?.name === "IdleHero")!.props.onCta();
    const broken = item("dev.pnpm-store", "dev", 0, { paths: [], problem: "tool-failed", problemDetail: "Error: Cannot find module '/Users/me/.cache/node/corepack/v1/pnpm/12.0.0/bin/pnpm.cjs'" });
    stream.emit({ kind: "start", items: [{ ...broken, size: -1 }] });
    stream.emit({ kind: "item", item: broken });
    stream.finish();
    await h.settle();
    for (const [lang, reason] of [["zh", "pnpm 无法运行，暂时不能清理"], ["en", "pnpm cannot run, so this cannot be cleaned for now"]] as const) {
      env.lang(lang);
      h.setProps({});
      await h.settle();
      const row = h.find((n) => n.props["data-key"] === broken.id);
      expect(row).toBeDefined();
      const note = h.find((n) => n.props.className === "cat-note" && textOf(n).includes(reason));
      expect(note).toBeDefined();
      expect(textOf(note!)).not.toContain(human(0));
      // 展开区：规则原有的说明在上，工具输出里说明出错的那一行在下。
      const details = h.find((n) => n.props.className === "junk-details" && textOf(n).includes(broken.problemDetail!))!;
      expect(details).toBeDefined();
      const text = textOf(details);
      expect(text.indexOf(cleanupExplanation(broken, t).method)).toBeLessThan(text.indexOf(broken.problemDetail!));
      expect(h.find((n) => n.props.ariaLabel === t("junk.select", { name: ruleName(broken.id) }))!.props.disabled).toBe(true);
      expect(h.find((n) => n.props.ariaLabel === t("junk.select", { name: t("junk.group.dev") }))!.props.disabled).toBe(true);
    }
  } finally { env.restore(); }
});

test("a timed-out tool says so, an unknown problem reads as a tool that cannot run, and an empty detail adds no line", async () => {
  const env = changes();
  try {
    const stream = fakeStream<import("../src/mygo").JunkScanEvent>();
    env.assign(cleanApi, { startScan: () => stream });
    const h = env.page(CleanPage, {});
    await h.settle();
    h.find((n) => n.type?.name === "IdleHero")!.props.onCta();
    const slow = item("dev.go-mod", "dev", 0, { paths: [], problem: "tool-timeout" });
    const odd = item("dev.homebrew-old", "dev", 0, { paths: [], problem: "tool-vanished" });
    stream.emit({ kind: "start", items: [slow, odd].map((it) => ({ ...it, size: -1 })) });
    stream.emit({ kind: "item", item: slow });
    stream.emit({ kind: "item", item: odd });
    stream.finish();
    await h.settle();
    for (const [lang, slowText, oddText] of [
      ["zh", "go 响应超时，暂时不能清理", "brew 无法运行，暂时不能清理"],
      ["en", "go timed out, so this cannot be cleaned for now", "brew cannot run, so this cannot be cleaned for now"],
    ] as const) {
      env.lang(lang);
      h.setProps({});
      await h.settle();
      for (const [it, text] of [[slow, slowText], [odd, oddText]] as const) {
        const row = h.find((n) => n.props["data-key"] === it.id);
        expect(row).toBeDefined();
        expect(textOf(nodes(row).find((n) => n.props.className === "cat-note"))).toBe(text);
        // 没有可用的输出行：展开区只有规则说明，不多出一行。
        const details = nodes(row).find((n) => n.props.className === "junk-details");
        expect(textOf(details)).toContain(cleanupExplanation(it, t).method);
        expect(nodes(details).some((n) => n.props.className === "cleanup-locations")).toBe(false);
      }
    }
  } finally { env.restore(); }
});

test("tool failure shows both its skip reason and failed count", () => {
  const row = rowStateNote(
    { state: "done", result: { name: "dev.go-mod", freed: 0, trashed: 0, inUse: 0, files: 0, failed: ["blocked"], skipped: "failed" } },
    t, human);
  expect(row.skipWarn).toBe("清理失败，已跳过");
  expect(row.failedCount).toBe(1);
});

test("Trash entries kept because they changed after the scan read as kept, not failed, yet the row is still a partial clean", () => {
  const kept = { name: "sys.trash", freed: 2048, trashed: 0, inUse: 0, files: 3, failed: ["changed", "changed"] };
  const note = rowStateNote({ state: "done", result: kept }, t, human);
  expect(note.failedCount).toBeUndefined();
  expect(note.changedNote).toBe("2 项有变动，已保留");
  setLang("en");
  expect(rowStateNote({ state: "done", result: { ...kept, failed: ["changed"] } }, t, human).changedNote).toBe("1 item changed, kept");
  setLang("zh");
  // 还有别的失败时照旧按失败计数。
  const mixed = rowStateNote({ state: "done", result: { ...kept, failed: ["changed", "blocked"] } }, t, human);
  expect(mixed.failedCount).toBe(2);
  expect(mixed.changedNote).toBeUndefined();
  expect(cleanTotals(new Map<string, RowState>([["sys.trash", { state: "done", result: kept }]])).failedNames).toEqual(["sys.trash"]);
});

test("all backend catalog rules have names and descriptions in both languages", () => {
  const root = new URL("../", import.meta.url);
  const ids = readdirSync(root).filter((name) => name.endsWith("rules.go")).flatMap((name) =>
    [...readFileSync(new URL(name, root), "utf8").matchAll(/ID: "([^"]+)"/g)].map((m) => m[1]!));
  expect(ids.length).toBeGreaterThanOrEqual(55);
  for (const lang of ["zh", "en"] as const) {
    setLang(lang);
    for (const id of ids) {
      expect(ruleName(id)).not.toBe(id);
      expect(ruleDesc(id)).not.toBe(id);
    }
  }
  setLang( "zh");
});

test("v3 errors from ignoring an item are translated rather than showing raw JSON", () => {
  for (const code of ["folder-empty", "folder-protected", "folder-dup", "group-unknown"]) {
    const raw = JSON.stringify({ code, args: ["~/cache"] });
    expect(errText(Error(raw))).not.toBe(raw);
  }
});

test("unsupported platform operations have a translated error in both languages", () => {
  for (const lang of ["zh", "en"] as const) {
    setLang(lang);
    expect(errText(Error("unsupported"))).toBe(t("fs.unsupported"));
    expect(errText(Error("unsupported"))).not.toBe("unsupported");
  }
  setLang("zh");
});

test("explicitly selecting the latest version removes Keep Latest and sends only that path", () => {
  const dev = item("review.device-support", "review", 3072, { default: false, subs: [
    { path: "/old", name: "17.0", detail: "iOS", size: 1024, default: true },
    { path: "/new", name: "18.0", detail: "iOS", size: 2048, default: false },
  ] });
  const st = scan([dev]);
  const on = toggleSub(dev, "/new", true, st.sel);
  // 显勾最新版本后「保留最新」提示消失。
  expect(keepLatestVisible(dev, dev.subs![1]!, on)).toBe(false);
  const plan = buildCleanPlan(st.items, on)!;
  expect(plan.selections).toEqual([{ id: "review.device-support", paths: ["/new"] }]);
});

test("stopping cleanup retains completed results and marks the remaining rows", () => {
  let rows = new Map<string, RowState>([["dev.npx", { state: "waiting" }], ["dev.npm", { state: "waiting" }]]);
  rows = applyCleanEvent(rows, { kind: "start", id: "dev.npx" });
  rows = applyCleanEvent(rows, { kind: "done", id: "dev.npx", result: { name: "dev.npx", freed: 1024, trashed: 0, inUse: 0, files: 0, failed: [] } });
  rows = applyCleanEvent(rows, { kind: "start", id: "dev.npm" });
  const { rows: final, stopped } = finalizeCleanRows(rows);
  expect(stopped).toBe(true);
  expect(final.get("dev.npx")!.state).toBe("done");
  expect(final.get("dev.npm")!.state).toBe("stopped");
  // 已完成的结果保留：已释放 1.0 KB。
  const totals = cleanTotals(final);
  expect(totals.freed).toBe(1024);
  expect(rowStateNote(final.get("dev.npx")!, t, human).text).toBe("已释放 1.0 KB");
  // 进度条按已清理比例（1024 / 3072 ≈ 33%）。
  expect(cleanBarWidth(1024, 3072, true, false, false)).toBeCloseTo(33.33, 1);
});

test("cleanup completion retains final results before React commits the queued render", () => {
  const tracker = createCleanRowTracker([{ id: "review.installers" }]);
  const staleRender = tracker.rows;
  tracker.apply({ kind: "start", id: "review.installers" });
  const beforeDoneRender = tracker.rows;
  const result = { name: "review.installers", freed: 0, trashed: 1024, inUse: 0, files: 1, failed: [] };
  tracker.apply({ kind: "done", id: "review.installers", result });
  // Both snapshots may still be the last React render when the stream closes.
  expect(staleRender.get(result.name)!.state).toBe("waiting");
  expect(beforeDoneRender.get(result.name)!.state).toBe("cleaning");
  const final = tracker.finish();
  expect(final.stopped).toBe(false);
  expect(final.rows.get(result.name)).toEqual({ state: "done", result });
  expect(cleanTotals(final.rows).trashed).toBe(1024);
  expect(rowStateNote(final.rows.get(result.name)!, t, human).text).toBe("已移到废纸篓 1.0 KB");
});

test("cleanup tracker keeps completed and failed results when another row is canceled", () => {
  const tracker = createCleanRowTracker([{ id: "done" }, { id: "failed" }, { id: "active" }, { id: "waiting" }]);
  const done = { name: "done", freed: 100, trashed: 0, inUse: 0, files: 1, failed: [] };
  const failed = { name: "failed", freed: 0, trashed: 0, inUse: 0, files: 0, failed: ["blocked"], skipped: "failed" };
  tracker.apply({ kind: "done", id: "done", result: done });
  tracker.apply({ kind: "done", id: "failed", result: failed });
  tracker.apply({ kind: "start", id: "active" });
  const final = tracker.finish();
  expect(final.stopped).toBe(true);
  expect(final.rows.get("done")!.result).toBe(done);
  expect(final.rows.get("failed")!.result).toBe(failed);
  expect(final.rows.get("active")!.state).toBe("stopped");
  expect(final.rows.get("waiting")!.state).toBe("skipped");
  expect(cleanTotals(final.rows)).toEqual({ freed: 100, trashed: 0, inUse: 0, failedNames: ["failed"] });
});


test("Windows hides the Full Disk Access notice even when the summary denies access", async () => {
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { FullDiskAccessNotice } = await import("../src/react/clean/CleanPage");
  const { setPlatformForTest } = await import("../src/platform");
  const props = { summary: { fullDiskAccess: false, containers: 3 } as ScanSummary, grant: () => {} };
  try {
    setPlatformForTest("darwin");
    expect(renderToStaticMarkup(createElement(FullDiskAccessNotice, props))).toContain("junk-banner");
    setPlatformForTest("win32");
    expect(renderToStaticMarkup(createElement(FullDiskAccessNotice, props))).toBe("");
  } finally { setPlatformForTest(undefined); }
});

test("scan progress counts measured rules and the phase is the first group still pending", () => {
  let st = scanStart([item("sys.logs", "system", -1), item("browser.chrome", "browsers", -1), item("dev.npm", "dev", -1)]);
  expect(scanProgress(st)).toEqual({ done: 0, total: 3 });
  expect(scanPhase(st)).toBe("system");
  st = scanItem(st, item("browser.chrome", "browsers", 5));
  expect(scanProgress(st)).toEqual({ done: 1, total: 3 });
  expect(scanPhase(st)).toBe("system");
  st = scanItem(st, item("sys.logs", "system", 5));
  expect(scanPhase(st)).toBe("dev");
  st = scanItem(st, item("dev.npm", "dev", 0));
  expect(scanProgress(st)).toEqual({ done: 3, total: 3 });
  expect(scanPhase(st)).toBeNull();
});

test("scan phase labels exist in both languages", () => {
  for (const lang of ["zh", "en"] as const) {
    setLang(lang);
    for (const g of ["system", "apps", "browsers", "updates", "dev", "projects", "review", "custom"]) {
      expect(t(`scan.phase.${g}` as never)).not.toBe(`scan.phase.${g}`);
    }
    expect(t("scan.progressCount", { done: 3, total: 56 })).toBe(lang === "zh" ? "已检查 3 / 56 项" : "Checked 3 of 56");
  }
});

test("dev projects come right after developer tools, unselected, with the project's path and idle days beneath", async () => {
  const env = changes();
  try {
    const stream = fakeStream<import("../src/mygo").JunkScanEvent>();
    env.assign(cleanApi, { startScan: () => stream });
    const h = env.page(CleanPage, {});
    await h.settle();
    h.find((n) => n.type?.name === "IdleHero")!.props.onCta();
    const cache = item("dev.npm", "dev", 1024);
    const project = item("project:/Users/demo/code/site", "projects", 4096, {
      name: "site", default: false, trash: true, idleDays: 37, paths: ["~/code/site/node_modules", "~/code/site/.next"],
    });
    const review = item("review.installers", "review", 2048, { default: false, trash: true });
    stream.emit({ kind: "start", items: [review, project, cache] });
    for (const it of [review, project, cache]) stream.emit({ kind: "item", item: it });
    stream.finish();
    await h.settle();
    const groups = h.all().filter((n) => n.props.className === "group-name").map((n) => textOf(n));
    expect(groups).toEqual([t("junk.group.dev"), t("junk.group.projects"), t("junk.group.review")]);
    expect(itemTitle(project, ruleName)).toBe("site");
    expect(h.find((n) => n.props.ariaLabel === t("junk.select", { name: "site" }))!.props.checked).toBe(false);
    expect(h.ofType(NumberTicker)[0]!.props.value).toBe(1024);
    const row = nodes(h.find((n) => n.props["data-key"] === project.id));
    const desc = row.find((n) => n.props.className === "cat-desc muted")!;
    expect(nodes(desc.props.children).find((n) => n.type === PathText)?.props.path).toBe("~/code/site");
    expect(row.some((n) => n.props.className === "cat-sub muted" && textOf(n) === "37 天没有改动")).toBe(true);
  } finally { env.restore(); }
  setLang("en");
  expect(t("junk.projectIdle", { n: 37 })).toBe("No changes for 37 days");
  expect(t("junk.group.projects")).toBe("Dev Projects");
  setLang("zh");
  expect(t("junk.group.projects")).toBe("开发项目");
});

test("a project row explains its rebuildable contents, how they come back, and that they go to the Trash", () => {
  const env = changes();
  try {
    for (const lang of ["zh", "en"] as const) {
      env.lang(lang);
      const e = cleanupExplanation(item("project:/Users/demo/code/site", "projects", 1, { default: false, trash: true }), t);
      expect(e.content).toBe(t("clean.explain.project"));
      expect(e.content).toContain(lang === "zh" ? "构建产物" : "build output");
      expect(e.content).toContain(lang === "zh" ? "依赖" : "dependencies");
      expect(e.consequence).toBe(t("clean.explain.projectEffect"));
      expect(e.consequence).toContain(lang === "zh" ? "重新生成" : "recreates");
      expect(e.method).toBe(t("clean.explain.trash"));
    }
  } finally { env.restore(); }
});

test("the clean-complete moment plays only for a full, error-free clean that freed space", async () => {
  const { shouldPlayFocal } = await import("../src/react/clean/focal");
  expect(shouldPlayFocal({ stopped: false, failed: 0, error: false, bytes: 1 })).toBe(true);
  expect(shouldPlayFocal({ stopped: true, failed: 0, error: false, bytes: 1 })).toBe(false);
  expect(shouldPlayFocal({ stopped: false, failed: 1, error: false, bytes: 1 })).toBe(false);
  expect(shouldPlayFocal({ stopped: false, failed: 0, error: true, bytes: 1 })).toBe(false);
  expect(shouldPlayFocal({ stopped: false, failed: 0, error: false, bytes: 0 })).toBe(false);
});
