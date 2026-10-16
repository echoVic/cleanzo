import * as React from "react";
import { ChevronRight, FolderOpen, Lock } from "lucide-react";
import type { AIPart, AITool } from "../../mygo";
import { Checkbox } from "../../ui/components/checkbox";
import { Badge } from "../../ui/components/badge";
import { cn } from "../../ui/cn";
import { useT, type DictKey } from "../../ui/i18n-react";
import { human, displayPath } from "./format";
import { offered, isSessions, groupSelectable, type WindowDays } from "./types";

const levelBadge: Record<AIPart["level"], { label: DictKey; variant: "safe" | "optional" | "warn" | "protected" }> = {
  safe: { label: "ai.level.safe", variant: "safe" },
  optional: { label: "ai.level.optional", variant: "optional" },
  sensitive: { label: "ai.level.sensitive", variant: "warn" },
  unknown: { label: "ai.level.unknown", variant: "protected" },
  protected: { label: "ai.level.protected", variant: "protected" },
};

interface Props {
  tool: AITool;
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
  const blockedByRun = p.tool.running && !part.runSafe;
  const hasDetails = part.groups.length > 0 || part.top.length > 0 || part.more > 0;
  const sessions = isSessions(part);

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
        const eligible = part.groups.filter((g) => groupSelectable(part, g, p.windowDays, p.tool ? 0 : 0));
        return eligible.length > 0 && p.groups.size > 0 && p.groups.size < eligible.length;
      })()
    : false;

  return (
    <div className={cn("border-b border-border last:border-b-0", !isOffered && "opacity-90")}>
      <div className="flex items-center gap-3 px-4 py-3">
        {isOffered ? (
          <Checkbox
            checked={indeterminate ? "indeterminate" : checked}
            disabled={!p.canSelect || blockedByRun}
            onCheckedChange={(v) => p.onTogglePart(v === true || v === "indeterminate")}
            aria-label={name}
          />
        ) : (
          <Lock className="h-4 w-4 text-muted" aria-hidden />
        )}
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2 text-[15px]">
            <span className="font-medium">{name}</span>
            <Badge variant={badge.variant}>{t(badge.label)}</Badge>
            {blockedByRun && isOffered && <Badge variant="warn">{t("ai.quitFirst")}</Badge>}
          </div>
          {desc && <div className="mt-0.5 truncate text-[13px] text-muted">{desc}</div>}
          {part.keep && (
            <div className="mt-0.5 text-xs text-muted">
              {t("ai.keepRelease", { v: part.keep.split("-")[0] ?? part.keep })}
            </div>
          )}
        </div>
        <div className="shrink-0 text-sm tabular-nums text-foreground/80">
          {part.groups.length > 0 && p.picked ? `${human(p.chosenSize)} / ` : ""}{human(part.size)}
        </div>
        <button
          type="button"
          className="grid h-8 w-8 place-items-center rounded-md text-muted hover:bg-muted-background hover:text-foreground"
          title={t("fs.reveal")}
          aria-label={t("fs.reveal")}
          onClick={() => p.onReveal(part.path)}
        >
          <FolderOpen className="h-4 w-4" />
        </button>
        {hasDetails ? (
          <button
            type="button"
            className="grid h-8 w-8 place-items-center rounded-md text-muted hover:bg-muted-background hover:text-foreground"
            onClick={p.onExpand}
            aria-expanded={p.expanded}
            aria-label={p.expanded ? t("fs.collapse") : t("fs.expand")}
          >
            <ChevronRight className={cn("h-4 w-4 transition-transform", p.expanded && "rotate-90")} />
          </button>
        ) : <span className="h-8 w-8" />}
      </div>

      {p.expanded && (
        <div className="border-t border-border bg-muted-background/40 px-4 py-2 pl-11">
          {part.groups.length > 0 && (
            <Groups
              part={part}
              windowDays={p.windowDays}
              groupsState={p.groups}
              locked={!p.canSelect || blockedByRun}
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
                  className="flex w-full items-center gap-3 py-1.5 text-left"
                  title={t("fs.reveal")}
                  onClick={() => p.onReveal(e.path)}
                >
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[13px] text-foreground hover:text-primary">{e.name}{e.isDir ? "/" : ""}</span>
                    <span className="block truncate text-xs text-muted">{displayPath(e.path)}</span>
                  </span>
                  <span className="text-xs tabular-nums text-muted">{human(e.size)}</span>
                </button>
              ))}
              {part.more > 0 && <div className="py-1 text-xs text-muted">{t("ai.more", { n: part.more })}</div>}
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
  return (
    <div>
      {sessions && (
        <div className="mb-2 flex flex-wrap items-center gap-3">
          <span className="text-xs text-muted">{t("ai.window.label")}</span>
          <div className="flex gap-1 rounded-lg bg-background/60 p-0.5">
            {([7, 30, 90] as WindowDays[]).map((d) => (
              <button
                key={d}
                type="button"
                role="radio"
                aria-checked={windowDays === d}
                className={cn(
                  "rounded-md px-3 py-1 text-xs",
                  windowDays === d ? "bg-card font-semibold shadow-sm" : "text-muted",
                )}
                onClick={() => onWindowChange(d)}
              >
                {t("ai.window.days", { n: d })}
              </button>
            ))}
          </div>
          <button
            type="button"
            className="text-xs text-primary hover:underline disabled:opacity-40"
            disabled={locked}
            onClick={onPickOld}
          >
            {t("ai.pickOld")}
          </button>
        </div>
      )}
      {sessions && <p className="mb-2 text-xs leading-relaxed text-muted">{t("ai.sessionHint")}</p>}
      {part.groups.map((g) => {
        const can = !locked && groupSelectable(part, g, windowDays, now);
        const label = sessions && g.label.startsWith("/") ? displayPath(g.label) : g.label;
        const notes = sessions
          ? [
              t("ai.group.sessions", { n: g.count }),
              g.last ? t("ai.group.last", { date: fmt(g.last) }) : "",
              g.recent > 0 ? t("ai.group.recent", { n: g.recent }) : "",
            ].filter(Boolean).join(" · ")
          : t("ai.group.release");
        return (
          <div key={g.key} className="flex items-center gap-3 py-1.5">
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
                <span className="truncate text-[13px] hover:text-primary">{label}</span>
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

function fmtDateLike(sec: number): string {
  const d = new Date(sec * 1000);
  return d.toLocaleDateString(undefined, { year: "numeric", month: "2-digit", day: "2-digit" });
}
const fmt = fmtDateLike;
