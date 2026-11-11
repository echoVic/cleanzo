import { expect, test } from "bun:test";
import type { Settings } from "../src/mygo";
import {
  baseNameOf, ignoredLabel, nextDisabledGroups, normalized, resolveFolderName, SaveQueue, toggleMenuBarItem,
} from "../src/react/settings/settings-helpers";
import type { Changes } from "./page-harness";

test("trash reminder controls expose threshold and permission, and hide when unsupported", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { TrashReminderControls } = await import("../src/react/settings/TrashReminderSection");
  const props = { state: { supported: true, fda: false, config: { enabled: true, thresholdGB: 2 } }, busy: false, onChange: () => {} };
  const html = renderToStaticMarkup(createElement(TrashReminderControls, props));
  expect(html).toContain('id="trash-threshold"');
  expect(html).toContain('value="2" selected=""');
  expect(html).toContain('完全磁盘访问权限');
  expect(renderToStaticMarkup(createElement(TrashReminderControls, { ...props, state: { ...props.state, supported: false } }))).toBe("");
  const disabled = renderToStaticMarkup(createElement(TrashReminderControls, { ...props, state: { ...props.state, config: { enabled: false, thresholdGB: 2 } } }));
  expect(disabled).toMatch(/<select[^>]+disabled/);
});

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

test("settings page offers system, light and dark appearance modes", async () => {
  await import("./setup-dom");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { SettingsPage } = await import("../src/react/settings/SettingsPage");
  const html = renderToStaticMarkup(createElement(SettingsPage, {
    callbacks: { changeLanguage: async () => {} },
    session: 0,
  }));
  expect(html).toContain('id="theme-select"');
  expect(html).toContain("外观");
  expect(html).toContain("跟随系统");
  expect(html).toContain("浅色");
  expect(html).toContain("深色");
});

/** 搭好设置页，自定义文件夹 Work 的改名总是存不上：改动都记在 env 里，由测试的 finally 还原。 */
async function renameFailsPage(env: Changes) {
  const { SettingsPage } = await import("../src/react/settings/SettingsPage");
  const { settingsApi } = await import("../src/react/settings/api");
  const { growthApi } = await import("../src/react/growth/api");
  const saved = settings({ custom: [{ name: "Work", path: "/Users/a/Work", enabled: true }] });
  env.spy(settingsApi, "getSettings").mockImplementation(async () => saved);
  env.spy(settingsApi, "saveSettings").mockImplementation(async () => { throw new Error("disk full"); });
  env.spy(settingsApi, "info").mockImplementation(async () => ({ version: "1.0.0" }) as never);
  env.spy(settingsApi, "fullDiskAccess").mockImplementation(async () => true);
  env.spy(settingsApi, "getTheme").mockImplementation(async () => "system");
  env.spy(settingsApi, "growthSummary").mockImplementation(async () => ({ supported: false }) as never);
  env.spy(settingsApi, "menuBar").mockImplementation(async () => ({ supported: false }) as never);
  env.spy(settingsApi, "trashReminder").mockImplementation(async () => ({ supported: false }) as never);
  env.spy(settingsApi, "onMenuBarUpdated").mockImplementation(() => () => {});
  env.spy(growthApi, "onUpdated").mockImplementation(() => () => {});
  const page = env.page(SettingsPage, { callbacks: { changeLanguage: async () => {} }, session: 0 });
  await page.settle();
  return {
    page,
    field() { return page.find((el) => el.props.name === "custom-folder-name")!; },
  };
}

test("after a failed rename the folder-name input shows the saved name again", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    const { page, field } = await renameFailsPage(env);
    expect(field().props.defaultValue).toBe("Work");
    const input = { value: "Renamed" };
    field().props.onBlur({ target: input });
    await page.settle();
    expect(page.text()).toContain("disk full");
    expect(input.value).toBe("Work");
  } finally { env.restore(); }
});

test("a failed rename puts the saved name back when the input lost focus because the window did", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    const { page, field } = await renameFailsPage(env);
    const input = { value: "Renamed" };
    // 切到别的应用：输入框收到 blur，activeElement 却仍是它，只是窗口没有焦点。
    page.document.activeElement = input;
    page.document.hasFocus = () => false;
    field().props.onBlur({ target: input });
    await page.settle();
    expect(page.text()).toContain("disk full");
    expect(input.value).toBe("Work");
  } finally { env.restore(); }
});

test("a failed rename leaves the input alone once the user is typing in it again", async () => {
  const env = (await import("./page-harness")).changes();
  try {
    const { page, field } = await renameFailsPage(env);
    const input = { value: "Renamed" };
    field().props.onBlur({ target: input });
    // 保存还没回来，用户已经回到框里接着改。
    page.document.activeElement = input;
    input.value = "Renamed again";
    await page.settle();
    expect(page.text()).toContain("disk full");
    expect(input.value).toBe("Renamed again");
  } finally { env.restore(); }
});

test("toggleMenuBarItem keeps the menu bar's order and drops names it doesn't know", () => {
  expect(toggleMenuBarItem(["mem", "cpu"], "gpu", true)).toEqual(["cpu", "mem", "gpu"]);
  expect(toggleMenuBarItem(["cpu", "mem"], "cpu", false)).toEqual(["mem"]);
  expect(toggleMenuBarItem(["cpu"], "cpu", true)).toEqual(["cpu"]);
  expect(toggleMenuBarItem(["cpu", "bogus"], "battery", true)).toEqual(["cpu", "battery"]);
  expect(toggleMenuBarItem(["cpu"], "fan", true, ["cpu", "fan"])).toEqual(["cpu", "fan"]);
});
