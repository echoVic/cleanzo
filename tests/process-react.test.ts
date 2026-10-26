import { expect, test } from "bun:test";
import type { GrowthSummary, ProcessRow, ProcessSample } from "../src/mygo";

// i18n 解析读 localStorage，这里钉住中文。
const storage = new Map([["cleanzo.lang", "zh"]]);
Object.assign(globalThis, {
  localStorage: { getItem: (k: string) => storage.get(k), setItem: (k: string, v: string) => storage.set(k, v) },
});

const {
  PROCESS_DIAG, diagnose, est, growthTags, pruneStuck, pushCpu, refreshFrozen, rowTag, shouldWatch, sortChildren, sortRows,
} = await import("../src/react/process/format");

const GB = 2 ** 30;

function row(p: Partial<ProcessRow> & { key: string }): ProcessRow {
  return { name: p.key, kind: "app", path: "", bundleId: "", cpu: 0, memory: 0, procs: 1, estimated: false, quit: false, children: [], ...p };
}

function sample(p: Partial<ProcessSample> = {}): ProcessSample {
  return { rows: [], cpu: 0, pressure: 1, own: 0, others: 0, ...p };
}

const chrome = row({ key: "app:/applications/google chrome.app", name: "Google Chrome", path: "/Applications/Google Chrome.app", bundleId: "com.google.chrome", cpu: 53.8, memory: 31.5 * GB, quit: true });
const ws = row({ key: "sys:WindowServer", name: "WindowServer", kind: "system", cpu: 40.2, memory: 0.13 * GB, estimated: true });
const node = row({ key: "cli:node", name: "node", kind: "cli", cpu: 12.6, memory: 2.9 * GB });
const lark = row({ key: "app:/applications/lark.app", name: "Lark", path: "/Applications/Lark.app", bundleId: "com.electron.lark", cpu: 21.4, memory: 4.28 * GB, quit: true });

test("sortRows: CPU or memory high to low, ties by name, without touching the sample", () => {
  const rows = [node, ws, chrome, lark];
  expect(sortRows(rows, "cpu").map((r) => r.name)).toEqual(["Google Chrome", "WindowServer", "Lark", "node"]);
  expect(sortRows(rows, "mem").map((r) => r.name)).toEqual(["Google Chrome", "Lark", "node", "WindowServer"]);
  const tie = [row({ key: "b", name: "b", cpu: 1 }), row({ key: "a", name: "a", cpu: 1 })];
  expect(sortRows(tie, "cpu").map((r) => r.name)).toEqual(["a", "b"]);
  expect(rows.map((r) => r.name)).toEqual(["node", "WindowServer", "Google Chrome", "Lark"]);
});

test("sortChildren follows the tab's order", () => {
  const kids = [
    { name: "Helper", count: 25, cpu: 5.5, memory: 5.3 * GB, estimated: false },
    { name: "Renderer", count: 61, cpu: 38.2, memory: 24.1 * GB, estimated: false },
    { name: "GPU", count: 1, cpu: 6.1, memory: 1.2 * GB, estimated: false },
  ];
  expect(sortChildren(kids, "cpu").map((c) => c.name)).toEqual(["Renderer", "GPU", "Helper"]);
  expect(sortChildren(kids, "mem").map((c) => c.name)).toEqual(["Renderer", "Helper", "GPU"]);
});

test("refreshFrozen keeps the rows and their order, updates numbers by key, keeps rows that left", () => {
  const rows = refreshFrozen([chrome, node], sample({ rows: [{ ...node, cpu: 90 }, ws] }));
  expect(rows.map((r) => r.key)).toEqual([chrome.key, node.key]);
  expect(rows[1]!.cpu).toBe(90);
  expect(rows[0]).toBe(chrome);
});

test("diagnose: memory pressure names the biggest memory user", () => {
  for (const pressure of [2, 4]) {
    expect(diagnose(sample({ pressure, rows: [node, chrome, ws] }), [])).toEqual({ kind: "mem", top: chrome });
  }
  expect(diagnose(sample({ pressure: 1, rows: [chrome] }), [])).toBeNull();
  expect(diagnose(null, [99, 99, 99])).toBeNull();
});

