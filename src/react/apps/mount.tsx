import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { AppsPage } from "./AppsPage";
import { appsRequest } from "./requests";
import "../../ui/index.css";

let root: Root | null = null;

/** 第一次进入才建根，之后复用；离开时传 active=false 取消更新检查。 */
export function mountApps(el: HTMLElement, active: boolean) {
  if (!root) {
    if (!active) return;
    root = createRoot(el);
  }
  const r = appsRequest();
  root.render(
    <React.StrictMode>
      <AppsPage active={active} initialFilter={r.filter} nonce={r.filterNonce} uninstallPath={r.uninstallPath} uninstallNonce={r.uninstallNonce} />
    </React.StrictMode>,
  );
}
