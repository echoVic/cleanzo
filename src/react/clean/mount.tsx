import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { CleanPage } from "./CleanPage";
import "../../ui/index.css";

let root: Root | null = null;

/** 在原生视图的容器里挂载垃圾清理页；root 复用，重进视图不整树卸载（扫描 / 清理流继续、结果保留）。 */
export function mountClean(el: HTMLElement) {
  if (!root) root = createRoot(el);
  root.render(
    <React.StrictMode>
      <CleanPage />
    </React.StrictMode>,
  );
}
