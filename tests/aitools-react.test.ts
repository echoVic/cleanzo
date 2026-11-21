import "./setup-dom";
import { expect, test } from "bun:test";
import type { AICleanResult, AIGroup, AIPart, AIScanResult, AISelection, AITool, ClaudeRunSummary, CrashDumpSummary } from "../src/mygo";
import { t } from "../src/i18n";
import { offered, isSessions, groupSelectable, canPick, worktreeSeen, type WindowDays } from "../src/react/ai/types";
import { human, displayPath } from "../src/react/ai/format";
import { nodes, textOf, type Changes } from "./page-harness";

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

/** 搭好 Codex 页：改动都记在 env 里，由测试的 finally 还原。 */
async function aiPage(env: Changes, scans: (() => Promise<AIScanResult>)[], crash: CrashDumpSummary[]) {
  const { AIToolPage } = await import("../src/react/ai/AIToolPage");
  const { aiApi } = await import("../src/react/ai/api");
  const { Cleaner } = await import("../src/mygo");
  const { forgetCrashDumps } = await import("../src/react/list/crash-dumps");
  env.add(forgetCrashDumps);
  forgetCrashDumps();
  const cleaned: { tool: string; part: string; groups: string[] }[][] = [];
  let crashReads = 0;
  env.spy(aiApi, "scanTool").mockImplementation(() => (scans.shift() ?? (() => Promise.reject(new Error("no scan"))))());
  env.spy(aiApi, "clean").mockImplementation(async (sel) => { cleaned.push(sel); return { freed: 100, parts: [] }; });
  env.spy(Cleaner, "crashDumps").mockImplementation(async () => crash[Math.min(crashReads++, crash.length - 1)]!);
  const page = env.page(AIToolPage, { toolId: "codex", toolName: "Codex", visit: 1, active: true, request: { tab: null, nonce: 0 } });
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
  };
}

const scanned = (tool: AITool) => () => Promise.resolve({ tools: [tool], scannedAt: NOW });

test("the Codex crash-dump banner is measured again with each scan", async () => {
  const banner = t("ai.crash.big", { size: human(6 * GB) });
  const env = (await import("./page-harness")).changes();
  try {
    const ai = await aiPage(env, [scanned(codex([fullPart("codex.cache", "safe")]))], [dumps(6 * GB), dumps(0)]);
    await ai.page.settle();
    expect(ai.page.text()).toContain(banner);
    // 垃圾清理清掉崩溃转储后会让缓存失效；回到这一页再扫描，提示跟着变。
    ai.forgetCrashDumps();
    ai.scan();
    await ai.page.settle();
    expect(ai.page.text()).not.toContain(banner);
  } finally { env.restore(); }
});

test("the Codex crash-dump banner is read again each time the view is shown", async () => {
  const banner = t("ai.crash.big", { size: human(6 * GB) });
  const env = (await import("./page-harness")).changes();
  try {
    const ai = await aiPage(env, [], [dumps(6 * GB), dumps(0)]);
    await ai.page.settle();
    expect(ai.page.text()).toContain(banner);
    // 垃圾清理清掉崩溃转储后缓存失效；离开再回到这一页，不扫描，提示也跟着变。
    ai.forgetCrashDumps();
    ai.page.setProps({ visit: 2 });
    await ai.page.settle();
    expect(ai.page.text()).not.toContain(banner);
  } finally { env.restore(); }
});

test("each time an AI tool view is shown, its page is rendered with a new visit", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    const mounted: [string, number][] = [];
    // 截获 mountAITool 收到的参数，不挂真的 React 根；entry 建容器用的 document 换成最小替身。
    const mount = await import("../src/react/ai/mount");
    env.spy(mount, "mountAITool").mockImplementation((_el, toolId, _toolName, visit, active) => { if (active) mounted.push([toolId, visit]); });
    env.assign(globalThis, { document: {
      createElement: () => ({ className: "", classList: { toggle() {} } }),
      querySelector: () => ({ appendChild() {} }),
    } });
    const { showReactAITool } = await import("../src/react/ai/entry");
    showReactAITool("codex", "Codex");
    showReactAITool("claude", "Claude Code");
    showReactAITool("codex", "Codex");
    const codexVisits = mounted.filter(([id]) => id === "codex").map(([, visit]) => visit);
    expect(codexVisits).toHaveLength(2);
    expect(codexVisits[1]).not.toBe(codexVisits[0]);
  } finally { env.restore(); }
});

test("a rescan that fails after a clean shows the failure, not the old list as cleaned", async () => {
  const tool = codex([fullPart("codex.cache", "safe")]);
  const env = (await import("./page-harness")).changes();
  try {
    const ai = await aiPage(env, [scanned(tool), () => Promise.reject(new Error("disk gone"))], [dumps(0)]);
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
  } finally { env.restore(); }
});

