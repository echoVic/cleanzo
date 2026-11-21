import "./setup-dom";
import { expect, test } from "bun:test";
import type { ClaudeEndResult, ClaudeLeft, ClaudeProc, ClaudeRunSummary, ClaudeRunning, ClaudeSession } from "../src/mygo";
import { dict, t } from "../src/i18n";
import { human } from "../src/react/ai/format";
import { Collapse } from "../src/ui/motion/Collapse";
import {
  RUNNING, badgeCount, durationText, endToast, freezeRunning, leftTargets, procTargets, sessionName,
} from "../src/react/ai/running";
import { changes, nodes, textOf, type Changes } from "./page-harness";

const MB = 2 ** 20;
const GB = 2 ** 30;
const HOUR = 3600;
const DAY = 86400;
const NOW = 1_700_000_000;

/** 一行进程：默认运行了 1 小时，没有端口。 */
function proc(pid: number, over: Partial<ClaudeProc> = {}): ClaudeProc {
  return { pid, start: (NOW - HOUR) * 1e6, command: `node server-${pid}.js`, cwd: "/Users/a/code/shop", worktree: "", ports: [], memory: 10 * MB, folded: [], ...over };
}

/** 一个开着的会话：默认空闲 3 小时，在终端里。 */
function session(pid: number, over: Partial<ClaudeSession> = {}): ClaudeSession {
  return {
    id: `S${pid}`, pid, start: (NOW - DAY) * 1e6, project: "shop", cwd: "/Users/a/code/shop", title: "修复登录",
    status: "idle", since: NOW - 3 * HOUR, host: "", entry: "cli", memory: 100 * MB, own: false, commands: [], ...over,
  };
}

function left(id: string, procs: ClaudeProc[], over: Partial<ClaudeLeft> = {}): ClaudeLeft {
  return { id, project: "blog", title: "换主题", procs, ...over };
}

function reading(over: Partial<ClaudeRunning> = {}): ClaudeRunning {
  return { sessions: [], left: [], uncertain: 0, noRecords: false, at: NOW, ...over };
}

// ---- 桥：页面只经 runningApi 碰 Cleaner ----

test("runningApi passes each call and its arguments to the Cleaner method behind it", async () => {
  const { Cleaner } = await import("../src/mygo");
  const { runningApi } = await import("../src/react/ai/api");
  const env = changes();
  try {
    const snapshot = reading({ uncertain: 1 });
    const summary: ClaudeRunSummary = { idle: 2, left: 3, rows: [] };
    const result: ClaudeEndResult = { procs: [{ pid: 10, result: "ended" }], freed: 5 };
    const calls: unknown[][] = [];
    env.spy(Cleaner, "claudeRunning").mockImplementation(async () => snapshot);
    env.spy(Cleaner, "claudeRunSummary").mockImplementation(async () => summary);
    env.spy(Cleaner, "endClaudeProcesses").mockImplementation(async (...args) => { calls.push(["procs", ...args]); return result; });
    env.spy(Cleaner, "endClaudeSession").mockImplementation(async (...args) => { calls.push(["session", ...args]); return result; });
    expect(await runningApi.read()).toBe(snapshot);
    expect(await runningApi.summary()).toBe(summary);
    const targets = [{ pid: 10, start: 100 }, { pid: 11, start: 101 }];
    expect(await runningApi.endProcs("S1", targets)).toBe(result);
    expect(await runningApi.endSession({ pid: 12, start: 102 }, "S2", "idle")).toBe(result);
    expect(calls).toEqual([["procs", "S1", targets], ["session", { pid: 12, start: 102 }, "S2", "idle"]]);
  } finally { env.restore(); }
});

// ---- 纯逻辑 ----

