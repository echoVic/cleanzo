import { beforeEach, expect, test } from "bun:test";
import "./setup-dom";
import { setLang } from "./setup-dom";
import type { CleanRecord, JunkItem, JunkScanEvent } from "../src/mygo";
import { ruleName, t } from "../src/i18n";
import { human } from "../src/shared";
import { changes, fakeStream, nodes, textOf } from "./page-harness";
import { Collapse } from "../src/ui/motion/Collapse";
import { NumberTicker } from "../src/ui/motion/NumberTicker";
import { CleanPage } from "../src/react/clean/CleanPage";
import { cleanApi } from "../src/react/clean/api";
import { buildCleanPlan, chosenSize, finalizeScan, recoveryOf, scanItem, scanStart, toggleRow } from "../src/react/clean/helpers";
import { diskScale } from "../src/react/clean/disk-scale";
import { BACK_MIN, cameBack, daysSince, historyTotals, lastCleaned, regrown, whenText } from "../src/react/clean/history";
import { CleanHistoryBody } from "../src/react/clean/CleanHistory";
import { Dialog } from "../src/ui/components/dialog";
import { GiB, goalLabel, goalPlanLines, goalSel, goalTier, parseGoal, planGoal, planSize } from "../src/react/clean/goal";

beforeEach(() => {
  setLang("zh");
});

const GB = 1024 ** 3;

const item = (id: string, group: string, size: number, extra: Partial<JunkItem> = {}): JunkItem => ({
  id, group, size, name: id, files: 1, default: true, trash: false, running: false,
  denied: false, estimate: false, inUse: 0, paths: [`~/cache/${id}`], top: [], subs: [], recovery: "download", ...extra,
});

/** 一轮完整扫描：start 全量骨架 → 逐项 measured → done。 */
function scan(items: JunkItem[]): ReturnType<typeof finalizeScan> {
  let st = scanStart(items.map((it) => ({ ...it, size: -1 })));
  for (const it of items) st = scanItem(st, it);
  return finalizeScan(st);
}

/** 挂上清理页并扫出这些行；streams 留给测试继续发清理事件。 */
async function scannedPage(env: ReturnType<typeof changes>, items: JunkItem[], over: Record<string, unknown> = {}) {
  const stream = fakeStream<JunkScanEvent>();
  env.assign(cleanApi, { startScan: () => stream, ...over });
  const h = env.page(CleanPage, {});
  await h.settle();
  h.find((n) => n.type?.name === "IdleHero")!.props.onCta();
  stream.emit({ kind: "start", items: items.map((it) => ({ ...it, size: -1 })) });
  for (const it of items) stream.emit({ kind: "item", item: it });
  stream.finish();
  await h.settle();
  return h;
}

/** 勾上或取消一条规则行。 */
function check(h: { find(pred: (n: any) => boolean): any }, id: string, on: boolean) {
  h.find((n) => n.props.ariaLabel === t("junk.select", { name: ruleName(id) }))!.props.onChange(on);
}

// ---- 清掉以后靠什么回来 ----

test("the confirmation adds up what needs a network to come back and what stays gone, the Trash-bound part apart", () => {
  const items = [
    item("dev.npm", "dev", 1024),
    item("dev.go-build", "dev", 100, { recovery: "rebuild" }),
    item("sys.logs", "system", 512, { recovery: "leftover" }),
    item("sys.trash", "system", 2048, { default: false, recovery: "gone" }),
    item("review.installers", "review", 4096, { default: false, trash: true, recovery: "gone" }),
  ];
  let st = scan(items);
  for (const it of items) st = { ...st, sel: toggleRow(it, true, st.sel) };
  expect(buildCleanPlan(st.items, st.sel)!.cost).toEqual({ download: 1024, gone: 6144, goneToTrash: 4096 });
});

test("a row whose recovery the backend did not say counts as staying gone", () => {
  expect(recoveryOf(item("dev.npm", "dev", 1, { recovery: "" }))).toBe("gone");
  expect(recoveryOf(item("dev.npm", "dev", 1, { recovery: "new-kind" }))).toBe("gone");
  expect(recoveryOf(item("dev.npm", "dev", 1, { recovery: "rebuild" }))).toBe("rebuild");
});

