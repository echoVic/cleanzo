import "./setup-dom";
import { expect, test } from "bun:test";
import type { ClaudeEndResult, ClaudeRunSummary, ClaudeRunning } from "../src/mygo";
import { changes } from "./page-harness";

// ---- 桥：页面只经 runningApi 碰 Cleaner ----

test("runningApi passes each call and its arguments to the Cleaner method behind it", async () => {
  const { Cleaner } = await import("../src/mygo");
  const { runningApi } = await import("../src/react/ai/api");
  const env = changes();
  try {
    const reading: ClaudeRunning = { sessions: [], left: [], uncertain: 1, noRecords: false, at: 1 };
    const summary: ClaudeRunSummary = { idle: 2, left: 3, rows: [] };
    const result: ClaudeEndResult = { procs: [{ pid: 10, result: "ended" }], freed: 5 };
    const calls: unknown[][] = [];
    env.spy(Cleaner, "claudeRunning").mockImplementation(async () => reading);
    env.spy(Cleaner, "claudeRunSummary").mockImplementation(async () => summary);
    env.spy(Cleaner, "endClaudeProcesses").mockImplementation(async (...args) => { calls.push(["procs", ...args]); return result; });
    env.spy(Cleaner, "endClaudeSession").mockImplementation(async (...args) => { calls.push(["session", ...args]); return result; });
    expect(await runningApi.read()).toBe(reading);
    expect(await runningApi.summary()).toBe(summary);
    const targets = [{ pid: 10, start: 100 }, { pid: 11, start: 101 }];
    expect(await runningApi.endProcs("S1", targets)).toBe(result);
    expect(await runningApi.endSession({ pid: 12, start: 102 }, "S2", "idle")).toBe(result);
    expect(calls).toEqual([["procs", "S1", targets], ["session", { pid: 12, start: 102 }, "S2", "idle"]]);
  } finally { env.restore(); }
});
