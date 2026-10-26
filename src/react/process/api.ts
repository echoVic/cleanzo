import { Cleaner } from "../../mygo";
import type { ProcessSample } from "../../mygo";

/** 进程页用到的 Cleaner 调用集中在此，组件不直接碰桥。 */
export const processApi = {
  /** 每 2 秒一条采样（第一条约 1 秒后）；Channel 关闭即 Go 侧取消 ctx，不再读进程、不再调用 ps。 */
  watch: (ch: import("mygo-runtime").Channel<ProcessSample>): Promise<void> => Cleaner.watchProcesses(ch),
  /** 正常退出；false = 5 秒后还在运行。 */
  quitApp: (path: string): Promise<boolean> => Cleaner.quitApp(path),
  /** 强制退出：只在正常退出没成功、用户确认后调用。 */
  forceQuitApp: (path: string): Promise<boolean> => Cleaner.forceQuitApp(path),
};
