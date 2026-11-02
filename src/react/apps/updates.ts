// 应用页的纯逻辑：筛选、计数、版本标签、占用拆分、增长对应；不依赖 DOM / React，可单测。
import type { AppInfo, AppUpdate, GrowthApp, GrowthSummary, UninstallDetail } from "../../mygo";
import type { DictKey } from "../../ui/i18n-react";
import { growthTags, type GrowthTag } from "../process/format";

export type AppsFilter = "all" | "updates" | "idle" | "leftovers";

/** 「很久没用」：上次打开在 90 天以前，或从没打开过。 */
export const IDLE_DAYS = 90;

export function isIdle(a: AppInfo, nowSec: number): boolean {
  return a.lastOpened === 0 || nowSec - a.lastOpened > IDLE_DAYS * 86400;
}

/** 按筛选取 App；「残留」不列已装 App，由残留列表接管。 */
export function filterByKind(apps: readonly AppInfo[], f: AppsFilter, updates: ReadonlyMap<string, AppUpdate>, nowSec: number): AppInfo[] {
  if (f === "leftovers") return [];
  if (f === "updates") return apps.filter((a) => updates.get(a.id)?.state === "update");
  if (f === "idle") return apps.filter((a) => isIdle(a, nowSec));
  return [...apps];
}

export function updateCount(updates: ReadonlyMap<string, AppUpdate>): number {
  let n = 0;
  for (const u of updates.values()) if (u.state === "update") n++;
  return n;
}

/** 检查进度：done 是已有结果的 App 数，finished 是收到 __done。 */
export interface CheckProgress { done: number; total: number; finished: boolean }

/** 行上的版本标签：只有有新版本时才有。 */
export function versionTag(u: AppUpdate | undefined): string | null {
  return u && u.state === "update" ? `${u.current} → ${u.latest}` : null;
}

export function sourceKey(src: string): "apps.src.mas" | "apps.src.brew" | "apps.src.sparkle" | "apps.src.electron" | null {
  switch (src) {
    case "mas": return "apps.src.mas";
    case "brew": return "apps.src.brew";
    case "sparkle": return "apps.src.sparkle";
    case "electron": return "apps.src.electron";
    default: return null;
  }
}

const REASONS = ["no-source", "bad-url", "network", "rate-limited", "bad-feed", "uncomparable", "not-found"] as const;

/** 无法检查的原因文案键；新原因码先当网络错误显示。 */
export function reasonKey(reason: string): DictKey {
  const r = (REASONS as readonly string[]).includes(reason) ? reason : "network";
  return `apps.reason.${r}` as DictKey;
}

/** 卸载路径列表拆成「App 本体 / 数据 / 缓存」；缓存只算 kind=cache，路径给「清理」用。 */
export interface UsageSplit { app: number; data: number; cache: number; cachePaths: string[] }

export function splitUsage(d: UninstallDetail): UsageSplit {
  const out: UsageSplit = { app: 0, data: 0, cache: 0, cachePaths: [] };
  for (const it of d.items) {
    const size = Math.max(0, it.size);
    if (it.kind === "app") out.app += size;
    else if (it.kind === "cache") { out.cache += size; out.cachePaths.push(it.path); }
    else out.data += size;
  }
  return out;
}

/** 这个 App 在占用增长里的那一行（按安装路径对上）。 */
export function growthFor(app: AppInfo, growth: readonly GrowthApp[]): GrowthApp | null {
  return growth.find((g) => g.appPath !== "" && g.appPath === app.path) ?? null;
}

/** 行上的「7 天 +X GB」小标签：口径和进程页一样，按 bundle ID 对上提醒。 */
export function growthTagFor(app: AppInfo, s: GrowthSummary | null): GrowthTag | null {
  if (!app.bundleId) return null;
  const tags = growthTags(s);
  return tags.get(`app:${app.bundleId}`) ?? tags.get(`macos:${app.bundleId}`) ?? null;
}
