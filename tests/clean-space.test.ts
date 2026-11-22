import { beforeEach, expect, test } from "bun:test";
import "./setup-dom";
import { setLang } from "./setup-dom";
import type { JunkItem, JunkScanEvent } from "../src/mygo";
import { ruleName, t } from "../src/i18n";
import { human } from "../src/shared";
import { changes, fakeStream } from "./page-harness";
import { CleanPage } from "../src/react/clean/CleanPage";
import { cleanApi } from "../src/react/clean/api";
import { buildCleanPlan, finalizeScan, recoveryOf, scanItem, scanStart, toggleRow } from "../src/react/clean/helpers";

beforeEach(() => {
  setLang("zh");
});

const GB = 1024 ** 3;

const item = (id: string, group: string, size: number, extra: Partial<JunkItem> = {}): JunkItem => ({
  id, group, size, name: id, files: 1, default: true, trash: false, running: false,
  denied: false, estimate: false, inUse: 0, paths: [`~/cache/${id}`], top: [], subs: [], recovery: "download", ...extra,
});

/** 一轮完整扫描：start 全量骨架 → 逐项 measured → done。 */
function scan(items: JunkItem[]): ReturnType<typeof finalizeScan> {
  let st = scanStart(items.map((it) => ({ ...it, size: -1 })));
  for (const it of items) st = scanItem(st, it);
  return finalizeScan(st);
}

/** 挂上清理页并扫出这些行；streams 留给测试继续发清理事件。 */
async function scannedPage(env: ReturnType<typeof changes>, items: JunkItem[], over: Record<string, unknown> = {}) {
  const stream = fakeStream<JunkScanEvent>();
  env.assign(cleanApi, { startScan: () => stream, ...over });
  const h = env.page(CleanPage, {});
  await h.settle();
  h.find((n) => n.type?.name === "IdleHero")!.props.onCta();
  stream.emit({ kind: "start", items: items.map((it) => ({ ...it, size: -1 })) });
  for (const it of items) stream.emit({ kind: "item", item: it });
  stream.finish();
  await h.settle();
  return h;
}

/** 勾上或取消一条规则行。 */
function check(h: { find(pred: (n: any) => boolean): any }, id: string, on: boolean) {
  h.find((n) => n.props.ariaLabel === t("junk.select", { name: ruleName(id) }))!.props.onChange(on);
}

// ---- 清掉以后靠什么回来 ----

test("the confirmation adds up what needs a network to come back and what stays gone, the Trash-bound part apart", () => {
  const items = [
    item("dev.npm", "dev", 1024),
    item("dev.go-build", "dev", 100, { recovery: "rebuild" }),
    item("sys.logs", "system", 512, { recovery: "leftover" }),
    item("sys.trash", "system", 2048, { default: false, recovery: "gone" }),
    item("review.installers", "review", 4096, { default: false, trash: true, recovery: "gone" }),
  ];
  let st = scan(items);
  for (const it of items) st = { ...st, sel: toggleRow(it, true, st.sel) };
  expect(buildCleanPlan(st.items, st.sel)!.cost).toEqual({ download: 1024, gone: 6144, goneToTrash: 4096 });
});

test("a row whose recovery the backend did not say counts as staying gone", () => {
  expect(recoveryOf(item("dev.npm", "dev", 1, { recovery: "" }))).toBe("gone");
  expect(recoveryOf(item("dev.npm", "dev", 1, { recovery: "new-kind" }))).toBe("gone");
  expect(recoveryOf(item("dev.npm", "dev", 1, { recovery: "rebuild" }))).toBe("rebuild");
});

test("the confirmation labels each row with what brings it back and states the totals, in both languages", async () => {
  for (const lang of ["zh", "en"] as const) {
    const env = changes();
    try {
      env.lang(lang);
      const h = await scannedPage(env, [
        item("dev.npm", "dev", 3 * GB),
        item("sys.logs", "system", GB, { recovery: "leftover" }),
        item("sys.trash", "system", 2 * GB, { default: false, recovery: "gone" }),
        item("review.installers", "review", 4 * GB, { default: false, trash: true, recovery: "gone" }),
      ]);
      for (const id of ["sys.trash", "review.installers"]) check(h, id, true);
      await h.settle();
      h.click(t("clean.now"));
      await h.settle();
      const text = h.text();
      // 标签是子组件，文字在它的 props 里。
      const tags = h.all().map((n) => n.props.text);
      for (const kind of ["download", "leftover", "gone"] as const) expect(tags).toContain(t(`clean.recovery.${kind}`));
      expect(text).toContain(t("clean.cost.download", { size: human(3 * GB) }));
      expect(text).toContain(t("clean.cost.goneMixed", { size: human(6 * GB), trash: human(4 * GB) }));
      expect(text).not.toContain(t("clean.cost.none"));
    } finally { env.restore(); }
  }
});

test("a selection that costs nothing to get back says so, and one that is all Trash-bound says it can be put back", async () => {
  const env = changes();
  try {
    const h = await scannedPage(env, [
      item("dev.go-build", "dev", GB, { recovery: "rebuild" }),
      item("sys.logs", "system", GB, { recovery: "leftover" }),
      item("review.installers", "review", 4 * GB, { default: false, trash: true, recovery: "gone" }),
    ]);
    h.click(t("clean.now"));
    await h.settle();
    expect(h.text()).toContain(t("clean.cost.none"));
    check(h, "review.installers", true);
    await h.settle();
    h.click(t("clean.now"));
    await h.settle();
    expect(h.text()).toContain(t("clean.cost.goneTrash", { size: human(4 * GB) }));
    expect(h.text()).not.toContain(t("clean.cost.none"));
  } finally { env.restore(); }
});
