import * as React from "react";
import { RefreshCw, ShieldCheck } from "lucide-react";
import type { AIPart, AITool } from "../../mygo";
import { Button } from "../../ui/components/button";
import { Card } from "../../ui/components/card";
import { IdleHero } from "../../ui/components/idle-hero";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogClose,
} from "../../ui/components/dialog";
import { cn } from "../../ui/cn";
import { useT, type DictKey } from "../../ui/i18n-react";
import { toast } from "../../shared";
import { human } from "./format";
import { offered, groupSelectable, type WindowDays } from "./types";
import { PartRow } from "./PartRow";
import { aiApi } from "./api";

type Phase = "idle" | "scanning" | "scanned" | "cleaned";

interface AIToolPageProps {
  toolId: string;
  toolName: string;
}

interface ConfirmedSelection {
  tool: string;
  part: string;
  groups: string[];
  size: number;
}

export function AIToolPage({ toolId, toolName }: AIToolPageProps) {
  const t = useT();
  const [tool, setTool] = React.useState<AITool | null>(null);
  const [phase, setPhase] = React.useState<Phase>("idle");
  const [error, setError] = React.useState<string | null>(null);
  const [windowDays, setWindowDays] = React.useState<WindowDays>(30);
  const [picked, setPicked] = React.useState<Set<string>>(new Set());
  const [pickedGroups, setPickedGroups] = React.useState<Map<string, Set<string>>>(new Map());
  const [expanded, setExpanded] = React.useState<Set<string>>(new Set());
  const [showLocked, setShowLocked] = React.useState(false);
  const [lastClean, setLastClean] = React.useState<{ freed: number; kept: number; skipped: number } | null>(null);
  const [confirmOpen, setConfirmOpen] = React.useState(false);
  const [scannedAt, setScannedAt] = React.useState(0);

  const parts = tool?.parts ?? [];
  const visible = parts.filter(offered);
  const lockedParts = parts.filter((p) => !offered(p));
  const lockedSize = lockedParts.reduce((n, p) => n + p.size, 0);
  const cleanable = React.useMemo(() => {
    if (!tool) return 0;
    return visible
      .filter((p) => !(tool.running && !p.runSafe))
      .reduce((n, p) => {
        if (p.groups.length === 0) return n + p.size;
        return n + p.groups.filter((g) => groupSelectable(p, g, windowDays, scannedAt)).reduce((m, g) => m + g.size, 0);
      }, 0);
  }, [tool, visible, windowDays, scannedAt]);

  const chosenSize = (p: AIPart) => {
    if (p.groups.length === 0) return picked.has(p.id) ? p.size : 0;
    const keys = pickedGroups.get(p.id);
    if (!keys) return 0;
    return p.groups.filter((g) => keys.has(g.key) && groupSelectable(p, g, windowDays, scannedAt)).reduce((n, g) => n + g.size, 0);
  };
  const chosenTotal = parts.reduce((n, p) => n + chosenSize(p), 0);

  const scan = React.useCallback(async () => {
    setPhase("scanning"); setError(null); setLastClean(null);
    try {
      const res = await aiApi.scanTool(toolId);
      const s = res.tools[0];
      if (!s) throw new Error("empty");
      setTool(s); setScannedAt(res.scannedAt);
      // 默认勾选安全项。
      const np = new Set<string>();
      const ng = new Map<string, Set<string>>();
      const now = res.scannedAt;
      for (const p of s.parts) {
        if (p.level !== "safe" || (s.running && !p.runSafe)) continue;
        np.add(p.id);
        if (p.groups.length > 0) {
          ng.set(p.id, new Set(p.groups.filter((g) => groupSelectable(p, g, 30, now)).map((g) => g.key)));
        }
      }
      setPicked(np); setPickedGroups(ng);
      setPhase("scanned");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setPhase("idle");
    }
  }, [toolId]);

  // 不自动扫描；进入页面保持 idle。
  const selectable = (p: AIPart) => p.size > 0 && !(tool?.running && !p.runSafe);

  const togglePart = (p: AIPart, on: boolean) => {
    if (!selectable(p)) return;
    setPicked((prev) => {
      const next = new Set(prev);
      if (on) next.add(p.id); else next.delete(p.id);
      return next;
    });
    if (p.groups.length > 0) {
      setPickedGroups((prev) => {
        const next = new Map(prev);
        if (on) {
          const keys = p.groups.filter((g) => groupSelectable(p, g, windowDays, scannedAt)).map((g) => g.key);
          next.set(p.id, new Set(keys));
          if (isSessionsPart(p)) setExpanded((e) => new Set(e).add(p.id));
        } else next.delete(p.id);
        return next;
      });
    }
  };
  const toggleGroup = (p: AIPart, key: string, on: boolean) => {
    setPickedGroups((prev) => {
      const next = new Map(prev);
      const keys = new Set(next.get(p.id) ?? []);
      if (on) keys.add(key); else keys.delete(key);
      next.set(p.id, keys);
      setPicked((pp) => {
        const nn = new Set(pp);
        if (keys.size > 0) nn.add(p.id); else nn.delete(p.id);
        return nn;
      });
      return next;
    });
  };
  const pickOld = (p: AIPart) => {
    if (!selectable(p)) return;
    togglePart(p, true);
  };
  const changeWindow = (d: WindowDays) => {
    setWindowDays(d);
    setPickedGroups((prev) => {
      const next = new Map(prev);
      for (const p of parts) {
        if (p.groups.length === 0) continue;
        const keys = next.get(p.id);
        if (!keys) continue;
        for (const k of [...keys]) {
          const g = p.groups.find((x) => x.key === k);
          if (!g || !groupSelectable(p, g, d, scannedAt)) keys.delete(k);
        }
        if (keys.size === 0) { next.delete(p.id); setPicked((pp) => { const n = new Set(pp); n.delete(p.id); return n; }); }
      }
      return next;
    });
  };
  const toggleExpand = (id: string) => setExpanded((prev) => {
    const next = new Set(prev);
    if (next.has(id)) next.delete(id); else next.add(id);
    return next;
  });

  const selections: ConfirmedSelection[] = parts.flatMap((p) => {
    const size = chosenSize(p);
    if (size === 0) return [];
    return [{ tool: toolId, part: p.id, size, groups: p.groups.length > 0 ? [...(pickedGroups.get(p.id) ?? [])] : [] }];
  });

  const doClean = async () => {
    setConfirmOpen(false);
    try {
      const res = await aiApi.clean(selections.map(({ tool, part, groups }) => ({ tool, part, groups })));
      const kept = res.parts.reduce((n, r) => n + r.kept, 0);
      const skipped = res.parts.filter((r) => r.skipped || r.failed.length).length;
      await scan();
      setPhase("cleaned");
      setLastClean({ freed: res.freed, kept, skipped });
    } catch (e) {
      toast(e instanceof Error ? e.message : String(e), "err");
    }
  };

  const headKey = `ai.head.${toolId}` as DictKey;
  const headText = t(headKey);

  return (
    <div className="cz-react px-1">
      <header className="view-head">
        <div>
          <h2>{toolName}</h2>
          <p className="muted">{headText.startsWith("ai.head.") ? "" : headText}</p>
        </div>
        <div className="head-actions">
          {tool && (
            <Button variant="ghost" size="sm" onClick={scan} disabled={phase === "scanning"}>
              <RefreshCw className={cn("h-3.5 w-3.5", phase === "scanning" && "animate-spin")} />
              {phase === "scanning" ? t("scan.busy") : t("fs.rescan")}
            </Button>
          )}
        </div>
      </header>

      {phase === "idle" && (
        <IdleHero
          label={t("hero.ready")}
          value="--"
          sub={t("clean.idleSub")}
          guide={t(`ai.idle.guide.${toolId}` as DictKey)}
          ctaText={t("ai.idle.cta")}
          onCta={scan}
          safetyText={t("ai.idle.desc")}
          chips={toolId === "codex"
            ? [t("ai.chip.oldVersions"), t("ai.chip.sessions"), t("ai.chip.cache")]
            : [t("ai.chip.sessions"), t("ai.chip.snapshots"), t("ai.chip.cache")]}
        />
      )}
      {phase === "scanning" && (
        <div className="flex flex-col items-center py-20 text-center">
          <RefreshCw className="h-9 w-9 animate-spin text-primary" />
          <div className="mt-4 text-sm text-muted">{t("ai.sub.scanning")}</div>
        </div>
      )}

      {(phase === "scanned" || phase === "cleaned") && tool && (
        <>
          <Card className="mb-5 flex items-center justify-between gap-4 p-5">
            <div className="min-w-0">
              <div className="flex items-baseline gap-3">
                <span className="text-sm text-muted">{t("ai.occupied")}</span>
                <span className="text-2xl font-semibold tabular-nums">{human(tool.size)}</span>
              </div>
              <div className="mt-1 text-xs text-muted">
                {lastClean
                  ? [t("ai.done", { size: human(lastClean.freed) }),
                     lastClean.kept > 0 ? t("ai.done.kept", { n: lastClean.kept }) : "",
                     lastClean.skipped > 0 ? t("ai.done.skipped", { n: lastClean.skipped }) : ""].filter(Boolean).join(" · ")
                  : t("ai.sub.scanned", { size: human(cleanable) })}
              </div>
            </div>
            <Button variant="destructive" size="lg" disabled={chosenTotal === 0} onClick={() => setConfirmOpen(true)}>
              {t("ai.clean", { size: human(chosenTotal) })}
            </Button>
          </Card>

          {tool.running && (
            <div className="mb-4 rounded-[10px] px-4 py-2.5 text-[12px] leading-relaxed text-warning-text"
                 style={{ background: "var(--warning-soft)" }}>
              {t("ai.running", { name: toolName })}
            </div>
          )}

          <Card className="overflow-hidden">
            <div>{visible.map((p) => (
              <PartRow
                key={p.id}
                tool={tool}
                part={p}
                windowDays={windowDays}
                scannedAt={scannedAt}
                picked={picked.has(p.id)}
                groups={pickedGroups.get(p.id) ?? new Set()}
                expanded={expanded.has(p.id)}
                canSelect={selectable(p)}
                chosenSize={chosenSize(p)}
                onTogglePart={(on) => togglePart(p, on)}
                onToggleGroup={(key, on) => toggleGroup(p, key, on)}
                onExpand={() => toggleExpand(p.id)}
                onReveal={(path) => aiApi.reveal(path).catch(() => {})}
                onPickOld={() => pickOld(p)}
                onWindowChange={changeWindow}
              />
            ))}</div>
            {lockedParts.length > 0 && (
              <>
                <button
                  type="button"
                  className="flex w-full items-center justify-between border-t border-border px-4 py-3 text-sm text-muted hover:bg-muted-background"
                  onClick={() => setShowLocked((v) => !v)}
                >
                  <span className="flex items-center gap-2">
                    <ShieldCheck className="h-4 w-4" />
                    {t("ai.locked", { n: lockedParts.length, size: human(lockedSize) })}
                  </span>
                  <RefreshCw className={cn("h-4 w-4 rotate-90 transition-transform", showLocked && "rotate-[270deg]")} />
                </button>
                {showLocked && <div className="border-t border-border bg-muted-background/50">
                  {lockedParts.map((p) => (
                    <PartRow
                      key={p.id}
                      tool={tool}
                      part={p}
                      windowDays={windowDays}
                      scannedAt={scannedAt}
                      picked={false}
                      groups={new Set()}
                      expanded={false}
                      canSelect={false}
                      chosenSize={0}
                      onTogglePart={() => {}}
                      onToggleGroup={() => {}}
                      onExpand={() => {}}
                      onReveal={(path) => aiApi.reveal(path).catch(() => {})}
                      onPickOld={() => {}}
                      onWindowChange={changeWindow}
                    />
                  ))}
                </div>}
              </>
            )}
          </Card>
          <p className="mt-3 text-xs text-muted">{t("ai.foot")}</p>
        </>
      )}

      <ConfirmDialog
        open={confirmOpen}
        toolName={toolName}
        total={chosenTotal}
        selections={selections}
        parts={parts}
        onClose={() => setConfirmOpen(false)}
        onConfirm={doClean}
      />
    </div>
  );
}

