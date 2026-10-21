import { expect, test } from "bun:test";
import { t } from "../src/i18n";
import type { AppUsage, StatusSample } from "../src/mygo";
import {
  SPARK_POINTS, STATUS_ALERT, emptyQuit, fmtMem, fmtPct, iconColor,
  pickAlerts, quitErrCode, refreshFrozenRows, sortApps, splitMem, syncQuitRows,
  trimSpark, uptimeParts,
} from "../src/react/status/format";

// i18n 解析读 localStorage（try/catch 兜底），这里显式钉住语言偏好。
const storage = new Map([["cleanzo.lang", "zh"]]);
Object.assign(globalThis, {
  localStorage: {
    getItem: (k: string) => storage.get(k),
    setItem: (k: string, v: string) => storage.set(k, v),
  },
});

const GB = 2 ** 30;
const MB = 2 ** 20;

const chrome = (over: Partial<AppUsage> = {}): AppUsage => ({
  key: "chrome", name: "Google Chrome", path: "/Applications/Google Chrome.app",
  memory: 20.5 * GB, cpu: 11.3, procs: 62, quit: true, ...over,
});
const code = (): AppUsage => ({
  key: "code", name: "Code", path: "/Applications/Visual Studio Code.app",
  memory: 4.2 * GB, cpu: 48.6, procs: 18, quit: true,
});
const finder = (): AppUsage => ({
  key: "finder", name: "Finder", path: "",
  memory: 3.1 * GB, cpu: 2.2, procs: 1, quit: false,
});
const slack = (mem: number): AppUsage => ({
  key: "slack", name: "Slack", path: "/Applications/Slack.app",
  memory: mem, cpu: 1.1, procs: 9, quit: true,
});

const sample = (over: Partial<StatusSample> = {}): StatusSample => ({
  memory: {
    total: 32 * GB, used: 27.2 * GB, app: 11.9 * GB, wired: 3.8 * GB,
    compressed: 11.5 * GB, cached: 4.1 * GB, pressure: 2,
    swapUsed: 21.5 * GB, swapTotal: 22 * GB,
  },
  cpu: { usage: 23.4, load1: 8.3, load5: 5.3, load15: 4.9 },
  disk: { total: 460 * GB, used: 369 * GB, free: 91 * GB },
  battery: { percent: 100, charging: false, full: true, plugged: true, cycles: 170, maxCapacity: 83, condition: "Normal" },
  apps: [chrome(), code(), finder()],
  procs: 740,
  ...over,
});

test("STATUS_ALERT thresholds match the spec constants", () => {
  expect(STATUS_ALERT.swapMentionGB).toBe(1);
  expect(STATUS_ALERT.swapStandaloneGB).toBe(8);
  expect(STATUS_ALERT.diskFreeWarnGB).toBe(20);
  expect(STATUS_ALERT.pressure[2]).toBe("warn");
  expect(STATUS_ALERT.pressure[4]).toBe("crit");
});

test("fmtMem strips .0 on whole GiB, MB below 1 GiB, dash for zero", () => {
  expect(fmtMem(0)).toBe("—");
  expect(fmtMem(22 * GB)).toBe("22 GB");
  expect(fmtMem(27.2 * GB)).toBe("27.2 GB");
  expect(fmtMem(512 * MB)).toBe("512 MB");
  expect(splitMem(27.2 * GB)).toEqual(["27.2", "GB"]);
  expect(splitMem(0)).toEqual(["—", ""]);
  expect(fmtPct(-5)).toBe("0%");
  expect(fmtPct(48.6)).toBe("49%");
});

test("uptimeParts: under a minute hidden, then days/hours/minutes buckets", () => {
  const now = 1_000_000;
  expect(uptimeParts(now - 30, now)).toBeNull();
  expect(uptimeParts(now - (4 * 86400 + 10 * 3600 + 5 * 60), now)).toEqual({ days: 4, hours: 10, mins: 5 });
  expect(uptimeParts(now - (2 * 3600 + 5), now)).toEqual({ days: 0, hours: 2, mins: 0 });
});

