import * as React from "react";
import { FolderInput, GitFork, PackagePlus, Search } from "lucide-react";
import { currentLang } from "../../i18n";
import {
  Cleaner, type SkillCatalogEntry, type SkillCatalogInstallResult,
  type SkillDiscoveryEntry, type SkillOrigin, type SkillRoot, type SkillUpdate,
} from "../../mygo";
import { errText } from "../../shared";
import { Button } from "../../ui/components/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "../../ui/components/dialog";
import { useT } from "../../ui/i18n-react";

const text = {
  zh: {
    add: "添加技能", bind: "绑定来源", repo: "GitHub 仓库", discover: "发现技能", local: "本地目录", zip: "ZIP 压缩包",
    installDescription: "浏览仓库、搜索 skills.sh，或从本地目录和 ZIP 安装。安装时可以选择一个或多个工具。",
    bindDescription: "选择这个技能的上游目录。绑定只保存来源，现有文件保持原样。",
    repository: "仓库地址", branch: "分支或标签", directory: "仓库内目录", defaultBranch: "默认分支",
    browse: "浏览仓库", query: "搜索 skills.sh", search: "搜索", openRepo: "查看仓库",
    installed: "已安装", sameName: "已有同名项", choose: "选择要安装的技能", chooseSource: "选择上游目录",
    noResults: "未找到技能。", installTo: "安装到", install: "安装选中的技能", import: "导入技能", apply: "绑定所选来源",
    sourceBound: "来源已绑定。", localDiffers: "来源已绑定。本地内容和上游不同，覆盖仍需确认。",
    path: "技能目录", pick: "选择目录", localDescription: "选择含 SKILL.md 的目录，Cleanzo 会将其复制到所选工具。",
    zipPath: "ZIP 文件", pickZIP: "选择 ZIP", zipDescription: "每个 ZIP 应包含一个含 SKILL.md 的技能目录。",
    invalidZIP: "请选择一个 ZIP 文件。", unsafeZIP: "压缩包包含不安全或重复的条目。", zipNoSkill: "压缩包中未找到 SKILL.md。", zipManySkills: "压缩包里有多个技能，请分别打包。",
    access: "共享目录可被多个工具读取。", busy: "处理中…",
    confirm: "安装会将技能文件写入所选目录。请确认来源可信。", copied: "安装完成",
    invalidRepository: "请输入 GitHub 的 owner/repo 或仓库地址。", invalidRef: "分支或标签格式有误。",
    invalidPath: "请输入仓库内的相对目录。", destinationExists: "目标已有同名项。",
    newSource: "上游已变化，请重新浏览仓库并选择。", missing: "找不到选中的技能目录。",
    tooMany: "仓库内技能过多，请指定子目录。", truncated: "GitHub 返回的目录树不完整。",
    gitOwned: "Git 工作区使用自身的远端作为来源。", invalidQuery: "请输入 2～200 字节的搜索词。",
    invalidTarget: "请选择至少一个安装目标。", installSaved: "文件已安装，但没有保存来源。",
  },
  en: {
    add: "Add Skill", bind: "Bind Source", repo: "GitHub Repository", discover: "Discover", local: "Local Folder", zip: "ZIP Archive",
    installDescription: "Browse a repository, search skills.sh, or install from a local folder or ZIP archive. Choose one or more tools to install to.",
    bindDescription: "Choose this skill’s upstream folder. Binding records the source and preserves the existing files.",
    repository: "Repository", branch: "Branch or Tag", directory: "Repository Subfolder", defaultBranch: "Default branch",
    browse: "Browse Repository", query: "Search skills.sh", search: "Search", openRepo: "Browse Repository",
    installed: "Installed", sameName: "Name already used", choose: "Select a skill to install", chooseSource: "Select an upstream folder",
    noResults: "No skills found.", installTo: "Install to", install: "Install Selected Skill", import: "Import Skill", apply: "Bind Selected Source",
    sourceBound: "Source bound.", localDiffers: "Source bound. Local files differ from upstream. Overwriting them still requires confirmation.",
    path: "Skill Folder", pick: "Choose Folder", localDescription: "Choose a folder with SKILL.md. Cleanzo will copy it to the selected tools.",
    zipPath: "ZIP File", pickZIP: "Choose ZIP", zipDescription: "Each ZIP should contain one skill folder with SKILL.md.",
    invalidZIP: "Select a ZIP file.", unsafeZIP: "The archive contains unsafe or duplicate entries.", zipNoSkill: "The archive does not contain SKILL.md.", zipManySkills: "The archive contains several skills. Package them separately.",
    access: "Multiple tools can read the shared folder.", busy: "Working…",
    confirm: "Installation writes the skill’s files into the selected folders. Make sure you trust its source.", copied: "Installation complete",
    invalidRepository: "Enter a GitHub owner/repo or repository URL.", invalidRef: "Enter a valid branch or tag.",
    invalidPath: "Enter a relative path within the repository.", destinationExists: "The destination already contains an item with this name.",
    newSource: "Upstream changed. Browse the repository and choose the skill again.", missing: "Could not find the selected skill directory.",
    tooMany: "This repository contains too many skills. Specify a subfolder.", truncated: "GitHub returned an incomplete repository tree.",
    gitOwned: "A Git checkout uses its own remote as the source.", invalidQuery: "Enter a search query between 2 and 200 bytes.",
    invalidTarget: "Select at least one destination.", installSaved: "Files were installed, but the source was not recorded.",
  },
} as const;