test("a sessions part with no selectable group cannot be ticked into a checked 0 B", async () => {
  const today = fullPart("codex.sessions", "sensitive", { size: 500, groups: [group({ key: "p", count: 3, recent: 3, last: NOW })] });
  const env = (await import("./page-harness")).changes();
  try {
    const ai = await aiPage(env, [scanned(codex([today]))], [dumps(0)]);
    await ai.page.settle();
    ai.scan();
    await ai.page.settle();
    expect(ai.part("codex.sessions").props.canSelect).toBe(false);
    ai.part("codex.sessions").props.onTogglePart(true);
    await ai.page.settle();
    expect(ai.part("codex.sessions").props.picked).toBe(false);
    expect(ai.cleanButton().props.disabled).toBe(true);
  } finally { env.restore(); }
});

// ---- Orca：第四个工具 ----

test("the Orca sidebar entry starts hidden and shows only when the tool is listed as installed", async () => {
  const { readFileSync } = await import("node:fs");
  const html = readFileSync(new URL("../index.html", import.meta.url), "utf8");
  // 侧栏里的工具按钮，按 data-tool 记下各自的 class。
  const buttons = new Map<string, Set<string>>();
  for (const m of html.matchAll(/<button class="([^"]*)" data-view="aitools" data-tool="([^"]+)"/g)) {
    buttons.set(m[2]!, new Set(m[1]!.split(" ")));
  }
  expect([...buttons.keys()]).toEqual(["claude", "codex", "trae", "orca"]);
  expect(buttons.get("orca")!.has("hidden")).toBe(true);
  const env = (await import("./page-harness")).changes();
  try {
    const { aiApi } = await import("../src/react/ai/api");
    const { initReactAITools } = await import("../src/react/ai/entry");
    env.assign(globalThis, { document: {
      querySelector: (sel: string) => {
        const cls = buttons.get(/data-tool="([^"]+)"/.exec(sel)?.[1] ?? "");
        return cls ? { classList: { toggle: (c: string, on: boolean) => { if (on) cls.add(c); else cls.delete(c); } } } : null;
      },
    } });
    let orca = true;
    env.spy(aiApi, "listTools").mockImplementation(async () => [
      { id: "claude", name: "Claude Code", installed: false },
      { id: "codex", name: "Codex", installed: true },
      { id: "trae", name: "TRAE", installed: false },
      { id: "orca", name: "Orca", installed: orca },
    ]);
    await initReactAITools();
    expect(buttons.get("orca")!.has("hidden")).toBe(false);
    orca = false;
    await initReactAITools();
    expect(buttons.get("orca")!.has("hidden")).toBe(true);
  } finally { env.restore(); }
  // 点进去时页面标题用工具自己的名字。
  const main = readFileSync(new URL("../src/main.ts", import.meta.url), "utf8");
  expect(main).toContain('orca: "Orca"');
});

test("Orca's page and every part it can list read in Chinese and English", async () => {
  const { readFileSync } = await import("node:fs");
  const go = readFileSync(new URL("../aitools.go", import.meta.url), "utf8");
  const ids = [...go.matchAll(/\{ID: "(orca\.[A-Za-z]+)"/g)].map((m) => m[1]!);
  expect(ids).toContain("orca.sessions");
  expect(ids).toContain("orca.auth");
  const env = (await import("./page-harness")).changes();
  try {
    for (const lang of ["zh", "en"] as const) {
      env.lang(lang);
      const keys = ["ai.head.orca", "ai.idle.guide.orca", "ai.part.orca.sessions.lose", "ai.part.orca.taskSessions.lose",
        ...ids.flatMap((id) => [`ai.part.${id}.name`, `ai.part.${id}.desc`])];
      for (const key of keys) expect(t(key as never)).not.toBe(key);
    }
  } finally { env.restore(); }
});

test("Orca's idle page names what a scan looks for: conversations and caches, no file snapshots", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    const { AIToolPage } = await import("../src/react/ai/AIToolPage");
    const page = env.page(AIToolPage, { toolId: "orca", toolName: "Orca", visit: 1, active: true, request: { tab: null, nonce: 0 } });
    await page.settle();
    expect(page.text()).toContain(t("ai.head.orca" as never));
    const hero = page.find((el) => typeof el.props.onCta === "function")!;
    expect(hero.props.guide).toBe(t("ai.idle.guide.orca" as never));
    expect(hero.props.chips).toEqual([t("ai.chip.sessions"), t("ai.chip.cache")]);
  } finally { env.restore(); }
});

// ---- 页内标签：Claude Code 页的「空间」和「运行中」 ----

/** entry 建容器用的最小 document。 */
const hostDocument = {
  createElement: () => ({ className: "", classList: { toggle() {} } }),
  querySelector: () => ({ appendChild() {} }),
};

