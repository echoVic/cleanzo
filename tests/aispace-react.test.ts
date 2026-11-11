import { setLang } from "./setup-dom";
import { expect, test } from "bun:test";
import type { AIGroup, AIPart, AITool } from "../src/mygo";
import { t } from "../src/i18n";
import { human } from "../src/shared";
import {
  cleanReport,
  cleanToastText,
  pickedBytes,
  relTime,
  rowsChecked,
  selectionsFor,
  splitParts,
  toggleRows,
  type PartRow,
} from "../src/react/aispace/selection";

const DAY = 86400;
const NOW = 1_700_000_000;

function group(key: string, over: Partial<AIGroup> = {}): AIGroup {
  return { key, label: key, path: `/tmp/${key}`, size: 100, count: 1, recent: 0, last: NOW - 200 * DAY, gone: false, ...over };
}

function part(id: string, level: string, over: Partial<AIPart> = {}): AIPart {
  return { id, name: id, path: `/tmp/${id}`, level, size: 10, files: 1, last: NOW, groups: [], top: [], more: 0, keep: "", runSafe: false, running: false, ...over };
}

function tool(id: string, parts: AIPart[]): AITool {
  return { id, name: id, parts } as AITool;
}

const claude = tool("claude", [
  part("claude.sessions", "sensitive", { size: 300, groups: [group("old"), group("stale"), group("today", { recent: 1 })] }),
  part("claude.cache", "safe", { size: 40 }),
  part("claude.promptHistory", "optional", { size: 7 }),
  part("claude.settings", "protected", { size: 3 }),
  part("unknown:notes", "unknown", { size: 5 }),
]);
const trae = tool("trae", [
  part("trae.ide.chats", "sensitive", { size: 900 }),
  part("trae.ide.cache", "safe", { size: 60, running: true }),
  part("trae.ide.logs", "safe", { size: 20, running: true, runSafe: true }),
]);

const ids = (rows: PartRow[]) => rows.map((r) => r.part.id);
const none = () => ({ parts: new Set<string>(), groups: new Map<string, Set<string>>() });

test("the cache rows hold safe parts only; optional parts are their own rows", () => {
  const s = splitParts([claude, trae]);
  expect(ids(s.sessions)).toEqual(["claude.sessions", "trae.ide.chats"]);
  expect(ids(s.caches)).toEqual(["claude.cache", "trae.ide.cache", "trae.ide.logs"]);
  expect(ids(s.optional)).toEqual(["claude.promptHistory"]);
});

test("protected and unknown parts are never offered and count as kept", () => {
  const s = splitParts([claude, trae]);
  expect(s.kept).toBe(3 + 5);
  expect(ids([...s.sessions, ...s.caches, ...s.optional])).not.toContain("unknown:notes");
});

test("ticking the cache row sends caches and nothing else", () => {
  const s = splitParts([claude, trae]);
  const picks = toggleRows(none(), s.caches, true);
  const sent = selectionsFor(picks, [...s.sessions, ...s.caches, ...s.optional]);
  expect(sent.map((x) => x.part)).toEqual(["claude.cache", "trae.ide.logs"]);
});

test("a part its running tool still uses cannot be ticked", () => {
  const s = splitParts([trae]);
  const picks = toggleRows(none(), s.caches, true);
  expect(picks.parts.has("trae:trae.ide.cache")).toBe(false);
  expect(picks.parts.has("trae:trae.ide.logs")).toBe(true);
  // 能勾的都勾上了：这一行算全选，而不是永远半选
  expect(rowsChecked(picks, s.caches)).toBe(true);
});

test("ticking a sessions row picks every group but today's, and unticking clears it", () => {
  const rows = splitParts([claude]).sessions;
  const on = toggleRows(none(), rows, true);
  expect([...(on.groups.get("claude:claude.sessions") ?? [])]).toEqual(["old", "stale"]);
  expect(rowsChecked(on, rows)).toBe(true);
  const off = toggleRows(on, rows, false);
  expect(rowsChecked(off, rows)).toBe(false);
  expect(selectionsFor(off, rows)).toEqual([]);
});

test("a row is half checked while only some of it is picked", () => {
  const rows = splitParts([claude]).sessions;
  const picks = { parts: new Set<string>(), groups: new Map([["claude:claude.sessions", new Set(["old"])]]) };
  expect(rowsChecked(picks, rows)).toBe("indeterminate");
});

