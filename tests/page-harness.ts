// 无浏览器的页面测试：接管 React 的 hook，把页面组件当函数调用，按依赖执行 effect，
// 再从返回的元素树里找按钮、读文字、调回调。子组件不执行，只看传给它们的 props。
// 先 import "./setup-dom"（shared.ts 顶层要 document），再 import 本文件。
import * as React from "react";
import { spyOn } from "bun:test";

type Slot = { value?: unknown; deps?: readonly unknown[]; cleanup?: () => void };
type El = React.ReactElement<Record<string, any>>;

const changed = (before: readonly unknown[] | undefined, after: readonly unknown[] | undefined) =>
  !before || !after || before.length !== after.length || before.some((v, i) => !Object.is(v, after[i]));

/** 树里所有元素（含子组件元素和它们 props.children 里的元素）。 */
export function nodes(node: React.ReactNode): El[] {
  if (Array.isArray(node)) return node.flatMap(nodes);
  if (!React.isValidElement<Record<string, any>>(node)) return [];
  return [node, ...nodes(node.props.children)];
}

/** 一个节点里所有的文字。 */
export function textOf(node: React.ReactNode): string {
  if (Array.isArray(node)) return node.map(textOf).join("");
  if (React.isValidElement<{ children?: React.ReactNode }>(node)) return textOf(node.props.children);
  return typeof node === "string" || typeof node === "number" ? String(node) : "";
}

/** 最后一条 toast 的文字和类型。 */
export interface Toasted { text: string; kind: string }

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
    for (let pass = 0; pass < 20; pass++) {
      dirty = false;
      cursor = 0;
      pending = [];
      rendering = true;
      tree = Component(props);
      rendering = false;
      // 渲染期间改了状态：像 React 一样丢掉这一遍，马上重新渲染。
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
    /** 渲染、执行 effect、等异步回调，直到状态不再变化。 */
    async settle() {
      for (let i = 0; i < 40; i++) {
        if (dirty && !rendering) render();
        await new Promise((r) => setImmediate(r));
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
    close() {
      for (const s of slots) s.cleanup?.();
      for (const spy of spies) spy.mockRestore();
      Object.assign(globalThis, {
        document: previous.document, window: previous.window,
        requestAnimationFrame: previous.raf, cancelAnimationFrame: previous.cancelRaf, CSS: previous.css,
      });
    },
  };
}
