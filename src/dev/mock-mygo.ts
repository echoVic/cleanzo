import type { Runtime, Channel } from "mygo-runtime";
import type {
  Cleaner, LanguageSettings, AppInfo, AppUsage, BigItem, CleanResult, DirItem,
  DiskStat, JunkItem, LargeFile, OrphanItem, PhotoCluster, PhotoItem, Settings,
  StatusSample, SubItem, SystemInfo, UninstallDetail,
} from "../mygo";

class MockChannel<V> implements AsyncIterable<V> {
  onmessage: ((value: V) => void) | null;
  onclose: (() => void) | null = null;
  closed = false;
  private queue: V[] = [];
  private waiters: ((r: IteratorResult<V>) => void)[] = [];
  constructor(onmessage?: (value: V) => void) { this.onmessage = onmessage ?? null; }
  /** 后端侧发送；页面已关闭通道时返回 false，流应停止。 */
  send(value: V): boolean {
    if (this.closed) return false;
    if (this.onmessage) { this.onmessage(value); return true; }
    const w = this.waiters.shift();
    if (w) w({ value, done: false }); else this.queue.push(value);
    return true;
  }
  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.waiters.splice(0).forEach((w) => w({ value: undefined as never, done: true }));
    this.onclose?.();
  }
  [Symbol.asyncIterator](): AsyncIterator<V> {
    return {
      next: () => {
        if (this.queue.length) return Promise.resolve({ value: this.queue.shift()!, done: false });
        if (this.closed) return Promise.resolve({ value: undefined as never, done: true });
        return new Promise((r) => this.waiters.push(r));
      },
      return: () => { this.close(); return Promise.resolve({ value: undefined as never, done: true }); },
    };
  }
}

const GB = 1024 ** 3;
const MB = 1024 ** 2;
const home = (path: string) => path.replace(/^~/, "/Users/demo");
const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));
const sum = (items: readonly { size: number }[]) => items.reduce((n, it) => n + it.size, 0);

// 事件先检查取消；正常结束和异常都由后端侧关通道，页面关闭则返回 Go 相同的错误。
async function stream<T>(channel: Channel<T>, run: (emit: (value: T, ms?: number) => Promise<void>) => Promise<void>): Promise<void> {
  const ch = channel as MockChannel<T>;
  const emit = async (value: T, ms = 0) => {
    if (ms) await wait(ms);
    if (ch.closed || !ch.send(value)) throw new Error("context canceled");
  };
  try {
    await run(emit);
    if (ch.closed) throw new Error("context canceled");
  } finally {
    ch.close();
  }
}

