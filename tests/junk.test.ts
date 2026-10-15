import { beforeAll, expect, mock, test } from "bun:test";

// 无浏览器的事件夹具，只检查文案、选择和 IPC；布局由浏览器验收。
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
    querySelector: node, querySelectorAll: () => [], addEventListener() {},
    createElement: () => new Element(), body: { appendChild() {} },
  },
  localStorage: { getItem: (key: string) => storage.get(key), setItem: (key: string, v: string) => storage.set(key, v) },
  window: { innerWidth: 1200, setTimeout: () => 0 },
});

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

let scanChannel: TestChannel;
let finishScan: () => void;
let rejectScan: (e: Error) => void;
let cleaned: unknown[] = [];
let cleanEvents: unknown[] = [];
let deferClean = false;
let cleanChannel: TestChannel;
let finishClean: () => void;
let rejectClean: (e: Error) => void;
let saveError = false;
let saved: unknown;
let revealed = "";
const settings = { disabledGroups: [], ignored: [], custom: [] };
mock.module("mygo-runtime", () => ({ Channel: TestChannel }));
mock.module("../src/mygo", () => ({ Cleaner: {
  scanJunk: (ch: TestChannel) => {
    scanChannel = ch;
    return new Promise<void>((resolve, reject) => {
      finishScan = () => { ch.close(); resolve(); };
      rejectScan = (e) => { ch.close(); reject(e); };
    });
  },
  cleanJunk: async (sel: unknown[], at: number, ch: TestChannel) => {
    expect(at).toBeGreaterThan(0);
    cleaned = sel;
    cleanEvents.forEach((e) => ch.send(e));
    if (deferClean) {
      cleanChannel = ch;
      return new Promise<void>((resolve, reject) => {
        finishClean = () => { ch.close(); resolve(); };
        rejectClean = (e) => { ch.close(); reject(e); };
      });
    }
    ch.close();
  },
  getSettings: async () => settings,
  saveSettings: async (s: unknown) => { if (saveError) throw Error("blocked"); saved = s; },
  revealInFinder: async (p: string) => { revealed = p; },
  openFullDiskAccessSettings: async () => {},
  // 兼容 status.test.ts 的 Cleaner 调用面（junk 不使用，但 mock.module
  // 注册表跨文件共享，缺这些方法会让 status 的 watchStatus/quitApp 变 undefined）
  info: async () => ({ chip: "", memory: 0, cores: 0, osVersion: "", bootTime: 0 }),
  watchStatus: (_ch: unknown) => new Promise<void>(() => {}),
  quitApp: async (_path: string) => true,
} }));

let junk: typeof import("../src/junk");
beforeAll(async () => { junk = await import("../src/junk"); junk.initJunk(); });
async function settle() { await Bun.sleep(0); }
const item = (id: string, group: string, size: number, extra = {}) => ({
  id, group, size, name: id, files: 1, default: true, trash: false, running: false,
  denied: false, estimate: false, inUse: 0, paths: [`~/cache/${id}`], top: [], subs: [], ...extra,
});
const summary = { fullDiskAccess: false, containers: 3, checked: 54, ignored: 2, leftovers: 2, leftoversSize: 2048 };
async function scan(items: ReturnType<typeof item>[], complete = true) {
  await node("#scan").fire("click");
  scanChannel.send({ kind: "start", items: items.map((it) => ({ ...it, size: -1 })) });
  await settle();
  if (!complete) return;
  items.forEach((it) => scanChannel.send({ kind: "item", item: it }));
  scanChannel.send({ kind: "done", summary });
  finishScan();
  await settle();
}
async function act(action: string, id = "", extra = {}, type = "click") {
  const target = new Element();
  Object.assign(target.dataset, { action, id }, extra);
  await node("#list").fire(type, target);
}

test("pending rows spin; measured zero rows disappear; running and denied rows never add to totals", async () => {
  const items = [item("dev.npx", "dev", 1024), item("dev.npm", "dev", 0),
    item("browser.chrome", "browsers", 8192, { running: true }),
    item("sys.trash", "system", 0, { denied: true }), item("review.ipsw", "review", 2048)];
  await scan(items, false);
  expect(node("#list").innerHTML).toContain("spin");
  items.forEach((it) => scanChannel.send({ kind: "item", item: it }));
  scanChannel.send({ kind: "done", summary }); finishScan(); await settle();
  expect(node("#total").textContent).toBe("1.0 KB");
  expect(node("#hero-sub").textContent).toContain("另有 2.0 KB 需要你确认");
  expect(node("#list").innerHTML).not.toContain('data-id="dev.npm"');
  expect(node("#list").innerHTML).toContain("退出「Chrome」后可清理");
  expect(node("#list").innerHTML).toContain("需要授权");
  expect(node("#list").innerHTML).toContain("3 个沙盒应用");
});

