import { expect, test } from "bun:test";
import type { GrowthApp, GrowthState, MachineInfo, MachineSample, ProcessRow, ProcessSample, StatusSample } from "../src/mygo";

// shared.ts 顶层要 DOM：先装最小环境，再按中文解析文案。
const { setLang } = await import("./setup-dom");
const { t } = await import("../src/i18n");
const F = await import("../src/panel/format");

const GB = 2 ** 30;

function machine(p: Partial<MachineSample> = {}): MachineSample {
  return {
    thermal: 0, chipTemp: 52.4, fanCount: 2, fans: [1350, 1400], power: 12.6, perfCpu: 44, effCpu: 72, gpu: 14,
    gpuMemory: 1.5 * GB, diskRead: 2e6, diskWrite: 1e6, diskTemp: 38, netDown: 156_000, netUp: 55_000, netKind: "wifi",
    displaySig: "1,2", ...p,
  };
}

function sample(p: Partial<StatusSample> = {}): StatusSample {
  return {
    memory: { total: 32 * GB, used: 27.5 * GB, app: 6.4 * GB, wired: 6.6 * GB, compressed: 14.5 * GB, cached: 3.8 * GB, pressure: 1, swapUsed: 0, swapTotal: 0 },
    cpu: { usage: 51.2, load1: 3.3, load5: 2.5, load15: 2.4 },
    disk: { total: 494 * GB, used: 467.3 * GB, free: 26.7 * GB },
    battery: { percent: 86, charging: true, full: false, plugged: true, cycles: 120, maxCapacity: 92, condition: "Normal", temperature: 31.2, adapterWatts: 96 },
    apps: [], procs: 0, machine: machine(), ...p,
  };
}

function row(p: Partial<ProcessRow> & { key: string }): ProcessRow {
  return { name: p.key, kind: "app", path: "", bundleId: "", cpu: 0, memory: 0, procs: 1, estimated: false, quit: false, children: [], ...p };
}

function procSample(p: Partial<ProcessSample> = {}): ProcessSample {
  return { rows: [], cpu: 0, pressure: 1, own: 0, others: 0, ...p };
}

const chrome = row({ key: "app:/applications/google chrome.app", name: "Google Chrome", path: "/Applications/Google Chrome.app", cpu: 53.8, memory: 31.5 * GB, quit: true });
const ws = row({ key: "sys:WindowServer", name: "WindowServer", kind: "system", cpu: 40.2, memory: 0.13 * GB, estimated: true });
const node = row({ key: "cli:node", name: "node", kind: "cli", cpu: 12.6, memory: 2.9 * GB });

function input(p: Partial<import("../src/panel/format").DiagInput> = {}) {
  return { s: sample(), proc: null, cpuHist: [], gpuHist: [], growth: null, ...p };
}

function gapp(key: string, week: number | null, ignored = false): GrowthApp {
  return {
    key, name: key, kind: "app", orphan: false, appPath: "", size: 10 * GB, partial: false, ignored,
    day: null, week: week === null ? null : { bytes: week, atLeast: false, days: 7 }, month: null, spark: [], folders: [],
  };
}

function growth(enabled: boolean, apps: GrowthApp[] = []): GrowthState {
  return {
    supported: true,
    config: { enabled, openAtLogin: false, dailyGB: 2, weeklyGB: 10, ignored: [], asked: true },
    status: { running: false, lastAt: 0, nextAt: 0, fda: true, notifications: "allowed" },
    records: 5, apps,
    alerts: [{ key: "app:com.youqu.todesk.mac", name: "ToDesk", window: "day", days: 1, bytes: 5.6 * GB, atLeast: false,
      folder: "/Library/Application Support/ToDesk/dumps", total: 152 * GB, at: 0 }],
  };
}

test("panelItem reads ?item= and refuses the rest", () => {
  expect(F.panelItem("?item=mem")).toBe("mem");
  expect(F.panelItem("?item=fans")).toBeNull();
  expect(F.panelItem("")).toBeNull();
});

test("hero: each reading's big number, a dash when it can't be read", () => {
  const s = sample();
  expect(F.hero("cpu", s)).toEqual({ value: "51", unit: "%" });
  expect(F.hero("mem", s)).toEqual({ value: "86", unit: "%" });
  expect(F.hero("gpu", s)).toEqual({ value: "14", unit: "%" });
  expect(F.hero("net", s)).toEqual({ value: "156", unit: "KB/s" });
  expect(F.hero("disk", s)).toEqual({ value: "26.7", unit: "GB" });
  expect(F.hero("temp", s)).toEqual({ value: "52", unit: "°C" });
  expect(F.hero("battery", s)).toEqual({ value: "86", unit: "%" });
  const none = { value: "—", unit: "" };
  expect(F.hero("cpu", null)).toEqual(none);
  expect(F.hero("gpu", sample({ machine: machine({ gpu: -1 }) }))).toEqual(none);
  expect(F.hero("temp", sample({ machine: null }))).toEqual(none);
  expect(F.hero("battery", sample({ battery: null }))).toEqual(none);
});

