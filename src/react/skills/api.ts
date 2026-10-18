import { Cleaner } from "../../mygo";
import type { SkillRoot, SkillScanResult, SkillTrashResult } from "../../mygo";

/** 技能页用到的全部 Cleaner 调用集中在此，组件不直接碰桥。 */
export const skillsApi = {
  listRoots: (): Promise<SkillRoot[]> => Cleaner.listSkillRoots(),
  scan: (): Promise<SkillScanResult> => Cleaner.scanSkills(),
  trash: (paths: string[]): Promise<SkillTrashResult> => Cleaner.trashSkills(paths),
  link: (path: string, target: string): Promise<void> => Cleaner.linkSkillCopy(path, target),
  reveal: (path: string) => Cleaner.revealInFinder(path),
};
