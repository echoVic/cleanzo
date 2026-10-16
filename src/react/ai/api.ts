import { Cleaner } from "../../mygo";
import type { AIScanResult } from "../../mygo";

export const aiApi = {
  listTools: () => Cleaner.listAITools(),
  scanTool: (id: string): Promise<AIScanResult> => Cleaner.scanAITool(id),
  clean: (selections: { tool: string; part: string; groups: string[] }[]) => Cleaner.cleanAITools(selections),
  reveal: (path: string) => Cleaner.revealInFinder(path),
};
