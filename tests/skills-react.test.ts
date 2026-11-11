import { expect, spyOn, test } from "bun:test";
import type { Skill, SkillCopy, SkillUpdate } from "../src/mygo";
import { daysAgo, linkCandidates, matchFilter, matchQuery, mergeOrigins, sortSkills, trashPlan, windowDays } from "../src/react/skills/helpers";

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

function update(path: string, over: Partial<SkillUpdate> = {}): SkillUpdate {
  return { path, name: path, origin: {}, local: "tree-1", remote: "", status: "unknown", modified: false, error: "", checkedAt: 0, ...over } as SkillUpdate;
}

test("a rescan keeps what the last update check found for folders that did not change", () => {
  const checked = {
    "/s/a": update("/s/a", { status: "update", remote: "tree-2", checkedAt: NOW }),
    "/s/b": update("/s/b", { status: "current", remote: "tree-1", checkedAt: NOW }),
    "/s/gone": update("/s/gone", { status: "update", remote: "tree-2", checkedAt: NOW }),
  };
  // 扫描后的离线来源：每个文件夹都是「未检查」
  const next = mergeOrigins(checked, [update("/s/a"), update("/s/b", { local: "tree-9" }), update("/s/new")]);
  expect(next["/s/a"]?.status).toBe("update"); // 没动过：保留检查结果
  expect(next["/s/b"]?.status).toBe("unknown"); // 内容变了：上次的结论作废
  expect(next["/s/new"]?.status).toBe("unknown");
  expect(next["/s/gone"]).toBeUndefined(); // 文件夹没了：不再留着它的状态
});

// ---- 页面：升级的确认（无浏览器，见 page-harness.ts） ----

async function skillsPage(skills: Skill[], checked: SkillUpdate[]) {
  await import("./setup-dom");
  const { pageHarness, textOf } = await import("./page-harness");
  const { t } = await import("../src/i18n");
  const { SkillsPage } = await import("../src/react/skills/SkillsPage");
  const { skillsApi } = await import("../src/react/skills/api");
  const upgrades: { path: string; force: boolean }[][] = [];
  const spies = [
    spyOn(skillsApi, "scan").mockImplementation(async () => ({ roots: [], skills, sources: [], scannedAt: NOW })),
    spyOn(skillsApi, "origins").mockImplementation(async () => checked.map((u) => ({ ...u, status: "unknown", checkedAt: 0 }))),
    spyOn(skillsApi, "checkUpdates").mockImplementation(async () => checked),
    spyOn(skillsApi, "upgrade").mockImplementation(async (reqs) => {
      upgrades.push(reqs);
      return reqs.map((r) => ({ path: r.path, tree: "tree-2", error: "" }));
    }),
  ];
  const page = pageHarness(SkillsPage, {});
  await page.settle();
  page.find((el) => typeof el.props.onCta === "function")!.props.onCta();
  await page.settle();
  page.click(t("skills.updates.check"));
  await page.settle();
  return {
    page, upgrades, t,
    row(name: string) { return page.find((el) => el.props.skill?.name === name && "onUpgrade" in el.props)!; },
    summary() { return page.find((el) => "onUpgradeAll" in el.props)!; },
    /** 升级确认框：开着时它的文字，关着时 null。 */
    dialog() {
      const el = page.find((n) => typeof n.props.onOpenChange === "function" && textOf(n).includes(t("skills.upgradeConfirm.title")));
      return el?.props.open ? textOf(el) : null;
    },
    close() { page.close(); for (const s of spies) s.mockRestore(); },
  };
}

test("a row's overwrite asks first, with the same dialog as Upgrade All", async () => {
  const s = skill("a", { copies: [copy({ path: "/s/a", real: "/s/a" })] });
  const sk = await skillsPage([s], [update("/s/a", { name: "a", status: "update", modified: true, remote: "tree-2", checkedAt: NOW })]);
  try {
    sk.row("a").props.onUpgrade("/s/a", true);
    await sk.page.settle();
    expect(sk.upgrades).toEqual([]);
    expect(sk.dialog()).toContain(sk.t("skills.upgradeConfirm.body", { n: 1 }));
    expect(sk.dialog()).toContain(sk.t("skills.upgradeConfirm.modified", { n: 1 }));
    sk.page.click(sk.t("skills.upgradeConfirm.force", { n: 1 }));
    await sk.page.settle();
    expect(sk.upgrades).toEqual([[{ path: "/s/a", force: true }]]);
    expect(sk.dialog()).toBeNull();
  } finally { sk.close(); }
});

test("Upgrade All counts the folders its dialog lists", async () => {
  // 同一个技能在两个工具里各有一份文件夹，都有更新；另有一条已经不在这次扫描里的结果。
  const s = skill("b", { copies: [
    copy({ tool: "claude", path: "/c/b", real: "/c/b" }),
    copy({ tool: "codex", path: "/x/b", real: "/x/b" }),
  ] });
  const ups = ["/c/b", "/x/b", "/gone/z"].map((p) => update(p, { name: "b", status: "update", remote: "tree-2", checkedAt: NOW }));
  const sk = await skillsPage([s], ups);
  try {
    const n = sk.summary().props.updatable;
    expect(n).toBe(2);
    sk.summary().props.onUpgradeAll();
    await sk.page.settle();
    expect(sk.dialog()).toContain(sk.t("skills.upgradeConfirm.body", { n }));
    sk.page.click(sk.t("skills.upgradeConfirm.ok"));
    await sk.page.settle();
    expect(sk.upgrades).toEqual([[{ path: "/c/b", force: false }, { path: "/x/b", force: false }]]);
  } finally { sk.close(); }
});

test("upgrade error codes read as text in both languages; an unknown error shows as sent", async () => {
  await import("./setup-dom");
  const { t, setPref } = await import("../src/i18n");
  const { failReason } = await import("../src/react/skills/SkillsPage");
  try {
    for (const lang of ["zh", "en"] as const) {
      setPref(lang);
      for (const code of ["rate-limited", "not-found", "modified", "mismatch", "no-origin", "too-large", "too-many-entries"]) {
        const text = failReason(t, code);
        expect(text).not.toBe(code);
        expect(text).not.toContain("skills.err.");
      }
      expect(failReason(t, "github 502")).toBe("github 502");
    }
  } finally { setPref("system"); }
});