test("alerts: pressure warn + swap line, critical, swap-only, disk low; at most two, memory on top", () => {
  // 压力 2 + 交换 ≥1GB：memWarn 带 swapLine，按钮 apps
  let a = pickAlerts(sample());
  expect(a.map((x) => x.kind)).toEqual(["memWarn"]);
  expect(a[0]!.swapDisk).toBe(22 * GB);
  expect(a[0]!.action).toBe("apps");

  // swap <1GB：memWarn 不带副行
  a = pickAlerts(sample({ memory: { ...sample().memory, swapUsed: 0, swapTotal: 0 } }));
  expect(a[0]!.swapDisk).toBeUndefined();

  // 压力 4：memCrit
  a = pickAlerts(sample({ memory: { ...sample().memory, pressure: 4 } }));
  expect(a[0]!.kind).toBe("memCrit");
  expect(a[0]!.action).toBe("apps");

  // 压力正常 + swap ≥8GB：swapOnly，无按钮
  a = pickAlerts(sample({
    memory: { ...sample().memory, pressure: 1, swapUsed: 8.5 * GB, swapTotal: 9 * GB },
    disk: { total: 460 * GB, used: 100 * GB, free: 360 * GB },
  }));
  expect(a.map((x) => x.kind)).toEqual(["swapOnly"]);
  expect(a[0]!.swapDisk).toBe(9 * GB);
  expect(a[0]!.action).toBeUndefined();

  // 磁盘 <20GB：diskLow + 去清理；与内存条同屏时内存在前，最多 2 条
  a = pickAlerts(sample({ disk: { total: 100 * GB, used: 85 * GB, free: 15 * GB } }));
  expect(a.map((x) => x.kind)).toEqual(["memWarn", "diskLow"]);
  expect(a[1]!.free).toBe(15 * GB);
  expect(a[1]!.action).toBe("clean");

  // 压力正常、磁盘充足：无提示条
  a = pickAlerts(sample({
    memory: { ...sample().memory, pressure: 1, swapUsed: 0, swapTotal: 0 },
    disk: { total: 460 * GB, used: 100 * GB, free: 360 * GB },
  }));
  expect(a).toEqual([]);
});

test("sortApps sorts by memory or CPU desc without mutating the sample", () => {
  const s = sample();
  expect(sortApps(s.apps, "mem").map((a) => a.key)).toEqual(["chrome", "code", "finder"]);
  expect(sortApps(s.apps, "cpu").map((a) => a.key)).toEqual(["code", "chrome", "finder"]);
  // 原采样不被改写
  expect(s.apps.map((a) => a.key)).toEqual(["chrome", "code", "finder"]);
  expect(sortApps([], "mem")).toEqual([]);
});

test("hover freeze keeps the row set and order while numbers refresh by key", () => {
  const s1 = sample({ apps: [chrome(), slack(6 * GB)] });
  const frozen = sortApps(s1.apps, "mem"); // chrome, slack
  // 新采样里 Slack 反超；冻结期间行集合与顺序不变，数字按同 key 更新
  const s2 = sample({ apps: [slack(30 * GB), chrome()] });
  const rows = refreshFrozenRows(frozen, s2);
  expect(rows.map((r) => r.key)).toEqual(["chrome", "slack"]);
  expect(rows[1]!.memory).toBe(30 * GB);
  // 移出后重排：Slack 到第一
  expect(sortApps(s2.apps, "mem").map((r) => r.key)).toEqual(["slack", "chrome"]);
});

test("quit state machine: busy → done → gone on disappearance; same key reappearance restores the row", () => {
  // 退出成功：busy 清空，done 记录 key（行置灰、去按钮）
  let st = emptyQuit();
  st = { ...st, busy: new Set(["/Applications/Google Chrome.app"]) };
  expect(st.busy.has("/Applications/Google Chrome.app")).toBe(true);
  st = { busy: new Set(), done: new Set(["chrome"]), gone: new Set() };
  // 下轮采样 Chrome 已退出：present 里没有 chrome → 记 gone
  st = syncQuitRows(st, new Set(["code", "finder"]));
  expect(st.gone.has("chrome")).toBe(true);
  expect(st.done.has("chrome")).toBe(true);
  // 用户重启 Chrome：同 key 再现 = 新实例 → done/gone 清除，恢复行态与退出按钮
  st = syncQuitRows(st, new Set(["chrome", "code", "finder"]));
  expect(st.done.has("chrome")).toBe(false);
  expect(st.gone.has("chrome")).toBe(false);
});

test("syncQuitRows is a no-op when no quitted rows are tracked", () => {
  const st = emptyQuit();
  expect(syncQuitRows(st, new Set())).toBe(st);
});

test("quitErrCode parses structured errors; plain text passes through", () => {
  expect(quitErrCode(new Error(JSON.stringify({ code: "not-running" })))).toBe("not-running");
  expect(quitErrCode(new Error(JSON.stringify({ code: "cannot-quit" })))).toBe("cannot-quit");
  expect(quitErrCode(new Error("boom"))).toBeNull();
  expect(quitErrCode("weird")).toBeNull();
});