test("a tool page is rendered active when shown, and inactive with the same visit when another tool or another view is shown", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    const mounted: [string, number, boolean][] = [];
    const mount = await import("../src/react/ai/mount");
    env.spy(mount, "mountAITool").mockImplementation((_el, toolId, _toolName, visit, active) => { mounted.push([toolId, visit, active]); });
    env.assign(globalThis, { document: hostDocument });
    const { leaveReactAITools, showReactAITool } = await import("../src/react/ai/entry");
    leaveReactAITools();
    mounted.length = 0;
    showReactAITool("codex", "Codex");
    showReactAITool("claude", "Claude Code");
    leaveReactAITools();
    leaveReactAITools();
    const codexVisit = mounted[0]![1];
    const claudeVisit = mounted[2]![1];
    expect(mounted).toEqual([["codex", codexVisit, true], ["codex", codexVisit, false], ["claude", claudeVisit, true], ["claude", claudeVisit, false]]);
    expect(claudeVisit).not.toBe(codexVisit);
  } finally { env.restore(); }
});

test("a tab request reaches the tool's page with a new nonce; showing the page again passes the same one", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    const requests: [string, unknown][] = [];
    const mount = await import("../src/react/ai/mount");
    env.spy(mount, "mountAITool").mockImplementation((_el, toolId, _toolName, _visit, active, request) => { if (active) requests.push([toolId, request]); });
    env.assign(globalThis, { document: hostDocument });
    const { requestAITab, showReactAITool } = await import("../src/react/ai/entry");
    showReactAITool("trae", "TRAE");
    requestAITab("claude", "running");
    showReactAITool("claude", "Claude Code");
    showReactAITool("claude", "Claude Code");
    requestAITab("claude", "running");
    showReactAITool("claude", "Claude Code");
    const [trae, first, again, next] = requests;
    expect(trae).toEqual(["trae", { tab: null, nonce: 0 }]);
    const nonce = (first![1] as { nonce: number }).nonce;
    expect(first).toEqual(["claude", { tab: "running", nonce }]);
    expect(again).toEqual(["claude", { tab: "running", nonce }]);
    expect(next).toEqual(["claude", { tab: "running", nonce: nonce + 1 }]);
  } finally { env.restore(); }
});

function claudeTool(over: Partial<AITool> = {}): AITool {
  return { id: "claude", name: "Claude Code", path: "/Users/a/.claude", installed: true, running: false, runningApps: [], size: 100, parts: [fullPart("claude.cache", "safe")], ...over };
}

/** 搭好 Claude Code 页（或 toolId 指的别的工具页）：扫描、摘要换成替身；改动都记在 env 里，由测试的 finally 还原。 */
async function toolPage(env: Changes, o: {
  toolId?: string; scans?: (() => Promise<AIScanResult>)[]; summary?: () => Promise<ClaudeRunSummary>;
  request?: { tab: "space" | "running" | null; nonce: number };
} = {}) {
  const { AIToolPage } = await import("../src/react/ai/AIToolPage");
  const { RunningPanel } = await import("../src/react/ai/RunningPanel");
  const { Trigger } = await import("@radix-ui/react-tabs");
  const { aiApi, runningApi } = await import("../src/react/ai/api");
  const { Cleaner } = await import("../src/mygo");
  const scans = o.scans ?? [];
  let scanCalls = 0;
  env.spy(aiApi, "scanTool").mockImplementation(() => { scanCalls++; return (scans.shift() ?? (() => Promise.reject(new Error("no scan"))))(); });
  env.spy(Cleaner, "crashDumps").mockImplementation(async () => dumps(0));
  const summary = env.spy(runningApi, "summary").mockImplementation(o.summary ?? (async () => ({ idle: 0, left: 0, rows: [] })));
  const toolId = o.toolId ?? "claude";
  const page = env.page(AIToolPage, { toolId, toolName: toolId === "claude" ? "Claude Code" : "Codex", visit: 1, active: true, request: o.request ?? { tab: null, nonce: 0 } });
  await page.settle();
  const tabs = () => page.find((el) => typeof el.props.onValueChange === "function");
  return {
    page, summary,
    get scans() { return scanCalls; },
    /** 当前标签；没有标签时为 undefined。 */
    tab: () => tabs()?.props.value as string | undefined,
    triggers: () => page.ofType(Trigger).map((el) => el.props.value as string),
    async pick(value: string) { tabs()!.props.onValueChange(value); await page.settle(); },
    panel: () => page.ofType(RunningPanel)[0],
    badge: () => page.find((el) => el.props.className === "pr-badge")?.props.children,
    async scan() { page.find((el) => typeof el.props.onCta === "function")!.props.onCta(); await page.settle(); },
    hasButton: (label: string) => page.all().some((el) => typeof el.props.onClick === "function" && textOf(el.props.children).trim() === label),
  };
}

