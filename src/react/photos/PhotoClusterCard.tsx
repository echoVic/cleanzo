import * as React from "react";
import { FolderSearch } from "lucide-react";
import type { PhotoCluster, PhotoItem } from "../../mygo";
import { useT } from "../../ui/i18n-react";
import { useFlip } from "../../ui/motion/flip";
import { Leave, useLeaveAnimation, usePresence } from "../../ui/motion/presence";
import { baseName } from "../../shared";
import { human } from "../ai/format";
import type { ListSelection } from "../list/selection";

interface PhotoClusterCardProps {
  cluster: PhotoCluster;
  items: PhotoItem[];
  leaving: boolean;
  index: number;
  selection: ListSelection;
  busy: boolean;
  onToggle(path: string, on: boolean): void;
  onKeepOnly(id: number): void;
  onOpen(path: string): void;
  onReveal(path: string): void;
}

/** 照片簇：整簇收起、单张缩小离场，剩余照片滑到新位置。 */
export function PhotoClusterCard({
  cluster: cl, items, leaving, index, selection, busy, onToggle, onKeepOnly, onOpen, onReveal,
}: PhotoClusterCardProps) {
  const t = useT();
  const cardRef = React.useRef<HTMLDivElement>(null);
  const gridRef = React.useRef<HTMLDivElement>(null);
  useLeaveAnimation(cardRef, leaving);
  const cells = usePresence(items, (it) => it.path, items.length > 0);
  const keys = items.map((it) => it.path);
  const previousKeys = React.useRef(keys);
  const removing = previousKeys.current.some((key) => !keys.includes(key));
  React.useLayoutEffect(() => { previousKeys.current = keys; });
  // 离场格仍占位，等 pop 完成、真正移除后再给剩余照片换位。
  useFlip(gridRef, cells.map((cell) => cell.key), !removing && !cells.some((cell) => cell.leaving));

  return (
    <div
      className="photo-cluster card cz-enter"
      data-cluster={cl.id}
      style={{ "--cz-i": Math.min(index, 6) } as React.CSSProperties}
      ref={cardRef}
      aria-hidden={leaving || undefined}
      inert={leaving || undefined}
    >
      <div className="photo-cluster-head">
        <span className="photo-cluster-title">
          {t("photos.cluster", { id: cl.id })}
          <span className={`photo-badge ${cl.kind === "exact" ? "exact" : "similar"}`}>
            {cl.kind === "exact" ? t("photos.kindExact") : t("photos.kindSimilar")}
          </span>
        </span>
        <span className="photo-cluster-meta muted">
          {t("photos.photoCount", { n: items.length })} · {t("photos.waste", { size: human(cl.waste) })}
        </span>
        <button
          className="btn small ghost"
          type="button"
          disabled={busy}
          onClick={() => onKeepOnly(cl.id)}
        >
          {t("photos.keepOnly")}
        </button>
      </div>
      <div className="photo-grid" ref={gridRef}>
        {cells.map(({ item: it, key, leaving }) => (
          <Leave key={key} leaving={leaving} kind="pop">
            <figure className={`photo-cell${it.keep ? " keep" : ""}`} data-key={it.path} data-photo={it.path}>
              <div className="photo-thumb-wrap">
                <label className="fs-check" title={it.path}>
                  <input
                    type="checkbox"
                    checked={selection.sel.has(it.path)}
                    disabled={busy}
                    onChange={(e) => onToggle(it.path, e.target.checked)}
                  />
                </label>
                {it.keep && <span className="photo-keep-tag">{t("photos.keep")}</span>}
                <img
                  className="photo-thumb"
                  src={it.thumb}
                  alt=""
                  loading="lazy"
                  onClick={() => onToggle(it.path, !selection.sel.has(it.path))}
                />
              </div>
              <figcaption className="photo-meta" title={it.path}>
                <div className="photo-name">{baseName(it.path)}</div>
                <div className="photo-sub muted small">
                  {t("photos.sizeDim", { w: it.width, h: it.height })} · {human(it.size)}
                </div>
              </figcaption>
              <div className="photo-actions">
                <button
                  className="btn small ghost icon"
                  type="button"
                  title={t("photos.viewLarger")}
                  aria-label={t("photos.viewLarger")}
                  data-preview={it.path}
                  onClick={() => onOpen(it.path)}
                >
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
                    <circle cx="10" cy="10" r="6.5" />
                    <path d="m15 15 6 6M7 10h6M10 7v6" />
                  </svg>
                </button>
                <button
                  className="btn small ghost icon"
                  type="button"
                  title={t("fs.reveal")}
                  aria-label={t("fs.reveal")}
                  onClick={() => onReveal(it.path)}
                >
                  <FolderSearch className="h-[15px] w-[15px]" />
                </button>
              </div>
            </figure>
          </Leave>
        ))}
      </div>
    </div>
  );
}
