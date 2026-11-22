import * as React from "react";
import { FolderOpen, Plus, RefreshCw } from "lucide-react";
import { Cleaner, type MCPChange, type MCPConfig, type MCPServer } from "../../mygo";
import { Button } from "../../ui/components/button";
import { Card } from "../../ui/components/card";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "../../ui/components/dialog";
import { shortHome } from "../../platform";
import { errText } from "../../shared";
import { useT } from "../../ui/i18n-react";

export const mcpApi = {
  list: () => Cleaner.listMCPConfigs(),
  apply: (change: MCPChange) => Cleaner.applyMCPChange(change),
  reveal: (path: string) => Cleaner.revealInFinder(path),
};

const clients: Record<string, string> = {
  claude: "Claude Code", codex: "Codex", gemini: "Gemini CLI", opencode: "OpenCode",
  "opencode-json": "OpenCode (.json)", "opencode-jsonc": "OpenCode (.jsonc)",
};
const clientName = (id: string) => clients[id] ?? id;
const fieldClass = "w-full rounded-md border border-border bg-card px-3 py-2 text-sm focus-visible:outline-2 focus-visible:outline-primary";

type Draft = {
  config: MCPConfig;
  action: "add" | "edit" | "remove" | "restore";
  name: string;
  transport: string;
  endpoint: string;
  argsText: string;
  replaceArgs: boolean;
};

const errorKeys = {
  "backup-unavailable": "mcp.err.backup",
  "config-changed": "mcp.err.stale",
  "config-too-large": "mcp.err.size",
  "credential-destination": "mcp.err.credentials",
  "invalid-arguments": "mcp.err.args",
  "invalid-config": "mcp.err.config",
  "invalid-endpoint": "mcp.err.endpoint",
  "invalid-name": "mcp.err.name",
  "invalid-server-list": "mcp.err.config",
  "invalid-value": "mcp.err.config",
  "linked-config": "mcp.linked",
  "name-exists": "mcp.err.exists",
  "read-config": "mcp.err.read",
  "server-missing": "mcp.err.stale",
  "unsupported-change": "mcp.err.layout",
  "unsupported-file": "mcp.err.layout",
  "unsupported-layout": "mcp.err.layout",
  "write-config": "mcp.err.write",
} as const;

