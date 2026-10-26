// React 占用增长页与原生导航的桥接：main.ts 调 showReactGrowth() / focusReactGrowth(key)，不直接接触 React。
import type { GrowthHandlers } from "./GrowthPage";
import { mountGrowth, requestGrowthFocus } from "./mount";

const handlers: GrowthHandlers = { goLeftovers: () => {}, goUninstall: () => {} };

/** main.ts 注入：跳到应用卸载的残留页签 / 打开某个 App 的卸载对话框。 */
export function setGrowthHandlers(h: GrowthHandlers) {
  Object.assign(handlers, h);
}

/** 每次切到占用增长视图都挂载 / 复用 React 根。 */
export function showReactGrowth() {
  const el = document.querySelector<HTMLElement>("#growth-react-root");
  if (el) mountGrowth(el, handlers);
}

/** 通知或菜单栏点进来：定位到这一行（调用方负责先切到本视图）。 */
export function focusReactGrowth(key: string) {
  requestGrowthFocus(key);
  showReactGrowth();
}