export const skillCatalogApi = {
  targets: (): Promise<SkillRoot[]> => Cleaner.listSkillInstallTargets(),
  browse: (repo: string, ref: string, subdirectory: string): Promise<SkillCatalogEntry[]> => Cleaner.browseSkillRepository(repo, ref, subdirectory),
  discover: (query: string): Promise<SkillDiscoveryEntry[]> => Cleaner.searchSkillCatalog(query),
  install: (origin: SkillOrigin, tools: string[]): Promise<SkillCatalogInstallResult[]> => Cleaner.installCatalogSkill(origin, tools),
  bind: (folder: string, origin: SkillOrigin): Promise<SkillUpdate> => Cleaner.bindCatalogSkillSource(folder, origin),
  pick: (): Promise<string> => Cleaner.pickFolder(),
  pickZIP: (): Promise<string> => Cleaner.pickCatalogSkillZIP(),
  import: (folder: string, tools: string[]): Promise<SkillCatalogInstallResult[]> => Cleaner.importCatalogSkillFolder(folder, tools),
  importZIP: (file: string, tools: string[]): Promise<SkillCatalogInstallResult[]> => Cleaner.importCatalogSkillZIP(file, tools),
};

export type SkillCatalogPanelProps = {
  disabled?: boolean;
  onChanged: (update?: SkillUpdate) => void | Promise<void>;
};

type CatalogMode = "repo" | "discover" | "local" | "zip";
type Words = typeof text[keyof typeof text];
const inputClass = "w-full min-w-0 rounded-md border border-border bg-background px-2.5 py-1.5 text-sm text-foreground";

function catalogError(error: unknown, words: Words): string {
  const reason = errText(error);
  const translated: Record<string, string> = {
    "invalid-repository": words.invalidRepository, "invalid-ref": words.invalidRef, "invalid-skill-path": words.invalidPath,
    "destination-exists": words.destinationExists, mismatch: words.newSource, "not-found": words.missing,
    "too-many-skills": words.tooMany, truncated: words.truncated, "git-owned": words.gitOwned,
    "invalid-query": words.invalidQuery, "invalid-install-target": words.invalidTarget,
    "invalid-zip": words.invalidZIP, "unsafe-zip": words.unsafeZIP, "zip-missing-skill": words.zipNoSkill, "zip-multiple-skills": words.zipManySkills,
  };
  if (reason.startsWith("installed-source-not-saved")) return `${words.installSaved} ${reason}`;
  return translated[reason] ?? reason;
}

export function SkillCatalog({ disabled, onChanged }: SkillCatalogPanelProps) {
  const [open, setOpen] = React.useState(false);
  useT();
  const words = text[currentLang()];
  return <>
    <Button size="sm" variant="ghost" disabled={disabled} onClick={() => setOpen(true)}>
      <PackagePlus className="size-3.5" />{words.add}
    </Button>
    {open && <SkillCatalogDialog onClose={() => setOpen(false)} onChanged={onChanged} />}
  </>;
}

