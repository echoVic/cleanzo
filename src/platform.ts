// 平台与路径展示集中处理；不参与后端删除安全判断。
let testPlatform: string | undefined;

export function setPlatformForTest(platform: string | undefined): void {
  testPlatform = platform;
}

export function isWindows(): boolean {
  return (testPlatform ?? (typeof window !== "undefined" ? window.mygo?.platform : undefined)) === "win32";
}

export function pathSep(): string {
  return isWindows() ? "\\" : "/";
}

/** childOnly 保留旧列表只缩写主目录子项的行为。 */
export function shortHome(path: string, childOnly = false): string {
  const home = isWindows() ? /^[A-Za-z]:\\Users\\[^\\]+/i : /^\/Users\/[^/]+/;
  const match = path.match(home);
  if (!match || (childOnly && !path.slice(match[0].length).startsWith(pathSep()))) return path;
  return "~" + path.slice(match[0].length);
}

export function splitPath(path: string): string[] {
  return path.split(pathSep()).filter(Boolean);
}

export function baseName(path: string): string {
  return splitPath(path).at(-1) ?? path;
}

export function dirName(path: string): string {
  const sep = pathSep();
  const trimmed = path.replace(isWindows() ? /\\+$/ : /\/+$/, "");
  const i = trimmed.lastIndexOf(sep);
  if (i < 0) return "";
  if (i === 0 || (isWindows() && i === 2 && trimmed[1] === ":")) return trimmed.slice(0, i + 1);
  return trimmed.slice(0, i);
}

export function isWithin(child: string, ancestor: string): boolean {
  if (isWindows()) {
    child = child.toLowerCase();
    ancestor = ancestor.toLowerCase().replace(/\\+$/, "");
  }
  return child === ancestor || child.startsWith(ancestor + pathSep());
}
