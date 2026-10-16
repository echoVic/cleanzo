import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { LargePage } from "./LargePage";
import "../../ui/index.css";

let root: Root | null = null;

/** 在原生视图的容器里挂载大文件页；root 复用，重进视图不整树卸载（结果保留、不重扫）。 */
export function mountLarge(el: HTMLElement) {
  if (!root) root = createRoot(el);
  root.render(
    <React.StrictMode>
      <LargePage />
    </React.StrictMode>,
  );
}
