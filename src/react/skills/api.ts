import { Cleaner } from "../../mygo";
import type { SkillRoot, SkillScanResult, SkillTrashResult, SkillUpdate, SkillUpgradeRequest, SkillUpgradeResult } from "../../mygo";

/** 技能页用到的全部 Cleaner 调用集中在此，组件不直接碰桥。 */
export const skillsApi = {
  listRoots: (): Promise<SkillRoot[]> => Cleaner.listSkillRoots(),
  scan: (): Promise<SkillScanResult> => Cleaner.scanSkills(),
  trash: (paths: string[]): Promise<SkillTrashResult> => Cleaner.trashSkills(paths),
  link: (path: string, target: string): Promise<void> => Cleaner.linkSkillCopy(path, target),
  origins: (): Promise<SkillUpdate[]> => Cleaner.listSkillOrigins(),
  checkUpdates: (): Promise<SkillUpdate[]> => Cleaner.checkSkillUpdates(),
  upgrade: (reqs: SkillUpgradeRequest[]): Promise<SkillUpgradeResult[]> => Cleaner.upgradeSkills(reqs),
  reveal: (path: string) => Cleaner.revealInFinder(path),
};
