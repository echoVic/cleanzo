import { isWindows } from "../../platform";
import * as React from "react";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "../../ui/components/tabs";
import { useT } from "../../ui/i18n-react";
import { setNavBusy } from "../../shared";
import { InstalledTab } from "./InstalledTab";
import { LeftoversTab } from "./LeftoversTab";

export type AppsTab = "installed" | "leftovers";

interface AppsPageProps {
  initialTab: AppsTab;
  /** 外部桥接请求切 tab 的版本号（垃圾页「残留」动作）。 */
  nonce: number;
  /** 进程页的磁盘占用标签请求打开卸载对话框的 App。 */
  uninstallPath?: string | null;
  /** 外部卸载请求的版本号。 */
  uninstallNonce?: number;
}

export function AppsPage({ initialTab, nonce, uninstallPath = null, uninstallNonce = 0 }: AppsPageProps) {
  const t = useT();
  const requestedTab = isWindows() ? "installed" : initialTab;
  const [tab, setTab] = React.useState<AppsTab>(requestedTab);
  // null = 还没扫过：tab 只显示「残留」，不用 0 误导用户以为没有残留。
  const [orphanCount, setOrphanCount] = React.useState<number | null>(null);
  const [installedBusy, setInstalledBusy] = React.useState(false);
  const [orphanBusy, setOrphanBusy] = React.useState(false);

  // 外部桥接（垃圾页「残留」）请求切到残留 tab。
  const firstNonce = React.useRef(nonce);
  React.useEffect(() => {
    if (nonce !== firstNonce.current) {
      firstNonce.current = nonce;
      setTab(requestedTab);
    }
  }, [nonce, requestedTab]);

  // 应用与残留的后台任务共用导航 busy 提示。
  React.useEffect(() => {
    setNavBusy("apps", installedBusy || orphanBusy);
  }, [installedBusy, orphanBusy]);

  return (
    <div className="cz-react">
      <header className="view-head">
        <div>
          <h1>{t("nav.apps")}</h1>
          <p className="muted">{t("apps.sub")}</p>
        </div>
      </header>

      <Tabs value={tab} onValueChange={(v) => setTab(v as AppsTab)}>
        <TabsList>
          <TabsTrigger value="installed">{t("apps.tab.installed")}</TabsTrigger>
          {!isWindows() && <TabsTrigger value="leftovers">
            {orphanCount === null
              ? t("apps.tab.leftovers")
              : t("apps.tab.leftoversCount", { n: orphanCount })}
          </TabsTrigger>}
        </TabsList>
        {/* 两个面板都常驻挂载，切 tab 不卸载、不重扫（与旧显隐行为一致）。 */}
        <TabsContent value="installed" forceMount hidden={tab !== "installed" || undefined}>
          <InstalledTab active={tab === "installed"} onBusyChange={setInstalledBusy} uninstallPath={uninstallPath} uninstallNonce={uninstallNonce} />
        </TabsContent>
        {!isWindows() && <TabsContent value="leftovers" forceMount hidden={tab !== "leftovers" || undefined}>
          <LeftoversTab
            active={tab === "leftovers"}
            onCountChange={setOrphanCount}
            onBusyChange={setOrphanBusy}
          />
        </TabsContent>}
      </Tabs>
      {isWindows() && (
        <footer className="muted small mt-4">
          <a className="mlink" href="ms-settings:appsfeatures" target="_blank" rel="noopener noreferrer">{t("apps.store")}</a>
        </footer>
      )}
    </div>
  );
}
