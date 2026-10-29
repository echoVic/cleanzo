// React 设置页与原生导航的桥接：main.ts 调 showReactSettings() 即可，不直接接触 React。
import { mountSettings } from "./mount";
import type { SettingsCallbacks } from "./SettingsPage";
import type { LangPref } from "../../ui/i18n-react";

const callbacks: SettingsCallbacks = { changeLanguage: async () => {} };
let session = 0;
let focus = { id: "", nonce: 0 };

/** 下次进入设置页时滚到哪一节（「菜单栏设置…」进来时为 "menubar"）；调用方随后切到设置页。 */
export function requestSettingsSection(id: string) {
  focus = { id, nonce: focus.nonce + 1 };
}

/** main.ts 注入语言切换回调（走已有的 language.change + toast）。 */
export function setSettingsLangHandler(changeLanguage: (pref: LangPref) => Promise<void>) {
  callbacks.changeLanguage = changeLanguage;
}

/** 每次进入设置页都重新读取配置（垃圾页可能新增了忽略项）。 */
export function showReactSettings() {
  session++;
  const el = document.querySelector<HTMLElement>("#settings-react-root");
  if (el) mountSettings(el, callbacks, session, focus);
}
