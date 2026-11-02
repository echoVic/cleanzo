import * as React from "react";
import { ChevronRight } from "lucide-react";
import type { AppInfo, AppUpdate } from "../../mygo";
import { isWindows } from "../../platform";
import { fmtDate, human } from "../../shared";
import { t } from "../../i18n";
import { Button } from "../../ui/components/button";
import { useLeaveAnimation } from "../../ui/motion/presence";
import { AppIcon } from "../icons/AppIcon";
import type { GrowthTag } from "../process/format";
import { versionTag } from "./updates";

export interface AppRowProps {
  app: AppInfo;
  update: AppUpdate | undefined;
  growthTag: GrowthTag | null;
  open: boolean;
  /** 正在跑的动作：有就禁用这一行的按钮 */
  busy: "upgrade" | "cache" | "quit" | "uninstall" | null;
  /** 别的 brew 行正在升级：本行的 brew「更新」禁用 */
  brewBusy: boolean;
  onToggle: (id: string) => void;
  onUpgrade: (id: string) => void;
  children?: React.ReactNode;
}

/** 增长小标签文字，和进程页一样：「磁盘 7 天 +3.0 GB」。 */
export function growthTagText(tag: GrowthTag): string {
  const size = `+${human(tag.bytes)}`;
  return t(tag.window === "day" ? "process.tag.day" : "process.tag.week", { size: tag.atLeast ? t("growth.atLeast", { size }) : size });
}

/**
 * 应用页的一行：点行展开；「更新」是行旁边的独立按钮，不展开也能点。
 * 和旧的 InstalledAppRow 一样是纯函数、用全局 t（父组件用 useT 订阅语言变化），测试可以直接调用。
 */
export function AppRow({ app: a, update, growthTag, open, busy, brewBusy, onToggle, onUpgrade, children }: AppRowProps) {
  const win = isWindows();
  const ver = win ? null : versionTag(update);
  const tags: React.ReactNode[] = [];
  if (ver) tags.push(<span key="ver" className="tag ok">{ver}</span>);
  if (a.running) tags.push(<span key="run" className="tag ok">{t("apps.running")}</span>);
  if (!win && growthTag && tags.length < 2) tags.push(<span key="grow" className="tag grow">{growthTagText(growthTag)}</span>);
  const canUpgrade = !win && update?.state === "update" && update.action !== "";
  const upgradeBlocked = busy !== null || (update?.action === "brew" && (brewBusy || a.running));
  return (
    <div className="app-item card" data-key={a.id}>
      <div className="app-head">
        <button type="button" className="app-row" aria-expanded={open} onClick={() => onToggle(a.id)}>
          <AppIcon path={a.path} box="app-icon">
            <div className="app-icon">{a.name.charAt(0).toUpperCase()}</div>
          </AppIcon>
          <div className="app-main">
            <div className="app-name">
              {a.name}
              {tags.slice(0, 2)}
              {!ver && a.version && <span className="ver muted"> {a.version}</span>}
            </div>
            <div className="app-path muted" title={win ? a.publisher : a.path}>{win ? a.publisher : a.path}</div>
          </div>
          <div className="app-meta">
            <div className="app-size">{human(a.size)}</div>
            {(!win || a.lastOpened !== 0) && <div className="app-used muted small">{a.lastOpened ? fmtDate(a.lastOpened) : t("apps.never")}</div>}
          </div>
          <ChevronRight className="app-chev h-4 w-4" aria-hidden="true" />
        </button>
        {canUpgrade && (
          <Button size="sm" className="app-upgrade" disabled={upgradeBlocked} onClick={() => onUpgrade(a.id)}>
            {busy === "upgrade" ? t("apps.updating") : t("apps.update")}
          </Button>
        )}
      </div>
      {children}
    </div>
  );
}

/** 带离场动画的行（列表里用这个）；动效 hook 单独挂载，AppRow 本身仍可直接调用。 */
export function AnimatedAppRow(props: AppRowProps & { leaving?: boolean }) {
  const ref = React.useRef<HTMLDivElement>(null);
  useLeaveAnimation(ref, !!props.leaving);
  return React.cloneElement(AppRow(props), { ref, "aria-hidden": props.leaving || undefined, inert: props.leaving || undefined } as never);
}
