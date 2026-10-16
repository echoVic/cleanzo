import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { StatusPage } from "./StatusPage";
import "../../ui/index.css";

let root: Root | null = null;

/** 在原生视图的容器里挂载系统状态页；root 复用，重进视图不整树卸载（采样与行态由 active prop 管，不跳顶）。 */
export function mountStatus(el: HTMLElement, active: boolean, goClean: () => void) {
  if (!root) root = createRoot(el);
  root.render(
    <React.StrictMode>
      <StatusPage active={active} goClean={goClean} />
    </React.StrictMode>,
  );
}
