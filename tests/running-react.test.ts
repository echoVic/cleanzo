import "./setup-dom";
import { expect, test } from "bun:test";
import type { ClaudeEndResult, ClaudeLeft, ClaudeProc, ClaudeRunSummary, ClaudeRunning, ClaudeSession } from "../src/mygo";
import { dict, t } from "../src/i18n";
import { human } from "../src/react/ai/format";
import {
  RUNNING, badgeCount, durationText, endToast, freezeRunning, leftTargets, procTargets, sessionName,
} from "../src/react/ai/running";
import { changes } from "./page-harness";

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
  const gone = freezeRunning(shown, reading({ at: NOW + 5 }));
  expect(gone.sessions.map((s) => [s.pid, s.memory])).toEqual([[1, 100], [2, 200]]);
  expect(gone.left[0]!.procs.map((p) => p.pid)).toEqual([21, 22]);
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
  expect(keys.length).toBe(40);
  const holes = (s: string) => [...s.matchAll(/\{(\w+)[|}]/g)].map((m) => m[1]).sort();
  const en: Readonly<Record<string, string>> = dict.en;
  for (const k of keys) {
    expect(en[k], k).toBeDefined();
    expect([...new Set(holes(en[k]!))], k).toEqual([...new Set(holes(dict.zh[k]))]);
  }
});
