// React 占用增长页与原生导航的桥接：main.ts 调 showReactGrowth() / focusReactGrowth(key)，不直接接触 React。
import type { GrowthHandlers } from "./GrowthPage";
import { mountGrowth, requestGrowthFocus } from "./mount";

const handlers: GrowthHandlers = { goLeftovers: () => {}, goUninstall: () => {} };

/** main.ts 注入：跳到应用卸载的残留页签 / 打开某个 App 的卸载对话框。 */
export function setGrowthHandlers(h: GrowthHandlers) {
  Object.assign(handlers, h);
}

/** main.ts 每次切换视图都调用：active 表示是否切到了本视图（进入时重新拉状态，隐藏时不跟着刷新）。 */
export function showReactGrowth(active: boolean) {
  const el = document.querySelector<HTMLElement>("#growth-react-root");
  if (el) mountGrowth(el, handlers, active);
}

/** 通知或菜单栏点进来：定位到这一行（调用方负责先切到本视图）。 */
export function focusReactGrowth(key: string) {
  requestGrowthFocus(key);
  showReactGrowth(true);
}
