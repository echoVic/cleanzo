import type { Channel } from "mygo-runtime";
import { Cleaner, events } from "../mygo";
import type { GrowthState, MachineInfo, MenuBarTarget, ProcessSample, StatusSample, SystemInfo } from "../mygo";

/** 菜单栏面板用到的 Cleaner 调用和事件集中在此，组件不直接碰桥。 */
export const panelApi = {
  /** 这个面板的读数：第一条约半秒后，之后每 2 秒一条；关通道 Go 就停。 */
  watch: (item: string, ch: Channel<StatusSample>): Promise<void> => Cleaner.watchMenuBarPanel(item, ch),
  /** 「进程」页的进程读数：CPU、内存面板的诊断和列表。 */
  watchProcesses: (ch: Channel<ProcessSample>): Promise<void> => Cleaner.watchProcesses(ch),
  machineInfo: (): Promise<MachineInfo> => Cleaner.machineInfo(),
  info: (): Promise<SystemInfo> => Cleaner.info(),
  growthState: (): Promise<GrowthState> => Cleaner.growthState(),
  /** 量完一次、增长设置变了；返回取消订阅。 */
  onGrowthUpdated: (fn: () => void): (() => void) => events.growthUpdated.on(() => fn()),
  /** 正常退出；false = 5 秒后还在运行。 */
  quitApp: (path: string): Promise<boolean> => Cleaner.quitApp(path),
  /** 强制退出：只在正常退出没成功、点了两下之后调用。 */
  forceQuitApp: (path: string): Promise<boolean> => Cleaner.forceQuitApp(path),
  /** 退出等待时为 true：点到别处面板也不关（设计 §四.5）。 */
  hold: (on: boolean): Promise<void> => Cleaner.menuBarHold(on),
  /** 关上面板，在主窗口打开 target 那一页。 */
  open: (target: MenuBarTarget): Promise<void> => Cleaner.menuBarOpen(target),
  /** 在面板里 x、y 处（页面左上角起的点）弹出 Cleanzo 的菜单；选了一行，面板随后关上。 */
  menu: (x: number, y: number): Promise<void> => Cleaner.menuBarMenu(x, y),
};
