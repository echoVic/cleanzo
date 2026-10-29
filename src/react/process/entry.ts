// React 进程页与原生导航的桥接：main.ts 调 showReactProcess(active)、openReactProcess(tab, focusKey)，不直接接触 React。
import type { GrowthHandlers } from "../growth/GrowthPage";
import type { ProcessTab } from "./format";
import { mountProcess, requestProcess } from "./mount";

const handlers: GrowthHandlers = { goLeftovers: () => {}, goUninstall: () => {} };

/** main.ts 注入：磁盘占用标签里跳到应用卸载的残留页签 / 打开某个 App 的卸载对话框。 */
export function setProcessHandlers(h: GrowthHandlers) {
  Object.assign(handlers, h);
}

/** main.ts 每次切换视图都调用：active 表示是否切到了本视图（进入时开采样，离开关通道）。 */
export function showReactProcess(active: boolean) {
  const el = document.querySelector<HTMLElement>("#process-react-root");
  if (el) mountProcess(el, handlers, active);
}

/** 下次进入时打开哪个标签；focusKey 是要定位的行（磁盘占用标签里的 App，或 CPU、内存标签里的一行）。调用方随后切到本视图。 */
export function openReactProcess(tab: ProcessTab, focusKey: string | null = null) {
  requestProcess(tab, focusKey);
}
