// 被测试文件最先 import：先于 shared.ts 的顶层 querySelector 装好最小 DOM 环境。
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

export function setLang(lang: string) {
  storage.set("cleanzo.lang", lang);
}
