import * as React from "react";
import { Link2, RefreshCw } from "lucide-react";
import { Cleaner, type Skill, type SkillDeployment } from "../../mygo";
import { currentLang } from "../../i18n";
import { isWindows, shortHome } from "../../platform";
import { errText } from "../../shared";
import { Button } from "../../ui/components/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from "../../ui/components/dialog";
import { useT } from "../../ui/i18n-react";

const labels = {
  zh: {
    open: "工具分发", description: "查看默认的全局技能目录，选择一份本地技能部署或修复链接。",
    source: "来源副本", choose: "选择一份来源副本", mode: "部署方式", link: "软链接（随来源更新）", copy: "复制（独立副本）",
    deploy: "部署", repair: "修复链接", shared: "通过共享目录可用", direct: "通过工具目录可用", absent: "未在已知全局目录找到", unknown: "工具发现规则或目录状态未知",
    directory: "独立目录", linked: "软链接", missing: "无条目", broken: "失效软链接", conflict: "同名条目待检查", unreadable: "无法读取",
    local: "默认全局目录", noSources: "没有可部署的本地副本。", done: "已更新目标工具的技能条目。", stillShared: "工具还可通过共享路径发现这项技能。",
    changed: "目标已变化，请重新查看。", exists: "目标已有同名条目，已保留原内容。", unsafeSource: "来源或目标不是可写入的已知技能位置。",
  },
  en: {
    open: "Tool distribution", description: "Inspect default global skill locations and choose a local copy to deploy or repair a link.",
    source: "Source copy", choose: "Choose a source copy", mode: "Deployment method", link: "Symlink (follows the source)", copy: "Copy (independent folder)",
    deploy: "Deploy", repair: "Repair link", shared: "Available via a shared directory", direct: "Available via the tool directory", absent: "Missing from known global locations", unknown: "Discovery rules or directory status unknown",
    directory: "Independent folder", linked: "Symlink", missing: "No entry", broken: "Broken link", conflict: "Same-name entry needs review", unreadable: "Unreadable",
    local: "Default global directory", noSources: "There is no local copy eligible for deployment.", done: "The destination skill entry is ready.", stillShared: "This tool can also discover the skill through a shared path.",
    changed: "The destination changed. Inspect it again.", exists: "The destination already has an entry with this name. Its contents were kept.", unsafeSource: "The source or destination is outside the supported skill locations.",
  },
} as const;

type DistributionLabels = (typeof labels)[keyof typeof labels];

function deploymentState(row: SkillDeployment, l: DistributionLabels): string {
  switch (row.state) {
    case "directory": return l.directory;
    case "link": return l.linked;
    case "broken": return l.broken;
    case "conflict": return l.conflict;
    case "unreadable": return l.unreadable;
    default: return l.missing;
  }
}

function deploymentAvailability(row: SkillDeployment, l: DistributionLabels): string {
  switch (row.availability) {
    case "direct": return row.shared ? l.stillShared : l.direct;
    case "shared": return l.shared;
    case "absent": return l.absent;
    default: return l.unknown;
  }
}

function deploymentError(error: unknown, l: DistributionLabels): string {
  const message = errText(error);
  if (message === "diverged") return l.changed;
  if (message === "not-a-skill") return l.unsafeSource;
  if (message === "file already exists") return l.exists;
  return message;
}

