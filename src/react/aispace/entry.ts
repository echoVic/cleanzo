import { mountAISpace } from "./mount";

let mounted = false;

/** 首次进入 AI 空间时挂载 React 页面；之后切回来不再重新挂载。 */
export function showReactAISpace() {
  const el = document.querySelector<HTMLElement>("#aispace-react-root");
  if (!el || mounted) return;
  mounted = true;
  mountAISpace(el);
}
