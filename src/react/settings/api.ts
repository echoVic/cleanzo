import { Cleaner } from "../../mygo";
import type { Settings, SystemInfo, AppInfo, GrowthConfigPatch, GrowthState } from "../../mygo";

/** 设置页用到的全部 Cleaner 调用集中在此，组件不直接碰桥。 */
export const settingsApi = {
  getSettings: (): Promise<Settings> => Cleaner.getSettings(),
  saveSettings: (s: Settings): Promise<void> => Cleaner.saveSettings(s),
  growthState: (): Promise<GrowthState> => Cleaner.growthState(),
  /** 只发改动的字段（见 growthApi.saveConfig）。 */
  saveGrowthConfig: (patch: GrowthConfigPatch): Promise<void> => Cleaner.saveGrowthConfig(patch),
  pickFolder: (): Promise<string> => Cleaner.pickFolder(),
  fullDiskAccess: (): Promise<boolean> => Cleaner.fullDiskAccess(),
  openFullDiskAccessSettings: (): Promise<void> => Cleaner.openFullDiskAccessSettings(),
  checkForUpdates: (): Promise<void> => Cleaner.checkForUpdates(),
  info: (): Promise<SystemInfo> => Cleaner.info(),
  listApps: (): Promise<AppInfo[]> => Cleaner.listApps(),
};
