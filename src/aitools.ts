import { Cleaner } from "./mygo";
import type { AIScanResult, AITool, AIPart, AIGroup, AISelection, AICleanResult, AIToolInfo } from "./mygo";
import { t } from "./i18n";
import {
  $, errText, human, esc, displayPath, toast, emptyState, errorState, skeletonRows, fmtDate,
  modalTitle, modalBody, modalOk, showModal, setPending,
} from "./shared";

// AI 工具清理：侧栏「AI 工具」分组下每个已安装的工具一页（Claude Code、Codex）。
// 安全分级来自后端：safe 默认勾选；optional 不预选；sensitive（对话记录）按项目/月份手动挑，
// 最近 24 小时的会话后端会保留；protected / unknown 只展示不可选。一律移到废纸篓。

const listEl = $("#ai-list");
const scanBtn = $<HTMLButtonElement>("#ai-scan");
const cleanBtn = $<HTMLButtonElement>("#ai-clean");
const totalEl = $("#ai-total");
const subEl = $("#ai-sub");
const titleEl = $("#ai-title");
const headSubEl = $("#ai-head-sub");
const sectionEl = $("#nav-ai-section");
const heroEl = $("#view-aitools .hero");
const headActionsEl = $("#view-aitools .head-actions");

type Phase = "idle" | "scanning" | "scanned" | "cleaning" | "cleaned";
type Level = "safe" | "optional" | "sensitive" | "unknown" | "protected";
type Window = 7 | 30 | 90;

/** 每个工具一页，各自保存扫描结果和勾选，切换页面不丢。 */
interface ToolState {
  id: string;
  name: string;
  phase: Phase;
  tool: AITool | null;
  scannedAt: number;
  failed: unknown;
  lastClean: AICleanResult | null;
  picked: Set<string>; // part id
  pickedGroups: Map<string, Set<string>>; // part id → group keys
  expanded: Set<string>;
  showLocked: boolean;
  seq: number;
}

const states = new Map<string, ToolState>();
let current = "";
let windowDays: Window = 30;

function state(id: string, name = id): ToolState {
  let s = states.get(id);
  if (!s) {
    s = { id, name, phase: "idle", tool: null, scannedAt: 0, failed: null, lastClean: null,
      picked: new Set(), pickedGroups: new Map(), expanded: new Set(), showLocked: false, seq: 0 };
    states.set(id, s);
  }
  return s;
}
const cur = () => states.get(current);

const DAY = 86400;
const grouped = (part: AIPart) => part.groups.length > 0;
const level = (part: AIPart) => part.level as Level;
const offered = (part: AIPart) => level(part) !== "protected" && level(part) !== "unknown";
const sessionsPart = (part: AIPart) => level(part) === "sensitive";
/** 运行中只有后端标了 runSafe 的部分能清。 */
const blockedByRun = (s: ToolState, part: AIPart) => !!s.tool?.running && !part.runSafe;
const selectable = (s: ToolState, part: AIPart) =>
  offered(part) && !blockedByRun(s, part) && (part.size > 0 || grouped(part));

/** 会话分组：只在保留窗口之外、且不全是最近 24 小时会话时才可选。 */
function groupSelectable(s: ToolState, part: AIPart, g: AIGroup): boolean {
  if (!sessionsPart(part)) return g.size > 0;
  return g.count > g.recent && s.scannedAt - g.last > windowDays * DAY;
}

function partName(part: AIPart): string {
  if (part.id.startsWith("unknown:")) return part.name;
  const key = `ai.part.${part.id}.name` as never;
  const text = t(key);
  return text === key ? part.id : text;
}
function partDesc(part: AIPart): string {
  if (part.id.startsWith("unknown:")) return t("ai.unknownDesc");
  const key = `ai.part.${part.id}.desc` as never;
  const text = t(key);
  return text === key ? "" : text;
}