test("the Claude Code page has the Space and Running tabs; the other tools and Windows do not", async () => {
  const { setPlatformForTest } = await import("../src/platform");
  const { changes } = await import("./page-harness");
  let env = changes();
  try {
    const claude = await toolPage(env);
    expect(claude.triggers()).toEqual(["space", "running"]);
    expect(claude.page.text()).toContain(t("ai.tab.space") + t("ai.tab.running"));
    expect(claude.panel()).toBeDefined();
  } finally { env.restore(); }
  env = changes();
  try {
    const other = await toolPage(env, { toolId: "codex" });
    expect(other.tab()).toBeUndefined();
    expect(other.triggers()).toEqual([]);
    expect(other.panel()).toBeUndefined();
  } finally { env.restore(); }
  env = changes();
  try {
    env.add(() => setPlatformForTest(undefined));
    setPlatformForTest("win32");
    const windows = await toolPage(env);
    expect(windows.tab()).toBeUndefined();
    expect(windows.panel()).toBeUndefined();
    expect(windows.summary).not.toHaveBeenCalled();
  } finally { env.restore(); }
});

test("the page opens on Space, or on the tab a new request names; the same nonce again goes back to Space", async () => {
  const { changes } = await import("./page-harness");
  let env = changes();
  try {
    const plain = await toolPage(env);
    expect(plain.tab()).toBe("space");
    expect(plain.panel()!.props.active).toBe(false);
  } finally { env.restore(); }
  env = changes();
  try {
    const c = await toolPage(env, { request: { tab: "running", nonce: 1 } });
    expect(c.tab()).toBe("running");
    expect(c.panel()!.props.active).toBe(true);
    // 「运行中」标签下只有 RunningPanel：空间的内容不画
    expect(c.page.find((el) => typeof el.props.onCta === "function")).toBeUndefined();
    c.page.setProps({ visit: 2 });
    await c.page.settle();
    expect(c.tab()).toBe("space");
    expect(c.panel()!.props.active).toBe(false);
    expect(c.page.find((el) => typeof el.props.onCta === "function")).toBeDefined();
    c.page.setProps({ visit: 3, request: { tab: "running", nonce: 2 } });
    await c.page.settle();
    expect(c.tab()).toBe("running");
    // 页面离开屏幕：「运行中」不再读
    c.page.setProps({ active: false });
    await c.page.settle();
    expect(c.panel()!.props.active).toBe(false);
  } finally { env.restore(); }
});

test("the Running tab's number: the summary on entry, then whatever the panel counts; no number at 0; Codex reads none", async () => {
  const { changes } = await import("./page-harness");
  let env = changes();
  try {
    const c = await toolPage(env, { summary: async () => ({ idle: 4, left: 2, rows: [] }) });
    expect(c.summary).toHaveBeenCalledTimes(1);
    expect(c.badge()).toBe(6);
    c.panel()!.props.onCount(3);
    await c.page.settle();
    expect(c.badge()).toBe(3);
    c.panel()!.props.onCount(0);
    await c.page.settle();
    expect(c.badge()).toBeUndefined();
    c.page.setProps({ visit: 2 });
    await c.page.settle();
    expect(c.summary).toHaveBeenCalledTimes(2);
    expect(c.badge()).toBe(6);
  } finally { env.restore(); }
  env = changes();
  try {
    const zero = await toolPage(env);
    expect(zero.badge()).toBeUndefined();
  } finally { env.restore(); }
  env = changes();
  try {
    // 摘要晚于面板回来：不盖掉面板报的数
    let answer!: (s: ClaudeRunSummary) => void;
    const late = await toolPage(env, { summary: () => new Promise((yes) => { answer = yes; }) });
    late.panel()!.props.onCount(3);
    await late.page.settle();
    answer({ idle: 4, left: 2, rows: [] });
    await late.page.settle();
    expect(late.badge()).toBe(3);
  } finally { env.restore(); }
  env = changes();
  try {
    const other = await toolPage(env, { toolId: "codex" });
    expect(other.summary).not.toHaveBeenCalled();
  } finally { env.restore(); }
});

test("while Claude Code runs, the scan's note links to the Running tab", async () => {
  const { changes } = await import("./page-harness");
  let env = changes();
  try {
    const c = await toolPage(env, { scans: [scanned(claudeTool({ running: true }))] });
    await c.scan();
    expect(c.page.text()).toContain(t("ai.running", { name: "Claude Code" }) + t("ai.run.see"));
    c.page.click(t("ai.run.see"));
    await c.page.settle();
    expect(c.tab()).toBe("running");
  } finally { env.restore(); }
  env = changes();
  try {
    const other = await toolPage(env, { toolId: "codex", scans: [scanned({ ...codex([fullPart("codex.cache", "safe")]), running: true })] });
    await other.scan();
    expect(other.page.text()).toContain(t("ai.running", { name: "Codex" }));
    expect(other.page.text()).not.toContain(t("ai.run.see"));
  } finally { env.restore(); }
});

