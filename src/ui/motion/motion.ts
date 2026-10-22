// 动效基础：时长、曲线、当前能不能动。时长与 style.css 的 --t-* 令牌一致（设计第四节）。
export const DUR = { press: 90, fade: 120, state: 180, layout: 240, move: 360, tick: 420, spark: 450 } as const;
export type DurKey = keyof typeof DUR;

export const EASE = {
  out: "cubic-bezier(0.16, 1, 0.3, 1)",
  move: "cubic-bezier(0.2, 0, 0, 1)",
  in: "cubic-bezier(0.3, 0, 0.8, 0.15)",
} as const;

/** full 完整动效；reduced 只留淡入淡出；none 没有 Web Animations（bun 测试、静态渲染），一切直接到位。 */
export type MotionLevel = "full" | "reduced" | "none";

let forced: MotionLevel | undefined;

/** 测试用：固定动效级别；传 undefined 恢复自动判断。 */
export function setMotionLevelForTest(level: MotionLevel | undefined): void {
  forced = level;
}

/** 当前动效级别。 */
export function motionLevel(): MotionLevel {
  if (forced) return forced;
  if (typeof window === "undefined" || typeof Element === "undefined" || typeof Element.prototype.animate !== "function") return "none";
  if (typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches) return "reduced";
  return "full";
}

/** 这一档在当前级别下的实际时长（ms）：reduced 只保留 fade，none 一律为 0。 */
export function dur(key: DurKey): number {
  const level = motionLevel();
  if (level === "none") return 0;
  if (level === "reduced") return key === "fade" ? DUR.fade : 0;
  return DUR[key];
}

/** 播放一段 Web Animation；时长为 0 或环境不支持时立即完成。 */
export function play(
  el: Element | null | undefined,
  keyframes: Keyframe[],
  ms: number,
  easing: string = EASE.out,
  opts: KeyframeAnimationOptions = {},
): Promise<void> {
  if (!el || !(ms > 0) || typeof el.animate !== "function") return Promise.resolve();
  const a = el.animate(keyframes, { duration: ms, easing, fill: "backwards", ...opts });
  return a.finished.then(() => undefined, () => undefined);
}

/** 平滑滚动；减弱动态效果或不支持时直接跳过去。 */
export function scrollBehavior(): ScrollBehavior {
  return motionLevel() === "full" ? "smooth" : "auto";
}
