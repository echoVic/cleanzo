// React 相似照片页与原生导航的桥接：main.ts 调 showReactPhotos() 即可，不直接接触 React。
import { mountPhotos } from "./mount";

/** 每次切到相似照片视图都挂载 / 复用 React 根；首次挂载时组件自行自动扫描。 */
export function showReactPhotos() {
  const el = document.querySelector<HTMLElement>("#photos-react-root");
  if (el) mountPhotos(el);
}