test("spark history caps at 30 points and drops the oldest", () => {
  let h: number[] = [];
  for (let i = 0; i < 35; i++) h = trimSpark(h, i);
  expect(h).toHaveLength(SPARK_POINTS);
  expect(h[0]).toBe(5); // 丢了前 5 个
  expect(h[h.length - 1]).toBe(34);
});

test("iconColor is stable per name and stays in the palette", () => {
  expect(iconColor("Google Chrome")).toBe(iconColor("Google Chrome"));
  expect(["#0c9d66", "#3b82f6", "#8b5cf6", "#f59e0b", "#ef4444", "#14b8a6", "#6366f1", "#ec4899"])
    .toContain(iconColor("Google Chrome"));
});

test("English interface renders the paired status strings", () => {
  storage.set("cleanzo.lang", "en");
  expect(t("status.mem.appMem")).toBe("App Memory");
  expect(t("status.mem.wired")).toBe("Wired Memory");
  expect(t("status.apps.procs", { n: 62 })).toBe("62 processes");
  expect(t("status.mem.swap", { used: "21.5 GB", size: "22 GB" })).toBe("Swap in use 21.5 GB · 22 GB of disk");
  expect(t("status.alert.memWarn")).toContain("Memory is under pressure");
  expect(t("status.footnote", { n: 740 })).toContain("Counting 740 processes");
  storage.set("cleanzo.lang", "zh");
  expect(t("status.mem.appMem")).toBe("App 内存");
});


test("Windows memory card hides zero compressed memory and uses page-file terms", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { MemoryCard } = await import("../src/react/status/StatusPage");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref } = await import("../src/i18n");
  setPref("en");
  const m = { ...sample().memory, compressed: 0 };
  try {
    setPlatformForTest("win32");
    const html = renderToStaticMarkup(createElement(MemoryCard, { m, t }));
    expect(html).toContain("Kernel");
    expect(html).toContain("Page file");
    expect(html).not.toContain("seg-compressed");
    expect(t("status.osVersion", { version: "Windows 11 24H2 (26100)" })).toBe("Windows 11 24H2 (26100)");
    setPlatformForTest("darwin");
    const mac = renderToStaticMarkup(createElement(MemoryCard, { m, t }));
    expect(mac).toContain("Compressed");
    expect(mac).toContain("seg-compressed");
    expect(t("status.osVersion", { version: "26" })).toBe("macOS 26");
  } finally { setPlatformForTest(undefined); setPref("system"); }
});

test("Windows CPU hides unsupported load tiles and memory uses the short Kernel label", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { CpuCard, MemoryCard } = await import("../src/react/status/StatusPage");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref } = await import("../src/i18n");
  try {
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      setPlatformForTest("win32");
      const props = { cpu: sample().cpu, cores: 8, hist: [10], t };
      const win = renderToStaticMarkup(createElement(CpuCard, props));
      expect(win).not.toContain("mloads");
      expect(win).not.toContain(t("status.cpu.load1"));
      expect(t("status.mem.wired")).toBe(lang === "zh" ? "内核" : "Kernel");
      expect(renderToStaticMarkup(createElement(MemoryCard, { m: sample().memory, t }))).toContain(t("status.mem.wired"));
      setPlatformForTest("darwin");
      const mac = renderToStaticMarkup(createElement(CpuCard, props));
      expect(mac).toContain("mloads");
      for (const key of ["status.cpu.load1", "status.cpu.load5", "status.cpu.load15"] as const) expect(mac).toContain(t(key));
    }
  } finally { setPlatformForTest(undefined); setPref("system"); }
});

test("Windows memory legend keeps three full label/value rows in zh/en", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { MemoryCard } = await import("../src/react/status/StatusPage");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref } = await import("../src/i18n");
  try {
    setPlatformForTest("win32");
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      const m = { ...sample().memory, compressed: 0, cached: 19.9 * GB };
      const html = renderToStaticMarkup(createElement(MemoryCard, { m, t }));
      // A Windows-only layout gives each label and value the full card width.
      const match = html.match(/<ul class="mlegend num mlegend-win">(.*?)<\/ul>/);
      expect(match).not.toBeNull();
      const legend = match![1]!;
      const rows = [...legend.matchAll(/<li>(.*?)<\/li>/g)].map((match) => match[1]!);
      expect(rows).toHaveLength(3);
      const labels = lang === "zh" ? ["App 内存", "内核", "已缓存文件"] : ["App Memory", "Kernel", "Cached files"];
      rows.forEach((row, i) => expect(row).toContain(`<span>${labels[i]}</span><b>`));
      expect(rows[2]).toContain("<b>19.9 GB</b>");
      expect(legend).not.toMatch(/…|\.\.\./);
    }
  } finally { setPlatformForTest(undefined); setPref("system"); }
});