test("the confirmation labels each row with what brings it back and states the totals, in both languages", async () => {
  for (const lang of ["zh", "en"] as const) {
    const env = changes();
    try {
      env.lang(lang);
      const h = await scannedPage(env, [
        item("dev.npm", "dev", 3 * GB),
        item("sys.logs", "system", GB, { recovery: "leftover" }),
        item("sys.trash", "system", 2 * GB, { default: false, recovery: "gone" }),
        item("review.installers", "review", 4 * GB, { default: false, trash: true, recovery: "gone" }),
      ]);
      for (const id of ["sys.trash", "review.installers"]) check(h, id, true);
      await h.settle();
      h.click(t("clean.now"));
      await h.settle();
      const text = h.text();
      // 标签是子组件，文字在它的 props 里。
      const tags = h.all().map((n) => n.props.text);
      for (const kind of ["download", "leftover", "gone"] as const) expect(tags).toContain(t(`clean.recovery.${kind}`));
      expect(text).toContain(t("clean.cost.download", { size: human(3 * GB) }));
      expect(text).toContain(t("clean.cost.goneMixed", { size: human(6 * GB), trash: human(4 * GB) }));
      expect(text).not.toContain(t("clean.cost.none"));
    } finally { env.restore(); }
  }
});

test("a selection that costs nothing to get back says so, and one that is all Trash-bound says it can be put back", async () => {
  const env = changes();
  try {
    const h = await scannedPage(env, [
      item("dev.go-build", "dev", GB, { recovery: "rebuild" }),
      item("sys.logs", "system", GB, { recovery: "leftover" }),
      item("review.installers", "review", 4 * GB, { default: false, trash: true, recovery: "gone" }),
    ]);
    h.click(t("clean.now"));
    await h.settle();
    expect(h.text()).toContain(t("clean.cost.none"));
    check(h, "review.installers", true);
    await h.settle();
    h.click(t("clean.now"));
    await h.settle();
    expect(h.text()).toContain(t("clean.cost.goneTrash", { size: human(4 * GB) }));
    expect(h.text()).not.toContain(t("clean.cost.none"));
  } finally { env.restore(); }
});

// ---- 按目标腾空间 ----

/** 扫描自己会勾的缓存行。 */
const cache = (id: string, gb: number, extra: Partial<JunkItem> = {}) => item(id, "dev", gb * GB, extra);
/** 扫描不会勾、直接删除、清了能再下回来的行。 */
const costly = (id: string, gb: number, extra: Partial<JunkItem> = {}) => item(id, "dev", gb * GB, { default: false, ...extra });
/** 废纸篓：直接删除，不会自己回来。 */
const bin = (gb: number) => item("sys.trash", "system", gb * GB, { default: false, recovery: "gone" });
/** 先进废纸篓的行。 */
const trashBound = (id: string, gb: number, extra: Partial<JunkItem> = {}) =>
  item(id, "review", gb * GB, { default: false, trash: true, recovery: "gone", ...extra });

test("rows fall into four tiers: preselected caches, costly rows that come back, the Trash, and rows bound for the Trash", () => {
  expect(goalTier(cache("dev.npm", 1))).toBe(0);
  expect(goalTier(costly("dev.gradle", 1))).toBe(1);
  expect(goalTier(bin(1))).toBe(2);
  expect(goalTier(trashBound("review.installers", 1))).toBe(3);
  // 扫描自己会勾、但先进废纸篓的自定义目录：也要清空废纸篓才腾出空间。
  expect(goalTier(item("custom:/x", "custom", GB, { trash: true, recovery: "gone" }))).toBe(3);
});

test("a goal inside what a scan selects narrows to the fewest rows, the last one the smallest that reaches it", () => {
  const plan = planGoal([cache("a", 4), cache("b", 3), cache("c", 2), cache("d", 1)], 5 * GB);
  expect(plan).toEqual({ target: 5 * GB, ids: ["a", "d"], now: 5 * GB, later: 0, look: 0, rough: 0, reach: null });
});

test("past the preselected rows a goal takes costly rows that come back, the smallest that reaches it", () => {
  const plan = planGoal([cache("a", 6), costly("dev.gradle", 8), costly("dev.playwright", 1.5), bin(3)], 7 * GB);
  expect(plan.ids).toEqual(["a", "dev.playwright"]);
  expect(plan.look).toBe(1);
  expect(plan.now).toBe(7.5 * GB);
});

test("the Trash is emptied only when rows that come back fall short, and a row it makes unnecessary is dropped", () => {
  const plan = planGoal([cache("a", 6), costly("dev.playwright", 2), bin(3), trashBound("review.installers", 5)], 9 * GB);
  expect(plan.ids).toEqual(["a", "sys.trash"]);
  expect(plan).toMatchObject({ now: 9 * GB, later: 0, look: 1, reach: null });
});

