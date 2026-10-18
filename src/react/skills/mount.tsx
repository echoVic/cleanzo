import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { SkillsPage } from "./SkillsPage";
import "../../ui/index.css";

let root: Root | null = null;

/** 在原生视图的容器里挂载技能页；root 复用，重进视图不重扫。 */
export function mountSkills(el: HTMLElement) {
  if (!root) root = createRoot(el);
  root.render(
    <React.StrictMode>
      <SkillsPage />
    </React.StrictMode>,
  );
}
