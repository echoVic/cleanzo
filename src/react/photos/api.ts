import { Channel } from "mygo-runtime";
import { Cleaner } from "../../mygo";
import type { SimilarPhotoEvent } from "../../mygo";

/** 大图内存缓存：模块级，重扫开始时整体清空。 */
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

export function cachedPreview(path: string): string | undefined {
  return previewCache.get(path);
}

/** 去重并发加载大图；缓存已被清空时旧请求不回填。 */
export function loadPhotoPreview(path: string): Promise<string> {
  let load = previewLoads.get(path);
  if (!load) {
    const request = Cleaner.photoPreview(path)
      .then((data) => {
        if (previewLoads.get(path) === request) previewCache.set(path, data);
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
