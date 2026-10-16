// React 全盘扫描页与原生导航的桥接：main.ts 调 showReactFullScan() 即可，不直接接触 React。
import { mountFullScan } from "./mount";

/** 每次切到全盘扫描视图都挂载 / 复用 React 根；结果与磁盘卡在组件内保留。 */
export function showReactFullScan() {
  const el = document.querySelector<HTMLElement>("#fullscan-react-root");
  if (el) mountFullScan(el);
}
