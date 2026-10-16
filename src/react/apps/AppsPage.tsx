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
}

export function AppsPage({ initialTab, nonce }: AppsPageProps) {
  const t = useT();
  const [tab, setTab] = React.useState<AppsTab>(initialTab);
  const [orphanCount, setOrphanCount] = React.useState(0);
  const [installedBusy, setInstalledBusy] = React.useState(false);
  const [orphanBusy, setOrphanBusy] = React.useState(false);

  // 外部桥接（垃圾页「残留」）请求切到残留 tab。
  const firstNonce = React.useRef(nonce);
  React.useEffect(() => {
    if (nonce !== firstNonce.current) {
      firstNonce.current = nonce;
      setTab(initialTab);
    }
  }, [nonce, initialTab]);

  // 应用与残留的后台任务共用导航 busy 提示。
  React.useEffect(() => {
    setNavBusy("apps", installedBusy || orphanBusy);
  }, [installedBusy, orphanBusy]);

  return (
    <div className="cz-react px-1">
      <header className="view-head">
        <div>
          <h2>{t("nav.apps")}</h2>
          <p className="muted">{t("apps.sub")}</p>
        </div>
      </header>

      <Tabs value={tab} onValueChange={(v) => setTab(v as AppsTab)}>
        <TabsList>
          <TabsTrigger value="installed">{t("apps.tab.installed")}</TabsTrigger>
          <TabsTrigger value="leftovers">
            {t("apps.tab.leftoversCount", { n: orphanCount })}
          </TabsTrigger>
        </TabsList>
        {/* 两个面板都常驻挂载，切 tab 不卸载、不重扫（与旧显隐行为一致）。 */}
        <TabsContent value="installed" forceMount hidden={tab !== "installed" || undefined}>
          <InstalledTab active={tab === "installed"} onBusyChange={setInstalledBusy} />
        </TabsContent>
        <TabsContent value="leftovers" forceMount hidden={tab !== "leftovers" || undefined}>
          <LeftoversTab
            active={tab === "leftovers"}
            onCountChange={setOrphanCount}
            onBusyChange={setOrphanBusy}
          />
        </TabsContent>
      </Tabs>
    </div>
  );
}
