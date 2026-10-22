import { EASE, dur, play } from "./motion";

/** 页面坐标里的矩形（DOMRect 的子集，测试里不依赖 DOM）。 */
export interface Box { left: number; top: number; width: number; height: number }

/** 图片按 object-fit: contain 放进 stage 后实际占的矩形。 */
export function containRect(stage: Box, width: number, height: number): Box {
  const ratio = width / height;
  let w = stage.width;
  let h = w / ratio;
  if (h > stage.height) { h = stage.height; w = h * ratio; }
  return { left: stage.left + (stage.width - w) / 2, top: stage.top + (stage.height - h) / 2, width: w, height: h };
}

/** 文字共享元素：替身从起点飞到 to 的位置并换成 to 的颜色，期间隐藏 to。 */
export function flyText(from: { rect: Box; fontSize: number; color: string }, to: HTMLElement): Promise<void> {
  const ms = dur("move");
  const r1 = to.getBoundingClientRect();
  if (!(ms > 0) || !r1.width) return Promise.resolve();
  const cs = getComputedStyle(to);
  const ghost = document.createElement("div");
  ghost.className = "cz-fly";
  ghost.setAttribute("aria-hidden", "true");
  ghost.textContent = to.textContent;
  Object.assign(ghost.style, {
    left: `${r1.left}px`, top: `${r1.top}px`, width: `${r1.width}px`, height: `${r1.height}px`,
    fontFamily: cs.fontFamily, fontSize: cs.fontSize, fontWeight: cs.fontWeight,
    letterSpacing: cs.letterSpacing, lineHeight: cs.lineHeight, color: cs.color,
  });
  document.body.appendChild(ghost);
  to.style.visibility = "hidden";
  const scale = from.fontSize / parseFloat(cs.fontSize) || 1;
  const dx = from.rect.left + from.rect.width / 2 - (r1.left + r1.width / 2);
  const dy = from.rect.top + from.rect.height / 2 - (r1.top + r1.height / 2);
  return play(ghost, [{ transform: `translate(${dx}px, ${dy}px) scale(${scale})`, color: from.color }, { transform: "none", color: cs.color }], ms, EASE.move)
    .then(() => { ghost.remove(); to.style.visibility = ""; });
}

/** 图片共享元素：替身图从 from 伸缩到 to（object-fit: cover），圆角同时过渡。 */
export function flyImage(src: string, from: Box, to: Box, ms: number, radius: [number, number]): Promise<void> {
  if (!(ms > 0)) return Promise.resolve();
  const img = document.createElement("img");
  img.className = "cz-fly-img";
  img.alt = "";
  img.src = src;
  const frame = (r: Box, rad: number) => ({ left: `${r.left}px`, top: `${r.top}px`, width: `${r.width}px`, height: `${r.height}px`, borderRadius: `${rad}px` });
  Object.assign(img.style, frame(from, radius[0]));
  document.body.appendChild(img);
  return play(img, [frame(from, radius[0]), frame(to, radius[1])], ms, EASE.move, { fill: "forwards" }).then(() => img.remove());
}