test("badgeCount: the left rows plus the sessions idle for two hours or longer", () => {
  const r = reading({
    sessions: [
      session(1, { status: "idle", since: NOW - 7200 }),
      session(2, { status: "idle", since: NOW - 7199 }),
      session(3, { status: "", since: NOW - 5 * DAY }),
      session(4, { status: "busy", since: NOW - 5 * DAY }),
      session(5, { status: "idle", since: NOW - 7 * DAY }),
      // 空闲了多久读不到：不算
      session(6, { status: "idle", since: 0 }),
    ],
    left: [left("L1", [proc(20), proc(21)]), left("L2", [proc(22)])],
  });
  expect(badgeCount(r, NOW)).toBe(3 + 2);
  expect(badgeCount(reading(), NOW)).toBe(0);
  expect(RUNNING).toEqual({ idleNoteSec: 7200, refreshMs: 5000 });
});

test("durationText: minutes under an hour (at least one), hours under a day, then days, rounded down", () => {
  expect(durationText(0, t)).toBe("1 分钟");
  expect(durationText(59, t)).toBe("1 分钟");
  expect(durationText(150, t)).toBe("2 分钟");
  expect(durationText(HOUR - 1, t)).toBe("59 分钟");
  expect(durationText(HOUR, t)).toBe("1 小时");
  expect(durationText(7200, t)).toBe("2 小时");
  expect(durationText(DAY - 1, t)).toBe("23 小时");
  expect(durationText(DAY, t)).toBe("1 天");
  expect(durationText(7.6 * DAY, t)).toBe("7 天");
  const env = changes();
  try {
    env.lang("en");
    expect([59, HOUR, 7200, DAY, 7.6 * DAY].map((sec) => durationText(sec, t))).toEqual(["1 min", "1 hour", "2 hours", "1 day", "7 days"]);
  } finally { env.restore(); }
});

test("procTargets puts the row's own process first, then what is folded into it; leftTargets joins a group's rows", () => {
  const a = proc(30, { start: 300, folded: [{ pid: 31, start: 310 }, { pid: 32, start: 320 }] });
  const b = proc(40, { start: 400 });
  expect(procTargets(a)).toEqual([{ pid: 30, start: 300 }, { pid: 31, start: 310 }, { pid: 32, start: 320 }]);
  expect(procTargets(b)).toEqual([{ pid: 40, start: 400 }]);
  expect(leftTargets(left("L1", [a, b]))).toEqual([
    { pid: 30, start: 300 }, { pid: 31, start: 310 }, { pid: 32, start: 320 }, { pid: 40, start: 400 },
  ]);
});

test("sessionName: project · title, the project alone, or an unknown session", () => {
  expect(sessionName({ project: "shop", title: "修复登录" }, t)).toBe("shop · 修复登录");
  expect(sessionName({ project: "shop", title: "" }, t)).toBe("shop");
  expect(sessionName({ project: "", title: "修复登录" }, t)).toBe("修复登录");
  expect(sessionName({ project: "", title: "" }, t)).toBe("未知会话");
});

test("freezeRunning keeps the shown rows in their order with the new numbers by pid, keeps rows that left and adds none", () => {
  const shown = reading({
    sessions: [session(1, { memory: 100, commands: [proc(11, { memory: 10 })] }), session(2, { memory: 200 })],
    left: [left("L1", [proc(21, { memory: 50 }), proc(22, { memory: 60 })])],
    uncertain: 1,
  });
  const next = reading({
    at: NOW + 5,
    sessions: [
      session(2, { memory: 250 }), session(3),
      session(1, { memory: 150, commands: [proc(12), proc(11, { memory: 15, ports: [5173] })] }),
    ],
    left: [left("L2", [proc(23)]), left("L1", [proc(22, { memory: 65 })])],
    uncertain: 2,
  });
  const out = freezeRunning(shown, next);
  expect(out.sessions.map((s) => [s.pid, s.memory])).toEqual([[1, 150], [2, 250]]);
  expect(out.sessions[0]!.commands.map((c) => [c.pid, c.memory, c.ports])).toEqual([[11, 15, [5173]]]);
  expect(out.left.map((g) => g.id)).toEqual(["L1"]);
  expect(out.left[0]!.procs.map((p) => [p.pid, p.memory])).toEqual([[21, 50], [22, 65]]);
  expect([out.uncertain, out.at]).toEqual([2, NOW + 5]);
  const gone = freezeRunning(shown, reading({ at: NOW + 5, noRecords: true }));
  expect(gone.sessions.map((s) => [s.pid, s.memory])).toEqual([[1, 100], [2, 200]]);
  expect(gone.left[0]!.procs.map((p) => p.pid)).toEqual([21, 22]);
  // 冻结期间读不到会话记录：留下的那一段照旧显示
  expect(gone.noRecords).toBe(false);
});

