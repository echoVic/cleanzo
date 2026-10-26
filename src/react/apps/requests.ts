// 其他页面对应用卸载页的请求（垃圾页「残留」、占用增长页「卸载」）；不含 React，测试可直接引入。
import type { AppsTab } from "./AppsPage";

/** 最近一次请求：要切到的 tab、要打开卸载对话框的 App；两个 nonce 变了才执行。 */
export interface AppsRequest {
  tab: AppsTab;
  tabNonce: number;
  uninstallPath: string | null;
  uninstallNonce: number;
}

let req: AppsRequest = { tab: "installed", tabNonce: 0, uninstallPath: null, uninstallNonce: 0 };

/** 外部入口请求切到指定 tab。 */
export function requestAppsTab(tab: AppsTab) {
  req = { ...req, tab, tabNonce: req.tabNonce + 1 };
}

/** 占用增长页「卸载」：切到已安装，并打开该 App 的卸载对话框（走现有流程）。 */
export function requestAppsUninstall(path: string) {
  req = { ...req, tab: "installed", tabNonce: req.tabNonce + 1, uninstallPath: path, uninstallNonce: req.uninstallNonce + 1 };
}

/** 当前请求，mountApps 渲染时读取。 */
export function appsRequest(): AppsRequest {
  return req;
}
