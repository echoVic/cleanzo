import type { ProcessRowViewProps } from "../src/react/process/ProcessPage";
import { expect, spyOn, test } from "bun:test";
import type { GrowthSummary, ProcessRow, ProcessSample } from "../src/mygo";
import { fmtMem, fmtPct } from "../src/react/status/format";

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

test("diagnose: only the most recent three CPU readings determine the diagnosis", () => {
  const s = sample({ rows: [chrome, node] });
  expect(diagnose(s, [20, 80, 85, 99])).toEqual({ kind: "cpu", top: chrome });
  expect(diagnose(s, [99, 99, 79, 99])).toBeNull();
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

test("rowTag: Apple apps can use macos owner tags, with app tags taking priority", () => {
  const safari = row({ key: "app:/applications/safari.app", bundleId: "com.apple.safari" });
  const tags = growthTags(summary({ alerts: [alert("macos:com.apple.safari", 5 * GB)] }));
  expect(rowTag(safari, tags)).toBe(tags.get("macos:com.apple.safari")!);
  const both = growthTags(summary({ alerts: [alert("macos:com.apple.safari", 5 * GB), alert("app:com.apple.safari", GB)] }));
  expect(rowTag(safari, both)).toBe(both.get("app:com.apple.safari")!);
  expect(rowTag(safari, new Map())).toBeNull();
});

test("pruneStuck drops apps that are gone and returns the same set when nothing changed", () => {
  expect([...pruneStuck(new Set([chrome.path, lark.path]), [chrome, node])]).toEqual([chrome.path]);
  const same = new Set([chrome.path]);
  expect(pruneStuck(same, [chrome])).toBe(same);
});

/** 渲染一行的静态 HTML；setup-dom 要先于 shared.ts 装好。 */
async function rowHtml(p: Partial<ProcessRowViewProps> & { row: ProcessRow }): Promise<string> {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { ProcessRowView } = await import("../src/react/process/ProcessPage");
  const { t } = await import("../src/i18n");
  return renderToStaticMarkup(createElement(ProcessRowView, {
    sortBy: "cpu", open: false, tag: null, busy: false, stuck: false, done: false, t,
    onToggle() {}, onQuit() {}, onForce() {}, onTag() {}, ...p,
  }));
}

test("rows: a system process is marked, its numbers are estimates, and it has no buttons", async () => {
  const { t } = await import("../src/i18n");
  const html = await rowHtml({ row: ws });
  expect(html).toContain("WindowServer");
  expect(html).toContain(t("process.system"));
  expect(html).toContain("≈ 40%");
  expect(html).not.toContain("st-quit");
  expect(html).not.toContain("pr-force");
});

test("rows: an app quits; while it is still running it offers force quit; a quit row has no buttons", async () => {
  const { t } = await import("../src/i18n");
  const app = { ...chrome, procs: 62, children: [
    { name: "Google Chrome", count: 1, cpu: 4, memory: 0.9 * GB, estimated: false },
    { name: "Google Chrome Helper (Renderer)", count: 61, cpu: 38.2, memory: 24.1 * GB, estimated: false },
  ] };
  const idle = await rowHtml({ row: app });
  expect(idle).toContain("st-quit");
  expect(idle).not.toContain("pr-force");
  expect(idle).toContain(t("status.apps.procs", { n: 62 }));
  // 忙态按钮保持可聚焦（aria-disabled 而非 disabled），弹层关闭后焦点能还回行内
  const busyHtml = await rowHtml({ row: app, busy: true });
  expect(busyHtml).toContain("st-quit");
  expect(busyHtml).toContain('aria-disabled="true"');
  expect(busyHtml).not.toContain(" disabled");
  const stuck = await rowHtml({ row: app, stuck: true });
  expect(stuck).toContain(t("process.stuck"));
  expect(stuck).toContain("pr-force");
  expect(stuck).not.toContain("st-quit");
  const stuckBusy = await rowHtml({ row: app, stuck: true, busy: true });
  expect(stuckBusy).toMatch(/class="pr-force"[^>]*aria-disabled="true"/);
  const done = await rowHtml({ row: app, done: true });
  expect(done).toContain("pr-item quitted");
  expect(done).not.toContain("st-quit");
  expect(done).not.toContain("pr-force");
  expect(await rowHtml({ row: app, open: true })).toContain("Google Chrome Helper (Renderer) × 61");
  expect(await rowHtml({ row: app, open: false })).not.toContain("× 61");
});

test("rows: a growth tag shows its window and size; command-line rows have no quit", async () => {
  const { t } = await import("../src/i18n");
  const { human } = await import("../src/shared");
  const tag = { key: "app:com.electron.lark", window: "week" as const, bytes: 11.2 * GB, atLeast: false };
  expect(await rowHtml({ row: lark, tag })).toContain(t("process.tag.week", { size: `+${human(tag.bytes)}` }));
  expect(await rowHtml({ row: node })).not.toContain("st-quit");
});

test("rows: an app row shows its icon once known; other rows keep the letter", async () => {
  const { loadIcon, resetIconsForTest } = await import("../src/react/icons/cache");
  resetIconsForTest(async () => "data:image/png;base64,QUJD");
  try {
    await loadIcon(chrome.path);
    const app = await rowHtml({ row: chrome });
    expect(app).toContain('class="st-ico cz-app-img" src="data:image/png;base64,QUJD"');
    const cli = await rowHtml({ row: node });
    expect(cli).toContain('class="st-ico pr-ico-sys"');
    expect(cli).not.toContain("<img");
  } finally { resetIconsForTest(async () => ""); }
});

test("rows: a row a menu bar panel named is highlighted", async () => {
  expect(await rowHtml({ row: chrome, hl: true })).toContain('class="pr-item pr-hl"');
  expect(await rowHtml({ row: chrome })).not.toContain("pr-hl");
});

test("sortRows compares the numbers the page shows; equal ones keep name order", () => {
  const MB = 2 ** 20;
  const a = row({ key: "a", name: "a", cpu: 0.2, memory: 512.2 * MB });
  const b = row({ key: "b", name: "b", cpu: 0.4, memory: 512.4 * MB });
  expect(sortRows([b, a], "cpu").map((r) => r.name)).toEqual(["a", "b"]); // both 0%
  expect(sortRows([b, a], "mem").map((r) => r.name)).toEqual(["a", "b"]); // both 512 MB
  const roundedDown = row({ key: "c", name: "c", memory: 512.4 * MB }); // 512 MB
  const roundedUp = row({ key: "d", name: "d", memory: 512.6 * MB }); // 513 MB
  // Keep the tie in this assertion so raw-value sorting fails too; truncation would put d last.
  expect(sortRows([b, a, roundedDown, roundedUp], "mem").map((r) => r.name)).toEqual(["d", "a", "b", "c"]);
  const c = row({ key: "c", name: "c", cpu: 0.6 }); // 1%
  expect(sortRows([a, b, c], "cpu").map((r) => r.name)).toEqual(["c", "a", "b"]);
  const x = row({ key: "x", name: "x", memory: 1.01 * GB }); // 1 GB
  const y = row({ key: "y", name: "y", memory: 1.04 * GB }); // 1 GB
  const z = row({ key: "z", name: "z", memory: 1.06 * GB }); // 1.1 GB
  expect(sortRows([y, x, z], "mem").map((r) => r.name)).toEqual(["z", "x", "y"]);
  const kids = [
    { name: "q", count: 1, cpu: 0.3, memory: 512.4 * MB, estimated: false },
    { name: "p", count: 1, cpu: 0.1, memory: 512.2 * MB, estimated: false },
  ];
  expect(sortChildren(kids, "cpu").map((k) => k.name)).toEqual(["p", "q"]); // both 0%
  const r = { name: "r", count: 1, cpu: 0, memory: 512.6 * MB, estimated: false }; // 513 MB
  expect(sortChildren([...kids, r], "mem").map((k) => k.name)).toEqual(["r", "p", "q"]); // p and q both 512 MB
});

// Compare displayed quantities rather than text: "1024 MB" and "1 GB" both mean 1 GiB.
test("sortRows follows fmtMem and fmtPct across display boundaries", () => {
  const MB = 2 ** 20;
  const memory = [
    0, 1, 0.4 * MB, 0.5 * MB - 1, 0.5 * MB, 0.5 * MB + 1, 0.6 * MB,
    512.49 * MB, 512.5 * MB, 512.51 * MB,
    GB - 1, GB, 1.04 * GB, 1.05 * GB, 1.06 * GB,
  ];
  const cpu = [0, 0.4, 0.5, 0.6, 1.49, 1.5, 99.5];

  function displayQuantity(text: string, field: "memory" | "cpu") {
    if (text === "—") return 0;
    const value = parseFloat(text);
    return field === "memory" ? value * (text.endsWith(" GB") ? GB : MB) : value;
  }

  function checkPairs(values: readonly number[], field: "memory" | "cpu", fmt: (value: number) => string) {
    for (const [i, x] of values.entries()) {
      for (const y of values.slice(i + 1)) {
        const high = Math.max(x, y);
        const low = Math.min(x, y);
        // Name order and raw-value order must disagree to distinguish display ties.
        const a = row({ key: "a", name: "a", [field]: low });
        const b = row({ key: "b", name: "b", [field]: high });
        const sorted = sortRows([b, a], field === "memory" ? "mem" : "cpu");
        const context = `${field}: ${high} (${fmt(high)}) vs ${low} (${fmt(low)})`;
        if (displayQuantity(fmt(high), field) === displayQuantity(fmt(low), field)) {
          expect(sorted.map((r) => r.name), context).toEqual(["a", "b"]);
        } else {
          expect(sorted.map((r) => r.name), context).toEqual(["b", "a"]);
        }
      }
    }
  }

  checkPairs(memory, "memory", fmtMem);
  checkPairs(cpu, "cpu", fmtPct);
});

test("sortRows and sortChildren compare names with one shared collator", () => {
  const spy = spyOn(String.prototype, "localeCompare");
  try {
    const rows = Array.from({ length: 40 }, (_, i) => row({ key: `k${i}`, name: `n${i % 7}`, cpu: i % 3 }));
    const kids = rows.map((r) => ({ name: r.name, count: 1, cpu: r.cpu, memory: 0, estimated: false }));
    const sorted = sortRows(rows, "cpu");
    sortChildren(kids, "mem");
    expect(spy).not.toHaveBeenCalled();
    expect(sorted.map((r) => r.name).slice(0, 3)).toEqual(["n0", "n0", "n1"]);
  } finally { spy.mockRestore(); }
});

test("rows: a closed row does not build its child rows", async () => {
  let built = 0;
  const kids = [
    { name: "Google Chrome Helper (Renderer)", count: 61, cpu: 38.2, memory: 24.1 * GB, estimated: false },
    { name: "Google Chrome", count: 1, cpu: 4, memory: 0.9 * GB, estimated: false },
  ];
  const counted = new Proxy(kids, {
    get(target, prop, receiver) {
      if (prop === Symbol.iterator || prop === "map" || prop === "slice") built++;
      return Reflect.get(target, prop, receiver);
    },
  });
  const app = { ...chrome, procs: 62, children: counted };
  expect(await rowHtml({ row: app, open: false })).not.toContain("× 61");
  expect(built).toBe(0);
  expect(await rowHtml({ row: app, open: true })).toContain("Google Chrome Helper (Renderer) × 61");
  expect(built).toBeGreaterThan(0);
});

/** 采样通道的替身：push 一条，页面的 for await 就收到一条。 */
function fakeChannel() {
  const queue: ProcessSample[] = [];
  let wake: (() => void) | null = null;
  let closed = false;
  return {
    push(s: ProcessSample) { queue.push(s); wake?.(); },
    close() { closed = true; wake?.(); },
    async *[Symbol.asyncIterator]() {
      while (!closed) {
        const s = queue.shift();
        if (s) { yield s; continue; }
        await new Promise<void>((r) => { wake = r; });
        wake = null;
      }
    },
  };
}

async function processPage() {
  await import("./setup-dom");
  const { pageHarness } = await import("./page-harness");
  const { ProcessPage } = await import("../src/react/process/ProcessPage");
  const { GrowthPanel } = await import("../src/react/growth/GrowthPage");
  const { processApi } = await import("../src/react/process/api");
  const { growthApi } = await import("../src/react/growth/api");
  const ch = fakeChannel();
  const g = globalThis as { mygo?: unknown };
  const previous = g.mygo;
  g.mygo = { channel: () => ch };
  const spies = [
    spyOn(processApi, "watch").mockImplementation(() => new Promise<void>(() => {})),
    spyOn(growthApi, "summary").mockImplementation(async () => summary()),
    spyOn(growthApi, "onUpdated").mockImplementation(() => () => {}),
  ];
  const page = pageHarness(ProcessPage, {
    active: true, visit: 1, request: { tab: null, focusKey: null, nonce: 0 }, goLeftovers() {}, goUninstall() {},
  });
  await page.settle();
  return {
    page, ch,
    rows() { return page.all().filter((el) => "onToggle" in el.props && el.props.row); },
    tab(v: string) { page.find((el) => typeof el.props.onValueChange === "function")!.props.onValueChange(v); },
    growth() { return page.ofType(GrowthPanel)[0]; },
    close() { page.close(); for (const s of spies) s.mockRestore(); g.mygo = previous; },
  };
}

test("page: rows are sorted once per sample and sort key, not on every render", async () => {
  const pp = await processPage();
  try {
    let reads = 0;
    const rows = [node, chrome, lark];
    pp.ch.push({ ...sample(), get rows() { reads++; return rows; } } as ProcessSample);
    await pp.page.settle();
    expect(pp.rows().map((r) => r.props.row.name)).toEqual(["Google Chrome", "Lark", "node"]);
    reads = 0;
    pp.rows()[0]!.props.onToggle();
    await pp.page.settle();
    expect(pp.rows()[0]!.props.open).toBe(true);
    expect(reads).toBe(0);
    pp.tab("mem");
    await pp.page.settle();
    expect(reads).toBeGreaterThan(0);
  } finally { pp.close(); }
});

test("page: the disk tab keeps its panel while CPU or memory is shown", async () => {
  const pp = await processPage();
  try {
    pp.tab("disk");
    await pp.page.settle();
    expect(pp.growth()?.props.active).toBe(true);
    pp.tab("cpu");
    await pp.page.settle();
    // 面板留着（隐藏、不刷新），回到磁盘占用标签时还是上次的内容，不从空白开始。
    expect(pp.growth()?.props.active).toBe(false);
    pp.tab("disk");
    await pp.page.settle();
    expect(pp.growth()?.props.active).toBe(true);
  } finally { pp.close(); }
});
