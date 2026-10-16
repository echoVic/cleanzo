import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { AIToolPage } from "./AIToolPage";
import "../../ui/index.css";

const roots = new Map<string, Root>();

/** 在原生视图的容器里挂载某个工具的 React 页面。 */
export function mountAITool(el: HTMLElement, toolId: string, toolName: string) {
  let root = roots.get(toolId);
  if (!root) {
    root = createRoot(el);
    roots.set(toolId, root);
  }
  root.render(
    <React.StrictMode>
      <AIToolPage toolId={toolId} toolName={toolName} />
    </React.StrictMode>,
  );
}

export function unmountAITool(toolId: string) {
  roots.get(toolId)?.unmount();
  roots.delete(toolId);
}
