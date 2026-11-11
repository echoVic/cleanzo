// 无浏览器的页面测试：接管 React 的 hook，把页面组件当函数调用，按依赖执行 effect，
// 再从返回的元素树里找按钮、读文字、调回调。子组件不执行，只看传给它们的 props。
// 桥接由各测试在页面的 api 对象上替换；最小 DOM 不能真正挂载，这里不验证布局、焦点和动画。
// 测试对全局状态的改动都记在 changes() 里，搭建放进 try，finally 里一次还原。
import "./setup-dom";
import * as React from "react";
import { spyOn } from "bun:test";
import { getPref, setPref, type LangPref } from "../src/i18n";

type Slot = { value?: unknown; deps?: readonly unknown[]; cleanup?: () => void };
type El = React.ReactElement<Record<string, any>> & { type: any };

const changed = (before: readonly unknown[] | undefined, after: readonly unknown[] | undefined) =>
  !before || !after || before.length !== after.length || before.some((v, i) => !Object.is(v, after[i]));

/** 树里所有元素（含子组件元素和它们 props.children 里的元素）。 */
export function nodes(node: React.ReactNode): El[] {
  if (Array.isArray(node)) return node.flatMap(nodes);
  if (!React.isValidElement<Record<string, any>>(node)) return [];
  return [node as El, ...nodes(node.props.children)];
}

/** 一个节点里所有的文字。 */
export function textOf(node: React.ReactNode): string {
  if (Array.isArray(node)) return node.map(textOf).join("");
  if (React.isValidElement<{ children?: React.ReactNode }>(node)) return textOf(node.props.children);
  return typeof node === "string" || typeof node === "number" ? String(node) : "";
}

/** 最后一条 toast 的文字和类型。 */
export interface Toasted { text: string; kind: string }

/** 页面连着重新渲染超过这么多遍（一次渲染里，或 settle 里连着的几轮）就算停不下来。 */
const MAX_RERENDERS = 20;
const notConverging = () => new Error("页面渲染未收敛");

