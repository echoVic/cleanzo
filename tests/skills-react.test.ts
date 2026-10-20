import { expect, test } from "bun:test";
import type { Skill, SkillCopy } from "../src/mygo";
import { daysAgo, linkCandidates, matchFilter, matchQuery, sortSkills, trashPlan, windowDays } from "../src/react/skills/helpers";

const DAY = 86400;
const NOW = 1_790_000_000;

function copy(over: Partial<SkillCopy> = {}): SkillCopy {
  return {
    tool: "agents", path: "/Users/a/.agents/skills/x", kind: "dir", target: "", real: "/Users/a/.agents/skills/x",
    size: 100, hash: "h", modified: NOW - 90 * DAY, linkedBy: [], removable: true, ...over,
  } as SkillCopy;
}

function skill(name: string, over: Partial<Skill> = {}): Skill {
  return {
    name, description: "", copies: [copy({ path: `/Users/a/.agents/skills/${name}`, real: `/Users/a/.agents/skills/${name}` })],
    size: 100, installed: NOW - 90 * DAY, uses: [], last: 0, count: 0, observed: true, since: NOW - 30 * DAY, idle: false,
    issues: [], ...over,
  } as Skill;
}

test("filters by idle, issues and missing data", () => {
  const idle = skill("a", { idle: true });
  const broken = skill("b", { issues: ["broken"] });
  const blind = skill("c", { observed: false });
  expect([idle, broken, blind].filter((s) => matchFilter(s, "idle")).map((s) => s.name)).toEqual(["a"]);
  expect([idle, broken, blind].filter((s) => matchFilter(s, "issues")).map((s) => s.name)).toEqual(["b"]);
  expect([idle, broken, blind].filter((s) => matchFilter(s, "unobserved")).map((s) => s.name)).toEqual(["c"]);
  expect([idle, broken, blind].filter((s) => matchFilter(s, "all")).length).toBe(3);
});

test("query matches name or description, case-insensitively", () => {
  const s = skill("pdf-tools", { description: "Merge PDF files" });
  expect(matchQuery(s, "PDF")).toBe(true);
  expect(matchQuery(s, "merge")).toBe(true);
  expect(matchQuery(s, "  ")).toBe(true);
  expect(matchQuery(s, "excel")).toBe(false);
});

test("last-called sort puts unused first, then oldest call, and no-data last", () => {
  const list = [
    skill("recent", { count: 3, last: NOW - DAY }),
    skill("blind", { observed: false }),
    skill("unused"),
    skill("old", { count: 1, last: NOW - 20 * DAY }),
  ];
  expect(sortSkills(list, "last").map((s) => s.name)).toEqual(["unused", "old", "recent", "blind"]);
  expect(sortSkills(list, "count").map((s) => s.name)[0]).toBe("recent");
});

test("window and age are whole days", () => {
  expect(windowDays(0, NOW)).toBe(0);
  expect(windowDays(NOW - 29.5 * DAY, NOW)).toBe(29);
  expect(windowDays(NOW - 10, NOW)).toBe(1);
  expect(daysAgo(NOW - 2.9 * DAY, NOW)).toBe(2);
  expect(daysAgo(NOW + 100, NOW)).toBe(0);
});

test("trash plan takes the links that point at a picked folder along with it", () => {
  const folder = copy({ path: "/Users/a/.agents/skills/s", real: "/Users/a/.agents/skills/s", size: 500, linkedBy: ["/Users/a/.claude/skills/s"] });
  const link = copy({ tool: "claude", path: "/Users/a/.claude/skills/s", kind: "link", real: "/Users/a/.agents/skills/s", size: 0 });
  const locked = copy({ tool: "codex", path: "/Users/a/.codex/skills/s", real: "/Users/a/.codex/skills/s", size: 7, removable: false });
  const s = skill("s", { copies: [folder, link, locked] });
  const other = skill("other");
  const plan = trashPlan([s, other], new Set(["s"]));
  expect(plan.paths.sort()).toEqual(["/Users/a/.agents/skills/s", "/Users/a/.claude/skills/s"]);
  // The link is one of the picked skill's own copies, not an extra.
  expect(plan.links).toEqual([]);
  expect(plan.size).toBe(500);
  expect(trashPlan([s, other], new Set()).paths).toEqual([]);
});

