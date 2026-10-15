import { beforeAll, expect, mock, test } from "bun:test";

// 与 junk.test.ts 同一套无浏览器事件夹具：只验证文案、阈值、
// 分段切换、退出四结果与通道生命周期；布局由真机浏览器验收。

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
  setAttribute(_k: string, _v: string) {}
  async fire(type: string, target: Element = this) {
    for (const fn of this.listeners.get(type) ?? []) await fn({ target, preventDefault() {}, stopPropagation() {} });
    await settle();
  }
}

const nodes = new Map<string, Element>();
const node = (s: string) => {
  if (!nodes.has(s)) nodes.set(s, new Element(s.replace(/^#/, "")));
  return nodes.get(s)!;
};
const storage = new Map([["cleanzo.lang", "zh"]]);
Object.assign(globalThis, {
  HTMLElement: Element, HTMLInputElement: Element, HTMLButtonElement: Element,
  document: {
    querySelector: node, querySelectorAll: () => [], addEventListener() {}, hidden: false,
    createElement: () => new Element(), body: { appendChild() {} },
  },
  localStorage: { getItem: (key: string) => storage.get(key), setItem: (key: string, v: string) => storage.set(key, v) },
  window: { innerWidth: 1200, setTimeout: () => 0 },
});
async function settle() { await Bun.sleep(0); }

class TestChannel {
  values: unknown[] = [];
  finished = false;
  wake: (() => void) | null = null;
  send(v: unknown) { this.values.push(v); this.wake?.(); }
  close() { this.finished = true; this.wake?.(); }
  async *[Symbol.asyncIterator]() {
    while (!this.finished || this.values.length) {
      if (this.values.length) yield this.values.shift();
      else await new Promise<void>((resolve) => { this.wake = resolve; });
    }
  }
}

const GB = 2 ** 30;
const MB = 2 ** 20;

let watchCh: TestChannel;
let finishWatch: () => void;
let rejectWatch: (e: Error) => void;
let quitResult: { ok?: boolean; err?: Error } = {};
let quitCalls: string[] = [];
let goCleanCalled = 0;
/** 注入 Cleaner.info() 失败（QA P2-2 回归） */
let infoFail = false;

const sysInfo = {
  version: "0.1.7", os: "darwin", arch: "arm64", goVersion: "go1.27",
  chip: "Apple M2 Pro", memory: 32 * GB, cores: 12, osVersion: "26.6.2",
  bootTime: Math.floor(Date.now() / 1000) - (4 * 86400 + 10 * 3600),
};

mock.module("mygo-runtime", () => ({ Channel: TestChannel }));
mock.module("../src/mygo", () => ({ Cleaner: {
  info: async () => {
    if (infoFail) throw Error("info failed");
    return sysInfo;
  },
  watchStatus: (ch: TestChannel) => {
    watchCh = ch;
    return new Promise<void>((resolve, reject) => {
      finishWatch = () => { ch.close(); resolve(); };
      rejectWatch = (e) => { ch.close(); reject(e); };
    });
  },
  quitApp: async (path: string) => {
    quitCalls.push(path);
    if (quitResult.err) throw quitResult.err;
    return quitResult.ok ?? false;
  },
} }));

const sample = (over: Record<string, unknown> = {}) => ({
  memory: {
    total: 32 * GB, used: 27.2 * GB, app: 11.9 * GB, wired: 3.8 * GB,
    compressed: 11.5 * GB, cached: 4.1 * GB, pressure: 2,
    swapUsed: 21.5 * GB, swapTotal: 22 * GB,
  },
  cpu: { usage: 23.4, load1: 8.3, load5: 5.3, load15: 4.9 },
  disk: { total: 460 * GB, used: 369 * GB, free: 91 * GB },
  battery: { percent: 100, charging: false, full: true, plugged: true, cycles: 170, maxCapacity: 83, condition: "Normal" },
  apps: [
    { key: "chrome", name: "Google Chrome", path: "/Applications/Google Chrome.app", memory: 20.5 * GB, cpu: 11.3, procs: 62, quit: true },
    { key: "code", name: "Code", path: "/Applications/Visual Studio Code.app", memory: 4.2 * GB, cpu: 48.6, procs: 18, quit: true },
    { key: "finder", name: "Finder", path: "", memory: 3.1 * GB, cpu: 2.2, procs: 1, quit: false },
  ],
  procs: 740,
  ...over,
});

let status: typeof import("../src/status");
beforeAll(async () => {
  // 真实 DOM 里 #overlay 初始带 hidden；夹具里也要有，否则列表永远冻结
  node("#overlay").classList.add("hidden");
  status = await import("../src/status");
  status.initStatus();
  status.setGoCleanView(() => { goCleanCalled++; });
});

const open = async (s?: ReturnType<typeof sample>) => {
  status.startStatus();
  await settle();
  if (s) { watchCh.send(s); await settle(); }
};
const send = async (s: ReturnType<typeof sample>) => { watchCh.send(s); await settle(); };
const rowOrder = () => [...node("#st-list").innerHTML.matchAll(/data-key="([^"]+)"/g)].map((m) => m[1]);
const hover = async () => { await node("#st-list").fire("mouseenter"); };
const unhover = async () => { await node("#st-list").fire("mouseleave"); };

test("QA P2-2: Info() 失败时 CPU 卡核数显示「—」而不是「0 核」", async () => {
  // 必须是全文件第一个跑：status.ts 模块级缓存 sysInfo，首次成功后就不再请求
  infoFail = true;
  status.startStatus();
  await settle();
  watchCh.send(sample());
  await settle();
  const cards = node("#st-cards").innerHTML;
  expect(cards).not.toContain("0 核");
  expect(cards).toContain("—");
  status.stopStatus();
  // Info 重试成功后核数正常显示
  infoFail = false;
  await open(sample());
  expect(node("#st-cards").innerHTML).toContain("12 核");
  status.stopStatus();
  await settle();
});

test("skeleton first, machine line from Info(), then real numbers replace it", async () => {
  status.startStatus();
  await settle();
  // 骨架态：无 0 / 假值，机器行先显
  expect(node("#st-cards").innerHTML).toContain("sk-line");
  expect(node("#st-cards").innerHTML).not.toContain("27.2");
  expect(node("#st-machine").textContent).toBe("Apple M2 Pro · 32 GB 内存 · macOS 26.6.2 · 已运行 4 天 10 小时");
  watchCh.send(sample());
  await settle();
  expect(node("#st-cards").innerHTML).not.toContain("sk-line");
  expect(node("#st-cards").innerHTML).toContain("27.2");
  expect(node("#st-footnote").textContent).toContain("740");
  expect(node("#st-list").innerHTML).toContain("62 个进程");
  status.stopStatus();
  await settle();
});

test("memory card: three segments, legend, swap subline over 0.1 GB, CompressorPages-based used", async () => {
  await open(sample({ memory: { ...sample().memory, swapUsed: 0, swapTotal: 0 } }));
  const html = node("#st-cards").innerHTML;
  // 0.1.8：图例改为 <li><span>标签</span><b>数值</b></li>，标签与数值间无空格，分别断言
  expect(html).toMatch(/App 内存<\/span><b[^>]*>11\.9 GB/);
  expect(html).toMatch(/联动内存<\/span><b[^>]*>3\.8 GB/);
  expect(html).toMatch(/被压缩<\/span><b[^>]*>11\.5 GB/);
  expect(html).toMatch(/已缓存文件<\/span><b[^>]*>4\.1 GB/);
  expect(html).not.toContain("交换文件");
  status.stopStatus();
  await open(sample({ memory: { ...sample().memory, swapUsed: 100 * MB } }));
  // 交换区 <0.1GB 不显示
  expect(node("#st-cards").innerHTML).not.toContain("交换文件");
  status.stopStatus();
  await open(sample());
  expect(node("#st-cards").innerHTML).toContain("交换文件 已用 21.5 GB · 占磁盘 22 GB");
  status.stopStatus();
  await settle();
});

test("no battery: the card is absent and everything else renders", async () => {
  await open(sample({ battery: null }));
  expect(node("#st-cards").innerHTML).not.toContain("电池");
  expect(node("#st-cards").innerHTML).toContain("内存");
  status.stopStatus();
  await settle();
});

test("alerts: pressure warn + swap line, critical, swap-only, disk low; at most two, memory on top", async () => {
  // 压力 2 + 交换 ≥1GB：memWarn + swapLine + 查看占内存的应用
  await open(sample({ disk: { total: 460 * GB, used: 100 * GB, free: 360 * GB } }));
  const a1 = node("#st-alerts").innerHTML;
  expect(a1).toContain("内存紧张");
  expect(a1).toContain("交换文件占了 22 GB 磁盘");
  expect(a1).toContain('data-alert="apps"');
  status.stopStatus();

  // 压力 4：红色条
  await open(sample({ memory: { ...sample().memory, pressure: 4 } }));
  expect(node("#st-alerts").innerHTML).toContain("内存严重不足");
  expect(node("#st-alerts").innerHTML).toContain("st-callout crit");
  status.stopStatus();

  // 压力正常 + swap ≥8GB：swapOnly，无按钮
  await open(sample({
    memory: { ...sample().memory, pressure: 1, swapUsed: 8.5 * GB, swapTotal: 9 * GB },
    disk: { total: 460 * GB, used: 100 * GB, free: 360 * GB },
  }));
  const a3 = node("#st-alerts").innerHTML;
  expect(a3).toContain("系统会陆续删掉用不到的交换文件");
  expect(a3).not.toContain('data-alert="apps"');
  expect(a3).not.toContain("内存紧张");
  status.stopStatus();

  // 磁盘 <20GB：diskLow + 去清理；与内存条同屏时内存在上，且最多 2 条
  await open(sample({ disk: { total: 100 * GB, used: 85 * GB, free: 15 * GB } }));
  const a4 = node("#st-alerts").innerHTML;
  expect(a4).toContain("磁盘只剩 15 GB 可用");
  expect(a4).toContain('data-alert="clean"');
  expect((a4.match(/st-callout /g) ?? []).length).toBe(2);
  expect(a4.indexOf("内存紧张")).toBeLessThan(a4.indexOf("磁盘只剩"));
  status.stopStatus();

  // 压力正常、磁盘充足：无提示条
  await open(sample({
    memory: { ...sample().memory, pressure: 1, swapUsed: 0, swapTotal: 0 },
    disk: { total: 460 * GB, used: 100 * GB, free: 360 * GB },
  }));
  expect(node("#st-alerts").innerHTML).toBe("");
  status.stopStatus();
  await settle();
});

test("segment control switches sort and remembers within the session", async () => {
  await open(sample());
  expect(rowOrder()).toEqual(["chrome", "code", "finder"]); // 默认按内存
  await node("#st-seg-cpu").fire("click");
  expect(rowOrder()).toEqual(["code", "chrome", "finder"]); // 按 CPU：Code 48.6% 在前
  // 查看占内存的应用：强制切回按内存
  await node("#st-alerts").fire("click", Object.assign(new Element(), { dataset: { alert: "apps" } }));
  expect(rowOrder()).toEqual(["chrome", "code", "finder"]);
  expect(node("#st-seg-mem").classes.has("on")).toBe(true);
  status.stopStatus();
  // 会话内记忆：重开采样后仍按内存
  await open(sample());
  expect(rowOrder()).toEqual(["chrome", "code", "finder"]);
  status.stopStatus();
  await settle();
});

test("quit=false rows (Finder) never render a quit button", async () => {
  await open(sample());
  const html = node("#st-list").innerHTML;
  expect(html).toContain('data-quit="/Applications/Google Chrome.app"');
  expect(html).not.toContain('data-quit=""');
  expect(html.match(/data-quit=/g)?.length).toBe(2); // 只剩 Chrome 和 Code
  status.stopStatus();
  await settle();
});

test("quit flow: confirm dialog, four outcomes map to the right toast and row state", async () => {
  quitCalls = [];
  // ① 全部退出
  await open(sample());
  await node("#st-list").fire("click", Object.assign(new Element(), { dataset: { key: "chrome", quit: "/Applications/Google Chrome.app" } }));
  expect(node("#modal-title").textContent).toBe("退出「Google Chrome」？");
  expect(node("#modal-body").textContent).toContain("和在 Dock 里选「退出」一样");
  quitResult = { ok: true };
  await node("#modal-ok").fire("click");
  expect(quitCalls).toEqual(["/Applications/Google Chrome.app"]);
  expect(node("#toast").textContent).toBe("已退出「Google Chrome」");
  // 已退出行置灰、无按钮
  expect(node("#st-list").innerHTML).toContain("quitted");
  expect(node("#st-list").innerHTML).not.toContain('data-quit="/Applications/Google Chrome.app"');
  status.stopStatus();

  // ② not-running
  await open(sample());
  quitResult = { err: Error(JSON.stringify({ code: "not-running" })) };
  await node("#st-list").fire("click", Object.assign(new Element(), { dataset: { key: "chrome", quit: "/Applications/Google Chrome.app" } }));
  await node("#modal-ok").fire("click");
  expect(node("#toast").textContent).toBe("「Google Chrome」已经退出了");
  status.stopStatus();

  // ③ 5s 超时仍在：按钮恢复「退出」
  await open(sample());
  quitResult = { ok: false };
  await node("#st-list").fire("click", Object.assign(new Element(), { dataset: { key: "chrome", quit: "/Applications/Google Chrome.app" } }));
  await node("#modal-ok").fire("click");
  expect(node("#toast").textContent).toContain("还没有退出");
  expect(node("#st-list").innerHTML).toContain('data-quit="/Applications/Google Chrome.app"');
  status.stopStatus();

  // ④ cannot-quit
  await open(sample());
  quitResult = { err: Error(JSON.stringify({ code: "cannot-quit" })) };
  await node("#st-list").fire("click", Object.assign(new Element(), { dataset: { key: "chrome", quit: "/Applications/Google Chrome.app" } }));
  await node("#modal-ok").fire("click");
  expect(node("#toast").textContent).toBe("没能退出「Google Chrome」");
  status.stopStatus();
  quitResult = {};
  await settle();
});

test("QA P2-1: 退出的应用消失后重启（同 key 再现）恢复行态与退出按钮", async () => {
  await open(sample());
  await node("#st-list").fire("click", Object.assign(new Element(), { dataset: { key: "chrome", quit: "/Applications/Google Chrome.app" } }));
  quitResult = { ok: true };
  await node("#modal-ok").fire("click");
  // 退出成功：行置灰、无按钮（同视图会话内）
  expect(node("#st-list").innerHTML).toContain("quitted");
  expect(node("#st-list").innerHTML).not.toContain('data-quit="/Applications/Google Chrome.app"');
  // 下轮采样：Chrome 进程已退出，行消失
  await send(sample({ apps: [sample().apps[1]!, sample().apps[2]!] }));
  expect(rowOrder()).toEqual(["code", "finder"]);
  // 用户重启 Chrome：同 key 再现 = 新实例 → 不再置灰，退出按钮恢复
  await send(sample());
  const html = node("#st-list").innerHTML;
  expect(html).not.toContain("quitted");
  expect(html).toContain('data-quit="/Applications/Google Chrome.app"');
  status.stopStatus();
  quitResult = {};
  await settle();
});

test("hover freezes the row set and order while numbers keep updating", async () => {
  await open(sample({ apps: [
    sample().apps[0]!,
    { key: "slack", name: "Slack", path: "/Applications/Slack.app", memory: 6 * GB, cpu: 1.1, procs: 9, quit: true },
  ] }));
  await hover();
  // 新采样里 Slack 内存反超 Chrome；冻结期间行集合与顺序不变，只更新数字
  await send(sample({ apps: [
    { key: "slack", name: "Slack", path: "/Applications/Slack.app", memory: 30 * GB, cpu: 1.1, procs: 9, quit: true },
    sample().apps[0]!,
  ] }));
  expect(rowOrder()).toEqual(["chrome", "slack"]);
  expect(node("#st-list").innerHTML).toContain("30 GB");
  await unhover();
  await send(sample({ apps: [
    { key: "slack", name: "Slack", path: "/Applications/Slack.app", memory: 30 * GB, cpu: 1.1, procs: 9, quit: true },
    sample().apps[0]!,
  ] }));
  expect(rowOrder()).toEqual(["slack", "chrome"]); // 恢复重排
  status.stopStatus();
  await settle();
});

test("leaving the view or hiding the window closes the channel; coming back reopens it", async () => {
  await open(sample());
  const first = watchCh;
  status.stopStatus();
  expect(first.finished).toBe(true);
  // 窗口隐藏关通道，回来重开（清空 CPU 曲线）
  status.startStatus();
  await settle();
  await send(sample());
  await send(sample());
  expect(node("#st-cards").innerHTML).toContain("st-spark"); // 两点以上画曲线
  status.onVisibility(true);
  expect(watchCh.finished).toBe(true);
  status.onVisibility(false);
  await settle();
  const second = watchCh;
  expect(second).not.toBe(first);
  expect(second.finished).toBe(false);
  status.stopStatus();
  await settle();
});

test("restarting clears the CPU curve; 30 points are the cap", async () => {
  await open(sample());
  for (let i = 0; i < 35; i++) await send(sample());
  const html = node("#st-cards").innerHTML;
  // 曲线（折线路径）最多 30 个点，每个点一个逗号；面积路径还带两个收尾点
  const paths = [...html.matchAll(/d="M[^"]*"/g)].map((m) => m[0] ?? "");
  expect(paths.some((p) => (p.match(/,/g) ?? []).length === 30)).toBe(true);
  status.stopStatus();
  await open(sample()); // 重开：清空
  expect(node("#st-cards").innerHTML).not.toContain("st-spark"); // 只有 1 个点：不画
  status.stopStatus();
  await settle();
});

test("a channel error shows errorState with retry, and a fresh start recovers", async () => {
  status.startStatus();
  await settle();
  rejectWatch(Error("boom"));
  await settle();
  expect(node("#st-cards").innerHTML).toContain("读取系统状态失败");
  expect(node("#st-cards").innerHTML).toContain("重试");
  // 等价于点「重试」：重新建立通道，成功后回内容态
  status.startStatus();
  await settle();
  watchCh.send(sample());
  await settle();
  expect(node("#st-cards").innerHTML).toContain("27.2");
  status.stopStatus();
  await settle();
});

test("empty apps list shows the empty line, not zeros", async () => {
  await open(sample({ apps: [] }));
  expect(node("#st-list").innerHTML).toContain("没有可统计的应用进程");
  status.stopStatus();
  await settle();
});

test("English interface renders the paired strings", async () => {
  storage.set("cleanzo.lang", "en");
  await open(sample());
  const cards = node("#st-cards").innerHTML;
  expect(cards).toMatch(/App Memory<\/span><b[^>]*>11\.9 GB/);
  expect(cards).toMatch(/Wired Memory<\/span><b[^>]*>3\.8 GB/);
  expect(cards).toContain("Swap in use 21.5 GB · 22 GB of disk");
  expect(node("#st-list").innerHTML).toContain("62 processes");
  expect(node("#st-footnote").textContent).toContain("Counting 740 processes");
  expect(node("#st-alerts").innerHTML).toContain("Memory is under pressure");
  status.stopStatus();
  storage.set("cleanzo.lang", "zh");
  await settle();
});

test("STATUS_ALERT thresholds match the spec constants", async () => {
  expect(status.STATUS_ALERT.swapMentionGB).toBe(1);
  expect(status.STATUS_ALERT.swapStandaloneGB).toBe(8);
  expect(status.STATUS_ALERT.diskFreeWarnGB).toBe(20);
  expect(status.STATUS_ALERT.pressure[2]).toBe("warn");
  expect(status.STATUS_ALERT.pressure[4]).toBe("crit");
});
