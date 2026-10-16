// 系统状态页：真实指标渲染、采样生命周期、提示条与应用列表。
// 阈值常量与文案同文件（设计稿 §9）；后端只发原始数字。
import {
  $, errText, esc, toast, errorState, overlay,
  modalTitle, modalBody, modalOk, showModal, setPending,
} from "./shared";
import { t } from "./i18n";
import { Cleaner } from "./mygo";
import type { AppUsage, StatusSample, SystemInfo } from "./mygo";
import { Channel } from "mygo-runtime";

export const STATUS_ALERT = {
  /** kern.memorystatus_vm_pressure_level：1 正常 / 2 紧张 / 4 严重，未知当正常 */
  pressure: { 1: "ok", 2: "warn", 4: "crit" } as Record<number, string>,
  /** 内存提示条里追加交换文件那行的下限 */
  swapMentionGB: 1,
  /** 压力正常时独立提示交换文件的下限 */
  swapStandaloneGB: 8,
  /** 磁盘可用空间告警下限 */
  diskFreeWarnGB: 20,
} as const;

const GiB = 2 ** 30;
const MiB = 2 ** 20;
/** CPU 曲线保留的点数：2s × 30 = 最近 1 分钟 */
const SPARK_POINTS = 30;

const machineEl = $("#st-machine");
const alertsEl = $("#st-alerts");
const cardsEl = $("#st-cards");
const listHeadEl = $("#st-list-head");
const segMemEl = $<HTMLButtonElement>("#st-seg-mem");
const segCpuEl = $<HTMLButtonElement>("#st-seg-cpu");
const listEl = $("#st-list");
const footnoteEl = $("#st-footnote");

// ---- 状态 ----

let inited = false;
/** 当前是否停在系统状态视图（切页时为 false） */
let inView = false;
/** 采样通道是否在跑 */
let active = false;
/** 本轮通道是被我们自己关掉的（切页 / 隐藏），不算错误 */
let stopped = false;
let channel: { close(): void } | null = null;
let sample: StatusSample | null = null;
/** CPU 卡 30 点环形缓冲；重开采样时清空 */
let cpuHist: number[] = [];
let sysInfo: SystemInfo | null = null;
let infoLoading = false;
/** 列表排序：会话内记忆，不持久化 */
let sortBy: "mem" | "cpu" = "mem";
/** 冻结期间的行集合与顺序（只更新数字） */
let frozenRows: AppUsage[] = [];
let hoverIn = false;
let focusIn = false;
/** 正在退出中的 .app 路径 */
const quitBusy = new Set<string>();
/** 已退出的行 key：置灰、去按钮，下轮重排自然消失 */
const quitDone = new Set<string>();
/**
 * quitDone 中已从采样里消失过的 key：同 key 再次出现 = 用户重启了该应用（新实例），
 * 此时恢复行态与退出按钮，而不是停留在「已退出」置灰（QA P2-1）
 */
const quitGone = new Set<string>();
const rowMap = new Map<string, AppUsage>();
/** 「去清理」由 main.ts 注入，与点侧栏「垃圾清理」同一条路径 */
let goClean: (() => void) | null = null;

export function setGoCleanView(fn: () => void) { goClean = fn; }

// ---- 数字格式（设计稿 §6/§13：内存与磁盘整 GiB 不带 .0，如 22 GB / 32 GB / 91 GB） ----

/** <1 GiB → MB 取整；≥1 GiB → GB 一位小数（整 GiB 去掉 .0，如 22 GB）；0 = 读不到 → “—” */
function fmtMem(n: number): string {
  if (!n) return "—";
  if (n >= GiB) {
    const g = (n / GiB).toFixed(1).replace(/\.0$/, "");
    return `${g} GB`;
  }
  return `${Math.round(n / MiB)} MB`;
}

const fmtPct = (v: number) => `${Math.max(0, Math.round(v))}%`;

// ---- 生命周期 ----

