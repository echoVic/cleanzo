import { expect, test } from "bun:test";
import type { Settings } from "../src/mygo";
import {
  baseNameOf, ignoredLabel, nextDisabledGroups, normalized, resolveFolderName, SaveQueue,
} from "../src/react/settings/settings-helpers";

function settings(over: Partial<Settings> = {}): Settings {
  return { disabledGroups: ["dev"], ignored: ["app:com.foo", "rule.sys.trash"], custom: [], ...over };
}

test("normalized fills null arrays the Go binding may emit", () => {
  const s = { disabledGroups: null, ignored: null, custom: null } as unknown as Settings;
  expect(normalized(s)).toEqual({ disabledGroups: [], ignored: [], custom: [] });
  expect(normalized(settings())).toEqual(settings());
});

test("group checkbox checked state mirrors membership in disabledGroups", () => {
  // 勾选 → 从 disabledGroups 移除；不勾选 → 加入。
  expect(nextDisabledGroups(["dev"], "dev", true)).toEqual([]);
  expect(nextDisabledGroups([], "apps", false)).toEqual(["apps"]);
  // 重复勾选/取消保持幂等，不产生重复项。
  expect(nextDisabledGroups(["dev"], "dev", false)).toEqual(["dev"]);
  expect(nextDisabledGroups(["dev", "updates"], "dev", true)).toEqual(["updates"]);
});

test("empty folder name falls back to the path's base name", () => {
  expect(baseNameOf("/Users/alice/Downloads/归档")).toBe("归档");
  expect(baseNameOf("/")).toBe("/");
  expect(resolveFolderName("  ", "/Users/alice/Downloads/归档")).toBe("归档");
  expect(resolveFolderName("我的目录", "/Users/alice/Downloads/归档")).toBe("我的目录");
  expect(resolveFolderName("  我的目录  ", "/x/y")).toBe("我的目录");
});

test("ignored item label resolves app: ids via the installed-app map, keeps rule ids", () => {
  const apps = new Map([["com.foo", "Foo"]]);
  expect(ignoredLabel("app:com.foo", apps, (id) => `rule:${id}`)).toBe("Foo");
  // bundleId 大小写不敏感；查不到保留原 ID，不阻塞。
  expect(ignoredLabel("app:com.Bar", apps, (id) => `rule:${id}`)).toBe("app:com.Bar");
  expect(ignoredLabel("app:com.missing", apps, (id) => `rule:${id}`)).toBe("app:com.missing");
  // 规则 id 走 ruleName 回调（未知规则由回调保留稳定 ID）。
  expect(ignoredLabel("rule.sys.trash", apps, (id) => `rule:${id}`)).toBe("rule:rule.sys.trash");
});

test("SaveQueue runs jobs serially and isolates failures", async () => {
  const q = new SaveQueue();
  const order: string[] = [];
  q.run(async () => { order.push("a1"); await Promise.resolve(); order.push("a2"); });
  const boom = q.run(async () => { order.push("b1"); throw new Error("boom"); });
  const c = q.run(async () => { order.push("c1"); });
  await expect(boom).rejects.toThrow("boom");
  await c;
  // 串行：a 整段跑完才轮到 b，b 失败后 c 仍继续。
  expect(order).toEqual(["a1", "a2", "b1", "c1"]);
});


test("Windows settings omit the permission section; macOS keeps its controls", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { PermissionsSection } = await import("../src/react/settings/SettingsPage");
  const { setPlatformForTest } = await import("../src/platform");
  const props = { access: false, permissionKey: "settings.notGranted" as const, permissionError: null, onOpen: () => {} };
  try {
    setPlatformForTest("darwin");
    expect(renderToStaticMarkup(createElement(PermissionsSection, props))).toContain("setting-permission");
    setPlatformForTest("win32");
    expect(renderToStaticMarkup(createElement(PermissionsSection, props))).toBe("");
    expect(resolveFolderName("", "C:\\Users\\alice\\Downloads")).toBe("Downloads");
  } finally { setPlatformForTest(undefined); }
});
