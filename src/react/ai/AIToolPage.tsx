import * as React from "react";
import { ChevronRight, RefreshCw, ShieldCheck } from "lucide-react";
import type { AIPart, AITool } from "../../mygo";
import { Button } from "../../ui/components/button";
import { Card } from "../../ui/components/card";
import { IdleHero } from "../../ui/components/idle-hero";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogClose,
} from "../../ui/components/dialog";
import { cn } from "../../ui/cn";
import { useT, type DictKey } from "../../ui/i18n-react";
import { errText, toast } from "../../shared";
import { human } from "./format";
import { offered, groupSelectable, blockedByRun, canPick, type WindowDays } from "./types";
import { PartRow } from "./PartRow";
import { aiApi } from "./api";
import { codexSize, crashDumps, goCleanForCrashDumps } from "../list/crash-dumps";

type Phase = "idle" | "scanning" | "scanned" | "cleaned";

/** 崩溃转储小于这个数不提示：Crashpad 平时也会留几份。 */
const CRASH_NOTE_BYTES = 100 * 1024 * 1024;

interface AIToolPageProps {
  toolId: string;
  toolName: string;
  /** 每次显示这一视图加一：根不重挂，靠它重新读崩溃转储。 */
  visit: number;
}

interface ConfirmedSelection {
  tool: string;
  part: string;
  groups: string[];
  size: number;
}