export function startStatus() {
  inView = true;
  if (active) return;
  active = true;
  stopped = false;
  sample = null;
  cpuHist = [];
  frozenRows = [];
  quitBusy.clear();
  quitDone.clear();
  quitGone.clear();
  rowMap.clear();
  hoverIn = false;
  focusIn = false;
  void loadInfo();
  renderMachine();
  renderSkeleton();
  void startWatch();
}

export function stopStatus() {
  inView = false;
  endWatch();
}

function endWatch() {
  active = false;
  stopped = true;
  channel?.close();
  channel = null;
}

/** 窗口隐藏 / 显示；hidden 关通道，回来且还在本视图时重开。 */
export function onVisibility(hidden: boolean) {
  if (hidden) {
    // 保持 inView：回来时直接重开
    endWatch();
  } else if (inView) {
    startStatus();
  }
}

async function startWatch() {
  const ch = new Channel<StatusSample>();
  channel = ch;
  try {
    const done = Cleaner.watchStatus(ch);
    for await (const s of ch) {
      if (!active) break;
      sample = s;
      cpuHist.push(s.cpu.usage);
      if (cpuHist.length > SPARK_POINTS) cpuHist.shift();
      render();
    }
    await done;
  } catch (e) {
    if (!stopped) showError(e);
  } finally {
    if (channel === ch) channel = null;
  }
}

function showError(_e: unknown) {
  // 本轮采样已经结束；重试走 startStatus 才能重建通道
  active = false;
  cardsEl.innerHTML = errorState(t("status.error"), () => {
    endWatch();
    startStatus();
  });
  alertsEl.innerHTML = "";
  listEl.innerHTML = "";
  footnoteEl.textContent = "";
}

async function loadInfo() {
  if (sysInfo) { renderMachine(); return; }
  if (infoLoading) return;
  infoLoading = true;
  try { sysInfo = await Cleaner.info(); } catch { sysInfo = null; }
  infoLoading = false;
  renderMachine();
}

// ---- 机器行 ----

function uptimeText(boot: number): string {
  const secs = Math.max(0, Math.floor(Date.now() / 1000 - boot));
  if (secs < 60) return "";
  const days = Math.floor(secs / 86400);
  const hours = Math.floor(secs / 3600) % 24;
  const mins = Math.floor(secs / 60) % 60;
  if (days >= 1) return t("status.uptime.days", { d: days, h: hours });
  if (hours >= 1) return t("status.uptime.hours", { h: hours, m: mins });
  return t("status.uptime.mins", { m: mins });
}

function renderMachine() {
  if (!sysInfo) return;
  const parts: string[] = [];
  if (sysInfo.chip) parts.push(sysInfo.chip);
  if (sysInfo.memory >= GiB) parts.push(t("status.machine.mem", { size: `${Math.round(sysInfo.memory / GiB)} GB` }));
  else if (sysInfo.memory) parts.push(t("status.machine.mem", { size: `${Math.round(sysInfo.memory / MiB)} MB` }));
  if (sysInfo.osVersion) parts.push(`macOS ${sysInfo.osVersion}`);
  const up = sysInfo.bootTime > 0 ? uptimeText(sysInfo.bootTime) : "";
  if (up) parts.push(up);
  machineEl.textContent = parts.join(" · ");
}

// ---- 骨架态（首份采样未到：禁 0 / 假值） ----

const skCard = `<article class="mcard st-skel"><div class="sk-line w40"></div><div class="sk-line w70"></div><div class="sk-line w90"></div></article>`;
const skRow = `<div class="st-row st-skel-row"><div class="sk-line w40"></div><div class="sk-line w60"></div><div class="sk-line w70"></div><div class="sk-line w50"></div></div>`;

function renderSkeleton() {
  alertsEl.innerHTML = "";
  cardsEl.innerHTML = skCard.repeat(4);
  listEl.innerHTML = skRow.repeat(3);
  footnoteEl.textContent = "";
}

