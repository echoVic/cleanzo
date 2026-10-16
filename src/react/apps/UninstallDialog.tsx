import { Loader2 } from "lucide-react";
import type { UninstallDetail } from "../../mygo";
import { Button } from "../../ui/components/button";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogClose,
} from "../../ui/components/dialog";
import { useT } from "../../ui/i18n-react";
import { kindLabel } from "../../i18n";
import { human, displayPath, errText } from "../../shared";

export type UninstallStage = "analyzing" | "detail" | "fail";

interface UninstallDialogProps {
  open: boolean;
  stage: UninstallStage;
  detail: UninstallDetail | null;
  failMsg: string | null;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}

/**
 * 卸载详情弹层：分析中 → 详情（或分析失败）。正在运行的应用只给
 * 「关闭」不提供卸载确认；异步晚到结果由调用方按 seq 丢弃。
 */
export function UninstallDialog({
  open, stage, detail, failMsg, onOpenChange, onConfirm,
}: UninstallDialogProps) {
  const t = useT();
  const running = detail?.app.running ?? false;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
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
            <div className="err-inline">{t("apps.analyzeFail", { msg: failMsg ?? "" })}</div>
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
            <div className="detail-list">
              {detail.items.map((it) => (
                <div className="detail-item" key={it.path}>
                  <span className="kind">{kindLabel(it.kind)}</span>
                  <span className="path" title={it.path}>{displayPath(it.path)}</span>
                  <span className="size">{human(it.size)}</span>
                </div>
              ))}
            </div>
            <p className="warn-text">
              {running
                ? t("apps.inUse", { name: detail.app.name })
                : t("apps.body", { n: detail.items.length, size: human(detail.totalSize) })}
            </p>
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
      </DialogContent>
    </Dialog>
  );
}
