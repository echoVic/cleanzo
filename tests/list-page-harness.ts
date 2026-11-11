// 无浏览器地驱动一个页面组件：换上自己的 hook dispatcher，按调用顺序保存 state / ref / effect，
// 每次 flush 都把页面渲染到不再变化。返回的元素树只用来读 props、触发回调，子组件不渲染；
// 桥接由各测试在页面的 api 对象上替换。最小 DOM 不能真正挂载，这里不验证布局、焦点和动画。
import "./setup-dom";
import * as React from "react";

type Node = React.ReactElement<Record<string, any>> & { type: any };

export interface PageHarness<P> {
  /** 渲染到稳定，并让异步回调跑上几轮。 */
  flush(next?: Partial<P>): Promise<void>;
  /** 再等一个宏任务：未处理的 rejection 在这时报告。 */
  settle(): Promise<void>;
  nodes(root?: unknown): Node[];
  find(match: (n: Node) => boolean): Node | undefined;
  text(root?: unknown): string;
  readonly toasts: { text: string; kind: string }[];
  dispose(): void;
}

export function pageHarness<P>(component: (props: P) => React.ReactNode, initial: P): PageHarness<P> {
  const saved = { window: globalThis.window, query: document.querySelector };
  const toasts: { text: string; kind: string }[] = [];
  let toastText = "";
  const toastEl = {
    setAttribute() {}, classList: { remove() {} },
    get textContent() { return toastText; }, set textContent(v: string) { toastText = v; },
    // toast() 先写文字再写 className，写 className 时记下一条。
    get className() { return ""; }, set className(v: string) { toasts.push({ text: toastText, kind: v.split(" ").at(-1) ?? "" }); },
  };
  document.querySelector = ((selector: string) => (selector === "#toast" ? toastEl : null)) as never;
  globalThis.window = { setTimeout: () => 0, clearTimeout() {} } as never;

  const slots: any[] = [];
  let index = 0, dirty = true, tree: React.ReactNode = null, props = initial;
  let pending: (() => void)[] = [];
  const changed = (a?: unknown[], b?: unknown[]) => !a || !b || a.length !== b.length || a.some((v, i) => !Object.is(v, b[i]));
  const effect = (fn: () => void | (() => void), deps?: unknown[]) => {
    const slot = index++;
    if (!slots[slot] || changed(slots[slot].deps, deps)) {
      pending.push(() => { slots[slot]?.cleanup?.(); slots[slot] = { deps, cleanup: fn() }; });
    }
  };
  const dispatcher = {
    useState(init: unknown) {
      const slot = index++;
      if (!(slot in slots)) slots[slot] = typeof init === "function" ? (init as () => unknown)() : init;
      return [slots[slot], (next: unknown) => {
        const value = typeof next === "function" ? (next as (p: unknown) => unknown)(slots[slot]) : next;
        if (!Object.is(value, slots[slot])) { slots[slot] = value; dirty = true; }
      }];
    },
    useRef(init: unknown) { const slot = index++; return slots[slot] ??= { current: init }; },
    useEffect: effect, useLayoutEffect: effect,
    useMemo(fn: () => unknown, deps: unknown[]) {
      const slot = index++;
      if (!slots[slot] || changed(slots[slot].deps, deps)) slots[slot] = { deps, value: fn() };
      return slots[slot].value;
    },
    useCallback(fn: unknown, deps: unknown[]) { return dispatcher.useMemo(() => fn, deps); },
    useSyncExternalStore(_subscribe: unknown, snapshot: () => unknown) { index++; return snapshot(); },
  };
  const internals = (React as any).__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE;
  function render() {
    let passes = 0;
    do {
      if (++passes > 40) throw new Error("页面渲染未收敛");
      dirty = false; index = 0; pending = [];
      const before = internals.H;
      internals.H = dispatcher;
      try { tree = component(props); } finally { internals.H = before; }
      const effects = pending;
      pending = [];
      for (const run of effects) run();
    } while (dirty);
  }
  function nodes(node: unknown = tree): Node[] {
    if (Array.isArray(node)) return node.flatMap((child) => nodes(child));
    return React.isValidElement(node) ? [node as Node, ...nodes((node.props as any).children ?? null)] : [];
  }
  function text(node: unknown = tree): string {
    if (Array.isArray(node)) return node.map((child) => text(child)).join("");
    if (React.isValidElement(node)) return text((node.props as any).children ?? null);
    return typeof node === "string" || typeof node === "number" ? String(node) : "";
  }
  return {
    async flush(next = {}) {
      props = { ...props, ...next };
      for (let i = 0; i < 32; i++) { render(); await Promise.resolve(); }
      render();
    },
    async settle() { await new Promise((resolve) => setTimeout(resolve, 0)); },
    nodes,
    find: (match) => nodes().find(match),
    text,
    toasts,
    dispose() {
      for (const slot of slots) slot?.cleanup?.();
      globalThis.window = saved.window;
      document.querySelector = saved.query;
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
