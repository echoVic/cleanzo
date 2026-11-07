import * as React from "react";
import { RefreshCw } from "lucide-react";
import type { AIModelSource, AIPart, AITool } from "../../mygo";
import { Badge } from "../../ui/components/badge";
import { Button } from "../../ui/components/button";
import { Card } from "../../ui/components/card";
import { Checkbox } from "../../ui/components/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "../../ui/components/dialog";
import { useT } from "../../ui/i18n-react";
import { human, toast } from "../../shared";
import { aiSpaceApi } from "./api";

type Phase = "idle" | "scanning" | "scanned";
type Filter = "all" | "models" | "sessions" | "caches";

/** 批量选择用的窗口：模型与会话共用「90 天」，用户只需要记一个数字。 */
const OLD = 90 * 24 * 3600;

interface PartRow {
  tool: AITool;
  part: AIPart;
}

/** 相对时间：今天 / 昨天 / N 天前 / N 个月前；0 表示不知道。 */
function relTime(ts: number, now: number): string {
  if (!ts) return "—";
  const d = Math.floor((now - ts) / 86400);
  if (d <= 0) return "今天";
  if (d === 1) return "昨天";
  if (d < 31) return `${d} 天前`;
  if (d < 365) return `${Math.max(1, Math.round(d / 30))} 个月前`;
  return `${Math.round(d / 365)} 年前`;
}

const levelBadge = {
  safe: { key: "aispace.level.safe", variant: "safe" },
  optional: { key: "aispace.level.optional", variant: "optional" },
  sensitive: { key: "aispace.level.sensitive", variant: "warn" },
} as const;

