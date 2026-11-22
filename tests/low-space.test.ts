import { beforeEach, expect, test } from "bun:test";
import "./setup-dom";
import { setLang } from "./setup-dom";
import type { JunkCleanEvent, SafeSpace } from "../src/mygo";
import { ruleName, t } from "../src/i18n";
import { changes, fakeStream, nodes, textOf } from "./page-harness";
import { Dialog } from "../src/ui/components/dialog";
import { LowSpaceNotice, lowSpaceClock, forgetSafeSpace } from "../src/react/status/LowSpace";
import { statusApi } from "../src/react/status/api";
import { fmtMem } from "../src/react/status/format";
import { SAFE_FRESH_MS, SAFE_MIN, safeNames } from "../src/react/status/safe-space";

beforeEach(() => {
  setLang("zh");
  forgetSafeSpace();
});

const GB = 2 ** 30;

const space = (over: Partial<SafeSpace> = {}): SafeSpace => ({
  bytes: 6.5 * GB, at: 1_800_000_000_000,
  rows: [
    { id: "dev.xcode", name: "", size: 3 * GB, recovery: "rebuild" }, { id: "dev.npm", name: "", size: 2 * GB, recovery: "download" },
    { id: "app:com.example.notes", name: "Notes", size: GB, recovery: "download" }, { id: "sys.logs", name: "", size: 0.5 * GB, recovery: "leftover" },
  ],
  ...over,
});

/** 一只可以拨的钟：量到的结果留多久看它。 */
function clockAt(env: ReturnType<typeof changes>, start = 1_000_000) {
  let now = start;
  env.assign(lowSpaceClock, { now: () => now });
  return { advance(ms: number) { now += ms; } };
}

type Notice = ReturnType<ReturnType<typeof changes>["page"]>;
/** 提示条上的按钮（确认框里的不算）。 */
const buttons = (h: Notice) => h.all().filter((n) => typeof n.props.onClick === "function" && n.props.className?.startsWith("btn ")).map((n) => textOf(n.props.children));
/** 确认框：开着没有，里面的文字和标签。 */
function confirmBox(h: Notice) {
  const box = h.ofType(Dialog)[0];
  if (!box) return { open: false, text: "", tags: [] as string[] };
  const tags = nodes(box.props.children).filter((n) => n.props.className === "tag dim").map((n) => textOf(n.props.children));
  return { open: !!box.props.open, text: textOf(box.props.children), tags };
}
/** 点提示条上的清理按钮，再在确认框里确认。 */
async function cleanConfirmed(h: Notice) {
  h.click(t("status.safe.clean", { size: fmtMem(6.5 * GB) }));
  await h.settle();
  h.click(t("clean.confirm"));
  await h.settle();
}

test("low on space, the notice measures what a scan would select and delete, and offers it with the rows it holds", async () => {
  for (const lang of ["zh", "en"] as const) {
    const env = changes();
    try {
      env.lang(lang);
      forgetSafeSpace();
      env.assign(statusApi, { safeSpace: async () => space() });
      const h = env.page(LowSpaceNotice, { free: 15 * GB, active: true, onGoClean: () => {} });
      await h.settle();
      const text = h.text();
      expect(text).toContain(t("status.alert.diskLow", { size: fmtMem(15 * GB) }));
      const names = [ruleName("dev.xcode"), ruleName("dev.npm"), "Notes"].join(t("list.sep"));
      expect(text).toContain(t("status.safe.line", { size: fmtMem(6.5 * GB), names: t("status.safe.more", { names, n: 4 }) }));
      expect(buttons(h)).toEqual([t("status.safe.clean", { size: fmtMem(6.5 * GB) }), t("status.disk.clean")]);
    } finally { env.restore(); }
  }
});

test("the rows are named up to three, and the count is added only when there are more", () => {
  expect(safeNames(space().rows.slice(0, 2), t)).toBe([ruleName("dev.xcode"), ruleName("dev.npm")].join(t("list.sep")));
  expect(safeNames(space().rows, t)).toBe(t("status.safe.more", { names: [ruleName("dev.xcode"), ruleName("dev.npm"), "Notes"].join(t("list.sep")), n: 4 }));
});

test("too little to be worth a button, or a measure that fails, leaves the plain notice and its way to the clean page", async () => {
  for (const safeSpace of [async () => space({ bytes: SAFE_MIN - 1 }), async () => { throw new Error("boom"); }]) {
    const env = changes();
    try {
      forgetSafeSpace();
      let went = 0;
      env.assign(statusApi, { safeSpace });
      const h = env.page(LowSpaceNotice, { free: 15 * GB, active: true, onGoClean: () => { went++; } });
      await h.settle();
      expect(buttons(h)).toEqual([t("status.disk.clean")]);
      expect(h.text()).toBe(t("status.alert.diskLow", { size: fmtMem(15 * GB) }) + t("status.disk.clean"));
      h.click(t("status.disk.clean"));
      expect(went).toBe(1);
    } finally { env.restore(); }
  }
});

