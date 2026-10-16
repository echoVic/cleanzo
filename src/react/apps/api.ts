import { Channel } from "mygo-runtime";
import { Cleaner } from "../../mygo";
import type { AppInfo, OrphanItem, UninstallDetail, UninstallResult } from "../../mygo";

/** 应用卸载页用到的全部 Cleaner 调用集中在此，组件不直接碰桥。 */
export const appsApi = {
  listApps: (): Promise<AppInfo[]> => Cleaner.listApps(),
  uninstallDetail: (path: string): Promise<UninstallDetail> => Cleaner.appUninstallDetail(path),
  uninstallApp: (path: string): Promise<UninstallResult> => Cleaner.uninstallApp(path),
  /** 启动残留流式扫描；返回 Channel 与完成 Promise。 */
  startOrphanScan(): { ch: Channel<OrphanItem>; done: Promise<void> } {
    const ch = new Channel<OrphanItem>();
    const done = Cleaner.scanOrphans(ch);
    return { ch, done };
  },
  dirChildren: (path: string) => Cleaner.dirChildren(path),
  trashPath: (path: string) => Cleaner.trashPath(path),
  cleanPaths: (paths: string[]) => Cleaner.cleanPaths(paths),
  reveal: (path: string) => Cleaner.revealInFinder(path),
};
