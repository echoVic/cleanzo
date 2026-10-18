import * as React from "react";
import { ArrowUpToLine, ChevronRight, FolderOpen, Link2, RefreshCw, Search } from "lucide-react";
import type { Skill, SkillCopy, SkillScanResult, SkillUpdate, SkillUpgradeResult } from "../../mygo";
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
  daysAgo, isFolder, linkCandidates, matchFilter, matchQuery, sortSkills, trashPlan, windowDays,
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
  const [updates, setUpdates] = React.useState<Record<string, SkillUpdate>>({});
  const [checking, setChecking] = React.useState(false);
  const [checkedAt, setCheckedAt] = React.useState(0);
  const [upgradeOpen, setUpgradeOpen] = React.useState(false);
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => { setNavBusy("skills", phase === "scanning" || busy || checking); }, [phase, busy, checking]);

  const scan = React.useCallback(async () => {
    setPhase("scanning");
    try {
      const res = await skillsApi.scan();
      setData(res);
      setPicked((prev) => new Set([...prev].filter((n) => res.skills.some((s) => s.name === n))));
      setPhase("scanned");
    } catch (e) {
      toast(t("skills.fail", { msg: errText(e) }), "err");
      setPhase(data ? "scanned" : "idle");
    }
  }, [t, data]);

  // Offline origins right after a scan: where each skill came from, no network.
  React.useEffect(() => {
    if (!data) return;
    let cancelled = false;
    skillsApi.origins().then((ups) => {
      if (cancelled) return;
      setUpdates(mergeUpdates(updates, ups));
    }).catch(() => {});
    return () => { cancelled = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  const checkUpdates = React.useCallback(async () => {
    setChecking(true);
    try {
      const ups = await skillsApi.checkUpdates();
      setUpdates(mergeUpdates({}, ups));
      const n = ups.filter((u) => u.status === "update").length;
      const m = ups.filter((u) => u.modified).length;
      const e = ups.filter((u) => u.status === "error").length;
      if (n > 0 || m > 0 || e > 0) {
        toast(t("skills.updates.found", { n, m, e }), n > 0 ? "ok" : "err");
      } else {
        toast(t("skills.updates.none"), "ok");
      }
      setCheckedAt(Date.now());
    } catch (e) {
      toast(t("skills.updates.failed", { msg: errText(e) }), "err");
    } finally {
      setChecking(false);
    }
  }, [t]);

  const skills = data?.skills ?? [];
  const now = data?.scannedAt ?? 0;
  const hasCheck = checkedAt > 0 || Object.values(updates).some((u) => u.checkedAt > 0);
  const updateFor = (s: Skill): SkillUpdate[] => {
    const reals = new Set(s.copies.map((c) => c.real).filter(Boolean));
    return Object.values(updates).filter((u) => reals.has(u.path));
  };
  const updateMatches = (s: Skill) => updateFor(s).some((u) => u.status === "update");
  const visible = sortSkills(skills.filter((s) =>
    (filter === "updates" ? updateMatches(s) : matchFilter(s, filter)) && matchQuery(s, query)
  ), sort);
  const counts: Record<SkillFilter, number> = {
    all: skills.length,
    idle: skills.filter((s) => s.idle).length,
    issues: skills.filter((s) => s.issues.length > 0).length,
    unobserved: skills.filter((s) => !s.observed).length,
    updates: skills.filter(updateMatches).length,
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

  const doLink = async (pairs: { path: string; target: string }[] | null) => {
    if (!pairs) return;
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

  const upgradeTargets = Object.values(updates).filter((u) => u.status === "update");
  const upgradeModified = upgradeTargets.filter((u) => u.modified);
  const upgradeCCSwitch = upgradeTargets.some((u) => u.origin.kind === "cc-switch");

  const doUpgrade = async (force: boolean) => {
    setUpgradeOpen(false);
    setBusy(true);
    try {
      const reqs = upgradeTargets.map((u) => ({ path: u.path, force: force && u.modified }));
      const res: SkillUpgradeResult[] = await skillsApi.upgrade(reqs);
      const failed = res.filter((r) => r.error);
      const firstFail = failed[0];
      toast(failed.length
        ? t("skills.upgrade.partial", { ok: res.length - failed.length, failed: failed.length, reason: firstFail ? failReason(t, firstFail.error) : "" })
        : t("skills.upgrade.done", { n: res.length }), failed.length ? "err" : "ok");
      await scan();
      await checkUpdates();
    } catch (e) {
      toast(errText(e), "err");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h2>{t("nav.skills")}</h2>
          <p className="muted">{t("skills.head")}</p>
        </div>
        <div className="head-actions">
          <Button variant="ghost" size="sm" onClick={checkUpdates} disabled={checking || busy || !data}>
            <ArrowUpToLine className={cn("h-3.5 w-3.5", checking && "animate-pulse")} />
            {checking ? t("skills.updates.checking") : t("skills.updates.check")}
          </Button>
          {data && (
            <Button variant="ghost" size="sm" onClick={scan} disabled={phase === "scanning" || busy || checking}>
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
            updatable={counts.updates} checking={checking} hasCheck={hasCheck}
            onLinkAll={() => setLinkPairs(linkable)}
            onUpgradeAll={() => setUpgradeOpen(true)} />

          <div className="mb-3 flex flex-wrap items-center gap-2">
            {(["all", "idle", "issues", "updates", "unobserved"] as SkillFilter[]).map((f) => (
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
                expanded={expanded.has(s.name)} busy={busy} ups={updateFor(s)} hasCheck={hasCheck}
                onToggle={(on) => toggle(s.name, on)} onExpand={() => toggleExpand(s.name)}
                onLink={(pairs) => setLinkPairs(pairs)}
                onUpgrade={(path, force) => upgradeOne(path, force, { setBusy, scan, checkUpdates, t })} />
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
            <Button onClick={() => void doLink(linkPairs)}>{t("skills.linkConfirm.ok")}</Button>
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
            <Button variant="destructive" onClick={() => void doTrash()}>{t("skills.confirm.ok")}</Button>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={upgradeOpen} onOpenChange={(v) => !v && setUpgradeOpen(false)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("skills.upgradeConfirm.title")}</DialogTitle>
            <DialogDescription>{t("skills.upgradeConfirm.body", { n: upgradeTargets.length })}</DialogDescription>
          </DialogHeader>
          <ul className="max-h-[30vh] space-y-1 overflow-auto text-xs">
            {upgradeTargets.map((u) => (
              <li key={u.path} className="flex items-center gap-2">
                <b>{u.name}</b>
                <span className="min-w-0 truncate text-muted">{u.origin.repo}{u.origin.path ? `/${u.origin.path}` : ""}</span>
                {u.modified && <span className="tag warn shrink-0">{t("skills.tag.modified")}</span>}
              </li>
            ))}
          </ul>
          {upgradeModified.length > 0 && (
            <p className="mt-3 text-xs text-warning-text">{t("skills.upgradeConfirm.modified", { n: upgradeModified.length })}</p>
          )}
          {upgradeCCSwitch && (
            <p className="mt-2 text-xs text-muted">{t("skills.upgradeConfirm.ccswitch")}</p>
          )}
          <p className="mt-3 text-xs text-muted">{t("skills.upgradeConfirm.safety")}</p>
          <div className="mt-4 flex justify-end gap-2">
            <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
            {upgradeModified.length > 0
              ? <Button onClick={() => void doUpgrade(true)}>{t("skills.upgradeConfirm.force", { n: upgradeModified.length })}</Button>
              : <Button onClick={() => void doUpgrade(false)}>{t("skills.upgradeConfirm.ok")}</Button>}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

/** Merge a new check into the previous one: entries a check did not cover keep their state. */
function mergeUpdates(prev: Record<string, SkillUpdate>, ups: SkillUpdate[]): Record<string, SkillUpdate> {
  const next = { ...prev };
  for (const u of ups) {
    next[u.path] = u;
  }
  return next;
}

async function upgradeOne(path: string, force: boolean, deps: {
  setBusy: (b: boolean) => void; scan: () => Promise<void>; checkUpdates: () => Promise<void>; t: T;
}) {
  const { setBusy, scan, checkUpdates, t } = deps;
  setBusy(true);
  try {
    const res = await skillsApi.upgrade([{ path, force }]);
    const r = res[0];
    if (r?.error) {
      toast(failReason(t, r.error), "err");
    } else {
      toast(t("skills.upgrade.done", { n: 1 }), "ok");
      await scan();
      await checkUpdates();
    }
  } catch (e) {
    toast(errText(e), "err");
  } finally {
    setBusy(false);
  }
}

function SummaryCard({ data, counts, linkable, busy, updatable, checking, hasCheck, onLinkAll, onUpgradeAll }: {
  data: SkillScanResult; counts: Record<SkillFilter, number>; linkable: number; busy: boolean;
  updatable: number; checking: boolean; hasCheck: boolean;
  onLinkAll: () => void; onUpgradeAll: () => void;
}) {
  const t = useT();
  const ok = data.sources.filter((s) => s.status === "ok");
  const missing = data.sources.filter((s) => s.status !== "ok");
  return (
    <Card className="mb-4 p-[18px] px-[22px]">
      <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
        <Stat label={t("skills.stat.total")} value={counts.all} />
        <Stat label={t("skills.stat.idle")} value={counts.idle} />
        <Stat label={t("skills.stat.issues")} value={counts.issues} />
        <Stat label={t("skills.stat.roots")} value={data.roots.length} />
        <div className="ml-auto flex gap-2">
          {updatable > 0 && (
            <Button className="" size="sm" disabled={busy || checking} onClick={onUpgradeAll}>
              <ArrowUpToLine className="h-3.5 w-3.5" />{t("skills.updateAll", { n: updatable })}
            </Button>
          )}
          {linkable > 0 && (
            <Button size="sm" variant="ghost" disabled={busy} onClick={onLinkAll}>
              <Link2 className="h-3.5 w-3.5" />{t("skills.linkAll", { n: linkable })}
            </Button>
          )}
        </div>
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
        {!hasCheck && <div>{t("skills.updates.hint")}</div>}
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

function SkillRow({ skill: s, now, picked, expanded, busy, ups, hasCheck, onToggle, onExpand, onLink, onUpgrade }: {
  skill: Skill; now: number; picked: boolean; expanded: boolean; busy: boolean;
  ups: SkillUpdate[]; hasCheck: boolean;
  onToggle: (on: boolean) => void; onExpand: () => void;
  onLink: (pairs: { path: string; target: string }[]) => void;
  onUpgrade: (path: string, force: boolean) => void;
}) {
  const t = useT();
  const removable = s.copies.some((c) => c.removable);
  const tools = [...new Set(s.copies.map((c) => c.tool))];
  const pairs = linkCandidates(s);
  const updatable = ups.some((u) => u.status === "update");
  const modified = ups.some((u) => u.modified);
  const upstreamGone = ups.some((u) => u.status === "missing");
  return (
    <div className="border-b border-border last:border-b-0">
      <div className="flex items-center gap-3 px-4 py-2.5">
        <Checkbox checked={picked} disabled={!removable} onCheckedChange={(v) => onToggle(v === true)}
          aria-label={s.name} />
        <button type="button" className="min-w-0 flex-1 cursor-pointer text-left" onClick={onExpand}>
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="text-[13px] font-semibold">{s.name}</span>
            {updatable && <span className="tag safe">{t("skills.tag.update")}</span>}
            {modified && <span className="tag warn">{t("skills.tag.modified")}</span>}
            {upstreamGone && <span className="tag warn">{t("skills.tag.missing")}</span>}
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
          {s.copies.map((c) => {
            const u = ups.find((x) => c.real && x.path === c.real);
            return <CopyLine key={c.path} copy={c} update={u} hasCheck={hasCheck} busy={busy}
              onUpgrade={(force) => onUpgrade(c.real, force)} />;
          })}
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

function CopyLine({ copy: c, update: u, hasCheck, busy, onUpgrade }: {
  copy: SkillCopy; update?: SkillUpdate; hasCheck: boolean; busy: boolean;
  onUpgrade: (force: boolean) => void;
}) {
  const t = useT();
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="w-[86px] shrink-0 text-muted">{toolName(c.tool)}</span>
      <span className={cn("tag", c.kind === "broken" || c.kind === "invalid" ? "warn" : "dim")}>
        {t(`skills.kind.${c.kind}` as DictKey)}
      </span>
      <span className="min-w-0 truncate" title={c.path}>
        {displayPath(c.path)}
        {c.kind === "link" || c.kind === "broken" ? <span className="text-muted"> → {displayPath(c.target)}</span> : null}
      </span>
      {u?.origin.repo && (
        <span className="shrink-0 text-[11px] text-muted" title={u.origin.remote}>
          {t("skills.origin", { repo: u.origin.repo })}
        </span>
      )}
      {isFolder(c) && c.linkedBy.length > 0 && (
        <span className="shrink-0 text-muted">{t("skills.linkedBy", { n: c.linkedBy.length })}</span>
      )}
      <span className="ml-auto flex shrink-0 items-center gap-2">
        {u && isFolder(c) && (
          <UpdateState update={u} hasCheck={hasCheck} busy={busy} onUpgrade={onUpgrade} />
        )}
        {c.kind !== "broken" && (
          <button type="button" className="cursor-pointer text-muted hover:text-foreground"
            onClick={() => skillsApi.reveal(c.real || c.path).catch(() => {})} aria-label={t("skills.reveal")}>
            <FolderOpen className="h-3.5 w-3.5" />
          </button>
        )}
      </span>
    </div>
  );
}

function UpdateState({ update: u, hasCheck, busy, onUpgrade }: {
  update: SkillUpdate; hasCheck: boolean; busy: boolean;
  onUpgrade: (force: boolean) => void;
}) {
  const t = useT();
  if (!hasCheck || u.status === "unknown") {
    return u.origin.repo ? <span className="text-[11px] text-muted">{t("skills.updates.unchecked")}</span> : null;
  }
  if (u.status === "current" && !u.modified) {
    return <span className="text-[11px] text-muted">{t("skills.updates.current")}</span>;
  }
  if (u.status === "missing") {
    return <span className="text-[11px] text-danger">{t("skills.tag.missing")}</span>;
  }
  if (u.status === "error") {
    return <span className="text-[11px] text-danger" title={failReason(t, u.error)}>{failReason(t, u.error)}</span>;
  }
  // update
  return (
    <Button size="sm" variant={u.modified ? "default" : "ghost"} disabled={busy}
      onClick={() => onUpgrade(u.modified)}>
      <ArrowUpToLine className="h-3.5 w-3.5" />
      {u.modified ? t("skills.upgrade.forceOne") : t("skills.upgrade.one")}
    </Button>
  );
}

/** 上次调用一栏：有记录显示「N 天前」，有数据但没调用显示「N 天内未调用」，没数据显示「无数据」。 */
function lastText(t: T, s: Skill, now: number): string {
  if (!s.observed) return t("skills.last.none");
  if (s.count > 0) {
    const d = daysAgo(s.last, now);
    return `${d === 0 ? t("skills.last.today") : t("skills.last.ago", { n: d })} · ${t("skills.last.count", { n: s.count })}`;
  }
  return s.idle ? t("skills.last.unused", { n: windowDays(s.since, now) })
    : t("skills.last.short", { n: windowDays(s.since, now) });
}

function failReason(t: T, err: string): string {
  const k = `skills.err.${err}` as DictKey;
  const s = t(k);
  return s === k ? err : s;
}