// ---- 冻结（设计稿 §10）：hover / 焦点 / 确认框 / 等待期行序不变 ----

const frozen = () => hoverIn || focusIn || quitBusy.size > 0 || !overlay.classList.contains("hidden");

function currentRows(s: StatusSample): AppUsage[] {
  if (frozen() && frozenRows.length > 0) {
    // 行集合与顺序不变，只按最新采样更新数字
    return frozenRows.map((r) => s.apps.find((a) => a.key === r.key) ?? r);
  }
  const rows = [...s.apps].sort(sortBy === "mem" ? (a, b) => b.memory - a.memory : (a, b) => b.cpu - a.cpu);
  frozenRows = rows;
  return rows;
}

// ---- 提示条（设计稿 §9） ----

const alertIcon = `<svg class="st-callout-ico" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 9v4M12 17h.01"/><path d="M10.3 3.9 1.9 18a2 2 0 0 0 1.7 3h16.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/></svg>`;

function renderAlerts(s: StatusSample) {
  interface Alert { cls: string; text: string; extra: string; action?: "apps" | "clean"; label?: string; }
  const items: Alert[] = [];
  const p = s.memory.pressure;
  // 设计稿 §221/§403：提示条里的「占磁盘」用 swapTotal（= /System/Volumes/VM 上的交换文件总占用）
  const swapDisk = s.memory.swapTotal;
  const swapBig = swapDisk >= STATUS_ALERT.swapMentionGB * GiB;
  if (p === 2 || p === 4) {
    items.push({
      cls: p === 4 ? "crit" : "warn",
      text: t(p === 4 ? "status.alert.memCrit" : "status.alert.memWarn"),
      extra: swapBig ? `<span class="st-extra num">${esc(t("status.alert.swapLine", { size: fmtMem(swapDisk) }))}</span>` : "",
      action: "apps",
      label: t("status.alert.viewApps"),
    });
  } else if (swapDisk >= STATUS_ALERT.swapStandaloneGB * GiB) {
    items.push({
      cls: "warn",
      text: t("status.alert.swapOnly", { size: fmtMem(swapDisk) }),
      extra: "",
    });
  }
  if (s.disk.free > 0 && s.disk.free < STATUS_ALERT.diskFreeWarnGB * GiB) {
    items.push({
      cls: "warn",
      text: t("status.alert.diskLow", { size: fmtMem(s.disk.free) }),
      extra: "",
      action: "clean",
      label: t("status.disk.clean"),
    });
  }
  // 前三条互斥取其一，加磁盘条；内存条固定在上；同屏最多 2 条
  alertsEl.innerHTML = items.slice(0, 2).map((a) => `
    <div class="st-callout ${a.cls}" role="status">
      ${alertIcon}
      <div class="st-callout-body">${esc(a.text)}${a.extra}</div>
      ${a.action ? `<button class="btn ghost small" data-alert="${a.action}" type="button">${esc(a.label ?? "")}</button>` : ""}
    </div>`).join("");
}

// ---- 指标卡 ----

const icon = {
  memory: `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="7" width="18" height="10" rx="2"/><path d="M7 10v4M11 10v4M15 10v4"/></svg>`,
  cpu: `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="6" y="6" width="12" height="12" rx="2"/><path d="M9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3"/></svg>`,
  disk: `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="2"/><circle cx="12" cy="12" r="3.2"/><path d="M17.5 7.5h.01"/></svg>`,
  battery: `<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="2" y="8" width="17" height="9" rx="2"/><path d="M21.5 11v3"/><path d="m10 10-2 3h2.2L10 16"/></svg>`,
};

function cardHead(name: string, badge = ""): string {
  return `<div class="mhead">${icon[name as keyof typeof icon] ?? ""}<span>${esc(t(`status.card.${name}` as never))}</span>${badge}</div>`;
}

