import { Cleaner, events } from "../../mygo";
import type { GrowthConfigPatch, GrowthState, GrowthStatus, UninstallDetail } from "../../mygo";

/** 占用增长页用到的全部 Cleaner 调用与事件集中在此，组件不直接碰桥。 */
export const growthApi = {
  state: (): Promise<GrowthState> => Cleaner.growthState(),
  /** 开始量一次（监控关着也能用）；结果经 growthUpdated 事件通知。 */
  measureNow: (): Promise<void> => Cleaner.growthMeasureNow(),
  /** 只发这次改动的字段，Go 在锁内合并进当前配置：页面拿着旧配置也盖不掉别处的修改。 */
  saveConfig: (patch: GrowthConfigPatch): Promise<void> => Cleaner.saveGrowthConfig(patch),
  reveal: (path: string): Promise<void> => Cleaner.revealInFinder(path),
  uninstallDetail: (path: string): Promise<UninstallDetail> => Cleaner.appUninstallDetail(path),
  /** 量开始、结束或设置变了；返回取消订阅。 */
  onUpdated: (fn: (s: GrowthStatus) => void): (() => void) => events.growthUpdated.on(fn),
};
