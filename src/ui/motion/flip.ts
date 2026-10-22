import * as React from "react";
import { EASE, dur, motionLevel } from "./motion";

type Pos = { x: number; y: number };

function measure(box: HTMLElement): Map<string, Pos> {
  const base = box.getBoundingClientRect();
  const out = new Map<string, Pos>();
  box.querySelectorAll<HTMLElement>(":scope > [data-key]").forEach((el) => {
    const r = el.getBoundingClientRect();
    out.set(el.dataset.key ?? "", { x: r.left - base.left, y: r.top - base.top });
  });
  return out;
}

/**
 * 列表换位：keys 顺序变化时，容器的直接子元素（[data-key]）从旧位置滑到新位置。
 * 只传留在列表里的 key；离场项由 usePresence 收起，不参与换位。enabled=false 的那次更新不播放。
 */
export function useFlip(container: React.RefObject<HTMLElement | null>, keys: readonly string[], enabled = true, max = 40): void {
  const sig = keys.join("\u0000");
  const committed = React.useRef(sig);
  const before = React.useRef<Map<string, Pos> | null>(null);
  if (sig !== committed.current && enabled && container.current && motionLevel() === "full") {
    before.current = measure(container.current);
  }
  React.useLayoutEffect(() => {
    if (sig === committed.current) return;
    committed.current = sig;
    const first = before.current;
    before.current = null;
    const box = container.current;
    if (!first || !box || !(dur("move") > 0)) return;
    const els = box.querySelectorAll<HTMLElement>(":scope > [data-key]");
    els.forEach((el) => el.getAnimations().forEach((a) => { if (a.id === "cz-flip") a.cancel(); }));
    const last = measure(box);
    let n = 0;
    els.forEach((el) => {
      const key = el.dataset.key ?? "";
      const p = first.get(key);
      const q = last.get(key);
      if (!p || !q || n >= max) return;
      const dx = p.x - q.x;
      const dy = p.y - q.y;
      if (Math.abs(dx) < 0.5 && Math.abs(dy) < 0.5) return;
      n++;
      const a = el.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: "none" }], { duration: dur("move"), easing: EASE.move });
      a.id = "cz-flip";
    });
  }, [sig, container, max]);
}