test("the header button follows the tab: Rescan on Space after a scan, Refresh on Running; Space keeps its scan", async () => {
  const { changes } = await import("./page-harness");
  const env = changes();
  try {
    const c = await toolPage(env, { scans: [scanned(claudeTool())] });
    await c.scan();
    expect(c.hasButton(t("fs.rescan"))).toBe(true);
    await c.pick("running");
    expect(c.hasButton(t("fs.rescan"))).toBe(false);
    expect(c.panel()!.props.refresh).toBe(0);
    c.page.click(t("ai.run.refresh"));
    await c.page.settle();
    expect(c.panel()!.props.refresh).toBe(1);
    expect(c.panel()!.props.active).toBe(true);
    await c.pick("space");
    expect(c.hasButton(t("ai.run.refresh"))).toBe(false);
    expect(c.hasButton(t("fs.rescan"))).toBe(true);
    expect(c.page.find((el) => el.props.part?.id === "claude.cache")).toBeDefined();
    expect(c.scans).toBe(1);
  } finally { env.restore(); }
});

// ---- Agent worktree：一个 worktree 一组，只有干净的和带改动的能勾 ----

const MB = 2 ** 20;

/** worktree 的十种状态，后端判定的顺序；只有前两种能移走。 */
const WT_STATES = ["clean", "dirty", "in-use", "holds-worktrees", "submodules", "mid-operation", "unbranched-commits", "git-unavailable", "index-only", "unrecognized"] as const;

/** 仓库 /Users/a/code/app 里的一个 worktree：key 和 path 是它的文件夹，名字是文件夹名，8 天没动。 */
function worktree(name: string, state: string, over: Partial<AIGroup> = {}): AIGroup {
  const path = `/Users/a/code/app/.claude/worktrees/${name}`;
  return {
    key: path, label: name, path, size: 10 * MB, count: 1, recent: 0, last: NOW - 8 * DAY, gone: false,
    repo: "/Users/a/code/app", branch: `feat/${name}`, state, ...(state === "dirty" ? { changes: 3 } : {}), ...over,
  };
}

/** 工具的 worktree 那一项：Claude Code 的没有自己的文件夹、边跑边清；Codex 的在 ~/.codex/worktrees 里。 */
function worktreePart(tool: "claude" | "codex", groups: AIGroup[], over: Partial<AIPart> = {}): AIPart {
  return fullPart(`${tool}.worktrees`, "sensitive", {
    path: tool === "codex" ? "/Users/a/.codex/worktrees" : "", worktrees: true, runSafe: tool === "claude",
    size: groups.reduce((n, g) => n + g.size, 0), files: groups.length, groups, ...over,
  });
}

test("groupSelectable: of the worktree states only clean and dirty can be ticked, whatever the window or recent", () => {
  const p = worktreePart("claude", []);
  for (const state of [...WT_STATES, "a-state-from-later"]) {
    // 8 天前、今天、今天写过（recent 和 count 一样多）：和对话记录的规则无关
    for (const g of [worktree(state, state), worktree(state, state, { last: NOW }), worktree(state, state, { last: NOW, recent: 1 })]) {
      for (const windowDays of [7, 30, 90] as const) {
        expect(groupSelectable(p, g, windowDays, NOW)).toBe(state === "clean" || state === "dirty");
      }
    }
  }
  const { state: _, ...stateless } = worktree("x", "clean");
  expect(groupSelectable(p, stateless, 30, NOW)).toBe(false);
});

test("isSessions: the worktree part is no conversation history; the history parts still are", () => {
  expect(isSessions(worktreePart("claude", [worktree("a", "clean")]))).toBe(false);
  expect(isSessions(worktreePart("codex", [worktree("a", "clean")]))).toBe(false);
  expect(isSessions(fullPart("claude.sessions", "sensitive"))).toBe(true);
  expect(isSessions(fullPart("codex.sessions", "sensitive"))).toBe(true);
});

test("canPick: a worktree part needs a clean or dirty worktree, and Codex's waits until Codex quits", () => {
  expect(canPick(worktreePart("claude", [worktree("a", "in-use"), worktree("b", "index-only")]), 30, NOW)).toBe(false);
  expect(canPick(worktreePart("claude", [worktree("a", "in-use"), worktree("b", "dirty")]), 30, NOW)).toBe(true);
  // Claude Code 的边跑边清，一个一个判；Codex 在运行（running 真、runSafe 假）时整项不能勾。
  expect(canPick(worktreePart("claude", [worktree("a", "clean")], { running: true }), 30, NOW)).toBe(true);
  expect(canPick(worktreePart("codex", [worktree("a", "clean")]), 30, NOW)).toBe(true);
  expect(canPick(worktreePart("codex", [worktree("a", "clean")], { running: true }), 30, NOW)).toBe(false);
});