test("endToast: all ended is an ok toast; what is still running or changed follows, and makes it an error", () => {
  const res = (results: string[], freed = GB): ClaudeEndResult => ({ procs: results.map((result, i) => ({ pid: 100 + i, result })), freed });
  expect(endToast(res(["ended", "forced"]), t, human)).toEqual({ text: "已结束 2 个进程，腾出 1.0 GB", kind: "ok" });
  expect(endToast(res(["ended", "running", "changed", "changed"]), t, human)).toEqual({
    text: "已结束 1 个进程，腾出 1.0 GB · 1 个还在运行 · 2 个已经变了，没有动", kind: "err",
  });
  expect(endToast(res(["running", "changed"], 0), t, human)).toEqual({ text: "1 个还在运行 · 1 个已经变了，没有动", kind: "err" });
  expect(endToast(res(["changed"], 0), t, human)).toEqual({ text: "1 个已经变了，没有动", kind: "err" });
});

test("the Running tab's strings read in Chinese and English with the same placeholders", () => {
  const keys = (Object.keys(dict.zh) as (keyof typeof dict.zh)[]).filter((k) => k === "ai.tabs" || k.startsWith("ai.tab.") || k.startsWith("ai.run."));
  expect(keys.length).toBe(41);
  const holes = (s: string) => [...s.matchAll(/\{(\w+)[|}]/g)].map((m) => m[1]).sort();
  const en: Readonly<Record<string, string>> = dict.en;
  for (const k of keys) {
    expect(en[k], k).toBeDefined();
    expect([...new Set(holes(en[k]!))], k).toEqual([...new Set(holes(dict.zh[k]))]);
  }
});

// ---- 面板（无浏览器，见 page-harness.ts）：子组件不执行，行直接写在 RunningPanel 里 ----

const ok = (r: ClaudeRunning) => () => Promise.resolve(structuredClone(r));
const fails = () => Promise.reject(new Error("unreadable"));

function deferred<V>() {
  let resolve!: (v: V) => void;
  const promise = new Promise<V>((yes) => { resolve = yes; });
  return { promise, resolve };
}

interface PanelProps { active: boolean; visit: number; refresh: number; onCount: (n: number) => void }

/** 结束的替身：每个目标都结束了，腾出 10 MB。 */
const endedAll = (targets: { pid: number }[]): ClaudeEndResult => ({ procs: targets.map(({ pid }) => ({ pid, result: "ended" })), freed: 10 * MB });

/**
 * 搭好面板：每次读取依次用 reads 里的下一个（用完了一直用最后一个）；
 * 定时器记下来由 tick() 触发，visibilitychange 由 visibility() 触发。改动都记在 env 里，由测试的 finally 还原。
 */
