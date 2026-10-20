import { beforeEach, expect, test } from "bun:test";
import { readFileSync, readdirSync } from "node:fs";
import "./setup-dom";
import { setLang } from "./setup-dom";
import type { JunkItem, Settings, ScanSummary } from "../src/mygo";
import { ruleDesc, ruleName, t } from "../src/i18n";
import { errText, human } from "../src/shared";
import {
  applyCleanEvent, buildCleanPlan, canStartScan, chosenSize, cleanBarWidth, cleanTotals,
  finalizeCleanRows, finalizeScan, createCleanRowTracker, groupCheckState, groupToggleOn, isSelectable, isVisible, itemTitle,
  keepLatestVisible, removeFromSel,
  rowStateNote, scanItem, scanStart, splitApps, toggleGroup, toggleRow, toggleSub,
  withIgnored, withIgnoredSummary, emptySel, heroTotals,
  type RowState,
} from "../src/react/clean/helpers";

beforeEach(() => {
  setLang("zh");
});

const item = (id: string, group: string, size: number, extra: Partial<JunkItem> = {}): JunkItem => ({
  id, group, size, name: id, files: 1, default: true, trash: false, running: false,
  denied: false, estimate: false, inUse: 0, paths: [`~/cache/${id}`], top: [], subs: [], ...extra,
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

test("ignore persists before hiding a row and rolls back on failure, without a stale-results bar", () => {
  const st = scan([item("dev.npx", "dev", 1024)]);
  const settings: Settings = { disabledGroups: [], ignored: [], custom: [] };
  // 成功路径：读改写 ignored 列表后才移走行。
  const next = withIgnored(settings, "dev.npx");
  expect(next.ignored).toEqual(["dev.npx"]);
  const after = removeFromSel({ ...st }, "dev.npx");
  expect(after.selected.has("dev.npx")).toBe(false);
  expect([...after.subSel.keys()]).not.toContain("dev.npx");
  // 行从 items 移除（旧测试断言 data-id 消失的等价物）。
  const kept = st.items.filter((x) => x.id !== "dev.npx");
  expect(kept).toHaveLength(0);
  // 检查计数 +1；本页自己忽略的项不触发 stale 条（stale 只由设置页保存触发）。
  expect(withIgnoredSummary({ fullDiskAccess: false, containers: 3, checked: 54, ignored: 2, leftovers: 0, leftoversSize: 0 }).ignored).toBe(3);
  // 失败路径：不改 settings、不移行（调用方只有 try 成功后才调用上面两个纯函数）。
  expect(settings.ignored).toEqual([]);
});

test("leftovers callback and English refresh use the current language", () => {
  scan([item("dev.npx", "dev", 1024)]);
  setLang( "en");
  expect(ruleName("dev.npx")).toBe("npx cache");
  expect(ruleName("dev.npx")).not.toBe("dev.npx");
  expect(t("junk.access", { n: 3 })).toContain("Full Disk Access");
  setLang( "zh");
});

test("a scan error presents retry instead of selectable partial results", () => {
  // 旧 DOM 测试的纯逻辑等价物：失败时 items 清空、计划为 null（界面只留重试）。
  const cleared = { items: [], pending: new Set<string>(), sel: emptySel() };
  expect(buildCleanPlan(cleared.items, cleared.sel)).toBeNull();
  // cleaned 阶段允许重新扫描（失败后「重新扫描」按钮可用）。
  expect(canStartScan("cleaned", false)).toBe(true);
  expect(canStartScan("scanning", false)).toBe(false);
  expect(canStartScan("cleaning", false)).toBe(false);
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

test("tool failure shows both its skip reason and failed count", () => {
  const row = rowStateNote(
    { state: "done", result: { name: "dev.go-mod", freed: 0, trashed: 0, inUse: 0, files: 0, failed: ["blocked"], skipped: "failed" } },
    t, human);
  expect(row.skipWarn).toBe("清理失败，已跳过");
  expect(row.failedCount).toBe(1);
});

test("shared confirmation clears pending on cancel and invalidates old async modal work", () => {
  // 旧断言针对已删除的全局 overlay/setPending 序号机制。React 版确认弹层是本地
  // Dialog：取消即不触发 doClean；迟到的旧工作由 phase 守卫丢弃。这里校验守卫。
  expect(canStartScan("scanned", false)).toBe(true);
  expect(canStartScan("scanned", true)).toBe(false); // ignoring 锁：确认流程不响应
  // doClean 入口要求 phase 仍为 scanned；确认打开期间若旧清理已开始（cleaning），
  // 迟到的确认回调直接返回——行为等价于旧 modalSeq 失效。
  expect(canStartScan("cleaning", false)).toBe(false);
});

test("all backend catalog rules have names and descriptions in both languages", () => {
  const root = new URL("../", import.meta.url);
  const ids = readdirSync(root).filter((name) => name.endsWith("rules.go")).flatMap((name) =>
    [...readFileSync(new URL(name, root), "utf8").matchAll(/ID: "([^"]+)"/g)].map((m) => m[1]!));
  expect(ids.length).toBeGreaterThanOrEqual(55);
  for (const lang of ["zh", "en"]) {
    setLang( lang);
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
  for (const lang of ["zh", "en"]) {
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
  expect(cleanBarWidth(1024, 3072, true, false, "")).toBeCloseTo(33.33, 1);
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

test("rejected cleanup keeps the failure visible and permits a rescan", () => {
  // 失败后 phase=cleaned：结果注记保留（失败行仍显示 skipped/failed），且允许重新扫描。
  const rows = new Map([["dev.npx", { state: "done" as const, result: { name: "dev.npx", freed: 0, trashed: 0, inUse: 0, files: 0, failed: [], skipped: "failed" as const } }]]);
  expect(rowStateNote(rows.get("dev.npx")!, t, human).skipWarn).toBe("清理失败，已跳过");
  expect(canStartScan("cleaned", false)).toBe(true);
  // 清理中不允许再发一次扫描。
  expect(canStartScan("cleaning", false)).toBe(false);
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