function isSessionsPart(p: AIPart) {
  return p.level === "sensitive";
}

function ConfirmDialog({
  open, toolName, total, selections, parts, onClose, onConfirm,
}: {
  open: boolean; toolName: string; total: number;
  selections: ConfirmedSelection[];
  parts: AIPart[];
  onClose: () => void; onConfirm: () => void;
}) {
  const t = useT();
  const lines = selections.map((s) => {
    const p = parts.find((x) => x.id === s.part);
    if (!p) return null;
    const k = `ai.part.${p.id}.lose` as DictKey;
    const lose = t(k);
    const count = p.groups.length > 0
      ? t(isSessionsPart(p) ? "ai.confirm.groups" : "ai.confirm.releases", { n: s.groups.length })
      : "";
    return { name: partName(p, t), size: human(s.size), count, lose: lose.startsWith("ai.part.") ? t("ai.lose.default") : lose };
  }).filter(Boolean) as { name: string; size: string; count: string; lose: string }[];

  return (
    <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("ai.confirm.title")}</DialogTitle>
          <DialogDescription>{t("ai.confirm.body", { name: toolName, size: human(total) })}</DialogDescription>
        </DialogHeader>
        <ul className="max-h-[42vh] space-y-2 overflow-auto">
          {lines.map((l, i) => (
            <li key={i} className="text-sm">
              <b>{l.name}</b> {l.size} {l.count && <span className="text-muted">· {l.count}</span>}
              <div className="text-xs text-muted">{l.lose}</div>
            </li>
          ))}
        </ul>
        <p className="mt-3 text-xs text-muted">{t("ai.confirm.safety")}</p>
        <div className="mt-4 flex justify-end gap-2">
          <DialogClose asChild>
            <Button variant="ghost">{t("btn.cancel")}</Button>
          </DialogClose>
          <Button variant="destructive" onClick={onConfirm}>{t("ai.confirm.ok")}</Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}

function partName(p: AIPart, t: (k: DictKey, v?: Record<string, string | number>) => string) {
  if (p.id.startsWith("unknown:")) return p.name;
  const k = `ai.part.${p.id}.name` as DictKey;
  const s = t(k);
  return s === k ? p.id : s;
}
