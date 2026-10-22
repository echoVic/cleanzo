import * as React from "react";

/**
 * 选中指示（页签下划线、分段控件滑块）：跟随 container 里 selector 命中的元素。
 * 只有第一次定位不带过渡；之后选中项变化（data-state / aria-selected）或尺寸变化都由 CSS transition 过去。
 * 选中项加粗会让容器变宽，尺寸回调紧跟在选中变化之后，若那时去掉过渡就会打断滑动。
 * container 需要 position: relative；隐藏时宽度为 0，显示后由 ResizeObserver 补上第一次定位。
 */
export function useIndicator(container: React.RefObject<HTMLElement | null>, selector: string): React.CSSProperties {
  const [style, setStyle] = React.useState<React.CSSProperties>({ opacity: 0 });
  React.useLayoutEffect(() => {
    const box = container.current;
    if (!box) return;
    let placed = false;
    const place = () => {
      const el = box.querySelector<HTMLElement>(selector);
      if (!el || !el.offsetWidth) return;
      setStyle({ width: el.offsetWidth, transform: `translateX(${el.offsetLeft}px)`, transition: placed ? undefined : "none" });
      placed = true;
    };
    place();
    const mo = new MutationObserver(place);
    mo.observe(box, { subtree: true, attributes: true, attributeFilter: ["data-state", "aria-selected"] });
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(place);
    ro?.observe(box);
    return () => { mo.disconnect(); ro?.disconnect(); };
  }, [container, selector]);
  return style;
}
