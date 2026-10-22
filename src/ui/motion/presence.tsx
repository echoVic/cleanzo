import * as React from "react";
import { EASE, dur, motionLevel, play } from "./motion";

/** 列表里的一项：leaving 表示正在离场，只为播放收起动画而保留。 */
export interface Presence<T> {
  item: T;
  key: string;
  leaving: boolean;
}

type Leaving<T> = ReadonlyMap<string, { item: T; index: number }>;

/** 把离场项按原来的位置插回当前列表（纯函数，便于单测）。 */
export function mergePresence<T>(items: readonly T[], keyOf: (item: T) => string, leaving: Leaving<T>): Presence<T>[] {
  const out: Presence<T>[] = items.map((item) => ({ item, key: keyOf(item), leaving: false }));
  if (leaving.size === 0) return out;
  const present = new Set(out.map((p) => p.key));
  [...leaving.entries()]
    .filter(([key]) => !present.has(key))
    .sort((a, b) => a[1].index - b[1].index)
    .forEach(([key, v]) => out.splice(Math.min(v.index, out.length), 0, { item: v.item, key, leaving: true }));
  return out;
}

/**
 * 列表项离场：从 items 里消失的项再保留一个离场时长（leaving=true），之后移除。
 * enabled=false 的那次更新（搜索过滤、整表重置）直接移除；none 级别从不保留，测试里同步。
 */
export function usePresence<T>(items: readonly T[], keyOf: (item: T) => string, enabled = true): Presence<T>[] {
  const [leaving, setLeaving] = React.useState<Leaving<T>>(() => new Map());
  const prev = React.useRef<readonly T[]>(items);
  const timers = React.useRef<number[]>([]);
  React.useEffect(() => () => timers.current.forEach((id) => window.clearTimeout(id)), []);
  // 渲染时就算出离场项：消失的那次提交里它仍在原位。等提交后再补回会先卸载再重挂，
  // 进场动画重播、闪一帧，也会让换位按缺了这一项的位置先移动一次。
  const gone = goneSince(prev.current, items, keyOf, enabled);
  React.useLayoutEffect(() => {
    const before = prev.current;
    prev.current = items;
    if (before === items) return;
    const now = new Set(items.map(keyOf));
    // 重新出现的项不再离场。
    setLeaving((m) => {
      if (![...m.keys()].some((k) => now.has(k))) return m;
      const next = new Map(m);
      now.forEach((k) => next.delete(k));
      return next;
    });
    if (gone.length === 0) return;
    setLeaving((m) => new Map([...m, ...gone]));
    const keys = gone.map(([k]) => k);
    timers.current.push(window.setTimeout(() => {
      setLeaving((m) => {
        const next = new Map(m);
        keys.forEach((k) => next.delete(k));
        return next;
      });
    }, Math.max(dur("layout"), dur("fade")) + 40));
  });
  return mergePresence(items, keyOf, gone.length > 0 ? new Map([...leaving, ...gone]) : leaving);
}

/** 上次提交里有、这次没有的项及其原位置；不需要离场动画时为空。 */
function goneSince<T>(before: readonly T[], items: readonly T[], keyOf: (item: T) => string, enabled: boolean): [string, { item: T; index: number }][] {
  if (before === items || !enabled || motionLevel() === "none") return [];
  const now = new Set(items.map(keyOf));
  const gone: [string, { item: T; index: number }][] = [];
  before.forEach((item, index) => {
    const key = keyOf(item);
    if (!now.has(key)) gone.push([key, { item, index }]);
  });
  return gone;
}

/** 行收起：高度、内边距、边框归零并淡出；父容器有 gap 时用负外边距抵掉，移除时不再跳一下。 */
export function collapseOut(el: HTMLElement | null): void {
  if (!el) return;
  el.style.pointerEvents = "none";
  const ms = dur("layout");
  if (!(ms > 0)) {
    void play(el, [{ opacity: 1 }, { opacity: 0 }], dur("fade"), "linear", { fill: "forwards" });
    return;
  }
  const cs = getComputedStyle(el);
  const gap = el.parentElement ? parseFloat(getComputedStyle(el.parentElement).rowGap) || 0 : 0;
  const last = !el.nextElementSibling;
  const height = el.getBoundingClientRect().height;
  el.style.overflow = "hidden";
  void play(el, [{ opacity: 1 }, { opacity: 0 }], dur("state"), EASE.in, { fill: "forwards" });
  void play(el, [
    { height: `${height}px`, paddingTop: cs.paddingTop, paddingBottom: cs.paddingBottom, borderTopWidth: cs.borderTopWidth, borderBottomWidth: cs.borderBottomWidth, marginTop: cs.marginTop, marginBottom: cs.marginBottom },
    { height: "0px", paddingTop: "0px", paddingBottom: "0px", borderTopWidth: "0px", borderBottomWidth: "0px", marginTop: last && gap ? `${-gap}px` : cs.marginTop, marginBottom: !last && gap ? `${-gap}px` : cs.marginBottom },
  ], ms, EASE.move, { fill: "forwards" });
}

/** 网格项离场：缩小淡出（减弱时只淡出）。 */
export function popOut(el: HTMLElement | null): void {
  if (!el) return;
  el.style.pointerEvents = "none";
  if (dur("state") > 0) void play(el, [{ opacity: 1, transform: "none" }, { opacity: 0, transform: "scale(0.92)" }], dur("state"), EASE.in, { fill: "forwards" });
  else void play(el, [{ opacity: 1 }, { opacity: 0 }], dur("fade"), "linear", { fill: "forwards" });
}

/** leaving 变为 true 时播放离场动画：collapse 用于列表行，pop 用于网格项。 */
export function useLeaveAnimation(ref: React.RefObject<HTMLElement | null>, leaving: boolean, kind: "collapse" | "pop" = "collapse"): void {
  React.useLayoutEffect(() => {
    if (!leaving) return;
    if (kind === "pop") popOut(ref.current);
    else collapseOut(ref.current);
  }, [leaving, kind, ref]);
}

/** 给唯一的宿主子元素挂上离场动画，不加包裹层（不影响 `.a + .a` 相邻选择器）。 */
export function Leave({ leaving, kind = "collapse", children }: {
  leaving: boolean;
  kind?: "collapse" | "pop";
  children: React.ReactElement;
}) {
  const ref = React.useRef<HTMLElement | null>(null);
  useLeaveAnimation(ref, leaving, kind);
  return React.cloneElement(children as React.ReactElement<Record<string, unknown>>, {
    ref,
    "aria-hidden": leaving || undefined,
    inert: leaving || undefined,
  });
}