export function pageHarness<P extends object>(Component: (p: P) => React.ReactNode, initial: P) {
  const slots: Slot[] = [];
  let pending: { slot: Slot; effect: () => unknown; deps?: readonly unknown[] }[] = [];
  let cursor = 0;
  let dirty = true;
  let rendering = false;
  let props = initial;
  let tree: React.ReactNode = null;
  const toasts: Toasted[] = [];
  const toastEl = {
    textContent: "",
    className: "",
    setAttribute() {},
    classList: { remove() {} },
  };
  const slot = () => (slots[cursor++] ??= {});

  const useEffect = (effect: () => unknown, deps?: readonly unknown[]) => {
    const s = slot();
    if (changed(s.deps, deps)) pending.push({ slot: s, effect, deps });
  };
  const spies = [
    spyOn(React, "useState").mockImplementation(((init: unknown) => {
      const s = slots[cursor] ?? (slots[cursor] = { value: typeof init === "function" ? (init as () => unknown)() : init });
      cursor++;
      return [s.value, (value: unknown) => {
        const next = typeof value === "function" ? (value as (v: unknown) => unknown)(s.value) : value;
        if (!Object.is(next, s.value)) { s.value = next; dirty = true; }
      }];
    }) as never),
    spyOn(React, "useRef").mockImplementation(((init: unknown) => {
      const s = slots[cursor] ?? (slots[cursor] = { value: { current: init } });
      cursor++;
      return s.value;
    }) as never),
    spyOn(React, "useCallback").mockImplementation(((fn: unknown, deps: readonly unknown[]) => {
      const s = slot();
      if (changed(s.deps, deps)) { s.value = fn; s.deps = deps; }
      return s.value;
    }) as never),
    spyOn(React, "useMemo").mockImplementation(((fn: () => unknown, deps: readonly unknown[]) => {
      const s = slot();
      if (changed(s.deps, deps)) { s.value = fn(); s.deps = deps; }
      return s.value;
    }) as never),
    spyOn(React, "useEffect").mockImplementation(useEffect as never),
    spyOn(React, "useLayoutEffect").mockImplementation(useEffect as never),
    spyOn(React, "useSyncExternalStore").mockImplementation(((_subscribe: unknown, snapshot: () => unknown) => snapshot()) as never),
  ];

  const previous = {
    document: globalThis.document, window: (globalThis as { window?: unknown }).window,
    raf: globalThis.requestAnimationFrame, cancelRaf: globalThis.cancelAnimationFrame, css: (globalThis as { CSS?: unknown }).CSS,
  };
  const doc = {
    hidden: false,
    activeElement: null as unknown,
    /** 窗口有没有焦点：窗口失焦时 activeElement 仍是原来的输入框。 */
    hasFocus: (): boolean => true,
    body: { appendChild() {} },
    querySelector: (sel: string) => (sel === "#toast" ? toastEl : null),
    querySelectorAll: () => [],
    getElementById: () => null,
    createElement: () => ({ ...toastEl }),
    addEventListener() {},
    removeEventListener() {},
  };
  Object.assign(globalThis, {
    document: doc,
    window: {
      setTimeout: (fn: () => void, ms?: number) => setTimeout(fn, ms) as unknown as number,
      clearTimeout: (id: number) => clearTimeout(id),
      addEventListener() {}, removeEventListener() {}, dispatchEvent() { return true; },
    },
    requestAnimationFrame: () => 0,
    cancelAnimationFrame() {},
    CSS: { escape: (s: string) => s },
  });

  // toast 写 #toast 的 className / textContent：每次写完记一条。
  let lastText = "";
  Object.defineProperty(toastEl, "className", {
    get: () => lastText,
    set: (v: string) => {
      lastText = v;
      const kind = v.split(" ").find((c) => c === "ok" || c === "err") ?? "";
      if (kind) toasts.push({ text: toastEl.textContent, kind });
    },
  });

  function render() {
    // 渲染期间改了状态：像 React 一样丢掉这一遍，马上重新渲染；一直改就是停不下来。
    for (let pass = 0; ; pass++) {
      if (pass >= MAX_RERENDERS) throw notConverging();
      dirty = false;
      cursor = 0;
      pending = [];
      rendering = true;
      try { tree = Component(props); } finally { rendering = false; }
      if (!dirty) break;
    }
    const effects = pending;
    pending = [];
    for (const { slot: s, effect, deps } of effects) {
      s.cleanup?.();
      s.deps = deps;
      const cleanup = effect();
      s.cleanup = typeof cleanup === "function" ? (cleanup as () => void) : undefined;
    }
  }

  const all = () => nodes(tree);
  return {
    get tree() { return tree; },
    document: doc,
    toasts,
    /** 换 props 并重新渲染（下一次 settle）。 */
    setProps(next: Partial<P>) { props = { ...props, ...next }; dirty = true; },
    /**
     * 跑 40 轮：状态变了就渲染、执行 effect，再等一个宏任务让异步回调跑完。
     * 连着超过 MAX_RERENDERS 轮都要重新渲染（effect 每次都改状态，或异步回调每轮都改）时抛出「页面渲染未收敛」。
     * 最后以渲染收尾，读到的树就是最新的状态。
     */
    async settle() {
      let busyRounds = 0;
      for (let i = 0; i < 40; i++) {
        if (dirty && !rendering) {
          if (++busyRounds > MAX_RERENDERS) throw notConverging();
          render();
        } else busyRounds = 0;
        await new Promise((r) => setImmediate(r));
      }
      for (let pass = 0; dirty && !rendering; pass++) {
        if (pass >= MAX_RERENDERS) throw notConverging();
        render();
      }
    },
    all,
    /** 第一个满足条件的元素。 */
    find(pred: (el: El) => boolean): El | undefined { return all().find(pred); },
    /** 某个组件的所有元素。 */
    ofType(type: unknown): El[] { return all().filter((el) => el.type === type); },
    /** 页面上所有的文字。 */
    text() { return textOf(tree); },
    /** 点文字为 label 的可点元素。 */
    click(label: string) {
      const el = all().find((n) => typeof n.props.onClick === "function" && textOf(n.props.children).trim() === label);
      if (!el) throw new Error(`no clickable element "${label}"`);
      el.props.onClick();
    },
    /** 执行 effect 的清理，再放回 hook 和全局对象；某个清理抛错也照样放回，最后抛出第一个错误。 */
    close() {
      const errors: unknown[] = [];
      for (const s of slots) {
        try { s.cleanup?.(); } catch (e) { errors.push(e); }
        s.cleanup = undefined;
      }
      for (const spy of spies) spy.mockRestore();
      Object.assign(globalThis, {
        document: previous.document, window: previous.window,
        requestAnimationFrame: previous.raf, cancelAnimationFrame: previous.cancelRaf, CSS: previous.css,
      });
      if (errors.length > 0) throw errors[0];
    },
  };
}

