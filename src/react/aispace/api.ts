import { Cleaner } from "../../mygo";

/** AI 空间页要用的后端调用，集中在这里，页面不直接碰 Cleaner。 */
export const aiSpaceApi = {
  scanModels: () => Cleaner.scanAIModels(),
  scanTools: () => Cleaner.scanAITools(),
  listModelSources: () => Cleaner.listAIModelSources(),
  listTools: () => Cleaner.listAITools(),
  cleanModels: (s: { source: string; models: string[] }[]) => Cleaner.cleanAIModels(s),
  cleanParts: (s: { tool: string; part: string; groups: string[] }[]) => Cleaner.cleanAITools(s),
};
