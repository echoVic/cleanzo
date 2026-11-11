import { expect, test } from "bun:test";
import type { PhotoCluster, PhotoItem } from "../src/mygo";
import {
  effectiveRemoved, emptySelection, isGone, subtractRemoved, type ListSelection,
} from "../src/react/list/selection";
import {
  allChecked, defaultPickedPaths, liveSel, removeClusters, someChecked,
  visibleClusters, visibleItems, visibleSelectablePaths,
} from "../src/react/photos/helpers";

function item(path: string, size: number, keep = false): PhotoItem {
  return { path, size, width: 100, height: 100, thumb: "data:,", keep };
}

function cluster(id: number, kind: string, items: PhotoItem[]): PhotoCluster {
  const totalSize = items.reduce((s, it) => s + (it.size >= 0 ? it.size : 0), 0);
  const keepIt = items.find((it) => it.keep);
  const waste = keepIt ? Math.max(0, totalSize - keepIt.size) : 0;
  return { id, kind, items, totalSize, waste };
}

test("defaultPickedPaths selects only non-keep items inside exact clusters", () => {
  const clusters = [
    cluster(1, "exact", [item("/a/1.jpg", 100, true), item("/a/2.jpg", 100)]),
    cluster(2, "similar", [item("/b/1.jpg", 200, true), item("/b/2.jpg", 200)]),
  ];
  expect(defaultPickedPaths(clusters).sort()).toEqual(["/a/2.jpg"]);
});

test("visibleSelectablePaths excludes keep items and gone paths", () => {
  const clusters = [
    cluster(1, "exact", [item("/a/1.jpg", 100, true), item("/a/2.jpg", 100), item("/a/3.jpg", 100)]),
  ];
  let st: ListSelection = emptySelection();
  expect(visibleSelectablePaths(clusters, st).sort()).toEqual(["/a/2.jpg", "/a/3.jpg"].sort());
  st = subtractRemoved(st, ["/a/2.jpg"]);
  expect(visibleSelectablePaths(clusters, st)).toEqual(["/a/3.jpg"]);
  expect(isGone(st, "/a/2.jpg")).toBe(true);
});

test("visibleClusters drops clusters with fewer than 2 visible items", () => {
  const clusters = [
    cluster(1, "exact", [item("/a/1.jpg", 100, true), item("/a/2.jpg", 100)]),
    cluster(2, "similar", [item("/b/1.jpg", 200, true), item("/b/2.jpg", 200)]),
  ];
  let st: ListSelection = subtractRemoved(emptySelection(), ["/a/2.jpg"]);
  expect(visibleClusters(clusters, st).map((c) => c.id)).toEqual([2]);
  expect(visibleItems(clusters[0]!, st).map((i) => i.path)).toEqual(["/a/1.jpg"]);
});

test("allChecked / someChecked give the tri-state of the toolbar checkbox", () => {
  const visible = ["/a/2.jpg", "/a/3.jpg"];
  let sel = new Set<string>();
  expect(allChecked(visible, sel)).toBe(false);
  expect(someChecked(visible, sel)).toBe(false);
  sel = new Set(["/a/2.jpg"]);
  expect(allChecked(visible, sel)).toBe(false);
  expect(someChecked(visible, sel)).toBe(true);
  sel = new Set(["/a/2.jpg", "/a/3.jpg"]);
  expect(allChecked(visible, sel)).toBe(true);
  expect(someChecked(visible, sel)).toBe(true);
});

test("liveSel drops checked paths that were moved to Trash", () => {
  let st: ListSelection = { ...emptySelection(), sel: new Set(["/a/2.jpg", "/a/3.jpg"]) };
  st = subtractRemoved(st, ["/a/2.jpg"]);
  expect(liveSel(st)).toEqual(["/a/3.jpg"]);
});

test("removeClusters drops clusters under 2 items and rewrites keep/totalSize/waste", () => {
  const clusters = [
    cluster(1, "exact", [item("/a/1.jpg", 100, true), item("/a/2.jpg", 100), item("/a/3.jpg", 100)]),
    cluster(2, "exact", [item("/b/1.jpg", 50, true), item("/b/2.jpg", 50)]),
  ];
  const next = removeClusters(clusters, ["/a/2.jpg"]);
  // 簇 1 剩 keep + /a/3：>= 2 保留；keep 仍是 /a/1；waste = 200 - 100。簇 2 未受影响。
  expect(next.map((c) => c.id)).toEqual([1, 2]);
  const c1 = next[0]!;
  expect(c1.items.map((i) => i.path)).toEqual(["/a/1.jpg", "/a/3.jpg"]);
  expect(c1.items.map((i) => i.keep)).toEqual([true, false]);
  expect(c1.totalSize).toBe(200);
  expect(c1.waste).toBe(100);
  // 簇 2 只有 2 项，删掉一个后不足 2 项，整簇消失；簇 1 保留。
  const dropped = removeClusters(clusters, ["/b/2.jpg"]);
  expect(dropped.map((c) => c.id)).toEqual([1]);
});

