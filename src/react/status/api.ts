import { Cleaner } from "../../mygo";
import type { MachineInfo, StatusSample, SystemInfo } from "../../mygo";

/** 系统状态页用到的全部 Cleaner 调用集中在此，组件不直接碰桥。 */
export const statusApi = {
  /** 机器信息（首次进入拉一次；失败留空，重进视图重试）。 */
  info: (): Promise<SystemInfo> => Cleaner.info(),
  /** 这台 Mac 是什么：型号、核数、GPU、显示器、SSD（macOS；进入首页读，显示器变化时再读）。 */
  machine: (): Promise<MachineInfo> => Cleaner.machineInfo(),
  /** 每 2 秒一条采样；Channel 关闭即 Go 侧取消 ctx。 */
  watch: (ch: import("mygo-runtime").Channel<StatusSample>): Promise<void> => Cleaner.watchStatus(ch),
  /** 退出应用；返回 false = 5s 超时仍在跑（多半在等未保存内容的确认）。 */
  quitApp: (path: string): Promise<boolean> => Cleaner.quitApp(path),
};