/** fmtMem 的输出拆成 hero 大数字 / 小单位两段（如 "27.2 GB" → "27.2" + "GB"） */
function splitMem(n: number): [string, string] {
  const s = fmtMem(n);
  const i = s.indexOf(" ");
  return i > 0 ? [s.slice(0, i), s.slice(i + 1)] : [s, ""];
}

function memoryCard(m: StatusSample["memory"]): string {
  const badge = m.pressure === 2
    ? `<em class="mbadge">${esc(t("status.mem.pressureWarn"))}</em>`
    : m.pressure === 4
      ? `<em class="mbadge crit">${esc(t("status.mem.pressureCrit"))}</em>`
      : "";
  const used = m.used;
  const [usedNum, usedUnit] = splitMem(used);
  const hero = m.total
    ? `<div class="mhero num"><b>${usedNum}</b><i>${usedUnit}</i><span class="mctx">/ ${fmtMem(m.total)}</span></div>`
    : `<div class="mhero num"><b class="ph">—</b></div>`;
  const pct = (n: number) => (used > 0 ? Math.max(0, Math.round((n / used) * 100)) : 0);
  const viz = used > 0 ? `
    <div class="mviz"><div class="mstack" role="img" aria-label="${esc(t("status.mem.barLabel", { used: fmtMem(used), app: fmtMem(m.app), wired: fmtMem(m.wired), comp: fmtMem(m.compressed) }))}">
      <i style="width:${pct(m.app)}%;background:var(--seg-app)"></i>
      <i style="width:${pct(m.wired)}%;background:var(--seg-wired)"></i>
      <i style="width:${pct(m.compressed)}%;background:var(--seg-compressed)"></i>
    </div></div>
    <ul class="mlegend num">
      <li><i class="mdot" style="background:var(--seg-app)"></i><span>${esc(t("status.mem.appMem"))}</span><b>${fmtMem(m.app)}</b></li>
      <li><i class="mdot" style="background:var(--seg-wired)"></i><span>${esc(t("status.mem.wired"))}</span><b>${fmtMem(m.wired)}</b></li>
      <li><i class="mdot" style="background:var(--seg-compressed)"></i><span>${esc(t("status.mem.compressed"))}</span><b>${fmtMem(m.compressed)}</b></li>
      <li><span>${esc(t("status.mem.cachedLabel"))}</span><b>${fmtMem(m.cached)}</b></li>
    </ul>` : "";
  const foot = m.swapUsed > 0.1 * GiB
    ? `<div class="mfoot num">${esc(t("status.mem.swap", { used: fmtMem(m.swapUsed), size: fmtMem(m.swapTotal) }))}</div>`
    : `<div class="mfoot"></div>`;
  return `<article class="mcard">
    ${cardHead("memory", badge)}
    ${hero}
    <div class="mhcapt">${esc(t("status.mem.caption"))}</div>
    ${viz}
    ${foot}
  </article>`;
}

/** 30 点 0–100% 面积曲线；重开采样时缓冲清空，不足 2 点不画 */
function sparkSvg(hist: number[]): string {
  if (hist.length < 2) return "";
  const step = 120 / (SPARK_POINTS - 1);
  const y = (v: number) => (28 - (Math.max(0, Math.min(100, v)) / 100) * 26).toFixed(1);
  const pts = hist.map((v, i) => `${(i * step).toFixed(1)},${y(v)}`);
  const line = `M${pts.join(" L")}`;
  const area = `${line} L${((hist.length - 1) * step).toFixed(1)},28 L0,28 Z`;
  return `<div class="st-spark" aria-hidden="true"><svg viewBox="0 0 120 28" preserveAspectRatio="none"><path fill="var(--accent-soft)" d="${area}"/><path fill="none" stroke="var(--accent)" stroke-width="1.5" stroke-linejoin="round" d="${line}"/></svg></div>`;
}

