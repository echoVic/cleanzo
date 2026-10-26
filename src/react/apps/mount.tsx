import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { AppsPage } from "./AppsPage";
import { appsRequest } from "./requests";
import "../../ui/index.css";

let root: Root | null = null;

/** 垃圾页「残留」动作等外部入口请求切到指定 tab。 */
export { requestAppsTab } from "./requests";

/** 在原生视图的容器里挂载应用卸载页；root 复用，重进视图不整树卸载（结果保留、不重扫）。 */
export function mountApps(el: HTMLElement) {
  if (!root) root = createRoot(el);
  const r = appsRequest();
  root.render(
    <React.StrictMode>
      <AppsPage initialTab={r.tab} nonce={r.tabNonce} uninstallPath={r.uninstallPath} uninstallNonce={r.uninstallNonce} />
    </React.StrictMode>,
  );
}
