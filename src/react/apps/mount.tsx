import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { AppsPage, type AppsTab } from "./AppsPage";
import "../../ui/index.css";

let root: Root | null = null;
let pendingTab: AppsTab = "installed";
let nonce = 0;

/** 垃圾页「残留」动作等外部入口请求切到指定 tab。 */
export function requestAppsTab(tab: AppsTab) {
  pendingTab = tab;
  nonce++;
}

/** 在原生视图的容器里挂载应用卸载页；root 复用，重进视图不整树卸载（结果保留、不重扫）。 */
export function mountApps(el: HTMLElement) {
  if (!root) root = createRoot(el);
  root.render(
    <React.StrictMode>
      <AppsPage initialTab={pendingTab} nonce={nonce} />
    </React.StrictMode>,
  );
}
