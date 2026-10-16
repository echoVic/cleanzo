import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { SettingsPage, type SettingsCallbacks } from "./SettingsPage";
import "../../ui/index.css";

let root: Root | null = null;

/** 在原生视图的容器里挂载设置页；session 递增触发页面重新读取配置。 */
export function mountSettings(el: HTMLElement, callbacks: SettingsCallbacks, session: number) {
  if (!root) root = createRoot(el);
  root.render(
    <React.StrictMode>
      <SettingsPage callbacks={callbacks} session={session} />
    </React.StrictMode>,
  );
}
