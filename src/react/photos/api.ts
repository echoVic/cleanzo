import { Channel } from "mygo-runtime";
import { Cleaner } from "../../mygo";
import type { SimilarPhotoEvent } from "../../mygo";

/** 大图缓存最多留几张：每张是最长边 1600 像素的 JPEG data URL。 */
export const PREVIEW_CACHE_MAX = 24;
/** 大图内存缓存：模块级，按最近使用排序，超出上限丢掉最久没看的；重扫开始时整体清空。 */
const previewCache = new Map<string, string>();
/** 同一路径并发加载去重；回包时凭请求身份判断缓存是否还有效。 */
const previewLoads = new Map<string, Promise<string>>();

/** 相似照片页用到的全部 Cleaner 调用集中在此，组件不直接碰桥。 */
export const photosApi = {
  /** 启动流式扫描；返回 Channel 与完成 Promise。 */
  startScan(): { ch: Channel<SimilarPhotoEvent>; done: Promise<void> } {
    const ch = new Channel<SimilarPhotoEvent>();
    const done = Cleaner.scanSimilarPhotos(ch);
    return { ch, done };
  },
  cleanPaths: (paths: string[]) => Cleaner.cleanPaths(paths),
  reveal: (path: string) => Cleaner.revealInFinder(path),
};

/** 重新扫描前清空大图缓存；旧请求的回包不得把它填回来。 */
export function clearPreviewCache(): void {
  previewCache.clear();
  previewLoads.clear();
}

/** 移到废纸篓的照片不再留大图；还在加载的请求回包后也不回填。 */
export function forgetPreviews(paths: readonly string[]): void {
  for (const path of paths) {
    previewCache.delete(path);
    previewLoads.delete(path);
  }
}

function remember(path: string, data: string): void {
  previewCache.delete(path);
  previewCache.set(path, data);
  for (const oldest of previewCache.keys()) {
    if (previewCache.size <= PREVIEW_CACHE_MAX) break;
    previewCache.delete(oldest);
  }
}

export function cachedPreview(path: string): string | undefined {
  const data = previewCache.get(path);
  if (data !== undefined) remember(path, data); // 刚看过的排到最后，最晚被挤掉
  return data;
}

/** 去重并发加载大图；缓存已被清空、或照片已被移走时旧请求不回填。 */
export function loadPhotoPreview(path: string): Promise<string> {
  let load = previewLoads.get(path);
  if (!load) {
    const request = Cleaner.photoPreview(path)
      .then((data) => {
        if (previewLoads.get(path) === request) remember(path, data);
        return data;
      })
      .finally(() => {
        if (previewLoads.get(path) === request) previewLoads.delete(path);
      });
    previewLoads.set(path, request);
    load = request;
  }
  return load;
}
