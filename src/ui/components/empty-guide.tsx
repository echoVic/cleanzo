import * as React from "react";

/**
 * 插画版闲置引导：大文件、空间分析和增长监控页没有结果时共用，虚线圆环 + 主题线稿；
 * 清理、AI 工具和技能页的空态用数字版 IdleHero。
 */
export interface EmptyGuideProps {
  /** linearGradient id，每页一个（eg-large-grad / eg-fs-grad / growth-empty）。 */
  gradId: string;
  title: string;
  desc: string;
  ctaText: string;
  onCta: () => void;
  /** CTA 正在进行：按钮转圈、不能再点。 */
  busy?: boolean;
  /** SVG <g> 内的主题线稿元素。 */
  children: React.ReactNode;
}

export function EmptyGuide({ gradId, title, desc, ctaText, onCta, busy, children }: EmptyGuideProps) {
  return (
    <div className="empty-guide">
      <svg className="eg-icon" viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <circle cx="12" cy="12" r="10.2" stroke={`url(#${gradId})`} strokeWidth={1.4} strokeDasharray="3.4 2.8" opacity={0.7} />
        <g stroke={`url(#${gradId})`} strokeWidth={1.4} strokeLinecap="round" strokeLinejoin="round">
          {children}
        </g>
        <defs>
          <linearGradient id={gradId} x1="0" y1="0" x2="24" y2="24">
            <stop offset="0" style={{ stopColor: "var(--accent)" }} />
            <stop offset="1" style={{ stopColor: "var(--accent-deep)" }} />
          </linearGradient>
        </defs>
      </svg>
      <h2>{title}</h2>
      <p>{desc}</p>
      <button className="btn primary big" type="button" onClick={onCta} disabled={busy}>
        {busy && <span className="spin" />}
        {ctaText}
      </button>
    </div>
  );
}
