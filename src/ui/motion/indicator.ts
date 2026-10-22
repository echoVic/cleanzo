import * as React from "react";

/**
 * 选中指示（页签下划线、分段控件滑块）：跟随 container 里 selector 命中的元素。
 * 第一次定位和尺寸变化时不带过渡；选中项变化（data-state / aria-selected）时由 CSS transition 滑过去。
 * container 需要 position: relative；隐藏时宽度为 0，显示后由 ResizeObserver 补上定位。
 */
export function useIndicator(container: React.RefObject<HTMLElement | null>, selector: string): React.CSSProperties {
  const [style, setStyle] = React.useState<React.CSSProperties>({ opacity: 0 });
  React.useLayoutEffect(() => {
    const box = container.current;
    if (!box) return;
    const place = (animate: boolean) => {
      const el = box.querySelector<HTMLElement>(selector);
      if (!el || !el.offsetWidth) return;
      setStyle({ width: el.offsetWidth, transform: `translateX(${el.offsetLeft}px)`, transition: animate ? undefined : "none" });
    };
    place(false);
    const mo = new MutationObserver(() => place(true));
    mo.observe(box, { subtree: true, attributes: true, attributeFilter: ["data-state", "aria-selected"] });
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(() => place(false));
    ro?.observe(box);
    return () => { mo.disconnect(); ro?.disconnect(); };
  }, [container, selector]);
  return style;
}