/** 选中部分里实际会移走的字节数（分组按勾选分组求和）。 */
function chosenSize(s: ToolState, part: AIPart): number {
  if (!selectable(s, part) || !s.picked.has(part.id)) return 0;
  if (!grouped(part)) return part.size;
  const keys = s.pickedGroups.get(part.id) ?? new Set();
  return part.groups.filter((g) => keys.has(g.key) && groupSelectable(s, part, g)).reduce((n, g) => n + g.size, 0);
}
const totalChosen = (s: ToolState) => (s.tool?.parts ?? []).reduce((n, p) => n + chosenSize(s, p), 0);
function cleanable(s: ToolState) {
  return (s.tool?.parts ?? []).filter((p) => selectable(s, p)).reduce((n, p) => {
    if (!grouped(p)) return n + p.size;
    return n + p.groups.filter((g) => groupSelectable(s, p, g)).reduce((m, g) => m + g.size, 0);
  }, 0);
}

/** 扫描后的默认勾选：safe 整项勾选（旧版本全选）；其余都不预选。 */
function applyDefaults(s: ToolState) {
  s.picked.clear(); s.pickedGroups.clear();
  for (const part of s.tool?.parts ?? []) {
    if (level(part) !== "safe" || !selectable(s, part)) continue;
    s.picked.add(part.id);
    if (grouped(part)) s.pickedGroups.set(part.id, new Set(part.groups.filter((g) => groupSelectable(s, part, g)).map((g) => g.key)));
  }
}

/** 窗口变了：去掉不再符合条件的已勾选会话分组。 */
function pruneSessionPicks(s: ToolState) {
  for (const part of s.tool?.parts ?? []) {
    if (!sessionsPart(part)) continue;
    const keys = s.pickedGroups.get(part.id);
    if (!keys) continue;
    for (const key of [...keys]) {
      const g = part.groups.find((x) => x.key === key);
      if (!g || !groupSelectable(s, part, g)) keys.delete(key);
    }
    if (keys.size === 0) { s.picked.delete(part.id); s.pickedGroups.delete(part.id); }
  }
}

// ---- 渲染 ----

const caret = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="m9 5 7 7-7 7"/></svg>';
const refreshRing = '<svg viewBox="0 0 24 24" width="28" height="28" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-2.64-6.36"/><path d="M21 3v6h-6"/></svg>';
const lock = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true"><rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/></svg>';
const folderIcon = '<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>';
const levelTag: Record<Level, [string, string]> = {
  safe: ["ai.level.safe", "ok"], optional: ["ai.level.optional", "dim"], sensitive: ["ai.level.sensitive", "warn"],
  unknown: ["ai.level.unknown", "dim"], protected: ["ai.level.protected", "dim"],
};
const tagHtml = (key: string, kind: string) => `<span class="tag ${kind}">${esc(t(key as never))}</span>`;

