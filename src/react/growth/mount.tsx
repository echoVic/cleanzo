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

/** 在原生视图的容器里挂载占用增长页；root 复用，每次进入视图重新拉一次状态。 */
export function mountGrowth(el: HTMLElement, handlers: GrowthHandlers) {
  if (!root) root = createRoot(el);
  visit++;
  root.render(
    <React.StrictMode>
      <GrowthPage visit={visit} focusKey={focus.key} focusNonce={focus.nonce} {...handlers} />
    </React.StrictMode>,
  );
}
