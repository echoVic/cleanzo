import { expect, test } from "bun:test";
import { ruleDesc, ruleName, t } from "../src/i18n";
import type { CrashDumpSummary } from "../src/mygo";
import { CRASH_RULE_ID, codexSize, oversized } from "../src/react/list/crash-dumps";

const storage = new Map([["cleanzo.lang", "zh"]]);
Object.assign(globalThis, {
  localStorage: {
    getItem: (k: string) => storage.get(k),
    setItem: (k: string, v: string) => storage.set(k, v),
  },
});

const GB = 2 ** 30;

function summary(dirs: CrashDumpSummary["dirs"]): CrashDumpSummary {
  return { total: dirs.reduce((n, d) => n + d.size, 0), threshold: 5 * GB, dirs };
}

test("Codex 的两个目录按应用合计后再比阈值", () => {
  // 单个都不到 5GB，合计超过：要提示，且只报一次 Codex
  const s = summary([
    { app: "Codex", path: "/h/Library/Application Support/com.openai.codex/web/Crashpad", size: 3 * GB },
    { app: "Codex", path: "/h/Library/Application Support/Codex/Crashpad", size: 2.5 * GB },
    { app: "Chrome", path: "/h/Library/Application Support/Google/Chrome/Crashpad", size: 1 * GB },
  ]);
  expect(oversized(s)).toEqual([{ app: "Codex", size: 5.5 * GB }]);
  expect(codexSize(s)).toBe(5.5 * GB);
});

test("没到阈值、测量失败都不提示", () => {
  expect(oversized(summary([{ app: "Codex", path: "/x", size: 4.9 * GB }]))).toEqual([]);
  expect(oversized(null)).toEqual([]);
  expect(codexSize(null)).toBe(0);
});

test("多个应用超阈值时最大的在前", () => {
  const s = summary([
    { app: "Chrome", path: "/a", size: 6 * GB },
    { app: "Codex", path: "/b", size: 300 * GB },
  ]);
  expect(oversized(s).map((a) => a.app)).toEqual(["Codex", "Chrome"]);
});

test("规则与提示文案中英文都有", () => {
  expect(ruleName(CRASH_RULE_ID)).toBe("应用崩溃转储");
  expect(ruleDesc(CRASH_RULE_ID)).toContain("不进废纸篓");
  expect(t("status.alert.crashDumps", { app: "Codex", size: "323 GB" })).toContain("Codex");
  expect(t("junk.crashFiles", { n: "4,040,000" })).toBe("4,040,000 个文件");
  storage.set("cleanzo.lang", "en");
  expect(ruleName(CRASH_RULE_ID)).toBe("App Crash Dumps");
  expect(t("ai.crash.big", { size: "323 GB" })).toContain("323 GB");
  storage.set("cleanzo.lang", "zh");
});