function render() {
  const s = cur();
  if (!s) return;
  renderHead(s);
  if (s.failed !== null) {
    listEl.innerHTML = errorState(t("scan.error", { msg: errText(s.failed) }), () => void scan(s.id));
    return;
  }
  if (!s.tool) {
    // 统计 5 GB 目录要几秒到几十秒，扫描期间中轴卡片显示转动的环和提示，不能停在「尚未扫描」。
    if (s.phase === "scanning") {
      listEl.innerHTML = `<div class="home-axis">
        <div class="ha-ring ai-ring-spin" aria-label="${esc(t("ai.sub.scanning"))}">${refreshRing}</div>
        <div class="ha-guide">${esc(t("ai.sub.scanning"))}</div>
      </div>`;
      return;
    }
    // 闲置态与垃圾清理页一致：中轴圆环、可释放空间、引导、CTA、安全说明和分类标签。
    const chips = s.id === "codex"
      ? [t("ai.chip.oldVersions"), t("ai.chip.sessions"), t("ai.chip.cache")]
      : [t("ai.chip.sessions"), t("ai.chip.snapshots"), t("ai.chip.cache")];
    listEl.innerHTML = `<div class="home-axis">
      <div class="ha-ring" aria-hidden="true">${refreshRing}</div>
      <div class="ha-avail-label">${esc(t("hero.ready"))}</div>
      <div class="ha-avail num"><b>--</b><span>${esc(t("clean.idleSub"))}</span></div>
      <div class="ha-guide">${esc(t(`ai.idle.guide.${s.id}` as never))}</div>
      <button class="ha-cta" type="button" data-action="scan">${esc(t("ai.idle.cta"))}</button>
      <div class="ha-safety"><svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 2l8 3v6c0 5-3.5 9-8 11-4.5-2-8-6-8-11V5z"/><path d="M9 12l2 2 4-4"/></svg><span>${esc(t("ai.idle.desc"))}</span></div>
      <div class="ha-chips">${chips.map((c) => `<span>${esc(c)}</span>`).join("")}</div>
    </div>`;
    return;
  }
  if (!s.tool.installed) {
    listEl.innerHTML = emptyState(t("ai.notFound", { name: s.name }));
    return;
  }
  const visible = s.tool.parts.filter(offered);
  const locked = s.tool.parts.filter((p) => !offered(p));
  const lockedSize = locked.reduce((n, p) => n + p.size, 0);
  const running = s.tool.running ? `<div class="ai-running">${esc(t("ai.running", { name: s.name }))}</div>` : "";
  listEl.innerHTML = `${running}<section class="junk-group ai-tool">
    <div class="group-rows">${visible.map((p) => partHtml(s, p)).join("") || emptyState(t("ai.nothing"))}</div>
    ${locked.length ? `<button class="junk-more ai-locked-toggle" type="button" data-action="locked" aria-expanded="${s.showLocked}">
        ${lock}<span>${esc(t("ai.locked", { n: locked.length, size: human(lockedSize) }))}</span><span class="junk-fold${s.showLocked ? " open" : ""}">${caret}</span></button>
      ${s.showLocked ? `<div class="group-rows ai-locked">${locked.map((p) => partHtml(s, p)).join("")}</div>` : ""}` : ""}
  </section><div class="junk-foot muted small">${esc(t("ai.foot"))}</div>`;
  syncChecks(s);
}

function renderHead(s: ToolState) {
  titleEl.textContent = s.name;
  const head = t(`ai.head.${s.id}` as never);
  headSubEl.textContent = head.startsWith("ai.head.") ? "" : head;
  // 只有真正没扫过（idle）才隐藏占用条；扫描中要显示「正在统计」。
  const idle = s.phase === "idle";
  heroEl.classList.toggle("hidden", idle);
  headActionsEl.classList.toggle("hidden", idle);
  const busy = s.phase === "scanning" || s.phase === "cleaning";
  scanBtn.disabled = busy;
  if (s.phase === "scanning") scanBtn.innerHTML = `<span class="spin"></span>${esc(t("scan.busy"))}`;
  else if (s.tool) scanBtn.textContent = t("fs.rescan");
  else scanBtn.textContent = t("ai.idle.cta");
  const chosen = totalChosen(s);
  cleanBtn.disabled = (s.phase !== "scanned" && s.phase !== "cleaned") || chosen === 0;
  if (s.phase === "cleaning") cleanBtn.innerHTML = `<span class="spin"></span>${esc(t("cat.cleaning"))}`;
  else cleanBtn.textContent = t("ai.clean", { size: human(chosen) });
  if (!s.tool || !s.tool.installed) {
    totalEl.textContent = "--";
    subEl.textContent = s.phase === "scanning" ? t("ai.sub.scanning") : t("ai.sub.idle");
  } else if (s.phase === "cleaned" && s.lastClean) {
    totalEl.textContent = human(s.tool.size);
    const kept = s.lastClean.parts.reduce((n, r) => n + r.kept, 0);
    const skipped = s.lastClean.parts.filter((r) => r.skipped || r.failed.length).length;
    subEl.textContent = [t("ai.done", { size: human(s.lastClean.freed) }),
      kept > 0 ? t("ai.done.kept", { n: kept }) : "", skipped > 0 ? t("ai.done.skipped", { n: skipped }) : ""].filter(Boolean).join(t("phrase.sep"));
  } else {
    totalEl.textContent = human(s.tool.size);
    subEl.textContent = t("ai.sub.scanned", { size: human(cleanable(s)) });
  }
}