test("trash plan lists links from another skill name as extras", () => {
  const folder = copy({ path: "/Users/a/.agents/skills/s", real: "/Users/a/.agents/skills/s", linkedBy: ["/Users/a/.pi/agent/skills/alias"] });
  const plan = trashPlan([skill("s", { copies: [folder] })], new Set(["s"]));
  expect(plan.links).toEqual(["/Users/a/.pi/agent/skills/alias"]);
  expect(plan.paths).toContain("/Users/a/.pi/agent/skills/alias");
});

test("only identical, unlinked copies outside ~/.agents are offered for linking", () => {
  const target = copy({ path: "/Users/a/.agents/skills/s", real: "/Users/a/.agents/skills/s", hash: "same" });
  const same = copy({ tool: "claude", path: "/Users/a/.claude/skills/s", real: "/Users/a/.claude/skills/s", hash: "same" });
  const drift = copy({ tool: "codex", path: "/Users/a/.codex/skills/s", real: "/Users/a/.codex/skills/s", hash: "other" });
  const linked = copy({ tool: "gemini", path: "/Users/a/.gemini/skills/s", real: "/Users/a/.gemini/skills/s", hash: "same", linkedBy: ["/x"] });
  const unhashed = copy({ tool: "pi", path: "/Users/a/.pi/agent/skills/s", real: "/Users/a/.pi/agent/skills/s", hash: "" });
  const s = skill("s", { copies: [same, target, drift, linked, unhashed] });
  expect(linkCandidates(s)).toEqual([{ path: "/Users/a/.claude/skills/s", target: "/Users/a/.agents/skills/s" }]);
  // Without a copy in ~/.agents there is no target.
  expect(linkCandidates(skill("t", { copies: [same, drift] }))).toEqual([]);
});

test("a cross-name symlink to a different skill moves only the link when its target is not picked", () => {
  // Pi has cz-alias -> ~/.agents/skills/cz-src (a different skill name).
  const src = skill("cz-src", { copies: [copy({ path: "/Users/a/.agents/skills/cz-src", real: "/Users/a/.agents/skills/cz-src" })] });
  const alias = skill("cz-alias", {
    observed: false, copies: [copy({
      tool: "pi", path: "/Users/a/.pi/agent/skills/cz-alias", kind: "link",
      real: "/Users/a/.agents/skills/cz-src", size: 0,
    })],
  });
  // Pick only the alias.
  let plan = trashPlan([src, alias], new Set(["cz-alias"]));
  expect(plan.paths).toEqual(["/Users/a/.pi/agent/skills/cz-alias"]);
  expect(plan.links).toEqual([]);
  expect(plan.size).toBe(0);
  // Pick both: folder and link move, and the link is not listed as an extra.
  plan = trashPlan([src, alias], new Set(["cz-src", "cz-alias"]));
  expect(plan.paths.sort()).toEqual(["/Users/a/.agents/skills/cz-src", "/Users/a/.pi/agent/skills/cz-alias"]);
  expect(plan.links).toEqual([]);
  expect(plan.size).toBe(100);
});


test("Windows offers no link-copy candidates for identical skill copies", async () => {
  const { setPlatformForTest } = await import("../src/platform");
  const s = skill("x", { copies: [copy(), copy({ tool: "claude", path: "/other/x", real: "/other/x" })] });
  try {
    setPlatformForTest("darwin");
    expect(linkCandidates(s)).toHaveLength(1);
    setPlatformForTest("win32");
    expect(linkCandidates(s)).toEqual([]);
  } finally { setPlatformForTest(undefined); }
});

test("Windows Skills empty guide uses a native agents path in zh/en", async () => {
  await import("./setup-dom");
  const { setPlatformForTest } = await import("../src/platform");
  const { setPref, t } = await import("../src/i18n");
  const { createElement } = await import("react");
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { SkillsPage } = await import("../src/react/skills/SkillsPage");
  try {
    setPlatformForTest("win32");
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      expect(t("skills.idle.guide")).toContain("~\\.agents");
      const html = renderToStaticMarkup(createElement(SkillsPage));
      expect(html).toContain("~\\.agents");
      expect(html).not.toContain("~/.agents");
    }
    setPlatformForTest("darwin");
    expect(t("skills.idle.guide")).toContain("~/.agents");
  } finally { setPlatformForTest(undefined); setPref("system"); }
});
