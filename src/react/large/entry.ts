// React 大文件页与原生导航的桥接：main.ts 调 showReactLarge() 即可，不直接接触 React。
import { mountLarge } from "./mount";

/** 每次切到大文件视图都挂载 / 复用 React 根；结果在组件内保留。 */
export function showReactLarge() {
  const el = document.querySelector<HTMLElement>("#large-react-root");
  if (el) mountLarge(el);
}
