import { Cleaner } from "../../mygo";
import type { Settings, SystemInfo, AppInfo } from "../../mygo";

/** 设置页用到的全部 Cleaner 调用集中在此，组件不直接碰桥。 */
export const settingsApi = {
  getSettings: (): Promise<Settings> => Cleaner.getSettings(),
  saveSettings: (s: Settings): Promise<void> => Cleaner.saveSettings(s),
  pickFolder: (): Promise<string> => Cleaner.pickFolder(),
  fullDiskAccess: (): Promise<boolean> => Cleaner.fullDiskAccess(),
  openFullDiskAccessSettings: (): Promise<void> => Cleaner.openFullDiskAccessSettings(),
  checkForUpdates: (): Promise<void> => Cleaner.checkForUpdates(),
  info: (): Promise<SystemInfo> => Cleaner.info(),
  listApps: (): Promise<AppInfo[]> => Cleaner.listApps(),
};
