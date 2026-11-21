import { baseName, pathSep } from "../../platform";
import * as React from "react";
import { ChevronRight, FolderOpen, Lock } from "lucide-react";
import type { AIGroup, AIPart } from "../../mygo";
import { Checkbox } from "../../ui/components/checkbox";
import { Badge } from "../../ui/components/badge";
import { cn } from "../../ui/cn";
import { useT, type DictKey } from "../../ui/i18n-react";
import { currentLang } from "../../i18n";
import { human, displayPath } from "./format";
import { offered, isSessions, groupSelectable, blockedByRun as runBlocked, type WindowDays } from "./types";

const levelBadge: Record<AIPart["level"], { label: DictKey; variant: "safe" | "optional" | "warn" | "protected" }> = {
  safe: { label: "ai.level.safe", variant: "safe" },
  optional: { label: "ai.level.optional", variant: "optional" },
  sensitive: { label: "ai.level.sensitive", variant: "warn" },
  unknown: { label: "ai.level.unknown", variant: "protected" },
  protected: { label: "ai.level.protected", variant: "protected" },
};

type Translate = ReturnType<typeof useT>;

/** 会话保留窗口选项（顺序即界面从左到右的顺序，也是键盘漫游顺序）。 */
const WINDOW_OPTIONS = [7, 30, 90] as const;

interface Props {
  part: AIPart;
  windowDays: WindowDays;
  scannedAt: number;
  picked: boolean;
  groups: Set<string>;
  expanded: boolean;
  canSelect: boolean;
  chosenSize: number;
  onTogglePart: (on: boolean) => void;
  onToggleGroup: (key: string, on: boolean) => void;
  onExpand: () => void;
  onReveal: (path: string) => void;
  onPickOld: () => void;
  onWindowChange: (d: WindowDays) => void;
}