async function panel(env: Changes, reads: (() => Promise<ClaudeRunning>)[], props: Partial<PanelProps> = {}) {
  const { RunningPanel } = await import("../src/react/ai/RunningPanel");
  const { runningApi } = await import("../src/react/ai/api");
  let readCount = 0;
  const ended: unknown[][] = [];
  const counts: number[] = [];
  const endProcs = env.spy(runningApi, "endProcs");
  const endSession = env.spy(runningApi, "endSession");
  env.spy(runningApi, "read").mockImplementation(() => reads[Math.min(readCount++, reads.length - 1)]!());
  endProcs.mockImplementation(async (session, targets) => { ended.push(["procs", session, targets]); return endedAll(targets); });
  endSession.mockImplementation(async (target, session, status) => { ended.push(["session", target, session, status]); return endedAll([target]); });
  const page = env.page(RunningPanel, { active: true, visit: 1, refresh: 0, onCount: (n: number) => { counts.push(n); }, ...props });
  const timers = new Map<number, { fn: () => void; ms: number }>();
  let timerId = 0;
  env.assign(globalThis.window, {
    setTimeout: (fn: () => void, ms: number) => { timers.set(++timerId, { fn, ms }); return timerId; },
    clearTimeout: (id: number) => { timers.delete(id); },
  });
  let onVisibility: (() => void) | undefined;
  env.assign(page.document, {
    addEventListener: (type: string, fn: () => void) => { if (type === "visibilitychange") onVisibility = fn; },
    removeEventListener: (type: string, fn: () => void) => { if (onVisibility === fn) onVisibility = undefined; },
  });
  await page.settle();
  type El = ReturnType<typeof nodes>[number];
  const button = (el: El | undefined, label: string) =>
    el && nodes(el).find((n) => typeof n.props.onClick === "function" && textOf(n.props.children).trim() === label);
  const row = (pid: number) => page.find((el) => el.props["data-pid"] === pid);
  const dialog = () => page.find((el) => "onOpenChange" in el.props && el.props.open === true);
  const confirmButton = () => button(dialog(), t("ai.run.confirm.ok"));
  return {
    page, ended, counts, endProcs, endSession,
    get reads() { return readCount; },
    /** 排着的「每 5 秒」那一次有几个。 */
    queued() { return [...timers.values()].filter((x) => x.ms === RUNNING.refreshMs).length; },
    /** 到点：执行排着的那一次读取。 */
    async tick() {
      for (const [id, x] of [...timers]) if (x.ms === RUNNING.refreshMs) { timers.delete(id); x.fn(); }
      await page.settle();
    },
    /** 窗口隐藏或回来。 */
    async visibility(hidden: boolean) { page.document.hidden = hidden; onVisibility?.(); await page.settle(); },
    /** 开着的会话，按页面上的顺序。 */
    sessionPids() {
      return page.all().filter((el) => typeof el.props["data-pid"] === "number" && !String(el.props.className).includes("cz-run-proc")).map((el) => el.props["data-pid"] as number);
    },
    row,
    hasEnd(pid: number) { return button(row(pid), t("ai.run.end")) !== undefined; },
    endButton(pid: number) { return nodes(row(pid)!).find((n) => n.type === "button" && typeof n.props.onClick === "function" && !("aria-expanded" in n.props)); },
    async end(pid: number) { button(row(pid), t("ai.run.end"))!.props.onClick(); await page.settle(); },
    async endAll(id: string) { button(page.find((el) => el.props["data-group"] === id), t("ai.run.endAll"))!.props.onClick(); await page.settle(); },
    dialog,
    confirmButton,
    async confirm() { confirmButton()!.props.onClick(); await page.settle(); },
    async cancel() { dialog()!.props.onOpenChange(false); await page.settle(); },
  };
}

test("panel: both sections show their title, count and total; sessions keep the reading's order and are keyed by pid", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({
      sessions: [
        session(3, { memory: 300 * MB, commands: [proc(31, { memory: 20 * MB })] }),
        session(1, { memory: 100 * MB }),
        session(2, { status: "busy", memory: 200 * MB }),
      ],
      left: [left("L1", [proc(41, { memory: 40 * MB }), proc(42, { memory: 2 * MB })]), left("L2", [proc(43, { memory: 8 * MB })])],
    }))]);
    const text = p.page.text();
    expect(text).toContain(t("ai.run.open.title") + t("ai.run.open.sum", { n: 3, size: human(620 * MB) }));
    expect(text).toContain(t("ai.run.left.title") + t("ai.run.left.sum", { n: 3, size: human(50 * MB) }));
    expect(p.sessionPids()).toEqual([3, 1, 2]);
    expect(p.page.all().filter((el) => el.props.className === "cz-run-item").map((el) => el.key)).toEqual(["3", "1", "2", "L1", "L2"]);
  } finally { env.restore(); }
});

