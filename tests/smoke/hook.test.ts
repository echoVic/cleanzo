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