test("subLine: the chip and cores, memory used, the network's kind and upload", () => {
  const info = { perfCores: 8, effCores: 4, gpuCores: 19 } as MachineInfo;
  const sys = { chip: "Apple M2 Pro", cores: 12 } as import("../src/mygo").SystemInfo;
  expect(F.subLine("cpu", sample(), info, sys, t)).toBe(
    ["Apple M2 Pro", t("status.spec.cpuSplit", { n: 12, p: 8, e: 4 }), t("status.cpu.load", { l1: "3.3", l5: "2.5", l15: "2.4" })].join(" · "));
  expect(F.subLine("mem", sample(), null, null, t)).toBe(t("menubar.panel.memSub", { used: "27.5 GB", total: "32 GB" }));
  expect(F.subLine("net", sample(), null, null, t)).toBe(`${t("status.net.wifi")} · ↑ 55 KB/s`);
  expect(F.subLine("battery", sample(), null, null, t)).toBe("");
});

test("diagnosis: CPU busy only after three readings at 80% or more, naming the top row", () => {
  const proc = procSample({ rows: [node, chrome, ws] });
  const vars = { name: "Google Chrome", pct: "54%" };
  expect(F.diagnosis("cpu", input({ proc, cpuHist: [85, 90, 81] }), t)).toEqual({ tone: "warn", text: t("process.diag.cpu", vars) });
  expect(F.diagnosis("cpu", input({ proc, cpuHist: [85, 70, 90] }), t)).toEqual({ tone: "ok", text: t("menubar.diag.cpuTop", vars) });
  expect(F.diagnosis("cpu", input({ proc, cpuHist: [30, 40, 20] }), t)).toEqual({ tone: "ok", text: t("menubar.diag.cpuIdle", vars) });
  expect(F.diagnosis("cpu", input({ proc, cpuHist: [95] }), t)).toEqual({ tone: "ok", text: t("menubar.diag.cpuTop", vars) });
  expect(F.diagnosis("cpu", input(), t)).toBeNull();
});

test("diagnosis: memory pressure uses the 进程 page's sentence; swap of 1 GB or more adds the home page's line", () => {
  const proc = procSample({ rows: [node, chrome] });
  const vars = { name: "Google Chrome", size: "31.5 GB" };
  const warn = F.diagnosis("mem", input({ s: sample({ memory: { ...sample().memory, pressure: 2, swapTotal: 37 * GB } }), proc }), t);
  expect(warn).toEqual({ tone: "warn", text: `${t("process.diag.mem", vars)} ${t("status.alert.swapLine", { size: "37 GB" })}` });
  expect(F.diagnosis("mem", input({ s: sample({ memory: { ...sample().memory, pressure: 4 } }), proc }), t)?.tone).toBe("crit");
  expect(F.diagnosis("mem", input({ proc }), t)).toEqual({ tone: "ok", text: t("menubar.diag.memOk", vars) });
});

test("diagnosis: GPU, network, temperature and battery", () => {
  expect(F.diagnosis("gpu", input({ gpuHist: [90, 95, 88] }), t)).toEqual({ tone: "warn", text: t("menubar.diag.gpuBusy") });
  expect(F.diagnosis("gpu", input({ gpuHist: [10] }), t)).toBeNull();
  expect(F.diagnosis("gpu", input({ gpuHist: [10, 20, 30] }), t)).toEqual({ tone: "ok", text: t("menubar.diag.gpuIdle") });
  expect(F.diagnosis("gpu", input({ gpuHist: [60, 70, 90] }), t)).toEqual({ tone: "ok", text: t("menubar.diag.gpuSome") });
  expect(F.diagnosis("gpu", input({ s: sample({ machine: machine({ gpu: -1 }) }) }), t)).toBeNull();
  expect(F.diagnosis("net", input(), t)?.text).toBe(t("menubar.diag.netWifi"));
  expect(F.diagnosis("net", input({ s: sample({ machine: machine({ netKind: "" }) }) }), t)?.text).toBe(t("menubar.diag.netNone"));
  expect(F.diagnosis("temp", input({ s: sample({ machine: machine({ thermal: 2 }) }) }), t)).toEqual({ tone: "crit", text: t("menubar.diag.thermal2") });
  expect(F.diagnosis("temp", input({ s: sample({ machine: machine({ thermal: -1 }) }) }), t)).toBeNull();
  expect(F.diagnosis("battery", input(), t)).toEqual({ tone: "ok", text: t("status.battery.charging", { pct: "86%" }) });
  const worn = sample({ battery: { ...sample().battery!, condition: "Service Recommended", charging: false, plugged: false } });
  expect(F.diagnosis("battery", input({ s: worn }), t)).toEqual({ tone: "warn", text: `${t("status.battery.onBattery")} · ${t("status.battery.service")}` });
});