export function SkillDistribution({ skill, disabled, onChanged }: {
  skill: Skill;
  disabled?: boolean;
  onChanged: () => void | Promise<void>;
}) {
  const t = useT();
  const l = labels[currentLang() === "zh" ? "zh" : "en"];
  const [open, setOpen] = React.useState(false);
  const [rows, setRows] = React.useState<SkillDeployment[] | null>(null);
  const [source, setSource] = React.useState("");
  const [mode, setMode] = React.useState<"link" | "copy">(isWindows() ? "copy" : "link");
  const [loading, setLoading] = React.useState(false);
  const [working, setWorking] = React.useState(false);
  const [error, setError] = React.useState("");
  const [notice, setNotice] = React.useState("");
  const changing = React.useRef(false);
  const actionBusy = disabled || loading || working;
  const sources = rows?.filter((row) => row.source) ?? [];

  const refresh = async (resetMode = false) => {
    setError("");
    setLoading(true);
    try {
      const next = await Cleaner.listSkillDistribution(skill.name);
      const candidates = next.filter((row) => row.source);
      const preferred = candidates.find((row) => row.tool === "agents") ?? (candidates.length === 1 ? candidates[0] : undefined);
      setRows(next);
      setSource((current) => candidates.some((row) => row.source === current) ? current : preferred?.source ?? "");
      if (resetMode) setMode(isWindows() ? "copy" : "link");
    } catch (issue) {
      setRows(null);
      setError(deploymentError(issue, l));
    } finally {
      setLoading(false);
    }
  };

  const apply = async (row: SkillDeployment) => {
    if (actionBusy || changing.current || !source) return;
    changing.current = true;
    setWorking(true);
    setError("");
    setNotice("");
    try {
      const repair = row.canRepair;
      await Cleaner.deploySkill({ name: skill.name, source, tool: row.tool, mode: repair ? "link" : mode, repair, previousTarget: row.linkTarget });
      setNotice(l.done);
      await refresh();
      await onChanged();
    } catch (issue) {
      setError(deploymentError(issue, l));
    } finally {
      changing.current = false;
      setWorking(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(value) => { if (!changing.current) setOpen(value); }}>
      <Button variant="ghost" size="sm" disabled={disabled} onClick={() => {
        setOpen(true);
        setRows(null);
        setNotice("");
        void refresh(true);
      }}><Link2 className="h-3.5 w-3.5" />{l.open}</Button>
      <DialogContent className="max-w-[650px]">
        <DialogHeader>
          <DialogTitle>{l.open} · {skill.name}</DialogTitle>
          <DialogDescription>{l.description}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3" aria-busy={loading || working}>
          {error && <div role="alert" className="whitespace-pre-wrap break-words text-sm text-danger-text">{error}</div>}
          {notice && <div role="status" className="text-sm text-muted">{notice}</div>}
          {loading && <p role="status" className="text-sm text-muted">{t("skills.updates.checking")}</p>}
          {rows && (
            <>
              {sources.length > 0 ? (
                <div className="grid gap-2 sm:grid-cols-2">
                  <label className="flex min-w-0 flex-col gap-1 text-xs text-muted">
                    {l.source}
                    <select className="min-w-0 rounded-md border border-border bg-card px-2 py-1 text-foreground" disabled={actionBusy} value={source} onChange={(e) => setSource(e.target.value)}>
                      <option value="">{l.choose}</option>
                      {sources.map((row) => <option key={row.tool} value={row.source}>{row.label} · {shortHome(row.source)}</option>)}
                    </select>
                  </label>
                  <label className="flex flex-col gap-1 text-xs text-muted">
                    {l.mode}
                    <select className="rounded-md border border-border bg-card px-2 py-1 text-foreground" disabled={actionBusy} value={mode}
                      onChange={(e) => setMode(e.target.value === "copy" ? "copy" : "link")}>
                      <option value="link">{l.link}</option>
                      <option value="copy">{l.copy}</option>
                    </select>
                  </label>
                </div>
              ) : <div className="text-sm text-muted">{l.noSources}</div>}
              <ul className="divide-y divide-border text-xs" aria-label={l.local}>
                {rows.map((row) => (
                  <li key={row.tool} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-x-2 font-semibold">
                        {row.label}
                        <span className="font-normal text-muted">{deploymentState(row, l)}</span>
                      </div>
                      <div className="break-all text-muted" title={row.path}>{shortHome(row.path)}</div>
                      {row.linkTarget && row.state === "broken" && (
                        <div className="break-all text-muted" title={row.linkTarget}>→ {shortHome(row.linkTarget)}</div>
                      )}
                      {row.tool !== "agents" && (
                        <div className="text-muted" title={row.sharedPath || undefined}>{deploymentAvailability(row, l)}</div>
                      )}
                    </div>
                    {(row.canRepair || row.canDeploy) && <Button variant="ghost" size="sm" disabled={actionBusy || !source}
                      onClick={() => void apply(row)}>{row.canRepair ? l.repair : l.deploy}</Button>}
                  </li>
                ))}
              </ul>
            </>
          )}
          <div className="flex items-center justify-end gap-2">
            <Button variant="ghost" size="sm" disabled={actionBusy} onClick={() => void refresh()}>
              <RefreshCw className="h-3.5 w-3.5" />{t("ai.run.refresh")}
            </Button>
            <Button variant="ghost" size="sm" disabled={working} onClick={() => setOpen(false)}>{t("btn.close")}</Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
