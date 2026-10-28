// React 系统状态页与原生导航的桥接：main.ts 调 showReactStatus(active) 即可，不直接接触 React。
import type { ProcessTab } from "../process/format";
import { mountStatus } from "./mount";

const goClean: { fn: () => void } = { fn: () => {} };
const goProcess: { fn: (tab: ProcessTab, focusKey?: string) => void } = { fn: () => {} };

/** 告警 / 磁盘卡的「去清理」：与点侧栏「垃圾清理」同一条路径（main.ts 注入）。 */
export function setGoCleanHandler(fn: () => void) {
  goClean.fn = fn;
}

/** 增长提示的「查看」、内存提示的「看谁在占内存」：打开进程页的某个标签（main.ts 注入）。 */
export function setGoProcessHandler(fn: (tab: ProcessTab, focusKey?: string) => void) {
  goProcess.fn = fn;
}

/** 进入系统状态视图传 true（开 2s 采样流），离开传 false（关 Channel）。root 复用。 */
export function showReactStatus(active: boolean) {
  const el = document.querySelector<HTMLElement>("#status-react-root");
  if (el) mountStatus(el, active, goClean.fn, goProcess.fn);
}