/** 一条可控的流：测试发事件，模拟后端正常返回、出错，或页面自己关掉通道。 */
export function fakeStream<T>() {
  const queue: T[] = [];
  let wake: (() => void) | undefined;
  let closed = false;
  let resolve!: () => void, reject!: (e: unknown) => void;
  const done = new Promise<void>((yes, no) => { resolve = yes; reject = no; });
  const end = () => { closed = true; wake?.(); };
  const ch = {
    get closed() { return closed; },
    close: end,
    async *[Symbol.asyncIterator]() {
      while (true) {
        if (queue.length) { yield queue.shift()!; continue; }
        if (closed) return;
        await new Promise<void>((yes) => { wake = yes; });
      }
    },
  };
  return {
    ch, done,
    emit(ev: T) { queue.push(ev); wake?.(); },
    /** 后端正常返回：关通道，再完成。 */
    finish() { end(); resolve(); },
    /** 后端出错：完成 Promise 先拒绝，通道之后才关（调 end）。 */
    fail(e: unknown) { reject(e); },
    end,
  };
}

/**
 * 一个测试对全局状态的改动：spy、页面的 api 对象、globalThis 上的成员、界面语言、挂上的页面。
 * 每改一处先记下怎么还原；restore 按相反顺序全部还原，某一步出错时其余照样还原。
 * 用法：const env = changes(); try { 搭建…断言… } finally { env.restore(); }
 */
export function changes() {
  const undo: (() => void)[] = [];
  return {
    /** 记下一处改动怎么还原。 */
    add(restore: () => void) { undo.push(restore); },
    /** spyOn，还原时 mockRestore。 */
    spy<T extends object, K extends keyof T>(obj: T, key: K) {
      const spy = spyOn(obj, key);
      undo.push(() => spy.mockRestore());
      return spy;
    },
    /**
     * 换掉对象上的几个成员，还原时放回原来的值；原来没有的成员还原时删掉。
     * 只查成员名：替身（如 fakeStream）不必和原来的类型一致。
     */
    assign<T extends object>(obj: T, over: { [K in keyof T]?: unknown }) {
      const target = obj as Record<string, unknown>;
      const saved = Object.keys(over).map((k) => ({ k, own: Object.hasOwn(target, k), value: target[k] }));
      undo.push(() => {
        for (const { k, own, value } of saved) {
          if (own) target[k] = value;
          else delete target[k];
        }
      });
      Object.assign(obj, over);
    },
    /** 切换界面语言，还原时回到原来的偏好。 */
    lang(pref: LangPref) {
      const before = getPref();
      undo.push(() => setPref(before));
      setPref(pref);
    },
    /** 挂上页面（见 pageHarness），还原时关掉。 */
    page<P extends object>(Component: (p: P) => React.ReactNode, initial: P) {
      const page = pageHarness(Component, initial);
      undo.push(page.close);
      return page;
    },
    /**
     * 按相反顺序还原全部改动。restore 在 finally 里调用，这里同步抛错会盖住测试本身的失败：
     * 还原出的错误改在微任务里抛出，测试的错误照常报告，还原的错误也算作这个（async）测试的错误。
     */
    restore() {
      const errors: unknown[] = [];
      for (let restore = undo.pop(); restore; restore = undo.pop()) {
        try { restore(); } catch (e) { errors.push(e); }
      }
      for (const error of errors) queueMicrotask(() => { throw error; });
    },
  };
}

export type Changes = ReturnType<typeof changes>;