function partHtml(s: ToolState, part: AIPart): string {
  const can = selectable(s, part) && (s.phase === "scanned" || s.phase === "cleaned");
  const lv = level(part);
  const [tagKey, tagKind] = levelTag[lv];
  const runTag = blockedByRun(s, part) && offered(part) ? tagHtml("ai.quitFirst", "warn") : "";
  const keep = part.keep ? `<div class="cat-sub muted">${esc(t("ai.keepRelease", { v: part.keep.split("-")[0] ?? part.keep }))}</div>` : "";
  const open = s.expanded.has(part.id);
  const size = grouped(part) && s.picked.has(part.id) ? `${human(chosenSize(s, part))} / ${human(part.size)}` : human(part.size);
  const check = offered(part)
    ? `<label class="cat-check"><input type="checkbox" data-action="part" data-part="${esc(part.id)}" aria-label="${esc(t("junk.select", { name: partName(part) }))}" ${can ? "" : "disabled"} ${s.picked.has(part.id) ? "checked" : ""}></label>`
    : `<span class="cat-check ai-lock" aria-hidden="true">${lock}</span>`;
  const hasDetails = part.groups.length > 0 || part.top.length > 0 || part.more > 0;
  const actions = `
    <button class="junk-icon-btn ai-reveal" type="button" data-action="reveal-part" data-part="${esc(part.id)}" title="${esc(t("fs.reveal"))}" aria-label="${esc(t("fs.reveal"))}">${folderIcon}</button>
    ${hasDetails ? `<button class="junk-icon-btn junk-expand${open ? " open" : ""}" type="button" data-action="expand" data-part="${esc(part.id)}"
          aria-expanded="${open}" aria-label="${esc(t(open ? "fs.collapse" : "fs.expand"))}" title="${esc(t(open ? "fs.collapse" : "fs.expand"))}">${caret}</button>` : '<span class="junk-action-space"></span>'}`;
  return `<div class="junk-item ai-part ai-${lv}">
    <div class="cat-row junk-row">
      ${check}
      <div class="cat-info"><div class="cat-name">${esc(partName(part))}${tagHtml(tagKey, tagKind)}${runTag}</div>
        <div class="cat-desc muted">${esc(partDesc(part))}</div>${keep}</div>
      <div class="cat-note">${size}</div>
      <div class="junk-row-actions">${actions}</div>
    </div>${open ? detailsHtml(s, part) : ""}</div>`;
}

function detailsHtml(s: ToolState, part: AIPart): string {
  if (part.groups.length > 0) return groupsHtml(s, part);
  return topHtml(part);
}

function topHtml(part: AIPart): string {
  if (part.top.length === 0) return "";
  const rows = part.top.map((e) => `
    <div class="junk-sub-row ai-top-row">
      <span class="ai-top-icon" aria-hidden="true">${folderIcon}</span>
      <div class="junk-sub-info"><button class="junk-link ai-reveal-link" type="button" data-action="reveal-entry" data-path="${esc(e.path)}"
          title="${esc(t("fs.reveal"))}">${esc(e.name)}${e.isDir ? "/" : ""}</button>
        <div class="muted small">${esc(displayPath(e.path))}</div></div>
      <span class="junk-size">${human(e.size)}</span>
    </div>`).join("");
  return `<div class="junk-details ai-groups">${rows}
    ${part.more > 0 ? `<div class="muted small ai-more">${esc(t("ai.more", { n: part.more }))}</div>` : ""}</div>`;
}

