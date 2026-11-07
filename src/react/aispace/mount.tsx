import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { AISpacePage } from "./AISpacePage";
import "../../ui/index.css";

let root: Root | null = null;

/** 在原生视图的容器里挂载 AI 空间页。 */
export function mountAISpace(el: HTMLElement) {
  if (!root) root = createRoot(el);
  root.render(
    <React.StrictMode>
      <AISpacePage />
    </React.StrictMode>,
  );
}
