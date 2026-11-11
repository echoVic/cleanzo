// 页面测试工具自身：渲染停不下来时让测试失败，关页面、还原改动时不把全局状态留给后面的测试。
import { expect, test } from "bun:test";
import * as React from "react";
import { changes, pageHarness } from "./page-harness";

/** effect 改几次状态、再等一个异步结果，之后不再变。 */
function Settles() {
  const [n, setN] = React.useState(0);
  const [loaded, setLoaded] = React.useState("");
  React.useEffect(() => { if (n < 3) setN(n + 1); }, [n]);
  React.useEffect(() => { void Promise.resolve("loaded").then(setLoaded); }, []);
  return React.createElement("p", null, `${n} ${loaded}`);
}

/** 每次渲染后 effect 都改状态。 */
function EffectLoop() {
  const [, setSpin] = React.useState(0);
  React.useEffect(() => { setSpin((n) => n + 1); });
  return null;
}

/** 每次渲染后都起一个异步回调改状态。 */
function AsyncLoop() {
  const [, setSpin] = React.useState(0);
  React.useEffect(() => { void Promise.resolve().then(() => setSpin((n) => n + 1)); });
  return null;
}

/** 渲染期间就改状态。 */
function RenderLoop() {
  const [n, setN] = React.useState(0);
  setN(n + 1);
  return null;
}

test("a page that settles renders its last state and does not throw", async () => {
  const env = changes();
  try {
    const page = env.page(Settles, {});
    await page.settle();
    expect(page.text()).toBe("3 loaded");
  } finally { env.restore(); }
});

for (const [name, Component] of [["sets state in an effect on every render", EffectLoop], ["sets state from an async callback after every render", AsyncLoop], ["sets state while rendering", RenderLoop]] as const) {
  test(`settle fails a page that ${name}`, async () => {
    const env = changes();
    try {
      const page = env.page(Component, {});
      await expect(page.settle()).rejects.toThrow("页面渲染未收敛");
    } finally { env.restore(); }
  });
}

test("close puts back the hooks and globals even when an effect cleanup throws", async () => {
  const useState = React.useState;
  const doc = globalThis.document;
  const win = (globalThis as { window?: unknown }).window;
  let later = 0;
  function Cleanups() {
    React.useEffect(() => () => { throw new Error("cleanup broke"); }, []);
    React.useEffect(() => () => { later++; }, []);
    return null;
  }
  const page = pageHarness(Cleanups, {});
  await page.settle();
  expect(globalThis.document).not.toBe(doc);
  expect(() => page.close()).toThrow("cleanup broke");
  expect(later).toBe(1);
  expect(React.useState).toBe(useState);
  expect(globalThis.document).toBe(doc);
  expect((globalThis as { window?: unknown }).window).toBe(win);
});

test("assign removes on restore a member that was not there before, and puts back one that was", () => {
  const g = globalThis as { czHarnessProbe?: unknown };
  const api = { load: () => "real" };
  const env = changes();
  try {
    env.assign(g, { czHarnessProbe: 1 });
    env.assign(api, { load: () => "fake" });
    expect(g.czHarnessProbe).toBe(1);
    expect(api.load()).toBe("fake");
  } finally { env.restore(); }
  expect("czHarnessProbe" in globalThis).toBe(false);
  expect(api.load()).toBe("real");
});

test("restore runs every step and reports a failing one later, so it does not hide the test's own error", () => {
  const ran: string[] = [];
  const queued: (() => void)[] = [];
  const env = changes();
  env.add(() => { ran.push("first"); });
  env.add(() => { throw new Error("restore broke"); });
  env.add(() => { ran.push("last"); });
  const queue = globalThis.queueMicrotask;
  globalThis.queueMicrotask = (fn) => { queued.push(fn); };
  try {
    // 同步不抛：finally 里正在传出的测试错误不会被换掉。
    expect(() => env.restore()).not.toThrow();
  } finally { globalThis.queueMicrotask = queue; }
  expect(ran).toEqual(["last", "first"]);
  expect(queued).toHaveLength(1);
  expect(() => queued[0]!()).toThrow("restore broke");
});