export function MCPPanel() {
  const t = useT();
  const [configs, setConfigs] = React.useState<MCPConfig[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [busy, setBusy] = React.useState(false);
  const [issue, setIssue] = React.useState("");
  const [dialogError, setDialogError] = React.useState("");
  const [draft, setDraft] = React.useState<Draft | null>(null);
  const activeRequest = React.useRef(0);
  const writing = React.useRef(false);
  const errorLabel = (value: string) => errorKeys[value as keyof typeof errorKeys] ? t(errorKeys[value as keyof typeof errorKeys]) : value;

  const reload = React.useCallback(async () => {
    const seq = ++activeRequest.current;
    setLoading(true);
    try {
      const rows = await mcpApi.list();
      if (seq === activeRequest.current) setConfigs(rows);
    } catch (e) {
      if (seq === activeRequest.current) setIssue(errText(e));
    } finally {
      if (seq === activeRequest.current) setLoading(false);
    }
  }, []);
  React.useEffect(() => {
    void reload();
    return () => { activeRequest.current++; };
  }, [reload]);

  const open = (config: MCPConfig, action: Draft["action"], server?: MCPServer) => {
    setDialogError("");
    setDraft({ config, action, name: server?.name ?? "", transport: server?.transport ?? "stdio", endpoint: "", argsText: "", replaceArgs: false });
  };
  const change = async (config: MCPConfig, action: string, payload: Partial<MCPChange> = {}, close = false) => {
    if (writing.current) return;
    writing.current = true;
    setBusy(true);
    setIssue("");
    setDialogError("");
    try {
      await mcpApi.apply({ tool: config.tool, revision: config.revision, action, name: "", transport: "", endpoint: "", args: [], replaceArgs: false, enabled: false, ...payload });
      if (close) setDraft(null);
      await reload();
    } catch (e) {
      const msg = errText(e);
      if (close && msg !== "config-changed") setDialogError(msg);
      else {
        if (close) setDraft(null);
        setIssue(msg);
        if (msg === "config-changed") await reload();
      }
    } finally {
      writing.current = false;
      setBusy(false);
    }
  };
  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!draft) return;
    const args = draft.argsText === "" ? [] : draft.argsText.replace(/\r\n/g, "\n").split("\n");
    void change(draft.config, draft.action, { name: draft.name, transport: draft.transport, endpoint: draft.endpoint, args, replaceArgs: draft.replaceArgs || draft.action === "add" }, true);
  };
  const editable = draft?.action === "add" || draft?.action === "edit";
  const changeDraft = (next: Partial<Draft>) => setDraft((prev) => prev ? { ...prev, ...next } : null);
  const locked = busy || loading;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-sm text-muted">{t("mcp.scope")}</p>
        <Button variant="ghost" size="sm" disabled={locked} onClick={() => { setIssue(""); void reload(); }}>
          <RefreshCw className={loading ? "h-4 w-4 animate-spin" : "h-4 w-4"} />{t(loading ? "scan.busy" : "fs.rescan")}
        </Button>
      </div>
      {issue && <div role="alert" className="rounded-md border border-border p-3 text-sm text-danger-text">{errorLabel(issue)}</div>}
      {configs.map((config) => {
        const readonly = config.readOnly || config.error !== "";
        return (
          <Card key={config.tool} className="overflow-hidden">
            <div className="flex flex-wrap items-start justify-between gap-3 px-4 py-3">
              <div className="min-w-0 grow">
                <h2 className="font-semibold">{clientName(config.tool)}</h2>
                <p className="break-all text-xs text-muted" title={config.path}>{shortHome(config.path)}</p>
                {config.error && <p className="mt-1 text-xs text-danger-text">{errorLabel(config.error)}</p>}
                {config.readOnly && <p className="mt-1 text-xs text-muted">{t("mcp.linked")}</p>}
              </div>
              <div className="flex flex-wrap items-center gap-1">
                {config.exists && <Button variant="ghost" size="sm" onClick={() => { void mcpApi.reveal(config.path).catch((e) => setIssue(errText(e))); }}><FolderOpen className="h-4 w-4" />{t("mcp.reveal")}</Button>}
                {config.hasBackup && <Button variant="ghost" size="sm" disabled={locked || config.readOnly} onClick={() => open(config, "restore")}>{t("mcp.restore")}</Button>}
                <Button variant="ghost" size="sm" disabled={locked || readonly} onClick={() => open(config, "add")}><Plus className="h-4 w-4" />{t("mcp.add")}</Button>
              </div>
            </div>
            {config.servers.length === 0 && !config.error && <p className="border-t border-border px-4 py-3 text-sm text-muted">{t("mcp.empty")}</p>}
            {config.servers.map((server) => (
              <div key={server.name} className="flex flex-wrap items-center justify-between gap-3 border-t border-border px-4 py-3">
                <div className="min-w-0 grow">
                  <div className="break-all text-sm font-medium">{server.name}</div>
                  <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 break-all text-xs text-muted">
                    <span>{server.transport === "stdio" ? "stdio" : server.transport === "http" ? "HTTP" : t("mcp.unknown")}{server.endpoint ? ` · ${server.endpoint}` : ""}</span>
                    {config.canToggle && <span>{t(server.enabled ? "mcp.enabled" : "mcp.disabled")}</span>}
                    {server.issue === "missing-command" && <span className="text-warning-text">{t("mcp.missingCommand")}</span>}
                    {server.issue === "invalid-url" && <span className="text-warning-text">{t("mcp.err.endpoint")}</span>}
                    {server.alsoIn.length > 0 && <span>{t("mcp.alsoIn", { tools: server.alsoIn.map(clientName).join("、") })}</span>}
                  </div>
                </div>
                <div className="flex flex-wrap gap-1">
                  {config.canToggle && <Button variant="ghost" size="sm" disabled={locked || readonly} onClick={() => { void change(config, "toggle", { name: server.name, enabled: !server.enabled }); }}>{t(server.enabled ? "mcp.disable" : "mcp.enable")}</Button>}
                  <Button variant="ghost" size="sm" disabled={locked || readonly || server.transport === "unknown"} onClick={() => open(config, "edit", server)}>{t("mcp.edit")}</Button>
                  <Button variant="ghost" size="sm" disabled={locked || readonly} onClick={() => open(config, "remove", server)}>{t("mcp.remove")}</Button>
                </div>
              </div>
            ))}
          </Card>
        );
      })}
      <Dialog open={draft !== null} onOpenChange={(next) => { if (!next && !writing.current) setDraft(null); }}>
        {draft && <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(`mcp.${draft.action}`)} · {clientName(draft.config.tool)}</DialogTitle>
            <DialogDescription>{draft.action === "restore" ? t("mcp.restoreHelp") : draft.action === "remove" ? t("mcp.removeHelp", { name: draft.name }) : t("mcp.editHelp")}</DialogDescription>
          </DialogHeader>
          <form className="space-y-3" onSubmit={submit}>
            {editable && <>
              <label className="block text-sm">{t("mcp.name")}<input className={fieldClass} value={draft.name} required readOnly={draft.action === "edit"} maxLength={80} onChange={(e) => changeDraft({ name: e.target.value })} /></label>
              <label className="block text-sm">{t("mcp.transport")}<select className={fieldClass} value={draft.transport} disabled={draft.action === "edit"} onChange={(e) => changeDraft({ transport: e.target.value })}><option value="stdio">stdio</option><option value="http">HTTP</option></select></label>
              <label className="block text-sm">{t(draft.transport === "stdio" ? "mcp.command" : "mcp.address")}<input className={fieldClass} value={draft.endpoint} required={draft.action === "add"} autoComplete="off" placeholder={draft.action === "edit" ? t("mcp.keepField") : ""} onChange={(e) => changeDraft({ endpoint: e.target.value })} /></label>
              {draft.transport === "stdio" && <>
                {draft.action === "edit" && <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={draft.replaceArgs} onChange={(e) => changeDraft({ replaceArgs: e.target.checked })} />{t("mcp.replaceArgs")}</label>}
                <label className="block text-sm">{t("mcp.args")}<textarea className={fieldClass} rows={3} disabled={draft.action === "edit" && !draft.replaceArgs} value={draft.argsText} onChange={(e) => changeDraft({ argsText: e.target.value })} /></label>
              </>}
            </>}
            {dialogError && <p role="alert" className="text-sm text-danger-text">{errorLabel(dialogError)}</p>}
            <div className="flex justify-end gap-2">
              <Button type="button" variant="ghost" disabled={busy} onClick={() => setDraft(null)}>{t("btn.cancel")}</Button>
              <Button type="submit" disabled={busy}>{t(editable ? "mcp.save" : `mcp.${draft.action}`)}</Button>
            </div>
          </form>
        </DialogContent>}
      </Dialog>
    </div>
  );
}