test("diagnosis: disk space, and the biggest growth alert while monitoring is on", () => {
  expect(F.diagnosis("disk", input(), t)).toEqual({ tone: "ok", text: t("menubar.diag.diskOk", { size: "26.7 GB" }) });
  expect(F.diagnosis("disk", input({ s: sample({ disk: { total: 494 * GB, used: 482 * GB, free: 12 * GB } }) }), t)?.tone).toBe("warn");
  const alerted = F.diagnosis("disk", input({ growth: growth(true) }), t);
  expect(alerted?.tone).toBe("warn");
  expect(alerted?.text).toContain("ToDesk");
  expect(F.diagnosis("disk", input({ growth: growth(false) }), t)?.text).not.toContain("ToDesk");
  const full = sample({ disk: { total: 494 * GB, used: 494 * GB, free: 0 } });
  expect(F.hero("disk", full)).toEqual({ value: "0", unit: "MB" });
  expect(F.diagnosis("disk", input({ s: full }), t)).toEqual({ tone: "warn", text: t("status.alert.diskLow", { size: "0 MB" }) });
  expect(F.hero("disk", sample({ disk: { total: 0, used: 0, free: 0 } }))).toEqual({ value: "—", unit: "" });
  expect(F.diagnosis("disk", input({ s: sample({ disk: { total: 0, used: 0, free: 0 } }) }), t)).toBeNull();
});

test("chart: one point per reading, scaled to its range; memory colored by pressure", () => {
  const s = sample();
  expect(F.chartPoint("mem", s)).toBeCloseTo(85.9375);
  expect(F.chartPoint("disk", s)).toBe(3e6);
  expect(F.chartPoint("gpu", sample({ machine: machine({ gpu: -1 }) }))).toBeNull();
  expect(F.chartPoint("battery", sample({ battery: null }))).toBeNull();
  expect(F.chartMax("cpu", [12])).toBe(100);
  expect(F.chartMax("net", [5_000])).toBe(100_000);
  expect(F.chartMax("disk", [3e6, 2e6])).toBe(3e6);
  expect(F.chartMax("disk", [])).toBe(F.DISK_SPARK_FLOOR);
  expect(F.chartTone("mem", sample({ memory: { ...sample().memory, pressure: 2 } }))).toBe("warn");
  expect(F.chartTone("mem", s)).toBe("accent");
  expect(F.chartTone("gpu", s)).toBe("gpu");
});

test("detailRows: what each panel lists, leaving out what can't be read", () => {
  const info = {
    model: "", modelId: "", perfCores: 8, effCores: 4, gpuCores: 19, displaySig: "1,2",
    displays: [
      { id: 1, name: "Built-in Retina Display", builtin: true, width: 1728, height: 1117, hz: 120 },
      { id: 2, name: "LS27D80xU", builtin: false, width: 1920, height: 1080, hz: 60 },
    ],
    ssd: { model: "APPLE SSD AP0512Z", capacity: 5e11, smart: true, warning: 0, tempC: 33, spare: 100, used: 26, written: 624.5e12 },
  } as MachineInfo;
  const keys = (item: import("../src/panel/format").PanelItem, s = sample()) => F.detailRows(item, s, info, t).map((r) => r.key);
  const cpu = F.detailRows("cpu", sample(), info, t);
  expect(cpu.map((r) => r.key)).toEqual(["perf", "eff", "chip"]);
  expect(cpu[0]).toEqual({ key: "perf", label: t("status.cpu.perfN", { n: 8 }), value: "44%", bar: 44 });
  expect(cpu[2]!.value).toBe("52 °C · 1350 · 1400 rpm");
  expect(keys("cpu", sample({ machine: machine({ perfCpu: -1, effCpu: -1, chipTemp: 0, fanCount: -1, fans: [] }) }))).toEqual([]);
  expect(keys("mem")).toEqual(["app", "wired", "compressed", "cached"]);
  expect(keys("mem", sample({ memory: { ...sample().memory, swapUsed: 2 * GB, swapTotal: 3 * GB } }))).toContain("swap");
  expect(F.detailRows("gpu", sample(), info, t).map((r) => r.value)).toEqual(
    [t("status.cpu.cores", { n: 19 }), "1.5 GB", "1728×1117 @120Hz", "LS27D80xU 1920×1080 @60Hz"]);
  expect(keys("net")).toEqual(["down", "up"]);
  expect(keys("disk")).toEqual(["space", "model", "health", "life", "written", "temp", "read", "write"]);
  expect(keys("temp")).toEqual(["thermal", "chip", "fans", "power", "ssd", "battery"]);
  expect(keys("battery")).toEqual(["charge", "health", "cycles", "temp", "adapter"]);
  expect(keys("battery", sample({ battery: { ...sample().battery!, plugged: false, maxCapacity: 0, cycles: 0, temperature: 0 } }))).toEqual(["charge"]);
  expect(F.detailRows("cpu", null, info, t)).toEqual([]);
});