test("worktreeSeen gives the state of each picked worktree in the order of the keys", () => {
  const p = worktreePart("claude", [worktree("a", "clean"), worktree("b", "dirty"), worktree("c", "in-use")]);
  const [a, b, c] = p.groups.map((g) => g.key);
  expect(worktreeSeen(p, [a!, b!])).toEqual(["clean", "dirty"]);
  expect(worktreeSeen(p, [b!, c!, a!])).toEqual(["dirty", "in-use", "clean"]);
  expect(worktreeSeen(p, [])).toEqual([]);
  // 列表里已经没有的：占着位置、和什么状态都对不上，后端照样一个一个回答
  expect(worktreeSeen(p, ["/Users/a/code/app/.claude/worktrees/gone", a!])).toEqual(["", "clean"]);
});

// ---- worktree 的行：PartRow 当根来画 ----

type PartRowProps = Parameters<typeof import("../src/react/ai/PartRow").PartRow>[0];

/** 画一项（默认展开）：记下点了哪些「在访达中显示」。 */
async function partRow(env: Changes, part: AIPart, over: Partial<PartRowProps> = {}) {
  const { PartRow } = await import("../src/react/ai/PartRow");
  const { Checkbox } = await import("../src/ui/components/checkbox");
  const { Badge } = await import("../src/ui/components/badge");
  const reveals: string[] = [];
  const page = env.page(PartRow, {
    part, windowDays: 30, scannedAt: NOW, picked: false, groups: new Set<string>(), expanded: true,
    canSelect: canPick(part, 30, NOW), chosenSize: 0,
    onTogglePart() {}, onToggleGroup() {}, onExpand() {}, onReveal: (path) => { reveals.push(path); }, onPickOld() {}, onWindowChange() {},
    ...over,
  });
  await page.settle();
  const checkboxes = () => page.ofType(Checkbox);
  return {
    page, reveals, checkboxes,
    /** 名字是 label 的勾选框。 */
    checkbox: (label: string) => checkboxes().find((el) => el.props["aria-label"] === label),
    /** 勾选框的 aria-describedby 指着的文字，按顺序用空格连起来。 */
    describedBy(box: { props: Record<string, unknown> }) {
      const ids = String(box.props["aria-describedby"] ?? "").split(" ").filter(Boolean);
      return ids.map((id) => textOf(page.find((el) => el.props.id === id))).join(" ");
    },
    badges: () => page.ofType(Badge).map((el) => textOf(el.props.children)),
    /** 名字是 name 的那个 worktree 的一行。 */
    row: (name: string) => page.find((el) => el.key === part.groups.find((g) => g.label === name)?.key),
    /** 这一项自己的「在访达中显示」。 */
    revealButton: () => page.find((el) => el.props["aria-label"] === t("fs.reveal")),
  };
}

test("a worktree part has no checkbox of its own, no window selector when expanded, and the Sensitive badge", async () => {
  const { Lock } = await import("lucide-react");
  const env = (await import("./page-harness")).changes();
  try {
    const r = await partRow(env, worktreePart("claude", [worktree("a", "clean"), worktree("b", "dirty")]), { expanded: false });
    // 位置空着：没有勾选框，也不是锁着的那种
    expect(r.checkboxes()).toEqual([]);
    expect(r.page.ofType(Lock)).toEqual([]);
    expect(r.badges()).toEqual([t("ai.level.sensitive")]);
    r.page.setProps({ expanded: true });
    await r.page.settle();
    // 展开：只有每个 worktree 自己的勾选框；没有保留窗口那一排，也没有对话记录的提示
    expect(r.checkboxes().map((el) => el.props["aria-label"])).toEqual(["a", "b"]);
    expect(r.page.find((el) => el.props.role === "radiogroup" || "onWindowChange" in el.props || "onPickOld" in el.props)).toBeUndefined();
    expect(r.page.text()).not.toContain(t("ai.sessionHint"));
    expect(r.page.text()).not.toContain(t("ai.pickOld"));
  } finally { env.restore(); }
});

test("each worktree state: clean and dirty can be ticked; the other eight are disabled and say why", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    env.lang("zh");
    const r = await partRow(env, worktreePart("claude", [...WT_STATES, "a-state-from-later"].map((s) => worktree(s, s))));
    for (const state of WT_STATES) {
      const box = r.checkbox(state)!;
      expect(box.props.disabled).toBe(state !== "clean" && state !== "dirty");
      if (state === "clean" || state === "dirty") continue;
      // 说明写在行上，也连着勾选框
      expect(r.describedBy(box)).toBe(t(`ai.wt.state.${state}` as never));
      expect(textOf(r.row(state))).toContain(t(`ai.wt.state.${state}` as never));
    }
    // 干净的不写；带改动的写着有几处
    expect(r.describedBy(r.checkbox("clean")!)).toBe("");
    expect(r.describedBy(r.checkbox("dirty")!)).toBe("3 处没提交的改动，会一起进废纸篓");
    expect(textOf(r.row("dirty"))).toContain("3 处没提交的改动，会一起进废纸篓");
    // 认不出的新状态：不能勾，说认不出
    const later = r.checkbox("a-state-from-later")!;
    expect(later.props.disabled).toBe(true);
    expect(r.describedBy(later)).toBe(t("ai.wt.state.unrecognized"));
  } finally { env.restore(); }
});

