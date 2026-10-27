// App 图标的缓存：同一个 .app 整个会话只向 Go 要一次，几个页面共用。不依赖 DOM / React，可单测。
import { iconApi } from "./api";

const icons = new Map<string, string>(); // .app 路径 → data URL；"" 表示没有图标
const pending = new Map<string, Promise<string>>();
let ask: (path: string) => Promise<string> = (path) => iconApi.appIcon(path);

/** 只有 .app 包才有图标可要：命令行、系统进程和 Windows 的路径都用首字母。 */
export function hasAppIcon(path: string): boolean {
  return path.startsWith("/") && /\.app\/?$/i.test(path);
}

/** 已经知道的图标（"" 表示没有）；还没要到时为 undefined。 */
export function peekIcon(path: string): string | undefined {
  return icons.get(path);
}

/** 要一个图标：同一个路径同时只问一次；出错按没有图标记下，不再重试。 */
export function loadIcon(path: string): Promise<string> {
  const known = icons.get(path);
  if (known !== undefined) return Promise.resolve(known);
  let p = pending.get(path);
  if (!p) {
    p = ask(path).catch(() => "").then((url) => {
      icons.set(path, url);
      pending.delete(path);
      return url;
    });
    pending.set(path, p);
  }
  return p;
}

/** 测试用：换掉向 Go 要图标的函数，清空缓存。 */
export function resetIconsForTest(fn: (path: string) => Promise<string>): void {
  icons.clear();
  pending.clear();
  ask = fn;
}
