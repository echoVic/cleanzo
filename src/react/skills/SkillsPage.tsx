import * as React from "react";
import { ChevronRight, FolderOpen, Link2, RefreshCw, Search } from "lucide-react";
import type { Skill, SkillCopy, SkillScanResult } from "../../mygo";
import { Button } from "../../ui/components/button";
import { Card } from "../../ui/components/card";
import { Checkbox } from "../../ui/components/checkbox";
import { IdleHero } from "../../ui/components/idle-hero";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogClose,
} from "../../ui/components/dialog";
import { cn } from "../../ui/cn";
import { useT, type DictKey } from "../../ui/i18n-react";
import { errText, setNavBusy, toast } from "../../shared";
import { human, displayPath } from "../ai/format";
import {
  daysAgo, isFolder, linkCandidates, matchFilter, matchQuery, okSources, sortSkills, trashPlan, windowDays,
  type SkillFilter, type SkillSort,
} from "./helpers";
import { skillsApi } from "./api";

type Phase = "idle" | "scanning" | "scanned";
type T = ReturnType<typeof useT>;

/** 工具 id → 展示名；~/.agents 是共享目录。 */
const TOOL_NAMES: Record<string, string> = {
  agents: "~/.agents", claude: "Claude Code", codex: "Codex", gemini: "Gemini CLI",
  opencode: "OpenCode", grok: "Grok", hermes: "Hermes", pi: "Pi",
};
const toolName = (id: string) => TOOL_NAMES[id] ?? id;

