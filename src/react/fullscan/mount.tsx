import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { FullScanPage } from "./FullScanPage";
import "../../ui/index.css";

let root: Root | null = null;

/** 在原生视图的容器里挂载全盘扫描页；root 复用，重进视图不整树卸载（结果与磁盘卡保留、不重扫）。 */
export function mountFullScan(el: HTMLElement) {
  if (!root) root = createRoot(el);
  root.render(
    <React.StrictMode>
      <FullScanPage />
    </React.StrictMode>,
  );
}