test("a worktree row's second line: the repository's folder, the branch or a detached HEAD, and the days untouched", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    env.lang("zh");
    const r = await partRow(env, worktreePart("claude", [
      worktree("x", "clean", { branch: "feat/x" }),
      worktree("y", "clean", { branch: "" }),
      worktree("z", "dirty", { last: 0 }),
      worktree("w", "unrecognized", { repo: "", branch: "" }),
    ]));
    const line = (name: string) => textOf(r.row(name));
    expect(line("x")).toContain("app · feat/x · 8 天没动");
    expect(line("y")).toContain("app · 游离的 HEAD · 8 天没动");
    // 读不到最后改动的时间：不写天数
    expect(line("z")).toContain("app · feat/z");
    expect(line("z")).not.toContain("天没动");
    // 认不出的文件夹不知道仓库和分支：没有第二行
    expect(line("w")).not.toContain(" · ");
    expect(line("w")).not.toContain(t("ai.wt.detached"));
    env.lang("en");
    r.page.setProps({});
    await r.page.settle();
    expect(line("x")).toContain("app · feat/x · untouched for 8 days");
    expect(line("y")).toContain("app · detached HEAD · untouched for 8 days");
  } finally { env.restore(); }
});

test("a worktree part without a folder of its own has no Reveal button, Codex's has one, and each row reveals its worktree", async () => {
  const { changes } = await import("./page-harness");
  let env = changes();
  try {
    const r = await partRow(env, worktreePart("claude", [worktree("a", "clean")]));
    expect(r.revealButton()).toBeUndefined();
    nodes(r.row("a")).find((el) => el.type === "button")!.props.onClick();
    expect(r.reveals).toEqual(["/Users/a/code/app/.claude/worktrees/a"]);
  } finally { env.restore(); }
  env = changes();
  try {
    const r = await partRow(env, worktreePart("codex", [worktree("a", "clean")]));
    r.revealButton()!.props.onClick();
    expect(r.reveals).toEqual(["/Users/a/.codex/worktrees"]);
  } finally { env.restore(); }
});

test("while Codex runs, its worktree part says Quit It First and every worktree is disabled", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    const r = await partRow(env, worktreePart("codex", WT_STATES.map((s) => worktree(s, s)), { running: true }));
    expect(r.badges()).toEqual([t("ai.quitFirst")]);
    expect(r.checkboxes()).toHaveLength(WT_STATES.length);
    for (const box of r.checkboxes()) expect(box.props.disabled).toBe(true);
    // 读屏也读得到为什么：「先退出」连着每一组，在它自己的说明前面
    expect(r.describedBy(r.checkbox("clean")!)).toBe(t("ai.quitFirst"));
    expect(r.describedBy(r.checkbox("in-use")!)).toBe(`${t("ai.quitFirst")} ${t("ai.wt.state.in-use")}`);
  } finally { env.restore(); }
});

// ---- worktree 的选择、确认框和失败提示：页面 ----

/** Claude Code：缓存（扫描时勾上），和三个 worktree：干净的 a、带改动的 b、在用的 c。 */
function claudeWithWorktrees(): AITool {
  return claudeTool({ parts: [
    fullPart("claude.cache", "safe"),
    worktreePart("claude", [worktree("a", "clean"), worktree("b", "dirty", { size: 20 * MB }), worktree("c", "in-use", { size: 5 * MB })]),
  ] });
}
const wtKey = (name: string) => `/Users/a/code/app/.claude/worktrees/${name}`;

/** Claude Code 页扫描出 tool；清理回答 answer，之后的重扫依次是 rescans。记下发给后端的选择。 */
async function worktreePage(env: Changes, tool: AITool, o: { answer?: AICleanResult; rescans?: AITool[] } = {}) {
  const { aiApi } = await import("../src/react/ai/api");
  const cleaned: AISelection[][] = [];
  env.spy(aiApi, "clean").mockImplementation(async (sel) => { cleaned.push(sel); return o.answer ?? { freed: 0, parts: [] }; });
  const c = await toolPage(env, { scans: [tool, ...(o.rescans ?? [])].map((x) => scanned(x)) });
  await c.scan();
  const part = (id: string) => c.page.find((el) => el.props.part?.id === id && "onTogglePart" in el.props)!;
  const cleanButton = () => c.page.find((el) => el.props.size === "lg")!;
  const dialog = () => c.page.find((el) => typeof el.props.onConfirm === "function")!;
  return {
    page: c.page, cleaned, part,
    async tick(id: string, key: string) { part(id).props.onToggleGroup(key, true); await c.page.settle(); },
    cleanLabel: () => textOf(cleanButton().props.children),
    /** 确认框的文字：ConfirmDialog 是子组件，照它收到的 props 调一遍。 */
    dialogText() { const d = dialog(); return textOf(d.type(d.props)); },
    async clean() {
      cleanButton().props.onClick();
      await c.page.settle();
      dialog().props.onConfirm();
      await c.page.settle();
    },
  };
}