test("detail selection uses only default children and confirmation sends their paths", async () => {
  await scan([item("review.device-support", "review", 3072, { default: false, subs: [
    { path: "/old", name: "17.0", detail: "iOS", size: 1024, default: true },
    { path: "/new", name: "18.0", detail: "iOS", size: 2048, default: false },
  ] })]);
  const cb = new Element(); cb.dataset = { action: "item", id: "review.device-support" }; cb.checked = true;
  await node("#list").fire("change", cb);
  await act("expand", "review.device-support");
  expect(node("#list").innerHTML).toContain("保留最新");
  cleanEvents = [{ kind: "start", id: "review.device-support" }, { kind: "done", id: "review.device-support", bytes: 1024,
    result: { name: "review.device-support", freed: 1024, trashed: 0, inUse: 0, failed: [] } }];
  await node("#clean-all").fire("click");
  expect(node("#modal-body").innerHTML).toContain("1.0 KB");
  await node("#modal-ok").fire("click");
  expect(cleaned).toEqual([{ id: "review.device-support", paths: ["/old"] }]);
  expect(node("#total").textContent).toBe("1.0 KB");
});

test("unchecking the last child never falls back to cleaning the entire item", async () => {
  await scan([item("review.ipsw", "review", 1024, { subs: [
    { path: "/old", name: "old", detail: "", size: 1024, default: true },
  ] })]);
  const cb = new Element(); cb.dataset = { action: "sub", id: "review.ipsw", path: "/old" }; cb.checked = false;
  await node("#list").fire("change", cb);
  expect(node("#clean-all").disabled).toBe(true);
});

test("apps are truncated at 15 and show their remaining size; usage and estimate labels render", async () => {
  await scan(Array.from({ length: 17 }, (_, i) => item(`app:${i}`, "apps", (17 - i) * 1024, { estimate: true, inUse: 2 })));
  expect(node("#list").innerHTML).toContain("其余 2 个应用（共 3.0 KB）");
  expect(node("#list").innerHTML).toContain("2 项正在使用，已跳过");
  expect(node("#list").innerHTML).toContain("最多");
  await act("all-apps");
  expect(node("#list").innerHTML).toContain('data-id="app:16"');
});

test("ignore persists before hiding a row and rolls back on failure, without a stale-results bar", async () => {
  await scan([item("dev.npx", "dev", 1024)]);
  await act("menu", "dev.npx");
  await act("reveal", "dev.npx");
  expect(revealed).toBe("~/cache/dev.npx");
  saveError = true;
  await act("ignore", "dev.npx");
  expect(node("#list").innerHTML).toContain('data-id="dev.npx"');
  saveError = false;
  await act("ignore", "dev.npx");
  expect(saved).toEqual({ ...settings, ignored: ["dev.npx"] });
  expect(node("#list").innerHTML).not.toContain('data-id="dev.npx"');
  // 这一页自己忽略的项已经从结果里去掉，不用提示「设置已更改」
  expect(node("#list").innerHTML).not.toContain("设置已更改");
});

test("leftovers callback and English refresh use the current language", async () => {
  await scan([item("dev.npx", "dev", 1024)]);
  let opened = false;
  junk.setOpenLeftovers(() => { opened = true; });
  await act("leftovers"); expect(opened).toBe(true);
  storage.set("cleanzo.lang", "en"); junk.refreshJunkI18n();
  expect(node("#list").innerHTML).toContain("npx cache");
  expect(node("#list").innerHTML).toContain("Full Disk Access");
  expect(node("#list").innerHTML).not.toContain("rule.dev.npx");
  storage.set("cleanzo.lang", "zh"); junk.refreshJunkI18n();
});

test("a scan error presents retry instead of selectable partial results", async () => {
  await scan([item("dev.npx", "dev", 1024)], false);
  rejectScan(Error("blocked")); await settle();
  expect(node("#list").innerHTML).toContain("重试");
  expect(node("#clean-all").disabled).toBe(true);
});

test("stopping a scan retains measured rows and drops buffered and unmeasured rows", async () => {
  await scan([item("dev.npx", "dev", 1024), item("dev.npm", "dev", 2048)], false);
  scanChannel.send({ kind: "item", item: item("dev.npx", "dev", 1024) }); await settle();
  node("#scan").dataset.started = "0";
  await node("#scan").fire("click");
  scanChannel.send({ kind: "item", item: item("dev.npm", "dev", 2048) });
  finishScan(); await settle();
  expect(node("#list").innerHTML).toContain('data-id="dev.npx"');
  expect(node("#list").innerHTML).not.toContain('data-id="dev.npm"');
  expect(node("#list").innerHTML).toContain("已停止扫描");
});

test("group selection never includes a running app or a denied item", async () => {
  await scan([item("app:ready", "apps", 1024), item("app:busy", "apps", 8192, { running: true }),
    item("app:denied", "apps", 4096, { denied: true })]);
  const cb = new Element(); cb.dataset = { action: "group", group: "apps" }; cb.checked = true;
  await node("#list").fire("change", cb);
  cleanEvents = [{ kind: "done", id: "app:ready", result: { name: "app:ready", freed: 0, trashed: 0, inUse: 0, failed: [], skipped: "running" } }];
  await node("#clean-all").fire("click"); await node("#modal-ok").fire("click");
  expect(cleaned).toEqual([{ id: "app:ready" }]);
  expect(node("#list").innerHTML).toContain("运行中，已跳过");
});