test("the bytes shown are the bytes of what is sent", () => {
  const s = splitParts([claude, trae]);
  const picks = { parts: new Set(["claude:claude.cache", "trae:trae.ide.chats"]), groups: new Map([["claude:claude.sessions", new Set(["old", "today"])]]) };
  expect(pickedBytes(picks, s.sessions)).toBe(100 + 900); // today's group is kept by the backend
  expect(pickedBytes(picks, s.caches)).toBe(40);
  expect(pickedBytes(picks, s.optional)).toBe(0);
  expect(selectionsFor(picks, [...s.sessions, ...s.caches])).toEqual([
    { tool: "claude", part: "claude.sessions", groups: ["old", "today"] },
    { tool: "trae", part: "trae.ide.chats", groups: [] },
    { tool: "claude", part: "claude.cache", groups: [] },
  ]);
});

test("relative times follow the interface language", () => {
  setLang("zh");
  expect(relTime(0, NOW, t)).toBe("—");
  expect(relTime(NOW - 3600, NOW, t)).toBe("今天");
  expect(relTime(NOW - DAY, NOW, t)).toBe("昨天");
  expect(relTime(NOW - 12 * DAY, NOW, t)).toBe("12 天前");
  expect(relTime(NOW - 60 * DAY, NOW, t)).toBe("2 个月前");
  expect(relTime(NOW - 800 * DAY, NOW, t)).toBe("2 年前");
  setLang("en");
  expect(relTime(NOW - 3600, NOW, t)).toBe("Today");
  expect(relTime(NOW - DAY, NOW, t)).toBe("Yesterday");
  expect(relTime(NOW - 12 * DAY, NOW, t)).toBe("12 days ago");
  expect(relTime(NOW - 31 * DAY, NOW, t)).toBe("1 month ago");
  expect(relTime(NOW - 800 * DAY, NOW, t)).toBe("2 years ago");
});

test("a clean with failed entries names the first reason in its toast and keeps the counts", () => {
  setLang("zh");
  const unreadable = "~/.ollama/models/manifests/registry.ollama.ai/library/llama3/latest: unreadable";
  const report = cleanReport([
    { freed: 1024, parts: [
      { tool: "ollama", part: "llama3", moved: 0, freed: 0, kept: 0, skipped: "", failed: [unreadable] },
      { tool: "ollama", part: "qwen", moved: 1, freed: 1024, kept: 0, skipped: "", failed: [] },
    ] },
    null,
    { freed: 0, parts: [{ tool: "claude", part: "claude.cache", moved: 0, freed: 0, kept: 2, skipped: "", failed: ["~/.claude/cache: in-use"] }] },
  ]);
  expect(report).toMatchObject({ freed: 1024, kept: 2, skipped: 2 });
  const text = cleanToastText(report, t, human);
  expect(text).toContain(t("aispace.done", { size: human(1024) }));
  expect(text).toContain(t("aispace.done.kept", { n: 2 }));
  expect(text).toContain(t("aispace.done.skipped", { n: 2 }));
  expect(text).toContain("~/.ollama/models/manifests/registry.ollama.ai/library/llama3/latest: 读不出来");
  expect(text).not.toContain("unreadable");
  expect(text).not.toContain("in-use");
  expect(cleanToastText(cleanReport([{ freed: 0, parts: [] }]), t, human)).toBe(t("aispace.done", { size: human(0) }));
});

test("the first failure in the toast keeps its path and reads its code in the interface language", () => {
  const first = (failed: string) =>
    cleanToastText(cleanReport([{ freed: 0, parts: [{ tool: "claude", part: "claude.cache", moved: 0, freed: 0, kept: 0, skipped: "", failed: [failed] }] }]), t, human)
      .split(" · ").at(-1);
  try {
    setLang("en");
    expect(first("~/.ollama/models/manifests/x/latest: unreadable")).toBe("~/.ollama/models/manifests/x/latest: could not be read");
    expect(first("~/.claude/cache: in-use")).toBe(`~/.claude/cache: ${t("fs.inUse")}`);
    expect(first("~/.claude/cache: blocked")).toBe(`~/.claude/cache: ${t("fs.blocked")}`);
    expect(first('~/.claude/cache: {"code":"gone"}')).toBe(`~/.claude/cache: ${t("fs.gone")}`);
    // 路径里有「: 」也照原样，只翻最后的代码。
    expect(first("~/Notes: old/x: not-recycled")).toBe(`~/Notes: old/x: ${t("fs.notRecycled")}`);
    setLang("zh");
    expect(first("~/.claude/cache: in-use")).toBe(`~/.claude/cache: ${t("fs.inUse")}`);
    expect(first("~/.claude/cache: in-use")).not.toContain("in-use");
    // 不认识的代码、系统的错误原文：照原样显示。
    expect(first("~/.claude/cache: weird-code")).toBe("~/.claude/cache: weird-code");
    expect(first("~/.claude/cache: open /Users/a/.claude/cache: permission denied")).toBe("~/.claude/cache: open /Users/a/.claude/cache: permission denied");
  } finally { setLang("zh"); }
});
