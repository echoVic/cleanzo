import * as React from "react";
import type { AppInfo, AppUpdate, GrowthDelta } from "../../mygo";
import { isWindows } from "../../platform";
import { human } from "../../shared";
import { Button } from "../../ui/components/button";
import { useT } from "../../ui/i18n-react";
import { fmtDelta } from "../growth/format";
import type { AppRowProps } from "./AppRow";
import { reasonKey, sourceKey, type UsageSplit } from "./updates";

export interface AppRowExpandedProps {
  app: AppInfo;
  update: AppUpdate | undefined;
  checking: boolean;
  usage: UsageSplit | null;
  usageError: string | null;
  week: GrowthDelta | null;
  month: GrowthDelta | null;
  busy: AppRowProps["busy"];
  brewBusy: boolean;
  stuck: boolean;
  upgradeFail: { detail: string; command: string } | null;
  onUpgrade: (id: string) => void;
  onCleanCache: (id: string) => void;
  onQuit: (id: string) => void;
  onForceQuit: (id: string) => void;
  onUninstall: (id: string) => void;
  onCopy: (text: string) => void;
}

/** 展开后的三栏；Windows 只有占用（App 大小）和卸载。 */
export function AppRowExpanded(p: AppRowExpandedProps) {
  if (isWindows()) {
    return (
      <div className="app-expand">
        <UsageCell {...p} />
        <RunCell {...p} />
      </div>
    );
  }
  return (
    <div className="app-expand">
      <VersionCell {...p} />
      <UsageCell {...p} />
      <RunCell {...p} />
    </div>
  );
}

export function VersionCell({ app: a, update: u, checking, busy, brewBusy, upgradeFail, onUpgrade, onCopy }: AppRowExpandedProps) {
  const t = useT();
  const src = u ? sourceKey(u.source) : null;
  return (
    <section>
      <h4>{t("apps.col.version")}</h4>
      {!u && checking && <div className="kv muted">{t("apps.checkingOne")}</div>}
      {!u && !checking && <div className="kv muted">{a.version || "—"}</div>}
      {u?.state === "update" && (
        <>
          <div className="kv"><span className="num">{u.current} → {u.latest}</span>{src && <span className="muted">{t(src)}</span>}</div>
          <div className="actions">
            {u.action !== "" && (
              <Button size="sm" disabled={busy !== null || (u.action === "brew" && (brewBusy || a.running))} onClick={() => onUpgrade(a.id)}>
                {busy === "upgrade" ? t("apps.updating") : t("apps.update")}
              </Button>
            )}
            {u.notesUrl && <a className="mlink" href={u.notesUrl} target="_blank" rel="noopener noreferrer">{t("apps.notes")}</a>}
          </div>
          {u.action === "open-app" && <p className="muted small">{t("apps.hint.openApp")}</p>}
          {u.action === "open-store" && <p className="muted small">{t("apps.hint.store")}</p>}
          {u.action === "brew" && a.running && <p className="muted small">{t("apps.hint.quitFirst")}</p>}
        </>
      )}
      {u?.state === "current" && <div className="kv"><span>{t("apps.upToDate")} · <span className="num">{u.current}</span></span>{src && <span className="muted">{t(src)}</span>}</div>}
      {(u?.state === "unknown" || u?.state === "error") && <div className="kv muted">{t(reasonKey(u.reason))}</div>}
      {upgradeFail && (
        <div className="err-inline small">
          <span>{upgradeFail.detail}</span>{" "}
          <Button variant="ghost" size="sm" onClick={() => onCopy(upgradeFail.command)}>{t("apps.copyCmd")}</Button>
        </div>
      )}
    </section>
  );
}

export function UsageCell({ app: a, usage, usageError, week, month, busy, onCleanCache }: AppRowExpandedProps) {
  const t = useT();
  const atLeast = (s: string) => t("growth.atLeast", { size: s });
  if (isWindows()) {
    return (
      <section>
        <h4>{t("apps.col.usage")}</h4>
        <div className="kv"><span>{t("apps.part.app")}</span><span className="num">{human(a.size)}</span></div>
      </section>
    );
  }
  return (
    <section>
      <h4>{t("apps.col.usage")}</h4>
      {usageError && <div className="kv err-inline">{usageError}</div>}
      {!usageError && !usage && <div className="kv muted">{t("apps.measuring")}</div>}
      {usage && (
        <>
          <div className="kv"><span>{t("apps.part.app")}</span><span className="num">{human(usage.app)}</span></div>
          <div className="kv"><span>{t("apps.part.data")}</span><span className="num">{human(usage.data)}</span></div>
          <div className="kv">
            <span>{t("apps.part.cache")}</span>
            <span>
              <span className="num">{human(usage.cache)}</span>{" "}
              <Button variant="ghost" size="sm" disabled={usage.cache <= 0 || a.running || busy !== null} onClick={() => onCleanCache(a.id)}>
                {t("apps.cleanCache")}
              </Button>
            </span>
          </div>
          {a.running && usage.cache > 0 && <p className="muted small">{t("apps.hint.quitFirst")}</p>}
        </>
      )}
      {(week || month) && <p className="muted small">{t("apps.growth", { week: fmtDelta(week, atLeast), month: fmtDelta(month, atLeast) })}</p>}
    </section>
  );
}

export function RunCell({ app: a, busy, stuck, onQuit, onForceQuit, onUninstall }: AppRowExpandedProps) {
  const t = useT();
  return (
    <section>
      {!isWindows() && (
        <>
          <h4>{t("apps.col.run")}</h4>
          {a.running ? (
            <div className="actions">
              <Button variant="ghost" size="sm" disabled={busy !== null} onClick={() => onQuit(a.id)}>{t("apps.quit")}</Button>
              {stuck && <Button variant="destructive" size="sm" disabled={busy !== null} onClick={() => onForceQuit(a.id)}>{t("process.force")}</Button>}
            </div>
          ) : (
            <div className="kv muted">{t("apps.notRunning")}</div>
          )}
        </>
      )}
      <div className="actions">
        <Button variant="ghost" size="sm" className="app-uninstall" disabled={busy !== null} onClick={() => onUninstall(a.id)}>
          {busy === "uninstall" ? t("apps.moving") : t("apps.uninstall")}
        </Button>
      </div>
    </section>
  );
}