test("panel: two processes with one session id are two rows, each ending its own pid", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({ sessions: [session(10, { id: "S-same", start: 1000 }), session(11, { id: "S-same", start: 1100 })] }))]);
    expect(p.sessionPids()).toEqual([10, 11]);
    expect([p.hasEnd(10), p.hasEnd(11)]).toEqual([true, true]);
    await p.end(11);
    await p.confirm();
    await p.end(10);
    await p.confirm();
    expect(p.ended).toEqual([["session", { pid: 11, start: 1100 }, "S-same", "idle"], ["session", { pid: 10, start: 1000 }, "S-same", "idle"]]);
  } finally { env.restore(); }
});

test("panel: nothing running, nothing left behind and unreadable records each say so", async () => {
  const env = changes();
  try {
    const p = await panel(env, [
      ok(reading()),
      ok(reading({ sessions: [session(1)] })),
      ok(reading({ sessions: [session(1, { id: "" })], noRecords: true })),
    ]);
    expect(p.page.text()).toContain(t("ai.run.none"));
    expect(p.page.text()).not.toContain(t("ai.run.left.none"));
    await p.tick();
    expect(p.page.text()).not.toContain(t("ai.run.none"));
    expect(p.page.text()).toContain(t("ai.run.left.none"));
    await p.tick();
    expect(p.page.text()).toContain(t("ai.run.left.noRecords"));
    expect(p.page.text()).not.toContain(t("ai.run.left.none"));
    expect(p.page.text()).not.toContain(t("ai.run.none"));
  } finally { env.restore(); }
});

test("panel: the footnote names the uncertain processes only when there are some", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({ sessions: [session(1)], uncertain: 2 })), ok(reading({ sessions: [session(1)] }))]);
    expect(p.page.text()).toContain(t("ai.run.foot") + t("ai.run.foot.uncertain", { n: 2 }));
    expect(p.page.text()).toContain(t("ai.run.foot.idle"));
    await p.tick();
    expect(p.page.text()).toContain(t("ai.run.foot"));
    expect(p.page.text()).not.toContain("另有");
    expect(p.page.text()).toContain(t("ai.run.foot.idle"));
  } finally { env.restore(); }
});

test("panel: a session row shows its name, how long it has idled or that it works, where it runs and its size", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({ sessions: [
      session(1, { since: NOW - 3 * HOUR, host: "Ghostty", memory: 240 * MB }),
      session(2, { status: "busy", project: "api", title: "" }),
      session(3, { status: "", since: 0, project: "", title: "" }),
      session(4, { since: 0, project: "docs", title: "" }),
    ] }))]);
    const one = textOf(p.row(1));
    expect(one).toContain("shop · 修复登录");
    expect(one).toContain(t("ai.run.idle", { time: "3 小时" }));
    expect(one).toContain(t("ai.run.in", { app: "Ghostty" }));
    expect(one).toContain(human(240 * MB));
    const two = textOf(p.row(2));
    expect(two).toContain("api");
    expect(two).toContain(t("ai.run.busy"));
    expect(two).not.toContain("在 ");
    // 读不到状态：照样列出，没有「空闲」也没有「正在工作」
    const three = textOf(p.row(3));
    expect(three).toContain(t("ai.run.unknownSession"));
    expect(three).not.toContain("空闲");
    expect(three).not.toContain(t("ai.run.busy"));
    // 空闲，但读不到空闲了多久：只写「空闲」，没有时长
    const four = textOf(p.row(4));
    expect(four).toContain(t("ai.run.idleNoTime"));
    expect(four).not.toMatch(/空闲 |天|小时|分钟/);
  } finally { env.restore(); }
});

test("panel: the session Cleanzo runs under has no End", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({ sessions: [session(1, { own: true }), session(2)] }))]);
    expect(p.hasEnd(1)).toBe(false);
    expect(p.hasEnd(2)).toBe(true);
  } finally { env.restore(); }
});

