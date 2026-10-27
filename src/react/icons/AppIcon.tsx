import * as React from "react";
import { hasAppIcon, loadIcon, peekIcon } from "./cache";

// 首字母圆标到了页面滚动容器（.content）的可见区附近（200px）才开始要图标；
// 找不到滚动容器时以视口为 root，长列表里远处的行不要。
const observers = new Map<Element | null, IntersectionObserver>();
const waiting = new Map<Element, () => void>();

function whenVisible(el: Element | null, fn: () => void): () => void {
  if (!el || typeof IntersectionObserver === "undefined") {
    fn();
    return () => {};
  }
  const root = el.closest(".content");
  let observer = observers.get(root);
  if (!observer) {
    observer = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue;
        const go = waiting.get(e.target);
        waiting.delete(e.target);
        observer?.unobserve(e.target);
        go?.();
      }
    }, { root, rootMargin: "200px" });
    observers.set(root, observer);
  }
  waiting.set(el, fn);
  observer.observe(el);
  return () => {
    waiting.delete(el);
    observer?.unobserve(el);
  };
}

export interface AppIconProps {
  /** App 的 .app 路径；不是 .app 时一直显示首字母。 */
  path: string;
  /** 首字母圆标那个框的类名（st-ico、cz-growth-icon、app-icon）：图标放进同样大小的框。 */
  box: string;
  /** 首字母圆标：图标到之前、或者没有图标时显示。 */
  children: React.ReactElement<{ ref?: React.Ref<HTMLElement> }>;
}

/** App 的真实图标；还没到或者没有时是首字母圆标。 */
export function AppIcon({ path, box, children }: AppIconProps) {
  const want = hasAppIcon(path);
  const url = want ? peekIcon(path) : undefined;
  const ref = React.useRef<HTMLElement>(null);
  const [, arrived] = React.useReducer((n: number) => n + 1, 0);
  const missing = want && url === undefined;
  React.useEffect(() => {
    if (!missing) return;
    let live = true;
    const stop = whenVisible(ref.current, () => {
      void loadIcon(path).then(() => {
        if (live) arrived();
      });
    });
    return () => {
      live = false;
      stop();
    };
  }, [path, missing]);
  if (url) return <img className={`${box} cz-app-img`} src={url} alt="" aria-hidden="true" draggable={false} />;
  return React.cloneElement(children, { ref });
}
