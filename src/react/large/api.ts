import { Channel } from "mygo-runtime";
import { Cleaner } from "../../mygo";
import type { LargeScanEvent } from "../../mygo";

/** 大文件页用到的全部 Cleaner 调用集中在此，组件不直接碰桥。 */
export const largeApi = {
  /** 启动流式扫描；返回 Channel 与完成 Promise。 */
  startScan(threshold: number): { ch: Channel<LargeScanEvent>; done: Promise<void> } {
    const ch = new Channel<LargeScanEvent>();
    const done = Cleaner.largeScan(threshold, ch);
    return { ch, done };
  },
  dirChildren: (path: string) => Cleaner.dirChildren(path),
  trashPath: (path: string) => Cleaner.trashPath(path),
  cleanPaths: (paths: string[]) => Cleaner.cleanPaths(paths),
  reveal: (path: string) => Cleaner.revealInFinder(path),
};
