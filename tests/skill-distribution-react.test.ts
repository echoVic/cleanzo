import { expect, test } from "bun:test";
import { Cleaner, type Skill, type SkillDeployment } from "../src/mygo";
import { SkillDistribution } from "../src/react/skills/SkillDistribution";
import { changes } from "./page-harness";

const source = "/Users/a/.agents/skills/example";
const skill: Skill = {
  name: "example", description: "", copies: [], size: 0, installed: 0,
  uses: [], last: 0, count: 0, observed: false, since: 0, idle: false, issues: [],
};
const base: SkillDeployment = {
  tool: "agents", label: "~/.agents", path: source, state: "directory", availability: "direct", shared: false,
  sharedPath: "", linkTarget: "", source, canDeploy: false, canRepair: false,
};

test("displays shared availability separately and repairs the exact observed broken link", async () => {
  const env = changes();
  try {
    env.lang("en");
    const rows = [base, {
      ...base, tool: "codex", label: "Codex", path: "/Users/a/.codex/skills/example", source: "",
      state: "broken", availability: "shared", shared: true, sharedPath: source, linkTarget: "../gone", canRepair: true,
    }];
    const list = env.spy(Cleaner, "listSkillDistribution").mockResolvedValue(rows);
    const apply = env.spy(Cleaner, "deploySkill").mockResolvedValue(undefined);
    let refreshes = 0;
    const page = env.page(SkillDistribution, { skill, onChanged: () => { refreshes++; } });
    await page.settle();
    expect(list).not.toHaveBeenCalled();
    page.click("Tool distribution");
    await page.settle();
    expect(page.text()).toContain("Available via a shared directory");
    expect(page.text()).toContain("Broken link");
    page.click("Repair link");
    await page.settle();
    expect(apply).toHaveBeenCalledWith({ name: "example", tool: "codex", source, mode: "link", repair: true, previousTarget: "../gone" });
    expect(refreshes).toBe(1);
  } finally {
    env.restore();
  }
});