export function SkillSourceBinding({ folder, onChanged, disabled }: SkillCatalogPanelProps & { folder: string }) {
  const [open, setOpen] = React.useState(false);
  useT();
  const words = text[currentLang()];
  return <>
    <Button size="sm" variant="ghost" disabled={disabled} onClick={() => setOpen(true)}>
      <GitFork className="size-3.5" />{words.bind}
    </Button>
    {open && <SkillCatalogDialog folder={folder} onClose={() => setOpen(false)} onChanged={onChanged} />}
  </>;
}

function SkillCatalogDialog({ folder, onClose, onChanged }: SkillCatalogPanelProps & { folder?: string; onClose: () => void }) {
  const t = useT();
  const words = text[currentLang()];
  const [mode, setMode] = React.useState<CatalogMode>("repo");
  const [repo, setRepo] = React.useState("");
  const [ref, setRef] = React.useState("");
  const [subdir, setSubdir] = React.useState("");
  const [query, setQuery] = React.useState("");
  const [local, setLocal] = React.useState("");
  const [zip, setZIP] = React.useState("");
  const [repos, setRepos] = React.useState<SkillCatalogEntry[] | null>(null);
  const [discovered, setDiscovered] = React.useState<SkillDiscoveryEntry[] | null>(null);
  const [selected, setSelected] = React.useState<SkillCatalogEntry | null>(null);
  const [targets, setTargets] = React.useState<SkillRoot[]>([]);
  const [tools, setTools] = React.useState(["agents"]);
  const [results, setResults] = React.useState<SkillCatalogInstallResult[] | null>(null);
  const [notice, setNotice] = React.useState("");
  const [error, setError] = React.useState("");
  const [pending, setPending] = React.useState(false);
  const request = React.useRef(false);
  const mounted = React.useRef(false);

  React.useEffect(() => {
    mounted.current = true;
    if (!folder) {
      void skillCatalogApi.targets().then((roots) => { if (mounted.current) setTargets(roots); }).catch((err) => {
        if (mounted.current) setError(catalogError(err, words));
      });
    }
    return () => { mounted.current = false; };
  }, [folder, words]);

  async function run(action: () => Promise<void>) {
    if (request.current) return;
    request.current = true;
    setPending(true);
    setError("");
    setNotice("");
    try { await action(); }
    catch (err) { if (mounted.current) setError(catalogError(err, words)); }
    finally {
      request.current = false;
      if (mounted.current) setPending(false);
    }
  }

  function browse(repoName: string, branch: string, directory: string, nameHint?: string) {
    setRepos(null);
    setSelected(null);
    setResults(null);
    void run(async () => {
      const found = await skillCatalogApi.browse(repoName, branch, directory);
      if (!mounted.current) return;
      const matching = nameHint ? found.filter((item) => item.name.toLowerCase() === nameHint.toLowerCase()) : [];
      setRepos(matching.length ? [...matching, ...found.filter((item) => !matching.includes(item))] : found);
      setSelected(matching.length === 1 ? matching[0]! : !nameHint && found.length === 1 ? found[0]! : null);
    });
  }

  function search() {
    setDiscovered(null);
    void run(async () => {
      const found = await skillCatalogApi.discover(query);
      if (mounted.current) setDiscovered(found);
    });
  }

  function changeRepo(set: React.Dispatch<React.SetStateAction<string>>, value: string) {
    set(value);
    setRepos(null);
    setSelected(null);
    setResults(null);
    setNotice("");
    setError("");
  }

  function showRepository(item: SkillDiscoveryEntry) {
    setRepo(item.repo);
    setRef("");
    setSubdir("");
    setMode("repo");
    browse(item.repo, "", "", item.slug);
  }

  function apply() {
    if (mode === "repo" && !selected) return;
    setResults(null);
    void run(async () => {
      if (folder && selected) {
        const result = await skillCatalogApi.bind(folder, selected.origin);
        if (mounted.current) setNotice(result.modified ? words.localDiffers : words.sourceBound);
        await onChanged(result);
        return;
      }
      const result = mode === "zip" ? await skillCatalogApi.importZIP(zip, tools)
        : mode === "local" ? await skillCatalogApi.import(local, tools)
          : await skillCatalogApi.install(selected!.origin, tools);
      if (mounted.current) setResults(result);
      if (result.some((entry) => !entry.error || entry.error.startsWith("installed-source-not-saved"))) await onChanged();
    });
  }

  const toolName = (tool: string) => targets.find((root) => root.tool === tool)?.name ?? TOOL_NAMES[tool] ?? tool;
  const localMode = mode === "local" || mode === "zip";
  const isZIP = mode === "zip";
  const showTargets = !folder && (mode === "repo" && selected || localMode);
  return <Dialog open onOpenChange={(open) => { if (!open && !request.current) onClose(); }}>
    <DialogContent className="max-w-[720px]">
      <DialogHeader>
        <DialogTitle>{folder ? words.bind : words.add}</DialogTitle>
        <DialogDescription>{folder ? words.bindDescription : words.installDescription}</DialogDescription>
      </DialogHeader>
      {!folder && <nav className="mb-4 flex flex-wrap gap-2" aria-label={words.add}>
        {(["repo", "discover", "local", "zip"] as const).map((entry) => <Button key={entry} size="sm" variant={mode === entry ? "default" : "ghost"} aria-pressed={mode === entry} disabled={pending} onClick={() => {
          setMode(entry);
          setResults(null);
          setError("");
        }}>{words[entry]}</Button>)}
      </nav>}
      {mode === "repo" && <>
        <form className="grid gap-2 sm:grid-cols-2" onSubmit={(event) => { event.preventDefault(); browse(repo, ref, subdir); }}>
          <label className="text-xs sm:col-span-2">{words.repository}<input className={inputClass} value={repo} onChange={(event) => changeRepo(setRepo, event.target.value)} placeholder="owner/repo" disabled={pending} required /></label>
          <label className="text-xs">{words.branch}<input className={inputClass} value={ref} onChange={(event) => changeRepo(setRef, event.target.value)} placeholder={words.defaultBranch} disabled={pending} /></label>
          <label className="text-xs">{words.directory}<input className={inputClass} value={subdir} onChange={(event) => changeRepo(setSubdir, event.target.value)} placeholder="skills" disabled={pending} /></label>
          <Button type="submit" variant="ghost" size="sm" className="justify-self-start" disabled={pending || !repo.trim()}>{words.browse}</Button>
        </form>
        {repos && (repos.length ? <fieldset className="mt-3 max-h-56 overflow-auto rounded-lg border border-border p-2">
          <legend className="px-1 text-xs font-semibold">{folder ? words.chooseSource : words.choose}</legend>
          {repos.map((item) => <label key={item.origin.path} className="flex items-start gap-2 rounded-md px-2 py-1.5 hover:bg-accent">
            <input type="radio" className="mt-1" name="catalog-entry" checked={item.origin.path === selected?.origin.path} onChange={() => { setSelected(item); setResults(null); setNotice(""); }} disabled={pending} />
            <span className="min-w-0 text-sm"><strong>{item.name}</strong> <span className="break-all text-xs text-muted">{item.origin.path || "/"}</span>
              <CatalogLocalStatus item={item} labels={words} toolName={toolName} />
            </span>
          </label>)}
        </fieldset> : <p className="mt-3 text-sm text-muted">{words.noResults}</p>)}
      </>}
      {mode === "discover" && <>
        <form className="flex flex-wrap items-end gap-2" onSubmit={(event) => { event.preventDefault(); search(); }}>
          <label className="min-w-36 flex-1 text-xs">{words.query}<input type="search" className={inputClass} value={query} onChange={(event) => setQuery(event.target.value)} disabled={pending} /></label>
          <Button type="submit" size="sm" disabled={pending || query.trim().length < 2}><Search className="size-3.5" />{words.search}</Button>
        </form>
        {discovered && (discovered.length ? <ul className="mt-3 max-h-80 divide-y divide-border overflow-auto rounded-lg border border-border">
          {discovered.map((item) => <li key={`${item.repo}/${item.slug}`} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2">
            <div className="min-w-0 text-sm"><strong>{item.name}</strong><span className="block break-all text-xs text-muted">{item.repo}{item.installs > 0 ? ` · ${item.installs.toLocaleString()}` : ""}</span>
              <CatalogLocalStatus item={item} labels={words} toolName={toolName} />
            </div>
            <Button type="button" size="sm" variant="ghost" disabled={pending} onClick={() => showRepository(item)}>{words.openRepo}</Button>
          </li>)}
        </ul> : <p className="mt-3 text-sm text-muted">{words.noResults}</p>)}
      </>}
      {localMode && <div className="space-y-2">
        <p className="text-sm text-muted">{isZIP ? words.zipDescription : words.localDescription}</p>
        <label className="block text-xs">{isZIP ? words.zipPath : words.path}<input className={inputClass} value={isZIP ? zip : local} readOnly /></label>
        <Button variant="ghost" size="sm" disabled={pending} onClick={() => void run(async () => {
          const path = await (isZIP ? skillCatalogApi.pickZIP() : skillCatalogApi.pick());
          if (path && mounted.current) {
            if (isZIP) setZIP(path);
            else setLocal(path);
            setResults(null);
          }
        })}><FolderInput className="size-3.5" />{isZIP ? words.pickZIP : words.pick}</Button>
      </div>}
      {showTargets && <fieldset className="mt-3 rounded-lg border border-border p-2" disabled={pending}>
        <legend className="px-1 text-xs font-semibold">{words.installTo}</legend>
        <div className="flex flex-wrap gap-x-4 gap-y-2">
          {targets.map((root) => <label key={root.tool} className="flex items-center gap-1.5 text-xs" title={root.path}>
            <input type="checkbox" checked={tools.includes(root.tool)} onChange={(event) => setTools((previous) => event.target.checked ? [...previous, root.tool] : previous.filter((tool) => tool !== root.tool))} />{root.name}
          </label>)}
        </div>
        {tools.includes("agents") && <p className="mt-2 text-xs text-muted">{words.access}</p>}
      </fieldset>}
      {!folder && showTargets && <p className="mt-2 text-xs text-muted">{words.confirm}</p>}
      {notice && <p role="status" className="mt-3 text-sm">{notice}</p>}
      {error && <p role="alert" className="mt-3 break-words text-sm text-danger-text">{error}</p>}
      {results && <ul aria-live="polite" className="mt-3 max-h-36 space-y-1 overflow-auto text-xs">
        {results.map((result) => <li key={result.tool} className="break-all"><strong>{toolName(result.tool)}</strong> · {result.error ? catalogError(result.error, words) : words.copied}</li>)}
      </ul>}
      <div className="mt-4 flex flex-wrap justify-end gap-2">
        <Button size="sm" variant="ghost" disabled={pending} onClick={onClose}>{t("btn.close")}</Button>
        {mode !== "discover" && <Button size="sm" disabled={pending || (mode === "repo" ? !selected : isZIP ? !zip : !local) || !folder && (!tools.length || !targets.length)} onClick={apply}>
          {pending ? words.busy : folder ? words.apply : localMode ? words.import : words.install}
        </Button>}
      </div>
    </DialogContent>
  </Dialog>;
}

const TOOL_NAMES: Record<string, string> = { agents: "~/.agents", claude: "Claude Code", codex: "Codex", gemini: "Gemini CLI", opencode: "OpenCode", grok: "Grok", hermes: "Hermes", pi: "Pi" };

function CatalogLocalStatus({ item, labels, toolName }: { item: { installed: string[]; sameName: string[] }; labels: Words; toolName: (tool: string) => string }) {
  return <span className="block text-xs text-muted">
    {item.installed.length > 0 && <span className="mr-2">{labels.installed} · {item.installed.map(toolName).join(", ")}</span>}
    {item.sameName.length > 0 && <span>{labels.sameName} · {item.sameName.map(toolName).join(", ")}</span>}
  </span>;
}
