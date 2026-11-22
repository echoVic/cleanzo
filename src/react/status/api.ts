import { Channel } from "mygo-runtime";
import { Cleaner } from "../../mygo";
import type { JunkCleanEvent, MachineInfo, SafeSpace, StatusSample, SystemInfo } from "../../mygo";

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
  /** 磁盘不足时量一次：扫描会自己勾上、直接删除的那些行现在能腾出多少。只量，不删。 */
  safeSpace: (): Promise<SafeSpace> => Cleaner.safeSpace(),
  /** 清掉量到的那些行；事件和垃圾清理页的清理流一样。 */
  cleanSafe(ids: string[], measuredAt: number): { ch: Channel<JunkCleanEvent>; done: Promise<void> } {
    const ch = new Channel<JunkCleanEvent>();
    const done = Cleaner.cleanSafeSpace(ids, measuredAt, ch);
    return { ch, done };
  },
};
