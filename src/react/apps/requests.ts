// 其他页面对应用页的请求；不含 React，测试可直接引入。
import type { AppsFilter } from "./updates";

/** 最近一次请求：筛选和要展开的 App；两个版本号变化时才执行。 */
export interface AppsRequest {
  filter: AppsFilter;
  filterNonce: number;
  uninstallPath: string | null;
  uninstallNonce: number;
}
let req: AppsRequest = { filter: "all", filterNonce: 0, uninstallPath: null, uninstallNonce: 0 };

/** 外部入口只记录筛选请求，不挂载页面。 */
export function requestAppsFilter(filter: AppsFilter) {
  req = { ...req, filter, filterNonce: req.filterNonce + 1 };
}

/** 外部卸载入口回到全部，并展开对应的行，等待用户确认。 */
export function requestAppsUninstall(path: string) {
  req = { ...req, filter: "all", filterNonce: req.filterNonce + 1, uninstallPath: path, uninstallNonce: req.uninstallNonce + 1 };
}

/** 当前请求，挂载页面时读取。 */
export function appsRequest(): AppsRequest { return req; }