export function SkillsPage() {
  const t = useT();
  const [phase, setPhase] = React.useState<Phase>("idle");
  const [data, setData] = React.useState<SkillScanResult | null>(null);
  const [filter, setFilter] = React.useState<SkillFilter>("all");
  const [sort, setSort] = React.useState<SkillSort>("last");
  const [query, setQuery] = React.useState("");
  const [picked, setPicked] = React.useState<Set<string>>(new Set());
  const [expanded, setExpanded] = React.useState<Set<string>>(new Set());
  const [confirmOpen, setConfirmOpen] = React.useState(false);
  const [linkPairs, setLinkPairs] = React.useState<{ path: string; target: string }[] | null>(null);
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => { setNavBusy("skills", phase === "scanning" || busy); }, [phase, busy]);

  const scan = React.useCallback(async () => {
    setPhase("scanning");
    try {
      const res = await skillsApi.scan();
      setData(res);
      // 按名字保留仍存在的勾选；默认什么都不勾。
      setPicked((prev) => new Set([...prev].filter((n) => res.skills.some((s) => s.name === n))));
      setPhase("scanned");
    } catch (e) {
      toast(t("skills.fail", { msg: errText(e) }), "err");
      setPhase(data ? "scanned" : "idle");
    }
  }, [t, data]);

  const skills = data?.skills ?? [];
  const now = data?.scannedAt ?? 0;
  const visible = sortSkills(skills.filter((s) => matchFilter(s, filter) && matchQuery(s, query)), sort);
  const counts: Record<SkillFilter, number> = {
    all: skills.length,
    idle: skills.filter((s) => s.idle).length,
    issues: skills.filter((s) => s.issues.length > 0).length,
    unobserved: skills.filter((s) => !s.observed).length,
  };
  const plan = trashPlan(skills, picked);
  const linkable = skills.flatMap((s) => linkCandidates(s));

  const toggle = (name: string, on: boolean) => setPicked((prev) => {
    const next = new Set(prev);
    if (on) next.add(name); else next.delete(name);
    return next;
  });
  const toggleExpand = (name: string) => setExpanded((prev) => {
    const next = new Set(prev);
    if (next.has(name)) next.delete(name); else next.add(name);
    return next;
  });
  const pickVisible = (on: boolean) => setPicked((prev) => {
    const next = new Set(prev);
    for (const s of visible) {
      if (!s.copies.some((c) => c.removable)) continue;
      if (on) next.add(s.name); else next.delete(s.name);
    }
    return next;
  });
  const visiblePicked = visible.filter((s) => picked.has(s.name)).length;

  const doTrash = async () => {
    setConfirmOpen(false);
    setBusy(true);
    try {
      const res = await skillsApi.trash(plan.paths);
      const failed = res.items.filter((i) => i.error);
      toast(failed.length
        ? t("skills.trash.partial", { n: res.moved, failed: failed.length, reason: failReason(t, failed[0]?.error ?? "") })
        : t("skills.trash.done", { n: res.moved, size: human(res.freed) }), failed.length ? "err" : "ok");
      setPicked(new Set());
      await scan();
    } catch (e) {
      toast(errText(e), "err");
    } finally {
      setBusy(false);
    }
  };

  const doLink = async (pairs: { path: string; target: string }[]) => {
    setLinkPairs(null);
    setBusy(true);
    let ok = 0;
    let firstErr = "";
    for (const p of pairs) {
      try { await skillsApi.link(p.path, p.target); ok++; } catch (e) { firstErr ||= errText(e); }
    }
    toast(firstErr
      ? t("skills.link.partial", { n: ok, failed: pairs.length - ok, reason: failReason(t, firstErr) })
      : t("skills.link.done", { n: ok }), firstErr ? "err" : "ok");
    setBusy(false);
    await scan();
  };

  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h2>{t("nav.skills")}</h2>
          <p className="muted">{t("skills.head")}</p>
        </div>
        <div className="head-actions">
          {data && (
            <Button variant="ghost" size="sm" onClick={scan} disabled={phase === "scanning" || busy}>
              <RefreshCw className={cn("h-3.5 w-3.5", phase === "scanning" && "animate-spin")} />
              {phase === "scanning" ? t("scan.busy") : t("fs.rescan")}
            </Button>
          )}
        </div>
      </header>

      {phase === "idle" && !data && (
        <IdleHero
          label={t("skills.idle.label")}
          value="--"
          sub={t("clean.idleSub")}
          guide={t("skills.idle.guide")}
          ctaText={t("ai.idle.cta")}
          onCta={scan}
          safetyText={t("skills.idle.safety")}
          chips={[t("skills.chip.usage"), t("skills.chip.dupes"), t("skills.chip.broken")]}
        />
      )}
      {phase === "scanning" && !data && (
        <div className="flex flex-col items-center py-20 text-center">
          <RefreshCw className="h-9 w-9 animate-spin text-primary" />
          <div className="mt-4 text-sm text-muted">{t("skills.scanning")}</div>
        </div>
      )}

      {data && (
        <>
          <SummaryCard data={data} counts={counts} linkable={linkable.length} busy={busy}
            onLinkAll={() => setLinkPairs(linkable)} />

          <div className="mb-3 flex flex-wrap items-center gap-2">
            {(["all", "idle", "issues", "unobserved"] as SkillFilter[]).map((f) => (
              <Button key={f} size="sm" variant={filter === f ? "default" : "ghost"} onClick={() => setFilter(f)}>
                {t(`skills.filter.${f}` as DictKey)} {counts[f]}
              </Button>
            ))}
            <div className="search-wrap ml-auto" style={{ maxWidth: 260 }}>
              <Search className="h-4 w-4" />
              <input className="search" value={query} placeholder={t("skills.search")}
                onChange={(e) => setQuery(e.target.value)} />
            </div>
            <select className="select" value={sort} onChange={(e) => setSort(e.target.value as SkillSort)}
              aria-label={t("skills.sort")}>
              {(["last", "count", "size", "name"] as SkillSort[]).map((s) => (
                <option key={s} value={s}>{t(`skills.sort.${s}` as DictKey)}</option>
              ))}
            </select>
          </div>

          <Card className="overflow-hidden">
            <div className="flex items-center gap-3 border-b border-border px-4 py-2.5 text-xs text-muted">
              <Checkbox
                checked={visiblePicked === 0 ? false : visiblePicked === visible.length ? true : "indeterminate"}
                onCheckedChange={(v) => pickVisible(v === true)}
                aria-label={t("skills.pickAll")}
                disabled={visible.length === 0}
              />
              <span className="flex-1">{t("skills.col.name")}</span>
              <span className="w-[170px] text-right">{t("skills.col.last")}</span>
              <span className="w-[70px] text-right">{t("skills.col.size")}</span>
              <span className="w-[24px]" />
            </div>
            {visible.length === 0 && (
              <div className="px-4 py-10 text-center text-sm text-muted">{t("skills.empty")}</div>
            )}
            {visible.map((s) => (
              <SkillRow key={s.name} skill={s} now={now} picked={picked.has(s.name)}
                expanded={expanded.has(s.name)} busy={busy}
                onToggle={(on) => toggle(s.name, on)} onExpand={() => toggleExpand(s.name)}
                onLink={(pairs) => setLinkPairs(pairs)} />
            ))}
          </Card>

          <div className="sticky bottom-0 mt-4 flex items-center justify-between gap-4 rounded-[12px] border border-border bg-card px-4 py-3 shadow-card">
            <span className="text-xs text-muted">{t("skills.foot")}</span>
            <Button variant="destructive" disabled={plan.paths.length === 0 || busy} onClick={() => setConfirmOpen(true)}>
              {t("skills.trash", { n: picked.size, size: human(plan.size) })}
            </Button>
          </div>
        </>
      )}

      <Dialog open={linkPairs !== null} onOpenChange={(v) => !v && setLinkPairs(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("skills.linkConfirm.title")}</DialogTitle>
            <DialogDescription>{t("skills.linkConfirm.body", { n: linkPairs?.length ?? 0 })}</DialogDescription>
          </DialogHeader>
          <ul className="max-h-[36vh] space-y-1 overflow-auto text-xs">
            {(linkPairs ?? []).map((p) => (
              <li key={p.path}>{displayPath(p.path)} <span className="text-muted">→ {displayPath(p.target)}</span></li>
            ))}
          </ul>
          <p className="mt-3 text-xs text-muted">{t("skills.linkConfirm.safety")}</p>
          <div className="mt-4 flex justify-end gap-2">
            <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
            <Button onClick={() => linkPairs && doLink(linkPairs)}>{t("skills.linkConfirm.ok")}</Button>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={confirmOpen} onOpenChange={(v) => !v && setConfirmOpen(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("skills.confirm.title")}</DialogTitle>
            <DialogDescription>{t("skills.confirm.body", { n: picked.size, size: human(plan.size) })}</DialogDescription>
          </DialogHeader>
          <ul className="max-h-[36vh] space-y-1 overflow-auto text-sm">
            {skills.filter((s) => picked.has(s.name)).map((s) => (
              <li key={s.name}>
                <b>{s.name}</b>{" "}
                <span className="text-xs text-muted">
                  {s.copies.filter((c) => c.removable).map((c) => toolName(c.tool)).join(" · ")}
                  {s.count > 0 && ` · ${t("skills.confirm.used", { n: s.count })}`}
                </span>
              </li>
            ))}
          </ul>
          {plan.links.length > 0 && (
            <div className="mt-3 rounded-[10px] px-3 py-2 text-xs leading-relaxed text-warning-text"
              style={{ background: "var(--warning-soft)" }}>
              {t("skills.confirm.links", { n: plan.links.length })}
              <ul className="mt-1">{plan.links.map((l) => <li key={l}>{displayPath(l)}</li>)}</ul>
            </div>
          )}
          <p className="mt-3 text-xs text-muted">{t("skills.confirm.safety")}</p>
          <div className="mt-4 flex justify-end gap-2">
            <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
            <Button variant="destructive" onClick={doTrash}>{t("skills.confirm.ok")}</Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function SummaryCard({ data, counts, linkable, busy, onLinkAll }: {
  data: SkillScanResult; counts: Record<SkillFilter, number>; linkable: number; busy: boolean; onLinkAll: () => void;
}) {
  const t = useT();
  const ok = okSources(data.sources);
  const missing = data.sources.filter((s) => s.status !== "ok");
  return (
    <Card className="mb-4 p-[18px] px-[22px]">
      <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
        <Stat label={t("skills.stat.total")} value={counts.all} />
        <Stat label={t("skills.stat.idle")} value={counts.idle} />
        <Stat label={t("skills.stat.issues")} value={counts.issues} />
        <Stat label={t("skills.stat.roots")} value={data.roots.length} />
        {linkable > 0 && (
          <Button className="ml-auto" size="sm" variant="ghost" disabled={busy} onClick={onLinkAll}>
            <Link2 className="h-3.5 w-3.5" />{t("skills.linkAll", { n: linkable })}
          </Button>
        )}
      </div>
      <div className="mt-3 space-y-0.5 text-xs text-muted">
        {ok.length > 0 && (
          <div>{t("skills.sources.ok", {
            list: ok.map((s) => t("skills.sources.item", { name: s.name, days: windowDays(s.since, data.scannedAt), n: s.records })).join("，"),
          })}</div>
        )}
        {missing.length > 0 && (
          <div>{t("skills.sources.none", { list: missing.map((s) => s.name).join("、") })}</div>
        )}
      </div>
    </Card>
  );
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <div className="flex items-baseline gap-2">
      <span className="text-[13px] text-muted">{label}</span>
      <span className="text-[22px] font-semibold tabular-nums">{value}</span>
    </div>
  );
}

function SkillRow({ skill: s, now, picked, expanded, busy, onToggle, onExpand, onLink }: {
  skill: Skill; now: number; picked: boolean; expanded: boolean; busy: boolean;
  onToggle: (on: boolean) => void; onExpand: () => void; onLink: (pairs: { path: string; target: string }[]) => void;
}) {
  const t = useT();
  const removable = s.copies.some((c) => c.removable);
  const tools = [...new Set(s.copies.map((c) => c.tool))];
  const pairs = linkCandidates(s);
  return (
    <div className="border-b border-border last:border-b-0">
      <div className="flex items-center gap-3 px-4 py-2.5">
        <Checkbox checked={picked} disabled={!removable} onCheckedChange={(v) => onToggle(v === true)}
          aria-label={s.name} />
        <button type="button" className="min-w-0 flex-1 cursor-pointer text-left" onClick={onExpand}>
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-[13px] font-semibold">{s.name}</span>
            {s.idle && <span className="tag warn">{t("skills.tag.idle")}</span>}
            {s.issues.map((i) => (
              <span key={i} className={cn("tag", i === "duplicate" ? "dim" : "warn")}>{t(`skills.issue.${i}` as DictKey)}</span>
            ))}
            <span className="text-[11px] text-muted">{tools.map(toolName).join(" · ")}</span>
          </div>
          {s.description && <div className="mt-0.5 truncate text-xs text-muted">{s.description}</div>}
        </button>
        <span className="w-[170px] whitespace-nowrap text-right text-xs tabular-nums">{lastText(t, s, now)}</span>
        <span className="w-[70px] text-right text-xs tabular-nums text-muted">{human(s.size)}</span>
        <button type="button" className="w-[24px] cursor-pointer text-muted" onClick={onExpand}
          aria-label={t("skills.details")}>
          <ChevronRight className={cn("h-4 w-4 transition-transform", expanded && "rotate-90")} />
        </button>
      </div>
      {expanded && (
        <div className="space-y-1.5 bg-muted-background/50 px-4 py-3 pl-[46px] text-xs">
          {s.uses.length > 0 && (
            <div className="text-muted">
              {s.uses.map((u) => t("skills.use", { tool: toolName(u.tool), n: u.count, days: daysAgo(u.last, now) })).join("；")}
            </div>
          )}
          {s.copies.map((c) => <CopyLine key={c.path} copy={c} />)}
          {pairs.length > 0 && (
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => onLink(pairs)}>
              <Link2 className="h-3.5 w-3.5" />{t("skills.link", { n: pairs.length })}
            </Button>
          )}
          {s.issues.includes("diverged") && <div className="text-muted">{t("skills.diverged.hint")}</div>}
        </div>
      )}
    </div>
  );
}