function groupsHtml(s: ToolState, part: AIPart): string {
  const keys = s.pickedGroups.get(part.id) ?? new Set<string>();
  const locked = !selectable(s, part) || (s.phase !== "scanned" && s.phase !== "cleaned");
  const windowBar = sessionsPart(part) ? `<div class="ai-window">
      <span class="muted small">${esc(t("ai.window.label"))}</span>
      <div class="seg" role="radiogroup" aria-label="${esc(t("ai.window.label"))}">
        ${([7, 30, 90] as Window[]).map((d) => `<button type="button" role="radio" data-action="window" data-days="${d}" aria-checked="${windowDays === d}" class="${windowDays === d ? "on" : ""}">${esc(t("ai.window.days", { n: d }))}</button>`).join("")}
      </div>
      <button class="junk-link" type="button" data-action="pick-old" data-part="${esc(part.id)}" ${locked ? "disabled" : ""}>${esc(t("ai.pickOld"))}</button>
    </div>
    <p class="muted small ai-hint">${esc(t("ai.sessionHint"))}</p>` : "";
  const rows = part.groups.map((g) => {
    const can = !locked && groupSelectable(s, part, g);
    const label = sessionsPart(part) && g.label.startsWith("/") ? displayPath(g.label) : g.label;
    const notes = sessionsPart(part) ? [
      t("ai.group.sessions", { n: g.count }),
      g.last ? t("ai.group.last", { date: fmtDate(g.last) }) : "",
      g.recent > 0 ? t("ai.group.recent", { n: g.recent }) : "",
    ].filter(Boolean).join(" · ") : t("ai.group.release");
    const why = sessionsPart(part) && !groupSelectable(s, part, g)
      ? tagHtml(g.count <= g.recent ? "ai.group.allRecent" : "ai.group.inWindow", "dim") : "";
    return `<div class="junk-sub-row">
      <input type="checkbox" data-action="group" data-part="${esc(part.id)}" data-group="${esc(g.key)}" aria-label="${esc(t("junk.select", { name: label }))}"
        ${can ? "" : "disabled"} ${can && keys.has(g.key) ? "checked" : ""}>
      <div class="junk-sub-info"><button class="junk-link ai-reveal-link" type="button" data-action="reveal-group" data-part="${esc(part.id)}" data-group="${esc(g.key)}"
          title="${esc(t("fs.reveal"))}"><span class="ai-group-label">${esc(label)}</span></button>${g.gone ? tagHtml("ai.group.gone", "dim") : ""}${why}
        <div class="muted small">${esc(notes)}</div></div>
      <span class="junk-size">${human(g.size)}</span>
    </div>`;
  }).join("");
  return `<div class="junk-details ai-groups">${windowBar}${rows}</div>`;
}

function syncChecks(s: ToolState) {
  listEl.querySelectorAll<HTMLInputElement>('input[data-action="part"]').forEach((cb) => {
    const part = s.tool?.parts.find((p) => p.id === cb.dataset.part);
    if (!part || !grouped(part)) return;
    const eligible = part.groups.filter((g) => groupSelectable(s, part, g)).length;
    const n = s.pickedGroups.get(part.id)?.size ?? 0;
    cb.indeterminate = s.picked.has(part.id) && n > 0 && n < eligible;
  });
}

function togglePart(s: ToolState, partID: string, on: boolean) {
  const part = s.tool?.parts.find((p) => p.id === partID);
  if (!part || !selectable(s, part)) return;
  if (grouped(part)) {
    // 对话记录勾整项时只勾符合条件的分组，并展开让用户看清勾了什么。
    const keys = on ? part.groups.filter((g) => groupSelectable(s, part, g)).map((g) => g.key) : [];
    if (keys.length === 0) { s.picked.delete(partID); s.pickedGroups.delete(partID); if (on) s.expanded.add(partID); return; }
    s.pickedGroups.set(partID, new Set(keys));
    if (sessionsPart(part)) s.expanded.add(partID);
  }
  if (on) s.picked.add(partID); else { s.picked.delete(partID); s.pickedGroups.delete(partID); }
}

function toggleGroup(s: ToolState, partID: string, group: string, on: boolean) {
  const keys = s.pickedGroups.get(partID) ?? new Set<string>();
  if (on) keys.add(group); else keys.delete(group);
  s.pickedGroups.set(partID, keys);
  if (keys.size > 0) s.picked.add(partID); else { s.picked.delete(partID); s.pickedGroups.delete(partID); }
}

// ---- 侧栏、扫描与清理 ----