test("removeClusters picks the largest remaining item as keep when the old keep is gone", () => {
  const clusters = [
    cluster(1, "exact", [item("/a/1.jpg", 100, true), item("/a/2.jpg", 300), item("/a/3.jpg", 200)]),
  ];
  const next = removeClusters(clusters, ["/a/1.jpg"]);
  const c1 = next[0]!;
  expect(c1.items.map((i) => i.path)).toEqual(["/a/2.jpg", "/a/3.jpg"]);
  expect(c1.items.find((i) => i.keep)?.path).toBe("/a/2.jpg");
  expect(c1.totalSize).toBe(500);
  expect(c1.waste).toBe(200);
});

test("hiding trashed photos does not walk the removed set once per photo", () => {
  class CountingSet<T> extends Set<T> {
    passes = 0;
    override [Symbol.iterator]() { this.passes++; return super[Symbol.iterator](); }
  }
  const clusters = Array.from({ length: 200 }, (_, c) =>
    cluster(c, "exact", Array.from({ length: 5 }, (_, i) => item(`/p/${c}/IMG_${i}.jpg`, 100, i === 0))));
  const gone = new CountingSet<string>();
  for (let c = 0; c < 200; c++) for (let i = 1; i < 5; i++) if ((c + i) % 2) gone.add(`/p/${c}/IMG_${i}.jpg`);
  const st: ListSelection = { ...emptySelection(), gone, sel: new Set([`/p/0/IMG_2.jpg`, `/p/0/IMG_1.jpg`]) };
  expect(visibleClusters(clusters, st)).toHaveLength(200);
  expect(visibleSelectablePaths(clusters, st)).toHaveLength(400);
  expect(liveSel(st)).toEqual(["/p/0/IMG_2.jpg"]);
  expect(gone.passes).toBeLessThanOrEqual(1);
});

test("effectiveRemoved collapses nested removals", () => {
  expect(effectiveRemoved(["/a/b", "/a/b/c", "/a/d"]).sort()).toEqual(["/a/b", "/a/d"].sort());
});

// ---- 大图缓存 ----

/** 让桥接的 PhotoPreview 立刻回一张假图。 */
function stubPreviews() {
  const saved = (globalThis as any).mygo;
  (globalThis as any).mygo = { call: async (_method: string, path: string) => `data:${path}` };
  return () => { (globalThis as any).mygo = saved; };
}

test("the preview cache keeps only the most recently viewed photos", async () => {
  const api = await import("../src/react/photos/api");
  const restore = stubPreviews();
  try {
    api.clearPreviewCache();
    const paths = Array.from({ length: 100 }, (_, i) => `/p/IMG_${i}.jpg`);
    for (const p of paths) await api.loadPhotoPreview(p);
    const kept = paths.filter((p) => api.cachedPreview(p) !== undefined);
    expect(kept.length).toBeLessThanOrEqual(api.PREVIEW_CACHE_MAX);
    expect(kept).toContain("/p/IMG_99.jpg");
    expect(kept).not.toContain("/p/IMG_0.jpg");
    // 看过的那张重新排到最后，不会先被挤掉
    api.clearPreviewCache();
    for (const p of paths.slice(0, api.PREVIEW_CACHE_MAX)) await api.loadPhotoPreview(p);
    api.cachedPreview(paths[0]!);
    await api.loadPhotoPreview(paths[api.PREVIEW_CACHE_MAX]!);
    expect(api.cachedPreview(paths[0]!)).toBe(`data:${paths[0]}`);
    expect(api.cachedPreview(paths[1]!)).toBeUndefined();
  } finally { api.clearPreviewCache(); restore(); }
});

test("trashed photos leave the preview cache, and a load still in flight does not put them back", async () => {
  const api = await import("../src/react/photos/api");
  const saved = (globalThis as any).mygo;
  let late!: (data: string) => void;
  (globalThis as any).mygo = { call: (_m: string, path: string) => path === "/p/late.jpg" ? new Promise((yes) => { late = yes; }) : Promise.resolve(`data:${path}`) };
  try {
    api.clearPreviewCache();
    await api.loadPhotoPreview("/p/a.jpg");
    await api.loadPhotoPreview("/p/b.jpg");
    const pending = api.loadPhotoPreview("/p/late.jpg");
    api.forgetPreviews(["/p/a.jpg", "/p/late.jpg"]);
    late("data:late");
    await pending;
    expect(api.cachedPreview("/p/a.jpg")).toBeUndefined();
    expect(api.cachedPreview("/p/late.jpg")).toBeUndefined();
    expect(api.cachedPreview("/p/b.jpg")).toBe("data:/p/b.jpg");
  } finally { api.clearPreviewCache(); (globalThis as any).mygo = saved; }
});

