// 把候选文件放进整块磁盘里看：纯计算，可单测。
import type { DiskStat } from "../../mygo";

export interface DiskScale {
  /** 各段占整盘的百分比：别的已用空间、没勾的候选、已勾选；余下的是可用空间。 */
  other: number;
  rest: number;
  picked: number;
  /** 没勾的候选、已勾选各是多少字节（封顶以后的）。 */
  restBytes: number;
  pickedBytes: number;
  /** 清理后大约可用：现在可用的，加上已勾选里直接删除的；先进废纸篓的不算。 */
  after: number;
}

/**
 * found 是量到的候选，picked 是其中已勾选的，pickedNow 是已勾选里直接删除的。
 * 几个读数来自不同时刻，可能对不上：候选按已用封顶，勾选按候选封顶，不画到磁盘外面去。
 * 读不到磁盘时不画。
 */
export function diskScale(disk: DiskStat | null, found: number, picked: number, pickedNow: number): DiskScale | null {
  if (!disk || !(disk.total > 0)) return null;
  const clamp = (n: number, max: number) => Math.min(Math.max(0, n), max);
  const used = clamp(disk.used, disk.total);
  const cand = clamp(found, used);
  const sel = clamp(picked, cand);
  // 百分比留两位小数：条宽用不着更细，也免得写出 7.000000000000001%。
  const percent = (n: number) => Math.round((n / disk.total) * 10000) / 100;
  return {
    other: percent(used - cand),
    rest: percent(cand - sel),
    picked: percent(sel),
    restBytes: cand - sel,
    pickedBytes: sel,
    after: Math.min(disk.total, Math.max(0, disk.free) + clamp(pickedNow, sel)),
  };
}
