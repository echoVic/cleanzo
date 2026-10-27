// 占用增长面板的纯函数：面板状态、横幅、增长文案、排序、迷你曲线、卸载差额。测试直接覆盖。
import type { GrowthApp, GrowthDelta, GrowthState, UninstallDetail } from "../../mygo";
import { human } from "../../shared";

/** 列表排序方式。 */
export type GrowthSort = "week" | "size";

/** 页面主体显示哪一块。 */
export type GrowthView = "loading" | "unsupported" | "empty" | "baseline" | "list";

/** 页面主体：不支持 / 没有记录 / 只有一次记录（还看不到增长）/ 列表。 */
export function growthView(s: GrowthState | null): GrowthView {
  if (!s) return "loading";
  if (!s.supported) return "unsupported";
  if (s.records === 0) return "empty";
  return s.records === 1 ? "baseline" : "list";
}

/** 顶部横幅：没问过、没开监控时请用户打开；问过还没开就提示一句；开着不显示。 */
export function growthBanner(s: GrowthState): "optin" | "off" | null {
  if (s.config.enabled) return null;
  return s.config.asked ? "off" : "optin";
}

/** 增长文案：+5.6 GB；变小写 −；下限交给 atLeast 包一层（「至少 …」）；没有可比的记录为 —。 */
export function fmtDelta(d: GrowthDelta | null | undefined, atLeast: (s: string) => string): string {
  if (!d) return "—";
  const s = (d.bytes < 0 ? "−" : "+") + human(Math.abs(d.bytes));
  return d.atLeast ? atLeast(s) : s;
}

/** 按 7 天增长（没有可比数据的排在后面，再按占用）或按占用排序；返回新数组。 */
export function sortApps(apps: GrowthApp[], by: GrowthSort): GrowthApp[] {
  const bySize = (a: GrowthApp, b: GrowthApp) => b.size - a.size || a.name.localeCompare(b.name);
  if (by === "size") return [...apps].sort(bySize);
  return [...apps].sort((a, b) => {
    const wa = a.week?.bytes ?? Number.NEGATIVE_INFINITY;
    const wb = b.week?.bytes ?? Number.NEGATIVE_INFINITY;
    return wb - wa || bySize(a, b);
  });
}

/** 30 天迷你曲线的 SVG path：-1（那天没有记录）处断开；不到两个点返回 ""。 */
export function sparkPath(values: number[], w: number, h: number): string {
  const known = values.filter((v) => v >= 0);
  if (known.length < 2) return "";
  const lo = Math.min(...known);
  const span = Math.max(...known) - lo || 1;
  const step = values.length > 1 ? w / (values.length - 1) : 0;
  let d = "";
  let pen = false;
  values.forEach((v, i) => {
    if (v < 0) { pen = false; return; }
    d += `${pen ? "L" : "M"}${(i * step).toFixed(1)} ${(h - ((v - lo) / span) * h).toFixed(1)}`;
    pen = true;
  });
  return d;
}

/** 卸载能移走多少（freed，含 App 本身），这一行的数据里还剩多少（rest，spec §六）。 */
export function uninstallGap(rowSize: number, detail: UninstallDetail | null): { freed: number; rest: number } | null {
  if (!detail) return null;
  const data = detail.items.filter((it) => it.kind !== "app").reduce((n, it) => n + it.size, 0);
  return { freed: detail.totalSize, rest: Math.max(0, rowSize - data) };
}
