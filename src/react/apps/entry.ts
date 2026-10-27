// React 应用卸载页与原生导航的桥接：main.ts 调 showReactApps() 即可，不直接接触 React。
import { mountApps, requestAppsTab } from "./mount";
import { requestAppsUninstall as request } from "./requests";

/** 每次切到应用卸载视图都挂载 / 复用 React 根；结果在组件内保留。可指定初始 tab（残留桥接用）。 */
export function showReactApps(tab?: "installed" | "leftovers") {
  if (tab) requestAppsTab(tab);
  const el = document.querySelector<HTMLElement>("#apps-react-root");
  if (el) mountApps(el);
}

/** 进程页的磁盘占用标签「卸载」：请求后挂载 / 复用根，InstalledTab 据 nonce 打开对话框。 */
export function requestAppsUninstall(path: string) {
  request(path);
  const el = document.querySelector<HTMLElement>("#apps-react-root");
  if (el) mountApps(el);
}
