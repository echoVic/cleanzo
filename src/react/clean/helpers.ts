// 垃圾清理页纯选择 / 状态逻辑：不依赖 DOM / React，可单测。
// 行为规格来自旧 src/junk.ts：三态勾选、默认子项、Keep Latest、分组三态、
// 扫描 / 清理事件归约、确认计划与收尾统计。
import type { CleanResult, JunkItem, JunkSelection, ScanSummary, Settings, SubItem } from "../../mygo";

export const GROUPS = ["system", "apps", "browsers", "updates", "dev", "review", "custom"] as const;

/** app:/custom: 前缀的项名称来自后端 name，其余规则名来自 i18n。 */
export function isDynamicId(id: string): boolean {
  return id.startsWith("app:") || id.startsWith("custom:");
}

/** 行 / 分组显示名：动态项用后端 name，规则项用 i18n 规则名。 */
export function itemTitle(it: JunkItem, ruleName: (id: string) => string): string {
  return isDynamicId(it.id) ? it.name : ruleName(it.id);
}

/** size===-1 是测量中的骨架行；size 0 且未被拒绝的行不显示。 */
export function isVisible(it: JunkItem): boolean {
  return it.size === -1 || it.size > 0 || it.denied;
}

/** 运行中 / 被锁住 / 无大小的行不可选，也永不进入分组全选与总量。 */
export function isSelectable(it: JunkItem): boolean {
  return it.size > 0 && !it.running && !it.denied;
}

export function subsOf(it: JunkItem): SubItem[] {
  return it.subs ?? [];
}

export function hasSubs(it: JunkItem): boolean {
  return subsOf(it).length > 0;
}

// ---- 勾选状态（行 / 子项 / 分组三态）----

export interface JunkSel {
  /** 已勾选的整项 id。 */
  selected: Set<string>;
  /** item id -> 已勾子项 path 集合。 */
  subSel: Map<string, Set<string>>;
}

export function emptySel(): JunkSel {
  return { selected: new Set(), subSel: new Map() };
}

/** 子项默认勾选规则：default 且有大小。 */
export function defaultSubPaths(it: JunkItem): Set<string> {
  return new Set(subsOf(it).filter((s) => s.default && s.size > 0).map((s) => s.path));
}

/** 选中项实际将清理的字节：无子项取整项大小，有子项只算已勾子项。 */
export function chosenSize(it: JunkItem, sel: JunkSel): number {
  if (!isSelectable(it) || !sel.selected.has(it.id)) return 0;
  if (!hasSubs(it)) return it.size;
  const picked = sel.subSel.get(it.id);
  return subsOf(it).filter((s) => picked?.has(s.path)).reduce((n, s) => n + s.size, 0);
}

/** 半选：整项已勾、有子项、已勾子项数在 (0, 有大小子项数) 之间。 */
export function isPartial(it: JunkItem, sel: JunkSel): boolean {
  const count = sel.subSel.get(it.id)?.size ?? 0;
  const eligible = subsOf(it).filter((s) => s.size > 0).length;
  return sel.selected.has(it.id) && hasSubs(it) && count > 0 && count < eligible;
}

/** 整行勾选切换：有子项时全部默认子项勾上 / 清空；清空后整项不勾。 */
export function toggleRow(it: JunkItem, on: boolean, sel: JunkSel): JunkSel {
  if (!isSelectable(it)) return sel;
  const subSel = new Map(sel.subSel);
  const selected = new Set(sel.selected);
  if (hasSubs(it)) {
    subSel.set(it.id, on ? defaultSubPaths(it) : new Set());
    on = (subSel.get(it.id)?.size ?? 0) > 0;
  }
  if (on) selected.add(it.id); else selected.delete(it.id);
  return { selected, subSel };
}

/** 子项勾选切换：未选整项时点单个子项不带入其他默认子项；清空后整项不勾。 */
export function toggleSub(it: JunkItem, path: string, on: boolean, sel: JunkSel): JunkSel {
  const sub = subsOf(it).find((s) => s.path === path);
  if (!sub || sub.size <= 0) return sel;
  const subSel = new Map(sel.subSel);
  const selected = new Set(sel.selected);
  const paths = selected.has(it.id) ? new Set(subSel.get(it.id)) : new Set<string>();
  if (on) paths.add(path); else paths.delete(path);
  subSel.set(it.id, paths);
  if (paths.size) selected.add(it.id); else selected.delete(it.id);
  return { selected, subSel };
}

export type GroupCheck = "unchecked" | "partial" | "checked";

/** 分组三态：按所有可见行算（含运行中、被锁住的），有行没勾就不能全选。 */
export function groupCheckState(group: string, items: JunkItem[], sel: JunkSel): GroupCheck {
  const shown = items.filter((it) => it.group === group && isVisible(it));
  if (shown.length === 0) return "unchecked";
  const picked = shown.filter((it) => sel.selected.has(it.id)).length;
  if (picked === 0) return "unchecked";
  return picked === shown.length ? "checked" : "partial";
}

