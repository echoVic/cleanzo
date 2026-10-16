// React AI 页与原生导航的桥接：main.ts 调这两个函数即可，不直接接触 React。
import { mountAITool } from "./mount";
import { aiApi } from "./api";

const containers = new Map<string, HTMLElement>();

/** 启动时按安装情况显示侧栏「AI 工具」分组和各工具入口。 */
export async function initReactAITools() {
  let list: { id: string; installed: boolean }[] = [];
  try {
    list = await aiApi.listTools();
  } catch {
    list = [];
  }
  let any = false;
  for (const info of list) {
    const btn = document.querySelector<HTMLElement>(`.nav-item[data-tool="${info.id}"]`);
    if (btn) btn.classList.toggle("hidden", !info.installed);
    if (info.installed) any = true;
  }
  document.querySelector("#nav-ai-section")?.classList.toggle("hidden", !any);
}

export function showReactAITool(toolId: string, toolName: string) {
  let host = containers.get(toolId);
  if (!host) {
    host = document.createElement("div");
    host.className = "cz-react-host";
    document.querySelector("#ai-react-root")?.appendChild(host);
    containers.set(toolId, host);
  }
  for (const [id, el] of containers) el.classList.toggle("hidden", id !== toolId);
  mountAITool(host, toolId, toolName);
}