test("memShares: the three parts of memory as shares of the total", () => {
  const sh = F.memShares(sample().memory)!;
  expect(sh.app).toBeCloseTo(20);
  expect(sh.wired).toBeCloseTo(20.625);
  expect(sh.compressed).toBeCloseTo(45.3125);
  expect(F.memShares({ ...sample().memory, total: 0 })).toBeNull();
});

test("topRows: the top 5 by the panel's measure; frozen, the same rows in the same order with new numbers", () => {
  const rows = Array.from({ length: 7 }, (_, i) => row({ key: `cli:p${i}`, name: `p${i}`, kind: "cli", cpu: i * 10, memory: (7 - i) * GB }));
  const first = procSample({ rows });
  expect(F.topRows(first, "cpu", [], false).map((r) => r.name)).toEqual(["p6", "p5", "p4", "p3", "p2"]);
  expect(F.topRows(first, "mem", [], false).map((r) => r.name)).toEqual(["p0", "p1", "p2", "p3", "p4"]);
  const shown = F.topRows(first, "cpu", [], false);
  const next = procSample({ rows: rows.map((r) => (r.name === "p0" ? { ...r, cpu: 99 } : r.name === "p6" ? { ...r, cpu: 1 } : r)) });
  expect(F.topRows(next, "cpu", shown, true).map((r) => r.name)).toEqual(["p6", "p5", "p4", "p3", "p2"]);
  expect(F.topRows(next, "cpu", shown, true)[0]!.cpu).toBe(1);
  expect(F.topRows(next, "cpu", shown, false)[0]!.name).toBe("p0");
});

test("growthTop: apps that grew in 7 days, not ignored, biggest first, at most 5, only while monitoring", () => {
  const apps = [gapp("a", 3 * GB), gapp("b", 9 * GB), gapp("c", 5 * GB, true), gapp("d", -GB), gapp("e", null),
    gapp("f", GB), gapp("g", 2 * GB), gapp("h", 4 * GB), gapp("i", 6 * GB)];
  expect(F.growthTop(growth(true, apps)).map((a) => a.key)).toEqual(["b", "i", "h", "a", "g"]);
  expect(F.growthTop(growth(false, apps))).toEqual([]);
  expect(F.growthTop(null)).toEqual([]);
});

test("quit book: confirmations fall back, a pending quit holds the list, a quit app comes back quittable", () => {
  let b = F.withStep(F.withStep(F.emptyBook(), "a", "confirm"), "b", "forceConfirm");
  expect(F.listFrozen(false, b)).toBe(true);
  expect(F.quitPending(b)).toBe(false);
  b = F.revertConfirms(b);
  expect([...b.steps]).toEqual([["b", "stuck"]]);
  expect(F.revertConfirms(b)).toBe(b);
  expect(F.quitPending(F.withStep(b, "a", "quitting"))).toBe(true);
  expect(F.listFrozen(false, F.emptyBook())).toBe(false);
  expect(F.listFrozen(true, F.emptyBook())).toBe(true);

  const app = row({ key: "app:a", name: "A", quit: true });
  let d = F.syncBook(F.withStep(F.emptyBook(), "app:a", "done"), [app]); // still shutting down
  expect(d.steps.get("app:a")).toBe("done");
  d = F.syncBook(d, []); // gone
  expect(d.gone.has("app:a")).toBe(true);
  expect(F.syncBook(d, [])).toBe(d);
  d = F.syncBook(d, [app]); // opened again
  expect(d.steps.has("app:a")).toBe(false);
  expect(d.gone.size).toBe(0);
  expect(F.syncBook(F.withStep(F.emptyBook(), "app:a", "stuck"), []).steps.size).toBe(0);
});

