import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { PhotosPage } from "./PhotosPage";
import "../../ui/index.css";

let root: Root | null = null;

/** 在原生视图的容器里挂载相似照片页；root 复用，重进视图不整树卸载（结果保留、不重扫）。 */
export function mountPhotos(el: HTMLElement) {
  if (!root) root = createRoot(el);
  root.render(
    <React.StrictMode>
      <PhotosPage />
    </React.StrictMode>,
  );
}
