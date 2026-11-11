import type { AICleanResult, AIPart, AITool } from "../../mygo";
import { errText } from "../../shared";

/** 一个工具的一个部分：页面上的行都由它们合成。 */
export interface PartRow {
  tool: AITool;
  part: AIPart;
}

/** 勾选状态：整块勾的部分，和按组勾的部分各自勾了哪些组。 */
export interface Picks {
  parts: Set<string>;
  groups: Map<string, Set<string>>;
}

export const partKey = (r: PartRow) => `${r.tool.id}:${r.part.id}`;

/**
 * 页面上的三类部分。「缓存」只收可安全清理的：这一行写着「会自动重建」，
 * 输入历史、生成的图片这类删了就没有的内容单独成行（optional），不混在里面。
 * 受保护和未识别的部分后端一律拒绝，只算进「保留不动」。
 */
export function splitParts(tools: AITool[]): { sessions: PartRow[]; caches: PartRow[]; optional: PartRow[]; kept: number } {
  const out = { sessions: [] as PartRow[], caches: [] as PartRow[], optional: [] as PartRow[], kept: 0 };
  for (const tool of tools) {
    for (const part of tool.parts) {
      if (part.level === "sensitive") out.sessions.push({ tool, part });
      else if (part.level === "safe") out.caches.push({ tool, part });
      else if (part.level === "optional") out.optional.push({ tool, part });
      else out.kept += part.size;
    }
  }
  return out;
}

/** 工具在运行时只有 runSafe 的部分能清，其余的勾不上（后端也会拒绝）。 */
const blockedByRun = (part: AIPart) => part.running && !part.runSafe;

/** 一个部分里能勾的组：最近 24 小时写过的留着。 */
const openGroups = (part: AIPart) => part.groups.filter((g) => g.recent === 0).map((g) => g.key);

/** 这一部分有没有能勾的东西。 */
export function pickable(part: AIPart): boolean {
  if (blockedByRun(part)) return false;
  return part.groups.length > 0 ? openGroups(part).length > 0 : part.size > 0;
}

/** 一行的复选框：把它下面每个能勾的部分一起勾上，或一起去掉。 */
export function toggleRows(picks: Picks, rows: PartRow[], on: boolean): Picks {
  const parts = new Set(picks.parts);
  const groups = new Map(picks.groups);
  for (const r of rows) {
    const key = partKey(r);
    if (r.part.groups.length > 0) {
      if (on && pickable(r.part)) groups.set(key, new Set(openGroups(r.part)));
      else groups.delete(key);
    } else if (on && pickable(r.part)) parts.add(key);
    else parts.delete(key);
  }
  return { parts, groups };
}

/** 一行的勾选状态，只看能勾的：全勾、半勾或没勾。 */
export function rowsChecked(picks: Picks, rows: PartRow[]): boolean | "indeterminate" {
  let all = 0;
  let picked = 0;
  for (const r of rows) {
    if (!pickable(r.part)) continue;
    if (r.part.groups.length > 0) {
      const open = openGroups(r.part);
      const keys = picks.groups.get(partKey(r));
      all += open.length;
      picked += open.filter((k) => keys?.has(k)).length;
    } else {
      all++;
      if (picks.parts.has(partKey(r))) picked++;
    }
  }
  if (picked === 0) return false;
  return picked >= all ? true : "indeterminate";
}

/** 勾中的内容实际会移走多少：最近 24 小时写过的组后端会留下，不算。 */
export function pickedBytes(picks: Picks, rows: PartRow[]): number {
  let n = 0;
  for (const r of rows) {
    if (r.part.groups.length > 0) {
      const keys = picks.groups.get(partKey(r));
      if (!keys) continue;
      for (const g of r.part.groups) if (keys.has(g.key) && g.recent === 0) n += g.size;
    } else if (picks.parts.has(partKey(r))) n += r.part.size;
  }
  return n;
}

/** 发给后端的选择：只有这几行里勾中的部分。 */
export function selectionsFor(picks: Picks, rows: PartRow[]): { tool: string; part: string; groups: string[] }[] {
  const out: { tool: string; part: string; groups: string[] }[] = [];
  for (const r of rows) {
    if (r.part.groups.length > 0) {
      const keys = [...(picks.groups.get(partKey(r)) ?? [])];
      if (keys.length > 0) out.push({ tool: r.tool.id, part: r.part.id, groups: keys });
    } else if (picks.parts.has(partKey(r))) {
      out.push({ tool: r.tool.id, part: r.part.id, groups: [] });
    }
  }
  return out;
}

type RelKey = "aispace.rel.today" | "aispace.rel.yesterday" | "aispace.rel.days" | "aispace.rel.months" | "aispace.rel.years";

/** 相对时间：今天 / 昨天 / N 天前 / N 个月前 / N 年前；0 表示不知道。 */
export function relTime(ts: number, now: number, t: (key: RelKey, vars?: Record<string, number>) => string): string {
  if (!ts) return "—";
  const d = Math.floor((now - ts) / 86400);
  if (d <= 0) return t("aispace.rel.today");
  if (d === 1) return t("aispace.rel.yesterday");
  if (d < 31) return t("aispace.rel.days", { n: d });
  if (d < 365) return t("aispace.rel.months", { n: Math.max(1, Math.round(d / 30)) });
  return t("aispace.rel.years", { n: Math.round(d / 365) });
}

/** 一次清理的汇总：释放的字节、正在使用而保留的项数、被跳过或有失败的部分数，以及第一条失败（「路径: 原因」）。 */
export interface CleanReport {
  freed: number;
  kept: number;
  skipped: number;
  failed: string;
}

/** 把模型和部分两次清理的结果合成一份汇总；没发出的那次是 null。 */
export function cleanReport(results: readonly (AICleanResult | null)[]): CleanReport {
  const report: CleanReport = { freed: 0, kept: 0, skipped: 0, failed: "" };
  for (const res of results) {
    if (!res) continue;
    report.freed += res.freed ?? 0;
    for (const r of res.parts ?? []) {
      report.kept += r.kept;
      if (r.skipped || r.failed.length > 0) report.skipped++;
      if (!report.failed && r.failed.length > 0) report.failed = r.failed[0]!;
    }
  }
  return report;
}

type DoneKey = "aispace.done" | "aispace.done.kept" | "aispace.done.skipped";

/**
 * 一条失败「路径: 代码」：路径照原样，代码按当前语言说出来。代码取最后一个「: 」之后的部分，
 * 路径里带「: 」也不受影响；不认识的代码和系统的错误原文照原样显示。
 */
function failureText(entry: string): string {
  const at = entry.lastIndexOf(": ");
  if (at < 0) return errText(entry);
  return `${entry.slice(0, at)}: ${errText(entry.slice(at + 2))}`;
}

/** 清理完成的提示：移走的大小，再加保留和跳过的计数；有失败时附上第一条，说清是哪个文件、为什么。 */
export function cleanToastText(report: CleanReport, t: (key: DoneKey, vars?: Record<string, string | number>) => string, human: (n: number) => string): string {
  const lines = [t("aispace.done", { size: human(report.freed) })];
  if (report.kept > 0) lines.push(t("aispace.done.kept", { n: report.kept }));
  if (report.skipped > 0) lines.push(t("aispace.done.skipped", { n: report.skipped }));
  if (report.failed) lines.push(failureText(report.failed));
  return lines.join(" · ");
}
