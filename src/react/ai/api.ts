import { Cleaner } from "../../mygo";
import type { AIScanResult, ClaudeTarget } from "../../mygo";

export const aiApi = {
  listTools: () => Cleaner.listAITools(),
  scanTool: (id: string): Promise<AIScanResult> => Cleaner.scanAITool(id),
  clean: (selections: { tool: string; part: string; groups: string[]; seen?: string[] }[]) => Cleaner.cleanAITools(selections),
  reveal: (path: string) => Cleaner.revealInFinder(path),
};

/** Claude Code 页「运行中」标签用到的 Cleaner 调用。 */
export const runningApi = {
  read: () => Cleaner.claudeRunning(),
  summary: () => Cleaner.claudeRunSummary(),
  endProcs: (session: string, targets: ClaudeTarget[]) => Cleaner.endClaudeProcesses(session, targets),
  endSession: (target: ClaudeTarget, session: string, status: string) => Cleaner.endClaudeSession(target, session, status),
};
