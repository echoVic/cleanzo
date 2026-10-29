import { expect, test } from "bun:test";
import { t } from "../src/i18n";
import type { AppUsage, MachineSample, SSDInfo, StatusSample } from "../src/mygo";
import {
  NET_SPARK_FLOOR, SPARK_POINTS, STATUS_ALERT, displayText, emptyQuit, fmtDec, fmtMem, fmtPct, fmtRate, fmtRateText,
  fmtTemp, iconColor, modelLabel, netSparkMax, pickAlerts, quitErrCode, refreshFrozenRows, sortApps, splitMem,
  ssdWarnings, syncQuitRows, trimSpark, uptimeParts,
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
  battery: { percent: 100, charging: false, full: true, plugged: true, cycles: 170, maxCapacity: 83, condition: "Normal", temperature: 0, adapterWatts: 0 },
  apps: [chrome(), code(), finder()],
  procs: 740,
  machine: null,
  ...over,
});

// 维护者这台 Mac 2026-10-28 的真实读数（spike）。
const machineSample = (over: Partial<MachineSample> = {}): MachineSample => ({
  thermal: 0, chipTemp: 51.9, fanCount: 2, fans: [1333, 1464], power: 37.8,
  perfCpu: 31, effCpu: 58, gpu: 8, gpuMemory: 1.1 * GB,
  diskRead: 10_300_000, diskWrite: 9_600_000, diskTemp: 36,
  netDown: 120_000, netUp: 110_000, netKind: "wifi", displaySig: "1,2",
  ...over,
});
const ssd = (over: Partial<SSDInfo> = {}): SSDInfo => ({
  model: "APPLE SSD AP0512Z", capacity: 500_277_792_768, smart: true, warning: 0,
  tempC: 33, spare: 100, used: 26, written: 624.4e12,
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

test("syncQuitRows returns the same state when a gone key remains absent", () => {
  const st = { ...emptyQuit(), done: new Set(["chrome"]) };
  const first = syncQuitRows(st, new Set(["code"]));
  expect(first).not.toBe(st);
  expect(first.gone.has("chrome")).toBe(true);
  expect(syncQuitRows(first, new Set(["code"]))).toBe(first);
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

test("a new CPU sample that only moves the window along is a shift", async () => {
  const { isSparkShift, SPARK_POINTS, trimSpark } = await import("../src/react/status/format");
  const full = Array.from({ length: SPARK_POINTS }, (_, i) => i);
  expect(isSparkShift(full, trimSpark(full, 99))).toBe(true);
  expect(isSparkShift(full.slice(1), trimSpark(full.slice(1), 99))).toBe(false); // 还没满 30 点
  expect(isSparkShift(full, full)).toBe(false);
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
    // 条用 transform 定位和缩放，不用 width（width 过渡每帧重排整页）
    expect(mac).toContain("transform:translateX(0%) scaleX(");
    expect(mac).not.toMatch(/<i style="width:/);
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

test("Windows disk legend keeps full labels and 103.3 GB in full-width rows in zh/en", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { DiskCard } = await import("../src/react/status/StatusPage");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref } = await import("../src/i18n");
  try {
    setPlatformForTest("win32");
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      const d = { total: 460 * GB, used: 356.7 * GB, free: 103.3 * GB };
      const disk = renderToStaticMarkup(createElement(DiskCard, { d, t, onClean() {} }));
      expect(disk).toContain('<ul class="mlegend num mlegend-win">');
      expect(disk).toContain(`<span>${lang === "zh" ? "可用" : "available"}</span><b>`);
      expect(disk).toContain("<b>103.3 GB</b>");
    }
  } finally { setPlatformForTest(undefined); setPref("system"); }
});

test("the top-apps list is Windows-only, without View All; macOS lists no apps", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { StatusPage } = await import("../src/react/status/StatusPage");
  const { setPlatformForTest } = await import("../src/platform");
  const props = { active: false, goClean() {}, goProcess() {} };
  try {
    setPlatformForTest("darwin");
    const mac = renderToStaticMarkup(createElement(StatusPage, props));
    expect(mac).not.toContain("st-listcard");
    expect(mac).not.toContain(t("status.apps.title"));
    setPlatformForTest("win32");
    const win = renderToStaticMarkup(createElement(StatusPage, props));
    expect(win).toContain("st-listcard");
    expect(win).toContain(t("status.apps.title"));
    expect(win).not.toContain("st-viewall");
  } finally { setPlatformForTest(undefined); }
});

test("modelLabel splits the device-tree name and localizes the details", () => {
  expect(modelLabel("MacBook Pro (16-inch, 2023)", "Mac14,10", "zh")).toEqual({ name: "MacBook Pro", note: "16 英寸 · 2023" });
  expect(modelLabel("MacBook Pro (16-inch, 2023)", "Mac14,10", "en")).toEqual({ name: "MacBook Pro", note: "16-inch, 2023" });
  expect(modelLabel("MacBook Air (13-inch, M2, 2022)", "Mac14,2", "zh")).toEqual({ name: "MacBook Air", note: "13 英寸 · M2 · 2022" });
  expect(modelLabel("MacBook Pro (14-inch, M3, Nov 2023)", "Mac15,3", "zh").note).toBe("14 英寸 · M3 · 2023 年 11 月");
  expect(modelLabel("Mac Studio (2023)", "Mac14,13", "en")).toEqual({ name: "Mac Studio", note: "2023" });
  expect(modelLabel("Mac Pro", "Mac14,8", "zh")).toEqual({ name: "Mac Pro", note: "" });
  // 设备树没有型号全名：由型号标识推出系列名，标识写在旁边（设计 §三.1）
  expect(modelLabel("", "Mac14,10", "zh")).toEqual({ name: "Mac", note: "Mac14,10" });
  expect(modelLabel("", "MacBookAir10,1", "en")).toEqual({ name: "MacBook Air", note: "MacBookAir10,1" });
  expect(modelLabel(" ", "", "zh")).toEqual({ name: "", note: "" });
  // 「M3 Pro or M3 Max」这种段不进标题：芯片在规格行里
  expect(modelLabel("MacBook Pro (14-inch, M3 Pro or M3 Max, Nov 2023)", "Mac15,8", "zh").note).toBe("14 英寸 · 2023 年 11 月");
});

test("machine numbers format without NaN: rates, temperatures, drive sizes", () => {
  expect(fmtRate(-1)).toEqual(["—", ""]);
  expect(fmtRate(Number.NaN)).toEqual(["—", ""]);
  expect(fmtRate(0)).toEqual(["0", "KB/s"]);
  expect(fmtRate(1_500)).toEqual(["2", "KB/s"]);
  expect(fmtRate(1_230_000)).toEqual(["1.2", "MB/s"]);
  expect(fmtRate(2_500_000_000)).toEqual(["2.5", "GB/s"]);
  // 按取整后的值选单位：不会出现 "1000 KB/s" 或 "1000.0 MB/s"
  expect(fmtRate(999_600)).toEqual(["1.0", "MB/s"]);
  expect(fmtRate(999_960_000)).toEqual(["1.0", "GB/s"]);
  expect(fmtRateText(-1)).toBe("—");
  expect(fmtRateText(10_300_000)).toBe("10.3 MB/s");
  expect(fmtTemp(0)).toBe("—");
  expect(fmtTemp(51.9)).toBe("52 °C");
  expect(fmtDec(0)).toBe("—");
  expect(fmtDec(500_277_792_768)).toBe("500 GB");
  expect(fmtDec(64.5e9)).toBe("64.5 GB");
  expect(fmtDec(624.4e12)).toBe("624 TB");
  expect(fmtDec(1.5e12)).toBe("1.5 TB");
  expect(fmtDec(2e12)).toBe("2 TB");
});

test("ssdWarnings names each NVMe critical-warning bit", () => {
  expect(ssdWarnings(0)).toEqual([]);
  expect(ssdWarnings(0x01)).toEqual(["status.ssd.warn.spare"]);
  expect(ssdWarnings(0x0a)).toEqual(["status.ssd.warn.temp", "status.ssd.warn.readonly"]);
  expect(ssdWarnings(0x20)).toEqual(["status.ssd.warn.other"]);
});

test("displayText writes the name, size and refresh rate", () => {
  const d = { id: 2, name: "LS27D80xU", builtin: false, width: 1920, height: 1080, hz: 60 };
  expect(displayText(d, "外接显示器")).toBe("LS27D80xU 1920×1080 @60Hz");
  expect(displayText({ ...d, name: " ", builtin: true, width: 1728, height: 1117, hz: 120 }, "内建显示器")).toBe("内建显示器 1728×1117 @120Hz");
  expect(displayText({ ...d, hz: 0 }, "外接显示器")).toBe("LS27D80xU 1920×1080");
  expect(displayText({ ...d, width: 0, height: 0 }, "外接显示器")).toBe("LS27D80xU");
});

test("the network chart keeps a floor so idle traffic doesn't fill it", () => {
  expect(netSparkMax([])).toBe(NET_SPARK_FLOOR);
  expect(netSparkMax([5_000, 20_000])).toBe(NET_SPARK_FLOOR);
  expect(netSparkMax([500_000, 2_000_000])).toBe(2_000_000);
});

test("the health strip shows what it read and leaves out the rest", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { HealthStrip } = await import("../src/react/status/StatusPage");
  const { setPref } = await import("../src/i18n");
  const html = (m: MachineSample) => renderToStaticMarkup(createElement(HealthStrip, { m, t }));
  try {
    setPref("zh");
    const full = html(machineSample());
    for (const s of ["散热状态", "正常", "芯片温度", "52 °C", "风扇", "1333 · 1464", "rpm", "整机功耗", "38 W"]) expect(full).toContain(s);
    expect(full).toContain('class="v num ok"');
    const hot = html(machineSample({ thermal: 2 }));
    expect(hot).toContain("很热");
    expect(hot).toContain('class="v num crit"');
    const warm = html(machineSample({ thermal: 1 }));
    expect(warm).toContain("偏热");
    expect(warm).toContain('class="v num warn"');
    expect(html(machineSample({ thermal: 3 }))).toContain("过热");
    expect(html(machineSample({ thermal: 7 }))).toContain("过热"); // 超出四档按最高一档
    expect(full).toContain('title="1333 · 1464 rpm"');
    // MacBook Air 没有风扇：写「无风扇」，不写 0
    expect(html(machineSample({ fanCount: 0, fans: [] }))).toContain("无风扇");
    expect(html(machineSample({ fans: [0, 0] }))).toContain("未转动");
    // 读不到的整格省掉；什么都读不到时整条不出现
    const partial = html(machineSample({ chipTemp: 0, power: 0, fanCount: -1, fans: [] }));
    for (const s of ["芯片温度", "整机功耗", "风扇"]) expect(partial).not.toContain(s);
    expect(partial).not.toMatch(/NaN|undefined/);
    expect(html(machineSample({ thermal: -1, chipTemp: 0, power: 0, fanCount: -1, fans: [] }))).toBe("");
  } finally { setPref("system"); }
});

test("the macOS CPU card splits performance and efficiency cores", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { CpuCard } = await import("../src/react/status/StatusPage");
  const { setPref } = await import("../src/i18n");
  try {
    setPref("zh");
    const base = { cpu: sample().cpu, cores: 12, hist: [10], t };
    const split = renderToStaticMarkup(createElement(CpuCard, { ...base, m: machineSample(), split: { perf: 8, eff: 4 } }));
    for (const s of ["性能核（8）", "31%", "能效核（4）", "58%"]) expect(split).toContain(s);
    expect(split).toContain('class="mlegend num mlegend-rows"');
    // 核数还没读到：只写「性能核」「能效核」
    const counts = renderToStaticMarkup(createElement(CpuCard, { ...base, m: machineSample() }));
    expect(counts).toContain("性能核");
    expect(counts).not.toContain("性能核（");
    const unknown = renderToStaticMarkup(createElement(CpuCard, { ...base, m: machineSample({ perfCpu: -1, effCpu: -1 }) }));
    expect(unknown).not.toContain("性能核");
  } finally { setPref("system"); }
});

test("the GPU and network cards show live numbers, and dashes until they are known", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { GpuCard, NetCard } = await import("../src/react/status/StatusPage");
  const { setPref } = await import("../src/i18n");
  try {
    setPref("zh");
    const gpu = renderToStaticMarkup(createElement(GpuCard, { m: machineSample(), cores: 19, displays: 2, hist: [8, 9], t }));
    for (const s of ["GPU", "19 核", "占用的内存", "1.1 GB", "显示器", "2 台"]) expect(gpu).toContain(s);
    const gpuUnknown = renderToStaticMarkup(createElement(GpuCard, { m: machineSample({ gpu: -1, gpuMemory: 0 }), cores: 0, displays: 0, hist: [], t }));
    expect(gpuUnknown).toContain('<b class="ph">—</b>');
    expect(gpuUnknown).not.toMatch(/NaN|undefined|>-1/);
    // 读不到的行整行省掉
    expect(gpuUnknown).not.toContain("占用的内存");
    expect(gpuUnknown).not.toContain("mlegend");

    const net = renderToStaticMarkup(createElement(NetCard, { m: machineSample(), hist: [120_000, 90_000], t }));
    for (const s of ["网络", '<span class="mhead-note">Wi-Fi</span>', "120", "110", "KB/s", "只算 Wi-Fi 和以太网"]) expect(net).toContain(s);
    expect(net).toContain('<i aria-hidden="true">↓</i><span class="sr-only">下载</span>');
    expect(net).toContain('<span class="sr-only">上传</span>');
    expect(gpu).toContain('class="mlegend num mlegend-rows"');
    const wired = renderToStaticMarkup(createElement(NetCard, { m: machineSample({ netKind: "ethernet" }), hist: [], t }));
    expect(wired).toContain('<span class="mhead-note">以太网</span>');
    // 第一份采样还没有速度：写「—」，不写 -1，也不写连接方式
    const first = renderToStaticMarkup(createElement(NetCard, { m: machineSample({ netDown: -1, netUp: -1, netKind: "" }), hist: [], t }));
    expect(first).toContain("—");
    expect(first).not.toContain("mhead-note");
    expect(first).not.toMatch(/NaN|undefined|>-1/);
  } finally { setPref("system"); }
});

