import nativeMessages from "../src/native-i18n.json";
import { afterEach, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { ruleDesc, ruleName, setPref, t } from "../src/i18n";
import { setPlatformForTest } from "../src/platform";

// 无豁免：隐藏页的文案也有覆盖，避免以后复用时漏出平台术语。
const allowList: Record<string, string> = {};
const forbidden = /废纸篓|访达|Finder|Trash|macOS|\bMac\b|系统设置|System Settings|Dock|活动监视器|Activity Monitor|~\/Library|\.app|完全磁盘访问|Full Disk Access/;
const source = readFileSync(new URL("../src/i18n.ts", import.meta.url), "utf8");
const keys = [...Object.keys(nativeMessages.zh), ...new Set([...source.matchAll(/^    "([^"]+)":/gm)].map(m => m[1]!))];
afterEach(() => { setPlatformForTest(undefined); setPref("system"); });

test("Windows resolves every zh/en dictionary value without macOS terms", () => {
  setPlatformForTest("win32");
  const leaks: string[] = [];
  for (const lang of ["zh", "en"] as const) {
    setPref(lang);
    for (const key of keys) {
      if (key in allowList) continue;
      const value = t(key as Parameters<typeof t>[0]);
      if (forbidden.test(value)) leaks.push(`${lang} ${key}: ${value}`);
    }
  }
  expect(keys.length).toBeGreaterThan(700);
  expect(leaks).toEqual([]);
});

test("Windows overrides precede the glossary; interpolation does not rewrite paths or app names", () => {
  setPlatformForTest("win32");
  setPref("en");
  expect(t("status.mem.wired")).toBe("Kernel");
  expect(t("fs.reveal")).toBe("Show in File Explorer");
  expect(ruleName("sys.trash")).toBe("Recycle Bin");
  expect(ruleDesc("review.installers")).toContain("MSI");
  expect(t("ai.notFound", { name: "Finder.app" })).toBe("Finder.app wasn't found on this PC.");
  expect(ruleName("unknown")).toBe("unknown");
  setPlatformForTest("darwin");
  expect(t("status.mem.wired")).toBe("Wired Memory");
  expect(ruleName("sys.trash")).toBe("Trash");
});

test("Recycle Bin refusal codes explain retained and connected files in both languages", async () => {
  const previous = globalThis.document;
  Object.assign(globalThis, { document: previous ?? { querySelectorAll: () => [] } });
  const { errText } = await import("../src/shared");
  Object.assign(globalThis, { document: previous });
  setPlatformForTest("win32");
  for (const [lang, retained, connected] of [
    ["zh", "无法移到回收站，已保留原文件（可能超过回收站容量，或该位置没有回收站）", "这个网页文件和同名附属文件夹相连，Windows 会把它们一起移走，请在文件资源管理器中处理"],
    ["en", "Couldn't move it to the Recycle Bin, so it was kept (it may exceed the Recycle Bin's size, or the location has no Recycle Bin)", "This web page is linked to a folder of the same name, and Windows would move both; handle it in File Explorer"],
  ] as const) {
    setPref(lang);
    expect(errText(new Error("not-recycled"))).toBe(retained);
    expect(errText("connected-files")).toBe(connected);
    expect(t("skills.err.not-recycled" as never)).toBe(retained);
    expect(t("skills.err.connected-files" as never)).toBe(connected);
  }
});
