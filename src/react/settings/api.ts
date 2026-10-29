import { Cleaner, events } from "../../mygo";
import type { Settings, SystemInfo, AppInfo, GrowthConfigPatch, GrowthSummary, MenuBarState } from "../../mygo";

/** 设置页用到的全部 Cleaner 调用集中在此，组件不直接碰桥。 */
export const settingsApi = {
  getSettings: (): Promise<Settings> => Cleaner.getSettings(),
  saveSettings: (s: Settings): Promise<void> => Cleaner.saveSettings(s),
  /** 只读设置和状态，不读记录。 */
  growthSummary: (): Promise<GrowthSummary> => Cleaner.growthSummary(),
  /** 只发改动的字段（见 growthApi.saveConfig）。 */
  saveGrowthConfig: (patch: GrowthConfigPatch): Promise<void> => Cleaner.saveGrowthConfig(patch),
  /** 菜单栏读数：显示哪些、这台 Mac 能显示哪些（没有电池时不含电池）。 */
  menuBar: (): Promise<MenuBarState> => Cleaner.menuBar(),
  /** 整份列表发给 Go：按固定顺序存、立即生效；读数全关且后台监控关着时，Go 顺带取消开机启动。 */
  saveMenuBar: (items: string[]): Promise<void> => Cleaner.saveMenuBar(items),
  /** 别处改了读数（如菜单栏的设置）；返回取消订阅。 */
  onMenuBarUpdated: (fn: () => void): (() => void) => events.menuBarUpdated.on(() => fn()),
  pickFolder: (): Promise<string> => Cleaner.pickFolder(),
  fullDiskAccess: (): Promise<boolean> => Cleaner.fullDiskAccess(),
  openFullDiskAccessSettings: (): Promise<void> => Cleaner.openFullDiskAccessSettings(),
  checkForUpdates: (): Promise<void> => Cleaner.checkForUpdates(),
  info: (): Promise<SystemInfo> => Cleaner.info(),
  listApps: (): Promise<AppInfo[]> => Cleaner.listApps(),
};
