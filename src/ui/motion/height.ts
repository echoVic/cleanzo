import * as React from "react";
import { EASE, dur, play } from "./motion";

/** 内容变化时外框高度平滑过渡：观察 inner 的尺寸，变化时让 outer 从旧高度过渡到新高度。 */
export function useAnimatedHeight(outer: React.RefObject<HTMLElement | null>, inner: React.RefObject<HTMLElement | null>, enabled: boolean): void {
  React.useLayoutEffect(() => {
    const box = outer.current;
    const body = inner.current;
    if (!enabled || !box || !body || typeof ResizeObserver === "undefined") return;
    let last = box.getBoundingClientRect().height;
    const ro = new ResizeObserver(() => {
      const next = box.getBoundingClientRect().height;
      const from = last;
      last = next;
      if (Math.abs(next - from) < 1 || !(dur("layout") > 0)) return;
      box.style.overflow = "hidden";
      void play(box, [{ height: `${from}px` }, { height: `${next}px` }], dur("layout"), EASE.move).then(() => { box.style.overflow = ""; });
    });
    ro.observe(body);
    return () => ro.disconnect();
  }, [enabled, outer, inner]);
}
