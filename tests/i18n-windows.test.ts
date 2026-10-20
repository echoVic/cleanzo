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