test("rows bound for the Trash come last and their bytes are counted apart, since they free nothing yet", () => {
  const items = [cache("a", 6), trashBound("review.installers", 5), bin(1)];
  const plan = planGoal(items, 11.5 * GB);
  expect(plan.ids).toEqual(["a", "sys.trash", "review.installers"]);
  expect(plan).toMatchObject({ now: 7 * GB, later: 5 * GB, look: 2, reach: null });
  // 10 GB 用不着清空废纸篓：去掉它仍然够，就不勾。
  expect(planGoal(items, 10 * GB)).toMatchObject({ ids: ["a", "review.installers"], now: 6 * GB, later: 5 * GB, look: 1 });
});

test("idle projects go longest untouched first, whatever their size", () => {
  const project = (id: string, gb: number, idleDays: number) =>
    item(`project:/${id}`, "projects", gb * GB, { default: false, trash: true, idleDays });
  const plan = planGoal([cache("a", 6), project("big", 5, 20), project("old", 1, 300), project("mid", 1, 90)], 6.5 * GB);
  expect(plan.ids).toEqual(["a", "project:/old"]);
  expect(plan.later).toBe(GB);
});

test("a goal out of reach selects only what a scan selects by itself and says how much could be reached", () => {
  const plan = planGoal([cache("a", 6), costly("dev.playwright", 2), bin(3)], 50 * GB);
  expect(plan).toEqual({ target: 50 * GB, ids: ["a"], now: 6 * GB, later: 0, look: 0, rough: 0, reach: 11 * GB });
});

test("rows that cannot be cleaned now never enter a plan", () => {
  const plan = planGoal([
    cache("a", 1), cache("running", 9, { running: true }), cache("denied", 9, { denied: true }),
    cache("broken", 9, { problem: "tool-failed" }), cache("empty", 0),
  ], GB);
  expect(plan.ids).toEqual(["a"]);
  expect(planGoal([cache("running", 9, { running: true })], GB)).toMatchObject({ ids: [], reach: 0 });
});

test("a row with sub-items counts the ones a click on the row would select, and the plan selects exactly those", () => {
  const support = item("review.device-support", "review", 3 * GB, { default: false, recovery: "rebuild", subs: [
    { path: "/old", name: "17.0", detail: "iOS", size: GB, default: true },
    { path: "/new", name: "18.0", detail: "iOS", size: 2 * GB, default: false },
  ] });
  expect(planSize(support)).toBe(GB);
  const items = [cache("a", 2), support];
  const plan = planGoal(items, 3 * GB);
  expect(plan.ids).toEqual(["a", "review.device-support"]);
  const sel = goalSel(items, plan);
  expect([...sel.subSel.get(support.id)!]).toEqual(["/old"]);
  expect(items.reduce((n, it) => n + chosenSize(it, sel), 0)).toBe(plan.now + plan.later);
});

test("rows whose size is only an upper bound are counted on last within their class, and the plan says when it leans on one", () => {
  const items = [cache("a", 3), cache("brew", 5, { estimate: true }), cache("b", 2)];
  // 5 GB 用量准的两行就够：不动只知道上限的那一行。
  expect(planGoal(items, 5 * GB)).toMatchObject({ ids: ["a", "b"], rough: 0 });
  const plan = planGoal(items, 8 * GB);
  expect(plan).toMatchObject({ ids: ["a", "b", "brew"], rough: 1 });
  for (const lang of ["zh", "en"] as const) {
    setLang(lang);
    expect(goalPlanLines(plan, t, human)).toContain(t("goal.plan.rough", { n: 1 }));
    expect(goalPlanLines(planGoal(items, 5 * GB), t, human)).not.toContain(t("goal.plan.rough", { n: 1 }));
  }
  setLang("zh");
});

test("a custom amount is a number of gigabytes from 1 up; anything else is no goal", () => {
  expect(parseGoal(" 25 ")).toBe(25);
  expect(parseGoal("2.5")).toBe(2.5);
  for (const bad of ["", "0", "-3", "abc", "1e9", "0.5"]) expect(parseGoal(bad)).toBeNull();
  expect(GiB).toBe(GB);
  expect([goalLabel(10 * GB), goalLabel(2.5 * GB), goalLabel(1.239 * GB)]).toEqual(["10 GB", "2.5 GB", "1.24 GB"]);
});

type Page = Awaited<ReturnType<typeof scannedPage>>;

/** 计划卡片是不是展开着；它的文字。 */
function planCard(h: Page): { open: boolean; text: string } {
  const card = h.ofType(Collapse).find((c) => h.all().some((n) => n.props.className === "goal-plan" && textOf(c.props.children).includes(textOf(n.props.children))));
  return { open: !!card?.props.open, text: card ? textOf(card.props.children) : "" };
}
const headline = (h: Page) => h.ofType(NumberTicker)[0]!.props.value;
const chip = (h: Page, label: string) => h.find((n) => n.props.className === "goal-chip" && textOf(n.props.children) === label)!;

