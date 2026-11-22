// 按目标腾空间：给一个要腾出的量，算出勾哪些行。只勾选，不删除；纯逻辑，可单测。
import type { JunkItem } from "../../mygo";
import {
  defaultSubPaths, emptySel, hasSubs, isSelectable, recoveryOf, subsOf, toggleRow, type JunkSel, type TFn,
} from "./helpers";

export const GiB = 1024 ** 3;

/** 目标档位（GB）。 */
export const GOAL_PRESETS = [5, 10, 20, 50] as const;

/** 自定目标的上限（GB）：再大的数没有意义，也免得算出溢出的字节数。 */
export const GOAL_MAX_GB = 9999;

/** 输入框里的字变成目标（GB）：1 到上限之间的数，别的都不算。 */
export function parseGoal(text: string): number | null {
  const n = Number(text.trim());
  return text.trim() !== "" && Number.isFinite(n) && n >= 1 && n <= GOAL_MAX_GB ? n : null;
}

/** 一行被计划勾上时会清掉多少：有子项的按默认会勾的子项算，和手动勾整行一致。 */
export function planSize(it: JunkItem): number {
  if (!isSelectable(it)) return 0;
  if (!hasSubs(it)) return it.size;
  const paths = defaultSubPaths(it);
  return subsOf(it).filter((s) => paths.has(s.path)).reduce((n, s) => n + s.size, 0);
}

/**
 * 取用的先后分四档，前一档不够才动后一档：
 * 0 扫描自己会勾、直接删除的：清了会自己回来，或者已经用不到；
 * 1 扫描不会勾、直接删除、清了还能回来的：要花时间重新下载；
 * 2 直接删除、不会自己回来的：废纸篓里的东西；
 * 3 先进废纸篓的：清空废纸篓以后才腾出空间。
 */
export function goalTier(it: JunkItem): 0 | 1 | 2 | 3 {
  if (it.trash) return 3;
  const gone = recoveryOf(it) === "gone";
  if (it.default && !gone) return 0;
  return gone ? 2 : 1;
}

/** 同一档里再比：扫描自己会勾的在前，清了能回来的在前。大小和闲置天数不算在「同一类」里。 */
function classKey(it: JunkItem): string {
  return `${goalTier(it)}${it.default ? 0 : 1}${recoveryOf(it) === "gone" ? 1 : 0}`;
}

/** 取用顺序：先比类，再比闲置（越久没动越靠前，不知道的排在知道的后面），再比大小（大的在前）。 */
export function goalOrder(items: JunkItem[]): JunkItem[] {
  return items.filter((it) => planSize(it) > 0).sort((a, b) =>
    Number(classKey(a)) - Number(classKey(b))
    || (b.idleDays ?? -1) - (a.idleDays ?? -1)
    || planSize(b) - planSize(a)
    || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

export interface GoalPlan {
  /** 要腾出的字节。 */
  target: number;
  /** 计划勾上的行，按取用顺序。 */
  ids: string[];
  /** 这些行里直接删除的字节：清完就腾出来。 */
  now: number;
  /** 这些行里先进废纸篓的字节：清空废纸篓后才腾出来。 */
  later: number;
  /** 其中扫描自己不会勾的行数：需要你确认。 */
  look: number;
  /** 够不着目标时，全部能清的加起来是多少；够得着时为 null。 */
  reach: number | null;
}

/**
 * 按目标算出要勾的行。
 * 够得着：按取用顺序一行行取到够为止；最后一行换成同一类里刚好够的最小一行（有闲置天数的类不换，
 * 那里越久没动越先取）；再从最后往前，把去掉以后仍然够的行去掉。
 * 够不着：只勾扫描自己会勾的那些，其余的留给用户选。
 */
export function planGoal(items: JunkItem[], target: number): GoalPlan {
  const order = goalOrder(items);
  const reach = order.reduce((n, it) => n + planSize(it), 0);
  let taken: JunkItem[];
  if (reach < target) {
    taken = order.filter((it) => it.default);
  } else {
    taken = [];
    let left = target;
    for (const it of order) {
      if (left <= 0) break;
      taken.push(it);
      left -= planSize(it);
    }
    const last = taken[taken.length - 1];
    if (last && last.idleDays == null) {
      const need = left + planSize(last);
      const fit = order.slice(order.indexOf(last) + 1)
        .filter((it) => classKey(it) === classKey(last) && it.idleDays == null && planSize(it) >= need)
        .pop();
      if (fit) taken[taken.length - 1] = fit;
    }
    let sum = taken.reduce((n, it) => n + planSize(it), 0);
    for (let i = taken.length - 1; i >= 0; i--) {
      const size = planSize(taken[i]!);
      if (sum - size >= target) {
        sum -= size;
        taken.splice(i, 1);
      }
    }
  }
  return {
    target,
    ids: taken.map((it) => it.id),
    now: taken.filter((it) => !it.trash).reduce((n, it) => n + planSize(it), 0),
    later: taken.filter((it) => it.trash).reduce((n, it) => n + planSize(it), 0),
    look: taken.filter((it) => !it.default).length,
    reach: reach < target ? reach : null,
  };
}

/** 计划对应的勾选：只有计划里的行，有子项的勾上默认子项。 */
export function goalSel(items: JunkItem[], plan: GoalPlan): JunkSel {
  const ids = new Set(plan.ids);
  let sel = emptySel();
  for (const it of items) {
    if (ids.has(it.id)) sel = toggleRow(it, true, sel);
  }
  return sel;
}

/** 目标怎么写：整数不带小数（10 GB），自定的量最多两位小数（2.5 GB）。 */
export function goalLabel(target: number): string {
  return `${Number((target / GiB).toFixed(2))} GB`;
}

/** 计划卡片上的几句话：选了什么、哪些要你确认、哪些先进废纸篓；够不着时说差多少。 */
export function goalPlanLines(plan: GoalPlan, t: TFn, human: (n: number) => string): string[] {
  const size = plan.now + plan.later;
  if (plan.reach === 0) return [t("goal.plan.nothing")];
  if (plan.reach !== null) {
    return [
      t("goal.plan.short", { target: goalLabel(plan.target), reach: human(plan.reach), short: human(plan.target - plan.reach) }),
      size > 0 ? t("goal.plan.shortSafe", { size: human(size) }) : t("goal.plan.shortPick"),
    ];
  }
  return [
    t("goal.plan.done", { target: goalLabel(plan.target), n: plan.ids.length, size: human(size) }),
    plan.look === 0 && plan.later === 0 ? t("goal.plan.safe") : "",
    plan.look > 0 ? t("goal.plan.look", { n: plan.look }) : "",
    plan.later > 0 ? t("goal.plan.later", { size: human(plan.later) }) : "",
  ].filter(Boolean);
}
