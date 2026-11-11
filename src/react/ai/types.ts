import type { AIPart, AIGroup } from "../../mygo";

export type WindowDays = 7 | 30 | 90;

export const offered = (p: AIPart) => p.level !== "protected" && p.level !== "unknown";
export const isSessions = (p: AIPart) => p.level === "sensitive";

/** 用这一项的应用正在运行、且这项不能边跑边清。TRAE 的 IDE 和 CLI 各算各的。 */
export const blockedByRun = (p: AIPart) => p.running && !p.runSafe;

/** 最近 24h 的会话不参与勾选；分组还要在保留窗口之外。 */
export function groupSelectable(p: AIPart, g: AIGroup, windowDays: WindowDays, now: number): boolean {
  if (!isSessions(p)) return g.size > 0;
  const DAY = 86400;
  return g.count > g.recent && now - g.last > windowDays * DAY;
}

/** 这一部分能不能勾：非空、没被运行中的应用锁住；分组的部分还要有一组在当前窗口下可选。 */
export function canPick(p: AIPart, windowDays: WindowDays, now: number): boolean {
  if (p.size <= 0 || blockedByRun(p)) return false;
  return p.groups.length === 0 || p.groups.some((g) => groupSelectable(p, g, windowDays, now));
}
