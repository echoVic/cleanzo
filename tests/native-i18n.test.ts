import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import nativeMessages from "../src/native-i18n.json";

const source = readFileSync(new URL("../src/i18n.ts", import.meta.url), "utf8");
const [zh, en] = source.split(/^  en: \{$/m);

// 比较字典里的原文，不通过 t()，避免 nativeMessages 的展开掩盖差异。
function rawMessages(block: string): Record<string, string> {
  return Object.fromEntries([...block.matchAll(
    /^    "([^"]+)": ("(?:\\.|[^"\\])*"),?$/gm,
  )].map((m) => [m[1]!, JSON.parse(m[2]!) as string]));
}

test("native Chinese and English have the same keys", () => {
  expect(Object.keys(nativeMessages.zh).sort()).toEqual(Object.keys(nativeMessages.en).sort());
});

test("native strings equal the web dictionary's raw strings, including placeholders", () => {
  expect(zh).toBeDefined();
  expect(en).toBeDefined();
  for (const [lang, block] of [["zh", zh!], ["en", en!]] as const) {
    const web = rawMessages(block);
    expect(Object.keys(web).length).toBeGreaterThan(700);
    for (const [key, value] of Object.entries(nativeMessages[lang])) {
      if (Object.hasOwn(web, key)) expect({ lang, key, value }).toEqual({ lang, key, value: web[key] });
    }
  }
});