async function rowHtml(p: Partial<import("../src/panel/PanelApp").PanelAppRowProps> & { row: ProcessRow }): Promise<string> {
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { PanelAppRow } = await import("../src/panel/PanelApp");
  return renderToStaticMarkup(createElement(PanelAppRow, { by: "cpu", step: "idle", t, onName() {}, onQuit() {}, onForce() {}, ...p }));
}

test("rows: quit in two clicks, force quit in two more; system rows are estimates without buttons", async () => {
  const app = row({ key: "app:/applications/lark.app", name: "Lark", path: "/Applications/Lark.app", quit: true, cpu: 21.4 });
  const idle = await rowHtml({ row: app });
  expect(idle).toContain('class="mb-quit"');
  expect(idle).toContain(t("status.apps.quit"));
  expect(idle).toContain("21%");
  expect(await rowHtml({ row: app, step: "confirm" })).toContain(t("menubar.panel.confirmQuit"));
  const stuck = await rowHtml({ row: app, step: "stuck" });
  expect(stuck).toContain(t("process.stuck"));
  expect(stuck).toContain(t("process.force"));
  expect(await rowHtml({ row: app, step: "forceConfirm" })).toContain(t("menubar.panel.confirmForce"));
  const done = await rowHtml({ row: app, step: "done" });
  expect(done).toContain('class="mb-app done"');
  expect(done).toContain(t("menubar.panel.quitted"));
  expect(done).not.toContain("mb-quit");
  const sys = await rowHtml({ row: ws, by: "mem" });
  expect(sys).toContain(t("process.system"));
  expect(sys).toContain("≈ 133 MB");
  expect(sys).not.toContain("mb-quit");
});

test("panel strings exist in Chinese and English", () => {
  const keys = [
    "menubar.panel.cpu", "menubar.panel.battery", "menubar.panel.every", "menubar.panel.more", "menubar.panel.openProcess", "menubar.panel.openHome",
    "menubar.panel.confirmQuit", "menubar.panel.confirmForce", "menubar.panel.quitted", "menubar.panel.swapValue",
    "menubar.diag.cpuIdle", "menubar.diag.memOk", "menubar.diag.diskOk", "menubar.diag.growth", "menubar.diag.thermal3",
    "menubar.diag.cpuTop", "menubar.diag.gpuSome",
  ] as const;
  try {
    for (const lang of ["zh", "en"]) {
      setLang(lang);
      for (const k of keys) expect(t(k)).not.toBe(k);
    }
  } finally { setLang("zh"); }
});

test("the panel's colors for each theme are style.css's light and dark tokens", async () => {
  const { readFileSync } = await import("node:fs");
  const style = readFileSync(new URL("../src/style.css", import.meta.url), "utf8");
  const panel = readFileSync(new URL("../src/panel/panel.css", import.meta.url), "utf8");
  const darkAt = style.indexOf("@media (prefers-color-scheme: dark)");
  const light = style.slice(style.indexOf(":root {"), darkAt);
  const dark = style.slice(darkAt, style.indexOf("\n}\n", darkAt));
  const token = (block: string, name: string) => block.match(new RegExp(`--${name}:\\s*([^;]+);`))?.[1]?.trim();
  const pairs = [...panel.matchAll(/--([a-z-]+): light-dark\((.*)\);/g)];
  expect(pairs.length).toBe(18);
  for (const [, name, args] of pairs) {
    let depth = 0;
    let cut = -1;
    for (let i = 0; i < args!.length && cut < 0; i++) {
      const c = args![i];
      if (c === "(") depth++;
      else if (c === ")") depth--;
      else if (c === "," && depth === 0) cut = i;
    }
    expect([name, args!.slice(0, cut).trim()]).toEqual([name, token(light, name!)]);
    expect([name, args!.slice(cut + 1).trim()]).toEqual([name, token(dark, name!)]);
  }
});

test("footer: the panel's links, the update interval and the 「⋯」 that opens Cleanzo's menu", async () => {
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { PanelFoot } = await import("../src/panel/PanelApp");
  const { PANEL } = await import("../src/panel/format");
  const html = renderToStaticMarkup(createElement(PanelFoot, { links: PANEL.cpu.links, t, onOpen() {}, onMenu() {} }));
  expect(html).toContain(t("menubar.panel.openProcess"));
  expect(html).toContain(t("menubar.panel.every"));
  expect(html).toContain('class="mb-more"');
  expect(html).toContain(`aria-label="${t("menubar.panel.more")}"`);
});
