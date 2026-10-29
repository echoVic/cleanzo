// 首页卡片和菜单栏面板共用的 30 点面积曲线：新读数到来时整条左移一格（设计 6.3）。
import * as React from "react";
import { dur, EASE, play } from "../../ui/motion/motion";
import { SPARK_POINTS, isSparkShift } from "./format";

const SPARK_STEP = 120 / (SPARK_POINTS - 1);
/** 平移时 svg 多画一格：宽 120 + 一格，平移一格占 svg 自身宽度的百分比。 */
const SPARK_WIDE = 120 + SPARK_STEP;
const SPARK_SHIFT_PCT = (SPARK_STEP / SPARK_WIDE) * 100;
/** 曲线配色：CPU 用主题色，GPU 紫、网络蓝（与内存条的联动、App 两色一致）；内存面板按内存压力用橙、红。 */
const SPARK_TONE = {
  accent: { stroke: "var(--accent)", fill: "var(--accent-soft)", fillOpacity: undefined },
  gpu: { stroke: "var(--seg-wired)", fill: "var(--seg-wired)", fillOpacity: 0.14 },
  net: { stroke: "var(--seg-app)", fill: "var(--seg-app)", fillOpacity: 0.14 },
  warn: { stroke: "var(--warn)", fill: "var(--warn)", fillOpacity: 0.16 },
  crit: { stroke: "var(--danger)", fill: "var(--danger)", fillOpacity: 0.14 },
} as const;

export type SparkTone = keyof typeof SPARK_TONE;

/** 30 点面积曲线，纵轴 0–max（默认 0–100%）；重开采样时缓冲清空，不足 2 点不画。 */
export function SparkSvg({ hist, max = 100, tone = "accent" }: { hist: number[]; max?: number; tone?: SparkTone }) {
  const svg = React.useRef<SVGSVGElement>(null);
  const prev = React.useRef<number[]>(hist);
  const ms = dur("spark");
  // 同一次采样保留同一组点，悬停等重渲染不打断平移。
  const { shift, pts } = React.useMemo(() => {
    const shift = ms > 0 && isSparkShift(prev.current, hist);
    // 平移：多画最左边那个旧点，整组左移一格后正好接上（设计 6.3）
    return { shift, pts: shift ? [prev.current[0]!, ...hist] : hist };
  }, [hist, ms]);
  React.useLayoutEffect(() => {
    prev.current = hist;
    const el = svg.current;
    if (!el) return;
    el.getAnimations().forEach((a) => a.cancel());
    // 移动整张 svg 而不是里面的 <g>：WebKit 把它交给合成器，平移时网页不用每帧重排重画
    if (shift) void play(el, [{ transform: "translateX(0)" }, { transform: `translateX(-${SPARK_SHIFT_PCT}%)` }], dur("spark"), EASE.out, { fill: "forwards" });
  }, [hist, shift]);
  if (pts.length < 2) return null;
  const top = max > 0 ? max : 100;
  const y = (v: number) => (28 - (Math.max(0, Math.min(top, v)) / top) * 26).toFixed(1);
  const c = SPARK_TONE[tone];
  const line = `M${pts.map((v, i) => `${(i * SPARK_STEP).toFixed(1)},${y(v)}`).join(" L")}`;
  const area = `${line} L${((pts.length - 1) * SPARK_STEP).toFixed(1)},28 L0,28 Z`;
  return (
    <div className="st-spark" aria-hidden="true">
      <svg ref={svg} viewBox={`0 0 ${SPARK_WIDE.toFixed(2)} 28`} preserveAspectRatio="none" style={{ width: `${((SPARK_WIDE / 120) * 100).toFixed(3)}%` }}>
        <path fill={c.fill} fillOpacity={c.fillOpacity} d={area} />
        <path fill="none" stroke={c.stroke} strokeWidth={1.5} strokeLinejoin="round" d={line} />
      </svg>
    </div>
  );
}