test("tool failure shows both its skip reason and failed count", async () => {
  await scan([item("dev.go-mod", "dev", 1024)]);
  cleanEvents = [{ kind: "done", id: "dev.go-mod", result: { name: "dev.go-mod", freed: 0, trashed: 0, inUse: 0, failed: ["blocked"], skipped: "failed" } }];
  await node("#clean-all").fire("click"); await node("#modal-ok").fire("click");
  expect(node("#list").innerHTML).toContain("清理失败，已跳过");
  expect(node("#list").innerHTML).toContain("1 项失败");
});

test("shared confirmation clears pending on cancel and invalidates old async modal work", async () => {
  const shared = await import("../src/shared");
  let confirmed = 0;
  shared.setPending(async () => { confirmed++; }); shared.showModal();
  const seq = shared.currentModalSeq();
  await node("#modal-cancel").fire("click");
  expect(shared.currentModalSeq()).toBeGreaterThan(seq);
  await node("#modal-ok").fire("click");
  expect(confirmed).toBe(0);
});

test("all backend catalog rules have names and descriptions in both languages", async () => {
  const { readFileSync } = await import("node:fs");
  const { ruleName, ruleDesc } = await import("../src/i18n");
  const ids = [...readFileSync(new URL("../rules.go", import.meta.url), "utf8").matchAll(/ID: "([^"]+)"/g)].map((m) => m[1]!);
  for (const lang of ["zh", "en"]) {
    storage.set("cleanzo.lang", lang);
    for (const id of ids) { expect(ruleName(id)).not.toBe(id); expect(ruleDesc(id)).not.toBe(id); }
  }
  storage.set("cleanzo.lang", "zh");
});

test("v3 errors from ignoring an item are translated rather than showing raw JSON", async () => {
  const { errText } = await import("../src/shared");
  for (const code of ["folder-empty", "folder-protected", "folder-dup", "group-unknown"]) {
    const raw = JSON.stringify({ code, args: ["~/cache"] });
    expect(errText(Error(raw))).not.toBe(raw);
  }
});

test("explicitly selecting the latest version removes Keep Latest and sends only that path", async () => {
  await scan([item("review.device-support", "review", 3072, { default: false, subs: [
    { path: "/old", name: "17.0", detail: "iOS", size: 1024, default: true },
    { path: "/new", name: "18.0", detail: "iOS", size: 2048, default: false },
  ] })]);
  await act("expand", "review.device-support");
  const cb = new Element(); cb.dataset = { action: "sub", id: "review.device-support", path: "/new" }; cb.checked = true;
  await node("#list").fire("change", cb);
  expect(node("#list").innerHTML).not.toContain('<span class="tag dim">保留最新</span>');
  cleanEvents = [];
  await node("#clean-all").fire("click"); await node("#modal-ok").fire("click");
  expect(cleaned).toEqual([{ id: "review.device-support", paths: ["/new"] }]);
});

test("stopping cleanup retains completed results and marks the remaining rows", async () => {
  await scan([item("dev.npx", "dev", 1024), item("dev.npm", "dev", 2048)]);
  deferClean = true;
  cleanEvents = [{ kind: "start", id: "dev.npx" }, { kind: "done", id: "dev.npx", bytes: 1024,
    result: { name: "dev.npx", freed: 1024, trashed: 0, inUse: 0, failed: [] } }, { kind: "start", id: "dev.npm" }];
  await node("#clean-all").fire("click");
  // 确认回调仍在等 IPC，不阻塞停止按钮。
  const confirmation = node("#modal-ok").fire("click"); await settle();
  expect(node("#clean-all").textContent).toBe("停止清理");
  await node("#clean-all").fire("click");
  expect(cleanChannel.finished).toBe(true);
  finishClean(); await confirmation;
  expect(node("#total").textContent).toBe("1.0 KB");
  expect(node("#total-prefix").textContent).toBe("已停止清理");
  expect(node("#list").innerHTML).toContain("已释放 1.0 KB");
  expect(node("#hero-sub").textContent).toContain("其余的没有清理");
  deferClean = false;
});

test("rejected cleanup keeps the failure visible and permits a rescan", async () => {
  await scan([item("dev.npx", "dev", 1024)]);
  deferClean = true; cleanEvents = [];
  await node("#clean-all").fire("click");
  const confirmation = node("#modal-ok").fire("click"); await settle();
  rejectClean(Error("blocked")); await confirmation;
  expect(node("#hero-sub").textContent).toContain("清理失败");
  expect(node("#clean-all").textContent).toBe("重新扫描");
  expect(node("#scan").disabled).toBe(false);
  deferClean = false;
});