test("the macOS disk card adds the SSD and live read/write; without them it stays as before", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { DiskCard } = await import("../src/react/status/StatusPage");
  const { setPref } = await import("../src/i18n");
  const d = { total: 460 * GB, used: 424.7 * GB, free: 35.3 * GB };
  const html = (over: { ssd?: SSDInfo | null; live?: MachineSample | null }) =>
    renderToStaticMarkup(createElement(DiskCard, { d, t, onClean() {}, ...over }));
  try {
    setPref("zh");
    const mac = html({ ssd: ssd(), live: machineSample() });
    for (const s of ["APPLE SSD AP0512Z", "读取", "10.3 MB/s", "写入", "9.6 MB/s", "健康", "正常", "寿命已用", "26%", "已写入", "624 TB", "温度", "36 °C"]) {
      expect(mac).toContain(s);
    }
    expect(mac).not.toContain("需要留意");
    const worn = html({ ssd: ssd({ warning: 0x01 }), live: machineSample() });
    expect(worn).toContain("需要留意");
    expect(worn).toContain("备用块不足");
    // 没有 NAND 传感器时用 SMART 日志里的温度
    expect(html({ ssd: ssd(), live: machineSample({ diskTemp: 0 }) })).toContain("33 °C");
    // SMART 读不到、速度还没有：这些行都不出现
    const bare = html({ ssd: ssd({ smart: false }), live: machineSample({ diskRead: -1, diskWrite: -1, diskTemp: 0 }) });
    for (const s of ["健康", "寿命已用", "已写入", "读取", "写入", "温度"]) expect(bare).not.toContain(s);
    expect(bare).toContain("APPLE SSD AP0512Z");
    const plain = html({});
    for (const s of ["读取", "健康", "APPLE"]) expect(plain).not.toContain(s);
  } finally { setPref("system"); }
});

