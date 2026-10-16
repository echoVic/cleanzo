import { beforeAll, beforeEach, expect, mock, test } from "bun:test";

// 无浏览器的事件夹具：检查侧栏显示规则、每个工具页的默认勾选、保留窗口、受保护项和发给后端的选择。
class Element {
  innerHTML = "";
  textContent = "";
  disabled = false;
  checked = false;
  dataset: Record<string, string> = {};
  style: Record<string, string> = {};
  className = "";
  classes = new Set<string>();
  classList = {
    add: (...names: string[]) => names.forEach((n) => this.classes.add(n)),
    remove: (...names: string[]) => names.forEach((n) => this.classes.delete(n)),
    contains: (n: string) => this.classes.has(n),
    toggle: (n: string, on = !this.classes.has(n)) => on ? this.classes.add(n) : this.classes.delete(n),
  };
  listeners = new Map<string, Function[]>();
  constructor(public id = "") {}
  addEventListener(type: string, fn: Function) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn]);
  }
  querySelectorAll() { return []; }
  querySelector() { return null; }
  closest() { return this; }
  focus() {}
  contains(el: Element) { return el === this; }
  async fire(type: string, target: Element = this) {
    for (const fn of this.listeners.get(type) ?? []) await fn({ target, preventDefault() {}, stopPropagation() {} });
    await Bun.sleep(0);
  }
}

