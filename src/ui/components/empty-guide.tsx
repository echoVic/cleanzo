import * as React from "react";

/**
 * 插画版闲置引导（旧版 large / fullscan / orphans 共用）：虚线圆环 + 主题线稿，
 * 与 home-axis 数字版 IdleHero 互斥。DOM 逐字复刻旧版 renderIdleScanPages。
 */
export interface EmptyGuideProps {
  /** linearGradient id（eg-large-grad / eg-fs-grad / eg-orphans-grad）。 */
  gradId: string;
  title: string;
  desc: string;
  ctaText: string;
  onCta: () => void;
  /** SVG <g> 内的主题线稿元素。 */
  children: React.ReactNode;
}

export function EmptyGuide({ gradId, title, desc, ctaText, onCta, children }: EmptyGuideProps) {
  return (
    <div className="empty-guide">
      <svg className="eg-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <circle cx="12" cy="12" r="10.2" stroke={`url(#${gradId})`} strokeWidth={1.4} strokeDasharray="3.4 2.8" opacity={0.7} />
        <g stroke={`url(#${gradId})`} strokeWidth={1.4} strokeLinecap="round" strokeLinejoin="round">
          {children}
        </g>
        <defs>
          <linearGradient id={gradId} x1="0" y1="0" x2="24" y2="24">
            <stop offset="0" stopColor="#0fb986" />
            <stop offset="1" stopColor="#0a9e8e" />
          </linearGradient>
        </defs>
      </svg>
      <h3>{title}</h3>
      <p>{desc}</p>
      <button className="btn primary big" type="button" onClick={onCta}>{ctaText}</button>
    </div>
  );
}
