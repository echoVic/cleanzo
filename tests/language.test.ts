import { beforeEach, expect, test } from "bun:test";
import { connectLanguage, type LanguageBridge } from "../src/language";
import { currentLang, dict, getPref, setNativeLocale, setPref, t } from "../src/i18n";

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

test("plural token picks singular only for exactly 1 in English", () => {
  setPref("en");
  expect(currentLang()).toBe("en");
  expect(t("junk.ignored", { n: 1 })).toBe("· 1 item ignored");
  expect(t("junk.ignored", { n: 2 })).toBe("· 2 items ignored");
  expect(t("junk.ignored", { n: 0 })).toBe("· 0 items ignored");
  expect(t("junk.moreApps", { n: 1, size: "12 MB" })).toBe("1 more app (12 MB total)");
  expect(t("junk.installerAge", { n: 1 })).toBe("Last changed: 1 day ago");
  expect(t("junk.installerAge", { n: "1" })).toBe("Last changed: 1 day ago");
});

test("multiple plural tokens in one string resolve independently", () => {
  setPref("en");
  expect(t("skills.updates.found", { n: 1, m: 1, e: 1 }))
    .toBe("1 skill has updates, 1 was modified locally, 1 check failed");
  expect(t("skills.updates.found", { n: 2, m: 3, e: 0 }))
    .toBe("2 skills have updates, 3 were modified locally, 0 checks failed");
  expect(t("photos.inUse", { n: 1 }))
    .toBe("1 photo is open in another app and was skipped");
  expect(t("photos.inUse", { n: 2 }))
    .toBe("2 photos are open in another app and were skipped");
  expect(t("ai.done.kept", { n: 1 })).toBe("1 item in use, kept");
  expect(t("skills.linkConfirm.body", { n: 1 }))
    .toBe("1 copy is identical to the one in ~/.agents. Each folder moves to the Trash and a link to ~/.agents takes its place.");
});

test("every English plural token resolves for 0, 1 and 2 without leaking syntax", () => {
  setPref("en");
  const tokenRe = /\{(\w+)\|[^{}/]+\/[^{}]+\}/g;
  const keys = Object.keys(dict.en) as Array<keyof typeof dict.en>;
  let checked = 0;
  for (const key of keys) {
    const value = dict.en[key]!;
    if (typeof value !== "string" || !value.includes("|")) continue;
    const vars: Record<string, number> = {};
    for (const m of value.matchAll(tokenRe)) vars[m[1]!] = 1;
    if (Object.keys(vars).length === 0) continue;
    checked++;
    for (const n of [0, 1, 2]) {
      const filled = Object.fromEntries(Object.keys(vars).map((k) => [k, n]));
      const out = t(key, filled);
      expect(out).not.toMatch(/\{\w+\|/);
      expect(out).not.toContain("|");
    }
  }
  expect(checked).toBeGreaterThan(20);
});

test("Chinese dictionary contains no plural token syntax", () => {
  const tokenRe = /\|\s*[^}]*\//;
  for (const value of Object.values(dict.zh)) {
    if (typeof value === "string") expect(value).not.toMatch(tokenRe);
  }
});

test("Chinese counters never leak plural token syntax", () => {
  setPref("zh");
  const out = t("junk.ignored", { n: 1 });
  expect(out).toBe("· 已忽略 1 项");
  expect(out).not.toContain("|");
});
