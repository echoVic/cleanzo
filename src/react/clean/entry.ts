// React 垃圾清理页与原生导航的桥接：main.ts 调 showReactClean() 即可，不直接接触 React。
import { mountClean } from "./mount";

// 设置页保存配置后通知本页显过期条；页未挂载时先记下，挂载后兜底。
const staleHandler: { fn: (() => void) | null } = { fn: null };
let stalePending = false;

/** 设置页保存配置成功后调用（无论 clean 页当前是否挂载）。 */
export function markCleanStale() {
  if (staleHandler.fn) staleHandler.fn();
  else stalePending = true;
}

/** CleanPage 挂载时注册：非 idle 才置过期（与旧 markJunkStale 语义一致）。 */
export function setCleanStaleHandler(fn: () => void) {
  staleHandler.fn = fn;
}

/** 挂载时取走未消费的过期标记（取后清零）。 */
export function takeStalePending(): boolean {
  const v = stalePending;
  stalePending = false;
  return v;
}

/** 每次切到垃圾清理视图都挂载 / 复用 React 根；结果与 phase 在组件内保留（root 复用，扫描后台继续）。 */
export function showReactClean() {
  const el = document.querySelector<HTMLElement>("#clean-react-root");
  if (el) mountClean(el);
}
