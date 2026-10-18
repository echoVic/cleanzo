import type { Skill, SkillCopy, SkillSource } from "../../mygo";

export type SkillFilter = "all" | "idle" | "issues" | "updates" | "unobserved";
export type SkillSort = "last" | "count" | "size" | "name";

const DAY = 86400;

/** 一份副本是否是真正的目录（不是软链）。 */
export const isFolder = (c: SkillCopy) => c.kind === "dir" || c.kind === "invalid";

/** 筛选：久未使用 = 后端判定的 idle；有问题 = 有任何 issue；无数据 = 没有可读的调用记录。 */
export function matchFilter(s: Skill, f: SkillFilter): boolean {
  switch (f) {
    case "idle": return s.idle;
    case "issues": return s.issues.length > 0;
    case "unobserved": return !s.observed;
    default: return true;
  }
}

export function matchQuery(s: Skill, q: string): boolean {
  const k = q.trim().toLowerCase();
  if (!k) return true;
  return s.name.toLowerCase().includes(k) || s.description.toLowerCase().includes(k);
}

/**
 * 排序。「上次调用」把久未使用和从没出现过的排前面：先是有数据但没调用过的，
 * 再按上次调用由旧到新；没有数据的排最后，不和「没用过」混在一起。
 */
export function sortSkills(list: Skill[], by: SkillSort): Skill[] {
  const out = [...list];
  const rank = (s: Skill) => (!s.observed ? 2 : s.count === 0 ? 0 : 1);
  out.sort((a, b) => {
    switch (by) {
      case "count": return b.count - a.count || a.name.localeCompare(b.name);
      case "size": return b.size - a.size || a.name.localeCompare(b.name);
      case "name": return a.name.localeCompare(b.name);
      default:
        return rank(a) - rank(b) || a.last - b.last || a.name.localeCompare(b.name);
    }
  });
  return out;
}

/** 记录覆盖的天数（向下取整，至少 1）；since 为 0 时返回 0。 */
export function windowDays(since: number, now: number): number {
  if (!since) return 0;
  return Math.max(1, Math.floor((now - since) / DAY));
}

/** 「N 天前」；0 表示今天。 */
export function daysAgo(ts: number, now: number): number {
  return Math.max(0, Math.floor((now - ts) / DAY));
}

/**
 * 一个技能名下的所有副本指向的实体目录。软链可能跨名字指向别的技能，
 * 例如 Pi 里的 cz-alias 指向 cz-src；移走这种软链时只移软链本身。
 */
export function skillRealDirs(s: Skill): Set<string> {
  const dirs = new Set<string>();
  for (const c of s.copies) {
    if (isFolder(c)) dirs.add(c.real);
  }
  return dirs;
}

/**
 * 勾选一个技能 = 要移走它的哪些条目。软链按软链移走；目录被别的技能目录
 * 里的软链指着时，那些软链必须一起移走（后端同样会拒绝落单的目录）。
 * 指向另一个被勾选技能的软链不算额外项目，因为那份技能本来就要移走。
 */
export function trashPlan(skills: Skill[], picked: Set<string>): { paths: string[]; links: string[]; size: number } {
  const pickedSkills = skills.filter((s) => picked.has(s.name));
  const pickedNames = picked;
  const paths = new Set<string>();
  const links = new Set<string>();
  let size = 0;
  for (const s of pickedSkills) {
    const ownReal = skillRealDirs(s);
    const counted = new Set<string>();
    for (const c of s.copies) {
      if (!c.removable) continue;
      paths.add(c.path);
      if (isFolder(c)) {
        if (!counted.has(c.real)) { counted.add(c.real); size += c.size; }
        for (const l of c.linkedBy) {
          paths.add(l);
          // 该软链属于另一个技能名下、且那个技能没被勾选时，才是额外项目。
          if (!isDirectPick(skills, pickedNames, l)) links.add(l);
        }
      }
    }
    void ownReal;
  }
  return { paths: [...paths], links: [...links], size };
}

/** 软链本身就是被勾选技能的副本时，不再当作「额外一起移走」。 */
function isDirectPick(skills: Skill[], picked: Set<string>, path: string): boolean {
  return skills.some((s) => picked.has(s.name) && s.copies.some((c) => c.path === path));
}

/**
 * 可以合并成软链的副本：同名、内容相同（hash 相等且非空）、不是软链、
 * 不在 ~/.agents 里，也没有别的软链指着它。目标优先用 ~/.agents 里的那份。
 */
export function linkCandidates(s: Skill): { path: string; target: string }[] {
  const folders = s.copies.filter((c) => isFolder(c) && c.kind === "dir" && c.hash);
  const target = folders.find((c) => c.tool === "agents") ?? null;
  if (!target) return [];
  return folders
    .filter((c) => c !== target && c.hash === target.hash && c.real !== target.real && c.removable && c.linkedBy.length === 0)
    .map((c) => ({ path: c.path, target: target.path }));
}

/** 能读到调用记录的来源。 */
export const okSources = (sources: SkillSource[]) => sources.filter((s) => s.status === "ok");
