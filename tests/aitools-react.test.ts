import { expect, test } from "bun:test";
import type { AIGroup, AIPart } from "../src/mygo";
import { offered, groupSelectable, type WindowDays } from "../src/react/ai/types";
import { human, displayPath } from "../src/react/ai/format";

const DAY = 86400;
const NOW = 1_700_000_000;

function part(level: AIPart["level"], groups: AIGroup[] = [], size = 1): AIPart {
  return {
    id: `${level}-${Math.random()}`,
    name: level,
    level,
    size,
    path: "/tmp/x",
    runSafe: false,
    groups,
  } as AIPart;
}

function group(over: Partial<AIGroup> = {}): AIGroup {
  return {
    key: "g",
    label: "g",
    path: "/tmp/g",
    size: 100,
    count: 5,
    recent: 0,
    last: NOW - 31 * DAY,
    gone: false,
    ...over,
  } as AIGroup;
}

test("offered hides protected and unknown parts only", () => {
  expect(offered(part("safe"))).toBe(true);
  expect(offered(part("optional"))).toBe(true);
  expect(offered(part("sensitive"))).toBe(true);
  expect(offered(part("protected"))).toBe(false);
  expect(offered(part("unknown"))).toBe(false);
});

test("non-session groups are selectable whenever nonempty", () => {
  expect(groupSelectable(part("safe"), group(), 30, NOW)).toBe(true);
  expect(groupSelectable(part("safe"), group({ size: 0 }), 30, NOW)).toBe(false);
});

test("session groups need entries beyond today's and an age outside the window", () => {
  const p = part("sensitive");
  // 全部是最近会话：不可选
  expect(groupSelectable(p, group({ count: 3, recent: 3 }), 30, NOW)).toBe(false);
  // 在 30 天窗口内：不可选
  expect(groupSelectable(p, group({ count: 5, recent: 1, last: NOW - 10 * DAY }), 30 as WindowDays, NOW)).toBe(false);
  // 窗口外且有旧会话：可选
  expect(groupSelectable(p, group({ count: 5, recent: 1, last: NOW - 31 * DAY }), 30 as WindowDays, NOW)).toBe(true);
  // 7 天窗口：31 天前可选
  expect(groupSelectable(p, group({ count: 5, recent: 1, last: NOW - 8 * DAY }), 7 as WindowDays, NOW)).toBe(true);
  // 90 天窗口：31 天前不可选
  expect(groupSelectable(p, group({ count: 5, recent: 1, last: NOW - 31 * DAY }), 90 as WindowDays, NOW)).toBe(false);
});

test("human formats bytes the same way the rest of the UI does", () => {
  expect(human(0)).toBe("0 B");
  expect(human(512)).toBe("512 B");
  expect(human(2048)).toBe("2 KB");
  expect(human(5 * 1024 * 1024)).toBe("5.0 MB");
  expect(human(5 * 1024 ** 3)).toBe("5.0 GB");
  expect(human(1500 * 1024 ** 3)).toBe("1.5 TB");
});

test("displayPath collapses the home directory", () => {
  expect(displayPath("/Users/alice/.claude/projects/x")).toBe("~/.claude/projects/x");
  expect(displayPath("/var/tmp/x")).toBe("/var/tmp/x");
});
