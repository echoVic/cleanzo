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
export function useT() {
  useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  return (key: DictKey, vars?: Record<string, string | number>) => t(key, vars);
}

export type { LangPref };
