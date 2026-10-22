import * as React from "react";
import { dur } from "./motion";

export interface NumberTickerProps {
  value: number;
  format: (n: number) => string;
  /** 首次挂载时从这个值滚到 value（例如扫描开始时从 0 累加）；不传则直接显示 value。 */
  from?: number;
  as?: "span" | "b";
  className?: string;
}

/** 数字滚动：数值变化时在 dur("tick") 内滚到新值，中途可改目标；减弱动态效果时直接显示。 */
export function NumberTicker({ value, format, from, as = "span", className }: NumberTickerProps) {
  const ref = React.useRef<HTMLElement | null>(null);
  const shown = React.useRef(from ?? value);
  const fmt = React.useRef(format);
  fmt.current = format;
  const [initial] = React.useState(() => format(from ?? value));
  React.useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const node = el.firstChild ?? el.appendChild(document.createTextNode(""));
    const start = shown.current;
    const ms = dur("tick");
    if (!(ms > 0) || start === value || !Number.isFinite(start)) {
      shown.current = value;
      node.nodeValue = fmt.current(value);
      return;
    }
    let raf = 0;
    const t0 = performance.now();
    const step = (now: number) => {
      const p = Math.min(1, (now - t0) / ms);
      shown.current = p < 1 ? start + (value - start) * (1 - Math.pow(1 - p, 4)) : value;
      node.nodeValue = fmt.current(shown.current);
      if (p < 1) raf = requestAnimationFrame(step);
    };
    raf = requestAnimationFrame(step);
    return () => cancelAnimationFrame(raf);
  }, [value]);
  return React.createElement(as, { ref, className }, initial);
}
