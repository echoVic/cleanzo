import { Channel } from "mygo-runtime";
import { Cleaner } from "../../mygo";
import type { DiskStat, FullScanEvent } from "../../mygo";

/** 全盘扫描页用到的全部 Cleaner 调用集中在此，组件不直接碰桥。 */
export const fullscanApi = {
  /** 启动流式全盘扫描；返回 Channel 与完成 Promise。 */
  startScan(): { ch: Channel<FullScanEvent>; done: Promise<void> } {
    const ch = new Channel<FullScanEvent>();
    const done = Cleaner.fullScan(ch);
    return { ch, done };
  },
  /** 磁盘容量（不依赖目录扫描）。 */
  disk: (): Promise<DiskStat> => Cleaner.disk(),
  dirChildren: (path: string) => Cleaner.dirChildren(path),
  dirSize: (path: string) => Cleaner.dirSize(path),
  trashPath: (path: string) => Cleaner.trashPath(path),
  cleanPaths: (paths: string[]) => Cleaner.cleanPaths(paths),
  reveal: (path: string) => Cleaner.revealInFinder(path),
};