/** 侧栏里这个工具的入口在扫描或清理时显示忙碌标记。 */
function setToolBusy(id: string, on: boolean) {
  document.querySelector<HTMLElement>(`.nav-item[data-tool="${id}"]`)?.classList.toggle("busy", on);
}

/** 启动时只看哪些工具装了：没装的不出现在侧栏，一个都没装时整个分组隐藏。 */
async function loadTools() {
  let list: AIToolInfo[] = [];
  try {
    list = await Cleaner.listAITools();
  } catch {
    list = [];
  }
  let any = false;
  for (const info of list) {
    document.querySelector<HTMLElement>(`.nav-item[data-tool="${info.id}"]`)?.classList.toggle("hidden", !info.installed);
    if (info.installed) {
      any = true;
      state(info.id, info.name).name = info.name;
    }
  }
  sectionEl.classList.toggle("hidden", !any);
}

async function scan(id: string) {
  const s = state(id);
  if (s.phase === "scanning" || s.phase === "cleaning") return;
  const seq = ++s.seq;
  s.phase = "scanning"; s.failed = null; s.lastClean = null;
  setToolBusy(id, true);
  if (id === current) render();
  try {
    const res = await Cleaner.scanAITool(id);
    if (seq !== s.seq) return;
    s.tool = res.tools[0] ?? null;
    s.scannedAt = res.scannedAt;
    if (s.tool) s.name = s.tool.name;
    applyDefaults(s);
  } catch (e) {
    if (seq !== s.seq) return;
    s.failed = e; s.tool = null;
    if (id === current) toast(t("scan.error", { msg: errText(e) }), "err");
  } finally {
    if (seq === s.seq) {
      s.phase = "scanned"; setToolBusy(id, false);
      if (id === current) render();
    }
  }
}

function selections(s: ToolState): { list: AISelection[]; lines: string[] } {
  const list: AISelection[] = [];
  const lines: string[] = [];
  for (const part of s.tool?.parts ?? []) {
    const size = chosenSize(s, part);
    if (size === 0) continue;
    const groups = grouped(part) ? part.groups.filter((g) => s.pickedGroups.get(part.id)?.has(g.key) && groupSelectable(s, part, g)).map((g) => g.key) : [];
    list.push({ tool: s.id, part: part.id, groups });
    const lose = t(`ai.part.${part.id}.lose` as never);
    const count = grouped(part) ? t(sessionsPart(part) ? "ai.confirm.groups" : "ai.confirm.releases", { n: groups.length }) : "";
    lines.push(`<li><b>${esc(partName(part))}</b> ${esc(human(size))}${count ? ` · ${esc(count)}` : ""}
      <div class="muted small">${esc(lose.startsWith("ai.part.") ? t("ai.lose.default") : lose)}</div></li>`);
  }
  return { list, lines };
}

function askClean() {
  const s = cur();
  if (!s || (s.phase !== "scanned" && s.phase !== "cleaned")) return;
  const { list, lines } = selections(s);
  if (list.length === 0) return;
  const id = s.id;
  setPending(() => clean(id, list));
  modalTitle.textContent = t("ai.confirm.title");
  modalBody.innerHTML = `<p>${esc(t("ai.confirm.body", { name: s.name, size: human(totalChosen(s)) }))}</p><ul class="ai-confirm">${lines.join("")}</ul>
    <p class="muted small">${esc(t("ai.confirm.safety"))}</p>`;
  modalOk.textContent = t("ai.confirm.ok");
  showModal();
}

async function clean(id: string, list: AISelection[]) {
  const s = state(id);
  if (s.phase !== "scanned" && s.phase !== "cleaned") return;
  s.phase = "cleaning"; setToolBusy(id, true);
  if (id === current) render();
  let done: AICleanResult | null = null;
  try {
    done = await Cleaner.cleanAITools(list);
  } catch (e) {
    toast(t("clean.fail", { msg: errText(e) }), "err");
  }
  setToolBusy(id, false);
  s.phase = "scanned";
  if (!done) { if (id === current) render(); return; }
  // 清理后重新扫描，界面显示真实剩余占用；结果摘要保留在头部。
  await scan(id);
  s.lastClean = done; s.phase = "cleaned";
  if (id === current) render();
  toast(done.freed > 0 ? t("ai.done", { size: human(done.freed) }) : t("clean.nothing"));
}