export function PartRow(p: Props) {
  const t = useT();
  const { part } = p;
  const isOffered = offered(part);
  const badge = levelBadge[part.level as AIPart["level"]] ?? levelBadge.safe!;
  const blockedByRun = runBlocked(part);
  const locked = !p.canSelect || blockedByRun;
  const hasDetails = part.groups.length > 0 || part.top.length > 0 || part.more > 0;
  // 「先退出」也是每个 worktree 勾不上的原因，勾选框连着它。
  const quitId = `cz-quit-${part.id}`;

  const name = part.id.startsWith("unknown:") ? part.name : (() => {
    const k = `ai.part.${part.id}.name` as DictKey;
    const s = t(k);
    return s === k ? part.id : s;
  })();
  const desc = part.id.startsWith("unknown:") ? t("ai.unknownDesc") : (() => {
    const k = `ai.part.${part.id}.desc` as DictKey;
    const s = t(k);
    return s === k ? "" : s;
  })();

  const checked = p.picked;
  const indeterminate = part.groups.length > 0 && p.picked
    ? (() => {
        const eligible = part.groups.filter((g) => groupSelectable(part, g, p.windowDays, p.scannedAt));
        return eligible.length > 0 && p.groups.size > 0 && p.groups.size < eligible.length;
      })()
    : false;

  /** worktree 的一行：文件夹名，「仓库 · 分支 · N 天没动」，状态说明，大小；点行在访达中显示。勾不上的原因连着勾选框。 */
  const worktreeRow = (g: AIGroup, i: number) => {
    const can = !locked && groupSelectable(part, g, p.windowDays, p.scannedAt);
    const note = worktreeNote(g, p.scannedAt, t);
    const state = worktreeState(g, t);
    const stateId = `cz-wt-${part.id}-${i}`;
    const describedBy = [blockedByRun ? quitId : "", state ? stateId : ""].filter(Boolean).join(" ");
    return (
      <div key={g.key} className="flex items-center gap-3 py-2">
        <Checkbox
          checked={p.groups.has(g.key)}
          disabled={!can}
          onCheckedChange={(v) => p.onToggleGroup(g.key, v === true)}
          aria-label={g.label}
          aria-describedby={describedBy || undefined}
        />
        <button
          type="button"
          className="min-w-0 flex-1 text-left"
          title={t("fs.reveal")}
          onClick={() => g.path && p.onReveal(g.path)}
        >
          <span className="block truncate text-[13px] hover:text-accent-strong" title={g.label}>{g.label}</span>
          {note && <span className="block text-xs text-muted">{note}</span>}
          {state && <span id={stateId} className={cn("block text-xs", g.state === "dirty" ? "text-warning-text" : "text-muted")}>{state}</span>}
        </button>
        <span className="shrink-0 text-xs tabular-nums text-muted">{human(g.size)}</span>
      </div>
    );
  };

  return (
    <div className={cn("border-b border-border last:border-b-0", !isOffered && "opacity-90")}>
      <div className="ai-part-head flex items-center gap-3 px-4 pb-[10px] pt-[9px] hover:bg-hover">
        {/* worktree 只能一个一个勾，免得一下勾中好几个带改动的：这一项没有自己的勾选框，位置空着。 */}
        {part.worktrees ? (
          <span className="w-[15px] shrink-0" />
        ) : isOffered ? (
          <Checkbox
            checked={indeterminate ? "indeterminate" : checked}
            disabled={locked}
            onCheckedChange={(v) => p.onTogglePart(v === true || v === "indeterminate")}
            aria-label={name}
          />
        ) : (
          <Lock className="h-4 w-4 text-muted" aria-hidden />
        )}
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5 text-[13.5px] font-semibold">
            <span>{name}</span>
            {/* 运行中被锁住时只显示「先退出」，避免「可安全清理 · 先退出」两个矛盾徽标。 */}
            {blockedByRun
              ? <Badge id={quitId} variant="warn">{t("ai.quitFirst")}</Badge>
              : <Badge variant={badge.variant}>{t(badge.label)}</Badge>}
          </div>
          {desc && <div className="mt-px truncate text-[12px] text-muted" title={desc}>{desc}</div>}
          {part.keep && (
            <div className="mt-[3px] text-[11.5px] text-muted">
              {t("ai.keepRelease", { v: part.keep.split("-")[0] ?? part.keep })}
            </div>
          )}
        </div>
        {/* 尺寸 + 显示/展开：窄窗口整体落到第二行右排（见 style.css 的 @container 规则），
            给名称和徽标让出整行，避免长尺寸文案把名称列压成逐字竖排 */}
        <div className="ai-part-meta flex shrink-0 items-center gap-3">
          <div className="text-[12.5px] font-[590] tabular-nums text-foreground">
            {part.groups.length > 0 && p.picked ? `${human(p.chosenSize)} / ` : ""}{human(part.size)}
          </div>
          {/* Claude Code 的 worktree 分散在各个仓库里，这一项没有自己的文件夹，只有每一行能显示。 */}
          {part.path ? (
            <button
              type="button"
              className="grid h-[30px] w-[30px] place-items-center rounded-[6px] text-muted hover:bg-bg-subtle hover:text-foreground"
              title={t("fs.reveal")}
              aria-label={t("fs.reveal")}
              onClick={() => p.onReveal(part.path)}
            >
              <FolderOpen className="h-4 w-4" />
            </button>
          ) : <span className="h-[30px] w-[30px]" />}
          {hasDetails ? (
            <button
              type="button"
              className="grid h-[30px] w-[30px] place-items-center rounded-[6px] text-muted hover:bg-bg-subtle hover:text-foreground"
              onClick={p.onExpand}
              aria-expanded={p.expanded}
              aria-label={p.expanded ? t("fs.collapse") : t("fs.expand")}
            >
              <ChevronRight className={cn("h-4 w-4 transition-transform", p.expanded && "rotate-90")} />
            </button>
          ) : <span className="h-[30px] w-[30px]" />}
        </div>
      </div>

      {p.expanded && (
        <div className="border-t border-border px-4 pb-3 pl-11 pt-1">
          {part.worktrees && part.groups.map(worktreeRow)}
          {!part.worktrees && part.groups.length > 0 && (
            <Groups
              part={part}
              windowDays={p.windowDays}
              groupsState={p.groups}
              locked={locked}
              now={p.scannedAt}
              onToggleGroup={p.onToggleGroup}
              onReveal={p.onReveal}
              onPickOld={p.onPickOld}
              onWindowChange={p.onWindowChange}
            />
          )}
          {part.groups.length === 0 && part.top.length > 0 && (
            <div>
              {part.top.map((e) => (
                <button
                  key={e.path}
                  type="button"
                  className="flex w-full items-center gap-3 py-2 text-left"
                  title={t("fs.reveal")}
                  onClick={() => p.onReveal(e.path)}
                >
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[13px] text-foreground hover:text-accent-strong" title={`${e.name}${e.isDir ? "/" : ""}`}>{e.name}{e.isDir ? "/" : ""}</span>
                    <span className="block truncate text-xs text-muted" title={e.path}>{displayPath(e.path)}</span>
                  </span>
                  <span className="text-xs tabular-nums text-muted">{human(e.size)}</span>
                </button>
              ))}
              {part.more > 0 && <div className="ai-more muted small">{t("ai.more", { n: part.more })}</div>}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function Groups({
  part, windowDays, groupsState, locked, now, onToggleGroup, onReveal, onPickOld, onWindowChange,
}: {
  part: AIPart;
  windowDays: WindowDays;
  groupsState: Set<string>;
  locked: boolean;
  now: number;
  onToggleGroup: (key: string, on: boolean) => void;
  onReveal: (path: string) => void;
  onPickOld: () => void;
  onWindowChange: (d: WindowDays) => void;
}) {
  const t = useT();
  const sessions = isSessions(part);
  const optionRefs = React.useRef<(HTMLButtonElement | null)[]>([]);
  const currentIndex = WINDOW_OPTIONS.indexOf(windowDays);

  // WAI-ARIA radiogroup 模式：方向键在选项间循环移动焦点并选中，Home/End 跳到首尾。
  const selectIndex = (i: number) => {
    const d = WINDOW_OPTIONS[i];
    if (d === undefined) return;
    onWindowChange(d);
    optionRefs.current[i]?.focus();
  };
  const onRadioKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (currentIndex === -1) return;
    let next: number | null = null;
    switch (e.key) {
      case "ArrowRight":
      case "ArrowDown":
        next = (currentIndex + 1) % WINDOW_OPTIONS.length;
        break;
      case "ArrowLeft":
      case "ArrowUp":
        next = (currentIndex - 1 + WINDOW_OPTIONS.length) % WINDOW_OPTIONS.length;
        break;
      case "Home":
        next = 0;
        break;
      case "End":
        next = WINDOW_OPTIONS.length - 1;
        break;
      default:
        return;
    }
    e.preventDefault();
    selectIndex(next);
  };

  return (
    <div>
      {sessions && (
        <>
          <div className="ai-window">
            <span className="muted small">{t("ai.window.label")}</span>
            <div className="seg" role="radiogroup" aria-label={t("ai.window.label")} onKeyDown={onRadioKeyDown}>
              {WINDOW_OPTIONS.map((d, i) => (
                <button
                  key={d}
                  ref={(el) => { optionRefs.current[i] = el; }}
                  type="button"
                  role="radio"
                  aria-checked={windowDays === d}
                  tabIndex={windowDays === d ? 0 : -1}
                  className={cn(windowDays === d && "on")}
                  onClick={() => onWindowChange(d)}
                >
                  {t("ai.window.days", { n: d })}
                </button>
              ))}
            </div>
            <button
              type="button"
              className="junk-link"
              disabled={locked}
              onClick={onPickOld}
            >
              {t("ai.pickOld")}
            </button>
          </div>
          <p className="muted small ai-hint">{t("ai.sessionHint")}</p>
        </>
      )}
      {part.groups.map((g) => {
        const can = !locked && groupSelectable(part, g, windowDays, now);
        const label = sessions && (g.label.startsWith(pathSep()) || /^[A-Za-z]:\\/.test(g.label)) ? displayPath(g.label) : g.label;
        const notes = sessions
          ? [
              t("ai.group.sessions", { n: g.count }),
              g.last ? t("ai.group.last", { date: fmt(g.last) }) : "",
              g.recent > 0 ? t("ai.group.recent", { n: g.recent }) : "",
            ].filter(Boolean).join(" · ")
          : t("ai.group.release");
        return (
          <div key={g.key} className="flex items-center gap-3 py-2">
            <Checkbox
              checked={groupsState.has(g.key)}
              disabled={!can}
              onCheckedChange={(v) => onToggleGroup(g.key, v === true)}
              aria-label={label}
            />
            <button
              type="button"
              className="min-w-0 flex-1 text-left"
              title={t("fs.reveal")}
              onClick={() => g.path && onReveal(g.path)}
            >
              <span className="flex flex-wrap items-center gap-2">
                <span className="truncate text-[13px] hover:text-accent-strong" title={label}>{label}</span>
                {g.gone && <Badge variant="protected">{t("ai.group.gone")}</Badge>}
                {!can && sessions && (
                  <Badge variant="protected">{g.count <= g.recent ? t("ai.group.allRecent") : t("ai.group.inWindow")}</Badge>
                )}
              </span>
              <span className="block text-xs text-muted">{notes}</span>
            </button>
            <span className="shrink-0 text-xs tabular-nums text-muted">{human(g.size)}</span>
          </div>
        );
      })}
    </div>
  );
}

/** 「仓库 · 分支 · N 天没动」：仓库只写文件夹名，没有分支是游离的 HEAD，不知道最后改动的时间就不写天数；认不出的文件夹不知道仓库，不写。 */
function worktreeNote(g: AIGroup, now: number, t: Translate): string {
  if (!g.repo) return "";
  const vars = { repo: baseName(g.repo), branch: g.branch || t("ai.wt.detached") };
  if (!g.last) return t("ai.wt.noteNoAge", vars);
  return t("ai.wt.note", { ...vars, n: Math.max(0, Math.floor((now - g.last) / 86400)) });
}

/** 状态说明：干净的不写，带改动的写有几处；认不出的状态（以后新加的也算）说认不出。 */
function worktreeState(g: AIGroup, t: Translate): string {
  if (g.state === "clean") return "";
  if (g.state === "dirty") return t("ai.wt.state.dirty", { n: g.changes ?? 0 });
  const k = `ai.wt.state.${g.state ?? ""}` as DictKey;
  const s = t(k);
  return s === k ? t("ai.wt.state.unrecognized") : s;
}

function fmtDateLike(sec: number): string {
  const d = new Date(sec * 1000);
  return d.toLocaleDateString(currentLang() === "zh" ? "zh-CN" : "en-US", { year: "numeric", month: "2-digit", day: "2-digit" });
}
const fmt = fmtDateLike;
