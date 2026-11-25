import { isWindows } from "../../platform";
import { Loader2 } from "lucide-react";
import type { UninstallDetail } from "../../mygo";
import { Button } from "../../ui/components/button";
import { Checkbox } from "../../ui/components/checkbox";
import { PathText } from "../../ui/components/path-text";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogClose,
} from "../../ui/components/dialog";
import { useT } from "../../ui/i18n-react";
import { kindLabel } from "../../i18n";
import { human, displayPath } from "../../shared";

export type UninstallStage = "analyzing" | "detail" | "fail" | "launched";

export function uninstallGroups(items: UninstallDetail["items"]) {
  const groupOf = (kind: string) => kind === "app" ? "app"
    : ["cache", "logs"].includes(kind) ? "cache"
    : ["prefs", "savedState", "launchAgent"].includes(kind) ? "settings" : "data";
  return (["app", "cache", "settings", "data"] as const).map((key) => {
    const members = items.filter((it) => groupOf(it.kind) === key);
    return { key, items: members, bytes: members.reduce((sum, it) => sum + it.size, 0) };
  }).filter((group) => group.items.length > 0);
}

interface UninstallDialogProps {
  open: boolean;
  stage: UninstallStage;
  detail: UninstallDetail | null;
  failMsg: string | null;
  removeData: boolean;
  onRemoveDataChange: (removeData: boolean) => void;
  ejectInstaller: boolean;
  onEjectInstallerChange: (eject: boolean) => void;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
  onRefresh?: () => void;
}

/**
 * 卸载详情弹层：分析中 → 详情（或分析失败）。正在运行的应用只给
 * 「关闭」不提供卸载确认；异步晚到结果由调用方按 seq 丢弃。
 */
export function UninstallDialog({
  open, onOpenChange, ...body
}: UninstallDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent animateHeight>
        <div key={body.stage} className="cz-fade-in">
          <UninstallDialogBody {...body} />
        </div>
      </DialogContent>
    </Dialog>
  );
}

// 弹层正文复用同一状态，供页面和事件夹具渲染。
export function UninstallDialogBody({ stage, detail, failMsg, removeData, onRemoveDataChange, ejectInstaller, onEjectInstallerChange, onConfirm, onRefresh }:
  Omit<UninstallDialogProps, "open" | "onOpenChange">) {
  const t = useT();
  const running = detail?.app.running ?? false;
  const win = isWindows();
  const items = detail?.items ?? [];
  const hasRelatedFiles = items.some((it) => it.kind !== "app");
  const selectedItems = removeData ? items : items.filter((it) => it.kind === "app");
  const selectedSize = selectedItems.reduce((sum, it) => sum + it.size, 0);
  return (
    <>
      {stage === "launched" && (
        <>
          <DialogHeader>
            <DialogTitle>{t("apps.launched")}</DialogTitle>
          </DialogHeader>
          <div className="mt-4 flex justify-end gap-2">
            <DialogClose asChild><Button variant="ghost">{t("btn.close")}</Button></DialogClose>
            <Button onClick={onRefresh}>{t("apps.refresh")}</Button>
          </div>
        </>
      )}
      {stage === "analyzing" && (
        <>
          <DialogHeader>
            <DialogTitle>{t("apps.analyzing")}</DialogTitle>
            <DialogDescription>{t("apps.analyzingBody")}</DialogDescription>
          </DialogHeader>
          <div className="mt-4 flex justify-end">
            <Button disabled>
              <Loader2 className="h-4 w-4 animate-spin" />
              {t("apps.wait")}
            </Button>
          </div>
        </>
      )}

      {stage === "fail" && (
        <>
          <DialogHeader>
            <DialogTitle>{t("apps.analyzing")}</DialogTitle>
          </DialogHeader>
          <DialogDescription className="err-inline">{t("apps.analyzeFail", { msg: failMsg ?? "" })}</DialogDescription>
          <div className="mt-4 flex justify-end">
            <DialogClose asChild><Button variant="ghost">{t("btn.close")}</Button></DialogClose>
          </div>
        </>
      )}

      {stage === "detail" && detail && (
        <>
          <DialogHeader>
            <DialogTitle>{t("apps.title", { name: detail.app.name })}</DialogTitle>
          </DialogHeader>
          {!win && !running && hasRelatedFiles && (
            <label className="check mt-3 mb-2">
              <Checkbox checked={removeData} onCheckedChange={(value) => onRemoveDataChange(value === true)} />
              <span>{t("apps.removeData")}</span>
            </label>
          )}
          {!win && !running && detail.installerId && detail.installerMount && (
            <label className="check mt-3 mb-2" title={detail.installerMount}>
              <Checkbox checked={ejectInstaller} onCheckedChange={(value) => onEjectInstallerChange(value === true)} />
              <span>{t("apps.ejectInstaller", { name: detail.installerMount.split("/").at(-1) ?? "" })}</span>
            </label>
          )}
          {!win && <div className="detail-list">
            {uninstallGroups(selectedItems).map((group) => <section className="uninstall-review-group" key={group.key}>
              <h3><span>{t(`apps.review.${group.key}`)}</span><span>{human(group.bytes)}</span></h3>
              {group.key === "data" && <p className="muted small">{t("apps.review.dataNote")}</p>}
              {group.items.map((it) => (
              <div className="detail-item" key={it.path}>
                <span className="kind">{kindLabel(it.kind)}</span>
                <span className="path" title={it.path}><PathText path={displayPath(it.path)} /></span>
                <span className="size">{human(it.size)}</span>
              </div>
              ))}
            </section>)}
          </div>}
          <DialogDescription className="warn-text">
            {running
              ? t("apps.inUse", { name: detail.app.name })
              : t("apps.body", { n: selectedItems.length, size: human(selectedSize) })}
          </DialogDescription>
          <div className="mt-4 flex justify-end gap-2">
            {running ? (
              <DialogClose asChild><Button variant="ghost">{t("btn.close")}</Button></DialogClose>
            ) : (
              <>
                <DialogClose asChild><Button variant="ghost">{t("btn.cancel")}</Button></DialogClose>
                <Button variant="destructive" onClick={onConfirm}>{t("apps.confirm")}</Button>
              </>
            )}
          </div>
        </>
      )}
    </>
  );
}