// ---- 页面接线：发去废纸篓的只能是页面上还勾着、还看得见的照片 ----

async function photosPage(overrides: Partial<typeof import("../src/react/photos/api").photosApi> = {}) {
  const { pageHarness, fakeStream } = await import("./list-page-harness");
  const { PhotosPage } = await import("../src/react/photos/PhotosPage");
  const { photosApi } = await import("../src/react/photos/api");
  const { getPref, setPref } = await import("../src/i18n");
  const savedPref = getPref();
  setPref("zh");
  const saved = { ...photosApi };
  const stream = fakeStream<never>();
  Object.assign(photosApi, { startScan: () => stream, ...overrides });
  const h = pageHarness(PhotosPage, {});
  await h.flush();
  return {
    h, stream,
    cards: () => h.nodes().filter((n) => n.type?.name === "PhotoClusterCard"),
    /** 工具栏「移到废纸篓」，再在确认框里确认。 */
    async trash() {
      h.find((n) => n.props.className === "btn danger small")!.props.onClick();
      await h.flush();
      h.find((n) => n.props.variant === "destructive")!.props.onClick();
      await h.flush();
    },
    dispose() { h.dispose(); Object.assign(photosApi, saved); setPref(savedPref); },
  };
}

test("a photo left alone in a cluster that went away is no longer selected or sent", async () => {
  const sent: string[][] = [];
  const results = [
    // 第一批：/a/2 移走了，被手动勾上的 /a/1 没能移走；/b/2 正在使用
    { moved: 1, freed: 100, files: 1, inUse: 1, failed: ["/a/1.jpg: denied"], removed: ["/a/2.jpg"] },
    { moved: 1, freed: 200, files: 1, inUse: 0, failed: [], removed: ["/b/2.jpg"] },
  ];
  const p = await photosPage({ cleanPaths: async (paths: string[]) => { sent.push([...paths].sort()); return results[sent.length - 1]!; } });
  try {
    p.stream.emit({ kind: "done", result: { scanned: 4, clusters: [
      cluster(1, "exact", [item("/a/1.jpg", 100, true), item("/a/2.jpg", 100)]),
      cluster(2, "exact", [item("/b/1.jpg", 200, true), item("/b/2.jpg", 200)]),
    ] } } as never);
    p.stream.finish();
    await p.h.flush();
    p.cards()[0]!.props.onToggle("/a/1.jpg", true);
    await p.h.flush();
    const api = await import("../src/react/photos/api");
    const restore = stubPreviews();
    try { await api.loadPhotoPreview("/a/2.jpg"); await api.loadPhotoPreview("/b/1.jpg"); } finally { restore(); }
    await p.trash();
    expect(sent[0]).toEqual(["/a/1.jpg", "/a/2.jpg", "/b/2.jpg"]);
    expect(api.cachedPreview("/a/2.jpg")).toBeUndefined();
    expect(api.cachedPreview("/b/1.jpg")).toBe("data:/b/1.jpg");
    // 簇 1 只剩 /a/1，整簇不再显示：它不能还选着
    expect(p.cards().map((c) => c.props.cluster.id)).toEqual([2]);
    expect(p.h.text(p.h.find((n) => n.props.className === "fs-toolbar-count"))).toContain("已选 1 张");
    await p.trash();
    expect(sent[1]).toEqual(["/b/2.jpg"]);
  } finally { p.dispose(); }
});

test("a batch where some photos could not be moved toasts as an error", async () => {
  const p = await photosPage({ cleanPaths: async () => ({ moved: 1, freed: 100, files: 1, inUse: 0, failed: ["/a/3.jpg: denied"], removed: ["/a/2.jpg"] }) });
  try {
    p.stream.emit({ kind: "done", result: { scanned: 3, clusters: [
      cluster(1, "exact", [item("/a/1.jpg", 100, true), item("/a/2.jpg", 100), item("/a/3.jpg", 100)]),
    ] } } as never);
    p.stream.finish();
    await p.h.flush();
    await p.trash();
    expect(p.h.toasts.at(-1)?.text).toContain("1 张未能移动");
    expect(p.h.toasts.at(-1)?.kind).toBe("err");
  } finally { p.dispose(); }
});

test("a photo scan that fails while its stream is open shows the error and leaves no unhandled rejection", async () => {
  const p = await photosPage();
  try {
    p.stream.fail(new Error("photos denied"));
    await p.h.settle();
    p.stream.end();
    await p.h.flush();
    const alert = p.h.find((n) => n.props.role === "alert");
    expect(alert && p.h.text(alert)).toContain("photos denied");
  } finally { p.dispose(); }
});
