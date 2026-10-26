import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { GrowthPage, type GrowthHandlers } from "./GrowthPage";
import "../../ui/index.css";

let root: Root | null = null;
let visit = 0;
let focus: { key: string | null; nonce: number } = { key: null, nonce: 0 };

/** 通知 / 菜单栏要定位的那一行；下次渲染时展开并高亮。 */
export function requestGrowthFocus(key: string) {
  focus = { key, nonce: focus.nonce + 1 };
}

/** 在原生视图的容器里挂载占用增长页；第一次进入视图才建根，之后复用。每次进入重新拉一次状态，隐藏时只传 active=false。 */
export function mountGrowth(el: HTMLElement, handlers: GrowthHandlers, active: boolean) {
  if (!root) {
    if (!active) return;
    root = createRoot(el);
  }
  if (active) visit++;
  root.render(
    <React.StrictMode>
      <GrowthPage active={active} visit={visit} focusKey={focus.key} focusNonce={focus.nonce} {...handlers} />
    </React.StrictMode>,
  );
}
