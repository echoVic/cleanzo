import { beforeEach, expect, test } from "bun:test";
import "./setup-dom";
import { setLang } from "./setup-dom";
import type { JunkItem, JunkScanEvent } from "../src/mygo";
import { ruleName, t } from "../src/i18n";
import { human } from "../src/shared";
import { changes, fakeStream, textOf } from "./page-harness";
import { Collapse } from "../src/ui/motion/Collapse";
import { NumberTicker } from "../src/ui/motion/NumberTicker";
import { CleanPage } from "../src/react/clean/CleanPage";
import { cleanApi } from "../src/react/clean/api";
import { buildCleanPlan, chosenSize, finalizeScan, recoveryOf, scanItem, scanStart, toggleRow } from "../src/react/clean/helpers";
import { GiB, goalLabel, goalSel, goalTier, parseGoal, planGoal, planSize } from "../src/react/clean/goal";

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
  expect(plan).toEqual({ target: 5 * GB, ids: ["a", "d"], now: 5 * GB, later: 0, look: 0, reach: null });
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
  expect(plan).toEqual({ target: 50 * GB, ids: ["a"], now: 6 * GB, later: 0, look: 0, reach: 11 * GB });
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
