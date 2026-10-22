import * as React from "react";
import { cn } from "../cn";
import { useAnimatedHeight } from "./height";
import { dur, motionLevel } from "./motion";

export interface CollapseProps {
  open: boolean;
  children: React.ReactNode;
  className?: string;
  /** 首次以 open 挂载时也从收起展开（默认直接展开、不播动画）。 */
  appear?: boolean;
  /** 展开期间内容变高变矮时也平滑过渡（例如子项异步加载）。 */
  animateResize?: boolean;
}

/**
 * 展开收起：外层 grid 行高 0fr ↔ 1fr 过渡，内容淡入淡出；收起动画结束后卸载内容。
 * 过渡期间才裁剪溢出，静止展开时不裁剪，行菜单和焦点环不会被切掉。
 */
export function Collapse({ open, children, className, appear = false, animateResize = false }: CollapseProps) {
  const [mounted, setMounted] = React.useState(open);
  const [expanded, setExpanded] = React.useState(open && (!appear || motionLevel() === "none"));
  const [moving, setMoving] = React.useState(false);
  const outer = React.useRef<HTMLDivElement>(null);
  const inner = React.useRef<HTMLDivElement>(null);
  useAnimatedHeight(outer, inner, animateResize && expanded && !moving);

  React.useLayoutEffect(() => {
    const ms = Math.max(dur("layout"), dur("fade"));
    if (open) {
      setMounted(true);
      if (expanded) return;
      if (!(ms > 0) || motionLevel() === "none") { setExpanded(true); return; }
      setMoving(true);
      // 先以收起态挂上，下一帧再展开，过渡才会发生。
      const raf = requestAnimationFrame(() => setExpanded(true));
      const timer = window.setTimeout(() => setMoving(false), ms + 60);
      return () => { cancelAnimationFrame(raf); window.clearTimeout(timer); };
    }
    setExpanded(false);
    if (!(ms > 0)) { setMounted(false); setMoving(false); return; }
    setMoving(true);
    const timer = window.setTimeout(() => { setMounted(false); setMoving(false); }, ms + 60);
    return () => window.clearTimeout(timer);
    // 只响应 open 的变化
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  if (!mounted && !open) return null;
  return (
    <div ref={outer} className={cn("cz-collapse", className)} data-state={expanded ? "open" : "closed"} data-moving={moving ? "" : undefined}>
      <div ref={inner} className="cz-collapse-in">{children}</div>
    </div>
  );
}
