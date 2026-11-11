import { useSyncExternalStore } from "react";
import { t, currentLang, type LangPref } from "../i18n";

/** 语言切换后 main.ts 广播此事件，React 页面据此重渲染。 */
export const LANG_CHANGED = "cleanzo:lang-changed";

let langVersion = 0;
export function bumpLang() {
  langVersion++;
  window.dispatchEvent(new CustomEvent(LANG_CHANGED));
}
function subscribe(onChange: () => void) {
  window.addEventListener(LANG_CHANGED, onChange);
  return () => window.removeEventListener(LANG_CHANGED, onChange);
}
function getSnapshot() {
  return `${currentLang()}:${langVersion}`;
}

/** React 侧的翻译 hook：语言切换时自动更新。 */
export type DictKey = Parameters<typeof t>[0];
type Translate = (key: DictKey, vars?: Record<string, string | number>) => string;

// 同一语言下所有组件拿到同一个函数，依赖 t 的 useCallback / useEffect 只在切换语言时重建。
let cached: { snapshot: string; translate: Translate } | undefined;
export function useT(): Translate {
  const snapshot = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  if (cached?.snapshot !== snapshot) cached = { snapshot, translate: (key, vars) => t(key, vars) };
  return cached.translate;
}

export type { LangPref };