function cpuCard(c: StatusSample["cpu"], cores: number): string {
  const loads: Array<[string, string]> = [
    [t("status.cpu.load1"), c.load1.toFixed(1)],
    [t("status.cpu.load5"), c.load5.toFixed(1)],
    [t("status.cpu.load15"), c.load15.toFixed(1)],
  ];
  const loadCells = loads.map(([label, v]) => `<div><span>${esc(label)}</span><b>${v}</b></div>`).join("");
  return `<article class="mcard">
    ${cardHead("cpu")}
    <div class="mhero num"><b>${Math.round(c.usage)}</b><i>%</i></div>
    <div class="mhcapt">${esc(t("status.cpu.caption"))}</div>
    <div class="mviz">${sparkSvg(cpuHist)}</div>
    <div class="mloads num">${loadCells}</div>
    <div class="mfoot num">${cores > 0 ? esc(t("status.cpu.cores", { n: cores })) : "—"}</div>
  </article>`;
}

function diskCard(d: StatusSample["disk"]): string {
  const usedPct = d.total ? Math.min(100, Math.round((d.used / d.total) * 100)) : 0;
  const [freeNum, freeUnit] = d.free > 0 ? splitMem(d.free) : ["—", ""];
  return `<article class="mcard">
    ${cardHead("disk")}
    <div class="mhero num"><b>${freeNum}</b><i>${freeUnit}</i><span class="mctx">${esc(t("status.disk.avail"))}</span></div>
    <div class="mhcapt">${esc(t("status.disk.totalOnly", { total: fmtMem(d.total) }))}</div>
    <div class="mviz"><div class="mbar"><i style="width:${usedPct}%"></i></div></div>
    <ul class="mlegend num">
      <li><span>${esc(t("status.disk.used"))}</span><b>${usedPct}%</b></li>
      <li><span>${esc(t("status.disk.avail"))}</span><b>${fmtMem(d.free)}</b></li>
    </ul>
    <div class="mfoot"><span>${esc(t("status.disk.footHint"))}</span><button class="mlink" data-act="clean" type="button">${esc(t("status.disk.clean"))} ›</button></div>
  </article>`;
}

function batteryCard(b: NonNullable<StatusSample["battery"]>): string {
  const cond = b.condition.trim().toLowerCase();
  const badge = cond && cond !== "good" && cond !== "normal"
    ? `<em class="mbadge">${esc(t("status.battery.service"))}</em>`
    : "";
  const state = b.full
    ? t("status.battery.full")
    : b.charging
      ? t("status.battery.charging", { pct: `${b.percent}%` })
      : b.plugged
        ? t("status.battery.plugged")
        : t("status.battery.onBattery");
  const healthPct = b.maxCapacity > 0 ? b.maxCapacity : 0;
  // 决策④：状况槽位以后端字符串为准；Good/Normal 显示「正常」，空值降级「—」
  const condVal = !b.condition.trim()
    ? "—"
    : cond === "good" || cond === "normal"
      ? t("status.battery.conditionOk")
      : esc(b.condition.trim());
  return `<article class="mcard">
    ${cardHead("battery", badge)}
    <div class="mhero num"><b>${b.percent}</b><i>%</i></div>
    <div class="mhcapt">${esc(state)}</div>
    <div class="mviz"><div class="mbar${healthPct > 0 && healthPct < 80 ? " low" : ""}"><i style="width:${healthPct}%"></i></div></div>
    <ul class="mlegend num">
      <li><span>${esc(t("status.battery.maxCap"))}</span><b>${healthPct > 0 ? `${healthPct}%` : "—"}</b></li>
      <li><span>${esc(t("status.battery.condition"))}</span><b>${condVal}</b></li>
    </ul>
    <div class="mfoot num">${b.cycles > 0 ? esc(t("status.battery.cycles", { n: b.cycles })) : "—"}</div>
  </article>`;
}

function renderCards(s: StatusSample) {
  const cores = sysInfo?.cores ?? 0;
  cardsEl.innerHTML =
    memoryCard(s.memory) + cpuCard(s.cpu, cores) + diskCard(s.disk) + (s.battery ? batteryCard(s.battery) : "");
}