test("the battery card adds its temperature and the adapter's watts when known", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { BatteryCard } = await import("../src/react/status/StatusPage");
  const { setPref } = await import("../src/i18n");
  const b = { ...sample().battery!, temperature: 30.64, adapterWatts: 90 };
  try {
    setPref("zh");
    const html = renderToStaticMarkup(createElement(BatteryCard, { b, t }));
    for (const s of ["温度", "31 °C", "90 W"]) expect(html).toContain(s);
    expect(renderToStaticMarkup(createElement(BatteryCard, { b: { ...b, plugged: false, full: false }, t }))).not.toContain("90 W");
    const unknown = renderToStaticMarkup(createElement(BatteryCard, { b: { ...b, temperature: 0, adapterWatts: 0 }, t }));
    expect(unknown).not.toContain("°C");
    expect(unknown).not.toContain("90 W");
  } finally { setPref("system"); }
});

test("English machine strings", async () => {
  const { setPref } = await import("../src/i18n");
  try {
    setPref("en");
    expect(t("status.spec.cpuSplit", { n: 12, p: 8, e: 4 })).toBe("12-core CPU (8P + 4E)");
    expect(t("status.health.noFans")).toBe("No fans");
    expect(t("status.alert.viewInProcess")).toBe("See what's using memory");
    expect(t("status.ssd.warn.spare")).toBe("Low spare blocks");
  } finally { setPref("system"); }
});

test("macOS home numbers change in place; Windows keeps the rolling number", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { CpuCard } = await import("../src/react/status/StatusPage");
  const { setPlatformForTest } = await import("../src/platform");
  const props = { cpu: sample().cpu, cores: 12, hist: [10], t };
  try {
    setPlatformForTest("darwin");
    expect(renderToStaticMarkup(createElement(CpuCard, props))).toContain('<div class="mhero num"><b>23</b><i>%</i></div>');
    setPlatformForTest("win32");
    expect(renderToStaticMarkup(createElement(CpuCard, props))).toContain("<b>23</b>");
  } finally { setPlatformForTest(undefined); }
});