test("pressing an amount selects just enough, says what the plan took, and marks the amount as the one in force", async () => {
  const env = changes();
  try {
    const h = await scannedPage(env, [cache("dev.npm", 6), cache("dev.pip", 3), costly("dev.playwright", 2), bin(3)]);
    expect(headline(h)).toBe(9 * GB);
    expect(planCard(h).open).toBe(false);
    h.click("5 GB");
    await h.settle();
    expect(headline(h)).toBe(6 * GB);
    expect(planCard(h)).toEqual({ open: true, text: [
      t("goal.plan.done", { target: "5 GB", n: 1, size: human(6 * GB) }), t("goal.plan.safe"), t("goal.undo"),
    ].join("") });
    expect(chip(h, "5 GB").props["aria-pressed"]).toBe(true);
    expect(chip(h, "10 GB").props["aria-pressed"]).toBe(false);
    h.click("10 GB");
    await h.settle();
    expect(headline(h)).toBe(11 * GB);
    expect(planCard(h).text).toContain(t("goal.plan.done", { target: "10 GB", n: 3, size: human(11 * GB) }));
    expect(planCard(h).text).toContain(t("goal.plan.look", { n: 1 }));
  } finally { env.restore(); }
});

test("undo puts back the selection from before the first plan, and a change by hand takes the plan card away", async () => {
  const env = changes();
  try {
    const h = await scannedPage(env, [cache("dev.npm", 6), cache("dev.pip", 3), costly("dev.playwright", 2)]);
    check(h, "dev.npm", false);
    await h.settle();
    expect(headline(h)).toBe(3 * GB);
    h.click("5 GB");
    await h.settle();
    h.click("10 GB");
    await h.settle();
    expect(headline(h)).toBe(11 * GB);
    h.click(t("goal.undo"));
    await h.settle();
    expect(headline(h)).toBe(3 * GB);
    expect(planCard(h).open).toBe(false);
    h.click("5 GB");
    await h.settle();
    expect(planCard(h).open).toBe(true);
    check(h, "dev.pip", true);
    await h.settle();
    expect(planCard(h).open).toBe(false);
    expect(chip(h, "5 GB").props["aria-pressed"]).toBe(false);
    expect(headline(h)).toBe(9 * GB);
  } finally { env.restore(); }
});

test("an amount out of reach says how far short it falls and checks only what a scan selects by itself", async () => {
  const env = changes();
  try {
    const h = await scannedPage(env, [cache("dev.npm", 6), costly("dev.playwright", 2), trashBound("review.installers", 4)]);
    h.click("50 GB");
    await h.settle();
    expect(headline(h)).toBe(6 * GB);
    expect(planCard(h).text).toContain(t("goal.plan.short", { target: "50 GB", reach: human(12 * GB), short: human(38 * GB) }));
    expect(planCard(h).text).toContain(t("goal.plan.shortSafe", { size: human(6 * GB) }));
  } finally { env.restore(); }
});

test("a plan that sends rows to the Trash says that part is freed only once the Trash is emptied", async () => {
  const env = changes();
  try {
    const h = await scannedPage(env, [cache("dev.npm", 6), trashBound("review.installers", 5)]);
    h.click("10 GB");
    await h.settle();
    expect(planCard(h).text).toContain(t("goal.plan.later", { size: human(5 * GB) }));
    expect(planCard(h).text).toContain(t("goal.plan.look", { n: 1 }));
  } finally { env.restore(); }
});

test("a custom amount applies when submitted, and one that is no amount changes nothing", async () => {
  const env = changes();
  try {
    const h = await scannedPage(env, [cache("dev.npm", 6), cache("dev.pip", 3), cache("dev.uv", 2)]);
    const type = (text: string) => h.find((n) => n.props.className === "goal-input")!.props.onChange({ target: { value: text } });
    const submit = () => h.find((n) => n.props.className === "goal-custom")!.props.onSubmit({ preventDefault() {} });
    type("abc");
    await h.settle();
    submit();
    await h.settle();
    expect(planCard(h).open).toBe(false);
    expect(headline(h)).toBe(11 * GB);
    type("8");
    await h.settle();
    submit();
    await h.settle();
    expect(headline(h)).toBe(8 * GB);
    expect(planCard(h).text).toContain(t("goal.plan.done", { target: "8 GB", n: 2, size: human(8 * GB) }));
    // 自定的量不是哪个档位：没有档位亮着。
    for (const label of ["5 GB", "10 GB", "20 GB", "50 GB"]) expect(chip(h, label).props["aria-pressed"]).toBe(false);
  } finally { env.restore(); }
});

