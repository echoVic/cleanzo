import { baseName } from "../../platform";
import type { Settings } from "../../mygo";

/** 设置页的 6 个扫描分组，顺序与旧原生页一致。 */
export const GROUPS = ["system", "apps", "browsers", "updates", "dev", "review"] as const;
export type GroupId = (typeof GROUPS)[number];

/** 空数组在 Go 的绑定中可能为 null。 */
export function normalized(s: Settings): Settings {
  return {
    disabledGroups: s.disabledGroups ?? [],
    ignored: s.ignored ?? [],
    custom: s.custom ?? [],
  };
}

/**
 * 勾选状态 = 不在 disabledGroups 中；按新勾选状态返回下一份 disabledGroups。
 */
export function nextDisabledGroups(disabledGroups: string[], group: GroupId, checked: boolean): string[] {
  const next = disabledGroups.filter((g) => g !== group);
  if (!checked) next.push(group);
  return next;
}

/** 目录路径的最后一段（自定义目录空名的回退值）。 */
export function baseNameOf(p: string): string {
  return baseName(p);
}

/** 名称 trim 后为空时回退为目录 baseName。 */
export function resolveFolderName(typed: string, path: string): string {
  return typed.trim() || baseNameOf(path);
}

/** 配置里这个自定义目录已保存的名称；目录已不在配置里时为 null。 */
export function savedFolderName(s: Settings | null, path: string): string | null {
  return s?.custom.find((c) => c.path === path)?.name ?? null;
}

/**
 * ignored 项的展示名：app:<bundleId> 用已安装应用名（查不到保留 ID），
 * 其余规则走 ruleName（未知规则同样保留稳定 ID）。
 */
export function ignoredLabel(
  id: string,
  appNames: ReadonlyMap<string, string>,
  ruleName: (id: string) => string,
): string {
  if (id.startsWith("app:")) return appNames.get(id.slice(4).toLowerCase()) ?? id;
  return ruleName(id);
}

/**
 * 串行保存队列：所有修改排队执行，避免两份完整配置互相覆盖；
 * 单个保存失败不中断后续任务（错误已落进行内行错误态）。
 */
export class SaveQueue {
  private chain: Promise<unknown> = Promise.resolve();

  run(job: () => Promise<void>): Promise<void> {
    const done = this.chain.then(() => job());
    this.chain = done.catch(() => {});
    return done;
  }
}

/** 菜单栏读数的固定顺序，和 Go 的 menuBarItems 一样（设计 2026-10-29 §三.3）。 */
export const MENUBAR_ITEMS = ["cpu", "mem", "gpu", "net", "disk", "temp", "battery"] as const;

/** 勾上或取消一项后的整份列表，按 order 的顺序（页面传 Go 给的 available，默认是固定顺序）；不在 order 里的名字去掉。 */
export function toggleMenuBarItem(items: readonly string[], item: string, on: boolean, order: readonly string[] = MENUBAR_ITEMS): string[] {
  const chosen = new Set(items);
  if (on) chosen.add(item);
  else chosen.delete(item);
  return order.filter((it) => chosen.has(it));
}
