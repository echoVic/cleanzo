// React 技能页与原生导航的桥接：main.ts 调这两个函数即可，不直接接触 React。
import { mountSkills } from "./mount";
import { skillsApi } from "./api";

/** 启动时有任何技能目录才显示侧栏「技能」入口。 */
export async function initReactSkills() {
  let any = false;
  try {
    any = (await skillsApi.listRoots()).length > 0;
  } catch {
    any = false;
  }
  document.querySelector(`.nav-item[data-view="skills"]`)?.classList.toggle("hidden", !any);
  if (any) document.querySelector("#nav-ai-section")?.classList.remove("hidden");
}

export function showReactSkills() {
  const el = document.querySelector<HTMLElement>("#skills-react-root");
  if (el) mountSkills(el);
}