test("panel: a process row shows its command, ports, folder or worktree, how long it has run and its size", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({ left: [left("L1", [
      proc(41, { command: "node next dev", ports: [3000, 3001], cwd: "/Users/a/code/blog", start: (NOW - 2 * DAY) * 1e6, memory: 410 * MB }),
      proc(42, { command: "python -m http.server", cwd: "/Users/a/code/blog/.claude/worktrees/fix-nav", worktree: "fix-nav", start: (NOW - 5 * HOUR) * 1e6 }),
    ])] }))]);
    const one = textOf(p.row(41));
    for (const part of ["node next dev", t("ai.run.port", { ports: "3000、3001" }), "blog", "2 天", human(410 * MB)]) expect(one).toContain(part);
    expect(p.hasEnd(41)).toBe(true);
    const two = textOf(p.row(42));
    for (const part of ["python -m http.server", "fix-nav", "5 小时"]) expect(two).toContain(part);
    expect(two).not.toContain("端口");
  } finally { env.restore(); }
});

test("panel: a session that started commands opens to show them", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({ sessions: [session(1, { commands: [proc(11), proc(12)] }), session(2)] }))]);
    const toggle = () => nodes(p.row(1)!).find((el) => "aria-expanded" in el.props)!;
    // 命令行放在 Collapse 里：它的 open 跟着展开走
    const fold = () => p.page.ofType(Collapse).find((el) => nodes(el).some((n) => n.props["data-pid"] === 11))!;
    expect(toggle().props["aria-expanded"]).toBe(false);
    expect(fold().props.open).toBe(false);
    toggle().props.onClick();
    await p.page.settle();
    expect(toggle().props["aria-expanded"]).toBe(true);
    expect(fold().props.open).toBe(true);
    expect([p.row(11), p.row(12)].every(Boolean)).toBe(true);
    expect(nodes(p.row(2)!).some((el) => "aria-expanded" in el.props)).toBe(false);
  } finally { env.restore(); }
});

test("panel: ending a process, a closed session's processes, an idle session or a working one asks with the right words", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({
      sessions: [
        session(1, { host: "Ghostty", commands: [proc(11, { command: "node vite", ports: [5173] }), proc(12, { command: "tsc -w" })] }),
        session(2, { project: "api", title: "加限流", status: "busy", entry: "t3-code", host: "T3 Code" }),
      ],
      left: [left("L1", [proc(41, { command: "node next dev", ports: [3000] }), proc(42, { command: "python worker.py", ports: [8000, 8001] })])],
    }))]);
    // 一个进程：哪个会话启动的、占着的端口
    await p.end(11);
    let text = textOf(p.dialog());
    expect(text).toContain(t("ai.run.confirm.proc.title"));
    expect(text).toContain(t("ai.run.confirm.proc.body", { session: "shop · 修复登录" }));
    expect(text).toContain(t("ai.run.confirm.ports", { ports: "5173" }));
    expect(text).toContain("node vite");
    expect(p.confirmButton()!.props.variant).not.toBe("destructive");
    await p.cancel();
    expect(p.dialog()).toBeUndefined();
    // 一个已关闭会话的全部：逐个列出命令和端口
    await p.endAll("L1");
    text = textOf(p.dialog());
    expect(text).toContain(t("ai.run.confirm.left.title", { session: "blog · 换主题", n: 2 }));
    expect(text).toContain(`node next dev · ${t("ai.run.port", { ports: "3000" })}`);
    expect(text).toContain(`python worker.py · ${t("ai.run.port", { ports: "8000、8001" })}`);
    expect(p.confirmButton()!.props.variant).not.toBe("destructive");
    await p.cancel();
    // 空闲的会话：对话已经保存，2 条命令会留着；终端里的会话没有「某 App 启动的」那一句
    await p.end(1);
    text = textOf(p.dialog());
    expect(text).toContain(t("ai.run.confirm.session.title", { session: "shop · 修复登录" }));
    expect(text).toContain(t("ai.run.confirm.idle.body"));
    expect(text).toContain(t("ai.run.confirm.keeps", { n: 2 }));
    expect(text).not.toContain(t("ai.run.confirm.busy.body"));
    expect(text).not.toContain(t("ai.run.confirm.host", { app: "Ghostty" }));
    expect(p.confirmButton()!.props.variant).not.toBe("destructive");
    await p.cancel();
    // 在干活的会话：会打断当前这一步，按钮是危险样式；T3 Code 启动的，多一句
    await p.end(2);
    text = textOf(p.dialog());
    expect(text).toContain(t("ai.run.confirm.session.title", { session: "api · 加限流" }));
    expect(text).toContain(t("ai.run.confirm.busy.body"));
    expect(text).toContain(t("ai.run.confirm.host", { app: "T3 Code" }));
    expect(text).not.toContain(t("ai.run.confirm.idle.body"));
    expect(text).not.toContain("会留着");
    expect(p.confirmButton()!.props.variant).toBe("destructive");
  } finally { env.restore(); }
});

