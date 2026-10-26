// 状态页的增长提示：监控开着、近 7 天有提醒时显示最大的一条（spec §六）。
import type { GrowthAlert, GrowthState } from "../../mygo";

/** 取最大的一条；跳过忽略项和崩溃转储提示已覆盖的同一 App 的 Crashpad 增长。 */
export function growthHint(s: GrowthState | null, crashApps: string[]): GrowthAlert | null {
  if (!s || !s.supported || !s.config.enabled) return null;
  const ignored = new Set(s.config.ignored);
  const crashed = new Set(crashApps.map((a) => a.toLowerCase()));
  return [...s.alerts]
    .sort((a, b) => b.bytes - a.bytes)
    .find((a) => !ignored.has(a.key) && !(crashed.has(a.name.toLowerCase()) && /[\\/]Crashpad([\\/]|$)/i.test(a.folder))) ?? null;
}