// ---- 在 Finder 中显示 ----

function reveal(path: string) {
  Cleaner.revealInFinder(path).catch((e) => toast(t("fs.revealFail", { msg: errText(e) }), "err"));
}
function revealPart(s: ToolState, partID: string) {
  const p = s.tool?.parts.find((x) => x.id === partID);
  if (p) reveal(p.path);
}
function revealGroup(s: ToolState, partID: string, key: string) {
  const p = s.tool?.parts.find((x) => x.id === partID);
  const g = p?.groups.find((x) => x.key === key);
  if (g?.path) reveal(g.path);
}

// ---- 入口 ----

export function refreshAIToolsI18n() { render(); }

/** 侧栏点进某个工具：不自动扫描，只显示入口和上次的结果，扫描由用户点按钮触发。 */
export function showAITool(id: string) {
  current = id;
  render();
}

export function initAITools() {
  scanBtn.addEventListener("click", () => { if (current) void scan(current); });
  cleanBtn.addEventListener("click", askClean);
  listEl.addEventListener("click", (e) => {
    const el = (e.target as HTMLElement).closest<HTMLElement>("[data-action]");
    const s = cur();
    // 勾选由 change 事件处理；这里遇到勾选框直接忽略（点 label 也会冒泡到这）。
    if (!el || !s || (el instanceof HTMLInputElement && (el.dataset.action === "part" || el.dataset.action === "group"))) return;
    const action = el.dataset.action;
    if (action === "expand") {
      const id = el.dataset.part!;
      if (s.expanded.has(id)) s.expanded.delete(id); else s.expanded.add(id);
    } else if (action === "scan") {
      void scan(s.id); return;
    } else if (action === "locked") {
      s.showLocked = !s.showLocked;
    } else if (action === "window") {
      windowDays = Number(el.dataset.days) as Window;
      states.forEach(pruneSessionPicks);
    } else if (action === "pick-old") {
      togglePart(s, el.dataset.part!, true);
    } else if (action === "reveal-part") {
      revealPart(s, el.dataset.part!); return;
    } else if (action === "reveal-group") {
      revealGroup(s, el.dataset.part!, el.dataset.group!); return;
    } else if (action === "reveal-entry") {
      if (el.dataset.path) reveal(el.dataset.path);
      return;
    } else return;
    render();
  });
  listEl.addEventListener("change", (e) => {
    const cb = e.target as HTMLInputElement;
    const s = cur();
    if (!s || (s.phase !== "scanned" && s.phase !== "cleaned")) return;
    if (cb.dataset.action === "part") togglePart(s, cb.dataset.part!, cb.checked);
    else if (cb.dataset.action === "group") toggleGroup(s, cb.dataset.part!, cb.dataset.group!, cb.checked);
    render();
  });
  void loadTools();
}

// 测试用：直接注入某个工具的扫描结果，不经过 IPC。
export const __test = {
  loadTools,
  load(id: string, res: AIScanResult) {
    const s = state(id);
    s.tool = res.tools[0] ?? null; s.scannedAt = res.scannedAt; s.phase = "scanned"; s.failed = null; s.lastClean = null;
    if (s.tool) s.name = s.tool.name;
    applyDefaults(s); current = id; render();
  },
  picked: () => [...(cur()?.picked ?? [])],
  groups: (part: string) => [...(cur()?.pickedGroups.get(part) ?? [])],
  selections: () => selections(cur()!).list,
  setWindow(d: Window) { windowDays = d; states.forEach(pruneSessionPicks); render(); },
  togglePart: (part: string, on: boolean) => togglePart(cur()!, part, on),
  toggleGroup: (part: string, group: string, on: boolean) => toggleGroup(cur()!, part, group, on),
  totalChosen: () => totalChosen(cur()!),
};