test("the amounts show once a scan found something that can be cleaned, and go when cleaning starts", async () => {
  const env = changes();
  try {
    const cleanup = fakeStream<import("../src/mygo").JunkCleanEvent>();
    const h = await scannedPage(env, [cache("dev.npm", 6)], { startClean: () => cleanup });
    const shown = () => h.all().some((n) => n.props.className === "goal-chip");
    expect(shown()).toBe(true);
    h.click(t("clean.now"));
    await h.settle();
    h.click(t("clean.confirm"));
    await h.settle();
    expect(shown()).toBe(false);
    cleanup.finish();
    await h.settle();
    expect(shown()).toBe(false);
  } finally { env.restore(); }
  const empty = changes();
  try {
    const h = await scannedPage(empty, [cache("dev.npm", 6, { running: true })]);
    expect(h.all().some((n) => n.props.className === "goal-chip")).toBe(false);
  } finally { empty.restore(); }
});

// ---- 整盘比例 ----

test("candidates are placed inside the whole disk: other used space, unselected candidates, the selection, and what is free", () => {
  const disk = { total: 500 * GB, used: 400 * GB, free: 100 * GB };
  expect(diskScale(disk, 40 * GB, 15 * GB, 10 * GB)).toEqual({
    other: 72, rest: 5, picked: 3, restBytes: 25 * GB, pickedBytes: 15 * GB, after: 110 * GB,
  });
});

test("readings that disagree are capped rather than drawn past the disk, and a disk that cannot be read draws nothing", () => {
  const disk = { total: 100 * GB, used: 30 * GB, free: 70 * GB };
  // 候选比已用还多、勾选比候选还多：都按上一层封顶。
  expect(diskScale(disk, 50 * GB, 80 * GB, 80 * GB)).toEqual({
    other: 0, rest: 0, picked: 30, restBytes: 0, pickedBytes: 30 * GB, after: 100 * GB,
  });
  expect(diskScale(null, GB, GB, GB)).toBeNull();
  expect(diskScale({ total: 0, used: 0, free: 0 }, GB, GB, GB)).toBeNull();
});

/** 比例条各段的宽度和图例里的文字。 */
function scaleOf(h: Page) {
  const width = (cls: string) => h.find((n) => n.props.className === cls)?.props.style?.width;
  const key = h.find((n) => n.props.className === "disk-scale-key");
  return { other: width("ds-other"), rest: width("ds-rest"), picked: width("ds-pick"), key: key ? textOf(key.props.children) : null };
}

test("after a scan the hero shows the selection inside the whole disk, and what would be free after cleaning", async () => {
  const env = changes();
  try {
    const disk = { total: 500 * GB, used: 400 * GB, free: 100 * GB };
    const h = await scannedPage(env, [cache("dev.npm", 10), costly("dev.playwright", 5), trashBound("review.installers", 25)], { disk: async () => disk });
    expect(scaleOf(h)).toEqual({ other: "72%", rest: "6%", picked: "2%", key: [
      t("disk.scale.total", { total: human(500 * GB) }), t("disk.scale.picked", { size: human(10 * GB) }),
      t("disk.scale.rest", { size: human(30 * GB) }), t("disk.scale.free", { size: human(100 * GB) }),
      t("disk.scale.after", { size: human(110 * GB) }),
    ].join("") });
    // 进废纸篓的不算进「清理后」：清空废纸篓以后才腾出来。
    check(h, "review.installers", true);
    await h.settle();
    expect(scaleOf(h)).toMatchObject({ rest: "1%", picked: "7%" });
    expect(scaleOf(h).key).toContain(t("disk.scale.after", { size: human(110 * GB) }));
    // 一行都不勾：没有「清理后」。
    check(h, "review.installers", false);
    check(h, "dev.npm", false);
    await h.settle();
    expect(scaleOf(h).key).not.toContain(t("disk.scale.after", { size: human(100 * GB) }));
    expect(scaleOf(h).picked).toBe("0%");
  } finally { env.restore(); }
});

test("a disk that cannot be read leaves the bar out and the page as it was", async () => {
  const env = changes();
  try {
    const h = await scannedPage(env, [cache("dev.npm", 10)], { disk: async () => { throw new Error("no disk"); } });
    expect(scaleOf(h).key).toBeNull();
    expect(headline(h)).toBe(10 * GB);
    expect(h.all().some((n) => n.props.className === "goal-chip")).toBe(true);
  } finally { env.restore(); }
});

