import * as React from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import type { PhotoItem } from "../../mygo";
import { useT } from "../../ui/i18n-react";
import { baseName, errText } from "../../shared";
import { human } from "../ai/format";
import { cachedPreview, loadPhotoPreview } from "./api";

interface PhotoViewerProps {
  /** 当前簇内未删除的项。 */
  items: PhotoItem[];
  /** 当前查看项路径。 */
  path: string;
  checked: boolean;
  busy: boolean;
  /** 打开查看器时聚焦的元素：关闭后焦点还原。 */
  opener: HTMLElement | null;
  onNavigate: (path: string) => void;
  onToggle: (on: boolean) => void;
  onClose: () => void;
}

/**
 * 大图查看器：portal 到 body，打开时 .app inert、遮罩点击关闭、
 * Esc 关闭、←/→ 切换、Tab 在面板内循环；大图带内存缓存与并发去重。
 */
export function PhotoViewer({
  items, path, checked, busy, opener, onNavigate, onToggle, onClose,
}: PhotoViewerProps) {
  const t = useT();
  const it = items.find((i) => i.path === path) ?? items[0]!;
  const index = items.findIndex((i) => i.path === it.path);

  const rootRef = React.useRef<HTMLDivElement>(null);
  const closeRef = React.useRef<HTMLButtonElement>(null);
  // 异步加载回包 / 导航动作经 refs 转发，键盘监听只绑一次。
  const onCloseRef = React.useRef(onClose);
  const navRef = React.useRef(onNavigate);
  onCloseRef.current = onClose;
  navRef.current = onNavigate;

  const [imgSrc, setImgSrc] = React.useState<string>(() => cachedPreview(it.path) ?? it.thumb);
  const [loading, setLoading] = React.useState(() => !cachedPreview(it.path));
  const [error, setError] = React.useState<string | null>(null);
  const seqRef = React.useRef(0);

  // 切换查看项：先落缩略图，再异步拉大图；过期回包丢弃。
  React.useEffect(() => {
    const target = it;
    const seq = ++seqRef.current;
    const cached = cachedPreview(target.path);
    setImgSrc(cached ?? target.thumb);
    setError(null);
    setLoading(!cached);
    if (cached) return;
    let alive = true;
    loadPhotoPreview(target.path)
      .then((data) => {
        if (!alive || seq !== seqRef.current) return;
        setImgSrc(data);
        setLoading(false);
      })
      .catch((e) => {
        if (!alive || seq !== seqRef.current) return;
        setError(errText(e));
        setLoading(false);
      });
    return () => { alive = false; };
  }, [it]);

  // 打开：记录 opener、.app inert、聚焦关闭钮；关闭：还原焦点。
  React.useEffect(() => {
    const app = document.querySelector(".app") as HTMLElement | null;
    if (app) app.inert = true;
    closeRef.current?.focus();
    return () => {
      if (app) app.inert = false;
      const targetPath = it.path;
      if (opener && opener.isConnected) {
        opener.focus();
      } else {
        const trigger = document.querySelector<HTMLElement>(
          `[data-preview="${CSS.escape(targetPath)}"]`,
        );
        if (trigger) trigger.focus();
        else document.querySelector<HTMLElement>("[data-ph-scan-btn]")?.focus();
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 键盘：Esc 关闭、←/→ 切换、Tab 在面板内循环。
  React.useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onCloseRef.current();
      } else if (e.key === "ArrowLeft") {
        e.preventDefault();
        const idx = items.findIndex((i) => i.path === it.path);
        const next = items[idx - 1];
        if (next) navRef.current(next.path);
      } else if (e.key === "ArrowRight") {
        e.preventDefault();
        const idx = items.findIndex((i) => i.path === it.path);
        const next = items[idx + 1];
        if (next) navRef.current(next.path);
      } else if (e.key === "Tab") {
        const controls = Array.from(
          rootRef.current?.querySelectorAll<HTMLElement>("button:not(:disabled), input:not(:disabled)") ?? [],
        );
        const first = controls[0];
        const last = controls[controls.length - 1];
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last?.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first?.focus();
        }
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items]);

  return createPortal(
    <div
      id="photo-viewer"
      className="overlay"
      ref={rootRef}
      onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
    >
      <div className="photo-viewer-panel" role="dialog" aria-modal="true" aria-labelledby="pv-name" aria-describedby="pv-meta">
        <header className="photo-viewer-head">
          <h2 id="pv-name" title={it.path}>{baseName(it.path)}</h2>
          <button
            id="pv-close"
            ref={closeRef}
            className="btn small ghost icon"
            type="button"
            title={t("photos.close")}
            aria-label={t("photos.close")}
            onClick={onClose}
          >
            <X width={18} height={18} />
          </button>
        </header>
        <div className="photo-viewer-stage">
          {!error && <img id="pv-image" src={imgSrc} alt={baseName(it.path)} />}
          {loading && (
            <div id="pv-loading" className="photo-viewer-loading" role="status">
              <span className="spin" />
              <span>{t("photos.loading")}</span>
            </div>
          )}
          {error && <p id="pv-error" className="err-inline" role="alert">{error}</p>}
        </div>
        <div className="photo-viewer-info">
          <p id="pv-meta" className="muted small">
            {t("photos.detail", { w: it.width, h: it.height, size: human(it.size) })}
          </p>
          {it.keep && <span id="pv-keep" className="tag ok">{t("photos.keep")}</span>}
        </div>
        <footer className="photo-viewer-controls">
          <label className="check">
            <input
              type="checkbox"
              checked={checked}
              disabled={busy}
              onChange={(e) => onToggle(e.target.checked)}
            />
            <span>{t("photos.selectTrash")}</span>
          </label>
          <div className="photo-viewer-nav">
            <button
              id="pv-prev"
              className="btn small ghost"
              type="button"
              disabled={index <= 0}
              onClick={() => { const n = items[index - 1]; if (n) onNavigate(n.path); }}
            >
              <span aria-hidden="true">←</span> <span>{t("photos.previous")}</span>
            </button>
            <button
              id="pv-next"
              className="btn small ghost"
              type="button"
              disabled={index >= items.length - 1}
              onClick={() => { const n = items[index + 1]; if (n) onNavigate(n.path); }}
            >
              <span>{t("photos.next")}</span> <span aria-hidden="true">→</span>
            </button>
          </div>
        </footer>
      </div>
    </div>,
    document.body,
  );
}
