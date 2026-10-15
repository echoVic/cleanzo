import { beforeEach, expect, test } from "bun:test";
import { connectLanguage, type LanguageBridge } from "../src/language";
import { currentLang, getPref, setNativeLocale, setPref } from "../src/i18n";

const storage = new Map<string, string>();
const local = { getItem: (key: string) => storage.get(key), setItem: (key: string, value: string) => storage.set(key, value) };
Object.assign(globalThis, { localStorage: local });

beforeEach(() => {
  Object.assign(globalThis, { localStorage: local });
  storage.clear();
  setPref("system");
  setNativeLocale("en");
});

function fixture() {
  let listener = (_locale: string) => {};
  let calls: string[] = [];
  let renders: string[] = [];
  const bridge: LanguageBridge = {
    locale: "en",
    async setLanguage(pref) {
      calls.push(pref);
      const locale = pref === "en" ? "en" : "zh-Hans";
      listener(locale); // MyGo may deliver the locale event before the reply.
      return locale;
    },
    onLocaleChange(fn) { listener = fn; return () => { listener = () => {}; }; },
  };
  const language = connectLanguage(bridge, () => renders.push(currentLang()));
  return { bridge, language, calls, renders, emit: (locale: string) => listener(locale) };
}

test("existing manual preference migrates to native language without a transient repaint", async () => {
  setPref("zh");
  const f = fixture();
  await f.language.initialize();
  expect(f.calls).toEqual(["zh"]);
  expect(f.renders).toEqual(["zh"]);
  expect(storage.get("cleanzo.lang")).toBe("zh");
});

test("system follows MyGo's supported locale, and changing back from manual refreshes immediately", async () => {
  const f = fixture();
  await f.language.initialize();
  expect(currentLang()).toBe("zh");
  f.emit("en");
  expect(currentLang()).toBe("en");
  await f.language.change("zh");
  f.emit("en");
  expect(currentLang()).toBe("zh");
  await f.language.change("system");
  expect(getPref()).toBe("system");
  expect(currentLang()).toBe("zh");
  f.language.disconnect();
  f.emit("en");
  expect(currentLang()).toBe("zh");
});

test("failed native change retains both the stored choice and the page language", async () => {
  setPref("zh");
  const f = fixture();
  f.bridge.setLanguage = async () => { throw new Error("disconnected"); };
  await expect(f.language.change("en")).rejects.toThrow("disconnected");
  expect(getPref()).toBe("zh");
  expect(currentLang()).toBe("zh");
  expect(storage.get("cleanzo.lang")).toBe("zh");
  expect(f.renders).toEqual([]);
});

test("browser fallback and unavailable storage still allow switching during this session", async () => {
  Object.assign(globalThis, { localStorage: { getItem() { throw Error(); }, setItem() { throw Error(); } } });
  const language = connectLanguage(undefined, () => {});
  await language.change("zh");
  expect(currentLang()).toBe("zh");
  await language.change("en");
  expect(currentLang()).toBe("en");
});
