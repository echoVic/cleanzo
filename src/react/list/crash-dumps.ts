// 崩溃转储（Crashpad）体积提示：系统状态页和 Codex 页共用。
// 只在打开 Cleanzo / 进入页面时用 du 量一次，不常驻、不弹系统通知。
import { Cleaner } from "../../mygo";
import type { CrashDumpSummary } from "../../mygo";

let cached: Promise<CrashDumpSummary | null> | null = null;

/** 本次打开 Cleanzo 期间只量一次；force 用于清理后重新量。失败返回 null，不打扰用户。 */
export function crashDumps(force = false): Promise<CrashDumpSummary | null> {
  if (!cached || force) cached = Cleaner.crashDumps().catch(() => null);
  return cached;
}

/** 清理崩溃转储后调用，下次进入页面重新量。 */
export function forgetCrashDumps() {
  cached = null;
}

export interface AppDumps {
  app: string;
  size: number;
}

/** 按应用合计（Codex 有两个目录），合计达到阈值的应用，最大的在前。 */
export function oversized(s: CrashDumpSummary | null): AppDumps[] {
  if (!s) return [];
  const by = new Map<string, number>();
  for (const d of s.dirs) by.set(d.app, (by.get(d.app) ?? 0) + d.size);
  return [...by].map(([app, size]) => ({ app, size }))
    .filter((a) => a.size >= s.threshold)
    .sort((a, b) => b.size - a.size);
}

/** Codex 的两个 Crashpad 目录合计。 */
export function codexSize(s: CrashDumpSummary | null): number {
  if (!s) return 0;
  return s.dirs.filter((d) => d.app === "Codex").reduce((n, d) => n + d.size, 0);
}

/** 垃圾清理里的规则 id，与 Go 侧 crashRuleID 一致。 */
export const CRASH_RULE_ID = "sys.crashdumps";

/** 「去垃圾清理」：main.ts 注入，与点侧栏同一条路径。 */
const goClean: { fn: () => void } = { fn: () => {} };
export function setCrashGoClean(fn: () => void) { goClean.fn = fn; }
export function goCleanForCrashDumps() { goClean.fn(); }