/** 分组勾选切换：对组内所有行应用 toggleRow（不可行行会被忽略，即组选永不带 running/denied）。 */
export function toggleGroup(group: string, on: boolean, items: JunkItem[], sel: JunkSel): JunkSel {
  let next = sel;
  for (const it of items.filter((x) => x.group === group)) next = toggleRow(it, on, next);
  return next;
}

/** 忽略行后从选择状态移除。 */
export function removeFromSel(sel: JunkSel, id: string): JunkSel {
  const selected = new Set(sel.selected);
  selected.delete(id);
  const subSel = new Map(sel.subSel);
  subSel.delete(id);
  return { selected, subSel };
}

/** 设置页「不再清理」：读改写 ignored 列表（成功后才调用）。 */
export function withIgnored(settings: Settings, id: string): Settings {
  return { ...settings, ignored: [...new Set([...(settings.ignored ?? []), id])] };
}

// ---- 扫描事件归约 ----

export interface ScanState {
  items: JunkItem[];
  /** 还在测量中的行 id（骨架）。 */
  pending: Set<string>;
  sel: JunkSel;
}

/** start 事件：全量 items 进入 pending 骨架。 */
export function scanStart(items: JunkItem[]): ScanState {
  return { items, pending: new Set(items.map((it) => it.id)), sel: emptySel() };
}

/** item 事件：替换行、离开 pending；子项默认勾；整项默认勾规则（无大小子项则不勾）。 */
export function scanItem(prev: ScanState, it: JunkItem): ScanState {
  const items = prev.items.map((old) => (old.id === it.id ? it : old));
  const pending = new Set(prev.pending);
  pending.delete(it.id);
  const subSel = new Map(prev.sel.subSel);
  const selected = new Set(prev.sel.selected);
  subSel.set(it.id, defaultSubPaths(it));
  if (it.default && isSelectable(it) && (!hasSubs(it) || (subSel.get(it.id)?.size ?? 0) > 0)) {
    selected.add(it.id);
  } else {
    selected.delete(it.id);
  }
  return { items, pending, sel: { selected, subSel } };
}

/** 扫描结束（完成 / 出错 / 停止）：丢掉未测量的 buffered 行，只保留 measured 行。 */
export function finalizeScan(prev: ScanState): ScanState {
  return { items: prev.items.filter((it) => !prev.pending.has(it.id)), pending: new Set(), sel: prev.sel };
}

// ---- 清理计划（确认弹层）----

export interface CleanPlan {
  selections: JunkSelection[];
  bytes: number;
  deleteItems: JunkItem[];
  trashItems: JunkItem[];
}

/** 由当前勾选生成清理计划；没有勾中字节时返回 null（弹层不出现）。 */
export function buildCleanPlan(items: JunkItem[], sel: JunkSel): CleanPlan | null {
  const chosen = items.filter((it) => chosenSize(it, sel) > 0);
  if (chosen.length === 0) return null;
  return {
    selections: chosen.map((it) => (hasSubs(it)
      ? { id: it.id, paths: [...(sel.subSel.get(it.id) ?? [])] }
      : { id: it.id })),
    bytes: chosen.reduce((n, it) => n + chosenSize(it, sel), 0),
    deleteItems: chosen.filter((it) => !it.trash),
    trashItems: chosen.filter((it) => it.trash),
  };
}

// ---- hero 派生数字 ----

export interface HeroTotals {
  /** 可清且非 review 的总大小（大数字「共发现」）。 */
  foundBytes: number;
  /** review 组可清大小（副行「另有…需要你确认」）。 */
  reviewBytes: number;
  /** 非 review 已勾字节（副行「已选择垃圾」）。 */
  selectedBytes: number;
}

export function heroTotals(items: JunkItem[], sel: JunkSel): HeroTotals {
  const eligible = items.filter(isSelectable);
  return {
    foundBytes: eligible.filter((it) => it.group !== "review").reduce((n, it) => n + it.size, 0),
    reviewBytes: eligible.filter((it) => it.group === "review").reduce((n, it) => n + it.size, 0),
    selectedBytes: eligible.filter((it) => it.group !== "review").reduce((n, it) => n + chosenSize(it, sel), 0),
  };
}

// ---- 清理行态 ----

export type RowState = {
  state: "waiting" | "cleaning" | "done" | "stopped" | "skipped";
  result?: CleanResult;
};

/** 宽松 t 函数类型（组件 useT 与 i18n.t 都兼容）。 */
import type { DictKey } from "../../ui/i18n-react";
export type TFn = (key: DictKey, vars?: Record<string, string | number>) => string;

