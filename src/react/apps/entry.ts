// React 应用页与原生导航的桥接；每次切视图都同步页面显隐。
import { mountApps } from "./mount";

/** 外部请求只记录，等导航激活页面时消费。 */
export { requestAppsFilter, requestAppsUninstall } from "./requests";

/** 首次激活才挂载；之后每次切视图都更新 active。 */
export function showReactApps(active: boolean) {
  const el = document.querySelector<HTMLElement>("#apps-react-root");
  if (el) mountApps(el, active);
}