test("once a clean ends the disk is read again, and the bar shows the new free space without the old candidates", async () => {
  const env = changes();
  try {
    const cleanup = fakeStream<import("../src/mygo").JunkCleanEvent>();
    const disk = { total: 500 * GB, used: 400 * GB, free: 100 * GB };
    const h = await scannedPage(env, [cache("dev.npm", 10)], { disk: async () => ({ ...disk }), startClean: () => cleanup });
    h.click(t("clean.now"));
    await h.settle();
    h.click(t("clean.confirm"));
    await h.settle();
    disk.used -= 10 * GB;
    disk.free += 10 * GB;
    cleanup.emit({ kind: "done", id: "dev.npm", bytes: 10 * GB, result: { name: "dev.npm", freed: 10 * GB, trashed: 0, files: 1, inUse: 0, failed: [] } });
    cleanup.finish();
    await h.settle();
    expect(scaleOf(h)).toEqual({ other: "78%", rest: "0%", picked: "0%", key: [
      t("disk.scale.total", { total: human(500 * GB) }), t("disk.scale.free", { size: human(110 * GB) }),
    ].join("") });
  } finally { env.restore(); }
});

// ---- 清理记录 ----

const DAY = 86400;
/** 本地时间的某一天中午（秒），免得时区让「昨天」跨不过去。 */
const noon = (day: number) => new Date(2026, 10, day, 12).getTime() / 1000;
const record = (at: number, rows: CleanRecord["rows"], source = "clean"): CleanRecord => ({
  at, source, rows, freed: rows.reduce((n, r) => n + r.freed, 0), trashed: rows.reduce((n, r) => n + r.trashed, 0),
});

test("days are counted by the calendar: late last night is yesterday, and the words follow", () => {
  expect(daysSince(noon(22), noon(22))).toBe(0);
  expect(daysSince(new Date(2026, 10, 21, 23, 50).getTime() / 1000, new Date(2026, 10, 22, 0, 10).getTime() / 1000)).toBe(1);
  expect(daysSince(noon(12), noon(22))).toBe(10);
  // 时钟被调回去过：记录比现在还晚，算今天。
  expect(daysSince(noon(23), noon(22))).toBe(0);
  for (const lang of ["zh", "en"] as const) {
    setLang(lang);
    expect(whenText(noon(22), noon(22), t)).toBe(t("history.when.today"));
    expect(whenText(noon(21), noon(22), t)).toBe(t("history.when.yesterday"));
    expect(whenText(noon(12), noon(22), t)).toBe(t("history.when.days", { n: 10 }));
  }
});

test("the last time a row was cleaned is its newest record that removed something, deleted and trashed together", () => {
  const history = [
    record(noon(22), [{ id: "dev.pip", freed: GB, trashed: 0 }]),
    record(noon(20), [{ id: "dev.npm", freed: 3 * GB, trashed: 0 }, { id: "review.installers", freed: 0, trashed: 2 * GB }]),
    record(noon(10), [{ id: "dev.npm", freed: 9 * GB, trashed: 0 }]),
  ];
  expect(lastCleaned(history, "dev.npm")).toEqual({ at: noon(20), bytes: 3 * GB });
  expect(lastCleaned(history, "review.installers")).toEqual({ at: noon(20), bytes: 2 * GB });
  expect(lastCleaned(history, "dev.uv")).toBeNull();
});

test("a row grew back when, within two weeks of a cleanup, it holds most of what was cleaned again and no trifle; the Trash never counts", () => {
  const last = { at: noon(12), bytes: 4 * GB };
  expect(cameBack(last, cache("dev.npm", 3.2), noon(22))).toBe(true);
  expect(cameBack(last, cache("dev.npm", 3.1), noon(22))).toBe(false);
  expect(cameBack({ at: noon(22) - 14 * DAY, bytes: 4 * GB }, cache("dev.npm", 4), noon(22))).toBe(true);
  expect(cameBack({ at: noon(22) - 15 * DAY, bytes: 4 * GB }, cache("dev.npm", 4), noon(22))).toBe(false);
  expect(cameBack(null, cache("dev.npm", 4), noon(22))).toBe(false);
  // 上次只清了一点：现在和那时一样多也不到下限，不算长回来。
  expect(cameBack({ at: noon(12), bytes: BACK_MIN - 1 }, item("dev.npm", "dev", BACK_MIN - 1), noon(22))).toBe(false);
  expect(cameBack({ at: noon(12), bytes: BACK_MIN }, item("dev.npm", "dev", BACK_MIN), noon(22))).toBe(true);
  expect(cameBack(last, bin(4), noon(22))).toBe(false);
  // 运行中的行量到了、只是现在不能清：照样算。
  expect(cameBack(last, item("dev.npm", "dev", 0, { running: true, foundSize: 3.5 * GB }), noon(22))).toBe(true);
});

