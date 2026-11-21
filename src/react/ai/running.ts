// Claude Code 页「运行中」标签的纯逻辑：标签上的数、时长、结束的目标、会话的名字、冻结、结束后的提示。不依赖 DOM / React，可单测。
import type { ClaudeEndResult, ClaudeLeft, ClaudeProc, ClaudeRunning, ClaudeTarget } from "../../mygo";
import type { useT } from "../../ui/i18n-react";

type T = ReturnType<typeof useT>;

/** 空闲满 2 小时的会话才计入标签上的数；标签显示着时每 5 秒读一次。 */
export const RUNNING = { idleNoteSec: 7200, refreshMs: 5000 } as const;

const MINUTE = 60;
const HOUR = 3600;
const DAY = 86400;

/** 标签上的数：已关闭的会话留下的进程的行数，加上空闲满 2 小时的会话数。空闲了多久读不到（since 是 0）的不算。 */
export function badgeCount(r: ClaudeRunning, nowSec: number): number {
  const rows = r.left.reduce((n, g) => n + g.procs.length, 0);
  const idle = r.sessions.filter((s) => s.status === "idle" && s.since > 0 && nowSec - s.since >= RUNNING.idleNoteSec).length;
  return rows + idle;
}

/** 时长：不满 1 小时写分钟（最少 1），不满 24 小时写小时，否则写天，都往下取整。 */
export function durationText(sec: number, t: T): string {
  if (sec < HOUR) return t("ai.run.dur.min", { n: Math.max(1, Math.floor(sec / MINUTE)) });
  if (sec < DAY) return t("ai.run.dur.hour", { n: Math.floor(sec / HOUR) });
  return t("ai.run.dur.day", { n: Math.floor(sec / DAY) });
}

/** 结束一行要发的目标：这一行自己在最前，然后是并进这一行的进程。 */
export function procTargets(p: ClaudeProc): ClaudeTarget[] {
  return [{ pid: p.pid, start: p.start }, ...p.folded];
}

/** 「全部结束」的目标：这一组每一行的目标接起来。 */
export function leftTargets(g: ClaudeLeft): ClaudeTarget[] {
  return g.procs.flatMap(procTargets);
}

/** 会话的名字：「项目 · 标题」，缺一样只写另一样，都没有是「未知会话」。 */
export function sessionName(s: { project: string; title: string }, t: T): string {
  return [s.project, s.title].filter(Boolean).join(" · ") || t("ai.run.unknownSession");
}

/**
 * 确认框开着、正在结束时显示的读数：行和顺序照 shown，每一行按 pid 换成 next 里的那一行；
 * next 里没有的行照旧留着，next 里新出现的不加进来。读不读得到会话记录也照 shown：列表不会在确认框后面整段消失。
 */
export function freezeRunning(shown: ClaudeRunning, next: ClaudeRunning): ClaudeRunning {
  const sessions = new Map(next.sessions.map((s) => [s.pid, s]));
  const procs = new Map([...next.sessions.flatMap((s) => s.commands), ...next.left.flatMap((g) => g.procs)].map((p) => [p.pid, p]));
  const fresh = (p: ClaudeProc) => procs.get(p.pid) ?? p;
  return {
    ...next,
    noRecords: shown.noRecords,
    sessions: shown.sessions.map((s) => ({ ...(sessions.get(s.pid) ?? s), commands: s.commands.map(fresh) })),
    left: shown.left.map((g) => ({ ...g, procs: g.procs.map(fresh) })),
  };
}

/**
 * 结束后的提示：都结束了写结束了几个、腾出多少；有还在运行或已经变了的，接在后面，用错误提示。
 * 一个都没结束时不写「已结束」。
 */
export function endToast(res: ClaudeEndResult, t: T, human: (n: number) => string): { text: string; kind: "ok" | "err" } {
  const count = (...results: string[]) => res.procs.filter((p) => results.includes(p.result)).length;
  const ended = count("ended", "forced");
  const still = count("running");
  const changed = count("changed");
  const failed = still + changed > 0;
  const text = [
    ended > 0 || !failed ? t("ai.run.done", { n: ended, size: human(res.freed) }) : "",
    still > 0 ? t("ai.run.still", { n: still }) : "",
    changed > 0 ? t("ai.run.changed", { n: changed }) : "",
  ].filter(Boolean).join(" · ");
  return { text, kind: failed ? "err" : "ok" };
}