// 选择器就是夹具里的节点键：#ai-list、#view-aitools .hero、.nav-item[data-tool="claude"] 各是一个节点。
const nodes = new Map<string, Element>();
const node = (s: string) => {
  if (!nodes.has(s)) nodes.set(s, new Element(s.replace(/^#/, "")));
  return nodes.get(s)!;
};
// 复合选择器只在测试断言里出现，夹具里给它们各自一个稳定节点。
const fixtureSelectors = [".ai-head-row", "#view-aitools .head-actions"];
for (const sel of fixtureSelectors) node(sel);
const navItem = (id: string) => node(`.nav-item[data-tool="${id}"]`);
const storage = new Map([["cleanzo.lang", "zh"]]);
Object.assign(globalThis, {
  HTMLElement: Element, HTMLInputElement: Element, HTMLButtonElement: Element,
  document: {
    querySelector: node, querySelectorAll: () => [], addEventListener() {},
    createElement: () => new Element(), body: { appendChild() {} },
  },
  localStorage: { getItem: (key: string) => storage.get(key), setItem: (key: string, v: string) => storage.set(key, v) },
  window: { innerWidth: 1200, setTimeout: () => 0 },
});

const NOW = 1_792_000_000;
const DAY = 86400;
let cleaned: unknown[] | null = null;
let scanned: string[] = [];
let installed: Record<string, boolean> = { claude: true, codex: true };

mock.module("mygo-runtime", () => ({ Channel: class { close() {} } }));
// mock.module 跨文件共享：保留其他测试文件用到的方法。
mock.module("../src/mygo", () => ({ Cleaner: {
  listAITools: async () => [
    { id: "claude", name: "Claude Code", installed: installed.claude },
    { id: "codex", name: "Codex", installed: installed.codex },
  ],
  scanAITool: async (id: string) => { scanned.push(id); return result(id); },
  cleanAITools: async (sel: unknown[]) => { cleaned = sel; return { parts: [], freed: 4096 }; },
  scanJunk: () => new Promise<void>(() => {}),
  cleanJunk: async () => {},
  getSettings: async () => ({ disabledGroups: [], ignored: [], custom: [] }),
  saveSettings: async () => {},
  revealInFinder: async () => {},
  openFullDiskAccessSettings: async () => {},
  info: async () => ({ chip: "", memory: 0, cores: 0, osVersion: "", bootTime: 0 }),
  watchStatus: (_ch: unknown) => new Promise<void>(() => {}),
  quitApp: async (_path: string) => true,
} }));

const part = (id: string, level: string, size: number, extra = {}) => ({
  id, name: "", path: `/Users/me/${id}`, level, size, files: 1, last: NOW - 50 * DAY, groups: [], top: [], more: 0, keep: "", runSafe: false, ...extra,
});
const group = (key: string, ageDays: number, extra = {}) => ({
  key, label: `/Users/me/work/${key}`, size: 1000, count: 2, last: NOW - ageDays * DAY, recent: 0, gone: false,
  path: `/Users/me/.claude/projects/${key}`, ...extra,
});

let claudeRunning = false;
function result(id: string) {
  const tools: Record<string, unknown> = {
    claude: { id: "claude", name: "Claude Code", path: "/Users/me/.claude", installed: true, running: claudeRunning, size: 10_000, parts: [
      part("claude.debug", "safe", 300),
      part("claude.fileHistory", "optional", 200),
      part("claude.sessions", "sensitive", 3000, { runSafe: true, groups: [
        group("-old", 60), group("-mid", 20), group("-today", 0, { recent: 2 }),
      ] }),
      part("claude.skills", "protected", 500),
      part("unknown:teams", "unknown", 50, { name: "teams" }),
    ] },
    codex: { id: "codex", name: "Codex", path: "/Users/me/.codex", installed: true, running: false, size: 20_000, parts: [
      part("codex.oldReleases", "safe", 2000, { runSafe: true, keep: "0.2.0-aarch64-apple-darwin", groups: [
        { key: "0.1.0-aarch64-apple-darwin", label: "0.1.0", size: 2000, count: 1, last: NOW - 30 * DAY, recent: 0, gone: false,
          path: "/Users/me/.codex/packages/app-server-daemon/releases/0.1.0-aarch64-apple-darwin" },
      ] }),
      part("codex.logs", "safe", 700),
    ] },
  };
  return { scannedAt: NOW, tools: [tools[id]] };
}

let ai: typeof import("../src/aitools");
beforeAll(async () => { ai = await import("../src/aitools"); ai.initAITools(); await Bun.sleep(1); });
beforeEach(() => { cleaned = null; claudeRunning = false; ai.__test.setWindow(30); });

test("the sidebar shows one entry per installed tool and hides the section when none is installed", async () => {
  installed = { claude: true, codex: true };
  await ai.__test.loadTools();
  expect(navItem("claude").classList.contains("hidden")).toBe(false);
  expect(navItem("codex").classList.contains("hidden")).toBe(false);
  expect(node("#nav-ai-section").classList.contains("hidden")).toBe(false);

  installed = { claude: false, codex: true };
  await ai.__test.loadTools();
  expect(navItem("claude").classList.contains("hidden")).toBe(true);
  expect(navItem("codex").classList.contains("hidden")).toBe(false);

  installed = { claude: false, codex: false };
  await ai.__test.loadTools();
  expect(node("#nav-ai-section").classList.contains("hidden")).toBe(true);
  installed = { claude: true, codex: true };
});

test("opening a tool page never scans; scanning is explicit and its result is kept when switching back", async () => {
  scanned = [];
  ai.showAITool("codex");
  await Bun.sleep(1);
  expect(scanned).toEqual([]);
  expect(node("#ai-title").textContent).toBe("Codex");
  // 闲置态与垃圾清理页一致：占用条和右上按钮隐藏，中轴卡片是唯一入口。
  expect(node("#ai-total").textContent).toBe("--");
  const idleHtml = node("#ai-list").innerHTML;
  expect(idleHtml).toContain("ha-ring");
  expect(idleHtml).toContain("尚未扫描");
  expect(idleHtml).toContain("一键扫描，找出 Codex");
  expect(idleHtml).toContain("旧版本");
  expect(idleHtml).toContain("扫描仅在本机进行");
  expect(node(".ai-head-row").classList.contains("hidden")).toBe(true);

  const start = new Element();
  Object.assign(start.dataset, { action: "scan" });
  await node("#ai-list").fire("click", start);
  expect(scanned).toEqual(["codex"]);
  expect(node("#ai-title").textContent).toBe("Codex");
  expect(node("#ai-total").textContent).toBe("19.5 KB");

  // 切到别的工具再切回来，不重新扫描，上次的内容还在。
  ai.showAITool("claude");
  await Bun.sleep(1);
  expect(scanned).toEqual(["codex"]); // 进 Claude 也没扫
  ai.showAITool("codex");
  await Bun.sleep(1);
  expect(scanned).toEqual(["codex"]);
  expect(node("#ai-list").innerHTML).toContain("旧版本");
  expect(node("#ai-list").innerHTML).not.toContain("对话记录");
});

test("every part can expand: groups or the largest entries, and every row can reveal a folder", async () => {
  const logs = part("codex.logs", "safe", 700, {
    top: [
      { name: "logs_2.sqlite", path: "/Users/me/.codex/logs_2.sqlite", size: 500, isDir: false, last: 0 },
      { name: "logs_2.sqlite-wal", path: "/Users/me/.codex/logs_2.sqlite-wal", size: 200, isDir: false, last: 0 },
    ],
    more: 2,
  });
  const teams = part("unknown:teams", "unknown", 50, {
    name: "teams",
    top: [{ name: "a.json", path: "/Users/me/.claude/teams/a.json", size: 50, isDir: false, last: 0 }],
  });
  const loaded = (ai.__test as unknown as { load: (id: string, r: unknown) => void }).load;

  const codex = result("codex");
  (codex.tools[0].parts as unknown[])[1] = logs;
  loaded("codex", codex as never);
  const expandLogs = new Element();
  Object.assign(expandLogs.dataset, { action: "expand", part: "codex.logs" });
  await node("#ai-list").fire("click", expandLogs);
  let html = node("#ai-list").innerHTML;
  expect(html).toContain("logs_2.sqlite");
  expect(html).toContain("reveal-entry");
  expect(html).toContain("还有 2 项");
  // 每项都有整项的 Finder 入口。
  expect(html).toContain('data-action="reveal-part" data-part="codex.logs"');

  // 受保护和未识别的项同样可以展开和打开文件夹。
  const claudeRes = result("claude");
  (claudeRes.tools[0].parts as unknown[]).push(teams);
  loaded("claude", claudeRes as never);
  // 未识别项收在「不建议删除」折叠区里，先展开。
  const toggleLocked = new Element();
  Object.assign(toggleLocked.dataset, { action: "locked" });
  await node("#ai-list").fire("click", toggleLocked);
  const expandTeams = new Element();
  Object.assign(expandTeams.dataset, { action: "expand", part: "unknown:teams" });
  await node("#ai-list").fire("click", expandTeams);
  html = node("#ai-list").innerHTML;
  expect(html).toContain("a.json");
  expect(html).toContain('data-action="reveal-part" data-part="unknown:teams"');
});

test("a group row opens its own folder", async () => {
  ai.__test.load("codex", result("codex") as never);
  const target = new Element();
  Object.assign(target.dataset, { action: "expand", part: "codex.oldReleases" });
  await node("#ai-list").fire("click", target);
  const html = node("#ai-list").innerHTML;
  expect(html).toContain("reveal-group");
  expect(html).toContain("data-group=\"0.1.0-aarch64-apple-darwin\"");
});

test("safe parts are preselected per page; sessions, optional, protected and unknown are not", () => {
  ai.__test.load("claude", result("claude") as never);
  expect(ai.__test.picked()).toEqual(["claude.debug"]);
  expect(ai.__test.totalChosen()).toBe(300);
  expect(node("#ai-list").innerHTML).toContain("2 项不建议删除");
  ai.__test.load("codex", result("codex") as never);
  expect(ai.__test.picked().sort()).toEqual(["codex.logs", "codex.oldReleases"]);
  expect(ai.__test.groups("codex.oldReleases")).toEqual(["0.1.0-aarch64-apple-darwin"]);
  expect(ai.__test.totalChosen()).toBe(2700);
});

test("ticking conversations picks only groups older than the window, never today's", () => {
  ai.__test.load("claude", result("claude") as never);
  ai.__test.togglePart("claude.sessions", true);
  expect(ai.__test.groups("claude.sessions")).toEqual(["-old"]);
  ai.__test.setWindow(7);
  ai.__test.togglePart("claude.sessions", true);
  expect(ai.__test.groups("claude.sessions").sort()).toEqual(["-mid", "-old"]);
  // 窗口改回 90 天：已勾选但不再符合条件的分组被撤销。
  ai.__test.setWindow(90);
  expect(ai.__test.groups("claude.sessions")).toEqual([]);
  expect(ai.__test.picked()).not.toContain("claude.sessions");
});

test("a group that is all today's sessions or inside the window can't be ticked by hand", () => {
  ai.__test.load("claude", result("claude") as never);
  ai.__test.toggleGroup("claude.sessions", "-today", true);
  ai.__test.toggleGroup("claude.sessions", "-mid", true);
  expect(ai.__test.totalChosen()).toBe(300);
  expect(ai.__test.selections().find((s) => s.part === "claude.sessions")).toBeUndefined();
});

test("protected and unknown parts can never be selected", () => {
  ai.__test.load("claude", result("claude") as never);
  ai.__test.togglePart("claude.skills", true);
  ai.__test.togglePart("unknown:teams", true);
  expect(ai.__test.picked()).toEqual(["claude.debug"]);
});

test("a running tool keeps only its run-safe parts selectable", () => {
  claudeRunning = true;
  ai.__test.load("claude", result("claude") as never);
  expect(ai.__test.picked()).toEqual([]);
  ai.__test.togglePart("claude.fileHistory", true);
  expect(ai.__test.picked()).toEqual([]);
  ai.__test.togglePart("claude.sessions", true);
  expect(ai.__test.groups("claude.sessions")).toEqual(["-old"]);
  expect(node("#ai-list").innerHTML).toContain("正在运行");
});

test("confirming sends exactly this tool's chosen parts and groups", async () => {
  ai.__test.load("claude", result("claude") as never);
  ai.__test.togglePart("claude.sessions", true);
  ai.__test.togglePart("claude.fileHistory", true);
  await node("#ai-clean").fire("click");
  expect(node("#modal-title").textContent).toBe("移到废纸篓？");
  expect(node("#modal-body").innerHTML).toContain("Claude Code 的以下内容");
  expect(node("#modal-body").innerHTML).toContain("这些对话将无法再接着聊");
  await node("#modal-ok").fire("click");
  await Bun.sleep(5);
  expect(cleaned).toEqual([
    { tool: "claude", part: "claude.debug", groups: [] },
    { tool: "claude", part: "claude.fileHistory", groups: [] },
    { tool: "claude", part: "claude.sessions", groups: ["-old"] },
  ]);
  expect(node("#ai-sub").textContent).toContain("已将 4.0 KB 移到废纸篓");
});
