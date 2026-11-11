import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

const source = () => readFileSync(new URL("./preload.js", import.meta.url), "utf8");

test("preload collects console errors, page errors and unhandled rejections without swallowing them", () => {
  const win = new EventTarget() as EventTarget & { __cleanzoSmoke: { errors: string[]; ready: boolean } };
  const logged: unknown[][] = [];
  const console = { error: (...args: unknown[]) => logged.push(args) };
  new Function("window", "document", "console", source())(win, {}, console);
  console.error("bad", new Error("stack trace"));
  const page = Object.assign(new Event("error"), { message: "page failed" });
  const rejected = Object.assign(new Event("unhandledrejection", { cancelable: true }), { reason: new Error("promise failed") });
  win.dispatchEvent(page);
  win.dispatchEvent(rejected);
  win.dispatchEvent(new Event("cleanzo:ready"));
  expect(win.__cleanzoSmoke.ready).toBe(true);
  expect(win.__cleanzoSmoke.errors).toHaveLength(3);
  expect(win.__cleanzoSmoke.errors.join("\n")).toContain("stack trace");
  expect(win.__cleanzoSmoke.errors.join("\n")).toContain("page failed");
  expect(win.__cleanzoSmoke.errors.join("\n")).toContain("promise failed");
  expect(logged).toHaveLength(1);
  expect(rejected.defaultPrevented).toBe(false);
});

test("clean tour uses scan controls in both phases and rejects a caught scan error", async () => {
  for (const failed of [false, true]) {
    const win = new EventTarget() as EventTarget & { __cleanzoSmoke: { visit(view: string): Promise<boolean> } };
    let idle = true;
    let scans = 0;
    let navigation = 0;
    const scan = { disabled: false, click() { idle = false; scans++; } };
    const nav = { click() { navigation++; }, classList: { contains: () => false } };
    const root = {
      classList: { contains: () => false },
      querySelectorAll() { return []; },
      querySelector(selector: string) {
        if (selector === ".cz-react") return {};
        if (selector === ".ha-cta") return idle ? scan : null;
        if (selector === '[data-action="scan"]') return scan;
        if (selector === ".empty.err") return failed ? { textContent: "Scan failed" } : null;
        throw Error(`unexpected selector: ${selector}`);
      },
    };
    const document = { querySelector: (selector: string) => selector.startsWith(".nav-item") ? nav : root };
    const immediate = (fn: () => void) => { queueMicrotask(fn); };
    new Function("window", "document", "console", "setTimeout", source())(win, document, { error() {} }, immediate);
    if (failed) {
      await expect(win.__cleanzoSmoke.visit("clean")).rejects.toThrow("scan failed");
    } else {
      expect(await win.__cleanzoSmoke.visit("clean")).toBe(true);
      expect(await win.__cleanzoSmoke.visit("clean")).toBe(true);
      expect(navigation).toBe(2);
      expect(scans).toBe(2);
    }
  }
});

test("AI space tour scans from the idle and the scanned page, waits for the scan and rejects a caught scan error", async () => {
  for (const failed of [false, true]) {
    const win = new EventTarget() as EventTarget & { __cleanzoSmoke: { visit(view: string): Promise<boolean> } };
    type Phase = "idle" | "scanning" | "scanned" | "failed";
    let phase = "idle" as Phase;
    let scans = 0;
    let polls = 0;
    // 每换一个画面，React 都换上新的按钮；点过的那个随之离开页面。
    const button = () => {
      const b = { isConnected: true, disabled: false, click() { b.isConnected = false; phase = "scanning"; polls = 0; scans++; } };
      return b;
    };
    let scan = button();
    const settle = () => {
      if (phase === "scanning" && ++polls > 3) {
        phase = failed ? "failed" : "scanned";
        scan = button();
      }
    };
    const nav = { click() {}, classList: { contains: () => false } };
    const root = {
      classList: { contains: () => false },
      querySelectorAll() { return []; },
      querySelector(selector: string) {
        settle();
        if (selector === ".cz-react") return {};
        if (selector === ".head-actions button") return phase === "scanned" ? scan : null;
        if (selector === "button") return phase === "idle" || phase === "scanned" ? scan : null;
        if (selector === ".empty.err") return phase === "failed" ? { textContent: "Scan failed" } : null;
        throw Error(`unexpected selector: ${selector}`);
      },
    };
    const document = { querySelector: (selector: string) => selector.startsWith(".nav-item") ? nav : root };
    const immediate = (fn: () => void) => { queueMicrotask(fn); };
    new Function("window", "document", "console", "setTimeout", source())(win, document, { error() {} }, immediate);
    if (failed) {
      await expect(win.__cleanzoSmoke.visit("aispace")).rejects.toThrow("aispace scan failed");
    } else {
      expect(await win.__cleanzoSmoke.visit("aispace")).toBe(true);
      expect(phase).toBe("scanned");
      expect(await win.__cleanzoSmoke.visit("aispace")).toBe(true);
      expect(phase).toBe("scanned");
      expect(scans).toBe(2);
    }
  }
});

test("theme readiness rejects an ambiguous native palette", async () => {
  for (const dark of [false, true]) {
    const win = Object.assign(new EventTarget(), { matchMedia: () => ({ matches: dark }) }) as EventTarget & {
      matchMedia: () => { matches: boolean };
      __cleanzoSmoke: { waitTheme(dark: boolean): Promise<boolean> };
    };
    let scheme = "light dark";
    new Function("window", "document", "console", "getComputedStyle", source())(
      win, { documentElement: {} }, { error() {} }, () => ({ colorScheme: scheme }),
    );
    await expect(win.__cleanzoSmoke.waitTheme(dark)).rejects.toThrow("ambiguous native palette");
    scheme = dark ? "dark" : "light";
    expect(await win.__cleanzoSmoke.waitTheme(dark)).toBe(true);
  }
});