export function AIToolPage({ toolId, toolName, visit }: AIToolPageProps) {
  const t = useT();
  const reveal = (path: string) => {
    void aiApi.reveal(path).catch((e: unknown) => toast(t("fs.revealFail", { msg: errText(e) }), "err"));
  };
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
  const [busy, setBusy] = React.useState(false); // 清理进行中：按钮不能再点出第二次清理
  const [scannedAt, setScannedAt] = React.useState(0);
  // Codex 桌面版的崩溃转储不在 ~/.codex 里，由垃圾清理的「应用崩溃转储」负责；这里只提示。
  // 每次显示这一页和每次扫描都读一次：垃圾清理清掉转储后缓存失效，提示跟着重新量。
  const [crash, setCrash] = React.useState({ bytes: 0, threshold: Infinity });
  const crashSeq = React.useRef(0);
  const readCrash = React.useCallback(() => {
    if (toolId !== "codex") return;
    const seq = ++crashSeq.current;
    void crashDumps().then((s) => {
      if (seq !== crashSeq.current) return;
      setCrash(s ? { bytes: codexSize(s), threshold: s.threshold } : { bytes: 0, threshold: Infinity });
    });
  }, [toolId]);
  React.useEffect(() => {
    readCrash();
    return () => { crashSeq.current++; };
  }, [readCrash, visit]);

  const parts = tool?.parts ?? [];
  const visible = parts.filter(offered);
  const lockedParts = parts.filter((p) => !offered(p));
  const lockedSize = lockedParts.reduce((n, p) => n + p.size, 0);
  const cleanable = React.useMemo(() => {
    if (!tool) return 0;
    return visible
      .filter((p) => !blockedByRun(p))
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

  /** 扫描并换上新列表；失败时显示错误并返回 false。 */
  const scan = React.useCallback(async (): Promise<boolean> => {
    setPhase("scanning"); setError(null); setLastClean(null);
    readCrash();
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
        if (p.level !== "safe" || blockedByRun(p)) continue;
        if (p.groups.length > 0) {
          // 分组部件（如旧版本）：没有可选分组时不勾，避免勾上一个 0 B 的空壳。
          const keys = p.groups.filter((g) => groupSelectable(p, g, 30, now)).map((g) => g.key);
          if (keys.length === 0) continue;
          ng.set(p.id, new Set(keys));
        } else if (p.size === 0) {
          // 清理后重扫：已清空的缓存部件保持未勾，CTA 不会顶着 0 B 的对勾。
          continue;
        }
        np.add(p.id);
      }
      setPicked(np); setPickedGroups(ng);
      setPhase("scanned");
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setPhase("idle");
      return false;
    }
  }, [toolId, readCrash]);

  // 不自动扫描；进入页面保持 idle。
  // 分组的部分在当前窗口下一组都选不了时也不能勾，免得勾出一个 0 B 的对勾。
  const selectable = (p: AIPart) => canPick(p, windowDays, scannedAt);

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
    if (busy) return;
    setConfirmOpen(false);
    setBusy(true);
    try {
      const res = await aiApi.clean(selections.map(({ tool, part, groups }) => ({ tool, part, groups })));
      const done = {
        freed: res.freed,
        kept: res.parts.reduce((n, r) => n + r.kept, 0),
        skipped: res.parts.filter((r) => r.skipped || r.failed.length).length,
      };
      // 重扫成功才把结果写在新列表上；重扫失败页面显示失败，清理结果用提示告诉用户。
      if (await scan()) {
        setPhase("cleaned");
        setLastClean(done);
      } else {
        toast(doneText(done, t));
      }
    } catch (e) {
      toast(errText(e), "err");
    } finally {
      setBusy(false);
    }
  };

  const headKey = `ai.head.${toolId}` as DictKey;
  const headText = t(headKey);

  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h1 translate="no">{toolName}</h1>
          <p className="muted">{headText.startsWith("ai.head.") ? "" : headText}</p>
        </div>
        <div className="head-actions">
          {tool && (
            <Button variant="ghost" size="sm" onClick={scan} disabled={phase === "scanning" || busy}>
              <RefreshCw className={cn("h-3.5 w-3.5", phase === "scanning" && "animate-spin")} />
              {phase === "scanning" ? t("scan.busy") : t("fs.rescan")}
            </Button>
          )}
        </div>
      </header>

      {crash.bytes >= CRASH_NOTE_BYTES && (
        <div
          className="mb-4 flex items-center justify-between gap-3 rounded-[10px] px-4 py-2.5 text-[12.5px] leading-relaxed text-warning-text"
          style={{ background: "var(--warning-soft)" }}
          role="status"
        >
          <span>{t(crash.bytes >= crash.threshold ? "ai.crash.big" : "ai.crash.some", { size: human(crash.bytes) })}</span>
          <Button variant="ghost" size="sm" className="shrink-0" onClick={goCleanForCrashDumps}>{t("ai.crash.clean")}</Button>
        </div>
      )}

      {phase === "idle" && error !== null && (
        <div className="empty err" role="alert">
          <div className="err-icon">⚠</div>
          <div>{t("scan.error", { msg: errText(error) })}</div>
          <button className="btn ghost retry-btn" type="button" onClick={() => void scan()}>
            {t("btn.retry")}
          </button>
        </div>
      )}
      {phase === "idle" && error === null && (
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
            : toolId === "trae"
              ? [t("ai.chip.sessions"), t("ai.chip.cache")]
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
          <Card className="ai-hero mb-5 flex items-center justify-between gap-4 p-[18px] px-[22px]">
            <div className="min-w-0">
              <div className="flex items-baseline gap-[10px]">
                <span className="text-[13px] text-muted">{t("ai.occupied")}</span>
                <span className="text-[26px] font-semibold tabular-nums tracking-[-0.4px]">{human(tool.size)}</span>
              </div>
              <div className="mt-[2px] text-xs text-muted">
                {lastClean ? doneText(lastClean, t) : t("ai.sub.scanned", { size: human(cleanable) })}
              </div>
            </div>
            <Button variant="default" size="lg" disabled={chosenTotal === 0 || busy} onClick={() => setConfirmOpen(true)}>
              {t("ai.clean", { size: human(chosenTotal) })}
            </Button>
          </Card>

          {tool.running && (
            <div className="mb-4 rounded-[10px] px-4 py-2.5 text-[12px] leading-relaxed text-warning-text"
                 style={{ background: "var(--warning-soft)" }}
                 role="status">
              {t("ai.running", { name: runningName(tool, toolName, t) })}
            </div>
          )}

          <Card className="overflow-hidden">
            <div>{visible.map((p) => (
              <PartRow
                key={p.id}
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
                onReveal={reveal}
                onPickOld={() => pickOld(p)}
                onWindowChange={changeWindow}
              />
            ))}</div>
            {lockedParts.length > 0 && (
              <>
                <button
                  type="button"
                  className="flex w-full items-center justify-between border-t border-border px-4 py-3 text-sm text-muted hover:bg-muted-background"
                  aria-expanded={showLocked}
                  onClick={() => setShowLocked((v) => !v)}
                >
                  <span className="flex items-center gap-2">
                    <ShieldCheck className="h-4 w-4" />
                    {t("ai.locked", { n: lockedParts.length, size: human(lockedSize) })}
                  </span>
                  <ChevronRight className={cn("h-4 w-4 transition-transform", showLocked && "rotate-90")} />
                </button>
                {showLocked && <div className="border-t border-border bg-muted-background/50">
                  {lockedParts.map((p) => (
                    <PartRow
                      key={p.id}
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
                      onReveal={reveal}
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

/** 一次清理的结果：释放了多少，另有几项留下、几部分跳过。 */
function doneText(done: { freed: number; kept: number; skipped: number }, t: (k: DictKey, v?: Record<string, string | number>) => string) {
  return [
    t("ai.done", { size: human(done.freed) }),
    done.kept > 0 ? t("ai.done.kept", { n: done.kept }) : "",
    done.skipped > 0 ? t("ai.done.skipped", { n: done.skipped }) : "",
  ].filter(Boolean).join(" · ");
}

/** TRAE 有 IDE 和 CLI 两个应用，提示里说清是哪个在运行。 */
function runningName(tool: AITool, toolName: string, t: (k: DictKey) => string) {
  const apps = tool.runningApps ?? [];
  if (tool.id !== "trae" || apps.length !== 1) return toolName;
  return t(apps[0] === "ide" ? "ai.app.trae.ide" : "ai.app.trae.cli");
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
