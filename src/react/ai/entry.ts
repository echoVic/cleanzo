// React AI 页与原生导航的桥接：main.ts 调这里的函数即可，不直接接触 React。
import { mountAITool } from "./mount";
import { aiApi } from "./api";

/** 工具页的页内标签：空间、运行中。 */
export type AITab = "space" | "running";

/** 工具页的打开请求：tab 为 null 时开在「空间」；nonce 变了页面才照它开。 */
export interface AITabRequest {
  tab: AITab | null;
  nonce: number;
}

const containers = new Map<string, HTMLElement>();
/** 每个工具页最近一次显示时的名字和 visit：离开时照这个 visit 重新渲染，页面不当成又进来了一次。 */
const pages = new Map<string, { toolName: string; visit: number }>();
const requests = new Map<string, AITabRequest>();
const noRequest: AITabRequest = { tab: null, nonce: 0 };
/** 屏幕上的工具页；不在 AI 工具视图时为 null。 */
let current: string | null = null;
/** 显示过几次 AI 工具视图：每次显示都给页面一个新的 visit。 */
let visits = 0;

function render(toolId: string, active: boolean) {
  const host = containers.get(toolId);
  const page = pages.get(toolId);
  if (host && page) mountAITool(host, toolId, page.toolName, page.visit, active, requests.get(toolId) ?? noRequest);
}

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

/** 显示一个工具页；之前显示着的另一个工具页随之按 active=false 重新渲染。 */
export function showReactAITool(toolId: string, toolName: string) {
  let host = containers.get(toolId);
  if (!host) {
    host = document.createElement("div");
    host.className = "cz-react-host";
    document.querySelector("#ai-react-root")?.appendChild(host);
    containers.set(toolId, host);
  }
  for (const [id, el] of containers) el.classList.toggle("hidden", id !== toolId);
  if (current !== null && current !== toolId) render(current, false);
  current = toolId;
  pages.set(toolId, { toolName, visit: ++visits });
  render(toolId, true);
}

/** 下次显示这个工具的页面时开在哪个标签；调用方随后切到这个工具的视图。 */
export function requestAITab(toolId: string, tab: AITab) {
  requests.set(toolId, { tab, nonce: (requests.get(toolId)?.nonce ?? 0) + 1 });
}

/** main.ts 切到 AI 工具之外的视图时调用：告诉正显示的工具页它已不在屏幕上。 */
export function leaveReactAITools() {
  if (current === null) return;
  const id = current;
  current = null;
  render(id, false);
}
