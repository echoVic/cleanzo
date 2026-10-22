import * as React from "react";
import { EASE, dur } from "./motion";

/**
 * 内容变化时外框高度平滑过渡：观察 inner（内容本身的自然高度），变化时让 outer 从当前高度过渡到新高度。
 * inner 不能是高度受 outer 约束的元素（比如 grid 1fr 的子项），否则动画会反过来触发尺寸回调、来回打断。
 * 动画途中内容再变，从当前高度接着过渡，不跳回旧值。
 */
export function useAnimatedHeight(outer: React.RefObject<HTMLElement | null>, inner: React.RefObject<HTMLElement | null>, enabled: boolean): void {
  React.useLayoutEffect(() => {
    const box = outer.current;
    const body = inner.current;
    if (!enabled || !box || !body || typeof ResizeObserver === "undefined") return;
    // 外框比内容多出的高度（内边距、边框），静止时量一次。
    const chrome = box.getBoundingClientRect().height - body.getBoundingClientRect().height;
    let last = box.getBoundingClientRect().height;
    let running: Animation | null = null;
    const ro = new ResizeObserver(() => {
      const next = body.getBoundingClientRect().height + chrome;
      const from = running ? box.getBoundingClientRect().height : last;
      last = next;
      if (Math.abs(next - from) < 1 || !(dur("layout") > 0) || typeof box.animate !== "function") return;
      running?.cancel();
      box.style.overflow = "hidden";
      const a = box.animate([{ height: `${from}px` }, { height: `${next}px` }], { duration: dur("layout"), easing: EASE.move });
      running = a;
      a.finished.then(() => {
        if (running !== a) return;
        running = null;
        box.style.overflow = "";
      }, () => {});
    });
    ro.observe(body);
    return () => {
      ro.disconnect();
      running?.cancel();
      box.style.overflow = "";
    };
  }, [enabled, outer, inner]);
}