// 每次加载页面重新开始，所有写操作只改内存中的示例数据。
function createRuntime(): Runtime {
  const now = Math.floor(Date.now() / 1000);
  let settings: Settings = { disabledGroups: [], ignored: [], custom: [] };
  const info: SystemInfo = {
    version: "0.2.4", os: "darwin", arch: "arm64", goVersion: "go1.27.1",
    chip: "Apple M2 Pro", memory: 32 * GB, cores: 12, osVersion: "26.6.2", bootTime: now - 4 * 86400,
  };
  const disk: DiskStat = { total: 460 * GB, used: 369 * GB, free: 91 * GB };
  const gone = new Set<string>();
  const sizes = new Map<string, number>();
  const isGone = (path: string) => [...gone].some((p) => path === p || path.startsWith(p + "/"));
  const freedDisk = (bytes: number) => { disk.used -= bytes; disk.free += bytes; };
  const sizeOf = (path: string) => isGone(path) ? 0 : sizes.get(path) ?? GB;
  const remove = (paths: string[]) => {
    let freed = 0;
    const removed = [...new Set(paths)].filter((p, _, all) => !all.some((parent) => parent !== p && p.startsWith(parent + "/")));
    for (const path of removed) {
      const bytes = sizeOf(path);
      freed += bytes;
      gone.add(path);
      for (const [parent, size] of sizes) {
        if (path.startsWith(parent + "/")) sizes.set(parent, Math.max(0, size - bytes));
      }
    }
    freedDisk(freed);
    return { freed, removed };
  };

  const installers: SubItem[] = [
    { path: home("~/Downloads/Xcode_26.xip"), name: "Xcode_26.xip", detail: "~/Downloads", size: 2.9 * GB, default: false, ageDays: 41 },
    { path: home("~/Downloads/Docker.dmg"), name: "Docker.dmg", detail: "~/Downloads", size: 486 * MB, default: false, ageDays: 12 },
    { path: home("~/Desktop/Cleanzo 0.2.3 arm64.dmg"), name: "Cleanzo 0.2.3 arm64.dmg", detail: "~/Desktop", size: 18 * MB, default: false, ageDays: 0 },
  ];
  const junk = (id: string, group: string, name: string, size: number, path: string, extra: Partial<JunkItem> = {}): JunkItem => ({
    id, group, name, size, files: size > 0 ? 240 : 0, default: true, trash: false,
    running: false, inUse: 0, denied: false, estimate: false, paths: [home(path)], top: [], subs: [], ...extra,
  });
  const junkItems: JunkItem[] = [
    junk("sys.logs", "system", "用户日志", 1.3 * GB, "~/Library/Logs", { top: [
      { name: "DiagnosticReports", path: home("~/Library/Logs/DiagnosticReports"), size: 486 * MB },
      { name: "JetBrains", path: home("~/Library/Logs/JetBrains"), size: 312 * MB },
      { name: "com.apple.xpc.launchd", path: home("~/Library/Logs/com.apple.xpc.launchd"), size: 205 * MB },
    ] }),
    junk("sys.temp", "system", "临时文件", 860 * MB, "/private/var/folders/demo/T/TemporaryItems"),
    junk("sys.apple", "system", "系统服务缓存", 2.4 * GB, "~/Library/Caches/com.apple.Music"),
    junk("sys.trash", "system", "废纸篓", 1.8 * GB, "~/.Trash"),
    junk("app:com.microsoft.vscode", "apps", "Visual Studio Code", 740 * MB, "~/Library/Application Support/Code/Cache"),
    junk("app:com.tencent.xinwechat", "apps", "微信", 1.9 * GB, "~/Library/Containers/com.tencent.xinWeChat/Data/Library/Caches"),
    junk("app:com.bytedance.lark", "apps", "飞书", 3.1 * GB, "~/Library/Caches/com.bytedance.lark"),
    junk("app:com.netease.163music", "apps", "网易云音乐", 2.6 * GB, "~/Library/Caches/com.netease.163music", { default: false }),
    junk("browser.chrome", "browsers", "Chrome 缓存", 1.1 * GB, "~/Library/Caches/Google/Chrome"),
    junk("browser.safari", "browsers", "Safari 缓存", 420 * MB, "~/Library/Caches/com.apple.Safari"),
    junk("browser.firefox", "browsers", "Firefox / Zen 缓存", 0, "~/Library/Caches/Firefox"),
    junk("dev.npm", "dev", "npm 缓存", 2.9 * GB, "~/.npm/_cacache"),
    junk("dev.xcode", "dev", "Xcode 构建缓存", 7.4 * GB, "~/Library/Developer/Xcode/DerivedData"),
    junk("dev.playwright", "dev", "Playwright 浏览器", 1.6 * GB, "~/Library/Caches/ms-playwright", { default: false }),
    junk("review.installers", "review", "安装包", sum(installers), "~/Downloads", { files: 3, default: false, trash: true, subs: installers }),
  ];
  const big = (path: string, size: number, trashable = true): BigItem => ({ path: home(path), size, trashable });
  const bigItems: BigItem[] = [
    big("~/Downloads", 22.4 * GB), big("~/Library/Caches", 6.4 * GB), big("~/Movies", 37.9 * GB),
    big("~/.cache", 9.6 * GB), big("~/Library/Containers", 31.2 * GB), big("~/Library/Developer/Xcode", 48.6 * GB),
    big("/Applications", 19.8 * GB, false), big("~/Library/Application Support", 17.5 * GB), big("~/Documents/Projects", 8.1 * GB),
  ];
  const largeFiles: LargeFile[] = [
    big("~/Downloads/Xcode_26.xip", 7.9 * GB),
    big("~/Library/Containers/com.docker.docker/Data/vms/0/data/Docker.raw", 6.2 * GB, false),
    big("~/Movies/2025 东京 4K 母版.mov", 8.4 * GB), big("~/Downloads/ubuntu-24.04-desktop-arm64.iso", 5.7 * GB),
    big("~/Documents/备份/iPhone-2024.zip", 4.3 * GB), big("~/Movies/屏幕录制 2026-04-18.mov", 2.2 * GB),
    big("~/Downloads/Figma-Agent.dmg", 620 * MB), big("~/Desktop/发布会素材.zip", 1.4 * GB),
    big("~/Library/Application Support/Steam/steamapps/common/Hades/Content.pak", 3.6 * GB),
  ];
  const xcodePath = home("~/Library/Developer/Xcode");
  const xcodeChildren: DirItem[] = [
    { name: "DerivedData", path: xcodePath + "/DerivedData", size: 21.3 * GB, isDir: true, trashable: true },
    { name: "iOS DeviceSupport", path: xcodePath + "/iOS DeviceSupport", size: -1, isDir: true, trashable: true },
    { name: "Archives", path: xcodePath + "/Archives", size: -1, isDir: true, trashable: true },
    { name: "Products", path: xcodePath + "/Products", size: 2.6 * GB, isDir: true, trashable: true },
  ];
  const orphans: OrphanItem[] = [
    { ...big("~/Library/Application Support/Sketch", 1.2 * GB), kind: "support", isDir: true },
    { ...big("~/Library/Containers/com.evernote.Evernote", 640 * MB), kind: "container", isDir: true },
    { ...big("~/Library/Caches/com.spotify.client", 96 * MB), kind: "cache", isDir: true },
  ];
  const app = (name: string, bundleId: string, size: number, version: string, days: number, running = false, file = name): AppInfo => ({
    id: `/Applications/${file}.app`, publisher: "", name, path: `/Applications/${file}.app`, bundleId, size, version,
    lastOpened: days < 0 ? 0 : now - days * 86400, running,
  });
  const apps: AppInfo[] = [
    app("Xcode", "com.apple.dt.Xcode", 12.4 * GB, "26.0", 3),
    app("Docker", "com.docker.docker", 2.1 * GB, "4.43.1", 48),
    app("Visual Studio Code", "com.microsoft.VSCode", 640 * MB, "1.105", 1),
    app("Blender", "org.blenderfoundation.blender", 1.6 * GB, "4.5", 120),
    app("IINA", "com.colliderli.iina", 180 * MB, "1.4", 9),
    app("飞书", "com.bytedance.lark", 1.2 * GB, "7.52", 0, true, "Lark"),
    app("微信", "com.tencent.xinWeChat", 980 * MB, "4.1", 0, true, "WeChat"),
    app("OBS Studio", "com.obsproject.obs-studio", 720 * MB, "31.1", -1, false, "OBS"),
  ];
  const usage = (key: string, name: string, memory: number, cpu: number, procs: number, file = name): AppUsage => ({
    key, name, path: `/Applications/${file}.app`, memory: memory * GB, cpu, procs, quit: true,
  });
  const runningApps: AppUsage[] = [
    usage("chrome", "Google Chrome", 20.5, 11, 62), usage("arc", "Arc", 11.7, 31, 14),
    usage("trae", "TRAE CN", 6.1, 10, 79), usage("lark", "飞书", 4.7, 3, 23, "Lark"),
    usage("doubao", "豆包", 3.8, 2, 35, "Doubao"), usage("cmux", "cmux", 3.7, 5, 66), usage("xcode", "Xcode", 2.6, 18, 9),
  ];
  const quitted = new Set<string>();
  let sampleIndex = 0;
  const sample = (): StatusSample => {
    const n = sampleIndex++;
    const drift = (offset: number) => n === 0 ? 0 : Math.sin(n * 0.8 + offset);
    const app = (19.4 + drift(0) * 0.3) * GB;
    const wired = (3.1 + drift(1) * 0.3) * GB;
    const compressed = (4.7 + drift(2) * 0.3) * GB;
    return {
      memory: { total: 32 * GB, used: app + wired + compressed, app, wired, compressed, cached: 3.6 * GB, pressure: 2, swapUsed: 6.2 * GB, swapTotal: 8 * GB },
      cpu: { usage: 39 + Math.sin(n * 0.7) * 31, load1: 2.8 + drift(1), load5: 2.4, load15: 2.1 },
      disk: { ...disk }, battery: { percent: 100, charging: false, full: true, plugged: true, cycles: 170, maxCapacity: 83, condition: "Normal" },
      apps: runningApps.filter((a) => !quitted.has(a.path)).map((a, i) => ({ ...a, memory: a.memory * (1 + drift(i) * 0.08), cpu: Math.max(0, a.cpu + drift(i + 2) * 14) })),
      procs: 740 + Math.round(drift(3) * 8),
    };
  };

  // 缩略图和大图共用构图，只改变 SVG 像素尺寸；不请求外部图片。
  const svg = (cluster: number, variant: number, width: number, height: number): string => {
    const hue = [24, 210, 270, 100][cluster - 1] ?? 24;
    const shift = variant * 7;
    return "data:image/svg+xml;charset=utf-8," + encodeURIComponent(`<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 300 225"><defs><linearGradient id="sky" x2="0.4" y2="1"><stop stop-color="hsl(${hue + shift},65%,72%)"/><stop offset="1" stop-color="hsl(${hue + 45},60%,30%)"/></linearGradient><linearGradient id="land" x2="1" y2="1"><stop stop-color="hsl(${hue + 90},35%,42%)"/><stop offset="1" stop-color="hsl(${hue + 120},50%,15%)"/></linearGradient></defs><rect width="300" height="225" fill="url(#sky)"/><circle cx="${210 + shift}" cy="${65 + variant * 3}" r="28" fill="#ffe8ac"/><path d="M0 160L80 ${95 + shift}L160 160L230 115L300 170V225H0Z" fill="url(#land)"/></svg>`);
  };
  const photos: PhotoCluster[] = [
    { id: 1, kind: "exact", names: ["IMG_4821.HEIC", "IMG_4821.HEIC", "IMG_4821 (1).HEIC"], dirs: ["~/Pictures/导出", "~/Downloads", "~/Desktop"], sizes: [3.1, 3.1, 3.1] },
    { id: 2, kind: "similar", names: ["IMG_5102.HEIC", "IMG_5103.HEIC", "IMG_5104.HEIC", "IMG_5105.HEIC"], dirs: ["~/Pictures/雪山"], sizes: [4.4, 4.2, 4.3, 4.1] },
    { id: 3, kind: "similar", names: ["截屏 2026-10-12 10.24.31.png", "截屏 2026-10-12 10.24.40.png", "截屏 2026-10-12 10.25.02.png"], dirs: ["~/Desktop"], sizes: [1.9, 1.9, 2] },
    { id: 4, kind: "exact", names: ["DSC_0918.JPG", "DSC_0918 拷贝.JPG"], dirs: ["~/Pictures/郊外"], sizes: [11.8, 11.8] },
  ].map((cl) => {
    const items: PhotoItem[] = cl.names.map((name, i) => ({
      path: home(`${cl.dirs[i] ?? cl.dirs[0]}/${name}`), size: cl.sizes[i]! * MB,
      width: 4032, height: 3024, thumb: svg(cl.id, cl.kind === "exact" ? 0 : i, 300, 225), keep: i === 0,
    }));
    return { id: cl.id, kind: cl.kind, items, totalSize: sum(items), waste: sum(items.slice(1)) };
  });
  for (const it of [...bigItems, ...installers, ...largeFiles, ...orphans, ...apps, ...photos.flatMap((cl) => cl.items)]) sizes.set(it.path, it.size);
  for (const it of xcodeChildren) sizes.set(it.path, it.size >= 0 ? it.size : it.name === "Archives" ? 8.2 * GB : 14.9 * GB);
  const detail = (path: string): UninstallDetail => {
    const a = apps.find((a) => a.path === path);
    if (!a) throw new Error("mock: app not found");
    const items = [
      { path, kind: "app", size: a.size },
      { path: home(`~/Library/Preferences/${a.bundleId}.plist`), kind: "prefs", size: 64 * 1024 },
      { path: home(`~/Library/Caches/${a.bundleId}`), kind: "cache", size: 240 * MB },
      { path: home(`~/Library/Containers/${a.bundleId}`), kind: "container", size: 1.3 * GB },
      { path: home(`~/Library/Application Support/${a.name}`), kind: "support", size: 580 * MB },
      { path: home(`~/Library/Logs/${a.name}`), kind: "logs", size: 24 * MB },
    ];
    items.forEach((it) => sizes.set(it.path, it.size));
    return { app: a, items, totalSize: sum(items) };
  };

  // 绑定签名直接取生成代码；方法名、参数顺序、返回数据都由 TypeScript 核对。
  type Bindings = { [K in keyof typeof Cleaner as `Cleaner.${Capitalize<K>}`]: (typeof Cleaner)[K] }
    & { "LanguageSettings.SetLanguage": typeof LanguageSettings.setLanguage };
  const handlers = {
    "Cleaner.Info": async () => info,
    "Cleaner.WatchStatus": async (ch) => stream(ch, async (emit) => {
      await emit(sample());
      for (;;) { await wait(2000); await emit(sample()); }
    }),
    "Cleaner.QuitApp": async (path) => { await wait(700); quitted.add(path); return true; },
    "Cleaner.CrashDumps": async () => ({ total: 0, threshold: GB, dirs: [] }),
    "Cleaner.ScanJunk": async (ch) => stream(ch, async (emit) => {
      const items = junkItems.filter((it) => !settings.disabledGroups.includes(it.group) && !settings.ignored.includes(it.id));
      await emit({ kind: "start", items: items.map((it) => ({ ...it, size: -1 })) });
      const order = [...items];
      // 同组里交错两项，模拟并发测量。
      for (let i = 0; i + 1 < order.length; i += 3) {
        if (order[i]!.group === order[i + 1]!.group) [order[i], order[i + 1]] = [order[i + 1]!, order[i]!];
      }
      for (const [i, item] of order.entries()) {
        await emit({ kind: "scanning", path: item.paths[0] }, 140 + (i % 3) * 140);
        await emit({ kind: "item", item: structuredClone(item) });
      }
      await emit({ kind: "done", summary: { checked: items.length, fullDiskAccess: true, containers: 12, ignored: settings.ignored.length, leftovers: 3, leftoversSize: sum(orphans) } });
    }),
    "Cleaner.CleanJunk": async (selections, _scannedAt, ch) => stream(ch, async (emit) => {
      let bytes = 0;
      for (const [i, sel] of selections.entries()) {
        const item = junkItems.find((it) => it.id === sel.id);
        if (!item) throw new Error(`mock: unknown junk id ${sel.id}`);
        const subs = item.subs.filter((sub) => sel.paths?.includes(sub.path));
        const size = item.subs.length ? sum(subs) : item.size;
        await emit({ kind: "start", id: sel.id });
        for (let j = 0; j < 2; j++) await emit({ kind: "checking", id: sel.id, path: subs[j]?.path ?? item.paths[0] }, 90);
        await emit({ kind: "removed", id: sel.id, bytes: bytes + size, path: item.paths[0] }, 70 + (i % 3) * 100);
        bytes += size;
        const result: CleanResult = { name: item.name, freed: item.trash ? 0 : size, trashed: item.trash ? size : 0, files: subs.length || item.files, inUse: 0, failed: [] };
        item.size = Math.max(0, item.size - size);
        item.subs = item.subs.filter((sub) => !sel.paths?.includes(sub.path));
        freedDisk(size);
        await emit({ kind: "done", id: sel.id, bytes, result });
      }
    }),
    "Cleaner.GetSettings": async () => settings,
    "Cleaner.SaveSettings": async (s) => { settings = structuredClone(s); },
    "Cleaner.FullDiskAccess": async () => true,
    "Cleaner.OpenFullDiskAccessSettings": async () => {},
    "Cleaner.RevealInFinder": async (_path) => {},
    "Cleaner.PickFolder": async () => "",
    "Cleaner.CheckForUpdates": async () => {},
    "Cleaner.ListAITools": async () => [],
    // main.ts 启动时也会探测技能入口；没有示例目录时隐藏它。
    "Cleaner.ListSkillRoots": async () => [],
    "Cleaner.Disk": async () => disk,
    "Cleaner.FullScan": async (ch) => stream(ch, async (emit) => {
      const items = bigItems.filter((it) => !isGone(it.path)).map((it) => ({ ...it, size: sizeOf(it.path) }));
      for (const [i, it] of items.entries()) await emit({ kind: "item", ...it, scanned: (i + 1) * 1200 }, 220 + (i % 3) * 90);
      await emit({ kind: "done", result: { disk: { ...disk }, items, scanned: 12480, skipped: 18, elapsedMs: 2900 } });
    }),
    "Cleaner.DirChildren": async (path) => {
      await wait(500);
      const items: DirItem[] = path === xcodePath ? xcodeChildren : [
        { name: "Cache", path: path + "/Cache", size: sizeOf(path) * 0.6, isDir: true, trashable: path !== "/Applications" },
        { name: "Data", path: path + "/Data", size: sizeOf(path) * 0.4, isDir: true, trashable: path !== "/Applications" },
      ];
      items.forEach((it) => { if (it.size >= 0 && !sizes.has(it.path)) sizes.set(it.path, it.size); });
      return { items: items.filter((it) => !isGone(it.path)).map((it) => ({ ...it, size: it.size < 0 ? -1 : sizeOf(it.path) })), total: path === xcodePath ? 7 : items.length };
    },
    "Cleaner.DirSize": async (path) => { await wait(900); return sizeOf(path); },
    "Cleaner.LargeScan": async (minSize, ch) => stream(ch, async (emit) => {
      const items = largeFiles.filter((it) => it.size >= minSize && !isGone(it.path));
      for (const [i, it] of items.entries()) await emit({ kind: "item", ...it }, 220 + (i % 3) * 90);
      await emit({ kind: "done", result: { items, scanned: 28400, skipped: 21, elapsedMs: 2600, threshold: minSize } });
    }),
    "Cleaner.TrashPath": async (path) => { await wait(600); return { moved: 1, freed: remove([path]).freed }; },
    "Cleaner.CleanPaths": async (paths) => { await wait(800); const r = remove(paths); return { ...r, moved: r.removed.length, files: r.removed.length, inUse: 0, failed: [] }; },
    "Cleaner.ScanSimilarPhotos": async (ch) => stream(ch, async (emit) => {
      for (let i = 1; i <= 8; i++) await emit({ kind: "progress", processed: Math.round(1284 * i / 8), total: 1284 }, 250);
      const clusters = photos.map((cl) => {
        const items = cl.items.filter((it) => !isGone(it.path));
        return { ...cl, items, totalSize: sum(items), waste: sum(items.filter((it) => !it.keep)) };
      }).filter((cl) => cl.items.length >= 2);
      await emit({ kind: "done", result: { clusters, scanned: 1284, elapsedMs: 2000 } });
    }),
    "Cleaner.PhotoPreview": async (path) => {
      await wait(300);
      const cl = photos.find((cl) => cl.items.some((it) => it.path === path));
      if (!cl) throw new Error("mock: photo not found");
      return svg(cl.id, cl.kind === "exact" ? 0 : cl.items.findIndex((it) => it.path === path), 1200, 900);
    },
    "Cleaner.ListApps": async () => apps.filter((a) => !isGone(a.path)),
    "Cleaner.AppUninstallDetail": async (path) => { await wait(800); return detail(path); },
    "Cleaner.UninstallApp": async (path) => {
      await wait(700);
      const d = detail(path);
      const r = remove(d.items.map((it) => it.path));
      quitted.add(path);
      return { launched: false, path, moved: r.removed.length, freed: r.freed, failed: [] };
    },
    "Cleaner.ScanOrphans": async (ch) => stream(ch, async (emit) => {
      const items = orphans.filter((it) => !isGone(it.path));
      for (const it of items) await emit(it, 320);
      await emit({ kind: "__done", path: "960", size: sum(items), trashable: false, isDir: false });
    }),
    "LanguageSettings.SetLanguage": async (_preference) => "zh-Hans",
  } satisfies Partial<Bindings>;

  return {
    async call<T>(method: string, ...args: unknown[]): Promise<T> {
      if (!Object.hasOwn(handlers, method)) throw new Error(`mock: ${method} not implemented`);
      const handler = handlers[method as keyof typeof handlers] as (...args: unknown[]) => Promise<unknown>;
      return structuredClone(await handler(...args)) as T;
    },
    channel: <T>(onmessage?: (value: T) => void) => new MockChannel<T>(onmessage),
    on: () => () => {}, once: () => () => {}, platform: "darwin", windowId: 1, version: "mock", locale: "zh-Hans", dir: "ltr",
    preferences: { accent: null, reduceMotion: matchMedia("(prefers-reduced-motion: reduce)").matches, reduceTransparency: false, highContrast: false, textScale: 1 },
    window: {
      minimize: async () => {}, maximize: async () => {}, unmaximize: async () => {}, toggleMaximize: async () => {},
      isMaximized: async () => false, toggleFullScreen: async () => {}, close: async () => {}, setTitle: async () => {},
    },
  };
}

if (!("mygo" in globalThis) && new URLSearchParams(location.search).has("mock")) {
  window.mygo = createRuntime();
}
