import { isWindows } from "../../platform";
import * as React from "react";
import { Root as TabsRoot, List as TabsList, Trigger as TabsTrigger } from "@radix-ui/react-tabs";
import { Button } from "../../ui/components/button";
import { segTrigger } from "../../ui/components/seg";
import { useIndicator } from "../../ui/motion/indicator";
import { useT } from "../../ui/i18n-react";
import { setNavBusy } from "../../shared";
import { AppsList } from "./AppsList";
import { LeftoversTab } from "./LeftoversTab";
import type { AppsFilter } from "./updates";

interface AppsPageProps {
  active: boolean;
  initialFilter: AppsFilter;
  nonce: number;
  uninstallPath?: string | null;
  uninstallNonce?: number;
}

export function AppsPage({ active, initialFilter, nonce, uninstallPath = null, uninstallNonce = 0 }: AppsPageProps) {
  const t = useT();
  const win = isWindows();
  const requestedFilter = win ? "all" : initialFilter;
  const [filter, setFilter] = React.useState<AppsFilter>(requestedFilter);
  const [orphanCount, setOrphanCount] = React.useState<number | null>(null);
  const [counts, setCounts] = React.useState<{ updates: number | null; total: number }>({ updates: null, total: 0 });
  const [installedBusy, setInstalledBusy] = React.useState(false);
  const [orphanBusy, setOrphanBusy] = React.useState(false);
  const [checking, setChecking] = React.useState(false);
  const [recheckNonce, setRecheckNonce] = React.useState(0);
  const [seenNonce, setSeenNonce] = React.useState(nonce);
  const tabsId = React.useId();
  const installedPanelId = `${tabsId}-installed`;
  const leftoversPanelId = `${tabsId}-leftovers`;
  const segRef = React.useRef<HTMLDivElement>(null);
  const segStyle = useIndicator(segRef, '[data-state="active"]');
  // 外部请求与本次渲染一起应用，列表收到请求时已经回到正确筛选。
  if (seenNonce !== nonce) { setSeenNonce(nonce); setFilter(requestedFilter); }

  React.useEffect(() => { setNavBusy("apps", installedBusy || orphanBusy); }, [installedBusy, orphanBusy]);
  const currentFilter = win ? "all" : filter;
  const installed = <AppsList active={active} filter={currentFilter} onBusyChange={setInstalledBusy} onCounts={setCounts} onChecking={setChecking} uninstallPath={uninstallPath} uninstallNonce={uninstallNonce} recheckNonce={recheckNonce} />;
  return (
    <div className="cz-react">
      <header className="view-head">
        <div><h1>{t("nav.apps")}</h1><p className="muted">{t("apps.sub")}</p></div>
        {!win && <Button variant="ghost" size="sm" onClick={() => setRecheckNonce((n) => n + 1)} disabled={checking}>{t("apps.recheck")}</Button>}
      </header>
      {win ? installed : <TabsRoot value={currentFilter} onValueChange={(v) => setFilter(v as AppsFilter)}>
        <TabsList ref={segRef} aria-label={t("apps.filters")} className="relative mb-4 inline-flex gap-[2px] rounded-[8px] bg-[var(--bg-subtle)] p-[2px]">
          <span aria-hidden="true" className="cz-seg-thumb" style={segStyle} />
          <TabsTrigger value="all" id={`${tabsId}-all`} aria-controls={installedPanelId} className={segTrigger}>{t("apps.filter.all", { n: counts.total })}</TabsTrigger>
          <TabsTrigger value="updates" id={`${tabsId}-updates`} aria-controls={installedPanelId} className={segTrigger}>{counts.updates === null ? t("apps.filter.updates") : t("apps.filter.updatesCount", { n: counts.updates })}</TabsTrigger>
          <TabsTrigger value="idle" id={`${tabsId}-idle`} aria-controls={installedPanelId} className={segTrigger}>{t("apps.filter.idle")}</TabsTrigger>
          <TabsTrigger value="leftovers" id={`${tabsId}-leftovers-tab`} aria-controls={leftoversPanelId} className={segTrigger}>{orphanCount === null ? t("apps.tab.leftovers") : t("apps.tab.leftoversCount", { n: orphanCount })}</TabsTrigger>
        </TabsList>
        <div id={installedPanelId} role="tabpanel" tabIndex={0} aria-labelledby={`${tabsId}-${currentFilter === "leftovers" ? "all" : currentFilter}`} hidden={currentFilter === "leftovers" || undefined}>{installed}</div>
        <div id={leftoversPanelId} role="tabpanel" tabIndex={0} aria-labelledby={`${tabsId}-leftovers-tab`} hidden={currentFilter !== "leftovers" || undefined}><LeftoversTab active={active && currentFilter === "leftovers"} onCountChange={setOrphanCount} onBusyChange={setOrphanBusy} /></div>
      </TabsRoot>}
      {!win && currentFilter !== "leftovers" && <footer className="muted small mt-4">{t("apps.checkNote")}</footer>}
      {win && <footer className="muted small mt-4"><a className="mlink" href="ms-settings:appsfeatures" target="_blank" rel="noopener noreferrer">{t("apps.store")}</a></footer>}
    </div>
  );
}
