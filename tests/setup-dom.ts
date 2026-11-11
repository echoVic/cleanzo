// 被测试文件最先 import：装好最小 DOM 环境与 localStorage。
import { setPref, type Lang } from "../src/i18n";

class Element {
  textContent = "";
  disabled = false;
  dataset: Record<string, string> = {};
  style: Record<string, string> = {};
  addEventListener() {}
}

const storage = new Map<string, string>([["cleanzo.lang", "zh"]]);

Object.assign(globalThis, {
  document: {
    querySelector: () => new Element(),
    querySelectorAll: () => [],
    addEventListener() {},
  },
  localStorage: {
    getItem: (key: string) => storage.get(key),
    setItem: (key: string, v: string) => storage.set(key, v),
  },
});

export function setLang(lang: Lang) {
  storage.set("cleanzo.lang", lang);
  setPref(lang);
}
