import "./setup-dom";
import { expect, spyOn, test } from "bun:test";
import type { AIGroup, AIPart, AIScanResult, AITool, CrashDumpSummary } from "../src/mygo";
import { t } from "../src/i18n";
import { offered, groupSelectable, canPick, type WindowDays } from "../src/react/ai/types";
import { human, displayPath } from "../src/react/ai/format";

const DAY = 86400;
const NOW = 1_700_000_000;

function part(level: AIPart["level"], groups: AIGroup[] = [], size = 1): AIPart {
  return {
    id: `${level}-${Math.random()}`,
    name: level,
    level,
    size,
    path: "/tmp/x",
    runSafe: false,
    groups,
  } as AIPart;
}

function group(over: Partial<AIGroup> = {}): AIGroup {
  return {
    key: "g",
    label: "g",
    path: "/tmp/g",
    size: 100,
    count: 5,
    recent: 0,
    last: NOW - 31 * DAY,
    gone: false,
    ...over,
  } as AIGroup;
}

test("offered hides protected and unknown parts only", () => {
  expect(offered(part("safe"))).toBe(true);
  expect(offered(part("optional"))).toBe(true);
  expect(offered(part("sensitive"))).toBe(true);
  expect(offered(part("protected"))).toBe(false);
  expect(offered(part("unknown"))).toBe(false);
});

test("non-session groups are selectable whenever nonempty", () => {
  expect(groupSelectable(part("safe"), group(), 30, NOW)).toBe(true);
  expect(groupSelectable(part("safe"), group({ size: 0 }), 30, NOW)).toBe(false);
});

test("session groups need entries beyond today's and an age outside the window", () => {
  const p = part("sensitive");
  // 全部是最近会话：不可选
  expect(groupSelectable(p, group({ count: 3, recent: 3 }), 30, NOW)).toBe(false);
  // 在 30 天窗口内：不可选
  expect(groupSelectable(p, group({ count: 5, recent: 1, last: NOW - 10 * DAY }), 30 as WindowDays, NOW)).toBe(false);
  // 窗口外且有旧会话：可选
  expect(groupSelectable(p, group({ count: 5, recent: 1, last: NOW - 31 * DAY }), 30 as WindowDays, NOW)).toBe(true);
  // 7 天窗口：31 天前可选
  expect(groupSelectable(p, group({ count: 5, recent: 1, last: NOW - 8 * DAY }), 7 as WindowDays, NOW)).toBe(true);
  // 90 天窗口：31 天前不可选
  expect(groupSelectable(p, group({ count: 5, recent: 1, last: NOW - 31 * DAY }), 90 as WindowDays, NOW)).toBe(false);
});

test("canPick needs a group that the current window lets through", () => {
  const sessions = part("sensitive", [group({ count: 5, recent: 1, last: NOW - 10 * DAY })], 100);
  expect(canPick(sessions, 30, NOW)).toBe(false);
  expect(canPick(sessions, 7, NOW)).toBe(true);
  expect(canPick(part("safe", [group({ size: 0 })], 100), 30, NOW)).toBe(false);
  expect(canPick(part("safe", [], 0), 30, NOW)).toBe(false);
  expect(canPick(part("safe", [], 10), 30, NOW)).toBe(true);
  expect(canPick({ ...part("safe", [], 10), running: true }, 30, NOW)).toBe(false);
  expect(canPick({ ...part("safe", [], 10), running: true, runSafe: true }, 30, NOW)).toBe(true);
});

test("human formats bytes the same way the rest of the UI does", () => {
  expect(human(0)).toBe("0 B");
  expect(human(512)).toBe("512 B");
  expect(human(1536)).toBe("1.5 KB");
  expect(human(2048)).toBe("2.0 KB");
  expect(human(5 * 1024 * 1024)).toBe("5.0 MB");
  expect(human(5 * 1024 ** 3)).toBe("5.0 GB");
  expect(human(1500 * 1024 ** 3)).toBe("1.5 TB");
});

test("displayPath collapses the home directory", () => {
  expect(displayPath("/Users/alice/.claude/projects/x")).toBe("~/.claude/projects/x");
  expect(displayPath("/var/tmp/x")).toBe("/var/tmp/x");
});

// ---- 页面：扫描、清理、勾选（无浏览器，见 page-harness.ts） ----

const GB = 2 ** 30;

function fullPart(id: string, level: string, over: Partial<AIPart> = {}): AIPart {
  return { id, name: id, path: `/Users/a/.codex/${id}`, level, size: 100, files: 1, last: NOW, groups: [], top: [], more: 0, keep: "", runSafe: false, running: false, ...over };
}