test("a scan ticks no worktree and counts none as cleanable; a ticked one counts on the Clean button", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    const w = await worktreePage(env, claudeWithWorktrees());
    const wt = () => w.part("claude.worktrees");
    expect(wt().props.picked).toBe(false);
    expect([...wt().props.groups]).toEqual([]);
    // 可清理的只算缓存；扫描时勾上的也只有它
    expect(w.page.text()).toContain(t("ai.sub.scanned", { size: human(100) }));
    expect(w.cleanLabel()).toBe(t("ai.clean", { size: human(100) }));
    // 整项勾不上，只能一个一个勾
    wt().props.onTogglePart(true);
    await w.page.settle();
    expect([...wt().props.groups]).toEqual([]);
    await w.tick("claude.worktrees", wtKey("a"));
    expect(wt().props.picked).toBe(true);
    expect(w.cleanLabel()).toBe(t("ai.clean", { size: human(100 + 10 * MB) }));
    expect(w.page.text()).toContain(t("ai.sub.scanned", { size: human(100) }));
  } finally { env.restore(); }
});

test("the selection carries the state each ticked worktree showed, in order; the other parts carry none", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    const w = await worktreePage(env, claudeWithWorktrees());
    await w.tick("claude.worktrees", wtKey("a"));
    await w.tick("claude.worktrees", wtKey("b"));
    // 不能勾的那个，有人叫它勾也不进选择
    await w.tick("claude.worktrees", wtKey("c"));
    await w.clean();
    expect(w.cleaned).toEqual([[
      { tool: "claude", part: "claude.cache", groups: [] },
      { tool: "claude", part: "claude.worktrees", groups: [wtKey("a"), wtKey("b")], seen: ["clean", "dirty"] },
    ]]);
    expect(w.cleaned[0]![0]).not.toHaveProperty("seen");
  } finally { env.restore(); }
});

test("the confirmation counts the worktrees, says what becomes of them, and how many hold uncommitted changes", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    env.lang("zh");
    const w = await worktreePage(env, claudeWithWorktrees());
    await w.tick("claude.worktrees", wtKey("a"));
    await w.tick("claude.worktrees", wtKey("b"));
    expect(w.dialogText()).toContain("2 个 worktree");
    expect(w.dialogText()).toContain(t("ai.part.claude.worktrees.lose"));
    expect(w.dialogText()).toContain("其中 1 个带着没提交的改动。");
    // 只勾着干净的：不多那一句
    w.part("claude.worktrees").props.onToggleGroup(wtKey("b"), false);
    await w.page.settle();
    expect(w.dialogText()).toContain("1 个 worktree");
    expect(w.dialogText()).not.toContain("没提交的改动");
  } finally { env.restore(); }
});

test("a worktree that could not be removed is told in an error toast: its path, then why", async () => {
  const failure = `~/code/app/.claude/worktrees/x: ${t("ai.wt.fail.wt-git")}`;
  const answer: AICleanResult = {
    freed: 0,
    parts: [{ tool: "claude", part: "claude.worktrees", moved: 0, freed: 0, kept: 0, skipped: "", failed: ["~/code/app/.claude/worktrees/x: wt-git"] }],
  };
  const { changes } = await import("./page-harness");
  let env = changes();
  try {
    const tool = claudeWithWorktrees();
    const w = await worktreePage(env, tool, { answer, rescans: [tool] });
    await w.tick("claude.worktrees", wtKey("a"));
    await w.clean();
    expect(w.page.toasts.at(-1)).toEqual({ text: failure, kind: "err" });
  } finally { env.restore(); }
  // 重扫也失败：清理的结果和这一条一起说
  env = changes();
  try {
    const w = await worktreePage(env, claudeWithWorktrees(), { answer });
    await w.tick("claude.worktrees", wtKey("a"));
    await w.clean();
    expect(w.page.toasts.at(-1)).toEqual({ text: [t("ai.done", { size: human(0) }), t("ai.done.skipped", { n: 1 }), failure].join(" · "), kind: "err" });
  } finally { env.restore(); }
});

test("errText says each worktree failure code in words", async () => {
  const { errText } = await import("../src/shared");
  for (const code of ["wt-changed", "wt-git", "wt-trash"] as const) expect(errText(code)).toBe(t(`ai.wt.fail.${code}`));
});