test("diagnose: CPU only after three readings at 80% or more, and memory wins", () => {
  const s = sample({ rows: [node, ws, chrome] });
  expect(diagnose(s, [80, 85, 99])).toEqual({ kind: "cpu", top: chrome });
  expect(diagnose(s, [85, 99])).toBeNull();
  expect(diagnose(s, [79.9, 99, 99])).toBeNull();
  expect(diagnose({ ...s, pressure: 2 }, [99, 99, 99])!.kind).toBe("mem");
  expect(diagnose(sample({ pressure: 4 }), [])).toEqual({ kind: "mem", top: null });
  expect(PROCESS_DIAG).toEqual({ cpuBusy: 80, cpuSamples: 3 });
});

test("pushCpu keeps the last three readings", () => {
  expect(pushCpu([1, 2, 3], 4)).toEqual([2, 3, 4]);
  expect(pushCpu([], 5)).toEqual([5]);
});

test("shouldWatch: only while shown, on CPU or memory, running and visible", () => {
  const on = { active: true, tab: "cpu" as const, running: true, hidden: false };
  expect(shouldWatch(on)).toBe(true);
  expect(shouldWatch({ ...on, tab: "mem" })).toBe(true);
  expect(shouldWatch({ ...on, tab: "disk" })).toBe(false);
  expect(shouldWatch({ ...on, active: false })).toBe(false);
  expect(shouldWatch({ ...on, running: false })).toBe(false);
  expect(shouldWatch({ ...on, hidden: true })).toBe(false);
});

test("est marks estimates but never a dash", () => {
  expect(est("40%", true)).toBe("≈ 40%");
  expect(est("40%", false)).toBe("40%");
  expect(est("—", true)).toBe("—");
});

function summary(p: Partial<GrowthSummary> = {}): GrowthSummary {
  return {
    supported: true,
    config: { enabled: true, openAtLogin: false, dailyGB: 2, weeklyGB: 10, ignored: [], asked: true },
    status: { running: false, lastAt: 0, nextAt: 0, fda: true, notifications: "allowed" },
    alerts: [], ...p,
  };
}
const alert = (key: string, bytes: number, window = "week") =>
  ({ key, name: key, window, days: 7, bytes, atLeast: false, folder: "", total: 0, at: 0 });

test("growthTags: the biggest alert per owner, without ignored ones, only while monitoring", () => {
  const s = summary({ alerts: [
    alert("app:com.electron.lark", 11 * GB), alert("app:com.electron.lark", 3 * GB, "day"),
    alert("app:com.youqu.todesk.mac", 5 * GB, "day"), alert("dir:/users/me/.npm", GB),
  ] });
  const tags = growthTags(s);
  expect([...tags.keys()].sort()).toEqual(["app:com.electron.lark", "app:com.youqu.todesk.mac", "dir:/users/me/.npm"]);
  expect(tags.get("app:com.electron.lark")).toEqual({ key: "app:com.electron.lark", window: "week", bytes: 11 * GB, atLeast: false });
  expect(tags.get("app:com.youqu.todesk.mac")!.window).toBe("day");
  expect(growthTags({ ...s, config: { ...s.config, ignored: ["app:com.youqu.todesk.mac"] } }).has("app:com.youqu.todesk.mac")).toBe(false);
  expect(growthTags({ ...s, config: { ...s.config, enabled: false } }).size).toBe(0);
  expect(growthTags({ ...s, supported: false }).size).toBe(0);
  expect(growthTags(null).size).toBe(0);
});

test("rowTag: only app rows, matched by bundle ID", () => {
  const tags = growthTags(summary({ alerts: [alert("app:com.electron.lark", 11 * GB)] }));
  expect(rowTag(lark, tags)?.bytes).toBe(11 * GB);
  expect(rowTag(chrome, tags)).toBeNull();
  expect(rowTag({ ...lark, kind: "system" }, tags)).toBeNull();
  expect(rowTag({ ...lark, bundleId: "" }, tags)).toBeNull();
});

test("pruneStuck drops apps that are gone and returns the same set when nothing changed", () => {
  expect([...pruneStuck(new Set([chrome.path, lark.path]), [chrome, node])]).toEqual([chrome.path]);
  const same = new Set([chrome.path]);
  expect(pruneStuck(same, [chrome])).toBe(same);
});
