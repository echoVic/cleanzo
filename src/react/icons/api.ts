import { Cleaner } from "../../mygo";

/** App 图标的桥调用集中在此，组件不直接碰桥。 */
export const iconApi = {
  /** .app 的图标（80 px PNG 的 data URL）；没有时为 ""。 */
  appIcon: (path: string): Promise<string> => Cleaner.appIcon(path),
};