// ---- 应用列表 ----

/** 首字母圆标的底色：按应用名哈希取自品牌色板（M8：不只靠颜色传义） */
const PALETTE = ["#0c9d66", "#3b82f6", "#8b5cf6", "#f59e0b", "#ef4444", "#14b8a6", "#6366f1", "#ec4899"];
function iconColor(name: string): string {
  let h = 0;
  for (let i = 0; i < name.length; i++) h = (h * 31 + name.charCodeAt(i)) >>> 0;
  return PALETTE[h % PALETTE.length]!;
}

function quitCell(a: AppUsage): string {
  if (!a.quit) return "";
  if (quitDone.has(a.key)) return "";
  if (quitBusy.has(a.path)) {
    return `<button class="st-quit" disabled type="button">${esc(t("status.apps.quitting"))}</button>`;
  }
  return `<button class="st-quit" data-quit="${esc(a.path)}" type="button">${esc(t("status.apps.quit"))}</button>`;
}

function renderList(s: StatusSample) {
  segMemEl.classList.toggle("on", sortBy === "mem");
  segCpuEl.classList.toggle("on", sortBy === "cpu");
  segMemEl.setAttribute("aria-selected", String(sortBy === "mem"));
  segCpuEl.setAttribute("aria-selected", String(sortBy === "cpu"));
  if (s.apps.length === 0) {
    rowMap.clear();
    listEl.innerHTML = `<div class="st-empty">${esc(t("status.apps.empty"))}</div>`;
    return;
  }
  const rows = currentRows(s);
  rowMap.clear();
  rows.forEach((r) => rowMap.set(r.key, r));
  // QA P2-1：已退出的应用在采样里消失过又以同 key 出现 = 新实例，恢复行态与退出按钮
  if (quitDone.size > 0) {
    const present = new Set(s.apps.map((a) => a.key));
    for (const k of quitDone) {
      if (!present.has(k)) quitGone.add(k);
      else if (quitGone.has(k)) {
        quitDone.delete(k);
        quitGone.delete(k);
      }
    }
  }
  listEl.innerHTML = rows.map((r) => `
    <div class="st-row${quitDone.has(r.key) ? " quitted" : ""}" data-key="${esc(r.key)}"${r.path ? ` data-path="${esc(r.path)}"` : ""}>
      <div class="st-app">
        <div class="st-ico" style="background:${iconColor(r.name)}">${esc(r.name.charAt(0).toUpperCase())}</div>
        <span class="st-name" title="${esc(`${r.name} · ${t("status.apps.procs", { n: r.procs })}`)}">${esc(r.name)}</span>
        <span class="st-procs">${esc(t("status.apps.procs", { n: r.procs }))}</span>
      </div>
      <span class="st-col st-mem num">${fmtMem(r.memory)}</span>
      <span class="st-col st-cpu num">${fmtPct(r.cpu)}</span>
      <span class="st-act">${quitCell(r)}</span>
    </div>`).join("");
}

// ---- 渲染入口 ----

/**
 * 每 2 秒整页重绘会重建指标卡和应用列表的 DOM。重建时记下滚动位置再还原，
 * 否则正在看下面的内容时，刷新会把视图弹回顶部。
 */
function preserveScroll<T>(fn: () => T): T {
  const scroller = document.querySelector(".content");
  const y = scroller?.scrollTop ?? 0;
  const out = fn();
  if (scroller) scroller.scrollTop = y;
  return out;
}

function render() {
  const s = sample;
  if (!s) { renderSkeleton(); return; }
  preserveScroll(() => {
    renderMachine();
    renderAlerts(s);
    renderCards(s);
    renderList(s);
    footnoteEl.textContent = t("status.footnote", { n: s.procs.toLocaleString() });
  });
}

/** 语言切换后整页按新语言重绘 */
export function refreshStatusI18n() {
  renderMachine();
  if (!sample) return;
  render();
}

