import type { AIPart, AIGroup } from "../../mygo";

export type Level = "safe" | "optional" | "sensitive" | "unknown" | "protected";
export type WindowDays = 7 | 30 | 90;

export interface ToolViewState {
  result: AIPart[] | null;
  phase: "idle" | "scanning" | "scanned" | "cleaned";
  error: string | null;
  picked: Set<string>;
  pickedGroups: Map<string, Set<string>>;
  expanded: Set<string>;
  showLocked: boolean;
  freed: number | null;
  scannedAt: number;
}

export const offered = (p: AIPart) => p.level !== "protected" && p.level !== "unknown";
export const isSessions = (p: AIPart) => p.level === "sensitive";

/** 最近 24h 的会话不参与勾选；分组还要在保留窗口之外。 */
export function groupSelectable(p: AIPart, g: AIGroup, windowDays: WindowDays, now: number): boolean {
  if (!isSessions(p)) return g.size > 0;
  const DAY = 86400;
  return g.count > g.recent && now - g.last > windowDays * DAY;
}