function CopyLine({ copy: c }: { copy: SkillCopy }) {
  const t = useT();
  return (
    <div className="flex items-center gap-2">
      <span className="w-[86px] shrink-0 text-muted">{toolName(c.tool)}</span>
      <span className={cn("tag", c.kind === "broken" || c.kind === "invalid" ? "warn" : "dim")}>
        {t(`skills.kind.${c.kind}` as DictKey)}
      </span>
      <span className="min-w-0 truncate" title={c.path}>
        {displayPath(c.path)}
        {c.kind === "link" || c.kind === "broken" ? <span className="text-muted"> → {displayPath(c.target)}</span> : null}
      </span>
      {isFolder(c) && c.linkedBy.length > 0 && (
        <span className="shrink-0 text-muted">{t("skills.linkedBy", { n: c.linkedBy.length })}</span>
      )}
      {c.kind !== "broken" && (
        <button type="button" className="ml-auto shrink-0 cursor-pointer text-muted hover:text-foreground"
          onClick={() => skillsApi.reveal(c.real || c.path).catch(() => {})} aria-label={t("skills.reveal")}>
          <FolderOpen className="h-3.5 w-3.5" />
        </button>
      )}
    </div>
  );
}

/** 上次调用一栏：有记录显示「N 天前」，有数据但没调用显示「N 天内未调用」，没数据显示「无数据」。 */
function lastText(t: T, s: Skill, now: number): string {
  if (!s.observed) return t("skills.last.none");
  if (s.count > 0) {
    const d = daysAgo(s.last, now);
    return `${d === 0 ? t("skills.last.today") : t("skills.last.ago", { n: d })} · ${t("skills.last.count", { n: s.count })}`;
  }
  // Only the backend's idle verdict says "not called in N days"; everything
  // else (installed recently, or the window still too short) stays provisional.
  return s.idle ? t("skills.last.unused", { n: windowDays(s.since, now) })
    : t("skills.last.short", { n: windowDays(s.since, now) });
}

function failReason(t: T, err: string): string {
  const k = `skills.err.${err}` as DictKey;
  const s = t(k);
  return s === k ? err : s;
}
