import * as React from "react";
import { History, ListChecks } from "lucide-react";
import { Cleaner, type SkillUpgradePreview as Preview, type SkillVersion } from "../../mygo";
import { currentLang } from "../../i18n";
import { errText } from "../../shared";
import { Button } from "../../ui/components/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "../../ui/components/dialog";

const labels = {
  zh: {
    history: "版本历史", description: "恢复 Cleanzo 保存的版本。已有目录会先保存在历史里。", empty: "暂无可恢复的版本。", loading: "正在读取…", cancel: "取消", close: "关闭", restore: "恢复", restoring: "正在恢复…", replaced: "恢复并替换", restored: "已恢复", upgrade: "升级前", remove: "移除前", rollback: "恢复前", confirm: "确认恢复", fileChanges: "预览文件变化", restoreDescription: "恢复到原始路径。", replaceDescription: "恢复到原始路径，当前文件夹会保存到版本历史。", none: "没有文件变化。", omitted: (n: number) => `另有 ${n} 个文件未显示。`, added: "新增", changed: "修改", removed: "删除",
  },
  en: {
    history: "Version History", description: "Restore a version saved by Cleanzo. Existing folders are saved in history first.", empty: "No versions to restore yet.", loading: "Loading…", cancel: "Cancel", close: "Close", restore: "Restore", restoring: "Restoring…", replaced: "Restore and Replace", restored: "Version restored", upgrade: "Before upgrade", remove: "Before removal", rollback: "Before restore", confirm: "Confirm Restore", fileChanges: "Preview File Changes", restoreDescription: "Restore to the original path.", replaceDescription: "Restore to the original path. The current folder will be saved in version history.", none: "No file changes.", omitted: (n: number) => `${n} more file changes.`, added: "Added", changed: "Changed", removed: "Removed",
  },
};

export function SkillHistory({ disabled, path = "", onChanged }: { disabled?: boolean; path?: string; onChanged: () => void | Promise<void> }) {
  const t = labels[currentLang()];
  const [open, setOpen] = React.useState(false);
  const [entries, setEntries] = React.useState<SkillVersion[] | null>(null);
  const [selected, setSelected] = React.useState<SkillVersion | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  const [notice, setNotice] = React.useState("");
  const request = React.useRef(0);

  const load = React.useCallback(async () => {
    const token = ++request.current;
    setEntries(null);
    setError("");
    try {
      const versions = await Cleaner.listSkillHistory(path);
      if (token === request.current) setEntries(versions);
    } catch (e) {
      if (token === request.current) setError(errText(e));
    }
  }, [path]);

  React.useEffect(() => {
    if (!open) return;
    void load();
    return () => { request.current++; };
  }, [load, open]);

  async function restore(version: SkillVersion) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await Cleaner.restoreSkillVersion(version.id, version.exists);
      setSelected(null);
      setNotice(t.restored);
      await load();
      await onChanged();
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  }

  return <>
    <Button variant="ghost" size="sm" disabled={disabled} onClick={() => { setNotice(""); setSelected(null); setOpen(true); }}>
      <History className="h-3.5 w-3.5" />{t.history}
    </Button>
    <Dialog open={open} onOpenChange={(next) => { if (!busy) setOpen(next); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{selected ? t.confirm : t.history}</DialogTitle>
          <DialogDescription>{selected ? selected.exists ? t.replaceDescription : t.restoreDescription : t.description}</DialogDescription>
        </DialogHeader>
        {error && <p role="alert" className="break-words text-sm text-danger-text">{error}</p>}
        {notice && <p role="status" className="text-sm">{notice}</p>}
        {selected ? <div className="space-y-3">
          <p className="break-all text-sm" translate="no">{selected.path}</p>
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => setSelected(null)}>{t.cancel}</Button>
            <Button size="sm" disabled={busy} onClick={() => void restore(selected)}>{busy ? t.restoring : selected.exists ? t.replaced : t.restore}</Button>
          </div>
        </div> : <>
          {!entries && !error && <p role="status" className="text-sm text-muted">{t.loading}</p>}
          {entries?.length === 0 && <p className="text-sm text-muted">{t.empty}</p>}
          {!!entries?.length && <ul className="max-h-[48vh] space-y-2 overflow-y-auto pr-1">
            {entries.map((entry) => <li key={entry.id} className="flex items-center gap-2 rounded border border-border px-2 py-1.5 text-sm">
              <div className="min-w-0 flex-1">
                <div><strong translate="no">{entry.name}</strong> · {entry.action === "upgrade" ? t.upgrade : entry.action === "remove" ? t.remove : t.rollback}</div>
                <p className="break-all text-xs text-muted" translate="no">{entry.path}</p>
                <time className="text-xs text-muted" dateTime={new Date(entry.at).toISOString()}>{new Date(entry.at).toLocaleString()}</time>
              </div>
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => { setError(""); setSelected(entry); }}>{t.restore}</Button>
            </li>)}
          </ul>}
          <div className="flex justify-end"><Button size="sm" variant="ghost" disabled={busy} onClick={() => setOpen(false)}>{t.close}</Button></div>
        </>}
      </DialogContent>
    </Dialog>
  </>;
}

export function SkillUpgradeChanges({ path }: { path: string }) {
  const t = labels[currentLang()];
  const [pending, setPending] = React.useState(false);
  const [preview, setPreview] = React.useState<Preview | null>(null);
  const [error, setError] = React.useState("");
  const load = async () => {
    setPending(true);
    setError("");
    try { setPreview(await Cleaner.previewSkillUpgrade(path)); }
    catch (e) { setError(errText(e)); }
    finally { setPending(false); }
  };
  return <div className="space-y-1.5">
    <Button size="sm" variant="ghost" disabled={pending} onClick={() => void load()}><ListChecks className="h-3.5 w-3.5" />{pending ? t.loading : t.fileChanges}</Button>
    {error && <p role="alert" className="break-words text-xs text-danger-text">{error}</p>}
    {preview && (preview.total === 0 ? <p className="text-xs text-muted">{t.none}</p> : <>
      <ul aria-label={t.fileChanges} className="max-h-[22vh] space-y-1 overflow-y-auto text-xs">
        {preview.changes.map(({ path: file, kind }) => <li key={file} className="break-all"><span className="font-semibold">{kind === "added" ? t.added : kind === "removed" ? t.removed : t.changed}</span> · <span translate="no">{file}</span></li>)}
      </ul>
      {preview.total > preview.changes.length && <p className="text-xs text-muted">{t.omitted(preview.total - preview.changes.length)}</p>}
    </>)}
  </div>;
}