test("the record's totals cover the last thirty days, and the rows that grew back are listed largest first", () => {
  const history = [
    record(noon(21), [{ id: "dev.npm", freed: 3 * GB, trashed: 0 }, { id: "review.installers", freed: 0, trashed: 2 * GB }]),
    record(noon(15), [{ id: "dev.pip", freed: GB, trashed: 0 }], "low-space"),
    record(noon(22) - 40 * DAY, [{ id: "dev.uv", freed: 9 * GB, trashed: 0 }]),
  ];
  expect(historyTotals(history, noon(22))).toEqual({ count: 2, freed: 4 * GB, trashed: 2 * GB });
  const items = [cache("dev.npm", 2.5), cache("dev.pip", 3), cache("dev.uv", 9)];
  expect(regrown(history, items, noon(22)).map((r) => [r.item.id, r.cleaned, r.size])).toEqual([
    ["dev.pip", GB, 3 * GB], ["dev.npm", 3 * GB, 2.5 * GB],
  ]);
});

test("the record lists each cleanup with where it started, what it removed and the rows that grew back, in both languages", async () => {
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const history = [
    record(noon(21), [{ id: "dev.npm", freed: 3 * GB, trashed: 0 }, { id: "app:com.example.notes", name: "Notes", freed: 0, trashed: 2 * GB }]),
    record(noon(15), [{ id: "dev.pip", freed: GB, trashed: 0 }], "low-space"),
    record(noon(14), [{ id: "sys.trash", freed: 5 * GB, trashed: 0 }], "trash-reminder"),
  ];
  for (const lang of ["zh", "en"] as const) {
    setLang(lang);
    const html = renderToStaticMarkup(createElement(CleanHistoryBody, { history, items: [cache("dev.npm", 2.5)], now: noon(22) }));
    expect(html).toContain(t("history.summary", { n: 3, size: human(9 * GB) }));
    expect(html).toContain(t("history.summaryTrash", { size: human(2 * GB) }));
    expect(html).toContain(t("history.backTitle"));
    expect(html).toContain(t("history.backRow", { when: t("history.when.yesterday"), cleaned: human(3 * GB), size: human(2.5 * GB) }));
    expect(html).toContain(t("history.source.low-space"));
    expect(html).toContain(t("history.source.trash-reminder"));
    expect(html).toContain(ruleName("dev.npm"));
    expect(html).toContain("Notes");
    expect(html).toContain(t("clean.done.freed", { size: human(3 * GB) }));
    expect(html).toContain(t("cat.trashed", { size: human(2 * GB) }));
    expect(html).toContain(t("history.local"));
    const empty = renderToStaticMarkup(createElement(CleanHistoryBody, { history: [], items: [], now: noon(22) }));
    expect(empty).toContain(t("history.empty"));
    expect(empty).not.toContain(t("history.backTitle"));
  }
  setLang("zh");
});

/** 记录弹窗是不是开着。 */
const historyOpen = (h: Page) => !!h.ofType(Dialog).find((d) => nodes(d.props.children).some((n) => n.type === CleanHistoryBody))?.props.open;

test("a row cleaned before says when and how much, and is marked when it grew back", async () => {
  const env = changes();
  try {
    const now = Date.now() / 1000;
    const history = [record(now - 3 * DAY, [{ id: "dev.npm", freed: 4 * GB, trashed: 0 }, { id: "dev.pip", freed: 4 * GB, trashed: 0 }])];
    const h = await scannedPage(env, [cache("dev.npm", 3.5), cache("dev.pip", 1), cache("dev.uv", 2)], { history: async () => history });
    const note = t("history.row", { when: t("history.when.days", { n: 3 }), size: human(4 * GB) });
    const notes = h.all().filter((n) => n.props.className === "cat-sub muted history-note");
    expect(notes.map((n) => textOf(n.props.children))).toEqual([note, note]);
    // 只有差不多长满了的那一行带「又长回来了」的标签。
    expect(notes.map((n) => nodes(n.props.children).filter((x) => x.props.text === t("history.back")).length)).toEqual([1, 0]);
  } finally { env.restore(); }
});