export function AISpacePage() {
  const t = useT();
  const [phase, setPhase] = React.useState<Phase>("idle");
  const [error, setError] = React.useState<string | null>(null);
  const [tools, setTools] = React.useState<AITool[]>([]);
  const [sources, setSources] = React.useState<AIModelSource[]>([]);
  const [scannedAt, setScannedAt] = React.useState(0);
  const [pickedModels, setPickedModels] = React.useState<Map<string, Set<string>>>(new Map());
  const [pickedParts, setPickedParts] = React.useState<Set<string>>(new Set());
  const [pickedGroups, setPickedGroups] = React.useState<Map<string, Set<string>>>(new Map());
  const [expanded, setExpanded] = React.useState<Set<string>>(new Set());
  const [filter, setFilter] = React.useState<Filter>("all");
  const [busy, setBusy] = React.useState(false);
  const [confirmOpen, setConfirmOpen] = React.useState(false);
  const [done, setDone] = React.useState<{ freed: number; kept: number; skipped: number } | null>(null);

  const now = scannedAt || Math.floor(Date.now() / 1000);

  const sessionRows = React.useMemo<PartRow[]>(() => {
    const out: PartRow[] = [];
    for (const tool of tools) for (const part of tool.parts) if (part.level === "sensitive") out.push({ tool, part });
    return out;
  }, [tools]);
  /** 会话按工具合并成一行：同一个工具散在几个 part 里也只占一行，展开看项目。 */
  const sessionByTool = React.useMemo(() => {
    const m = new Map<string, { tool: AITool; parts: PartRow[] }>();
    for (const r of sessionRows) {
      const cur = m.get(r.tool.id) ?? { tool: r.tool, parts: [] };
      cur.parts.push(r);
      m.set(r.tool.id, cur);
    }
    return [...m.values()];
  }, [sessionRows]);
  const cacheRows = React.useMemo<PartRow[]>(() => {
    const out: PartRow[] = [];
    for (const tool of tools)
      for (const part of tool.parts)
        if (part.level === "safe" || part.level === "optional" || part.level === "unknown") out.push({ tool, part });
    return out;
  }, [tools]);
  /** 缓存按「日志与缓存 / 旧版本」两个桶合并：总览页只做决定，细节留给展开和逐工具页。 */
  const cacheBuckets = React.useMemo(() => {
    const mk = () => ({ size: 0, byTool: new Map<string, { toolName: string; size: number; parts: PartRow[] }>() });
    const logs = mk();
    const releases = mk();
    for (const r of cacheRows) {
      const bucket = r.part.keep !== "" || /release/i.test(r.part.id) ? releases : logs;
      bucket.size += r.part.size;
      const cur = bucket.byTool.get(r.tool.id) ?? { toolName: r.tool.name, size: 0, parts: [] };
      cur.size += r.part.size;
      cur.parts.push(r);
      bucket.byTool.set(r.tool.id, cur);
    }
    return { logs, releases };
  }, [cacheRows]);
  const bucketSelected = (parts: PartRow[]) => {
    const pickable = parts.filter((r) => (r.part.groups.length > 0 ? pickedGroups.get(partKey(r))?.size : pickedParts.has(partKey(r))));
    if (pickable.length === 0) return false;
    return pickable.length >= parts.length ? true : "indeterminate";
  };
  const keptSize = React.useMemo(
    () => tools.reduce((n, tool) => n + tool.parts.filter((p) => p.level === "protected").reduce((m, p) => m + p.size, 0), 0),
    [tools],
  );
  const modelSources = React.useMemo(() => sources.filter((s) => s.installed && s.models.length > 0), [sources]);
  const modelTotal = modelSources.reduce((n, s) => n + s.size, 0);
  const sessionTotal = sessionRows.reduce((n, r) => n + r.part.size, 0);
  const cacheTotal = cacheRows.reduce((n, r) => n + r.part.size, 0);
  const total = modelTotal + sessionTotal + cacheTotal;

  const partKey = (r: PartRow) => `${r.tool.id}:${r.part.id}`;
  const groupPicked = (r: PartRow, key: string) => pickedGroups.get(partKey(r))?.has(key) ?? false;
  const partPicked = (r: PartRow) => (r.part.groups.length > 0 ? (pickedGroups.get(partKey(r))?.size ?? 0) > 0 : pickedParts.has(partKey(r)));
  const modelPicked = (sourceId: string, path: string) => pickedModels.get(sourceId)?.has(path) ?? false;
  const sourcePicked = (s: AIModelSource) => {
    const set = pickedModels.get(s.id);
    if (!set || set.size === 0) return false;
    return set.size >= s.models.length ? true : "indeterminate";
  };

  const chosenModelBytes = modelSources.reduce(
    (n, s) => n + s.models.filter((m) => modelPicked(s.id, m.path)).reduce((m, x) => m + x.size, 0),
    0,
  );
  const chosenPartBytes = React.useMemo(() => {
    let n = 0;
    for (const r of [...sessionRows, ...cacheRows]) {
      if (r.part.groups.length > 0) {
        const keys = pickedGroups.get(partKey(r));
        if (!keys || keys.size === 0) continue;
        for (const g of r.part.groups) if (keys.has(g.key) && g.recent === 0) n += g.size;
      } else if (pickedParts.has(partKey(r))) n += r.part.size;
    }
    return n;
  }, [sessionRows, cacheRows, pickedParts, pickedGroups]);
  const chosenTotal = chosenModelBytes + chosenPartBytes;

  const scan = React.useCallback(async () => {
    setPhase("scanning");
    setError(null);
    setDone(null);
    try {
      const [mr, tr] = await Promise.all([aiSpaceApi.scanModels(), aiSpaceApi.scanTools()]);
      setSources(mr.sources ?? []);
      setTools(tr.tools ?? []);
      setScannedAt(tr.scannedAt || Math.floor(Date.now() / 1000));
      // 默认只勾「可安全清理」：缓存与旧版本。模型和会话一律不预选。
      const np = new Set<string>();
      const ng = new Map<string, Set<string>>();
      for (const tool of tr.tools ?? []) {
        for (const part of tool.parts) {
          if (part.level !== "safe" || part.running) continue;
          if (part.groups.length > 0) {
            const keys = part.groups.filter((g) => g.recent === 0).map((g) => g.key);
            if (keys.length > 0) ng.set(`${tool.id}:${part.id}`, new Set(keys));
          } else if (part.size > 0) {
            np.add(`${tool.id}:${part.id}`);
          }
        }
      }
      setPickedParts(np);
      setPickedGroups(ng);
      setPickedModels(new Map());
      setExpanded(new Set());
      setPhase("scanned");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setPhase("idle");
    }
  }, []);

  const toggleSource = (s: AIModelSource, on: boolean) => {
    setPickedModels((m) => {
      const next = new Map(m);
      next.set(s.id, on ? new Set(s.models.map((x) => x.path)) : new Set());
      return next;
    });
  };
  const toggleModel = (s: AIModelSource, path: string, on: boolean) => {
    setPickedModels((m) => {
      const next = new Map(m);
      const set = new Set(next.get(s.id) ?? []);
      if (on) set.add(path);
      else set.delete(path);
      next.set(s.id, set);
      return next;
    });
  };
  const togglePart = (r: PartRow, on: boolean) => {
    if (r.part.groups.length > 0) {
      setPickedGroups((m) => {
        const next = new Map(m);
        next.set(partKey(r), on ? new Set(r.part.groups.filter((g) => g.recent === 0).map((g) => g.key)) : new Set());
        return next;
      });
      return;
    }
    setPickedParts((p) => {
      const next = new Set(p);
      if (on) next.add(partKey(r));
      else next.delete(partKey(r));
      return next;
    });
  };
  const toggleGroup = (r: PartRow, key: string, on: boolean) => {
    setPickedGroups((m) => {
      const next = new Map(m);
      const set = new Set(next.get(partKey(r)) ?? []);
      if (on) set.add(key);
      else set.delete(key);
      next.set(partKey(r), set);
      return next;
    });
  };
  const toggleExpand = (key: string) =>
    setExpanded((s) => {
      const next = new Set(s);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  /** 一次性批量：只勾「超过 90 天没动过」的，正在用的永远带不上。 */
  const pickOldModels = () => {
    if (busy) return;
    setPickedModels((m) => {
      const next = new Map(m);
      for (const s of modelSources) {
        const set = new Set(next.get(s.id) ?? []);
        for (const model of s.models) if (model.last > 0 && model.last < now - OLD) set.add(model.path);
        next.set(s.id, set);
      }
      return next;
    });
  };
  const pickOldSessions = () => {
    if (busy) return;
    setPickedGroups((m) => {
      const next = new Map(m);
      for (const r of sessionRows) {
        if (r.part.groups.length === 0) continue;
        const set = new Set(next.get(partKey(r)) ?? []);
        for (const g of r.part.groups) if (g.recent === 0 && g.last < now - OLD) set.add(g.key);
        next.set(partKey(r), set);
      }
      return next;
    });
  };

  const clean = async () => {
    setConfirmOpen(false);
    setBusy(true);
    try {
      const models = modelSources
        .map((s) => ({ source: s.id, models: s.models.filter((m) => modelPicked(s.id, m.path)).map((m) => m.path) }))
        .filter((x) => x.models.length > 0);
      const parts: { tool: string; part: string; groups: string[] }[] = [];
      for (const r of [...sessionRows, ...cacheRows]) {
        if (r.part.groups.length > 0) {
          const keys = [...(pickedGroups.get(partKey(r)) ?? [])];
          if (keys.length > 0) parts.push({ tool: r.tool.id, part: r.part.id, groups: keys });
        } else if (pickedParts.has(partKey(r))) {
          parts.push({ tool: r.tool.id, part: r.part.id, groups: [] });
        }
      }
      let freed = 0;
      if (models.length > 0) {
        const res = await aiSpaceApi.cleanModels(models);
        freed += res.freed ?? 0;
      }
      if (parts.length > 0) {
        const res = await aiSpaceApi.cleanParts(parts);
        freed += res.freed ?? 0;
      }
      setDone({ freed, kept: 0, skipped: 0 });
      await scan();
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    } finally {
      setBusy(false);
    }
  };

  // ---------------------------------------------------------------- 空态 / 扫描

  if (phase === "idle") {
    return (
      <div className="cz-react">
        <header className="view-head">
          <div>
            <h1>{t("nav.aispace")}</h1>
            <p className="muted">{t("aispace.sub")}</p>
          </div>
        </header>
        {error ? (
          <div className="empty err" role="alert">
            <div className="err-icon">⚠</div>
            <div>{t("aispace.fail", { msg: error })}</div>
            <button className="btn ghost retry-btn" type="button" onClick={() => void scan()}>
              {t("btn.retry")}
            </button>
          </div>
        ) : (
          <Card className="flex flex-col items-center px-6 py-16 text-center">
            <div className="text-[15px] font-semibold">{t("aispace.idle.label")}</div>
            <p className="mt-2 max-w-[520px] text-[13px] text-muted">{t("aispace.idle.guide")}</p>
            <Button className="mt-5" onClick={() => void scan()}>{t("ai.idle.cta")}</Button>
            <p className="mt-4 text-xs text-muted">{t("aispace.idle.safety")}</p>
          </Card>
        )}
      </div>
    );
  }

  if (phase === "scanning") {
    return (
      <div className="cz-react">
        <header className="view-head">
          <div>
            <h1>{t("nav.aispace")}</h1>
            <p className="muted">{t("aispace.sub")}</p>
          </div>
        </header>
        <div className="flex flex-col items-center py-20 text-center">
          <RefreshCw className="h-9 w-9 animate-spin text-primary" />
          <div className="mt-4 text-sm text-muted">{t("aispace.scanning")}</div>
        </div>
      </div>
    );
  }

  // ---------------------------------------------------------------- 结果

  const rows: { kind: Filter; node: React.ReactNode }[] = [];
  const showModels = filter === "all" || filter === "models";
  const showSessions = filter === "all" || filter === "sessions";
  const showCaches = filter === "all" || filter === "caches";

  if (showModels && modelSources.length > 0) {
    rows.push({
      kind: "models",
      node: (
        <GroupHeader
          key="gh-models"
          title={t("aispace.group.models")}
          size={modelTotal}
          note={t("aispace.group.modelsNote")}
          action={(() => {
            const any = modelSources.some((s) => s.models.some((m) => m.last > 0 && m.last < now - OLD));
            return any ? <Button size="sm" variant="ghost" onClick={pickOldModels}>{t("aispace.bulk.oldModels")}</Button> : null;
          })()}
        />
      ),
    });
    for (const s of modelSources) {
      const key = `src:${s.id}`;
      rows.push({
        kind: "models",
        node: (
          <div key={key} className="border-b border-border last:border-b-0">
            <RowLine
              checked={sourcePicked(s)}
              onToggle={(on) => toggleSource(s, on)}
              name={s.name}
              badge={<Badge variant="optional">{t("aispace.level.optional")}</Badge>}
              path={s.path}
              sub={t("aispace.modelsOf", { n: s.models.length })}
              last={relTime(s.last, now)}
              size={human(s.size)}
              expanded={expanded.has(key)}
              onExpand={() => toggleExpand(key)}
            />
            {expanded.has(key) && (
              <div className="border-t border-dashed border-border px-4 pb-2 pt-1 pl-[70px]">
                {s.models.map((m) => (
                  <div key={m.path} className="flex items-center gap-2 py-1 text-[12.5px]">
                    <Checkbox checked={modelPicked(s.id, m.path)} onCheckedChange={(v) => toggleModel(s, m.path, v === true)} aria-label={m.name} />
                    <span className="min-w-0 flex-1 truncate font-mono text-[12px]">{m.name}</span>
                    <span className="w-[110px] text-right text-xs text-muted tabular-nums">{relTime(m.last, now)}</span>
                    <span className="w-[70px] text-right text-xs tabular-nums">{human(m.size)}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        ),
      });
    }
  }

  if (showSessions && sessionRows.length > 0) {
    rows.push({
      kind: "sessions",
      node: (
        <GroupHeader
          key="gh-sessions"
          title={t("aispace.group.sessions")}
          size={sessionTotal}
          note={t("aispace.group.sessionsNote")}
          action={<Button size="sm" variant="ghost" onClick={pickOldSessions}>{t("aispace.bulk.oldSessions")}</Button>}
        />
      ),
    });
    for (const group of sessionByTool) {
      const key = `tool:${group.tool.id}`;
      const parts = group.parts;
      const groups = parts.flatMap((r) => r.part.groups.map((g) => ({ r, g })));
      const total = parts.reduce((n, r) => n + r.part.size, 0);
      rows.push({
        kind: "sessions",
        node: (
          <div key={key} className="border-b border-border last:border-b-0">
            <RowLine
              checked={bucketSelected(parts)}
              onToggle={(on) => parts.forEach((r) => togglePart(r, on))}
              name={group.tool.name}
              badge={<Badge variant="warn">{t("aispace.level.sensitive")}</Badge>}
              path={parts[0]?.part.path ?? ""}
              sub={t("aispace.groupsOf", { n: groups.length, m: groups.reduce((x, y) => x + y.g.count, 0) })}
              last={relTime(Math.max(...parts.map((r) => r.part.last)), now)}
              size={human(total)}
              expanded={expanded.has(key)}
              onExpand={() => toggleExpand(key)}
            />
            {expanded.has(key) && (
              <div className="border-t border-dashed border-border px-4 pb-2 pt-1 pl-[70px]">
                {groups.map(({ r, g }) => {
                  const locked = g.recent > 0;
                  return (
                    <div key={`${r.part.id}:${g.key}`} className="flex items-center gap-2 py-1 text-[12.5px]">
                      <Checkbox checked={locked ? false : groupPicked(r, g.key)} disabled={locked}
                        onCheckedChange={(v) => toggleGroup(r, g.key, v === true)} aria-label={g.label} />
                      <span className={locked ? "min-w-0 flex-1 truncate text-muted" : "min-w-0 flex-1 truncate"}>{g.label}</span>
                      {locked && <Badge variant="protected">{t("aispace.inUse")}</Badge>}
                      <span className="w-[110px] text-right text-xs text-muted tabular-nums">{relTime(g.last, now)}</span>
                      <span className="w-[70px] text-right text-xs tabular-nums">{human(g.size)}</span>
                    </div>
                  );
                })}
              </div>
            )}
          </div>
        ),
      });
    }
  }

  if (showCaches && cacheRows.length > 0) {
    const buckets = [
      { key: "logs", title: t("aispace.cache.logs"), note: t("aispace.group.cachesNote"), data: cacheBuckets.logs },
      { key: "releases", title: t("aispace.cache.releases"), note: t("aispace.cache.releasesNote"), data: cacheBuckets.releases },
    ].filter((b) => b.data.size > 0);
    buckets.forEach((b, i) => {
      const parts = [...b.data.byTool.values()].flatMap((x) => x.parts);
      const key = `bucket:${b.key}`;
      const tools = [...b.data.byTool.values()];
      rows.push({
        kind: "caches",
        node: (
          <React.Fragment key={key}>
            {i === 0 && (
              <GroupHeader title={t("aispace.group.caches")} size={cacheTotal} note={t("aispace.group.cachesNote")} />
            )}
            <div className="border-b border-border last:border-b-0">
              <RowLine
                checked={bucketSelected(parts)}
                onToggle={(on) => parts.forEach((r) => togglePart(r, on))}
                name={b.title}
                badge={<Badge variant="safe">{t("aispace.level.safe")}</Badge>}
                path={tools.map((x) => x.toolName).join(" · ")}
                sub={t("aispace.toolsCount", { n: tools.length })}
                last="—"
                size={human(b.data.size)}
                expanded={expanded.has(key)}
                onExpand={() => toggleExpand(key)}
              />
              {expanded.has(key) && (
                <div className="border-t border-dashed border-border px-4 pb-2 pt-1 pl-[70px]">
                  {tools.map((x) => (
                    <div key={x.toolName} className="flex items-center gap-2 py-1 text-[12.5px]">
                      <span className="min-w-0 flex-1 truncate">{x.toolName}</span>
                      <span className="w-[110px] text-right text-xs text-muted tabular-nums">—</span>
                      <span className="w-[70px] text-right text-xs tabular-nums">{human(x.size)}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </React.Fragment>
        ),
      });
    });
  }

  const counts = { models: modelSources.length, sessions: sessionByTool.length, caches: cacheBuckets.logs.byTool.size + cacheBuckets.releases.byTool.size };

  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h1>{t("nav.aispace")}</h1>
          <p className="muted">{t("aispace.sub")}</p>
        </div>
        <div className="head-actions">
          <Button variant="ghost" size="sm" onClick={() => void scan()} disabled={busy}>
            <RefreshCw className="h-3.5 w-3.5" />
            {t("fs.rescan")}
          </Button>
        </div>
      </header>

      <span className="sr-only" role="status">
        {done ? t("aispace.done", { size: human(done.freed) }) : ""}
      </span>

      <Card className="mb-4 p-[18px] px-[22px]">
        <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1">
          <Stat label={t("aispace.stat.total")} value={human(total)} strong />
          <Stat label={t("aispace.stat.models")} value={human(modelTotal)} />
          <Stat label={t("aispace.stat.sessions")} value={human(sessionTotal)} />
          <Stat label={t("aispace.stat.caches")} value={human(cacheTotal)} />
          <div className="ml-auto text-xs text-muted">
            {t("aispace.stat.kept")} {human(keptSize)} {t("aispace.stat.keptNote")}
          </div>
        </div>
        <div className="mt-3 flex h-[10px] w-full overflow-hidden rounded-[5px] bg-bg-subtle">
          <i style={{ width: `${total > 0 ? (modelTotal / total) * 100 : 0}%`, background: "var(--accent-solid)" }} />
          <i style={{ width: `${total > 0 ? (sessionTotal / total) * 100 : 0}%`, background: "var(--warn)" }} />
          <i style={{ width: `${total > 0 ? (cacheTotal / total) * 100 : 0}%`, background: "var(--muted)" }} />
        </div>
        <div className="mt-2 text-xs text-muted">{t("aispace.hint")}</div>
      </Card>

      <div className="mb-3 flex flex-wrap items-center gap-2">
        {(["all", "models", "sessions", "caches"] as Filter[]).map((f) => (
          <Button key={f} size="sm" variant={filter === f ? "default" : "ghost"} onClick={() => setFilter(f)}>
            {t(`aispace.filter.${f}` as never)} {f === "all" ? counts.models + counts.sessions + counts.caches : counts[f]}
          </Button>
        ))}
      </div>

      {rows.length === 0 ? (
        <div className="empty">{t("aispace.empty")}</div>
      ) : (
        <Card className="overflow-hidden">
          <div className="flex items-center gap-3 border-b border-border px-4 py-2.5 text-xs text-muted">
            <span className="w-[16px]" />
            <span className="flex-1">{t("aispace.col.source")}</span>
            <span className="w-[110px] text-right">{t("aispace.col.last")}</span>
            <span className="w-[70px] text-right">{t("aispace.col.size")}</span>
            <span className="w-[24px]" />
          </div>
          {rows.map((r) => r.node)}
        </Card>
      )}

      <div className="sticky bottom-0 mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-[12px] border border-border bg-card px-4 py-3 shadow-card">
        <span className="min-w-0 flex-1 text-xs text-muted">
          {chosenTotal > 0 ? t("aispace.foot.picked", { size: human(chosenTotal) }) : t("aispace.foot.empty")}
        </span>
        <Button className="ml-auto max-w-full whitespace-normal" variant="destructive" disabled={chosenTotal === 0 || busy} onClick={() => setConfirmOpen(true)}>
          {t("aispace.trash", { size: human(chosenTotal) })}
        </Button>
      </div>

      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("aispace.confirm.title")}</DialogTitle>
            <DialogDescription>{t("aispace.confirm.body", { size: human(chosenTotal) })}</DialogDescription>
          </DialogHeader>
          <ul className="mt-3 space-y-1 text-xs">
            {chosenModelBytes > 0 && <li>{t("aispace.stat.models")} · {human(chosenModelBytes)}</li>}
            {sessionRows.length > 0 && pickedGroups.size > 0 && <li>{t("aispace.stat.sessions")} · {human(chosenPartBytes)}</li>}
          </ul>
          <p className="mt-3 text-xs text-muted">{t("aispace.confirm.safety")}</p>
          <div className="mt-4 flex justify-end gap-2">
            <Button variant="ghost" onClick={() => setConfirmOpen(false)}>{t("btn.cancel")}</Button>
            <Button variant="destructive" onClick={() => void clean()} disabled={busy}>{t("aispace.confirm.ok")}</Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function Stat({ label, value, strong }: { label: string; value: string; strong?: boolean }) {
  return (
    <div className="flex items-baseline gap-2">
      <span className="text-[13px] text-muted">{label}</span>
      <span className={strong ? "text-[22px] font-semibold tabular-nums" : "text-[18px] font-semibold tabular-nums"}>{value}</span>
    </div>
  );
}

function GroupHeader({ title, size, note, action }: { title: string; size: number; note: string; action?: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-border bg-bg-subtle px-4 py-2 text-xs text-muted">
      <b className="text-[12.5px] font-semibold text-foreground">{title}</b>
      <span className="tabular-nums">{human(size)}</span>
      <span className="min-w-0 flex-1 truncate">{note}</span>
      {action}
    </div>
  );
}

function RowLine({ checked, onToggle, name, badge, path, sub, last, size, expanded, onExpand }: {
  checked: boolean | "indeterminate";
  onToggle: (on: boolean) => void;
  name: string;
  badge: React.ReactNode;
  path: string;
  sub: string;
  last: string;
  size: string;
  expanded: boolean;
  onExpand?: () => void;
}) {
  return (
    <div className="flex items-center gap-3 px-4 py-2.5">
      <Checkbox checked={checked} onCheckedChange={(v) => onToggle(v === true)} aria-label={name} />
      <button type="button" className="min-w-0 flex-1 cursor-pointer text-left" onClick={onExpand} disabled={!onExpand}>
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
          <b className="text-[13.5px] font-semibold">{name}</b>
          {badge}
          <span className="min-w-0 truncate text-xs text-muted">{path}</span>
        </div>
        <div className="mt-0.5 text-xs text-muted">{sub}</div>
      </button>
      <span className="w-[110px] whitespace-nowrap text-right text-xs text-muted tabular-nums">{last}</span>
      <span className="w-[70px] text-right text-xs tabular-nums">{size}</span>
      <button type="button" className="w-[24px] cursor-pointer text-center text-muted" onClick={onExpand} disabled={!onExpand} aria-label={name}>
        {onExpand ? (expanded ? "⌄" : "›") : ""}
      </button>
    </div>
  );
}
