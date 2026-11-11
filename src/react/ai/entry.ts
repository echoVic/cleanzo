// React AI 页与原生导航的桥接：main.ts 调这两个函数即可，不直接接触 React。
import { mountAITool } from "./mount";
import { aiApi } from "./api";

const containers = new Map<string, HTMLElement>();
/** 显示过几次 AI 工具视图：每次显示都给页面一个新的 visit。 */
let visits = 0;

/** 启动时按安装情况显示侧栏「AI 工具」分组和各工具入口。 */
export async function initReactAITools() {
  let list: { id: string; installed: boolean }[] = [];
  try {
    list = await aiApi.listTools();
  } catch {
    list = [];
  }
  for (const info of list) {
    const btn = document.querySelector<HTMLElement>(`.nav-item[data-tool="${info.id}"]`);
    if (btn) btn.classList.toggle("hidden", !info.installed);
  }
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
  mountAITool(host, toolId, toolName, ++visits);
}
