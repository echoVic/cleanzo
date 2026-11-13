import type { JunkItem } from "../../mygo";
import { ruleName } from "../../i18n";
import { errText, human } from "../../shared";
import { useT, type DictKey } from "../../ui/i18n-react";
import { itemTitle, type RowState } from "./helpers";

export type ResultGroup = "processed" | "skipped" | "incomplete";

/** Counts here refer to cleanup rules; inUse and failed refer to individual entries. */
export function resultGroup(row: RowState): ResultGroup {
  if (row.state !== "done" || !row.result || row.result.failed.length > 0) return "incomplete";
  if (row.result.skipped || row.result.inUse > 0) return "skipped";
  return "processed";
}

export function CleanResults({ rows, items }: { rows: Map<string, RowState>; items: JunkItem[] }) {
  const t = useT();
  const reasonText = (row: RowState) => {
    const res = row.result;
    const reasons: string[] = [];
    if (row.state !== "done") reasons.push(t(row.state === "stopped" ? "clean.result.stopped" : "clean.result.notStarted"));
    if (res?.skipped) {
      const key = `junk.skip.${res.skipped}` as DictKey;
      const translated = t(key);
      reasons.push(translated === key ? errText(res.skipped) : translated);
    }
    if (res?.inUse) reasons.push(t("clean.done.inUse", { n: res.inUse }));
    const counts = new Map<string, number>();
    for (const failure of res?.failed ?? []) counts.set(failure, (counts.get(failure) ?? 0) + 1);
    for (const [reason, n] of counts) {
      reasons.push(reason === "changed" ? t("cat.changedN", { n }) : t("clean.result.reason", { n, reason: errText(reason) }));
    }
    return reasons.join(t("phrase.sep"));
  };
  return <section className="cleanup-results" aria-label={t("clean.result.title")}>
    <h2>{t("clean.result.title")}</h2>
    <p className="muted small">{t("clean.result.trashNote")}</p>
    {(["processed", "skipped", "incomplete"] as const).map((group) => {
      const members = [...rows].filter(([, row]) => resultGroup(row) === group);
      if (!members.length) return null;
      return <details className="cleanup-result-group" key={group} open={group !== "processed"}>
        <summary>{t(`clean.result.${group}`)} <span className="muted">{t("clean.result.count", { n: members.length })}</span></summary>
        <div className="cleanup-result-rows">
          {members.map(([id, row]) => {
            const it = items.find((item) => item.id === id);
            const res = row.result;
            const amounts = [
              res?.freed ? t("clean.done.freed", { size: human(res.freed) }) : "",
              res?.trashed ? t("cat.trashed", { size: human(res.trashed) }) : "",
            ].filter(Boolean).join(t("phrase.sep"));
            const reason = reasonText(row);
            return <div className="cleanup-result-row" key={id}>
              <div><strong>{it ? itemTitle(it, ruleName) : id}</strong>{reason && <div className="muted small">{reason}</div>}</div>
              <span className="small">{amounts || (row.state === "stopped" ? t("clean.result.unconfirmed") : group === "processed" ? t("cat.cleanedNothing") : t("cat.notCleaned"))}</span>
            </div>;
          })}
        </div>
      </details>;
    })}
  </section>;
}
