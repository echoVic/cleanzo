import * as React from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import type { PhotoItem } from "../../mygo";
import { useT } from "../../ui/i18n-react";
import { baseName, errText } from "../../shared";
import { human } from "../ai/format";
import { cachedPreview, loadPhotoPreview } from "./api";
import { containRect, flyImage } from "../../ui/motion/fly";
import { DUR, EASE, dur, motionLevel, play } from "../../ui/motion/motion";

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
  const panelRef = React.useRef<HTMLDivElement>(null);
  const stageRef = React.useRef<HTMLDivElement>(null);
  const imgRef = React.useRef<HTMLImageElement>(null);
  const itRef = React.useRef(it);
  itRef.current = it;
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
  const prevIndex = React.useRef(index);
  const navDir = React.useRef(0);
  if (prevIndex.current !== index) { navDir.current = index > prevIndex.current ? 1 : -1; prevIndex.current = index; }

  const closing = React.useRef(false);
  const flightRef = React.useRef<Element | null>(null);
  const flightEpoch = React.useRef(0);
  const cancelFlight = React.useCallback(() => {
    flightEpoch.current++;
    const ghost = flightRef.current;
    flightRef.current = null;
    ghost?.getAnimations().forEach((a) => a.cancel());
    ghost?.remove();
    if (imgRef.current) imgRef.current.style.visibility = "";
  }, []);
  // flyImage 同步把替身挂到 body；只记录本查看器创建的节点。
  const startFlight = React.useCallback((...args: Parameters<typeof flyImage>) => {
    const flight = flyImage(...args);
    const ghost = document.body.lastElementChild;
    flightRef.current = ghost?.classList.contains("cz-fly-img") ? ghost : null;
    return flight;
  }, []);
  const requestClose = React.useCallback(async () => {
    if (closing.current) return;
    closing.current = true;
    cancelFlight();
    // 关闭动画期间不再响应点击（上一张、下一张会打断缩回）。
    if (rootRef.current) rootRef.current.style.pointerEvents = "none";
    const level = motionLevel();
    const current = itRef.current;
    const thumb = document.querySelector<HTMLImageElement>(`[data-photo="${CSS.escape(current.path)}"] .photo-thumb`);
    const view = document.querySelector(".content")?.getBoundingClientRect();
    const tr = thumb?.getBoundingClientRect();
    const visible = !!(tr && view && tr.width > 0 && tr.bottom > view.top && tr.top < view.bottom);
    const img = imgRef.current;
    const stage = stageRef.current;
    if (level === "full" && visible && img && stage && tr) {
      const from = containRect(stage.getBoundingClientRect(), current.width, current.height);
      img.style.visibility = "hidden";
      void play(rootRef.current, [{ opacity: 1 }, { opacity: 0 }], 200, "linear", { fill: "forwards" });
      await startFlight(img.currentSrc || img.src, from, tr, 240, [6, 10]);
    } else if (level !== "none") {
      if (level === "full") void play(panelRef.current, [{ transform: "none" }, { transform: "scale(0.98)" }], 150, EASE.in, { fill: "forwards" });
      await play(rootRef.current, [{ opacity: 1 }, { opacity: 0 }], level === "full" ? 150 : dur("fade"), level === "full" ? EASE.in : "linear", { fill: "forwards" });
    }
    onCloseRef.current();
  }, []);

  // 切换查看项：先落缩略图，再异步拉大图；过期回包丢弃。
  React.useEffect(() => {
    const dir = navDir.current;
    const level = motionLevel();
    if (dir !== 0) cancelFlight();
    if (dir !== 0 && imgRef.current && stageRef.current && level !== "none") {
      const img = imgRef.current;
      const ghost = img.cloneNode() as HTMLImageElement;
      ghost.removeAttribute("id");
      ghost.className = "pv-ghost";
      ghost.setAttribute("aria-hidden", "true");
      stageRef.current.appendChild(ghost);
      void play(ghost, level === "full"
        ? [{ opacity: 1, transform: "none" }, { opacity: 0, transform: `translateX(${-dir * 16}px)` }]
        : [{ opacity: 1 }, { opacity: 0 }], DUR.fade, level === "full" ? EASE.in : "linear", { fill: "forwards" })
        .then(() => ghost.remove());
      void play(img, level === "full"
        ? [{ opacity: 0, transform: `translateX(${dir * 16}px)` }, { opacity: 1, transform: "none" }]
        : [{ opacity: 0 }, { opacity: 1 }], level === "full" ? 200 : dur("fade"), level === "full" ? EASE.out : "linear", { delay: level === "full" ? 40 : 0 });
    }
    navDir.current = 0;
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
    const level = motionLevel();
    if (level !== "none") {
      void play(rootRef.current, [{ opacity: 0 }, { opacity: 1 }], level === "full" ? 180 : dur("fade"), "linear");
      rootRef.current?.querySelectorAll<HTMLElement>(".photo-viewer-head, .photo-viewer-info, .photo-viewer-controls")
        .forEach((el) => void play(el, [{ opacity: 0 }, { opacity: 1 }], level === "full" ? DUR.state : dur("fade"), "linear", { delay: level === "full" ? 120 : 0 }));
      const thumb = document.querySelector<HTMLImageElement>(`[data-photo="${CSS.escape(it.path)}"] .photo-thumb`);
      const img = imgRef.current;
      const stage = stageRef.current;
      if (level === "full" && thumb && img && stage) {
        img.style.visibility = "hidden";
        const epoch = ++flightEpoch.current;
        void startFlight(thumb.currentSrc || thumb.src, thumb.getBoundingClientRect(), containRect(stage.getBoundingClientRect(), it.width, it.height), dur("move"), [10, 6])
          .then(() => {
            if (epoch !== flightEpoch.current || closing.current) return;
            flightRef.current = null;
            img.style.visibility = "";
          });
      }
    }
    closeRef.current?.focus();
    return () => {
      cancelFlight();
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
      if (closing.current) return;
      if (e.key === "Escape") {
        e.preventDefault();
        void requestClose();
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
      onClick={(e) => { if (e.target === e.currentTarget) void requestClose(); }}
    >
      <div className="photo-viewer-panel" ref={panelRef} role="dialog" aria-modal="true" aria-labelledby="pv-name" aria-describedby="pv-meta">
        <header className="photo-viewer-head">
          <h2 id="pv-name" title={it.path}>{baseName(it.path)}</h2>
          <button
            id="pv-close"
            ref={closeRef}
            className="btn small ghost icon"
            type="button"
            title={t("photos.close")}
            aria-label={t("photos.close")}
            onClick={() => void requestClose()}
          >
            <X width={18} height={18} />
          </button>
        </header>
        <div className="photo-viewer-stage" ref={stageRef}>
          {!error && <img id="pv-image" ref={imgRef} src={imgSrc} alt={baseName(it.path)} />}
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