function codex(parts: AIPart[]): AITool {
  return { id: "codex", name: "Codex", path: "/Users/a/.codex", installed: true, running: false, runningApps: [], size: parts.reduce((n, p) => n + p.size, 0), parts };
}

function dumps(size: number): CrashDumpSummary {
  return { total: size, threshold: 5 * GB, dirs: size ? [{ app: "Codex", path: "/Users/a/Library/Application Support/Codex/Crashpad", size }] : [] };
}

async function aiPage(scans: (() => Promise<AIScanResult>)[], crash: CrashDumpSummary[]) {
  await import("./setup-dom");
  const { pageHarness } = await import("./page-harness");
  const { AIToolPage } = await import("../src/react/ai/AIToolPage");
  const { aiApi } = await import("../src/react/ai/api");
  const { Cleaner } = await import("../src/mygo");
  const { forgetCrashDumps } = await import("../src/react/list/crash-dumps");
  forgetCrashDumps();
  const cleaned: { tool: string; part: string; groups: string[] }[][] = [];
  let crashReads = 0;
  const spies = [
    spyOn(aiApi, "scanTool").mockImplementation(() => (scans.shift() ?? (() => Promise.reject(new Error("no scan"))))()),
    spyOn(aiApi, "clean").mockImplementation(async (sel) => { cleaned.push(sel); return { freed: 100, parts: [] }; }),
    spyOn(Cleaner, "crashDumps").mockImplementation(async () => crash[Math.min(crashReads++, crash.length - 1)]!),
  ];
  const page = pageHarness(AIToolPage, { toolId: "codex", toolName: "Codex" });
  return {
    page, cleaned, forgetCrashDumps,
    /** 空闲页的「扫描」或页头的「重新扫描」。 */
    scan() {
      const hero = page.find((el) => typeof el.props.onCta === "function");
      if (hero) hero.props.onCta(); else page.click(t("fs.rescan"));
    },
    part(id: string) { return page.find((el) => el.props.part?.id === id && "onTogglePart" in el.props)!; },
    cleanButton() { return page.find((el) => el.props.size === "lg")!; },
    confirm() { page.find((el) => typeof el.props.onConfirm === "function")!.props.onConfirm(); },
    close() { page.close(); for (const s of spies) s.mockRestore(); forgetCrashDumps(); },
  };
}

const scanned = (tool: AITool) => () => Promise.resolve({ tools: [tool], scannedAt: NOW });

test("the Codex crash-dump banner is measured again with each scan", async () => {
  const banner = t("ai.crash.big", { size: human(6 * GB) });
  const ai = await aiPage([scanned(codex([fullPart("codex.cache", "safe")]))], [dumps(6 * GB), dumps(0)]);
  try {
    await ai.page.settle();
    expect(ai.page.text()).toContain(banner);
    // 垃圾清理清掉崩溃转储后会让缓存失效；回到这一页再扫描，提示跟着变。
    ai.forgetCrashDumps();
    ai.scan();
    await ai.page.settle();
    expect(ai.page.text()).not.toContain(banner);
  } finally { ai.close(); }
});

test("a rescan that fails after a clean shows the failure, not the old list as cleaned", async () => {
  const tool = codex([fullPart("codex.cache", "safe")]);
  const ai = await aiPage([scanned(tool), () => Promise.reject(new Error("disk gone"))], [dumps(0)]);
  try {
    await ai.page.settle();
    ai.scan();
    await ai.page.settle();
    expect(ai.cleanButton().props.disabled).toBe(false);
    ai.confirm();
    await ai.page.settle();
    expect(ai.cleaned).toEqual([[{ tool: "codex", part: "codex.cache", groups: [] }]]);
    expect(ai.page.text()).toContain(t("scan.error", { msg: "disk gone" }));
    expect(ai.page.find((el) => el.props.part?.id === "codex.cache")).toBeUndefined();
    // 清理本身成功了：结果另外告诉用户。
    expect(ai.page.toasts.at(-1)).toEqual({ text: t("ai.done", { size: human(100) }), kind: "ok" });
  } finally { ai.close(); }
});

test("a sessions part with no selectable group cannot be ticked into a checked 0 B", async () => {
  const today = fullPart("codex.sessions", "sensitive", { size: 500, groups: [group({ key: "p", count: 3, recent: 3, last: NOW })] });
  const ai = await aiPage([scanned(codex([today]))], [dumps(0)]);
  try {
    await ai.page.settle();
    ai.scan();
    await ai.page.settle();
    expect(ai.part("codex.sessions").props.canSelect).toBe(false);
    ai.part("codex.sessions").props.onTogglePart(true);
    await ai.page.settle();
    expect(ai.part("codex.sessions").props.picked).toBe(false);
    expect(ai.cleanButton().props.disabled).toBe(true);
  } finally { ai.close(); }
});
