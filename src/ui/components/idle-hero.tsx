import * as React from "react";

/**
 * 闲置中轴卡片：逐像素复用原生「垃圾清理」闲置态的 home-axis / ha-* 全局类，
 * 所有迁移页面的闲置态统一走这里，保证与未迁移原生页视觉一致。
 */
export interface IdleHeroProps {
  /** 顶部小标签（原生 hero.ready「可释放空间」）。 */
  label: string;
  /** 大数字位（原生为 "--"）。 */
  value: React.ReactNode;
  /** 大数字旁的小注（原生 clean.idleSub「尚未扫描」）。 */
  sub?: string;
  /** 引导语（原生 ha-guide）。 */
  guide: string;
  /** 主按钮文案。 */
  ctaText: string;
  onCta: () => void;
  /** 带盾牌图标的安全说明（原生 ha-safety）。 */
  safetyText?: string;
  /** 无盾牌的补充说明行（与 ha-safety 同排版）。 */
  note?: string;
  /** 底部 chips。 */
  chips?: string[];
}

export function IdleHero({
  label, value, sub, guide, ctaText, onCta, safetyText, note, chips,
}: IdleHeroProps) {
  return (
    <div className="home-axis">
      <div className="ha-ring" aria-hidden="true">
        <svg
          viewBox="0 0 24 24" width="28" height="28" fill="none"
          stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round"
        >
          <path d="M21 12a9 9 0 1 1-2.64-6.36" />
          <path d="M21 3v6h-6" />
        </svg>
      </div>
      <div className="ha-avail-label">{label}</div>
      <div className="ha-avail num">
        <b>{value}</b>
        {sub && <span>{sub}</span>}
      </div>
      <div className="ha-guide">{guide}</div>
      <button className="ha-cta" type="button" onClick={onCta}>{ctaText}</button>
      {safetyText && (
        <div className="ha-safety">
          <svg
            viewBox="0 0 24 24" width="13" height="13" fill="none"
            stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round"
            aria-hidden="true"
          >
            <path d="M12 2l8 3v6c0 5-3.5 9-8 11-4.5-2-8-6-8-11V5z" />
            <path d="M9 12l2 2 4-4" />
          </svg>
          <span>{safetyText}</span>
        </div>
      )}
      {note && !safetyText && (
        <div className="ha-safety"><span>{note}</span></div>
      )}
      {chips && chips.length > 0 && (
        <div className="ha-chips">{chips.map((c) => <span key={c}>{c}</span>)}</div>
      )}
    </div>
  );
}
