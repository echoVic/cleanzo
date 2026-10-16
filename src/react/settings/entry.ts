// React 设置页与原生导航的桥接：main.ts 调 showReactSettings() 即可，不直接接触 React。
import { mountSettings } from "./mount";
import type { SettingsCallbacks } from "./SettingsPage";
import type { LangPref } from "../../ui/i18n-react";

const callbacks: SettingsCallbacks = { changeLanguage: async () => {} };
let session = 0;

/** main.ts 注入语言切换回调（走已有的 language.change + toast）。 */
export function setSettingsLangHandler(changeLanguage: (pref: LangPref) => Promise<void>) {
  callbacks.changeLanguage = changeLanguage;
}

/** 每次进入设置页都重新读取配置（垃圾页可能新增了忽略项）。 */
export function showReactSettings() {
  session++;
  const el = document.querySelector<HTMLElement>("#settings-react-root");
  if (el) mountSettings(el, callbacks, session);
}