test("panel: a confirmed end sends the processes with their session, or the session with what was seen, then reads again", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({
      sessions: [session(1, { id: "S-a", start: 1000, commands: [proc(11, { start: 1100, folded: [{ pid: 12, start: 1200 }] })] })],
      left: [left("L1", [proc(41, { start: 4100 }), proc(42, { start: 4200, folded: [{ pid: 43, start: 4300 }] })])],
    }))]);
    expect(p.reads).toBe(1);
    await p.end(11);
    await p.confirm();
    expect(p.ended.at(-1)).toEqual(["procs", "S-a", [{ pid: 11, start: 1100 }, { pid: 12, start: 1200 }]]);
    expect(p.page.toasts.at(-1)).toEqual({ text: t("ai.run.done", { n: 2, size: human(10 * MB) }), kind: "ok" });
    expect(p.reads).toBe(2);
    await p.endAll("L1");
    await p.confirm();
    expect(p.ended.at(-1)).toEqual(["procs", "L1", [{ pid: 41, start: 4100 }, { pid: 42, start: 4200 }, { pid: 43, start: 4300 }]]);
    expect(p.reads).toBe(3);
    await p.end(1);
    await p.confirm();
    expect(p.ended.at(-1)).toEqual(["session", { pid: 1, start: 1000 }, "S-a", "idle"]);
    expect(p.reads).toBe(4);
  } finally { env.restore(); }
});

test("panel: an end the backend refuses says why, and the list is read again", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({ sessions: [session(1)] }))]);
    p.endSession.mockImplementation(async () => { throw new Error("blocked"); });
    await p.end(1);
    await p.confirm();
    expect(p.page.toasts.at(-1)).toEqual({ text: t("fs.blocked"), kind: "err" });
    expect(p.reads).toBe(2);
  } finally { env.restore(); }
});

test("panel: while ending, the row says so, no other End starts, and the list stays put until it is read again", async () => {
  const env = changes();
  try {
    const p = await panel(env, [
      ok(reading({ sessions: [session(1), session(2)] })),
      ok(reading({ sessions: [session(2), session(1)] })),
      ok(reading({ sessions: [session(2)] })),
    ]);
    const gate = deferred<ClaudeEndResult>();
    p.endSession.mockImplementation(() => gate.promise);
    await p.end(1);
    await p.confirm();
    expect(textOf(p.endButton(1))).toBe(t("ai.run.ending"));
    expect(p.endButton(2)!.props["aria-disabled"]).toBe(true);
    p.endButton(2)!.props.onClick();
    await p.page.settle();
    expect(p.dialog()).toBeUndefined();
    await p.tick();
    expect(p.reads).toBe(2);
    expect(p.sessionPids()).toEqual([1, 2]);
    gate.resolve(endedAll([{ pid: 1 }]));
    await p.page.settle();
    expect(p.reads).toBe(3);
    expect(p.sessionPids()).toEqual([2]);
  } finally { env.restore(); }
});

test("panel: reads on entry, on each scheduled round and on refresh, never while inactive or hidden, and reports the count each time", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({ sessions: [session(1, { since: NOW - 3 * HOUR })], left: [left("L1", [proc(41)])] }))]);
    expect(p.reads).toBe(1);
    expect(p.queued()).toBe(1);
    await p.tick();
    expect(p.reads).toBe(2);
    expect(p.queued()).toBe(1);
    p.page.setProps({ refresh: 1 });
    await p.page.settle();
    expect(p.reads).toBe(3);
    expect(p.queued()).toBe(1);
    expect(p.counts).toEqual([2, 2, 2]);
    p.page.setProps({ active: false });
    await p.page.settle();
    expect(p.queued()).toBe(0);
    p.page.setProps({ refresh: 2 });
    await p.page.settle();
    expect(p.reads).toBe(3);
    p.page.setProps({ active: true });
    await p.page.settle();
    expect(p.reads).toBe(4);
    p.page.setProps({ visit: 2 });
    await p.page.settle();
    expect(p.reads).toBe(5);
    p.page.document.hidden = true;
    await p.tick();
    expect(p.reads).toBe(5);
    expect(p.queued()).toBe(0);
    expect(p.counts).toEqual([2, 2, 2, 2, 2]);
  } finally { env.restore(); }
});