test("nothing is measured while the page is not showing", async () => {
  const env = changes();
  try {
    let calls = 0;
    env.assign(statusApi, { safeSpace: async () => { calls++; return space(); } });
    const h = env.page(LowSpaceNotice, { free: 15 * GB, active: false, onGoClean: () => {} });
    await h.settle();
    expect(calls).toBe(0);
    h.setProps({ active: true });
    await h.settle();
    expect(calls).toBe(1);
  } finally { env.restore(); }
});

test("the offer cleans nothing by itself: it opens a list of every row with what brings it back, and cleans once that is confirmed", async () => {
  const env = changes();
  try {
    const cleanup = fakeStream<JunkCleanEvent>();
    let sent: unknown;
    env.assign(statusApi, { safeSpace: async () => space(), cleanSafe: (ids: string[], at: number) => { sent = { ids, at }; return cleanup; } });
    const h = env.page(LowSpaceNotice, { free: 15 * GB, active: true, onGoClean: () => {} });
    await h.settle();
    expect(confirmBox(h).open).toBe(false);
    h.click(t("status.safe.clean", { size: fmtMem(6.5 * GB) }));
    await h.settle();
    expect(sent).toBeUndefined();
    const box = confirmBox(h);
    expect(box.open).toBe(true);
    for (const name of [ruleName("dev.xcode"), ruleName("dev.npm"), "Notes", ruleName("sys.logs")]) expect(box.text).toContain(name);
    for (const size of [3 * GB, 2 * GB, GB, 0.5 * GB]) expect(box.text).toContain(fmtMem(size));
    expect(box.tags).toEqual([t("clean.recovery.rebuild"), t("clean.recovery.download"), t("clean.recovery.download"), t("clean.recovery.leftover")]);
    expect(box.text).toContain(t("clean.cost.download", { size: fmtMem(3 * GB) }));
    expect(box.text).toContain(t("clean.explain.deleteEffect"));
    // 取消：什么都不清。
    expect(box.text).toContain(t("btn.cancel"));
    h.ofType(Dialog)[0]!.props.onOpenChange(false);
    await h.settle();
    expect(confirmBox(h).open).toBe(false);
    expect(sent).toBeUndefined();
    expect(buttons(h)).toEqual([t("status.safe.clean", { size: fmtMem(6.5 * GB) }), t("status.disk.clean")]);
    await cleanConfirmed(h);
    expect(confirmBox(h).open).toBe(false);
    expect(sent).toEqual({ ids: ["dev.xcode", "dev.npm", "app:com.example.notes", "sys.logs"], at: 1_800_000_000_000 });
    expect(buttons(h)).toEqual([t("clean.stop")]);
    cleanup.emit({ kind: "removed", id: "dev.xcode", path: "/x", bytes: 2 * GB });
    await h.settle();
    expect(h.text()).toContain(t("status.safe.cleaning", { size: fmtMem(2 * GB), total: fmtMem(6.5 * GB) }));
    expect(h.text()).not.toContain(t("status.alert.diskLow", { size: fmtMem(15 * GB) }));
    cleanup.emit({ kind: "done", id: "dev.xcode", bytes: 3 * GB, result: { name: "dev.xcode", freed: 3 * GB, trashed: 0, files: 9, inUse: 0, failed: [] } });
    cleanup.emit({ kind: "done", id: "dev.npm", bytes: 4.5 * GB, result: { name: "dev.npm", freed: 1.5 * GB, trashed: 0, files: 9, inUse: 2, failed: [] } });
    cleanup.finish();
    await h.settle();
    // 清完以后这一条换成结果：磁盘可能已经不算不足了，不再说「只剩」。
    h.setProps({ free: 19.5 * GB });
    await h.settle();
    expect(h.text()).toContain(t("status.safe.done", { size: fmtMem(4.5 * GB), free: fmtMem(19.5 * GB) }) + t("phrase.sep") + t("clean.done.inUse", { n: 2 }));
    expect(h.text()).not.toContain(t("status.alert.diskLow", { size: fmtMem(19.5 * GB) }));
    expect(h.toasts.at(-1)).toEqual({ text: t("clean.done.freed", { size: fmtMem(4.5 * GB) }), kind: "ok" });
    expect(buttons(h)).toEqual([t("status.disk.clean")]);
  } finally { env.restore(); }
});

test("rows that all come back without a network say so in the confirmation", async () => {
  const env = changes();
  try {
    const rows = [{ id: "dev.xcode", name: "", size: 3 * GB, recovery: "rebuild" }, { id: "sys.logs", name: "", size: GB, recovery: "leftover" }];
    env.assign(statusApi, { safeSpace: async () => space({ bytes: 4 * GB, rows }) });
    const h = env.page(LowSpaceNotice, { free: 15 * GB, active: true, onGoClean: () => {} });
    await h.settle();
    h.click(t("status.safe.clean", { size: fmtMem(4 * GB) }));
    await h.settle();
    expect(confirmBox(h).text).toContain(t("clean.cost.none"));
  } finally { env.restore(); }
});