export interface RowNote {
  kind: RowState["state"];
  /** 主文案：waiting/cleaning/stopped/skipped 的标签文字，或 done 的「已释放…/已移到…/已完成」。 */
  text: string;
  /** done 且 result.skipped 时的跳过原因（warn 标签）。 */
  skipWarn?: string;
  /** done 时 failed 数组长度。 */
  failedCount?: number;
}

/** 行清理状态注记（旧 stateNote 的纯函数版，组件负责排版标签）。 */
export function rowStateNote(row: RowState, t: TFn, human: (n: number) => string): RowNote {
  if (row.state === "waiting") return { kind: "waiting", text: t("cat.waiting") };
  if (row.state === "cleaning") return { kind: "cleaning", text: t("cat.cleaning") };
  if (row.state === "stopped") return { kind: "stopped", text: t("cat.stopped") };
  if (row.state === "skipped") return { kind: "skipped", text: t("cat.notCleaned") };
  const res = row.result!;
  const parts = [
    res.freed > 0 ? t("cat.freed", { size: human(res.freed) }) : "",
    res.trashed > 0 ? t("cat.trashed", { size: human(res.trashed) }) : "",
  ].filter(Boolean);
  return {
    kind: "done",
    text: parts.join(t("phrase.sep")) || t("cat.cleanedNothing"),
    skipWarn: res.skipped ? t(`junk.skip.${res.skipped}` as never) : undefined,
    failedCount: res.failed?.length ? res.failed.length : undefined,
  };
}

/** 开始扫描的前置守卫：清理中 / 扫描中 / ignoring 锁时不响应。 */
export function canStartScan(phase: string, ignoring: boolean): boolean {
  return phase !== "cleaning" && phase !== "scanning" && !ignoring;
}

/** 清理事件归约：start 行进入 cleaning；done 落结果。checking/removed 只动进度。 */
export function applyCleanEvent(rows: Map<string, RowState>, ev: { kind: string; id?: string; result?: CleanResult }): Map<string, RowState> {
  const next = new Map(rows);
  if (ev.kind === "start" && ev.id) next.set(ev.id, { state: "cleaning" });
  else if (ev.kind === "done" && ev.id && ev.result) next.set(ev.id, { state: "done", result: ev.result });
  return next;
}

/** 清理收尾（完成 / 停止 / 失败）：正在清理→stopped，等待中→skipped；标记是否有行未完成。 */
export function finalizeCleanRows(rows: Map<string, RowState>): { rows: Map<string, RowState>; stopped: boolean } {
  const next = new Map(rows);
  let stopped = false;
  for (const [id, row] of next) {
    if (row.state === "cleaning") { next.set(id, { ...row, state: "stopped" }); stopped = true; }
    else if (row.state === "waiting") { next.set(id, { ...row, state: "skipped" }); stopped = true; }
  }
  return { rows: next, stopped };
}

export interface CleanTotals {
  freed: number;
  trashed: number;
  inUse: number;
  /** 未完全清理的行名（失败或被跳过）。 */
  failedNames: string[];
}

export function cleanTotals(rows: Map<string, RowState>): CleanTotals {
  let freed = 0, trashed = 0, inUse = 0;
  const failedNames: string[] = [];
  for (const row of rows.values()) {
    if (!row.result) continue;
    freed += row.result.freed;
    trashed += row.result.trashed;
    inUse += row.result.inUse;
    if (row.result.failed?.length || row.result.skipped) failedNames.push(row.result.name);
  }
  return { freed, trashed, inUse, failedNames };
}

/** 完成态进度条宽度：正常到底 100%；停止 / 失败 / 出错按已清理比例。 */
export function cleanBarWidth(bytes: number, total: number, stopped: boolean, failed: boolean, error: boolean): number {
  if (stopped || failed || error) return total > 0 ? Math.min(100, (bytes / total) * 100) : 0;
  return 100;
}

// ---- apps 组截断 ----

export function splitApps(members: JunkItem[], showAll: boolean): { shown: JunkItem[]; rest: JunkItem[] } {
  if (showAll) return { shown: members, rest: [] };
  return { shown: members.slice(0, 15), rest: members.slice(15) };
}

/** device-support 行内「保留最新」标签：非默认子项在未显勾时才提示。 */
export function keepLatestVisible(it: JunkItem, sub: SubItem, sel: JunkSel): boolean {
  return it.id === "review.device-support" && !sub.default && !(sel.selected.has(it.id) && sel.subSel.get(it.id)?.has(sub.path));
}

/** 自定义目录 / 分组禁用 / 忽略列表来自 Settings；summary 随 ignore 自增。 */
export function withIgnoredSummary(summary: ScanSummary | null): ScanSummary | null {
  return summary ? { ...summary, ignored: summary.ignored + 1 } : summary;
}