test("before a scan the page says when the last cleanup was, and the record opens from there", async () => {
  const env = changes();
  try {
    const now = Date.now() / 1000;
    const history = [record(now - DAY, [{ id: "dev.npm", freed: 4 * GB, trashed: 0 }]), record(now - 9 * DAY, [{ id: "dev.pip", freed: GB, trashed: 0 }])];
    env.assign(cleanApi, { history: async () => history });
    const h = env.page(CleanPage, {});
    await h.settle();
    expect(h.text()).toContain(t("history.last", { when: t("history.when.yesterday"), size: human(4 * GB) }));
    expect(historyOpen(h)).toBe(false);
    h.click(t("history.open"));
    await h.settle();
    expect(historyOpen(h)).toBe(true);
    expect(h.ofType(CleanHistoryBody)[0]!.props.history).toBe(history);
  } finally { env.restore(); }
});

test("with nothing on record the page shows no line about a last cleanup and no way into an empty record", async () => {
  const env = changes();
  try {
    env.assign(cleanApi, { history: async () => [] });
    const h = env.page(CleanPage, {});
    await h.settle();
    expect(h.all().some((n) => typeof n.props.onClick === "function" && textOf(n.props.children) === t("history.open"))).toBe(false);
  } finally { env.restore(); }
});

test("clearing the record empties it on the page and says so; a failure keeps it and says why", async () => {
  const env = changes();
  try {
    const now = Date.now() / 1000;
    let stored = [record(now - DAY, [{ id: "dev.npm", freed: 4 * GB, trashed: 0 }])];
    let fail = true;
    env.assign(cleanApi, {
      history: async () => stored,
      forgetHistory: async () => { if (fail) throw new Error("disk full"); stored = []; },
    });
    const h = env.page(CleanPage, {});
    await h.settle();
    h.click(t("history.open"));
    await h.settle();
    h.click(t("history.forget"));
    await h.settle();
    expect(h.toasts.at(-1)).toEqual({ text: t("history.forgetFail", { msg: "disk full" }), kind: "err" });
    expect(h.ofType(CleanHistoryBody)[0]!.props.history).toHaveLength(1);
    fail = false;
    h.click(t("history.forget"));
    await h.settle();
    expect(h.toasts.at(-1)).toEqual({ text: t("history.forgot"), kind: "ok" });
    expect(h.ofType(CleanHistoryBody)[0]!.props.history).toHaveLength(0);
    expect(historyOpen(h)).toBe(false);
  } finally { env.restore(); }
});

test("once a clean ends the record is read again, so the cleanup just run is in it", async () => {
  const env = changes();
  try {
    const cleanup = fakeStream<import("../src/mygo").JunkCleanEvent>();
    let stored: CleanRecord[] = [];
    const h = await scannedPage(env, [cache("dev.npm", 10)], { history: async () => stored, startClean: () => cleanup });
    h.click(t("clean.now"));
    await h.settle();
    h.click(t("clean.confirm"));
    await h.settle();
    stored = [record(Date.now() / 1000, [{ id: "dev.npm", freed: 10 * GB, trashed: 0 }])];
    cleanup.emit({ kind: "done", id: "dev.npm", bytes: 10 * GB, result: { name: "dev.npm", freed: 10 * GB, trashed: 0, files: 1, inUse: 0, failed: [] } });
    cleanup.finish();
    await h.settle();
    expect(h.ofType(CleanHistoryBody)[0]!.props.history).toBe(stored);
    // 刚清完的行不再写「今天清过」：结果已经写在这一行上。
    expect(h.all().some((n) => n.props.className === "cat-sub muted history-note")).toBe(false);
  } finally { env.restore(); }
});

test("a scan reads the record again, so a cleanup made from another page shows on its rows", async () => {
  const env = changes();
  try {
    let stored: CleanRecord[] = [];
    const stream = fakeStream<JunkScanEvent>();
    env.assign(cleanApi, { startScan: () => stream, history: async () => stored });
    const h = env.page(CleanPage, {});
    await h.settle();
    // 进页面时还没有记录；之后在首页清过一次。
    stored = [record(Date.now() / 1000, [{ id: "dev.npm", freed: 4 * GB, trashed: 0 }], "low-space")];
    h.find((n) => n.type?.name === "IdleHero")!.props.onCta();
    const npm = cache("dev.npm", 1);
    stream.emit({ kind: "start", items: [{ ...npm, size: -1 }] });
    stream.emit({ kind: "item", item: npm });
    stream.finish();
    await h.settle();
    const notes = h.all().filter((n) => n.props.className === "cat-sub muted history-note").map((n) => textOf(n.props.children));
    expect(notes).toEqual([t("history.row", { when: t("history.when.today"), size: human(4 * GB) })]);
  } finally { env.restore(); }
});