test("stopping ends the cleanup and keeps what was freed; a cleanup that fails says why", async () => {
  const env = changes();
  try {
    let cleanup = fakeStream<JunkCleanEvent>();
    env.assign(statusApi, { safeSpace: async () => space(), cleanSafe: () => cleanup });
    const h = env.page(LowSpaceNotice, { free: 15 * GB, active: true, onGoClean: () => {} });
    await h.settle();
    await cleanConfirmed(h);
    cleanup.emit({ kind: "done", id: "dev.xcode", bytes: 3 * GB, result: { name: "dev.xcode", freed: 3 * GB, trashed: 0, files: 9, inUse: 0, failed: [] } });
    await h.settle();
    h.click(t("clean.stop"));
    // 页面关掉通道，后端随后以「已取消」结束：不算出错。
    cleanup.fail(new Error("context canceled"));
    await h.settle();
    expect(cleanup.ch.closed).toBe(true);
    expect(h.text()).toContain(t("status.safe.done", { size: fmtMem(3 * GB), free: fmtMem(15 * GB) }));
    expect(h.toasts.every((x) => x.kind === "ok")).toBe(true);
  } finally { env.restore(); }
  const failing = changes();
  try {
    forgetSafeSpace();
    const cleanup = fakeStream<JunkCleanEvent>();
    failing.assign(statusApi, { safeSpace: async () => space(), cleanSafe: () => cleanup });
    const h = failing.page(LowSpaceNotice, { free: 15 * GB, active: true, onGoClean: () => {} });
    await h.settle();
    await cleanConfirmed(h);
    cleanup.fail(new Error("disk error"));
    cleanup.end();
    await h.settle();
    expect(h.toasts.at(-1)).toEqual({ text: t("clean.fail", { msg: "disk error" }), kind: "err" });
  } finally { failing.restore(); }
});

test("a measure is kept for ten minutes across visits, and taken again once it is older or a cleanup has run", async () => {
  const env = changes();
  try {
    const clock = clockAt(env);
    let calls = 0;
    const cleanup = fakeStream<JunkCleanEvent>();
    env.assign(statusApi, { safeSpace: async () => { calls++; return space(); }, cleanSafe: () => cleanup });
    const h = env.page(LowSpaceNotice, { free: 15 * GB, active: true, onGoClean: () => {} });
    await h.settle();
    expect(calls).toBe(1);
    // 离开再回来，十分钟以内：不再量。
    h.setProps({ active: false });
    await h.settle();
    clock.advance(SAFE_FRESH_MS - 1);
    h.setProps({ active: true });
    await h.settle();
    expect(calls).toBe(1);
    h.setProps({ active: false });
    await h.settle();
    clock.advance(2);
    h.setProps({ active: true });
    await h.settle();
    expect(calls).toBe(2);
    // 清理过以后，量到的已经不作数。
    await cleanConfirmed(h);
    cleanup.finish();
    await h.settle();
    h.setProps({ active: false });
    await h.settle();
    h.setProps({ active: true });
    await h.settle();
    expect(calls).toBe(3);
  } finally { env.restore(); }
});

test("the notice asks to stay while it cleans and for a moment after, even once the disk is no longer low", async () => {
  const env = changes();
  try {
    const later: (() => void)[] = [];
    env.assign(lowSpaceClock, { later: (fn: () => void) => { later.push(fn); return 0; }, cancel: () => {} });
    const cleanup = fakeStream<JunkCleanEvent>();
    const holds: boolean[] = [];
    env.assign(statusApi, { safeSpace: async () => space(), cleanSafe: () => cleanup });
    const h = env.page(LowSpaceNotice, { free: 15 * GB, active: true, onGoClean: () => {}, onHold: (hold: boolean) => { holds.push(hold); } });
    await h.settle();
    expect(holds.at(-1) ?? false).toBe(false);
    await cleanConfirmed(h);
    expect(holds.at(-1)).toBe(true);
    cleanup.emit({ kind: "done", id: "dev.xcode", bytes: 3 * GB, result: { name: "dev.xcode", freed: 3 * GB, trashed: 0, files: 9, inUse: 0, failed: [] } });
    cleanup.finish();
    await h.settle();
    // 结果先留着；过一会儿才放手。
    expect(holds.at(-1)).toBe(true);
    expect(later).toHaveLength(1);
    later[0]!();
    await h.settle();
    expect(holds.at(-1)).toBe(false);
  } finally { env.restore(); }
});

test("a notice taken off the page while it cleans no longer asks to stay", async () => {
  const env = changes();
  try {
    const cleanup = fakeStream<JunkCleanEvent>();
    const holds: boolean[] = [];
    env.assign(statusApi, { safeSpace: async () => space(), cleanSafe: () => cleanup });
    const h = env.page(LowSpaceNotice, { free: 15 * GB, active: true, onGoClean: () => {}, onHold: (hold: boolean) => { holds.push(hold); } });
    await h.settle();
    await cleanConfirmed(h);
    expect(holds.at(-1)).toBe(true);
    h.close();
    expect(holds.at(-1)).toBe(false);
  } finally { env.restore(); }
});