test("panel: a window that was hidden reads at once when it is shown again, and keeps reading", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({ sessions: [session(1)] }))]);
    await p.visibility(true);
    expect([p.reads, p.queued()]).toEqual([1, 0]);
    await p.visibility(false);
    expect([p.reads, p.queued()]).toEqual([2, 1]);
  } finally { env.restore(); }
});

test("panel: one read at a time; a reading that comes back after a newer request, or after the panel left, is dropped", async () => {
  const env = changes();
  try {
    const first = deferred<ClaudeRunning>();
    const third = deferred<ClaudeRunning>();
    const p = await panel(env, [
      () => first.promise, ok(reading({ sessions: [session(2)] })),
      () => third.promise, ok(reading({ sessions: [session(4)] })),
    ]);
    p.page.setProps({ refresh: 1 });
    await p.page.settle();
    expect(p.reads).toBe(1);
    first.resolve(reading({ sessions: [session(1)] }));
    await p.page.settle();
    expect(p.reads).toBe(2);
    expect(p.sessionPids()).toEqual([2]);
    expect(p.counts).toEqual([1]);
    p.page.setProps({ refresh: 2 });
    await p.page.settle();
    expect(p.reads).toBe(3);
    p.page.setProps({ active: false });
    await p.page.settle();
    third.resolve(reading({ sessions: [session(3)] }));
    await p.page.settle();
    expect(p.sessionPids()).toEqual([2]);
    expect([p.reads, p.queued(), p.counts.length]).toEqual([3, 0, 1]);
    p.page.setProps({ active: true });
    await p.page.settle();
    expect(p.reads).toBe(4);
    expect(p.sessionPids()).toEqual([4]);
  } finally { env.restore(); }
});

test("panel: while a confirm is open, a reading in another order keeps the rows in place with the new numbers", async () => {
  const env = changes();
  try {
    const p = await panel(env, [
      ok(reading({ sessions: [session(1, { memory: 100 * MB }), session(2, { memory: 200 * MB })] })),
      ok(reading({ sessions: [session(2, { memory: 250 * MB }), session(3), session(1, { memory: 150 * MB })] })),
    ]);
    await p.end(1);
    await p.tick();
    expect(p.reads).toBe(2);
    expect(p.sessionPids()).toEqual([1, 2]);
    expect(textOf(p.row(1))).toContain(human(150 * MB));
    expect(textOf(p.row(2))).toContain(human(250 * MB));
    await p.cancel();
    expect(p.sessionPids()).toEqual([2, 3, 1]);
  } finally { env.restore(); }
});

test("panel: leaving the page closes an open confirm", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({ sessions: [session(1)] }))]);
    await p.end(1);
    expect(p.dialog()).toBeDefined();
    p.page.setProps({ active: false });
    await p.page.settle();
    expect(p.dialog()).toBeUndefined();
  } finally { env.restore(); }
});

test("panel: a failed read says so with Retry and keeps the last list", async () => {
  const env = changes();
  try {
    const p = await panel(env, [ok(reading({ sessions: [session(1)] })), fails, ok(reading({ sessions: [session(2)] }))]);
    await p.tick();
    expect(p.page.text()).toContain(t("ai.run.error"));
    expect(p.sessionPids()).toEqual([1]);
    p.page.click(t("btn.retry"));
    await p.page.settle();
    expect(p.reads).toBe(3);
    expect(p.page.text()).not.toContain(t("ai.run.error"));
    expect(p.sessionPids()).toEqual([2]);
  } finally { env.restore(); }
});
