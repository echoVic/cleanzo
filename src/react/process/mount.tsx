import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import type { GrowthHandlers } from "../growth/GrowthPage";
import { ProcessPage } from "./ProcessPage";
import type { ProcessRequest, ProcessTab } from "./format";
import "../../ui/index.css";

let root: Root | null = null;
let visit = 0;
let request: ProcessRequest = { tab: null, focusKey: null, nonce: 0 };

/** 记下下次进入时要打开的标签和磁盘占用里要定位的行。 */
export function requestProcess(tab: ProcessTab, focusKey: string | null) {
  request = { tab, focusKey, nonce: request.nonce + 1 };
}

/** 在原生视图的容器里挂载进程页；第一次进入才建根，之后复用。每次进入 visit 加一，隐藏时只传 active=false。 */
export function mountProcess(el: HTMLElement, handlers: GrowthHandlers, active: boolean) {
  if (!root) {
    if (!active) return;
    root = createRoot(el);
  }
  if (active) visit++;
  root.render(
    <React.StrictMode>
      <ProcessPage active={active} visit={visit} request={request} {...handlers} />
    </React.StrictMode>,
  );
}