// ---- 退出应用（设计稿 §11：四结果，复用既有 modal 与 toast） ----

/** QuitApp 的结构化错误：{"code":"not-running"} / {"code":"cannot-quit"} */
function quitErrCode(e: unknown): string | null {
  const raw = e instanceof Error ? e.message : String(e);
  try {
    const parsed = JSON.parse(raw) as { code?: string };
    if (parsed && typeof parsed.code === "string") return parsed.code;
  } catch { /* 非 JSON 按原文报 */ }
  return null;
}

function askQuit(a: AppUsage) {
  if (quitBusy.has(a.path)) return;
  setPending(() => doQuit(a));
  modalTitle.textContent = t("status.quit.title", { name: a.name });
  modalBody.textContent = t("status.quit.body", { name: a.name });
  modalOk.textContent = t("status.quit.ok");
  showModal();
}

async function doQuit(a: AppUsage) {
  quitBusy.add(a.path);
  render();
  try {
    const ok = await Cleaner.quitApp(a.path);
    if (ok) {
      toast(t("status.toast.quitDone", { name: a.name }));
      quitDone.add(a.key);
    } else {
      // 5s 超时仍在跑：多半在等未保存内容的确认
      toast(t("status.toast.quitPending", { name: a.name }));
    }
  } catch (e) {
    const code = quitErrCode(e);
    if (code === "not-running") {
      toast(t("status.toast.notRunning", { name: a.name }));
      quitDone.add(a.key);
    } else if (code === "cannot-quit") {
      toast(t("status.toast.cannotQuit", { name: a.name }), "err");
    } else {
      toast(t("status.error") + "：" + errText(e), "err");
    }
  } finally {
    quitBusy.delete(a.path);
    render();
  }
}

// ---- 「查看占内存的应用」联动：切按内存 + 滚动 + 高亮 1s ----

function viewApps() {
  if (sortBy !== "mem") {
    sortBy = "mem";
    if (sample) renderList(sample);
  }
  if (typeof listHeadEl.scrollIntoView === "function") {
    listHeadEl.scrollIntoView({ behavior: "auto", block: "start" });
  }
  listHeadEl.classList.add("hl");
  window.setTimeout(() => listHeadEl.classList.remove("hl"), 1000);
}

// ---- 事件绑定（只绑一次） ----

export function initStatus() {
  if (inited) return;
  inited = true;
  segMemEl.addEventListener("click", () => {
    if (sortBy === "mem") return;
    sortBy = "mem";
    if (sample) renderList(sample);
  });
  segCpuEl.addEventListener("click", () => {
    if (sortBy === "cpu") return;
    sortBy = "cpu";
    if (sample) renderList(sample);
  });
  // hover / 键盘焦点在列表内：冻结行序
  listEl.addEventListener("mouseenter", () => { hoverIn = true; });
  listEl.addEventListener("mouseleave", () => { hoverIn = false; });
  listEl.addEventListener("focusin", () => { focusIn = true; });
  listEl.addEventListener("focusout", () => { focusIn = false; });
  listEl.addEventListener("click", (e) => {
    const btn = (e.target as HTMLElement).closest<HTMLButtonElement>("[data-quit]");
    if (!btn || btn.disabled) return;
    const row = btn.closest<HTMLElement>(".st-row");
    const key = row?.dataset.key;
    const a = key ? rowMap.get(key) : undefined;
    if (a) askQuit(a);
  });
  cardsEl.addEventListener("click", (e) => {
    if ((e.target as HTMLElement).closest("[data-act='clean']")) goClean?.();
  });
  alertsEl.addEventListener("click", (e) => {
    const btn = (e.target as HTMLElement).closest<HTMLElement>("[data-alert]");
    if (btn?.dataset.alert === "apps") viewApps();
    else if (btn?.dataset.alert === "clean") goClean?.();
  });
  document.addEventListener("visibilitychange", () => {
    onVisibility(document.hidden);
  });
}
