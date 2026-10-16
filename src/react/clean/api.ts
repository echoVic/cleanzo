import { Channel } from "mygo-runtime";
import { Cleaner } from "../../mygo";
import type { JunkCleanEvent, JunkScanEvent, JunkSelection, Settings } from "../../mygo";

/** 垃圾清理页用到的全部 Cleaner 调用集中在此，组件不直接碰桥。 */
export const cleanApi = {
  /** 启动流式垃圾扫描；返回 Channel 与完成 Promise。 */
  startScan(): { ch: Channel<JunkScanEvent>; done: Promise<void> } {
    const ch = new Channel<JunkScanEvent>();
    const done = Cleaner.scanJunk(ch);
    return { ch, done };
  },
  /** 启动流式清理；scannedAt 与扫描结果配对，防止用旧结果清理。 */
  startClean(selections: JunkSelection[], scannedAt: number): { ch: Channel<JunkCleanEvent>; done: Promise<void> } {
    const ch = new Channel<JunkCleanEvent>();
    const done = Cleaner.cleanJunk(selections, scannedAt, ch);
    return { ch, done };
  },
  getSettings: (): Promise<Settings> => Cleaner.getSettings(),
  saveSettings: (s: Settings): Promise<void> => Cleaner.saveSettings(s),
  reveal: (path: string): Promise<void> => Cleaner.revealInFinder(path),
  grantAccess: (): Promise<void> => Cleaner.openFullDiskAccessSettings(),
};
