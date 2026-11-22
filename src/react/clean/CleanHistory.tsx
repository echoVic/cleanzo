import type { CleanRecord, CleanRecordRow, JunkItem } from "../../mygo";
import { ruleName } from "../../i18n";
import { human } from "../../shared";
import { useT, type DictKey } from "../../ui/i18n-react";
import { itemTitle } from "./helpers";
import { HISTORY_SHOWN, fmtWhen, historyTotals, regrown, whenText } from "./history";

/** 清理不是从清理页发起时，记录里写明是从哪里发起的。 */
const SOURCES = new Set(["low-space", "trash-reminder"]);

/**
 * 清理记录的内容：最近的合计、这次扫描里清过又长回来的行、每一次清理清掉了什么。
 * 弹窗的外壳和按钮在清理页里。
 */
export function CleanHistoryBody({ history, items, now }: { history: readonly CleanRecord[]; items: readonly JunkItem[]; now: number }) {
  const t = useT();
  if (history.length === 0) return <p className="muted">{t("history.empty")}</p>;
  const totals = historyTotals(history, now);
  const back = regrown(history, items, now);
  // 规则行按当前语言取名；应用、自定义目录和项目用清理当时显示的名字。
  const rowName = (row: CleanRecordRow) => row.name || ruleName(row.id);
  const amounts = (freed: number, trashed: number) => [
    freed > 0 ? t("clean.done.freed", { size: human(freed) }) : "",
    trashed > 0 ? t("cat.trashed", { size: human(trashed) }) : "",
  ].filter(Boolean).join(t("phrase.sep"));
  return (
    <div className="clean-history">
      <p className="clean-history-sum">
        {t("history.summary", { n: totals.count, size: human(totals.freed) })}
        {totals.trashed > 0 && <span className="muted">{t("phrase.sep")}{t("history.summaryTrash", { size: human(totals.trashed) })}</span>}
      </p>
      {back.length > 0 && (
        <section className="clean-history-back">
          <h3>{t("history.backTitle")}</h3>
          {back.map(({ item, at, cleaned, size }) => (
            <div className="cleanup-result-row" key={item.id}>
              <div><strong>{itemTitle(item, ruleName)}</strong></div>
              <span className="small">{t("history.backRow", { when: whenText(at, now, t), cleaned: human(cleaned), size: human(size) })}</span>
            </div>
          ))}
        </section>
      )}
      <div className="clean-history-list">
        {history.slice(0, HISTORY_SHOWN).map((rec, i) => (
          <details className="cleanup-result-group" key={`${rec.at}:${i}`}>
            <summary>
              {fmtWhen(rec.at)}
              {SOURCES.has(rec.source) && <span className="tag dim">{t(`history.source.${rec.source}` as DictKey)}</span>}
              <span className="muted">{amounts(rec.freed, rec.trashed)}</span>
            </summary>
            <div className="cleanup-result-rows">
              {rec.rows.map((row) => (
                <div className="cleanup-result-row" key={row.id}>
                  <div><strong>{rowName(row)}</strong></div>
                  <span className="small">{amounts(row.freed, row.trashed)}</span>
                </div>
              ))}
            </div>
          </details>
        ))}
      </div>
      <p className="muted small">{t("history.local")}</p>
    </div>
  );
}
