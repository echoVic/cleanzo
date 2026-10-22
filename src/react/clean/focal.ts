import { EASE, dur, motionLevel, play } from "../../ui/motion/motion";

/** 进度条走满（没停止、没失败项、没出错）且确实释放了空间时才播放招牌时刻（设计 6.2）。 */
export function shouldPlayFocal(o: { stopped: boolean; failed: number; error: boolean; bytes: number }): boolean {
  return !o.stopped && o.failed === 0 && !o.error && o.bytes > 0;
}

/** 清理完成：光带从左到右擦过横幅，扫到星标时星标转 90° 亮一下，大数字轻轻落定；减弱时只让星标亮一下。 */
export function playCleanFocal(hero: HTMLElement | null): void {
  if (!hero || motionLevel() === "none") return;
  const spark = hero.querySelector<HTMLElement>(".hero-spark");
  if (!(dur("move") > 0)) {
    void play(spark, [{ opacity: 0.09 }, { opacity: 0.45, offset: 0.35 }, { opacity: 0.09 }], 640, "linear");
    return;
  }
  const band = hero.querySelector<HTMLElement>(".hero-sweep > i");
  const width = hero.clientWidth;
  const bandWidth = band?.offsetWidth ?? 0;
  void play(band, [
    { transform: `translateX(${-bandWidth}px) skewX(-14deg)`, opacity: 0 },
    { opacity: 1, offset: 0.18 },
    { opacity: 1, offset: 0.8 },
    { transform: `translateX(${width}px) skewX(-14deg)`, opacity: 0 },
  ], 540, "cubic-bezier(0.45, 0, 0.2, 1)");
  void play(hero.querySelector(".hero-num"), [{ transform: "scale(1.06)" }, { transform: "none" }], 420, EASE.out);
  window.setTimeout(() => {
    void play(spark, [{ opacity: 0.09 }, { opacity: 0.55, offset: 0.3 }, { opacity: 0.09 }], 440, EASE.out);
    void play(spark?.querySelector("svg"), [{ transform: "rotate(0deg) scale(1)" }, { transform: "rotate(45deg) scale(1.16)", offset: 0.35 }, { transform: "rotate(90deg) scale(1)" }], 440, EASE.out);
  }, 290);
}
