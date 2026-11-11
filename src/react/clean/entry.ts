// React 垃圾清理页与原生导航的桥接：main.ts 调 showReactClean() 即可，不直接接触 React。
import { mountClean } from "./mount";

// 设置页保存配置后通知本页显过期条。页还没挂载时没有结果可过期，什么也不用记。
const staleHandler: { fn: (() => void) | null } = { fn: null };

/** 设置页保存配置成功后调用（无论 clean 页当前是否挂载）。 */
export function markCleanStale() {
  staleHandler.fn?.();
}

/** CleanPage 挂载时注册：非 idle 才置过期。 */
export function setCleanStaleHandler(fn: () => void) {
  staleHandler.fn = fn;
}

/** 每次切到垃圾清理视图都挂载 / 复用 React 根；结果与 phase 在组件内保留（root 复用，扫描后台继续）。 */
export function showReactClean() {
  const el = document.querySelector<HTMLElement>("#clean-react-root");
  if (el) mountClean(el);
}
